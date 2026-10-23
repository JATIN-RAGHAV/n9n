# n9n — agent rules

_Last updated: 2026-10-23 14:26 IST · Claude Fable 5.1 (claude-fable-5-1)_

## This is a personal project

n9n belongs to Walrus personally. It has nothing to do with ProCMO or any client.
Owner's words (2026-10-23): "make it so that non of this goes to the pcmo brain, this is
private stuff and not related to the company."

- Never write anything about this repo, its deployment, its accounts or its sessions into
  `pro-cmo-brain` (`~/Documents/Programming/work/pcmo/pro-cmo-brain`).
- Never use ProCMO accounts or connectors for it: Cloudflare deploys go through
  `cloudflare/wrangler.sh` (personal login in `~/.config/n9n-cf`), the database is the
  personal Supabase, never the PCMO projects.
- Session notes for this repo go to the Library of Alexandria (personal), handled by the
  SessionEnd hook; the pro-cmo-brain hook ignores this directory because it is outside
  `~/Documents/Programming/work/pcmo` (enforcement: Automated, by path).

## Layout

| Path | Content |
| --- | --- |
| `backend/` | Go API (PostgreSQL) |
| `runner/` | Rust worker that executes runs |
| `web/` | Flutter web editor |
| `mobile/` | React Native (Expo) app |
| `cloudflare/` | Production deployment: Worker + Container via Wrangler, see `cloudflare/README.md` |
| `docs/`, `contracts/`, `tests/` | Architecture notes, API contract, smoke and browser tests |

## Working rules

- Commit each landed unit as you go, conventional messages, no AI co-author trailer.
  Push only when Walrus asks.
- Secrets live in `.env` (local) and `cloudflare/.secrets.production` (deploy), both
  gitignored; never print or commit them.
- Run `make test` / `make analyze` before handing back changes to backend, runner or web.
