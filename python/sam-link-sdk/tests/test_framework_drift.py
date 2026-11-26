# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Adapters fail at import, with one clear message, when their framework lost a hook or event they depend on."""

from __future__ import annotations

import dataclasses
import importlib
import sys
import types

import pytest

import sam_link_sdk
from sam_link_sdk._drift import FrameworkDrift, require, require_members, resolve, resolves


@dataclasses.dataclass
class _Event:
    call_id: str = dataclasses.field(default_factory=str)


class _Model:
    """Declares its fields the way a pydantic model does, so the module runs in a compatibility cell whose framework
    brings no pydantic."""

    model_fields = {"started_event_id": None}


@pytest.fixture
def fake_framework(monkeypatch):
    module = types.ModuleType("fakefw")
    module.Event, module.Model = _Event, _Model

    class Hooks:
        def on_tool_end(self):
            pass

    module.Hooks = Hooks
    monkeypatch.setitem(sys.modules, "fakefw", module)
    return module


def test_symbols_resolve_through_attributes_dataclass_fields_and_pydantic_fields(fake_framework):
    assert resolves("fakefw:Hooks.on_tool_end")
    assert resolves("fakefw:Event.call_id")
    assert resolves("fakefw:Model.started_event_id")
    assert not resolves("fakefw:Hooks.on_tool_finished")
    assert not resolves("not_a_framework_module:Thing")


def test_a_missing_symbol_names_the_framework_the_installed_version_and_every_missing_symbol(fake_framework):
    with pytest.raises(FrameworkDrift) as raised:
        require(
            "sam_link_sdk.fake",
            "FakeFW",
            "pytest",
            "1.0.0",
            ["fakefw:Hooks.on_tool_end", "fakefw:Hooks.on_tool_finished", "fakefw:Model.call_sequence"],
        )
    message = str(raised.value)
    assert message.startswith("sam_link_sdk.fake cannot use FakeFW ")
    assert importlib.metadata.version("pytest") in message
    assert "fakefw.Hooks.on_tool_finished, fakefw.Model.call_sequence" in message
    assert "tested with FakeFW 1.0.0" in message
    assert raised.value.missing == ["fakefw.Hooks.on_tool_finished", "fakefw.Model.call_sequence"]


def test_a_hook_renamed_between_supported_releases_is_present_under_either_name(fake_framework):
    renamed = "fakefw:Hooks.on_tool_finished | fakefw:Hooks.on_tool_end"
    assert resolves(renamed)
    assert resolve(renamed) is fake_framework.Hooks.on_tool_end
    require("sam_link_sdk.fake", "FakeFW", "pytest", "1.0.0", [renamed])


def test_alternatives_the_installed_release_has_none_of_are_drift_naming_all_of_them(fake_framework):
    with pytest.raises(FrameworkDrift) as raised:
        require("sam_link_sdk.fake", "FakeFW", "pytest", "1.0.0", ["fakefw:Hooks.on_start | fakefw:Hooks.on_begin"])
    assert raised.value.missing == ["fakefw.Hooks.on_start | fakefw.Hooks.on_begin"]
    with pytest.raises(LookupError):
        resolve("fakefw:Hooks.on_start | fakefw:Hooks.on_begin")


def test_a_hook_moved_to_another_module_imports_whichever_module_the_release_has(fake_framework):
    require(
        "sam_link_sdk.fake",
        "FakeFW",
        "pytest",
        "1.0.0",
        ["fakefw_old.hooks:Hooks.on_tool_end | fakefw:Hooks.on_tool_end"],
    )
    with pytest.raises(ImportError):
        require("sam_link_sdk.fake", "FakeFW", "pytest", "1.0.0", ["fakefw_old:Hooks | fakefw_older:Hooks"])


def test_a_framework_that_is_not_installed_is_an_import_error_not_drift():
    with pytest.raises(ImportError) as raised:
        require("sam_link_sdk.fake", "FakeFW", "fakefw-dist", "1.0.0", ["not_a_framework_module:Thing"])
    assert not isinstance(raised.value, FrameworkDrift)


