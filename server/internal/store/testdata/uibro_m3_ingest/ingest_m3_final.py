#!/usr/bin/env python3
import os, uuid, json
from datetime import datetime, timezone, timedelta
from langsmith import Client

ENDPOINT = os.environ["LANGSMITH_ENDPOINT"]
API_KEY = os.environ["LANGSMITH_API_KEY"]
PROJECT = os.environ.get("LANGSMITH_PROJECT", "m3-final")
client = Client(api_url=ENDPOINT, api_key=API_KEY)

def uid():
    return str(uuid.uuid4())

def dotted_part(dt: datetime, rid: str) -> str:
    # LangSmith-ish: 20261028T074200000000Z + first 8 of uuid without dashes sometimes;
    # use compact UTC + 8 hex of id
    stamp = dt.strftime("%Y%m%dT%H%M%S%f") + "Z"
    return stamp + rid.replace("-", "")[:8]

t0 = datetime.now(timezone.utc)
def T(ms: int) -> datetime:
    return t0 + timedelta(milliseconds=ms)

ids = {}

# --- Nested support chain ---
root, llm1, tool, llm2 = uid(), uid(), uid(), uid()
ids.update(nested_root=root, llm1=llm1, tool=tool, llm2=llm2)

client.create_run(
    id=root, name="m3_support_chain", run_type="chain",
    inputs={"question": "Where is order ORD-42?"},
    project_name=PROJECT, start_time=T(0),
    dotted_order=dotted_part(T(0), root),
)

# Child with EARLIER start_time than root (out-of-order clock)
client.create_run(
    id=llm1, name="ChatOpenAI", run_type="llm",
    parent_run_id=root, trace_id=root,
    inputs={"messages": [
        {"role": "system", "content": "You are a support agent with tools."},
        {"role": "user", "content": "Where is order ORD-42?"},
    ]},
    project_name=PROJECT,
    start_time=T(-5),
    extra={"metadata": {"ls_model_name": "gpt-4o-mini"}},
    dotted_order=dotted_part(T(0), root) + "." + dotted_part(T(-5), llm1),
)
client.update_run(
    llm1,
    outputs={
        "generations": [[{
            "message": {
                "role": "assistant",
                "content": "",
                "tool_calls": [{
                    "id": "call_lookup",
                    "type": "function",
                    "function": {"name": "lookup_order", "arguments": "{\"order_id\":\"ORD-42\"}"},
                }],
                "usage_metadata": {
                    "input_tokens": 128,
                    "output_tokens": 42,
                    "total_tokens": 170,
                },
            },
        }]],
        "llm_output": {
            "model_name": "gpt-4o-mini",
            "token_usage": {
                "prompt_tokens": 128,
                "completion_tokens": 42,
                "total_tokens": 170,
            },
        },
    },
    end_time=T(47),
)

client.create_run(
    id=tool, name="lookup_order", run_type="tool",
    parent_run_id=root, trace_id=root,
    inputs={"order_id": "ORD-42"},
    project_name=PROJECT, start_time=T(50),
    dotted_order=dotted_part(T(0), root) + "." + dotted_part(T(50), tool),
)
client.update_run(tool, outputs={"status": "shipped", "eta": "2026-10-30"}, end_time=T(68))

client.create_run(
    id=llm2, name="ChatOpenAI", run_type="llm",
    parent_run_id=root, trace_id=root,
    inputs={"messages": [
        {"role": "user", "content": "Where is order ORD-42?"},
        {"role": "assistant", "content": "", "tool_calls": [
            {"id": "call_lookup", "type": "function",
             "function": {"name": "lookup_order", "arguments": "{\"order_id\":\"ORD-42\"}"}}
        ]},
        {"role": "tool", "tool_call_id": "call_lookup", "content": "{\"status\":\"shipped\"}"},
    ]},
    project_name=PROJECT, start_time=T(70),
    extra={"metadata": {"ls_model_name": "gpt-4o-mini"}},
    dotted_order=dotted_part(T(0), root) + "." + dotted_part(T(70), llm2),
)
client.update_run(
    llm2,
    outputs={
        "generations": [[{
            "message": {
                "role": "assistant",
                "content": "Order ORD-42 has shipped; ETA 2026-10-30.",
                "usage_metadata": {
                    "input_tokens": 210,
                    "output_tokens": 28,
                    "total_tokens": 238,
                },
            }
        }]],
        "llm_output": {
            "model_name": "gpt-4o-mini",
            "token_usage": {"prompt_tokens": 210, "completion_tokens": 28, "total_tokens": 238},
        },
    },
    end_time=T(95),
)
client.update_run(root, outputs={"answer": "Order ORD-42 has shipped; ETA 2026-10-30."}, end_time=T(100))

# --- Explicit child-before-parent create order ---
oo_child, oo_root = uid(), uid()
ids.update(oo_child=oo_child, oo_root=oo_root)
# Post CHILD first (parent does not exist yet)
client.create_run(
    id=oo_child, name="early_child_llm", run_type="llm",
    parent_run_id=oo_root,
    inputs={"messages": [{"role": "user", "content": "ping"}]},
    outputs={
        "generations": [[{
            "message": {
                "role": "assistant", "content": "pong",
                "usage_metadata": {"input_tokens": 5, "output_tokens": 3, "total_tokens": 8},
            }
        }]],
        "llm_output": {"token_usage": {"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8}},
    },
    project_name=PROJECT,
    start_time=T(200),
    end_time=T(220),
    dotted_order=dotted_part(T(250), oo_root) + "." + dotted_part(T(200), oo_child),
)
client.create_run(
    id=oo_root, name="oo_parent_chain", run_type="chain",
    inputs={"q": "ping"},
    outputs={"a": "pong"},
    project_name=PROJECT,
    start_time=T(250),
    end_time=T(260),
    dotted_order=dotted_part(T(250), oo_root),
)

# --- Error ---
err, err_child = uid(), uid()
ids.update(err=err, err_child=err_child)
client.create_run(
    id=err, name="m3_error_chain", run_type="chain",
    inputs={"ticket": "T-9"},
    project_name=PROJECT, start_time=T(300),
    dotted_order=dotted_part(T(300), err),
)
client.create_run(
    id=err_child, name="close_ticket", run_type="tool",
    parent_run_id=err, trace_id=err,
    inputs={"id": "T-9"},
    project_name=PROJECT, start_time=T(305),
    dotted_order=dotted_part(T(300), err) + "." + dotted_part(T(305), err_child),
)
client.update_run(err_child, error="ticket already closed", end_time=T(320))
client.update_run(err, error="ticket already closed", end_time=T(325))

print(json.dumps(ids, indent=2))
