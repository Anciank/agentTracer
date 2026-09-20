from __future__ import annotations

import json
import logging
from dataclasses import dataclass, field
from typing import Any, Optional

logger = logging.getLogger(__name__)

ZSTD_CONTENT_ENCODING = "zstd"


@dataclass
class RawPart:
    name: str
    headers: dict[str, str]
    data: bytes

    @property
    def content_type(self) -> Optional[str]:
        ct = self.headers.get("content-type")
        if not ct:
            return None
        # LangSmith appends "; length=<bytes>" for file-backed attachments.
        return ct.split(";")[0].strip() or None


@dataclass
class IngestBatch:
    """Normalized set of operations parsed out of one HTTP request."""

    creates: dict[str, dict] = field(default_factory=dict)
    updates: dict[str, dict] = field(default_factory=dict)
    feedback: list[dict] = field(default_factory=list)
    attachments: list[tuple[str, str, Optional[str], bytes]] = field(default_factory=list)

    def merge(self, other: "IngestBatch") -> "IngestBatch":
        for run_id, payload in other.creates.items():
            self.creates.setdefault(run_id, {}).update(payload)
        for run_id, payload in other.updates.items():
            self.updates.setdefault(run_id, {}).update(payload)
        self.feedback.extend(other.feedback)
        self.attachments.extend(other.attachments)
        return self

    @property
    def total_runs(self) -> int:
        return len(self.creates) + len(self.updates)


# --------------------------------------------------------------------- multipart


def _parse_content_disposition(value: str) -> tuple[Optional[str], Optional[str]]:
    name = filename = None
    for token in value.split(";"):
        token = token.strip()
        if token.lower().startswith("name="):
            name = token[5:].strip().strip('"')
        elif token.lower().startswith("filename="):
            filename = token[9:].strip().strip('"')
    return name, filename


def parse_multipart(body: bytes, boundary: str) -> list[RawPart]:
    """Parse a `multipart/form-data` body into parts.

    Hand-rolled rather than delegated to the framework's form parser because
    LangSmith packs raw binary attachments into parts that carry no `filename`,
    which a standard form parser would hand back as a lossily-decoded string.
    """
    delimiter = b"\r\n--" + boundary.encode("utf-8")
    segments = (b"\r\n" + body).split(delimiter)
    parts: list[RawPart] = []

    for segment in segments[1:]:
        if segment.startswith(b"--"):
            break  # closing delimiter
        if segment.startswith(b"\r\n"):
            segment = segment[2:]

        split_at = segment.find(b"\r\n\r\n")
        if split_at == -1:
            continue
        header_block, data = segment[:split_at], segment[split_at + 4 :]

        headers: dict[str, str] = {}
        for line in header_block.split(b"\r\n"):
            if b":" not in line:
                continue
            key, _, val = line.partition(b":")
            headers[key.decode("utf-8", "replace").strip().lower()] = val.decode(
                "utf-8", "replace"
            ).strip()

        disposition = headers.get("content-disposition", "")
        name, _filename = _parse_content_disposition(disposition)
        if not name:
            continue
        parts.append(RawPart(name=name, headers=headers, data=data))

    return parts


def maybe_decompress(body: bytes, content_encoding: Optional[str]) -> bytes:
    """Undo zstd body compression, which the SDK can enable for large traces."""
    if not content_encoding:
        return body
    encodings = [e.strip().lower() for e in content_encoding.split(",")]
    if ZSTD_CONTENT_ENCODING not in encodings:
        return body
    try:
        import zstandard

        return zstandard.ZstdDecompressor().decompressobj().decompress(body)
    except ImportError:  # pragma: no cover
        raise ValueError(
            "received a zstd-compressed body but the 'zstandard' package is not installed"
        )


# ----------------------------------------------------------------- payload shape


def _decode_json(data: bytes) -> Any:
    if not data:
        return None
    try:
        return json.loads(data.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        logger.warning("Dropping unparsable JSON part (%d bytes): %s", len(data), exc)
        return None


def batch_from_parts(parts: list[RawPart]) -> IngestBatch:
    """Turn LangSmith multipart parts into a normalized batch.

    Part naming (see langsmith/_internal/_operations.py):
      post.<run_id>[.<field>]      run create, optionally one fragmented field
      patch.<run_id>[.<field>]     run update, optionally one fragmented field
      feedback.<feedback_id>       feedback create
      attachment.<run_id>.<name>   raw attachment body
    """
    batch = IngestBatch()

    for part in parts:
        name = part.name
        if name.startswith("attachment."):
            _, _, rest = name.partition(".")
            run_id, _, att_name = rest.partition(".")
            if run_id and att_name:
                batch.attachments.append(
                    (run_id, att_name, part.content_type, part.data)
                )
            continue

        if name.startswith("feedback."):
            payload = _decode_json(part.data)
            if isinstance(payload, dict):
                batch.feedback.append(payload)
            continue

        op, _, rest = name.partition(".")
        if op not in ("post", "patch") or not rest:
            logger.debug("Ignoring unrecognized multipart part %r", name)
            continue

        run_id, _, sub_field = rest.partition(".")
        payload = _decode_json(part.data)
        if payload is None:
            continue

        target = batch.creates if op == "post" else batch.updates
        entry = target.setdefault(run_id, {})
        if sub_field:
            # Fragmented fields carry whatever JSON type they hold: `error` is a
            # bare string, while `inputs`/`outputs`/`extra` are objects.
            entry[sub_field] = payload
        elif isinstance(payload, dict):
            entry.update(payload)

    return batch


def batch_from_json(body: dict) -> IngestBatch:
    """Parse the `POST /runs/batch` body: `{"post": [run, ...], "patch": [...]}`."""
    batch = IngestBatch()
    for op, target in (("post", batch.creates), ("patch", batch.updates)):
        payloads = body.get(op) or []
        if isinstance(payloads, dict):
            payloads = [payloads]
        for payload in payloads:
            if not isinstance(payload, dict):
                continue
            run_id = str(payload.get("id") or "")
            if not run_id:
                logger.warning("Skipping %s payload without an id", op)
                continue
            target.setdefault(run_id, {}).update(payload)
    return batch
