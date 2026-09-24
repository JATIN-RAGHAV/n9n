# Architecture and operations

The public boundary is the nginx web service on port 80 (published as 8080 by default). It serves Flutter assets and forwards `/api/*` to the Go service. `/api/internal` is blocked at nginx. The Go service and Rust worker are reachable only on the Compose network. The worker authenticates internal calls with `RUNNER_TOKEN` and uses leases so a failed worker can be detected. Run and step data is in SQLite under the named `n9n_data` volume.

Draft edits do not change an active execution. Publishing validates and snapshots a graph as a numbered version. Runs use that version's snapshot. A workflow allows one trigger, one incoming connection per action, and no cycles. Condition nodes have true and false output ports.

Secrets for Gmail credentials are encrypted by the backend using `ENCRYPTION_KEY`. The browser only sees credential metadata and never receives saved secret values. Back up `.env` alongside the `n9n_data` volume; losing the encryption key makes saved credentials unreadable. If a worker lease expires during a potentially side effecting step, the run may be marked `uncertain` for manual review.

For production, place HTTPS in front of web, set `COOKIE_SECURE=true`, set `ALLOWED_ORIGIN` to the exact public origin, rotate placeholder secrets using `make setup` before first use, and back up the volume. For local HTTP development, `COOKIE_SECURE=false` is required for login cookies.
