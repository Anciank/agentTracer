import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { EdgeStats, GraphResponse, NodeStats, PathGroup } from "../api/types";
import {
  FAIL_ERROR_RATE,
  isFailingNode,
  isWarnNode,
  layoutGraph,
  type LayoutNode,
} from "../lib/graphLayout";
import { fmtDurMs, fmtInt, fmtPct, fmtTokens } from "../lib/format";

/** Backend often sends {0,0,0} when no token samples — show — if latency also null. */
function fmtNodeTok(s: NodeStats): string {
  if (s.tokens == null) return "—";
  if (s.tokens.total === 0 && s.p95_ms == null && s.p50_ms == null) return "—";
  return fmtTokens(s.tokens);
}

/** Fit never zooms out below this so 13px meta ≥ ~11px effective. Pan/scroll for the rest. */
const MIN_FIT_ZOOM = 0.85;

type Props = {
  graph: GraphResponse;
  paths?: PathGroup[];
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
  if (isFailingNode(n)) return "var(--error-bg)";
  if (isWarnNode(n)) return "var(--warn-bg)";
  // Healthy (error_count === 0): theme card surface — no tint in light or dark.
  return "var(--raise)";
}

function nodeStroke(n: NodeStats, highlight: boolean): string {
  if (highlight) return "var(--focus)";
  if (isFailingNode(n)) return "var(--error)";
  if (isWarnNode(n)) return "var(--warn, #b45309)";
  if (n.kind === "other") return "var(--muted)";
  return "var(--border)";
}

function edgeLabel(kind: "forward" | "back" | "self", e: EdgeStats): string {
  // Self / repeat: show avg repeats (×N), never share (>1 is not a share %).
  if (kind === "self") {
    const n = e.repeat_avg != null ? Math.round(e.repeat_avg) : e.repeat_max != null ? Math.round(e.repeat_max) : null;
    return n != null && n > 0 ? `×${n}` : "↻";
  }
  if (kind === "back" && e.repeat_avg != null && e.share > 1) {
    return `×${Math.round(e.repeat_avg)}`;
  }
  return fmtInt(e.count);
}

declare global {
  interface Window {
    __mapPerf?: {
      layout_ms: number;
      paint_ms: number;
      total_ms: number;
      nodes: number;
      edges: number;
    };
  }
}

