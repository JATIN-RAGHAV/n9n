# n9n

n9n is a visual workflow automation workspace. A Flutter Web editor builds workflows, a Go API stores accounts, drafts, versions, credentials, and durable run records in PostgreSQL, and a Rust worker executes published graphs. The three application services and a local PostgreSQL instance run together with Docker Compose.

## Start

Requirements: Docker with Compose, Make, Python 3, and enough disk space to build Flutter, Go, and Rust images.

```sh
make setup
make up
```

Open [http://localhost:8080](http://localhost:8080), create an account, and create a workflow. `make setup` writes `.env` once with random runner and encryption secrets. Keep this file and the Docker volume backed up. Set `COOKIE_SECURE=true` behind HTTPS in production, and set `ALLOWED_ORIGIN` to the public site origin.

Add one trigger (manual, webhook, schedule, or Gmail email), drag action nodes onto the canvas, and connect an output port to an input port. Select a node to set its fields. Save the draft and publish it to create an executable version. **Run now** starts a manual run of that published version. Activate a published workflow to enable webhook and polling triggers. You can inspect a run from the workflow's Runs strip or the workspace home page.

For **Connect Gmail**, enable the Gmail API in a Google Cloud project, configure the OAuth consent screen and test users, and create an OAuth web client. Add the exact redirect URI `http://localhost:8080/api/oauth/google/callback` (or your public HTTPS origin), enter `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and the matching `GOOGLE_REDIRECT_URI` in `.env`, then restart the backend. The connection requests `gmail.readonly` and `gmail.send` scopes. Connect the mailbox under **Credentials**, then assign it to each email node. Advanced users can enter an OAuth refresh token manually. The OAuth state and token exchange are covered by mocked backend tests; a live Google authorization requires your own Cloud project and has not been exercised in this repository. An HTTP request node resolves mapping expressions such as `{{input.customer_id}}` and `{{nodes.NODE_ID.email}}` at runtime.

To call an active webhook, send a POST to `/api/hooks/WORKFLOW_ID` with `X-Webhook-Secret` matching the webhook node secret. `X-Event-ID` is optional for deduplication.

```sh
make logs
make down
```

`make down` keeps the PostgreSQL volume. The web server exposes `/healthz`; the API exposes `/api/health`. The browser only reaches the Go API through the web server's `/api/` proxy. Internal runner endpoints are unavailable from that proxy. `RUN_RETENTION_DAYS` defaults to 30 for terminal runs and step data; the backend cleans them and expired sessions hourly. Webhook event IDs remain as deduplication tombstones after run cleanup. If you change `WEB_PORT`, set `ALLOWED_ORIGIN` and `GOOGLE_REDIRECT_URI` to the matching public origin.

## PostgreSQL configuration

Local `make up` starts PostgreSQL 16 with a persistent `postgres_data` volume. The backend reads `DATABASE_URL`; the default is `postgres://n9n:n9n_local_dev@postgres:5432/n9n?sslmode=disable`. Local database tools can connect on `127.0.0.1:55432` (override `POSTGRES_PORT`). These credentials are for local development. If you change the local password, update both `POSTGRES_PASSWORD` and `DATABASE_URL` before initializing the volume.

For production, replace `DATABASE_URL` in `.env` with your provider's PostgreSQL URL, including its required TLS parameters (for example `sslmode=verify-full`). Then recreate the application services:

```sh
docker compose up --build -d --wait web backend runner
```

This command does not start the local database. If it was already running, `docker compose stop postgres` stops it without deleting its data. No application code or image configuration changes are needed to switch database hosts. Keep `ENCRYPTION_KEY` unchanged when moving existing credentials. Database migrations run automatically at backend startup. Back up production PostgreSQL with your provider's backup tools or `pg_dump`.

See [database migration](docs/database.md) for moving legacy SQLite data and [tests](tests/README.md) for local verification.

## Development and architecture

- [Flutter web app](web/README.md)
- [Android and iOS app](mobile/README.md)
- [HTTP API contract](contracts/README.md)
- [Architecture and operations](docs/architecture.md)

The release web build uses `flutter build web --wasm`. Flutter supplies a JavaScript fallback on browsers without WasmGC support. The web server sends COOP and COEP headers to permit the renderer's threaded mode in capable browsers. Run `flutter analyze` and `flutter test` in `web/`, `go test ./...` in `backend/`, and `cargo test` in `runner/` for local checks.

The React Native Android/iOS client lives in `mobile/`. It uses revocable bearer sessions stored in platform secure storage and the same Go API; see [mobile setup and builds](mobile/README.md).
