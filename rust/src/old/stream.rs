use crate::{BV, JtRPCError, Client, Request, ArcRes, ReadUnpin, WriteUnpin, utils::append8};
use std::borrow::Cow;
use std::sync::{Arc, atomic::AtomicPtr};
use std::time::Duration;
use tokio::io::AsyncWriteExt;
use tokio::sync::{mpsc::{error::TryRecvError, UnboundedReceiver}, Mutex, RwLock};
use tokio::time as ttime;
use std::sync::atomic::{AtomicBool, Ordering};

pub struct Stream<R: ReadUnpin + 'static, W: WriteUnpin + 'static>(Arc<InnerStream<R, W>>);

impl<R: ReadUnpin + 'static, W: WriteUnpin + 'static> Stream<R, W> {
    pub(crate) fn new(
        client: Client<R, W>, req: Request, rx: UnboundedReceiver<Message>,
    ) -> Self {
        Self(Arc::new(InnerStream {
            client,
            req,
            rx: Mutex::new(rx),
            deadline: RwLock::new(None),
            is_closed: AtomicBool::new(false),
            close_err: AtomicPtr::new(0 as _),
        }))
    }

    pub fn request(&self) -> &Request {
        &self.0.req
    }

    pub async fn send(&self, msg: Message) -> ArcRes<()> {
        self.0.send(msg).await
    }

    pub async fn recv(&self) -> ArcRes<Message> {
        self.0.recv().await
    }

    pub async fn recv_timeout(&self, dur: Duration) -> ArcRes<Message> {
        self.0.recv_timeout(dur).await
    }

    pub async fn set_recv_deadline(&self, deadline: Option<impl Into<ttime::Instant>>) {
        self.0.set_recv_deadline(deadline).await
    }

    pub async fn close(&self) -> ArcRes<()> {
        self.0.close().await
    }

    pub async fn close_with_msg(&self, msg: Message) -> ArcRes<()> {
        self.0.close_with_msg(msg).await
    }

    pub async fn is_closed(&self) -> bool {
        self.0.is_closed()
    }

    pub async fn get_closed_err(&self) -> Option<Arc<JtRPCError>> {
        self.0.get_closed_err()
    }

    pub(crate) async fn close_with_send(&self, send: bool) -> ArcRes<()> {
        self.0.close_with_send(send).await
    }

    pub(crate) async fn close_with_msg_send(&self, msg: Option<Message>, send: bool) -> ArcRes<()> {
        self.0.close_with_msg_send(msg, send).await
    }
}

impl<R: ReadUnpin + 'static, W: WriteUnpin + 'static> Clone for Stream<R, W> {
    fn clone(&self) -> Self {
        Self(Arc::clone(&self.0))
    }
}

pub(crate) struct InnerStream<R: ReadUnpin + 'static, W: WriteUnpin + 'static> {
    client: Client<R, W>,
    req: Request,
    rx: Mutex<UnboundedReceiver<Message>>,
    deadline: RwLock<Option<ttime::Instant>>,
    is_closed: AtomicBool,
    close_err: AtomicPtr<Arc<JtRPCError>>,
}

impl<R: ReadUnpin + 'static, W: WriteUnpin + 'static> InnerStream<R, W> {
    async fn send(&self, msg: Message) -> ArcRes<()> {
        if self.is_closed() {
            return Err(self.get_closed_err_or(JtRPCError::TimedOut));
        }
        self.send_msg(msg).await
    }

    async fn recv(&self) -> ArcRes<Message> {
        let mut rx = self.rx.lock().await;
        let deadline = *self.deadline.read().await;
        let msg = if let Some(deadline) = deadline {
            if deadline <= ttime::Instant::now() {
                return Err(Arc::new(JtRPCError::TimedOut));
            }
            let mut rx = self.rx.lock().await;
            // TODO: Check if this has the desired behavior when the deadline has already been
            // reached. If so, the lines above aren't needed.
            match ttime::timeout_at(deadline, rx.recv()).await {
                Ok(msg) => msg,
                Err(_) => return Err(self.get_closed_err_or(JtRPCError::TimedOut)),
            }
        } else {
            rx.recv().await
        };
        msg.ok_or(self.get_closed_err_or(JtRPCError::StreamClosed(None)))
    }

    async fn recv_timeout(&self, dur: Duration) -> ArcRes<Message> {
        let mut rx = self.rx.lock().await;
        match rx.try_recv() {
            Ok(msg) => Ok(msg),
            Err(TryRecvError::Disconnected) => Err(self.get_closed_err_or(JtRPCError::StreamClosed(None))),
            Err(TryRecvError::Empty) => match ttime::timeout(dur, rx.recv()).await {
                Ok(Some(msg)) => Ok(msg),
                Ok(None) => Err(self.get_closed_err_or(JtRPCError::StreamClosed(None))),
                Err(_) => Err(Arc::new(JtRPCError::TimedOut)),
            }
        }
    }

    // TODO: does it REALLY need to be async? Can/should something else be used?
    async fn set_recv_deadline(&self, t: Option<impl Into<ttime::Instant>>) {
        *self.deadline.write().await = t.map(|t| t.into());
    }

