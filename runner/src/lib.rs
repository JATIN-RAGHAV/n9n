mod gmail;
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use chrono::{DateTime, Utc};
use regex::Regex;
use reqwest::{Client, Method, Url};
use serde::{Deserialize, Serialize};
use serde_json::{json, Map, Value};
use std::{
    collections::HashMap,
    net::{IpAddr, SocketAddr},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    time::Duration,
};
use tokio::{
    net::lookup_host,
    time::{sleep, timeout},
};

pub type Result<T> = std::result::Result<T, String>;

pub fn health_touch(kind: &str) {
    let dir = std::env::var("RUNNER_HEALTH_DIR").unwrap_or_else(|_| "/tmp/n9n-health".into());
    let _ = std::fs::create_dir_all(&dir);
    let temp = format!(
        "{dir}/{kind}.{}.tmp",
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|x| x.as_nanos())
            .unwrap_or(0)
    );
    if std::fs::write(&temp, Utc::now().timestamp().to_string()).is_ok() {
        let _ = std::fs::rename(&temp, format!("{dir}/{kind}"));
    }
}

pub fn health_check() -> Result<()> {
    let dir = std::env::var("RUNNER_HEALTH_DIR").unwrap_or_else(|_| "/tmp/n9n-health".into());
    let now = Utc::now().timestamp();
    for kind in ["jobs", "triggers"] {
        let value = std::fs::read_to_string(format!("{dir}/{kind}")).map_err(|e| e.to_string())?;
        let last: i64 = value
            .parse()
            .map_err(|e: std::num::ParseIntError| e.to_string())?;
        if now - last > 35 || last > now + 5 {
            return Err(format!("{kind} loop stale"));
        }
    }
    Ok(())
}

