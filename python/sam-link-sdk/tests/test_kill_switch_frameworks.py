# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The kill switch in each framework: the per-framework guards stop runs outside the mesh, and a stopped mesh task's
CrewAI crew stops at its next model call."""

from __future__ import annotations

import asyncio
import json
import os

import pytest

from sam_link_sdk import AgentStopped
from sam_link_sdk.context import TaskHalted

from .adapter_task import adapter_task


class Switch:
    """Stands in for MeshAgent: check() raises while stopped."""

    def __init__(self, stopped=False):
        self.stopped = stopped

    def check(self):
        if self.stopped:
            raise AgentStopped("stopped_by_sam")


async def test_check_running_raises_once_the_task_is_stopped():
    async with adapter_task(text="hi") as task:
        task.ctx.check_running()
        task.ctx._run.halt()
        with pytest.raises(TaskHalted):
            task.ctx.check_running()


pytest.importorskip("langgraph")

from langchain_core.messages import AIMessage  # noqa: E402
from langgraph.prebuilt import create_react_agent  # noqa: E402

from sam_link_sdk.langchain.stop import StopGuard  # noqa: E402

from .test_langgraph import ToolCallingFake, usage as lc_usage  # noqa: E402


def part_price(part: str) -> str:
    """Price of a part."""
    return "$900"


async def test_a_langgraph_agent_refuses_to_run_and_stops_mid_run():
    switch = Switch()

    def pricing(part: str) -> str:
        """Price of a part."""
        switch.stopped = True
        return "$900"

    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content="",
                    tool_calls=[{"id": "c1", "name": "pricing", "args": {"part": "door"}}],
                    usage_metadata=lc_usage(10, 2),
                ),
                AIMessage(content="Estimate: $900.", usage_metadata=lc_usage(12, 2)),
            ]
        )
    )
    graph = create_react_agent(model, [pricing]).with_config(callbacks=[StopGuard(switch)])
    with pytest.raises(AgentStopped):
        await graph.ainvoke({"messages": [("user", "Estimate")]})

    with pytest.raises(AgentStopped):
        await graph.ainvoke({"messages": [("user", "Estimate")]})


pytest.importorskip("google.adk")

from google.adk.agents import LlmAgent  # noqa: E402
from google.adk.runners import InMemoryRunner  # noqa: E402
from google.genai import types  # noqa: E402

from sam_link_sdk.adk.stop import StopPlugin  # noqa: E402

from .test_adk import Scripted, fc, usage as adk_usage  # noqa: E402


async def run_adk(runner):
    session = await runner.session_service.create_session(app_name="claims", user_id="u")
    text = ""
    async for event in runner.run_async(
        user_id="u", session_id=session.id, new_message=types.Content(role="user", parts=[types.Part(text="hi")])
    ):
        if event.is_final_response() and event.content and event.content.parts:
            text = event.content.parts[0].text or text
    return text


async def test_an_adk_runner_refuses_to_run_and_stops_before_the_next_tool_or_model_call():
    """ADK wraps a plugin's exception in RuntimeError("Error in plugin ..."), keeping AgentStopped as its cause."""
    switch, calls = Switch(), []

    def lookup_policy(policy_id: str) -> dict:
        """Look up a policy."""
        calls.append(policy_id)
        switch.stopped = True
        return {"tier": "gold"}

    model = Scripted(
        turns=[
            ([fc("lookup_policy", {"policy_id": "P-1"}, "c1")], adk_usage(10, 2)),
            ([types.Part(text="Gold.")], adk_usage(12, 2)),
        ]
    )
    agent = LlmAgent(name="claims", model=model, instruction="x", tools=[lookup_policy])
    runner = InMemoryRunner(agent=agent, app_name="claims", plugins=[StopPlugin(switch)])
    with pytest.raises(RuntimeError) as stopped:
        await run_adk(runner)
    assert isinstance(stopped.value.__cause__, AgentStopped)
    assert calls == ["P-1"] and len(model.turns) == 1

    with pytest.raises(RuntimeError) as refused:
        await run_adk(runner)
    assert isinstance(refused.value.__cause__, AgentStopped)


pytest.importorskip("agents")

from agents import Agent, Runner, function_tool  # noqa: E402

from sam_link_sdk.openai_agents.stop import StopHooks  # noqa: E402

from .test_openai_agents import ScriptedModel, message, usage as oa_usage  # noqa: E402


