// Owner-authenticated Core HTTP client: fetch, claim, heartbeat and run credential only.
import type { RunSpec } from "./protocol.js";

export class HTTPError extends Error {
  constructor(readonly status: number, message: string) {
    super(`Core HTTP ${status}: ${message}`);
  }
}

export interface SavedRun {
  spec: RunSpec;
  status: string;
  session_id: string;
}

export interface Credential {
  token: string;
  instructions: string;
}

export class CoreAPI {
  constructor(readonly url: string, private readonly token: string, private readonly timeoutMs = 10_000) {}

  async do<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    const timeout = AbortSignal.timeout(this.timeoutMs);
    const res = await fetch(this.url.replace(/\/+$/, "") + path, {
      method,
      headers: { Authorization: `Bearer ${this.token}`, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
    if (res.status >= 300) {
      throw new HTTPError(res.status, (await res.text()).slice(0, 4096));
    }
    const text = await res.text();
    return (text ? JSON.parse(text) : undefined) as T;
  }

  getRun(id: string) {
    return this.do<SavedRun>("GET", `/runs/${id}`);
  }
  claim(id: string) {
    return this.do<{ lease: string }>("POST", `/runs/${id}/claim`);
  }
  heartbeat(id: string, lease: string, signal?: AbortSignal) {
    return this.do<{ cancel: boolean }>("POST", `/runs/${id}/heartbeat`, { lease }, signal);
  }
  credential(id: string, lease: string) {
    return this.do<Credential>("POST", `/runs/${id}/credential`, { lease });
  }
}
