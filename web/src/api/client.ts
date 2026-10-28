/**
 * API client for /api/ui/*.
 *
 * Production builds are REAL-ONLY. Mock implementation lives in ./mockClient and
 * is only reached via dynamic import behind `import.meta.env.DEV || VITE_MOCK===1`
 * so Vite/Rollup tree-shakes fixtures out of the embed.
 */
import { getApiKey, clearApiKey } from "../lib/auth";
import type {
  ProjectCard,
  TraceListParams,
  TraceListResponse,
  TraceSummaryResponse,
  RunDetail,
  MessagesResponse,
  StatsResponse,
  StreamEvent,
  SpanSummary,
  TraceSummary,
} from "./types";

/** True when fixture mocks are allowed (never in a plain production build). */
export function mocksAllowed(): boolean {
  return import.meta.env.DEV || import.meta.env.VITE_MOCK === "1";
}

/** Active data source. */
export function useMockMode(): boolean {
  if (import.meta.env.VITE_MOCK === "0") return false;
  if (import.meta.env.VITE_MOCK === "1") return true;
  return import.meta.env.DEV;
}

export function getApiMode(): "mock" | "real" {
  return useMockMode() ? "mock" : "real";
}

function authHeaders(): HeadersInit {
  const key = getApiKey();
  return key ? { "x-api-key": key } : {};
}

export class ApiError extends Error {
  status: number;
  detail: string;
  constructor(status: number, detail: string) {
    super(detail);
    this.status = status;
    this.detail = detail;
  }
}

async function realFetch<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      ...init,
      headers: { ...authHeaders(), ...(init?.headers || {}) },
    });
  } catch (e) {
    const msg = e instanceof Error ? e.message : "network error";
    throw new ApiError(0, msg);
  }
  if (res.status === 401) {
    clearApiKey();
    throw new ApiError(401, "unauthorized");
  }
  if (!res.ok) {
    let detail = res.statusText || `HTTP ${res.status}`;
    try {
      const j = await res.json();
      if (j?.detail) detail = String(j.detail);
    } catch {
      /* ignore */
    }
    throw new ApiError(res.status, detail);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export async function listProjects(): Promise<ProjectCard[]> {
  if (import.meta.env.DEV || import.meta.env.VITE_MOCK === "1") {
    if (useMockMode()) return (await import("./mockClient")).listProjects();
  }
  return realFetch("/api/ui/projects");
}

export async function listTraces(params: TraceListParams = {}): Promise<TraceListResponse> {
  if (import.meta.env.DEV || import.meta.env.VITE_MOCK === "1") {
    if (useMockMode()) return (await import("./mockClient")).listTraces(params);
  }
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") qs.set(k, String(v));
  }
  const qstr = qs.toString();
  return realFetch(`/api/ui/traces${qstr ? `?${qstr}` : ""}`);
}

export async function getTraceSummary(traceId: string): Promise<TraceSummaryResponse> {
  if (import.meta.env.DEV || import.meta.env.VITE_MOCK === "1") {
    if (useMockMode()) return (await import("./mockClient")).getTraceSummary(traceId);
  }
  return realFetch(`/api/ui/traces/${encodeURIComponent(traceId)}/summary`);
}

export async function getRun(id: string, full = false): Promise<RunDetail> {
  if (import.meta.env.DEV || import.meta.env.VITE_MOCK === "1") {
    if (useMockMode()) return (await import("./mockClient")).getRun(id, full);
  }
  const q = full ? "?full=1" : "";
  return realFetch(`/api/ui/runs/${encodeURIComponent(id)}${q}`);
}

export async function getMessages(id: string): Promise<MessagesResponse> {
  if (import.meta.env.DEV || import.meta.env.VITE_MOCK === "1") {
    if (useMockMode()) return (await import("./mockClient")).getMessages(id);
  }
  return realFetch(`/api/ui/runs/${encodeURIComponent(id)}/messages`);
}

export async function getStats(
  params: { project?: string; from?: string; to?: string } = {}
): Promise<StatsResponse> {
  if (import.meta.env.DEV || import.meta.env.VITE_MOCK === "1") {
    if (useMockMode()) return (await import("./mockClient")).getStats();
  }
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v) qs.set(k, v);
  }
  const qstr = qs.toString();
  return realFetch(`/api/ui/stats${qstr ? `?${qstr}` : ""}`);
}

export type StreamHandle = { close: () => void };

export function openTraceStream(
  params: Omit<TraceListParams, "cursor" | "limit" | "sort">,
  onEvent: (ev: StreamEvent) => void,
  opts?: { getOpenTraceId?: () => string | null | undefined }
): StreamHandle {
  if (import.meta.env.DEV || import.meta.env.VITE_MOCK === "1") {
    if (useMockMode()) {
      // Synchronous dynamic import pattern isn't available; use a deferred loader.
      let closed = false;
      let inner: StreamHandle | null = null;
      void import("./mockClient").then((m) => {
        if (closed) return;
        inner = m.openTraceStream(params, onEvent, opts);
      });
      return {
        close: () => {
          closed = true;
          inner?.close();
        },
      };
    }
  }

  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") qs.set(k, String(v));
  }
  const ac = new AbortController();
  (async () => {
    try {
      const res = await fetch(`/api/ui/traces/stream?${qs}`, {
        headers: { ...authHeaders(), Accept: "text/event-stream" },
        signal: ac.signal,
      });
      if (!res.ok || !res.body) {
        if (res.status === 401) clearApiKey();
        return;
      }
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      let event = "message";
      let id: string | undefined;
      let data: string[] = [];
      const flush = () => {
        if (!data.length) return;
        const raw = data.join("\n");
        data = [];
        try {
          const parsed = raw ? JSON.parse(raw) : {};
          if (event === "trace") onEvent({ type: "trace", id, data: parsed as TraceSummary });
          else if (event === "run_upsert")
            onEvent({
              type: "run_upsert",
              id,
              data: parsed as { trace_id: string; row: SpanSummary },
            });
          else if (event === "heartbeat") onEvent({ type: "heartbeat", data: {} });
        } catch {
          /* ignore */
        }
        event = "message";
        id = undefined;
      };
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        const lines = buf.split(/\r?\n/);
        buf = lines.pop() || "";
        for (const line of lines) {
          if (line === "") flush();
          else if (line.startsWith("event:")) event = line.slice(6).trim();
          else if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
          else if (line.startsWith("id:")) id = line.slice(3).trim();
        }
      }
    } catch {
      /* aborted */
    }
  })();
  return { close: () => ac.abort() };
}
