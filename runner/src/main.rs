use n9n_runner::{execute, poll_triggers, Api};
use std::{env, time::Duration};
use tokio::sync::watch;
use tokio::time::sleep;

#[tokio::main]
async fn main() {
    let backend = env::var("BACKEND_URL").unwrap_or_else(|_| "http://backend:8080".into());
    let token = env::var("RUNNER_TOKEN").expect("RUNNER_TOKEN required");
    if token.len() < 32 {
        panic!("RUNNER_TOKEN must have at least 32 characters")
    }
    let runner_id =
        env::var("RUNNER_ID").unwrap_or_else(|_| format!("runner-{}", std::process::id()));
    let interval = env::var("RUNNER_POLL_MS")
        .ok()
        .and_then(|v| v.parse::<u64>().ok())
        .unwrap_or(2000)
        .max(250);
    let allow_private = env::var("ALLOW_PRIVATE_HTTP")
        .map(|v| v == "true")
        .unwrap_or(false);
    let api = Api::new(backend, token).expect("invalid backend client");
    let (shutdown_tx, shutdown_rx) = watch::channel(false);
    let trigger_api = api.clone();
    let mut trigger_shutdown = shutdown_rx.clone();
    let trigger_task = tokio::spawn(async move {
        loop {
            if *trigger_shutdown.borrow() {
                break;
            };
            if let Err(e) = poll_triggers(&trigger_api).await {
                eprintln!("trigger poll: {e}")
            };
            tokio::select! {_ = sleep(Duration::from_secs(5))=>{},_ = trigger_shutdown.changed()=>{break}}
        }
    });
    let jobs_api = api.clone();
    let mut job_shutdown = shutdown_rx;
    let job_task = tokio::spawn(async move {
        loop {
            if *job_shutdown.borrow() {
                break;
            };
            match jobs_api.claim(&runner_id).await {
                Ok(Some(job)) => {
                    let run_id = job.id.clone();
                    if let Err(e) = execute(&jobs_api, job, allow_private).await {
                        eprintln!("job {run_id}: {e}")
                    }
                }
                Ok(None) => {
                    tokio::select! {_ = sleep(Duration::from_millis(interval))=>{},_ = job_shutdown.changed()=>{break}}
                }
                Err(e) => {
                    eprintln!("claim: {e}");
                    tokio::select! {_ = sleep(Duration::from_secs(5))=>{},_ = job_shutdown.changed()=>{break}}
                }
            }
        }
    });
    #[cfg(unix)]
    {
        let mut term = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
            .expect("SIGTERM handler");
        tokio::select! {_ = tokio::signal::ctrl_c()=>{},_ = term.recv()=>{}}
    }
    #[cfg(not(unix))]
    {
        let _ = tokio::signal::ctrl_c().await;
    }
    let _ = shutdown_tx.send(true);
    let _ = tokio::time::timeout(Duration::from_secs(45), async {
        let _ = trigger_task.await;
        let _ = job_task.await;
    })
    .await;
}
