// Request routing for /api and /internal (backend/main.go: ServeHTTP).
import type { ApiEnv, Ctx } from "./context";
import { connect, ensureSchema } from "./db";
import { HttpError, fail, write } from "./http";
import { auth, currentUser } from "./auth";
import { workflows } from "./workflows";
import { runs } from "./runs";
import { credentials, oauth } from "./credentials";
import { catalog, catalogVersion, features, hook } from "./catalog";
import { internal } from "./internal";
import { formatTime } from "./util";

function originAllowed(req: Request, url: URL, allow: string | undefined): boolean {
  const o = req.headers.get("Origin");
  if (!o || req.method === "GET" || req.method === "HEAD") return true;
  const m = /^[A-Za-z][A-Za-z0-9+.-]*:\/\/([^/?#]*)/.exec(o);
  const originHost = m ? m[1].replace(/^[^@]*@/, "") : "";
  const host = req.headers.get("X-Forwarded-Host") || url.host;
  return originHost.toLowerCase() === host.toLowerCase() || o === allow;
}

function misconfigured(env: ApiEnv): string | null {
  if (!env.HYPERDRIVE) return "HYPERDRIVE binding missing";
  if (!/^[0-9a-fA-F]{64}$/.test(env.ENCRYPTION_KEY ?? "")) return "ENCRYPTION_KEY must be 64 hex characters";
  if ((env.RUNNER_TOKEN ?? "").length < 32) return "RUNNER_TOKEN must be at least 32 characters";
  return null;
}

async function route(c: Ctx): Promise<Response> {
  const path = c.url.pathname;
  const method = c.req.method;
  if (path.startsWith("/internal/")) return internal(c, path.slice("/internal/".length).split("/"));
  const parts = path.replace(/^\/+|\/+$/g, "").split("/").slice(1);
  if (parts.length === 0 || (parts.length === 1 && parts[0] === "")) return fail(404, "not found");
  if (parts.length === 3 && parts[0] === "oauth" && parts[1] === "google") return oauth(c, parts[2]);
  if (parts.length >= 2 && parts[0] === "auth") {
    if (parts.length === 2) return auth(c, parts[1]);
    if (parts.length === 3 && parts[1] === "mobile") return auth(c, "mobile/" + parts[2]);
    return fail(404, "not found");
  }
  if (parts.length === 2 && parts[0] === "hooks" && method === "POST") return hook(c, parts[1]);
  const uid = await currentUser(c);
  if (!uid) return fail(401, "authentication required");
  switch (parts[0]) {
    case "nodes":
      if (parts.length === 1 && method === "GET") return write(200, { version: catalogVersion, nodes: catalog });
      break;
    case "features":
      if (parts.length === 1 && method === "GET") return write(200, { features: features(c) });
      break;
    case "workflows":
      return workflows(c, uid, parts.slice(1));
    case "runs":
      return runs(c, uid, parts.slice(1));
    case "credentials":
      return credentials(c, uid, parts.slice(1));
  }
  return fail(404, "not found");
}

/** Handles a request for /api/* or /internal/*. */
export async function handleApi(req: Request, env: ApiEnv, ctx: ExecutionContext): Promise<Response> {
  const url = new URL(req.url);
  if (!originAllowed(req, url, env.ALLOWED_ORIGIN)) return fail(403, "origin mismatch");
  if (!url.pathname.startsWith("/internal/") && !url.pathname.startsWith("/api/")) return fail(404, "not found");
  if (/^\/api\/health\/*$/.test(url.pathname) && req.method === "GET") return write(200, { status: "ok" });
  const bad = misconfigured(env);
  if (bad) {
    console.error(`n9n API misconfigured: ${bad}`);
    return fail(500, "server misconfigured");
  }
  let conn;
  try {
    conn = await connect(env.HYPERDRIVE.connectionString);
    await ensureSchema(conn.db);
  } catch (e) {
    console.error("database unavailable", e);
    if (conn) ctx.waitUntil(conn.close());
    return fail(500, "database error");
  }
  try {
    return await route({ req, url, env, db: conn.db });
  } catch (e) {
    if (e instanceof HttpError) return fail(e.status, e.message);
    console.error("request failed", e);
    return fail(500, "database error");
  } finally {
    ctx.waitUntil(conn.close());
  }
}

/** Hourly cleanup of expired sessions, OAuth states and old finished runs (Go: cleanup). */
export async function cleanup(env: ApiEnv): Promise<void> {
  if (misconfigured(env)) return;
  const conn = await connect(env.HYPERDRIVE.connectionString);
  try {
    await ensureSchema(conn.db);
    const cutoff = formatTime(Date.now());
    await conn.db.exec(`DELETE FROM sessions WHERE expires_at<$1`, [cutoff]);
    await conn.db.exec(`DELETE FROM oauth_states WHERE expires_at<$1`, [cutoff]);
    let days = Number(env.RUN_RETENTION_DAYS ?? "30");
    if (!/^[+-]?\d+$/.test(env.RUN_RETENTION_DAYS ?? "30") || days < 1) days = 30;
    const before = formatTime(Date.now() - days * 24 * 3600 * 1000);
    await conn.db.exec(`DELETE FROM runs WHERE status IN ('succeeded','failed','cancelled','uncertain') AND updated_at<$1`, [before]);
  } finally {
    await conn.close();
  }
}
