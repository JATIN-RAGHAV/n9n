# Node catalog version 2

`GET /api/nodes` returns `version: 2` and eight node entries. Version 2 marks the start of the platform v2 node additions; clients must ignore node types they cannot render rather than failing to load a workflow. Each entry includes `config_schema`, `input_schema`, `output_schema`, and input/output ports. The published graph is a directed acyclic graph with exactly one trigger, at most one incoming edge per action, and all nodes reachable from the trigger. Only `condition` has `true` and `false` output ports; all other nodes output on `out`.

| Type | Configuration | Output |
| --- | --- | --- |
| `manual_trigger` | Empty object | Manual run input object |
| `webhook_trigger` | `secret` (nonempty string) | Webhook request JSON object |
| `schedule_trigger` | `interval_seconds` (integer ≥ 10), `timezone` (`UTC`) | `scheduled_at` timestamp |
| `email_trigger` | `poll_seconds` (integer ≥ 30), optional Gmail `query`; Gmail OAuth credential | `id`, `thread_id`, `sender`, `subject`, `body`, `snippet`, `headers`, `message` |
| `set_fields` | `fields` object with literals or mappings | Incoming object merged with rendered fields |
| `http_request` | `url`, uppercase `method`, optional `headers`, `body`, `idempotency_key` | `status`, `headers`, parsed JSON or text `body` |
| `condition` | `left`, `operator` (`equals`, `contains`, `greater_than`, `exists`), optional `right` for `exists` | Incoming object, routed to `true` or `false` |
| `send_email` | `to`, `subject`, `body`; Gmail OAuth credential | Gmail send response, including message `id` |

All actions receive the output of their single incoming edge as `input`. `{{input.field}}` reads that immediate input. `{{nodes.NODE_ID.field}}` reads a completed upstream node's output; node IDs use letters, digits, `_`, and `-`. A string made entirely of one mapping preserves the mapped JSON type. An embedded mapping converts the value to text. A missing field fails the step. Node config and run input JSON nesting is limited to 32 levels.

HTTP request destinations resolve to public IP addresses by default (`ALLOW_PRIVATE_HTTP=false`). Redirects are not followed. `idempotency_key` must be a nonempty string when supplied; use a unique value for each logical event, such as `{{input.id}}`, rather than a constant shared by all runs. GET, HEAD and idempotent requests are retried up to three times on a 5xx response or a transport failure; every request is recorded as a step attempt (`attempts` on the node's latest step in run detail) with its status code, error and duration.

## Replay

`POST /api/runs/{id}/replay` with `{from_node, input_override?, graph}` queues a `replay` run of a finished run. The worker's resume path does the work: succeeded steps of every node that is not downstream of `from_node` are copied into the new run, so those nodes keep their recorded outputs and only `from_node` and its descendants execute. `input_override` replaces the copied output of the node feeding `from_node` (or the run input when `from_node` is the trigger), so the replayed node sees the edited input. A copied condition keeps its recorded branch. With `graph: "draft"` the saved draft is validated like Test draft and snapshotted; the workflow stays unpublished. Copied steps carry no timing. Run detail returns the executed `graph` and the `children` replays of a run. Clients warn before a replay that would execute `http_request` or `send_email` nodes again.

## Run kinds and step timing

Runs carry a `kind`: `normal` (a published version), `test` (the saved draft), `replay` (re-run from a chosen node of an earlier run, linked through `parent_run_id` and `resume_from_node`) or `shadow` (a draft candidate paired with a live run). Only `normal` runs reference a published version; the others store a `graph_snapshot`. The `test` boolean remains as a mirror of `kind == "test"` for older clients. Each step records `started_at`, `finished_at` and `duration_ms`; runners that omit timing still work.
