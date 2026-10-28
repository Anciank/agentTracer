/** Run: npx tsx src/lib/monitorMerge.selftest.ts */
import {
  applyMonitorRefresh,
  monitorFingerprint,
  shouldPollMonitor,
  DEFAULT_MONITOR_UI,
} from "./monitorMerge.ts";
import type { MonitorResponse } from "../api/types";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

const base = (over: Partial<MonitorResponse> = {}): MonitorResponse => ({
  project_id: "11111111-1111-1111-1111-111111111111",
  project: "support",
  window: "24h",
  from: "2026-10-27T08:00:00.000Z",
  to: "2026-10-28T08:00:00.000Z",
  bucket: "1h",
  trace_count: 100,
  success_count: 90,
  error_count: 10,
  p50_ms: 400,
  p99_ms: 2000,
  tokens: { input: 1, output: 2, total: 3 },
  cost: null,
  series_traces: [
    { t: "a", t_end: "b", success: 4, error: 1, total: 5 },
    { t: "b", t_end: "c", success: 5, error: 0, total: 5 },
  ],
  series_latency: [
    { t: "a", t_end: "b", p50_ms: 100, p99_ms: 200 },
    { t: "b", t_end: "c", p50_ms: 110, p99_ms: 210 },
  ],
  series_tokens: [
    { t: "a", t_end: "b", input: 1, output: 1, total: 2 },
    { t: "b", t_end: "c", input: 2, output: 1, total: 3 },
  ],
  by_model: [],
  by_tool: [],
  ...over,
});

{
  const prev = base();
  const ui = { ...DEFAULT_MONITOR_UI, hoverIdx: 1, scrollTop: 120, modelSort: { key: "error_rate", dir: "asc" as const } };
  const { data, ui: nextUi } = applyMonitorRefresh(prev, prev, ui, { unchanged: true });
  assert(data === prev, "unchanged keeps data ref");
  assert(nextUi.scrollTop === 120, "scroll kept");
  assert(nextUi.modelSort.key === "error_rate", "sort kept");
}

{
  const prev = base({ trace_count: 100 });
  const next = base({ trace_count: 105, error_count: 12 });
  const ui = { ...DEFAULT_MONITOR_UI, hoverIdx: 1, tooltipPinned: true, scrollTop: 50 };
  const { data, ui: nextUi } = applyMonitorRefresh(prev, next, ui);
  assert(data !== prev, "new data ref");
  assert(data!.trace_count === 105, "updated");
  assert(nextUi.hoverIdx === 1, "hover kept when in range");
  assert(nextUi.tooltipPinned === true, "pin kept");
  assert(nextUi.scrollTop === 50, "scroll kept");
}

{
  const prev = base();
  const next = base({
    series_traces: [{ t: "only", t_end: "x", success: 1, error: 0, total: 1 }],
    series_latency: [{ t: "only", t_end: "x", p50_ms: 1, p99_ms: 2 }],
    series_tokens: [{ t: "only", t_end: "x", input: 1, output: 0, total: 1 }],
  });
  const ui = { ...DEFAULT_MONITOR_UI, hoverIdx: 5 };
  const { ui: nextUi } = applyMonitorRefresh(prev, next, ui);
  assert(nextUi.hoverIdx === null, "hover clamped out of range");
}

{
  const a = base();
  const b = base();
  assert(monitorFingerprint(a) === monitorFingerprint(b), "same fingerprint");
  assert(monitorFingerprint(base({ trace_count: 99 })) !== monitorFingerprint(a), "diff fingerprint");
}

{
  assert(shouldPollMonitor(DEFAULT_MONITOR_UI, true) === true, "poll ok");
  assert(shouldPollMonitor(DEFAULT_MONITOR_UI, false) === false, "hidden pauses");
  assert(
    shouldPollMonitor({ ...DEFAULT_MONITOR_UI, tooltipPinned: true }, true) === false,
    "pinned pauses"
  );
}

console.log("monitorMerge.selftest: ok");
