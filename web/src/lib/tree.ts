import type { SpanSummary } from "../api/types";

export type TreeIndex = Map<string, { end: number; kids: number }>;

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
