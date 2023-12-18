use crate::{utils::*, Headers, ReadUnpin, Stream, WriteUnpin, BV};
use std::{borrow::Cow, fmt, io};
use tokio::io::AsyncReadExt;

#[derive(Default, Debug, Clone)]
pub struct Request {
    pub(crate) id: u64,
    pub(crate) flags: u8,
    pub path: String,
    pub headers: Headers,
    pub body: BV,
}

impl Request {
    pub fn builder() -> RequestBuilder {
        RequestBuilder::default()
    }
}

#[derive(Clone, Debug, Default)]
pub struct RequestBuilder(Request);

impl RequestBuilder {
    pub fn build(self) -> Request {
        self.0
    }

    pub fn stream(mut self, stream: bool) -> Self {
        if stream {
            self.0.flags |= REQ_FLAG_STREAM;
        } else {
            self.0.flags &= !REQ_FLAG_STREAM;
        }
        self
    }

    pub fn path(self, path: impl ToString) -> Self {
        Self(Request {
            path: path.to_string(),
            ..self.0
        })
    }

    pub fn headers(self, headers: Headers) -> Self {
        Self(Request { headers, ..self.0 })
    }

    // TODO: Add individual headers?

    pub fn body(self, body: impl Into<BV>) -> Self {
        Self(Request {
            body: body.into(),
            ..self.0
        })
    }
}

// Used for req_id
#[allow(dead_code)]
#[derive(Debug)]
pub struct Response<W: WriteUnpin + 'static> {
    pub(crate) req_id: u64,
    pub(crate) flags: u8,
    pub status_code: u8,
    pub path: String,
    pub headers: Headers,
    pub body: Vec<u8>,
    pub(crate) stream: Option<Stream<W>>,
}

pub type OWHResponse = Response<tokio::net::tcp::OwnedWriteHalf>;

impl<W: WriteUnpin + 'static> Response<W> {
    pub(crate) async fn read_from(r: &mut impl ReadUnpin) -> io::Result<Self> {
        let mut buf = vec![0u8; 20];
        let _ = r.read_exact(&mut buf).await;
        let req_id = get8(&buf);
        let flags = buf[8];
        let status_code = buf[9];
        let hl = get2(&buf[10..]) as usize;
        let bl = get8(&buf[12..]) as usize;
        let headers_bytes = if hl == 0 {
            Vec::new()
        } else {
            let mut b = vec![0u8; hl];
            r.read_exact(&mut b).await?;
            b
        };
        let body = if bl == 0 {
            Vec::new()
        } else {
            let mut b = vec![0u8; bl];
            r.read_exact(&mut b).await?;
            b
        };
        Ok(Self {
            req_id,
            flags,
            status_code,
            path: String::new(),
            headers: Headers::from_bytes(headers_bytes),
            body,
            stream: None,
        })
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

    pub fn stream(&self) -> Option<&Stream<W>> {
        self.stream.as_ref()
    }

    pub fn take_stream(&mut self) -> Option<Stream<W>> {
        self.stream.take()
    }
}

#[deprecated(note = "Status enum is cleaner and more robust")]
pub mod status {
    #[deprecated(note = "Status enum is cleaner and more robust")]
    pub const STATUS_OK: u8 = 0;
}

#[repr(u8)]
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Status {
    OK = 0,
    PartialError = 1,

    NotFound = 128,
    IsStream = 129,
    NotStream = 130,
    BadRequest = 131,
    Unauthorized = 132,
    BodyTooLarge = 133,

    InvalidInitialBytes = 160,
    BadVersion = 161,

    InternalServerError = 192,

    Unknown(u8),
}

impl From<u8> for Status {
    fn from(status: u8) -> Self {
        use Status::*;
        match status {
            0 => OK,
            1 => PartialError,
            128 => NotFound,
            129 => IsStream,
            130 => NotStream,
            131 => BadRequest,
            132 => Unauthorized,
            133 => BodyTooLarge,
            160 => InvalidInitialBytes,
            161 => BadVersion,
            192 => InternalServerError,
            u => Unknown(u),
        }
    }
}

impl From<Status> for u8 {
    fn from(status: Status) -> Self {
        use Status::*;
        match status {
            OK => 0,
            PartialError => 1,
            NotFound => 128,
            IsStream => 129,
            NotStream => 130,
            BadRequest => 131,
            Unauthorized => 132,
            BodyTooLarge => 133,
            InvalidInitialBytes => 160,
            BadVersion => 161,
            InternalServerError => 192,
            Unknown(u) => u,
        }
    }
}

impl PartialEq<u8> for Status {
    fn eq(&self, other: &u8) -> bool {
        *self == Self::from(*other)
    }
}

impl PartialEq<Status> for u8 {
    fn eq(&self, other: &Status) -> bool {
        *other == *self
    }
}

impl fmt::Debug for Status {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        use Status::*;
        match *self {
            OK => write!(f, "OK (code: {})", u8::from(*self)),
            PartialError => write!(f, "PartialError (code: {})", u8::from(*self)),
            NotFound => write!(f, "NotFound (code: {})", u8::from(*self)),
            IsStream => write!(f, "IsStream (code: {})", u8::from(*self)),
            NotStream => write!(f, "NotStream (code: {})", u8::from(*self)),
            BadRequest => write!(f, "BadRequest (code: {})", u8::from(*self)),
            Unauthorized => write!(f, "Unauthorized (code: {})", u8::from(*self)),
            BodyTooLarge => write!(f, "BodyTooLarge (code: {})", u8::from(*self)),
            InvalidInitialBytes => write!(f, "InvalidInitialBytes (code: {})", u8::from(*self)),
            BadVersion => write!(f, "BadVersion (code: {})", u8::from(*self)),
            InternalServerError => write!(f, "InternalServerError (code: {})", u8::from(*self)),
            Unknown(u) => write!(f, "Unknown (code: {u})"),
        }
    }
}

impl fmt::Display for Status {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        use Status::*;
        match *self {
            OK => write!(f, "OK"),
            PartialError => write!(f, "PartialError"),
            NotFound => write!(f, "NotFound"),
            IsStream => write!(f, "IsStream"),
            NotStream => write!(f, "NotStream"),
            BadRequest => write!(f, "BadRequest"),
            Unauthorized => write!(f, "Unauthorized"),
            BodyTooLarge => write!(f, "BodyTooLarge"),
            InvalidInitialBytes => write!(f, "InvalidInitialBytes"),
            BadVersion => write!(f, "BadVersion"),
            InternalServerError => write!(f, "InternalServerError"),
            Unknown(u) => write!(f, "Unknown (code: {u})"),
        }
    }
}

pub(crate) const REQ_FLAG_STREAM: u8 = 0b0100_0000;

pub(crate) fn has_stream_flag(flags: u8) -> bool {
    flags & REQ_FLAG_STREAM != 0
}
