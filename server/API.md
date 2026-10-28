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
  "path_fingerprint": "supervisor>tools×3>respond",
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
- `path_fingerprint`: collapsed path identity on **roots** when the trace has ended (e.g. `supervisor>tools×3>respond`); `null` on unfinished roots and non-root runs.
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

- `trace`: TraceSummary for list prepend/refresh. Emitted after commit when a root is created or a root/trace rollup changes.
- `run_upsert`: **required**. Emitted for **every** run on create (`POST /runs`, batch, or multipart post) and on **every** `PATCH` (including end_time, status, and token updates). Payload is `{ "trace_id", "row": SpanSummary }` with the **post-merge** run so an open waterfall updates live. Relative fields (`depth`, `offset_ms`) are filled when cheap from current siblings; they may be briefly stale until the trace ends.
- `heartbeat`: every 15s; no `id` required.
- Target: new root visible <2s after multipart flush; `run_upsert` ASAP after each persist.

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


### `GET /api/ui/projects/{id}/graph` (M3.5)

Aggregate Agent map for a project. `{id}` is a session **UUID or name** (via `LookupSessionID`; unknown → `404`).

**Query:**
| Param | Default | Notes |
|---|---|---|
| `window` | `24h` | `1h` \| `24h` \| `7d` (or omit and pass `from`+`to`) |
| `from`, `to` | — | RFC3339; when both set, overrides preset |
| `top_n` | `15` | Max named nodes; failing nodes always kept |
| `min_edge_share` | `0.01` | Edges below → `edges_hidden` |
| `compare` | — | `1` adds previous equal-window deltas |

**Response:** `{ project, window:{from,to,preset}, trace_count, nodes[], edges[], edges_hidden[], generated_at, compare? }`  
Node: `{ key, label, run_type, kind, count, error_count, error_rate, p50_ms, p95_ms, tokens:{input,output,total}, truncated }`  
`__other__` also includes `rolled_keys: string[]` (keys rolled into the bucket).  
Edge: `{ from, to, count, share, avg_ms, p95_ms, error_count, error_rate, loop, repeat_avg, repeat_max }`  
`kind` is `langgraph` \| `run` \| `other`. Truncated tail rolls into `__other__`.

### `GET /api/ui/projects/{id}/paths` (M3.5)

Top path fingerprints for a project window. `{id}` = session UUID or name (`404` unknown).

**Query:** `window` / `from`+`to` (same as graph), `limit` (default 20).

**Response:** `{ project, window, paths:[{ fingerprint, label, count, share, error_rate, p95_ms, fail_nodes, is_new, steps:[{key,repeat}] }] }`  
- `fail_nodes`: distinct node keys that errored on traces with this fingerprint (sorted).  
- `is_new`: fingerprint absent from the previous equal-length window.  
- `p95_ms`: root latency p95 for finished roots on this fingerprint; `null` if none finished.

Fingerprint is the collapsed sequence string (e.g. `supervisor>tools×3>respond`), written on the root when `end_time` is set and refreshed on later child upserts in a finished trace.

### `GET /api/ui/projects/{id}/monitor` (M3.6)

Project Monitor board. `{id}` = session UUID or name (`404` unknown). **Not** an alias of `/api/ui/stats` (stats unchanged).

**Query:** `window=1h|24h|7d` (default `24h`); optional `from`+`to` ISO override (both required to win); optional `limit` (by_model/by_tool cap, default 50).

**Buckets (fixed, zero-filled):** `1h`→1m×60; `24h`→1h×24; `7d`→6h×28. Half-open `[t,t_end)`.

**Headers:** `ETag` (hash of `project_id|window|from|to|trace_count|error_count|last_bucket`); `Cache-Control: private, max-age=0, must-revalidate`. `If-None-Match` → `304`.

**Response `200`:**
```json
{
  "project_id": "…", "project": "support", "window": "24h",
  "from": "…", "to": "…", "bucket": "1h",
  "trace_count": 10000, "success_count": 9400, "error_count": 600,
  "p50_ms": 420, "p99_ms": 3200,
  "tokens": { "input": 0, "output": 0, "total": 0 },
  "cost": null,
  "series_traces": [{ "t": "…", "t_end": "…", "success": 0, "error": 0, "total": 0 }],
  "series_latency": [{ "t": "…", "t_end": "…", "p50_ms": null, "p99_ms": null }],
  "series_tokens": [{ "t": "…", "t_end": "…", "input": 0, "output": 0, "total": 0 }],
  "by_model": [{ "model": "gpt-4o-mini", "calls": 1, "share": 1.0,
    "error_count": 0, "error_rate": 0, "p50_ms": 10, "p99_ms": 10,
    "tokens": { "input": 1, "output": 1, "total": 2 } }],
  "by_tool": [{ "tool": "refund", "calls": 1, "share": 1.0,
    "error_count": 0, "error_rate": 0, "p50_ms": 5, "p99_ms": 5, "tokens": null }]
}
```
- Tokens series: sum of **all runs** in traces whose root `start_time` falls in the bucket.  
- `by_model`: `run_type=llm`, null/empty model → `"(unknown)"`.  
- `by_tool`: `run_type=tool` by run `name`; `tokens` null when tools carry no token counts.  
- `cost` always `null` (M6 reserved). Migration `007_monitor_indexes.sql` is idempotent.

