"""A supervisor coordinating three specialist subagents.

The supervisor never touches a tool itself. It reads the request, decides which
specialists to involve, and delegates via routing calls. Each specialist is its
own compiled ReAct subgraph with a deliberately narrow tool surface, and every
specialist reports back to the supervisor, which may delegate again before
finishing.

                       +--> policy_agent  --+
    START --> supervisor --> orders_agent --+--> supervisor ... --> finalize --> END
                       +--> action_agent  --+
                       +--> FINISH

    policy_agent  reads company policy            (search_knowledge_base)
    orders_agent  looks up customers and orders   (get_customer/list_orders/get_order)
    action_agent  performs writes                 (issue_refund/send_email/escalate_ticket)

Two specialists can be delegated to in one turn, which fans out in parallel.

Subagents are invoked from wrapper nodes rather than registered directly, so
each one gets a *fresh* message list. Sharing the supervisor's `messages`
channel would leak every specialist's tool chatter into the supervisor's
context, which is exactly what makes a supervisor start mis-routing. The
wrapper still nests the subgraph's runs under the node, so the waterfall shows
the full hierarchy either way.

    python -m langsmith_server --port 1984
    python examples/support_graph.py
"""

from __future__ import annotations

import os
import sys
from pathlib import Path
from typing import Annotated, TypedDict

sys.path.insert(0, str(Path(__file__).resolve().parent))

from _env import load_env  # noqa: E402

load_env()

from langchain_core.messages import AIMessage, HumanMessage, SystemMessage  # noqa: E402
from langchain_core.tools import tool  # noqa: E402
from langchain_openai import ChatOpenAI  # noqa: E402
from langgraph.graph import END, START, StateGraph  # noqa: E402
from langgraph.graph.message import add_messages  # noqa: E402
from langgraph.prebuilt import ToolNode, tools_condition  # noqa: E402

from support_agent import resolve_credentials  # noqa: E402
from support_tools import ACTION_TOOLS, ORDER_TOOLS, POLICY_TOOLS  # noqa: E402

MAX_ROUNDS = 4

SUPERVISOR_PROMPT = (
    "You coordinate a support team. You have no tools of your own -- your only "
    "job is to delegate to specialists and then finish.\n\n"
    "Specialists:\n"
    "- policy_agent: company policy, procedures, refund and returns rules\n"
    "- orders_agent: customer profiles, order history, order details\n"
    "- action_agent: refunds, customer emails, ticket escalations\n\n"
    "Delegate with the delegate_* tools. Pass a clear, self-contained task; the "
    "specialist cannot see this conversation. If a request needs both rules and "
    "customer data, delegate to both in the same turn. Never invent policy or "
    "customer details -- if a specialist reports a failure, do not retry it the "
    "same way. Once you have what you need, call finish with the answer."
)

POLICY_PROMPT = (
    "You are the policy specialist. Search the knowledge base and answer strictly "
    "from what it returns. Quote the article ids you relied on. If nothing matches, "
    "say so plainly -- never invent policy."
)

ORDERS_PROMPT = (
    "You are the orders specialist. Look up customers and orders with the tools. "
    "Use only ids that appear in the request or in tool output; never guess an id. "
    "Report exactly what the tools returned."
)

ACTION_PROMPT = (
    "You are the action specialist. You perform writes: refunds, emails and "
    "escalations. Confirm the details with the lookup tools first if you are "
    "unsure. If a write fails, report the failure verbatim -- do not retry it "
    "unchanged and do not pretend it succeeded."
)


# --------------------------------------------------------------- routing tools
# These are never executed. The supervisor emits them as calls, and the graph
# reads the call names to decide which subagent to run -- the standard
# supervisor pattern.

@tool
def delegate_to_policy_agent(task: str) -> str:
    """Delegate a question about company policy, procedures or refund rules."""
    return task


@tool
def delegate_to_orders_agent(task: str) -> str:
    """Delegate a lookup of customer profiles or order details."""
    return task


@tool
def delegate_to_action_agent(task: str) -> str:
    """Delegate a write: issuing a refund, emailing a customer, escalating a ticket."""
    return task


@tool
def finish(answer: str) -> str:
    """Finish, with the final answer for the person who asked."""
    return answer


ROUTE_TOOLS = [
    delegate_to_policy_agent,
    delegate_to_orders_agent,
    delegate_to_action_agent,
    finish,
]
ROUTE_BY_TOOL = {
    "delegate_to_policy_agent": "policy_agent",
    "delegate_to_orders_agent": "orders_agent",
    "delegate_to_action_agent": "action_agent",
}


class State(TypedDict):
    messages: Annotated[list, add_messages]
    question: str
    round: int
    routes: list[str]
    final: str


# ------------------------------------------------------------------ subagents

def build_react_subagent(llm, tools: list, name: str):
    """A self-contained ReAct loop with its own message history.

    Named `<agent>_loop` so the trace reads as the delegation (`orders_agent`)
    followed by that agent's own loop, rather than two spans with one name.
    """
    bound = llm.bind_tools(tools)

    def call_model(state: State) -> dict:
        return {"messages": [bound.invoke(state["messages"])]}

    graph = StateGraph(State)
    graph.add_node("agent", call_model)
    graph.add_node("tools", ToolNode(tools))
    graph.add_edge(START, "agent")
    graph.add_conditional_edges("agent", tools_condition)
    graph.add_edge("tools", "agent")
    return graph.compile(name=f"{name}_loop")


