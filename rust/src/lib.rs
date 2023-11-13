use tokio::io::{AsyncRead, AsyncWrite};

pub trait ReadUnpin: AsyncRead + Send + Unpin {}
impl<T> ReadUnpin for T where T: AsyncRead + Send + Unpin {}
pub trait WriteUnpin: AsyncWrite + Send + Unpin {}
impl<T> WriteUnpin for T where T: AsyncWrite + Send + Unpin {}

mod client;
pub use client::*;

mod error;
pub use error::*;

mod headers;
pub use headers::Headers;

mod req_resp;
pub use req_resp::*;

mod stream;
pub use stream::*;

pub mod utils;

pub(crate) type BV = Vec<u8>;
