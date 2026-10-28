/** Run: npx tsx src/fixtures/monitor.selftest.ts */
import { getMonitorFixture } from "./monitor.ts";
import { monitorBucketCount, monitorBucketForWindow } from "../lib/monitorFilters.ts";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

for (const w of ["1h", "24h", "7d"] as const) {
  const m = getMonitorFixture("11111111-1111-1111-1111-111111111111", w);
  assert(m.bucket === monitorBucketForWindow(w), `${w} bucket`);
  assert(m.series_traces.length === monitorBucketCount(w), `${w} points`);
  assert(m.series_latency.length === m.series_traces.length, `${w} latency len`);
  assert(m.series_tokens.length === m.series_traces.length, `${w} tokens len`);

  const tokIn = m.by_model.reduce((a, r) => a + r.tokens.input, 0);
  const tokOut = m.by_model.reduce((a, r) => a + r.tokens.output, 0);
  const tokTot = m.by_model.reduce((a, r) => a + r.tokens.total, 0);
  assert(tokIn === m.tokens.input, `${w} model input sum ${tokIn} vs ${m.tokens.input}`);
  assert(tokOut === m.tokens.output, `${w} model output sum`);
  assert(tokTot === m.tokens.total, `${w} model total sum`);

  const shareSum = m.by_model.reduce((a, r) => a + r.share, 0);
  assert(Math.abs(shareSum - 1) < 1e-9, `${w} model share sum`);

  // series_traces[].total sums to header trace_count (no series_trace_n)
  const seriesTot = m.series_traces.reduce((a, b) => a + b.total, 0);
  assert(seriesTot === m.trace_count, `${w} series_traces sum vs trace_count`);

  // by_tool token shapes: null | total-only | in/out
  assert(m.by_tool.length >= 3, `${w} tools`);
  const byName = Object.fromEntries(m.by_tool.map((t) => [t.tool, t]));
  assert(byName.search_docs?.tokens === null, `${w} search_docs tokens null`);
  const refund = byName.refund?.tokens;
  assert(refund != null && refund.input === 0 && refund.output === 0 && refund.total > 0, `${w} refund total-only`);
  const lookup = byName.lookup_order?.tokens;
  assert(
    lookup != null && lookup.input > 0 && lookup.output > 0 && lookup.total === lookup.input + lookup.output,
    `${w} lookup_order in/out`
  );
}

console.log("monitor.selftest: ok");
