"""Port of Python multipart wire tests against the Go server."""
from __future__ import annotations

import json
from uuid import uuid4

import httpx
import zstandard

from helpers import BOUNDARY, encode_multipart


def test_info_requires_key_and_reports_multipart(client: httpx.Client):
    info = client.get("/info").json()
    assert info["version"] == "0.9.0"
    assert info["batch_ingest_config"]["use_multipart_endpoint"] is True


def test_multipart_nested_trace_and_attachment(client: httpx.Client):
    run_id, child_id = str(uuid4()), str(uuid4())
    main = json.dumps({
        "id": run_id, "trace_id": run_id, "name": "root", "run_type": "chain",
        "session_name": "compat-mp", "start_time": "2026-01-01T00:00:00+00:00",
        "dotted_order": f"20260101T000000000000Z{run_id[:8]}",
    }).encode()
    child = json.dumps({
        "id": child_id, "trace_id": run_id, "parent_run_id": run_id, "name": "llm",
        "run_type": "llm", "session_name": "compat-mp",
        "start_time": "2026-01-01T00:00:00.100000+00:00",
        "dotted_order": f"20260101T000000000000Z{run_id[:8]}.20260101T000000100000Z{child_id[:8]}",
    }).encode()
    patch = json.dumps({
        "id": run_id, "outputs": {"ok": True}, "end_time": "2026-01-01T00:00:01+00:00", "name": None,
    }).encode()
    blob = bytes(range(256))
    body = encode_multipart([
        (f"post.{run_id}", main, "application/json"),
        (f"post.{child_id}", child, "application/json"),
        (f"patch.{run_id}", patch, "application/json"),
        (f"attachment.{run_id}.chart", blob, "image/png; length=256"),
    ])
    r = client.post(
        "/runs/multipart",
        content=body,
        headers={"Content-Type": f"multipart/form-data; boundary={BOUNDARY}"},
    )
    assert r.status_code == 204, r.text

    # zstd
    zst = zstandard.ZstdCompressor().compress(body)
    r = client.post(
        "/runs/multipart",
        content=zst,
        headers={
            "Content-Type": f"multipart/form-data; boundary={BOUNDARY}",
            "Content-Encoding": "zstd",
        },
    )
    assert r.status_code == 204, r.text

    detail = client.get(f"/api/ui/traces/{run_id}/summary").json()
    assert len(detail["rows"]) >= 2
    assert detail["summary"]["run_count"] >= 2

    run = client.get(f"/runs/{run_id}").json()
    assert run["name"] == "root"  # null patch must not wipe
    assert run["outputs"]["ok"] is True


def test_batch_json(client: httpx.Client):
    rid = str(uuid4())
    r = client.post("/runs/batch", json={
        "post": [{"id": rid, "trace_id": rid, "name": "batch", "run_type": "chain",
                  "session_name": "compat-batch", "start_time": "2026-01-01T00:00:00+00:00"}],
        "patch": [{"id": rid, "outputs": {"x": 1}, "end_time": "2026-01-01T00:00:01+00:00"}],
    })
    assert r.status_code == 202, r.text
    run = client.get(f"/runs/{rid}").json()
    assert run["outputs"]["x"] == 1


def test_ui_projects_and_list(client: httpx.Client):
    projects = client.get("/api/ui/projects").json()
    assert isinstance(projects, list)
    traces = client.get("/api/ui/traces").json()
    assert "traces" in traces
    html = client.get("/ui")
    assert html.status_code == 200
