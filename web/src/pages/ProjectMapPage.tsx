import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { getGraph, getPaths, listProjects } from "../api/client";
import type { EdgeStats, GraphWindow, NodeStats, PathGroup } from "../api/types";
import { AgentMap } from "../components/AgentMap";
import { ErrorState } from "../components/ErrorState";
import { fmtDurMs, fmtInt, fmtPct } from "../lib/format";

const WINDOWS: GraphWindow[] = ["1h", "24h", "7d"];

export default function ProjectMapPage() {
  const { projectId = "" } = useParams();
  const [sp, setSp] = useSearchParams();
  const nav = useNavigate();
  const findRef = useRef<HTMLInputElement>(null);

  const windowParam = (sp.get("window") as GraphWindow) || "24h";
  const win: GraphWindow = WINDOWS.includes(windowParam) ? windowParam : "24h";
  const showHidden = sp.get("rare") === "1";
  const expandOther = sp.get("expand") === "1";

  const [findQuery, setFindQuery] = useState("");

  const projectsQ = useQuery({ queryKey: ["projects"], queryFn: listProjects });
  const projectMeta = useMemo(
    () => projectsQ.data?.find((p) => p.id === projectId || p.name === projectId),
    [projectsQ.data, projectId]
  );
  const idForApi = projectMeta?.id || projectId;

  const graphQ = useQuery({
    queryKey: ["graph", idForApi, win],
    queryFn: () => getGraph(idForApi, { window: win }),
    enabled: !!idForApi,
  });
  const pathsQ = useQuery({
    queryKey: ["paths", idForApi, win],
    queryFn: () => getPaths(idForApi, { window: win }),
    enabled: !!idForApi,
  });

  const setWindow = (w: GraphWindow) => {
    const next = new URLSearchParams(sp);
    next.set("window", w);
    setSp(next, { replace: true });
  };
  const toggleRare = () => {
    const next = new URLSearchParams(sp);
    if (showHidden) next.delete("rare");
    else next.set("rare", "1");
    setSp(next, { replace: true });
  };
  const toggleExpand = () => {
    const next = new URLSearchParams(sp);
    if (expandOther) next.delete("expand");
    else next.set("expand", "1");
    setSp(next, { replace: true });
  };

  const drill = useCallback(
    (extra: Record<string, string>) => {
      const g = graphQ.data;
      const name = g?.project || projectMeta?.name || "";
      const qs = new URLSearchParams();
      if (name) qs.set("project", name);
      if (g?.from) qs.set("from", g.from);
      if (g?.to) qs.set("to", g.to);
      for (const [k, v] of Object.entries(extra)) qs.set(k, v);
      nav(`/traces?${qs.toString()}`);
    },
    [graphQ.data, projectMeta, nav]
  );

  const onNodeClick = (key: string, stats: NodeStats) => {
    if (stats.error_count > 0 && key) {
      // Prefer failed-at filter when node is failing; still pass node.
      drill({ node: key, node_fail: key });
    } else {
      drill({ node: key });
    }
  };
  const onEdgeClick = (edge: EdgeStats) => {
    drill({ edge: `${edge.from}>${edge.to}` });
  };
  const onPathClick = (p: PathGroup) => {
    drill({ path: p.fingerprint });
  };

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement)?.tagName;
      const typing = tag === "INPUT" || tag === "TEXTAREA";
      if (e.key === "/" && !typing) {
        e.preventDefault();
        findRef.current?.focus();
        findRef.current?.select();
      }
      if (e.key === "f" && !typing && !e.metaKey && !e.ctrlKey) {
        e.preventDefault();
        document.getElementById("map-fit-btn")?.click();
      }
      if (e.key === "r" && !typing) {
        e.preventDefault();
        const next = new URLSearchParams(window.location.search);
        if (next.get("rare") === "1") next.delete("rare");
        else next.set("rare", "1");
        setSp(next, { replace: true });
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setSp]);

  if (graphQ.isLoading || projectsQ.isLoading) {
    return <div className="page empty">Loading agent map…</div>;
  }
  if (graphQ.isError) {
    return (
      <div className="page">
        <ErrorState error={graphQ.error} onRetry={() => void graphQ.refetch()} title="Failed to load graph" />
      </div>
    );
  }
  const graph = graphQ.data!;
  const paths = pathsQ.data?.paths || [];

  return (
    <div className="page map-page">
      <div className="map-header">
        <div>
          <div className="map-crumbs">
            <Link to="/">Projects</Link>
            <span> / </span>
            <strong>{graph.project}</strong>
            <span className="map-mode"> · {graph.mode}</span>
          </div>
          <h2 style={{ margin: "4px 0 0" }}>Agent map</h2>
        </div>
        <div className="map-controls">
          <div className="seg">
            {WINDOWS.map((w) => (
              <button
                key={w}
                type="button"
                className={`chip ${win === w ? "active" : ""}`}
                onClick={() => setWindow(w)}
              >
                {w}
              </button>
            ))}
          </div>
          <button type="button" className={`chip ${showHidden ? "active" : ""}`} onClick={toggleRare}>
            Rare edges (&lt;1%)
          </button>
          {graph.other && (
            <button type="button" className={`chip ${expandOther ? "active" : ""}`} onClick={toggleExpand}>
              {expandOther ? "Fold" : graph.other.label}
            </button>
          )}
          <Link className="chip" to={`/traces?project=${encodeURIComponent(graph.project)}`}>
            All traces
          </Link>
        </div>
      </div>

      <div className="map-body">
        <AgentMap
          graph={graph}
          showHiddenEdges={showHidden}
          expandOther={expandOther}
          findQuery={findQuery}
          findInputRef={findRef}
          onFindQuery={setFindQuery}
          onToggleOther={toggleExpand}
          onNodeClick={onNodeClick}
          onEdgeClick={onEdgeClick}
        />
        <aside className="paths-panel">
          <h3>Paths</h3>
          {pathsQ.isLoading && <div className="hint">Loading…</div>}
          {!paths.length && !pathsQ.isLoading && <div className="hint">No path groups</div>}
          <ul className="paths-list">
            {paths.map((p) => (
              <li key={p.fingerprint}>
                <button type="button" className="path-row" onClick={() => onPathClick(p)}>
                  <div className="path-label">
                    {p.label}
                    {p.is_new ? <span className="badge">new</span> : null}
                  </div>
                  <div className="path-meta">
                    {fmtInt(p.count)} runs · err {fmtPct(p.error_rate)} · p95 {fmtDurMs(p.p95_ms)}
                    {p.fail_nodes.length ? ` · fail @ ${p.fail_nodes.join(", ")}` : ""}
                  </div>
                </button>
              </li>
            ))}
          </ul>
        </aside>
      </div>
    </div>
  );
}
