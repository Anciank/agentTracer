"""Real langsmith SDK (RunTree) against Go /runs/multipart."""
from __future__ import annotations

import os
import time
from uuid import uuid4

import httpx
import pytest


@pytest.fixture()
def sdk_env(base_url: str, api_key: str, monkeypatch):
    monkeypatch.setenv("LANGSMITH_TRACING", "true")
    monkeypatch.setenv("LANGSMITH_ENDPOINT", base_url)
    monkeypatch.setenv("LANGSMITH_API_KEY", api_key)
    monkeypatch.setenv("LANGSMITH_PROJECT", f"sdk-smoke-{uuid4().hex[:8]}")
    # force multipart
    monkeypatch.setenv("LANGSMITH_RUNS_ENDPOINTS", "")
    yield


def test_run_tree_nested(sdk_env, client: httpx.Client, base_url: str, api_key: str):
    langsmith = pytest.importorskip("langsmith")
    from langsmith import RunTree

    project = os.environ["LANGSMITH_PROJECT"]
    root = RunTree(name="sdk-root", run_type="chain", inputs={"q": "hi"}, project_name=project)
    child = root.create_child(name="sdk-child", run_type="llm", inputs={"prompt": "hi"})
    child.end(outputs={"text": "echo: hi"})
    root.end(outputs={"answer": "echo: hi"})
    # post entire tree; also post child explicitly for older SDKs
    child.post()
    root.post()
    from langsmith import Client
    client = Client(api_url=base_url, api_key=api_key)
    for _ in range(5):
        try:
            client.flush()
        except Exception:
            pass
        time.sleep(0.4)
    time.sleep(1.0)

    # Find via UI projects / traces
    projects = httpx.get(f"{base_url}/ui/projects", timeout=30).json()
    assert project in projects or any(project in p for p in projects)

    traces = httpx.get(f"{base_url}/ui/traces", params={"session": project}, timeout=30).json()
    assert traces, f"no traces for {project}: projects={projects}"
    detail = httpx.get(f"{base_url}/ui/traces/{traces[0]['trace_id']}", timeout=30).json()
    names = {r["name"] for r in detail["rows"]}
    assert "sdk-root" in names
    assert "sdk-child" in names


def test_langchain_fake_llm(sdk_env, client: httpx.Client, base_url: str, api_key: str):
    pytest.importorskip("langchain_core")
    langsmith = pytest.importorskip("langsmith")
    from langchain_core.language_models.fake import FakeListLLM
    from langchain_core.prompts import ChatPromptTemplate
    from langsmith import traceable

    project = os.environ["LANGSMITH_PROJECT"]

    @traceable(name="echo-chain", project_name=project)
    def run_echo(text: str) -> str:
        llm = FakeListLLM(responses=[f"echo:{text}"])
        return llm.invoke(text)

    out = run_echo("ping")
    assert "ping" in out
    try:
        from langsmith import Client
        Client(api_url=base_url, api_key=api_key).flush()
    except Exception:
        time.sleep(1.5)
    time.sleep(1.0)

    traces = httpx.get(f"{base_url}/ui/traces", params={"session": project}, timeout=30).json()
    assert traces, "langchain trace missing"
