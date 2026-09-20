"""Tests for the local LangSmith-compatible server.

The multipart encoder below intentionally reproduces the exact byte layout that
`requests_toolbelt.MultipartEncoder` (used by the real SDK) produces, so the
parser is exercised against the real wire format rather than a convenient one.
"""

from __future__ import annotations

import json
from typing import Optional
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient

from langsmith_server.app import app
from langsmith_server.ingest import batch_from_json, batch_from_parts, parse_multipart
from langsmith_server.storage import Storage

BOUNDARY = "boundary123"


def encode_multipart(parts: list[tuple[str, bytes, str]]) -> bytes:
    """Reproduce requests_toolbelt.MultipartEncoder's wire format byte for byte."""
    out = bytearray()
    for name, data, content_type in parts:
        out += f"--{BOUNDARY}\r\n".encode()
        out += f'Content-Disposition: form-data; name="{name}"\r\n'.encode()
        out += f"Content-Type: {content_type}\r\n\r\n".encode()
        out += data
        out += b"\r\n"
    out += f"--{BOUNDARY}--\r\n".encode()
    return bytes(out)


def run_payload(name="root", run_type="chain", **extra) -> dict:
    return {
        "id": str(uuid4()),
        "trace_id": str(uuid4()),
        "name": name,
        "run_type": run_type,
        "start_time": "2026-01-01T00:00:00+00:00",
        **extra,
    }


# --------------------------------------------------------------- multipart parsing


def test_multipart_parses_fragmented_fields_and_binary_attachments():
    run_id, trace_id = str(uuid4()), str(uuid4())
    main = json.dumps({"id": run_id, "trace_id": trace_id, "name": "n", "run_type": "llm"}).encode()
    inputs = json.dumps({"prompt": "hi"}).encode()
    error = json.dumps("boom").encode()  # a bare JSON string, not an object
    # bytes that would be mangled by any utf-8 decoding path
    blob = bytes(range(256))

    body = encode_multipart(
        [
            (f"post.{run_id}", main, "application/json"),
            (f"post.{run_id}.inputs", inputs, "application/json"),
            (f"post.{run_id}.error", error, "application/json"),
            (f"attachment.{run_id}.chart", blob, "image/png"),
        ]
    )

    batch = batch_from_parts(parse_multipart(body, BOUNDARY))

    assert batch.creates[run_id]["name"] == "n"
    assert batch.creates[run_id]["inputs"] == {"prompt": "hi"}
    assert batch.creates[run_id]["error"] == "boom"
    assert batch.attachments == [(run_id, "chart", "image/png", blob)]


def test_multipart_handles_payload_containing_crlf():
    run_id = str(uuid4())
    payload = json.dumps({"id": run_id, "trace_id": str(uuid4()), "name": "a\r\nb"}).encode()
    body = encode_multipart([(f"post.{run_id}", payload, "application/json")])

    batch = batch_from_parts(parse_multipart(body, BOUNDARY))
    assert batch.creates[run_id]["name"] == "a\r\nb"


def test_multipart_strips_attachment_length_parameter():
    run_id = str(uuid4())
    body = encode_multipart([(f"attachment.{run_id}.x", b"\x00\x01", "image/png; length=2")])
    batch = batch_from_parts(parse_multipart(body, BOUNDARY))
    assert batch.attachments[0][2] == "image/png"


def test_multipart_separates_posts_patches_and_feedback():
    post_id, patch_id, fb_id = str(uuid4()), str(uuid4()), str(uuid4())
    body = encode_multipart(
        [
            (f"post.{post_id}", json.dumps({"id": post_id, "name": "created"}).encode(), "application/json"),
            (f"patch.{patch_id}", json.dumps({"id": patch_id, "outputs": {"o": 1}}).encode(), "application/json"),
            (f"feedback.{fb_id}", json.dumps({"id": fb_id, "key": "quality", "score": 1}).encode(), "application/json"),
        ]
    )
    batch = batch_from_parts(parse_multipart(body, BOUNDARY))
    assert set(batch.creates) == {post_id}
    assert set(batch.updates) == {patch_id}
    assert batch.feedback == [{"id": fb_id, "key": "quality", "score": 1}]


def test_batch_from_json_accepts_dicts_and_skips_id_less_payloads():
    batch = batch_from_json(
        {"post": [{"id": "a", "name": "x"}, {"name": "no-id"}], "patch": [{"id": "b", "outputs": {}}]}
    )
    assert set(batch.creates) == {"a"}
    assert set(batch.updates) == {"b"}


# ------------------------------------------------------------------- merge rules


@pytest.fixture()
def storage(tmp_path) -> Storage:
    return Storage(tmp_path / "test.db")


def test_patch_merges_into_existing_run_without_wiping_fields(storage):
    run_id = str(uuid4())
    storage.upsert_run({"id": run_id, "name": "n", "inputs": {"a": 1}, "session_name": "p"}, op="post")
    storage.upsert_run({"id": run_id, "outputs": {"b": 2}, "end_time": None, "name": None}, op="patch")

    run = storage.get_run(run_id)
    assert run["name"] == "n"  # a null in the patch must not erase it
    assert run["inputs"] == {"a": 1}
    assert run["outputs"] == {"b": 2}


def test_error_string_is_persisted_and_queryable(storage):
    run_id = str(uuid4())
    storage.upsert_run({"id": run_id, "name": "t", "run_type": "tool"}, op="post")
    storage.upsert_run({"id": run_id, "error": "division by zero"}, op="patch")
    assert storage.get_run(run_id)["error"] == "division by zero"
    assert len(storage.query_runs(error=True)) == 1


