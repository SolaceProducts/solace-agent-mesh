# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The structural guard: a tool given scopes (each adapter's ``with_scopes``) checks the task's grants where it runs.

The hooks hide such a tool and refuse a call to it, but a run no hook sees (a nested run the adapter cannot follow, a
thread that lost the context, a background run, a fresh agent built inside a tool) must not run it either. So each
adapter's guarded tool is called here through paths no hook sees: the framework's own call of the tool object, from a
thread with no context, and from a fresh agent with no step reporter; and approval-guarded tools likewise. A refused
call never runs the tool's body.
"""

from __future__ import annotations

import asyncio
import contextlib
import contextvars
import json
import threading
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any

import pytest

from sam_link_sdk.context import bind_task, current_task
from sam_link_sdk.events import ModelFinished, ToolFinished, ToolStarted

from .adapter_task import adapter_task, steps
from .agent_rig import settle, start_agent

SCOPE = "tool:claims:route"
SCOPES = {"route_claim": [SCOPE]}
REFUSED = "Access denied: you do not have the scopes required to use route_claim."


@dataclass
class Kit:
    """How one adapter's tools are made, guarded, and called the way the framework calls the tool object (no hook,
    no reporter), returning the text the model would get."""

    module: str
    framework: str
    make: Callable[[Callable], Any]
    call: Callable[[Any, dict], Awaitable[str]]
    approval: Callable[[Any, list], list] | None = None

    def adapter(self):
        pytest.importorskip(self.framework)
        import importlib

        return importlib.import_module(f"sam_link_sdk.{self.module}")


def _langchain_make(fn):
    from langchain_core.tools import tool

    return tool(fn)


async def _langchain_call(tool, args):
    from langchain_core.tools import ToolException

    try:
        return str(await tool.ainvoke(args))
    except ToolException as exc:
        return str(exc)


def _openai_make(fn):
    from agents import function_tool

    return function_tool(fn)


async def _openai_call(tool, args):
    from agents.tool_context import ToolContext

    raw = json.dumps(args)
    return str(
        await tool.on_invoke_tool(
            ToolContext(context=None, tool_name=tool.name, tool_call_id="c1", tool_arguments=raw), raw
        )
    )


def _adk_make(fn):
    return fn


async def _adk_call(tool, args):
    from google.adk.tools import BaseTool
    from google.adk.tools.function_tool import FunctionTool

    tool = tool if isinstance(tool, BaseTool) else FunctionTool(tool)
    return json.dumps(await tool.run_async(args=args, tool_context=None))


def _crewai_make(fn):
    from crewai.tools import tool

    return tool(fn.__name__)(fn)


async def _crewai_call(tool, args):
    return str(await asyncio.to_thread(tool.run, **args))


def _strands_make(fn):
    from strands import tool

    return tool(fn)


async def _strands_call(tool, args):
    last = None
    async for event in tool.stream({"toolUseId": "c1", "name": tool.tool_name, "input": args}, {}):
        last = event
    result = last.tool_result
    return json.dumps(result["content"])


def _af_make(fn):
    from agent_framework import tool

    return tool(approval_mode="never_require")(fn)


async def _af_call(tool, args):
    contents = await tool.invoke(arguments=args)
    return " ".join(str(getattr(c, "text", c)) for c in contents)


def _approval(module):
    def guard(ctx, tools):
        import importlib

        adapter = importlib.import_module(f"sam_link_sdk.{module}")
        return adapter.with_approval(tools) if module == "adk" else adapter.with_approval(ctx, tools)

    return guard


KITS = {
    "langchain": Kit("langchain", "langchain_core", _langchain_make, _langchain_call, _approval("langchain")),
    "openai_agents": Kit("openai_agents", "agents", _openai_make, _openai_call, _approval("openai_agents")),
    "adk": Kit("adk", "google.adk", _adk_make, _adk_call, _approval("adk")),
    "crewai": Kit("crewai", "crewai", _crewai_make, _crewai_call, _approval("crewai")),
    "strands": Kit("strands", "strands", _strands_make, _strands_call),
    "agent_framework": Kit("agent_framework", "agent_framework", _af_make, _af_call),
}
APPROVAL_WRAPPERS = [name for name, kit in KITS.items() if kit.approval is not None]


class Body:
    """route_claim, recording each run of its body."""

    def __init__(self):
        self.ran: list[str] = []
        body = self

        def route_claim(claim_id: str) -> str:
            """Route a claim to a work queue."""
            body.ran.append(claim_id)
            return "fast-track"

        self.fn = route_claim


def guarded(kit: Kit, body: Body):
    [tool] = kit.adapter().with_scopes([kit.make(body.fn)], SCOPES)
    return tool


async def _approval_tool(kit: Kit, ctx, tool):
    [wrapped] = kit.approval(ctx, [tool])
    if kit.module == "adk":
        [wrapped] = await wrapped.get_tools()
    return wrapped


@pytest.mark.parametrize("adapter", list(KITS))
async def test_a_scoped_tool_called_outside_any_mesh_task_refuses_and_its_body_does_not_run(adapter):
    kit, body = KITS[adapter], Body()
    tool = guarded(kit, body)
    assert current_task() is None
    text = await kit.call(tool, {"claim_id": "C-1"})
    assert body.ran == [] and "no mesh task is running here" in text


@pytest.mark.parametrize("adapter", list(KITS))
async def test_a_scoped_tool_refuses_a_task_whose_user_lacks_its_scopes_however_it_is_called(adapter):
    kit, body, records = KITS[adapter], Body(), []
    tool = guarded(kit, body)
    async with adapter_task(scopes=SCOPES, granted=[], audit=records.append) as task:
        with bind_task(task.ctx):
            text = await kit.call(tool, {"claim_id": "C-1"})
    assert body.ran == [] and REFUSED in text
    assert [(r.message, r.fields.get("tool")) for r in records] == [("tool access denied", "route_claim")]


@pytest.mark.parametrize("adapter", list(KITS))
async def test_a_scoped_tool_runs_for_a_task_whose_user_holds_its_scopes(adapter):
    kit, body = KITS[adapter], Body()
    tool = guarded(kit, body)
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            text = await kit.call(tool, {"claim_id": "C-1"})
    assert body.ran == ["C-1"] and "fast-track" in text


@pytest.mark.parametrize("adapter", list(KITS))
async def test_a_scoped_tool_refuses_a_stopped_task(adapter):
    kit, body = KITS[adapter], Body()
    tool = guarded(kit, body)
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        task.ctx._run.halt()
        with bind_task(task.ctx):
            text = await kit.call(tool, {"claim_id": "C-1"})
    assert body.ran == [] and "was stopped" in text


@pytest.mark.parametrize("adapter", list(KITS))
async def test_a_scoped_tool_refuses_when_the_agents_card_does_not_declare_its_scopes(adapter):
    """The card is what SAM grants against: a scope the tool requires but the card never declared is granted to
    nobody, so the tool refuses rather than run unchecked."""
    kit, body = KITS[adapter], Body()
    tool = guarded(kit, body)
    async with adapter_task(scopes={}, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            text = await kit.call(tool, {"claim_id": "C-1"})
    assert body.ran == [] and REFUSED in text


@pytest.mark.parametrize("adapter", list(KITS))
async def test_a_scoped_tool_called_on_a_thread_that_lost_the_context_refuses(adapter):
    """A framework that ran the tool on a thread without the caller's context would leave no current task: the tool
    refuses there, even while a task whose user holds the scope runs in the caller."""
    kit, body = KITS[adapter], Body()
    tool = guarded(kit, body)
    out: list[str] = []

    def bare_thread():
        out.append(asyncio.run(kit.call(tool, {"claim_id": "C-1"})))

    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            thread = threading.Thread(target=lambda: contextvars.Context().run(bare_thread))
            thread.start()
            await asyncio.to_thread(thread.join, 30)
    assert body.ran == [] and "no mesh task is running here" in out[0]


@pytest.mark.parametrize("adapter", list(KITS))
def test_tool_infos_reads_the_scopes_off_the_guarded_tools(adapter):
    kit, body = KITS[adapter], Body()
    adapter_module = kit.adapter()
    [info] = adapter_module.tool_infos([guarded(kit, body)])
    assert (info.name, info.required_scopes) == ("route_claim", [SCOPE])


@pytest.mark.parametrize("adapter", list(KITS))
def test_with_scopes_refuses_a_name_that_is_none_of_the_tools(adapter):
    kit, body = KITS[adapter], Body()
    with pytest.raises(ValueError, match="route_claims"):
        kit.adapter().with_scopes([kit.make(body.fn)], {"route_claims": [SCOPE]})
    with pytest.raises(TypeError):
        kit.adapter().with_scopes([kit.make(body.fn)], {"route_claim": SCOPE})


@pytest.mark.parametrize("adapter", APPROVAL_WRAPPERS)
async def test_an_approval_guarded_tool_outside_its_task_asks_nobody_and_does_not_run(adapter):
    """A guarded tool called outside its task asks nobody and does not run.

    Another task being current does not help: its user is not the one who must approve.
    """
    kit, body = KITS[adapter], Body()
    kit.adapter()
    asked: list[str] = []

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        raise AssertionError("asked")

    async with (
        adapter_task(scopes=SCOPES, granted=[SCOPE]) as task,
        adapter_task("T2", scopes=SCOPES, granted=[SCOPE]) as other,
    ):
        ctx = task.ctx
        ctx.request_approval = request_approval
        if adapter == "crewai":
            with bind_task(ctx):
                tool = await _approval_tool(kit, ctx, kit.make(body.fn))
        else:
            tool = await _approval_tool(kit, ctx, kit.make(body.fn))
        texts = [await kit.call(tool, {"claim_id": "C-1"})]
        if adapter != "adk":
            with bind_task(other.ctx):
                texts.append(await kit.call(tool, {"claim_id": "C-2"}))
    assert body.ran == [] and asked == []
    assert all("mesh task" in text for text in texts)


@pytest.mark.parametrize("adapter", APPROVAL_WRAPPERS)
async def test_an_approval_over_a_scoped_tool_refuses_a_user_without_the_scope_before_asking(adapter):
    kit, body = KITS[adapter], Body()
    asked: list[str] = []

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        raise AssertionError("asked")

    async with adapter_task(scopes=SCOPES, granted=[]) as task:
        ctx = task.ctx
        ctx.request_approval = request_approval
        with bind_task(ctx):
            tool = await _approval_tool(kit, ctx, guarded(kit, body))
            text = await kit.call(tool, {"claim_id": "C-1"})
        assert body.ran == [] and asked == [] and REFUSED in text
        assert [i.required_scopes for i in kit.adapter().tool_infos(kit.approval(ctx, [guarded(kit, Body())]))] == [
            [SCOPE]
        ]


async def _fresh_langgraph(tool):
    pytest.importorskip("langgraph")
    from langchain_core.messages import AIMessage, HumanMessage
    from langgraph.prebuilt import create_react_agent

    from .test_callbacks import ToolCallingFake

    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(content="", tool_calls=[{"id": "c1", "name": "route_claim", "args": {"claim_id": "C-1"}}]),
                AIMessage(content="done"),
            ]
        )
    )
    try:
        await create_react_agent(model, [tool]).ainvoke({"messages": [HumanMessage("route C-1")]})
    except Exception:
        pass


async def _fresh_openai(tool):
    from agents import Agent, Runner

    from .test_openai_agents import ScriptedModel, call, message, usage

    model = ScriptedModel(
        [([call("c1", "route_claim", {"claim_id": "C-1"})], usage(1, 1)), ([message("done")], usage(1, 1))]
    )
    await Runner.run(Agent(name="fresh", instructions="x", model=model, tools=[tool]), "route C-1")


async def _fresh_adk(tool):
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.genai import types

    from .test_adk import Scripted, fc, usage

    model = Scripted(
        turns=[
            ([fc("route_claim", {"claim_id": "C-1"}, call_id="c1")], usage(1, 1)),
            ([types.Part(text="done")], usage(1, 1)),
        ]
    )
    runner = InMemoryRunner(agent=LlmAgent(name="fresh", model=model, instruction="x", tools=[tool]), app_name="fresh")
    await runner.session_service.create_session(app_name="fresh", user_id="u", session_id="s")
    message = types.Content(role="user", parts=[types.Part(text="route C-1")])
    async for _ in runner.run_async(user_id="u", session_id="s", new_message=message):
        pass


async def _fresh_strands(tool):
    from strands import Agent

    from .test_strands import ScriptedModel, call, text, usage

    model = ScriptedModel(
        [([call("c1", "route_claim", {"claim_id": "C-1"})], usage(1, 1)), ([text("done")], usage(1, 1))]
    )
    await Agent(model=model, tools=[tool], callback_handler=None).invoke_async("route C-1")


async def _fresh_af(tool):
    from agent_framework import Agent

    from .test_agent_framework import ScriptedClient, call, text, usage

    client = ScriptedClient(
        [([call("c1", "route_claim", {"claim_id": "C-1"})], usage(1, 1)), ([text("done")], usage(1, 1))]
    )
    await Agent(client, "x", name="fresh", tools=[tool]).run("route C-1")


FRESH = {
    "langchain": _fresh_langgraph,
    "openai_agents": _fresh_openai,
    "adk": _fresh_adk,
    "strands": _fresh_strands,
    "agent_framework": _fresh_af,
}


@pytest.mark.parametrize("adapter", list(FRESH))
async def test_a_fresh_agent_no_hook_sees_cannot_run_a_scoped_tool_for_a_user_without_its_scopes(adapter):
    """The shape every nesting bypass comes down to: an agent the adapter never prepared (built inside a tool, run by
    a framework path no hook follows) calls the scoped tool for the task's user. Its body does not run. (CrewAI runs
    a crew's tools with tool.run on a worker thread, as the direct call above does.)"""
    kit, body = KITS[adapter], Body()
    tool = guarded(kit, body)
    async with adapter_task(scopes=SCOPES, granted=[]) as task:
        with bind_task(task.ctx):
            await FRESH[adapter](tool)
    assert body.ran == []
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            await FRESH[adapter](guarded(kit, body))
    assert body.ran == ["C-1"]


