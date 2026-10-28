# agentTracer UI API — frozen shapes (M2)

**Status:** frozen 2026-10-28 for UIBro (M3 SPA).  
**Prefix:** `/api/ui`  
**Content-Type:** `application/json` (except SSE and attachment bytes).  
**Auth:** when `AGENTTRACER_API_KEY` is set, require `x-api-key` (same gate as LangSmith paths). When empty, open (dev).  
**No cost / price fields** anywhere in this namespace. Est. USD is out of scope for M2/M3 UI API; do not add `cost_usd*` or `/models/prices`.

Old Python `/ui/*` JSON routes remain for M1 compatibility until cutover; **UIBro must call `/api/ui/*` only.**

Timestamps are ISO-8601 UTC with `Z` (e.g. `2026-10-27T15:04:05.123Z`).  
UUIDs are lowercase hex with hyphens.  
`error_rate*` is a fraction in `[0, 1]`, not a percentage.  
Nullability: omit optional filter params; response fields that are unknown are `null` (not omitted), except where noted.

Cursor encoding: keyset `(start_time, trace_id)` as URL-safe base64 of  
`{"s":"<ISO start>","t":"<trace_id>"}` (no padding). Opaque to clients — pass through only.

---

## Shared types

### `Tokens`
```json
{ "input": 6000, "output": 1152, "total": 7152 }
```
Integers ≥ 0. Missing sides are `0`, not null.

### `TraceSummary` (list row / stream event / summary.summary)
```json
{
  "trace_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
  "name": "agent",
  "project": "support",
  "start": "2026-10-27T15:04:05.123Z",
  "end": "2026-10-27T15:04:42.823Z",
  "duration_ms": 37700,
  "run_count": 33,
  "error_count": 1,
  "running_count": 0,
  "status": "error",
  "tokens": { "input": 6000, "output": 1152, "total": 7152 },
  "model": "gpt-4o-mini",
  "run_types": { "chain": 12, "llm": 8, "tool": 13 }
}
```
- `status`: `"success"` | `"error"` | `"running"` — derived: any error → `error`; else any running → `running`; else `success`.
- `end`: `null` while any run is still running.
- `duration_ms`: root span wall time (`end - start`); `null` if `end` is null.
- `model`: most-frequent non-empty model among LLM runs in the trace; ties → lexicographically first; `null` if none.
- `run_types`: counts by `run_type` string; empty object if none.
- **No `cost_usd`.**

### `SpanSummary` (waterfall row — no payloads)
```json
{
  "id": "…",
  "parent_run_id": null,
  "name": "root",
  "run_type": "chain",
  "node": "supervisor",
  "status": "success",
  "depth": 0,
  "offset_ms": 0,
  "latency_ms": 37700,
  "self_time_ms": 1200,
  "start_time": "2026-10-27T15:04:05.123Z",
  "end_time": "2026-10-27T15:04:42.823Z",
  "tokens": { "input": 100, "output": 20, "total": 120 },
  "model": "gpt-4o-mini",
  "tags": ["prod"],
  "dotted_order": "…",
  "has_error": false,
  "payload_hot": true,
  "attachment_count": 0
}
```
- `node`: LangGraph node name from `extra.metadata.langgraph_node` (or equiv); `null` if absent.
- `offset_ms` / `latency_ms` / `self_time_ms` / `depth`: same maths as current Python `traces.py` / Go `internal/traces`.
- `payload_hot`: `false` when any bulky field is cold (S3 pointer only).
- `tags`: `[]` when none (never null).
- **No `cost_usd`.**

### `TruncatedField`
When a single JSON field would exceed **2 MiB** decompressed and `?full=1` is not set:
```json
{ "_truncated": true, "_bytes": 3500000, "preview": "<first ~64KiB as UTF-8 / JSON value>" }
```
`preview` is a JSON value (object/array/string) snipped for display — not a guaranteed valid parse of the original. With `?full=1`, return the real value (may be large).

---

## Endpoints

### `GET /api/ui/projects`
Project cards for the home grid.

