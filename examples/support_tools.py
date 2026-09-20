"""Domain data and tools for the support examples.

Shared by `support_agent.py` (a plain ReAct loop) and `support_graph.py` (a
routed, parallel, self-verifying graph) so both run against exactly the same
tools and the two graphs are directly comparable.

State is in-memory and resets per process. "Remote" calls are local sleeps
standing in for network I/O -- without them every tool span would be a
sub-millisecond speck and the waterfall unreadable.
"""

from __future__ import annotations

import json
import math
import re
import time
from collections import Counter
from typing import Optional

from langchain_core.tools import tool

# --------------------------------------------------------------- seed data

CUSTOMERS: dict[str, dict] = {
    "C-1001": {"id": "C-1001", "name": "Ada Lovelace", "email": "ada@example.com",
               "tier": "gold", "since": "2021-03-04"},
    "C-1002": {"id": "C-1002", "name": "Grace Hopper", "email": "grace@example.com",
               "tier": "platinum", "since": "2019-11-20"},
    "C-1003": {"id": "C-1003", "name": "Alan Turing", "email": "alan@example.com",
               "tier": "silver", "since": "2023-07-11"},
    "C-1004": {"id": "C-1004", "name": "Katherine Johnson", "email": "katherine@example.com",
               "tier": "gold", "since": "2020-01-09"},
}

ORDERS: dict[str, dict] = {
    "O-5001": {"id": "O-5001", "customer_id": "C-1001", "status": "delivered",
               "placed": "2026-08-02", "total": 128.50, "refunded": 0.0,
               "items": [{"sku": "KB-77", "name": "Mechanical Keyboard", "qty": 1, "price": 128.50}]},
    "O-5002": {"id": "O-5002", "customer_id": "C-1001", "status": "open",
               "placed": "2026-09-10", "total": 42.00, "refunded": 0.0,
               "items": [{"sku": "MP-12", "name": "Mouse Pad", "qty": 2, "price": 21.00}]},
    "O-5003": {"id": "O-5003", "customer_id": "C-1002", "status": "delivered",
               "placed": "2026-08-21", "total": 899.99, "refunded": 0.0,
               "items": [{"sku": "MN-31", "name": "27\" Monitor", "qty": 1, "price": 899.99}]},
    "O-5004": {"id": "O-5004", "customer_id": "C-1002", "status": "shipped",
               "placed": "2026-09-14", "total": 65.25, "refunded": 0.0,
               "items": [{"sku": "CB-05", "name": "USB-C Cable", "qty": 3, "price": 21.75}]},
    "O-5005": {"id": "O-5005", "customer_id": "C-1003", "status": "delivered",
               "placed": "2026-07-30", "total": 310.00, "refunded": 310.00,
               "items": [{"sku": "HD-88", "name": "Noise-Cancelling Headphones", "qty": 1, "price": 310.00}]},
}

KNOWLEDGE_BASE: list[dict] = [
    {"id": "KB-001", "title": "Returns and refunds policy",
     "body": "Customers may return unused items within 30 days of delivery for a full refund. "
             "Damaged goods are refunded in full regardless of the 30 day window, but photographic "
             "evidence is required. Refunds are issued to the original payment method and settle "
             "within 5 to 7 business days.",
     "tags": ["returns", "refunds", "damaged"]},
    {"id": "KB-002", "title": "Damaged on arrival procedure",
     "body": "When an order arrives damaged the agent should confirm the order number, request a "
             "photo of the damage, then issue a full refund without requiring a return shipment. "
             "Orders above 500 USD also require a supervisor note on the ticket.",
     "tags": ["damaged", "refunds", "escalation"]},
    {"id": "KB-003", "title": "Shipping delays and lost parcels",
     "body": "Parcels marked shipped but not delivered within 10 business days are treated as lost. "
             "Agents may reship the order at no cost or refund it in full, at the customer's choice.",
     "tags": ["shipping", "lost", "refunds"]},
    {"id": "KB-004", "title": "Refund limits and approvals",
     "body": "Refunds above 500 USD require supervisor approval before they are issued. A refund may "
             "never exceed the original order total, and an order may not be refunded twice. "
             "Partial refunds are allowed and accumulate against the order total.",
     "tags": ["refunds", "approval", "limits"]},
    {"id": "KB-005", "title": "Ticket escalation matrix",
     "body": "Billing problems are escalated at high priority; anything involving a security concern "
             "or suspected fraud is escalated at urgent priority. Standard product questions stay at "
             "normal priority and are handled by tier one.",
     "tags": ["escalation", "billing", "security"]},
]

