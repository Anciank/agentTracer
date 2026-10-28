import { filtersFromSearch, filtersToSearch, type UiFilters } from "./filters";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(msg);
}

export function runFiltersSelftest(): void {
  const f: UiFilters = {
    project: "demo-project",
    status: "error",
    q: "boom",
    model: "demo-model",
    tool: "search",
    sort: "duration_desc",
    live: true,
    from: "2026-01-01T00:00:00Z",
    to: "2026-01-02T00:00:00Z",
  };
  const qs = filtersToSearch(f);
  assert(qs.includes("status=error"), "status=error must be in query");
  assert(qs.includes("project=demo-project"), "project must be in query");
  assert(qs.includes("model="), "model must be in query");
  const round = filtersFromSearch(new URLSearchParams(qs.replace(/^\?/, "")));
  assert(round.status === "error", "status must survive round-trip (Errors must not reset to All)");
  assert(round.project === f.project, "project round-trip");
  assert(round.q === f.q, "q round-trip");
  assert(round.model === f.model, "model round-trip");
  assert(round.tool === f.tool, "tool round-trip");
  assert(round.sort === f.sort, "sort round-trip");
  assert(round.live === true, "live round-trip");

  // Clearing model must not clear status
  const cleared = { ...round, model: undefined };
  delete (cleared as { model?: string }).model;
  const qs2 = filtersToSearch(cleared);
  assert(qs2.includes("status=error"), "clearing model must keep status=error");
  assert(!qs2.includes("model="), "model cleared");
}

runFiltersSelftest();
console.log("filters.selftest: ok");
