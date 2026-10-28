import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import type { LatencyBucket, MonitorBucketSize, TokenBucket, TraceBucket } from "../../api/types";
import { fmtDurMs, fmtInt } from "../../lib/format";

const PAD = { l: 52, r: 12, t: 14, b: 28 };

function useWidth(defaultW = 640): { hostRef: RefObject<HTMLDivElement>; w: number } {
  const hostRef = useRef<HTMLDivElement>(null);
  const [w, setW] = useState(defaultW);
  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const cw = entries[0]?.contentRect.width;
      if (cw) setW(cw);
    });
    ro.observe(el);
    setW(el.clientWidth || defaultW);
    return () => ro.disconnect();
  }, [defaultW]);
  return { hostRef, w };
}

/** HH:MM for 1m/1h; 'Mon 12:00' for 6h (7d window). */
export function fmtBucketLabel(t: string, bucket: string): string {
  const d = new Date(t);
  if (bucket === "6h") {
    const day = d.toLocaleString("en-US", { weekday: "short", timeZone: "UTC" });
    const hh = String(d.getUTCHours()).padStart(2, "0");
    const mm = String(d.getUTCMinutes()).padStart(2, "0");
    return `${day} ${hh}:${mm}`;
  }
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}

/** ~6 evenly spaced x-axis labels including first/last (same for all charts). */
export function xLabelIndices(n: number, target = 6): number[] {
  if (n <= 0) return [];
  if (n <= target) return Array.from({ length: n }, (_, i) => i);
  const count = Math.min(target, n);
  const out: number[] = [];
  for (let i = 0; i < count; i++) {
    out.push(Math.round((i * (n - 1)) / (count - 1)));
  }
  return [...new Set(out)];
}

function yTicks(max: number, n = 4): number[] {
  if (max <= 0) return [0];
  const raw = max / n;
  const pow = Math.pow(10, Math.floor(Math.log10(raw || 1)));
  const nice = Math.ceil(raw / pow) * pow || 1;
  const ticks: number[] = [];
  for (let v = 0; v <= max + 1e-9; v += nice) ticks.push(Math.round(v * 1000) / 1000);
  if (ticks[ticks.length - 1] < max) ticks.push(ticks[ticks.length - 1] + nice);
  return ticks;
}

function fmtTok(t: number): string {
  if (t >= 1_000_000) return `${(t / 1_000_000).toFixed(1)}M`;
  if (t >= 1000) return `${Math.round(t / 1000)}k`;
  return fmtInt(t);
}

type StackedProps = {
  series: TraceBucket[];
  bucket: MonitorBucketSize | string;
  height?: number;
  hoverIdx: number | null;
  onHover: (idx: number | null) => void;
  onClickSegment: (idx: number, segment: "success" | "error") => void;
};

