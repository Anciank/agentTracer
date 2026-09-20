"""Shared by the example scripts: minimal .env loader, no dependencies.

Uses setdefault, so a real environment variable always beats the file.
"""

from __future__ import annotations

from pathlib import Path

DEFAULT_ENV = Path(__file__).resolve().parent.parent / ".env"


def load_env(path: Path = DEFAULT_ENV) -> None:
    import os

    if not path.exists():
        return
    for raw in path.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        value = value.strip().strip('"').strip("'")
        if value:  # don't let a bare `KEY=` shadow a real env var
            os.environ.setdefault(key.strip(), value)
