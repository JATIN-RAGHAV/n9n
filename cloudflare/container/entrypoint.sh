#!/bin/bash
# Runs the Go API and the Rust runner in one container. If either process
# exits, the other is stopped and the container exits so the Durable Object
# starts a fresh instance on the next request.
set -u
export BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:8080}"

/usr/local/bin/n9n-backend &
backend=$!

# Wait until the API listens (it first connects to PostgreSQL and migrates).
for _ in $(seq 1 120); do
  if (exec 3<>/dev/tcp/127.0.0.1/8080) 2>/dev/null; then break; fi
  if ! kill -0 "$backend" 2>/dev/null; then
    echo "n9n-backend exited before listening" >&2
    wait "$backend"
    exit 1
  fi
  sleep 1
done

/usr/local/bin/n9n-runner &
runner=$!

stop() { kill -TERM "$runner" "$backend" 2>/dev/null; }
trap stop TERM INT

wait -n "$backend" "$runner"
status=$?
stop
wait "$runner" 2>/dev/null
wait "$backend" 2>/dev/null
exit "$status"
