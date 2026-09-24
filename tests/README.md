# End-to-end smoke

Run `make up`, then `python3 tests/e2e_smoke.py`. The script uses Python's standard library and exercises the public HTTP API through nginx. It registers unique test accounts and leaves their test workflows in the local database. Schedule verification can take up to 35 seconds. Gmail OAuth requires external Google configuration and is covered separately by backend tests with mocked Google endpoints.

Run `python3 tests/restart_smoke.py` to verify a queued run survives a backend restart and executes its original published graph version after the runner returns. This test temporarily stops the runner and restores it in a `finally` block. It creates a unique test account and workflow.

The browser smoke in `tests/browser` exercises the Flutter Wasm UI; see its README for setup.
