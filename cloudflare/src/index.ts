// n9n on Cloudflare Workers: the Flutter web app as static assets and the API
// (a TypeScript port of backend/, kept in sync with it) on the same origin.
// The Rust runner connects to /internal/* from wherever it runs.
import { cleanup, handleApi } from "./api/router";
import type { ApiEnv } from "./api/context";

type WorkerEnv = ApiEnv & { ASSETS: Fetcher };

function notFound(): Response {
  return Response.json({ error: "not found" }, { status: 404 });
}

export default {
  async fetch(request, env, ctx): Promise<Response> {
    const path = new URL(request.url).pathname;
    if (path === "/healthz") return new Response("ok\n", { headers: { "content-type": "text/plain" } });
    // /api/internal was never public (nginx returned 404); keep it that way.
    if (path === "/api/internal" || path.startsWith("/api/internal/") || path === "/internal") return notFound();
    if (path === "/api" || path.startsWith("/api/") || path.startsWith("/internal/")) return handleApi(request, env, ctx);
    return env.ASSETS.fetch(request);
  },

  async scheduled(_controller, env, ctx): Promise<void> {
    ctx.waitUntil(cleanup(env));
  },
} satisfies ExportedHandler<WorkerEnv>;