TICKETS: list[dict] = []
EMAILS: list[dict] = []


# ------------------------------------------------------------------ helpers

def _remote(seconds: float) -> None:
    """Stand in for network I/O against a backend service."""
    time.sleep(seconds)


def _dumps(value: object) -> str:
    return json.dumps(value, indent=2, default=str)


_STOPWORDS = {"the", "a", "an", "is", "are", "of", "to", "and", "or", "for",
              "in", "on", "at", "it", "we", "i", "you", "do", "does", "be", "with"}


def _tokens(text: str) -> list[str]:
    return [t for t in re.findall(r"[a-z0-9]+", text.lower()) if t not in _STOPWORDS]


class _BM25:
    """Small in-memory BM25 index over the knowledge base articles."""

    def __init__(self, docs: list[dict]) -> None:
        self.docs = docs
        self.lens = [len(_tokens(d["title"] + " " + d["body"] + " " + " ".join(d["tags"])))
                     for d in docs]
        self.avg = sum(self.lens) / len(self.lens) if self.lens else 1.0
        df: Counter = Counter()
        self.tfs: list[Counter] = []
        for doc in docs:
            text = doc["title"] + " " + doc["body"] + " " + " ".join(doc["tags"])
            tf = Counter(_tokens(text))
            self.tfs.append(tf)
            df.update(tf.keys())
        n = len(docs)
        self.idf = {term: math.log(1 + (n - c + 0.5) / (c + 0.5)) for term, c in df.items()}

    def search(self, query: str, top_k: int) -> list[dict]:
        k1, b = 1.5, 0.75
        terms = _tokens(query)
        scored = []
        for i, doc in enumerate(self.docs):
            score = 0.0
            for term in terms:
                tf = self.tfs[i].get(term, 0)
                if not tf:
                    continue
                denom = tf + k1 * (1 - b + b * self.lens[i] / self.avg)
                score += self.idf.get(term, 0.0) * (tf * (k1 + 1)) / denom
            if score > 0:
                scored.append((score, doc))
        scored.sort(key=lambda pair: pair[0], reverse=True)
        return [{**doc, "score": round(score, 3)} for score, doc in scored[:top_k]]


_KB_INDEX = _BM25(KNOWLEDGE_BASE)


# -------------------------------------------------------------------- tools

@tool
def search_knowledge_base(query: str, top_k: int = 3) -> str:
    """Search internal policy and procedure articles. Use this before answering
    any question about policy, refunds, shipping or escalation."""
    articles = _KB_INDEX.search(query, max(1, min(top_k, 5)))
    if not articles:
        return _dumps({"results": [], "note": "no articles matched; do not invent policy"})
    return _dumps({"results": [
        {"id": a["id"], "title": a["title"], "score": a["score"],
         "excerpt": a["body"][:220] + ("…" if len(a["body"]) > 220 else "")}
        for a in articles
    ]})


@tool
def get_customer(customer_id: str) -> str:
    """Look up a customer by id (format C-nnnn). Returns profile and tier."""
    _remote(0.03)
    customer = CUSTOMERS.get(customer_id.strip().upper())
    if customer is None:
        raise ValueError(
            f"customer {customer_id!r} not found; check the id format (C-nnnn) or ask the customer"
        )
    return _dumps(customer)


