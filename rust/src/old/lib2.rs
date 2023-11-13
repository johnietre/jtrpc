use std::collections::HashMap;
use std::io::{self, prelude::*, Error as IOError};
use std::net::TcpStream;
use stc::sync::{Arc, Mutex};
use std::sync::atomic::{AtomicBool, Ordering};
use std::thread;

pub type Bytes = Vec<u8>;
pub type HeaderBytes = Vec<u8>;
pub type HeaderMap = HashMap<String, String>;
pub enum Headers {
    Bytes(HeaderBytes),
    Map(HashMap<String, String>),
}

impl Headers {
    pub fn is_parsed(&self) -> bool {
        matches!(self, Map(_))
    }

    pub fn parse(&mut self) -> &HashMap<Bytes, Bytes> {
    }
}

pub struct Request {
    id: u64,
    flags: u8,
    path: Arc<str>,
    headers: Headers,
    body: Bytes,
}

struct Response {
    req_id: u64,
    flags: u8,
    status_code: u8,
    path: String,
    headers: Headers,
    body: Vec<u8>,
    stream: Option<Arc<Stream>>,
}

struct Message {
    flags: u8,
    body: Bytes,
}

struct Stream {
    client: Arc<Client>,
    req: Request,
    chan: Receiver<Message>,
    is_closed: AtomicBool,
}

type ReqTup = (Request, Sender<Response>);

struct Client {
    addr: SocketAddr,
    conn_read: &'static TcpStream,
    conn_write: Mutex<&'static TcpStream>,
    is_closed: AtomicBool,
    req_counter: AtomicU64,
    reqs: Mutex<HashMap<u64, ReqTup>>,
    streams: Mutex<HashMap<u64, Stream>>,
}

impl Client {
    fn connect<A: ToSocketAddrs>(addr: A) -> Res<Self> {
        let addr = addr.to_socket_addrs()?.next().unwrap();
        let conn = TcpStream::connect(addr)?;
        let client = Self {
            addr,
            conn,
            _: _,
            is_closed: AtomicBool::new(false),
            req_counter: AtomicU64::new(0),
            reqs: Mutex::new(HashMap::new()),
            streams: Mutex::new(HashMap::new()),
        });
        client.run()?;
        Ok(client)
    }

    fn run(self: Arc<Self>) -> Res<()> {
        thread::spawn(move || {
            let mut buf = vec![0u8; 9];
            loop {
                // TODO: Read
                let id = from_le_bytes8(&buf);
                let flags = buf[8];
                if has_stream_msg_flag(flags) {
                    continue;
                }
                if let Some(req_tup) = self.reqs.lock().remove(&id) {
                    self.handle_resp(req_tup, &mut buf);
                }
            }
        });
    }

    fn handle_resp(self: &Arc<Self>, (req, tx): ReqTup, buf: &mut Vec<u8>) -> Res<()> {
        //
    }

    fn handle_stream_msg(self: &Arc<Self>) -> Res<()> {
    }

    fn close(self: &Arc<Self>) -> Res<()> {
    }
}

impl Drop for Client {
    fn drop(&mut self) {
        let cw = *self.conn_write.lock().unwrap();
        unsafe { Box::from_raw(&()) };
    }
}

const FLAG_STREAM_MSG: u8 = 0b1000_0000;

fn has_stream_msg_flag(flags: u8) -> bool {
    flags & FLAG_STREAM_MSG != 0
}

fn from_le_bytes8(buf: &[u8]) -> u64 {
    u64::from_le_bytes(buf.try_into().unwrap())
}

type Res<T> = Result<T, Error>;

enum Error {
    IO(io::Error),
}

impl From<IOError> for Error {
    fn from(err: IOError) -> Self {
        Error::IO(err)
    }
}
