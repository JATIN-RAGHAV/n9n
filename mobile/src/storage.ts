import * as SecureStore from 'expo-secure-store';
import { normalizeApiUrl } from './api';
import { Session } from './types';

const SESSION_KEY = 'n9n.mobile.session';
const URL_KEY = 'n9n.mobile.apiUrl';
const THEME_KEY = 'n9n.mobile.theme';

export async function loadApiUrl(): Promise<string> {
  try { return normalizeApiUrl((await SecureStore.getItemAsync(URL_KEY)) ||
    process.env.EXPO_PUBLIC_API_URL || 'http://localhost:8080'); }
  catch { return 'http://localhost:8080'; }
}

export async function saveApiUrl(value: string): Promise<string> {
  const url = normalizeApiUrl(value);
  const previous = await loadApiUrl();
  if (url !== previous) await SecureStore.deleteItemAsync(SESSION_KEY);
  await SecureStore.setItemAsync(URL_KEY, url);
  return url;
}

export async function loadSession(apiUrl: string): Promise<Session | null> {
  try {
    const raw = await SecureStore.getItemAsync(SESSION_KEY);
    if (!raw) return null;
    const value = JSON.parse(raw) as Session;
    if (normalizeApiUrl(value.apiUrl) !== normalizeApiUrl(apiUrl) ||
        !value.token || !value.user?.id ||
        !Number.isFinite(Date.parse(value.expiresAt)) || Date.parse(value.expiresAt) <= Date.now()) {
      await clearSession();
      return null;
    }
    return value;
  } catch { try { await clearSession(); } catch { /* Storage may be unavailable. */ } return null; }
}

export async function saveSession(session: Session): Promise<void> {
  await SecureStore.setItemAsync(SESSION_KEY, JSON.stringify(session));
}
export async function clearSession(): Promise<void> {
  await SecureStore.deleteItemAsync(SESSION_KEY);
}
export async function loadTheme(): Promise<'dark' | 'light'> {
  try { return (await SecureStore.getItemAsync(THEME_KEY)) === 'light' ? 'light' : 'dark'; }
  catch { return 'dark'; }
}
export async function saveTheme(value: 'dark' | 'light'): Promise<void> {
  await SecureStore.setItemAsync(THEME_KEY, value);
}
