# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Per-tool required scopes (AgentConfig.tools), decided from the scopes SAM granted the task's user, and per-user
history keys."""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from sam_link_sdk.audit import AuditLogger
from sam_link_sdk.config import AgentConfig
from sam_link_sdk.context import TaskContext

SCOPES = {"route_claim": ["tool:claims:route"]}


def make_ctx(
    granted=(), records=None, *, context_id="CTX", user_id="", tool_scopes=SCOPES, declared=("lookup_policy",)
):
    """A TaskContext as the agent builds it for a task: the scopes SAM granted the task's user, none by default."""
    return TaskContext._new(
        task_id="T",
        context_id=context_id,
        text="Route P-1",
        agent_name="claims-triage",
        user_id=user_id,
        tool_scopes=tool_scopes,
        declared_tool_names=declared,
        granted_tool_scopes=frozenset(granted),
        audit=AuditLogger(records.append) if records is not None else None,
    )


def named(name):
    return SimpleNamespace(name=name)


def test_a_task_granted_nothing_gets_only_the_tools_that_need_no_scope():
    tools = [named("lookup_policy"), named("route_claim")]
    assert make_ctx().permitted_tools(tools) == [tools[0]]
    assert TaskContext._new(
        task_id="T", context_id="C", text="x", agent_name="a", tool_scopes=SCOPES, declared_tool_names=["lookup_policy"]
    ).permitted_tools(tools) == [tools[0]]


def test_a_user_without_a_tools_scopes_does_not_get_it_and_the_denial_is_audited():
    records = []
    tools = [named("lookup_policy"), named("route_claim")]

    assert make_ctx({"claims:case:read"}, records).permitted_tools(tools) == [tools[0]]
    assert [(r.message, r.fields["tool"], r.fields["requiredScopes"]) for r in records] == [
        ("tool access denied", "route_claim", ["tool:claims:route"])
    ]
    assert make_ctx({"tool:claims:route"}).permitted_tools(tools) == tools


def test_a_tool_the_card_does_not_declare_is_refused_and_logged_once_when_the_card_declares_a_scoped_tool(caplog):
    records = []
    ctx = make_ctx({"tool:claims:route"}, records)
    with caplog.at_level("ERROR", logger="sam_link_sdk.task"):
        assert not ctx.check_tool("mystery") and not ctx.check_tool("mystery")
    assert [r.message for r in caplog.records if "mystery" in r.message and "route_claim" in r.message] == [
        caplog.records[0].message
    ]
    assert len(caplog.records) == 1
    assert [r.fields["tool"] for r in records] == ["mystery", "mystery"]
    assert ctx.check_tool("lookup_policy") and ctx.check_tool("ask_user_question")


def test_a_card_with_no_scoped_tool_allows_every_tool():
    ctx = make_ctx(tool_scopes={}, declared=())
    assert ctx.check_tool("anything")


def test_plain_functions_are_matched_by_their_name():
    def route_claim():
        return None

    assert make_ctx().permitted_tools([route_claim]) == []


def link_ctx(granted, records=None):
    """A TaskContext as a task from the agent link builds it: no identity, the granted list SAM sent."""
    return TaskContext._new(
        task_id="T",
        context_id="C",
        text="Route P-1",
        agent_name="claims-triage",
        tool_scopes=SCOPES,
        declared_tool_names=["lookup_policy"],
        granted_tool_scopes=granted,
        audit=AuditLogger(records.append) if records is not None else None,
    )


def test_on_the_link_a_tool_runs_only_with_every_scope_sam_granted_matched_exactly():
    records = []
    tools = [named("lookup_policy"), named("route_claim")]
    assert link_ctx(frozenset({"tool:claims:route"})).permitted_tools(tools) == tools
    assert link_ctx(frozenset({"tool:claims:*"}), records).permitted_tools(tools) == [tools[0]]
    assert link_ctx(frozenset()).permitted_tools(tools) == [tools[0]]
    assert [(r.message, r.fields["tool"]) for r in records] == [("tool access denied", "route_claim")]


def test_the_scopes_a_context_enforces_are_readable_and_not_writable():
    ctx = link_ctx(frozenset())
    assert dict(ctx._run.tools.declared_scopes) == {"route_claim": ("tool:claims:route",)}
    with pytest.raises(TypeError):
        ctx._run.tools.declared_scopes["route_claim"] = ()
    assert dict(TaskContext(task_id="T", context_id="C", text="x", agent_name="a")._run.tools.declared_scopes) == {}


