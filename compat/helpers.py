from __future__ import annotations

import gzip
import json
from uuid import uuid4

import httpx

BOUNDARY = "boundary123"


def encode_multipart(parts: list[tuple[str, bytes, str]]) -> bytes:
    out = bytearray()
    for name, data, content_type in parts:
        out += f"--{BOUNDARY}\r\n".encode()
        out += f'Content-Disposition: form-data; name="{name}"\r\n'.encode()
        out += f"Content-Type: {content_type}\r\n\r\n".encode()
        out += data
        out += b"\r\n"
    out += f"--{BOUNDARY}--\r\n".encode()
    return bytes(out)


def gzip_bytes(data: bytes) -> bytes:
    return gzip.compress(data)


def new_run(**extra) -> dict:
    rid = str(uuid4())
    body = {
        "id": rid,
        "trace_id": rid,
        "name": "root",
        "run_type": "chain",
        "start_time": "2026-01-01T00:00:00+00:00",
        "session_name": "compat-default",
    }
    body.update(extra)
    if "id" in extra and "trace_id" not in extra:
        body["trace_id"] = extra["id"]
    return body


def assert_ok_ingest(r: httpx.Response, *, multipart: bool = False):
    """Multipart returns 204; JSON ingest returns 202."""
    if multipart:
        assert r.status_code == 204, r.text
    else:
        assert r.status_code in (200, 202), r.text