def test_members_checked_at_first_use_name_the_object(fake_framework):
    require_members(
        "sam_link_sdk.fake", "FakeFW", "fakefw-dist", "1.0.0", fake_framework.Hooks(), "Hooks", ["on_tool_end"]
    )
    with pytest.raises(FrameworkDrift, match=r"FakeFW \(version unknown\): it no longer has agent\._persist_disabled"):
        require_members("sam_link_sdk.fake", "FakeFW", "fakefw-dist", "1.0.0", object(), "agent", ["_persist_disabled"])


_DRIFT_CASES = [
    ("sam_link_sdk.openai_agents", "agents.tool:resolve_function_tool_failure_error_function"),
    ("sam_link_sdk.crewai", "crewai.events.types.tool_usage_events:ToolUsageFinishedEvent.output"),
    ("sam_link_sdk.strands", "strands.hooks:BeforeToolCallEvent.cancel_tool"),
    ("sam_link_sdk.adk", "google.adk.plugins.base_plugin:BasePlugin.on_tool_error_callback"),
    ("sam_link_sdk.agent_framework", "agent_framework:Agent.run"),
    ("sam_link_sdk.langchain", "langchain_core.callbacks:AsyncCallbackHandler.on_tool_error"),
    ("sam_link_sdk.langchain", "langgraph.errors:GraphInterrupt"),
    ("sam_link_sdk.openai_agents", "agents:FunctionTool._agent_instance"),
    ("sam_link_sdk.openai_agents", "agents:FunctionTool._is_agent_tool"),
    ("sam_link_sdk.openai_agents", "agents.tool:_FailureHandlingFunctionToolInvoker.__agents_bind_function_tool__"),
    ("sam_link_sdk.strands", "strands.agent._agent_as_tool:_AgentAsTool.agent"),
    ("sam_link_sdk.crewai", "crewai.events.types.tool_usage_events:ToolUsageFinishedEvent.failure"),
    ("sam_link_sdk.adk", "google.adk.tools.base_tool:BaseTool.check_require_confirmation"),
    ("sam_link_sdk.adk", "google.adk.utils._function_call_names:REQUEST_CONFIRMATION_FUNCTION_CALL_NAME"),
    ("sam_link_sdk.openai_agents", "agents.usage:Usage.input_tokens"),
    ("sam_link_sdk.adk", "google.genai.types:GenerateContentResponseUsageMetadata.prompt_token_count"),
    ("sam_link_sdk.agent_framework", "agent_framework:UsageDetails.output_token_count"),
    ("sam_link_sdk.strands", "strands.types.event_loop:Usage.inputTokens"),
    ("sam_link_sdk.langchain", "langchain_core.messages.ai:UsageMetadata.output_tokens"),
    ("sam_link_sdk.otel_steps", "opentelemetry.sdk.trace:SpanProcessor.on_end"),
    ("sam_link_sdk.otel_steps", "opentelemetry.sdk.trace:TracerProvider.add_span_processor"),
]
"""One hook each adapter cannot work without; removing it must stop the adapter's import with FrameworkDrift.

A usage field the adapter reads, renamed by a release, would leave SAM's usage and cost records at zero.
"""


def _remove(monkeypatch, symbol):
    """Take the symbol away from where it is defined: a module attribute, a class member or a declared field."""
    module_name, _, path = symbol.partition(":")
    *owners, name = path.split(".")
    owner = importlib.import_module(module_name)
    for part in owners:
        owner = getattr(owner, part)
    if not isinstance(owner, type):
        monkeypatch.delattr(owner, name)
        return
    for klass in owner.__mro__:
        if name in vars(klass):
            monkeypatch.delattr(klass, name)
        if name in getattr(klass, "__annotations__", {}):
            monkeypatch.delitem(klass.__annotations__, name)
    fields = getattr(owner, "model_fields", None)
    if fields and name in fields:
        monkeypatch.delitem(fields, name)
    dataclass_fields = getattr(owner, "__dataclass_fields__", None)
    if dataclass_fields and name in dataclass_fields:
        monkeypatch.delitem(dataclass_fields, name)


