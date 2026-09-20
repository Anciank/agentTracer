from __future__ import annotations

import logging
import os
from contextlib import asynccontextmanager
from pathlib import Path
from typing import Any, Optional

from fastapi import FastAPI, Request, Response
from fastapi.responses import JSONResponse

from . import DEFAULT_DB_PATH, SERVER_VERSION
from .ingest import (
    IngestBatch,
    batch_from_json,
    batch_from_parts,
    maybe_decompress,
    parse_multipart,
)
from .requestlog import RequestLogMiddleware
from .storage import Storage, now_iso, to_iso
from .ui import router as ui_router

logger = logging.getLogger("langsmith_server")

# The SDK gates newer code paths on the server version it reads from /info, and
# compares it against real LangSmith release numbers. Report a plain semver that
# clears those checks; our own identity is exposed via /health.
COMPAT_VERSION = "0.9.0"


def _db_path() -> Path:
    return Path(os.environ.get("LANGSMITH_LOCAL_DB", str(DEFAULT_DB_PATH)))


@asynccontextmanager
async def lifespan(app: FastAPI):
    app.state.storage = Storage(_db_path())
    logger.info("local langsmith server using %s", _db_path())
    yield


app = FastAPI(title="Local LangSmith-compatible server", lifespan=lifespan)

# Every request -- including the full SDK payloads -- is appended to a transcript
# file so the wire traffic can be inspected after the fact.
app.add_middleware(RequestLogMiddleware)

# The waterfall viewer over the same local data.
app.include_router(ui_router)


def store(request: Request) -> Storage:
    return request.app.state.storage


def _boundary_from_content_type(content_type: str) -> Optional[str]:
    for chunk in content_type.split(";"):
        chunk = chunk.strip()
        if chunk.lower().startswith("boundary="):
            return chunk[len("boundary=") :].strip().strip('"')
    return None


def _persist(storage: Storage, batch: IngestBatch, endpoint: str) -> dict:
    """Write a parsed batch, isolating failures per run."""
    written = 0
    errors: list[str] = []

    for run_id, payload in batch.creates.items():
        try:
            storage.upsert_run({"id": run_id, **payload}, endpoint=endpoint, op="post")
            written += 1
        except Exception as exc:  # noqa: BLE001 - one bad run must not fail the batch
            errors.append(f"post {run_id}: {exc}")
            logger.warning("failed to store run %s: %s", run_id, exc)

    for run_id, payload in batch.updates.items():
        try:
            storage.upsert_run({"id": run_id, **payload}, endpoint=endpoint, op="patch")
            written += 1
        except Exception as exc:  # noqa: BLE001
            errors.append(f"patch {run_id}: {exc}")
            logger.warning("failed to update run %s: %s", run_id, exc)

    for payload in batch.feedback:
        try:
            storage.create_feedback(payload)
        except Exception as exc:  # noqa: BLE001
            errors.append(f"feedback: {exc}")
            logger.warning("failed to store feedback: %s", exc)

    for run_id, name, content_type, data in batch.attachments:
        try:
            storage.save_attachment(run_id, name, content_type, data)
        except Exception as exc:  # noqa: BLE001
            errors.append(f"attachment {name}: {exc}")
            logger.warning("failed to store attachment %s: %s", name, exc)

    return {"written": written, "errors": errors}


# ------------------------------------------------------------------ health/info


@app.get("/health")
async def health(request: Request) -> dict:
    return {
        "status": "ok",
        "implementation": "langsmith-local",
        "version": SERVER_VERSION,
        "database": str(_db_path()),
        "request_log": os.environ.get(
            "LANGSMITH_LOCAL_REQLOG", str(DEFAULT_DB_PATH.parent / "requests.log")
        )
        or None,
        **store(request).stats(),
    }


@app.get("/info")
async def info() -> dict:
    return {
        "version": COMPAT_VERSION,
        "license_expiration_time": None,
        "batch_ingest_config": {
            "use_multipart_endpoint": True,
            "size_limit": 100,
            "size_limit_bytes": 20_000_000,
            "scale_up_qsize_trigger": 1000,
            "scale_up_nthreads_limit": 16,
            "scale_down_nempty_trigger": 4,
        },
        "instance_flags": {"local_only": True},
    }


@app.get("/")
async def root() -> dict:
    return {"service": "langsmith-local", "version": SERVER_VERSION}


# ----------------------------------------------------------------------- runs


@app.post("/runs")
async def create_run(request: Request, payload: dict[str, Any]) -> dict:
    run_id = payload.get("id")
    if not run_id:
        return JSONResponse({"detail": "run id is required"}, status_code=422)
    store(request).upsert_run(payload, endpoint="runs", op="post")
    return {"id": str(run_id)}


@app.patch("/runs/{run_id}")
async def update_run(request: Request, run_id: str, payload: dict[str, Any]) -> dict:
    store(request).upsert_run({"id": run_id, **payload}, endpoint="runs", op="patch")
    return {"id": run_id}


