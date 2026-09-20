# agentTracer

A **LangSmith-compatible tracing server that logs to a local SQLite file**. Point any
LangSmith SDK (LangGraph, LangChain, or the raw `RunTree` API) at it and every run lands on
disk — no cloud account, no API key, no external service to keep running.

Plus three things on top of it:

- `examples/support_agent.py` — a LangGraph ReAct agent with a realistic tool surface
- a **waterfall UI** at `http://127.0.0.1:1984/ui`, rendered from the local data
- `analysis.py` — a text report over the same trace store

## Why

LangSmith is the default tracing backend for LangGraph, but it's a hosted service. This
server implements the wire protocol the SDKs speak, so you get identical tracing behaviour
with local-only storage that you can query with plain SQL.

## Quick start

```bash
uv venv --python 3.12 .venv
uv pip install fastapi "uvicorn[standard]" pydantic python-multipart langsmith langgraph langchain-openai pytest

# 1. start the trace server
#    -> data/langsmith.db     traces, queryable
#    -> data/requests.log     every request + full payload
#    -> data/server.log       server logs
.venv/bin/python -m langsmith_server --port 1984 --log-file data/server.log

# 2. in another shell: run the agent (model comes from .env)
.venv/bin/python examples/support_agent.py

# 3. read back what happened -- text report, and the waterfall UI
.venv/bin/python -m langsmith_server.analysis --out data/report.txt
open http://127.0.0.1:1984/ui
```

## Models and credentials

The examples talk to **OpenRouter** and are restricted to **free models**. Nothing
but the base URL and a model id is configured in `.env` — the key is read from the
environment, so it never lands in the repo:

```bash
export OPENROUTER_API_KEY=sk-or-v1-...
```

or set `OPENROUTER_API_KEY=` in `.env` (gitignored). A real environment variable
always wins, because `.env` is loaded with `setdefault`.

`MODEL` must name a free model. `resolve_credentials()` refuses to start against
OpenRouter with anything else, so a stray paid model can't quietly spend money:

```
Refusing to run 'anthropic/claude-sonnet-4.6': this project is configured for
free models only.
```

**Free tiers are rate limited hard.** OpenRouter allows **50 free-model requests
per day** (resetting at 00:00 UTC); it returns `429 free-models-per-day` when you
run out, and individual providers return `502 provider_unavailable` under load.
Both are visible in the traces rather than hidden — worth knowing before you
blame your own code. Check your remaining quota any time:

```bash
curl -s https://openrouter.ai/api/v1/key -H "Authorization: Bearer $OPENROUTER_API_KEY"
# -> "free_model_daily_requests": {"used": 58, "limit": 50, "remaining": 0}
```

> **Watch out:** `.env` is loaded with `setdefault`, so a `MODEL` already exported
> in your shell **wins over the file**. If changing `MODEL` in `.env` appears to do
> nothing, run `echo $MODEL` — that is the value being used.

Free models that support tool calling — which this agent requires — can be listed
without a key, since the catalogue endpoint is public:

```bash
curl -s https://openrouter.ai/api/v1/models | .venv/bin/python -c "
import json,sys
for m in json.load(sys.stdin)['data']:
    p = m.get('pricing') or {}
    if p.get('prompt')=='0' and p.get('completion')=='0' and 'tools' in (m.get('supported_parameters') or []):
        print(f\"{m['id']:<56} ctx={m.get('context_length')}\")
"
```

## The example agents

Both examples run against the same domain (`support_tools.py`): customers, orders,
a knowledge base, tickets, and seven tools, each with real failure modes.

| Tool | Kind | Fails when |
| --- | --- | --- |
| `search_knowledge_base` | BM25 retrieval | never (returns an empty hint) |
| `get_customer` | lookup | unknown customer id |
| `list_orders` | lookup, filterable | unknown customer id |
| `get_order` | lookup | unknown order id |
| `issue_refund` | write | over-refund, double-refund, non-positive amount |
| `send_email` | slow write | malformed address |
| `escalate_ticket` | slow write | bad priority, unknown customer |

The point is the *mix*: retrieval that is genuinely CPU-bound, fast lookups,
slow writes that validate hard, and five distinct failure paths. Remote calls
are `time.sleep` stand-ins for network I/O — without them every tool span is a
sub-millisecond speck and the waterfall is unreadable.

### `support_agent.py` — the baseline

One ReAct loop: `agent -> tools -> agent` until the model stops calling tools.

### `support_graph.py` — supervisor + specialist subagents

The supervisor owns no tools. It reads the request, decides *who* to involve, and
delegates; every specialist reports back and the supervisor may delegate again
before finishing.

```
                     +--> policy_agent  --+
START -> supervisor --> orders_agent  --+--> supervisor ... -> finalize -> END
                     +--> action_agent  --+
```

- `policy_agent` reads policy (`search_knowledge_base`)
- `orders_agent` reads customer/order data (`get_customer`, `list_orders`, `get_order`)
- `action_agent` performs writes (`issue_refund`, `send_email`, `escalate_ticket`)

