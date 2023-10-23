use std::io::prelude::*;
use std::net::TcpStream;
use std::sync::Arc;

fn main() {
    let mut buf = vec![0u8];
    let stream = TcpStream::connect("yes").unwrap();
    let s1 = Arc::new(stream);
    s1.as_ref().read(&mut buf).unwrap();
    /*
    let s2 = Arc::clone(&s1);
    let mut buf = vec![0u8];
    /*
    s1.read(&mut buf);
    s1.write(&buf);
    */
    s1.set_nonblocking(false).unwrap();
    */
}
