/**
 * DEV / VITE_MOCK fixtures for M3.6 Monitor. Never statically imported from prod.
 */
import type {
  GraphWindow,
  MonitorBucketSize,
  MonitorResponse,
  ModelRow,
  ToolRow,
  Tokens,
  TraceBucket,
  LatencyBucket,
  TokenBucket,
} from "../api/types";

const SUPPORT_ID = "11111111-1111-1111-1111-111111111111";
const RESEARCH_ID = "22222222-2222-2222-2222-222222222222";

const T = (input: number, output: number): Tokens => ({
  input,
  output,
  total: input + output,
});

function bucketSize(w: GraphWindow): MonitorBucketSize {
  if (w === "1h") return "1m";
  if (w === "7d") return "1d";
  return "1h";
}

function windowMs(w: GraphWindow): number {
  if (w === "1h") return 3600_000;
  if (w === "7d") return 7 * 86400_000;
  return 86400_000;
}

function stepMs(b: MonitorBucketSize): number {
  if (b === "1m") return 60_000;
  if (b === "1d") return 86400_000;
  return 3600_000;
}

function alignStart(toMs: number, w: GraphWindow, step: number): number {
  const from = toMs - windowMs(w);
  if (w === "7d") {
    const d = new Date(from);
    d.setUTCHours(0, 0, 0, 0);
    return d.getTime();
  }
  return Math.floor(from / step) * step;
}

function buildSeries(w: GraphWindow, seed: number): {
  from: string;
  to: string;
  bucket: MonitorBucketSize;
  series_traces: TraceBucket[];
  series_latency: LatencyBucket[];
  series_tokens: TokenBucket[];
} {
  const bucket = bucketSize(w);
  const step = stepMs(bucket);
  const toMs = Date.parse("2026-10-28T08:00:00.000Z");
  let t0 = alignStart(toMs, w, step);
  const series_traces: TraceBucket[] = [];
  const series_latency: LatencyBucket[] = [];
  const series_tokens: TokenBucket[] = [];
  let i = 0;
  while (t0 < toMs) {
    const t_end = t0 + step;
    const wave = 0.7 + 0.3 * Math.sin((i + seed) / 3);
    const total = Math.max(0, Math.round((w === "1h" ? 8 : w === "7d" ? 1400 : 400) * wave + (i % 5)));
    const error = Math.round(total * (0.02 + ((i + seed) % 7) * 0.008));
    const success = Math.max(0, total - error);
    series_traces.push({
      t: new Date(t0).toISOString(),
      t_end: new Date(t_end).toISOString(),
      success,
      error,
      total,
    });
    const has = total > 0;
    series_latency.push({
      t: new Date(t0).toISOString(),
      t_end: new Date(t_end).toISOString(),
      p50_ms: has ? Math.round(350 + (i % 9) * 40 + seed * 10) : null,
      p99_ms: has ? Math.round(1800 + (i % 11) * 120 + seed * 50) : null,
    });
    const input = total * (800 + (i % 6) * 40);
    const output = total * (180 + (i % 4) * 20);
    series_tokens.push({
      t: new Date(t0).toISOString(),
      t_end: new Date(t_end).toISOString(),
      input,
      output,
      total: input + output,
    });
    t0 = t_end;
    i++;
  }
  return {
    from: series_traces[0]?.t ?? new Date(toMs - windowMs(w)).toISOString(),
    to: new Date(toMs).toISOString(),
    bucket,
    series_traces,
    series_latency,
    series_tokens,
  };
}

