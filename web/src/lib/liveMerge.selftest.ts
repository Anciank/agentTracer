/** Run with: npx tsx src/lib/liveMerge.selftest.ts */
import {
  mergeSpanIntoRows,
  applyRunUpsert,
  patchTraceSummaryInList,
  mergePendingTraces,
} from "./liveMerge.ts";
import type { SpanSummary, TraceSummary, TraceSummaryResponse } from "../api/types";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

const base = (partial: Partial<SpanSummary> & { id: string }): SpanSummary => ({
  parent_run_id: null,
  name: "n",
  run_type: "chain",
  node: null,
  status: "success",
  depth: 0,
  offset_ms: 0,
  latency_ms: 10,
  self_time_ms: 10,
  start_time: "2026-10-27T15:00:00.000Z",
  end_time: "2026-10-27T15:00:00.010Z",
  tokens: { input: 0, output: 0, total: 0 },
  model: null,
  tags: [],
  dotted_order: "1",
  has_error: false,
  payload_hot: true,
  attachment_count: 0,
  ...partial,
});

const a = base({ id: "a", name: "root", dotted_order: "1" });
const b = base({ id: "b", name: "child", parent_run_id: "a", depth: 1, dotted_order: "1.1" });

{
  const merged = mergeSpanIntoRows([a, b], base({ id: "a", latency_ms: 99, name: "root2" }));
  assert(merged.length === 2, "update keeps length");
  assert(merged[0].latency_ms === 99, "latency updated");
  assert(merged[0].name === "root2", "name updated");
  assert(merged[1].id === "b", "other row intact");
}

{
  const c = base({ id: "c", name: "new", dotted_order: "1.2" });
  const merged = mergeSpanIntoRows([a, b], c);
  assert(merged.length === 3, "append grows");
  assert(merged[2].id === "c", "appended at end");
}

{
  const cached: TraceSummaryResponse = {
    trace_id: "t1",
    span_ms: 10,
    summary: {
      trace_id: "t1",
      name: "agent",
      project: "support",
      start: a.start_time,
      end: null,
      duration_ms: null,
      run_count: 1,
      error_count: 0,
      running_count: 1,
      status: "running",
      tokens: { input: 0, output: 0, total: 0 },
      model: null,
      run_types: {},
    },
    rows: [base({ id: "a", status: "running", latency_ms: null, end_time: null })],
  };
  const next = applyRunUpsert(
    cached,
    base({ id: "b", parent_run_id: "a", status: "success", offset_ms: 5, latency_ms: 20 })
  );
  assert(next.rows.length === 2, "upsert appends child");
  assert(next.summary.run_count === 2, "run_count bumped");
  assert(next.rows[0].id === "a", "root id preserved for selection");
}

{
  const t1: TraceSummary = {
    trace_id: "t1",
    name: "a",
    project: "p",
    start: "2026-10-27T15:00:00.000Z",
    end: null,
    duration_ms: null,
    run_count: 1,
    error_count: 0,
    running_count: 1,
    status: "running",
    tokens: { input: 1, output: 0, total: 1 },
    model: null,
    run_types: {},
  };
  const t2 = { ...t1, trace_id: "t2", name: "b" };
  const patched = patchTraceSummaryInList([t1, t2], { trace_id: "t2", run_count: 9 });
  assert(patched[0].trace_id === "t1", "no reorder");
  assert(patched[1].run_count === 9, "in-place patch");
}

{
  const cur: TraceSummary[] = [
    {
      trace_id: "x0",
      name: "a",
      project: "p",
      start: a.start_time,
      end: a.end_time,
      duration_ms: a.latency_ms,
      run_count: 1,
      error_count: 0,
      running_count: 0,
      status: "success",
      tokens: a.tokens,
      model: null,
      run_types: {},
    },
    {
      trace_id: "x1",
      name: "b",
      project: "p",
      start: b.start_time,
      end: b.end_time,
      duration_ms: b.latency_ms,
      run_count: 1,
      error_count: 0,
      running_count: 0,
      status: "success",
      tokens: b.tokens,
      model: null,
      run_types: {},
    },
  ];
  const pending = [{ ...cur[0], trace_id: "new1", name: "fresh" }];
  const merged = mergePendingTraces(cur, pending);
  assert(merged[0].trace_id === "new1", "pending prepends");
  assert(merged.length === 3, "merge length");
}

console.log("liveMerge.selftest: ok");
