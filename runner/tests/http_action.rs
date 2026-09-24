use n9n_runner::{http_node, Result};
use serde_json::json;
use std::{
    collections::HashMap,
    sync::{Arc, Mutex},
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
};

async fn endpoint(reply: Vec<u8>) -> (String, Arc<Mutex<Vec<u8>>>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let captured = Arc::new(Mutex::new(Vec::new()));
    let copy = captured.clone();
    tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.unwrap();
        let mut raw = Vec::new();
        let mut buffer = [0u8; 4096];
        loop {
            let n = socket.read(&mut buffer).await.unwrap();
            if n == 0 {
                break;
            };
            raw.extend_from_slice(&buffer[..n]);
            if let Some(end) = raw.windows(4).position(|x| x == b"\r\n\r\n") {
                let head = String::from_utf8_lossy(&raw[..end]);
                let len = head
                    .lines()
                    .find_map(|line| {
                        line.to_ascii_lowercase()
                            .strip_prefix("content-length:")
                            .and_then(|x| x.trim().parse::<usize>().ok())
                    })
                    .unwrap_or(0);
                if raw.len() >= end + 4 + len {
                    break;
                }
            }
        }
        *copy.lock().unwrap() = raw;
        if !reply.is_empty() {
            socket.write_all(&reply).await.unwrap();
        }
    });
    (format!("http://{addr}/action"), captured)
}
fn response(status: &str, body: &str) -> Vec<u8> {
    format!("HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",body.len()).into_bytes()
}
async fn run(url: &str, method: &str, body: serde_json::Value) -> Result<serde_json::Value> {
    http_node(
        &json!({"url":url,"method":method,"body":body}),
        &json!({}),
        &HashMap::new(),
        true,
    )
    .await
}

#[tokio::test]
async fn sends_string_body_without_json_quoting() {
    let (url, raw) = endpoint(response("200 OK", "{}")).await;
    run(&url, "POST", json!("{\"a\":1}")).await.unwrap();
    let bytes = raw.lock().unwrap();
    let body = &bytes[bytes.windows(4).position(|x| x == b"\r\n\r\n").unwrap() + 4..];
    assert_eq!(body, b"{\"a\":1}");
}

#[tokio::test]
async fn rejects_redirect_and_private_target() {
    let (url, _) = endpoint(response("302 Found", "")).await;
    let denied = http_node(
        &json!({"url":url,"method":"GET"}),
        &json!({}),
        &HashMap::new(),
        false,
    )
    .await;
    assert!(denied.unwrap_err().contains("private"));
    let err = run(&url, "GET", json!("")).await.unwrap_err();
    assert!(err.contains("HTTP 302"));
}

#[tokio::test]
async fn interrupted_post_is_uncertain() {
    let (url, _) = endpoint(Vec::new()).await;
    let err = run(&url, "POST", json!({"name":"Ada"})).await.unwrap_err();
    assert!(err.starts_with("uncertain:"), "{err}");
}