@pytest.mark.parametrize("adapter,symbol", _DRIFT_CASES, ids=[f"{c[0]}-{c[1]}" for c in _DRIFT_CASES])
def test_each_adapter_refuses_to_import_when_its_framework_lost_a_hook(monkeypatch, adapter, symbol):
    pytest.importorskip(symbol.partition(":")[0].split(".")[0])
    package = importlib.import_module(adapter)
    _remove(monkeypatch, symbol)
    with pytest.raises(FrameworkDrift) as raised:
        importlib.reload(package)
    assert raised.value.missing == [symbol.replace(":", ".")]
    assert str(raised.value).startswith(f"{adapter} cannot use ")


_HOSTED_DRIFT_CASES = [
    ("sam_link_sdk.foundry", "azure.ai.agentserver.invocations:InvocationAgentServerHost.invoke_handler"),
    ("sam_link_sdk.agentcore", "bedrock_agentcore.runtime:BedrockAgentCoreApp.add_async_task"),
    ("sam_link_sdk.agentcore", "bedrock_agentcore.runtime:BedrockAgentCoreApp.entrypoint"),
]
"""The serverless runtimes import their framework only when the server is built, so they are checked then."""


@pytest.mark.parametrize("runtime,symbol", _HOSTED_DRIFT_CASES, ids=[f"{c[0]}-{c[1]}" for c in _HOSTED_DRIFT_CASES])
def test_each_serverless_runtime_refuses_to_build_its_server_when_its_framework_lost_a_hook(
    monkeypatch, runtime, symbol
):
    pytest.importorskip(symbol.partition(":")[0])
    module = importlib.import_module(runtime)
    _remove(monkeypatch, symbol)
    with pytest.raises(FrameworkDrift) as raised:
        module.app(lambda link_url: None)
    assert raised.value.missing == [symbol.replace(":", ".")]
    assert str(raised.value).startswith(f"{runtime} cannot use ")


def test_hermes_is_checked_when_it_is_first_used(monkeypatch):
    from sam_link_sdk.hermes import run as runner, steps as bridge

    plugins = types.ModuleType("hermes_cli.plugins")
    plugins.get_plugin_manager = lambda: types.SimpleNamespace(hooks={})
    monkeypatch.setitem(sys.modules, "hermes_cli", types.ModuleType("hermes_cli"))
    monkeypatch.setitem(sys.modules, "hermes_cli.plugins", plugins)
    with pytest.raises(FrameworkDrift, match=r"sam_link_sdk.hermes cannot use Hermes Agent .*PluginManager\._hooks"):
        bridge.install_hooks()

    agent = types.SimpleNamespace(
        session_id="sam-T",
        tools=[],
        valid_tool_names=set(),
        ephemeral_system_prompt="",
        run_conversation=lambda *a, **k: {},
    )
    monkeypatch.setattr(runner, "require", lambda *args: None)
    with pytest.raises(FrameworkDrift, match=r"AIAgent\.hard_interrupt"):
        runner.check_hermes(agent)


async def test_agent_framework_checks_its_middleware_context_on_first_use():
    pytest.importorskip("agent_framework")
    from sam_link_sdk.agent_framework import steps as middleware

    class OldChatContext:
        options, client = {}, None

    with pytest.raises(FrameworkDrift, match=r"ChatContext\.result"):
        middleware._check_context(OldChatContext(), "ChatContext", ("options", "client", "result"))
    assert OldChatContext not in middleware._CHECKED_CONTEXTS


def test_openai_agents_checks_the_failure_invoker_members_when_it_wraps_a_tool(monkeypatch):
    pytest.importorskip("agents")
    from agents import function_tool

    from sam_link_sdk.openai_agents import with_scopes

    @function_tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        return "routed"

    assert with_scopes([route_claim], {"route_claim": ["tool:route_claim:use"]})
    monkeypatch.delattr(route_claim.on_invoke_tool, "_on_handled_error")
    with pytest.raises(FrameworkDrift, match=r"FunctionTool\.on_invoke_tool\._on_handled_error"):
        with_scopes([route_claim], {"route_claim": ["tool:route_claim:use"]})


