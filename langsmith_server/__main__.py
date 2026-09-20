from __future__ import annotations

import argparse
import logging
import os
from pathlib import Path

import uvicorn

from . import DEFAULT_DB_PATH
from .requestlog import DEFAULT_LOG_PATH


def configure_logging(log_file: str) -> None:
    """Send server logs to stdout and, when asked, to a file as well."""
    handlers: list[logging.Handler] = [logging.StreamHandler()]
    if log_file:
        path = Path(log_file)
        path.parent.mkdir(parents=True, exist_ok=True)
        handlers.append(logging.FileHandler(path))

    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)-8s %(name)s: %(message)s",
        handlers=handlers,
        force=True,
    )
    # uvicorn installs its own handlers; point them at ours so nothing is lost.
    for name in ("uvicorn", "uvicorn.error", "uvicorn.access"):
        uvicorn_logger = logging.getLogger(name)
        uvicorn_logger.handlers = handlers
        uvicorn_logger.propagate = False


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="langsmith-local",
        description="Run the local LangSmith-compatible trace server.",
    )
    parser.add_argument(
        "--host", default=os.environ.get("LANGSMITH_LOCAL_HOST", "127.0.0.1")
    )
    parser.add_argument(
        "--port", type=int, default=int(os.environ.get("LANGSMITH_LOCAL_PORT", "1984"))
    )
    parser.add_argument(
        "--db",
        default=os.environ.get("LANGSMITH_LOCAL_DB"),
        help=f"SQLite file for traces (default: {DEFAULT_DB_PATH})",
    )
    parser.add_argument(
        "--request-log",
        default=os.environ.get("LANGSMITH_LOCAL_REQLOG"),
        help=(
            "File for the full request/response transcript, including payloads "
            f"(default: {DEFAULT_LOG_PATH}; empty string disables it)"
        ),
    )
    parser.add_argument(
        "--log-file",
        default=os.environ.get("LANGSMITH_LOCAL_SERVER_LOG"),
        help="Also write server logs (not the request transcript) to this file",
    )
    parser.add_argument("--reload", action="store_true", help="auto-reload on code changes")
    args = parser.parse_args()

    if args.db:
        os.environ["LANGSMITH_LOCAL_DB"] = args.db
    if args.request_log is not None:
        os.environ["LANGSMITH_LOCAL_REQLOG"] = args.request_log

    configure_logging(args.log_file)

    uvicorn.run(
        "langsmith_server.app:app",
        host=args.host,
        port=args.port,
        reload=args.reload,
        log_config=None,  # keep the handlers configured above
    )


if __name__ == "__main__":
    main()