def test_hermes_tool_infos_scopes_go_on_the_named_tools_and_a_name_that_is_no_tool_is_refused():
    from sam_link_sdk._adapter import scoped_infos as with_scopes
    from sam_link_sdk.config import ToolInfo

    infos = [ToolInfo("lookup_policy", "Look up"), ToolInfo("route_claim", "Route", ["claims:*:read"])]
    assert with_scopes(infos, {"route_claim": ["tool:claims:route", "claims:*:read"]}) == [
        ToolInfo("lookup_policy", "Look up"),
        ToolInfo("route_claim", "Route", ["claims:*:read", "tool:claims:route"]),
    ]
    assert with_scopes(infos, None) == infos
    with pytest.raises(ValueError, match="route_claims"):
        with_scopes(infos, {"route_claims": ["tool:claims:route"]})
    with pytest.raises(TypeError):
        with_scopes(infos, {"route_claim": "tool:claims:route"})
    with pytest.raises(TypeError):
        with_scopes(infos, [("route_claim", ["tool:claims:route"])])


def test_history_is_keyed_by_user_and_context():
    alice = make_ctx(context_id="CHAT", user_id="alice")
    bob = make_ctx(context_id="CHAT", user_id="bob")
    assert (alice.session_id, bob.session_id) == ("alice:CHAT", "bob:CHAT")
    assert make_ctx(context_id="CHAT").session_id == "CHAT"
    assert make_ctx(context_id="").session_id == ""


def test_a_task_is_bounded_by_default():
    assert AgentConfig(name="a", description="d").task_timeout_seconds == 600


pytest.importorskip("langchain_core")

from langchain_core.language_models import BaseChatModel  # noqa: E402
from langchain_core.messages import AIMessage  # noqa: E402
from langchain_core.outputs import ChatGeneration, ChatResult  # noqa: E402
from langchain_core.tools import tool  # noqa: E402
from pydantic import Field  # noqa: E402

from sam_link_sdk.langchain import run_agent as run_langchain  # noqa: E402


@tool
def lookup_policy(policy_id: str) -> str:
    """Look up a policy."""
    return "gold"


@tool
def route_claim(claim_id: str) -> str:
    """Route a claim."""
    return "fast-track"


class BindingModel(BaseChatModel):
    bound: list = Field(default_factory=list)

    @property
    def _llm_type(self) -> str:
        return "binding"

    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        return ChatResult(generations=[ChatGeneration(message=AIMessage(content="done"))])

    def bind_tools(self, tools, **kwargs):
        self.bound.append([t.name for t in tools])
        return self


async def test_langchain_loop_gives_the_model_only_the_permitted_tools():
    model = BindingModel()
    await run_langchain(make_ctx({"claims:case:read"}), model, [lookup_policy, route_claim])
    await run_langchain(make_ctx({"tool:claims:route"}), model, [lookup_policy, route_claim])
    assert model.bound == [["lookup_policy"], ["lookup_policy", "route_claim"]]


pytest.importorskip("agents")

from agents import Agent, function_tool  # noqa: E402

from sam_link_sdk.openai_agents import run_agent  # noqa: E402

from .test_openai_agents import ScriptedModel, message, usage  # noqa: E402


class ToolRecordingModel(ScriptedModel):
    def __init__(self, turns):
        super().__init__(turns)
        self.tool_names = []

    async def get_response(self, *args, **kwargs):
        tools = kwargs.get("tools", args[3] if len(args) > 3 else [])
        self.tool_names.append([t.name for t in tools])
        return await super().get_response(*args, **kwargs)


@function_tool
def oa_route_claim(claim_id: str) -> str:
    """Route a claim."""
    return "fast-track"


@function_tool
def oa_lookup_policy(policy_id: str) -> str:
    """Look up a policy."""
    return "gold"


async def test_openai_agents_runner_leaves_out_tools_the_user_lacks_scopes_for():
    model = ToolRecordingModel([([message("ok")], usage(5, 1))])
    agent = Agent(name="claims-triage", instructions="x", model=model, tools=[oa_lookup_policy, oa_route_claim])
    ctx = make_ctx(
        {"claims:case:read"}, tool_scopes={"oa_route_claim": ["tool:claims:route"]}, declared=("oa_lookup_policy",)
    )
    assert await run_agent(ctx, agent) == "ok"
    assert model.tool_names == [["oa_lookup_policy"]]
    assert [t.name for t in agent.tools] == ["oa_lookup_policy", "oa_route_claim"]


pytest.importorskip("google.adk")

from google.genai import types  # noqa: E402

from sam_link_sdk.adk import run_agent as adk_run_agent  # noqa: E402

from .test_adk import Scripted, fc, runner_for, usage as adk_usage  # noqa: E402


async def test_adk_refuses_a_call_to_a_tool_the_user_lacks_scopes_for():
    calls = []

    def route_claim(claim_id: str) -> dict:
        """Route a claim."""
        calls.append(claim_id)
        return {"queue": "fast-track"}

    model = Scripted(
        turns=[
            ([fc("route_claim", {"claim_id": "P-1"}, "c1")], adk_usage(10, 2)),
            ([types.Part(text="I could not route it.")], adk_usage(12, 3)),
        ]
    )
    records = []
    ctx = make_ctx({"claims:case:read"}, records)
    assert await adk_run_agent(ctx, runner_for(model, [route_claim])) == "I could not route it."
    assert calls == []
    assert [r.message for r in records if r.message.startswith("tool access")] == ["tool access denied"]