def test_run_auto_creates_and_joins_a_project(storage):
    run_id = str(uuid4())
    storage.upsert_run({"id": run_id, "name": "n", "session_name": "my-project"}, op="post")

    sessions = storage.list_sessions()
    assert [s["name"] for s in sessions] == ["my-project"]
    assert storage.get_run(run_id)["session_id"] == sessions[0]["id"]


def test_query_filters_by_trace_parent_type_and_limit(storage):
    trace_id = str(uuid4())
    root = str(uuid4())
    storage.upsert_run({"id": root, "trace_id": trace_id, "name": "root", "run_type": "chain"}, op="post")
    storage.upsert_run(
        {"id": str(uuid4()), "trace_id": trace_id, "parent_run_id": root, "name": "kid", "run_type": "llm"},
        op="post",
    )

    assert len(storage.query_runs(trace=trace_id)) == 2
    assert [r["name"] for r in storage.query_runs(is_root=True, trace=trace_id)] == ["root"]
    assert [r["name"] for r in storage.query_runs(parent_run=root)] == ["kid"]
    assert [r["name"] for r in storage.query_runs(run_type="llm")] == ["kid"]
    assert len(storage.query_runs(limit=1)) == 1


def test_attachments_round_trip_binary_and_record_metadata(storage):
    run_id = str(uuid4())
    storage.upsert_run({"id": run_id, "name": "n"}, op="post")
    blob = bytes(range(256))
    storage.save_attachment(run_id, "chart", "image/png", blob)

    saved = storage.get_attachment(run_id, "chart")
    assert saved["data"] == blob
    assert storage.get_run(run_id)["attachments"]["chart"] == {"content_type": "image/png", "size": 256}


# ------------------------------------------------------------------- HTTP surface


def test_info_advertises_a_version_the_sdk_accepts(client):
    body = client.get("/info").json()
    assert body["batch_ingest_config"]["use_multipart_endpoint"] is True


def test_sessions_endpoint_returns_tenant_id_required_by_sdk(client):
    created = client.post("/sessions", json={"name": "proj"}).json()
    assert created["tenant_id"]
    assert client.get("/sessions", params={"name": "proj"}).json()[0]["id"] == created["id"]


def test_multipart_endpoint_ingests_and_query_returns_cursor_shape(client):
    run_id, trace_id = str(uuid4()), str(uuid4())
    payload = json.dumps(
        {"id": run_id, "trace_id": trace_id, "name": "http-run", "run_type": "chain"}
    ).encode()
    body = encode_multipart([(f"post.{run_id}", payload, "application/json")])

    response = client.post(
        "/runs/multipart",
        content=body,
        headers={"Content-Type": f"multipart/form-data; boundary={BOUNDARY}"},
    )
    assert response.status_code == 204

    query = client.post("/runs/query", json={"trace": trace_id}).json()
    assert [r["name"] for r in query["runs"]] == ["http-run"]
    assert query["cursors"]["next"] is None


def test_multipart_endpoint_rejects_missing_boundary(client):
    response = client.post("/runs/multipart", content=b"x", headers={"Content-Type": "multipart/form-data"})
    assert response.status_code == 400


def test_feedback_round_trip(client):
    run_id = str(uuid4())
    created = client.post("/feedback", json={"run_id": run_id, "key": "correctness", "score": 0.5}).json()
    assert created["modified_at"] == created["created_at"]
    assert client.get("/feedback", params={"run_id": run_id}).json()[0]["key"] == "correctness"


def test_health_reports_stored_counts(client):
    run_id = str(uuid4())
    client.post("/runs", json={"id": run_id, "name": "n", "run_type": "chain"})
    assert client.get("/health").json()["runs"] == 1


def test_create_run_without_id_is_rejected(client):
    assert client.post("/runs", json={"name": "n"}).status_code == 422


# ----------------------------------------------------------------- request log


def test_request_log_records_method_status_and_full_payload(client, tmp_path):
    run_id = str(uuid4())
    client.post("/runs", json={"id": run_id, "name": "logged", "run_type": "chain"})

    log = (tmp_path / "requests.log").read_text()
    assert "POST /runs  ->  200" in log
    assert '"name": "logged"' in log
    assert run_id in log
    assert "--- RESPONSE" in log


def test_request_log_decodes_multipart_into_readable_parts(client, tmp_path):
    run_id = str(uuid4())
    body = encode_multipart(
        [
            (f"post.{run_id}", json.dumps({"id": run_id, "name": "mp"}).encode(), "application/json"),
            (f"post.{run_id}.inputs", json.dumps({"prompt": "hello world"}).encode(), "application/json"),
            (f"attachment.{run_id}.shot", b"\x89PNG\x00\x01", "image/png"),
        ]
    )
    client.post(
        "/runs/multipart",
        content=body,
        headers={"Content-Type": f"multipart/form-data; boundary={BOUNDARY}"},
    )

    log = (tmp_path / "requests.log").read_text()
    assert "POST /runs/multipart  ->  204" in log
    assert "[part] post." in log
    assert f"[part] post.{run_id}.inputs" in log
    assert '"prompt": "hello world"' in log
    assert "[part] attachment." in log
    assert "<binary omitted>" in log  # never inline raw binary into a text log


def test_request_log_is_skipped_when_path_is_empty(client_factory, tmp_path):
    with client_factory(reqlog="") as client:
        client.get("/health")
    assert not (tmp_path / "requests.log").exists()


def test_request_log_truncates_oversized_payloads(tmp_path, monkeypatch):
    from langsmith_server.requestlog import format_body

    rendered = format_body("application/json", json.dumps({"k": "x" * 500}).encode(), limit=50)
    assert "truncated" in rendered

    rendered = format_body("image/png", bytes(range(256)))
    assert rendered == "<256 bytes of image/png>"

    assert format_body("application/json", b"") == "<empty>"
