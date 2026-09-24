# Node catalog version 1

`GET /api/nodes` returns `version: 1` and eight node entries. Each entry includes `config_schema`, `input_schema`, `output_schema`, and input/output ports. The published graph is a directed acyclic graph with exactly one trigger, at most one incoming edge per action, and all nodes reachable from the trigger. Only `condition` has `true` and `false` output ports; all other nodes output on `out`.

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

HTTP request destinations resolve to public IP addresses by default (`ALLOW_PRIVATE_HTTP=false`). Redirects are not followed. `idempotency_key` must be a nonempty string when supplied; use a unique value for each logical event, such as `{{input.id}}`, rather than a constant shared by all runs.
