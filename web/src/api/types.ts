/** M2 /api/ui shapes — keep in sync with server/API.md */

export type Tokens = { input: number; output: number; total: number };

export type TraceStatus = "success" | "error" | "running";

export type TraceSummary = {
  trace_id: string;
  name: string;
  project: string;
  start: string;
  end: string | null;
  duration_ms: number | null;
  run_count: number;
  error_count: number;
  running_count: number;
  status: TraceStatus;
  tokens: Tokens;
  model: string | null;
  run_types: Record<string, number>;
};

export type SpanSummary = {
  id: string;
  parent_run_id: string | null;
  name: string;
  run_type: string;
  node: string | null;
  status: TraceStatus;
  depth: number;
  offset_ms: number;
  latency_ms: number | null;
  self_time_ms: number | null;
  start_time: string;
  end_time: string | null;
  tokens: Tokens;
  model: string | null;
  tags: string[];
  dotted_order: string;
  has_error: boolean;
  payload_hot: boolean;
  attachment_count: number;
};

export type TruncatedField = {
  _truncated: true;
  _bytes: number;
  preview: unknown;
};

export function isTruncatedField(v: unknown): v is TruncatedField {
  return !!v && typeof v === "object" && (v as TruncatedField)._truncated === true;
}

export type ProjectCard = {
  id: string;
  name: string;
  trace_count_24h: number;
  trace_count_7d: number;
  error_rate_24h: number;
  p50_ms: number | null;
  p95_ms: number | null;
  tokens_24h: number;
};

export type TraceListResponse = {
  traces: TraceSummary[];
  next_cursor: string | null;
};

export type TraceSummaryResponse = {
  trace_id: string;
  span_ms: number;
  summary: TraceSummary;
  rows: SpanSummary[];
};

export type AttachmentMeta = {
  name: string;
  content_type: string;
  size: number;
  url: string;
};

export type RunDetail = {
  id: string;
  parent_run_id: string | null;
  trace_id: string;
  session_id: string;
  name: string;
  run_type: string;
  node: string | null;
  status: TraceStatus;
  start_time: string;
  end_time: string | null;
  latency_ms: number | null;
  self_time_ms: number | null;
  tokens: Tokens;
  model: string | null;
  tags: string[];
  dotted_order: string;
  inputs: unknown;
  outputs: unknown;
  extra: unknown;
  events: unknown;
  serialized: unknown;
  error: string | TruncatedField | null;
  attachments: AttachmentMeta[];
  cold: boolean;
  truncated: boolean;
};

export type ContentPart =
  | { type: "text"; text: string }
  | { type: "image_url"; url: string }
  | { type: "tool_use"; id: string; name: string; input: unknown }
  | { type: "tool_result"; tool_use_id: string; content: string }
  | { type: "json"; value: unknown };

export type ToolCall = { id: string; name: string; arguments: Record<string, unknown> };

export type ChatMessage = {
  id: string;
  role: "user" | "assistant" | "system" | "tool" | "function";
  name: string | null;
  content: ContentPart[];
  tool_calls: ToolCall[] | null;
  tool_call_id: string | null;
  source: "openai" | "langchain" | "anthropic" | "unknown";
};

export type MessagePair = { call_id: string; call_msg: number; result_msg: number };

export type MessagesResponse = {
  messages: ChatMessage[];
  paired: MessagePair[];
  raw_path: string | null;
};

export type StatsResponse = {
  trace_count: number;
  error_rate: number;
  p50_ms: number | null;
  p95_ms: number | null;
  tokens: Tokens;
  by_day: { day: string; traces: number; errors: number; tokens: number }[];
  by_model: { model: string; calls: number; tokens: number }[];
};

export type TraceListParams = {
  project?: string;
  q?: string;
  status?: TraceStatus;
  run_type?: string;
  model?: string;
  min_tokens?: number;
  min_duration_ms?: number;
  from?: string;
  to?: string;
  sort?: "start_desc" | "duration_desc" | "tokens_desc" | "errors_desc";
  limit?: number;
  cursor?: string;
};

export type StreamEvent =
  | { type: "trace"; id?: string; data: TraceSummary }
  | { type: "run_upsert"; id?: string; data: { trace_id: string; row: SpanSummary } }
  | { type: "heartbeat"; data: Record<string, never> };
