# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Every framework adapter has the same layout and the same public names, and the names and module paths of earlier
versions are gone (sam-link-sdk 0.1.0 keeps no compatibility names)."""

from __future__ import annotations

import importlib
import inspect
import pathlib

import pytest

SRC = pathlib.Path(__file__).resolve().parents[1] / "src" / "sam_link_sdk"

ADAPTERS = ("langchain", "openai_agents", "adk", "crewai", "strands", "agent_framework", "hermes")
FRAMEWORK = {
    "langchain": "langchain_core",
    "openai_agents": "agents",
    "adk": "google.adk",
    "crewai": "crewai",
    "strands": "strands",
    "agent_framework": "agent_framework",
    "hermes": None,
}
"""The framework each adapter needs installed; Hermes is not a published package, and its adapter imports without it."""

LAYOUT = {
    "__init__": "the adapter's public names, and the check that the installed framework still has what it uses",
    "steps": "reporting the framework's model and tool steps to the mesh task, and enforcing per-tool scopes",
    "stop": "making SAM's stop halt the framework's runs outside the mesh",
    "tools": "approvals, asking the user, and the card's tool manifest",
    "peer_tools": "other mesh agents as tools",
    "run": "running the framework on a mesh task",
}

REMOVED_MODULES = (
    "langchain.callbacks",
    "langchain.agent_loop",
    "langchain.hil_tools",
    "langchain.kill_switch",
    "openai_agents.hooks",
    "openai_agents.kill_switch",
    "adk.plugin",
    "adk.kill_switch",
    "crewai.listener",
    "crewai.kill_switch",
    "strands.hooks",
    "strands.kill_switch",
    "agent_framework.middleware",
    "agent_framework.kill_switch",
    "hermes.bridge",
    "hermes.runner",
    "hermes.kill_switch",
)
"""Module paths of earlier versions, removed before 0.1.0."""
REMOVED_NAMES = {
    "langchain": ("run_tool_calling_agent",),
    "crewai": ("run_crew", "guard_crew", "SamMeshListener", "MeshAgentTool"),
    "agent_framework": ("sam_mesh_middleware", "kill_switch_middleware"),
    "adk": ("ApprovalToolset", "AskUserToolset", "GuardedTool", "MeshAgentTool", "MeshAgentToolset"),
}
"""Names of earlier versions, removed before 0.1.0: adapter -> names."""


def _needs(adapter: str) -> None:
    if FRAMEWORK[adapter]:
        pytest.importorskip(FRAMEWORK[adapter])


@pytest.mark.parametrize("adapter", ADAPTERS)
def test_every_adapter_has_the_same_modules_and_no_others(adapter):
    assert {p.stem for p in (SRC / adapter).glob("*.py")} == set(LAYOUT)


@pytest.mark.parametrize("old", REMOVED_MODULES)
def test_a_module_path_of_an_earlier_version_is_gone(old):
    adapter = old.split(".")[0]
    _needs(adapter)
    importlib.import_module(f"sam_link_sdk.{adapter}")
    with pytest.raises(ModuleNotFoundError):
        importlib.import_module(f"sam_link_sdk.{old}")


@pytest.mark.parametrize("adapter,name", [(a, n) for a, names in REMOVED_NAMES.items() for n in names])
def test_a_name_of_an_earlier_version_is_gone(adapter, name):
    _needs(adapter)
    package = importlib.import_module(f"sam_link_sdk.{adapter}")
    assert not hasattr(package, name) and name not in package.__all__


def test_the_deprecation_machinery_is_gone():
    from sam_link_sdk import _adapter

    assert not hasattr(_adapter, "warn_deprecated")


def test_the_adk_run_agent_takes_no_message_keyword():
    _needs("adk")
    from sam_link_sdk.adk import run_agent

    assert "message" not in inspect.signature(run_agent).parameters


UNIFORM = (
    "run_agent",
    "step_reporter",
    "stop_guard",
    "tool_infos",
    "mesh_agent_tools",
    "with_approval",
    "ask_user_tool",
    "with_scopes",
)

NOT_SUPPORTED = {
    ("crewai", "step_reporter"): "CrewAI has one process-wide event bus: run_agent listens on it for the task it runs",
    ("hermes", "step_reporter"): "Hermes calls process-wide hooks: run_agent installs them and routes them to the task",
    ("hermes", "ask_user_tool"): "Hermes has its own clarify tool, which run_agent routes to SAM's question prompt",
    ("hermes", "with_scopes"): "Hermes builds its tools inside the agent (its registry), so there is no tool to wrap: "
    "tool_infos(scopes=...) declares them and the pre_tool_call hook enforces them",
}
"""Where a framework genuinely cannot have a name (README, "What each framework supports")."""

LEADING = {
    "run_agent": {
        "langchain": ["ctx", "model", "tools"],
        "openai_agents": ["ctx", "agent"],
        "adk": ["ctx", "runner"],
        "crewai": ["ctx", "build"],
        "strands": ["ctx", "build"],
        "agent_framework": ["ctx", "agent"],
        "hermes": ["ctx", "build"],
    },
    "step_reporter": {"*": ["ctx"], "openai_agents": ["ctx", "agent"], "adk": []},
    "stop_guard": {"*": ["agent"], "crewai": ["agent", "crew"]},
    "tool_infos": {"*": ["tools"]},
    "mesh_agent_tools": {"*": ["ctx", "agents"], "adk": ["agents"]},
    "with_approval": {"*": ["ctx", "tools"], "adk": ["tools"], "hermes": []},
    "ask_user_tool": {"*": ["ctx"], "adk": []},
    "with_scopes": {"*": ["tools", "scopes"]},
}
"""The leading (positional) parameters of each name. Where an adapter differs, the reason is the framework's shape: an ADK
agent and its tools are built once with the Runner rather than per task (no ctx); Hermes builds its tools inside the
agent, so its approvals are a policy for run_agent (no ctx, no tools); OpenAI Agents' hooks cannot see a tool fail or
refuse a call, so its step reporter prepares the agent it reports on (agent); CrewAI has no hook object to attach, so
stop_guard patches the crew.
"""
KEYWORD_ONLY = {
    "with_approval": ("names", "message"),
    "step_reporter": {"openai_agents": ("run_config",)},
    "tool_infos": {"hermes": ("scopes",)},
}
"""Keyword-only parameters each name must have, so a misplaced argument is a TypeError rather than a silent no-op."""
EXTRA_EXPORTS = {
    "langchain": {"SamLinkCallbackHandler", "StopGuard", "run_graph"},
    "openai_agents": {"SamLinkRunHooks", "StopHooks"},
    "adk": {"SamLinkPlugin", "StopPlugin"},
    "crewai": set(),
    "strands": {"SamLinkHooks", "StopHooks"},
    "agent_framework": set(),
    "hermes": {"StopGuard", "Approval", "MeshTool", "agent_defaults"},
}
"""Besides the uniform names, each adapter exports only the classes its step_reporter and stop_guard return (for
annotations); what runs raise is exported once, from sam_link_sdk; tools come as the framework's own tool types. Hermes has
no tool type of its own for the mesh's tools and approvals, so it exports those, and its agent's arguments for a mesh
task.
"""
RUN_KEYWORDS = ("input", "include_request_context")
"""run_agent's keyword arguments, the same in every adapter; `memory` where the framework does not keep the conversation
itself.
"""
OWN_MEMORY = {"adk": "ADK's session service keeps the conversation, keyed by the task's session"}


def _uniform_cases():
    return [(a, n) for a in ADAPTERS for n in UNIFORM if (a, n) not in NOT_SUPPORTED]


@pytest.mark.parametrize("adapter,name", _uniform_cases(), ids=[f"{a}.{n}" for a, n in _uniform_cases()])
def test_every_adapter_has_the_uniform_name_with_the_same_leading_parameters(adapter, name):
    _needs(adapter)
    package = importlib.import_module(f"sam_link_sdk.{adapter}")
    assert name in package.__all__
    function = getattr(package, name)
    spec = LEADING[name]
    expected = spec.get(adapter, spec.get("*"))
    params = inspect.signature(function).parameters
    positional = [p for p in params if params[p].kind in (params[p].POSITIONAL_ONLY, params[p].POSITIONAL_OR_KEYWORD)]
    if name in (
        "with_approval",
        "step_reporter",
        "stop_guard",
        "tool_infos",
        "ask_user_tool",
        "mesh_agent_tools",
        "with_scopes",
    ):
        assert positional == expected
    else:
        assert positional[: len(expected)] == expected
    keyword_only = KEYWORD_ONLY.get(name, ())
    keyword_only = keyword_only.get(adapter, ()) if isinstance(keyword_only, dict) else keyword_only
    assert all(params[p].kind == params[p].KEYWORD_ONLY for p in keyword_only), f"{adapter}.{name}: {keyword_only}"
    if name == "run_agent":
        assert inspect.iscoroutinefunction(function)
        assert inspect.signature(function).return_annotation in ("str", str)
        keywords = [p for p in params if params[p].kind == params[p].KEYWORD_ONLY]
        assert set(RUN_KEYWORDS) <= set(keywords), f"{adapter}.run_agent lacks {set(RUN_KEYWORDS) - set(keywords)}"
        assert ("memory" in keywords) == (adapter not in OWN_MEMORY)


@pytest.mark.parametrize("adapter", [a for a in ADAPTERS if (a, "with_scopes") not in NOT_SUPPORTED])
def test_a_scope_is_declared_only_by_with_scopes_so_it_cannot_be_declared_without_its_guard(adapter):
    """One way to declare a tool's scopes: with_scopes, which returns the tools guarded. tool_infos reads the scopes off
    the guarded tools and takes none of its own, so a scope cannot reach the card on a tool that does not check it."""
    _needs(adapter)
    package = importlib.import_module(f"sam_link_sdk.{adapter}")
    assert list(inspect.signature(package.tool_infos).parameters) == ["tools"]


@pytest.mark.parametrize("adapter,name", sorted(NOT_SUPPORTED))
def test_a_name_a_framework_cannot_have_is_absent_rather_than_faked(adapter, name):
    _needs(adapter)
    package = importlib.import_module(f"sam_link_sdk.{adapter}")
    assert not hasattr(package, name) and name not in package.__all__


@pytest.mark.parametrize("adapter", ADAPTERS)
def test_every_adapter_exports_the_uniform_names_and_only_its_own_types(adapter):
    _needs(adapter)
    package = importlib.import_module(f"sam_link_sdk.{adapter}")
    uniform = {n for n in UNIFORM if (adapter, n) not in NOT_SUPPORTED}
    assert set(package.__all__) == uniform | EXTRA_EXPORTS[adapter]
    assert [n for n in package.__all__ if not hasattr(package, n)] == []


def test_the_opentelemetry_adapter_has_the_adapters_vocabulary_where_it_is_honest():
    pytest.importorskip("opentelemetry.sdk")
    from sam_link_sdk import otel_steps

    assert inspect.iscoroutinefunction(otel_steps.run_agent)
    assert list(inspect.signature(otel_steps.run_agent).parameters) == ["ctx", "run", "input"]
    assert list(inspect.signature(otel_steps.step_reporter).parameters) == ["tracer_provider"]
    assert not hasattr(otel_steps, "instrument") and not hasattr(otel_steps, "run_with_otel")


def _approval_call(adapter):
    """The adapter's with_approval, its leading arguments, and one tool named route_claim of the framework's kind."""
    from sam_link_sdk.context import TaskContext

    package = importlib.import_module(f"sam_link_sdk.{adapter}")
    ctx = TaskContext(task_id="T", context_id="C", text="x", agent_name="claims-triage")

    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        return "fast-track"

    if adapter == "langchain":
        from langchain_core.tools import tool

        tools = [tool(route_claim)]
    elif adapter == "openai_agents":
        from agents import function_tool

        tools = [function_tool(route_claim)]
    elif adapter == "crewai":
        from crewai.tools import tool as crew_tool

        tools = [crew_tool("route_claim")(route_claim)]
    elif adapter == "strands":
        from strands import tool

        tools = [tool(route_claim)]
    elif adapter == "agent_framework":
        from agent_framework import tool

        tools = [tool(approval_mode="never_require")(route_claim)]
    else:
        tools = [route_claim]
    leading = {"adk": (tools,), "hermes": ()}.get(adapter, (ctx, tools))
    return package.with_approval, leading, tools


@pytest.mark.parametrize("adapter", ADAPTERS)
async def test_with_approval_refuses_names_given_wrongly_instead_of_guarding_nothing(adapter):
    """Names given wrongly are refused instead of guarding nothing.

    A bare string is refused rather than read as a list of names, since a string would be its characters.
    """
    _needs(adapter)
    with_approval, leading, tools = _approval_call(adapter)
    with pytest.raises(TypeError):
        with_approval(*leading, ["route_claim"])
    with pytest.raises(TypeError, match="list of tool names"):
        with_approval(*leading, names="route_claim")
    with pytest.raises(TypeError, match="tool names"):
        with_approval(*leading, names=tools)
    assert with_approval(*leading, names=["route_claim"]) is not None