export function StackedTracesChart({
  series,
  bucket,
  height = 180,
  hoverIdx,
  onHover,
  onClickSegment,
}: StackedProps) {
  const { hostRef, w } = useWidth();
  const innerW = Math.max(40, w - PAD.l - PAD.r);
  const innerH = height - PAD.t - PAD.b;
  const maxY = Math.max(1, ...series.map((s) => s.total));
  const ticks = yTicks(maxY);
  const yMax = ticks[ticks.length - 1] || maxY;
  const n = series.length || 1;
  const gap = n > 40 ? 1 : 2;
  const barW = Math.max(2, innerW / n - gap);
  const xLabels = useMemo(() => new Set(xLabelIndices(n)), [n]);

  const tip = useMemo(() => {
    if (hoverIdx == null || !series[hoverIdx]) return null;
    const s = series[hoverIdx];
    return {
      x: PAD.l + hoverIdx * (barW + gap) + barW / 2,
      title: `${fmtBucketLabel(s.t, bucket)} → ${fmtBucketLabel(s.t_end, bucket)}`,
      lines: [`${fmtInt(s.success)} succeeded`, `${fmtInt(s.error)} failed`, `${fmtInt(s.total)} total`],
    };
  }, [hoverIdx, series, bucket, barW, gap]);

  return (
    <div className="mon-chart" ref={hostRef}>
      <svg width={w} height={height} className="mon-svg" onMouseLeave={() => onHover(null)}>
        {ticks.map((t) => {
          const y = PAD.t + innerH - (t / yMax) * innerH;
          return (
            <g key={t}>
              <line x1={PAD.l} x2={w - PAD.r} y1={y} y2={y} className="mon-grid-line" />
              <text x={PAD.l - 6} y={y + 3} textAnchor="end" className="mon-axis">
                {fmtInt(t)}
              </text>
            </g>
          );
        })}
        {series.map((s, i) => {
          const x = PAD.l + i * (barW + gap);
          const hOk = (s.success / yMax) * innerH;
          const hErr = (s.error / yMax) * innerH;
          const yErr = PAD.t + innerH - hErr;
          const yOk = yErr - hOk;
          const active = hoverIdx === i;
          return (
            <g key={s.t} opacity={hoverIdx == null || active ? 1 : 0.45}>
              <rect
                x={x}
                y={yOk}
                width={barW}
                height={Math.max(0, hOk)}
                className="mon-bar-ok"
                onMouseEnter={() => onHover(i)}
                onClick={() => onClickSegment(i, "success")}
              />
              <rect
                x={x}
                y={yErr}
                width={barW}
                height={Math.max(0, hErr)}
                className="mon-bar-err"
                onMouseEnter={() => onHover(i)}
                onClick={(e) => {
                  e.stopPropagation();
                  onClickSegment(i, "error");
                }}
              />
            </g>
          );
        })}
        {series.map((s, i) => {
          if (!xLabels.has(i)) return null;
          const x = PAD.l + i * (barW + gap) + barW / 2;
          return (
            <text key={`xl-${s.t}`} x={x} y={height - 8} textAnchor="middle" className="mon-axis">
              {fmtBucketLabel(s.t, bucket)}
            </text>
          );
        })}
      </svg>
      {tip && (
        <div className="mon-tip" style={{ left: Math.min(Math.max(8, tip.x - 60), w - 150), top: PAD.t }}>
          <div className="mon-tip-title">{tip.title}</div>
          {tip.lines.map((l) => (
            <div key={l}>{l}</div>
          ))}
          <div className="hint">click segment → traces</div>
        </div>
      )}
    </div>
  );
}

type LineProps = {
  series: LatencyBucket[];
  bucket: MonitorBucketSize | string;
  height?: number;
  hoverIdx: number | null;
  onHover: (idx: number | null) => void;
  onClickPoint: (idx: number) => void;
};

export function LatencyLinesChart({
  series,
  bucket,
  height = 180,
  hoverIdx,
  onHover,
  onClickPoint,
}: LineProps) {
  const { hostRef, w } = useWidth();
  const innerW = Math.max(40, w - PAD.l - PAD.r);
  const innerH = height - PAD.t - PAD.b;
  const vals = series.flatMap((s) => [s.p50_ms || 0, s.p99_ms || 0]);
  const maxY = Math.max(1, ...vals);
  const ticks = yTicks(maxY);
  const yMax = ticks[ticks.length - 1] || maxY;
  const n = series.length || 1;
  const xAt = (i: number) => PAD.l + (n === 1 ? innerW / 2 : (i / Math.max(1, n - 1)) * innerW);
  const yAt = (v: number | null) => PAD.t + innerH - ((v || 0) / yMax) * innerH;
  const xLabels = useMemo(() => new Set(xLabelIndices(n)), [n]);
  const path = (key: "p50_ms" | "p99_ms") => {
    const pts: string[] = [];
    series.forEach((s, i) => {
      if (s[key] != null) pts.push(`${xAt(i)},${yAt(s[key])}`);
    });
    return pts.length ? `M ${pts.join(" L ")}` : "";
  };
  const hover = hoverIdx != null ? series[hoverIdx] : null;

  return (
    <div className="mon-chart" ref={hostRef}>
      <svg width={w} height={height} className="mon-svg" onMouseLeave={() => onHover(null)}>
        {ticks.map((t) => {
          const y = PAD.t + innerH - (t / yMax) * innerH;
          return (
            <g key={t}>
              <line x1={PAD.l} x2={w - PAD.r} y1={y} y2={y} className="mon-grid-line" />
              <text x={PAD.l - 6} y={y + 3} textAnchor="end" className="mon-axis">
                {fmtDurMs(t)}
              </text>
            </g>
          );
        })}
        <path d={path("p99_ms")} className="mon-line-p99" fill="none" />
        <path d={path("p50_ms")} className="mon-line-p50" fill="none" />
        {series.map((s, i) => (
          <circle
            key={s.t}
            cx={xAt(i)}
            cy={yAt(s.p50_ms)}
            r={hoverIdx === i ? 4 : 2.5}
            className="mon-dot-p50"
            onMouseEnter={() => onHover(i)}
            onClick={() => onClickPoint(i)}
          />
        ))}
        {series.map((s, i) => {
          if (!xLabels.has(i)) return null;
          return (
            <text key={`xl-${s.t}`} x={xAt(i)} y={height - 8} textAnchor="middle" className="mon-axis">
              {fmtBucketLabel(s.t, bucket)}
            </text>
          );
        })}
      </svg>
      {hover && hoverIdx != null && (
        <div
          className="mon-tip"
          style={{ left: Math.min(Math.max(8, xAt(hoverIdx) - 60), w - 150), top: PAD.t }}
        >
          <div className="mon-tip-title">{fmtBucketLabel(hover.t, bucket)}</div>
          <div>p50 {fmtDurMs(hover.p50_ms)}</div>
          <div>p99 {fmtDurMs(hover.p99_ms)}</div>
          <div className="hint">click → traces in bucket</div>
        </div>
      )}
    </div>
  );
}

