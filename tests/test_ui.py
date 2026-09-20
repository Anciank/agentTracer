"""The waterfall UI: derived trace semantics and the /ui JSON endpoints."""

from __future__ import annotations

from uuid import uuid4

from conftest import TRACE, run

from langsmith_server.traces import (
    depth_of,
    run_status,
    run_usage,
    trace_detail,
    trace_summaries,
)


def nested_runs():
    root = run("root", start=0, end=10, dotted="a")
    child = run("child", parent=root["id"], start=2, end=6, dotted="a.b")
    leaf = run(
        "leaf", parent=child["id"], run_type="tool", start=3, end=4, dotted="a.b.c",
        error="boom", inputs={"x": 1},
    )
    return root, child, leaf


# ------------------------------------------------------------- derived fields


def test_depth_comes_from_the_dotted_order():
    root, child, leaf = nested_runs()
    by_id = {r["id"]: r for r in (root, child, leaf)}
    assert depth_of(root, by_id) == 0
    assert depth_of(child, by_id) == 1
    assert depth_of(leaf, by_id) == 2


def test_depth_falls_back_to_walking_parents():
    root, child, leaf = nested_runs()
    for r in (root, child, leaf):
        r.pop("dotted_order")
    by_id = {r["id"]: r for r in (root, child, leaf)}
    assert depth_of(root, by_id) == 0
    assert depth_of(leaf, by_id) == 2


def test_status_distinguishes_error_running_and_success():
    assert run_status(run("a", end=1)) == "success"
    assert run_status(run("a", error="nope", end=1)) == "error"
    assert run_status(run("a")) == "running"  # no end_time yet


def test_usage_prefers_usage_metadata_then_falls_back_to_llm_output():
    normalized = run("llm", extra={"metadata": {"usage_metadata": {
        "input_tokens": 10, "output_tokens": 3, "total_tokens": 13}}})
    assert run_usage(normalized) == {"input": 10, "output": 3, "total": 13}

    legacy = run("llm", outputs={"llm_output": {"token_usage": {
        "prompt_tokens": 7, "completion_tokens": 2, "total_tokens": 9}}})
    assert run_usage(legacy) == {"input": 7, "output": 2, "total": 9}

    assert run_usage(run("chain")) is None


# ------------------------------------------------------------- waterfall maths


def test_trace_detail_orders_rows_and_computes_offsets():
    detail = trace_detail(TRACE, list(nested_runs()))

    assert [r["name"] for r in detail["rows"]] == ["root", "child", "leaf"]
    assert [r["depth"] for r in detail["rows"]] == [0, 1, 2]
    assert [r["offset"] for r in detail["rows"]] == [0.0, 2.0, 3.0]
    assert [r["latency"] for r in detail["rows"]] == [10.0, 4.0, 1.0]
    assert detail["span"] == 10.0


def test_self_time_subtracts_direct_children():
    detail = trace_detail(TRACE, list(nested_runs()))
    by_name = {r["name"]: r for r in detail["rows"]}

    assert by_name["root"]["self_time"] == 6.0   # 10s span, 4s of child work
    assert by_name["child"]["self_time"] == 3.0  # 4s span, 1s of leaf work
    assert by_name["leaf"]["self_time"] == 1.0   # no children


def test_self_time_never_goes_negative_when_children_overlap():
    root = run("root", start=0, end=1, dotted="a")
    a = run("a", parent=root["id"], start=0, end=1, dotted="a.b")
    b = run("b", parent=root["id"], start=0, end=1, dotted="a.c")
    detail = trace_detail(TRACE, [root, a, b])
    assert detail["rows"][0]["self_time"] == 0.0


def test_unfinished_run_is_marked_running():
    root = run("root", start=0, end=5, dotted="a")
    pending = run("pending", parent=root["id"], start=1, dotted="a.b")
    detail = trace_detail(TRACE, [root, pending])
    pending_row = next(r for r in detail["rows"] if r["name"] == "pending")
    assert pending_row["status"] == "running"
    assert pending_row["latency"] is None


def test_trace_detail_returns_none_without_a_start_time():
    assert trace_detail(TRACE, [{"id": "x", "name": "n"}]) is None
    assert trace_detail(TRACE, []) is None


