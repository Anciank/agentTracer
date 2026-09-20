"""Read-only JSON endpoints plus the waterfall page, served from local data.

Kept separate from the LangSmith-compatible surface in `app.py`: these routes
(`/ui/*`) are ours and carry no compatibility obligation.
"""

from __future__ import annotations

from pathlib import Path
from typing import Optional

from fastapi import APIRouter, Query, Request
from fastapi.responses import FileResponse, JSONResponse

from .storage import Storage
from .traces import trace_detail, trace_summaries

STATIC_DIR = Path(__file__).resolve().parent / "static"
INDEX_HTML = STATIC_DIR / "index.html"
LOGO_SVG = STATIC_DIR / "logo.svg"
FAVICON_SVG = STATIC_DIR / "favicon.svg"

router = APIRouter(prefix="/ui", tags=["ui"])


def _store(request: Request) -> Storage:
    return request.app.state.storage


def _resolve_sessions(
    storage: Storage, session: Optional[str]
) -> Optional[list[str]]:
    """Map a project name to ids, or None for 'all projects'."""
    if not session:
        return None
    matches = storage.list_sessions(limit=100, name=session)
    # An unknown project name yields [] rather than None so the caller returns
    # an empty result instead of silently showing every project's traces.
    return [s["id"] for s in matches]


def _svg(path: Path) -> FileResponse:
    return FileResponse(path, media_type="image/svg+xml")


@router.get("", include_in_schema=False)
async def index() -> FileResponse:
    return FileResponse(INDEX_HTML, media_type="text/html")


@router.get("/logo.svg", include_in_schema=False)
async def logo() -> FileResponse:
    return _svg(LOGO_SVG)


@router.get("/favicon.svg", include_in_schema=False)
async def favicon() -> FileResponse:
    return _svg(FAVICON_SVG)


@router.get("/projects")
async def projects(request: Request) -> list[str]:
    names = [s["name"] for s in _store(request).list_sessions(limit=200) if s["name"]]
    return sorted(names)


@router.get("/traces")
async def traces(
    request: Request,
    session: Optional[str] = None,
    limit: int = Query(100, ge=1, le=1000),
) -> list[dict]:
    storage = _store(request)
    session_ids = _resolve_sessions(storage, session)
    if session_ids == []:
        return []
    trace_ids = storage.list_trace_ids(session=session_ids, limit=limit)
    # limit=None: the trace-id list already bounds this, and leaving the default
    # row cap on would silently drop most of the traces from the summary.
    runs = storage.query_runs(traces=trace_ids, limit=None)
    return trace_summaries(runs)


@router.get("/traces/{trace_id}")
async def trace(request: Request, trace_id: str) -> JSONResponse:
    runs = _store(request).query_runs(trace=trace_id)
    detail = trace_detail(trace_id, runs)
    if detail is None:
        return JSONResponse({"detail": "trace not found"}, status_code=404)
    return JSONResponse(detail)
