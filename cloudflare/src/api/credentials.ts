// Encrypted credentials and Gmail OAuth (backend/credentials.go, backend/oauth.go).
import type { Ctx } from "./context";
import { byteLen, decode, fail, write } from "./http";
import { cookie, currentUser, SESSION_COOKIE } from "./auth";
import { storedGraph } from "./graph";
import { decrypt, encrypt, id, now, sha256Hex, timeAfter } from "./util";

export async function credentials(c: Ctx, uid: string, p: string[]): Promise<Response> {
  const method = c.req.method;
  if (p.length === 0) {
    if (method === "GET") {
      const rows = await c.db.rows(`SELECT id,name,kind,created_at FROM credentials WHERE user_id=$1 ORDER BY created_at DESC`, [uid]);
      return write(200, { credentials: rows.map((r) => ({ id: r.id, name: r.name, kind: r.kind, created_at: r.created_at })) });
    }
    if (method === "POST") {
      const a = await decode<{ name: string; kind: string; data: Record<string, unknown> | null }>(c.req, { name: "string", kind: "string", data: "object" });
      const name = a.name.trim();
      const kind = a.kind.trim();
      if (name === "" || kind === "" || byteLen(name) > 200 || byteLen(kind) > 100 || a.data === null) return fail(400, "invalid credential");
      let ciphertext: string;
      try {
        ciphertext = await encrypt(c.env.ENCRYPTION_KEY, a.data);
      } catch {
        return fail(500, "encryption error");
      }
      const cid = id();
      const created = now();
      await c.db.exec(`INSERT INTO credentials VALUES($1,$2,$3,$4,$5,$6)`, [cid, uid, name, kind, ciphertext, created]);
      return write(200, { credential: { id: cid, name, kind, created_at: created } });
    }
  }
  if (p.length === 1 && method === "DELETE") {
    const n = await c.db.exec(`DELETE FROM credentials WHERE id=$1 AND user_id=$2`, [p[0], uid]);
    if (n === 0) return fail(404, "credential not found");
    return write(200, { ok: true });
  }
  return fail(404, "not found");
}

async function decrypted(c: Ctx, credID: string, name: string, kind: string, ciphertext: string): Promise<Response> {
  let data: unknown;
  try {
    data = await decrypt(c.env.ENCRYPTION_KEY, ciphertext);
  } catch {
    return fail(500, "decryption error");
  }
  return write(200, { credential: { id: credID, name, kind, data } });
}

export async function credentialForJob(c: Ctx, runID: string, credID: string, lease: string): Promise<Response> {
  const r = await c.db.row(
    `SELECT COALESCE(r.graph_snapshot,v.graph) AS graph,r.lease_until,c.ciphertext,c.name,c.kind FROM runs r LEFT JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version JOIN workflows w ON w.id=r.workflow_id JOIN credentials c ON c.id=$1 AND c.user_id=w.user_id WHERE r.id=$2 AND r.status='running' AND r.lease_token=$3`,
    [credID, runID, lease],
  );
  if (!r || r.graph === null || r.lease_until === null || r.lease_until < now()) return fail(403, "invalid job lease or credential");
  if (!(storedGraph(r.graph).nodes ?? []).some((n) => n.credential_id === credID)) return fail(403, "credential not in workflow");
  return decrypted(c, credID, r.name, r.kind, r.ciphertext);
}

export async function credentialForTrigger(c: Ctx, wid: string, credID: string, version: number): Promise<Response> {
  const r = await c.db.row(
    `SELECT v.graph,c.ciphertext,c.name,c.kind FROM workflows w JOIN versions v ON v.workflow_id=w.id AND v.version=w.published_version JOIN credentials c ON c.id=$1 AND c.user_id=w.user_id WHERE w.id=$2 AND w.active=TRUE AND v.version=$3`,
    [credID, wid, version],
  );
  if (!r) return fail(403, "trigger credential unavailable");
  if (!(storedGraph(r.graph).nodes ?? []).some((n) => n.type === "email_trigger" && n.credential_id === credID)) return fail(403, "credential not in trigger");
  return decrypted(c, credID, r.name, r.kind, r.ciphertext);
}

