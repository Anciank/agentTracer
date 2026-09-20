"""Derived trace semantics shared by the reporting CLI and the waterfall UI.

Everything here is a pure function over run dicts as returned by
`Storage.query_runs`, so the same numbers back both the text report and the UI.
"""

from __future__ import annotations

from collections import defaultdict
from typing import Any, Optional

from .storage import parse_iso

STATUS_SUCCESS = "success"
STATUS_ERROR = "error"
STATUS_RUNNING = "running"


def run_latency(run: dict) -> Optional[float]:
    start = parse_iso(run.get("start_time"))
    end = parse_iso(run.get("end_time"))
    if start is None or end is None:
        return None
    return (end - start).total_seconds()


def run_usage(run: dict) -> Optional[dict[str, int]]:
    """Token usage, preferring the normalized `usage_metadata`."""
    extra = run.get("extra") or {}
    meta = (extra.get("metadata") or {}) if isinstance(extra, dict) else {}
    usage = meta.get("usage_metadata") if isinstance(meta, dict) else None
    if isinstance(usage, dict) and usage:
        return {
            "input": int(usage.get("input_tokens") or 0),
            "output": int(usage.get("output_tokens") or 0),
            "total": int(usage.get("total_tokens") or 0),
        }

    outputs = run.get("outputs") or {}
    token_usage = (
        (outputs.get("llm_output") or {}).get("token_usage")
        if isinstance(outputs, dict)
        else None
    )
    if isinstance(token_usage, dict):
        return {
            "input": int(token_usage.get("prompt_tokens") or 0),
            "output": int(token_usage.get("completion_tokens") or 0),
            "total": int(token_usage.get("total_tokens") or 0),
        }
    return None


def run_model(run: dict) -> Optional[str]:
    extra = run.get("extra") or {}
    meta = (extra.get("metadata") or {}) if isinstance(extra, dict) else {}
    if isinstance(meta, dict):
        name = meta.get("ls_model_name") or meta.get("model_name")
        if name:
            return str(name)
    invocation = (extra.get("invocation_params") or {}) if isinstance(extra, dict) else {}
    if isinstance(invocation, dict) and invocation.get("model"):
        return str(invocation["model"])
    return None


def run_status(run: dict) -> str:
    if run.get("error"):
        return STATUS_ERROR
    if not run.get("end_time"):
        return STATUS_RUNNING
    return STATUS_SUCCESS


def run_node(run: dict) -> Optional[str]:
    """The graph node a run executed in, when LangGraph recorded one."""
    extra = run.get("extra") or {}
    meta = (extra.get("metadata") or {}) if isinstance(extra, dict) else {}
    if isinstance(meta, dict):
        node = meta.get("langgraph_node")
        if node:
            return str(node)
    return None


def order_key(run: dict) -> tuple:
    """Depth-first ordering. `dotted_order` is a path, so it sorts lexically."""
    dotted = run.get("dotted_order") or ""
    if dotted:
        return (0, dotted)
    return (1, run.get("start_time") or "")


def depth_of(run: dict, by_id: dict[str, dict]) -> int:
    dotted = run.get("dotted_order")
    if dotted:
        return dotted.count(".")
    depth, seen, cursor = 0, set(), run
    while cursor.get("parent_run_id") and cursor["parent_run_id"] not in seen:
        seen.add(cursor["parent_run_id"])
        cursor = by_id.get(cursor["parent_run_id"])
        if cursor is None:
            break
        depth += 1
    return depth


def group_by_trace(runs: list[dict]) -> dict[str, list[dict]]:
    grouped: dict[str, list[dict]] = defaultdict(list)
    for run in runs:
        grouped[run.get("trace_id") or run["id"]].append(run)
    for trace_runs in grouped.values():
        trace_runs.sort(key=order_key)
    return dict(grouped)


def _blank_vs_none(run: dict, key: str) -> Any:
    return run.get(key)


