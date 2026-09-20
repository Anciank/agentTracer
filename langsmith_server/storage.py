from __future__ import annotations

import json
import sqlite3
import threading
import uuid
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterator, Optional

from . import DEFAULT_TENANT_ID

SCHEMA = """
CREATE TABLE IF NOT EXISTS sessions (
    id            TEXT PRIMARY KEY,
    name          TEXT,
    description   TEXT,
    extra         TEXT,
    start_time    TEXT,
    end_time      TEXT,
    tenant_id     TEXT NOT NULL,
    created_at    TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_name ON sessions(name);

CREATE TABLE IF NOT EXISTS runs (
    id              TEXT PRIMARY KEY,
    trace_id        TEXT,
    parent_run_id   TEXT,
    session_id      TEXT,
    session_name    TEXT,
    name            TEXT,
    run_type        TEXT,
    start_time      TEXT,
    end_time        TEXT,
    dotted_order    TEXT,
    status          TEXT,
    error           TEXT,
    data            TEXT NOT NULL,
    received_at     TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_runs_trace   ON runs(trace_id);
CREATE INDEX IF NOT EXISTS idx_runs_parent  ON runs(parent_run_id);
CREATE INDEX IF NOT EXISTS idx_runs_session ON runs(session_id);
CREATE INDEX IF NOT EXISTS idx_runs_start   ON runs(start_time);

CREATE TABLE IF NOT EXISTS feedback (
    id          TEXT PRIMARY KEY,
    run_id      TEXT,
    trace_id    TEXT,
    key         TEXT,
    score       REAL,
    value       TEXT,
    comment     TEXT,
    correction  TEXT,
    data        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    modified_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_feedback_run ON feedback(run_id);

CREATE TABLE IF NOT EXISTS attachments (
    run_id       TEXT NOT NULL,
    name         TEXT NOT NULL,
    content_type TEXT,
    size         INTEGER,
    data         BLOB,
    PRIMARY KEY (run_id, name)
);

CREATE TABLE IF NOT EXISTS ingest_log (
    seq       INTEGER PRIMARY KEY AUTOINCREMENT,
    at        TEXT NOT NULL,
    endpoint  TEXT NOT NULL,
    op        TEXT,
    run_id    TEXT,
    detail    TEXT
);
"""

# Fields that are only present on a partial (patch) payload and must never be
# allowed to blank out a previously stored value just because they are null.
MERGED_KEYS = (
    "name",
    "run_type",
    "start_time",
    "end_time",
    "dotted_order",
    "status",
    "error",
    "inputs",
    "outputs",
    "events",
    "extra",
    "serialized",
    "tags",
    "parent_run_id",
    "trace_id",
    "reference_example_id",
    "attachments",
)


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def to_iso(value: Any) -> Optional[str]:
    """Normalize a datetime or ISO-8601 string to a UTC ISO-8601 string."""
    if value is None:
        return None
    if isinstance(value, datetime):
        dt = value
    else:
        raw = str(value).strip()
        if not raw:
            return None
        try:
            dt = datetime.fromisoformat(raw.replace("Z", "+00:00"))
        except ValueError:
            return raw
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc).isoformat()


def parse_iso(value: Any) -> Optional[datetime]:
    if not value:
        return None
    try:
        dt = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt


def _dumps(value: Any) -> Optional[str]:
    if value is None:
        return None
    return json.dumps(value, default=str)


def _loads(value: Any) -> Any:
    if value is None:
        return None
    try:
        return json.loads(value)
    except (TypeError, ValueError):
        return value