async def test_an_openai_agents_run_refuses_to_start_and_stops_before_the_next_model_call():
    switch = Switch()

    @function_tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        switch.stopped = True
        return "fast-track"

    from agents.items import ModelResponse  # noqa: F401  (ScriptedModel returns it)
    from openai.types.responses import ResponseFunctionToolCall

    call = ResponseFunctionToolCall(
        type="function_call",
        call_id="c1",
        name="route_claim",
        arguments='{"claim_id": "P-1"}',
        id="fc1",
        status="completed",
    )
    model = ScriptedModel([([call], oa_usage(10, 2)), ([message("Routed.")], oa_usage(12, 2))])
    agent = Agent(name="claims", instructions="x", model=model, tools=[route_claim])
    with pytest.raises(AgentStopped):
        await Runner.run(agent, "Route P-1", hooks=StopHooks(switch))
    assert len(model.turns) == 1

    with pytest.raises(AgentStopped):
        await Runner.run(agent, "Route P-1", hooks=StopHooks(switch))


pytest.importorskip("crewai")
os.environ.setdefault("CREWAI_DISABLE_TELEMETRY", "true")

from crewai import (  # noqa: E402
    Agent as CrewAgent,
    Crew,
    Task,
)
from crewai.llms.base_llm import BaseLLM  # noqa: E402
from crewai.tools import BaseTool  # noqa: E402

from sam_link_sdk.crewai import run_agent, stop_guard  # noqa: E402

REACT = [
    'Thought: look\nAction: lookup\nAction Input: {"query": "a"}',
    'Thought: again\nAction: lookup\nAction Input: {"query": "b"}',
    "Thought: done\nFinal Answer: answer",
]


class ScriptedLLM(BaseLLM):
    def __init__(self, replies):
        super().__init__(model="scripted")
        self.replies, self.calls = list(replies), 0

    def call(self, messages, tools=None, callbacks=None, available_functions=None, **kwargs):
        self.calls += 1
        return self.replies.pop(0) if self.replies else "Thought: done\nFinal Answer: fallback"

    def supports_function_calling(self):
        return False

    def supports_stop_words(self):
        return False

    def get_context_window_size(self):
        return 8000


class Lookup(BaseTool):
    name: str = "lookup"
    description: str = "Look something up."
    on_run: object = None

    def _run(self, query: str = "") -> str:
        if self.on_run:
            self.on_run()
        return "gold"


class AsyncScriptedLLM(ScriptedLLM):
    def __init__(self, replies):
        super().__init__(replies)
        self.offered = []

    async def acall(self, messages, tools=None, callbacks=None, available_functions=None, **kwargs):
        self.calls += 1
        self.offered.append([t["function"]["name"] for t in tools or []])
        return "ok"


async def test_the_async_model_call_is_checked_and_offered_only_the_tools_the_user_may_use():
    from types import SimpleNamespace

    from sam_link_sdk.crewai.run import _hide_denied_tools
    from sam_link_sdk.crewai.steps import _current
    from sam_link_sdk.crewai.stop import guard_llm

    llm = AsyncScriptedLLM([])
    state = SimpleNamespace(stopped=False)

    def check():
        if state.stopped:
            raise AgentStopped("stopped_by_sam")

    guard_llm(llm, check)
    _hide_denied_tools([llm])
    specs = [{"type": "function", "function": {"name": n}} for n in ("lookup", "route_claim")]
    token = _current.set(SimpleNamespace(scopes=SimpleNamespace(permits=lambda name: name != "route_claim")))
    try:
        assert await llm.acall([], tools=specs) == "ok"
    finally:
        _current.reset(token)
    assert llm.offered == [["lookup"]]
    state.stopped = True
    with pytest.raises(AgentStopped):
        await llm.acall([], tools=specs)
    assert llm.calls == 1


def make_crew(llm, tool):
    agent = CrewAgent(role="r", goal="g", backstory="b", llm=llm, tools=[tool], verbose=False, max_iter=4)
    return Crew(agents=[agent], tasks=[Task(description="Do it", expected_output="x", agent=agent)], verbose=False)


def test_a_guarded_crew_refuses_to_start_and_stops_at_its_next_model_call():
    switch = Switch()
    llm = ScriptedLLM(REACT)
    crew = stop_guard(switch, make_crew(llm, Lookup(on_run=lambda: setattr(switch, "stopped", True))))
    with pytest.raises(AgentStopped):
        crew.kickoff()
    assert llm.calls == 1

    with pytest.raises(AgentStopped):
        crew.kickoff()
    assert llm.calls == 1


