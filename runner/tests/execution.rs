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

async fn step_events(input: Value, graph: Value) -> (Vec<(String, String, String)>, Vec<Value>) {
    let (api, seen) = mock_backend().await;
    let job: Job = serde_json::from_value(json!({"id":"run1","lease_token":"lease1","workflow_id":"wf1","version":1,"input":input,"steps":[],"graph":graph})).unwrap();
    let result = tokio::time::timeout(Duration::from_secs(5), execute(&api, job, false))
        .await
        .unwrap();
    assert_eq!(result.unwrap_err(), "action output exceeded 256 KiB");
    let events = seen.lock().unwrap();
    let steps = events
        .iter()
        .filter(|(path, _)| path.ends_with("/steps"))
        .map(|(_, body)| {
            (
                body["node_id"].as_str().unwrap().to_string(),
                body["status"].as_str().unwrap().to_string(),
                body["error"].as_str().unwrap().to_string(),
            )
        })
        .collect();
    let completes = events
        .iter()
        .filter(|(path, _)| path.ends_with("/complete"))
        .map(|(_, body)| body.clone())
        .collect();
    (steps, completes)
}

#[tokio::test]
async fn oversized_trigger_output_records_failed_step() {
    let big = "x".repeat(300 * 1024);
    let graph = json!({"nodes":[{"id":"start","type":"webhook_trigger","config":{"secret":"s"}}],"edges":[]});
    let (steps, completes) = step_events(json!({"blob":big}), graph).await;
    assert_eq!(
        steps,
        vec![
            ("start".into(), "running".into(), "".into()),
            (
                "start".into(),
                "failed".into(),
                "action output exceeded 256 KiB".into()
            ),
        ]
    );
    assert_eq!(completes.len(), 1);
    assert_eq!(completes[0]["status"], "failed");
}

#[tokio::test]
async fn oversized_action_output_records_failed_step_and_stops() {
    let half = "x".repeat(150 * 1024);
    let graph = json!({"nodes":[{"id":"start","type":"manual_trigger","config":{}},{"id":"grow","type":"set_fields","config":{"fields":{"extra":half}}},{"id":"after","type":"set_fields","config":{"fields":{"done":true}}}],"edges":[{"id":"a","source":"start","target":"grow","source_port":"out"},{"id":"b","source":"grow","target":"after","source_port":"out"}]});
    let (steps, completes) = step_events(json!({"blob":half}), graph).await;
    let last = steps.last().unwrap();
    assert_eq!(
        last,
        &(
            "grow".into(),
            "failed".into(),
            "action output exceeded 256 KiB".into()
        )
    );
    assert!(!steps.iter().any(|(node, _, _)| node == "after"));
    assert_eq!(completes[0]["status"], "failed");
}

/// An HTTP target that answers every connection with the same status line.
async fn fixed_target(status: &'static str) -> String {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        loop {
            let Ok((mut socket, _)) = listener.accept().await else {
                break;
            };
            tokio::spawn(async move {
                let mut buffer = [0u8; 4096];
                let _ = socket.read(&mut buffer).await;
                let reply = format!("HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{{}}");
                let _ = socket.write_all(reply.as_bytes()).await;
            });
        }
    });
    format!("http://{addr}/")
}

async fn http_job_attempts(method: &str, target: String) -> (Result<(), String>, Vec<Value>) {
    let (api, seen) = mock_backend().await;
    let job: Job = serde_json::from_value(json!({"id":"run1","lease_token":"lease1","workflow_id":"wf1","version":1,"input":{},"steps":[],"graph":{"nodes":[{"id":"start","type":"manual_trigger","config":{}},{"id":"call","type":"http_request","config":{"url":target,"method":method}}],"edges":[{"id":"a","source":"start","target":"call","source_port":"out"}]}})).unwrap();
    let result = tokio::time::timeout(Duration::from_secs(10), execute(&api, job, true))
        .await
        .unwrap();
    let events = seen.lock().unwrap();
    let attempts = events
        .iter()
        .filter(|(path, _)| path == "/internal/jobs/run1/attempts")
        .map(|(_, body)| body.clone())
        .collect();
    (result, attempts)
}

#[tokio::test]
async fn retried_get_records_every_attempt() {
    let target = fixed_target("500 Internal Server Error").await;
    let (result, attempts) = http_job_attempts("GET", target).await;
    assert!(result.unwrap_err().contains("HTTP 500"));
    assert_eq!(attempts.len(), 3);
    for (i, attempt) in attempts.iter().enumerate() {
        assert_eq!(attempt["lease_token"], "lease1");
        assert_eq!(attempt["node_id"], "call");
        assert_eq!(attempt["attempt"], i as u64 + 1);
        assert_eq!(attempt["status"], "failed");
        assert_eq!(attempt["http_status"], 500);
        assert!(attempt["error"].as_str().unwrap().contains("HTTP 500"));
        assert!(attempt["duration_ms"].is_u64());
    }
}

#[tokio::test]
async fn post_without_idempotency_key_records_one_attempt() {
    let target = fixed_target("500 Internal Server Error").await;
    let (result, attempts) = http_job_attempts("POST", target).await;
    assert!(result.unwrap_err().starts_with("uncertain:"));
    assert_eq!(attempts.len(), 1);
    assert_eq!(attempts[0]["attempt"], 1);
    assert_eq!(attempts[0]["http_status"], 500);
}

#[tokio::test]
async fn successful_request_records_a_succeeded_attempt() {
    let target = fixed_target("200 OK").await;
    let (result, attempts) = http_job_attempts("GET", target).await;
    result.unwrap();
    assert_eq!(attempts.len(), 1);
    assert_eq!(attempts[0]["status"], "succeeded");
    assert_eq!(attempts[0]["http_status"], 200);
    assert_eq!(attempts[0]["error"], "");
}

#[tokio::test]
async fn steps_report_start_time_and_duration() {
    let (api, seen) = mock_backend().await;
    tokio::time::timeout(Duration::from_secs(5), execute(&api, job(json!([])), false))
        .await
        .unwrap()
        .unwrap();
    let events = seen.lock().unwrap();
    let steps: Vec<&Value> = events
        .iter()
        .filter(|(path, _)| path.ends_with("/steps"))
        .map(|(_, body)| body)
        .collect();
    for step in &steps {
        match step["status"].as_str().unwrap() {
            "running" => {
                assert!(step["started_at"].as_str().unwrap().ends_with('Z'));
                assert!(step.get("duration_ms").is_none());
            }
            "succeeded" => {
                assert!(step["started_at"].is_string());
                assert!(step["duration_ms"].is_u64());
            }
            "skipped" => assert!(step.get("started_at").is_none()),
            other => panic!("unexpected status {other}"),
        }
    }
}