#[derive(Clone)]
pub struct Api {
    pub base: String,
    pub token: String,
    pub client: Client,
}
impl Api {
    pub fn new(base: String, token: String) -> Result<Self> {
        Ok(Self {
            base: base.trim_end_matches('/').to_string(),
            token,
            client: Client::builder()
                .timeout(Duration::from_secs(20))
                .build()
                .map_err(|e| e.to_string())?,
        })
    }
    pub async fn get(&self, path: &str, lease: Option<&str>) -> Result<Value> {
        let mut req = self
            .client
            .get(format!("{}{}", self.base, path))
            .bearer_auth(&self.token);
        if let Some(l) = lease {
            req = req.header("X-Lease-Token", l)
        };
        self.send(req).await
    }
    pub async fn post(&self, path: &str, body: Value) -> Result<Value> {
        self.send(
            self.client
                .post(format!("{}{}", self.base, path))
                .bearer_auth(&self.token)
                .json(&body),
        )
        .await
    }
    async fn send(&self, req: reqwest::RequestBuilder) -> Result<Value> {
        let res = req.send().await.map_err(|e| e.to_string())?;
        let status = res.status();
        let bytes = res.bytes().await.map_err(|e| e.to_string())?;
        if !status.is_success() {
            return Err(format!(
                "HTTP {}: {}",
                status,
                String::from_utf8_lossy(&bytes)
            ));
        };
        serde_json::from_slice(&bytes).map_err(|e| e.to_string())
    }
    pub async fn claim(&self, runner_id: &str) -> Result<Option<Job>> {
        let value = self
            .post("/internal/jobs/claim", json!({"runner_id":runner_id}))
            .await?;
        serde_json::from_value(value.get("job").cloned().unwrap_or(Value::Null))
            .map_err(|e| e.to_string())
    }
    #[allow(clippy::too_many_arguments)]
    pub async fn step(
        &self,
        job: &Job,
        node: &str,
        status: &str,
        input: Value,
        output: Value,
        error: &str,
        branch: &str,
        attempt: u32,
    ) -> Result<()> {
        self.post(&format!("/internal/jobs/{}/steps",job.id),json!({"lease_token":job.lease_token,"node_id":node,"status":status,"input":input,"output":output,"error":error,"branch":branch,"attempt":attempt})).await.map(|_|())
    }
    pub async fn complete(&self, job: &Job, status: &str, error: &str) -> Result<()> {
        self.post(
            &format!("/internal/jobs/{}/complete", job.id),
            json!({"lease_token":job.lease_token,"status":status,"error":error}),
        )
        .await
        .map(|_| ())
    }
    pub async fn credential(&self, job: &Job, id: &str) -> Result<Value> {
        let v = self
            .get(
                &format!("/internal/jobs/{}/credentials/{}", job.id, id),
                Some(&job.lease_token),
            )
            .await?;
        Ok(v["credential"]["data"].clone())
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Node {
    pub id: String,
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub config: Value,
    #[serde(default)]
    pub credential_id: Option<String>,
}
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Edge {
    pub id: String,
    pub source: String,
    pub target: String,
    pub source_port: String,
}
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Graph {
    pub nodes: Vec<Node>,
    pub edges: Vec<Edge>,
}
#[derive(Clone, Debug, Deserialize)]
pub struct Step {
    pub node_id: String,
    pub status: String,
    #[serde(default)]
    pub output: Value,
    #[serde(default)]
    pub branch: String,
    #[serde(default)]
    pub attempt: u32,
}
#[derive(Clone, Debug, Deserialize)]
pub struct Job {
    pub id: String,
    pub lease_token: String,
    pub workflow_id: String,
    pub version: i64,
    #[serde(default)]
    pub test: bool,
    pub graph: Graph,
    pub input: Value,
    pub steps: Vec<Step>,
}

pub fn render(template: &Value, input: &Value, outputs: &HashMap<String, Value>) -> Result<Value> {
    let mut remaining = 256 * 1024;
    render_bounded(template, input, outputs, &mut remaining, 0)
}
fn render_bounded(
    template: &Value,
    input: &Value,
    outputs: &HashMap<String, Value>,
    remaining: &mut usize,
    depth: usize,
) -> Result<Value> {
    if depth > 32 {
        return Err("mapping exceeds maximum nesting depth".into());
    }
    match template {
        Value::String(s) => {
            let value = render_string(s, input, outputs)?;
            charge(
                remaining,
                serde_json::to_vec(&value).map_err(|e| e.to_string())?.len(),
            )?;
            Ok(value)
        }
        Value::Array(a) => {
            charge(remaining, 2 + a.len())?;
            let mut out = Vec::with_capacity(a.len().min(1024));
            for value in a {
                out.push(render_bounded(value, input, outputs, remaining, depth + 1)?)
            }
            Ok(Value::Array(out))
        }
        Value::Object(o) => {
            charge(remaining, 2 + o.len())?;
            let mut m = Map::new();
            for (k, v) in o {
                charge(remaining, k.len() + 2)?;
                m.insert(
                    k.clone(),
                    render_bounded(v, input, outputs, remaining, depth + 1)?,
                );
            }
            Ok(Value::Object(m))
        }
        v => {
            charge(
                remaining,
                serde_json::to_vec(v).map_err(|e| e.to_string())?.len(),
            )?;
            Ok(v.clone())
        }
    }
}
fn charge(remaining: &mut usize, bytes: usize) -> Result<()> {
    if bytes > *remaining {
        return Err("rendered node configuration exceeds 256 KiB".into());
    };
    *remaining -= bytes;
    Ok(())
}
fn append_limited(out: &mut String, piece: &str) -> Result<()> {
    if piece.len() > 256 * 1024 - out.len().min(256 * 1024) {
        return Err("rendered node configuration exceeds 256 KiB".into());
    }
    out.push_str(piece);
    Ok(())
}
fn render_string(s: &str, input: &Value, outputs: &HashMap<String, Value>) -> Result<Value> {
    let re = Regex::new(r"\{\{\s*(input|nodes)\.([A-Za-z0-9_-]+)(?:\.([A-Za-z0-9_.-]+))?\s*\}\}")
        .unwrap();
    let captures: Vec<_> = re.captures_iter(s).collect();
    if captures.is_empty() {
        return Ok(Value::String(s.into()));
    };
    let resolve = |c: &regex::Captures<'_>| -> Result<Value> {
        let scope = &c[1];
        let key = &c[2];
        let mut v = if scope == "input" {
            input
                .get(key)
                .cloned()
                .ok_or_else(|| format!("missing input field {key}"))?
        } else {
            outputs
                .get(key)
                .cloned()
                .ok_or_else(|| format!("missing node output {key}"))?
        };
        if let Some(path) = c.get(3) {
            for part in path.as_str().split('.') {
                v = v
                    .get(part)
                    .cloned()
                    .ok_or_else(|| format!("missing mapping field {part}"))?
            }
        };
        Ok(v)
    };
    if captures.len() == 1 && captures[0].get(0).unwrap().as_str() == s {
        return resolve(&captures[0]);
    };
    let mut out = String::new();
    let mut last = 0;
    for c in captures {
        let m = c.get(0).unwrap();
        append_limited(&mut out, &s[last..m.start()])?;
        let value = resolve(&c)?;
        let encoded = match &value {
            Value::String(v) => v.clone(),
            _ => value.to_string(),
        };
        append_limited(&mut out, &encoded)?;
        last = m.end()
    }
    append_limited(&mut out, &s[last..])?;
    Ok(Value::String(out))
}

pub fn condition(config: &Value, input: &Value, outputs: &HashMap<String, Value>) -> Result<bool> {
    let operator = config["operator"].as_str().unwrap_or("");
    let left = match render(&config["left"], input, outputs) {
        Ok(value) => value,
        Err(_) if operator == "exists" => return Ok(false),
        Err(error) => return Err(error),
    };
    let right = if operator == "exists" {
        Value::Null
    } else {
        render(&config["right"], input, outputs)?
    };
    match operator {
        "equals" => Ok(left == right),
        "contains" => Ok(match (&left, &right) {
            (Value::String(a), Value::String(b)) => a.contains(b),
            (Value::Array(a), b) => a.contains(b),
            _ => false,
        }),
        "greater_than" => Ok(match (left.as_f64(), right.as_f64()) {
            (Some(a), Some(b)) => a > b,
            _ => left
                .as_str()
                .zip(right.as_str())
                .map(|(a, b)| a > b)
                .unwrap_or(false),
        }),
        "exists" => Ok(!left.is_null()),
        _ => Err("invalid condition operator".into()),
    }
}

pub fn is_public_ip(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(a) => {
            let o = a.octets();
            !(a.is_private()
                || a.is_loopback()
                || a.is_link_local()
                || a.is_broadcast()
                || a.is_unspecified()
                || a.is_multicast()
                || o[0] == 0
                || o[0] >= 224
                || o[0] == 100 && (64..=127).contains(&o[1])
                || o[0] == 169 && o[1] == 254
                || o[0] == 192 && o[1] == 0
                || o[0] == 198 && (o[1] == 18 || o[1] == 19)
                || o[0] == 192 && o[1] == 0 && o[2] == 0
                || o[0] == 198 && o[1] == 51 && o[2] == 100
                || o[0] == 203 && o[1] == 0 && o[2] == 113)
        }
        IpAddr::V6(a) => {
            let first = a.segments()[0];
            !(a.is_loopback()
                || a.is_unspecified()
                || a.is_multicast()
                || a.is_unique_local()
                || a.is_unicast_link_local()
                || a.to_ipv4_mapped().is_some()
                || first & 0xffc0 == 0xfe80
                || first == 0x2001 && a.segments()[1] == 0x0db8
                || first == 0x0064 && a.segments()[1] == 0xff9b
                || first == 0
                    && a.segments()[1] == 0
                    && a.segments()[2] == 0
                    && a.segments()[3] == 0)
        }
    }
}
pub async fn safe_http_client(url: &Url, allow_private: bool) -> Result<Client> {
    if url.scheme() != "https" && url.scheme() != "http" {
        return Err("HTTP URL must use http or https".into());
    };
    if !url.username().is_empty() || url.password().is_some() {
        return Err("URL credentials forbidden".into());
    };
    let host = url.host_str().ok_or("URL host required")?;
    let port = url.port_or_known_default().ok_or("URL port required")?;
    let addresses: Vec<SocketAddr> = lookup_host((host, port))
        .await
        .map_err(|e| format!("DNS: {e}"))?
        .collect();
    if addresses.is_empty() {
        return Err("DNS returned no addresses".into());
    };
    if !allow_private && addresses.iter().any(|x| !is_public_ip(x.ip())) {
        return Err("private or reserved HTTP address blocked".into());
    };
    Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(Duration::from_secs(15))
        .resolve_to_addrs(host, &addresses)
        .build()
        .map_err(|e| e.to_string())
}

