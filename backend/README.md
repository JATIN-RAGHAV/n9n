# Go API

The API stores users, published workflow snapshots, encrypted credentials, trigger checkpoints, and run/step history in SQLite. Run it with:

```sh
export DATABASE_PATH=./n9n.db
export ENCRYPTION_KEY="$(openssl rand -hex 32)"
export RUNNER_TOKEN="$(openssl rand -hex 32)"
go run .
```

The server listens on `:8080` unless `ADDR` is set. Set `COOKIE_SECURE=true` behind HTTPS and `ALLOWED_ORIGIN` to the public web origin if the reverse proxy changes the Host header. Public routes are under `/api`; runner routes are under `/internal` and require the bearer token. Keep both secrets stable across restarts. Changing `ENCRYPTION_KEY` prevents decryption of stored credentials.

The database uses WAL mode. Back up the database with SQLite's online backup API or while the service is stopped. The published version is a graph snapshot, so a draft edit does not change running or queued jobs. Each job lease lasts 60 seconds and is extended by heartbeat. Expired leases on workflows containing email or potentially unsafe HTTP actions become `uncertain`; review these runs before manually retrying to avoid duplicate effects.

Run tests with `go test ./...`.
