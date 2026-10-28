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
  /** M3.5 ask: traces that visited this node_key */
  node?: string;
  /** M3.5 ask: "{from}>{to}" transition */
  edge?: string;
  /** M3.5 ask: path fingerprint */
  path?: string;
  /** M3.5 ask: with node, only traces that errored at that node */
  node_fail?: string;
  /** M3.6: traces containing a run_type=tool with this name */
  tool?: string;
};

export type StreamEvent =
  | { type: "trace"; id?: string; data: TraceSummary }
  | { type: "run_upsert"; id?: string; data: { trace_id: string; row: SpanSummary } }
  | { type: "heartbeat"; data: Record<string, never> };

/** M3.5 agent map — match server/API.md + Go handlers (origin/go-rewrite). */

export type GraphWindow = "1h" | "24h" | "7d";

export type NodeKind = "langgraph" | "run" | "other";

/** Window object returned by /graph and /paths. */
export type GraphWindowBounds = {
  from: string;
  to: string;
  /** Preset used (`1h`|`24h`|`7d`) or `custom` when from/to override. */
  preset: string;
};

export type NodeStats = {
  key: string;
  label: string;
  run_type: string | null;
  kind: NodeKind;
  count: number;
  error_count: number;
  error_rate: number;
  p50_ms: number | null;
  p95_ms: number | null;
  tokens: Tokens;
  truncated: boolean;
  error_rate_delta?: number | null;
  p95_ms_delta?: number | null;
  /** On __other__ only: keys rolled into the bucket (API.md M3.5 follow-up). */
  rolled_keys?: string[];
};

export type EdgeStats = {
  from: string;
  to: string;
  count: number;
  share: number;
  /** Null when no gap samples. */
  avg_ms: number | null;
  p95_ms: number | null;
  error_count: number;
  /** Fraction 0–1; error_count / count (0 if count=0). */
  error_rate: number;
  loop: boolean;
  repeat_avg: number | null;
  repeat_max: number | null;
};

/**
 * GET /api/ui/projects/{id}/graph — `{id}` is project/session **name**.
 * `__other__` rollup (when present) lives inside `nodes` with kind=other.
 */
export type GraphResponse = {
  project: string;
  window: GraphWindowBounds;
  trace_count: number;
  nodes: NodeStats[];
  edges: EdgeStats[];
  edges_hidden: EdgeStats[];
  generated_at: string;
  compare?: Record<string, unknown>;
};

export type PathStep = { key: string; repeat: number };

export type PathGroup = {
  fingerprint: string;
  label: string;
  count: number;
  share: number;
  error_rate: number;
  /** Root latency p95 for finished roots on this fingerprint; null if none. */
  p95_ms: number | null;
  /** Distinct node keys that errored on traces with this fingerprint (sorted). */
  fail_nodes: string[];
  /** Fingerprint absent from the previous equal-length window. */
  is_new: boolean;
  steps: PathStep[];
};

export type PathsResponse = {
  project: string;
  window: GraphWindowBounds;
  paths: PathGroup[];
};

export type GraphParams = {
  window?: GraphWindow;
  from?: string;
  to?: string;
  top_n?: number;
  min_edge_share?: number;
  compare?: 0 | 1;
};

export type PathsParams = {
  window?: GraphWindow;
  from?: string;
  to?: string;
  limit?: number;
};


/** M3.6 Monitor — keep in sync with agentTracer_graph_shapes.md */

export type MonitorBucketSize = "1m" | "1h" | "6h";

export type MonitorParams = {
  window?: GraphWindow;
  from?: string;
  to?: string;
};

export type TraceBucket = {
  t: string;
  t_end: string;
  success: number;
  error: number;
  total: number;
};

export type LatencyBucket = {
  t: string;
  t_end: string;
  p50_ms: number | null;
  p99_ms: number | null;
};

export type TokenBucket = {
  t: string;
  t_end: string;
  input: number;
  output: number;
  total: number;
};

export type ModelRow = {
  model: string;
  calls: number;
  share: number;
  error_count: number;
  error_rate: number;
  p50_ms: number | null;
  p99_ms: number | null;
  tokens: Tokens;
};

export type ToolRow = {
  tool: string;
  calls: number;
  share: number;
  error_count: number;
  error_rate: number;
  p50_ms: number | null;
  p99_ms: number | null;
  /**
   * null when SUM(input)=SUM(output)=SUM(total)=0 (UI: "—").
   * Otherwise real SUMs. total-only rows have input=0 and output=0 — UI shows
   * just total with no in/out split.
   */
  tokens: Tokens | null;
};

export type MonitorResponse = {
  project_id: string;
  project: string;
  window: GraphWindow;
  from: string;
  to: string;
  bucket: MonitorBucketSize;
  trace_count: number;
  success_count: number;
  error_count: number;
  p50_ms: number | null;
  p99_ms: number | null;
  tokens: Tokens;
  /** Always null in M3.6 — reserved for M6 cost panel */
  cost: null;
  series_traces: TraceBucket[];
  series_latency: LatencyBucket[];
  series_tokens: TokenBucket[];
  by_model: ModelRow[];
  by_tool: ToolRow[];
};
