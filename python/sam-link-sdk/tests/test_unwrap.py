# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Our own wrappers (with_scopes, with_approval) must not hide what a walk over the tools needs to see.

Every wrapper an adapter returns registers the tool it stands in for (``_adapter.mark_wrapper``), and every walk that
inspects tools looks through them (``_adapter.unwrapped``): agent-as-tool detection (Strands, Agent Framework, OpenAI
Agents), isolated AgentTool detection (ADK), the tools an approval toolset holds (ADK). Each test wraps the tool both
ways (scopes over approval, approval over scopes) where the adapter's approval is a wrapper, and checks the walk still
sees through, for a plain tool and for an agent given as a tool.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from sam_link_sdk._adapter import NestedRunRefused, unwrapped
from sam_link_sdk.events import ModelFinished, ToolFinished

from .adapter_task import adapter_task, steps
from .test_scope_guard import SCOPE, Body

ORDERS = ["scopes_over_approval", "approval_over_scopes"]


def _deny_approvals(ctx, asked):
    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        return SimpleNamespace(approved=False, outcome="denied")

    ctx.request_approval = request_approval


def _wrapped(adapter: str, order: str, ctx, tool):
    import importlib

    module = importlib.import_module(f"sam_link_sdk.{adapter}")
    scoped = lambda tools: module.with_scopes(tools, {tool_name(tool): [SCOPE]})  # noqa: E731
    approved = (
        (lambda tools: module.with_approval(tools))
        if adapter == "adk"
        else (lambda tools: module.with_approval(ctx, tools))
    )
    return scoped(approved([tool])) if order == "scopes_over_approval" else approved(scoped([tool]))


def tool_name(tool):
    from sam_link_sdk.context import tool_name as name

    return name(tool)


WRAPPING = {"langchain": "langchain_core", "openai_agents": "agents", "crewai": "crewai", "adk": "google.adk"}


@pytest.mark.parametrize("order", ORDERS)
@pytest.mark.parametrize("adapter", list(WRAPPING))
async def test_unwrapped_reaches_the_tool_behind_both_wrappers_in_either_order(adapter, order):
    pytest.importorskip(WRAPPING[adapter])
    from .test_scope_guard import KITS

    body = Body()
    tool = KITS[adapter].make(body.fn)
    async with adapter_task(scopes={"route_claim": [SCOPE]}, granted=[SCOPE]) as task:
        [outer] = _wrapped(adapter, order, task.ctx, tool)
        if adapter == "adk":
            [outer] = await outer.get_tools() if hasattr(outer, "get_tools") else [outer]
            assert unwrapped(outer).func is body.fn
            return
    assert unwrapped(outer) is tool


async def test_strands_an_agent_tool_behind_with_scopes_is_still_routed_its_approvals_asked_and_steps_reported():
    """HIGH 1: with_scopes([router.as_tool()]) hid the router from the walk that routes it, so its send_payment ran
    without the user being asked, and its steps and tokens went unreported."""
    pytest.importorskip("strands")
    from strands import Agent, tool

    from sam_link_sdk.strands import run_agent, with_approval, with_scopes

    from .test_strands import ScriptedModel, call, text, usage

    ran, asked = [], []

    @tool
    def send_payment(claim_id: str) -> str:
        """Pay a claim."""
        ran.append(claim_id)
        return "paid"

    model = ScriptedModel(
        [
            ([call("c1", "router", {"input": "pay C-1"})], usage(1, 1)),
            ([call("c2", "send_payment", {"claim_id": "C-1"})], usage(3, 1)),
            ([text("Not paid.")], usage(4, 1)),
            ([text("Done.")], usage(2, 1)),
        ]
    )
    router = Agent(name="router", description="Pays claims.", model=model, tools=[send_payment], callback_handler=None)
    [scoped_router] = with_scopes([router.as_tool()], {"router": ["tool:router:use"]})

    def build(c):
        return Agent(
            model=model,
            tools=[scoped_router],
            callback_handler=None,
            hooks=[with_approval(c, [send_payment], names=["send_payment"])],
        )

    async with adapter_task(
        scopes={"router": ["tool:router:use"]}, declared=["send_payment"], granted=["tool:router:use"]
    ) as task:
        ctx = task.ctx
        _deny_approvals(ctx, asked)
        assert await run_agent(ctx, build) == "Done."
        sent = steps(task)
    assert asked == ["send_payment"] and ran == []
    assert [type(e) for e in sent].count(ModelFinished) == 4
    assert ctx._run.usage.final_token_usage()["total_input_tokens"] == 10