function modelsFor(seed: number): ModelRow[] {
  const rows: Omit<ModelRow, "share">[] = [
    {
      model: "gpt-4o-mini",
      calls: 8000 - seed * 100,
      error_count: 400,
      error_rate: 0,
      p50_ms: 380,
      p99_ms: 2100,
      tokens: T(9_000_000, 2_000_000),
    },
    {
      model: "gpt-4o",
      calls: 1500,
      error_count: 90,
      error_rate: 0,
      p50_ms: 620,
      p99_ms: 4100,
      tokens: T(2_500_000, 800_000),
    },
    {
      model: "claude-3-5-sonnet",
      calls: 500 + seed * 20,
      error_count: 40,
      error_rate: 0,
      p50_ms: 900,
      p99_ms: 5200,
      tokens: T(800_000, 350_000),
    },
  ];
  const sum = rows.reduce((a, r) => a + r.calls, 0) || 1;
  return rows
    .map((r) => ({
      ...r,
      error_rate: r.calls ? r.error_count / r.calls : 0,
      share: r.calls / sum,
    }))
    .sort((a, b) => b.calls - a.calls || a.model.localeCompare(b.model));
}

function toolsFor(seed: number, empty: boolean): ToolRow[] {
  if (empty) return [];
  const rows: Omit<ToolRow, "share">[] = [
    {
      tool: "search_docs",
      calls: 1800,
      error_count: 40,
      error_rate: 0,
      p50_ms: 60,
      p99_ms: 400,
      tokens: T(0, 0),
    },
    {
      tool: "refund",
      calls: 1200 - seed * 10,
      error_count: 150,
      error_rate: 0,
      p50_ms: 90,
      p99_ms: 800,
      tokens: T(0, 0),
    },
    {
      tool: "lookup_order",
      calls: 900,
      error_count: 20,
      error_rate: 0,
      p50_ms: 45,
      p99_ms: 220,
      tokens: T(0, 0),
    },
  ];
  const sum = rows.reduce((a, r) => a + r.calls, 0) || 1;
  return rows
    .map((r) => ({
      ...r,
      error_rate: r.calls ? r.error_count / r.calls : 0,
      share: r.calls / sum,
    }))
    .sort((a, b) => b.calls - a.calls || a.tool.localeCompare(b.tool));
}

export function getMonitorFixture(projectId: string, window: GraphWindow = "24h"): MonitorResponse {
  const isResearch = projectId === RESEARCH_ID || projectId === "research";
  const isLocal = projectId === "33333333-3333-3333-3333-333333333333" || projectId === "local-dev";
  const seed = isResearch ? 2 : isLocal ? 5 : 0;
  const name = isResearch ? "research" : isLocal ? "local-dev" : "support";
  const id = isResearch ? RESEARCH_ID : isLocal ? "33333333-3333-3333-3333-333333333333" : SUPPORT_ID;
  const series = buildSeries(window, seed);
  const success_count = series.series_traces.reduce((a, b) => a + b.success, 0);
  const error_count = series.series_traces.reduce((a, b) => a + b.error, 0);
  const tokens = series.series_tokens.reduce(
    (a, b) => ({ input: a.input + b.input, output: a.output + b.output, total: a.total + b.total }),
    { input: 0, output: 0, total: 0 }
  );
  const lat = series.series_latency.filter((x) => x.p50_ms != null);
  const p50 = lat.length ? lat[Math.floor(lat.length / 2)].p50_ms : null;
  const p99 = lat.length ? Math.max(...lat.map((x) => x.p99_ms || 0)) : null;
  return {
    project_id: id,
    project: name,
    window,
    from: series.from,
    to: series.to,
    bucket: series.bucket,
    trace_count: success_count + error_count,
    success_count,
    error_count,
    p50_ms: p50,
    p99_ms: p99,
    tokens,
    cost: null,
    series_traces: series.series_traces,
    series_latency: series.series_latency,
    series_tokens: series.series_tokens,
    by_model: modelsFor(seed),
    by_tool: toolsFor(seed, isLocal),
  };
}

/** Stable weak etag for mock polling demos. */
export function monitorEtag(m: MonitorResponse): string {
  return `"m36-${m.project_id}-${m.window}-${m.trace_count}-${m.error_count}-${m.series_traces.length}"`;
}
