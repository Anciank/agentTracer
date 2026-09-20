"""A plain ReAct support agent -- the simple baseline.

One loop: agent -> tools -> agent until the model stops calling tools. For the
routed, parallel, self-verifying version of the same task, see
`support_graph.py`; both run against the identical tool set in
`support_tools.py`, so their traces are directly comparable.

    python -m langsmith_server --port 1984
    python examples/support_agent.py
"""

from __future__ import annotations

import os
import sys
from pathlib import Path
from typing import Annotated, TypedDict

sys.path.insert(0, str(Path(__file__).resolve().parent))

from _env import load_env  # noqa: E402

load_env()

from langchain_openai import ChatOpenAI  # noqa: E402
from langgraph.graph import START, StateGraph  # noqa: E402
from langgraph.graph.message import add_messages  # noqa: E402
from langgraph.prebuilt import ToolNode, tools_condition  # noqa: E402

from support_tools import TOOLS  # noqa: E402

OPENROUTER_BASE_URL = "https://openrouter.ai/api/v1"
DEFAULT_MODEL = "nvidia/nemotron-3-super-120b-a12b:free"

SYSTEM_PROMPT = (
    "You are a support operations assistant. Use the available tools to answer "
    "questions with real data -- never invent customer details, order numbers or "
    "policy. Look things up first, then act. If a tool fails, explain the failure "
    "plainly instead of retrying blindly. Finish with a short answer for the agent "
    "who will read it."
)


class AgentState(TypedDict):
    messages: Annotated[list, add_messages]


def is_free_model(model: str) -> bool:
    """OpenRouter names free variants with a `:free` suffix."""
    return model.endswith(":free") or model == "openrouter/free"


def resolve_credentials() -> tuple[str, str, str]:
    """Return (base_url, api_key, model), preferring the environment.

    The key is read from OPENROUTER_API_KEY so it never has to be written into
    the repo. `.env` is loaded with setdefault, so a real environment variable
    always wins over anything in the file.
    """
    base_url = os.environ.get("OPENAI_BASE_URL") or OPENROUTER_BASE_URL
    api_key = os.environ.get("OPENROUTER_API_KEY") or os.environ.get("OPENAI_API_KEY")
    model = os.environ.get("MODEL") or DEFAULT_MODEL

    if not api_key:
        raise SystemExit(
            "No API key found. Export OPENROUTER_API_KEY in your shell:\n"
            "    export OPENROUTER_API_KEY=sk-or-v1-...\n"
            "or put OPENROUTER_API_KEY=... in .env (which is gitignored)."
        )

    if "openrouter.ai" in base_url and not is_free_model(model):
        raise SystemExit(
            f"Refusing to run {model!r}: this project is configured for free models only.\n"
            "Choose one ending in ':free', or use 'openrouter/free'.\n"
            "Browse them at https://openrouter.ai/models?max_price=0"
        )

    return base_url, api_key, model


def build_graph(model: str, base_url: str, api_key: str, tools: list = TOOLS):
    llm = ChatOpenAI(model=model, base_url=base_url, api_key=api_key,
                     temperature=0, timeout=120, max_retries=4)
    llm_with_tools = llm.bind_tools(tools)

    def agent(state: AgentState) -> dict:
        return {"messages": [llm_with_tools.invoke(state["messages"])]}

    graph = StateGraph(AgentState)
    graph.add_node("agent", agent)
    graph.add_node("tools", ToolNode(tools))
    graph.add_edge(START, "agent")
    graph.add_conditional_edges("agent", tools_condition)
    graph.add_edge("tools", "agent")
    return graph.compile()


QUESTIONS = [
    "Customer C-1002 says their order arrived damaged. Look them up, find the order, "
    "and tell me what our policy says we should do.",
    "What is our return window, and what happens with damaged goods?",
    "Please refund order O-5003 in full.",
    "Refund order O-5001 for $5000.",
    "Escalate an urgent ticket for customer C-9999 about repeated billing failures.",
    "Email Ada Lovelace a summary of her open orders.",
]


def run_questions(app, questions: list[str]) -> None:
    """Invoke the graph per question, never letting one failure stop the run."""
    for i, question in enumerate(questions, 1):
        print(f"[{i}/{len(questions)}] {question}")
        try:
            result = app.invoke(
                {"messages": [{"role": "system", "content": SYSTEM_PROMPT},
                              {"role": "user", "content": question}]},
                config={"metadata": {"question_index": i}, "recursion_limit": 25},
            )
        except Exception as exc:  # noqa: BLE001 - a failed question still gets traced
            print(f"      -> FAILED: {type(exc).__name__}: {exc}\n")
            continue
        final = result["messages"][-1]
        print(f"      -> {' '.join(str(final.content or '').split())[:200]}\n")


def main() -> None:
    base_url, api_key, model = resolve_credentials()

    print(f"endpoint   : {base_url}")
    print(f"model      : {model}")
    print(f"tools      : {len(TOOLS)} ({', '.join(t.name for t in TOOLS)})")
    print(f"tracing to : {os.environ.get('LANGSMITH_ENDPOINT')}")
    print(f"project    : {os.environ.get('LANGSMITH_PROJECT')}\n")

    app = build_graph(model, base_url, api_key)
    run_questions(app, QUESTIONS)

    from support_tools import EMAILS, ORDERS, TICKETS

    print(f"side effects: {len(EMAILS)} email(s), {len(TICKETS)} ticket(s), "
          f"refunded={sum(o['refunded'] for o in ORDERS.values()):.2f}")

    # `langsmith.Client()` is not a singleton -- a fresh instance has its own
    # (empty) tracing queue, so `Client().flush()` returns immediately and
    # silently flushes nothing. This is the client the tracer itself uses.
    from langchain_core.tracers.langchain import get_client

    get_client().flush()
    print("traces flushed to the local server")


if __name__ == "__main__":
    main()
