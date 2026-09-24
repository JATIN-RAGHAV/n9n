import assert from 'node:assert/strict';
import test from 'node:test';
import { addNode, connect, removeNode, validateDraft } from '../src/graph';
import { Draft } from '../src/types';

const empty: Draft = { nodes: [], edges: [] };
const node = (draft: Draft, type: 'manual_trigger' | 'set_fields' | 'condition') =>
  addNode(draft, type, { x: 0, y: draft.nodes.length * 100 });

test('mobile editor can build a trigger-to-action flow', () => {
  let draft = node(empty, 'manual_trigger');
  draft = node(draft, 'set_fields');
  draft = connect(draft, draft.nodes[0].id, draft.nodes[1].id, 'out');
  assert.equal(draft.edges.length, 1);
  assert.equal(validateDraft(draft), null);
});

test('graph operations reject trigger inputs, repeated inputs, and cycles', () => {
  let draft = node(empty, 'manual_trigger');
  draft = node(draft, 'set_fields');
  draft = node(draft, 'condition');
  const [trigger, action, condition] = draft.nodes;
  draft = connect(draft, trigger.id, action.id, 'out');
  draft = connect(draft, action.id, condition.id, 'out');
  assert.throws(() => connect(draft, action.id, trigger.id, 'out'), /Triggers cannot/);
  assert.throws(() => connect(draft, trigger.id, action.id, 'out'), /incoming/);
  assert.throws(() => connect(draft, condition.id, trigger.id, 'true'), /Triggers cannot/);
  assert.throws(() => connect(draft, condition.id, action.id, 'true'), /incoming/);
  assert.equal(removeNode(draft, action.id).edges.length, 0);
});

test('only one trigger can be added and conditions select a branch port', () => {
  const draft = node(empty, 'manual_trigger');
  assert.throws(() => node(draft, 'manual_trigger'), /one trigger/);
  const withActions = node(node(draft, 'condition'), 'set_fields');
  assert.throws(() => connect(withActions, withActions.nodes[1].id, withActions.nodes[2].id, 'out'), /true or false/);
  const connected = connect(withActions, withActions.nodes[1].id, withActions.nodes[2].id, 'false');
  assert.equal(connected.edges[0].source_port, 'false');
});
