use crate::{utils::*, *};
use std::collections::HashMap;
use std::net::ToSocketAddrs;
use std::sync::{
    atomic::{AtomicBool, AtomicPtr, AtomicU64, Ordering},
    Arc,
};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{tcp::OwnedWriteHalf, TcpStream};
use tokio::sync::{
    mpsc::{self, UnboundedSender},
    oneshot::{self, Receiver, Sender},
    Mutex,
};

type ReqTup<W> = (Request, Sender<Response<W>>);

#[allow(dead_code)]
const INITIAL_BYTES: &[u8] = &[
    0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x05, b'j', b't', b'r', b'p', b'c',
];
const INITIAL_BYTES_VER: &[u8] = &[
    0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x05, b'j', b't', b'r', b'p', b'c', 0x00, 0x01,
];

#[derive(Debug)]
pub struct Client<W: WriteUnpin + 'static>(pub(crate) Arc<InnerClient<W>>);

impl Client<OwnedWriteHalf> {
    pub async fn connect<A: ToSocketAddrs>(addr: A) -> Result<Self, JtRPCError> {
        let addr = addr.to_socket_addrs()?.next().unwrap();
        let conn = TcpStream::connect(addr).await?;
        let (r, w) = conn.into_split();
        Self::with_rw(r, w).await
    }
}

impl<W: WriteUnpin + 'static> Client<W> {
    pub async fn with_rw(
        reader: (impl ReadUnpin + 'static),
        writer: W,
    ) -> Result<Self, JtRPCError> {
        Ok(Self(InnerClient::with_rw(reader, writer).await?))
    }

    pub async fn send(&self, req: Request) -> Res<RespChan<W>> {
        self.0.send(req).await
    }

    pub async fn close(&self) -> Res<()> {
        self.0.close().await
    }

    pub fn is_closed(&self) -> bool {
        self.0.is_closed()
    }

    pub async fn get_closed_err(&self) -> Option<Arc<JtRPCError>> {
        self.0.get_closed_err()
    }
}

impl<W: WriteUnpin + 'static> Clone for Client<W> {
    fn clone(&self) -> Self {
        Self(Arc::clone(&self.0))
    }
}

#[derive(Debug)]
pub(crate) struct InnerClient<W: WriteUnpin + 'static> {
    pub(crate) writer: Mutex<W>,
    is_closed: AtomicBool,
    req_counter: AtomicU64,
    reqs: Mutex<HashMap<u64, ReqTup<W>>>,
    streams: Mutex<HashMap<u64, (UnboundedSender<Message>, Stream<W>)>>,
    close_err: AtomicPtr<Arc<JtRPCError>>,
}

