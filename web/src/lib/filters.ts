import type { TraceListParams, TraceStatus } from "../api/types";

export type UiFilters = {
  project?: string;
  q?: string;
  status?: TraceStatus;
  sort?: TraceListParams["sort"];
  live?: boolean;
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
  return f;
}

export function filtersToSearch(f: UiFilters, extra?: Record<string, string | undefined>): string {
  const sp = new URLSearchParams();
  if (f.project) sp.set("project", f.project);
  if (f.q) sp.set("q", f.q);
  if (f.status) sp.set("status", f.status);
  if (f.sort && f.sort !== "start_desc") sp.set("sort", f.sort);
  if (f.live) sp.set("live", "1");
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
