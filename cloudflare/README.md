# n9n on Cloudflare (Workers + Containers)

_Last updated: 2026-10-23 13:45 IST · Claude Fable 5.1 (claude-fable-5-1)_

This folder deploys the whole n9n stack to Cloudflare with Wrangler:

| Piece | Where it runs | Notes |
| --- | --- | --- |
| Flutter web app | Workers static assets (edge cache) | `web/build/web`, SPA fallback, COOP/COEP headers from `_headers` |
| Worker (`src/index.ts`) | Every Cloudflare location | Serves `/healthz`, blocks `/internal*`, proxies `/api/*` to the container |
| Go API + Rust runner | One Cloudflare Container (`Dockerfile`) | Both processes in one `lite` instance, supervised by `entrypoint.sh` |
| PostgreSQL | **Not on Cloudflare** | Any reachable PostgreSQL 16+ (Supabase, Neon, RDS, your own). Set `DATABASE_URL`. |

Containers need the **Workers Paid plan** (USD 5/month, includes 25 GiB-hours, 375 vCPU-minutes and 200 GB-hours of container usage). A `lite` instance kept warm all month uses about 180 GiB-hours, so expect a few dollars on top of the plan.

## Deploy

Requirements: Node 20+, Docker running (Wrangler builds the image locally), Flutter (same version as `web/` expects).

All Wrangler commands go through `./wrangler.sh`, which keeps this project's Cloudflare login in its own config home (`~/.config/n9n-cf`) so it never touches the login other projects use on this machine. Log in once with `npm run login`.

```sh
cd cloudflare
npm install
./wrangler.sh secret put DATABASE_URL      # postgres://user:pass@host:5432/db?sslmode=require
./wrangler.sh secret put ENCRYPTION_KEY    # openssl rand -hex 32 ; never rotate after first use
./wrangler.sh secret put RUNNER_TOKEN      # openssl rand -hex 32
npm run deploy                            # builds web, builds the image, pushes everything
```

`ALLOWED_ORIGIN` and `GOOGLE_REDIRECT_URI` in the `vars` block already point at `https://n9n.walrus.codes`; change them if the hostname changes. Gmail also needs `GOOGLE_CLIENT_ID` (var) and `GOOGLE_CLIENT_SECRET` (`wrangler secret put`).

The Worker is bound to the custom domain `n9n.walrus.codes` (`routes` in `wrangler.jsonc`); Wrangler creates the DNS record and certificate on deploy. Check: `https://n9n.walrus.codes/healthz` (Worker) and `/api/health` (container). The first API call after a deploy takes 10–30 s while the container boots and the API runs migrations.

## How requests flow

1. A browser loads `/`: static assets, served from the edge, no Worker, no container.
2. The app calls `/api/...`: the Worker adds `X-Forwarded-Host`, `X-Forwarded-Proto` and `X-Real-IP` (from `CF-Connecting-IP`) and forwards to the container's port 8080 via the `N9N` Durable Object binding. The backend's origin check and per-IP login throttle rely on those headers.
3. Inside the container `entrypoint.sh` starts `n9n-backend`, waits for port 8080, then starts `n9n-runner` with `BACKEND_URL=http://127.0.0.1:8080`. If either process exits the container exits and is recreated on the next request.
4. A cron trigger (`*/5 * * * *`) calls `/api/health` so the container never reaches its 20 minute `sleepAfter`; schedule and Gmail triggers therefore keep polling. Remove the cron to let it scale to zero when nobody uses it (triggers then pause too).

## Local test

```sh
docker compose up -d --wait postgres          # from the repo root
cp cloudflare/.dev.vars.example cloudflare/.dev.vars   # paste ENCRYPTION_KEY and RUNNER_TOKEN from .env
cd cloudflare && npm run build:web && npm run dev
python3 ../tests/e2e_smoke.py --base http://localhost:8787
```

## Known limits

- One container instance (`max_instances: 1`). Multiple instances would work (the backend uses `SKIP LOCKED` claims) but each would run its own runner; raise it only with a load-balancing strategy in `src/index.ts`.
- Cloudflare can stop a container at any time (host maintenance, deploys). A run interrupted mid side-effect becomes `uncertain`, as documented in `backend/README.md`.
- The image must be `linux/amd64`; the Dockerfile cross-compiles from Apple Silicon, so no emulation is needed for the build.
- Egress from the container to PostgreSQL and HTTP nodes is billed at Cloudflare's container egress rates.
