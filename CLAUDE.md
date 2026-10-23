# n9n — agent rules

_Last updated: 2026-10-23 17:24 IST · Claude Opus 5.5 (claude-opus-5-5)_

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
| `cloudflare/` | Production deployment on the Workers free plan; `cloudflare/src/api/` is the TypeScript port of `backend/`. See `cloudflare/README.md` |
| `docs/`, `contracts/`, `tests/` | Architecture notes, API contract, smoke and browser tests |

## Two API implementations, kept in sync

`backend/` (Go) and `cloudflare/src/api/` (TypeScript Worker) are one API in two languages.
Owner's words (2026-10-23): "don't remove go code from the repo. just make sure that you will have
the go and the TS code both there. and on subsequent changes they should be make sure that they are
in sync."

- Every change to API behaviour lands in both, in the same pull request: routes, validation,
  error messages, SQL, response shapes, and data formats (IDs, timestamps, password hashes,
  credential encryption).
- A new schema migration is appended to both lists verbatim; `python3 scripts/check_api_sync.py`
  fails otherwise.
- Before handing back an API change, run the smoke tests against both servers and
  `tests/api_parity.py`. When a difference is intended, list it in the table in `cloudflare/README.md`.
- Enforcement: Automated for the schema, the smoke tests and the parity cases (CI job
  `worker-api`); instruction only for everything the parity cases do not exercise.

## Working rules

- Commit each landed unit as you go, conventional messages, no AI co-author trailer.
  Push only when Walrus asks.
- Secrets live in `.env` (local) and `cloudflare/.secrets.production` (deploy), both
  gitignored; never print or commit them.
- Run `make test` / `make analyze` before handing back changes to backend, runner or web.
