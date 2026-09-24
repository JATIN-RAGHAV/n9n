# PostgreSQL

The backend requires `DATABASE_URL`. Local Compose supplies PostgreSQL 16; production uses the same backend with an external PostgreSQL URL. TLS is controlled by the URL's `sslmode` and certificate options. Local loopback access is at port 55432, configurable with `POSTGRES_PORT`.

Go integration tests need a real database and create/drop isolated schemas:

```sh
docker compose up -d --wait postgres
cd backend
TEST_DATABASE_URL='postgres://n9n:n9n_local_dev@127.0.0.1:55432/n9n?sslmode=disable' go test -race ./...
```

Use a dedicated test database in production-like environments; the test user must have schema creation privileges.

## Import an existing SQLite installation

Keep the old SQLite volume and encryption key until the migration is verified. Stop the legacy backend and runner before copying the database so the snapshot is consistent. Back up the database using SQLite's backup API, or copy it after a clean shutdown (including WAL files if present).

Start the new PostgreSQL backend once to initialize the schema, then stop the backend and runner while importing. The destination application tables must be empty. Export with restrictive permissions:

```sh
umask 077
python3 scripts/export_sqlite.py /secure/backup/n9n.db > /secure/backup/import.sql
# Local Compose database:
docker compose exec -T postgres psql -U n9n -d n9n -v ON_ERROR_STOP=1 < /secure/backup/import.sql
# Or an external database:
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f /secure/backup/import.sql
```

The import is transactional and refuses to write into nonempty application tables. It preserves IDs, accounts, sessions, encrypted credentials, versions, runs, and checkpoints, and advances the step ID sequence. Keep the same `ENCRYPTION_KEY`. Restart the application services, verify login/workflows/run history, and retain the original backup until satisfied. Exported SQL contains private data and must be protected like the source database.
