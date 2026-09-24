# n9n web

Flutter Web interface for the n9n API. It is served at `/`; nginx sends `/api/*` to the `backend` service and supports direct links to application routes.

The interface uses a red and charcoal theme across authentication, workflows, credentials, runs, and the editor. The primary accent and connection ports are `#e5484d` (RGB 229, 72, 77); errors also carry an explicit “Error” label so the color alone does not convey their meaning.

## Local development

Install Flutter 3.35.7 or a compatible stable release, then run:

```sh
flutter pub get
flutter run -d chrome --web-port 3000
```

For local API calls, serve the built application through the bundled nginx proxy, or use a development reverse proxy that forwards `/api` to the backend. The production image uses `flutter build web --wasm --release`.

## Editor

Drag nodes from the left palette onto the canvas. Click the circle on a node's right edge, then the circle on the next node's left edge to connect them. Condition nodes offer separate `true` and `false` circles. Click a connection to delete it. Select a node to edit its configuration, choose a credential, or open the raw JSON editor. Save stores a draft; Publish creates an executable version; Run now starts that published version. The Runs view shows each step's input, output, and errors. Browser paths are `/workflows`, `/workflows/:id`, `/credentials`, and `/runs/:id`.
