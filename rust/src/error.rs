use crate::{Headers, Message, Response, WriteUnpin, BV};
use std::error::Error as StdError;
use std::fmt;
use std::io::Error as IOError;
use tokio::io::AsyncWrite;

pub type Res<T> = Result<T, JtRPCError>;
pub type ArcRes<T> = Result<T, std::sync::Arc<JtRPCError>>;

pub type DynError = Box<dyn StdError>;

pub type DynResponse = Response<Box<dyn AsyncWrite>>;

// Used for flags
#[allow(dead_code)]
#[derive(Debug)]
pub struct ErrResp {
    flags: u8,
    pub status_code: u8,
    pub path: String,
    pub headers: Headers,
    pub body: BV,
}

impl<W: WriteUnpin> From<Response<W>> for ErrResp {
    fn from(resp: Response<W>) -> ErrResp {
        Self {
            flags: resp.flags,
            status_code: resp.status_code,
            path: resp.path,
            headers: resp.headers,
            body: resp.body,
        }
    }
}

#[derive(Debug)]
pub enum JtRPCError {
    // TODO
    Response(ErrResp),
    IO(IOError),
    ClientClosed,
    StreamClosed(Option<Message>),
    TimedOut,
    Other(DynError),
}

impl From<ErrResp> for JtRPCError {
    fn from(resp: ErrResp) -> Self {
        JtRPCError::Response(resp)
    }
}

impl<W: WriteUnpin> From<Response<W>> for JtRPCError {
    fn from(resp: Response<W>) -> Self {
        JtRPCError::Response(resp.into())
    }
}

impl From<IOError> for JtRPCError {
    fn from(err: IOError) -> Self {
        JtRPCError::IO(err)
    }
}

impl From<DynError> for JtRPCError {
    fn from(err: DynError) -> Self {
        JtRPCError::Other(err)
    }
}

impl StdError for JtRPCError {}

impl fmt::Display for JtRPCError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        use JtRPCError::*;
        match self {
            Response(r) => write!(f, "Received non-200 response: {r:?}"),
            IO(e) => write!(f, "{e}"),
            ClientClosed => write!(f, "client is closed"),
            StreamClosed(None) => write!(f, "stream is closed"),
            StreamClosed(Some(msg)) => write!(f, "stream is closed: {msg:?}"),
            TimedOut => write!(f, "operation timed out"),
            Other(e) => write!(f, "{e}"),
        }
    }
}
