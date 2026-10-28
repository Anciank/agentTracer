/**
 * Deterministic layered DAG layout for the agent map (no dagre/elk dependency).
 * Top-to-bottom layers; siblings share a row (horizontal branching).
 * Cycles: pick entry roots from path fingerprints, DFS-mark back edges,
 * reverse them for layering only, draw back edges as side return curves.
 * Disconnected components are placed side by side.
 */
import type { EdgeStats, NodeStats, PathGroup } from "../api/types";

/** Minimum error_rate for the strong "failing" visual (red stroke + badge). */
export const FAIL_ERROR_RATE = 0.05;

/** Server rollup key; lives inside `nodes[]` (not a top-level `other` field). */
export const OTHER_KEY = "__other__";

export function isOtherNode(n: Pick<NodeStats, "key" | "kind">): boolean {
  return n.key === OTHER_KEY || n.kind === "other";
}

export function pickOther(nodes: NodeStats[]): NodeStats | null {
  return nodes.find(isOtherNode) ?? null;
}

export function withoutOther(nodes: NodeStats[]): NodeStats[] {
  return nodes.filter((n) => !isOtherNode(n));
}

/** Derive display mode — server no longer sends `mode`. */
export function inferGraphMode(nodes: NodeStats[]): "langgraph" | "fallback" {
  return nodes.some((n) => n.kind === "langgraph") ? "langgraph" : "fallback";
}

export function isFailingNode(n: Pick<NodeStats, "error_count" | "error_rate">): boolean {
  return n.error_count > 0 && n.error_rate >= FAIL_ERROR_RATE;
}

export function isWarnNode(n: Pick<NodeStats, "error_count" | "error_rate">): boolean {
  return n.error_count > 0 && n.error_rate < FAIL_ERROR_RATE;
}

export type LayoutEdgeKind = "forward" | "back" | "self";

export type LayoutNode = {
  key: string;
  label: string;
  x: number;
  y: number;
  w: number;
  h: number;
  stats: NodeStats;
  isOther: boolean;
  /** Self-loop repeat badge (×N) when a self-loop edge exists. */
  selfRepeat: number | null;
  selfLoopCount: number;
};

export type LayoutEdge = {
  id: string;
  from: string;
  to: string;
  stats: EdgeStats;
  kind: LayoutEdgeKind;
  /** SVG path d */
  d: string;
  labelX: number;
  labelY: number;
};

export type GraphLayout = {
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  width: number;
  height: number;
};

export type LayoutOptions = {
  nodeW?: number;
  nodeH?: number;
  gapX?: number;
  gapY?: number;
  pad?: number;
  expandOther?: boolean;
  /** Max siblings per layer row before wrapping to the next sub-row. */
  wrapCols?: number;
  /** Path groups — first steps bias root selection. */
  paths?: PathGroup[];
};

type SoftEdge = { from: string; to: string; stats: EdgeStats };

/** Entry-node scores from path fingerprints (higher = better root). */
export function entryScoresFromPaths(paths: PathGroup[] | undefined): Map<string, number> {
  const scores = new Map<string, number>();
  if (!paths?.length) return scores;
  for (const p of paths) {
    const first = p.steps?.[0]?.key;
    if (!first) continue;
    scores.set(first, (scores.get(first) || 0) + (p.count || 1));
  }
  return scores;
}

/** Undirected connected components (stable: sorted by size desc, then first key). */
function connectedComponents(keys: string[], edges: SoftEdge[]): string[][] {
  const keySet = new Set(keys);
  const adj = new Map<string, Set<string>>();
  for (const k of keys) adj.set(k, new Set());
  for (const e of edges) {
    if (!keySet.has(e.from) || !keySet.has(e.to)) continue;
    if (e.from === e.to) continue;
    adj.get(e.from)!.add(e.to);
    adj.get(e.to)!.add(e.from);
  }
  const seen = new Set<string>();
  const comps: string[][] = [];
  for (const start of [...keys].sort()) {
    if (seen.has(start)) continue;
    const stack = [start];
    const comp: string[] = [];
    seen.add(start);
    while (stack.length) {
      const u = stack.pop()!;
      comp.push(u);
      for (const v of [...adj.get(u)!].sort()) {
        if (seen.has(v)) continue;
        seen.add(v);
        stack.push(v);
      }
    }
    comps.push(comp.sort());
  }
  comps.sort((a, b) => b.length - a.length || a[0].localeCompare(b[0]));
  return comps;
}