def _adk_router(ran, turns):
    from google.adk.agents import LlmAgent

    from .test_adk import Scripted

    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    return LlmAgent(name="router", model=Scripted(turns=turns), instruction="Route claims.", tools=[route_claim])


async def test_h1_adk_an_isolated_agent_tool_inside_an_approval_toolset_is_refused_before_the_run():
    """H1: with_approval([AgentTool(agent=router, include_plugins=False)]) hid the isolated agent tool from the pre-run
    walk (inside the approval toolset) and from the plugin (behind the approval wrapper)."""
    pytest.importorskip("google.adk")
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.adk.tools.agent_tool import AgentTool
    from google.genai import types

    from sam_link_sdk._adapter import NestedRunRefused
    from sam_link_sdk.adk import run_agent, step_reporter, with_approval

    from .test_adk import Scripted, fc, usage

    ran: list[str] = []
    router = _adk_router(
        ran,
        [
            ([fc("route_claim", {"claim_id": "C-1"}, call_id="c2")], usage(3, 1)),
            ([types.Part(text="routed")], usage(4, 1)),
        ],
    )
    front_model = Scripted(
        turns=[
            ([fc("router", {"request": "route C-1"}, call_id="c1")], usage(1, 1)),
            ([types.Part(text="Done.")], usage(2, 1)),
        ]
    )
    front = LlmAgent(
        name="front",
        model=front_model,
        instruction="x",
        tools=with_approval([AgentTool(agent=router, include_plugins=False)], names=["router"]),
    )
    async with adapter_task(scopes=SCOPES, granted=[]) as task:
        with pytest.raises(NestedRunRefused, match="include_plugins"):
            await run_agent(task.ctx, InMemoryRunner(agent=front, app_name="claims", plugins=[step_reporter()]))
    assert ran == [] and len(front_model.turns) == 2


