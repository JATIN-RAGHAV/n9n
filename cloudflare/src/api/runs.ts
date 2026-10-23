// Runs, steps and replay (backend/runs.go, backend/replay.go).
import type { Ctx } from "./context";
import type { Queryer, Row } from "./db";
import { HttpError, decode, fail, write, withinJSONDepth } from "./http";
import { type Edge, type Graph, storedGraph, triggerTypes } from "./graph";
import { id, jsonText, now } from "./util";
import { checkCredentials, validatedDraft } from "./workflows";

const RUN_COLUMNS = `r.id,r.workflow_id,r.version,r.kind,r.is_test,r.parent_run_id,r.resume_from_node,r.status,r.input,r.error,r.created_at,r.updated_at`;

/** The Run JSON shape, with Go's omitempty fields left out when empty. */
export function runJSON(r: Row): Record<string, unknown> {
  const out: Record<string, unknown> = { id: r.id, workflow_id: r.workflow_id, version: r.version, kind: r.kind };
  if (r.is_test) out.test = true;
  if (r.parent_run_id) out.parent_run_id = r.parent_run_id;
  if (r.resume_from_node) out.resume_from_node = r.resume_from_node;
  out.status = r.status;
  out.input = r.input ?? null;
  if (r.error) out.error = r.error;
  out.created_at = r.created_at;
  out.updated_at = r.updated_at;
  return out;
}

export async function getRun(c: Ctx, runID: string, q: Queryer = c.db.q): Promise<Row | undefined> {
  return c.db.row(`SELECT ${RUN_COLUMNS} FROM runs r WHERE r.id=$1`, [runID], q);
}

async function ownedRun(c: Ctx, runID: string, uid: string): Promise<Row | undefined> {
  return c.db.row(`SELECT ${RUN_COLUMNS} FROM runs r JOIN workflows w ON w.id=r.workflow_id WHERE r.id=$1 AND w.user_id=$2`, [runID, uid]);
}

export function queuedRun(rid: string, wid: string, version: number, input: unknown, t: string, extra: Record<string, unknown> = {}) {
  return runJSON({ id: rid, workflow_id: wid, version, kind: "normal", status: "queued", input, created_at: t, updated_at: t, ...extra });
}

