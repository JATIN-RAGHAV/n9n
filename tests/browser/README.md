# Browser smoke

Start the application and local PostgreSQL stack with `make up`, then run:

```sh
cd tests/browser
npm install
npx playwright install chromium
npm test
```

Set `BASE_URL` to test another host. Set `PLAYWRIGHT_BROWSER=firefox` to run the same tests in Firefox (install it first with `npx playwright install firefox`); `PLAYWRIGHT_BROWSER=webkit` selects WebKit. Set `PLAYWRIGHT_CHANNEL=chrome` to use an installed Chrome browser without downloading Playwright's Chromium; this channel setting applies only to Chromium. The smoke test loads the actual Flutter Wasm build, registers a unique account through the UI, creates and connects a manual trigger and Set fields node, saves and publishes, runs the workflow, inspects its steps, refreshes a run deep link, and checks the mobile navigation menu. It also checks Wasm and JavaScript module MIME types and uncaught page errors. Each run leaves its smoke account and workflow in the local test database.

`physical-ports.spec.js` is a pointer regression for the red port circles. It creates a draft through the API, opens the editor without enabling Flutter accessibility semantics, locates the rendered port pixels, clicks each port's outer half with the mouse, then saves and verifies the edge. To run it alone: `npx playwright test physical-ports.spec.js`.

The pointer suite also verifies drag-to-connect without moving either node. `theme.spec.js` checks visible palette changes and persistence after refresh on mobile; the workflow smoke switches themes while a draft has unsaved nodes.

Connection regression tests cover input-first and output-first drags and unchanged node positions. Run them in Firefox with `npx playwright install firefox` followed by `PLAYWRIGHT_BROWSER=firefox npx playwright test physical-ports.spec.js --workers=1`.
