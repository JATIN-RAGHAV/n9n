# Papers for n9n

_Last updated: 2026-10-23 08:40 (14:10 IST) · Claude Fable 5.1 (claude-fable-5-1)_

| File | What it is |
| --- | --- |
| `AI_Workflow_Agentic_Orchestration_SEO_Research_Paper.pdf` | Research paper "AI Workflow using Agentic Orchestration and SEO" (IEEE conference format, 11 pages): review of 24 works (22 papers plus the n8n and LangGraph documentation), research gap, the n9n design, and a measured evaluation of the implemented substrate at commit `bcc035d`. |
| `research-paper-src/` | LaTeX source (`n9n_paper.tex`, vendored `IEEEtran.cls`), generated number macros and table bodies, and `build.sh`. |
| `research-paper-src/experiments/` | The measurement harness that produced every number in Section VIII: `run_experiments.py` (drives the public API, manages the worker process), `mock_site.py` (local web site for the SEO and fault cases), `gen_tables.py` (results → LaTeX), `results.json` (final data), `results-run1.json` (first pass, before the makespan method and the flaky page were corrected; kept for the record), `go-test-2026-10-23.log` and `cargo-test-2026-10-23.log` (the test-suite runs cited in Table II). |
| `AI_Workflow_Agentic_Orchestration_Review.pdf` | Earlier review paper for the same project (input to this one, not produced here). |
| `CareSphere_IEEE_Conference_Paper (1).pdf`, `Chronic Kidney Disease Review Paper.docx` | Format samples (not produced here). |

## Reproducing the measurements

```sh
docker compose up -d --wait postgres
docker compose exec -T postgres psql -U n9n -d n9n -c "CREATE DATABASE n9n_paper"
(cd backend && go build -o /tmp/n9n-backend .)
(cd runner && cargo build --release)
cd paper/research-paper-src
python3 -I experiments/run_experiments.py --project ../.. --backend-bin /tmp/n9n-backend \
  --runner-bin ../../runner/target/release/n9n-runner --out experiments
python3 -I experiments/gen_tables.py experiments/results.json .
./build.sh
```

The harness reads `ENCRYPTION_KEY` and `RUNNER_TOKEN` from the repo's `.env`, starts the API on port 18080 and the mock site on 18090, and uses the `n9n_paper` database only. The fault-injection experiment takes about four minutes (three 60-second lease expiries). Numbers depend on the machine; the paper reports an Apple M4 laptop.

## What the paper does and does not claim

- Measured: latency, throughput, replay, a technical SEO audit workflow and fault recovery of the deterministic substrate (Go API, Rust worker, PostgreSQL). The Go (34) and Rust (25) test suites were executed and passed (logs in `experiments/`). The worker ran with `ALLOW_PRIVATE_HTTP=true` so the loopback mock site was reachable.
- Fact-checked on 2026-10-23 by a separate agent against the code, `results.json` and the arXiv records; its 20 findings were applied (request counts per run, recovery-time precision, agent node marked as proposed rather than planned, works count, LangGraph/n8n replay wording, replay-override claim attributed to the backend tests).
- Not measured: the agentic layer (prompt-to-workflow builder, failure explainer, agent node) and the content side of the SEO layer. They are specified from `docs/feature-plan.md` Phase 8 and feature-flagged; Section X of the paper is their evaluation plan.
- Findings about the code that came out of the experiments: the HTTP node fails on any non-2xx status (so audits cannot branch on status inside the graph), and its 256 KiB response cap stops large pages. Both are discussed in Section VIII-H.

Updated 2026-10-23 08:40 UTC (14:10 IST) by Claude Fable 5.1 (claude-fable-5-1)
