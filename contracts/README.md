# n9n HTTP contract

All public routes are prefixed `/api`. JSON requests use `Content-Type: application/json`. Authentication uses an HttpOnly `n9n_session` cookie. Internal routes require `Authorization: Bearer $RUNNER_TOKEN`; a job lease is additionally required for job mutations and credential reads.

The source of truth for paths and payloads is [openapi.yaml](openapi.yaml). Workflow graph nodes have stable IDs, a `type`, position, configuration, and optional `credential_id`. Edges name source and target node IDs and a source port (`out`, `true`, or `false`). Runner mappings use `{{input.FIELD}}` and `{{nodes.NODE_ID.FIELD}}`. A string consisting solely of a mapping returns its JSON value; embedded mappings stringify their values.

Runs are queued durably in SQLite. A claimed job carries a graph snapshot and lease token. The worker reports each completed step and then completes the job. If a lease expires, a run with an interrupted side-effect step can be marked `uncertain` rather than retried automatically.
