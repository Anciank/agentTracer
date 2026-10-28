import type { TraceListParams, TraceStatus } from "../api/types";

export type UiFilters = {
  project?: string;
  q?: string;
  status?: TraceStatus;
  sort?: TraceListParams["sort"];
  live?: boolean;
  from?: string;
  to?: string;
  node?: string;
  edge?: string;
  path?: string;
  node_fail?: string;
};

export function filtersFromSearch(sp: URLSearchParams): UiFilters {
  const f: UiFilters = {};
  const project = sp.get("project") || undefined;
  const q = sp.get("q") || undefined;
  const status = sp.get("status") as TraceStatus | null;
  const sort = sp.get("sort") as UiFilters["sort"];
  if (project) f.project = project;
  if (q) f.q = q;
  if (status === "success" || status === "error" || status === "running") f.status = status;
  if (sort === "start_desc" || sort === "duration_desc" || sort === "tokens_desc" || sort === "errors_desc") {
    f.sort = sort;
  }
  if (sp.get("live") === "1") f.live = true;
  const from = sp.get("from") || undefined;
  const to = sp.get("to") || undefined;
  const node = sp.get("node") || undefined;
  const edge = sp.get("edge") || undefined;
  const path = sp.get("path") || undefined;
  const node_fail = sp.get("node_fail") || undefined;
  if (from) f.from = from;
  if (to) f.to = to;
  if (node) f.node = node;
  if (edge) f.edge = edge;
  if (path) f.path = path;
  if (node_fail) f.node_fail = node_fail;
  return f;
}

export function filtersToSearch(f: UiFilters, extra?: Record<string, string | undefined>): string {
  const sp = new URLSearchParams();
  if (f.project) sp.set("project", f.project);
  if (f.q) sp.set("q", f.q);
  if (f.status) sp.set("status", f.status);
  if (f.sort && f.sort !== "start_desc") sp.set("sort", f.sort);
  if (f.live) sp.set("live", "1");
  if (f.from) sp.set("from", f.from);
  if (f.to) sp.set("to", f.to);
  if (f.node) sp.set("node", f.node);
  if (f.edge) sp.set("edge", f.edge);
  if (f.path) sp.set("path", f.path);
  if (f.node_fail) sp.set("node_fail", f.node_fail);
  if (extra) {
    for (const [k, v] of Object.entries(extra)) {
      if (v) sp.set(k, v);
    }
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

export function parseHash(hash: string): { runId?: string; folded: string[] } {
  const h = hash.replace(/^#/, "");
  const folded: string[] = [];
  let runId: string | undefined;
  for (const part of h.split("&")) {
    if (part.startsWith("r/")) runId = decodeURIComponent(part.slice(2));
    else if (part.startsWith("f=")) {
      folded.push(
        ...part
          .slice(2)
          .split(",")
          .filter(Boolean)
          .map(decodeURIComponent)
      );
    }
  }
  return { runId, folded };
}

export function buildHash(runId?: string | null, folded?: Iterable<string>): string {
  const parts: string[] = [];
  if (runId) parts.push(`r/${encodeURIComponent(runId)}`);
  const f = folded ? [...folded] : [];
  if (f.length) parts.push(`f=${f.map(encodeURIComponent).join(",")}`);
  return parts.length ? `#${parts.join("&")}` : "";
}
