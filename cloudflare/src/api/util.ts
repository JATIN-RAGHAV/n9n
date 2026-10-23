// Identifiers, timestamps, hashing and encryption shared with the Go API.
// Every format here must stay byte-compatible with backend/ so both servers
// can run against the same database.
import bcrypt from "bcryptjs";
import { encoder } from "./http";

const hex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");

export function fromHex(s: string): Uint8Array {
  if (s.length % 2 !== 0 || /[^0-9a-fA-F]/.test(s)) throw new Error("invalid hex");
  const out = new Uint8Array(s.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.slice(i * 2, i * 2 + 2), 16);
  return out;
}

/** 16 random bytes as 32 hex characters (Go: id()). */
export function id(): string {
  return hex(crypto.getRandomValues(new Uint8Array(16)));
}

// Go formats times as 2006-01-02T15:04:05.000000000Z. The Workers clock has
// millisecond resolution and does not advance during a request, so the six
// sub-millisecond digits carry a per-isolate counter that keeps ordering
// stable for rows written in the same millisecond.
let lastMs = 0;
let seq = 0;
export function formatTime(ms: number, sub = 0): string {
  const iso = new Date(ms).toISOString(); // 2026-10-23T10:00:00.123Z
  return iso.slice(0, 23) + String(sub).padStart(6, "0") + "Z";
}
export function now(): string {
  const ms = Date.now();
  if (ms === lastMs) seq = Math.min(seq + 1, 999999);
  else {
    lastMs = ms;
    seq = 0;
  }
  return formatTime(ms, seq);
}
export function timeAfter(seconds: number): string {
  return formatTime(Date.now() + seconds * 1000);
}

/** JSON text for a JSONB parameter (Go: jsonText). Always a string so pg never turns arrays into Postgres arrays. */
export function jsonText(v: unknown): string {
  return JSON.stringify(v === undefined ? null : v);
}

export async function sha256Hex(s: string): Promise<string> {
  return hex(new Uint8Array(await crypto.subtle.digest("SHA-256", encoder.encode(s))));
}

/** Constant-time string comparison (Go: equalSecret). */
export function equalSecret(a: string, b: string): boolean {
  const x = encoder.encode(a);
  const y = encoder.encode(b);
  if (x.length !== y.length) return false;
  let diff = 0;
  for (let i = 0; i < x.length; i++) diff |= x[i] ^ y[i];
  return diff === 0;
}

/** Shortens s to at most max UTF-8 bytes without splitting a character (Go: truncateUTF8). */
export function truncateUTF8(s: string, max: number): string {
  const b = encoder.encode(s);
  if (b.length <= max) return s;
  let cut = max;
  while (cut > 0 && (b[cut] & 0xc0) === 0x80) cut--;
  return new TextDecoder().decode(b.slice(0, cut));
}

/** RFC 3339 timestamp check matching Go's time.Parse(time.RFC3339Nano, s). */
export function isRFC3339(s: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/.exec(s);
  if (!m) return false;
  const [y, mo, d, h, mi, se] = m.slice(1, 7).map(Number);
  if (mo < 1 || mo > 12 || h > 23 || mi > 59 || se > 59) return false;
  const days = new Date(Date.UTC(y, mo, 0)).getUTCDate();
  return d >= 1 && d <= days;
}

function b64(bytes: Uint8Array): string {
  let s = "";
  for (const x of bytes) s += String.fromCharCode(x);
  return btoa(s);
}
function unb64(s: string): Uint8Array {
  const raw = atob(s);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

// Credentials are AES-256-GCM: base64(nonce || ciphertext || tag), the layout
// Go's gcm.Seal(nonce, nonce, plain, nil) produces.
async function aesKey(keyHex: string): Promise<CryptoKey> {
  return crypto.subtle.importKey("raw", fromHex(keyHex), "AES-GCM", false, ["encrypt", "decrypt"]);
}
export async function encrypt(keyHex: string, data: unknown): Promise<string> {
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const sealed = new Uint8Array(
    await crypto.subtle.encrypt({ name: "AES-GCM", iv: nonce }, await aesKey(keyHex), encoder.encode(JSON.stringify(data))),
  );
  const out = new Uint8Array(nonce.length + sealed.length);
  out.set(nonce);
  out.set(sealed, nonce.length);
  return b64(out);
}
export async function decrypt(keyHex: string, value: string): Promise<unknown> {
  const raw = unb64(value);
  if (raw.length < 12) throw new Error("bad ciphertext");
  const plain = await crypto.subtle.decrypt({ name: "AES-GCM", iv: raw.slice(0, 12) }, await aesKey(keyHex), raw.slice(12));
  return JSON.parse(new TextDecoder().decode(plain));
}

// Passwords: new hashes are PBKDF2-SHA256, stored as the bytes of
// "pbkdf2_sha256$<iterations>$<salt base64>$<hash base64>". bcrypt hashes from
// older Go installs still verify. Workers' Web Crypto allows at most 100,000
// PBKDF2 iterations, which is also the default here and in Go.
export const PBKDF2_PREFIX = "pbkdf2_sha256$";
export const DEFAULT_PBKDF2_ITERATIONS = 100000;

async function pbkdf2(password: string, salt: Uint8Array, iterations: number): Promise<Uint8Array> {
  const key = await crypto.subtle.importKey("raw", encoder.encode(password), "PBKDF2", false, ["deriveBits"]);
  return new Uint8Array(await crypto.subtle.deriveBits({ name: "PBKDF2", hash: "SHA-256", salt, iterations }, key, 256));
}

export function pbkdf2Iterations(value: string | undefined): number {
  const n = Number(value);
  return Number.isInteger(n) && n >= 10000 && n <= 100000 ? n : DEFAULT_PBKDF2_ITERATIONS;
}

export async function hashPassword(password: string, iterations: number): Promise<Uint8Array> {
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const hash = await pbkdf2(password, salt, iterations);
  return encoder.encode(`${PBKDF2_PREFIX}${iterations}$${b64(salt)}$${b64(hash)}`);
}

export async function verifyPassword(stored: Uint8Array, password: string): Promise<boolean> {
  const text = new TextDecoder().decode(stored);
  if (text.startsWith(PBKDF2_PREFIX)) {
    const parts = text.split("$");
    if (parts.length !== 4) return false;
    const iterations = Number(parts[1]);
    if (!Number.isInteger(iterations) || iterations < 1 || iterations > 100000) return false;
    const want = unb64(parts[3]);
    const got = await pbkdf2(password, unb64(parts[2]), iterations);
    if (got.length !== want.length) return false;
    let diff = 0;
    for (let i = 0; i < got.length; i++) diff |= got[i] ^ want[i];
    return diff === 0;
  }
  if (text.startsWith("$2")) return bcrypt.compare(password, text);
  return false;
}
