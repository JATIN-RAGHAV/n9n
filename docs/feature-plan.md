# n9n platform v2 — implementation plan

Branch: `feature/platform-v2` (created from `main` at `ad9b853`). Every commit below lands on this branch only. `main` is merged into it, never the reverse, until the whole plan passes its final gate.

This plan adds nine features:

| # | Feature | Phase |
| --- | --- | --- |
| F2 | Time-travel debugging and replay from any step | 2 |
| F9 | Cost and latency heatmap on the canvas | 3 |
| F3 | Human-in-the-loop Approval node | 4 |
| F7 | Workflows as an API (Respond node) | 5 |
| F4 | Sandboxed WebAssembly Code node | 6 |
| F8 | Shadow runs for safe publishing | 7 |
| F1 | Build a workflow from a sentence (AI builder) | 8 |
| F5 | Explain and propose fixes for failed or uncertain runs | 8 |
| F6 | Live collaborative canvas | 9 |

The order follows dependencies. Replay, approvals, Respond and shadow runs all need the run and step model changes from Phase 1. The two AI features are better once replay exists, because the builder and the explainer can test their proposals against real run data. The collaborative canvas changes how drafts are saved, so it comes last to avoid rebasing every earlier editor change onto it.

---

## Ground rules

### Commit style

Commits follow the repo's existing convention: `type(scope): summary`, for example `feat(runner): ...`, `fix(backend): ...`, `test(e2e): ...`, `docs: ...`. Each commit:

- builds and passes the test gate for every component it touches;
- contains one logical change (schema + handler + test for that change travel together);
- never mixes a refactor with a behaviour change.

### Test gate (run before every commit)

| Component | Command | Notes |
| --- | --- | --- |
| Backend | `cd backend && TEST_DATABASE_URL=... go test -race ./... && go vet ./...` | Uses a disposable PostgreSQL database. |
| Runner | `cd runner && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test` | |
| Web | `cd web && flutter analyze && flutter test` | Flutter must be installed locally (it is not today). |
| Mobile | `cd mobile && npm run typecheck && npm test` | |
| Contract | Redocly lint on `contracts/openapi.yaml` | Only when the API changes. |

At the end of every phase, also run the full-stack gate: `make up`, then `python3 tests/e2e_smoke.py`, `python3 tests/restart_smoke.py`, `python3 tests/mobile_api_smoke.py`, and the Playwright suite in `tests/browser`. Each phase adds its own smoke script under `tests/` and adds it to this gate.

### Environment blockers to clear first

1. **Docker Desktop's engine does not start.** The full-stack gate and the browser tests need Compose. Until it is fixed, the backend and runner can run from source against a local PostgreSQL (as in the last QA pass), but the Flutter web build and nginx cannot be tested.
2. **Flutter is not installed.** Install Flutter 3.35.7 so the web gate can run.
3. **An Anthropic API key** is needed for Phase 8's live tests. Unit tests use a mocked HTTP server and do not need one.

### Cross-cutting conventions

- **Schema changes:** each is a new numbered migration (`migration5`, `migration6`, ...) in `backend/main.go`, applied under the existing advisory lock. Migrations only add columns or tables with safe defaults, so old runners keep working during a rolling upgrade.
- **Node types:** new node types are added in four places in one commit: `backend/catalog.go` (catalog + schemas), `backend/workflows.go` (publish validation), `runner/src/lib.rs` `run_node` (execution), and `contracts/nodes.md`. The editor palettes (`web/lib/editor.dart`, `mobile/src/editor.tsx`) follow in a separate UI commit. The catalog version goes from `1` to `2` in Phase 1 and stays there; clients must ignore unknown node types they cannot render.
- **Run kinds:** Phase 1 replaces the `is_test` boolean's role with a `kind` column (`normal`, `test`, `replay`, `shadow`). `is_test` stays and is kept in sync for one release so older clients still work.
- **Feature flags:** every AI feature is off unless `ANTHROPIC_API_KEY` is set. The Code node is off unless `ENABLE_CODE_NODE=true`. The UI hides disabled features using a new `GET /api/features` endpoint.

---

## Phase 0 — Baseline

Goal: a green starting point, plus the one confirmed bug that would corrupt replay data.

| # | Commit | Contents |
| --- | --- | --- |
| 0.1 | `docs: add platform v2 feature plan` | This file. |
| 0.2 | `fix(runner): record failed step when output exceeds 256 KiB` | In `execute_inner` (`runner/src/lib.rs:658`), write a `failed` (or `uncertain`) step row before returning the size error. Add a runner test with an oversized `set_fields` output that asserts the final step row is `failed`. Replay and the heatmap both read step rows, so a step stuck in `running` would break them. |
| 0.3 | `fix(runner): support IPv6 literal hosts in HTTP node` | Use `url.host()` and pass the bare address to `lookup_host`, so `http://[::1]/` reaches the private-address check and is blocked with the correct error. Tests for `[::1]` (blocked) and a public IPv6 literal (resolved). |