fn string_config<'a>(config: &'a Value, key: &str) -> Result<&'a str> {
    config
        .get(key)
        .and_then(Value::as_str)
        .ok_or_else(|| format!("{key} must be string"))
}
pub async fn http_node(
    config: &Value,
    input: &Value,
    outputs: &HashMap<String, Value>,
    allow_private: bool,
) -> Result<Value> {
    let rendered = render(config, input, outputs)?;
    let url = Url::parse(string_config(&rendered, "url")?).map_err(|e| e.to_string())?;
    let client = safe_http_client(&url, allow_private).await?;
    let method = Method::from_bytes(string_config(&rendered, "method")?.as_bytes())
        .map_err(|e| e.to_string())?;
    let safe = matches!(method, Method::GET | Method::HEAD);
    let idem = match rendered.get("idempotency_key") {
        None => None,
        Some(Value::String(value)) if !value.is_empty() => Some(value.as_str()),
        Some(_) => return Err("idempotency_key must render to a nonempty string".into()),
    };
    let ambiguous = !safe && idem.is_none();
    let mut tries = 0;
    loop {
        tries += 1;
        let mut req = client.request(method.clone(), url.clone());
        if let Some(headers) = rendered["headers"].as_object() {
            for (k, v) in headers {
                let value = v.as_str().ok_or("header value must be string")?;
                req = req.header(k, value)
            }
        };
        if let Some(key) = idem {
            req = req.header("Idempotency-Key", key)
        };
        match &rendered["body"] {
            Value::Null => {}
            Value::String(s) if s.is_empty() => {}
            Value::String(s) => {
                req = req.body(s.clone());
            }
            value => {
                req = req.json(value);
            }
        }
        match req.send().await {
            Ok(mut res) => {
                let status = res.status();
                let headers: Map<String, Value> = res
                    .headers()
                    .iter()
                    .filter_map(|(k, v)| {
                        v.to_str()
                            .ok()
                            .map(|x| (k.to_string(), Value::String(x.into())))
                    })
                    .collect();
                let mut bytes = Vec::new();
                loop {
                    let chunk = timeout(Duration::from_secs(10), res.chunk())
                        .await
                        .map_err(|_| {
                            if ambiguous {
                                "uncertain: HTTP response timeout".to_string()
                            } else {
                                "HTTP response timeout".to_string()
                            }
                        })?
                        .map_err(|e| {
                            if ambiguous {
                                format!("uncertain: HTTP response read failed: {e}")
                            } else {
                                e.to_string()
                            }
                        })?;
                    let Some(chunk) = chunk else { break };
                    if bytes.len() + chunk.len() > 256 << 10 {
                        return Err(if ambiguous {
                            "uncertain: HTTP response too large"
                        } else {
                            "HTTP response too large"
                        }
                        .into());
                    }
                    bytes.extend_from_slice(&chunk);
                }
                if status.is_server_error() && (safe || idem.is_some()) && tries < 3 {
                    sleep(Duration::from_millis(250 * tries)).await;
                    continue;
                };
                if !status.is_success() {
                    return Err(format!(
                        "{}HTTP {}: {}",
                        if ambiguous && status.is_server_error() {
                            "uncertain: "
                        } else {
                            ""
                        },
                        status,
                        String::from_utf8_lossy(&bytes)
                    ));
                };
                let body = serde_json::from_slice::<Value>(&bytes)
                    .unwrap_or_else(|_| Value::String(String::from_utf8_lossy(&bytes).to_string()));
                return Ok(json!({"status":status.as_u16(),"headers":headers,"body":body}));
            }
            Err(e) => {
                if (safe || idem.is_some()) && tries < 3 {
                    sleep(Duration::from_millis(250 * tries)).await;
                    continue;
                };
                return Err(format!(
                    "{}HTTP request failed: {e}",
                    if ambiguous { "uncertain: " } else { "" }
                ));
            }
        }
    }
}

