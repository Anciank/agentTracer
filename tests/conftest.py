from __future__ import annotations

import uuid
from typing import Any, Optional

import pytest
from fastapi.testclient import TestClient

from langsmith_server.app import app

TRACE = "11111111-1111-1111-1111-111111111111"


def run(
    name: str, *,
    id: Optional[str] = None,
    parent: Optional[str] = None,
    run_type: str = "chain",
    start: float = 0.0,
    end: Optional[float] = None,
    error: Optional[str] = None,
    dotted: Optional[str] = None,
    trace: str = TRACE,
    **extra: Any,
) -> dict:
    """Build a run dict whose times are `start`/`end` seconds past 00:00:00."""
    def stamp(offset: float) -> str:
        return f"2026-01-01T00:00:{offset:09.6f}+00:00"

    payload: dict = {
        "id": id or str(uuid.uuid4()),
        "trace_id": trace,
        "parent_run_id": parent,
        "name": name,
        "run_type": run_type,
        "start_time": stamp(start),
        "dotted_order": dotted,
        "session_name": "proj",
        **extra,
    }
    if end is not None:
        payload["end_time"] = stamp(end)
    if error is not None:
        payload["error"] = error
    return payload


def make_client(
    tmp_path, monkeypatch, reqlog: Optional[str] = None
) -> TestClient:
    """Build a client whose DB and request log are isolated to `tmp_path`.

    `reqlog=""` disables request logging; the default keeps it inside tmp_path.
    """
    monkeypatch.setenv("LANGSMITH_LOCAL_DB", str(tmp_path / "http.db"))
    monkeypatch.setenv(
        "LANGSMITH_LOCAL_REQLOG",
        reqlog if reqlog is not None else str(tmp_path / "requests.log"),
    )
    # The middleware stack is cached on the app, so force a rebuild to pick up
    # the environment this test just set.
    app.middleware_stack = None
    return TestClient(app)


@pytest.fixture()
def client_factory(tmp_path, monkeypatch):
    """Returns a callable so a test can build extra, differently-configured clients."""

    def _factory(reqlog: Optional[str] = None) -> TestClient:
        return make_client(tmp_path, monkeypatch, reqlog)

    return _factory


@pytest.fixture()
def client(tmp_path, monkeypatch) -> TestClient:
    with make_client(tmp_path, monkeypatch) as test_client:
        yield test_client