@tool
def list_orders(customer_id: str, status: Optional[str] = None) -> str:
    """List a customer's orders, optionally filtered by status
    (open, shipped, delivered, cancelled)."""
    _remote(0.04)
    customer_id = customer_id.strip().upper()
    if customer_id not in CUSTOMERS:
        raise ValueError(f"customer {customer_id!r} not found")
    orders = [o for o in ORDERS.values() if o["customer_id"] == customer_id]
    if status:
        wanted = status.strip().lower()
        orders = [o for o in orders if o["status"] == wanted]
    orders.sort(key=lambda o: o["placed"], reverse=True)
    return _dumps({
        "customer_id": customer_id,
        "count": len(orders),
        "orders": [{"id": o["id"], "status": o["status"], "placed": o["placed"],
                    "total": o["total"], "refundable": round(o["total"] - o["refunded"], 2)}
                   for o in orders],
    })


@tool
def get_order(order_id: str) -> str:
    """Fetch a single order with its line items and remaining refundable amount."""
    _remote(0.03)
    order = ORDERS.get(order_id.strip().upper())
    if order is None:
        raise ValueError(f"order {order_id!r} not found")
    return _dumps({
        **{k: v for k, v in order.items() if k != "customer_id"},
        "refundable": round(order["total"] - order["refunded"], 2),
        "customer_name": CUSTOMERS[order["customer_id"]]["name"],
    })


@tool
def issue_refund(order_id: str, amount: float, reason: str) -> str:
    """Refund an order, fully or partially. Fails if the amount exceeds what is
    still refundable, or if the order was already refunded."""
    _remote(0.25)  # payment gateway round-trip
    order = ORDERS.get(order_id.strip().upper())
    if order is None:
        raise ValueError(f"order {order_id!r} not found")
    if amount <= 0:
        raise ValueError("refund amount must be positive")
    remaining = round(order["total"] - order["refunded"], 2)
    if remaining <= 0:
        raise ValueError(f"order {order['id']} is already fully refunded")
    if amount > remaining:
        raise ValueError(
            f"refund of {amount:.2f} exceeds refundable amount {remaining:.2f} "
            f"for order {order['id']}"
        )
    order["refunded"] = round(order["refunded"] + amount, 2)
    return _dumps({
        "order_id": order["id"], "refunded": amount,
        "remaining_refundable": round(order["total"] - order["refunded"], 2),
        "reason": reason, "eta_business_days": "5-7",
    })


@tool
def send_email(to: str, subject: str, body: str) -> str:
    """Send an email to a customer. `to` must be a valid address."""
    _remote(0.18)  # mail provider round-trip
    if not re.fullmatch(r"[^@\s]+@[^@\s]+\.[A-Za-z]{2,}", to.strip()):
        raise ValueError(f"{to!r} is not a valid email address")
    EMAILS.append({"to": to, "subject": subject, "body": body})
    return _dumps({"status": "sent", "to": to, "subject": subject, "message_id": len(EMAILS)})


@tool
def escalate_ticket(customer_id: str, priority: str, summary: str) -> str:
    """Escalate an issue to a human team. priority is one of low, normal, high, urgent."""
    _remote(0.08)
    allowed = {"low", "normal", "high", "urgent"}
    priority = priority.strip().lower()
    if priority not in allowed:
        raise ValueError(f"priority {priority!r} is invalid; expected one of {sorted(allowed)}")
    customer = CUSTOMERS.get(customer_id.strip().upper())
    if customer is None:
        raise ValueError(f"customer {customer_id!r} not found, cannot escalate on their behalf")
    ticket = {"id": f"T-{9000 + len(TICKETS) + 1}", "customer_id": customer["id"],
              "priority": priority, "summary": summary}
    TICKETS.append(ticket)
    return _dumps({**ticket, "status": "open", "assigned_team": "tier-two"})


TOOLS = [
    search_knowledge_base,
    get_customer,
    list_orders,
    get_order,
    issue_refund,
    send_email,
    escalate_ticket,
]

# Split by concern, so the complex graph can route each branch to a specialist
# with a smaller tool surface (and therefore a smaller prompt). The split is
# read vs write: the specialists that research can't change anything, and the
# one that writes has to look up first.
POLICY_TOOLS = [search_knowledge_base]
ORDER_TOOLS = [get_customer, list_orders, get_order]          # read-only
ACTION_TOOLS = [issue_refund, send_email, escalate_ticket]    # write-only

TOOLS_BY_NAME = {t.name: t for t in TOOLS}