async def test_strands_an_agent_with_background_tasks_behind_with_scopes_is_still_refused():
    pytest.importorskip("strands")
    from strands import Agent

    from sam_link_sdk.strands import run_agent, with_scopes

    from .test_strands import ScriptedModel, text, usage

    model = ScriptedModel([([text("never")], usage(1, 1))])
    inner = Agent(name="router", description="Routes.", model=model, background_tasks=True, callback_handler=None)
    [scoped] = with_scopes([inner.as_tool()], {"router": [SCOPE]})
    async with adapter_task(scopes={"router": [SCOPE]}, granted=[SCOPE]) as task:
        with pytest.raises(NestedRunRefused, match="background"):
            await run_agent(task.ctx, lambda c: Agent(model=model, tools=[scoped], callback_handler=None))
    assert model.turns


def _af_router(ran):
    from agent_framework import Agent, tool

    from .test_agent_framework import ScriptedClient, call, text, usage

    @tool(approval_mode="never_require")
    def send_payment(claim_id: str) -> str:
        """Pay a claim."""
        ran.append(claim_id)
        return "paid"

    client = ScriptedClient(
        [([call("c2", "send_payment", {"claim_id": "C-1"})], usage(3, 1)), ([text("Not paid.")], usage(4, 1))]
    )
    return Agent(client, "Pay.", name="router", tools=[send_payment]), send_payment


async def test_agent_framework_an_agent_tool_behind_with_scopes_is_routed_not_refused():
    """LOW 1: with_scopes on router.as_tool() made every run fail (the agent behind the tool could not be found)."""
    pytest.importorskip("agent_framework")
    from agent_framework import Agent

    from sam_link_sdk.agent_framework import run_agent, with_approval, with_scopes

    from .test_agent_framework import ScriptedClient, call, text, usage

    ran, asked = [], []
    router, send_payment = _af_router(ran)
    [scoped] = with_scopes([router.as_tool()], {"router": ["tool:router:use"]})
    front = Agent(
        ScriptedClient([([call("c1", "router", {"task": "pay C-1"})], usage(1, 1)), ([text("Done.")], usage(2, 1))]),
        "Ask.",
        name="front",
        tools=[scoped],
    )
    async with adapter_task(
        scopes={"router": ["tool:router:use"]}, declared=["send_payment"], granted=["tool:router:use"]
    ) as task:
        ctx = task.ctx
        _deny_approvals(ctx, asked)
        assert (
            await run_agent(ctx, front, middleware=[with_approval(ctx, [send_payment], names=["send_payment"])])
            == "Done."
        )
        sent = steps(task)
    assert asked == ["send_payment"] and ran == []
    assert [type(e) for e in sent].count(ModelFinished) == 4


async def test_agent_framework_a_workflow_agent_behind_with_scopes_is_still_refused():
    pytest.importorskip("agent_framework")
    from agent_framework import Agent, WorkflowBuilder

    from sam_link_sdk.agent_framework import run_agent, with_scopes

    from .test_agent_framework import ScriptedClient, call, text, usage

    router, _ = _af_router([])
    flow = WorkflowBuilder(start_executor=router).build().as_agent(name="flow").as_tool()
    [scoped] = with_scopes([flow], {"flow": [SCOPE]})
    client = ScriptedClient([([call("c1", "flow", {"task": "x"})], usage(1, 1)), ([text("Done.")], usage(2, 1))])
    async with adapter_task(scopes={"flow": [SCOPE]}, granted=[SCOPE]) as task:
        with pytest.raises(NestedRunRefused, match="WorkflowAgent"):
            await run_agent(task.ctx, Agent(client, "Ask.", name="front", tools=[scoped]))


