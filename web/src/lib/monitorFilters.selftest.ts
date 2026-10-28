/** Run: npx tsx src/lib/monitorFilters.selftest.ts */
import {
  tracesHref,
  drillTraceSegment,
  drillBucket,
  drillModel,
  drillTool,
  drillToParams,
  monitorBucketForWindow,
  monitorBucketCount,
  monitorBucketStepMs,
} from "./monitorFilters.ts";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

{
  assert(monitorBucketForWindow("1h") === "1m", "1h→1m");
  assert(monitorBucketCount("1h") === 60, "1h points");
  assert(monitorBucketStepMs("1m") === 60_000, "1m step");

  assert(monitorBucketForWindow("24h") === "1h", "24h→1h");
  assert(monitorBucketCount("24h") === 24, "24h points");
  assert(monitorBucketStepMs("1h") === 3600_000, "1h step");

  assert(monitorBucketForWindow("7d") === "6h", "7d→6h");
  assert(monitorBucketCount("7d") === 28, "7d points");
  assert(monitorBucketStepMs("6h") === 6 * 3600_000, "6h step");
}

{
  const d = drillTraceSegment("support", "2026-10-27T08:00:00.000Z", "2026-10-27T09:00:00.000Z", "error");
  const href = tracesHref(d);
  assert(href.includes("project=support"), "project");
  assert(href.includes("status=error"), "status=error");
  assert(href.includes("from=2026-10-27T08"), "from");
  assert(href.includes("to=2026-10-27T09"), "to");
  assert(!href.includes("model="), "no model");
}

{
  const d = drillTraceSegment("support", "a", "b", "success");
  assert(drillToParams(d).status === "success", "success segment");
}

{
  const d = drillBucket("support", "t0", "t1");
  const p = drillToParams(d);
  assert(p.from === "t0" && p.to === "t1", "bucket bounds");
  assert(!p.status && !p.model && !p.tool, "no extras");
}

{
  // 6h bucket bounds → from/to filter
  const d = drillBucket("support", "2026-10-21T00:00:00.000Z", "2026-10-21T06:00:00.000Z");
  const p = drillToParams(d);
  assert(p.from === "2026-10-21T00:00:00.000Z", "6h from");
  assert(p.to === "2026-10-21T06:00:00.000Z", "6h to");
}

{
  const d = drillModel("support", "W0", "W1", "gpt-4o-mini", true);
  const p = drillToParams(d);
  assert(p.model === "gpt-4o-mini", "model");
  assert(p.status === "error", "model errors");
  assert(tracesHref(d).includes("model=gpt-4o-mini"), "href model");
}

{
  const d = drillTool("support", "W0", "W1", "refund", false);
  const p = drillToParams(d);
  assert(p.tool === "refund", "tool");
  assert(!p.status, "no status");
  assert(!p.model, "not model");
  const err = drillTool("support", "W0", "W1", "refund", true);
  assert(drillToParams(err).status === "error", "tool errors");
}

console.log("monitorFilters.selftest: ok");