/**
 * DFS from roots: classify each directed non-self edge as tree/forward/cross (keep)
 * or back (reverse for layering). Deterministic: adjacency sorted, roots sorted.
 */
export function breakCycles(
  keys: string[],
  edges: SoftEdge[],
  roots: string[]
): { forward: SoftEdge[]; back: SoftEdge[]; self: SoftEdge[] } {
  const keySet = new Set(keys);
  const self: SoftEdge[] = [];
  const directed2: SoftEdge[] = [];
  for (const e of edges) {
    if (!keySet.has(e.from) || !keySet.has(e.to)) continue;
    if (e.from === e.to) {
      self.push(e);
      continue;
    }
    directed2.push(e);
  }

  const out = new Map<string, SoftEdge[]>();
  for (const k of keys) out.set(k, []);
  for (const e of directed2) {
    out.get(e.from)!.push(e);
  }
  for (const arr of out.values()) {
    arr.sort(
      (a, b) =>
        b.stats.count - a.stats.count || a.to.localeCompare(b.to)
    );
  }

  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Map<string, number>();
  for (const k of keys) color.set(k, WHITE);
  const backKeys = new Set<string>(); // "from>to"
  const keep: SoftEdge[] = [];

  function dfs(u: string) {
    color.set(u, GRAY);
    for (const e of out.get(u)!) {
      const v = e.to;
      const c = color.get(v) ?? WHITE;
      const id = `${e.from}>${e.to}`;
      if (c === GRAY) {
        backKeys.add(id);
      } else if (c === WHITE) {
        dfs(v);
      }
      // cross/forward to BLACK: keep as forward for layering
    }
    color.set(u, BLACK);
  }

  const rootSet = roots.filter((r) => keySet.has(r));
  const order = [...rootSet, ...keys.filter((k) => !rootSet.includes(k))];
  for (const r of order) {
    if (color.get(r) === WHITE) dfs(r);
  }

  const forward: SoftEdge[] = [];
  const back: SoftEdge[] = [];
  for (const e of directed2) {
    const id = `${e.from}>${e.to}`;
    if (backKeys.has(id)) {
      // reverse for layering only
      back.push(e);
      forward.push({ from: e.to, to: e.from, stats: e.stats });
    } else {
      forward.push(e);
    }
  }
  return { forward, back, self };
}

/** Pick roots for a component using path entry scores + indegree heuristic. */
export function pickRoots(
  comp: string[],
  edges: SoftEdge[],
  entryScores: Map<string, number>
): string[] {
  const compSet = new Set(comp);
  const scored = comp
    .map((k) => ({ k, s: entryScores.get(k) || 0 }))
    .filter((x) => x.s > 0)
    .sort((a, b) => b.s - a.s || a.k.localeCompare(b.k));
  if (scored.length) return [scored[0].k];

  // No path hint: prefer nodes with lowest non-self indegree (entry-like).
  const indeg = new Map<string, number>();
  for (const k of comp) indeg.set(k, 0);
  for (const e of edges) {
    if (!compSet.has(e.from) || !compSet.has(e.to) || e.from === e.to) continue;
    indeg.set(e.to, (indeg.get(e.to) || 0) + 1);
  }
  let best = Infinity;
  for (const k of comp) best = Math.min(best, indeg.get(k) || 0);
  const candidates = comp.filter((k) => (indeg.get(k) || 0) === best).sort();
  return candidates.slice(0, 1);
}

