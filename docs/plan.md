# n9n delivery plan

## Implemented

- Go API: account registration and sessions, workflow drafts and versions, graph validation, credential encryption, durable run and step records, lease based internal job protocol, webhook enqueue, and node catalog.
- Rust worker: queued run execution, graph mappings, conditions, HTTP requests, Gmail email, trigger polling, retries and cancellation checks.
- Flutter Web: auth screens, workflow list, searchable node palette, drag canvas with pan and zoom, port connections and deletion, undo and redo, node settings, mapping suggestions, draft save, publish, activate, manual run, run inspection, and credential management.
- Compose: a public nginx/Flutter service, private Go backend with SQLite volume, private Rust runner, generated secrets, health checks for all three services, and bounded resources.
- Google OAuth connection: configured client credentials, one use state callback, Gmail scope authorization, and credential creation. Manual refresh token entry remains available. The token exchange is tested against a mock server; live Google authorization depends on external Cloud setup.

## Verified locally

- Go API: all 13 tests passed with `go test -race ./...`; `go vet ./...` passed after the final edits.
- Rust worker: 13 tests, `cargo fmt --check`, and strict Clippy passed.
- Flutter Web: five tests, `flutter analyze`, and `flutter build web --wasm --release` passed. A browser smoke covered registration, node creation/connection, publish, run, auto-refreshing inspection, mobile layout, and deep-link reload. `main.dart.wasm` loaded successfully in the tested browser.
- The public API smoke exercised branching, mapping, immutable versions, webhook authentication and deduplication, schedule activation, private HTTP blocking, cross-user isolation, and cancellation.
- The final OpenAPI document passed Redocly validation. It has 33 nonblocking warnings for unspecified license metadata and routes using a default error response instead of an explicit 4xx response.
- All three Compose images built and all three services became healthy. The final public API smoke passed.
- A restart smoke confirmed that a queued version 1 run survived a backend restart. After version 2 was published, the worker executed the queued run against version 1 and a new run against version 2.
- Persistent browser automation passed in 16 seconds. It exercised account and workflow creation, node placement and port connection, save, publish, successful run and step inspection, reload, mobile navigation and credentials, Wasm and MJS MIME responses, and confirmed zero page errors.

Flutter's release build includes a JavaScript fallback on browsers without WasmGC support.

## Deferred after first release

- Native calendar, database, and chat app connectors beyond the initial HTTP and Gmail nodes.
- Multi user teams, role based permissions, and shared workflow ownership.
- Arbitrary loops and multi input joins; the current graph is a directed acyclic graph with one incoming edge per action.
- Distributed database and worker scaling beyond the single SQLite backed deployment.
- Custom OAuth client setup through the UI. Operators configure the Google web client in `.env`.
