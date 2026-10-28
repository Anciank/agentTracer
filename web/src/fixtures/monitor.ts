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
import {
  monitorBucketCount,
  monitorBucketForWindow,
  monitorBucketStepMs,
} from "../lib/monitorFilters";

const SUPPORT_ID = "11111111-1111-1111-1111-111111111111";
const RESEARCH_ID = "22222222-2222-2222-2222-222222222222";

const T = (input: number, output: number): Tokens => ({
  input,
  output,
  total: input + output,
});

function windowMs(w: GraphWindow): number {
  if (w === "1h") return 3600_000;
  if (w === "7d") return 7 * 86400_000;
  return 86400_000;
}

function alignStart(toMs: number, w: GraphWindow, step: number): number {
  // Align so we get exactly expected point counts ending at `to`.
  return toMs - monitorBucketCount(w) * step;
}

function buildSeries(w: GraphWindow, seed: number): {
  from: string;
  to: string;
  bucket: MonitorBucketSize;
  series_traces: TraceBucket[];
  series_latency: LatencyBucket[];
  series_tokens: TokenBucket[];
} {
  const bucket = monitorBucketForWindow(w);
  const step = monitorBucketStepMs(bucket);
  const nExpect = monitorBucketCount(w);
  const toMs = Date.parse("2026-10-28T08:00:00.000Z");
  let t0 = alignStart(toMs, w, step);
  const series_traces: TraceBucket[] = [];
  const series_latency: LatencyBucket[] = [];
  const series_tokens: TokenBucket[] = [];
  const base =
    w === "1h" ? 8 : w === "7d" ? 280 : 300; // per-bucket baseline → sensible totals
  for (let i = 0; i < nExpect; i++) {
    const t_end = t0 + step;
    const wave = 0.75 + 0.25 * Math.sin((i + seed) / 3);
    const total = Math.max(0, Math.round(base * wave + (i % 5)));
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
    // ~1.1k tokens per trace on average
    const input = total * (900 + (i % 6) * 20);
    const output = total * (200 + (i % 4) * 15);
    series_tokens.push({
      t: new Date(t0).toISOString(),
      t_end: new Date(t_end).toISOString(),
      input,
      output,
      total: input + output,
    });
    t0 = t_end;
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

/** Allocate models so token totals + shares reconcile with header. */
function modelsFor(
  seed: number,
  traceCount: number,
  headerTokens: Tokens
): ModelRow[] {
  // Relative weights (sums to 1 after normalize)
  const weights = [
    { model: "gpt-4o-mini", w: 0.72, p50: 380, p99: 2100, err: 0.05 },
    { model: "gpt-4o", w: 0.18, p50: 620, p99: 4100, err: 0.06 },
    { model: "claude-3-5-sonnet", w: 0.1, p50: 900, p99: 5200, err: 0.08 },
  ];
  // Slight seed nudge without breaking sum
  const nudge = seed * 0.01;
  weights[0].w = Math.max(0.5, weights[0].w - nudge);
  weights[2].w = Math.min(0.25, weights[2].w + nudge);
  const wSum = weights.reduce((a, x) => a + x.w, 0);
  // ~1.4 LLM calls per root trace (multi-step agents)
  const totalCalls = Math.max(1, Math.round(traceCount * 1.4));

  const rows: ModelRow[] = weights.map((x, idx) => {
    const share = x.w / wSum;
    const calls =
      idx === weights.length - 1
        ? Math.max(1, totalCalls - weights.slice(0, -1).reduce(
            (a, y, j) => a + Math.max(1, Math.round(totalCalls * (y.w / wSum))),
            0
          ))
        : Math.max(1, Math.round(totalCalls * share));
    const error_count = Math.round(calls * x.err);
    const input =
      idx === weights.length - 1
        ? headerTokens.input -
          weights.slice(0, -1).reduce(
            (a, y) => a + Math.round(headerTokens.input * (y.w / wSum)),
            0
          )
        : Math.round(headerTokens.input * share);
    const output =
      idx === weights.length - 1
        ? headerTokens.output -
          weights.slice(0, -1).reduce(
            (a, y) => a + Math.round(headerTokens.output * (y.w / wSum)),
            0
          )
        : Math.round(headerTokens.output * share);
    return {
      model: x.model,
      calls,
      share: calls / totalCalls,
      error_count,
      error_rate: calls ? error_count / calls : 0,
      p50_ms: x.p50 + seed * 5,
      p99_ms: x.p99 + seed * 20,
      tokens: T(Math.max(0, input), Math.max(0, output)),
    };
  });

  // Fix share to sum ~1 from calls; recompute from actual calls
  const callSum = rows.reduce((a, r) => a + r.calls, 0) || 1;
  for (const r of rows) r.share = r.calls / callSum;

  // Ensure token totals match header exactly
  const tokIn = rows.reduce((a, r) => a + r.tokens.input, 0);
  const tokOut = rows.reduce((a, r) => a + r.tokens.output, 0);
  rows[0].tokens.input += headerTokens.input - tokIn;
  rows[0].tokens.output += headerTokens.output - tokOut;
  rows[0].tokens.total = rows[0].tokens.input + rows[0].tokens.output;

  return rows.sort((a, b) => b.calls - a.calls || a.model.localeCompare(b.model));
}

function toolsFor(seed: number, empty: boolean, traceCount: number): ToolRow[] {
  if (empty) return [];
  // Tool calls ≈ 0.55× traces (not every trace hits every tool)
  const totalToolCalls = Math.max(3, Math.round(traceCount * 0.55));
  const weights = [
    { tool: "search_docs", w: 0.46, p50: 60, p99: 400, err: 0.022 },
    { tool: "refund", w: 0.31, p50: 90, p99: 800, err: 0.125 },
    { tool: "lookup_order", w: 0.23, p50: 45, p99: 220, err: 0.022 },
  ];
  const wSum = weights.reduce((a, x) => a + x.w, 0);
  const rows: ToolRow[] = weights.map((x, idx) => {
    const share = x.w / wSum;
    const calls =
      idx === weights.length - 1
        ? Math.max(1, totalToolCalls - weights.slice(0, -1).reduce(
            (a, y) => a + Math.max(1, Math.round(totalToolCalls * (y.w / wSum))),
            0
          ))
        : Math.max(1, Math.round(totalToolCalls * share) - (idx === 1 ? seed : 0));
    const error_count = Math.round(calls * x.err);
    // Demo shapes matching API.md: null | total-only | real in/out SUMs.
    let tokens: ToolRow["tokens"] = null;
    if (idx === 0) {
      tokens = null; // typical tool — no usage
    } else if (idx === 1) {
      const total = Math.max(12, calls * 40);
      tokens = { input: 0, output: 0, total }; // total-only
    } else {
      const input = Math.max(8, calls * 25);
      const output = Math.max(4, calls * 10);
      tokens = { input, output, total: input + output };
    }
    return {
      tool: x.tool,
      calls,
      share: 0,
      error_count,
      error_rate: calls ? error_count / calls : 0,
      p50_ms: x.p50,
      p99_ms: x.p99,
      tokens,
    };
  });
  const callSum = rows.reduce((a, r) => a + r.calls, 0) || 1;
  for (const r of rows) r.share = r.calls / callSum;
  return rows.sort((a, b) => b.calls - a.calls || a.tool.localeCompare(b.tool));
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
  const trace_count = success_count + error_count;
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
    trace_count,
    success_count,
    error_count,
    p50_ms: p50,
    p99_ms: p99,
    tokens,
    cost: null,
    series_traces: series.series_traces,
    series_latency: series.series_latency,
    series_tokens: series.series_tokens,
    by_model: modelsFor(seed, trace_count, tokens),
    by_tool: toolsFor(seed, isLocal, trace_count),
  };
}

/** Stable weak etag for mock polling demos. */
export function monitorEtag(m: MonitorResponse): string {
  return `"m36-${m.project_id}-${m.window}-${m.trace_count}-${m.error_count}-${m.series_traces.length}"`;
}
