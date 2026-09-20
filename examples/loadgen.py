"""Generate a large volume of realistic traces into the local server.

These traces are SYNTHESISED, not produced by a model. They exist so the store,
the waterfall UI and the analysis can be exercised at scale without burning API
calls. They are written through the real LangSmith SDK batch endpoint, so the
ingestion path under test is the genuine one -- but the payloads are invented,
and they land in their own project (`loadtest`) so they never get mixed up with
real agent runs.

Shapes, latencies and token growth are modelled on traces captured from a real
LangGraph ReAct agent (see examples/support_agent.py). It still is not the real
thing, and nothing here should be read as a measurement of any model.

    python -m langsmith_server --port 1984
    python examples/loadgen.py --traces 1000 --seed 7
"""

from __future__ import annotations

import argparse
import os
import random
import statistics
import sys
import time as time_mod
import uuid
from datetime import datetime, timedelta, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from _env import load_env  # noqa: E402

load_env()

from langsmith import Client  # noqa: E402

PROJECT = "loadtest"
MODEL = "synthetic:free"

# Latency ranges in seconds, modelled on captured support-agent traces.
TOOL_LATENCY = {
    "search_knowledge_base": (0.0006, 0.004),
    "get_customer": (0.030, 0.036),
    "list_orders": (0.040, 0.048),
    "get_order": (0.030, 0.036),
    "issue_refund": (0.240, 0.270),
    "send_email": (0.170, 0.190),
    "escalate_ticket": (0.075, 0.085),
}
TOOL_ERRORS = {
    "get_customer": "customer 'C-9{n:03d}' not found; check the id format (C-nnnn) or ask the customer",
    "list_orders": "customer 'C-9{n:03d}' not found",
    "get_order": "order 'O-9{n:03d}' not found",
    "issue_refund": "refund of 5000.00 exceeds refundable amount 128.50 for order O-5001",
    "send_email": "'not-an-email' is not a valid email address",
    "escalate_ticket": "priority 'critical' is invalid; expected one of ['high', 'low', 'normal', 'urgent']",
}

# (scenario name, user question, tool the agent reaches for each turn).
# `None` means that turn answers without calling a tool.
SCENARIOS: list[tuple[str, str, list[str | None]]] = [
    ("kb_lookup", "What is our return window for damaged goods?",
     ["search_knowledge_base", None]),
    ("order_status", "Where is order O-5004 for customer C-1002?",
     ["get_customer", "list_orders", None]),
    ("refund_flow", "Please refund order O-5003 in full.",
     ["get_order", "issue_refund", None]),
    ("damaged_goods", "Customer C-1002 says their order arrived damaged. What should we do?",
     ["get_customer", "list_orders", "search_knowledge_base", None]),
    ("escalation", "Escalate an urgent billing ticket for customer C-1001.",
     ["get_customer", "escalate_ticket", None]),
    ("failed_lookup", "Look up customer C-9999 and tell me their tier.",
     ["get_customer", "get_customer", None]),
    ("email_customer", "Email Ada Lovelace a summary of her open orders.",
     ["get_customer", "list_orders", "send_email", None]),
    ("multi_hop", "Refund the damaged monitor order after checking our policy.",
     ["search_knowledge_base", "get_order", "search_knowledge_base",
      "issue_refund", None]),
]
# Failure paths are deliberately over-represented so error reporting has data.
SCENARIO_WEIGHTS = [12, 18, 12, 14, 8, 9, 10, 6]

# Tool latency for the simulated model call, in seconds.
LLM_LATENCY = (1.2, 6.5)


def segment(ts: datetime, run_id: str) -> str:
    """One dotted_order component: fixed-width UTC stamp + the run id."""
    return ts.strftime("%Y%m%dT%H%M%S%f") + "Z" + run_id