**Response** `200` — array (empty `[]` if none):
```json
[
  {
    "id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
    "name": "support",
    "trace_count_24h": 120,
    "trace_count_7d": 800,
    "error_rate_24h": 0.04,
    "p50_ms": 820,
    "p95_ms": 4100,
    "tokens_24h": 1800000
  }
]
```
- Latency percentiles and `error_rate_24h` are over **root runs** that started in the last 24h.
- `tokens_24h`: sum of `total_tokens` over all runs in those traces (or denormalized root totals — implementation must document which; prefer **sum over all runs in 24h window**).
- `p50_ms` / `p95_ms`: `null` when fewer than 1 finished root in window.
- Sorted by `name` ascending.
- **No `cost_usd_24h`.**

---

### `GET /api/ui/traces`
Summary rows only. Keyset pagination.

**Query params**

| Param | Type | Notes |
| --- | --- | --- |
| `project` | string | Session/project **name**. Omit = all. |
| `q` | string | ILIKE over root `name` and any run `error` in the trace. |
| `status` | string | `success` \| `error` \| `running` |
| `run_type` | string | Trace contains ≥1 run of this type. |
| `model` | string | Exact match on denormalized model (root or any LLM run — match list-row `model`). |
| `min_tokens` | int | Trace `tokens.total` ≥ N. |
| `min_duration_ms` | int | Trace `duration_ms` ≥ N (excludes still-running). |
| `from` / `to` | ISO | Filter on root `start_time` (inclusive / exclusive). |
| `sort` | string | `start_desc` (default) \| `duration_desc` \| `tokens_desc` \| `errors_desc` |
| `limit` | int | 1..200, default 50. |
| `cursor` | string | Opaque keyset from prior `next_cursor`. |

**Response** `200`:
```json
{
  "traces": [ /* TraceSummary */ ],
  "next_cursor": "…" 
}
```
`next_cursor` is `null` when no more pages.  
**No payloads** (no inputs/outputs/extra/events/serialized).

---

### `GET /api/ui/traces/{traceId}/summary`
Waterfall + tree data without payloads.

**Response** `200`:
```json
{
  "trace_id": "…",
  "span_ms": 37700,
  "summary": { /* TraceSummary */ },
  "rows": [ /* SpanSummary, sorted by dotted_order */ ]
}
```
`404` if unknown `traceId`.  
Budget target: ≲1.5 KB/row uncompressed.

---

### `GET /api/ui/runs/{id}`
Full run for the detail pane. Hydrates cold S3 payloads server-side.

**Query:** `full=1` — disable truncation.

**Response** `200`:
```json
{
  "id": "…",
  "parent_run_id": null,
  "trace_id": "…",
  "session_id": "…",
  "name": "root",
  "run_type": "chain",
  "node": "supervisor",
  "status": "success",
  "start_time": "…",
  "end_time": "…",
  "latency_ms": 37700,
  "self_time_ms": 1200,
  "tokens": { "input": 100, "output": 20, "total": 120 },
  "model": "…",
  "tags": [],
  "dotted_order": "…",
  "inputs": {},
  "outputs": {},
  "extra": {},
  "events": [],
  "serialized": null,
  "error": null,
  "attachments": [
    {
      "name": "img.png",
      "content_type": "image/png",
      "size": 12345,
      "url": "/api/ui/runs/{id}/attachments/img.png"
    }
  ],
  "cold": false,
  "truncated": false
}
```
- `inputs` / `outputs` / `extra` / `events` / `serialized` / `error`: each may be a real value **or** a `TruncatedField` object. `error` is normally a string or `null`.
- `cold`: `true` if any field was hydrated from S3 for this response.
- `truncated`: `true` if any field was replaced with `TruncatedField`.
- `attachments[].url`: same-origin path; browser sends `x-api-key` if required (or cookie later).
- `404` if unknown.

---

### `GET /api/ui/runs/{id}/messages`
Normalized chat view. Server sniffs shapes once.

**Detection order** (first hit wins):  
`inputs.messages` → `outputs.messages` → `outputs.generations[*][*].message` → `inputs.kwargs.input` (LC) → top-level array of role/type objects on inputs or outputs.

