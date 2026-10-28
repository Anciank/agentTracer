import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { EdgeStats, GraphResponse, NodeStats } from "../api/types";
import { layoutGraph, type LayoutNode } from "../lib/graphLayout";
import { fmtDurMs, fmtInt, fmtPct } from "../lib/format";

type Props = {
  graph: GraphResponse;
  showHiddenEdges: boolean;
  expandOther: boolean;
  findQuery: string;
  findInputRef?: React.RefObject<HTMLInputElement | null>;
  onFindQuery: (q: string) => void;
  onToggleOther: () => void;
  onNodeClick: (key: string, stats: NodeStats) => void;
  onEdgeClick: (edge: EdgeStats) => void;
};

function nodeFill(n: NodeStats): string {
  if (n.kind === "other") return "var(--chip)";
  if (n.error_rate >= 0.2) return "var(--error-bg)";
  if (n.error_rate >= 0.05) return "color-mix(in srgb, var(--error-bg) 55%, var(--raise))";
  return "var(--raise)";
}

function nodeStroke(n: NodeStats, highlight: boolean): string {
  if (highlight) return "var(--focus)";
  if (n.error_count > 0) return "var(--error)";
  if (n.kind === "other") return "var(--muted)";
  return "var(--border)";
}

function edgeLabel(e: EdgeStats): string {
  const base = fmtInt(e.count);
  if (e.loop && e.repeat_max != null) {
    const n = Math.round(e.repeat_avg ?? e.repeat_max);
    return `${base} ×${n}`;
  }
  if (e.loop) return `${base} ×N`;
  return base;
}