async def test_h1_adk_the_plugin_sees_an_isolated_agent_tool_behind_the_approval_wrapper():
    """A toolset that hands the wrapped agent tool over only at run time: the plugin unwraps it at the call."""
    pytest.importorskip("google.adk")
    from google.adk.tools.agent_tool import AgentTool

    from sam_link_sdk._adapter import NestedRunRefused
    from sam_link_sdk.adk import step_reporter
    from sam_link_sdk.adk.tools import _GuardedTool

    router = _adk_router([], [])
    plugin = step_reporter()
    wrapped = _GuardedTool(AgentTool(agent=router, include_plugins=False))
    context = type("ToolContext", (), {"invocation_id": "i1", "function_call_id": "c1"})()
    async with adapter_task(scopes=SCOPES, granted=[]) as task:
        with bind_task(task.ctx), pytest.raises(NestedRunRefused, match="include_plugins"):
            await plugin.before_tool_callback(tool=wrapped, tool_args={}, tool_context=context)


def _af_router(ran, turns, name="router"):
    from agent_framework import Agent, tool

    from .test_agent_framework import ScriptedClient

    @tool(approval_mode="never_require")
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    return Agent(ScriptedClient(turns), "Route.", name=name, tools=[route_claim])


async def test_h2_agent_framework_an_agent_tool_a_context_provider_adds_for_the_run_is_routed():
    """H2: a ContextProvider adds router.as_tool() in before_run (SessionContext.extend_tools): the run is routed at
    each model call, from the tools that call is given, so the nested agent's steps, tokens and tool checks are the
    task's."""
    pytest.importorskip("agent_framework")
    from agent_framework import Agent, ContextProvider

    from sam_link_sdk.agent_framework import run_agent

    from .test_agent_framework import ScriptedClient, call, text, usage

    ran: list[str] = []
    router = _af_router(
        ran, [([call("c2", "route_claim", {"claim_id": "C-1"})], usage(3, 1)), ([text("Not routed.")], usage(4, 1))]
    )

    class AddsTheRouter(ContextProvider):
        async def before_run(self, *, agent, session, context, state):
            context.extend_tools("routing", [router.as_tool()])

    front = Agent(
        ScriptedClient([([call("c1", "router", {"task": "route C-1"})], usage(1, 1)), ([text("Done.")], usage(2, 1))]),
        "Ask.",
        name="front",
        context_providers=[AddsTheRouter("routing")],
    )
    async with adapter_task(scopes=SCOPES, declared=["router", "lookup_policy"], granted=[]) as task:
        ctx = task.ctx
        assert await run_agent(ctx, front) == "Done."
        sent = steps(task)
    assert ran == []
    assert [type(e) for e in sent].count(ModelFinished) == 4
    assert ctx._run.usage.final_token_usage()["total_input_tokens"] == 10
    refused = [e for e in sent if isinstance(e, ToolFinished) and e.name == "route_claim"]
    assert [r.result for r in refused] == [{"error": REFUSED}]


