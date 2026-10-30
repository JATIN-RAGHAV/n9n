# n9n

_Last updated: 2026-10-30 23:43 IST · Claude Opus 5.5 (claude-opus-5-5)_

n9n is a visual workflow automation workspace. You build workflows in a Flutter Web editor or the React Native mobile app. A Go API stores them in PostgreSQL, and a Rust worker runs them.

## System architecture

[![n9n architecture](docs/architecture.png)](docs/architecture.png)

## Run locally

### Requirements

- Docker with Compose
- Enough disk space to build the Flutter, Go, and Rust images (about 10 GB free)

### Start the stack

```sh
docker compose up --build -d --wait
```

No `.env` file is needed. On the first start, the one-off `secrets` service creates a random `ENCRYPTION_KEY` and `RUNNER_TOKEN` for this machine and keeps them in the `secrets` Docker volume; later starts reuse them. Never commit or share these keys: each machine makes its own. Deleting the `secrets` volume (for example with `docker compose down -v`) makes saved credentials unreadable.

To set your own keys or other settings, create `.env` instead: `make setup` (needs Make and Python 3) copies `.env.example` and fills in random keys. Values in `.env` take precedence over the generated ones. `make up` runs `make setup` and then the command above.

The command builds and starts four containers and waits until they are healthy:

| Service | Purpose | Local address |
| --- | --- | --- |
| `web` | nginx serving the Flutter app and proxying `/api/*` | [http://localhost:8080](http://localhost:8080) |
| `backend` | Go API | Only through `web` at `/api/*` |
| `runner` | Rust worker that executes runs | Internal only |
| `postgres` | PostgreSQL 16 with the `postgres_data` volume | `127.0.0.1:55432` |

Open [http://localhost:8080](http://localhost:8080) and create an account.

### Try a workflow

1. Create a workflow and add one trigger (manual, webhook, schedule, or Gmail email).
2. Drag action nodes onto the canvas and connect an output port to an input port.
3. Select a node to set its fields.
4. **Save** the draft, then **Publish** it.
5. Click **Run now** and open the run to inspect each step.

**Test draft** runs the saved graph without publishing it. **Activate** a published workflow to enable webhook, schedule, and Gmail triggers. HTTP and Gmail nodes call real external services, even in test runs.

To call an active webhook:

```sh
curl -X POST http://localhost:8080/api/hooks/WORKFLOW_ID \
  -H 'Content-Type: application/json' \
  -H 'X-Webhook-Secret: YOUR_NODE_SECRET' \
  -d '{"hello":"world"}'
```

### Stop, logs, and reset

```sh
make logs                 # follow logs from all services
make down                 # stop containers; the database volume is kept
docker compose down -v    # stop and delete the local database volume
```

Health checks: `http://localhost:8080/healthz` (web) and `http://localhost:8080/api/health` (API).

### Configuration

Settings live in `.env`. Run `make up` again after changing them.

| Variable | Default | Notes |
| --- | --- | --- |
| `WEB_PORT` | `8080` | If changed, also update `ALLOWED_ORIGIN` and `GOOGLE_REDIRECT_URI` |
| `POSTGRES_PORT` | `55432` | Host port for local database tools |
| `POSTGRES_PASSWORD` | `n9n_local_dev` | Change it in `DATABASE_URL` too, before the volume is first created |
| `COOKIE_SECURE` | `false` | Must stay `false` for plain-HTTP localhost logins |
| `RUNNER_CONCURRENCY` | `4` | Runs executed in parallel (1–32) |
| `ALLOW_PRIVATE_HTTP` | `false` | Set to `true` to let HTTP nodes call `localhost` or LAN services |
| `RUN_RETENTION_DAYS` | `30` | Finished runs are deleted after this many days |

Connect to the local database with any PostgreSQL client:

```sh
psql 'postgres://n9n:n9n_local_dev@127.0.0.1:55432/n9n'
```

### Optional: Gmail

Gmail triggers and send-email nodes need a Google OAuth client:

1. In Google Cloud, enable the Gmail API.
2. Configure the OAuth consent screen and add yourself as a test user.
3. Create an OAuth **web** client with the redirect URI `http://localhost:8080/api/oauth/google/callback`.
4. Set `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` in `.env`, then run `make up`.
5. In the app, open **Credentials → Connect Gmail**, then assign the credential to each email node.

### Deploy to Cloudflare

`cloudflare/` deploys n9n on the Cloudflare Workers free plan: the Flutter app as static assets and the API as a TypeScript Worker (a port of `backend/`, kept in sync with it) on Supabase through Hyperdrive. The Rust runner is not hosted there yet. See [cloudflare/README.md](cloudflare/README.md).

### Mobile app (Android / iOS)

With the stack running, and Node.js 22 or newer installed:

```sh
cd mobile
npm ci
npm start
```

Press `a` for the Android emulator or `i` for the iOS simulator, or scan the QR code with Expo Go. In **Settings → Server address**, enter:

- Android emulator: `http://10.0.2.2:8080`
- iOS simulator: `http://localhost:8080`
- Physical device: `http://<your computer's LAN IP>:8080`

### Run services outside Docker (development)

Start only the database, then run each service from source in its own terminal. Use the `RUNNER_TOKEN` and `ENCRYPTION_KEY` values from `.env`.

```sh
docker compose up -d --wait postgres
```

Backend (Go 1.26):

```sh
cd backend
DATABASE_URL='postgres://n9n:n9n_local_dev@127.0.0.1:55432/n9n?sslmode=disable' \
ENCRYPTION_KEY=... RUNNER_TOKEN=... go run .
```

Runner (Rust stable):

```sh
cd runner
BACKEND_URL=http://localhost:8080 RUNNER_TOKEN=... cargo run
```

Web (Flutter 3.35.7):

```sh
cd web
flutter pub get
flutter run -d chrome --web-port 3000
```

The web app calls `/api` on its own origin, so it needs a proxy that forwards `/api` to the backend. You can also use the Docker `web` service for the UI.

### Tests

```sh
make test            # Go, Rust, and Flutter tests (needs the local database running)
make test-backend    # Go tests with the race detector against the Compose database
make analyze         # flutter analyze, go vet, cargo fmt --check
```

With the stack running:

```sh
python3 tests/e2e_smoke.py         # end-to-end API smoke test
python3 tests/restart_smoke.py     # a queued run survives a backend restart
python3 tests/mobile_api_smoke.py  # mobile bearer-session flow
python3 tests/replay_smoke.py      # time-travel replay from a middle node
```

Browser tests (Playwright):

```sh
cd tests/browser
npm install
npx playwright install chromium
npm test
```

Mobile type checks and tests: `cd mobile && npm run typecheck && npm test`.
