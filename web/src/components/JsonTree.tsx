import { useState } from "react";
import { isTruncatedField } from "../api/types";
import { fmtBytes } from "../lib/format";

type Props = {
  value: unknown;
  depth?: number;
  name?: string;
  /** When a TruncatedField is shown, call to re-fetch with ?full=1. */
  onRequestFull?: () => void;
};

const MAX_KIDS = 100;
const MAX_DEPTH = 8;
const PREVIEW_LEN = 80;

function StrPreview({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  if (text.length <= PREVIEW_LEN) {
    return <span className="s">{JSON.stringify(text)}</span>;
  }
  if (open) {
    return (
      <span className="s">
        {JSON.stringify(text)}{" "}
        <button type="button" className="linkish" onClick={() => setOpen(false)}>
          collapse
        </button>
      </span>
    );
  }
  return (
    <span className="s">
      {JSON.stringify(text.slice(0, PREVIEW_LEN) + "…")}{" "}
      <button type="button" className="linkish" onClick={() => setOpen(true)}>
        show full
      </button>
    </span>
  );
}

function previewObj(v: unknown): string {
  if (v === null) return "null";
  if (typeof v === "string") return JSON.stringify(v.length > PREVIEW_LEN ? v.slice(0, PREVIEW_LEN) + "…" : v);
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v)) return `Array(${v.length})`;
  if (typeof v === "object") return `{${Object.keys(v as object).length}}`;
  return String(v);
}

export function JsonTree({ value, depth = 0, name, onRequestFull }: Props) {
  if (isTruncatedField(value)) {
    return (
      <div className="banner">
        Truncated field ({fmtBytes(value._bytes)}). Preview below
        {onRequestFull ? (
          <>
            {" — "}
            <button type="button" className="btn" onClick={onRequestFull}>
              Load full
            </button>
          </>
        ) : (
          " — use Load full."
        )}
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
        {typeof value === "string" ? (
          <StrPreview text={value} />
        ) : (
          <span className={cls}>{previewObj(value)}</span>
        )}
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
        {previewObj(value)}
        {tooDeep ? " (collapsed: depth)" : more > 0 ? ` (+${more} more)` : ""}
      </summary>
      {kids.map(([k, v]) => (
        <div key={k}>
          <JsonTree value={v} depth={depth + 1} name={k} onRequestFull={onRequestFull} />
        </div>
      ))}
    </details>
  );
}
