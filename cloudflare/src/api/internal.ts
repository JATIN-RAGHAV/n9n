// Runner endpoints: job claims, leases, steps, attempts, triggers
// (backend/internal.go). Authenticated with RUNNER_TOKEN as a bearer token.
import type { Ctx } from "./context";
import type { Queryer } from "./db";
import { HttpError, byteLen, decode, decodeStruct, fail, readJSON, write, withinJSONDepth } from "./http";
import { storedGraph, triggerTypes } from "./graph";
import { equalSecret, id, isRFC3339, jsonText, now, timeAfter, truncateUTF8 } from "./util";
import { credentialForJob, credentialForTrigger } from "./credentials";
import { recordEvent } from "./catalog";
import { stepJSON } from "./runs";

export async function internal(c: Ctx, p: string[]): Promise<Response> {
  if (!equalSecret(c.req.headers.get("Authorization") ?? "", "Bearer " + c.env.RUNNER_TOKEN)) return fail(401, "runner authentication required");
  if (p.length === 0) return fail(404, "not found");
  if (p[0] === "jobs") return jobs(c, p.slice(1));
  if (p[0] === "triggers") return triggers(c, p.slice(1));
  return fail(404, "not found");
}

const LEASED_GRAPH = `SELECT COALESCE(r.graph_snapshot,v.graph) AS graph FROM runs r LEFT JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version WHERE r.id=$1 AND r.status='running' AND r.lease_token=$2 AND r.lease_until>$3 FOR UPDATE OF r`;

/** Locks a run whose lease is still held and checks the node belongs to its graph. */
async function leasedNode(c: Ctx, q: Queryer, runID: string, lease: string, nodeID: string): Promise<void> {
  const r = await c.db.row(LEASED_GRAPH, [runID, lease, now()], q);
  if (!r) throw new HttpError(409, "lease expired");
  if (!(storedGraph(r.graph).nodes ?? []).some((n) => n.id === nodeID)) throw new HttpError(400, "node not in workflow");
}