The tool surfaces are split read/write: the researching agents *cannot* change
anything, and the one that writes has to look up first. Two specialists can be
delegated to in one turn, which fans out in parallel.

> **Subagents are invoked from wrapper nodes, not registered directly.** Sharing
> the supervisor's `messages` channel leaks every specialist's tool chatter into
> the supervisor's context — which is exactly what makes a supervisor start
> mis-routing. Wrapping gives each specialist a fresh message list. Both
> approaches nest the subgraph's runs correctly in the trace, so the waterfall
> shows the full hierarchy either way.

A real captured trace: the supervisor delegated to `orders_agent` (five ReAct
rounds, four tool calls) *and* `policy_agent` in the same turn, then finished —
33 runs, 7,152 tokens, 37.7s.

## Generating volume

To exercise the store, the waterfall and the analysis at scale without spending
API calls:

```bash
python examples/loadgen.py --traces 5000 --seed 7
```

These traces are **synthesised**, not produced by a model. Nothing in them should
be read as a measurement of any model. What they *are* good for:

- real load through the real ingestion path (the official SDK's batch endpoint)
- realistic shapes — the same node names, tree structure, latency ranges and
  token growth as traces captured from a real agent
- enough breadth to catch bugs that small datasets hide (see below)

They land in their own `loadtest` project so they can never be confused with real
runs. Seeded, so a given `--seed` always produces the same data.

Measured on a laptop: **76,430 runs across 5,000 traces in 133s** (~577 runs/s),
a 175 MB SQLite file. At that size the analysis takes 2.7s, the trace list
endpoint 120ms for 100 traces / 420ms for 1,000, and a single trace detail 2ms.

> This generator paid for itself immediately: it exposed a bug where the trace
> list inherited `query_runs`' default `limit=100`, so requesting 100 traces
> returned summaries for only **6** of them. Small datasets never hit the cap.

## Pointing an existing app at it

The SDK only needs these environment variables:

```bash
export LANGSMITH_TRACING=true
export LANGSMITH_ENDPOINT=http://127.0.0.1:1984
export LANGSMITH_API_KEY=local          # value is ignored
export LANGSMITH_PROJECT=my-project
```

## API surface

Implemented against the behaviour of `langsmith` 0.12.x, read from the SDK source:

| Endpoint | Purpose |
| --- | --- |
| `GET /info` | version + batch ingest config the SDK reads at startup |
| `POST /runs/multipart` | **the path tracing actually uses** — multipart run ops |
| `POST /runs/batch` | JSON fallback: `{"post": [...], "patch": [...]}` |
| `POST /runs`, `PATCH /runs/{id}` | single-run create/update |
| `POST /runs/query`, `GET /runs`, `GET /runs/{id}` | querying (cursor-shaped response) |
| `GET/POST/PATCH/DELETE /sessions` | projects; auto-created from `session_name` |
| `GET /sessions/{id}/runs` | runs within a project |
| `POST/GET /feedback` | feedback records |
| `GET /runs/{id}/attachments/{name}` | raw attachment bytes |
| `GET /health` | row counts (this is a local server, so it's cheap and useful) |

### Two details worth knowing

**The multipart body is parsed by hand** (`ingest.py`), not by Starlette's form parser. The
SDK packs raw binary attachments into parts that carry no `filename`, which a standard form
parser hands back as a lossily-decoded `str` — silently corrupting the bytes.

**`error` is not always a JSON object.** The SDK sends it as a bare JSON *string* while
`inputs`/`outputs`/`extra` are objects, so fragmented parts are accepted as any JSON type.

Body compression (`Content-Encoding: zstd`) is opt-in in the SDK and handled if the
`zstandard` package is installed.

## Does LangGraph talk to LangSmith over HTTP?

Yes — plain HTTP(S), no gRPC, no websocket. The transports differ but LangGraph
(the `LangGraph` chain run, the `agent` node, each `ChatOpenAI` call) is just a
client of the same HTTP API. The processes are typically different machines.

Two calls are made, on two different triggers:

| Call | When | Why |
| --- | --- | --- |
| `GET /info` | once per `Client()` construction | reads `version` + `batch_ingest_config` to decide feature paths |
| `POST /runs/multipart` | on the background tracing worker, in batches | ships run creates/updates |

Everything else (`/sessions`, `/runs/query`, `/feedback`) exists for the
evaluation and read APIs, not for tracing. The project is **not** created with a
`POST /sessions` — the run payload carries `session_name`, and the server
resolves (or creates) the project from it.

A real recording of one example run, from `data/requests.log`:

```
  #  time          method + path           status  parts  req bytes
  1  GET  /health              200         -          0      (curl)
  2  GET  /info                200         -          0      <- client init
  3  POST /runs/multipart      204        16       8756      |
  4  POST /runs/multipart      204        36      28686      |
  ...                                                        } tracing worker
 10  POST /runs/multipart      204        36      35039      |
 12  POST /runs/multipart      204        32      31543      <- final flush
```

38 runs arrive in 9 requests — the tracer batches at the *operation* level, so
"parts" outnumber runs (each run contributes ~8 parts: the main object plus
`inputs`, `outputs`, `extra`, `events`, `serialized`, …).

> Gotcha: `langsmith.Client()` is **not** a singleton, and each instance owns its
> own tracing queue. A fresh `Client().flush()` therefore returns instantly and
> flushes nothing. Flush the client the tracer actually uses —
> `langchain_core.tracers.langchain.get_client().flush()`.

## Request log

Every request, with its full payload and response, is appended to
`data/requests.log`:

```
==============================================================================
2026-09-16T11:49:39.376958+00:00  POST /runs/multipart  ->  204  (7.2 ms)
content-type: multipart/form-data; boundary=3af793c1...
user-agent:   langsmith-py/0.12.5-httpx2
--- REQUEST (8756 bytes) ---
16 part(s)
[part] post.01a0aa0d-...  (application/json, 297 bytes)
{
  "id": "01a0aa0d-...",
  "name": "LangGraph",
  "session_name": "simple-graph",
  ...
}
[part] post.01a0aa0d-....inputs  (application/json, 222 bytes)
{ "messages": [ ... ] }
[part] attachment.01a0aa0d-....shot  (image/png, 2048 bytes)
  <binary omitted>
--- RESPONSE (0 bytes) ---
<empty>
```

Multipart bodies are decoded into named parts with their JSON pretty-printed, so
the log is readable rather than a wall of boundary noise. Binary attachments show
their size and type instead of being inlined into a text file. Bodies are
truncated at 200 KB by default.

```bash
--request-log path      # default data/requests.log; "" disables it
LANGSMITH_LOCAL_REQLOG= # same, via environment
--log-file path         # server/stdout logs, separate from the request transcript
```

## Waterfall UI

`http://127.0.0.1:1984/ui` — a single self-contained HTML page (no build step, no
npm, no CDN) served by the same server, reading the same SQLite file through a
small read-only JSON API.

- **Trace list** on the left, newest first, with duration, run count, token total
  and an error badge. Filter by project.
- **Waterfall** on the right: one row per span, positioned by offset and sized by
  latency, indented by tree depth, coloured by run type. Error spans are red;
  unfinished ones are striped. The ruler scales to the trace.
- **Detail panel** on click: status, timing, self time, tokens, model, graph node,
  then collapsible Inputs / Outputs / Metadata / Error / Raw sections with
  syntax-highlighted JSON.
- Hover for the row label, `↑`/`↓` to walk spans, and the selection is written to
  the URL hash so a view is linkable.

Backing endpoints (ours, not part of the LangSmith API):

| Endpoint | Purpose |
| --- | --- |
| `GET /ui` | the page |
| `GET /ui/projects` | project names for the filter |
| `GET /ui/traces?session=&limit=` | trace summaries (no run payloads) |
| `GET /ui/traces/{trace_id}` | positioned rows + full payloads for one trace |

The split matters: the list endpoint never loads run payloads, and the detail
endpoint loads exactly one trace, so the payload stays bounded no matter how many
traces are stored.

**Self time** — a run's latency minus the sum of its direct children. It separates
genuine work from orchestration waiting. A root that spends 9.3 ms of a 21.9 s
trace of its own is a graph waiting on its model, not a slow graph.

## Storage

One SQLite file (`data/langsmith.db`), four tables:

- `sessions` — projects
- `runs` — promoted columns for querying (`trace_id`, `parent_run_id`, `run_type`, `name`,
  `start_time`, `end_time`, `status`, `error`, `dotted_order`) **plus** a lossless `data`
  JSON column holding the complete merged run payload
- `feedback`
- `attachments` — raw bytes + content type
- `ingest_log` — append-only record of what arrived, for debugging ingestion

`post` and `patch` operations merge: a `null` in a patch never erases a previously stored
value, and nested `extra`/`attachments` objects merge key-by-key rather than being replaced.
The client-side fragment merging LangSmith does is therefore reproducible server-side.

## Analysis

```bash
.venv/bin/python -m langsmith_server.analysis                  # text report
.venv/bin/python -m langsmith_server.analysis --trace <id>     # one trace
.venv/bin/python -m langsmith_server.analysis --session my-project --json
.venv/bin/python -m langsmith_server.analysis --max-traces 0   # every tree
```

Reports run counts by type, latency per node, token usage per model, tool call and
error rates, slowest runs, and a rendered tree per trace.

Two things keep it readable as the store grows:

- **Trace trees are capped** at the 25 most recent (`--max-traces`, `0` for all).
  Rendering all 5,000 traces produced a 4 MB report.
- **Identical failures are grouped with counts** rather than printed one per line.
  At 6,659 errors, listing them individually is noise; `209x chain agent: refund of
  5000.00 exceeds refundable amount...` is the signal.

## Tests

```bash
.venv/bin/python -m pytest tests/ -q
```

The multipart fixtures reproduce `requests_toolbelt.MultipartEncoder`'s exact byte layout,
so the parser is tested against the real wire format rather than a convenient approximation.
