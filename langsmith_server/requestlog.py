"""Append-only transcript of every HTTP request the server receives.

This is the raw evidence layer: it records the exact wire traffic the LangSmith
SDK produces, decoded into something readable. The SQLite store is the queryable
view; this file is the audit trail.

Implemented as pure ASGI middleware rather than `BaseHTTPMiddleware` so the
request body can be captured and replayed byte-for-byte -- the multipart
endpoint reads its own body, and it must see the exact original stream.
"""

from __future__ import annotations

import json
import os
import threading
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional

from . import DEFAULT_DB_PATH
from .ingest import RawPart, parse_multipart

DEFAULT_MAX_CHARS = 200_000
DEFAULT_LOG_PATH = DEFAULT_DB_PATH.parent / "requests.log"
SEPARATOR = "=" * 78


def _boundary(content_type: str) -> Optional[str]:
    for chunk in content_type.split(";"):
        chunk = chunk.strip()
        if chunk.lower().startswith("boundary="):
            return chunk[len("boundary=") :].strip().strip('"')
    return None


def _truncate(text: str, limit: int) -> str:
    if len(text) <= limit:
        return text
    return f"{text[:limit]}\n... [truncated, {len(text) - limit} more characters]"


def _pretty_json(data: bytes) -> Optional[str]:
    try:
        return json.dumps(json.loads(data.decode("utf-8")), indent=2, ensure_ascii=False)
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None


def _format_part(part: RawPart, limit: int) -> list[str]:
    content_type = part.content_type or "unknown"
    header = f"[part] {part.name}  ({content_type}, {len(part.data)} bytes)"
    pretty = _pretty_json(part.data)
    if pretty is not None:
        return [header, _truncate(pretty, limit)]
    if part.name.startswith("attachment."):
        return [header, "  <binary omitted>"]
    return [header, f"  <{len(part.data)} bytes of {content_type}>"]


def format_body(content_type: str, body: bytes, limit: int = DEFAULT_MAX_CHARS) -> str:
    """Render a request/response body for the log."""
    if not body:
        return "<empty>"

    if "multipart/form-data" in content_type:
        boundary = _boundary(content_type)
        if not boundary:
            return f"<{len(body)} bytes of malformed multipart>"
        parts = parse_multipart(body, boundary)
        if not parts:
            return f"<{len(body)} bytes, no parsable parts>"
        lines: list[str] = [f"{len(parts)} part(s)"]
        for part in parts:
            lines.extend(_format_part(part, limit))
        return "\n".join(lines)

    if "json" in content_type or content_type == "":
        pretty = _pretty_json(body)
        if pretty is not None:
            return _truncate(pretty, limit)

    if content_type.startswith("text/") or "urlencoded" in content_type:
        return _truncate(body.decode("utf-8", "replace"), limit)

    return f"<{len(body)} bytes of {content_type}>"


def format_entry(
    *,
    method: str,
    path: str,
    query: str,
    status: int,
    duration_ms: float,
    content_type: str,
    user_agent: str,
    request_body: bytes,
    response_body: bytes,
    response_content_type: str,
    limit: int = DEFAULT_MAX_CHARS,
) -> str:
    stamp = datetime.now(timezone.utc).isoformat()
    target = f"{path}?{query}" if query else path
    lines = [
        SEPARATOR,
        f"{stamp}  {method} {target}  ->  {status}  ({duration_ms:.1f} ms)",
        f"content-type: {content_type or '<none>'}",
        f"user-agent:   {user_agent or '<none>'}",
        f"--- REQUEST ({len(request_body)} bytes) ---",
        format_body(content_type, request_body, limit),
        f"--- RESPONSE ({len(response_body)} bytes) ---",
        format_body(response_content_type, response_body, limit),
        "",
    ]
    return "\n".join(lines)


class RequestLogMiddleware:
    """Write a full request/response transcript to `log_path`."""

    def __init__(
        self,
        app: Any,
        log_path: Optional[str] = None,
        max_chars: int = DEFAULT_MAX_CHARS,
    ) -> None:
        self.app = app
        # Resolved lazily (on middleware-stack build, not at import) so callers
        # and tests can redirect or disable the log through the environment.
        # An env value of "" disables logging entirely.
        if log_path is None:
            log_path = os.environ.get("LANGSMITH_LOCAL_REQLOG", str(DEFAULT_LOG_PATH))
        self.log_path = Path(log_path) if log_path else None
        self.max_chars = max_chars
        self._lock = threading.Lock()
        if self.log_path is not None:
            self.log_path.parent.mkdir(parents=True, exist_ok=True)

    async def __call__(self, scope: dict, receive: Any, send: Any) -> None:
        if scope["type"] != "http" or self.log_path is None:
            await self.app(scope, receive, send)
            return

        started = time.perf_counter()
        request_chunks: list[bytes] = []

        async def receive_capture() -> dict:
            message = await receive()
            if message["type"] == "http.request":
                request_chunks.append(message.get("body", b""))
            return message

        status = 0
        response_headers: dict[str, str] = {}
        response_chunks: list[bytes] = []

        async def send_capture(message: dict) -> None:
            nonlocal status, response_headers
            if message["type"] == "http.response.start":
                status = message["status"]
                response_headers = {
                    k.decode("latin-1").lower(): v.decode("latin-1")
                    for k, v in message.get("headers", [])
                }
            elif message["type"] == "http.response.body":
                response_chunks.append(message.get("body", b""))
            await send(message)

        failed: Optional[BaseException] = None
        try:
            await self.app(scope, receive_capture, send_capture)
        except BaseException as exc:  # record the attempt, then re-raise
            failed = exc
            raise
        finally:
            duration_ms = (time.perf_counter() - started) * 1000
            headers = {
                k.decode("latin-1").lower(): v.decode("latin-1")
                for k, v in scope.get("headers", [])
            }
            entry = format_entry(
                method=scope.get("method", "?"),
                path=scope.get("path", "?"),
                query=scope.get("query_string", b"").decode("latin-1"),
                status=status or (500 if failed else 0),
                duration_ms=duration_ms,
                content_type=headers.get("content-type", ""),
                user_agent=headers.get("user-agent", ""),
                request_body=b"".join(request_chunks),
                response_body=b"".join(response_chunks),
                response_content_type=response_headers.get("content-type", ""),
                limit=self.max_chars,
            )
            if failed is not None:
                entry = entry.replace(
                    SEPARATOR + "\n",
                    f"{SEPARATOR}\n!! handler raised {type(failed).__name__}: {failed}\n",
                    1,
                )
            self._write(entry)

    def _write(self, entry: str) -> None:
        with self._lock:
            with self.log_path.open("a", encoding="utf-8") as handle:  # type: ignore[union-attr]
                handle.write(entry)
