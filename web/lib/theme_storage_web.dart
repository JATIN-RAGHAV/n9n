import 'package:web/web.dart' as web;

const _key = 'n9n.theme';

String? readThemePreference() {
  try {
    return web.window.localStorage.getItem(_key);
  } catch (_) {
    return null;
  }
}

void saveThemePreference(String value) {
  try {
    web.window.localStorage.setItem(_key, value);
  } catch (_) {
    // Private browsing and policy may deny storage; switching still works.
  }
}