@app.post("/runs/batch")
async def batch_ingest(request: Request, payload: dict[str, Any]) -> dict:
    batch = batch_from_json(payload)
    return _persist(store(request), batch, "runs/batch")


@app.post("/runs/multipart")
async def multipart_ingest(request: Request) -> Response:
    body = await request.body()
    content_type = request.headers.get("content-type", "")
    boundary = _boundary_from_content_type(content_type)
    if boundary is None:
        return JSONResponse(
            {"detail": "multipart request requires a boundary"}, status_code=400
        )

    try:
        body = maybe_decompress(body, request.headers.get("content-encoding"))
    except ValueError as exc:
        return JSONResponse({"detail": str(exc)}, status_code=400)

    batch = batch_from_parts(parse_multipart(body, boundary))
    result = _persist(store(request), batch, "runs/multipart")
    logger.debug("multipart ingest: %s", result)
    return Response(status_code=204)


@app.post("/runs/query")
async def query_runs(request: Request, payload: dict[str, Any] = None) -> dict:
    payload = payload or {}
    runs = store(request).query_runs(
        session=payload.get("session"),
        run_type=payload.get("run_type"),
        ids=payload.get("id"),
        trace=payload.get("trace"),
        parent_run=payload.get("parent_run"),
        name=payload.get("name"),
        is_root=payload.get("is_root"),
        error=payload.get("error"),
        start_time=payload.get("start_time"),
        limit=int(payload.get("limit") or 100),
        offset=int(payload.get("offset") or 0),
    )
    return {"runs": runs, "cursors": {"next": None}}


@app.get("/runs")
async def list_runs(request: Request, limit: int = 100, offset: int = 0) -> list[dict]:
    return store(request).query_runs(limit=limit, offset=offset)


@app.get("/runs/stats")
async def run_stats(request: Request) -> dict:
    return store(request).stats()


@app.get("/runs/{run_id}")
async def get_run(request: Request, run_id: str) -> Response:
    run = store(request).get_run(run_id)
    if run is None:
        return JSONResponse({"detail": "run not found"}, status_code=404)
    return JSONResponse(run)


@app.delete("/runs/{run_id}")
async def delete_run(request: Request, run_id: str) -> Response:
    store(request).delete_run(run_id)
    return Response(status_code=204)


@app.get("/runs/{run_id}/attachments/{name}")
async def get_attachment(request: Request, run_id: str, name: str) -> Response:
    attachment = store(request).get_attachment(run_id, name)
    if attachment is None:
        return JSONResponse({"detail": "attachment not found"}, status_code=404)
    return Response(
        content=attachment["data"],
        media_type=attachment["content_type"] or "application/octet-stream",
    )


# ------------------------------------------------------------------- sessions


@app.get("/sessions")
async def list_sessions(
    request: Request, limit: int = 100, name: Optional[str] = None
) -> list[dict]:
    return store(request).list_sessions(limit=limit, name=name)


@app.post("/sessions")
async def create_session(request: Request, payload: dict[str, Any]) -> dict:
    return store(request).create_session(payload)


@app.get("/sessions/{session_id}")
async def get_session(request: Request, session_id: str) -> Response:
    session = store(request).get_session(session_id)
    if session is None:
        return JSONResponse({"detail": "session not found"}, status_code=404)
    return JSONResponse(session)


@app.patch("/sessions/{session_id}")
async def update_session(
    request: Request, session_id: str, payload: dict[str, Any]
) -> Response:
    session = store(request).update_session(session_id, payload)
    if session is None:
        return JSONResponse({"detail": "session not found"}, status_code=404)
    return JSONResponse(session)


@app.delete("/sessions/{session_id}")
async def delete_session(request: Request, session_id: str) -> Response:
    store(request).delete_session(session_id)
    return Response(status_code=204)


@app.get("/sessions/{session_id}/runs")
async def list_session_runs(
    request: Request, session_id: str, limit: int = 100, offset: int = 0
) -> list[dict]:
    return store(request).query_runs(session=[session_id], limit=limit, offset=offset)


# ------------------------------------------------------------------- feedback


@app.post("/feedback")
async def create_feedback(request: Request, payload: dict[str, Any]) -> dict:
    stored = store(request).create_feedback(payload)
    created = to_iso(stored.get("created_at")) or now_iso()
    return {
        "id": stored.get("id"),
        "run_id": stored.get("run_id"),
        "trace_id": stored.get("trace_id"),
        "key": stored.get("key"),
        "score": stored.get("score"),
        "value": stored.get("value"),
        "comment": stored.get("comment"),
        "correction": stored.get("correction"),
        "created_at": created,
        "modified_at": created,
        "feedback_source": stored.get("feedback_source"),
    }


@app.get("/feedback")
async def list_feedback(
    request: Request, run_id: Optional[str] = None, key: Optional[str] = None
) -> list[dict]:
    return store(request).list_feedback(run_id=run_id, key=key)
