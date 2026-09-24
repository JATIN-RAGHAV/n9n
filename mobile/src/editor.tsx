import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { ActivityIndicator, Alert, ScrollView, View } from 'react-native';
import { Api, ApiError } from './api';
import { addNode, connect, removeEdge, removeNode, updateNode, validateDraft } from './graph';
import { Action, Chip, ErrorText, Field, Label, Panel, usePalette } from './theme';
import { ALL_TYPES, Credential, Draft, JsonObject, NEEDS_GMAIL, NODE_NAMES, NodeType, Port, Workflow } from './types';

function describe(error: unknown) { return error instanceof Error ? error.message : String(error); }

export function EditorScreen({ api, workflowId, onRun, onDirtyChange, onUnauthorized }: {
  api: Api; workflowId: string; onRun: (id: string) => void;
  onDirtyChange: (dirty: boolean) => void; onUnauthorized: () => void;
}) {
  const p = usePalette();
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [draft, setDraft] = useState<Draft>({ nodes: [], edges: [] });
  const [name, setName] = useState('');
  const [selected, setSelected] = useState<string | null>(null);
  const [rawConfig, setRawConfig] = useState('{}');
  const [runInput, setRunInput] = useState('{}');
  const [source, setSource] = useState<string | null>(null);
  const [port, setPort] = useState<Port>('out');
  const [target, setTarget] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(true);
  const selectedNode = useMemo(() => draft.nodes.find(n => n.id === selected) ?? null, [draft.nodes, selected]);

  const load = useCallback(async () => {
    try {
      const [result, credentialResult] = await Promise.all([api.workflow(workflowId), api.credentials()]);
      setWorkflow(result.workflow); setName(result.workflow.name); setDraft(result.workflow.draft);
      setCredentials(credentialResult.credentials.filter(item => item.kind === 'gmail_oauth'));
      setSaved(true); setError(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) onUnauthorized(); else setError(describe(e));
    } finally { setLoading(false); }
  }, [api, workflowId, onUnauthorized]);
  useEffect(() => { setLoading(true); load(); }, [load]);
  useEffect(() => { onDirtyChange(!saved); }, [saved, onDirtyChange]);
  useEffect(() => { if (selectedNode) setRawConfig(JSON.stringify(selectedNode.config, null, 2)); }, [selectedNode]);

  const changeDraft = (next: Draft) => { setDraft(next); setSaved(false); setError(null); };
  const add = (type: NodeType) => {
    try {
      const next = addNode(draft, type, { x: 0, y: draft.nodes.length * 100 });
      changeDraft(next); setSelected(next.nodes[next.nodes.length - 1].id);
    } catch (e) { setError(describe(e)); }
  };
  const commitConfig = () => {
    if (!selectedNode) return;
    try {
      const config: unknown = JSON.parse(rawConfig);
      if (config === null || Array.isArray(config) || typeof config !== 'object') throw new Error('Config must be a JSON object.');
      changeDraft(updateNode(draft, { ...selectedNode, config: config as JsonObject }));
    } catch (e) { setError(`Invalid node config: ${describe(e)}`); }
  };
  const save = async () => {
    setSaving(true); setError(null);
    try {
      const result = await api.updateWorkflow(workflowId, name.trim(), draft);
      setWorkflow(result.workflow); setSaved(true);
      return true;
    } catch (e) { if (e instanceof ApiError && e.status === 401) onUnauthorized(); else setError(describe(e)); }
    finally { setSaving(false); }
    return false;
  };
  const publish = async () => {
    const issue = validateDraft(draft);
    if (issue) { setError(issue); return; }
    try {
      if (!saved && !await save()) return;
      const result = await api.publish(workflowId); setWorkflow(result.workflow); setError(null);
      Alert.alert('Published', `Version ${result.workflow.published_version ?? ''} is ready to run.`);
    } catch (e) { if (e instanceof ApiError && e.status === 401) onUnauthorized(); else setError(describe(e)); }
  };
  const activate = async (active: boolean) => {
    try { const result = await api.activate(workflowId, active); setWorkflow(result.workflow); setError(null); }
    catch (e) { if (e instanceof ApiError && e.status === 401) onUnauthorized(); else setError(describe(e)); }
  };
  const run = async () => {
    try {
      if (!workflow?.published_version) { setError('Publish this workflow before running it.'); return; }
      const input: unknown = JSON.parse(runInput);
      if (input === null || Array.isArray(input) || typeof input !== 'object') throw new Error('Run input must be a JSON object.');
      const result = await api.startRun(workflowId, input as JsonObject); onRun(result.run.id);
    } catch (e) { if (e instanceof ApiError && e.status === 401) onUnauthorized(); else setError(describe(e)); }
  };
  const makeConnection = () => {
    if (!source || !target) { setError('Select a source and target node.'); return; }
    try { changeDraft(connect(draft, source, target, port)); setSource(null); setTarget(null); setPort('out'); }
    catch (e) { setError(describe(e)); }
  };
  const sourceNode = draft.nodes.find(n => n.id === source);

  if (loading) return <ActivityIndicator color={p.red} style={{ flex: 1 }} />;
  return <ScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{ padding: 16, paddingBottom: 55 }}>
    <Label size={26} bold>Workflow editor</Label>
    <Label muted size={12} style={{ marginTop: 4, marginBottom: 15 }}>{workflow?.active ? 'Active' : workflow?.published_version ? `Published v${workflow.published_version}` : 'Draft'} · {workflowId.slice(0, 12)}</Label>
    <Field label="Workflow name" value={name} onChangeText={value => { setName(value); setSaved(false); }} placeholder="New workflow" autoCapitalize="sentences" />
    <Panel style={{ marginBottom: 14 }}>
      <Label size={15} bold>Manual run input</Label>
      <Label muted size={12} style={{ marginTop: 4, marginBottom: 10 }}>JSON passed to the trigger. Use an empty object when no values are needed.</Label>
      <Field label="Run input JSON" value={runInput} onChangeText={setRunInput} multiline autoCapitalize="none" />
    </Panel>
    <View style={{ flexDirection: 'row', gap: 8 }}>
      <View style={{ flex: 1 }}><Action title={saving ? 'Saving…' : 'Save draft'} disabled={saving || saved} onPress={save} /></View>
      <View style={{ flex: 1 }}><Action title="Publish" secondary onPress={publish} /></View>
    </View>
    <View style={{ flexDirection: 'row', gap: 8, marginTop: 8 }}>
      <View style={{ flex: 1 }}><Action title={workflow?.active ? 'Deactivate' : 'Activate'} secondary disabled={!workflow?.published_version} onPress={() => activate(!workflow?.active)} /></View>
      <View style={{ flex: 1 }}><Action title="Run now" onPress={run} /></View>
    </View>
    <ErrorText message={error} />

    <Label size={20} bold style={{ marginTop: 25 }}>Add a node</Label>
    <Label muted size={12} style={{ marginTop: 4 }}>Workflows allow one trigger. Tap a node below to configure it.</Label>
    <View style={{ flexDirection: 'row', flexWrap: 'wrap', marginTop: 10 }}>
      {ALL_TYPES.map(type => <Chip key={type} title={NODE_NAMES[type]} selected={false} onPress={() => add(type)} />)}
    </View>

    <Label size={20} bold style={{ marginTop: 23 }}>Nodes · {draft.nodes.length}</Label>
    {draft.nodes.length === 0 && <Panel style={{ marginTop: 10 }}><Label muted>Add a trigger to start building.</Label></Panel>}
    {draft.nodes.map((node, index) => <Panel key={node.id} style={{ marginTop: 9, borderColor: selected === node.id ? p.red : p.border }}>
      <View style={{ flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center' }}>
        <Label size={16} bold>{index + 1}. {NODE_NAMES[node.type]}</Label>
        <Action title="Remove" danger small onPress={() => { changeDraft(removeNode(draft, node.id)); if (selected === node.id) setSelected(null); }} />
      </View>
      <Label muted size={11} style={{ marginTop: 5 }}>{node.id}</Label>
      <View style={{ marginTop: 9 }}><Action title={selected === node.id ? 'Selected' : 'Configure'} secondary small onPress={() => setSelected(node.id)} /></View>
    </Panel>)}

    {selectedNode && <Panel style={{ marginTop: 13 }}>
      <Label size={17} bold>Configure {NODE_NAMES[selectedNode.type]}</Label>
      <Label muted size={12} style={{ marginTop: 5, marginBottom: 12 }}>Edit this node’s JSON config. Use mappings like {'{{input.email}}'} or {'{{nodes.node_id.output}}'} where supported.</Label>
      <Field label="Config JSON" value={rawConfig} onChangeText={setRawConfig} multiline autoCapitalize="none" />
      <Action title="Apply config" onPress={commitConfig} />
      {NEEDS_GMAIL.includes(selectedNode.type) && <View style={{ marginTop: 15 }}>
        <Label size={12} bold>GMAIL CREDENTIAL</Label>
        <Label muted size={12} style={{ marginTop: 4 }}>Choose the saved mailbox used by this node.</Label>
        <View style={{ flexDirection: 'row', flexWrap: 'wrap', marginVertical: 6 }}>
          <Chip title="None" selected={!selectedNode.credential_id} onPress={() => {
            changeDraft({ ...draft, nodes: draft.nodes.map(n => n.id === selectedNode.id ? { ...n, credential_id: undefined } : n) });
          }} />
          {credentials.map(credential => <Chip key={credential.id} title={credential.name}
            selected={selectedNode.credential_id === credential.id} onPress={() => {
              changeDraft({ ...draft, nodes: draft.nodes.map(n => n.id === selectedNode.id ? { ...n, credential_id: credential.id } : n) });
            }} />)}
        </View>
        {credentials.length === 0 && <Label muted size={12}>No Gmail credential found. Add one under Credentials, then reopen this workflow.</Label>}
      </View>}
    </Panel>}

    <Label size={20} bold style={{ marginTop: 25 }}>Connect nodes</Label>
    <Label muted size={12} style={{ marginTop: 4 }}>Choose the source and target. Trigger nodes only appear as sources.</Label>
    <Panel style={{ marginTop: 10 }}>
      <Label size={12} bold>FROM</Label>
      <View style={{ flexDirection: 'row', flexWrap: 'wrap', marginVertical: 5 }}>
        {draft.nodes.map(node => <Chip key={node.id} title={NODE_NAMES[node.type]} selected={source === node.id}
          onPress={() => { setSource(node.id); setPort(node.type === 'condition' ? 'true' : 'out'); }} />)}
      </View>
      {sourceNode?.type === 'condition' && <>
        <Label size={12} bold>CONDITION OUTPUT</Label>
        <View style={{ flexDirection: 'row', marginVertical: 5 }}>
          {(['true', 'false'] as const).map(value => <Chip key={value} title={value.toUpperCase()} selected={port === value} onPress={() => setPort(value)} />)}
        </View>
      </>}
      <Label size={12} bold style={{ marginTop: 7 }}>TO</Label>
      <View style={{ flexDirection: 'row', flexWrap: 'wrap', marginVertical: 5 }}>
        {draft.nodes.filter(n => !['manual_trigger', 'webhook_trigger', 'schedule_trigger', 'email_trigger'].includes(n.type))
          .map(node => <Chip key={node.id} title={NODE_NAMES[node.type]} selected={target === node.id} onPress={() => setTarget(node.id)} />)}
      </View>
      <Action title="Connect selected nodes" onPress={makeConnection} />
    </Panel>

    <Label size={20} bold style={{ marginTop: 25 }}>Connections · {draft.edges.length}</Label>
    {draft.edges.map(edge => {
      const from = draft.nodes.find(n => n.id === edge.source); const to = draft.nodes.find(n => n.id === edge.target);
      return <Panel key={edge.id} style={{ marginTop: 8 }}>
        <Label bold>{from ? NODE_NAMES[from.type] : edge.source} → {to ? NODE_NAMES[to.type] : edge.target}</Label>
        <Label muted size={12} style={{ marginTop: 4 }}>Output: {edge.source_port}</Label>
        <View style={{ marginTop: 9 }}><Action title="Remove connection" danger small onPress={() => changeDraft(removeEdge(draft, edge.id))} /></View>
      </Panel>;
    })}
    {draft.edges.length === 0 && <Label muted style={{ marginTop: 8 }}>No connections yet.</Label>}

  </ScrollView>;
}
