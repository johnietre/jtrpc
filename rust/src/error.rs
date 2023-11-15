use crate::{Headers, Message, Response, WriteUnpin, BV};
use anyhow::Error as AnyError;
use std::borrow::Cow;
use std::error::Error as StdError;
use std::fmt;
use std::io::Error as IOError;

pub type Res<T> = Result<T, JtRPCError>;
pub type ArcRes<T> = Result<T, std::sync::Arc<JtRPCError>>;

pub type DynError = Box<dyn StdError>;

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

impl ErrResp {
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

impl fmt::Display for ErrResp {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        // TODO: Body?
        write!(
            f,
            "Status Code: {}, Path: \"{}\", Body: {}",
            self.status_code,
            self.path,
            self.body_string_lossy()
        )
    }
}

impl StdError for ErrResp {}

#[derive(Debug)]
pub enum JtRPCError {
    // TODO
    Response(ErrResp),
    IO(IOError),
    ClientClosed,
    StreamClosed(Option<Message>),
    TimedOut,
    Other(AnyError),
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

impl From<AnyError> for JtRPCError {
    fn from(err: AnyError) -> Self {
        JtRPCError::Other(err)
    }
}

impl StdError for JtRPCError {}

impl fmt::Display for JtRPCError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        use JtRPCError::*;
        match self {
            Response(r) => write!(f, "Received non-200 response: {r}"),
            IO(e) => write!(f, "{e}"),
            ClientClosed => write!(f, "client is closed"),
            StreamClosed(None) => write!(f, "stream is closed"),
            StreamClosed(Some(msg)) => write!(f, "stream is closed: {msg:?}"),
            TimedOut => write!(f, "operation timed out"),
            Other(e) => write!(f, "{e}"),
        }
    }
}
