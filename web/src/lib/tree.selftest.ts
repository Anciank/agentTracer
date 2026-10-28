/** Run with: npx tsx src/lib/tree.selftest.ts */
import {
  buildWaterfallRows,
  compareSpanOrder,
  isOrphanGroupId,
  orphanGroupId,
} from "./tree.ts";
import type { SpanSummary } from "../api/types";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

const span = (partial: Partial<SpanSummary> & { id: string; name: string }): SpanSummary => ({
  parent_run_id: null,
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
  dotted_order: "",
  has_error: false,
  payload_hot: true,
  attachment_count: 0,
  ...partial,
});

// --- child before parent in input (wrong server order / start_time) ---
{
  const root = span({
    id: "root",
    name: "m3_support_chain",
    run_type: "chain",
    dotted_order: "20261027T150000000000Zroot0000",
    start_time: "2026-10-27T15:00:00.050Z",
    latency_ms: 140,
    depth: 99, // wrong on purpose
  });
  const childEarly = span({
    id: "llm1",
    name: "ChatOpenAI",
    run_type: "llm",
    parent_run_id: "root",
    dotted_order: "20261027T150000000000Zroot0000.20261027T150000000000Zllm10000",
    start_time: "2026-10-27T15:00:00.000Z", // starts BEFORE root clock
    latency_ms: 47,
    depth: 0, // wrong — looked like a root in the bug screenshot
  });
  const tool = span({
    id: "tool",
    name: "lookup_order",
    run_type: "tool",
    parent_run_id: "root",
    dotted_order: "20261027T150000000000Zroot0000.20261027T150000010000Ztool0000",
    start_time: "2026-10-27T15:00:00.060Z",
    depth: 0,
  });
  // Deliberately: child first in array
  const built = buildWaterfallRows([childEarly, tool, root]);
  assert(built[0].id === "root", `root first, got ${built.map((r) => r.name)}`);
  assert(built[0].depth === 0, "root depth 0");
  assert(built[1].id === "llm1", `llm under root, got ${built.map((r) => r.id)}`);
  assert(built[1].depth === 1, "llm depth 1");
  assert(built[2].id === "tool", "tool after llm by dotted_order");
  assert(built[2].depth === 1, "tool depth 1");
  assert(!built.some((r) => r.id === "llm1" && built.indexOf(r) < built.indexOf(built[0])), "llm not above root");
  console.log("ok: child-before-parent input");
}

// --- missing dotted_order: fall back to start_time then id ---
{
  const root = span({
    id: "r",
    name: "root",
    start_time: "2026-10-27T15:00:00.000Z",
    dotted_order: "",
  });
  const c2 = span({
    id: "c2",
    name: "late",
    parent_run_id: "r",
    start_time: "2026-10-27T15:00:00.020Z",
    dotted_order: "",
  });
  const c1 = span({
    id: "c1",
    name: "early",
    parent_run_id: "r",
    start_time: "2026-10-27T15:00:00.010Z",
    dotted_order: "",
  });
  const built = buildWaterfallRows([c2, c1, root]);
  assert(
    built.map((r) => r.id).join(",") === "r,c1,c2",
    `start_time order, got ${built.map((r) => r.id)}`
  );
  assert(compareSpanOrder(c1, c2) < 0, "compare start_time");
  console.log("ok: missing dotted_order → start_time");
}

// --- orphan (parent not in trace) under root in marked group ---
{
  const root = span({
    id: "root",
    name: "root",
    dotted_order: "a",
    start_time: "2026-10-27T15:00:00.000Z",
  });
  const kid = span({
    id: "kid",
    name: "kid",
    parent_run_id: "root",
    dotted_order: "a.b",
    start_time: "2026-10-27T15:00:00.010Z",
  });
  const orphan = span({
    id: "orphan",
    name: "ChatOpenAI",
    parent_run_id: "missing-parent",
    dotted_order: "0early",
    start_time: "2026-10-27T14:59:59.000Z", // would sort above root if treated as root
    depth: 0,
  });
  const built = buildWaterfallRows([orphan, kid, root]);
  assert(built[0].id === "root", `root first, got ${built.map((r) => r.name)}`);
  assert(built[1].id === "kid", "real child before orphans group");
  assert(isOrphanGroupId(built[2].id), `orphans group, got ${built[2].id}`);
  assert(built[2].name === "orphans", "group named orphans");
  assert(built[2].id === orphanGroupId("root"), "group id");
  assert(built[3].id === "orphan", "orphan under group");
  assert(built[3].depth === 2, `orphan depth 2, got ${built[3].depth}`);
  assert(built[3].parent_run_id === "missing-parent", "preserves original parent_run_id");
  const orphanIdx = built.findIndex((r) => r.id === "orphan");
  const rootIdx = built.findIndex((r) => r.id === "root");
  assert(orphanIdx > rootIdx, "orphan never above root");
  console.log("ok: orphan under marked group");
}

// --- 5k rows perf sanity ---
{
  const n = 5000;
  const rows: SpanSummary[] = [];
  const root = span({
    id: "root",
    name: "root",
    dotted_order: "0",
    start_time: "2026-10-27T15:00:00.000Z",
  });
  rows.push(root);
  for (let i = 0; i < n - 1; i++) {
    const id = `c${i}`;
    // scramble order: push in reverse-ish
    rows.push(
      span({
        id,
        name: id,
        parent_run_id: i % 7 === 0 ? "root" : `c${Math.floor(i / 2)}`,
        // ensure parent exists: for i=0 parent c0 is self — fix: parent root or earlier
        dotted_order: `0.${String(n - i).padStart(6, "0")}`,
        start_time: new Date(Date.parse("2026-10-27T15:00:00.000Z") + i).toISOString(),
      })
    );
  }
  // Fix parents to only reference existing ids (root or lower index)
  for (let i = 1; i < rows.length; i++) {
    const parent = i === 1 || i % 5 === 0 ? "root" : rows[Math.max(1, Math.floor(i / 2))].id;
    rows[i] = { ...rows[i], parent_run_id: parent };
  }
  // Shuffle
  for (let i = rows.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [rows[i], rows[j]] = [rows[j], rows[i]];
  }
  const t0 = performance.now();
  const built = buildWaterfallRows(rows);
  const ms = performance.now() - t0;
  assert(built.length === n, `length ${built.length} != ${n}`);
  assert(built[0].id === "root", "root first on 5k");
  assert(built[0].depth === 0, "root depth");
  assert(ms < 500, `5k build took ${ms.toFixed(1)}ms (want <500)`);
  console.log(`ok: 5k rows in ${ms.toFixed(1)}ms`);
}

console.log("tree.selftest: ok");
