/**
 * Perf + correctness self-test for agent map layout.
 * Run: npx tsx src/lib/graphLayout.selftest.ts
 */
import { FAIL_ERROR_RATE, isFailingNode, isWarnNode, layoutGraph, pickOther, withoutOther } from "./graphLayout";
import { makeLargeGraph, supportGraph } from "../fixtures/graph";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

function main() {
  const g = supportGraph("24h");
  const other = pickOther(g.nodes);
  assert(other, "fixture embeds __other__");
  const lay = layoutGraph(g.nodes, [...g.edges, ...g.edges_hidden], {
    expandOther: false,
  });
  assert(lay.nodes.length === withoutOther(g.nodes).length + 1, "other fold present");
  assert(lay.nodes.some((n) => n.isOther), "other node flagged");
  assert(lay.nodes.every((n) => Number.isFinite(n.x) && Number.isFinite(n.y)), "finite coords");
  // top-to-bottom: avoid ultra-wide horizontal ribbon for the support DAG
  assert(lay.height >= lay.width * 0.35 || lay.nodes.length < 5, "TB aspect (not ultra-wide)");
  const tools = lay.nodes.find((n) => n.key === "tools")!;
  assert(isFailingNode(tools.stats), "tools rate >= fail threshold");
  const retrieve = lay.nodes.find((n) => n.key === "retrieve");
  if (retrieve) assert(isWarnNode(retrieve.stats) || retrieve.stats.error_count === 0 || retrieve.stats.error_rate >= FAIL_ERROR_RATE, "retrieve warn-or-ok");

  // failing nodes always in layout (tools, escalate have errors)
  assert(lay.nodes.some((n) => n.key === "tools" && n.stats.error_count > 0), "tools visible");
  assert(lay.nodes.some((n) => n.key === "escalate"), "escalate visible");
  // loops drawn once
  const loops = lay.edges.filter((e) => e.stats.loop && e.from === e.to);
  assert(loops.length >= 1, "self-loop present");
  const toolsLoop = lay.edges.filter((e) => e.from === "tools" && e.to === "tools");
  assert(toolsLoop.length === 1, "tools loop once");

  // Large fixture perf
  const large = makeLargeGraph(40, 8);
  const allEdges = [...large.edges, ...large.edges_hidden];
  assert(large.nodes.length <= 40, "top-n + failing bound");
  assert(large.nodes.some((n) => n.error_count > 0), "failing kept");
  const t0 = performance.now();
  let last = layoutGraph(large.nodes, allEdges);
  for (let i = 0; i < 20; i++) {
    last = layoutGraph(large.nodes, allEdges);
  }
  const elapsed = performance.now() - t0;
  const per = elapsed / 21;
  assert(last.nodes.length > 0 && last.edges.length > 0, "large layout non-empty");
  assert(per < 1000, `layout ${per.toFixed(2)}ms >= 1000ms budget`);
  // Simulate SVG path string work (cheap stand-in for render)
  const t1 = performance.now();
  let pathBytes = 0;
  for (const e of last.edges) pathBytes += e.d.length;
  for (const n of last.nodes) pathBytes += n.label.length;
  const renderish = performance.now() - t1;
  assert(pathBytes > 0, "paths produced");
  console.log(
    JSON.stringify({
      ok: true,
      support_nodes: lay.nodes.length,
      support_edges: lay.edges.length,
      large_nodes: last.nodes.length,
      large_edges: last.edges.length,
      layout_ms_avg: Number(per.toFixed(3)),
      path_bytes: pathBytes,
      renderish_ms: Number(renderish.toFixed(3)),
      budget_ms: 1000,
      fail_error_rate: FAIL_ERROR_RATE,
    })
  );
}

main();
