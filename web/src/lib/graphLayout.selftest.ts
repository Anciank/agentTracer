/**
 * Perf + correctness self-test for agent map layout.
 * Run: npx tsx src/lib/graphLayout.selftest.ts
 * Real LangGraph JSON lives in web/testdata/lgtest (not prod-bundled).
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { GraphResponse, PathsResponse } from "../api/types";
import {
  FAIL_ERROR_RATE,
  breakCycles,
  entryScoresFromPaths,
  isFailingNode,
  isWarnNode,
  layoutGraph,
  pickOther,
  pickRoots,
  withoutOther,
} from "./graphLayout";
import { makeLargeGraph, supportGraph } from "../fixtures/graph";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

function loadLgtest(name: string): { graph: GraphResponse; paths: PathsResponse } {
  const dir = join(dirname(fileURLToPath(import.meta.url)), "../../testdata/lgtest");
  const graph = JSON.parse(readFileSync(join(dir, `${name}.graph.json`), "utf8")) as GraphResponse;
  const paths = JSON.parse(readFileSync(join(dir, `${name}.paths.json`), "utf8")) as PathsResponse;
  return { graph, paths };
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
  const tools = lay.nodes.find((n) => n.key === "tools")!;
  assert(isFailingNode(tools.stats), "tools rate >= fail threshold");
  const retrieve = lay.nodes.find((n) => n.key === "retrieve");
  if (retrieve)
    assert(
      isWarnNode(retrieve.stats) ||
        retrieve.stats.error_count === 0 ||
        retrieve.stats.error_rate >= FAIL_ERROR_RATE,
      "retrieve warn-or-ok"
    );

  assert(lay.nodes.some((n) => n.key === "tools" && n.stats.error_count > 0), "tools visible");
  assert(lay.nodes.some((n) => n.key === "escalate"), "escalate visible");
  const loops = lay.edges.filter((e) => e.kind === "self");
  assert(loops.length >= 1, "self-loop present");
  const toolsLoop = lay.edges.filter((e) => e.kind === "self" && e.from === "tools");
  assert(toolsLoop.length === 1, "tools loop once");

  // --- Real LangGraph: supervisor cyclic graph ---
  const { graph: sg, paths: sp } = loadLgtest("lgtest-supervisor-live5-r0");
  const scores = entryScoresFromPaths(sp.paths);
  assert((scores.get("supervisor") || 0) > 0, "supervisor is a path entry");
  const soft = sg.edges.map((e) => ({ from: e.from, to: e.to, stats: e }));
  const keys = sg.nodes.map((n) => n.key);
  const roots = pickRoots(
    keys.filter((k) => ["supervisor", "agent", "tools", "policy"].includes(k)),
    soft.filter((e) => ["supervisor", "agent", "tools", "policy"].includes(e.from)),
    scores
  );
  assert(roots[0] === "supervisor", `expected supervisor root, got ${roots[0]}`);

  const tLay0 = performance.now();
  const realLay = layoutGraph(sg.nodes, sg.edges, { paths: sp.paths });
  const realMs = performance.now() - tLay0;
  assert(realMs < 1000, `real layout ${realMs.toFixed(2)}ms >= 1000ms`);

  const sup = realLay.nodes.find((n) => n.key === "supervisor")!;
  const agent = realLay.nodes.find((n) => n.key === "agent")!;
  const ret = realLay.nodes.find((n) => n.key === "retrieve")!;
  assert(sup.y < agent.y, "supervisor above agent");
  // Disconnected retrieve chain should be side-by-side, not stacked under supervisor
  assert(
    Math.abs(ret.x - sup.x) > 80,
    `retrieve should sit beside main component (dx=${Math.abs(ret.x - sup.x)})`
  );
  // Wide rather than ultra-tall for this 7-node graph
  assert(realLay.width >= realLay.height * 0.45, "aspect prefers width for cyclic+component");

  const selfEdges = realLay.edges.filter((e) => e.kind === "self");
  const selfKeys = new Set(selfEdges.map((e) => e.from));
  assert(selfKeys.has("agent"), "agent self-loop glyph");
  assert(selfEdges.filter((e) => e.from === "agent").length === 1, "one agent self glyph");
  const back = realLay.edges.filter((e) => e.kind === "back");
  assert(back.length >= 1, "back edges drawn");

  // Label collision: self labels must not share exact (x,y) with another
  const labels = realLay.edges.map((e) => `${Math.round(e.labelX)},${Math.round(e.labelY)}`);
  assert(new Set(labels).size === labels.length, "edge labels uniquely placed after nudge");

  // Cycle break helper
  const broken = breakCycles(keys, soft, ["supervisor"]);
  assert(broken.back.length >= 1, "DFS found back edges");
  assert(broken.self.length >= 1, "self loops separated");

  // More real graphs (smoke)
  for (const name of [
    "lgtest-parallel_fanout-L090953-r0",
    "lgtest-react_tools-smoke1-r0",
    "lgtest-subgraph_nested-L090953s-r9",
  ]) {
    const { graph, paths } = loadLgtest(name);
    const L = layoutGraph(graph.nodes, graph.edges, { paths: paths.paths });
    assert(L.nodes.length === graph.nodes.length, `${name} node count`);
    const entry = paths.paths[0]?.steps[0]?.key;
    if (entry) {
      const rootNode = L.nodes.find((n) => n.key === entry)!;
      const maxY = Math.max(...L.nodes.filter((n) => Math.abs(n.x - rootNode.x) < 200).map((n) => n.y));
      // entry should be among the top of its component
      assert(rootNode.y <= maxY, `${name} entry not below peers`);
      const topY = Math.min(...L.nodes.filter((n) => Math.abs(n.x - rootNode.x) < 200).map((n) => n.y));
      assert(rootNode.y === topY, `${name} entry on top of component (got y=${rootNode.y} top=${topY})`);
    }
  }

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
      real_supervisor: {
        nodes: realLay.nodes.length,
        edges: realLay.edges.length,
        back: back.length,
        self: selfEdges.length,
        width: realLay.width,
        height: realLay.height,
        ms: Number(realMs.toFixed(3)),
        supervisor_y: sup.y,
        agent_y: agent.y,
        retrieve_x: ret.x,
      },
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