def test_summaries_roll_up_counts_and_tokens():
    root, child, leaf = nested_runs()
    child["extra"] = {"metadata": {"usage_metadata": {
        "input_tokens": 100, "output_tokens": 20, "total_tokens": 120}}}
    summaries = trace_summaries([root, child, leaf])

    assert len(summaries) == 1
    s = summaries[0]
    assert s["name"] == "root"
    assert s["run_count"] == 3
    assert s["error_count"] == 1
    assert s["duration"] == 10.0
    assert s["tokens"] == {"input": 100, "output": 20, "total": 120}
    assert s["run_types"] == {"chain": 2, "tool": 1}


# ------------------------------------------------------------------ /ui routes


def test_index_serves_the_page(client):
    res = client.get("/ui")
    assert res.status_code == 200
    assert "text/html" in res.headers["content-type"]
    assert "waterfall" in res.text


def test_projects_lists_only_named_projects(client):
    client.post("/runs", json=run("a"))
    client.post("/runs", json={**run("b"), "session_name": "other"})
    assert client.get("/ui/projects").json() == ["other", "proj"]


def test_traces_endpoint_returns_summaries(client):
    for r in nested_runs():
        client.post("/runs", json=r)
    traces = client.get("/ui/traces").json()
    assert len(traces) == 1
    assert traces[0]["run_count"] == 3


def test_traces_endpoint_filters_by_project(client):
    client.post("/runs", json=run("a"))
    client.post("/runs", json={**run("b", trace=str(uuid4())), "session_name": "other"})

    assert len(client.get("/ui/traces").json()) == 2
    proj = client.get("/ui/traces", params={"session": "proj"}).json()
    assert len(proj) == 1 and proj[0]["session_name"] == "proj"


def test_unknown_project_returns_nothing_not_everything(client):
    client.post("/runs", json=run("a"))
    assert client.get("/ui/traces", params={"session": "nope"}).json() == []


def test_trace_list_is_not_truncated_by_the_run_row_cap(client):
    """Regression: the list endpoint used to inherit query_runs' limit=100, so
    only the first 100 runs were summarised and most traces vanished."""
    traces = 40
    for t in range(traces):
        tid = str(uuid4())
        root = run(f"root-{t}", trace=tid, start=0, end=20)
        client.post("/runs", json=root)
        # 5 runs each -> 240 runs total, well past a cap of 100
        for i in range(4):
            client.post("/runs", json=run(
                f"child-{t}-{i}", trace=tid, parent=root["id"],
                start=1 + i, end=2 + i, dotted=f"a.{i}"))

    listed = client.get("/ui/traces", params={"limit": traces}).json()
    assert len(listed) == traces, f"expected {traces} traces, got {len(listed)}"
    assert all(t["run_count"] == 5 for t in listed)


def test_query_runs_limit_none_removes_the_cap(client):
    from langsmith_server.storage import Storage
    storage = client.app.state.storage
    for i in range(150):
        storage.upsert_run({"id": str(uuid4()), "name": f"n{i}", "run_type": "chain"}, op="post")

    assert len(storage.query_runs()) == 100          # default cap still applies
    assert len(storage.query_runs(limit=None)) == 150
    assert len(storage.query_runs(limit=None, offset=140)) == 10


def test_trace_detail_endpoint_returns_positioned_rows(client):
    for r in nested_runs():
        client.post("/runs", json=r)
    detail = client.get(f"/ui/traces/{TRACE}").json()

    assert [r["name"] for r in detail["rows"]] == ["root", "child", "leaf"]
    assert detail["rows"][2]["error"] == "boom"
    assert detail["rows"][2]["inputs"] == {"x": 1}
    assert detail["summary"]["error_count"] == 1


def test_trace_detail_endpoint_404s_for_unknown_trace(client):
    assert client.get(f"/ui/traces/{uuid4()}").status_code == 404


def test_ui_endpoints_do_not_require_the_langsmith_sdk_headers(client):
    # The viewer is a plain browser client, not an SDK: no x-api-key involved.
    assert client.get("/ui/traces").status_code == 200
