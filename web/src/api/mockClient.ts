/**
 * DEV / VITE_MOCK=1 only. Production builds must never import this module
 * (see client.ts guards) so Vite tree-shakes fixtures out of the embed.
 */
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
  GraphResponse,
  GraphParams,
  PathsResponse,
  PathsParams,
  GraphWindow,
  MonitorResponse,
  MonitorParams,
} from "./types";

function encodeCursor(start: string, traceId: string): string {
  const json = JSON.stringify({ s: start, t: traceId });
  return btoa(json).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function fx() {
  return import("../fixtures/data");
}

function filterTraces(all: TraceSummary[], params: TraceListParams): TraceSummary[] {
  let list = all.slice();
  if (params.project) list = list.filter((t) => t.project === params.project);
  if (params.status) list = list.filter((t) => t.status === params.status);
  if (params.q) {
    const q = params.q.toLowerCase();
    list = list.filter((t) => t.name.toLowerCase().includes(q));
  }
  if (params.model) list = list.filter((t) => t.model === params.model);
  // tool filter is server-side in real API; mock keeps all (deep-link smoke)
  void params.tool;
  if (params.min_tokens != null) list = list.filter((t) => t.tokens.total >= params.min_tokens!);
  if (params.min_duration_ms != null) {
    list = list.filter((t) => t.duration_ms != null && t.duration_ms >= params.min_duration_ms!);
  }
  // M3.5 drill-down (mock): use fixture allow-lists when present.
  // Dynamic import would be async; callers already await fx() before filter in listTraces —
  // so we apply id filters via a sync cache filled in listTraces.
  if (params.node || params.edge || params.path || params.node_fail) {
    const ids = mockDrillIds(params);
    if (ids) list = list.filter((t) => ids.has(t.trace_id));
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


let _drill: typeof import("../fixtures/graph") | null = null;

function mockDrillIds(params: TraceListParams): Set<string> | null {
  const map = _drill?.MOCK_FILTER_TRACE_IDS;
  if (!map) return null;
  const keys: string[] = [];
  if (params.node_fail) keys.push(`node_fail:${params.node_fail}`);
  else if (params.node) keys.push(`node:${params.node}`);
  if (params.edge) keys.push(`edge:${params.edge}`);
  if (params.path) keys.push(`path:${params.path}`);
  const ids = new Set<string>();
  let hit = false;
  for (const k of keys) {
    const arr = map[k];
    if (arr) {
      hit = true;
      for (const id of arr) ids.add(id);
    }
  }
  return hit ? ids : null;
}

/**
 * Real LangGraph captures for DEV mock. Loaded only via dynamic import from this
 * mock module (tree-shaken from prod). Canonical copies for selftests live in
 * web/testdata/lgtest (never embedded).
 */
const LGTEST_GRAPH: Record<string, () => Promise<{ default: unknown }>> = {
  "lgtest-supervisor-live5-r0": () => import("../fixtures/lgtest/lgtest-supervisor-live5-r0.graph.json"),
  "lgtest-parallel_fanout-L090953-r0": () => import("../fixtures/lgtest/lgtest-parallel_fanout-L090953-r0.graph.json"),
  "lgtest-react_tools-smoke1-r0": () => import("../fixtures/lgtest/lgtest-react_tools-smoke1-r0.graph.json"),
  "lgtest-subgraph_nested-L090953s-r9": () => import("../fixtures/lgtest/lgtest-subgraph_nested-L090953s-r9.graph.json"),
};
const LGTEST_PATHS: Record<string, () => Promise<{ default: unknown }>> = {
  "lgtest-supervisor-live5-r0": () => import("../fixtures/lgtest/lgtest-supervisor-live5-r0.paths.json"),
  "lgtest-parallel_fanout-L090953-r0": () => import("../fixtures/lgtest/lgtest-parallel_fanout-L090953-r0.paths.json"),
  "lgtest-react_tools-smoke1-r0": () => import("../fixtures/lgtest/lgtest-react_tools-smoke1-r0.paths.json"),
  "lgtest-subgraph_nested-L090953s-r9": () => import("../fixtures/lgtest/lgtest-subgraph_nested-L090953s-r9.paths.json"),
};

export async function getGraph(projectId: string, params: GraphParams = {}): Promise<GraphResponse> {
  const g = await import("../fixtures/graph");
  _drill = g;
  const loader = LGTEST_GRAPH[projectId];
  if (loader) return (await loader()).default as GraphResponse;
  const w = (params.window || "24h") as GraphWindow;
  return g.getGraphFixture(projectId, w);
}

export async function getPaths(projectId: string, params: PathsParams = {}): Promise<PathsResponse> {
  const g = await import("../fixtures/graph");
  _drill = g;
  const loader = LGTEST_PATHS[projectId];
  if (loader) return (await loader()).default as PathsResponse;
  const w = (params.window || "24h") as GraphWindow;
  return g.getPathsFixture(projectId, w);
}

export async function listProjects(): Promise<ProjectCard[]> {
  return (await fx()).PROJECTS;
}

export async function listTraces(params: TraceListParams = {}): Promise<TraceListResponse> {
  const data = await fx();
  if (params.node || params.edge || params.path || params.node_fail) {
    _drill = await import("../fixtures/graph");
  }
  const all = data.makeManyTraces(10_000);
  const filtered = filterTraces(all, params);
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
    start + limit < filtered.length && last ? encodeCursor(last.start, last.trace_id) : null;
  return { traces: page, next_cursor: next };
}

export async function getTraceSummary(traceId: string): Promise<TraceSummaryResponse> {
  const s = (await fx()).SUMMARIES[traceId];
  if (!s) {
    const err = new Error("trace not found") as Error & { status: number; detail: string };
    err.status = 404;
    err.detail = "trace not found";
    throw err;
  }
  return s;
}

export async function getRun(id: string, full = false): Promise<RunDetail> {
  const data = await fx();
  let run = data.RUNS[id];
  if (!run) {
    for (const sum of Object.values(data.SUMMARIES)) {
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
  if (!run) {
    const err = new Error("run not found") as Error & { status: number; detail: string };
    err.status = 404;
    err.detail = "run not found";
    throw err;
  }
  if (full && run.truncated) {
    return {
      ...run,
      truncated: false,
      outputs: { ok: false, error: "ticket already closed", full: true, note: "full payload" },
    };
  }
  return run;
}

export async function getMessages(id: string): Promise<MessagesResponse> {
  const data = await fx();
  return data.MESSAGES[id] || data.EMPTY_MESSAGES;
}

export async function getStats(): Promise<StatsResponse> {
  return {
    trace_count: 120,
    error_rate: 0.04,
    p50_ms: 820,
    p95_ms: 4100,
    tokens: { input: 1_000_000, output: 800_000, total: 1_800_000 },
    by_day: [{ day: "2026-10-27", traces: 40, errors: 2, tokens: 600000 }],
    by_model: [{ model: "gpt-4o-mini", calls: 800, tokens: 1500000 }],
  };
}

export type StreamHandle = { close: () => void };

export function openTraceStream(
  params: Omit<TraceListParams, "cursor" | "limit" | "sort">,
  onEvent: (ev: StreamEvent) => void,
  opts?: { getOpenTraceId?: () => string | null | undefined }
): StreamHandle {
  void params;
  let n = 0;
  const hb = window.setInterval(() => onEvent({ type: "heartbeat", data: {} }), 15_000);
  const tick = window.setInterval(async () => {
    n++;
    const data = await fx();
    const base = data.TRACES[0];
    if (n % 2 === 1) {
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
    } else {
      const openId =
        opts?.getOpenTraceId?.() ||
        data.TRACES.find((t) => t.status === "running")?.trace_id ||
        data.TRACES[0].trace_id;
      const sum = data.SUMMARIES[openId];
      const parent = sum?.rows[0];
      if (parent) {
        const child: SpanSummary = {
          id: `live-run-${n}`,
          parent_run_id: parent.id,
          name: `live_child_${n}`,
          run_type: "tool",
          node: "tail",
          status: "success",
          depth: (parent.depth || 0) + 1,
          offset_ms: (parent.latency_ms || 0) + n * 50,
          latency_ms: 100 + n,
          self_time_ms: 100 + n,
          start_time: new Date().toISOString(),
          end_time: new Date().toISOString(),
          tokens: { input: 0, output: 0, total: 0 },
          model: null,
          tags: [],
          dotted_order: `${parent.dotted_order}.${String(n).padStart(4, "0")}`,
          has_error: false,
          payload_hot: true,
          attachment_count: 0,
        };
        const bump: SpanSummary = {
          ...parent,
          latency_ms: (parent.latency_ms || 0) + n,
          status: parent.status === "running" ? "running" : parent.status,
        };
        onEvent({
          type: "run_upsert",
          id: encodeCursor(new Date().toISOString(), openId),
          data: { trace_id: openId, row: n % 4 === 0 ? bump : child },
        });
      }
    }
  }, 2500);
  return {
    close: () => {
      clearInterval(hb);
      clearInterval(tick);
    },
  };
}


export type MonitorFetchResult = {
  data: MonitorResponse | null;
  etag: string | null;
  unchanged: boolean;
};

export async function getMonitor(
  projectId: string,
  params: MonitorParams = {},
  opts: { etag?: string | null } = {}
): Promise<MonitorFetchResult> {
  const m = await import("../fixtures/monitor");
  const w = (params.window || "24h") as GraphWindow;
  const data = m.getMonitorFixture(projectId, w);
  const etag = m.monitorEtag(data);
  if (opts.etag && opts.etag === etag) {
    return { data: null, etag, unchanged: true };
  }
  return { data, etag, unchanged: false };
}
