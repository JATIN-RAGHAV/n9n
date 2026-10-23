// Workflow graphs: decoding, normalisation and validation
// (backend/main.go types, backend/workflows.go validateDraft/validatePublish).
import { HttpError, byteLen, decodeStruct, field, isObject, withinJSONDepth } from "./http";

export interface Node {
  id: string;
  type: string;
  position: Record<string, number> | null;
  config: Record<string, unknown> | null;
  credential_id?: string;
}
export interface Edge {
  id: string;
  source: string;
  target: string;
  source_port: string;
}
export interface Graph {
  nodes: Node[] | null;
  edges: Edge[] | null;
}

export const nodeTypes = new Set([
  "manual_trigger",
  "webhook_trigger",
  "schedule_trigger",
  "email_trigger",
  "set_fields",
  "http_request",
  "condition",
  "send_email",
]);
export const triggerTypes = new Set(["manual_trigger", "webhook_trigger", "schedule_trigger", "email_trigger"]);

export const emptyGraph = (): Graph => ({ nodes: [], edges: [] });

function decodeNode(v: unknown): Node {
  const n = decodeStruct<{ id: string; type: string; position: unknown; config: unknown; credential_id: string }>(v, {
    id: "string",
    type: "string",
    position: "object",
    config: "object",
    credential_id: "string",
  });
  let position: Record<string, number> | null = null;
  if (n.position) {
    position = {};
    for (const [k, x] of Object.entries(n.position as Record<string, unknown>)) {
      if (x !== null && typeof x !== "number") throw new HttpError(400, "json: cannot unmarshal into Go struct field Node.nodes.position of type float64");
      position[k] = (x as number | null) ?? 0;
    }
  }
  const node: Node = { id: n.id, type: n.type, position, config: (n.config as Record<string, unknown>) ?? null };
  if (n.credential_id) node.credential_id = n.credential_id;
  return node;
}

function decodeEdge(v: unknown): Edge {
  return decodeStruct<Edge>(v, { id: "string", source: "string", target: "string", source_port: "string" });
}

function decodeList<T>(name: string, v: unknown, one: (x: unknown) => T): T[] | null {
  if (v === null || v === undefined) return null;
  if (!Array.isArray(v)) throw new HttpError(400, `json: cannot unmarshal into Go struct field Graph.${name}`);
  return v.map(one);
}

/** Decodes a request's graph strictly, as encoding/json does into Graph. */
export function decodeGraph(v: unknown): Graph {
  const g = decodeStruct<{ nodes: unknown; edges: unknown }>(v, { nodes: "any", edges: "any" });
  return { nodes: decodeList("nodes", g.nodes, decodeNode), edges: decodeList("edges", g.edges, decodeEdge) };
}

/** Reads a stored graph leniently, the way json.Unmarshal fills Graph from a database row. */
export function storedGraph(v: unknown): Graph {
  const g = isObject(v) ? v : {};
  const nodes = Array.isArray(g.nodes)
    ? g.nodes.map((raw): Node => {
        const n = isObject(raw) ? raw : {};
        const node: Node = {
          id: typeof n.id === "string" ? n.id : "",
          type: typeof n.type === "string" ? n.type : "",
          position: isObject(n.position) ? (n.position as Record<string, number>) : null,
          config: isObject(n.config) ? n.config : null,
        };
        if (typeof n.credential_id === "string" && n.credential_id !== "") node.credential_id = n.credential_id;
        return node;
      })
    : null;
  const edges = Array.isArray(g.edges)
    ? g.edges.map((raw): Edge => {
        const e = isObject(raw) ? raw : {};
        const s = (k: string) => (typeof e[k] === "string" ? (e[k] as string) : "");
        return { id: s("id"), source: s("source"), target: s("target"), source_port: s("source_port") };
      })
    : null;
  return { nodes, edges };
}

const nodeIDPattern = /^[A-Za-z0-9_-]+$/;
// Go's RE2 \s is [\t\n\f\r ]; spelled out so both servers accept the same text.
const ws = "[\\t\\n\\f\\r ]";
const mappingSource = `\\{\\{${ws}*(input|nodes)\\.([A-Za-z0-9_-]+)(?:\\.([A-Za-z0-9_.-]+))?${ws}*\\}\\}`;

export function validateDraft(g: Graph): void {
  const nodes = g.nodes ?? [];
  const edges = g.edges ?? [];
  if (nodes.length > 100 || edges.length > 300) throw new HttpError(400, "graph too large");
  const ids = new Set<string>();
  for (const n of nodes) {
    if (n.id === "" || byteLen(n.id) > 100 || !nodeIDPattern.test(n.id) || ids.has(n.id)) throw new HttpError(400, "invalid or duplicate node ID");
    ids.add(n.id);
    if (!nodeTypes.has(n.type)) throw new HttpError(400, `unknown node type: ${n.type}`);
    if (n.config === null) throw new HttpError(400, `node ${n.id} requires config`);
    if (!withinJSONDepth(n.config)) throw new HttpError(400, `node ${n.id} config exceeds nesting limit of 32`);
  }
  const seen = new Set<string>();
  for (const e of edges) {
    if (e.id === "" || seen.has(e.id) || !ids.has(e.source) || !ids.has(e.target)) throw new HttpError(400, "invalid edge");
    seen.add(e.id);
    if (e.source_port !== "out" && e.source_port !== "true" && e.source_port !== "false") throw new HttpError(400, "invalid source port");
  }
}

