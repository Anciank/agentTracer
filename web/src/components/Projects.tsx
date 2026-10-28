import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { listProjects } from "../api/client";
import { fmtInt, fmtPct, fmtDurMs } from "../lib/format";
import { ErrorState } from "./ErrorState";

export function ProjectsPage() {
  const nav = useNavigate();
  const q = useQuery({ queryKey: ["projects"], queryFn: listProjects });

  if (q.isLoading) return <div className="page empty">Loading projects…</div>;
  if (q.isError) {
    return (
      <div className="page">
        <ErrorState error={q.error} onRetry={() => void q.refetch()} title="Failed to load projects" />
      </div>
    );
  }
  const projects = q.data || [];
  if (!projects.length) {
    return (
      <div className="page empty">
        No projects yet — point <code>LANGSMITH_ENDPOINT</code> here.
      </div>
    );
  }

  return (
    <div className="page">
      <h2 style={{ marginTop: 0 }}>Projects · last 24h</h2>
      <div className="cards">
        {projects.map((p) => (
          <button
            key={p.id}
            type="button"
            className="card"
onClick={() => nav(`/projects/${encodeURIComponent(p.name)}?window=24h`)}
          >
            <h3>{p.name}</h3>
            <div className="meta">
              <div className="hint" style={{ marginBottom: 4 }}>Monitor →</div>
              <div>
                <b>{fmtInt(p.trace_count_24h)}</b> traces / 24h · <b>{fmtInt(p.trace_count_7d)}</b> / 7d
              </div>
              <div>
                errors <b>{fmtPct(p.error_rate_24h)}</b> · latency p50 <b>{fmtDurMs(p.p50_ms)}</b> · p95{" "}
                <b>{fmtDurMs(p.p95_ms)}</b>
              </div>
              <div>
                tokens <b>{fmtInt(p.tokens_24h)}</b>
              </div>
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}