/** Longest-path layering on a DAG (forward edges only), seeds at layer 0. */
function assignLayers(
  keys: string[],
  forward: SoftEdge[],
  roots: string[]
): Map<string, number> {
  const keySet = new Set(keys);
  const out = new Map<string, Set<string>>();
  const indeg = new Map<string, number>();
  for (const k of keys) {
    out.set(k, new Set());
    indeg.set(k, 0);
  }
  for (const e of forward) {
    if (!keySet.has(e.from) || !keySet.has(e.to) || e.from === e.to) continue;
    if (out.get(e.from)!.has(e.to)) continue;
    out.get(e.from)!.add(e.to);
    indeg.set(e.to, (indeg.get(e.to) || 0) + 1);
  }

  const layer = new Map<string, number>();
  const rootSet = new Set(roots.filter((r) => keySet.has(r)));
  // Seed: declared roots at 0, plus any remaining indegree-0 not reachable issue
  const q: string[] = [];
  for (const r of [...rootSet].sort()) {
    layer.set(r, 0);
    q.push(r);
  }
  for (const k of keys) {
    if (!layer.has(k) && (indeg.get(k) || 0) === 0) {
      layer.set(k, 0);
      q.push(k);
    }
  }
  q.sort();

  const seen = new Set<string>();
  while (q.length) {
    const u = q.shift()!;
    if (seen.has(u)) continue;
    seen.add(u);
    const lu = layer.get(u) ?? 0;
    const nexts = [...out.get(u)!].sort();
    for (const v of nexts) {
      const cand = lu + 1;
      if ((layer.get(v) ?? -1) < cand) layer.set(v, cand);
      indeg.set(v, (indeg.get(v) || 0) - 1);
      if ((indeg.get(v) || 0) <= 0) q.push(v);
    }
    q.sort();
  }
  // Orphans (shouldn't happen after cycle break): append stably
  let maxL = 0;
  for (const v of layer.values()) maxL = Math.max(maxL, v);
  for (const k of keys) {
    if (!layer.has(k)) {
      maxL += 1;
      layer.set(k, maxL);
    }
  }
  return layer;
}

function edgePathForward(
  a: LayoutNode,
  b: LayoutNode,
  slot: number
): { d: string; labelX: number; labelY: number } {
  const x1 = a.x + a.w / 2;
  const y1 = a.y + a.h;
  const x2 = b.x + b.w / 2;
  const y2 = b.y;
  const dy = Math.max(28, (y2 - y1) / 2);
  const bow = (slot % 5) * 14 * (slot % 2 === 0 ? 1 : -1);
  const cx1 = x1 + bow;
  const cy1 = y1 + dy;
  const cx2 = x2 + bow;
  const cy2 = y2 - dy;
  const d = `M ${x1} ${y1} C ${cx1} ${cy1}, ${cx2} ${cy2}, ${x2} ${y2}`;
  return {
    d,
    labelX: (x1 + x2) / 2 + bow * 0.5,
    labelY: (y1 + y2) / 2 - 4,
  };
}

/** Back edge: curve returning on the right side of the component. */
function edgePathBack(
  a: LayoutNode,
  b: LayoutNode,
  slot: number,
  sideX: number
): { d: string; labelX: number; labelY: number } {
  const x1 = a.x + a.w;
  const y1 = a.y + a.h / 2;
  const x2 = b.x + b.w;
  const y2 = b.y + b.h / 2;
  const bulge = sideX + 28 + slot * 16;
  const d = `M ${x1} ${y1} C ${bulge} ${y1}, ${bulge} ${y2}, ${x2} ${y2}`;
  return {
    d,
    labelX: bulge - 8,
    labelY: (y1 + y2) / 2,
  };
}

/** Self-loop glyph on the right of the node. */
function edgePathSelf(
  a: LayoutNode,
  slot: number
): { d: string; labelX: number; labelY: number } {
  const r = 12 + slot * 3;
  const x = a.x + a.w;
  const y = a.y + a.h * 0.38; // upper-right — clear of mid-side back-edge rails
  const d = `M ${x} ${y - 5} C ${x + r} ${y - r}, ${x + r} ${y + r}, ${x} ${y + 5}`;
  return { d, labelX: x + r + 14, labelY: y - 10 };
}

