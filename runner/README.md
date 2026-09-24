# Continuous Rust runner

The runner leases one job at a time from the Go API and polls active schedule and Gmail triggers concurrently. Set `BACKEND_URL` (default `http://backend:8080`), the shared `RUNNER_TOKEN`, and optionally `RUNNER_ID`, `RUNNER_POLL_MS` (default 2000), and `ALLOW_PRIVATE_HTTP` (default false). Keep private HTTP disabled unless workflows intentionally call trusted services on the local network.

Workflow mappings use `{{input.field}}` for the immediate parent node's output and `{{nodes.node_id.field}}` for any previously completed node. A string made entirely of one mapping retains its JSON type. `set_fields` merges configured fields into its input, `condition` follows its true or false port, and an HTTP node returns `{status,headers,body}`. Nonselected branches are recorded as skipped. Confirmed successful steps resume from saved output after a lost lease.

HTTP nodes resolve DNS before each request, reject private and reserved addresses by default, pin the resolved address for that request, and do not follow redirects. GET and HEAD requests retry transient failures up to three attempts. Other methods retry only when `idempotency_key` is configured; it is sent as `Idempotency-Key`. An interrupted non-idempotent request or Gmail send is marked uncertain for review.

Gmail credentials use OAuth refresh tokens with `client_id`, `client_secret`, and `refresh_token`. The backend's Connect Gmail flow creates these credentials. Gmail triggers poll the Gmail API with a configurable `query` (default `in:inbox`) and `poll_seconds` (minimum 30). The first poll establishes a checkpoint; later polls page through messages newer than the checkpoint, queue one event per message ID, and advance the checkpoint after all events are accepted. Gmail send uses the Gmail API's `users.messages.send` endpoint. `GMAIL_TOKEN_URL` and `GMAIL_API_BASE` can redirect these calls in local integration tests.

Stop with SIGTERM or SIGINT. The runner stops claiming new jobs and gives the current job up to 45 seconds to finish while heartbeats continue. Run tests with `cargo test`.