@pytest.mark.parametrize("order", ORDERS)
async def test_openai_agents_an_agent_tool_behind_our_wrappers_is_run_on_the_task_with_its_wrappers_kept(order):
    """with_scopes and with_approval copy the function tool; the agent behind Agent.as_tool stays on the original, so
    the copy the adapter runs on the task gets the same wrappers in front: the user is asked once about the agent tool,
    and the agent's own model call is reported."""
    pytest.importorskip("agents")
    from agents import Agent

    from sam_link_sdk.openai_agents import run_agent

    from .test_openai_agents import ScriptedModel, call, message, usage

    asked: list[str] = []

    async def approve(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        return SimpleNamespace(approved=True, outcome="approved")

    inner = Agent(name="router", instructions="x", model=ScriptedModel([([message("Routed.")], usage(3, 1))]))
    model = ScriptedModel(
        [([call("c1", "router", {"input": "route C-1"})], usage(1, 1)), ([message("Done.")], usage(2, 1))]
    )
    async with adapter_task(scopes={"router": [SCOPE]}, granted=[SCOPE]) as task:
        ctx = task.ctx
        ctx.request_approval = approve
        wrapped = _wrapped("openai_agents", order, ctx, inner.as_tool(tool_name="router", tool_description="Routes."))
        assert await run_agent(ctx, Agent(name="front", instructions="x", model=model, tools=wrapped)) == "Done."
        sent = steps(task)
    assert asked == ["router"]
    assert [type(e) for e in sent].count(ModelFinished) == 3
    assert [e.result for e in sent if isinstance(e, ToolFinished)] == ["Routed."]


@pytest.mark.parametrize("order", ORDERS)
async def test_adk_an_isolated_agent_tool_behind_our_wrappers_is_still_refused(order):
    pytest.importorskip("google.adk")
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.adk.tools.agent_tool import AgentTool
    from google.genai import types

    from sam_link_sdk.adk import run_agent, step_reporter

    from .test_adk import Scripted, usage

    model = Scripted(turns=[([types.Part(text="never")], usage(1, 1))])
    inner = LlmAgent(name="router", model=model, instruction="x")
    async with adapter_task(scopes={"router": [SCOPE]}, granted=[SCOPE]) as task:
        ctx = task.ctx
        tools = _wrapped("adk", order, ctx, AgentTool(agent=inner, include_plugins=False))
        front = LlmAgent(name="front", model=model, instruction="x", tools=tools)
        with pytest.raises(NestedRunRefused, match="include_plugins"):
            await run_agent(ctx, InMemoryRunner(agent=front, app_name="claims", plugins=[step_reporter()]))
    assert model.turns


@pytest.mark.parametrize("order", ORDERS)
@pytest.mark.parametrize("adapter", ["langchain", "openai_agents", "crewai", "adk"])
async def test_both_orders_ask_once_and_refuse_a_user_without_the_scope(adapter, order):
    """A plain tool behind both wrappers, either way round: a user without the scope is refused without being asked;
    a user with it is asked once; the card lists the scope."""
    pytest.importorskip(WRAPPING[adapter])
    import importlib

    from sam_link_sdk.context import bind_task

    from .test_scope_guard import KITS

    kit = KITS[adapter]
    for granted, expect_asked in (([], []), ([SCOPE], ["route_claim"])):
        body, asked = Body(), []
        async with adapter_task(scopes={"route_claim": [SCOPE]}, granted=granted) as task:
            ctx = task.ctx
            _deny_approvals(ctx, asked)
            with bind_task(ctx):
                [outer] = _wrapped(adapter, order, ctx, kit.make(body.fn))
                if adapter == "adk":
                    [outer] = await outer.get_tools() if hasattr(outer, "get_tools") else [outer]
                await kit.call(outer, {"claim_id": "C-1"})
        assert body.ran == [] and asked == expect_asked, (granted, asked)
    module = importlib.import_module(f"sam_link_sdk.{adapter}")
    async with adapter_task(scopes={"route_claim": [SCOPE]}, granted=[]) as task:
        infos = module.tool_infos(_wrapped(adapter, order, task.ctx, kit.make(Body().fn)))
    assert [i.required_scopes for i in infos] == [[SCOPE]]


async def test_strands_a_delegating_agent_tool_behind_with_scopes_still_delegates():
    """Strands' delegation (as_tool(delegate=True)) makes the agent tool's answer the run's answer; it finds such a
    tool by its class, so with_scopes keeps the class (a copy of the same tool type with the check in front)."""
    pytest.importorskip("strands")
    from strands import Agent

    from sam_link_sdk.strands import run_agent, with_scopes

    from .test_strands import ScriptedModel, call, text, usage

    billing = Agent(
        name="billing",
        description="Answers billing questions.",
        callback_handler=None,
        model=ScriptedModel([([text("Billing: the claim is paid.")], usage(2, 1))]),
    )
    outer_model = ScriptedModel(
        [
            ([call("c1", "billing", {"input": "is C-1 paid?"})], usage(1, 1)),
            ([text("never: the delegate's answer is the run's")], usage(1, 1)),
        ]
    )
    [scoped] = with_scopes([billing.as_tool(delegate=True)], {"billing": [SCOPE]})
    async with adapter_task(scopes={"billing": [SCOPE]}, granted=[SCOPE]) as task:
        answer = await run_agent(task.ctx, lambda c: Agent(model=outer_model, tools=[scoped], callback_handler=None))
    assert getattr(scoped, "delegate", False) is True
    assert "the claim is paid" in answer and len(outer_model.turns) == 1