export function AgentMap({
  graph,
  paths,
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
  const layoutStart = useRef(0);

  const edges = useMemo(() => {
    const list = showHiddenEdges ? [...graph.edges, ...graph.edges_hidden] : graph.edges.slice();
    list.sort((a, b) => b.count - a.count || a.from.localeCompare(b.from) || a.to.localeCompare(b.to));
    return list;
  }, [graph, showHiddenEdges]);

  const layout = useMemo(() => {
    layoutStart.current = performance.now();
    return layoutGraph(graph.nodes, edges, { expandOther, paths });
  }, [graph.nodes, edges, expandOther, paths]);

  const layoutMs = useMemo(() => {
    return performance.now() - layoutStart.current;
  }, [layout]);

  useEffect(() => {
    const t0 = performance.now();
    // Two rAFs: after React commit + browser paint of the SVG.
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        const paint_ms = performance.now() - t0;
        const total_ms = layoutMs + paint_ms;
        window.__mapPerf = {
          layout_ms: Number(layoutMs.toFixed(3)),
          paint_ms: Number(paint_ms.toFixed(3)),
          total_ms: Number(total_ms.toFixed(3)),
          nodes: layout.nodes.length,
          edges: layout.edges.length,
        };
      });
    });
  }, [layout, layoutMs]);

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
    // Clamp so labels stay readable; pan/scroll for overflow.
    const z = Math.max(MIN_FIT_ZOOM, Math.min(1.5, Math.min(zx, zy)));
    setZoom(z);
    setPan({
      x: Math.max(16, (vw - width * z) / 2),
      y: Math.max(16, (vh - height * z) / 2),
    });
  }, [layout]);

  useEffect(() => {
    fit();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- fit on first graph identity
  }, [graph.project, graph.window.preset, graph.window.from]);

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
    const failing = isFailingNode(s);
    const warn = isWarnNode(s);
    const strokeW = failing ? 2.5 : warn || highlight ? 2 : 1;
    return (
      <g
        key={n.key}
        data-node={n.key}
        data-failing={failing ? "1" : undefined}
        data-warn={warn ? "1" : undefined}
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
        <text x={12} y={22} className="map-label">
          {n.label.length > 20 ? n.label.slice(0, 18) + "…" : n.label}
        </text>
        <text x={12} y={42} className="map-meta">
          {fmtInt(s.count)} · err {fmtPct(s.error_rate)}
        </text>
        <text x={12} y={60} className="map-meta">
          p95 {fmtDurMs(s.p95_ms)} · {fmtNodeTok(s)}
        </text>
        {failing && !n.isOther && (
          <circle cx={n.w - 10} cy={10} r={5} fill="var(--error)" />
        )}
        {warn && !n.isOther && (
          <circle cx={n.w - 10} cy={10} r={4} fill="var(--warn, #b45309)" opacity={0.85} />
        )}
      </g>
    );
  };

  return (
    <div className="map-shell" data-fail-threshold={FAIL_ERROR_RATE}>
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
          {" · "}fail ≥{Math.round(FAIL_ERROR_RATE * 100)}% err
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
            {layout.edges.map((e) => {
              const label = edgeLabel(e.kind, e.stats);
              const lw = Math.max(e.kind === "self" ? 22 : 28, label.length * 6.5 + 8);
              const er = e.stats.error_rate ?? (e.stats.count ? e.stats.error_count / e.stats.count : 0);
              const edgeFail = e.stats.error_count > 0 && er >= FAIL_ERROR_RATE;
              const edgeWarn = e.stats.error_count > 0 && er < FAIL_ERROR_RATE;
              const tip = [
                `${e.stats.from} → ${e.stats.to}`,
                `${e.stats.count} · share ${fmtPct(e.stats.share)}`,
                e.stats.error_count > 0 ? `err ${fmtPct(er)} (${e.stats.error_count})` : null,
                e.stats.p95_ms != null ? `p95 ${fmtDurMs(e.stats.p95_ms)}` : null,
              ].filter(Boolean).join(" · ");
              return (
              <g
                key={e.id}
                data-edge={e.id}
                data-edge-kind={e.kind}
                data-edge-fail={edgeFail ? "1" : undefined}
                data-edge-warn={edgeWarn ? "1" : undefined}
                className="map-edge"
                style={{ cursor: "pointer" }}
                onClick={(ev) => {
                  ev.stopPropagation();
                  onEdgeClick(e.stats);
                }}
              >
                <title>{tip}</title>
                <path d={e.d} fill="none" stroke="transparent" strokeWidth={12} />
                <path
                  d={e.d}
                  fill="none"
                  stroke={edgeFail ? "var(--error)" : edgeWarn ? "var(--warn, #b45309)" : "var(--muted)"}
                  strokeWidth={e.kind === "self" ? 1.75 : e.kind === "back" ? 1.75 : 1.5}
                  strokeOpacity={e.kind === "back" ? 0.55 : 0.75}
                  strokeDasharray={e.kind === "back" ? "5 3" : undefined}
                  markerEnd="url(#arrow)"
                />
                <g transform={`translate(${e.labelX},${e.labelY})`}>
                  <rect
                    x={-lw / 2}
                    y={-9}
                    width={lw}
                    height={16}
                    rx={4}
                    fill="var(--panel)"
                    stroke="var(--border)"
                  />
                  <text textAnchor="middle" y={3} className="map-edge-label">
                    {label}
                  </text>
                </g>
              </g>
              );
            })}
            {layout.nodes.map(renderNode)}
          </g>
        </svg>
      </div>
    </div>
  );
}