pub async fn execute(api: &Api, job: Job, allow_private: bool) -> Result<()> {
    let alive = Arc::new(AtomicBool::new(true));
    let heartbeat_alive = alive.clone();
    let heartbeat_api = api.clone();
    let heartbeat_job = job.clone();
    let heartbeat = tokio::spawn(async move {
        loop {
            sleep(Duration::from_secs(10)).await;
            if !heartbeat_alive.load(Ordering::SeqCst) {
                break;
            };
            if heartbeat_api
                .post(
                    &format!("/internal/jobs/{}/heartbeat", heartbeat_job.id),
                    json!({"lease_token":heartbeat_job.lease_token}),
                )
                .await
                .is_err()
            {
                heartbeat_alive.store(false, Ordering::SeqCst);
                break;
            }
            health_touch("jobs");
        }
    });
    let result = execute_inner(api, &job, allow_private, &alive).await;
    alive.store(false, Ordering::SeqCst);
    heartbeat.abort();
    if let Err(e) = result {
        if e == "lease lost" {
            return Err(e);
        };
        let status = if e.starts_with("uncertain:") {
            "uncertain"
        } else {
            "failed"
        };
        api.complete(&job, status, &e).await?;
        return Err(e);
    };
    api.complete(&job, "succeeded", "").await
}
async fn execute_inner(
    api: &Api,
    job: &Job,
    allow_private: bool,
    alive: &AtomicBool,
) -> Result<()> {
    let mut nodes = HashMap::new();
    for n in &job.graph.nodes {
        nodes.insert(n.id.clone(), n.clone());
    }
    let mut children: HashMap<String, Vec<(String, String, String)>> = HashMap::new();
    for e in &job.graph.edges {
        children.entry(e.source.clone()).or_default().push((
            e.id.clone(),
            e.target.clone(),
            e.source_port.clone(),
        ));
    }
    for list in children.values_mut() {
        list.sort_by(|a, b| a.0.cmp(&b.0))
    }
    let root = job
        .graph
        .nodes
        .iter()
        .find(|n| n.kind.ends_with("_trigger"))
        .ok_or("trigger missing")?
        .id
        .clone();
    let mut latest: HashMap<String, Step> = HashMap::new();
    for step in &job.steps {
        latest.insert(step.node_id.clone(), step.clone());
    }
    let mut outputs: HashMap<String, Value> = HashMap::new();
    for (id, step) in &latest {
        if step.status == "succeeded" {
            outputs.insert(id.clone(), step.output.clone());
        }
    }
    let mut stack = vec![(root, job.input.clone(), false)];
    while let Some((node_id, input, skip)) = stack.pop() {
        if !alive.load(Ordering::SeqCst) {
            return Err("lease lost".into());
        };
        let node = nodes.get(&node_id).ok_or("graph node missing")?;
        let previous = latest.get(&node_id);
        let already = previous.map(|s| s.status.as_str());
        let mut branch = String::new();
        let mut output = Value::Null;
        if skip {
            if already != Some("skipped") {
                api.step(
                    job,
                    &node_id,
                    "skipped",
                    input.clone(),
                    Value::Null,
                    "",
                    "",
                    1,
                )
                .await?
            }
        } else if already == Some("succeeded") {
            output = previous.unwrap().output.clone();
            branch = previous.unwrap().branch.clone();
            outputs.insert(node_id.clone(), output.clone());
        } else {
            let attempt = previous.map(|s| s.attempt + 1).unwrap_or(1);
            if matches!(already, Some("running" | "failed")) && is_unsafe(&node.kind, &node.config)
            {
                return Err(format!("uncertain: interrupted action {}", node_id));
            };
            api.step(
                job,
                &node_id,
                "running",
                input.clone(),
                Value::Null,
                "",
                "",
                attempt,
            )
            .await?;
            let action = run_node(api, job, node, &input, &outputs, allow_private).await;
            match action {
                Ok((o, b)) => {
                    let output_len = serde_json::to_vec(&o)
                        .map_err(|error| error.to_string())?
                        .len();
                    if output_len > 256 * 1024 {
                        return Err(if is_unsafe(&node.kind, &node.config) {
                            "uncertain: action output exceeded 256 KiB"
                        } else {
                            "action output exceeded 256 KiB"
                        }
                        .into());
                    }
                    output = o;
                    branch = b;
                    outputs.insert(node_id.clone(), output.clone());
                    api.step(
                        job,
                        &node_id,
                        "succeeded",
                        input.clone(),
                        output.clone(),
                        "",
                        &branch,
                        attempt,
                    )
                    .await
                    .map_err(|error| if is_unsafe(&node.kind, &node.config) { format!("uncertain: action succeeded but step could not be recorded: {error}") } else { error })?
                }
                Err(e) => {
                    let report = api
                        .step(
                            job,
                            &node_id,
                            "failed",
                            input.clone(),
                            Value::Null,
                            &e,
                            "",
                            attempt,
                        )
                        .await;
                    if let Err(report_error) = report {
                        if e.starts_with("uncertain:") {
                            return Err(e);
                        }
                        return Err(format!(
                            "step failure could not be recorded: {report_error}; action: {e}"
                        ));
                    }
                    return Err(e);
                }
            }
        }
        let mut next = children.get(&node_id).cloned().unwrap_or_default();
        next.reverse();
        for (_, child, port) in next {
            let child_skip = skip || (node.kind == "condition" && port != branch);
            stack.push((child, output.clone(), child_skip))
        }
    }
    Ok(())
}
fn is_unsafe(kind: &str, config: &Value) -> bool {
    if kind == "send_email" {
        return true;
    };
    if kind == "http_request" {
        let method = config["method"].as_str().unwrap_or("GET");
        return !matches!(method.to_ascii_uppercase().as_str(), "GET" | "HEAD")
            && config["idempotency_key"].as_str().is_none_or(str::is_empty);
    };
    false
}
async fn run_node(
    api: &Api,
    job: &Job,
    node: &Node,
    input: &Value,
    outputs: &HashMap<String, Value>,
    allow_private: bool,
) -> Result<(Value, String)> {
    match node.kind.as_str() {
        "manual_trigger" | "webhook_trigger" | "schedule_trigger" | "email_trigger" => {
            Ok((input.clone(), String::new()))
        }
        "set_fields" => {
            let fields = render(&node.config["fields"], input, outputs)?;
            let mut out = input.as_object().cloned().unwrap_or_default();
            for (k, v) in fields.as_object().ok_or("fields must be object")? {
                out.insert(k.clone(), v.clone());
            }
            Ok((Value::Object(out), String::new()))
        }
        "condition" => {
            let b = condition(&node.config, input, outputs)?;
            Ok((input.clone(), b.to_string()))
        }
        "http_request" => Ok((
            http_node(&node.config, input, outputs, allow_private).await?,
            String::new(),
        )),
        "send_email" => {
            let cred = api
                .credential(
                    job,
                    node.credential_id
                        .as_deref()
                        .ok_or("email credential missing")?,
                )
                .await?;
            let config = render(&node.config, input, outputs)?;
            let sent = gmail_send(&api.client, &cred, &config).await?;
            Ok((sent, String::new()))
        }
        _ => Err(format!("unknown node type {}", node.kind)),
    }
}

