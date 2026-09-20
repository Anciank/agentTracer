"""The text report: it has to stay readable as the trace store grows."""

from __future__ import annotations

import re
from uuid import uuid4

from conftest import run

from langsmith_server.analysis import build_report, render_text


def many_traces(count: int, runs_each: int = 2) -> list[dict]:
    runs: list[dict] = []
    for t in range(count):
        tid = str(uuid4())
        root = run(f"root-{t}", trace=tid, start=0, end=10)
        runs.append(root)
        for i in range(runs_each - 1):
            runs.append(run(f"child-{t}-{i}", trace=tid, parent=root["id"],
                            start=1, end=2, dotted=f"a.{i}"))
    return runs


def test_identical_failures_are_grouped_with_counts():
    runs = [
        run("tool-a", run_type="tool", start=0, end=1, error="boom"),
        run("tool-a", run_type="tool", start=2, end=3, error="boom"),
        run("tool-a", run_type="tool", start=4, end=5, error="boom"),
        run("tool-b", run_type="tool", start=6, end=7, error="different"),
    ]
    groups = build_report(runs)["error_groups"]

    assert len(groups) == 2
    assert groups[0]["count"] == 3          # most common first
    assert groups[0]["name"] == "tool-a"
    assert groups[0]["message"] == "boom"
    assert groups[1]["count"] == 1
    assert groups[0]["example_trace"]


def test_no_errors_yields_no_groups():
    assert build_report([run("a", start=0, end=1)])["error_groups"] == []


def tree_roots(text: str) -> list[str]:
    """Depth-0 rows in the rendered trees (the root of each shown trace)."""
    return re.findall(r"^- (\S+)", text, re.M)


def test_report_caps_rendered_trace_trees():
    report = build_report(many_traces(60))
    assert report["totals"]["traces"] == 60

    text = render_text(report, max_traces=5)
    assert "showing the 5 most recent of 60" in text
    assert len(tree_roots(text)) == 5


def test_max_traces_zero_renders_everything():
    report = build_report(many_traces(30))
    text = render_text(report, max_traces=0)
    assert "showing the" not in text
    assert len(tree_roots(text)) == 30


def test_report_caps_rendered_error_groups():
    runs = [
        run(f"tool-{i}", run_type="tool", start=i, end=i + 1, error=f"failure {i}")
        for i in range(40)
    ]
    text = render_text(build_report(runs), max_error_groups=6)
    assert "more distinct failure(s)" in text
    assert text.count("failure ") <= 6


def test_empty_report_says_so():
    assert "No runs found" in render_text(build_report([]))


def test_report_of_a_single_trace_still_renders_its_tree():
    report = build_report(many_traces(1))
    text = render_text(report)
    assert "root-0" in text
    assert "showing the" not in text
