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

  const seriesTot = m.series_traces.reduce((a, b) => a + b.total, 0);
  assert(seriesTot === m.trace_count, `${w} series vs header traces`);

  for (const t of m.by_tool) {
    assert(t.tokens === null, `${w} tool tokens null`);
  }
}

console.log("monitor.selftest: ok");
