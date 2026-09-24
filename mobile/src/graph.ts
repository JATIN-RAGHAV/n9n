import { Draft, NodeType, Port, TRIGGERS, WorkflowEdge, WorkflowNode, defaultConfig } from './types';

export function nodeId(): string {
  return `node_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

export function addNode(draft: Draft, type: NodeType, position: { x: number; y: number }): Draft {
  if (TRIGGERS.includes(type) && draft.nodes.some(n => TRIGGERS.includes(n.type))) {
    throw new Error('A workflow can have one trigger. Remove the existing trigger first.');
  }
  return {
    ...draft,
    nodes: [...draft.nodes, { id: nodeId(), type, position, config: defaultConfig(type) }],
  };
}

export function removeNode(draft: Draft, id: string): Draft {
  return { nodes: draft.nodes.filter(n => n.id !== id),
    edges: draft.edges.filter(e => e.source !== id && e.target !== id) };
}

export function moveNode(draft: Draft, id: string, x: number, y: number): Draft {
  return { ...draft, nodes: draft.nodes.map(n => n.id === id
    ? { ...n, position: { x: Math.max(0, Math.round(x)), y: Math.max(0, Math.round(y)) } }
    : n) };
}

export function updateNode(draft: Draft, node: WorkflowNode): Draft {
  return { ...draft, nodes: draft.nodes.map(n => n.id === node.id ? node : n) };
}

export function connect(draft: Draft, source: string, target: string, sourcePort: Port): Draft {
  const from = draft.nodes.find(n => n.id === source);
  const to = draft.nodes.find(n => n.id === target);
  if (!from || !to) throw new Error('Select two nodes in this workflow.');
  if (source === target) throw new Error('A node cannot connect to itself.');
  if (TRIGGERS.includes(to.type)) throw new Error('Triggers cannot have incoming connections.');
  if (sourcePort !== 'out' && from.type !== 'condition') {
    throw new Error('Only conditions have true and false outputs.');
  }
  if (sourcePort === 'out' && from.type === 'condition') {
    throw new Error('Choose the true or false output of a condition.');
  }
  if (draft.edges.some(e => e.target === target)) {
    throw new Error('This node already has an incoming connection.');
  }
  const reaches = (start: string, goal: string, seen = new Set<string>()): boolean => {
    if (start === goal) return true;
    if (seen.has(start)) return false;
    seen.add(start);
    return draft.edges.filter(e => e.source === start)
      .some(e => reaches(e.target, goal, seen));
  };
  if (reaches(target, source)) throw new Error('That connection would create a cycle.');
  const edge: WorkflowEdge = { id: `edge_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`,
    source, target, source_port: sourcePort };
  return { ...draft, edges: [...draft.edges, edge] };
}

export function removeEdge(draft: Draft, id: string): Draft {
  return { ...draft, edges: draft.edges.filter(e => e.id !== id) };
}

export function validateDraft(draft: Draft): string | null {
  const triggers = draft.nodes.filter(n => TRIGGERS.includes(n.type));
  if (triggers.length !== 1) return 'Add exactly one trigger before publishing.';
  if (draft.nodes.some(n => !TRIGGERS.includes(n.type) && !draft.edges.some(e => e.target === n.id))) {
    return 'Connect every action to an upstream node.';
  }
  return null;
}
