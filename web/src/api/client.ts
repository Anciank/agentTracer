/**
 * API client for /api/ui/*.
 *
 * Mode:
 * - VITE_MOCK=1 → always mock
 * - VITE_MOCK=0 → always real
 * - unset → try real once; on 404/network failure flip to mock for the session
 *
 * Auth: x-api-key from session/local storage (see lib/auth).
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
  TraceSummary,
  SpanSummary,
} from "./types";

export type ApiMode = "mock" | "real" | "auto";

let mode: ApiMode =
  import.meta.env.VITE_MOCK === "1" ? "mock" : import.meta.env.VITE_MOCK === "0" ? "real" : "auto";
let resolved: "mock" | "real" | null = mode === "auto" ? null : mode;

export function getApiMode(): string {
  return resolved ?? `auto→pending`;
}

export function forceMock(): void {
  mode = "mock";
  resolved = "mock";
}

async function loadMock() {
  return import("../fixtures/data");
}

function encodeCursor(start: string, traceId: string): string {
  const json = JSON.stringify({ s: start, t: traceId });
  const b64 = btoa(json).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  return b64;
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
  const res = await fetch(path, {
    ...init,
    headers: { ...authHeaders(), ...(init?.headers || {}) },
  });
  if (res.status === 401) {
    clearApiKey();
    throw new ApiError(401, "unauthorized");
  }
  if (!res.ok) {
    let detail = res.statusText;
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

async function withFallback<T>(real: () => Promise<T>, mock: () => Promise<T>): Promise<T> {
  if (resolved === "mock" || mode === "mock") {
    resolved = "mock";
    return mock();
  }
  if (resolved === "real" || mode === "real") {
    return real();
  }
  // auto
  try {
    const v = await real();
    resolved = "real";
    return v;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) throw e;
    if (e instanceof ApiError && (e.status === 404 || e.status === 501)) {
      resolved = "mock";
      return mock();
    }
    // network / other → mock for local SPA without server
    if (!(e instanceof ApiError)) {
      resolved = "mock";
      return mock();
    }
    throw e;
  }
}

function filterTraces(
  all: TraceSummary[],
  params: TraceListParams
): TraceSummary[] {
  let list = all.slice();
  if (params.project) list = list.filter((t) => t.project === params.project);
  if (params.status) list = list.filter((t) => t.status === params.status);
  if (params.q) {
    const q = params.q.toLowerCase();
    list = list.filter((t) => t.name.toLowerCase().includes(q));
  }
  if (params.model) list = list.filter((t) => t.model === params.model);
  if (params.min_tokens != null) list = list.filter((t) => t.tokens.total >= params.min_tokens!);
  if (params.min_duration_ms != null) {
    list = list.filter((t) => t.duration_ms != null && t.duration_ms >= params.min_duration_ms!);
  }
  const sort = params.sort || "start_desc";
  list.sort((a, b) => {
    if (sort === "duration_desc") return (b.duration_ms || 0) - (a.duration_ms || 0);
    if (sort === "tokens_desc") return b.tokens.total - a.tokens.total;
    if (sort === "errors_desc") return b.error_count - a.error_count;
    return b.start.localeCompare(a.start);
  });
  return list;
}

export async function listProjects(): Promise<ProjectCard[]> {
  return withFallback(
    () => realFetch("/api/ui/projects"),
    async () => (await loadMock()).PROJECTS
  );
}

export async function listTraces(params: TraceListParams = {}): Promise<TraceListResponse> {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") qs.set(k, String(v));
  }
  const qstr = qs.toString();
  return withFallback(
    () => realFetch(`/api/ui/traces${qstr ? `?${qstr}` : ""}`),
    async () => {
      const fx = await loadMock();
      const all = fx.makeManyTraces(10_000);
      const filtered = filterTraces(all, params);
      // Cursor is opaque base64url of {"s","t"} — mock may decode for paging only.
      let start = 0;
      if (params.cursor) {
        try {
          const pad = params.cursor + "=".repeat((4 - (params.cursor.length % 4)) % 4);
          const json = JSON.parse(atob(pad.replace(/-/g, "+").replace(/_/g, "/")));
          const idx = filtered.findIndex((t) => t.trace_id === json.t && t.start === json.s);
          start = idx >= 0 ? idx + 1 : 0;
        } catch {
          start = 0;
        }
      }
      const limit = Math.min(Math.max(params.limit || 50, 1), 200);
      const page = filtered.slice(start, start + limit);
      const last = page[page.length - 1];
      const next =
        start + limit < filtered.length && last
          ? encodeCursor(last.start, last.trace_id)
          : null;
      return { traces: page, next_cursor: next };
    }
  );
}

export async function getTraceSummary(traceId: string): Promise<TraceSummaryResponse> {
  return withFallback(
    () => realFetch(`/api/ui/traces/${encodeURIComponent(traceId)}/summary`),
    async () => {
      const fx = await loadMock();
      const s = fx.SUMMARIES[traceId];
      if (!s) throw new ApiError(404, "trace not found");
      return s;
    }
  );
}

export async function getRun(id: string, full = false): Promise<RunDetail> {
  const q = full ? "?full=1" : "";
  return withFallback(
    () => realFetch(`/api/ui/runs/${encodeURIComponent(id)}${q}`),
    async () => {
      const fx = await loadMock();
      let run = fx.RUNS[id];
      if (!run) {
        // synthesize skinny detail from span summary
        for (const sum of Object.values(fx.SUMMARIES)) {
          const row = sum.rows.find((r) => r.id === id);
          if (row) {
            run = {
              id: row.id,
              parent_run_id: row.parent_run_id,
              trace_id: sum.trace_id,
              session_id: "00000000-0000-0000-0000-000000000000",
              name: row.name,
              run_type: row.run_type,
              node: row.node,
              status: row.status,
              start_time: row.start_time,
              end_time: row.end_time,
              latency_ms: row.latency_ms,
              self_time_ms: row.self_time_ms,
              tokens: row.tokens,
              model: row.model,
              tags: row.tags,
              dotted_order: row.dotted_order,
              inputs: {},
              outputs: {},
              extra: {},
              events: [],
              serialized: null,
              error: row.has_error ? "error" : null,
              attachments: [],
              cold: !row.payload_hot,
              truncated: false,
            };
            break;
          }
        }
      }
      if (!run) throw new ApiError(404, "run not found");
      if (full && run.truncated) {
        return {
          ...run,
          truncated: false,
          outputs: { ok: false, error: "ticket already closed", full: true, note: "full payload" },
        };
      }
      return run;
    }
  );
}

export async function getMessages(id: string): Promise<MessagesResponse> {
  return withFallback(
    () => realFetch(`/api/ui/runs/${encodeURIComponent(id)}/messages`),
    async () => {
      const fx = await loadMock();
      return fx.MESSAGES[id] || fx.EMPTY_MESSAGES;
    }
  );
}

export async function getStats(params: { project?: string; from?: string; to?: string } = {}): Promise<StatsResponse> {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v) qs.set(k, v);
  }
  const qstr = qs.toString();
  return withFallback(
    () => realFetch(`/api/ui/stats${qstr ? `?${qstr}` : ""}`),
    async () => ({
      trace_count: 120,
      error_rate: 0.04,
      p50_ms: 820,
      p95_ms: 4100,
      tokens: { input: 1_000_000, output: 800_000, total: 1_800_000 },
      by_day: [{ day: "2026-10-27", traces: 40, errors: 2, tokens: 600000 }],
      by_model: [{ model: "gpt-4o-mini", calls: 800, tokens: 1500000 }],
    })
  );
}

export type StreamHandle = { close: () => void };

export function openTraceStream(
  params: Omit<TraceListParams, "cursor" | "limit" | "sort">,
  onEvent: (ev: StreamEvent) => void
): StreamHandle {
  if (resolved === "mock" || mode === "mock" || (mode === "auto" && resolved !== "real")) {
    // mock SSE: emit a heartbeat then occasional synthetic traces
    let n = 0;
    const hb = window.setInterval(() => onEvent({ type: "heartbeat", data: {} }), 15_000);
    const tick = window.setInterval(async () => {
      n++;
      const fx = await loadMock();
      const base = fx.TRACES[0];
      const syn: TraceSummary = {
        ...base,
        trace_id: `live0000-0000-4000-8000-${String(n).padStart(12, "0")}`,
        name: `live_tail_${n}`,
        start: new Date().toISOString(),
        end: new Date().toISOString(),
        duration_ms: 500 + n * 10,
        status: n % 5 === 0 ? "error" : "success",
        error_count: n % 5 === 0 ? 1 : 0,
      };
      onEvent({ type: "trace", id: encodeCursor(syn.start, syn.trace_id), data: syn });
    }, 4000);
    // mark mock if auto
    if (mode === "auto" && resolved === null) resolved = "mock";
    return {
      close: () => {
        clearInterval(hb);
        clearInterval(tick);
      },
    };
  }

  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") qs.set(k, String(v));
  }
  const key = getApiKey();
  // EventSource can't set headers; use fetch-stream for x-api-key
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
          /* ignore bad event */
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
      /* aborted or network */
    }
    void key;
  })();
  return { close: () => ac.abort() };
}
