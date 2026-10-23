// HTTP helpers that reproduce the Go API's request decoding and responses
// (backend/main.go: write, fail, decode, decodeLimit, withinJSONDepth).

export class HttpError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export function write(status: number, value: unknown, headers?: HeadersInit): Response {
  const h = new Headers(headers);
  h.set("Content-Type", "application/json");
  // json.Encoder.Encode ends the body with a newline.
  return new Response(JSON.stringify(value) + "\n", { status, headers: h });
}

export function fail(status: number, msg: string, headers?: HeadersInit): Response {
  return write(status, { error: msg }, headers);
}

export const encoder = new TextEncoder();

/** Byte length of a string in UTF-8, matching Go's len(string). */
export function byteLen(s: string): number {
  return encoder.encode(s).length;
}

/** Reads and parses one JSON value, rejecting oversized bodies and trailing data like decodeLimit. */
export async function readJSON(request: Request, limit = 1 << 20): Promise<unknown> {
  const buf = await request.arrayBuffer();
  if (buf.byteLength > limit) throw new HttpError(400, "http: request body too large");
  const text = new TextDecoder().decode(buf);
  if (text.trim() === "") throw new HttpError(400, "EOF");
  try {
    return JSON.parse(text);
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    throw new HttpError(400, /non-whitespace|after JSON/i.test(msg) ? "unexpected trailing JSON" : msg);
  }
}

type FieldType = "string" | "bool" | "int" | "intPtr" | "object" | "any";
export type Spec = Record<string, FieldType>;

export function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function zero(t: FieldType): unknown {
  switch (t) {
    case "string":
      return "";
    case "bool":
      return false;
    case "int":
      return 0;
    default:
      return null;
  }
}

function typeName(v: unknown): string {
  if (Array.isArray(v)) return "array";
  if (v === null) return "null";
  if (typeof v === "boolean") return "bool";
  return typeof v;
}

/** Converts one decoded field the way encoding/json fills a Go struct field. */
export function field(name: string, t: FieldType, v: unknown): unknown {
  if (v === null || v === undefined) return zero(t);
  const goType = { string: "string", bool: "bool", int: "int", intPtr: "int", object: "map[string]interface {}", any: "interface {}" }[t];
  const bad = () => new HttpError(400, `json: cannot unmarshal ${typeName(v)} into Go struct field .${name} of type ${goType}`);
  switch (t) {
    case "string":
      if (typeof v !== "string") throw bad();
      return v;
    case "bool":
      if (typeof v !== "boolean") throw bad();
      return v;
    case "int":
    case "intPtr":
      if (typeof v !== "number" || !Number.isInteger(v)) throw bad();
      return v;
    case "object":
      if (!isObject(v)) throw bad();
      return v;
    case "any":
      return v;
  }
}

/** Decodes a JSON object into the fields of spec, rejecting unknown fields (DisallowUnknownFields). */
export function decodeStruct<T>(value: unknown, spec: Spec): T {
  const out: Record<string, unknown> = {};
  for (const [k, t] of Object.entries(spec)) out[k] = zero(t);
  if (value === null) return out as T; // decoding null into a struct leaves it unchanged
  if (!isObject(value)) throw new HttpError(400, `json: cannot unmarshal ${typeName(value)} into Go value of type struct`);
  for (const [k, v] of Object.entries(value)) {
    if (!(k in spec)) throw new HttpError(400, `json: unknown field "${k}"`);
    out[k] = field(k, spec[k], v);
  }
  return out as T;
}

export async function decode<T>(request: Request, spec: Spec, limit = 1 << 20): Promise<T> {
  return decodeStruct<T>(await readJSON(request, limit), spec);
}

/** Decodes a JSON object into map[string]any: null becomes null, other non-objects fail. */
export async function decodeMap(request: Request): Promise<Record<string, unknown> | null> {
  const v = await readJSON(request);
  if (v === null) return null;
  if (!isObject(v)) throw new HttpError(400, `json: cannot unmarshal ${typeName(v)} into Go value of type map[string]interface {}`);
  return v;
}

export function withinJSONDepth(value: unknown): boolean {
  const check = (item: unknown, depth: number): boolean => {
    if (depth > 32) return false;
    if (Array.isArray(item)) return item.every((c) => check(c, depth + 1));
    if (isObject(item)) return Object.values(item).every((c) => check(c, depth + 1));
    return true;
  };
  return check(value, 1);
}