export function AgentMap({
  graph,
  showHiddenEdges,
  expandOther,
  findQuery,
  findInputRef,
  onFindQuery,
  onToggleOther,
  onNodeClick,
  onEdgeClick,
}: Props) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [pan, setPan] = useState({ x: 40, y: 40 });
  const [zoom, setZoom] = useState(1);
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null);
  const [hover, setHover] = useState<string | null>(null);

  const edges = useMemo(() => {
    const list = showHiddenEdges ? [...graph.edges, ...graph.edges_hidden] : graph.edges.slice();
    list.sort((a, b) => b.count - a.count || a.from.localeCompare(b.from) || a.to.localeCompare(b.to));
    return list;
  }, [graph, showHiddenEdges]);

  const layout = useMemo(
    () => layoutGraph(graph.nodes, graph.other, edges, { expandOther }),
    [graph.nodes, graph.other, edges, expandOther]
  );

  const matchKey = useMemo(() => {
    const q = findQuery.trim().toLowerCase();
    if (!q) return null;
    const hit = layout.nodes.find(
      (n) => n.label.toLowerCase().includes(q) || n.key.toLowerCase().includes(q)
    );
    return hit?.key ?? null;
  }, [findQuery, layout.nodes]);

  const fit = useCallback(() => {
    const el = wrapRef.current;
    if (!el) return;
    const { width, height } = layout;
    const vw = el.clientWidth || 800;
    const vh = el.clientHeight || 500;
    const zx = (vw - 32) / Math.max(width, 1);
    const zy = (vh - 32) / Math.max(height, 1);
    const z = Math.max(0.25, Math.min(1.5, Math.min(zx, zy)));
    setZoom(z);
    setPan({
      x: (vw - width * z) / 2,
      y: Math.max(16, (vh - height * z) / 2),
    });
  }, [layout]);

  useEffect(() => {
    fit();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- fit on first graph identity
  }, [graph.project_id, graph.window]);

  useEffect(() => {
    if (!matchKey) return;
    const n = layout.nodes.find((x) => x.key === matchKey);
    const el = wrapRef.current;
    if (!n || !el) return;
    const vw = el.clientWidth;
    const vh = el.clientHeight;
    setPan({
      x: vw / 2 - (n.x + n.w / 2) * zoom,
      y: vh / 2 - (n.y + n.h / 2) * zoom,
    });
  }, [matchKey, layout.nodes, zoom]);

  const onWheel = (e: React.WheelEvent) => {
    e.preventDefault();
    const el = wrapRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    const my = e.clientY - rect.top;
    const factor = e.deltaY > 0 ? 0.9 : 1.1;
    const next = Math.max(0.2, Math.min(3, zoom * factor));
    // zoom toward cursor
    const wx = (mx - pan.x) / zoom;
    const wy = (my - pan.y) / zoom;
    setZoom(next);
    setPan({ x: mx - wx * next, y: my - wy * next });
  };

  const onPointerDown = (e: React.PointerEvent) => {
    if ((e.target as HTMLElement).closest("[data-node],[data-edge]")) return;
    drag.current = { x: e.clientX, y: e.clientY, px: pan.x, py: pan.y };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  };
  const onPointerMove = (e: React.PointerEvent) => {
    if (!drag.current) return;
    setPan({
      x: drag.current.px + (e.clientX - drag.current.x),
      y: drag.current.py + (e.clientY - drag.current.y),
    });
  };
  const onPointerUp = () => {
    drag.current = null;
  };

  const renderNode = (n: LayoutNode) => {
    const highlight = matchKey === n.key || hover === n.key;
    const s = n.stats;
    const strokeW = s.error_count > 0 ? 2.5 : highlight ? 2 : 1;
    return (
      <g
        key={n.key}
        data-node={n.key}
        transform={`translate(${n.x},${n.y})`}
        style={{ cursor: "pointer" }}
        onMouseEnter={() => setHover(n.key)}
        onMouseLeave={() => setHover(null)}
        onClick={(e) => {
          e.stopPropagation();
          if (n.isOther) onToggleOther();
          else onNodeClick(n.key, s);
        }}
      >
        <rect
          width={n.w}
          height={n.h}
          rx={8}
          fill={nodeFill(s)}
          stroke={nodeStroke(s, highlight)}
          strokeWidth={strokeW}
          strokeDasharray={n.isOther ? "4 3" : undefined}
          filter={highlight ? "url(#glow)" : undefined}
        />
        <text x={10} y={18} className="map-label">
          {n.label.length > 18 ? n.label.slice(0, 16) + "…" : n.label}
        </text>
        <text x={10} y={34} className="map-meta">
          {fmtInt(s.count)} · err {fmtPct(s.error_rate)}
        </text>
        <text x={10} y={48} className="map-meta">
          p95 {fmtDurMs(s.p95_ms)} · {fmtInt(s.tokens.total)} tok
        </text>
        {s.error_count > 0 && !n.isOther && (
          <circle cx={n.w - 10} cy={10} r={5} fill="var(--error)" />
        )}
      </g>
    );
  };

  return (
    <div className="map-shell">
      <div className="map-toolbar">
        <input
          ref={findInputRef as React.RefObject<HTMLInputElement>}
          className="map-find"
          placeholder="Find node (/)"
          value={findQuery}
          onChange={(e) => onFindQuery(e.target.value)}
          aria-label="Find node"
        />
        <button type="button" id="map-fit-btn" className="chip" onClick={fit} title="Fit (f)">
          Fit
        </button>
        <span className="map-hint">
          {graph.trace_count.toLocaleString()} traces · {layout.nodes.length} nodes · wheel zoom · drag pan
        </span>
      </div>
      <div
        className="map-viewport"
        ref={wrapRef}
        onWheel={onWheel}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
      >
        <svg className="map-svg" width="100%" height="100%">
          <defs>
            <marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--muted)" />
            </marker>
            <filter id="glow">
              <feDropShadow dx="0" dy="0" stdDeviation="2" floodColor="var(--focus)" floodOpacity="0.7" />
            </filter>
          </defs>
          <g transform={`translate(${pan.x},${pan.y}) scale(${zoom})`}>
            {layout.edges.map((e) => (
              <g
                key={e.id}
                data-edge={e.id}
                className="map-edge"
                style={{ cursor: "pointer" }}
                onClick={(ev) => {
                  ev.stopPropagation();
                  onEdgeClick(e.stats);
                }}
              >
                <path d={e.d} fill="none" stroke="transparent" strokeWidth={12} />
                <path
                  d={e.d}
                  fill="none"
                  stroke={e.stats.error_count > 0 ? "var(--error)" : "var(--muted)"}
                  strokeWidth={e.stats.loop ? 2 : 1.5}
                  strokeOpacity={0.75}
                  markerEnd="url(#arrow)"
                />
                <g transform={`translate(${e.labelX},${e.labelY})`}>
                  <rect
                    x={-18}
                    y={-9}
                    width={Math.max(36, edgeLabel(e.stats).length * 6.5)}
                    height={16}
                    rx={4}
                    fill="var(--panel)"
                    stroke="var(--border)"
                  />
                  <text textAnchor="middle" y={3} className="map-edge-label">
                    {edgeLabel(e.stats)}
                  </text>
                </g>
              </g>
            ))}
            {layout.nodes.map(renderNode)}
          </g>
        </svg>
      </div>
    </div>
  );
}