async def test_h3_agent_framework_a_workflow_agent_behind_a_tool_is_refused():
    """H3: workflow.as_agent().as_tool(): the agents inside the workflow run through no middleware a router could
    reach, so the run is refused before any model call."""
    pytest.importorskip("agent_framework")
    from agent_framework import Agent, WorkflowBuilder

    from sam_link_sdk._adapter import NestedRunRefused
    from sam_link_sdk.agent_framework import run_agent

    from .test_agent_framework import ScriptedClient, call, text, usage

    ran: list[str] = []
    inner = _af_router(
        ran, [([call("c2", "route_claim", {"claim_id": "C-1"})], usage(3, 1)), ([text("Not routed.")], usage(4, 1))]
    )
    workflow = WorkflowBuilder(start_executor=inner).build()
    front_client = ScriptedClient(
        [([call("c1", "flow", {"task": "route C-1"})], usage(1, 1)), ([text("Done.")], usage(2, 1))]
    )
    front = Agent(front_client, "Ask.", name="front", tools=[workflow.as_agent(name="flow").as_tool()])
    async with adapter_task(scopes=SCOPES, granted=[]) as task:
        with pytest.raises(NestedRunRefused, match="WorkflowAgent"):
            await run_agent(task.ctx, front)
    assert ran == [] and len(front_client.turns) == 2


