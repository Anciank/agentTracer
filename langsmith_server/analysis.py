"""Read the local trace database and report on what the agents actually did.

    python -m langsmith_server.analysis
    python -m langsmith_server.analysis --trace <trace_id> --json
"""

from __future__ import annotations

import argparse
import json
import statistics
from collections import Counter, defaultdict
from datetime import datetime
from pathlib import Path
from typing import Any, Optional

from . import DEFAULT_DB_PATH
from .storage import Storage, parse_iso
from .traces import (
    depth_of,
    group_by_trace,
    run_latency,
    run_model,
    run_usage,
    trace_summary,
)


def _one_line(value: Any, limit: int = 70) -> str:
    text = json.dumps(value, default=str) if not isinstance(value, str) else value
    text = " ".join(text.split())
    return text if len(text) <= limit else text[: limit - 1] + "…"


def _fmt(value: Optional[float], unit: str = "s") -> str:
    if value is None:
        return "-"
    if unit == "s":
        return f"{value:.3f}s" if value < 10 else f"{value:.2f}s"
    return f"{value:.0f}{unit}"


def build_report(runs: list[dict]) -> dict:
    """Compute an aggregate view over a flat list of runs."""
    if not runs:
        return {"empty": True}

    traces = []
    for trace_id, trace_runs in group_by_trace(runs).items():
        summary = trace_summary(trace_id, trace_runs)
        summary["runs"] = trace_runs
        traces.append(summary)
    traces.sort(key=lambda t: t["start"] or "")

    # latency by run type
    by_type: dict[str, list[float]] = defaultdict(list)
    for run in runs:
        lat = run_latency(run)
        if lat is not None:
            by_type[run.get("run_type") or "unknown"].append(lat)

    # latency by node name, for the noisiest nodes
    by_name: dict[str, list[float]] = defaultdict(list)
    for run in runs:
        lat = run_latency(run)
        if lat is not None:
            by_name[run.get("name") or "?"].append(lat)

    # token usage by model
    models: dict[str, dict[str, int]] = defaultdict(
        lambda: {"calls": 0, "input": 0, "output": 0, "total": 0}
    )
    for run in runs:
        usage = run_usage(run)
        if usage is None:
            continue
        model = run_model(run) or "unknown"
        models[model]["calls"] += 1
        for key in ("input", "output", "total"):
            models[model][key] += usage[key]

    # tool usage
    tools: dict[str, dict[str, Any]] = defaultdict(
        lambda: {"calls": 0, "errors": 0, "durations": [], "examples": []}
    )
    for run in runs:
        if run.get("run_type") != "tool":
            continue
        entry = tools[run.get("name") or "?"]
        entry["calls"] += 1
        if run.get("error"):
            entry["errors"] += 1
        lat = run_latency(run)
        if lat is not None:
            entry["durations"].append(lat)
        if len(entry["examples"]) < 2:
            entry["examples"].append(
                {
                    "inputs": _one_line(run.get("inputs"), 60),
                    "outputs": _one_line(run.get("outputs"), 60),
                    "error": bool(run.get("error")),
                }
            )

    # Group identical failures rather than printing thousands of near-identical
    # lines -- at scale the grouping is the only readable form.
    error_counts: Counter = Counter()
    error_sample: dict[tuple, str] = {}
    for r in runs:
        if not r.get("error"):
            continue
        message = " ".join(str(r["error"]).split())[:120]
        key = (r.get("run_type") or "?", r.get("name") or "?", message)
        error_counts[key] += 1
        error_sample.setdefault(key, r.get("trace_id"))

    error_groups = [
        {
            "run_type": run_type,
            "name": name,
            "count": count,
            "message": message,
            "example_trace": error_sample[(run_type, name, message)],
        }
        for (run_type, name, message), count in error_counts.most_common()
    ]

    slowest = sorted(
        (r for r in runs if run_latency(r) is not None),
        key=lambda r: run_latency(r) or 0,
        reverse=True,
    )[:10]

    return {
        "empty": False,
        "totals": {
            "runs": len(runs),
            "traces": len(traces),
            "runs_with_error": sum(1 for r in runs if r.get("error")),
            "traces_with_error": sum(1 for t in traces if t["error_count"]),
            "run_types": dict(Counter(r.get("run_type") or "unknown" for r in runs)),
            "unfinished": sum(1 for r in runs if not r.get("end_time")),
        },
        "traces": traces,
        "latency_by_type": by_type,
        "latency_by_name": by_name,
        "models": dict(models),
        "tools": {k: {**v, "durations": v["durations"]} for k, v in tools.items()},
        "error_groups": error_groups,
        "slowest": [
            {
                "name": r.get("name"),
                "run_type": r.get("run_type"),
                "latency": run_latency(r),
                "trace_id": r.get("trace_id"),
            }
            for r in slowest
        ],
    }


def _render_tree(trace: dict) -> list[str]:
    lines = []
    by_id = {r["id"]: r for r in trace["runs"]}
    for run in trace["runs"]:
        depth = depth_of(run, by_id)
        lat = run_latency(run)
        tokens = run_usage(run)
        bits = [f"{'  ' * depth}- {run.get('name')}", f"[{run.get('run_type')}]"]
        if lat is not None:
            bits.append(_fmt(lat))
        if tokens:
            bits.append(f"tok={tokens['total']}")
        if run.get("error"):
            bits.append("ERROR")
        elapsed = ""
        start = parse_iso(run.get("start_time"))
        trace_start = parse_iso(trace["runs"][0].get("start_time"))
        if start and trace_start:
            elapsed = f"+{(start - trace_start).total_seconds():.2f}s"
        lines.append("  ".join(bits) + (f"  ({elapsed})" if elapsed else ""))
    return lines


