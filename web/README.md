# n9n web

Flutter Web interface for the n9n API. It is served at `/`; nginx sends `/api/*` to the `backend` service and supports direct links to application routes.

The interface defaults to a red and charcoal theme. Use the sun/moon button on the sign-in screen or navigation bar to switch between dark and light themes; the preference is saved in browser local storage as `n9n.theme`. The primary accent and connection ports are `#e5484d` (RGB 229, 72, 77) in both themes. Errors also carry an explicit “Error” label so color alone does not convey their meaning.

## Local development

Install Flutter 3.35.7 or a compatible stable release, then run:

```sh
flutter pub get
flutter run -d chrome --web-port 3000
```

For local API calls, serve the built application through the bundled nginx proxy, or use a development reverse proxy that forwards `/api` to the backend. The production image uses `flutter build web --wasm --release`.

## Editor

Drag nodes from the left palette onto the canvas. Drag between a node's right-edge output and another node's left-edge input, starting at either end; a preview follows the pointer. Dragging the card body moves a node, while port drags only create connections. You can also click the two ports in either order, or select a source node and choose **Connect to node** in its settings panel. Condition nodes offer separate `true` and `false` circles. Click a connection to delete it. Select a node to edit its configuration, choose a credential, or open the raw JSON editor. Save stores a draft; Publish creates an executable version; Run now starts that published version. The Runs view shows each step's input, output, and errors. Browser paths are `/workflows`, `/workflows/:id`, `/credentials`, and `/runs/:id`.
