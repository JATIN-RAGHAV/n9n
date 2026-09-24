export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };
export type JsonObject = { [key: string]: Json };

export type NodeType =
  | 'manual_trigger' | 'webhook_trigger' | 'schedule_trigger' | 'email_trigger'
  | 'set_fields' | 'http_request' | 'condition' | 'send_email';
export type Port = 'out' | 'true' | 'false';
export type WorkflowNode = {
  id: string;
  type: NodeType;
  position: { x: number; y: number };
  config: JsonObject;
  credential_id?: string;
};
export type WorkflowEdge = {
  id: string;
  source: string;
  target: string;
  source_port: Port;
};
export type Draft = { nodes: WorkflowNode[]; edges: WorkflowEdge[] };
export type Workflow = {
  id: string;
  name: string;
  draft: Draft;
  published_version?: number | null;
  active: boolean;
  created_at?: string;
  updated_at?: string;
};
export type Run = {
  id: string;
  workflow_id: string;
  status: string;
  error?: string | null;
  version?: number;
  created_at?: string;
  updated_at?: string;
};
export type Step = {
  id?: string;
  node_id: string;
  status: string;
  attempt?: number;
  input?: Json;
  output?: Json;
  error?: string | null;
};
export type Credential = { id: string; name: string; kind: string; created_at?: string };
export type User = { id: string; email: string };
export type Session = { apiUrl: string; token: string; user: User; expiresAt: string };
export type NodeDefinition = {
  type: NodeType;
  name: string;
  category: string;
  description: string;
  input_ports?: string[];
  output_ports?: string[];
};

export const TRIGGERS: NodeType[] = [
  'manual_trigger', 'webhook_trigger', 'schedule_trigger', 'email_trigger',
];
export const NEEDS_GMAIL: NodeType[] = ['email_trigger', 'send_email'];
export const NODE_NAMES: Record<NodeType, string> = {
  manual_trigger: 'Manual trigger', webhook_trigger: 'Webhook trigger',
  schedule_trigger: 'Schedule trigger', email_trigger: 'Gmail trigger',
  set_fields: 'Set fields', http_request: 'HTTP request',
  condition: 'Condition', send_email: 'Send email',
};
export const ALL_TYPES = Object.keys(NODE_NAMES) as NodeType[];

export function defaultConfig(type: NodeType): JsonObject {
  switch (type) {
    case 'schedule_trigger': return { interval_seconds: 300, timezone: 'UTC' };
    case 'webhook_trigger': return { secret: '' };
    case 'email_trigger': return { poll_seconds: 60, query: '' };
    case 'set_fields': return { fields: {} };
    case 'http_request': return { url: '', method: 'GET', headers: {} };
    case 'condition': return { left: '', operator: 'equals', right: '' };
    case 'send_email': return { to: '', subject: '', body: '' };
    default: return {};
  }
}