### Trace list filters (M3.5 + M3.6)

`GET /api/ui/traces` accepts (compose with existing filters + keyset cursor):
| Param | Meaning |
|---|---|
| `node` | Exact `node_key` visit in the trace |
| `node_fail` | Exact `node_key` with an error visit |
| `edge` | `from>to` transition present |
| `path` | Exact `path_fingerprint` on the root |
| `model` | Exact match on a run `model` in the trace (existing) |
| `tool` | **M3.6** Exact match on a `run_type=tool` run `name` in the trace |

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


### LangGraph denorm (M3.5 prep)

At ingest (create and PATCH), runs denormalize:
- `langgraph_node` / `langgraph_step` from `extra.metadata.langgraph_*`
- Fallback: if `langgraph_node` is missing and the run is a **direct child of a LangGraph root**, use the run `name`.
- LangGraph root heuristic: root `name == "LangGraph"`, or root `extra.metadata` has a `langgraph_*` / checkpoint key, or `serialized.id`/`name` contains `"langgraph"`.
- `path_fingerprint` is written on finished roots (and refreshed on late child upserts in finished traces).

## Changes

_(2026-10-28)_ Ingest: UTF-8-safe preview + NUL strip; PersistBatch degraded retry + `GET /info` `ingest_stats` (multipart stays 204).
_(2026-10-28)_ M3.6 Monitor `GET /api/ui/projects/{id}/monitor` + `tool=` on traces; `__other__.rolled_keys`; paths `fail_nodes`/`is_new`/`p95_ms`; edge `error_rate`.
_(2026-10-28)_ LangGraph denorm columns (`langgraph_node`/`langgraph_step`/`path_fingerprint`) + ingest extraction/fallback; index `(session_id, langgraph_node, start_time)`.
_(2026-10-28)_ SSE `run_upsert` is mandatory on every run create/PATCH (post-merge SpanSummary); relative depth/offset_ms may be briefly stale.

---

## Ingest notes (M2 wire)

### `trace_id` derivation
On every ingest path (`POST /runs`, `/runs/batch`, `/runs/multipart`) and on `PATCH`, if `trace_id` is missing **or** equals the run's own `id` while `parent_run_id` is set, the server derives `trace_id` from `dotted_order`: segments are `.`-joined `<YYYYMMDDTHHMMSSffffffZ><uuid>`; the first segment's trailing UUID (dashed or 32-hex undashed) is the root run id (= trace id). Some clients append only the undashed first 8 hex of the root id; those short suffixes are matched against `parent_run_id` when the parent is the root (child-before-parent ingest). If `dotted_order` yields nothing, fall back to the parent's stored `trace_id` via `GetRun`. Roots without either keep their own id.

### Token denormalization priority
`input_tokens` / `output_tokens` / `total_tokens` (and `model`) are filled in this order — first non-empty wins; nested sources are read before denormalized columns so a PATCH with new usage overwrites stale columns:

1. `outputs.llm_output.token_usage` (`prompt_tokens` / `completion_tokens` / `total_tokens`)
2. `outputs.generations[*][*].message(.kwargs).usage_metadata` and flat `generations[*].message(.kwargs).usage_metadata` (`input_tokens` / `output_tokens` / `total_tokens`)
3. `extra.metadata.usage_metadata`
4. Existing digs under `extra` / `outputs` / `inputs` (`usage`, `token_usage`, `usage_metadata`, bare token fields, `invocation_params` for model)
5. Top-level `input_tokens` / `output_tokens` / `total_tokens` / `model` columns

`total_tokens` is computed as `input + output` when still missing.

### PersistBatch partial failure (multipart still 204)
`POST /runs/multipart` always returns empty **204** (LangSmith SDK parity — retrying a poison run would loop). Per-run `UpsertRun` failures are:
1. Logged (`run_id`, `trace_id`, `op`, error; no payload / secrets).
2. Counted on the process.
3. Retried **once** with bulky fields dropped (`inputs`/`outputs`/`events`/`extra`/`serialized`/`attachments`) so `end_time` / `status` / linkage still land.

Counters are exposed on LangSmith-compat **`GET /info`** as:
```json
"ingest_stats": {
  "persist_errors": 0,
  "degraded_retries_ok": 0,
  "degraded_retries_failed": 0
}
```
`POST /runs/batch` still returns `202` with `{written, errors}` (unchanged). Previews and JSON responses truncate with a shared UTF-8-safe helper; NULs (`\u0000`) are stripped from text/jsonb-bound fields before Postgres write.

---
