use jtrpc::*;
use std::io::{prelude::*, stdin, stdout};

#[tokio::main]
async fn main() {
    let client = Client::connect("127.0.0.1:8080").await.unwrap();

    let mut req = Request::builder().path("/echo").body("no and yes").build();
    req.headers.set("h1".into(), "value1".into());
    req.headers.set("h2".into(), "value2".into());
    req.headers.set("header3579".into(), "value3579".into());
    let mut resp = client.send(req).await.unwrap().await.unwrap();
    resp.headers.parse();
    println!("Response: {resp:?}");
    println!("Body: {}", resp.body_str().unwrap());
    println!();

    let req = Request::builder().path("/yes").body("no and yes").build();
    let mut resp = client.send(req).await.unwrap().await.unwrap();
    resp.headers.parse();
    println!("Response: {resp:?}");
    println!("Body: {}", resp.body_str().unwrap());
    println!();

    let req = Request::builder().path("/yes").build();
    let mut resp = client.send(req).await.unwrap().await.unwrap();
    resp.headers.parse();
    println!("Response: {resp:?}");
    println!("Body: {}", resp.body_str().unwrap());
    println!();

    let req = Request::builder().path("/no").body("no and yes").build();
    let mut resp = client.send(req).await.unwrap().await.unwrap();
    resp.headers.parse();
    println!("Response: {resp:?}");
    println!("Body: {}", resp.body_str().unwrap());
    println!();

    let mut req = Request::builder().path("/stream").stream(true).build();
    req.headers.set("stream_header1".into(), "value1".into());
    req.headers.set("stream_header2".into(), "value2".into());
    req.headers
        .set("stream_header3579".into(), "value3579".into());
    let mut resp = client.send(req).await.unwrap().await.unwrap();
    resp.headers.parse();
    println!("Response: {resp:?}");
    println!("Body: {}", resp.body_str().unwrap());
    let stream = resp.take_stream().unwrap();
    print!("Message: ");
    stdout().flush().unwrap();
    for line in stdin().lines() {
        let Ok(msg_str) = line else {
            break;
        };
        if msg_str == "exit" {
            break;
        }
        stream.send(Message::new(msg_str)).await.unwrap();
        let msg = stream.recv().await.unwrap();
        println!("Received: {}", msg.body_string_lossy());
        print!("Message: ");
        stdout().flush().unwrap();
    }
    let _ = stream.close().await;
}