The other QA findings (credential deletion, `X-Real-IP` trust, timezone type check) are not prerequisites. They can be fixed on `main` separately and merged in.

**Gate:** all component tests green; runner test count goes up by 3.

---

## Phase 1 — Foundations

Goal: the run and step model changes that later phases share.

### Schema (`migration5`)

- `runs.kind TEXT NOT NULL DEFAULT 'normal'` with a check constraint for `normal | test | replay | shadow`. Backfill `kind='test'` where `is_test`. Update the snapshot trigger from migration 4 so `replay` and `shadow` runs, like test runs, carry a `graph_snapshot`.
- `runs.parent_run_id TEXT NULL REFERENCES runs(id) ON DELETE SET NULL`: links a replay or shadow run to the run it came from.
- `runs.resume_from_node TEXT NULL`: the node a replay starts at.
- `steps.started_at TEXT NULL`, `steps.finished_at TEXT NULL`, `steps.duration_ms INTEGER NULL`: filled in by the runner. Needed for the heatmap and useful in the run inspector.
- `step_attempts(id, run_id, node_id, attempt, status, error, duration_ms, created_at)`: one row per internal HTTP retry, so retries are visible (a QA finding).

### Changes

| # | Commit | Contents |
| --- | --- | --- |
| 1.1 | `feat(backend): add run kind, parent run and step timing columns` | `migration5`, `Run` and `Step` struct fields, JSON output, `getRun`/`ownedRun`/run detail updated. Tests: migration applies on an empty database and on one with existing test runs (backfill check); the snapshot trigger rejects a `replay` run with no snapshot. |
| 1.2 | `feat(runner): report step start, finish and duration` | The runner sends `started_at`/`duration_ms` with each step post; `internal.go` stores them. Older runners that omit them still work (columns are nullable). Test: step rows have durations after a run. |
| 1.3 | `feat(runner): record individual HTTP retry attempts` | `http_node` reports each attempt through a new `POST /internal/jobs/:id/attempts`; stored in `step_attempts`. Run detail returns them nested under each step. Tests: a GET to a mocked 500 records 3 attempts; a POST without an idempotency key records 1. |
| 1.4 | `feat(backend): add feature discovery endpoint and catalog v2` | `GET /api/features` returns which optional features are enabled. `GET /api/nodes` returns `version: 2` (no new nodes yet). Tests for both. |
| 1.5 | `feat(web): show step timing and retry attempts in run inspector` | Duration badge per step and an expandable attempts list in `RunDetailScreen`. Widget test. |
| 1.6 | `feat(mobile): show step timing and retry attempts` | Same in `RunDetailScreen` (`mobile/src/screens.tsx`). |
| 1.7 | `docs(contracts): document run kinds, timing and attempts` | `openapi.yaml`, `nodes.md` (catalog v2 note). |

**Gate:** full-stack gate; `restart_smoke.py` still passes (old queued runs have `kind='normal'`).

---

## Phase 2 — F2: Time-travel debugging and replay

Goal: scrub through a run's steps, re-run from any node with edited input, and replay a run against the current draft.

### Design

The runner already resumes a run from its existing step rows: `execute_inner` loads `job.steps` and skips nodes whose latest step `succeeded`, reusing their output. Replay reuses that path instead of adding a second execution engine:

