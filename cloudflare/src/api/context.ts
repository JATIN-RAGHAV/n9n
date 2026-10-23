import type { DB } from "./db";

export interface ApiEnv {
  HYPERDRIVE: Hyperdrive;
  ENCRYPTION_KEY: string;
  RUNNER_TOKEN: string;
  AUTH_LIMITER?: RateLimit;
  COOKIE_SECURE?: string;
  ALLOWED_ORIGIN?: string;
  RUN_RETENTION_DAYS?: string;
  PASSWORD_PBKDF2_ITERATIONS?: string;
  GOOGLE_CLIENT_ID?: string;
  GOOGLE_CLIENT_SECRET?: string;
  GOOGLE_REDIRECT_URI?: string;
  GOOGLE_AUTH_URL?: string;
  GOOGLE_TOKEN_URL?: string;
  ANTHROPIC_API_KEY?: string;
  ENABLE_CODE_NODE?: string;
}

export interface Ctx {
  req: Request;
  url: URL;
  env: ApiEnv;
  db: DB;
}
