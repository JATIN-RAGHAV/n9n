// Node catalog, feature flags and webhooks (backend/catalog.go).
import type { Ctx } from "./context";
import { HttpError, byteLen, decodeMap, fail, write, withinJSONDepth } from "./http";
import { storedGraph } from "./graph";
import { equalSecret, id, jsonText, now } from "./util";
import { getRun, queuedRun, runJSON } from "./runs";

export const catalogVersion = 2;

export function features(c: Ctx): Record<string, boolean> {
  return { ai: !!c.env.ANTHROPIC_API_KEY, code_node: c.env.ENABLE_CODE_NODE === "true" };
}

const s = (type: string) => ({ type });
const base: Record<string, any>[] = [
  { type: "manual_trigger", name: "Manual Trigger", category: "Trigger", description: "Start a workflow manually.", config_schema: { type: "object", properties: {} }, input_ports: [], output_ports: ["out"] },
  { type: "webhook_trigger", name: "Webhook Trigger", category: "Trigger", description: "Start on an authenticated HTTP request.", config_schema: { type: "object", required: ["secret"], properties: { secret: s("string") } }, input_ports: [], output_ports: ["out"] },
  { type: "schedule_trigger", name: "Schedule Trigger", category: "Trigger", description: "Run at a fixed interval in UTC.", config_schema: { type: "object", required: ["interval_seconds"], properties: { interval_seconds: { type: "integer", minimum: 10 }, timezone: { type: "string", enum: ["UTC"] } } }, input_ports: [], output_ports: ["out"] },
  { type: "email_trigger", name: "Gmail Trigger", category: "Trigger", description: "Poll Gmail for new messages.", config_schema: { type: "object", properties: { poll_seconds: { type: "integer", minimum: 30 } } }, input_ports: [], output_ports: ["out"] },
  { type: "set_fields", name: "Set Fields", category: "Transform", description: "Merge configured fields into input.", config_schema: { type: "object", required: ["fields"], properties: { fields: s("object") } }, input_ports: ["in"], output_ports: ["out"] },
  { type: "http_request", name: "HTTP Request", category: "Action", description: "Send an HTTP request.", config_schema: { type: "object", required: ["url", "method"], properties: { url: s("string"), method: { type: "string", enum: ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"] }, headers: s("object"), body: {} } }, input_ports: ["in"], output_ports: ["out"] },
  { type: "condition", name: "Condition", category: "Logic", description: "Route by a comparison.", config_schema: { type: "object", required: ["left", "operator"], properties: { left: {}, right: {}, operator: { type: "string", enum: ["equals", "contains", "greater_than", "exists"] } } }, input_ports: ["in"], output_ports: ["true", "false"] },
  { type: "send_email", name: "Send Email", category: "Action", description: "Send an email through Gmail.", config_schema: { type: "object", required: ["to", "subject", "body"], properties: { to: s("string"), subject: s("string"), body: s("string") } }, input_ports: ["in"], output_ports: ["out"] },
];

const outputProperties: Record<string, Record<string, unknown>> = {
  schedule_trigger: { scheduled_at: { type: "string", format: "date-time" } },
  email_trigger: { id: s("string"), thread_id: s("string"), sender: s("string"), subject: s("string"), body: s("string"), snippet: s("string"), headers: s("object"), message: s("object") },
  http_request: { status: s("integer"), headers: s("object"), body: {} },
  send_email: { id: s("string"), threadId: s("string") },
};

export const catalog = base.map((entry) => {
  const output: Record<string, unknown> = { type: "object", additionalProperties: true };
  if (outputProperties[entry.type]) output.properties = outputProperties[entry.type];
  return { ...entry, input_schema: { type: "object", additionalProperties: true }, output_schema: output };
});

export async function hook(c: Ctx, wid: string): Promise<Response> {
  const w = await c.db.row(
    `SELECT w.published_version,v.graph FROM workflows w JOIN versions v ON v.workflow_id=w.id AND v.version=w.published_version WHERE w.id=$1 AND w.active=TRUE`,
    [wid],
  );
  if (!w) return fail(404, "webhook not active");
  const node = (storedGraph(w.graph).nodes ?? []).find((n) => n.type === "webhook_trigger");
  const secret = typeof node?.config?.secret === "string" ? (node.config.secret as string) : "";
  if (!node || secret === "" || !equalSecret(c.req.headers.get("X-Webhook-Secret") ?? "", secret)) return fail(403, "invalid webhook secret");
  const input = (await decodeMap(c.req)) ?? {};
  if (!withinJSONDepth(input)) return fail(400, "webhook input exceeds nesting limit of 32");
  const event = c.req.headers.get("X-Event-ID") || id();
  if (byteLen(event) > 500) return fail(400, "event ID too long");
  const version: number = w.published_version;
  return write(200, await recordEvent(c, wid, version, event, input, undefined, "webhook version is no longer active"));
}

/**
 * Queues a run for a trigger event once: a repeated event ID returns the
 * first run with duplicate=true. Shared by webhooks and runner-reported events.
 */
export async function recordEvent(c: Ctx, wid: string, version: number, event: string, input: unknown, checkpoint: unknown, staleMessage: string) {
  return c.db.tx(async (q) => {
    const cur = await c.db.row(`SELECT published_version FROM workflows WHERE id=$1 AND active=TRUE FOR UPDATE`, [wid], q);
    if (!cur || cur.published_version !== version) throw new HttpError(409, staleMessage);
    const existing = await c.db.row(`SELECT run_id FROM trigger_events WHERE workflow_id=$1 AND version=$2 AND event_id=$3`, [wid, version, event], q);
    if (existing) {
      const run = await getRun(c, existing.run_id, q);
      return { run: run ? runJSON(run) : null, duplicate: true };
    }
    const rid = id();
    const t = now();
    await c.db.exec(`INSERT INTO runs(id,workflow_id,version,status,input,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, [rid, wid, version, "queued", jsonText(input), t, t], q);
    await c.db.exec(`INSERT INTO trigger_events VALUES($1,$2,$3,$4)`, [wid, version, event, rid], q);
    if (checkpoint !== undefined && checkpoint !== null) {
      await c.db.exec(`INSERT INTO checkpoints VALUES($1,$2,$3) ON CONFLICT(workflow_id,version) DO UPDATE SET value=excluded.value`, [wid, version, jsonText(checkpoint)], q);
    }
    return { run: queuedRun(rid, wid, version, input, t), duplicate: false };
  });
}
