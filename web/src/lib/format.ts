export function fmtDurMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || Number.isNaN(ms)) return "—";
  if (ms === 0) return "0";
  const s = ms / 1000;
  if (ms < 1) return `${(ms * 1000).toFixed(0)}µs`;
  if (ms < 1000) return `${ms.toFixed(0)}ms`;
  if (s < 60) return `${s.toFixed(2)}s`;
  return `${Math.floor(s / 60)}m${(s % 60).toFixed(0)}s`;
}

export function fmtInt(n: number | null | undefined): string {
  return (n || 0).toLocaleString();
}

export function fmtRel(iso: string | null | undefined): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  const d = Date.now() - t;
  if (d < 60_000) return `${Math.max(0, Math.round(d / 1000))}s ago`;
  if (d < 3600_000) return `${Math.round(d / 60_000)}m ago`;
  if (d < 86400_000) return `${Math.round(d / 3600_000)}h ago`;
  return `${Math.round(d / 86400_000)}d ago`;
}

export function fmtPct(frac: number): string {
  return `${(frac * 100).toFixed(1)}%`;
}

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

/** Null tokens → "—"; real zero totals still show "0 tok". */
export function fmtTokens(t: { total: number } | null | undefined): string {
  if (t == null) return "—";
  return `${(t.total || 0).toLocaleString()} tok`;
}

/** by_tool tokens: null → —; total-only (in=out=0) → total; else in · out. */
export function fmtToolTokens(
  t: { input: number; output: number; total: number } | null | undefined
): string {
  if (t == null) return "—";
  if (t.input === 0 && t.output === 0) return fmtInt(t.total);
  return `${fmtInt(t.input)} in · ${fmtInt(t.output)} out`;
}