type TokensProps = {
  series: TokenBucket[];
  bucket: MonitorBucketSize | string;
  height?: number;
  hoverIdx: number | null;
  onHover: (idx: number | null) => void;
  onClickPoint: (idx: number) => void;
};

export function TokensChart({
  series,
  bucket,
  height = 180,
  hoverIdx,
  onHover,
  onClickPoint,
}: TokensProps) {
  const { hostRef, w } = useWidth();
  const innerW = Math.max(40, w - PAD.l - PAD.r);
  const innerH = height - PAD.t - PAD.b;
  const maxY = Math.max(1, ...series.map((s) => s.total));
  const ticks = yTicks(maxY);
  const yMax = ticks[ticks.length - 1] || maxY;
  const n = series.length || 1;
  const gap = n > 40 ? 1 : 2;
  const barW = Math.max(2, innerW / n - gap);
  const xLabels = useMemo(() => new Set(xLabelIndices(n)), [n]);
  const hover = hoverIdx != null ? series[hoverIdx] : null;

  return (
    <div className="mon-chart" ref={hostRef}>
      <svg width={w} height={height} className="mon-svg" onMouseLeave={() => onHover(null)}>
        {ticks.map((t) => {
          const y = PAD.t + innerH - (t / yMax) * innerH;
          return (
            <g key={t}>
              <line x1={PAD.l} x2={w - PAD.r} y1={y} y2={y} className="mon-grid-line" />
              <text x={PAD.l - 6} y={y + 3} textAnchor="end" className="mon-axis">
                {fmtTok(t)}
              </text>
            </g>
          );
        })}
        {series.map((s, i) => {
          const x = PAD.l + i * (barW + gap);
          const hIn = (s.input / yMax) * innerH;
          const hOut = (s.output / yMax) * innerH;
          const yOut = PAD.t + innerH - hOut;
          const yIn = yOut - hIn;
          const active = hoverIdx === i;
          return (
            <g key={s.t} opacity={hoverIdx == null || active ? 1 : 0.45}>
              <rect
                x={x}
                y={yIn}
                width={barW}
                height={Math.max(0, hIn)}
                className="mon-bar-in"
                onMouseEnter={() => onHover(i)}
                onClick={() => onClickPoint(i)}
              />
              <rect
                x={x}
                y={yOut}
                width={barW}
                height={Math.max(0, hOut)}
                className="mon-bar-out"
                onMouseEnter={() => onHover(i)}
                onClick={() => onClickPoint(i)}
              />
            </g>
          );
        })}
        {series.map((s, i) => {
          if (!xLabels.has(i)) return null;
          const x = PAD.l + i * (barW + gap) + barW / 2;
          return (
            <text key={`xl-${s.t}`} x={x} y={height - 8} textAnchor="middle" className="mon-axis">
              {fmtBucketLabel(s.t, bucket)}
            </text>
          );
        })}
      </svg>
      {hover && hoverIdx != null && (
        <div
          className="mon-tip"
          style={{
            left: Math.min(Math.max(8, PAD.l + hoverIdx * (barW + gap) - 40), w - 150),
            top: PAD.t,
          }}
        >
          <div className="mon-tip-title">{fmtBucketLabel(hover.t, bucket)}</div>
          <div>in {fmtInt(hover.input)}</div>
          <div>out {fmtInt(hover.output)}</div>
          <div>total {fmtInt(hover.total)}</div>
        </div>
      )}
    </div>
  );
}
