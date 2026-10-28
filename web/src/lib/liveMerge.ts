import type { SpanSummary, TraceSummary, TraceSummaryResponse } from "../api/types";

/** Merge a run_upsert span into waterfall rows by id (update existing, else append). Preserves relative order of prior rows. */
export function mergeSpanIntoRows(rows: SpanSummary[], row: SpanSummary): SpanSummary[] {
  const idx = rows.findIndex((r) => r.id === row.id);
  if (idx >= 0) {
    const next = rows.slice();
    next[idx] = { ...rows[idx], ...row };
    return next;
  }
  return [...rows, row];
}

/** Apply run_upsert to a cached TraceSummaryResponse. Updates summary.run_count / status lightly. */
export function applyRunUpsert(
  cached: TraceSummaryResponse,
  row: SpanSummary
): TraceSummaryResponse {
  const rows = mergeSpanIntoRows(cached.rows, row);
  const error_count = rows.filter((r) => r.has_error || r.status === "error").length;
  const running_count = rows.filter((r) => r.status === "running").length;
  let status: TraceSummary["status"] = "success";
  if (error_count > 0) status = "error";
  else if (running_count > 0) status = "running";
  const summary: TraceSummary = {
    ...cached.summary,
    run_count: rows.length,
    error_count,
    running_count,
    status,
  };
  const span_ms = Math.max(
    cached.span_ms,
    ...rows.map((r) => (r.offset_ms || 0) + (r.latency_ms || 0))
  );
  return { ...cached, rows, summary, span_ms };
}

/** Update a TraceSummary list row in place (no reorder). Returns same array ref if missing. */
export function patchTraceSummaryInList(
  traces: TraceSummary[],
  patch: Partial<TraceSummary> & { trace_id: string }
): TraceSummary[] {
  const idx = traces.findIndex((t) => t.trace_id === patch.trace_id);
  if (idx < 0) return traces;
  const next = traces.slice();
  next[idx] = { ...traces[idx], ...patch };
  return next;
}

/** Merge queued new traces in front; keep selection if still present. */
export function mergePendingTraces(
  current: TraceSummary[],
  pending: TraceSummary[]
): TraceSummary[] {
  if (!pending.length) return current;
  const seen = new Set(pending.map((t) => t.trace_id));
  return [...pending, ...current.filter((t) => !seen.has(t.trace_id))];
}