pub async fn gmail_token(client: &Client, cred: &Value) -> Result<String> {
    let endpoint = std::env::var("GMAIL_TOKEN_URL")
        .unwrap_or_else(|_| "https://oauth2.googleapis.com/token".into());
    let form = [
        ("client_id", string_config(cred, "client_id")?),
        ("client_secret", string_config(cred, "client_secret")?),
        ("refresh_token", string_config(cred, "refresh_token")?),
        ("grant_type", "refresh_token"),
    ];
    let res = client
        .post(endpoint)
        .form(&form)
        .send()
        .await
        .map_err(|e| e.to_string())?;
    if !res.status().is_success() {
        return Err(format!("Gmail token HTTP {}", res.status()));
    };
    let v: Value = res.json().await.map_err(|e| e.to_string())?;
    Ok(string_config(&v, "access_token")?.to_string())
}
pub async fn gmail_send(client: &Client, cred: &Value, config: &Value) -> Result<Value> {
    let to = string_config(config, "to")?;
    let subject = string_config(config, "subject")?;
    let body = string_config(config, "body")?;
    if to.contains(['\r', '\n']) || subject.contains(['\r', '\n']) {
        return Err("invalid email header".into());
    };
    let raw=format!("To: {to}\r\nSubject: {subject}\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n{body}");
    let token = gmail_token(client, cred).await?;
    let base =
        std::env::var("GMAIL_API_BASE").unwrap_or_else(|_| "https://gmail.googleapis.com".into());
    let res = client
        .post(format!(
            "{}/gmail/v1/users/me/messages/send",
            base.trim_end_matches('/')
        ))
        .bearer_auth(token)
        .json(&json!({"raw":URL_SAFE_NO_PAD.encode(raw)}))
        .send()
        .await
        .map_err(|e| format!("uncertain: Gmail send request failed: {e}"))?;
    if !res.status().is_success() {
        return Err(format!(
            "{}Gmail send HTTP {}",
            if res.status().is_server_error() {
                "uncertain: "
            } else {
                ""
            },
            res.status()
        ));
    };
    res.json()
        .await
        .map_err(|e| format!("uncertain: Gmail send response read failed: {e}"))
}