const isNumber = (v: unknown): v is number => typeof v === "number";

export function validatePublish(g: Graph): void {
  validateDraft(g);
  const nodes = g.nodes ?? [];
  const edges = g.edges ?? [];
  const byID = new Map<string, Node>();
  const indeg = new Map<string, number>();
  const out = new Map<string, string[]>();
  let triggers = 0;
  let root = "";
  for (const n of nodes) {
    byID.set(n.id, n);
    indeg.set(n.id, 0);
    if (triggerTypes.has(n.type)) {
      triggers++;
      root = n.id;
    }
    const c = n.config ?? {};
    switch (n.type) {
      case "schedule_trigger": {
        const v = c.interval_seconds;
        if (!isNumber(v) || v < 10 || !Number.isInteger(v)) throw new HttpError(400, "schedule interval_seconds must be integer >= 10");
        if (typeof c.timezone === "string" && c.timezone !== "UTC") throw new HttpError(400, "only UTC schedules supported");
        break;
      }
      case "webhook_trigger":
        if (typeof c.secret !== "string" || c.secret === "") throw new HttpError(400, "webhook secret required");
        break;
      case "email_trigger":
        if (!n.credential_id) throw new HttpError(400, "email node requires credential");
        if ("poll_seconds" in c) {
          const p = c.poll_seconds;
          if (!isNumber(p) || p < 30 || !Number.isInteger(p)) throw new HttpError(400, "poll_seconds must be integer >= 30");
        }
        break;
      case "send_email":
        if (!n.credential_id) throw new HttpError(400, "email node requires credential");
        for (const key of ["to", "subject", "body"]) {
          if (typeof c[key] !== "string") throw new HttpError(400, `send_email requires ${key}`);
        }
        break;
      case "set_fields":
        if (!isObject(c.fields)) throw new HttpError(400, "set_fields requires fields object");
        break;
      case "http_request": {
        if (typeof c.url !== "string" || c.url === "") throw new HttpError(400, "http_request requires url");
        const m = typeof c.method === "string" ? c.method : "";
        if (m === "") throw new HttpError(400, "http_request requires method");
        if (!["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"].includes(m)) throw new HttpError(400, "unsupported HTTP method");
        if ("idempotency_key" in c) {
          const key = c.idempotency_key;
          if (typeof key !== "string" || key.trim() === "") throw new HttpError(400, "idempotency_key must be a nonempty string");
        }
        break;
      }
      case "condition": {
        const op = typeof c.operator === "string" ? c.operator : "";
        if (!("left" in c)) throw new HttpError(400, "condition requires left");
        if (op !== "exists" && !("right" in c)) throw new HttpError(400, "condition requires right");
        if (!["equals", "contains", "greater_than", "exists"].includes(op)) throw new HttpError(400, "invalid condition operator");
        break;
      }
    }
  }
  if (triggers !== 1) throw new HttpError(400, "workflow must have exactly one trigger");
  for (const e of edges) {
    if (triggerTypes.has(byID.get(e.target)?.type ?? "")) throw new HttpError(400, "trigger cannot have incoming edge");
    if (byID.get(e.source)?.type === "condition") {
      if (e.source_port !== "true" && e.source_port !== "false") throw new HttpError(400, "condition requires true or false port");
    } else if (e.source_port !== "out") throw new HttpError(400, "non-condition requires out port");
    indeg.set(e.target, (indeg.get(e.target) ?? 0) + 1);
    if ((indeg.get(e.target) ?? 0) > 1) throw new HttpError(400, "nodes may have at most one incoming edge");
    out.set(e.source, [...(out.get(e.source) ?? []), e.target]);
  }
  const seen = new Set<string>();
  const visit = (n: string): void => {
    if (seen.has(n)) throw new HttpError(400, "cycle detected");
    seen.add(n);
    for (const t of out.get(n) ?? []) visit(t);
  };
  visit(root);
  if (seen.size !== byID.size) throw new HttpError(400, "all nodes must be reachable from trigger");
  for (const n of nodes) {
    const ancestors = new Set<string>();
    const collect = (target: string): void => {
      for (const e of edges) {
        if (e.target === target && !ancestors.has(e.source)) {
          ancestors.add(e.source);
          collect(e.source);
        }
      }
    };
    collect(n.id);
    try {
      validateMappings(n.config, ancestors);
    } catch (e) {
      if (e instanceof HttpError) throw new HttpError(400, `node ${n.id}: ${e.message}`);
      throw e;
    }
  }
}

function validateMappings(value: unknown, ancestors: Set<string>): void {
  if (typeof value === "string") {
    const clean = value.replace(new RegExp(mappingSource, "g"), "");
    if (clean.includes("{{") || clean.includes("}}")) throw new HttpError(400, "invalid mapping expression");
    for (const m of value.matchAll(new RegExp(mappingSource, "g"))) {
      if (m[1] === "nodes" && !ancestors.has(m[2])) throw new HttpError(400, `mapping references a node that is not upstream: ${m[2]}`);
    }
  } else if (Array.isArray(value)) value.forEach((v) => validateMappings(v, ancestors));
  else if (isObject(value)) Object.values(value).forEach((v) => validateMappings(v, ancestors));
}

/** Request field holding a graph: decodes with Graph's rules, or null when absent. */
export function graphField(v: unknown): Graph | null {
  if (v === null || v === undefined) return null;
  return decodeGraph(field("draft", "object", v));
}
