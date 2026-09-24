# Architecture and operations

The public boundary is the nginx web service on port 80 (published as 8080 by default). It serves Flutter assets and forwards `/api/*` to the Go service. `/internal` and `/api/internal` are blocked at nginx. The Go service and Rust worker are reachable only on the Compose network. The worker authenticates internal calls with `RUNNER_TOKEN` and uses leases so a failed worker can be detected. Run and step data is in PostgreSQL, selected by `DATABASE_URL`. Local Compose adds a PostgreSQL service backed by `postgres_data`; production can use an external PostgreSQL URL.

Android and iOS use a React Native (Expo) client in `mobile/`. The Go API issues revocable native bearer sessions, stored in Keychain/Keystore-backed SecureStore and scoped to the configured server origin. Native requests do not rely on ambient browser cookies. Gmail's configured OAuth browser flow remains in the web app.

Draft edits do not change an active execution. Publishing validates and snapshots a graph as a numbered version. Runs use that version's snapshot. A workflow allows one trigger, one incoming connection per action, and no cycles. Condition nodes have true and false output ports.

Secrets for Gmail credentials are encrypted by the backend using `ENCRYPTION_KEY`. The browser only sees credential metadata and never receives saved secret values. Back up `.env` alongside the `postgres_data` volume; losing the encryption key makes saved credentials unreadable. If a worker lease expires during a potentially side effecting step, the run may be marked `uncertain` for manual review.

For production, place HTTPS in front of web, set `COOKIE_SECURE=true`, set `ALLOWED_ORIGIN` to the exact public origin, generate secrets using `make setup` before first use, and back up the volume. For local HTTP development, `COOKIE_SECURE=false` is required for login cookies. The backend allows 20 registration/login attempts per source IP per minute and at most four concurrent password checks. It records a PostgreSQL schema version and serializes migrations at startup. Terminal runs and their steps are retained for `RUN_RETENTION_DAYS` (30 by default); expired sessions and OAuth states are removed hourly. Webhook event IDs remain as deduplication tombstones. A replay after its run is pruned returns `{"run":null,"duplicate":true}`.

Gmail credential secrets are encrypted by `ENCRYPTION_KEY`. Workflow configuration such as webhook secrets and HTTP headers, along with run inputs and outputs, remain plaintext in PostgreSQL; protect the volume and its backups accordingly.
