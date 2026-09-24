# Go API

The API stores users, published workflow snapshots, encrypted credentials, trigger checkpoints, and run/step history in PostgreSQL 16 or newer. Run it with:

```sh
export DATABASE_URL='postgres://n9n:password@localhost:5432/n9n?sslmode=disable'
export ENCRYPTION_KEY="$(openssl rand -hex 32)"
export RUNNER_TOKEN="$(openssl rand -hex 32)"
go run .
```

The server listens on `:8080` unless `ADDR` is set. Set `COOKIE_SECURE=true` behind HTTPS and `ALLOWED_ORIGIN` to the public web origin if the reverse proxy changes the Host header. Public routes are under `/api`; runner routes are under `/internal` and require the bearer token. Keep both secrets stable across restarts. Changing `ENCRYPTION_KEY` prevents decryption of stored credentials.

Startup waits up to 60 seconds for PostgreSQL, then applies versioned migrations under a PostgreSQL advisory lock. Back up with `pg_dump` or your managed database snapshot service. The published version is a graph snapshot, so a draft edit does not change running or queued jobs. Each job lease lasts 60 seconds and is extended by heartbeat. Expired leases on workflows containing email or potentially unsafe HTTP actions become `uncertain`; review these runs before manually retrying to avoid duplicate effects. Multiple API and runner instances can safely share the database: job claims use row locks with `SKIP LOCKED`.

Run tests against a disposable PostgreSQL database with `TEST_DATABASE_URL='postgres://...?...' go test ./...`. Tests create and drop isolated schemas. The account must have `CREATE` privilege on the database.
