import React, { useCallback, useEffect, useState } from 'react';
import { ActivityIndicator, Alert, Linking, Modal, Pressable, RefreshControl, ScrollView, View } from 'react-native';
import { Api, ApiError } from './api';
import { Action, Chip, ErrorText, Field, Label, Panel, usePalette } from './theme';
import { Credential, JsonObject, Run, Session, Step, Workflow } from './types';

function message(error: unknown): string { return error instanceof Error ? error.message : String(error); }
function fail(error: unknown, onUnauthorized: () => void, setError: (value: string) => void) {
  if (error instanceof ApiError && error.status === 401) onUnauthorized();
  else setError(message(error));
}

export function AuthScreen({ apiUrl, onAuth, onChangeUrl, onToggleTheme, mode }: {
  apiUrl: string; onAuth: (value: Session) => Promise<void>; onChangeUrl: (value: string) => Promise<void>;
  onToggleTheme: () => void; mode: 'dark' | 'light';
}) {
  const p = usePalette();
  const [register, setRegister] = useState(false);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [server, setServer] = useState(apiUrl);
  const [serverOpen, setServerOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const submit = async () => {
    if (!email.trim() || !password) { setError('Enter an email and password.'); return; }
    setBusy(true); setError(null);
    try {
      const response = await new Api(apiUrl).mobileAuth(register ? 'register' : 'login', email.trim(), password);
      await onAuth({ apiUrl, token: response.token, user: response.user, expiresAt: response.expires_at });
    } catch (e) { setError(message(e)); } finally { setBusy(false); }
  };
  return <ScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{ flexGrow: 1,
    justifyContent: 'center', padding: 24 }}>
    <View style={{ alignItems: 'flex-end', marginBottom: 18 }}>
      <Action title={mode === 'dark' ? '☀ Light theme' : '☾ Dark theme'} secondary small onPress={onToggleTheme} />
    </View>
    <Label size={36} bold style={{ letterSpacing: -2 }}>◈ n9n</Label>
    <Label muted style={{ marginTop: 7, marginBottom: 34 }}>Automation from idea to execution.</Label>
    <Panel>
      <Label size={25} bold>{register ? 'Create your workspace' : 'Welcome back'}</Label>
      <Label muted style={{ marginTop: 5, marginBottom: 22 }}>Connect to your n9n server.</Label>
      <Field label="Email" value={email} onChangeText={setEmail} keyboardType="email-address" placeholder="you@company.com" />
      <Field label="Password" value={password} onChangeText={setPassword} secureTextEntry placeholder="Password" />
      <ErrorText message={error} />
      <Action title={busy ? 'Please wait…' : register ? 'Create account' : 'Sign in'}
        onPress={submit} disabled={busy} />
      <View style={{ marginTop: 10 }}><Action secondary title={register ? 'Already have an account? Sign in' : 'New here? Create account'}
        onPress={() => { setRegister(!register); setError(null); }} /></View>
    </Panel>
    <Pressable accessibilityRole="button" accessibilityLabel="Server settings" onPress={() => setServerOpen(!serverOpen)}
      style={{ paddingVertical: 18 }}><Label muted size={12}>Server: {apiUrl} · Change</Label></Pressable>
    {serverOpen && <Panel><Field label="Server origin" value={server} onChangeText={setServer}
      placeholder="https://n9n.example.com" keyboardType="url" />
      <Action title="Use this server" onPress={async () => {
        try { await onChangeUrl(server); setServerOpen(false); setError(null); }
        catch (e) { setError(message(e)); }
      }} />
      <Label muted size={12} style={{ marginTop: 10 }}>Android emulator: http://10.0.2.2:8080 · iOS simulator: http://localhost:8080 · phone: your computer’s LAN IP.</Label>
    </Panel>}
    <View style={{ height: 20 }} />
  </ScrollView>;
}