def render_text(report: dict, max_traces: int = 25, max_error_groups: int = 15) -> str:
    if report.get("empty"):
        return "No runs found. Start the server and run an example first."

    out: list[str] = []
    totals = report["totals"]
    out.append("=" * 78)
    out.append("TRACE ANALYSIS")
    out.append("=" * 78)
    out.append(
        f"runs={totals['runs']}  traces={totals['traces']}  "
        f"errors={totals['runs_with_error']} run(s) in {totals['traces_with_error']} trace(s)"
    )
    out.append(f"run types: {totals['run_types']}")
    if totals["unfinished"]:
        out.append(f"unfinished runs (no end_time): {totals['unfinished']}")

    out.append("")
    out.append("-- LATENCY BY TYPE " + "-" * 59)
    for run_type, latencies in sorted(report["latency_by_type"].items()):
        out.append(
            f"{run_type:8s} n={len(latencies):3d}  "
            f"total={sum(latencies):8.3f}s  mean={statistics.mean(latencies):7.3f}s  "
            f"max={max(latencies):7.3f}s"
        )

    out.append("")
    out.append("-- LATENCY BY NODE (slowest first) " + "-" * 42)
    ranked = sorted(
        report["latency_by_name"].items(),
        key=lambda kv: sum(kv[1]),
        reverse=True,
    )
    for name, latencies in ranked[:12]:
        out.append(
            f"{name:26s} n={len(latencies):3d}  mean={statistics.mean(latencies):7.3f}s  "
            f"max={max(latencies):7.3f}s"
        )

    if report["models"]:
        out.append("")
        out.append("-- TOKEN USAGE " + "-" * 62)
        for model, stats in sorted(report["models"].items()):
            out.append(
                f"{model:26s} calls={stats['calls']:3d}  in={stats['input']:6d}  "
                f"out={stats['output']:6d}  total={stats['total']:6d}"
            )

    if report["tools"]:
        out.append("")
        out.append("-- TOOLS " + "-" * 69)
        for name, stats in sorted(report["tools"].items()):
            durations = stats["durations"]
            mean = f"{statistics.mean(durations):.3f}s" if durations else "-"
            out.append(
                f"{name:16s} calls={stats['calls']:2d}  errors={stats['errors']:2d}  mean={mean}"
            )
            for example in stats["examples"]:
                flag = "ERR " if example["error"] else "    "
                out.append(f"    {flag}in={example['inputs']} -> out={example['outputs']}")

    groups = report["error_groups"]
    if groups:
        out.append("")
        out.append("-- ERRORS (grouped) " + "-" * 58)
        for g in groups[:max_error_groups]:
            out.append(
                f"{g['count']:>5}x  {g['run_type']:6s} {g['name']:22s} {g['message'][:88]}"
            )
        if len(groups) > max_error_groups:
            out.append(f"       ... {len(groups) - max_error_groups} more distinct "
                       f"failure(s); use --json for all")

    out.append("")
    out.append("-- SLOWEST RUNS " + "-" * 62)
    for run in report["slowest"]:
        out.append(
            f"{_fmt(run['latency']):>9s}  {run['run_type']:6s} {run['name']:26s} "
            f"trace={str(run['trace_id'])[:8]}"
        )

    all_traces = report["traces"]
    # Rendering every tree does not scale: 5,000 traces produces a ~4 MB report.
    # Show the most recent slice and say what was left out.
    shown = all_traces[-max_traces:] if max_traces and len(all_traces) > max_traces else all_traces
    omitted = len(all_traces) - len(shown)

    out.append("")
    out.append("-- TRACES " + "-" * 68)
    if omitted:
        out.append(f"(showing the {len(shown)} most recent of {len(all_traces)}; "
                   f"use --max-traces N or --json for all)")
    for trace in shown:
        status = f"{trace['error_count']} error(s)" if trace["error_count"] else "ok"
        out.append("")
        out.append(
            f"trace {trace['trace_id']}  ({status}, {trace['run_count']} runs, "
            f"{_fmt(trace['duration'])}, {trace['tokens']['total']} tokens)"
        )
        out.extend(_render_tree(trace))

    return "\n".join(out)


def _default_db() -> Path:
    import os

    return Path(os.environ.get("LANGSMITH_LOCAL_DB", str(DEFAULT_DB_PATH)))


def main() -> None:
    parser = argparse.ArgumentParser(description="Analyze traces in the local LangSmith DB.")
    parser.add_argument("--db", default=str(_default_db()))
    parser.add_argument("--session", help="restrict to a project name")
    parser.add_argument("--trace", help="restrict to a single trace id")
    parser.add_argument("--limit", type=int, default=100_000)
    parser.add_argument("--json", action="store_true", help="emit raw JSON instead of text")
    parser.add_argument("--out", help="write the report to a file")
    parser.add_argument("--max-traces", type=int, default=25,
                        help="how many trace trees to render (0 for all)")
    args = parser.parse_args()

    storage = Storage(args.db)
    session_ids = None
    if args.session:
        matches = storage.list_sessions(limit=100, name=args.session)
        if not matches:
            raise SystemExit(f"no project named {args.session!r} in {args.db}")
        session_ids = [s["id"] for s in matches]

    runs = storage.query_runs(session=session_ids, trace=args.trace, limit=args.limit)
    report = build_report(runs)

    if args.json:
        text = json.dumps(report, indent=2, default=str)
    else:
        text = render_text(report, max_traces=args.max_traces)

    print(text)
    if args.out:
        Path(args.out).write_text(text + "\n")
        print(f"\nwrote {args.out}")


if __name__ == "__main__":
    main()