impl<W: WriteUnpin + 'static> InnerClient<W> {
    pub async fn with_rw(
        mut reader: (impl ReadUnpin + 'static),
        mut writer: W,
    ) -> Result<Arc<Self>, JtRPCError> {
        writer.write_all(INITIAL_BYTES_VER).await?;
        let resp = Response::<W>::read_from(&mut reader).await?;
        if resp.status_code != crate::status::STATUS_OK {
            return Err(resp.into());
        }
        let client = Arc::new(Self {
            writer: Mutex::new(writer),
            is_closed: Default::default(),
            req_counter: AtomicU64::new(1),
            reqs: Default::default(),
            streams: Default::default(),
            close_err: AtomicPtr::new(0 as _),
        });
        let c = Arc::clone(&client);
        tokio::spawn(async move {
            let _ = c.run(reader).await;
        });
        Ok(client)
    }

    async fn run(self: Arc<Self>, mut reader: (impl ReadUnpin + 'static)) -> Res<()> {
        loop {
            let mut buf = vec![0u8; 20];
            if let Err(e) = reader.read_exact(&mut buf[..9]).await {
                let _ = self.close_with_err(Some(e.into()));
                break;
            }
            let id = get8(&buf);
            let flags = buf[8];
            if has_stream_msg_flag(flags) {
                if let Some(tup) = self.streams.lock().await.get(&id) {
                    if let Err(e) = self.handle_stream_msg(&mut reader, tup, buf).await {
                        let _ = self.close_with_err(Some(e));
                        break;
                    }
                }
                continue;
            }
            if let Some(req_tup) = self.reqs.lock().await.remove(&id) {
                if let Err(e) = self.handle_resp(&mut reader, req_tup, buf).await {
                    let _ = self.close_with_err(Some(e));
                    break;
                }
            }
        }
        Ok(())
    }

    async fn handle_resp(
        self: &Arc<Self>,
        reader: &mut (impl ReadUnpin + 'static),
        (req, tx): ReqTup<W>,
        mut buf: Vec<u8>,
    ) -> Res<()> {
        let flags = buf[8];

        reader.read_exact(&mut buf[..11]).await?;
        let status_code = buf[0];
        let has_stream = status_code == crate::status::STATUS_OK && has_stream_flag(flags);

        // Get the headers and body length
        let hl = get2(&buf[1..]) as usize;
        let bl = get8(&buf[3..]) as usize;

        let mut headers_bytes = vec![0u8; hl];
        reader.read_exact(&mut headers_bytes).await?;
        let mut body = vec![0u8; bl];
        reader.read_exact(&mut body).await?;
        let (req_id, path) = (req.id, req.path.clone());
        let stream = if has_stream {
            let (tx, rx) = mpsc::unbounded_channel();
            let stream = Stream::new(Client(Arc::clone(self)), req, rx);
            self.streams
                .lock()
                .await
                .insert(req_id, (tx, stream.clone()));
            Some(stream)
        } else {
            None
        };
        let resp = Response {
            req_id,
            flags,
            status_code,
            path,
            headers: Headers::from_bytes(headers_bytes),
            body,
            stream,
        };
        let _ = tx.send(resp);
        Ok(())
    }

    async fn handle_stream_msg(
        self: &Arc<Self>,
        reader: &mut (impl ReadUnpin + 'static),
        (tx, stream): &(UnboundedSender<Message>, Stream<W>),
        mut buf: BV,
    ) -> Res<()> {
        let flags = buf[8];
        reader.read_exact(&mut buf[..8]).await?;
        let ml = get8(&buf) as usize;
        buf.resize(ml, 0);
        reader.read_exact(&mut buf[..ml]).await?;
        let msg = Message { flags, body: buf };
        if has_msg_close_flag(flags) {
            let _ = stream.close_with_msg_send(Some(msg), false);
        } else if tx.send(msg).is_err() {
            let _ = stream.close_with_send(false);
        }
        Ok(())
    }

    async fn send(&self, mut req: Request) -> Result<RespChan<W>, JtRPCError> {
        if self.is_closed() {
            return Err(JtRPCError::ClientClosed);
        }
        loop {
            // TODO: Check duplicate ID
            req.id = self.req_counter.fetch_add(1, Ordering::Relaxed);
            break;
        }
        let mut buf = vec![0u8; 21];
        // Add the ID, flags, and path length
        place8(&mut buf, req.id);
        buf[8] = req.flags;
        place2(&mut buf[9..], req.path.len() as _);
        // Encode headers and get add headers length
        let hb = if req.headers.encode() {
            // TODO: Return error?
            let hb = req.headers.bytes().unwrap();
            place2(&mut buf[11..], hb.len() as _);
            hb
        } else {
            &[]
        };
        // Add body length
        if req.body.len() != 0 {
            place8(&mut buf[13..], req.body.len() as _);
        }
        // TODO: Multiple writes?
        buf.extend_from_slice(req.path.as_bytes());
        buf.extend_from_slice(hb);
        buf.append(&mut req.body);
        // Write to server
        {
            let mut writer = self.writer.lock().await;
            writer.write_all(&buf).await?;
        }
        // Add request and return response channel
        let (tx, rx) = oneshot::channel();
        let mut reqs = self.reqs.lock().await;
        reqs.insert(req.id, (req, tx));
        Ok(rx)
    }

    async fn close(&self) -> Res<()> {
        self.close_with_err(None).await
    }

    async fn close_with_err(&self, err: Option<JtRPCError>) -> Res<()> {
        if self.is_closed.swap(true, Ordering::SeqCst) {
            return Err(JtRPCError::ClientClosed);
        }
        if let Some(err) = err {
            self.set_close_err(err);
        }
        let _writer = self.writer.lock().await;
        let mut streams = self.streams.lock().await;
        for (_, (_, stream)) in streams.drain() {
            let _ = stream.close().await;
        }
        self.reqs.lock().await.clear();
        Ok(())
    }

    fn is_closed(&self) -> bool {
        self.is_closed.load(Ordering::SeqCst)
    }

    pub(crate) async fn remove_stream(&self, req_id: u64) {
        self.streams.lock().await.remove(&req_id);
    }

    fn set_close_err(&self, err: JtRPCError) -> Arc<JtRPCError> {
        let err = Arc::new(err);
        let new_ptr = Box::into_raw(Box::new(Arc::clone(&err)));
        match self
            .close_err
            .compare_exchange(0 as _, new_ptr, Ordering::Relaxed, Ordering::Relaxed)
        {
            Ok(_) => err,
            Err(_) => self
                .get_closed_err()
                .expect("value present in aptr, should be Some"),
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
}

// TODO: is there a way to send close messages on drop

pub type RespChan<W> = Receiver<Response<W>>;