export function HomeScreen({ api, onOpen, onRun, onUnauthorized }: {
  api: Api; onOpen: (id: string) => void; onRun: (id: string) => void; onUnauthorized: () => void;
}) {
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [runs, setRuns] = useState<Run[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(async () => {
    try {
      const [w, r] = await Promise.all([api.workflows(), api.runs()]);
      setWorkflows(w.workflows); setRuns(r.runs); setError(null);
    } catch (e) { fail(e, onUnauthorized, setError); } finally { setLoading(false); }
  }, [api, onUnauthorized]);
  useEffect(() => { load(); }, [load]);
  const create = async () => {
    if (!name.trim()) { setError('Give the workflow a name.'); return; }
    try { const value = await api.createWorkflow(name.trim()); setCreating(false); setName(''); onOpen(value.workflow.id); }
    catch (e) { fail(e, onUnauthorized, setError); }
  };
  const remove = (item: Workflow) => Alert.alert('Delete workflow?', `${item.name} and its run history will be removed.`, [
    { text: 'Cancel', style: 'cancel' },
    { text: 'Delete', style: 'destructive', onPress: async () => {
      try { await api.deleteWorkflow(item.id); await load(); } catch (e) { fail(e, onUnauthorized, setError); }
    } },
  ]);
  return <ScrollView refreshControl={<RefreshControl refreshing={loading} onRefresh={() => { setLoading(true); load(); }} />}
    contentContainerStyle={{ padding: 18, paddingBottom: 60 }}>
    <Label size={28} bold>Workflows</Label><Label muted style={{ marginTop: 4, marginBottom: 20 }}>Build the flow. Know what happened.</Label>
    <Action title="New workflow" onPress={() => setCreating(true)} />
    <ErrorText message={error} />
    {loading && <ActivityIndicator style={{ marginTop: 30 }} />}
    {!loading && workflows.length === 0 && <Panel style={{ marginTop: 18 }}>
      <Label size={19} bold>Your first flow starts here</Label>
      <Label muted style={{ marginTop: 7 }}>Add a trigger, connect actions, then publish.</Label>
    </Panel>}
    {workflows.map(item => <Panel key={item.id} style={{ marginTop: 12 }}>
      <Pressable accessibilityRole="button" accessibilityLabel={`Open ${item.name}`} onPress={() => onOpen(item.id)}>
        <Label size={18} bold>{item.name}</Label>
        <Label muted size={12} style={{ marginTop: 7 }}>{item.draft.nodes.length} nodes · {item.draft.edges.length} connections · {item.active ? 'Active' : item.published_version ? `Published v${item.published_version}` : 'Draft'}</Label>
      </Pressable>
      <View style={{ flexDirection: 'row', gap: 8, marginTop: 13 }}>
        <View style={{ flex: 1 }}><Action title="Edit" small secondary onPress={() => onOpen(item.id)} /></View>
        <View style={{ flex: 1 }}><Action title="Delete" small danger onPress={() => remove(item)} /></View>
      </View>
    </Panel>)}
    <Label size={19} bold style={{ marginTop: 30, marginBottom: 12 }}>Recent runs</Label>
    {runs.length === 0 && !loading && <Label muted>No runs yet.</Label>}
    {runs.slice(0, 6).map(run => <Pressable key={run.id} accessibilityRole="button" onPress={() => onRun(run.id)}
      style={{ paddingVertical: 12, borderBottomColor: '#343943', borderBottomWidth: 1 }}>
      <Label bold>{run.test ? 'DRAFT TEST · ' : ''}{run.status.toUpperCase()}</Label><Label muted size={12}>{run.id.slice(0, 10)} · {run.created_at || ''}</Label>
    </Pressable>)}
    <Modal visible={creating} transparent animationType="fade" onRequestClose={() => setCreating(false)}>
      <View style={{ flex: 1, justifyContent: 'center', backgroundColor: '#0009', padding: 22 }}>
        <Panel><Label size={22} bold>New workflow</Label><View style={{ height: 17 }} />
          <Field label="Name" value={name} onChangeText={setName} placeholder="Customer follow-up" />
          <Action title="Create" onPress={create} />
          <View style={{ marginTop: 9 }}><Action secondary title="Cancel" onPress={() => setCreating(false)} /></View>
        </Panel>
      </View>
    </Modal>
  </ScrollView>;
}

export function RunsScreen({ api, onOpen, onUnauthorized }: {
  api: Api; onOpen: (id: string) => void; onUnauthorized: () => void;
}) {
  const [runs, setRuns] = useState<Run[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const load = useCallback(async () => { try { setRuns((await api.runs()).runs); setError(null); }
    catch (e) { fail(e, onUnauthorized, setError); } finally { setLoading(false); } }, [api, onUnauthorized]);
  useEffect(() => { load(); }, [load]);
  return <ScrollView refreshControl={<RefreshControl refreshing={loading} onRefresh={() => { setLoading(true); load(); }} />}
    contentContainerStyle={{ padding: 18, paddingBottom: 55 }}>
    <Label size={28} bold>Runs</Label><Label muted style={{ marginTop: 4, marginBottom: 18 }}>Inspect what each workflow did.</Label>
    <ErrorText message={error} />
    {!loading && runs.length === 0 && <Panel><Label muted>No runs yet.</Label></Panel>}
    {runs.map(run => <Pressable key={run.id} accessibilityRole="button" onPress={() => onOpen(run.id)}>
      <Panel style={{ marginBottom: 10 }}><Label size={16} bold>{run.status.toUpperCase()}</Label>
        <Label muted size={12} style={{ marginTop: 5 }}>Workflow {run.workflow_id.slice(0, 10)} · v{run.version || '?'} · {run.created_at || ''}</Label>
      </Panel></Pressable>)}
  </ScrollView>;
}

export function RunDetailScreen({ api, runId, onUnauthorized }: {
  api: Api; runId: string; onUnauthorized: () => void;
}) {
  const [run, setRun] = useState<Run | null>(null);
  const [steps, setSteps] = useState<Step[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const refresh = useCallback(async () => {
    try { const result = await api.run(runId); setRun(result.run); setSteps(result.steps); setError(null); }
    catch (e) { fail(e, onUnauthorized, setError); }
  }, [api, runId, onUnauthorized]);
  useEffect(() => { refresh(); }, [refresh]);
  useEffect(() => {
    if (!run || !['queued', 'running'].includes(run.status)) return;
    const timer = setInterval(refresh, 2000); return () => clearInterval(timer);
  }, [run?.status, refresh]);
  const latest = new Map<string, Step>();
  for (const step of steps) latest.set(step.node_id, step);
  const cancel = async () => { try { await api.cancelRun(runId); await refresh(); }
    catch (e) { fail(e, onUnauthorized, setError); } };
  return <ScrollView contentContainerStyle={{ padding: 18, paddingBottom: 55 }}>
    <Label size={27} bold>Run inspector</Label><Label muted size={12} style={{ marginTop: 5 }}>{runId}</Label>
    <ErrorText message={error} />
    {!run && !error && <ActivityIndicator style={{ marginTop: 30 }} />}
    {run && <><Panel style={{ marginTop: 18 }}>
      <Label size={18} bold>{run.test ? 'DRAFT TEST · ' : ''}{run.status.toUpperCase()} · {run.test ? 'draft snapshot' : `v${run.version || '?'}`}</Label>
      <Label muted size={12} style={{ marginTop: 8 }}>Created {run.created_at || '—'}</Label>
      <Label muted size={12}>Updated {run.updated_at || '—'}</Label>
      {run.error && <ErrorText message={run.error} />}
      {['queued', 'running'].includes(run.status) && <View style={{ marginTop: 12 }}><Action danger title="Cancel run" onPress={cancel} /></View>}
    </Panel>
    <Label size={19} bold style={{ marginTop: 25, marginBottom: 10 }}>Steps</Label>
    {[...latest.values()].map(step => <Panel key={step.node_id} style={{ marginBottom: 10 }}>
      <Pressable accessibilityRole="button" onPress={() => setExpanded(expanded === step.node_id ? null : step.node_id)}>
        <Label bold>{step.node_id} · {step.status}</Label>
        <Label muted size={12} style={{ marginTop: 5 }}>Attempt {step.attempt || 1} · {expanded === step.node_id ? 'Hide' : 'View'} input/output</Label>
      </Pressable>
      {expanded === step.node_id && <View style={{ marginTop: 12 }}>
        <Label muted size={11} bold>INPUT</Label><Label size={12} style={{ marginTop: 5, fontFamily: 'monospace' }}>{JSON.stringify(step.input ?? null, null, 2)}</Label>
        <Label muted size={11} bold style={{ marginTop: 15 }}>OUTPUT</Label><Label size={12} style={{ marginTop: 5, fontFamily: 'monospace' }}>{JSON.stringify(step.output ?? null, null, 2)}</Label>
        {step.error && <ErrorText message={step.error} />}
      </View>}
    </Panel>)}
    {latest.size === 0 && <Label muted>No steps recorded yet.</Label>}
    <View style={{ marginTop: 8 }}><Action secondary title="Refresh" onPress={refresh} /></View>
    </>}
  </ScrollView>;
}

export function CredentialsScreen({ api, onUnauthorized }: { api: Api; onUnauthorized: () => void }) {
  const [items, setItems] = useState<Credential[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState('');
  const [clientId, setClientId] = useState('');
  const [clientSecret, setClientSecret] = useState('');
  const [refreshToken, setRefreshToken] = useState('');
  const load = useCallback(async () => { try { setItems((await api.credentials()).credentials); setError(null); }
    catch (e) { fail(e, onUnauthorized, setError); } }, [api, onUnauthorized]);
  useEffect(() => { load(); }, [load]);
  const create = async () => {
    if (![name, clientId, clientSecret, refreshToken].every(s => s.trim())) {
      setError('Enter all Gmail OAuth fields.'); return;
    }
    try { await api.createCredential(name.trim(), { client_id: clientId.trim(),
      client_secret: clientSecret.trim(), refresh_token: refreshToken.trim() });
      setShowForm(false); setName(''); setClientId(''); setClientSecret(''); setRefreshToken(''); await load(); }
    catch (e) { fail(e, onUnauthorized, setError); }
  };
  const remove = (item: Credential) => Alert.alert('Delete credential?', `${item.name} will be removed.`, [
    { text: 'Cancel', style: 'cancel' },
    { text: 'Delete', style: 'destructive', onPress: async () => {
      try { await api.deleteCredential(item.id); await load(); }
      catch (e) { fail(e, onUnauthorized, setError); }
    } },
  ]);
  return <ScrollView contentContainerStyle={{ padding: 18, paddingBottom: 55 }}>
    <Label size={28} bold>Credentials</Label>
    <Label muted style={{ marginTop: 5, marginBottom: 15 }}>Mailbox credentials are encrypted on your n9n server.</Label>
    <Panel><Label size={16} bold>Connect Gmail</Label>
      <Label muted size={12} style={{ marginTop: 8 }}>Open your n9n web app in a browser and use Connect Gmail. Native Google authorization is not configured in this app. You can also enter an existing OAuth refresh token below.</Label>
      <View style={{ marginTop: 12 }}><Action secondary title="Open web app" onPress={() => Linking.openURL(api.url)} /></View>
    </Panel>
    <View style={{ marginTop: 13 }}><Action title="Add manual Gmail credential" onPress={() => setShowForm(true)} /></View>
    <ErrorText message={error} />
    {items.length === 0 && <Label muted style={{ marginTop: 25 }}>No credentials saved yet.</Label>}
    {items.map(item => <Panel key={item.id} style={{ marginTop: 12 }}>
      <Label size={17} bold>{item.name}</Label><Label muted size={12} style={{ marginTop: 5 }}>{item.kind} · {item.created_at || ''}</Label>
      <View style={{ marginTop: 12 }}><Action danger small title="Delete" onPress={() => remove(item)} /></View>
    </Panel>)}
    <Modal visible={showForm} animationType="slide" onRequestClose={() => setShowForm(false)}>
      <ScrollView contentContainerStyle={{ flexGrow: 1, padding: 20, backgroundColor: usePalette().bg }}>
        <Label size={23} bold>Manual Gmail credential</Label>
        <Label muted size={12} style={{ marginTop: 7, marginBottom: 18 }}>Use your Google OAuth client and a refresh token for a mailbox with Gmail read/send scopes. Secrets are sent only to the server and are never stored as form history.</Label>
        <Field label="Name" value={name} onChangeText={setName} placeholder="Sales mailbox" />
        <Field label="Client ID" value={clientId} onChangeText={setClientId} />
        <Field label="Client secret" value={clientSecret} onChangeText={setClientSecret} secureTextEntry />
        <Field label="Refresh token" value={refreshToken} onChangeText={setRefreshToken} secureTextEntry />
        <ErrorText message={error} />
        <Action title="Save credential" onPress={create} />
        <View style={{ marginTop: 9 }}><Action secondary title="Cancel" onPress={() => setShowForm(false)} /></View>
      </ScrollView>
    </Modal>
  </ScrollView>;
}

export function SettingsScreen({ apiUrl, onChangeUrl, onLogout }: {
  apiUrl: string; onChangeUrl: (value: string) => Promise<void>; onLogout: () => Promise<void>;
}) {
  const [value, setValue] = useState(apiUrl);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  return <ScrollView contentContainerStyle={{ padding: 18, paddingBottom: 55 }}>
    <Label size={28} bold>Settings</Label>
    <Panel style={{ marginTop: 18 }}><Label size={17} bold>Server address</Label>
      <Label muted size={12} style={{ marginTop: 7, marginBottom: 15 }}>Changing servers signs you out and removes this device’s stored token for the old server.</Label>
      <Field label="API origin" value={value} onChangeText={setValue} keyboardType="url"
        placeholder="https://n9n.example.com" />
      <ErrorText message={error} />
      {saved && <Label muted size={12} style={{ marginBottom: 10 }}>Saved.</Label>}
      <Action title="Save server" onPress={async () => { try { await onChangeUrl(value); setSaved(true); setError(null); }
        catch (e) { setError(message(e)); } }} />
      <Label muted size={12} style={{ marginTop: 14 }}>Android emulator: http://10.0.2.2:8080{ '\n' }iOS simulator: http://localhost:8080{ '\n' }Physical device: use your computer’s LAN IP and allow access through your firewall. Use HTTPS for remote servers.</Label>
    </Panel>
    <View style={{ marginTop: 20 }}><Action danger title="Sign out" onPress={() => onLogout()} /></View>
  </ScrollView>;
}