class TraceBuilder:
    """Assembles one trace, tracking the clock and the dotted order tree."""

    def __init__(self, rng: random.Random, scenario: str) -> None:
        self.rng = rng
        self.scenario = scenario
        self.trace_id = str(uuid.uuid4())
        self.runs: list[dict] = []

    def add(
        self,
        name: str,
        run_type: str,
        start: datetime,
        duration: float,
        *,
        parent: dict | None,
        inputs: dict | None = None,
        outputs: dict | None = None,
        error: str | None = None,
        extra: dict | None = None,
        serialized: dict | None = None,
    ) -> dict:
        run_id = str(uuid.uuid4())
        dotted = segment(start, run_id)
        if parent is not None:
            dotted = parent["dotted_order"] + "." + dotted
        run: dict = {
            "id": run_id,
            "trace_id": self.trace_id,
            "parent_run_id": parent["id"] if parent else None,
            "name": name,
            "run_type": run_type,
            "start_time": start.isoformat(),
            "end_time": (start + timedelta(seconds=duration)).isoformat(),
            "dotted_order": dotted,
            "session_name": PROJECT,
            "tags": [],
            "inputs": inputs or {},
            "outputs": outputs or {},
            "extra": extra or {},
        }
        if error:
            run["error"] = error
        if serialized:
            run["serialized"] = serialized
        self.runs.append(run)
        return run

    def build(self, turns: list[str | None], start: datetime) -> list[dict]:
        """Emit every run in the trace, root first, and return them all."""
        rng = self.rng
        question = self.scenario
        # First turn carries the system prompt plus every tool schema; each
        # later turn resends the whole message list, so this grows fast.
        prompt_tokens = rng.randint(900, 1050)
        failed = False

        root = self.add("LangGraph", "chain", start, 0.0, parent=None,
                        inputs={"messages": [{"role": "user", "content": question}]},
                        extra={"metadata": {"ls_integration": "langgraph"}})

        cursor = start
        for turn in turns:
            agent_start = cursor
            agent = self.add("agent", "chain", agent_start, 0.0, parent=root,
                             inputs={"messages": [{"role": "user", "content": question}]})

            llm_dur = rng.uniform(*LLM_LATENCY)
            completion = rng.randint(28, 90)
            usage = {"input_tokens": prompt_tokens, "output_tokens": completion,
                     "total_tokens": prompt_tokens + completion}
            self.add(
                "ChatOpenAI", "llm",
                agent_start + timedelta(milliseconds=rng.uniform(20, 120)), llm_dur,
                parent=agent,
                inputs={"messages": [{"role": "user", "content": question}]},
                outputs={
                    "generations": [[{"text": "..."}]],
                    "llm_output": {"token_usage": {
                        "prompt_tokens": prompt_tokens,
                        "completion_tokens": completion,
                        "total_tokens": prompt_tokens + completion}},
                },
                serialized={"lc": 1, "type": "constructor",
                            "id": ["langchain", "chat_models", "openai", "ChatOpenAI"],
                            "kwargs": {"model_name": MODEL, "temperature": 0.0}},
                extra={"metadata": {
                    "ls_provider": "openai", "ls_model_name": MODEL,
                    "ls_model_type": "chat", "usage_metadata": usage,
                    "langgraph_node": "agent"}},
            )

            cursor = agent_start + timedelta(seconds=llm_dur)
            self.add("tools_condition", "chain", cursor, rng.uniform(0.0002, 0.002),
                     parent=agent,
                     outputs={"next": "tools" if turn else "__end__"})
            agent["end_time"] = cursor.isoformat()

            if turn is None:
                break

            tools_start = cursor + timedelta(milliseconds=rng.uniform(0.2, 2))
            tools_node = self.add("tools", "chain", tools_start,
                                  rng.uniform(0.0005, 0.003), parent=root)

            lo, hi = TOOL_LATENCY[turn]
            tool_dur = rng.uniform(lo, hi)
            # Only tools that actually have a failure mode may fail --
            # search_knowledge_base, for one, never raises.
            is_error = (
                not failed
                and turn in TOOL_ERRORS
                and rng.random() < (0.16 if turn == "get_customer" else 0.05)
            )
            failed = failed or is_error
            self.add(
                turn, "tool",
                tools_start + timedelta(milliseconds=rng.uniform(0.05, 0.4)), tool_dur,
                parent=tools_node,
                inputs=({"query": question} if turn == "search_knowledge_base"
                        else {"id": "C-1002"}),
                outputs={} if is_error else {"output": {"content": "{...}"}},
                error=TOOL_ERRORS[turn].format(n=rng.randint(1, 999)) if is_error else None,
            )
            cursor = tools_start + timedelta(seconds=tool_dur + 0.001)
            agent["end_time"] = cursor.isoformat()
            prompt_tokens += completion + rng.randint(110, 640)

        root["end_time"] = cursor.isoformat()
        root["outputs"] = {"messages": [{"role": "assistant", "content": "done"}]}
        if failed:
            # LangGraph propagates a tool failure up through the node and root.
            message = next(r["error"] for r in self.runs if r.get("error"))
            for run in self.runs:
                if run is not root and run["name"] in ("tools", "agent"):
                    run["error"] = message
            root["error"] = message
        return self.runs


def build_traces(rng: random.Random, count: int, now: datetime,
                 spread: timedelta) -> list[dict]:
    runs: list[dict] = []
    for _ in range(count):
        _, question, turns = rng.choices(
            SCENARIOS, weights=SCENARIO_WEIGHTS, k=1)[0]
        started = now - timedelta(seconds=rng.uniform(0, spread.total_seconds()))
        runs.extend(TraceBuilder(rng, question).build(turns, started))
    return runs


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Generate synthetic traces into the local LangSmith server.")
    parser.add_argument("--traces", type=int, default=1000)
    parser.add_argument("--seed", type=int, default=7)
    parser.add_argument("--batch", type=int, default=400,
                        help="max runs per HTTP request")
    parser.add_argument("--spread-hours", type=float, default=6.0,
                        help="scatter trace start times over this window")
    parser.add_argument("--endpoint",
                        default=os.environ.get("LANGSMITH_ENDPOINT",
                                               "http://127.0.0.1:1984"))
    args = parser.parse_args()

    client = Client(api_url=args.endpoint, api_key="local")
    rng = random.Random(args.seed)
    now = datetime.now(timezone.utc)
    spread = timedelta(hours=args.spread_hours)

    print(f"endpoint : {args.endpoint}")
    print(f"project  : {PROJECT}  (synthetic -- kept separate from real runs)")
    print(f"plan     : {args.traces:,} synthetic traces, seed={args.seed}\n")

    t0 = time_mod.perf_counter()
    total_runs = 0
    done = 0
    while done < args.traces:
        chunk = min(args.traces - done, 25)
        runs = build_traces(rng, chunk, now, spread)
        for i in range(0, len(runs), args.batch):
            client.batch_ingest_runs(create=runs[i : i + args.batch])
        total_runs += len(runs)
        done += chunk
        if done % 250 == 0 or done == args.traces:
            elapsed = time_mod.perf_counter() - t0
            print(f"  {done:>6,}/{args.traces:,} traces  {total_runs:>8,} runs  "
                  f"{elapsed:6.1f}s  {done / max(elapsed, 1e-9):,.0f} traces/s")

    elapsed = time_mod.perf_counter() - t0
    print(f"\nwrote {total_runs:,} runs in {elapsed:.1f}s "
          f"({total_runs / max(elapsed, 1e-9):,.0f} runs/s)")


if __name__ == "__main__":
    main()