1. The backend creates a new run with `kind='replay'`, `parent_run_id` set, `resume_from_node` set, and a `graph_snapshot` (the original run's graph, or the current draft for "replay against draft").
2. In the same transaction it copies the parent's `succeeded` step rows for every node that is **upstream of** `resume_from_node` into the new run. Those nodes are then treated as done.
3. If the user edited the input to `resume_from_node`, the backend overwrites the copied output of that node's parent so the runner renders the edited input.
4. The runner claims and executes the run normally. No runner changes are needed for basic replay.

Safety: a replay that would run an HTTP or Gmail node shows a confirmation listing those nodes, the same way Test draft warns today.

### API

- `POST /api/runs/:id/replay` with `{from_node, input_override?, graph: "original" | "draft"}` returns the new run.
- `GET /api/runs/:id` gains `parent_run_id`, `kind` and `resume_from_node`, plus a `children` list of replay runs.
- Replay is rejected with 400 if `from_node` is not in the chosen graph, or if the graph is `draft` and fails validation.

### Changes

| # | Commit | Contents |
| --- | --- | --- |
| 2.1 | `feat(backend): replay a run from a chosen node` | Handler, upstream-step copying, input override, validation. Tests: replay from the middle of a 5-node chain re-runs only the downstream nodes; replay from the trigger re-runs all; replay on another user's run returns 404; replay with an invalid `from_node` returns 400; replay of a failed run produces a new succeeded run after an input fix. |
| 2.2 | `feat(backend): replay a run against the current draft` | `graph: "draft"` validates the draft (reusing the Test draft code) and snapshots it. Tests: a replay against an edited draft uses the new node config; the workflow stays unpublished. |
| 2.3 | `test(runner): cover resume from copied steps` | Runner integration test that a job with pre-seeded succeeded steps skips them and uses their outputs in mappings, including `{{nodes.ID.x}}` references to seeded nodes. |
| 2.4 | `feat(web): add run timeline scrubber` | Horizontal timeline in the run inspector. Selecting a step highlights its node on a read-only canvas and shows the input and output at that moment. Widget test. |
| 2.5 | `feat(web): replay from step with editable input` | "Re-run from here" on each step opens a JSON editor with the step's input, shows the side-effect warning, and opens the new run. "Replay against current draft" button on failed runs. Widget test plus a Playwright spec (`tests/browser/replay.spec.js`). |
| 2.6 | `feat(mobile): replay from step` | Same actions in the mobile run inspector. |
| 2.7 | `test(e2e): add replay smoke test` | `tests/replay_smoke.py`: run a branching workflow, replay from a middle node with changed input so it takes the other branch, verify the parent link and the copied steps. |
| 2.8 | `docs(contracts): document replay endpoints` | |

**Gate:** full-stack gate plus `replay_smoke.py`.

---

## Phase 3 — F9: Cost and latency heatmap

Goal: colour each canvas node by its p95 duration, failure rate or call volume.

### Design

- `GET /api/workflows/:id/stats?window=24h|7d|30d&version=N` returns per node: run count, success count, failure count, skipped count, p50 and p95 `duration_ms`, and retry count (from `step_attempts`). Computed in SQL with `percentile_cont` over the final step row per node per run, limited to the workflow's owner.
- Add an index `steps(run_id, node_id)` if the query plan needs it (checked with `EXPLAIN` in a test on 10k seeded steps).
- Stats are computed on demand; if they are slow on large data, a later commit can add a materialized summary refreshed by the existing hourly cleanup job.

### Changes

| # | Commit | Contents |
| --- | --- | --- |
| 3.1 | `feat(backend): add per-node workflow stats endpoint` | Handler, SQL, index if needed. Tests with seeded runs: correct counts and percentiles; other users get 404; only the chosen version is counted. |
| 3.2 | `feat(web): add heatmap overlay to the canvas` | Toggle for "latency / failures / volume" and a time window. Nodes are tinted along a colour scale with a legend; the numbers show in the node's hover card. Colour is never the only signal: values also appear as text, as the web README requires for errors. Widget test. |
| 3.3 | `feat(mobile): show node stats in the editor` | A stats row on each node in the mobile editor list. |
| 3.4 | `docs(contracts): document workflow stats` | |

**Gate:** full-stack gate.

---

## Phase 4 — F3: Human-in-the-loop Approval node

Goal: pause a run until a person approves or rejects it, from an email link or the mobile app, then continue down a `true` or `false` port.

### Design

- **New node `approval`.** Config: `message` (mapping template), `timeout_seconds` (60 to 30 days), `on_timeout` (`reject` or `fail`), optional `notify_email` (uses a Gmail credential), `notify_push` (bool). Ports `true` and `false`, like `condition`.
- **New run status `waiting`.** When the runner reaches an approval node it posts `POST /internal/jobs/:id/wait` with the node ID. The backend records an `approvals` row, sets the run to `waiting`, and clears its lease so the runner slot is freed. A waiting run holds no runner resources and does not expire through lease recovery.
- **`approvals` table:** `id, run_id, node_id, token_hash, message, status (pending|approved|rejected|expired), decided_by, decided_at, expires_at`. The token in links is random; only its hash is stored, like sessions.
- **Deciding:** `POST /api/approvals/:id/decide {approved}` (signed-in owner) or `GET/POST /api/approvals/decide?token=...` (email link: GET shows a confirmation page, POST decides, so mail scanners that prefetch links cannot approve). Deciding writes the approval node's step (`branch` = `true` or `false`) and sets the run back to `queued`. The runner then resumes it through the existing resume path; the approval step is already `succeeded`, so execution continues down the chosen port.
- **Timeouts:** the hourly cleanup job becomes a one-minute ticker for approvals only, which expires overdue approvals and resolves them according to `on_timeout`.
- **Push notifications:** the mobile app registers an Expo push token (`expo-notifications`) through `POST /api/devices`. The backend sends a push through the Expo Push API when an approval is created. The notification opens the approval screen.
- **Unsafe-node rule:** approvals are not side effects, so a lost lease while waiting is harmless. The `uncertain` logic is unchanged.

### Changes

| # | Commit | Contents |
| --- | --- | --- |
| 4.1 | `feat(backend): add waiting run status and approvals table` | `migration6` (status check, `approvals`, `devices`), and `waiting` excluded from lease recovery and from `cancel` rules except explicit cancellation. Tests: a waiting run is never claimed; cancelling a waiting run expires its approval. |
| 4.2 | `feat(backend): add approval node to catalog and validation` | Catalog entry, config validation, `true`/`false` ports. Publish validation tests. |
| 4.3 | `feat(runner): pause runs at approval nodes` | `run_node` handles `approval` by calling the wait endpoint and ending the job cleanly without marking it complete. Test with a mock backend: the job stops, the lease is released, and downstream nodes do not run. |
| 4.4 | `feat(backend): decide approvals and resume runs` | Signed-in decide endpoint, token link endpoints, resume logic, expiry ticker. Tests: approve takes the `true` branch; reject takes `false`; a double decision returns 409; an expired token returns 410; another user's approval returns 404; the GET link does not decide. |
| 4.5 | `feat(backend): send approval notifications` | Email through the node's Gmail credential and push through the Expo Push API (URL configurable for tests). Tests with mocked Gmail and Expo servers. |
| 4.6 | `feat(web): add approval node and pending approvals view` | Palette entry, config panel, an "Approvals" inbox, and a waiting badge in the run inspector. Widget tests. |
| 4.7 | `feat(mobile): approve and reject from the app with push notifications` | `expo-notifications`, device registration, an approvals screen, and deep links from notifications. Typecheck and unit tests. |
| 4.8 | `test(e2e): add approval smoke test` | `tests/approval_smoke.py`: a run pauses, is approved through the API, and finishes down the `true` branch; a second is rejected; a third times out. |
| 4.9 | `docs: document approvals` | Contracts, `nodes.md`, README configuration table (`EXPO_ACCESS_TOKEN` optional). |

**Gate:** full-stack gate plus `approval_smoke.py`, and `restart_smoke.py` extended so a backend restart during a waiting run keeps it waiting.

---

## Phase 5 — F7: Workflows as an API (Respond node)

Goal: a webhook workflow can end in a Respond node, so the caller gets a synchronous HTTP response.

### Design

- **New node `respond`.** Config: `status` (100–599), `headers` (object), `body` (mapping template). It is an action; it must be downstream of a `webhook_trigger`, and at most one Respond node can run per execution (validation: every path from the trigger reaches at most one).
- **Sync mode on the webhook trigger:** `config.mode: "async" | "sync"`, plus `timeout_ms` (100–30000, default 10000). Async keeps today's behaviour.
- **Waiting for the response:** in sync mode the hook handler queues the run, then waits on PostgreSQL `LISTEN run_response_<run_id>`. When the runner executes a Respond node it posts `POST /internal/jobs/:id/respond`; the backend stores the response on the run (`runs.response JSONB`) and runs `NOTIFY`. The waiting handler returns it. Using `LISTEN/NOTIFY` means this works across multiple API instances.
- **Timeouts and failures:** if the run fails before a Respond node, the caller gets `502` with the run ID. On timeout, `504` with the run ID; the run keeps going in the background.
- **Nodes after Respond** still run; the response is sent as soon as Respond executes.
- **Load protection:** a per-workflow cap on concurrent sync requests (default 20) returns `429` when exceeded, so held connections cannot exhaust the API server.

### Changes

| # | Commit | Contents |
| --- | --- | --- |
| 5.1 | `feat(backend): add respond node and sync webhook mode` | `migration7` (`runs.response`), catalog, validation (respond only under a webhook trigger, one per path). Validation tests. |
| 5.2 | `feat(runner): execute respond nodes` | Renders status, headers and body, posts to the respond endpoint. Tests with a mock backend. |
| 5.3 | `feat(backend): return workflow responses to sync webhook callers` | `LISTEN/NOTIFY` wait, 502/504 handling, concurrency cap. Tests: the response body and status match the node; a failing run returns 502; a slow run returns 504 and later succeeds; two API instances in one test (one queues, the other's runner path notifies) still deliver the response; the cap returns 429. |
| 5.4 | `feat(web): configure sync webhooks and respond nodes` | Palette entry, config panel, a "copy curl" button showing the sync call. |
| 5.5 | `feat(mobile): configure respond nodes` | |
| 5.6 | `test(e2e): add sync webhook smoke test` | `tests/respond_smoke.py`: a webhook → set_fields → condition → respond workflow answers with different status codes per branch. |
| 5.7 | `docs: document sync webhooks` | Also update nginx `proxy_read_timeout` in `web/nginx.conf` to exceed the maximum `timeout_ms`. |

**Gate:** full-stack gate plus `respond_smoke.py` (through nginx, to confirm proxy timeouts).

---

## Phase 6 — F4: Sandboxed WebAssembly Code node

Goal: users write small JavaScript functions that run in a WebAssembly sandbox inside the runner.

### Design

- **Engine:** `wasmtime` in the runner, running a JavaScript engine (QuickJS) compiled to WebAssembly. The QuickJS module is built once and embedded in the runner binary. User code is passed in as source, not compiled on the host.
- **Language scope:** JavaScript first. Python (a much larger interpreter build) and uploading a precompiled `.wasm` module are deferred; the node config has a `language` field so they can be added without a schema change.
- **Contract:** the user writes `export default function (input, nodes) { return {...} }`. `input` is the parent output and `nodes` is the map of completed upstream outputs, the same data the mapping syntax sees. The return value must be JSON; it becomes the node's output.
- **Limits:** fuel metering (default budget about 50 ms of CPU; configurable per node up to a server maximum), a memory cap (default 32 MiB through `ResourceLimiter`), a wall-clock timeout, and the existing 256 KiB output cap. No WASI filesystem, network, clock or environment access. `console.log` output is captured (up to 16 KiB) into the step output under `logs`.
- **Isolation:** each execution gets a new `Store`; nothing carries over between runs. The node is classified as safe (no side effects), so a lost lease never makes it `uncertain`.
- **Off by default:** `ENABLE_CODE_NODE=true` on both backend and runner. Publish rejects code nodes when it is off.

### Changes

| # | Commit | Contents |
| --- | --- | --- |
| 6.1 | `build(runner): embed wasmtime and a QuickJS wasm module` | Dependency, build script that fetches or builds a pinned, checksum-verified QuickJS module, and a Dockerfile update. Test: the engine starts and evaluates `1+1`. |
| 6.2 | `feat(runner): execute sandboxed JavaScript code nodes` | Host glue, JSON in and out, log capture, fuel and memory limits. Tests: a normal transform; an infinite loop stops on fuel with a clear error; a memory bomb stops at the cap; attempts to reach the filesystem or network fail; non-JSON return values fail; a thrown exception shows its message and line. |
| 6.3 | `feat(backend): add code node to catalog and validation` | Catalog entry, size limit on source (64 KiB), feature flag check, `GET /api/features` flag. Tests. |
| 6.4 | `feat(web): add code editor for code nodes` | Monospace editor with a "Test with last input" button that runs a Test draft. Widget test. |
| 6.5 | `feat(mobile): edit code nodes` | Plain text editor. |
| 6.6 | `test(e2e): add code node smoke test` | `tests/code_node_smoke.py`: a transform, a fuel timeout and a thrown error. |
| 6.7 | `docs: document code nodes and sandbox limits` | Including the threat model: what the sandbox allows and blocks. |

**Gate:** full-stack gate plus `code_node_smoke.py`. Runner image size is checked and recorded in the commit message of 6.1.

---

## Phase 7 — F8: Shadow runs for safe publishing

Goal: before publishing, run live traffic through both the published version and the draft, with the draft's side effects mocked, and show how their outputs differ.

### Design

- **Shadow mode per workflow:** `POST /api/workflows/:id/shadow {enabled, sample_percent, max_runs}` snapshots the current draft (after validation) as the shadow candidate. It is stored in a `shadow_sessions` table: `id, workflow_id, candidate_graph, sample_percent, max_runs, runs_started, status, created_at`.
- **Pairing:** whenever a live run is queued from a trigger (webhook, schedule, Gmail or manual), the backend also queues a `kind='shadow'` run with the candidate graph, the same input and `parent_run_id` pointing to the live run, subject to the sampling rate and the cap.
- **Mocked side effects:** the runner receives `mode: "shadow"` in the job. In shadow mode, `http_request`, `send_email` and `respond` nodes do not execute. Instead the runner asks the backend for the matching step of the paired live run (by node ID). If the live run has that node with the same rendered config, its recorded output is reused. Otherwise the node is recorded as `mocked` with a synthetic output (`{status: 200, body: null}` for HTTP), and the diff marks it as "could not compare". Code, condition and set_fields nodes execute normally. Approval nodes are auto-approved down the same branch the live run took.
- **Diffs:** `GET /api/workflows/:id/shadow` returns, for each pair, the per-node status and output differences (JSON diff), plus totals: pairs compared, pairs identical, pairs that differ, candidate-only failures.
- **Promote:** `POST /api/workflows/:id/shadow/promote` publishes the exact candidate graph that was tested, not whatever the draft has become since, and ends the session.
- Shadow runs are excluded from the heatmap by default and never trigger Respond or approvals.

### Changes

| # | Commit | Contents |
| --- | --- | --- |
| 7.1 | `feat(backend): add shadow sessions and paired shadow runs` | `migration8`, the session endpoint, pairing on every trigger path, sampling and cap. Tests: sampling at 0 and 100 percent; the cap stops pairing; a disabled session stops pairing; shadow runs are hidden from default run lists. |
| 7.2 | `feat(runner): mock side-effect nodes in shadow runs` | Shadow mode, the lookup endpoint for paired live outputs, `mocked` step status. Tests: in a shadow run no HTTP request leaves the runner (checked with a mock server that fails the test if called); reused outputs match the live run; approval follows the live branch. |
| 7.3 | `feat(backend): compute shadow run diffs and promote candidates` | JSON diff per node, totals, promote. Tests: identical graphs give zero differences; a changed `set_fields` shows exactly that field; promote publishes the candidate even after the draft changed. |
| 7.4 | `feat(web): add shadow mode panel and diff viewer` | Start/stop, sampling controls, a pairs list, a side-by-side diff per node, and a promote button. Widget tests. |
| 7.5 | `feat(mobile): show shadow session summary and promote` | Summary and promote only; the detailed diff stays on web. |
| 7.6 | `test(e2e): add shadow run smoke test` | `tests/shadow_smoke.py`: webhook traffic through v1 with a v2 candidate that changes one field; check the diff and that the candidate's HTTP node never contacted the echo server. |
| 7.7 | `docs: document shadow runs` | |

**Gate:** full-stack gate plus `shadow_smoke.py`.

---

## Phase 8 — AI features (F1 builder, F5 failure explainer)

Both features call Claude from the Go backend. They share one client package, one data policy and one test harness.

### Shared design

- **SDK:** the official Go SDK, `github.com/anthropics/anthropic-sdk-go`, in a new `backend/ai` package. Model `claude-opus-5`, adaptive thinking, structured outputs (`output_config.format` with a JSON schema) so responses always parse, and streaming for the builder, whose outputs can be long. Server-side refusal fallbacks enabled. The exact SDK calls are checked against the SDK's documentation when the code is written, not assumed.
- **Configuration:** `ANTHROPIC_API_KEY` (feature off when unset), `AI_MODEL` (default `claude-opus-5`), `AI_ENABLED_FOR_RUN_DATA` (default `false`).
- **Data policy:** workflow graphs are sent to the API when a user uses the builder. Run inputs and outputs are sent only if `AI_ENABLED_FOR_RUN_DATA=true` and the user confirms per request, because they can contain personal data. Credential values are never sent: credentials are referenced by ID and name only, and `webhook_trigger.secret` and HTTP header values are replaced with `"<redacted>"` before any prompt is built. A unit test fails if a known secret string appears in any outgoing request body.
- **Limits:** per-user rate limit (default 30 AI requests per hour), maximum prompt size, and a usage log table (`ai_requests: user_id, feature, input_tokens, output_tokens, created_at`) so cost is visible.
- **Testing:** unit tests run against a mock HTTP server that returns canned API responses, through the SDK's base URL option. A separate, opt-in `tests/ai_live_smoke.py` runs against the real API when a key is present.

### F1: Build a workflow from a sentence

- `POST /api/ai/build {prompt, workflow_id?}` streams progress (server-sent events) and ends with a proposed graph.
- The model receives the node catalog (config, input and output schemas from `GET /api/nodes`), the user's credential names and kinds, and the mapping syntax rules from `contracts/nodes.md`. It returns a graph in the draft JSON format.
- **Validate-and-repair loop:** the backend runs the same `validatePublish` code on the proposed graph. On failure it sends the validation errors back to the model and asks for a corrected graph, up to 3 rounds. Only a graph that passes validation is returned; otherwise the user gets the last errors.
- **Layout:** the backend assigns node positions with a simple layered layout, so the model does not have to produce coordinates.
- **Result:** the graph is returned to the editor as an unsaved draft change, not written to the database, so the user can review it. The editor then offers "Test draft" with a sample input the model suggests. When replay data exists for the workflow (Phase 2), the builder can also be asked to "change this workflow so it handles this failed run", using the run's steps as context (subject to the data policy).

### F5: Explain and propose fixes for failed or uncertain runs

- `POST /api/runs/:id/explain` returns `{summary, likely_cause, safe_to_retry: yes|no|unknown, retry_reasoning, proposed_fix?}`. `proposed_fix` is a graph patch (list of node config changes and edge changes) in the same format the editor applies.
- The model receives the graph snapshot, the failing step, the steps before it (inputs and outputs only if the data policy allows), the step attempts from Phase 1, and the node documentation for the node types involved.
- `safe_to_retry` must be `no` for any `uncertain` run whose unsafe node has no idempotency key; this rule is enforced in code after the model responds, not left to the model.
- **Applying a fix:** "Apply as draft" applies the patch to the draft; "Test on this run" uses Phase 2's replay-against-draft with the failed run's input, so the user sees the fix work before publishing.

### Changes

| # | Commit | Contents |
| --- | --- | --- |
| 8.1 | `feat(backend): add Claude client, data policy and usage limits` | `backend/ai` package, config, redaction, rate limit, `ai_requests` table (`migration9`), `GET /api/features` flags. Tests: redaction removes every secret kind; rate limit; feature off without a key. |
| 8.2 | `feat(backend): generate workflows from a prompt` | Build endpoint, catalog prompt, validate-and-repair loop, layout. Tests with the mock server: a valid first answer is returned; an invalid answer is repaired in the second round; three invalid answers return the errors; an answer that references another user's credential is rejected. |
| 8.3 | `feat(backend): explain failed and uncertain runs` | Explain endpoint, patch format, the enforced `safe_to_retry` rule. Tests: an uncertain POST without a key is never marked safe; patches that would fail validation are dropped; another user's run returns 404. |
| 8.4 | `feat(web): add prompt-to-workflow builder` | A prompt box on the workflow list and in the editor, streamed progress, a preview of the generated graph, accept or discard. Widget tests. |
| 8.5 | `feat(web): add explain and fix on failed runs` | "Explain" on failed and uncertain runs, the explanation panel, "Apply as draft" and "Test on this run". Widget tests. |
| 8.6 | `feat(mobile): add prompt builder and run explanations` | |
| 8.7 | `test(e2e): add AI smoke test with a mock model server` | `tests/ai_smoke.py` runs the backend against a local mock of the Messages API, so CI does not need a key. `tests/ai_live_smoke.py` is opt-in. |
| 8.8 | `docs: document AI features and data handling` | Including exactly what data is sent and how to turn it off. |

**Gate:** full-stack gate plus `ai_smoke.py`. `ai_live_smoke.py` is run once manually with a real key before the phase is considered done.

---

## Phase 9 — F6: Live collaborative canvas

Goal: several people edit the same workflow draft at the same time, with cursors, presence and comments on nodes.

### Design

- **Transport:** a WebSocket endpoint `GET /api/workflows/:id/live`, authenticated with the existing session cookie or bearer token, and checked against the `Origin` rules the API already uses. Go library: `github.com/coder/websocket`. nginx gets the `Upgrade` headers for this path.
- **Sharing:** collaboration needs more than one user per workflow, which the app does not support yet ("Multi user teams" is deferred in `docs/plan.md`). This phase adds the minimum: `workflow_members(workflow_id, user_id, role: editor|viewer)` and invite by email of an existing account. Every ownership check (`w.user_id = $uid`) is replaced by a helper that also accepts members, and every existing isolation test is re-run against members and non-members. Credentials stay private to their owner; a shared workflow can only run with the owner's credentials, and editors see credential names only.
- **Editing model:** operations, not full-document saves. Each operation is small and targets one thing: `add_node`, `remove_node`, `move_node`, `set_config_field` (a JSON path within one node's config), `add_edge`, `remove_edge`, `rename`. The server applies operations one at a time in order, assigns each a sequence number, stores the resulting draft, and broadcasts the operation. Conflicts resolve as last-writer-wins per node field, which is enough because two people rarely edit the same field of the same node at once. A client that falls behind (missed sequence numbers) re-fetches the draft. This avoids adding a CRDT library to both Dart and TypeScript.
- **Existing saves:** `PUT /api/workflows/:id` still works and is broadcast as a full replacement, so the mobile app and older clients keep working.
- **Presence and cursors:** sent over the same socket, never stored.
- **Multiple API instances:** operations are fanned out between instances with PostgreSQL `LISTEN/NOTIFY` on a per-workflow channel.
- **Comments:** `node_comments(id, workflow_id, node_id, user_id, body, resolved, created_at)` with REST endpoints, broadcast live over the socket.

### Changes

| # | Commit | Contents |
| --- | --- | --- |
| 9.1 | `feat(backend): add workflow members and shared access checks` | `migration10`, membership helper used by every workflow, run, stats, replay, shadow and approval endpoint. Tests: every isolation test from the QA pass runs for owner, editor, viewer and stranger. |
| 9.2 | `feat(backend): add draft operations and sequencing` | Operation types, validation of each operation, apply-and-store in one transaction with the sequence number. Tests: each operation type; concurrent operations from two goroutines on one workflow end in a consistent draft; an invalid operation is rejected without changing the draft. |
| 9.3 | `feat(backend): add live collaboration websocket` | Socket auth, operation relay, presence, `LISTEN/NOTIFY` fan-out. Tests: two clients see each other's operations; a viewer's operations are rejected; a client on a second API instance receives operations. |
| 9.4 | `feat(backend): add node comments` | Endpoints and broadcast. Tests. |
| 9.5 | `feat(web): live collaborative editing` | The editor sends operations instead of whole-draft saves when connected, applies remote operations, shows remote cursors and selections, reconnects and resyncs. Undo and redo apply only to the user's own operations. Widget tests plus `tests/browser/collab.spec.js` with two browser contexts editing one workflow. |
| 9.6 | `feat(web): sharing dialog and node comments` | Invite, roles, a comment thread per node. |
| 9.7 | `feat(mobile): see collaborators and comments` | Presence list and comments; mobile edits use the existing save path. |
| 9.8 | `feat(web): add websocket proxying to nginx` | `web/nginx.conf` upgrade headers and timeouts for `/api/workflows/*/live`. |
| 9.9 | `test(e2e): add collaboration smoke test` | `tests/collab_smoke.py` with two users over raw WebSockets. |
| 9.10 | `docs: document sharing and live collaboration` | |

**Gate:** full-stack gate plus `collab_smoke.py` and the two-browser Playwright spec.

---

## Phase 10 — Wrap-up

| # | Commit | Contents |
| --- | --- | --- |
| 10.1 | `test(e2e): run every smoke test from one command` | `make smoke` runs all Python smoke scripts against the running stack; CI gets a job that runs it inside Compose. |
| 10.2 | `ci: add runner wasm build, web and mobile checks for new features` | CI updates for wasmtime and the QuickJS module build, and the new Playwright specs. |
| 10.3 | `docs: update architecture diagram and README for platform v2` | Regenerate `docs/architecture.png` with the new tables, nodes and components (approvals, shadow sessions, AI client, WebSocket hub, Wasm sandbox), and add new environment variables to the README configuration table. |
| 10.4 | `docs: update delivery plan` | Move completed items in `docs/plan.md` to "Implemented", with the verification results. |

**Final gate before merging to `main`:**

1. Every component test gate green.
2. Full-stack gate plus all new smoke scripts green in Compose.
3. The QA agent pass from before this branch (branching, webhooks, schedules, HTTP, validation and isolation) re-run against the branch, with no new failures.
4. `restart_smoke.py` extended to restart the backend while runs are waiting on approvals, holding a sync webhook, and pairing shadow runs.
5. The migration run tested on a copy of a database created by `main`, to confirm existing workflows, runs and credentials survive.

---

## Risks and open decisions

| Risk or decision | Impact | Proposed handling |
| --- | --- | --- |
| Collaboration requires multi-user workflows, which the product does not have yet | Largest change to access control in the plan | Kept to Phase 9 with a full isolation re-test. Could ship without sharing (same user, several devices) if the team work is too big. |
| Replay and shadow runs re-send data to external services | Accidental duplicate emails or API calls | Replay warns and lists side-effect nodes; shadow runs never execute them. |
| AI features send workflow data to the Anthropic API | Privacy | Off by default, run data needs a second opt-in, secrets redacted and tested. |
| Wasm sandbox security | A sandbox escape would run code in the runner | Off by default, no WASI capabilities, fuel and memory limits, pinned engine version. A focused security review before enabling it anywhere public. |
| Sync webhooks hold connections open | API server exhaustion | Timeouts, a per-workflow concurrency cap, and `LISTEN/NOTIFY` instead of polling. |
| Approvals can wait for weeks | Old waiting runs pile up | Maximum timeout of 30 days; waiting runs are excluded from `RUN_RETENTION_DAYS` cleanup until decided. |
| Python in the Code node | Large interpreter build | Deferred; JavaScript first. |
| Mobile push notifications need Expo credentials | Setup burden | Optional; approvals still work by email and in-app without them. |

## Commit count

74 commits across 11 phases (0–10). Each phase ends on a green full-stack gate, so the branch can be paused or partially merged at any phase boundary.
