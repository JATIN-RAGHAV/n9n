# Browser smoke

Start the three-service stack with `make up`, then run:

```sh
cd tests/browser
npm install
npx playwright install chromium
npm test
```

Set `BASE_URL` to test another host. Set `PLAYWRIGHT_CHANNEL=chrome` to use an installed Chrome browser without downloading Playwright's Chromium. The smoke test loads the actual Flutter Wasm build, registers a unique account through the UI, creates and connects a manual trigger and Set fields node, saves and publishes, runs the workflow, inspects its steps, refreshes a run deep link, and checks the mobile navigation menu. It also checks Wasm and JavaScript module MIME types and uncaught page errors. Each run leaves its smoke account and workflow in the local test database.