async def test_agent_framework_an_agent_once_run_as_a_tool_is_reported_once_when_it_runs_as_the_main_agent():
    """The router an agent gets as a tool is taken off when the run ends, and does nothing for the run's own top-level
    agent: run directly afterwards, or alongside, its steps and tokens are reported once and its approvals asked
    once."""
    pytest.importorskip("agent_framework")
    from agent_framework import Agent

    from sam_link_sdk.agent_framework import run_agent, with_approval

    from .test_agent_framework import ScriptedClient, call, text, usage

    ran: list[str] = []
    router = _af_router(
        ran,
        [
            ([text("first")], usage(3, 1)),
            ([call("c9", "route_claim", {"claim_id": "C-9"})], usage(5, 1)),
            ([text("second")], usage(6, 1)),
        ],
    )
    middleware_before = list(router.middleware or [])
    front = Agent(
        ScriptedClient([([call("c1", "router", {"task": "hi"})], usage(1, 1)), ([text("Done.")], usage(2, 1))]),
        "Ask.",
        name="front",
        tools=[router.as_tool()],
    )
    async with adapter_task("T1") as first:
        assert await run_agent(first.ctx, front) == "Done."
    assert list(router.middleware or []) == middleware_before

    asked: list[str] = []

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        from types import SimpleNamespace

        return SimpleNamespace(approved=True, outcome="approved")

    async with adapter_task("T2") as task:
        second = task.ctx
        second.request_approval = request_approval
        route_claim = router.default_options["tools"][0]
        assert await run_agent(second, router, middleware=[with_approval(second, [route_claim])]) == "second"
        kinds = [type(e) for e in steps(task)]
    assert kinds.count(ModelFinished) == 2 and kinds.count(ToolStarted) == 1
    assert second._run.usage.final_token_usage()["total_input_tokens"] == 11
    assert asked == ["route_claim"] and ran == ["C-9"]


async def test_langchain_a_nested_run_on_a_worker_thread_stops_at_its_next_model_call_once_the_task_is_stopped():
    """A synchronous tool runs on a worker thread and starts a run of its own there, with the task's callbacks: once
    the task is stopped, that run's next model call (or tool call) ends it, as CrewAI's adapter does."""
    pytest.importorskip("langchain_core")
    from langchain_core.messages import AIMessage
    from langchain_core.tools import tool

    from sam_link_sdk.context import TaskHalted
    from sam_link_sdk.langchain import run_agent

    from .test_callbacks import ToolCallingFake

    inner = ToolCallingFake(messages=iter([AIMessage(content="never")]))
    inner_calls: list[str] = []
    held: list = []

    @tool
    def consult(question: str) -> str:
        """Ask another model."""
        held[0]._run.halt()
        try:
            return inner.invoke(question).text
        except TaskHalted:
            inner_calls.append("refused")
            raise

    outer = ToolCallingFake(
        messages=iter(
            [
                AIMessage(content="", tool_calls=[{"id": "c1", "name": "consult", "args": {"question": "q"}}]),
                AIMessage(content="never reached"),
            ]
        )
    )
    async with adapter_task() as task:
        held.append(task.ctx)
        with pytest.raises(TaskHalted):
            await run_agent(task.ctx, outer, [consult])
    assert inner_calls == ["refused"]


