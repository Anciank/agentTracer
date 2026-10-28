/**
 * Deterministic layered DAG layout for the agent map (no dagre/elk dependency).
 * Top-to-bottom layering: layers advance downward; siblings share a row.
 * Forward edges drive layers; loop/back-edges are drawn as arcs and do not
 * create extra layers.
 *
 * Fail styling helpers (used by AgentMap):
 *   - failing (strong): error_count > 0 && error_rate >= FAIL_ERROR_RATE (5%)
 *   - warn (subtle):    error_count > 0 && error_rate < FAIL_ERROR_RATE
 * Nodes with any error_count stay in the graph (server/top-N keeps them);
 * only the visual flag is thresholded so low rates do not paint the map red.
 */
import type { EdgeStats, NodeStats } from "../api/types";

/** Minimum error_rate for the strong "failing" visual (red stroke + badge). */
export const FAIL_ERROR_RATE = 0.05;

export function isFailingNode(n: Pick<NodeStats, "error_count" | "error_rate">): boolean {
  return n.error_count > 0 && n.error_rate >= FAIL_ERROR_RATE;
}

export function isWarnNode(n: Pick<NodeStats, "error_count" | "error_rate">): boolean {
  return n.error_count > 0 && n.error_rate < FAIL_ERROR_RATE;
}

export type LayoutNode = {
  key: string;
  label: string;
  x: number;
  y: number;
  w: number;
  h: number;
  stats: NodeStats;
  isOther: boolean;
};

export type LayoutEdge = {
  id: string;
  from: string;
  to: string;
  stats: EdgeStats;
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
};

function isBackOrLoop(
  from: string,
  to: string,
  layerOf: Map<string, number>,
  edge: EdgeStats
): boolean {
  if (edge.loop || from === to) return true;
  const lf = layerOf.get(from);
  const lt = layerOf.get(to);
  if (lf == null || lt == null) return false;
  return lt <= lf;
}