export async function queueRun(c: Ctx, wid: string, version: number, input: unknown) {
  const rid = id();
  const t = now();
  await c.db.exec(`INSERT INTO runs(id,workflow_id,version,status,input,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, [rid, wid, version, "queued", jsonText(input), t, t]);
  return queuedRun(rid, wid, version, input, t);
}

export async function queueTestRun(c: Ctx, q: Queryer, wid: string, version: number, graph: Graph, input: unknown) {
  const rid = id();
  const t = now();
  await c.db.exec(
    `INSERT INTO runs(id,workflow_id,version,status,input,graph_snapshot,is_test,kind,created_at,updated_at) VALUES($1,$2,$3,'queued',$4,$5,TRUE,'test',$6,$6)`,
    [rid, wid, version, jsonText(input), jsonText(graph), t],
    q,
  );
  return queuedRun(rid, wid, version, input, t, { kind: "test", is_test: true });
}

const GRAPH_OF_RUN = `SELECT COALESCE(r.graph_snapshot,v.graph) AS graph FROM runs r LEFT JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version WHERE r.id=$1`;

/** Step rows in the JSON shape of Go's Step, omitting empty optional fields. */
export function stepJSON(r: Row): Record<string, unknown> {
  const out: Record<string, unknown> = { node_id: r.node_id, status: r.status, input: r.input ?? null, output: r.output ?? null };
  if (r.error) out.error = r.error;
  if (r.branch) out.branch = r.branch;
  out.attempt = r.attempt;
  if (r.started_at) out.started_at = r.started_at;
  if (r.finished_at) out.finished_at = r.finished_at;
  if (r.duration_ms !== null && r.duration_ms !== undefined) out.duration_ms = r.duration_ms;
  out.created_at = r.created_at;
  return out;
}

function attemptJSON(r: Row): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (r.step_attempt !== null) out.step_attempt = r.step_attempt;
  out.attempt = r.attempt;
  out.status = r.status;
  if (r.http_status !== null) out.http_status = r.http_status;
  if (r.error) out.error = r.error;
  out.duration_ms = r.duration_ms;
  out.created_at = r.created_at;
  return out;
}

/** Hangs each HTTP attempt off the step row it belongs to (Go: attachAttempts). */
function attachAttempts(steps: Record<string, any>[], attempts: Row[]): void {
  const latest = new Map<string, number>();
  const latestFor = new Map<string, Map<number, number>>();
  steps.forEach((t, i) => {
    latest.set(t.node_id, i);
    if (!latestFor.has(t.node_id)) latestFor.set(t.node_id, new Map());
    latestFor.get(t.node_id)!.set(t.attempt, i);
  });
  for (const a of attempts) {
    let i = latest.get(a.node_id);
    if (a.step_attempt !== null) {
      const j = latestFor.get(a.node_id)?.get(a.step_attempt);
      if (j !== undefined) i = j;
    }
    if (i !== undefined) (steps[i].attempts ??= []).push(attemptJSON(a));
  }
}

export async function runs(c: Ctx, uid: string, p: string[]): Promise<Response> {
  const method = c.req.method;
  if (p.length === 0 && method === "GET") {
    const filter = c.url.searchParams.get("workflow_id") ?? "";
    let q = `SELECT ${RUN_COLUMNS} FROM runs r JOIN workflows w ON w.id=r.workflow_id WHERE w.user_id=$1`;
    const args: unknown[] = [uid];
    if (filter) {
      q += ` AND w.id=$2`;
      args.push(filter);
    }
    q += ` ORDER BY r.created_at DESC LIMIT 200`;
    return write(200, { runs: (await c.db.rows(q, args)).map(runJSON) });
  }
  if (p.length === 0) return fail(404, "not found");
  const run = await ownedRun(c, p[0], uid);
  if (!run) return fail(404, "run not found");
  if (p.length === 1 && method === "GET") {
    const steps = (
      await c.db.rows(
        `SELECT node_id,status,input,output,error,branch,attempt,COALESCE(started_at,'') AS started_at,COALESCE(finished_at,'') AS finished_at,duration_ms,created_at FROM steps WHERE run_id=$1 ORDER BY id`,
        [run.id],
      )
    ).map(stepJSON);
    const attempts = await c.db.rows(
      `SELECT node_id,step_attempt,attempt,status,http_status,error,duration_ms,created_at FROM step_attempts WHERE run_id=$1 ORDER BY id`,
      [run.id],
    );
    attachAttempts(steps, attempts);
    const g = await c.db.row(GRAPH_OF_RUN, [run.id]);
    if (!g || g.graph === null) return fail(500, "workflow snapshot unavailable");
    const graph = storedGraph(g.graph);
    const nodeTypes: Record<string, string> = {};
    for (const n of graph.nodes ?? []) nodeTypes[n.id] = n.type;
    const children = (
      await c.db.rows(`SELECT ${RUN_COLUMNS} FROM runs r JOIN workflows w ON w.id=r.workflow_id WHERE r.parent_run_id=$1 AND w.user_id=$2 ORDER BY r.created_at`, [run.id, uid])
    ).map(runJSON);
    return write(200, { run: runJSON(run), steps, node_types: nodeTypes, graph, children });
  }
  if (p.length === 2 && p[1] === "replay" && method === "POST") return replay(c, uid, run);
  if (p.length === 2 && p[1] === "cancel" && method === "POST") {
    let current = run;
    if (run.status === "queued" || run.status === "running") {
      await c.db.exec(`UPDATE runs SET status='cancelled',lease_token=NULL,lease_until=NULL,updated_at=$1 WHERE id=$2 AND status IN ('queued','running')`, [now(), run.id]);
      current = (await ownedRun(c, run.id, uid)) ?? run;
    }
    return write(200, { run: runJSON(current) });
  }
  return fail(404, "not found");
}

interface Recorded {
  input: unknown;
  output: unknown;
  branch: string;
}

function feederOf(g: Graph, node: string): [string, string] {
  for (const e of g.edges ?? []) if (e.target === node) return [e.source, e.source_port];
  return ["", ""];
}

/** The parent steps a replay reuses, walking the graph as the runner will (Go: replayPlan). */
function replayPlan(g: Graph, from: string, succeeded: Map<string, Recorded>, seeded: boolean): { node: string; branch: string }[] {
  const nodes = new Map((g.nodes ?? []).map((n) => [n.id, n]));
  let root = "";
  for (const n of g.nodes ?? []) if (triggerTypes.has(n.type)) root = n.id;
  if (root === "") throw new HttpError(400, "workflow has no trigger");
  const children = new Map<string, Edge[]>();
  for (const e of g.edges ?? []) children.set(e.source, [...(children.get(e.source) ?? []), e]);
  const pathPort = new Map<string, string>();
  const [feeder, firstPort] = feederOf(g, from);
  for (let n = feeder, port = firstPort; n !== ""; [n, port] = feederOf(g, n)) pathPort.set(n, port);
  const plan: { node: string; branch: string }[] = [];
  const visited = new Set<string>();
  const stack = [root];
  while (stack.length > 0) {
    const nid = stack.pop()!;
    if (nid === from || visited.has(nid)) continue;
    visited.add(nid);
    const rec = succeeded.get(nid);
    if (!rec && !(seeded && nid === feeder)) throw new HttpError(400, `node ${nid} did not complete in the parent run; replay from it or earlier`);
    const node = nodes.get(nid);
    let branch = rec?.branch ?? "";
    if (pathPort.has(nid) && node?.type === "condition") branch = pathPort.get(nid)!;
    plan.push({ node: nid, branch });
    for (const e of children.get(nid) ?? []) {
      if (node?.type === "condition" && e.source_port !== branch) continue;
      stack.push(e.target);
    }
  }
  return plan;
}

async function replay(c: Ctx, uid: string, parent: Row): Promise<Response> {
  const a = await decode<{ from_node: string; input_override: Record<string, unknown> | null; graph: string }>(c.req, {
    from_node: "string",
    input_override: "object",
    graph: "string",
  });
  const which = a.graph || "original";
  if (which !== "original" && which !== "draft") return fail(400, "graph must be original or draft");
  if (!a.from_node) return fail(400, "from_node required");
  if (a.input_override !== null && !withinJSONDepth(a.input_override)) return fail(400, "input_override exceeds nesting limit of 32");
  if (!["succeeded", "failed", "cancelled", "uncertain"].includes(parent.status)) return fail(409, "run is still in progress");
  const run = await c.db.tx(async (q) => {
    let graph: Graph;
    let version: number = parent.version;
    if (which === "draft") {
      [graph, version] = await validatedDraft(c, q, parent.workflow_id, uid);
    } else {
      const g = await c.db.row(GRAPH_OF_RUN, [parent.id], q);
      if (!g || g.graph === null) throw new HttpError(500, "workflow snapshot unavailable");
      graph = storedGraph(g.graph);
    }
    const fromType = (graph.nodes ?? []).filter((n) => n.id === a.from_node).map((n) => n.type).pop() ?? "";
    if (fromType === "") throw new HttpError(400, "from_node is not in the workflow");
    await checkCredentials(c, q, graph, uid);
    const input = a.input_override !== null && triggerTypes.has(fromType) ? a.input_override : (parent.input ?? null);
    const succeeded = new Map<string, Recorded>();
    for (const r of await c.db.rows(`SELECT node_id,input,output,branch FROM steps WHERE run_id=$1 AND status='succeeded' ORDER BY id`, [parent.id], q)) {
      succeeded.set(r.node_id, { input: r.input, output: r.output, branch: r.branch });
    }
    const [feeder] = feederOf(graph, a.from_node);
    const seeded = a.input_override !== null && feeder !== "";
    const plan = replayPlan(graph, a.from_node, succeeded, seeded);
    const rid = id();
    const t = now();
    await c.db.exec(
      `INSERT INTO runs(id,workflow_id,version,status,input,graph_snapshot,kind,parent_run_id,resume_from_node,created_at,updated_at) VALUES($1,$2,$3,'queued',$4,$5,'replay',$6,$7,$8,$8)`,
      [rid, parent.workflow_id, version, jsonText(input), jsonText(graph), parent.id, a.from_node, t],
      q,
    );
    const stepTime = now();
    for (const step of plan) {
      const rec = succeeded.get(step.node);
      const stepInput = rec ? rec.input : {};
      const stepOutput = seeded && step.node === feeder ? a.input_override : rec?.output;
      await c.db.exec(
        `INSERT INTO steps(run_id,node_id,status,input,output,error,branch,attempt,finished_at,created_at) VALUES($1,$2,'succeeded',$3,$4,'',$5,1,$6,$6)`,
        [rid, step.node, jsonText(stepInput), jsonText(stepOutput), step.branch, stepTime],
        q,
      );
    }
    return queuedRun(rid, parent.workflow_id, version, input, t, { kind: "replay", parent_run_id: parent.id, resume_from_node: a.from_node });
  });
  return write(200, { run });
}
