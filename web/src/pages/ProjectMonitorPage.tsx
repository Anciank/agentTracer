import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { getMonitor } from "../api/client";
import type { GraphWindow, ModelRow, MonitorResponse, ToolRow } from "../api/types";
import { ErrorState } from "../components/ErrorState";
import {
  LatencyLinesChart,
  StackedTracesChart,
  TokensChart,
} from "../components/monitor/Charts";
import { fmtDurMs, fmtInt, fmtPct } from "../lib/format";
import { isMapEndpointMissing } from "../lib/mapStatus";
import {
  drillBucket,
  drillModel,
  drillTool,
  drillTraceSegment,
  tracesHref,
} from "../lib/monitorFilters";
import {
  DEFAULT_MONITOR_UI,
  applyMonitorRefresh,
  shouldPollMonitor,
  type MonitorUiState,
} from "../lib/monitorMerge";

const POLL_MS = 20_000;

type Props = {
  projectId: string;
  window: GraphWindow;
};

type SortKey = "calls" | "share" | "error_rate" | "p50_ms" | "p99_ms" | "tokens" | "name";

function sortModels(rows: ModelRow[], key: string, dir: "asc" | "desc"): ModelRow[] {
  const mul = dir === "asc" ? 1 : -1;
  return rows.slice().sort((a, b) => {
    let cmp = 0;
    if (key === "name" || key === "model") cmp = a.model.localeCompare(b.model);
    else if (key === "calls") cmp = a.calls - b.calls;
    else if (key === "share") cmp = a.share - b.share;
    else if (key === "error_rate") cmp = a.error_rate - b.error_rate;
    else if (key === "p50_ms") cmp = (a.p50_ms || 0) - (b.p50_ms || 0);
    else if (key === "p99_ms") cmp = (a.p99_ms || 0) - (b.p99_ms || 0);
    else if (key === "tokens") cmp = (a.tokens?.total ?? -1) - (b.tokens?.total ?? -1);
    if (cmp === 0) cmp = a.model.localeCompare(b.model);
    return cmp * mul;
  });
}

function sortTools(rows: ToolRow[], key: string, dir: "asc" | "desc"): ToolRow[] {
  const mul = dir === "asc" ? 1 : -1;
  return rows.slice().sort((a, b) => {
    let cmp = 0;
    if (key === "name" || key === "tool") cmp = a.tool.localeCompare(b.tool);
    else if (key === "calls") cmp = a.calls - b.calls;
    else if (key === "share") cmp = a.share - b.share;
    else if (key === "error_rate") cmp = a.error_rate - b.error_rate;
    else if (key === "p50_ms") cmp = (a.p50_ms || 0) - (b.p50_ms || 0);
    else if (key === "p99_ms") cmp = (a.p99_ms || 0) - (b.p99_ms || 0);
    else if (key === "tokens") cmp = (a.tokens?.total ?? -1) - (b.tokens?.total ?? -1);
    if (cmp === 0) cmp = a.tool.localeCompare(b.tool);
    return cmp * mul;
  });
}