class Storage:
    """SQLite-backed storage for sessions, runs, feedback and attachments."""

    def __init__(self, db_path: str | Path) -> None:
        self.db_path = Path(db_path)
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()
        with self._conn() as conn:
            conn.executescript(SCHEMA)

    @contextmanager
    def _conn(self) -> Iterator[sqlite3.Connection]:
        conn = sqlite3.connect(self.db_path, timeout=30.0)
        conn.row_factory = sqlite3.Row
        conn.execute("PRAGMA journal_mode=WAL")
        conn.execute("PRAGMA foreign_keys=ON")
        try:
            yield conn
            conn.commit()
        finally:
            conn.close()

    # ---------------------------------------------------------------- sessions

    def ensure_session(self, name: str, *, description: Optional[str] = None) -> str:
        """Return the id of the project called `name`, creating it if needed."""
        with self._lock, self._conn() as conn:
            row = conn.execute(
                "SELECT id FROM sessions WHERE name = ?", (name,)
            ).fetchone()
            if row:
                return row["id"]
            session_id = str(uuid.uuid4())
            conn.execute(
                """INSERT INTO sessions
                   (id, name, description, extra, start_time, end_time, tenant_id, created_at)
                   VALUES (?, ?, ?, NULL, ?, NULL, ?, ?)""",
                (session_id, name, description, now_iso(), DEFAULT_TENANT_ID, now_iso()),
            )
            return session_id

    def create_session(self, payload: dict) -> dict:
        name = payload.get("name")
        session_id = str(payload.get("id") or uuid.uuid4())
        with self._lock, self._conn() as conn:
            if name:
                existing = conn.execute(
                    "SELECT * FROM sessions WHERE name = ?", (name,)
                ).fetchone()
                if existing:
                    session_id = existing["id"]
                    if payload.get("description") is not None:
                        conn.execute(
                            "UPDATE sessions SET description = ?, extra = ? WHERE id = ?",
                            (
                                payload.get("description"),
                                _dumps(payload.get("extra")) or existing["extra"],
                                session_id,
                            ),
                        )
                    return self._session_row(conn, session_id)
            conn.execute(
                """INSERT OR REPLACE INTO sessions
                   (id, name, description, extra, start_time, end_time, tenant_id, created_at)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    session_id,
                    name,
                    payload.get("description"),
                    _dumps(payload.get("extra")),
                    to_iso(payload.get("start_time")) or now_iso(),
                    to_iso(payload.get("end_time")),
                    DEFAULT_TENANT_ID,
                    now_iso(),
                ),
            )
            return self._session_row(conn, session_id)

    def update_session(self, session_id: str, payload: dict) -> Optional[dict]:
        with self._lock, self._conn() as conn:
            row = conn.execute(
                "SELECT * FROM sessions WHERE id = ?", (session_id,)
            ).fetchone()
            if row is None:
                return None
            conn.execute(
                """UPDATE sessions
                   SET name = COALESCE(?, name),
                       description = COALESCE(?, description),
                       extra = COALESCE(?, extra),
                       end_time = COALESCE(?, end_time)
                   WHERE id = ?""",
                (
                    payload.get("name"),
                    payload.get("description"),
                    _dumps(payload.get("extra")),
                    to_iso(payload.get("end_time")),
                    session_id,
                ),
            )
            return self._session_row(conn, session_id)

    @staticmethod
    def _session_row(conn: sqlite3.Connection, session_id: str) -> dict:
        row = conn.execute("SELECT * FROM sessions WHERE id = ?", (session_id,)).fetchone()
        return Storage._session_to_api(row)

    @staticmethod
    def _session_to_api(row: sqlite3.Row) -> dict:
        return {
            "id": row["id"],
            "name": row["name"],
            "description": row["description"],
            "extra": _loads(row["extra"]),
            "start_time": row["start_time"],
            "end_time": row["end_time"],
            "tenant_id": row["tenant_id"],
            "reference_dataset_id": None,
        }

    def get_session(self, session_id: str) -> Optional[dict]:
        with self._conn() as conn:
            row = conn.execute("SELECT * FROM sessions WHERE id = ?", (session_id,)).fetchone()
        return self._session_to_api(row) if row else None

    def list_sessions(self, limit: int = 100, name: Optional[str] = None) -> list[dict]:
        sql = "SELECT * FROM sessions"
        params: list[Any] = []
        if name:
            sql += " WHERE name = ?"
            params.append(name)
        sql += " ORDER BY start_time DESC LIMIT ?"
        params.append(limit)
        with self._conn() as conn:
            rows = conn.execute(sql, params).fetchall()
        return [self._session_to_api(r) for r in rows]

    def delete_session(self, session_id: str) -> None:
        with self._lock, self._conn() as conn:
            conn.execute("DELETE FROM runs WHERE session_id = ?", (session_id,))
            conn.execute("DELETE FROM sessions WHERE id = ?", (session_id,))

    # -------------------------------------------------------------------- runs

    def upsert_run(self, payload: dict, *, endpoint: str = "runs/batch", op: str = "post") -> str:
        """Insert or merge a run. `op` is 'post' (create/upsert) or 'patch'."""
        run_id = str(payload.get("id"))
        if not run_id or run_id == "None":
            raise ValueError("run payload requires an 'id'")

        session_id = payload.get("session_id")
        session_name = payload.get("session_name")
        if not session_id and session_name:
            session_id = self.ensure_session(str(session_name))

        incoming = {k: v for k, v in payload.items() if k != "id"}

        with self._lock, self._conn() as conn:
            row = conn.execute("SELECT data FROM runs WHERE id = ?", (run_id,)).fetchone()
            if row is None:
                data = dict(incoming)
                if session_id:
                    data["session_id"] = session_id
                created = now_iso()
                conn.execute(
                    """INSERT INTO runs
                       (id, trace_id, parent_run_id, session_id, session_name, name, run_type,
                        start_time, end_time, dotted_order, status, error, data,
                        received_at, updated_at)
                       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                    (
                        run_id,
                        incoming.get("trace_id"),
                        incoming.get("parent_run_id"),
                        session_id,
                        session_name,
                        incoming.get("name"),
                        incoming.get("run_type"),
                        to_iso(incoming.get("start_time")),
                        to_iso(incoming.get("end_time")),
                        incoming.get("dotted_order"),
                        incoming.get("status"),
                        incoming.get("error"),
                        _dumps(data),
                        created,
                        created,
                    ),
                )
            else:
                data = _loads(row["data"]) or {}
                for key, value in incoming.items():
                    if value is None and op == "patch":
                        continue
                    if key in MERGED_KEYS and isinstance(data.get(key), dict) and isinstance(value, dict):
                        # `extra` arrives fragmented across post/patch; merge shallowly.
                        merged = dict(data[key])
                        merged.update(value)
                        data[key] = merged
                    else:
                        data[key] = value
                if session_id:
                    data.setdefault("session_id", session_id)
                conn.execute(
                    """UPDATE runs SET
                         trace_id      = COALESCE(?, trace_id),
                         parent_run_id = COALESCE(?, parent_run_id),
                         session_id    = COALESCE(?, session_id),
                         session_name  = COALESCE(?, session_name),
                         name          = COALESCE(?, name),
                         run_type      = COALESCE(?, run_type),
                         start_time    = COALESCE(?, start_time),
                         end_time      = COALESCE(?, end_time),
                         dotted_order  = COALESCE(?, dotted_order),
                         status        = COALESCE(?, status),
                         error         = COALESCE(?, error),
                         data          = ?,
                         updated_at    = ?
                       WHERE id = ?""",
                    (
                        incoming.get("trace_id"),
                        incoming.get("parent_run_id"),
                        session_id,
                        session_name,
                        incoming.get("name"),
                        incoming.get("run_type"),
                        to_iso(incoming.get("start_time")),
                        to_iso(incoming.get("end_time")),
                        incoming.get("dotted_order"),
                        incoming.get("status"),
                        incoming.get("error"),
                        _dumps(data),
                        now_iso(),
                        run_id,
                    ),
                )

            conn.execute(
                "INSERT INTO ingest_log (at, endpoint, op, run_id, detail) VALUES (?, ?, ?, ?, ?)",
                (now_iso(), endpoint, op, run_id, None),
            )
        return run_id

    def save_attachment(
        self, run_id: str, name: str, content_type: Optional[str], data: bytes
    ) -> None:
        with self._lock, self._conn() as conn:
            conn.execute(
                """INSERT OR REPLACE INTO attachments
                   (run_id, name, content_type, size, data) VALUES (?, ?, ?, ?, ?)""",
                (run_id, name, content_type, len(data), data),
            )
            row = conn.execute("SELECT data FROM runs WHERE id = ?", (run_id,)).fetchone()
            if row:
                run_data = _loads(row["data"]) or {}
                attachments = run_data.get("attachments") or {}
                attachments[name] = {"content_type": content_type, "size": len(data)}
                run_data["attachments"] = attachments
                conn.execute(
                    "UPDATE runs SET data = ?, updated_at = ? WHERE id = ?",
                    (_dumps(run_data), now_iso(), run_id),
                )

    def get_attachment(self, run_id: str, name: str) -> Optional[dict]:
        with self._conn() as conn:
            row = conn.execute(
                "SELECT * FROM attachments WHERE run_id = ? AND name = ?", (run_id, name)
            ).fetchone()
        return dict(row) if row else None

    @staticmethod
    def _run_to_api(row: sqlite3.Row) -> dict:
        data = _loads(row["data"]) or {}
        data.setdefault("id", row["id"])
        for key in ("trace_id", "parent_run_id", "session_id", "name", "run_type",
                    "start_time", "end_time", "dotted_order", "status", "error"):
            if data.get(key) is None and row[key] is not None:
                data[key] = row[key]
        data.setdefault("feedback_stats", None)
        return data

    def get_run(self, run_id: str) -> Optional[dict]:
        with self._conn() as conn:
            row = conn.execute("SELECT * FROM runs WHERE id = ?", (run_id,)).fetchone()
        return self._run_to_api(row) if row else None

    def delete_run(self, run_id: str) -> None:
        with self._lock, self._conn() as conn:
            conn.execute("DELETE FROM attachments WHERE run_id = ?", (run_id,))
            conn.execute("DELETE FROM feedback WHERE run_id = ?", (run_id,))
            conn.execute("DELETE FROM runs WHERE id = ?", (run_id,))

    def list_trace_ids(
        self, *, session: Optional[list[str]] = None, limit: int = 100
    ) -> list[str]:
        """Most recent trace ids first, without pulling every run into memory."""
        sql = "SELECT trace_id, MIN(start_time) AS started FROM runs WHERE trace_id IS NOT NULL"
        params: list[Any] = []
        if session:
            sql += f" AND session_id IN ({','.join('?' * len(session))})"
            params.extend(session)
        sql += " GROUP BY trace_id ORDER BY started DESC LIMIT ?"
        params.append(limit)
        with self._conn() as conn:
            rows = conn.execute(sql, params).fetchall()
        return [r["trace_id"] for r in rows]

    def query_runs(
        self,
        *,
        session: Optional[list[str]] = None,
        run_type: Optional[str] = None,
        ids: Optional[list[str]] = None,
        trace: Optional[str] = None,
        traces: Optional[list[str]] = None,
        parent_run: Optional[str] = None,
        name: Optional[str] = None,
        is_root: Optional[bool] = None,
        error: Optional[bool] = None,
        start_time: Optional[str] = None,
        limit: Optional[int] = 100,
        offset: int = 0,
    ) -> list[dict]:
        """Query runs. `limit=None` removes the row cap.

        Callers that have already bounded the result set another way -- the UI
        fetches runs for an explicit list of trace ids -- must pass None, or the
        row cap silently truncates the set behind their back.
        """
        clauses: list[str] = []
        params: list[Any] = []
        if session:
            clauses.append(f"session_id IN ({','.join('?' * len(session))})")
            params.extend(session)
        if run_type:
            clauses.append("run_type = ?")
            params.append(run_type)
        if ids:
            clauses.append(f"id IN ({','.join('?' * len(ids))})")
            params.extend(ids)
        if trace:
            clauses.append("trace_id = ?")
            params.append(trace)
        if traces is not None:
            if not traces:
                return []
            clauses.append(f"trace_id IN ({','.join('?' * len(traces))})")
            params.extend(traces)
        if parent_run:
            clauses.append("parent_run_id = ?")
            params.append(parent_run)
        if name:
            clauses.append("name = ?")
            params.append(name)
        if is_root is True:
            clauses.append("parent_run_id IS NULL")
        elif is_root is False:
            clauses.append("parent_run_id IS NOT NULL")
        if error is True:
            clauses.append("error IS NOT NULL")
        elif error is False:
            clauses.append("error IS NULL")
        if start_time:
            clauses.append("start_time >= ?")
            params.append(to_iso(start_time))

        sql = "SELECT * FROM runs"
        if clauses:
            sql += " WHERE " + " AND ".join(clauses)
        sql += " ORDER BY start_time ASC, received_at ASC"
        if limit is not None:
            sql += " LIMIT ? OFFSET ?"
            params.extend([limit, offset])
        elif offset:
            sql += " LIMIT -1 OFFSET ?"
            params.append(offset)

        with self._conn() as conn:
            rows = conn.execute(sql, params).fetchall()
        return [self._run_to_api(r) for r in rows]

    def delete_runs_for_session(self, session_id: str) -> None:
        with self._lock, self._conn() as conn:
            conn.execute("DELETE FROM runs WHERE session_id = ?", (session_id,))

    # ---------------------------------------------------------------- feedback

    def create_feedback(self, payload: dict) -> dict:
        feedback_id = str(payload.get("id") or uuid.uuid4())
        run_id = payload.get("run_id")
        trace_id = payload.get("trace_id")
        created = to_iso(payload.get("created_at")) or now_iso()
        with self._lock, self._conn() as conn:
            conn.execute(
                """INSERT OR REPLACE INTO feedback
                   (id, run_id, trace_id, key, score, value, comment, correction, data,
                    created_at, modified_at)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    feedback_id,
                    str(run_id) if run_id else None,
                    str(trace_id) if trace_id else None,
                    payload.get("key"),
                    payload.get("score"),
                    _dumps(payload.get("value")) if not isinstance(payload.get("value"), str) else payload.get("value"),
                    payload.get("comment"),
                    _dumps(payload.get("correction")),
                    _dumps({k: v for k, v in payload.items() if k not in ("id",)}),
                    created,
                    created,
                ),
            )
        return {**payload, "id": feedback_id, "created_at": created, "modified_at": created}

    def list_feedback(
        self, *, run_id: Optional[str] = None, key: Optional[str] = None, limit: int = 100
    ) -> list[dict]:
        clauses: list[str] = []
        params: list[Any] = []
        if run_id:
            clauses.append("run_id = ?")
            params.append(run_id)
        if key:
            clauses.append("key = ?")
            params.append(key)
        sql = "SELECT * FROM feedback"
        if clauses:
            sql += " WHERE " + " AND ".join(clauses)
        sql += " ORDER BY created_at DESC LIMIT ?"
        params.append(limit)
        with self._conn() as conn:
            rows = conn.execute(sql, params).fetchall()
        out = []
        for r in rows:
            item = {
                "id": r["id"],
                "run_id": r["run_id"],
                "trace_id": r["trace_id"],
                "key": r["key"],
                "score": r["score"],
                "value": r["value"],
                "comment": r["comment"],
                "correction": _loads(r["correction"]),
                "created_at": r["created_at"],
                "modified_at": r["modified_at"],
            }
            out.append(item)
        return out

    def stats(self) -> dict:
        with self._conn() as conn:
            runs = conn.execute("SELECT COUNT(*) c FROM runs").fetchone()["c"]
            sessions = conn.execute("SELECT COUNT(*) c FROM sessions").fetchone()["c"]
            feedback = conn.execute("SELECT COUNT(*) c FROM feedback").fetchone()["c"]
            by_type = conn.execute(
                "SELECT run_type, COUNT(*) c FROM runs GROUP BY run_type ORDER BY c DESC"
            ).fetchall()
        return {
            "runs": runs,
            "sessions": sessions,
            "feedback": feedback,
            "runs_by_type": {r["run_type"]: r["c"] for r in by_type},
        }
