import { isTruncatedField } from "../api/types";
import { fmtBytes } from "../lib/format";

type Props = {
  value: unknown;
  depth?: number;
  name?: string;
};

const MAX_KIDS = 100;
const MAX_DEPTH = 8;

function preview(v: unknown): string {
  if (v === null) return "null";
  if (typeof v === "string") return JSON.stringify(v.length > 80 ? v.slice(0, 80) + "…" : v);
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v)) return `Array(${v.length})`;
  if (typeof v === "object") return `{${Object.keys(v as object).length}}`;
  return String(v);
}

export function JsonTree({ value, depth = 0, name }: Props) {
  if (isTruncatedField(value)) {
    return (
      <div className="banner">
        Truncated field ({fmtBytes(value._bytes)}). Preview below — use Load full.
        <pre className="json" style={{ marginTop: 8 }}>
          {JSON.stringify(value.preview, null, 2)}
        </pre>
      </div>
    );
  }
  if (value === null || typeof value !== "object") {
    const cls =
      value === null ? "null" : typeof value === "string" ? "s" : typeof value === "number" ? "n" : "";
    return (
      <span>
        {name != null && <span className="k">{name}: </span>}
        <span className={cls}>{preview(value)}</span>
      </span>
    );
  }
  const entries = Array.isArray(value)
    ? value.map((v, i) => [String(i), v] as const)
    : Object.entries(value as Record<string, unknown>);
  const tooDeep = depth >= MAX_DEPTH;
  const kids = tooDeep ? [] : entries.slice(0, MAX_KIDS);
  const more = entries.length - kids.length;

  return (
    <details className="jnode" open={depth < 2}>
      <summary>
        {name != null ? <span className="k">{name} </span> : null}
        {preview(value)}
        {tooDeep ? " (collapsed: depth)" : more > 0 ? ` (+${more} more)` : ""}
      </summary>
      {kids.map(([k, v]) => (
        <div key={k}>
          <JsonTree value={v} depth={depth + 1} name={k} />
        </div>
      ))}
    </details>
  );
}
