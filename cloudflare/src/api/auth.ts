// Sessions, registration and login (backend/main.go: user, sessionToken,
// newSession, session, auth, allowAuth).
import { Buffer } from "node:buffer";
import type { Ctx } from "./context";
import { byteLen, decode, fail, write } from "./http";
import { formatTime, hashPassword, id, now, pbkdf2Iterations, sha256Hex, verifyPassword } from "./util";

export const SESSION_COOKIE = "n9n_session";

export function cookie(req: Request, name: string): string | null {
  const header = req.headers.get("Cookie");
  if (!header) return null;
  for (const part of header.split(";")) {
    const i = part.indexOf("=");
    if (i < 0) continue;
    if (part.slice(0, i).trim() === name) {
      let v = part.slice(i + 1).trim();
      if (v.length >= 2 && v.startsWith('"') && v.endsWith('"')) v = v.slice(1, -1);
      return v;
    }
  }
  return null;
}

/** The session token and whether it came from a bearer header. */
export function sessionToken(req: Request): { token: string; bearer: boolean } {
  const auth = req.headers.get("Authorization");
  if (auth) {
    if (!auth.toLowerCase().startsWith("bearer ")) return { token: "", bearer: true };
    const token = auth.slice(7).trim();
    if (token === "" || /[ \t\r\n]/.test(token)) return { token: "", bearer: true };
    return { token, bearer: true };
  }
  return { token: cookie(req, SESSION_COOKIE) ?? "", bearer: false };
}

export async function currentUser(c: Ctx): Promise<string> {
  const { token } = sessionToken(c.req);
  if (!token) return "";
  const r = await c.db.row(`SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at>$2`, [await sha256Hex(token), now()]);
  return r?.user_id ?? "";
}

async function newSession(c: Ctx, uid: string): Promise<{ token: string; expiry: number }> {
  const token = id() + id();
  const expiry = Date.now() + 30 * 24 * 3600 * 1000;
  await c.db.exec(`INSERT INTO sessions VALUES($1,$2,$3)`, [await sha256Hex(token), uid, formatTime(expiry)]);
  return { token, expiry };
}

export function sessionCookie(c: Ctx, value: string, expiry: number | null): string {
  const parts = [`${SESSION_COOKIE}=${value}`, "Path=/"];
  if (expiry === null) parts.push("Max-Age=0");
  else parts.push(`Expires=${new Date(expiry).toUTCString()}`);
  parts.push("HttpOnly");
  if (c.env.COOKIE_SECURE === "true") parts.push("Secure");
  parts.push("SameSite=Lax");
  return parts.join("; ");
}

// In-isolate fallbacks for the per-IP throttle and the password-check limit.
const windows = new Map<string, { start: number; count: number }>();
let busy = 0;

function clientIP(req: Request): string {
  const ip = req.headers.get("CF-Connecting-IP") || req.headers.get("X-Real-IP") || "";
  return /^[0-9.]+$/.test(ip) || ip.includes(":") ? ip : "unknown";
}

/** At most 20 registration or login attempts per source IP per minute. */
async function allowAuth(c: Ctx): Promise<boolean> {
  const ip = clientIP(c.req);
  if (c.env.AUTH_LIMITER) return (await c.env.AUTH_LIMITER.limit({ key: `auth:${ip}` })).success;
  const t = Date.now();
  if (windows.size > 1024) for (const [k, w] of windows) if (t - w.start >= 60000) windows.delete(k);
  let w = windows.get(ip);
  if (!w || t - w.start >= 60000) w = { start: t, count: 0 };
  if (w.count >= 20) return false;
  w.count++;
  windows.set(ip, w);
  return true;
}

export async function auth(c: Ctx, action: string): Promise<Response> {
  const mobile = action.startsWith("mobile/");
  if (mobile) action = action.slice("mobile/".length);
  if (mobile && action !== "login" && action !== "register") return fail(404, "not found");
  const method = c.req.method;
  if (action === "me" && method === "GET") {
    const uid = await currentUser(c);
    if (!uid) return fail(401, "authentication required");
    const r = await c.db.row(`SELECT email FROM users WHERE id=$1`, [uid]);
    return write(200, { user: { id: uid, email: r?.email ?? "" } });
  }
  if (action === "logout" && method === "POST") {
    const { token, bearer } = sessionToken(c.req);
    if (bearer && (token === "" || (await currentUser(c)) === "")) return fail(401, "authentication required");
    if (token) {
      try {
        await c.db.exec(`DELETE FROM sessions WHERE token_hash=$1`, [await sha256Hex(token)]);
      } catch {
        return fail(500, "session error");
      }
    }
    const headers = bearer ? undefined : { "Set-Cookie": sessionCookie(c, "", null) };
    return write(200, { ok: true }, headers);
  }
  if (method !== "POST" || (action !== "register" && action !== "login")) return fail(404, "not found");
  if (!(await allowAuth(c))) return fail(429, "too many authentication attempts; try again shortly");
  if (busy >= 4) return fail(429, "authentication is busy; try again shortly");
  busy++;
  try {
    const a = await decode<{ email: string; password: string }>(c.req, { email: "string", password: "string" });
    const email = a.email.trim().toLowerCase();
    const pwLen = byteLen(a.password);
    if (byteLen(email) > 254 || !email.includes("@") || pwLen < 8 || pwLen > 72) return fail(400, "invalid email or password");
    let uid: string;
    if (action === "register") {
      const hash = await hashPassword(a.password, pbkdf2Iterations(c.env.PASSWORD_PBKDF2_ITERATIONS));
      uid = id();
      try {
        await c.db.exec(`INSERT INTO users VALUES($1,$2,$3,$4)`, [uid, email, Buffer.from(hash), now()]);
      } catch {
        return fail(409, "email already registered");
      }
    } else {
      const r = await c.db.row(`SELECT id,password_hash FROM users WHERE email=$1`, [email]);
      if (!r || !(await verifyPassword(new Uint8Array(r.password_hash), a.password))) return fail(401, "invalid credentials");
      uid = r.id;
    }
    const user = { id: uid, email };
    let session;
    try {
      session = await newSession(c, uid);
    } catch {
      return fail(500, "session error");
    }
    if (mobile) {
      return write(200, { user, token: session.token, expires_at: formatTime(session.expiry) }, { "Cache-Control": "no-store" });
    }
    return write(200, { user }, { "Set-Cookie": sessionCookie(c, session.token, session.expiry) });
  } finally {
    busy--;
  }
}