/** Nudge overlapping edge labels apart (deterministic, O(n²) fine for ≤200). */
export function nudgeLabels(
  edges: LayoutEdge[],
  minDx = 22,
  minDy = 16
): void {
  const items = edges
    .map((e, i) => ({ e, i }))
    .sort((a, b) => a.e.labelY - b.e.labelY || a.e.labelX - b.e.labelX || a.i - b.i);
  for (let i = 0; i < items.length; i++) {
    for (let j = i + 1; j < items.length; j++) {
      const a = items[i].e;
      const b = items[j].e;
      const dy = Math.abs(a.labelY - b.labelY);
      const dx = Math.abs(a.labelX - b.labelX);
      if (dy < minDy && dx < minDx) {
        const push = (minDy - dy) / 2 + 1;
        if (a.labelY <= b.labelY) {
          a.labelY -= push;
          b.labelY += push;
        } else {
          a.labelY += push;
          b.labelY -= push;
        }
        if (dx < minDx / 2) {
          b.labelX += minDx - dx;
        }
      }
    }
  }
}

function expandNodes(nodesIn: NodeStats[], expandOther: boolean): NodeStats[] {
  const other = pickOther(nodesIn);
  const nodes: NodeStats[] = withoutOther(nodesIn);
  if (other) {
    if (expandOther && other.rolled_keys?.length) {
      for (const k of other.rolled_keys) {
        if (nodes.some((n) => n.key === k)) continue;
        nodes.push({
          key: k,
          label: k,
          run_type: null,
          kind: "langgraph",
          count: 1,
          error_count: 0,
          error_rate: 0,
          p50_ms: null,
          p95_ms: null,
          tokens: { input: 0, output: 0, total: 0 },
          truncated: true,
        });
      }
    } else {
      nodes.push(other);
    }
  }
  return nodes;
}

