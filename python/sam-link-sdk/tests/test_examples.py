# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The claims-triage examples are one agent in every framework: the same tools, card and structure, joined through the
agent link, and each runs against a scripted model where the framework can be driven offline."""

from __future__ import annotations

import ast
import importlib.util
import os
import pathlib
import re

import pytest

from sam_link_sdk.events import ModelFinished, Progress, ToolFinished

from .adapter_task import adapter_task
from .conftest import EXAMPLES, load_example

TRIAGE = {
    "claims_triage_langchain": "langchain_core",
    "claims_triage_langgraph": "langgraph",
    "claims_triage_openai_agents": "agents",
    "claims_triage_adk": "google.adk",
    "claims_triage_crewai": "crewai",
    "claims_triage_strands": "strands",
    "claims_triage_agent_framework": "agent_framework",
    "claims_triage_hermes": "run_agent",
    "claims_triage_otel": "opentelemetry.sdk",
}
TOOL_NAMES = ["lookup_policy", "route_claim", "fraud_screen"]
JOIN = "MeshAgent(config, make_handler(build_model(settings), settings))"
DEPRECATED = re.compile(
    r"\b(run_crew|run_tool_calling_agent|guard_crew|sam_mesh_middleware|kill_switch_middleware|"
    r"run_with_otel|track_failures)\b|\bwith_approval\(\s*ctx\s*,\s*names\s*="
)


@pytest.fixture(autouse=True)
def environment_restored():
    """The CrewAI example turns off CrewAI's (and OpenTelemetry's) export in os.environ as it loads, as it should when
    run; the rest of the suite must not inherit that."""
    saved = dict(os.environ)
    yield
    os.environ.clear()
    os.environ.update(saved)


def test_every_framework_has_a_claims_triage_example():
    assert sorted(p.stem for p in EXAMPLES.glob("claims_triage_*.py")) == sorted(TRIAGE)


@pytest.mark.parametrize("name", sorted(TRIAGE))
def test_each_example_has_the_same_order_entry_points_and_way_of_joining(name):
    source = (EXAMPLES / f"{name}.py").read_text()
    functions = [n.name for n in ast.parse(source).body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))]
    assert [f for f in functions if f in ("build_model", "make_handler", "main")] == [
        "build_model",
        "make_handler",
        "main",
    ]
    assert source.index("TOOLS") < source.index("def build_model")
    calls = [n for n in ast.walk(ast.parse(source)) if isinstance(n, ast.Call)]
    assert JOIN in {ast.unparse(n) for n in calls}, "joined through the agent link, with the shared settings and card"
    assert "from claims_common import" in source
    assert any(ast.unparse(n).startswith("agent_config(settings,") for n in calls)
    assert "Environment and scenario: examples/claims_common.py." in source


@pytest.mark.parametrize("path", sorted(p for p in EXAMPLES.rglob("*.py")), ids=lambda p: str(p.relative_to(EXAMPLES)))
def test_every_example_compiles_and_uses_no_deprecated_name(path):
    source = path.read_text()
    compile(source, str(path), "exec")
    assert not DEPRECATED.search(source)
    assert _missing_sam_link_names(source) == []


def _missing_sam_link_names(source: str) -> list[str]:
    """The sam_link_sdk modules the source imports that do not exist, and the names it imports from the package itself
    that it does not have."""
    import sam_link_sdk

    missing = []
    for node in ast.walk(ast.parse(source)):
        if isinstance(node, ast.ImportFrom) and node.module and node.module.split(".")[0] == "sam_link_sdk":
            if importlib.util.find_spec(node.module) is None:
                missing.append(node.module)
            elif node.module == "sam_link_sdk":
                missing += [f"sam_link_sdk.{a.name}" for a in node.names if not hasattr(sam_link_sdk, a.name)]
        elif isinstance(node, ast.Import):
            missing += [
                a.name
                for a in node.names
                if a.name.split(".")[0] == "sam_link_sdk" and importlib.util.find_spec(a.name) is None
            ]
    return missing


@pytest.mark.parametrize("name", sorted(TRIAGE))
def test_each_example_imports_and_offers_the_same_tools(name):
    if importlib.util.find_spec(TRIAGE[name].split(".")[0]) is None or importlib.util.find_spec(TRIAGE[name]) is None:
        pytest.skip(f"{TRIAGE[name]} is not installed")
    example = load_example(name)
    if name == "claims_triage_hermes":
        names = [t[0] for t in example.TOOLS]
    elif name == "claims_triage_otel":
        names = [t.tool_name for t in example.TOOLS]
    else:
        names = [t.name for t in importlib.import_module(_adapter_of(name)).tool_infos(example.TOOLS)]
    assert names == TOOL_NAMES


def _adapter_of(name: str) -> str:
    framework = name.removeprefix("claims_triage_")
    return f"sam_link_sdk.{'langchain' if framework == 'langgraph' else framework}"


def test_the_shared_settings_read_one_set_of_variables(monkeypatch):
    common = load_example("claims_common")
    for key, value in {
        "SAM_AGENT_NAME": "claims-b",
        "LLM_SERVICE_ENDPOINT": "https://llm.example.com/v1",
        "LLM_SERVICE_API_KEY": "k",
        "LLM_SERVICE_GENERAL_MODEL_NAME": "m1",
        "CLAIMS_LLM_MODEL": "m2",
        "CLAIMS_PEER_AGENTS": "*",
        "CLAIMS_APPROVAL_TOOLS": "route_claim, fraud_screen",
        "CLAIMS_ASK_USER": "true",
    }.items():
        monkeypatch.setenv(key, value)
    s = common.Settings.from_env()
    assert (s.agent_name, s.llm_base_url, s.llm_model, s.use_peers, s.peer_agents, s.approval_tools, s.ask_user) == (
        "claims-b",
        "https://llm.example.com/v1",
        "m2",
        True,
        None,
        ("route_claim", "fraud_screen"),
        True,
    )
    monkeypatch.setenv("CLAIMS_PEER_AGENTS", "policy-desk, regulations")
    assert common.Settings.from_env().peer_agents == ("policy-desk", "regulations")
    monkeypatch.delenv("CLAIMS_PEER_AGENTS")
    assert not common.Settings.from_env().use_peers


TURNS = [
    ("", [("call_a", "lookup_policy", {"policy_id": "P-1182"})], (300, 20)),
    ("", [("call_b", "route_claim", {"policy_id": "P-1182", "queue": "fast-track"})], (340, 25)),
    ("P-1182 is routed to fast-track.", [], (380, 15)),
]


def langchain_model():
    from langchain_core.messages import AIMessage

    from .test_callbacks import ToolCallingFake

    return ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content=t,
                    tool_calls=[{"id": c, "name": n, "args": a} for c, n, a in calls],
                    usage_metadata={"input_tokens": i, "output_tokens": o, "total_tokens": i + o},
                )
                for t, calls, (i, o) in TURNS
            ]
        )
    )


def openai_agents_model():
    from .test_openai_agents import ScriptedModel, call, message, usage

    return ScriptedModel(
        [
            ([*([message(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(i, o))
            for t, calls, (i, o) in TURNS
        ]
    )


def strands_model():
    from .test_strands import ScriptedModel, call, text, usage

    return ScriptedModel(
        [
            ([*([text(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(i, o))
            for t, calls, (i, o) in TURNS
        ]
    )


def agent_framework_model():
    from .test_agent_framework import ScriptedClient, call, text, usage

    return ScriptedClient(
        [
            ([*([text(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(i, o))
            for t, calls, (i, o) in TURNS
        ]
    )


def adk_model():
    from google.genai import types

    from .test_adk import Scripted, fc, usage

    return Scripted(
        turns=[
            ([*([types.Part(text=t)] if t else []), *(fc(n, a, call_id=c) for c, n, a in calls)], usage(i, o))
            for t, calls, (i, o) in TURNS
        ]
    )


RUNNABLE = {
    "claims_triage_langchain": langchain_model,
    "claims_triage_openai_agents": openai_agents_model,
    "claims_triage_strands": strands_model,
    "claims_triage_agent_framework": agent_framework_model,
    "claims_triage_adk": adk_model,
}


@pytest.mark.parametrize("name", sorted(RUNNABLE))
async def test_each_example_triages_a_claim_on_a_scripted_model(name):
    """claims_triage_langgraph and claims_triage_otel run in test_langgraph and test_otel_steps. Not run here:
    claims_triage_crewai (a real crew needs a model CrewAI can call; its adapter is driven at the event bus in
    test_crewai) and claims_triage_hermes (Hermes is not installed; test_hermes drives the adapter with Hermes).
    """
    pytest.importorskip(TRIAGE[name])
    example = load_example(name)
    async with adapter_task(text="Triage claim P-1182") as task:
        answer = await example.make_handler(RUNNABLE[name]())(task.ctx)
        sent = list(task.events)

    assert answer == "P-1182 is routed to fast-track."
    assert sent[0] == Progress("Triaging claim")
    results = [(e.name, e.call_id, e.result) for e in sent if isinstance(e, ToolFinished)]
    assert [(name, data.get("tier") or data.get("queue")) for name, _, data in results] == [
        ("lookup_policy", "gold"),
        ("route_claim", "fast-track"),
    ]
    asked = [c.id for e in sent if isinstance(e, ModelFinished) for c in e.tool_calls]
    assert asked == [call_id for _, call_id, _ in results]
    assert task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 1020


def test_every_example_is_documented():
    page = (pathlib.Path(__file__).resolve().parents[1] / "docs" / "examples.md").read_text()
    for name in [*TRIAGE, "claims_common", "claims_intake"]:
        assert f"{name}.py" in page
