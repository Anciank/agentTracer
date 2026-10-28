import { useMemo, useRef, useEffect } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import type { SpanSummary } from "../api/types";
import { indexTree, visibleRows, treeGuides } from "../lib/tree";
import { fmtDurMs, fmtInt } from "../lib/format";

const TYPE_COLOR: Record<string, string> = {
  chain: "var(--chain)",
  llm: "var(--llm)",
  tool: "var(--tool)",
  retriever: "var(--retriever)",
  embedding: "var(--embedding)",
  prompt: "var(--prompt)",
  parser: "var(--parser)",
};

type Props = {
  rows: SpanSummary[];
  spanMs: number;
  selectedId: string | null;
  folded: Set<string>;
  onSelect: (id: string) => void;
  onToggleFold: (id: string) => void;
};

export function Waterfall({ rows, spanMs, selectedId, folded, onSelect, onToggleFold }: Props) {
  const parentRef = useRef<HTMLDivElement>(null);
  const tree = useMemo(() => indexTree(rows), [rows]);
  const visible = useMemo(() => visibleRows(rows, folded, tree), [rows, folded, tree]);
  const guides = useMemo(() => treeGuides(visible), [visible]);

  const virt = useVirtualizer({
    count: visible.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 28,
    overscan: 20,
  });

  useEffect(() => {
    if (!selectedId) return;
    const idx = visible.findIndex((r) => r.id === selectedId);
    if (idx >= 0) virt.scrollToIndex(idx, { align: "auto" });
  }, [selectedId, visible, virt]);

  const span = Math.max(spanMs, 1);
  const pct = (v: number) => (v / span) * 100;
  const steps = [10, 50, 100, 250, 500, 1000, 2000, 5000, 10000, 30000, 60000];
  const step = steps.find((s) => span / s <= 6) || 60000;
  const ticks: number[] = [];
  for (let t = 0; t <= span + 1e-6; t += step) ticks.push(t);

  return (
    <div className="wf-wrap" ref={parentRef}>
      <div className="ruler-row">
        <div />
        <div />
        <div />
        <div className="ruler">
          {ticks.map((t) => (
            <div key={t} className="tick" style={{ left: `${pct(t)}%` }}>
              <span>{fmtDurMs(t)}</span>
            </div>
          ))}
        </div>
      </div>
      <div style={{ height: virt.getTotalSize(), position: "relative", width: "100%" }}>
        {virt.getVirtualItems().map((vi) => {
          const r = visible[vi.index];
          const kids = tree.get(r.id)?.kids || 0;
          const isFolded = folded.has(r.id);
          const w = r.latency_ms == null ? 0 : Math.max(pct(r.latency_ms), 0.25);
          const color = TYPE_COLOR[r.run_type] || "var(--unknown)";
          const barCls = ["bar", r.status === "error" ? "error" : "", r.status === "running" ? "running" : ""]
            .filter(Boolean)
            .join(" ");
          return (
            <div
              key={r.id}
              className={`wf-row ${r.id === selectedId ? "selected" : ""}`}
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                height: 28,
                transform: `translateY(${vi.start}px)`,
              }}
              onClick={() => onSelect(r.id)}
            >
              <div className="label" title={r.name}>
                <span className="tree">
                  {guides[vi.index].map((c, i) => (
                    <i key={i} className={c} />
                  ))}
                </span>
                {kids ? (
                  <button
                    type="button"
                    className="caret"
                    aria-expanded={!isFolded}
                    onClick={(e) => {
                      e.stopPropagation();
                      onToggleFold(r.id);
                    }}
                  >
                    {isFolded ? "▸" : "▾"}
                  </button>
                ) : (
                  <span className="caret leaf" />
                )}
                <span className="dot" style={{ background: color }} />
                <span className="nm">{r.name}</span>
                {isFolded ? <span className="fold-count">{kids}</span> : null}
              </div>
              <div className="rtype">{r.run_type}</div>
              <div className="dur">{fmtDurMs(r.latency_ms)}</div>
              <div className="track">
                <div
                  className={barCls}
                  style={{ left: `${pct(r.offset_ms)}%`, width: `${w}%`, background: color }}
                />
                {r.tokens.total > 0 && (
                  <div className="bar-tokens" style={{ left: `calc(${pct(r.offset_ms)}% + ${w}% + 4px)` }}>
                    {fmtInt(r.tokens.total)} tok
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
