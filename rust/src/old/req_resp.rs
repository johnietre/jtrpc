use crate::{utils::*, Headers, Stream, BV, ReadUnpin, WriteUnpin};
use std::{io, borrow::Cow};
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

    pub fn path(self, path: String)-> Self {
        Self(Request { path, ..self.0 })
    }

    pub fn headers(self, headers: Headers) -> Self {
        Self(Request { headers, ..self.0 })
    }

    // TODO: Add individual headers?

    pub fn body(self, body: BV) -> Self {
        Self(Request { body, ..self.0 })
    }
}

// Used for req_id
#[allow(dead_code)]
pub struct Response<R: ReadUnpin + 'static, W: WriteUnpin + 'static> {
    pub(crate) req_id: u64,
    pub(crate) flags: u8,
    pub status_code: u8,
    pub path: String,
    pub headers: Headers,
    pub body: Vec<u8>,
    pub(crate) stream: Option<Stream<R, W>>,
}

impl<R: ReadUnpin + 'static, W: WriteUnpin + 'static> Response<R, W> {
    //pub(crate) async fn read_from(r: &mut impl AsyncRead) -> io::Result<Self> {
    pub(crate) async fn read_from(r: &mut R) -> io::Result<Self> {
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

    pub fn stream(&self) -> Option<&Stream<R, W>> {
        self.stream.as_ref()
    }

    pub fn take_stream(&mut self) -> Option<Stream<R, W>> {
        self.stream.take()
    }
}

pub mod status {
    pub const STATUS_OK: u8 = 0;
}

pub(crate) const REQ_FLAG_STREAM: u8 = 0b0100_0000;

pub(crate) fn has_stream_flag(flags: u8) -> bool {
    flags & REQ_FLAG_STREAM != 0
}
