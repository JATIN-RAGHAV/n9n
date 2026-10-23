#!/bin/sh
# Builds the Flutter web app and adds the static-asset headers file.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
cd "$here/../web"
flutter pub get
flutter build web --wasm --release
cp "$here/_headers" build/web/_headers
echo "web build ready at $here/../web/build/web"
