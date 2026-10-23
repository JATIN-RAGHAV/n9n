// Workflow CRUD, publish, activate, run and draft test (backend/workflows.go).
import type { Ctx } from "./context";
import type { Queryer, Row } from "./db";
import { HttpError, byteLen, decode, decodeStruct, fail, readJSON, write, withinJSONDepth } from "./http";
import { type Graph, emptyGraph, graphField, storedGraph, validateDraft, validatePublish } from "./graph";
import { id, jsonText, now } from "./util";
import { queueRun, queueTestRun } from "./runs";

const WORKFLOW_COLUMNS = `id,name,draft,published_version,active,created_at,updated_at`;

function workflowJSON(r: Row) {
  const draft = storedGraph(r.draft);
  return {
    id: r.id,
    name: r.name,
    draft: { nodes: draft.nodes ?? [], edges: draft.edges ?? [] },
    published_version: r.published_version ?? null,
    active: !!r.active,
    created_at: r.created_at,
    updated_at: r.updated_at,
  };
}

async function loadWorkflow(c: Ctx, wid: string, uid: string) {
  const r = await c.db.row(`SELECT ${WORKFLOW_COLUMNS} FROM workflows WHERE id=$1 AND user_id=$2`, [wid, uid]);
  return r ? workflowJSON(r) : undefined;
}

/** Confirms every credential a graph references belongs to the user. */
export async function checkCredentials(c: Ctx, q: Queryer, graph: Graph, uid: string): Promise<void> {
  for (const node of graph.nodes ?? []) {
    if (!node.credential_id) continue;
    const r = await c.db.row(`SELECT count(*) AS n FROM credentials WHERE id=$1 AND user_id=$2`, [node.credential_id, uid], q);
    if (Number(r?.n ?? 0) === 0) throw new HttpError(400, "credential not found: " + node.credential_id);
  }
}

/** The owner's saved draft, validated like publish, and the version a snapshot run reports. */
export async function validatedDraft(c: Ctx, q: Queryer, wid: string, uid: string): Promise<[Graph, number]> {
  const r = await c.db.row(`SELECT draft,published_version FROM workflows WHERE id=$1 AND user_id=$2 FOR SHARE`, [wid, uid], q);
  if (!r) throw new HttpError(404, "workflow not found");
  const graph = storedGraph(r.draft);
  validatePublish(graph);
  return [graph, r.published_version ?? 0];
}

function runInput(v: unknown): Record<string, unknown> {
  const a = decodeStruct<{ input: Record<string, unknown> | null }>(v, { input: "object" });
  const input = a.input ?? {};
  if (!withinJSONDepth(input)) throw new HttpError(400, "run input exceeds nesting limit of 32");
  return input;
}

export async function workflows(c: Ctx, uid: string, p: string[]): Promise<Response> {
  const method = c.req.method;
  if (p.length === 0) {
    if (method === "GET") {
      const rows = await c.db.rows(`SELECT ${WORKFLOW_COLUMNS} FROM workflows WHERE user_id=$1 ORDER BY updated_at DESC`, [uid]);
      return write(200, { workflows: rows.map(workflowJSON) });
    }
    if (method === "POST") {
      const body = decodeStruct<{ name: string; draft: unknown }>(await readJSON(c.req), { name: "string", draft: "any" });
      let name = body.name.trim();
      if (name === "") name = "Untitled workflow";
      if (byteLen(name) > 200) return fail(400, "name too long");
      const g = graphField(body.draft) ?? emptyGraph();
      validateDraft(g);
      const wid = id();
      const t = now();
      await c.db.exec(`INSERT INTO workflows(id,user_id,name,draft,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6)`, [wid, uid, name, jsonText(g), t, t]);
      return write(200, { workflow: await loadWorkflow(c, wid, uid) });
    }
    return fail(404, "not found");
  }
  const x = await loadWorkflow(c, p[0], uid);
  if (!x) return fail(404, "workflow not found");
  if (p.length === 1) {
    if (method === "GET") return write(200, { workflow: x });
    if (method === "PUT") {
      const body = decodeStruct<{ name: string; draft: unknown }>(await readJSON(c.req), { name: "string", draft: "any" });
      const name = body.name.trim();
      if (name === "" || byteLen(name) > 200) return fail(400, "invalid name");
      const g = graphField(body.draft) ?? { nodes: null, edges: null };
      validateDraft(g);
      await c.db.exec(`UPDATE workflows SET name=$1,draft=$2,updated_at=$3 WHERE id=$4`, [name, jsonText(g), now(), x.id]);
      return write(200, { workflow: await loadWorkflow(c, x.id, uid) });
    }
    if (method === "DELETE") {
      await c.db.exec(`DELETE FROM workflows WHERE id=$1`, [x.id]);
      return write(200, { ok: true });
    }
  }
  if (p.length === 2 && method === "POST") {
    switch (p[1]) {
      case "test": {
        const run = await c.db.tx(async (q) => {
          const [graph, version] = await validatedDraft(c, q, x.id, uid);
          const input = runInput(await readJSON(c.req));
          await checkCredentials(c, q, graph, uid);
          return queueTestRun(c, q, x.id, version, graph, input);
        });
        return write(200, { run });
      }
      case "publish": {
        await c.db.tx(async (q) => {
          const r = await c.db.row(`SELECT draft,published_version FROM workflows WHERE id=$1 AND user_id=$2 FOR UPDATE`, [x.id, uid], q);
          if (!r) throw new HttpError(404, "workflow not found");
          const draft = storedGraph(r.draft);
          validatePublish(draft);
          await checkCredentials(c, q, draft, uid);
          const v = r.published_version === null ? 1 : r.published_version + 1;
          await c.db.exec(`INSERT INTO versions VALUES($1,$2,$3)`, [x.id, v, jsonText(draft)], q);
          await c.db.exec(`UPDATE workflows SET published_version=$1,updated_at=$2 WHERE id=$3`, [v, now(), x.id], q);
        });
        return write(200, { workflow: await loadWorkflow(c, x.id, uid) });
      }
      case "activate": {
        const a = await decode<{ active: boolean }>(c.req, { active: "bool" });
        if (a.active && x.published_version === null) return fail(400, "publish before activation");
        await c.db.exec(`UPDATE workflows SET active=$1,updated_at=$2 WHERE id=$3`, [a.active, now(), x.id]);
        return write(200, { workflow: await loadWorkflow(c, x.id, uid) });
      }
      case "run": {
        if (x.published_version === null) return fail(400, "publish before running");
        const input = runInput(await readJSON(c.req));
        return write(200, { run: await queueRun(c, x.id, x.published_version, input) });
      }
    }
  }
  return fail(404, "not found");
}
