use n9n_runner::{execute, Api, Job};
use serde_json::{json, Value};
use std::{
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
};

async fn mock_backend() -> (Api, Arc<Mutex<Vec<(String, Value)>>>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let seen = Arc::new(Mutex::new(Vec::new()));
    let captured = seen.clone();
    tokio::spawn(async move {
        loop {
            let Ok((mut socket, _)) = listener.accept().await else {
                break;
            };
            let captured = captured.clone();
            tokio::spawn(async move {
                let mut bytes = Vec::new();
                let mut buffer = [0u8; 4096];
                loop {
                    let n = socket.read(&mut buffer).await.unwrap();
                    if n == 0 {
                        return;
                    };
                    bytes.extend_from_slice(&buffer[..n]);
                    if let Some(end) = bytes.windows(4).position(|x| x == b"\r\n\r\n") {
                        let head = String::from_utf8_lossy(&bytes[..end]);
                        let length = head
                            .lines()
                            .find_map(|line| {
                                line.to_ascii_lowercase()
                                    .strip_prefix("content-length:")
                                    .and_then(|x| x.trim().parse::<usize>().ok())
                            })
                            .unwrap_or(0);
                        if bytes.len() >= end + 4 + length {
                            let path = head
                                .lines()
                                .next()
                                .unwrap()
                                .split_whitespace()
                                .nth(1)
                                .unwrap()
                                .to_string();
                            let body = serde_json::from_slice(&bytes[end + 4..end + 4 + length])
                                .unwrap_or(Value::Null);
                            captured.lock().unwrap().push((path, body));
                            let response=b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}";
                            socket.write_all(response).await.unwrap();
                            return;
                        }
                    }
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

fn job(steps: Value) -> Job {
    serde_json::from_value(json!({"id":"run1","lease_token":"lease1","workflow_id":"wf1","version":1,"input":{"score":9},"steps":steps,"graph":{"nodes":[{"id":"start","type":"manual_trigger","config":{}},{"id":"gate","type":"condition","config":{"left":"{{input.score}}","operator":"greater_than","right":5}},{"id":"yes","type":"set_fields","config":{"fields":{"answer":"yes"}}},{"id":"no","type":"set_fields","config":{"fields":{"answer":"no"}}}],"edges":[{"id":"a","source":"start","target":"gate","source_port":"out"},{"id":"b","source":"gate","target":"yes","source_port":"true"},{"id":"c","source":"gate","target":"no","source_port":"false"}]}})).unwrap()
}

#[tokio::test]
async fn runs_selected_branch_and_records_skips() {
    let (api, seen) = mock_backend().await;
    tokio::time::timeout(Duration::from_secs(5), execute(&api, job(json!([])), false))
        .await
        .unwrap()
        .unwrap();
    let events = seen.lock().unwrap();
    let steps: Vec<_> = events
        .iter()
        .filter(|(path, _)| path.ends_with("/steps"))
        .map(|(_, body)| {
            (
                body["node_id"].as_str().unwrap().to_string(),
                body["status"].as_str().unwrap().to_string(),
            )
        })
        .collect();
    assert!(steps.contains(&("yes".into(), "succeeded".into())));
    assert!(steps.contains(&("no".into(), "skipped".into())));
    assert!(!steps.contains(&("no".into(), "running".into())));
    assert!(events
        .iter()
        .any(|(path, body)| path.ends_with("/complete") && body["status"] == "succeeded"));
}

#[tokio::test]
async fn resume_reuses_confirmed_outputs() {
    let (api, seen) = mock_backend().await;
    let saved = json!([{"node_id":"start","status":"succeeded","output":{"score":9},"branch":"","attempt":1},{"node_id":"gate","status":"succeeded","output":{"score":9},"branch":"true","attempt":1},{"node_id":"yes","status":"succeeded","output":{"score":9,"answer":"yes"},"branch":"","attempt":1}]);
    tokio::time::timeout(Duration::from_secs(5), execute(&api, job(saved), false))
        .await
        .unwrap()
        .unwrap();
    let events = seen.lock().unwrap();
    let steps: Vec<_> = events
        .iter()
        .filter(|(path, _)| path.ends_with("/steps"))
        .map(|(_, body)| {
            (
                body["node_id"].as_str().unwrap().to_string(),
                body["status"].as_str().unwrap().to_string(),
            )
        })
        .collect();
    assert_eq!(steps, vec![("no".into(), "skipped".into())]);
}