async def test_strands_an_agent_that_runs_tools_in_the_background_is_refused():
    """Strands' background tools (Agent(background_tasks=...)) outlive the model turn, in asyncio tasks a stop of the
    mesh task does not reach."""
    pytest.importorskip("strands")
    from strands import Agent

    from sam_link_sdk._adapter import NestedRunRefused
    from sam_link_sdk.strands import run_agent

    from .test_strands import ScriptedModel, text, usage

    tool = _strands_make(Body().fn)
    model = ScriptedModel([([text("never")], usage(1, 1))])
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        with pytest.raises(NestedRunRefused, match="background"):
            await run_agent(
                task.ctx, lambda c: Agent(model=model, tools=[tool], background_tasks=True, callback_handler=None)
            )
    assert model.turns


async def test_strands_the_hooks_refuse_a_tool_call_once_the_task_is_stopped():
    pytest.importorskip("strands")
    from strands import Agent, tool
    from strands.tools.executors import SequentialToolExecutor

    from sam_link_sdk.strands import run_agent

    from .test_strands import ScriptedModel, call, text, usage

    ran: list[str] = []
    held: list = []

    @tool
    def stop_then_lookup(policy_id: str) -> str:
        """Look up a policy."""
        ran.append(policy_id)
        held[0]._run.halt()
        return "gold"

    model = ScriptedModel(
        [
            (
                [
                    call("c1", "stop_then_lookup", {"policy_id": "P-1"}),
                    call("c2", "stop_then_lookup", {"policy_id": "P-2"}),
                ],
                usage(1, 1),
            ),
            ([text("never")], usage(1, 1)),
        ]
    )
    async with adapter_task(scopes=SCOPES, declared=["stop_then_lookup"], granted=[SCOPE]) as task:
        held.append(task.ctx)
        with pytest.raises(Exception, match="was stopped"):
            await run_agent(
                task.ctx,
                lambda c: Agent(
                    model=model, tools=[stop_then_lookup], callback_handler=None, tool_executor=SequentialToolExecutor()
                ),
            )
    assert ran == ["P-1"] and len(model.turns) == 1


async def test_openai_agents_an_approved_tool_that_raises_is_reported_failed():
    pytest.importorskip("agents")
    from types import SimpleNamespace

    from agents import Agent, function_tool

    from sam_link_sdk.openai_agents import run_agent, with_approval

    from .test_openai_agents import ScriptedModel, call, message, usage

    @function_tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        raise RuntimeError("queue service down")

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        return SimpleNamespace(approved=True, outcome="approved")

    model = ScriptedModel(
        [([call("c1", "route_claim", {"claim_id": "C-1"})], usage(1, 1)), ([message("It failed.")], usage(1, 1))]
    )
    async with adapter_task() as task:
        ctx = task.ctx
        ctx.request_approval = request_approval
        agent = Agent(name="triage", instructions="x", model=model, tools=with_approval(ctx, [route_claim]))
        assert await run_agent(ctx, agent) == "It failed."
        [end] = [e for e in steps(task) if isinstance(e, ToolFinished)]
    assert set(end.result) == {"error"} and "queue service down" in json.dumps(end.result)


async def test_a_scoped_strands_tool_refuses_under_the_opentelemetry_adapter_too():
    """The generic adapter only reads spans, so it cannot refuse a call; a tool guarded with with_scopes refuses
    itself, since run_agent runs `run` with the task current."""
    pytest.importorskip("strands")
    pytest.importorskip("opentelemetry.sdk")
    from strands import Agent

    from sam_link_sdk.otel_steps import run_agent
    from sam_link_sdk.strands import with_scopes

    from .test_strands import ScriptedModel, call, text, usage

    body = Body()
    [tool] = with_scopes([_strands_make(body.fn)], SCOPES)
    model = ScriptedModel(
        [([call("c1", "route_claim", {"claim_id": "C-1"})], usage(1, 1)), ([text("done")], usage(1, 1))]
    )
    agent = Agent(model=model, tools=[tool], callback_handler=None)
    async with adapter_task(scopes=SCOPES, granted=[]) as task:
        await run_agent(task.ctx, agent.invoke_async)
    assert body.ran == []


async def test_a_mesh_agent_runs_its_handler_with_the_task_current():
    """So a handler's own framework run (a LangGraph graph it invokes, a crew it kicks off) gives its scoped tools the
    task without the adapter's run_agent."""
    seen = []

    async def handler(ctx):
        seen.append(current_task() is ctx)
        return "ok"

    agent, transport = await start_agent(handler)
    try:
        await transport.deliver("T1")
    finally:
        await agent.stop()
    assert seen == [True] and transport.finished["T1"].text == "ok"


