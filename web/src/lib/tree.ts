import type { SpanSummary } from "../api/types";

export type TreeIndex = Map<string, { end: number; kids: number }>;

/** Lexicographic dotted_order, then start_time, then id. Empty dotted_order sorts after non-empty. */
export function compareSpanOrder(a: SpanSummary, b: SpanSummary): number {
  const da = a.dotted_order || "";
  const db = b.dotted_order || "";
  if (da && db) {
    if (da < db) return -1;
    if (da > db) return 1;
  } else if (da && !db) {
    return -1;
  } else if (!da && db) {
    return 1;
  }
  if (a.start_time < b.start_time) return -1;
  if (a.start_time > b.start_time) return 1;
  if (a.id < b.id) return -1;
  if (a.id > b.id) return 1;
  return 0;
}

export function orphanGroupId(rootId: string): string {
  return `__orphans__:${rootId}`;
}

export function isOrphanGroupId(id: string): boolean {
  return id.startsWith("__orphans__:");
}

/**
 * Rebuild a DFS waterfall from parent_run_id.
 * Roots first (sorted by dotted_order → start_time → id); children nested under
 * parent with the same sort. Rows whose parent is not in the trace go into a
 * synthetic "orphans" group UNDER the primary root — never above it.
 * Recomputes depth; preserves each real row's parent_run_id.
 */
export function buildWaterfallRows(input: SpanSummary[]): SpanSummary[] {
  if (input.length === 0) return [];

  const byId = new Map<string, SpanSummary>();
  for (const r of input) byId.set(r.id, r);

  const children = new Map<string, SpanSummary[]>();
  const roots: SpanSummary[] = [];
  const orphans: SpanSummary[] = [];

  for (const r of input) {
    const pid = r.parent_run_id;
    if (!pid) {
      roots.push(r);
      continue;
    }
    if (!byId.has(pid)) {
      orphans.push(r);
      continue;
    }
    const list = children.get(pid);
    if (list) list.push(r);
    else children.set(pid, [r]);
  }

  for (const list of children.values()) list.sort(compareSpanOrder);
  roots.sort(compareSpanOrder);
  orphans.sort(compareSpanOrder);

  let primary = roots[0];
  let promoted = false;
  if (!primary) {
    const sorted = [...input].sort(compareSpanOrder);
    primary = sorted[0];
    promoted = true;
    const oi = orphans.findIndex((o) => o.id === primary.id);
    if (oi >= 0) orphans.splice(oi, 1);
  }

  const out: SpanSummary[] = [];
  const visited = new Set<string>();

  const walkInto = (target: SpanSummary[], node: SpanSummary, depth: number) => {
    if (visited.has(node.id)) return;
    visited.add(node.id);
    target.push(depth === node.depth ? node : { ...node, depth });
    for (const k of children.get(node.id) || []) walkInto(target, k, depth + 1);
  };

  if (promoted) {
    walkInto(out, primary, 0);
  } else {
    for (const r of roots) walkInto(out, r, 0);
  }

  const pending = orphans.filter((o) => !visited.has(o.id));
  if (pending.length > 0) {
    const primaryDepth = out.find((r) => r.id === primary.id)?.depth ?? 0;
    const group: SpanSummary = {
      id: orphanGroupId(primary.id),
      parent_run_id: primary.id,
      name: "orphans",
      run_type: "chain",
      node: null,
      status: "success",
      depth: primaryDepth + 1,
      offset_ms: 0,
      latency_ms: null,
      self_time_ms: null,
      start_time: primary.start_time,
      end_time: null,
      tokens: { input: 0, output: 0, total: 0 },
      model: null,
      tags: ["orphans"],
      dotted_order: `${primary.dotted_order || primary.id}.\uffff`,
      has_error: false,
      payload_hot: false,
      attachment_count: 0,
    };
    const block: SpanSummary[] = [group];
    for (const o of pending) walkInto(block, o, primaryDepth + 2);

    const start = out.findIndex((r) => r.id === primary.id);
    if (start < 0) {
      out.push(...block);
    } else {
      let end = start + 1;
      while (end < out.length && out[end].depth > primaryDepth) end++;
      out.splice(end, 0, ...block);
    }
  }

  for (const r of input.filter((x) => !visited.has(x.id)).sort(compareSpanOrder)) {
    walkInto(out, r, 0);
  }

  return out;
}

export function indexTree(rows: SpanSummary[]): TreeIndex {
  const end = new Array(rows.length);
  const stack: number[] = [];
  for (let i = 0; i < rows.length; i++) {
    while (stack.length && rows[i].depth <= rows[stack[stack.length - 1]].depth) {
      end[stack.pop()!] = i;
    }
    stack.push(i);
  }
  while (stack.length) end[stack.pop()!] = rows.length;
  const tree: TreeIndex = new Map();
  rows.forEach((r, i) => tree.set(r.id, { end: end[i], kids: end[i] - i - 1 }));
  return tree;
}

export function visibleRows(rows: SpanSummary[], folded: Set<string>, tree: TreeIndex): SpanSummary[] {
  const out: SpanSummary[] = [];
  for (let i = 0; i < rows.length; ) {
    const row = rows[i];
    out.push(row);
    i = folded.has(row.id) ? tree.get(row.id)!.end : i + 1;
  }
  return out;
}

export function treeGuides(rows: SpanSummary[]): string[][] {
  const nextAtDepth = new Array(rows.length).fill(-1);
  const lastAtDepth: number[] = [];
  for (let i = 0; i < rows.length; i++) {
    const d = rows[i].depth;
    if (lastAtDepth[d] !== undefined) nextAtDepth[lastAtDepth[d]] = i;
    lastAtDepth.length = d + 1;
    lastAtDepth[d] = i;
  }
  const ancestry: number[] = [];
  return rows.map((row, i) => {
    const d = row.depth;
    ancestry.length = d;
    const cols: string[] = [];
    for (let k = 0; k < d - 1; k++) {
      const anc = ancestry[k + 1];
      cols.push(anc !== undefined && nextAtDepth[anc] !== -1 ? "g" : "g off");
    }
    if (d > 0) cols.push(nextAtDepth[i] !== -1 ? "e more" : "e");
    ancestry[d] = i;
    return cols;
  });
}
