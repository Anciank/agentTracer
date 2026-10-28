import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { getGraph, getPaths, listProjects } from "../api/client";
import type { EdgeStats, GraphWindow, NodeStats, PathGroup } from "../api/types";
import { AgentMap } from "../components/AgentMap";
import { ErrorState } from "../components/ErrorState";
import { fmtInt, fmtPct } from "../lib/format";
import { inferGraphMode, pickOther } from "../lib/graphLayout";
import { isMapEndpointMissing } from "../lib/mapStatus";

const WINDOWS: GraphWindow[] = ["1h", "24h", "7d"];

type Props = {
  /** When true, ProjectPage owns crumbs / window / tabs. */
  embedded?: boolean;
  projectIdProp?: string;
  windowProp?: GraphWindow;
};

export default function ProjectMapPage({ embedded, projectIdProp, windowProp }: Props = {}) {
  const params = useParams();
  const projectId = projectIdProp || params.projectId || "";
  const [sp, setSp] = useSearchParams();
  const nav = useNavigate();
  const findRef = useRef<HTMLInputElement>(null);

  const windowParam = windowProp || (sp.get("window") as GraphWindow) || "24h";
  const win: GraphWindow = WINDOWS.includes(windowParam) ? windowParam : "24h";
  const showHidden = sp.get("rare") === "1";
  const expandOther = sp.get("expand") === "1";

  const [findQuery, setFindQuery] = useState("");

  const projectsQ = useQuery({ queryKey: ["projects"], queryFn: listProjects });
  const projectMeta = useMemo(
    () => projectsQ.data?.find((p) => p.id === projectId || p.name === projectId),
    [projectsQ.data, projectId]
  );
  // Graph/paths :id is project/session **name** (API.md), not UUID.
  const idForApi = projectMeta?.name || projectId;

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
      if (g?.window?.from) qs.set("from", g.window.from);
      if (g?.window?.to) qs.set("to", g.window.to);
      for (const [k, v] of Object.entries(extra)) qs.set(k, v);
      nav(`/traces?${qs.toString()}`);
    },
    [graphQ.data, projectMeta, nav]
  );

  const onNodeClick = (key: string, stats: NodeStats) => {
    if (stats.error_count > 0 && key) {
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
    if (isMapEndpointMissing(graphQ.error)) {
      return (
        <div className={embedded ? "" : "page"}>
          <div className="map-unsupported" role="status">
            <h2>The agent map needs a server update</h2>
            <p>
              This UI build talks to <code>/api/ui/.../graph</code>, but the server you are
              connected to does not expose it yet (HTTP 404/501). Upgrade the agentTracer
              backend, then reload.
            </p>
            <Link className="chip" to="/traces">
              Back to traces
            </Link>
          </div>
        </div>
      );
    }
    return (
      <div className={embedded ? "" : "page"}>
        <ErrorState error={graphQ.error} onRetry={() => void graphQ.refetch()} title="Failed to load graph" />
      </div>
    );
  }
  const graph = graphQ.data!;
  const paths = pathsQ.data?.paths || [];
  const otherNode = pickOther(graph.nodes);
  const mode = inferGraphMode(graph.nodes);

  return (
    <div className={embedded ? "map-page map-embedded" : "page map-page"}>
      {!embedded && (
        <div className="map-header">
          <div>
            <div className="map-crumbs">
              <Link to="/">Projects</Link>
              <span> / </span>
              <strong>{graph.project}</strong>
              <span className="map-mode"> · {mode}</span>
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
            {otherNode && (
              <button type="button" className={`chip ${expandOther ? "active" : ""}`} onClick={toggleExpand}>
                {expandOther ? "Fold" : otherNode.label}
              </button>
            )}
            <Link className="chip" to={`/traces?project=${encodeURIComponent(graph.project)}`}>
              All traces
            </Link>
          </div>
        </div>
      )}

      {embedded && (
        <div className="map-controls" style={{ marginBottom: 8 }}>
          <span className="map-mode">mode · {mode}</span>
          <button type="button" className={`chip ${showHidden ? "active" : ""}`} onClick={toggleRare}>
            Rare edges (&lt;1%)
          </button>
          {otherNode && (
            <button type="button" className={`chip ${expandOther ? "active" : ""}`} onClick={toggleExpand}>
              {expandOther ? "Fold" : otherNode.label}
            </button>
          )}
        </div>
      )}

      <div className="map-body">
        <AgentMap
          graph={graph}
          paths={paths}
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
          {pathsQ.isError && isMapEndpointMissing(pathsQ.error) && (
            <div className="hint">Paths need a server update (404/501).</div>
          )}
          {pathsQ.isError && !isMapEndpointMissing(pathsQ.error) && (
            <div className="hint">Failed to load paths.</div>
          )}
          {!paths.length && !pathsQ.isLoading && !pathsQ.isError && <div className="hint">No path groups</div>}
          <ul className="paths-list">
            {paths.map((p) => {
              const errish = p.error_rate >= 0.05;
              const runLabel = p.count === 1 ? "1 run" : `${fmtInt(p.count)} runs`;
              return (
              <li key={p.fingerprint}>
                <button
                  type="button"
                  className={`path-row${errish ? " path-row-err" : ""}`}
                  onClick={() => onPathClick(p)}
                >
                  <div className="path-label" title={p.label}>
                    {(p.steps?.length ? p.steps : [{ key: p.fingerprint, repeat: 1 }]).map((s, i) => (
                      <span key={`${s.key}-${i}`} className="path-step-wrap">
                        {i > 0 ? <span className="path-arrow" aria-hidden>→</span> : null}
                        <span className="path-step">
                          {s.key}{s.repeat > 1 ? ` ×${s.repeat}` : ""}
                        </span>
                      </span>
                    ))}
                  </div>
                  <div className="path-meta">
                    {runLabel} · {fmtPct(p.share)} · err {fmtPct(p.error_rate)}
                  </div>
                </button>
              </li>
              );
            })}
          </ul>
        </aside>
      </div>
    </div>
  );
}