async def test_hostile_langchain_every_entry_point_to_a_scoped_tool_refuses():
    """The question each test answers: is there any way to run the body of a scoped (or approval-guarded) tool for a task
    or user that should not, by a path no hook sees? Each tries the framework's other entry points to the same tool
    object, and copies or renamings of it, outside any task and for a user without the scope.

    The control: the same entry points run it for a user with the scope (the renamed copy is a tool of its own name, so
    the card declares it too).
    """
    pytest.importorskip("langchain_core")
    from langchain_core.tools import ToolException

    body = Body()
    tool = guarded(KITS["langchain"], body)
    renamed = tool.model_copy(update={"name": "route_claim_copy"})
    args = {"claim_id": "C-1"}
    attempts = [
        lambda: tool.invoke(args),
        lambda: tool.run(args),
        lambda: tool.batch([args]),
        lambda: tool.with_config(tags=["x"]).invoke(args),
        lambda: renamed.invoke(args),
    ]
    async_attempts = [lambda: tool.ainvoke(args), lambda: tool.arun(args), lambda: tool.abatch([args])]
    async with adapter_task(scopes=SCOPES, granted=[]) as denied:
        for ctx in (None, denied.ctx):
            with bind_task(ctx) if ctx is not None else contextlib.nullcontext():
                for attempt in attempts:
                    with pytest.raises(ToolException):
                        attempt()
                for attempt in async_attempts:
                    with pytest.raises(ToolException):
                        await attempt()
    assert body.ran == []
    async with adapter_task(scopes={**SCOPES, "route_claim_copy": [SCOPE]}, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            for attempt in attempts:
                attempt()
            for attempt in async_attempts:
                await attempt()
    assert len(body.ran) == len(attempts) + len(async_attempts)


async def test_hostile_openai_agents_a_copy_or_renamed_copy_of_a_scoped_tool_still_refuses():
    pytest.importorskip("agents")
    import copy
    import dataclasses

    body = Body()
    tool = guarded(KITS["openai_agents"], body)
    copies = [tool, copy.copy(tool), dataclasses.replace(tool, name="route_claim_copy")]
    with pytest.raises(AttributeError):
        _ = tool.__wrapped__
    async with adapter_task(scopes=SCOPES, granted=[]) as task:
        with bind_task(task.ctx):
            texts = [await _openai_call(t, {"claim_id": "C-1"}) for t in copies]
            await _fresh_openai(copies[1])
    assert body.ran == [] and all("Access denied" in t for t in texts)
    async with adapter_task(scopes={**SCOPES, "route_claim_copy": [SCOPE]}, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            await _fresh_openai(copies[1])
            assert [await _openai_call(t, {"claim_id": "C-2"}) for t in copies] and len(body.ran) == 4


@pytest.mark.parametrize(
    "guard,granted,runs",
    [(False, [], True), (True, [], False), (True, [SCOPE], True)],
    ids=["unguarded", "guarded_without_grant", "guarded_with_grant"],
)
async def test_hostile_adk_an_isolated_agent_tool_run_by_hand_cannot_run_a_scoped_tool(guard, granted, runs):
    """No step reporter at all: the user's code runs, in a Runner with no plugin, an agent whose isolated AgentTool's
    agent calls the scoped tool. The inner agent really runs (the unguarded control shows the body running)."""
    pytest.importorskip("google.adk")
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.adk.tools.agent_tool import AgentTool
    from google.genai import types

    from .test_adk import Scripted, fc, usage

    body = Body()
    tool = guarded(KITS["adk"], body) if guard else body.fn
    inner = LlmAgent(
        name="router",
        model=Scripted(
            turns=[
                ([fc("route_claim", {"claim_id": "C-1"}, call_id="c2")], usage(1, 1)),
                ([types.Part(text="no")], usage(1, 1)),
            ]
        ),
        instruction="x",
        tools=[tool],
    )
    outer_model = Scripted(
        turns=[
            ([fc("router", {"request": "route C-1"}, call_id="c1")], usage(1, 1)),
            ([types.Part(text="done")], usage(1, 1)),
        ]
    )
    outer = LlmAgent(
        name="front", model=outer_model, instruction="x", tools=[AgentTool(agent=inner, include_plugins=False)]
    )
    runner = InMemoryRunner(agent=outer, app_name="fresh")
    await runner.session_service.create_session(app_name="fresh", user_id="u", session_id="s")
    message = types.Content(role="user", parts=[types.Part(text="route C-1")])
    async with adapter_task(scopes=SCOPES, granted=granted) as task:
        with bind_task(task.ctx):
            async for _ in runner.run_async(user_id="u", session_id="s", new_message=message):
                pass
    assert body.ran == (["C-1"] if runs else []) and not outer_model.turns


async def test_hostile_crewai_the_structured_tool_crewai_builds_and_a_renamed_copy_refuse():
    pytest.importorskip("crewai")
    body = Body()
    tool = guarded(KITS["crewai"], body)
    structured = tool.to_structured_tool()
    renamed = tool.model_copy(update={"name": "route_claim_copy"})
    async with adapter_task(scopes=SCOPES, granted=[]) as denied:
        for ctx in (None, denied.ctx):
            with bind_task(ctx) if ctx is not None else contextlib.nullcontext():
                results = [
                    await asyncio.to_thread(structured.invoke, {"claim_id": "C-1"}),
                    await asyncio.to_thread(renamed.run, claim_id="C-1"),
                ]
            assert all("error" in json.loads(r) for r in results)
    assert body.ran == []
    async with adapter_task(scopes={**SCOPES, "route_claim_copy": [SCOPE]}, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            assert [
                await asyncio.to_thread(structured.invoke, {"claim_id": "C-1"}),
                await asyncio.to_thread(renamed.run, claim_id="C-2"),
            ] == ["fast-track", "fast-track"]


async def test_hostile_strands_a_direct_tool_call_on_the_agent_refuses():
    """Strands lets code call an agent's tool directly (agent.tool.<name>(...)), with no model and no hooks asked."""
    pytest.importorskip("strands")
    from strands import Agent

    from .test_strands import ScriptedModel

    body = Body()
    agent = Agent(model=ScriptedModel([]), tools=[guarded(KITS["strands"], body)], callback_handler=None)
    outside = agent.tool.route_claim(claim_id="C-1", record_direct_tool_call=False)
    async with adapter_task(scopes=SCOPES, granted=[]) as task:
        with bind_task(task.ctx):
            inside = await asyncio.to_thread(agent.tool.route_claim, claim_id="C-1", record_direct_tool_call=False)
    assert body.ran == []
    assert "no mesh task is running here" in json.dumps(outside) and REFUSED in json.dumps(inside)
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            await asyncio.to_thread(agent.tool.route_claim, claim_id="C-1", record_direct_tool_call=False)
    assert body.ran == ["C-1"]


async def test_hostile_agent_framework_calling_the_tool_object_or_a_copy_refuses():
    pytest.importorskip("agent_framework")
    import copy

    body = Body()
    tool = guarded(KITS["agent_framework"], body)
    async with adapter_task(scopes=SCOPES, granted=[]) as denied:
        for ctx in (None, denied.ctx):
            with bind_task(ctx) if ctx is not None else contextlib.nullcontext():
                direct = await asyncio.to_thread(tool, claim_id="C-1")
                copied = await _af_call(copy.copy(tool), {"claim_id": "C-1"})
            assert "error" in json.loads(direct) and "error" in copied
            assert ("no mesh task" if ctx is None else "Access denied") in direct
    assert body.ran == []
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            await asyncio.to_thread(tool, claim_id="C-1")
            await _af_call(copy.copy(tool), {"claim_id": "C-2"})
    assert body.ran == ["C-1", "C-2"]


async def test_hostile_hermes_a_session_that_names_no_run_or_a_closed_one_is_refused(monkeypatch):
    """Hermes' registry tools cannot carry a check, so the hook decides: a grandchild a background subagent starts
    after its parent's turn ended joins the closed run and is refused; a tool call with no session while a mesh run is
    open is refused."""
    from sam_link_sdk.hermes import run_agent, steps as hermes_steps

    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)
    blocked = []

    def turn(h, agent):
        blocked.append(h.call_tool(None, "lookup_policy", "c0"))
        h.fire("subagent_start", parent_session_id="sam-T", child_session_id="child-bg")

    async with adapter_task() as task:
        await run_agent(task.ctx, lambda c: hermes.agent(["lookup_policy", "delegate_task"], turn))
        hermes.fire("subagent_start", parent_session_id="child-bg", child_session_id="grandchild")
        late = hermes.call_tool("grandchild", "lookup_policy", "c1")
    assert blocked[0]["action"] == "block" and late["action"] == "block" and hermes.ran == []
    assert not hermes_steps.runs_active()


@pytest.mark.parametrize("adapter", APPROVAL_WRAPPERS)
async def test_an_approval_guarded_tool_asks_nobody_once_its_task_is_answered(adapter):
    kit, body = KITS[adapter], Body()
    kit.adapter()
    asked: list[str] = []

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        raise AssertionError("asked")

    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        ctx = task.ctx
        ctx.request_approval = request_approval
        with bind_task(ctx):
            tool = await _approval_tool(kit, ctx, kit.make(body.fn))
        await task.run(lambda c: "Claim C-1 is routed.")
        await settle()
        assert task.answer is not None and ctx._run.answered
        with bind_task(ctx):
            text = await kit.call(tool, {"claim_id": "C-1"})
    assert body.ran == [] and asked == [] and "already been answered" in text