    async fn send_msg(&self, mut msg: Message) -> ArcRes<()> {
        msg.flags |= FLAG_STREAM_MSG;
        let mut writer = self.client.0.writer.lock().await;
        // TODO: double write?
        let mut buf = vec![0u8; 9];
        buf[0] = msg.flags;
        append8(&mut buf, msg.body.len() as u64);
        writer.write_all(&buf).await.map_err(|e| Arc::new(JtRPCError::from(e)))?;
        writer.write_all(&msg.body).await.map_err(|e| Arc::new(JtRPCError::from(e)))?;
        Ok(())
    }

    async fn close(&self) -> ArcRes<()> {
        self.close_with_send(true).await
    }

    async fn close_with_msg(&self, msg: Message) -> ArcRes<()> {
        self.close_with_msg_send(Some(msg), true).await
    }

    async fn close_with_send(&self, send: bool) -> ArcRes<()> {
        self.close_with_msg_send(None, send).await
    }

    // If `msg` is None and `send` is true, the default close message is sent.
    // If `msg` is None and `send` is false, the server closed the stream without a message.
    // If `msg` is Some and `send` is true, the custom close message is sent.
    // If `msg` is Some and `send` is false, the server closed the stream with the passed message.
    async fn close_with_msg_send(&self, msg: Option<Message>, send: bool) -> ArcRes<()> {
        if self.is_closed.swap(true, Ordering::SeqCst) {
            // TODO
            return Err(self.get_closed_err_or(JtRPCError::StreamClosed(None)));
        }
        // TODO
        self.client.0.remove_stream(self.req.id).await;
        if !send {
            self.set_close_err(JtRPCError::StreamClosed(msg));
            return Ok(());
        }
        let mut msg = msg.unwrap_or_default();
        msg.flags |= MSG_FLAG_CLOSE;
        // TODO: Do something with errors?
        let _ = self.send_msg(msg).await;
        Ok(())
    }

    fn is_closed(&self) -> bool {
        self.is_closed.load(Ordering::SeqCst)
    }

    fn set_close_err(&self, err: JtRPCError) -> Arc<JtRPCError> {
        let err = Arc::new(err);
        let new_ptr = Box::into_raw(Box::new(Arc::clone(&err)));
        match self.close_err.compare_exchange(
            0 as _, new_ptr,
            Ordering::Relaxed, Ordering::Relaxed,
        ) {
            Ok(_) => err,
            Err(_) => self.get_closed_err().expect("value present in aptr, should be Some"),
        }
    }

    fn get_closed_err(&self) -> Option<Arc<JtRPCError>> {
        let ptr = self.close_err.load(Ordering::Relaxed);
        if ptr.is_null() {
            None
        } else {
            // Safety
            unsafe { Some(Arc::clone(&*ptr)) }
        }
    }

    fn get_closed_err_or(&self, err: JtRPCError) -> Arc<JtRPCError> {
        self.get_closed_err().unwrap_or(Arc::new(err))
    }
}

impl<R: ReadUnpin + 'static, W: WriteUnpin + 'static> Drop for InnerStream<R, W> {
    fn drop(&mut self) {
        let ptr = self.close_err.load(Ordering::Relaxed);
        if !ptr.is_null() {
            // Safety
            let _ = unsafe { Box::from_raw(ptr) };
        }
    }
}

#[derive(PartialEq, Eq, Clone, Debug)]
pub struct Message {
    pub(crate) flags: u8,
    //body: Bytes,
    pub(crate) body: BV,
}

impl Message {
    pub fn new(body: impl Into<BV>) -> Self {
        Self { body: body.into(), ..Self::default() }
    }
    pub fn take_body(&mut self) -> BV {
        std::mem::replace(&mut self.body, Vec::new())
    }

    pub fn body_str(&self) -> Result<&str, std::str::Utf8Error> {
        std::str::from_utf8(&self.body)
    }

    pub fn take_body_string(&mut self) -> Result<String, std::string::FromUtf8Error> {
        let body = self.take_body();
        String::from_utf8(body)
    }

    pub fn body_string_lossy<'a>(&'a self) -> Cow<'a, str> {
        String::from_utf8_lossy(&self.body)
    }

    pub fn take_body_string_lossy(&mut self) -> String {
        let body = self.take_body();
        String::from_utf8_lossy(&body).into_owned()
    }
}

impl Default for Message {
    fn default() -> Self {
        Message { flags: FLAG_STREAM_MSG, body: Vec::new() }
    }
}

pub(crate) const FLAG_STREAM_MSG: u8 = 0b1000_0000;

pub(crate) fn has_stream_msg_flag(flags: u8) -> bool {
    flags & FLAG_STREAM_MSG != 0
}

pub(crate) const MSG_FLAG_CLOSE: u8 = 0b0000_0001;

pub(crate) fn has_msg_close_flag(flags: u8) -> bool {
    flags & MSG_FLAG_CLOSE != 0
}

/*
impl<R, W> FStream for Stream<R, W> {
    type Item = Res<Message>;

    fn poll_next(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        // TODO
    }
}

impl<R, W> Sink for Stream<R, W> {
    type Error = JtRPCError;

    fn poll_ready(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
    }

    fn start_send(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
    }

    fn poll_flush(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
    }

    fn poll_close(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
    }
}
*/