const redirect = (to: string) => new Response(null, { status: 302, headers: { Location: to } });

export async function oauth(c: Ctx, action: string): Promise<Response> {
  if (c.req.method !== "GET") return fail(405, "method not allowed");
  if (c.req.headers.get("Authorization")) return fail(401, "browser session required for Gmail connection");
  const session = cookie(c.req, SESSION_COOKIE);
  if (session === null) return fail(401, "browser session required for Gmail connection");
  const uid = await currentUser(c);
  if (!uid) return fail(401, "authentication required");
  const clientID = c.env.GOOGLE_CLIENT_ID ?? "";
  const secret = c.env.GOOGLE_CLIENT_SECRET ?? "";
  const redirectURI = c.env.GOOGLE_REDIRECT_URI ?? "";
  if (!clientID || !secret || !redirectURI) return fail(503, "Gmail connection not configured");
  const sessionKey = await sha256Hex(session);
  if (action === "start") {
    const state = id() + id();
    await c.db.exec(`INSERT INTO oauth_states VALUES($1,$2,$3,$4)`, [await sha256Hex(state), uid, sessionKey, timeAfter(600)]);
    let u: URL;
    try {
      u = new URL(c.env.GOOGLE_AUTH_URL || "https://accounts.google.com/o/oauth2/v2/auth");
    } catch {
      return fail(500, "OAuth configuration error");
    }
    const q = u.searchParams;
    q.set("client_id", clientID);
    q.set("redirect_uri", redirectURI);
    q.set("response_type", "code");
    q.set("scope", "https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/gmail.send");
    q.set("access_type", "offline");
    q.set("prompt", "consent");
    q.set("state", state);
    q.sort();
    return write(200, { url: u.toString() });
  }
  if (action === "callback") {
    const state = c.url.searchParams.get("state") ?? "";
    const code = c.url.searchParams.get("code") ?? "";
    if (!state || !code) return redirect("/#/credentials?error=gmail");
    const stateKey = await sha256Hex(state);
    const valid = await c.db.tx(async (q) => {
      const r = await c.db.row(`SELECT user_id,session_hash,expires_at FROM oauth_states WHERE state_hash=$1 FOR UPDATE`, [stateKey], q);
      if (!r || r.user_id !== uid || r.session_hash !== sessionKey || r.expires_at < now()) return false;
      await c.db.exec(`DELETE FROM oauth_states WHERE state_hash=$1`, [stateKey], q);
      return true;
    });
    if (!valid) return redirect("/#/credentials?error=gmail");
    const form = new URLSearchParams({ client_id: clientID, client_secret: secret, code, grant_type: "authorization_code", redirect_uri: redirectURI });
    let raw: string;
    try {
      const resp = await fetch(c.env.GOOGLE_TOKEN_URL || "https://oauth2.googleapis.com/token", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: form.toString(),
        signal: AbortSignal.timeout(15000),
      });
      raw = (await resp.text()).slice(0, 64 << 10);
      if (resp.status !== 200) return redirect("/#/credentials?error=gmail");
    } catch {
      return redirect("/#/credentials?error=gmail");
    }
    let refresh = "";
    try {
      const t = JSON.parse(raw);
      refresh = typeof t?.refresh_token === "string" ? t.refresh_token : "";
    } catch {
      refresh = "";
    }
    if (!refresh) return redirect("/#/credentials?error=gmail");
    let ciphertext: string;
    try {
      ciphertext = await encrypt(c.env.ENCRYPTION_KEY, { client_id: clientID, client_secret: secret, refresh_token: refresh });
    } catch {
      return fail(500, "encryption error");
    }
    await c.db.exec(`INSERT INTO credentials VALUES($1,$2,$3,$4,$5,$6)`, [id(), uid, "Gmail", "gmail_oauth", ciphertext, now()]);
    return redirect("/#/credentials?connected=gmail");
  }
  return fail(404, "not found");
}
