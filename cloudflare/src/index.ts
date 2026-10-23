import { Container, getContainer } from "@cloudflare/containers";

/** Stable name of the single container instance that runs the API and the runner. */
const INSTANCE = "n9n";

/**
 * One container runs `n9n-backend` (port 8080) and `n9n-runner` together.
 * Worker secrets and vars are handed to the processes as environment variables.
 */
export class N9nContainer extends Container<Env> {
  defaultPort = 8080;
  // The cron trigger below pings the container every 5 minutes, so with this
  // timeout the container stays up continuously and triggers keep polling.
  sleepAfter = "20m";

  constructor(ctx: DurableObjectState<{}>, env: Env) {
    super(ctx, env);
    const vars: Record<string, string | undefined> = {
      ADDR: ":8080",
      DATABASE_URL: env.DATABASE_URL,
      ENCRYPTION_KEY: env.ENCRYPTION_KEY,
      RUNNER_TOKEN: env.RUNNER_TOKEN,
      COOKIE_SECURE: env.COOKIE_SECURE,
      ALLOWED_ORIGIN: env.ALLOWED_ORIGIN,
      RUN_RETENTION_DAYS: env.RUN_RETENTION_DAYS,
      RUNNER_CONCURRENCY: env.RUNNER_CONCURRENCY,
      RUNNER_POLL_MS: env.RUNNER_POLL_MS,
      ALLOW_PRIVATE_HTTP: env.ALLOW_PRIVATE_HTTP,
      GOOGLE_CLIENT_ID: env.GOOGLE_CLIENT_ID,
      GOOGLE_CLIENT_SECRET: (env as Env & { GOOGLE_CLIENT_SECRET?: string }).GOOGLE_CLIENT_SECRET,
      GOOGLE_REDIRECT_URI: env.GOOGLE_REDIRECT_URI,
      RUNNER_ID: `cf-${ctx.id.toString().slice(0, 8)}`,
    };
    this.envVars = Object.fromEntries(
      Object.entries(vars).filter((e): e is [string, string] => typeof e[1] === "string" && e[1] !== ""),
    );
  }
}

function notFound(): Response {
  return Response.json({ error: "not found" }, { status: 404 });
}

function isInternal(path: string): boolean {
  return (
    path === "/internal" ||
    path.startsWith("/internal/") ||
    path === "/api/internal" ||
    path.startsWith("/api/internal/")
  );
}

export default {
  async fetch(request, env): Promise<Response> {
    const url = new URL(request.url);
    const path = url.pathname;

    if (path === "/healthz") {
      return new Response("ok\n", { headers: { "content-type": "text/plain" } });
    }
    // Runner-only routes never leave the container (nginx did the same locally).
    if (isInternal(path)) {
      return notFound();
    }
    if (path === "/api" || path.startsWith("/api/")) {
      const headers = new Headers(request.headers);
      headers.set("X-Forwarded-Host", url.host);
      headers.set("X-Forwarded-Proto", url.protocol.replace(":", ""));
      const ip = request.headers.get("CF-Connecting-IP");
      if (ip) {
        headers.set("X-Real-IP", ip);
        headers.set("X-Forwarded-For", ip);
      }
      const forwarded = new Request(request, { headers });
      return getContainer(env.N9N, INSTANCE).fetch(forwarded);
    }
    return env.ASSETS.fetch(request);
  },

  // Keep-alive: an API health check counts as activity and resets `sleepAfter`.
  async scheduled(_controller, env): Promise<void> {
    const res = await getContainer(env.N9N, INSTANCE).fetch(
      new Request("https://n9n.internal/api/health"),
    );
    await res.body?.cancel();
    if (!res.ok) {
      throw new Error(`n9n keep-alive: API health returned ${res.status}`);
    }
  },
} satisfies ExportedHandler<Env>;