**Response** `200` always (never 204):
```json
{
  "messages": [
    {
      "id": "m0",
      "role": "user",
      "name": null,
      "content": [
        { "type": "text", "text": "…" }
      ],
      "tool_calls": null,
      "tool_call_id": null,
      "source": "openai"
    }
  ],
  "paired": [
    { "call_id": "call_abc", "call_msg": 0, "result_msg": 3 }
  ],
  "raw_path": "inputs.messages"
}
```

**Message fields**
- `role`: `"user"` | `"assistant"` | `"system"` | `"tool"` | `"function"`  
  Map: `human|user→user`, `ai|assistant→assistant`, `system→system`, `tool|function→tool`.
- `content`: array of parts:
  - `{ "type":"text", "text":"…" }`
  - `{ "type":"image_url", "url":"…" }`
  - `{ "type":"tool_use", "id":"…", "name":"…", "input":{} }`
  - `{ "type":"tool_result", "tool_use_id":"…", "content":"…" }`
  - unknown → `{ "type":"json", "value": <any> }`
- `tool_calls`: OpenAI-style `[{ "id","name","arguments":{} }]` or `null`. `arguments` is a parsed object when JSON; else `{ "_raw": "<string>" }`.
- `tool_call_id`: for `role=tool` results; else `null`.
- `source`: `"openai"` | `"langchain"` | `"anthropic"` | `"unknown"`.
- `id`: stable within response (`m0`, `m1`, …).

**`paired`:** links assistant tool calls to tool results by id. Indices into `messages`. Unpaired calls/results are simply absent from `paired` (UI flags them).

**Not message-shaped:** `{ "messages": [], "paired": [], "raw_path": null }` — UI falls back to JSON tabs from `GET /runs/:id`.

---

### `GET /api/ui/traces/stream` (SSE)
Live tail. `Content-Type: text/event-stream`.

**Query params:** same filters as `GET /api/ui/traces` (except `cursor` / `limit` / `sort`).  
**Last-Event-ID:** prior event id (opaque; server may use keyset cursor string).

**Events**

```
id: <cursor>
event: trace
data: { /* TraceSummary */ }

id: <cursor>
event: run_upsert
data: { "trace_id":"…", "row": { /* SpanSummary */ } }

event: heartbeat
data: {}
```

- `trace`: new root / new list row (prepend). Emitted after commit.
- `run_upsert`: optional; for an already-open waterfall. UI may ignore.
- `heartbeat`: every 15s; no `id` required.
- Target: new root visible <2s after multipart flush.

---

### `GET /api/ui/stats`
Rollups for a project (or all). **Tokens only — no cost.**

**Query:** `project` (name, optional), `from`, `to` (ISO; default last 24h if both omitted).

**Response** `200`:
```json
{
  "trace_count": 120,
  "error_rate": 0.04,
  "p50_ms": 820,
  "p95_ms": 4100,
  "tokens": { "input": 1000000, "output": 800000, "total": 1800000 },
  "by_day": [
    { "day": "2026-10-27", "traces": 40, "errors": 2, "tokens": 600000 }
  ],
  "by_model": [
    { "model": "gpt-4o-mini", "calls": 800, "tokens": 1500000 }
  ]
}
```
- `by_day[].day`: `YYYY-MM-DD` UTC.
- `by_model[].calls`: count of LLM runs with that model.
- `by_model` sorted by `tokens` desc; `by_day` ascending by day.
- **No `cost_usd` on the root or in buckets.**

---

### `GET /api/ui/runs/{id}/attachments/{name}` (M2 included)
Proxy attachment bytes from S3.  
Headers: `Content-Type` from meta; `Content-Length` when known.  
`404` if missing. No JSON body on success.

---

## Out of scope (do not implement under `/api/ui` in M2)

- Any `cost_usd*` field or price map endpoint (`/api/ui/models/prices` deferred).
- Compare-two-traces.
- Mutations (delete project, patch tags, etc.).
- LangSmith `/runs/query` — SDK only; UI must not call it.

---

## Errors

JSON: `{ "detail": "<string>" }` with 4xx/5xx.  
Unknown trace/run → `404`. Invalid cursor / params → `422`.

---

## Changes

_(none yet — shapes frozen 2026-10-28)_