def trace_summary(trace_id: str, runs: list[dict]) -> dict:
    roots = [r for r in runs if not r.get("parent_run_id")]
    starts = [s for s in (parse_iso(r.get("start_time")) for r in runs) if s]
    ends = [e for e in (parse_iso(r.get("end_time")) for r in runs) if e]
    usages = [u for u in (run_usage(r) for r in runs) if u]

    tok = {
        "input": sum(u["input"] for u in usages),
        "output": sum(u["output"] for u in usages),
        "total": sum(u["total"] for u in usages),
    }
    types: dict[str, int] = defaultdict(int)
    for run in runs:
        types[run.get("run_type") or "unknown"] += 1

    start = min(starts) if starts else None
    end = max(ends) if ends else None
    return {
        "trace_id": trace_id,
        "name": roots[0]["name"] if roots else runs[0].get("name") or "(unnamed)",
        "session_name": runs[0].get("session_name"),
        "start": start.isoformat() if start else None,
        "end": end.isoformat() if end else None,
        "duration": (end - start).total_seconds() if start and end else None,
        "run_count": len(runs),
        "error_count": sum(1 for r in runs if r.get("error")),
        "running_count": sum(1 for r in runs if run_status(r) == STATUS_RUNNING),
        "tokens": tok,
        "run_types": dict(types),
    }


def trace_summaries(runs: list[dict]) -> list[dict]:
    summaries = [trace_summary(tid, rs) for tid, rs in group_by_trace(runs).items()]
    summaries.sort(key=lambda s: s["start"] or "", reverse=True)
    return summaries


def trace_detail(trace_id: str, runs: list[dict]) -> Optional[dict]:
    """One trace rendered as positioning data for a waterfall chart."""
    if not runs:
        return None

    ordered = sorted(runs, key=order_key)
    by_id = {r["id"]: r for r in ordered}

    starts = [s for s in (parse_iso(r.get("start_time")) for r in ordered) if s]
    ends = [e for e in (parse_iso(r.get("end_time")) for r in ordered) if e]
    if not starts:
        return None
    t0 = min(starts)
    t_end = max(ends) if ends else t0
    span = max((t_end - t0).total_seconds(), 1e-6)

    rows = []
    for run in ordered:
        start = parse_iso(run.get("start_time"))
        end = parse_iso(run.get("end_time"))
        latency = (end - start).total_seconds() if start and end else None
        usage = run_usage(run)
        rows.append(
            {
                "id": run["id"],
                "parent_run_id": run.get("parent_run_id"),
                "name": run.get("name"),
                "run_type": run.get("run_type") or "chain",
                "node": run_node(run),
                "status": run_status(run),
                "depth": depth_of(run, by_id),
                "offset": (start - t0).total_seconds() if start else 0.0,
                "latency": latency,
                "self_time": _self_time(run, by_id, runs),
                "start_time": run.get("start_time"),
                "end_time": run.get("end_time"),
                "tokens": usage,
                "model": run_model(run),
                "tags": run.get("tags") or [],
                "dotted_order": run.get("dotted_order"),
                "inputs": _blank_vs_none(run, "inputs"),
                "outputs": _blank_vs_none(run, "outputs"),
                "extra": _blank_vs_none(run, "extra"),
                "serialized": run.get("serialized"),
                "error": run.get("error"),
                "attachments": run.get("attachments") or {},
            }
        )

    return {
        "trace_id": trace_id,
        "summary": trace_summary(trace_id, ordered),
        "span": span,
        "rows": rows,
    }


def _self_time(run: dict, by_id: dict[str, dict], runs: list[dict]) -> Optional[float]:
    """Latency excluding time spent in direct children.

    The gap between a node and the sum of its children is where the trace shows
    genuine self-cost versus pure orchestration overhead.
    """
    latency = run_latency(run)
    if latency is None:
        return None
    child_total = sum(
        run_latency(child) or 0.0
        for child in runs
        if child.get("parent_run_id") == run["id"]
    )
    return max(latency - child_total, 0.0)
