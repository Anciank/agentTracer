"""A local, LangSmith-compatible trace ingestion server.

Implements the subset of the LangSmith API that the official SDKs use when
tracing, backed by a single SQLite file. No cloud, no auth, no UI.
"""

from pathlib import Path

__version__ = "0.1.0"

DEFAULT_TENANT_ID = "00000000-0000-0000-0000-000000000001"
SERVER_VERSION = "0.1.0-local"

#: Where traces are stored unless LANGSMITH_LOCAL_DB overrides it.
DEFAULT_DB_PATH = Path(__file__).resolve().parent.parent / "data" / "langsmith.db"