pub async fn poll_triggers(api: &Api) -> Result<()> {
    let value = api.get("/internal/triggers", None).await?;
    health_touch("triggers");
    let triggers = value["triggers"].as_array().ok_or("triggers missing")?;
    let mut first_error = None;
    for t in triggers {
        if let Err(e) = poll_trigger(api, t).await {
            eprintln!("trigger {}: {e}", t["workflow_id"]);
            if first_error.is_none() {
                first_error = Some(e);
            }
        }
    }
    if let Some(error) = first_error {
        Err(error)
    } else {
        Ok(())
    }
}
async fn poll_trigger(api: &Api, t: &Value) -> Result<()> {
    let wid = string_config(t, "workflow_id")?;
    let version = t["version"].as_i64().ok_or("version missing")?;
    let node = &t["node"];
    let kind = string_config(node, "type")?;
    let checkpoint = &t["checkpoint"];
    match kind {
        "schedule_trigger" => {
            let interval = node["config"]["interval_seconds"]
                .as_i64()
                .ok_or("interval_seconds missing")?;
            if interval < 10 {
                return Err("interval too short".into());
            };
            let occurrence = Utc::now().timestamp().div_euclid(interval) * interval;
            let previous = checkpoint["last_occurrence"].as_i64();
            if previous.is_none() {
                api.post(
                    &format!("/internal/triggers/{wid}/checkpoint"),
                    json!({"version":version,"checkpoint":{"last_occurrence":occurrence}}),
                )
                .await?;
                return Ok(());
            };
            if previous.unwrap() < occurrence {
                api.post(&format!("/internal/triggers/{wid}/events"),json!({"version":version,"event_id":format!("schedule:{occurrence}"),"input":{"scheduled_at":DateTime::<Utc>::from_timestamp(occurrence,0).unwrap().to_rfc3339()},"checkpoint":{"last_occurrence":occurrence}})).await?;
            }
            Ok(())
        }
        "email_trigger" => gmail::poll_email(api, wid, version, node, checkpoint).await,
        _ => Ok(()),
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn mapping_preserves_types_and_reports_missing_values() {
        let mut outputs = HashMap::new();
        outputs.insert("step1".into(), json!({"body":{"count":3}}));
        assert_eq!(
            render(&json!("{{nodes.step1.body.count}}"), &json!({}), &outputs).unwrap(),
            json!(3)
        );
        assert_eq!(
            render(
                &json!("Count: {{nodes.step1.body.count}}"),
                &json!({}),
                &outputs
            )
            .unwrap(),
            json!("Count: 3")
        );
        assert_eq!(
            render(
                &json!({"a":"{{input.name}}"}),
                &json!({"name":"Ada"}),
                &outputs
            )
            .unwrap(),
            json!({"a":"Ada"})
        );
        assert!(render(&json!("{{nodes.missing.value}}"), &json!({}), &outputs).is_err());
    }
    #[test]
    fn condition_routes() {
        let outputs = HashMap::new();
        assert!(condition(
            &json!({"left":"{{input.score}}","operator":"greater_than","right":5}),
            &json!({"score":7}),
            &outputs
        )
        .unwrap());
        assert!(!condition(
            &json!({"left":"hello","operator":"contains","right":"xyz"}),
            &Value::Null,
            &outputs
        )
        .unwrap());
        assert!(!condition(
            &json!({"left":"{{input.absent}}","operator":"exists"}),
            &json!({}),
            &outputs
        )
        .unwrap())
    }
    #[test]
    fn blocks_private_addresses() {
        for ip in [
            "127.0.0.1",
            "10.0.0.1",
            "169.254.169.254",
            "::1",
            "fc00::1",
            "::ffff:127.0.0.1",
            "2001:db8::1",
            "64:ff9b::a00:1",
        ] {
            assert!(!is_public_ip(ip.parse().unwrap()), "{ip}")
        }
        assert!(is_public_ip("8.8.8.8".parse().unwrap()))
    }
    #[test]
    fn unsafe_action_detection() {
        assert!(is_unsafe("send_email", &json!({})));
        assert!(is_unsafe("http_request", &json!({"method":"POST"})));
        assert!(!is_unsafe(
            "http_request",
            &json!({"method":"POST","idempotency_key":"abc"})
        ));
        assert!(!is_unsafe("http_request", &json!({"method":"GET"})));
        assert!(is_unsafe(
            "http_request",
            &json!({"method":"post","idempotency_key":""})
        ))
    }
    #[test]
    fn render_limits_expansion_and_depth() {
        let big = "x".repeat(300_000);
        assert!(render(
            &json!("{{input.big}}"),
            &json!({"big":big}),
            &HashMap::new()
        )
        .is_err());
        let mut deep = json!(1);
        for _ in 0..34 {
            deep = json!([deep]);
        }
        assert!(render(&deep, &Value::Null, &HashMap::new()).is_err());
        let repeated = "{{input.big}}".repeat(1000);
        assert!(render(
            &json!(repeated),
            &json!({"big":"z".repeat(1024)}),
            &HashMap::new()
        )
        .is_err());
    }
}