export function layoutGraph(
  nodesIn: NodeStats[],
  edgesIn: EdgeStats[],
  opts: LayoutOptions = {}
): GraphLayout {
  const nodeW = opts.nodeW ?? 176;
  const nodeH = opts.nodeH ?? 72;
  const gapX = opts.gapX ?? 48;
  const gapY = opts.gapY ?? 72;
  const pad = opts.pad ?? 36;
  const wrapCols = opts.wrapCols ?? 8;
  const entryScores = entryScoresFromPaths(opts.paths);

  const nodes = expandNodes(nodesIn, !!opts.expandOther);
  const byStats = new Map(nodes.map((n) => [n.key, n]));
  const keys = nodes.map((n) => n.key).sort();

  const soft: SoftEdge[] = edgesIn
    .filter((e) => byStats.has(e.from) && byStats.has(e.to))
    .map((e) => ({ from: e.from, to: e.to, stats: e }));

  const comps = connectedComponents(keys, soft);
  const layoutNodes: LayoutNode[] = [];
  const layoutEdges: LayoutEdge[] = [];

  let cursorX = pad;
  let maxY = pad;
  let maxX = pad;

  for (const comp of comps) {
    const compSet = new Set(comp);
    const compEdges = soft.filter((e) => compSet.has(e.from) && compSet.has(e.to));
    const roots = pickRoots(comp, compEdges, entryScores);
    const { forward, back, self } = breakCycles(comp, compEdges, roots);
    // Layering uses forward (with back edges reversed)
    const layerOf = assignLayers(comp, forward, roots);

    const byLayer = new Map<number, string[]>();
    for (const k of comp) {
      const L = layerOf.get(k) ?? 0;
      if (!byLayer.has(L)) byLayer.set(L, []);
      byLayer.get(L)!.push(k);
    }
    for (const arr of byLayer.values()) {
      arr.sort((a, b) => {
        const sa = byStats.get(a)!;
        const sb = byStats.get(b)!;
        return (
          sb.error_count - sa.error_count ||
          sb.count - sa.count ||
          a.localeCompare(b)
        );
      });
    }

    const layers = [...byLayer.keys()].sort((a, b) => a - b);
    // Component width from widest layer
    let compW = 0;
    for (const L of layers) {
      const col = byLayer.get(L)!;
      const cols = Math.min(wrapCols, col.length);
      compW = Math.max(compW, cols * nodeW + (cols - 1) * gapX);
    }

    const selfByNode = new Map<string, SoftEdge>();
    for (const e of self) {
      // one self-loop per node (highest count)
      const prev = selfByNode.get(e.from);
      if (!prev || e.stats.count > prev.stats.count) selfByNode.set(e.from, e);
    }

    const localNodes: LayoutNode[] = [];
    let rowCursor = 0;
    for (const L of layers) {
      const col = byLayer.get(L)!;
      const subRows = Math.max(1, Math.ceil(col.length / wrapCols));
      col.forEach((k, i) => {
        const localRow = Math.floor(i / wrapCols);
        const localCol = i % wrapCols;
        const rowNodes = Math.min(wrapCols, col.length - localRow * wrapCols);
        const rowWidth = rowNodes * nodeW + (rowNodes - 1) * gapX;
        // Center row within component width
        const x0 = cursorX + Math.max(0, (compW - rowWidth) / 2);
        const x = x0 + localCol * (nodeW + gapX);
        const y = pad + (rowCursor + localRow) * (nodeH + gapY);
        const n = byStats.get(k)!;
        const sl = selfByNode.get(k);
        const repeat =
          sl == null
            ? null
            : sl.stats.repeat_avg != null
              ? Math.round(sl.stats.repeat_avg)
              : sl.stats.repeat_max != null
                ? Math.round(sl.stats.repeat_max)
                : null;
        localNodes.push({
          key: k,
          label: n.label,
          x,
          y,
          w: nodeW,
          h: nodeH,
          stats: n,
          isOther: n.kind === "other",
          selfRepeat: repeat,
          selfLoopCount: sl?.stats.count ?? 0,
        });
        maxY = Math.max(maxY, y + nodeH);
        maxX = Math.max(maxX, x + nodeW + (sl ? 48 : 0));
      });
      rowCursor += subRows;
    }
    layoutNodes.push(...localNodes);
    const byKey = new Map(localNodes.map((n) => [n.key, n]));
    const sideX = cursorX + compW + 56;

    // Forward edges (original non-back, non-self)
    const forwardOrig = compEdges.filter((e) => {
      if (e.from === e.to) return false;
      return !back.some((b) => b.from === e.from && b.to === e.to);
    });
    const slotOf = new Map<string, number>();
    const pushEdge = (
      e: SoftEdge,
      kind: LayoutEdgeKind,
      pathFn: () => { d: string; labelX: number; labelY: number }
    ) => {
      const id =
        kind === "self"
          ? `${e.from}↻`
          : kind === "back"
            ? `${e.from}↩${e.to}`
            : `${e.from}>${e.to}`;
      const slot = slotOf.get(id) || 0;
      slotOf.set(id, slot + 1);
      const { d, labelX, labelY } = pathFn();
      layoutEdges.push({
        id,
        from: e.from,
        to: e.to,
        stats: { ...e.stats, loop: kind !== "forward" ? true : e.stats.loop },
        kind,
        d,
        labelX,
        labelY,
      });
    };

    for (const e of forwardOrig.sort(
      (a, b) => b.stats.count - a.stats.count || a.from.localeCompare(b.from) || a.to.localeCompare(b.to)
    )) {
      const a = byKey.get(e.from);
      const b = byKey.get(e.to);
      if (!a || !b) continue;
      const slot = slotOf.get(`${e.from}>${e.to}`) || 0;
      pushEdge(e, "forward", () => edgePathForward(a, b, slot));
    }

    for (const e of back.sort(
      (a, b) => b.stats.count - a.stats.count || a.from.localeCompare(b.from)
    )) {
      const a = byKey.get(e.from);
      const b = byKey.get(e.to);
      if (!a || !b) continue;
      const slot = slotOf.get(`${e.from}↩${e.to}`) || 0;
      pushEdge(e, "back", () => edgePathBack(a, b, slot, sideX));
    }

    // One self-loop glyph per node
    for (const [k, e] of [...selfByNode.entries()].sort((a, b) => a[0].localeCompare(b[0]))) {
      const a = byKey.get(k);
      if (!a) continue;
      pushEdge(e, "self", () => edgePathSelf(a, 0));
    }

    // Next component to the right
    const compHeight = rowCursor * (nodeH + gapY);
    void compHeight;
    cursorX += compW + gapX + 64; // side gutter for back-edge curves + next comp
    maxX = Math.max(maxX, cursorX);
  }

  nudgeLabels(layoutEdges);

  return {
    nodes: layoutNodes,
    edges: layoutEdges,
    width: maxX + pad,
    height: maxY + pad,
  };
}