async function jobs(c: Ctx, p: string[]): Promise<Response> {
  const method = c.req.method;
  if (p.length === 1 && p[0] === "claim" && method === "POST") {
    let runner = "";
    try {
      runner = (await decode<{ runner_id: string }>(c.req, { runner_id: "string" })).runner_id;
    } catch {
      runner = "";
    }
    if (!runner) return fail(400, "runner_id required");
    try {
      return write(200, { job: await claim(c, runner) });
    } catch {
      return fail(500, "claim error");
    }
  }
  if (p.length < 2) return fail(404, "not found");
  const runID = p[0];
  if (p.length === 3 && p[1] === "credentials" && method === "GET") return credentialForJob(c, runID, p[2], c.req.headers.get("X-Lease-Token") ?? "");
  if (method !== "POST" || p.length !== 2) return fail(404, "not found");
  switch (p[1]) {
    case "heartbeat": {
      const a = await decode<{ lease_token: string }>(c.req, { lease_token: "string" });
      const n = await c.db.exec(
        `UPDATE runs SET lease_until=$1,updated_at=$2 WHERE id=$3 AND status='running' AND lease_token=$4 AND lease_until>$5`,
        [timeAfter(60), now(), runID, a.lease_token, now()],
      );
      if (n === 0) return fail(409, "lease expired");
      return write(200, { ok: true });
    }
    case "steps": {
      const a = await decode<{
        lease_token: string;
        node_id: string;
        status: string;
        input: unknown;
        output: unknown;
        error: string;
        branch: string;
        attempt: number;
        started_at: string;
        duration_ms: number | null;
      }>(
        c.req,
        { lease_token: "string", node_id: "string", status: "string", input: "any", output: "any", error: "string", branch: "string", attempt: "int", started_at: "string", duration_ms: "intPtr" },
        4 << 20,
      );
      if (a.node_id === "" || !["running", "succeeded", "failed", "skipped"].includes(a.status) || a.attempt < 0 || a.attempt > 100) return fail(400, "invalid step");
      const attempt = a.attempt === 0 ? 1 : a.attempt;
      if (a.branch !== "" && a.branch !== "true" && a.branch !== "false") return fail(400, "invalid branch");
      let startedAt: string | null = null;
      if (a.started_at !== "") {
        if (!isRFC3339(a.started_at)) return fail(400, "invalid started_at");
        startedAt = a.started_at;
      }
      if (a.duration_ms !== null && (a.duration_ms < 0 || a.duration_ms > 7 * 24 * 3600 * 1000)) return fail(400, "invalid duration_ms");
      const finishedAt = a.status !== "running" ? now() : null;
      await c.db.tx(async (q) => {
        await leasedNode(c, q, runID, a.lease_token, a.node_id);
        await c.db.exec(
          `INSERT INTO steps(run_id,node_id,status,input,output,error,branch,attempt,started_at,finished_at,duration_ms,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
          [runID, a.node_id, a.status, jsonText(a.input), jsonText(a.output), a.error, a.branch, attempt, startedAt, finishedAt, a.duration_ms, now()],
          q,
        );
      });
      return write(200, { ok: true });
    }
    case "attempts": {
      const a = await decode<{
        lease_token: string;
        node_id: string;
        step_attempt: number | null;
        attempt: number;
        status: string;
        http_status: number | null;
        error: string;
        duration_ms: number;
      }>(c.req, { lease_token: "string", node_id: "string", step_attempt: "intPtr", attempt: "int", status: "string", http_status: "intPtr", error: "string", duration_ms: "int" });
      if (a.node_id === "" || (a.status !== "succeeded" && a.status !== "failed") || a.attempt < 1 || a.attempt > 100 || a.duration_ms < 0 || a.duration_ms > 7 * 24 * 3600 * 1000) {
        return fail(400, "invalid attempt");
      }
      if (a.step_attempt !== null && (a.step_attempt < 1 || a.step_attempt > 100)) return fail(400, "invalid step_attempt");
      if (a.http_status !== null && (a.http_status < 100 || a.http_status > 599)) return fail(400, "invalid http_status");
      const error = truncateUTF8(a.error, 4096);
      await c.db.tx(async (q) => {
        await leasedNode(c, q, runID, a.lease_token, a.node_id);
        // A retried report repeats the same step attempt and attempt number; keep the first copy.
        await c.db.exec(
          `INSERT INTO step_attempts(run_id,node_id,step_attempt,attempt,status,http_status,error,duration_ms,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (run_id,node_id,step_attempt,attempt) WHERE step_attempt IS NOT NULL DO NOTHING`,
          [runID, a.node_id, a.step_attempt, a.attempt, a.status, a.http_status, error, a.duration_ms, now()],
          q,
        );
      });
      return write(200, { ok: true });
    }
    case "complete": {
      const a = await decode<{ lease_token: string; status: string; error: string }>(c.req, { lease_token: "string", status: "string", error: "string" });
      if (!["succeeded", "failed", "uncertain"].includes(a.status)) return fail(400, "invalid status");
      const n = await c.db.exec(
        `UPDATE runs SET status=$1,error=$2,lease_token=NULL,lease_until=NULL,updated_at=$3 WHERE id=$4 AND status='running' AND lease_token=$5 AND lease_until>$6`,
        [a.status, a.error, now(), runID, a.lease_token, now()],
      );
      if (n === 0) return fail(409, "lease expired");
      return write(200, { ok: true });
    }
  }
  return fail(404, "not found");
}

/** Recovers expired leases, then leases the oldest queued run (Go: claim). */
async function claim(c: Ctx, runner: string) {
  return c.db.tx(async (q) => {
    const expired = await c.db.rows(`SELECT id FROM runs WHERE status='running' AND lease_until<$1 FOR UPDATE SKIP LOCKED`, [now()], q);
    for (const { id: rid } of expired) {
      const g = await c.db.row(`SELECT COALESCE(r.graph_snapshot,v.graph) AS graph FROM runs r LEFT JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version WHERE r.id=$1`, [rid], q);
      if (!g) throw new Error("run vanished");
      const graph = storedGraph(g.graph);
      const latest = new Map<string, string>();
      for (const s of await c.db.rows(`SELECT node_id,status FROM steps WHERE run_id=$1 ORDER BY id`, [rid], q)) latest.set(s.node_id, s.status);
      let unsafe = false;
      for (const n of graph.nodes ?? []) {
        const st = latest.get(n.id);
        if (st !== "running" && st !== "failed") continue;
        if (n.type === "send_email") unsafe = true;
        if (n.type === "http_request") {
          const m = typeof n.config?.method === "string" && n.config.method !== "" ? (n.config.method as string) : "GET";
          const key = typeof n.config?.idempotency_key === "string" ? (n.config.idempotency_key as string) : "";
          const upper = m.toUpperCase();
          if (!(upper === "GET" || upper === "HEAD" || key !== "")) unsafe = true;
        }
      }
      await c.db.exec(
        `UPDATE runs SET status=$1,error=$2,lease_token=NULL,lease_until=NULL,updated_at=$3 WHERE id=$4`,
        [unsafe ? "uncertain" : "queued", unsafe ? "lease expired during non-idempotent action" : "", now(), rid],
        q,
      );
    }
    const r = await c.db.row(
      `SELECT r.id,r.workflow_id,r.version,COALESCE(r.graph_snapshot,v.graph) AS graph,r.input,r.is_test,r.kind FROM runs r LEFT JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version WHERE r.status='queued' ORDER BY r.created_at LIMIT 1 FOR UPDATE OF r SKIP LOCKED`,
      [],
      q,
    );
    if (!r) return null;
    const lease = id() + id();
    await c.db.exec(`UPDATE runs SET status='running',lease_token=$1,lease_until=$2,runner_id=$3,updated_at=$4 WHERE id=$5`, [lease, timeAfter(60), runner, now(), r.id], q);
    const steps = (await c.db.rows(`SELECT node_id,status,input,output,error,branch,attempt,created_at FROM steps WHERE run_id=$1 ORDER BY id`, [r.id], q)).map((s) =>
      stepJSON({ ...s, started_at: "", finished_at: "", duration_ms: null }),
    );
    return { id: r.id, lease_token: lease, workflow_id: r.workflow_id, version: r.version, test: r.is_test, kind: r.kind, graph: storedGraph(r.graph), input: r.input ?? null, steps };
  });
}

async function triggers(c: Ctx, p: string[]): Promise<Response> {
  const method = c.req.method;
  if (p.length === 0 && method === "GET") {
    const rows = await c.db.rows(
      `SELECT w.id,w.published_version,v.graph,cp.value AS checkpoint FROM workflows w JOIN versions v ON v.workflow_id=w.id AND v.version=w.published_version LEFT JOIN checkpoints cp ON cp.workflow_id=w.id AND cp.version=w.published_version WHERE w.active=TRUE`,
    );
    const out: unknown[] = [];
    for (const t of rows) {
      const node = (storedGraph(t.graph).nodes ?? []).find((n) => triggerTypes.has(n.type));
      if (node) out.push({ workflow_id: t.id, version: t.published_version, node, checkpoint: t.checkpoint ?? null });
    }
    return write(200, { triggers: out });
  }
  if (p.length === 3 && p[1] === "credentials" && method === "GET") {
    const raw = c.url.searchParams.get("version") ?? "";
    if (!/^[+-]?\d+$/.test(raw)) return fail(400, "version required");
    return credentialForTrigger(c, p[0], p[2], Number(raw));
  }
  if (p.length !== 2 || method !== "POST") return fail(404, "not found");
  const wid = p[0];
  const a = decodeStruct<{ version: number; event_id: string; input: Record<string, unknown> | null; checkpoint: unknown }>(await readJSON(c.req), {
    version: "int",
    event_id: "string",
    input: "object",
    checkpoint: "any",
  });
  if (a.version <= 0) return fail(400, "version required");
  const cur = await c.db.row(`SELECT published_version FROM workflows WHERE id=$1 AND active=TRUE`, [wid]);
  if (!cur || cur.published_version !== a.version) return fail(409, "inactive or stale trigger");
  if (p[1] === "checkpoint") {
    await c.db.tx(async (q) => {
      const locked = await c.db.row(`SELECT published_version FROM workflows WHERE id=$1 AND active=TRUE FOR UPDATE`, [wid], q);
      if (!locked || locked.published_version !== a.version) throw new HttpError(409, "inactive or stale trigger");
      await c.db.exec(`INSERT INTO checkpoints VALUES($1,$2,$3) ON CONFLICT(workflow_id,version) DO UPDATE SET value=excluded.value`, [wid, a.version, jsonText(a.checkpoint)], q);
    });
    return write(200, { ok: true });
  }
  if (p[1] !== "events") return fail(404, "not found");
  if (a.event_id === "" || byteLen(a.event_id) > 500) return fail(400, "event_id required");
  const input = a.input ?? {};
  if (!withinJSONDepth(input) || !withinJSONDepth(a.checkpoint)) return fail(400, "trigger event exceeds nesting limit of 32");
  return write(200, await recordEvent(c, wid, a.version, a.event_id, input, a.checkpoint, "inactive or stale trigger"));
}