/** Longest-path layering on the forward DAG (ignoring loop flags + self). */
function assignLayers(keys: string[], edges: EdgeStats[]): Map<string, number> {
  const keySet = new Set(keys);
  const forward: { from: string; to: string }[] = [];
  for (const e of edges) {
    if (!keySet.has(e.from) || !keySet.has(e.to)) continue;
    if (e.loop || e.from === e.to) continue;
    forward.push({ from: e.from, to: e.to });
  }
  const out = new Map<string, Set<string>>();
  const indeg = new Map<string, number>();
  for (const k of keys) {
    out.set(k, new Set());
    indeg.set(k, 0);
  }
  for (const e of forward) {
    if (out.get(e.from)!.has(e.to)) continue;
    out.get(e.from)!.add(e.to);
    indeg.set(e.to, (indeg.get(e.to) || 0) + 1);
  }
  const layer = new Map<string, number>();
  const q = keys.filter((k) => (indeg.get(k) || 0) === 0).sort();
  const seen = new Set<string>();
  while (q.length) {
    const u = q.shift()!;
    if (seen.has(u)) continue;
    seen.add(u);
    const lu = layer.get(u) ?? 0;
    layer.set(u, lu);
    const nexts = [...out.get(u)!].sort();
    for (const v of nexts) {
      const cand = lu + 1;
      if ((layer.get(v) ?? -1) < cand) layer.set(v, cand);
      indeg.set(v, (indeg.get(v) || 0) - 1);
      if (indeg.get(v) === 0) q.push(v);
    }
    q.sort();
  }
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

/** Top→bottom edge path (source bottom → target top). */
function edgePath(
  a: LayoutNode,
  b: LayoutNode,
  loop: boolean,
  slot: number
): { d: string; labelX: number; labelY: number } {
  if (loop || a.key === b.key) {
    const r = 18 + slot * 8;
    const x = a.x + a.w;
    const y = a.y + a.h / 2;
    const d = `M ${x} ${y} C ${x + r} ${y - r}, ${x + r} ${y + r}, ${x} ${y}`;
    return { d, labelX: x + r + 4, labelY: y };
  }
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

export function layoutGraph(
  nodesIn: NodeStats[],
  other: NodeStats | null,
  edgesIn: EdgeStats[],
  opts: LayoutOptions = {}
): GraphLayout {
  const nodeW = opts.nodeW ?? 148;
  const nodeH = opts.nodeH ?? 56;
  const gapX = opts.gapX ?? 28;
  const gapY = opts.gapY ?? 72;
  const pad = opts.pad ?? 32;
  const wrapCols = opts.wrapCols ?? 6;

  const nodes: NodeStats[] = nodesIn.slice();
  if (other) {
    if (opts.expandOther && other.rolled_keys?.length) {
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

  nodes.sort(
    (a, b) =>
      b.error_count - a.error_count || b.count - a.count || a.key.localeCompare(b.key)
  );

  const keys = nodes.map((n) => n.key);
  const layerOf = assignLayers(keys, edgesIn);

  const byLayer = new Map<number, NodeStats[]>();
  for (const n of nodes) {
    const L = layerOf.get(n.key) ?? 0;
    if (!byLayer.has(L)) byLayer.set(L, []);
    byLayer.get(L)!.push(n);
  }
  for (const arr of byLayer.values()) {
    arr.sort(
      (a, b) =>
        b.error_count - a.error_count || b.count - a.count || a.key.localeCompare(b.key)
    );
  }

  const layers = [...byLayer.keys()].sort((a, b) => a - b);
  const layoutNodes: LayoutNode[] = [];
  let maxX = 0;
  let maxY = 0;
  let rowCursor = 0; // cumulative sub-rows for wrap across layers
  for (const L of layers) {
    const col = byLayer.get(L)!;
    const subRows = Math.max(1, Math.ceil(col.length / wrapCols));
    col.forEach((n, i) => {
      const localRow = Math.floor(i / wrapCols);
      const localCol = i % wrapCols;
      const rowNodes = Math.min(wrapCols, col.length - localRow * wrapCols);
      // Center the row's nodes
      const rowWidth = rowNodes * nodeW + (rowNodes - 1) * gapX;
      const x0 = pad;
      const x = x0 + localCol * (nodeW + gapX);
      const y = pad + (rowCursor + localRow) * (nodeH + gapY);
      layoutNodes.push({
        key: n.key,
        label: n.label,
        x,
        y,
        w: nodeW,
        h: nodeH,
        stats: n,
        isOther: n.kind === "other",
      });
      maxX = Math.max(maxX, x + nodeW);
      maxY = Math.max(maxY, y + nodeH);
      void rowWidth;
    });
    rowCursor += subRows;
  }
  const byKey = new Map(layoutNodes.map((n) => [n.key, n]));
  const width = maxX + pad;
  const height = maxY + pad;

  const slotOf = new Map<string, number>();
  const layoutEdges: LayoutEdge[] = [];
  const sortedEdges = edgesIn
    .filter((e) => byKey.has(e.from) && byKey.has(e.to))
    .slice()
    .sort((a, b) => b.count - a.count || a.from.localeCompare(b.from) || a.to.localeCompare(b.to));

  for (const e of sortedEdges) {
    const id = `${e.from}>${e.to}`;
    const slot = slotOf.get(id) || 0;
    slotOf.set(id, slot + 1);
    const a = byKey.get(e.from)!;
    const b = byKey.get(e.to)!;
    const loop = isBackOrLoop(e.from, e.to, layerOf, e);
    const { d, labelX, labelY } = edgePath(a, b, loop || e.loop, slot);
    layoutEdges.push({ id, from: e.from, to: e.to, stats: { ...e, loop: e.loop || loop }, d, labelX, labelY });
  }

  return { nodes: layoutNodes, edges: layoutEdges, width, height };
}