async def test_a_stopped_mesh_task_stops_its_crew_at_the_next_model_call():
    llm = ScriptedLLM(REACT)
    async with adapter_task(text="hi") as task:
        crew = make_crew(llm, Lookup(on_run=lambda: task.ctx._run.halt()))
        with pytest.raises(TaskHalted):
            await run_agent(task.ctx, lambda _: crew)
    assert llm.calls == 1


async def test_a_crew_outside_run_agent_is_not_checked_against_any_task():
    llm = ScriptedLLM(["Thought: done\nFinal Answer: answer"])
    crew = make_crew(llm, Lookup())
    async with adapter_task(text="hi") as task:
        await run_agent(task.ctx, lambda _: crew)
    out = await asyncio.to_thread(crew.kickoff)
    assert getattr(out, "raw", "") and llm.calls == 2


MANAGER_DELEGATES = [
    "Thought: ask the worker\nAction: Delegate work to coworker\n"
    'Action Input: {"task": "look it up", "context": "claim P-1", "coworker": "r"}'
] * 4


@pytest.mark.parametrize("manager", ["model name", "manager agent"])
async def test_a_stopped_mesh_task_stops_a_hierarchical_crew_s_manager_too(manager, monkeypatch):
    """CrewAI builds the manager of a hierarchical crew when the kickoff starts (from manager_llm, often a model
    name), or takes a manager_agent; either way its model calls stop with the task, not at its max_iter."""
    import crewai.crew
    from crewai import Process

    async with adapter_task(text="hi") as task:
        ctx = task.ctx
        manager_llm = ScriptedLLM(MANAGER_DELEGATES)
        worker = CrewAgent(
            role="r",
            goal="g",
            backstory="b",
            llm=ScriptedLLM(REACT),
            verbose=False,
            max_iter=4,
            tools=[Lookup(on_run=lambda: ctx._run.halt())],
        )
        kwargs = {}
        if manager == "model name":
            monkeypatch.setattr(crewai.crew, "create_llm", lambda value: manager_llm if value == "gpt-4o" else value)
            kwargs["manager_llm"] = "gpt-4o"
        else:
            kwargs["manager_agent"] = CrewAgent(
                role="manager",
                goal="g",
                backstory="b",
                llm=manager_llm,
                allow_delegation=True,
                verbose=False,
                max_iter=4,
            )
        crew = Crew(
            agents=[worker],
            tasks=[Task(description="Do it", expected_output="x")],
            process=Process.hierarchical,
            verbose=False,
            **kwargs,
        )
        with pytest.raises(TaskHalted):
            await run_agent(ctx, lambda _: crew)
    assert manager_llm.calls == 1


async def test_crewai_run_agent_keeps_the_conversation_with_memory():
    from sam_link_sdk import ConversationMemory

    seen = []

    class Recording(ScriptedLLM):
        def call(self, messages, **kwargs):
            seen.append(json.dumps(messages, default=str))
            return super().call(messages, **kwargs)

    memory = ConversationMemory()

    def crew(_ctx):
        agent = CrewAgent(
            role="r",
            goal="g",
            backstory="b",
            llm=Recording(["Thought: done\nFinal Answer: Gold."]),
            verbose=False,
            max_iter=2,
        )
        return Crew(
            agents=[agent],
            tasks=[Task(description="{request}\\nEarlier:\\n{history}", expected_output="x", agent=agent)],
            verbose=False,
        )

    async with adapter_task("T1", "hi") as task:
        assert await run_agent(task.ctx, crew, memory=memory) == "Gold."
    async with adapter_task("T2", "And P-2?") as task:
        await run_agent(task.ctx, crew, memory=memory)
    assert "Assistant: Gold." in seen[-1] and "User: hi" in seen[-1]


async def test_a_stopped_mesh_task_stops_a_crew_its_tool_kicks_off():
    """A crew a tool kicks off has LLMs run_agent never saw: CrewAI's before-LLM hook stops their calls too."""
    inner_llm = ScriptedLLM(["Thought: done\nFinal Answer: inner"])
    async with adapter_task(text="hi") as task:
        ctx = task.ctx

        def run_inner():
            ctx._run.halt()
            try:
                make_crew(inner_llm, Lookup()).kickoff()
            except Exception:
                pass

        crew = make_crew(ScriptedLLM(REACT), Lookup(on_run=run_inner))
        with pytest.raises(TaskHalted):
            await run_agent(ctx, lambda _: crew)
    assert inner_llm.calls == 0
