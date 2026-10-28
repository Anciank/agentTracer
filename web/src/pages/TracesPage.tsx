import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams, useLocation } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { getTraceSummary } from "../api/client";
import { TraceList } from "../components/TraceList";
import { Waterfall } from "../components/Waterfall";
import { RunDetail } from "../components/RunDetail";
import {
  filtersFromSearch,
  filtersToSearch,
  parseHash,
  buildHash,
  type UiFilters,
} from "../lib/filters";
import { fmtDurMs, fmtInt } from "../lib/format";
import { ErrorState } from "../components/ErrorState";
import { buildWaterfallRows, indexTree, isOrphanGroupId, visibleRows } from "../lib/tree";

export function TracesPage() {
  const { traceId: routeTraceId } = useParams();
  const [sp] = useSearchParams();
  const loc = useLocation();
  const nav = useNavigate();

  const filters = useMemo(() => filtersFromSearch(sp), [sp]);
  const deepTrace = sp.get("trace") || routeTraceId || null;

  const [listSel, setListSel] = useState<string | null>(deepTrace);
  const [folded, setFolded] = useState<Set<string>>(() => new Set(parseHash(loc.hash).folded));
  const [runId, setRunId] = useState<string | null>(() => parseHash(loc.hash).runId || null);
  const [tab, setTab] = useState(0);

  useEffect(() => {
    const h = parseHash(loc.hash);
    if (h.runId) setRunId(h.runId);
    if (h.folded.length) setFolded(new Set(h.folded));
  }, [loc.hash]);

  useEffect(() => {
    if (deepTrace) setListSel(deepTrace);
  }, [deepTrace]);

  const openTrace = useCallback(
    (id: string) => {
      setListSel(id);
      setRunId(null);
      setFolded(new Set());
      const q = filtersToSearch(filters);
      nav(`/traces/${id}${q}`);
    },
    [filters, nav]
  );

  const patchFilters = useCallback(
    (patch: Partial<UiFilters>) => {
      const next = { ...filters, ...patch };
      // clear undefined status explicitly
      if ("status" in patch && patch.status === undefined) delete next.status;
      if ("q" in patch && !patch.q) delete next.q;
      for (const k of ["node", "edge", "path", "node_fail", "from", "to"] as const) {
        if (k in patch && !patch[k]) delete next[k];
      }
      const path = routeTraceId ? `/traces/${routeTraceId}` : "/traces";
      nav(`${path}${filtersToSearch(next)}`);
    },
    [filters, nav, routeTraceId]
  );

  const summaryQ = useQuery({
    queryKey: ["summary", deepTrace],
    queryFn: () => getTraceSummary(deepTrace!),
    enabled: !!deepTrace,
    // Live upserts write via setQueryData; never blank the pane on background refetch.
    placeholderData: (prev) => prev,
    refetchOnMount: false,
  });

  const rows = useMemo(
    () => buildWaterfallRows(summaryQ.data?.rows || []),
    [summaryQ.data?.rows]
  );
  const tree = useMemo(() => indexTree(rows), [rows]);
  const visible = useMemo(() => visibleRows(rows, folded, tree), [rows, folded, tree]);

  useEffect(() => {
    if (!summaryQ.data || runId) return;
    if (filters.status === "error" || summaryQ.data.summary.status === "error") {
      const err = summaryQ.data.rows.find((r) => r.has_error || r.status === "error");
      if (err) setRunId(err.id);
    }
  }, [summaryQ.data, runId, filters.status]);

  useEffect(() => {
    if (!deepTrace) return;
    const hash = buildHash(runId, folded);
    if (hash !== loc.hash) {
      window.history.replaceState(null, "", `${loc.pathname}${loc.search}${hash}`);
    }
  }, [deepTrace, runId, folded, loc.pathname, loc.search, loc.hash]);

  const selectedSpan = (runId && !isOrphanGroupId(runId) ? rows.find((r) => r.id === runId) : null) || null;

  const toggleFold = (id: string) => {
    setFolded((prev) => {
      const n = new Set(prev);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  };

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement)?.tagName;
      const typing =
        tag === "INPUT" || tag === "TEXTAREA" || (e.target as HTMLElement)?.isContentEditable;
      if (typing && e.key !== "Escape") return;

      if (e.key === "j" || e.key === "ArrowDown") {
        e.preventDefault();
        if (deepTrace && visible.length) {
          let idx = runId ? visible.findIndex((r) => r.id === runId) : -1;
          for (let i = idx + 1; i < visible.length; i++) {
            if (!isOrphanGroupId(visible[i].id)) {
              setRunId(visible[i].id);
              break;
            }
          }
        }
        return;
      }
      if (e.key === "k" || e.key === "ArrowUp") {
        e.preventDefault();
        if (deepTrace && visible.length) {
          let idx = runId ? visible.findIndex((r) => r.id === runId) : 0;
          if (idx < 0) idx = 0;
          for (let i = idx - 1; i >= 0; i--) {
            if (!isOrphanGroupId(visible[i].id)) {
              setRunId(visible[i].id);
              break;
            }
          }
        }
        return;
      }
      if (e.key === "Enter" || e.key === "o") {
        if (!deepTrace && listSel) openTrace(listSel);
        return;
      }
      if (e.key === "Escape") {
        if (runId) setRunId(null);
        else if (deepTrace) nav(`/traces${filtersToSearch(filters)}`);
        else if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
        return;
      }
      if (e.key === "/" && !typing) {
        e.preventDefault();
        document.getElementById("trace-search")?.focus();
        return;
      }
      if (e.key === "e" && !typing) {
        e.preventDefault();
        const next = filters.status === "error" ? undefined : ("error" as const);
        patchFilters({ status: next });
        return;
      }
      if (e.key === "l" && !typing) {
        e.preventDefault();
        patchFilters({ live: !filters.live });
        return;
      }
      if ((e.key === "ArrowLeft" || e.key === "h") && runId) {
        e.preventDefault();
        const node = tree.get(runId);
        if (node && node.kids > 0) setFolded((p) => new Set(p).add(runId));
        return;
      }
      if (e.key === "ArrowRight" && runId) {
        e.preventDefault();
        setFolded((p) => {
          const n = new Set(p);
          n.delete(runId);
          return n;
        });
        return;
      }
      if (/^[1-7]$/.test(e.key) && !typing) {
        setTab(Number(e.key) - 1);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [deepTrace, visible, runId, listSel, openTrace, filters, nav, patchFilters, tree]);

  return (
    <div className="main">
      <div className="filters">
        <input
          id="trace-search"
          type="search"
          placeholder="Search name / error…"
          defaultValue={filters.q || ""}
          onChange={(e) => {
            const v = e.target.value;
            window.clearTimeout((window as unknown as { __q?: number }).__q);
            (window as unknown as { __q?: number }).__q = window.setTimeout(() => {
              patchFilters({ q: v || undefined });
            }, 150);
          }}
        />
        <select
          value={filters.status || ""}
          onChange={(e) =>
            patchFilters({ status: (e.target.value || undefined) as UiFilters["status"] })
          }
        >
          <option value="">All status</option>
          <option value="error">Errors</option>
          <option value="success">Success</option>
          <option value="running">Running</option>
        </select>
        <select
          value={filters.sort || "start_desc"}
          onChange={(e) => patchFilters({ sort: e.target.value as UiFilters["sort"] })}
        >
          <option value="start_desc">Newest</option>
          <option value="duration_desc">Slowest</option>
          <option value="tokens_desc">Most tokens</option>
          <option value="errors_desc">Most errors</option>
        </select>
        {filters.project && <span className="chip active">project: {filters.project}</span>}
        {filters.node && (
          <button type="button" className="chip active" onClick={() => patchFilters({ node: undefined, node_fail: undefined })}>
            node: {filters.node} ✕
          </button>
        )}
        {filters.edge && (
          <button type="button" className="chip active" onClick={() => patchFilters({ edge: undefined })}>
            edge: {filters.edge} ✕
          </button>
        )}
        {filters.path && (
          <button type="button" className="chip active" onClick={() => patchFilters({ path: undefined })} title={filters.path}>
            path ✕
          </button>
        )}
        {filters.node_fail && !filters.node && (
          <button type="button" className="chip active" onClick={() => patchFilters({ node_fail: undefined })}>
            fail@{filters.node_fail} ✕
          </button>
        )}
        <button
          type="button"
          className={`chip ${filters.live ? "active" : ""}`}
          onClick={() => patchFilters({ live: !filters.live })}
        >
          {filters.live && <span className="live-dot" style={{ marginRight: 6 }} />}
          Live tail
        </button>
        <button
          type="button"
          className={`chip ${filters.status === "error" ? "active" : ""}`}
          onClick={() => patchFilters({ status: filters.status === "error" ? undefined : "error" })}
        >
          Errors only
        </button>
      </div>

      <div className={`split ${deepTrace ? "" : "no-detail"}`}>
        <div className="pane">
          <div className="pane-head">
            <h2>Traces</h2>
            <div className="meta">{filters.project || "all projects"}</div>
          </div>
          <TraceList
            filters={filters}
            selectedId={listSel}
            openTraceId={deepTrace}
            onSelect={setListSel}
            onOpen={openTrace}
          />
        </div>

        {deepTrace && (
          <div className="pane" style={{ display: "flex", flexDirection: "column" }}>
            <div className="pane-head">
              <h2>{summaryQ.data?.summary.name || "…"}</h2>
              <div className="meta">
                {summaryQ.data
                  ? `${fmtDurMs(summaryQ.data.span_ms)} · ${fmtInt(summaryQ.data.rows.length)} spans · ${fmtInt(
                      summaryQ.data.summary.tokens.total
                    )} tok`
                  : "loading…"}
                <div style={{ fontFamily: "var(--mono)", fontSize: 11 }}>{deepTrace}</div>
              </div>
            </div>
            {summaryQ.isLoading && !summaryQ.data && <div className="empty">Loading waterfall…</div>}
            {summaryQ.isError && !summaryQ.data && (
              <ErrorState error={summaryQ.error} onRetry={() => void summaryQ.refetch()} />
            )}
            {summaryQ.data && (
              <Waterfall
                rows={summaryQ.data.rows}
                spanMs={summaryQ.data.span_ms}
                selectedId={runId}
                folded={folded}
                onSelect={setRunId}
                onToggleFold={toggleFold}
              />
            )}
          </div>
        )}

        {deepTrace && (
          <div className="pane">
            <RunDetail span={selectedSpan} tabIndex={tab} onTab={setTab} />
          </div>
        )}
      </div>
    </div>
  );
}
