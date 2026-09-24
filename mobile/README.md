# n9n for Android and iOS

The React Native app is an Expo managed project in this directory. It connects to the same Go API and PostgreSQL-backed account as the web app. Native login issues a revocable bearer session stored using iOS Keychain / Android Keystore through `expo-secure-store`; the app uses no browser cookies for API authentication.

## Run on a device or simulator

Install Node.js 22 or newer, then run:

```sh
npm ci
npm start
```

Press `a` for Android or `i` for iOS if the relevant simulator is installed, or scan the Expo QR code with Expo Go. In **Settings → Server address**, set the API origin. Use `http://10.0.2.2:8080` from the Android emulator, `http://localhost:8080` from an iOS simulator, or the development computer's LAN IP from a physical device. Use an HTTPS origin for a remote server. Local HTTP may require Android cleartext/network security configuration when producing a native release build; development tooling permits local testing.

The mobile workflow editor supports the initial trigger, transform, HTTP, condition, and Gmail nodes; node configuration, graph connections, saving, publishing, activation, and manual runs with editable JSON input. **Test draft** runs the saved graph snapshot without publishing it, then opens the per-step run inspector. HTTP and Gmail nodes still perform their configured external actions during a test. Assign an existing Gmail credential to Gmail nodes in the editor. Add or remove credentials in the Credentials tab; Gmail OAuth browser authorization remains in the web app, and the mobile app can also enter a refresh token manually. It does not provide native Google OAuth.

## Validate and build

```sh
npm run typecheck
npm test
npm run export:android
npm run export:ios
```

The export commands bundle JavaScript for each native platform. To produce installable signed binaries, install Xcode for iOS and Android Studio/SDK for Android, or configure an Expo account and EAS credentials, then use `npx eas-cli build --platform android --profile preview` and `npx eas-cli build --platform ios --profile preview`. The bundle identifiers are `com.n9n.app` for both platforms. Production API origins should use HTTPS; set the server URL in app settings or provide `EXPO_PUBLIC_API_URL` at bundle time.

Sessions are bound to the configured server URL. Changing the server clears the saved native session. Signing out revokes the token on the server and clears it from secure storage.
