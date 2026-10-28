import type { MessagesResponse, ChatMessage, ToolCall } from "../api/types";

function ToolCallCard({
  call,
  result,
  unpaired,
}: {
  call: ToolCall;
  result?: ChatMessage;
  unpaired?: boolean;
}) {
  return (
    <div className={`tool-card ${unpaired ? "unpaired" : ""}`}>
      <div className="tn">{call.name}</div>
      <pre className="json" style={{ maxHeight: 160, marginTop: 6 }}>
        {JSON.stringify(call.arguments, null, 2)}
      </pre>
      {result ? (
        <div style={{ marginTop: 6 }}>
          <div className="role">result</div>
          {result.content.map((p, i) =>
            p.type === "text" ? <div key={i}>{p.text}</div> : <Jsonish key={i} v={p} />
          )}
        </div>
      ) : unpaired ? (
        <div className="hint">unpaired tool call</div>
      ) : null}
    </div>
  );
}

function Jsonish({ v }: { v: unknown }) {
  return <pre className="json">{JSON.stringify(v, null, 2)}</pre>;
}

export function MessagesView({ data }: { data: MessagesResponse }) {
  if (!data.messages.length) {
    return <div className="hint">No chat messages.</div>;
  }
  const pairedCalls = new Set(data.paired.map((p) => p.call_id));
  const resultByCall = new Map<string, ChatMessage>();
  for (const p of data.paired) {
    const msg = data.messages[p.result_msg];
    if (msg) resultByCall.set(p.call_id, msg);
  }
  const resultMsgIdx = new Set(data.paired.map((p) => p.result_msg));

  return (
    <div className="msgs">
      <div className="hint">source path: {data.raw_path || "—"} · {data.messages[0]?.source}</div>
      {data.messages.map((m, idx) => {
        if (resultMsgIdx.has(idx)) return null; // rendered with its call
        return (
          <div key={m.id} className={`msg ${m.role}`}>
            <div className="role">
              {m.role}
              {m.name ? ` · ${m.name}` : ""}
            </div>
            {m.content.map((p, i) => {
              if (p.type === "text") return <div key={i}>{p.text}</div>;
              if (p.type === "image_url") return <div key={i} className="hint">image: {p.url}</div>;
              if (p.type === "tool_use")
                return (
                  <ToolCallCard
                    key={i}
                    call={{ id: p.id, name: p.name, arguments: (p.input as Record<string, unknown>) || {} }}
                    unpaired={!pairedCalls.has(p.id)}
                    result={resultByCall.get(p.id)}
                  />
                );
              if (p.type === "tool_result")
                return (
                  <div key={i} className="tool-card">
                    <div className="tn">tool_result · {p.tool_use_id}</div>
                    <div>{p.content}</div>
                  </div>
                );
              return <Jsonish key={i} v={p} />;
            })}
            {m.tool_calls?.map((c) => (
              <ToolCallCard
                key={c.id}
                call={c}
                unpaired={!pairedCalls.has(c.id)}
                result={resultByCall.get(c.id)}
              />
            ))}
            {m.role === "tool" && m.tool_call_id && !pairedCalls.has(m.tool_call_id) && (
              <div className="hint">unpaired tool result · {m.tool_call_id}</div>
            )}
          </div>
        );
      })}
    </div>
  );
}
