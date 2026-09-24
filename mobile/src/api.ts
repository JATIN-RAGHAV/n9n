import { Credential, Draft, JsonObject, NodeDefinition, Run, Session, Step, User, Workflow } from './types';

export function normalizeApiUrl(value: string): string {
  const trimmed = value.trim().replace(/\/+$/, '');
  let url: URL;
  try { url = new URL(trimmed); } catch { throw new Error('Enter a complete http:// or https:// server URL.'); }
  if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password ||
      url.search || url.hash || !['', '/'].includes(url.pathname)) {
    throw new Error('Use only the server origin, such as https://n9n.example.com.');
  }
  return url.origin;
}

type Fetcher = typeof fetch;
export class ApiError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
}
export class Api {
  readonly url: string;
  private token?: string;
  constructor(url: string, token?: string, private fetcher: Fetcher = fetch) {
    this.url = normalizeApiUrl(url);
    this.token = token;
  }

  private async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 20000);
    try {
      const response = await this.fetcher(`${this.url}/api${path}`, {
        method,
        signal: controller.signal,
        headers: {
          Accept: 'application/json',
          ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
          ...(this.token ? { Authorization: `Bearer ${this.token}` } : {}),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      let data: any;
      try { data = await response.json(); } catch { data = {}; }
      if (!response.ok) throw new ApiError(data.error || `Server returned ${response.status}.`, response.status);
      return data as T;
    } catch (error) {
      if ((error as Error).name === 'AbortError') throw new Error('Request timed out. Check the server connection.');
      throw error;
    } finally { clearTimeout(timeout); }
  }

  mobileAuth(mode: 'login' | 'register', email: string, password: string) {
    return this.request<{ user: User; token: string; expires_at: string }>(
      `/auth/mobile/${mode}`, 'POST', { email, password });
  }
  me() { return this.request<{ user: User }>('/auth/me'); }
  logout() { return this.request<{ ok: boolean }>('/auth/logout', 'POST'); }
  nodes() { return this.request<{ nodes: NodeDefinition[] }>('/nodes'); }
  workflows() { return this.request<{ workflows: Workflow[] }>('/workflows'); }
  workflow(id: string) { return this.request<{ workflow: Workflow }>(`/workflows/${encodeURIComponent(id)}`); }
  createWorkflow(name: string) { return this.request<{ workflow: Workflow }>('/workflows', 'POST', {
    name, draft: { nodes: [], edges: [] },
  }); }
  updateWorkflow(id: string, name: string, draft: Draft) {
    return this.request<{ workflow: Workflow }>(`/workflows/${encodeURIComponent(id)}`, 'PUT', { name, draft });
  }
  deleteWorkflow(id: string) { return this.request<{ ok: boolean }>(`/workflows/${encodeURIComponent(id)}`, 'DELETE'); }
  publish(id: string) { return this.request<{ workflow: Workflow }>(`/workflows/${encodeURIComponent(id)}/publish`, 'POST'); }
  activate(id: string, active: boolean) {
    return this.request<{ workflow: Workflow }>(`/workflows/${encodeURIComponent(id)}/activate`, 'POST', { active });
  }
  startRun(id: string, input: JsonObject) {
    return this.request<{ run: Run }>(`/workflows/${encodeURIComponent(id)}/run`, 'POST', { input });
  }
  testDraft(id: string, input: JsonObject) {
    return this.request<{ run: Run }>(`/workflows/${encodeURIComponent(id)}/test`, 'POST', { input });
  }
  runs(workflowId?: string) {
    const query = workflowId ? `?workflow_id=${encodeURIComponent(workflowId)}` : '';
    return this.request<{ runs: Run[] }>(`/runs${query}`);
  }
  run(id: string) { return this.request<{ run: Run; steps: Step[] }>(`/runs/${encodeURIComponent(id)}`); }
  cancelRun(id: string) { return this.request<{ run: Run }>(`/runs/${encodeURIComponent(id)}/cancel`, 'POST'); }
  credentials() { return this.request<{ credentials: Credential[] }>('/credentials'); }
  createCredential(name: string, data: { client_id: string; client_secret: string; refresh_token: string }) {
    return this.request<{ credential: Credential }>('/credentials', 'POST', { name, kind: 'gmail_oauth', data });
  }
  deleteCredential(id: string) { return this.request<{ ok: boolean }>(`/credentials/${encodeURIComponent(id)}`, 'DELETE'); }
}

export function apiForSession(session: Session): Api {
  const expiry = Date.parse(session.expiresAt);
  if (!Number.isFinite(expiry) || expiry <= Date.now()) throw new Error('Your session expired. Sign in again.');
  return new Api(session.apiUrl, session.token);
}
