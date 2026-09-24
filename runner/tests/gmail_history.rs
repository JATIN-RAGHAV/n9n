use n9n_runner::{poll_triggers, Api};
use serde_json::{json, Value};
use std::sync::{Arc, Mutex};
use tokio::sync::Mutex as AsyncMutex;
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
};

static TEST_ENV: AsyncMutex<()> = AsyncMutex::const_new(());
type Records = Arc<Mutex<Vec<(String, Value)>>>;

async fn server(reject_events: bool) -> (Api, Records) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    std::env::set_var("GMAIL_TOKEN_URL", format!("http://{addr}/token"));
    std::env::set_var("GMAIL_API_BASE", format!("http://{addr}"));
    let seen: Records = Arc::new(Mutex::new(Vec::new()));
    let recorded = seen.clone();
    tokio::spawn(async move {
        loop {
            let Ok((mut socket, _)) = listener.accept().await else {
                break;
            };
            let recorded = recorded.clone();
            tokio::spawn(async move {
                let mut raw = Vec::new();
                let mut buffer = [0u8; 4096];
                loop {
                    let n = socket.read(&mut buffer).await.unwrap();
                    if n == 0 {
                        return;
                    }
                    raw.extend_from_slice(&buffer[..n]);
                    let Some(end) = raw.windows(4).position(|part| part == b"\r\n\r\n") else {
                        continue;
                    };
                    let head = String::from_utf8_lossy(&raw[..end]);
                    let length = head
                        .lines()
                        .find_map(|line| {
                            line.to_ascii_lowercase()
                                .strip_prefix("content-length:")
                                .and_then(|value| value.trim().parse::<usize>().ok())
                        })
                        .unwrap_or(0);
                    if raw.len() < end + 4 + length {
                        continue;
                    }
                    let path = head
                        .lines()
                        .next()
                        .unwrap()
                        .split_whitespace()
                        .nth(1)
                        .unwrap()
                        .to_owned();
                    let body = serde_json::from_slice(&raw[end + 4..end + 4 + length])
                        .unwrap_or(Value::Null);
                    recorded.lock().unwrap().push((path.clone(), body));
                    let (status, response) = response_for(&path, reject_events);
                    let text = response.to_string();
                    let wire = format!("HTTP/1.1 {status} {}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{text}",if status==200{"OK"}else{"Error"},text.len());
                    socket.write_all(wire.as_bytes()).await.unwrap();
                    return;
                }
            });
        }
    });
    (
        Api::new(
            format!("http://{addr}"),
            "0123456789abcdef0123456789abcdef".into(),
        )
        .unwrap(),
        seen,
    )
}

fn response_for(path: &str, reject_events: bool) -> (u16, Value) {
    if path == "/internal/triggers" {
        return (
            200,
            json!({"triggers":[{"workflow_id":"wf1","version":1,"node":{"id":"mail","type":"email_trigger","credential_id":"cred1","config":{"query":"in:inbox","poll_seconds":30}},"checkpoint":{"history_id":"100","last_poll_at":1}}]}),
        );
    }
    if path.starts_with("/internal/triggers/wf1/credentials/") {
        return (
            200,
            json!({"credential":{"data":{"client_id":"id","client_secret":"secret","refresh_token":"refresh"}}}),
        );
    }
    if path == "/token" {
        return (200, json!({"access_token":"access"}));
    }
    if path.contains("/history?") {
        return if path.contains("pageToken=p2") {
            (
                200,
                json!({"historyId":"102","history":[{"messagesAdded":[{"message":{"id":"m2"}}]}]}),
            )
        } else {
            (
                200,
                json!({"historyId":"101","nextPageToken":"p2","history":[{"messagesAdded":[{"message":{"id":"m1"}}]}]}),
            )
        };
    }
    if path.contains("/messages/m1?") {
        return (
            200,
            json!({"id":"m1","threadId":"t1","labelIds":["INBOX"],"snippet":"one","payload":{"headers":[{"name":"From","value":"a@example.com"},{"name":"Subject","value":"One"}],"body":{"data":"b25l"}}}),
        );
    }
    if path.contains("/messages/m2?") {
        return (
            200,
            json!({"id":"m2","threadId":"t2","labelIds":["INBOX"],"snippet":"two","payload":{"headers":[{"name":"From","value":"b@example.com"},{"name":"Subject","value":"Two"}],"body":{"data":"dHdv"}}}),
        );
    }
    if path.ends_with("/events") && reject_events {
        return (500, json!({"error":"queue unavailable"}));
    }
    (200, json!({"ok":true}))
}

#[tokio::test]
async fn gmail_pages_history_and_commits_only_after_events() {
    let _guard = TEST_ENV.lock().await;
    let (api, seen) = server(false).await;
    poll_triggers(&api).await.unwrap();
    let records = seen.lock().unwrap();
    let events: Vec<_> = records
        .iter()
        .filter(|(path, _)| path.ends_with("/events"))
        .collect();
    assert_eq!(events.len(), 2);
    assert_eq!(events[0].1["input"]["sender"], "a@example.com");
    assert_eq!(events[1].1["input"]["body"], "two");
    let checkpoint = records
        .iter()
        .find(|(path, _)| path.ends_with("/checkpoint"))
        .unwrap();
    assert_eq!(checkpoint.1["checkpoint"]["history_id"], "102");
    assert!(records
        .iter()
        .any(|(path, _)| path.contains("pageToken=p2")));
}

#[tokio::test]
async fn gmail_does_not_advance_cursor_when_queue_rejects_event() {
    let _guard = TEST_ENV.lock().await;
    let (api, seen) = server(true).await;
    assert!(poll_triggers(&api).await.is_err());
    let records = seen.lock().unwrap();
    assert!(records.iter().any(|(path, _)| path.ends_with("/events")));
    assert!(!records
        .iter()
        .any(|(path, _)| path.ends_with("/checkpoint")));
}
