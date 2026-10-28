"""Compat suite hits a live Go agentTracer (AGENTTRACER_BASE_URL + AGENTTRACER_API_KEY)."""
from __future__ import annotations

import os
import sys
from pathlib import Path

import pytest
import httpx

# Allow `from helpers import ...` when running from repo root or compat/
sys.path.insert(0, str(Path(__file__).resolve().parent))


@pytest.fixture(scope="session")
def base_url() -> str:
    url = os.environ.get("AGENTTRACER_BASE_URL", "http://127.0.0.1:1984").rstrip("/")
    return url


@pytest.fixture(scope="session")
def api_key() -> str:
    key = os.environ.get("AGENTTRACER_API_KEY", "")
    if not key:
        path = os.environ.get("AGENTTRACER_API_KEY_FILE", "")
        if path and os.path.isfile(path):
            key = open(path).read().strip()
    if not key:
        pytest.skip("AGENTTRACER_API_KEY not set")
    return key


@pytest.fixture(scope="session")
def client(base_url: str, api_key: str) -> httpx.Client:
    c = httpx.Client(base_url=base_url, headers={"x-api-key": api_key}, timeout=30.0)
    r = c.get("/health")
    if r.status_code != 200:
        pytest.skip(f"server not healthy: {r.status_code}")
    return c
