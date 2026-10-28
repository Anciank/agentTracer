import { lazy, Suspense, useEffect, useMemo } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { listProjects } from "../api/client";
import type { GraphWindow } from "../api/types";

const ProjectMonitorPage = lazy(() => import("./ProjectMonitorPage"));
const ProjectMapPage = lazy(() => import("./ProjectMapPage"));

const WINDOWS: GraphWindow[] = ["1h", "24h", "7d"];
type Tab = "monitor" | "map";

export default function ProjectPage() {
  const { projectId = "" } = useParams();
  const [sp, setSp] = useSearchParams();

  const tabParam = sp.get("tab");
  const tab: Tab = tabParam === "map" ? "map" : "monitor";
  const windowParam = (sp.get("window") as GraphWindow) || "24h";
  const win: GraphWindow = WINDOWS.includes(windowParam) ? windowParam : "24h";

  const projectsQ = useQuery({ queryKey: ["projects"], queryFn: listProjects });
  const projectMeta = useMemo(
    () => projectsQ.data?.find((p) => p.id === projectId || p.name === projectId),
    [projectsQ.data, projectId]
  );
  const idForApi = projectMeta?.id || projectId;
  const projectName = projectMeta?.name || projectId;

  const setWindow = (w: GraphWindow) => {
    const next = new URLSearchParams(sp);
    next.set("window", w);
    setSp(next, { replace: true });
  };
  const setTab = (t: Tab) => {
    const next = new URLSearchParams(sp);
    if (t === "monitor") next.delete("tab");
    else next.set("tab", t);
    if (!next.get("window")) next.set("window", win);
    setSp(next, { replace: true });
  };

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement)?.tagName;
      const typing = tag === "INPUT" || tag === "TEXTAREA";
      if (typing || e.metaKey || e.ctrlKey) return;
      if (e.key === "1" || e.key === "m") {
        e.preventDefault();
        setTab("monitor");
      }
      if (e.key === "2" || e.key === "g") {
        e.preventDefault();
        setTab("map");
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sp, win]);

  return (
    <div className="page project-page">
      <div className="map-header">
        <div>
          <div className="map-crumbs">
            <Link to="/">Projects</Link>
            <span> / </span>
            <strong>{projectName}</strong>
          </div>
          <div className="project-tabs" role="tablist" aria-label="Project views">
            <button
              type="button"
              role="tab"
              aria-selected={tab === "monitor"}
              className={`chip ${tab === "monitor" ? "active" : ""}`}
              onClick={() => setTab("monitor")}
            >
              Monitor
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "map"}
              className={`chip ${tab === "map" ? "active" : ""}`}
              onClick={() => setTab("map")}
            >
              Map
            </button>
          </div>
        </div>
        <div className="map-controls">
          <div className="seg" aria-label="Time window">
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
          <Link className="chip" to={`/traces?project=${encodeURIComponent(projectName)}`}>
            All traces
          </Link>
        </div>
      </div>

      <Suspense
        fallback={
          <div className="page empty">
            Loading {tab === "monitor" ? "monitor" : "map"}…
          </div>
        }
      >
        {tab === "monitor" ? (
          <ProjectMonitorPage projectId={idForApi} window={win} />
        ) : (
          <ProjectMapPage embedded projectIdProp={idForApi} windowProp={win} />
        )}
      </Suspense>
    </div>
  );
}
