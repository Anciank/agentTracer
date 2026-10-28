import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { getMessages, getRun } from "../api/client";
import type { MessagesResponse, RunDetail as RunDetailT, SpanSummary } from "../api/types";
import { fmtDurMs, fmtInt, fmtBytes } from "../lib/format";
import { JsonTree } from "./JsonTree";
import { MessagesView } from "./Messages";

const TABS = [
  "Overview",
  "Messages",
  "Inputs",
  "Outputs",
  "Metadata",
  "Error",
  "Attachments",
  "Raw",
] as const;

type Props = {
  span: SpanSummary | null;
  tabIndex?: number;
  onTab?: (i: number) => void;
};

function OverviewPanel({
  run,
  msgLoading,
  msgData,
  onLoadFull,
}: {
  run: RunDetailT | undefined;
  msgLoading: boolean;
  msgData: MessagesResponse | undefined;
  onLoadFull: () => void;
}) {
  const errText =
    run?.error == null
      ? null
      : typeof run.error === "string"
        ? run.error
        : JSON.stringify(run.error, null, 2);

  return (
    <div className="overview-scroll">
      {run?.truncated && (
        <div className="banner">
          Some fields truncated ({fmtBytes(2 * 1024 * 1024)}+).
          <button type="button" className="btn" onClick={onLoadFull}>
            Load full
          </button>
        </div>
      )}

      <section className="ov-sec">
        <h3>Input</h3>
        {run ? <JsonTree value={run.inputs} onRequestFull={onLoadFull} /> : <div className="hint">…</div>}
      </section>

      <section className="ov-sec">
        <h3>Output</h3>
        {msgLoading ? (
          <div className="hint">Loading messages…</div>
        ) : msgData && msgData.messages.length ? (
          <MessagesView data={msgData} />
        ) : run ? (
          <>
            <div className="hint">No chat messages — outputs JSON.</div>
            <JsonTree value={run.outputs} onRequestFull={onLoadFull} />
          </>
        ) : (
          <div className="hint">…</div>
        )}
      </section>

      <section className="ov-sec">
        <h3>Error</h3>
        {errText ? (
          <div className="banner error" style={{ whiteSpace: "pre-wrap" }}>
            {errText}
          </div>
        ) : (
          <div className="hint">No error</div>
        )}
      </section>
    </div>
  );
}

export function RunDetail({ span, tabIndex = 0, onTab }: Props) {
  const [full, setFull] = useState(false);
  const tab = TABS[Math.min(Math.max(tabIndex, 0), TABS.length - 1)];

  const runQ = useQuery({
    queryKey: ["run", span?.id, full],
    queryFn: () => getRun(span!.id, full),
    enabled: !!span,
  });
  const wantMsgs = !!span && (tab === "Overview" || tab === "Messages");
  const msgQ = useQuery({
    queryKey: ["messages", span?.id],
    queryFn: () => getMessages(span!.id),
    enabled: wantMsgs,
  });

  if (!span) {
    return (
      <div className="detail">
        <div className="detail-empty">
          <h3>Select a span</h3>
          <p>Click a row in the waterfall to inspect inputs, outputs, and metadata.</p>
          <div className="keys" aria-hidden="true">
            <kbd>↑</kbd>
            <kbd>↓</kbd>
            <span className="hint">move</span>
            <kbd>Enter</kbd>
            <span className="hint">open</span>
            <kbd>Esc</kbd>
            <span className="hint">back</span>
          </div>
        </div>
      </div>
    );
  }

  const run = runQ.data;
  const loadFull = () => setFull(true);

  return (
    <div className="detail">
      <div className="detail-inner">
        <div>
          <span className={`badge ${span.status === "error" ? "err" : ""}`}>{span.status}</span>
          <span className="badge">{span.run_type}</span>
          {span.node && <span className="badge">{span.node}</span>}
        </div>
        <h2 style={{ margin: "8px 0 4px", fontSize: 15 }}>{span.name}</h2>
        <div className="kv">
          <dt>duration</dt>
          <dd>{fmtDurMs(span.latency_ms)}</dd>
          <dt>self-time</dt>
          <dd>{fmtDurMs(span.self_time_ms)}</dd>
          <dt>tokens</dt>
          <dd>
            {fmtInt(span.tokens.input)} in / {fmtInt(span.tokens.output)} out · {fmtInt(span.tokens.total)}
          </dd>
          <dt>model</dt>
          <dd>{span.model || "—"}</dd>
          <dt>tags</dt>
          <dd>{span.tags.length ? span.tags.join(", ") : "—"}</dd>
        </div>

        <div className="tabs" role="tablist">
          {TABS.map((t, i) => (
            <button
              key={t}
              type="button"
              role="tab"
              className={`tab ${tab === t ? "active" : ""}`}
              onClick={() => onTab?.(i)}
            >
              {t}
            </button>
          ))}
        </div>

        {runQ.isLoading && <div className="hint">hydrating…</div>}
        {run?.cold && <div className="banner">Cold payload hydrated from object store.</div>}
        {tab !== "Overview" && run?.truncated && (
          <div className="banner">
            Some fields truncated ({fmtBytes(2 * 1024 * 1024)}+).
            <button type="button" className="btn" onClick={loadFull}>
              Load full
            </button>
          </div>
        )}

        {tab === "Overview" && (
          <OverviewPanel
            run={run}
            msgLoading={msgQ.isLoading}
            msgData={msgQ.data}
            onLoadFull={loadFull}
          />
        )}
        {tab === "Messages" &&
          (msgQ.isLoading ? (
            <div className="hint">Loading messages…</div>
          ) : msgQ.data && msgQ.data.messages.length ? (
            <MessagesView data={msgQ.data} />
          ) : run ? (
            <div>
              <div className="hint">No chat messages (empty /messages) — JSON tree fallback.</div>
              <h3 style={{ fontSize: 12, margin: "8px 0 4px" }}>inputs</h3>
              <JsonTree value={run.inputs} onRequestFull={loadFull} />
              <h3 style={{ fontSize: 12, margin: "12px 0 4px" }}>outputs</h3>
              <JsonTree value={run.outputs} onRequestFull={loadFull} />
            </div>
          ) : msgQ.data ? (
            <MessagesView data={msgQ.data} />
          ) : null)}
        {tab === "Inputs" && run && <JsonTree value={run.inputs} onRequestFull={loadFull} />}
        {tab === "Outputs" && run && <JsonTree value={run.outputs} onRequestFull={loadFull} />}
        {tab === "Metadata" && run && <JsonTree value={run.extra} onRequestFull={loadFull} />}
        {tab === "Error" && (
          <div className="banner error" style={{ whiteSpace: "pre-wrap" }}>
            {typeof run?.error === "string" ? run.error : run?.error ? JSON.stringify(run.error) : "No error"}
          </div>
        )}
        {tab === "Attachments" && (
          <div>
            {(run?.attachments || []).length === 0 && <div className="hint">No attachments</div>}
            {(run?.attachments || []).map((a) => (
              <div key={a.name} style={{ marginBottom: 6 }}>
                <a href={a.url} download={a.name}>
                  {a.name}
                </a>{" "}
                <span className="hint">
                  {a.content_type} · {fmtBytes(a.size)}
                </span>
              </div>
            ))}
          </div>
        )}
        {tab === "Raw" && run && <JsonTree value={run} onRequestFull={loadFull} />}
      </div>
    </div>
  );
}

export { TABS };