def make_subagent_node(subagent, name: str, system_prompt: str):
    """Wrap a compiled subagent as a parent node with an isolated context."""

    def node(state: State) -> dict:
        result = subagent.invoke(
            {
                "messages": [
                    {"role": "system", "content": system_prompt},
                    {"role": "user", "content": state["question"]},
                ]
            },
            config={"recursion_limit": 12},
        )
        summary = " ".join(str(result["messages"][-1].content or "").split())
        return {"messages": [AIMessage(content=f"[{name}] {summary}", name=name)]}

    node.__name__ = f"{name}_node"
    return node


# ------------------------------------------------------------------ supervisor

def build_graph(model: str, base_url: str, api_key: str):
    # Free tiers hand back transient 429/502 regularly; retry rather than
    # letting a provider hiccup look like an agent failure.
    llm = ChatOpenAI(model=model, base_url=base_url, api_key=api_key,
                     temperature=0, timeout=120, max_retries=4)
    router = llm.bind_tools(ROUTE_TOOLS)

    policy_agent = build_react_subagent(llm, POLICY_TOOLS, "policy_agent")
    orders_agent = build_react_subagent(llm, ORDER_TOOLS, "orders_agent")
    action_agent = build_react_subagent(llm, ACTION_TOOLS, "action_agent")

    def supervisor(state: State) -> dict:
        round_no = state.get("round", 0) + 1
        history = state.get("messages", [])
        prompt = [
            SystemMessage(content=SUPERVISOR_PROMPT),
            *history,
            HumanMessage(content=(
                f"Request: {state['question']}\n\n"
                f"This is round {round_no} of {MAX_ROUNDS}. "
                + ("You must call finish now." if round_no >= MAX_ROUNDS
                   else "Delegate, or finish if you already have enough.")
            )),
        ]
        message = router.invoke(prompt)
        routes, final = [], None
        for call in getattr(message, "tool_calls", None) or []:
            target = ROUTE_BY_TOOL.get(call["name"])
            if target and target not in routes:
                routes.append(target)
            elif call["name"] == "finish":
                final = str(call["args"].get("answer", "")).strip()

        if not routes and final is None:
            # The model answered instead of routing -- treat it as a finish
            # rather than looping until the recursion limit kills the run.
            final = " ".join(str(message.content or "").split()) or "(no answer)"
            routes = []

        return {"messages": [message], "round": round_no,
                "routes": routes, "final": final or ""}

    def route(state: State) -> list[str]:
        if state.get("final") or state.get("round", 0) >= MAX_ROUNDS:
            return ["finalize"]
        return state.get("routes") or ["finalize"]

    def finalize(state: State) -> dict:
        if state.get("final"):
            return {}
        summary = llm.invoke([
            SystemMessage(content=(
                "Compose the final answer for the person who asked, using only the "
                "specialist reports above. Be concise. If a specialist reported a "
                "failure, say what failed."
            )),
            *state["messages"],
            HumanMessage(content=f"Request: {state['question']}"),
        ])
        return {"final": " ".join(str(summary.content or "").split())}

    graph = StateGraph(State)
    graph.add_node("supervisor", supervisor)
    graph.add_node("policy_agent", make_subagent_node(policy_agent, "policy_agent", POLICY_PROMPT))
    graph.add_node("orders_agent", make_subagent_node(orders_agent, "orders_agent", ORDERS_PROMPT))
    graph.add_node("action_agent", make_subagent_node(action_agent, "action_agent", ACTION_PROMPT))
    graph.add_node("finalize", finalize)

    graph.add_edge(START, "supervisor")
    graph.add_conditional_edges(
        "supervisor", route,
        ["policy_agent", "orders_agent", "action_agent", "finalize"],
    )
    for agent in ("policy_agent", "orders_agent", "action_agent"):
        graph.add_edge(agent, "supervisor")
    graph.add_edge("finalize", END)
    return graph.compile()


QUESTIONS = [
    "Customer C-1002 says their order arrived damaged. Look them up, find the order, "
    "and tell me what our policy says we should do.",
    "What is our return window for damaged goods?",
    "Please refund order O-5003 in full.",
    "Refund order O-5001 for $5000.",
    "Escalate an urgent ticket for customer C-9999 about repeated billing failures.",
    "Email Ada Lovelace a summary of her open orders.",
]


def main() -> None:
    base_url, api_key, model = resolve_credentials()

    print(f"endpoint   : {base_url}")
    print(f"model      : {model}")
    print(f"graph      : supervisor -> policy_agent | orders_agent | action_agent")
    print(f"tracing to : {os.environ.get('LANGSMITH_ENDPOINT')}")
    print(f"project    : {os.environ.get('LANGSMITH_PROJECT')}\n")

    app = build_graph(model, base_url, api_key)

    for i, question in enumerate(QUESTIONS, 1):
        print(f"[{i}/{len(QUESTIONS)}] {question}")
        try:
            result = app.invoke(
                {"messages": [], "question": question, "round": 0,
                 "routes": [], "final": ""},
                config={"metadata": {"question_index": i,
                                     "graph": "supervisor-subagents"},
                        "recursion_limit": 60},
            )
        except Exception as exc:  # noqa: BLE001 - a failed question still gets traced
            print(f"      -> FAILED: {type(exc).__name__}: {exc}\n")
            continue
        print(f"      -> {result.get('final', '')[:220]}\n")

    from support_tools import EMAILS, ORDERS, TICKETS

    print(f"side effects: {len(EMAILS)} email(s), {len(TICKETS)} ticket(s), "
          f"refunded={sum(o['refunded'] for o in ORDERS.values()):.2f}")

    from langchain_core.tracers.langchain import get_client

    get_client().flush()
    print("traces flushed to the local server")


if __name__ == "__main__":
    main()
