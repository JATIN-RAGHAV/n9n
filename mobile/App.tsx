import React, { useEffect, useMemo, useState } from 'react';
import { ActivityIndicator, Alert, BackHandler, Pressable, ScrollView, Text, View } from 'react-native';
import { SafeAreaProvider, SafeAreaView } from 'react-native-safe-area-context';
import { StatusBar } from 'expo-status-bar';
import { Api, ApiError } from './src/api';
import { clearSession, loadApiUrl, loadSession, loadTheme, saveApiUrl, saveSession, saveTheme } from './src/storage';
import { Session } from './src/types';
import { Action, Label, Mode, palettes, ThemeProvider, usePalette } from './src/theme';
import { AuthScreen, CredentialsScreen, HomeScreen, RunsScreen, RunDetailScreen, SettingsScreen } from './src/screens';
import { EditorScreen } from './src/editor';

type Screen = { kind: 'home' | 'runs' | 'credentials' | 'settings' } |
  { kind: 'editor' | 'run'; id: string };

function Chrome({ screen, onGo, onBack, onToggle, mode, email }: {
  screen: Screen; onGo: (screen: Screen) => void; onBack: () => void;
  onToggle: () => void; mode: Mode; email: string;
}) {
  const p = usePalette();
  return <View style={{ backgroundColor: '#090a0d', paddingHorizontal: 16, paddingTop: 8,
    paddingBottom: 10, borderBottomColor: p.red, borderBottomWidth: 2 }}>
    <View style={{ flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' }}>
      <Pressable accessibilityRole="button" accessibilityLabel="Go to workflows" onPress={() => onGo({ kind: 'home' })}>
        <Text style={{ color: '#f5f2ef', fontWeight: '900', fontSize: 27, letterSpacing: -1.5 }}>◈ n9n</Text>
      </Pressable>
      <View style={{ flexDirection: 'row', alignItems: 'center', gap: 10 }}>
        <Pressable accessibilityRole="button" accessibilityLabel={mode === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
          onPress={onToggle} style={{ padding: 9 }}>
          <Text style={{ color: '#f5f2ef', fontSize: 22 }}>{mode === 'dark' ? '☀' : '☾'}</Text>
        </Pressable>
        {screen.kind !== 'home' && <Pressable accessibilityRole="button" accessibilityLabel="Back"
          onPress={onBack} style={{ padding: 9 }}><Text style={{ color: '#f5f2ef', fontWeight: '700' }}>Back</Text></Pressable>}
      </View>
    </View>
    {screen.kind === 'home' || screen.kind === 'runs' || screen.kind === 'credentials' || screen.kind === 'settings' ?
      <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginTop: 8 }}>
        {(['home', 'runs', 'credentials', 'settings'] as const).map(kind =>
          <Pressable key={kind} accessibilityRole="tab" accessibilityState={{ selected: screen.kind === kind }}
            onPress={() => onGo({ kind })} style={{ paddingHorizontal: 13, paddingVertical: 8,
              marginRight: 7, borderRadius: 15, backgroundColor: screen.kind === kind ? '#3d1a20' : '#242832' }}>
            <Text style={{ color: screen.kind === kind ? '#ff7378' : '#f5f2ef', fontWeight: '700', fontSize: 12 }}>
              {kind === 'home' ? 'Workflows' : kind[0].toUpperCase() + kind.slice(1)}
            </Text>
          </Pressable>)}</ScrollView> :
      <Text numberOfLines={1} style={{ color: '#adb3bd', marginTop: 4, fontSize: 12 }}>{email}</Text>}
  </View>;
}

function AppContent() {
  const [booting, setBooting] = useState(true);
  const [apiUrl, setApiUrl] = useState('http://localhost:8080');
  const [session, setSession] = useState<Session | null>(null);
  const [mode, setMode] = useState<Mode>('dark');
  const [screen, setScreen] = useState<Screen>({ kind: 'home' });
  const [editorDirty, setEditorDirty] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const p = palettes[mode];
  const api = useMemo(() => new Api(apiUrl, session?.token), [apiUrl, session?.token]);

  useEffect(() => {
    let active = true;
    (async () => {
      const [url, savedMode] = await Promise.all([loadApiUrl(), loadTheme()]);
      const saved = await loadSession(url);
      let valid = saved;
      if (saved) {
        try { await new Api(url, saved.token).me(); }
        catch (e) {
          if (e instanceof ApiError && e.status === 401) { await clearSession(); valid = null; }
          else { /* Keep a valid offline session; requests still report network failures. */ }
        }
      }
      if (active) { setApiUrl(url); setMode(savedMode); setSession(valid); setBooting(false); }
    })().catch(() => { if (active) setBooting(false); });
    return () => { active = false; };
  }, []);

  const toggleTheme = () => {
    const next = mode === 'dark' ? 'light' : 'dark';
    setMode(next);
    saveTheme(next).catch(() => setNotice('Theme changed, but its preference could not be saved.'));
  };
  const navigate = (next: Screen) => {
    if (screen.kind === 'editor' && editorDirty && (next.kind !== 'editor' || next.id !== screen.id)) {
      Alert.alert('Unsaved draft', 'Discard unsaved workflow changes?', [
        { text: 'Keep editing', style: 'cancel' },
        { text: 'Discard', style: 'destructive', onPress: () => { setEditorDirty(false); setScreen(next); } },
      ]);
    } else setScreen(next);
  };
  const back = () => navigate(screen.kind === 'run' ? { kind: 'runs' } : { kind: 'home' });
  useEffect(() => {
    const listener = BackHandler.addEventListener('hardwareBackPress', () => {
      if (screen.kind === 'home') return false;
      back(); return true;
    });
    return () => listener.remove();
  });

  const onAuth = async (value: Session) => {
    await saveSession(value);
    setSession(value);
    setScreen({ kind: 'home' });
  };
  const onChangeUrl = async (value: string) => {
    const normalized = await saveApiUrl(value);
    if (normalized !== apiUrl) { setSession(null); setScreen({ kind: 'home' }); setEditorDirty(false); }
    setApiUrl(normalized);
  };
  const onLogout = async () => {
    let revoked = true;
    try { await api.logout(); } catch { revoked = false; }
    try { await clearSession(); } catch { setNotice('Could not clear secure storage. Please clear app data before sharing this device.'); }
    setSession(null); setEditorDirty(false); setScreen({ kind: 'home' });
    if (!revoked) setNotice('Signed out on this device. Server token revocation could not be confirmed.');
  };
  const onUnauthorized = () => {
    clearSession().catch(() => {});
    setSession(null); setEditorDirty(false); setScreen({ kind: 'home' });
    setNotice('Your session expired. Sign in again.');
  };

  return <ThemeProvider value={p}>
    <SafeAreaView style={{ flex: 1, backgroundColor: p.bg }}>
      <StatusBar style={mode === 'dark' ? 'light' : 'dark'} />
      {booting ? <ActivityIndicator color={p.red} style={{ flex: 1 }} /> :
        session === null ? <AuthScreen apiUrl={apiUrl} onAuth={onAuth} onChangeUrl={onChangeUrl}
          onToggleTheme={toggleTheme} mode={mode} /> : <>
          <Chrome screen={screen} onGo={navigate} onBack={back} onToggle={toggleTheme}
            mode={mode} email={session.user.email} />
          <View style={{ flex: 1 }}>
            {screen.kind === 'home' ? <HomeScreen api={api} onOpen={id => navigate({ kind: 'editor', id })}
              onRun={id => navigate({ kind: 'run', id })} onUnauthorized={onUnauthorized} /> :
              screen.kind === 'editor' ? <EditorScreen api={api} workflowId={screen.id}
                onRun={id => navigate({ kind: 'run', id })} onDirtyChange={setEditorDirty}
                onUnauthorized={onUnauthorized} /> :
              screen.kind === 'runs' ? <RunsScreen api={api} onOpen={id => navigate({ kind: 'run', id })}
                onUnauthorized={onUnauthorized} /> :
              screen.kind === 'run' ? <RunDetailScreen api={api} runId={screen.id}
                onUnauthorized={onUnauthorized} /> :
              screen.kind === 'credentials' ? <CredentialsScreen api={api} onUnauthorized={onUnauthorized} /> :
              <SettingsScreen apiUrl={apiUrl} onChangeUrl={onChangeUrl} onLogout={onLogout} />}
          </View>
        </>}
      {notice && <Pressable accessibilityRole="alert" onPress={() => setNotice(null)}
        style={{ backgroundColor: p.dangerBg, padding: 12, borderTopColor: p.red, borderTopWidth: 1 }}>
        <Label>{notice} · Dismiss</Label>
      </Pressable>}
    </SafeAreaView>
  </ThemeProvider>;
}

export default function App() { return <SafeAreaProvider><AppContent /></SafeAreaProvider>; }