def test_openai_agents_checks_that_as_tool_still_keeps_its_agent_hooks_and_run_config_in_the_closure(monkeypatch):
    """The adapter runs an Agent.as_tool tool's function over a closure with SAM's agent copy, hooks and run config
    swapped in; a release that renames them would leave the nested run unchecked, so it must fail instead."""
    pytest.importorskip("agents")
    from sam_link_sdk.openai_agents import steps

    monkeypatch.setattr(steps, "_AS_TOOL_CHECKED", False)
    steps._check_as_tool()
    assert steps._AS_TOOL_CHECKED

    monkeypatch.setattr(steps, "_AS_TOOL_CHECKED", False)
    monkeypatch.setattr(steps, "AGENT_TOOL_CLOSURE", ("self", "nested_hooks", "run_config"))
    with pytest.raises(FrameworkDrift, match=r"Agent\.as_tool\(\)'s nested_hooks in the closure"):
        steps._check_as_tool()


def test_strands_checks_the_registry_it_reads_nested_agents_from():
    pytest.importorskip("strands")
    from sam_link_sdk.strands.steps import _nested_agents

    with pytest.raises(FrameworkDrift, match=r"Agent\.tool_registry"):
        list(_nested_agents(types.SimpleNamespace()))
    with pytest.raises(FrameworkDrift, match=r"Agent\.tool_registry\.registry"):
        list(_nested_agents(types.SimpleNamespace(tool_registry=types.SimpleNamespace())))


def test_agent_framework_checks_that_as_tool_still_holds_its_agent_the_way_the_adapter_finds_it(monkeypatch):
    pytest.importorskip("agent_framework")
    from sam_link_sdk.agent_framework import steps

    monkeypatch.setattr(steps, "_AS_TOOL_CHECKED", False)
    steps._check_as_tool()
    assert steps._AS_TOOL_CHECKED

    monkeypatch.setattr(steps, "_AS_TOOL_CHECKED", False)
    monkeypatch.setattr(steps, "AGENT_TOOL_FUNCTION", "_run_agent")
    with pytest.raises(FrameworkDrift, match=r"Agent\.as_tool\(\)'s function _run_agent"):
        steps._check_as_tool()


def test_agent_framework_refuses_a_tool_that_wraps_an_agent_but_not_the_way_the_adapter_finds_it(monkeypatch):
    pytest.importorskip("agent_framework")
    from agent_framework import Agent

    from sam_link_sdk._adapter import NestedRunRefused
    from sam_link_sdk.agent_framework import steps

    from .test_agent_framework import ScriptedClient

    monkeypatch.setattr(steps, "_AS_TOOL_CHECKED", True)
    inner = Agent(ScriptedClient([]), "Route.", name="router")

    def renamed(**kwargs):
        return ""

    tool = types.SimpleNamespace(name="router", func=renamed, _wrapped_agent=inner)
    with pytest.raises(NestedRunRefused, match="not the way SAM finds it"):
        list(steps._nested_agents([tool]))
    assert list(steps._nested_agents([types.SimpleNamespace(name="plain", func=renamed)])) == []


async def test_adk_logs_once_when_the_confirmation_check_fails(caplog):
    pytest.importorskip("google.adk")
    from sam_link_sdk.adk import steps

    class Tool:
        name = "route_claim"

        async def check_require_confirmation(self, args, tool_context):
            raise TypeError("signature changed")

    steps._HOLD_CHECK_WARNED = False
    with caplog.at_level("WARNING", logger="sam_link_sdk.adk"):
        assert await steps._holds(Tool(), {}, None) is False
        assert await steps._holds(Tool(), {}, None) is False
    assert [r.getMessage() for r in caplog.records].count(
        "check_require_confirmation raised for the tool route_claim; treating its calls as not held for confirmation "
        "(logged once)"
    ) == 1


def test_framework_drift_is_exported_from_the_package_root():
    assert sam_link_sdk.FrameworkDrift is FrameworkDrift
    assert "FrameworkDrift" in sam_link_sdk.__all__
