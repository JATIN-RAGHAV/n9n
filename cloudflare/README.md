# n9n on Cloudflare (Workers free plan)

_Last updated: 2026-10-23 17:24 IST · Claude Opus 5.5 (claude-opus-5-5)_

This folder deploys n9n to `https://n9n.walrus.codes` as one Cloudflare Worker on the **free plan**:

| Piece | Where it runs | Notes |
| --- | --- | --- |
| Flutter web app | Workers static assets | `web/build/web`, SPA fallback, COOP/COEP headers from `_headers` |
| API | The Worker (`src/api/`) | TypeScript port of `backend/`, same routes, SQL, schema and data formats |
| PostgreSQL | Supabase, reached through Hyperdrive | Free plan: 100,000 queries a day |
| Rust runner | **Not on Cloudflare yet** | Connects to `https://n9n.walrus.codes/internal/*` with `RUNNER_TOKEN` from any host |

Until a runner is connected, workflows can be built, saved and published, webhooks are accepted and runs are queued, but no run executes.

## The two API implementations

`backend/` (Go) and `cloudflare/src/api/` (TypeScript) are the same API. Either can serve the same database, and both must change together. Three checks hold them together, all run by the `worker-api` CI job:

- `scripts/check_api_sync.py` fails if the migration lists differ by one statement.
- `tests/e2e_smoke.py`, `mobile_api_smoke.py` and `replay_smoke.py` run against the Worker with the Rust runner attached.
- `tests/api_parity.py` runs both servers on one database: accounts and sessions cross over, catalog, workflows and runs read back identically, and 14 bad requests fail with the same status and message.

Formats both sides share: 32-hex IDs, `2006-01-02T15:04:05.000000000Z` timestamps, SHA-256 session hashes, AES-256-GCM credentials (`base64(nonce||ciphertext||tag)`), and PBKDF2-SHA256 password hashes (`pbkdf2_sha256$<iterations>$<salt>$<hash>`, bcrypt still verifies).

Known differences, by design:

| Area | Go | Worker |
| --- | --- | --- |
| Schema migration | At startup | On the first request in each isolate, under the same advisory lock |
| Login throttle (20 per IP per minute) | In-process | Rate limiting binding, counted per Cloudflare location |
| Client IP | `X-Real-IP` from nginx | `CF-Connecting-IP` |
| `/internal/*` | Blocked at nginx, private network only | Public, protected only by the `RUNNER_TOKEN` bearer token |
| Hourly cleanup | Goroutine | Cron Trigger `17 * * * *` |
| Timestamps | Nanoseconds | Milliseconds plus a per-isolate counter in the last six digits |

## Deploy

Requirements: Node 20+, Flutter, and `npm run login` done once with the personal Cloudflare account. All Wrangler commands go through `./wrangler.sh`, which keeps this project's login in `~/.config/n9n-cf`.

1. Create the Hyperdrive configuration from the Supabase **session pooler** URL, with caching off. The API must never read a cached session or run row:
   ```sh
   ./wrangler.sh hyperdrive create n9n-db --caching-disabled --connection-string="$DATABASE_URL"
   ```
   Put the printed ID into `hyperdrive[0].id` in `wrangler.jsonc`.
2. Set the two secrets (64 hex characters each; never change `ENCRYPTION_KEY` after the first deploy):
   ```sh
   ./wrangler.sh secret put ENCRYPTION_KEY
   ./wrangler.sh secret put RUNNER_TOKEN
   ```
3. Deploy: `npm run deploy`. It builds the web app and publishes the Worker on `n9n.walrus.codes`.

Check `https://n9n.walrus.codes/healthz` (Worker) and `/api/health` (API).

## Continuous deployment

`.github/workflows/deploy-cloudflare.yml` deploys when `CI` succeeds on `main`, or by hand from the Actions tab. Repository secrets: `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`, `ENCRYPTION_KEY`, `RUNNER_TOKEN`, optionally `GOOGLE_CLIENT_SECRET`. The database URL is not a GitHub secret; it lives in the Hyperdrive configuration.

## Local development

```sh
docker compose up -d --wait postgres
cp cloudflare/.dev.vars.example cloudflare/.dev.vars    # fill ENCRYPTION_KEY and RUNNER_TOKEN
cd cloudflare && npm run build:web
CLOUDFLARE_HYPERDRIVE_LOCAL_CONNECTION_STRING_HYPERDRIVE='postgres://n9n:n9n_local_dev@127.0.0.1:55432/n9n?sslmode=disable' npm run dev
# in another terminal, attach a runner to the Worker:
BACKEND_URL=http://localhost:8787 RUNNER_TOKEN=... runner/target/release/n9n-runner
```

## Free-plan limits to watch

- **CPU: 10 ms per request.** Registration and login run 100,000 PBKDF2 iterations, which can exceed it. Cloudflare tolerates occasional overruns. If logins fail with "exceeded CPU", lower `PASSWORD_PBKDF2_ITERATIONS` (minimum 10,000) or move to Workers Paid. Not yet measured on the live Worker.
- **100,000 requests and 100,000 database queries a day.** A runner polling every second uses about 86,400 claim requests a day on its own; set `RUNNER_POLL_MS` to 5000 or more when the runner is attached.
- **5 Cron Triggers per account**; this Worker uses one.

## Paid-plan alternative

`container/` holds the image that runs the Go API and the Rust runner together in a Cloudflare Container (Workers Paid only). It is kept for a future runner host; the free deployment does not use it.