export default function ProjectMonitorPage({ projectId, window: win }: Props) {
  const nav = useNavigate();
  const [data, setData] = useState<MonitorResponse | null>(null);
  const [ui, setUi] = useState<MonitorUiState>(DEFAULT_MONITOR_UI);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const etagRef = useRef<string | null>(null);
  const uiRef = useRef(ui);
  uiRef.current = ui;
  const scrollerRef = useRef<HTMLDivElement>(null);
  const dataRef = useRef(data);
  dataRef.current = data;

  const load = useCallback(
    async (opts: { initial?: boolean } = {}) => {
      const curUi = uiRef.current;
      if (!opts.initial && !shouldPollMonitor(curUi, typeof document !== "undefined" ? !document.hidden : true)) {
        return;
      }
      try {
        const res = await getMonitor(projectId, { window: win }, { etag: etagRef.current });
        if (res.etag) etagRef.current = res.etag;
        const merged = applyMonitorRefresh(dataRef.current, res.data, curUi, {
          unchanged: res.unchanged,
        });
        setData(merged.data);
        setUi(merged.ui);
        setError(null);
        // restore scroll after paint
        requestAnimationFrame(() => {
          if (scrollerRef.current) scrollerRef.current.scrollTop = merged.ui.scrollTop;
        });
      } catch (e) {
        if (opts.initial || !dataRef.current) setError(e);
      } finally {
        if (opts.initial) setLoading(false);
      }
    },
    [projectId, win]
  );

  // Reset when project/window changes
  useEffect(() => {
    etagRef.current = null;
    setData(null);
    setUi(DEFAULT_MONITOR_UI);
    setLoading(true);
    setError(null);
    void load({ initial: true });
  }, [projectId, win, load]);

  // Poll ~20s with ETag; pause when hidden or tooltip pinned
  useEffect(() => {
    const id = window.setInterval(() => void load(), POLL_MS);
    const onVis = () => {
      if (!document.hidden) void load();
    };
    document.addEventListener("visibilitychange", onVis);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [load]);

  const onScroll = () => {
    const top = scrollerRef.current?.scrollTop || 0;
    setUi((u) => (u.scrollTop === top ? u : { ...u, scrollTop: top }));
  };

  const setHover = (series: MonitorUiState["hoverSeries"], idx: number | null) => {
    setUi((u) => ({ ...u, hoverSeries: series, hoverIdx: idx }));
  };

  const go = (href: string) => nav(href);

  const models = useMemo(
    () => (data ? sortModels(data.by_model, ui.modelSort.key, ui.modelSort.dir) : []),
    [data, ui.modelSort]
  );
  const tools = useMemo(
    () => (data ? sortTools(data.by_tool, ui.toolSort.key, ui.toolSort.dir) : []),
    [data, ui.toolSort]
  );
  /** Hide Tokens column when every tool row has null/empty/zero tokens. */
  const showToolTokens = useMemo(
    () => tools.some((r) => r.tokens != null && r.tokens.total != null && r.tokens.total > 0),
    [tools]
  );

  const toggleSort = (table: "model" | "tool", key: SortKey) => {
    setUi((u) => {
      const cur = table === "model" ? u.modelSort : u.toolSort;
      const next = {
        key,
        dir: cur.key === key && cur.dir === "desc" ? ("asc" as const) : ("desc" as const),
      };
      return table === "model" ? { ...u, modelSort: next } : { ...u, toolSort: next };
    });
  };

  if (loading) return <div className="page empty">Loading monitor…</div>;
  if (error) {
    if (isMapEndpointMissing(error)) {
      return (
        <div className="page">
          <div className="map-unsupported" role="status">
            <h2>The monitor board needs a server update</h2>
            <p>
              This UI build talks to <code>/api/ui/.../monitor</code>, but the server you are
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
      <div className="page">
        <ErrorState
          error={error}
          onRetry={() => {
            setLoading(true);
            void load({ initial: true });
          }}
          title="Failed to load monitor"
        />
      </div>
    );
  }
  if (!data) return <div className="page empty">No monitor data</div>;

  const project = data.project;
  const hoverTraces = ui.hoverSeries === "traces" ? ui.hoverIdx : null;
  const hoverLat = ui.hoverSeries === "latency" ? ui.hoverIdx : null;
  const hoverTok = ui.hoverSeries === "tokens" ? ui.hoverIdx : null;

  return (
    <div className="mon-board" ref={scrollerRef} onScroll={onScroll}>
      <div className="mon-header">
        <div className="mon-stat">
          <div className="mon-stat-val">
            <button
              type="button"
              className="linkish"
              onClick={() =>
                go(tracesHref({ project, from: data.from, to: data.to, status: "success" }))
              }
            >
              {fmtInt(data.success_count)}
            </button>
            <span className="muted"> succeeded</span>
            <span className="dotsep">·</span>
            <button
              type="button"
              className="linkish err"
              onClick={() =>
                go(tracesHref({ project, from: data.from, to: data.to, status: "error" }))
              }
            >
              {fmtInt(data.error_count)}
            </button>
            <span className="muted"> failed</span>
          </div>
          <div className="mon-stat-label">{fmtInt(data.trace_count)} traces · {data.bucket} buckets</div>
        </div>
        <div className="mon-stat">
          <div className="mon-stat-val">
            p50 <b>{fmtDurMs(data.p50_ms)}</b>
            <span className="dotsep">·</span>
            p99 <b>{fmtDurMs(data.p99_ms)}</b>
          </div>
          <div className="mon-stat-label">latency</div>
        </div>
        <div className="mon-stat">
          <div className="mon-stat-val">
            <b>{fmtInt(data.tokens.total)}</b>
            <span className="muted"> tok</span>
          </div>
          <div className="mon-stat-label">
            in {fmtInt(data.tokens.input)} · out {fmtInt(data.tokens.output)}
          </div>
        </div>
        <div className="mon-stat mon-cost-slot" title="Reserved for M6">
          <div className="mon-stat-val muted">—</div>
          <div className="mon-stat-label">Cost (M6)</div>
        </div>
      </div>

      <div className="mon-grid">
        <section className="mon-card">
          <div className="mon-card-head">
            <h3>Traces over time</h3>
            <div className="mon-legend">
              <span className="lg ok" /> success
              <span className="lg err" /> error
            </div>
          </div>
          <StackedTracesChart
            series={data.series_traces}
            bucket={data.bucket}
            hoverIdx={hoverTraces}
            onHover={(i) => setHover("traces", i)}
            onClickSegment={(i, seg) => {
              const b = data.series_traces[i];
              if (!b) return;
              go(tracesHref(drillTraceSegment(project, b.t, b.t_end, seg)));
            }}
          />
        </section>

        <section className="mon-card">
          <div className="mon-card-head">
            <h3>Latency</h3>
            <div className="mon-legend">
              <span className="lg p50" /> p50
              <span className="lg p99" /> p99
            </div>
          </div>
          <LatencyLinesChart
            series={data.series_latency}
            bucket={data.bucket}
            hoverIdx={hoverLat}
            onHover={(i) => setHover("latency", i)}
            onClickPoint={(i) => {
              const b = data.series_latency[i];
              if (!b) return;
              go(tracesHref(drillBucket(project, b.t, b.t_end)));
            }}
          />
        </section>

        <section className="mon-card">
          <div className="mon-card-head">
            <h3>Tokens</h3>
            <div className="mon-legend">
              <span className="lg in" /> in
              <span className="lg out" /> out
              <span className="mon-cost-pill" title="Reserved — no cost fields in M3.6">
                Cost (M6)
              </span>
            </div>
          </div>
          <TokensChart
            series={data.series_tokens}
            bucket={data.bucket}
            hoverIdx={hoverTok}
            onHover={(i) => setHover("tokens", i)}
            onClickPoint={(i) => {
              const b = data.series_tokens[i];
              if (!b) return;
              go(tracesHref(drillBucket(project, b.t, b.t_end)));
            }}
          />
        </section>
      </div>

      <div className="mon-tables">
        <section className="mon-card">
          <div className="mon-card-head">
            <h3>Models</h3>
          </div>
          <div className="mon-table-wrap">
            <table className="mon-table">
              <thead>
                <tr>
                  {(
                    [
                      ["name", "Model"],
                      ["calls", "Calls"],
                      ["share", "Share"],
                      ["error_rate", "Errors"],
                      ["p50_ms", "p50"],
                      ["p99_ms", "p99"],
                      ["tokens", "Tokens"],
                    ] as [SortKey, string][]
                  ).map(([k, label]) => (
                    <th key={k}>
                      <button type="button" className="th-btn" onClick={() => toggleSort("model", k)}>
                        {label}
                        {ui.modelSort.key === k ? (ui.modelSort.dir === "desc" ? " ↓" : " ↑") : ""}
                      </button>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {models.map((r) => (
                  <tr
                    key={r.model}
                    className="mon-row"
                    onClick={() =>
                      go(tracesHref(drillModel(project, data.from, data.to, r.model)))
                    }
                  >
                    <td className="mono">{r.model}</td>
                    <td>{fmtInt(r.calls)}</td>
                    <td>{fmtPct(r.share)}</td>
                    <td>
                      <button
                        type="button"
                        className="linkish err"
                        onClick={(e) => {
                          e.stopPropagation();
                          go(tracesHref(drillModel(project, data.from, data.to, r.model, true)));
                        }}
                      >
                        {fmtPct(r.error_rate)}
                      </button>
                    </td>
                    <td>{fmtDurMs(r.p50_ms)}</td>
                    <td>{fmtDurMs(r.p99_ms)}</td>
                    <td>{fmtInt(r.tokens.total)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className="mon-card">
          <div className="mon-card-head">
            <h3>Tools</h3>
          </div>
          {!tools.length ? (
            <div className="mon-empty">No tool calls in this window</div>
          ) : (
            <div className="mon-table-wrap">
              <table className="mon-table">
                <thead>
                  <tr>
                    {(
                      [
                        ["name", "Tool"],
                        ["calls", "Calls"],
                        ["share", "Share"],
                        ["error_rate", "Errors"],
                        ["p50_ms", "p50"],
                        ["p99_ms", "p99"],
                        ...(showToolTokens ? ([["tokens", "Tokens"]] as [SortKey, string][]) : []),
                      ] as [SortKey, string][]
                    ).map(([k, label]) => (
                      <th key={k}>
                        <button type="button" className="th-btn" onClick={() => toggleSort("tool", k)}>
                          {label}
                          {ui.toolSort.key === k ? (ui.toolSort.dir === "desc" ? " ↓" : " ↑") : ""}
                        </button>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {tools.map((r) => (
                    <tr
                      key={r.tool}
                      className="mon-row"
                      onClick={() =>
                        go(tracesHref(drillTool(project, data.from, data.to, r.tool)))
                      }
                    >
                      <td className="mono">{r.tool}</td>
                      <td>{fmtInt(r.calls)}</td>
                      <td>{fmtPct(r.share)}</td>
                      <td>
                        <button
                          type="button"
                          className="linkish err"
                          onClick={(e) => {
                            e.stopPropagation();
                            go(tracesHref(drillTool(project, data.from, data.to, r.tool, true)));
                          }}
                        >
                          {fmtPct(r.error_rate)}
                        </button>
                      </td>
                      <td>{fmtDurMs(r.p50_ms)}</td>
                      <td>{fmtDurMs(r.p99_ms)}</td>
                      {showToolTokens ? (
                        <td>{r.tokens && r.tokens.total > 0 ? fmtInt(r.tokens.total) : "—"}</td>
                      ) : null}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
