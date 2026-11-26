# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A stop that one of our hooks, guards or callbacks raises (TaskHalted) reaches the agent bare, whatever the framework
does with it on the way: ADK wraps a plugin's exception in a RuntimeError, Strands a hook's in an EventLoopException,
CrewAI's executor its own way, Hermes interrupts its turn and reports it failed. Every adapter's ``run_agent`` ends in
the bare TaskHalted of its task (``_adapter.halts_bare``), so the agent answers it with the stop's message, not as a
handler crash.

Each run below is stopped inside a tool, as SAM's stop does when the agent link has been down past the offline grace
there (``agent_stop_reason`` read live, before the agent's offline timer applies it) or a limit stops the task
(``ctx._run.halt()``); the next hook then raises.

What each framework does with what our code raises (the survey in docs/contributing/adapters.md, "When a hook stops a
run") is pinned per adapter by ``WRAPS``: the exception type ``run_agent`` would let out without ``halts_bare``.
"""

from __future__ import annotations

import asyncio
import json
import logging
import time

import pytest

from sam_link_sdk._model import RUN
from sam_link_sdk.context import TaskHalted

from .adapter_task import adapter_task
from .agent_rig import RecordingTransport, start_agent

STOPS = {"disconnected": "agent", "stopped": "task"}


class LapsingTransport(RecordingTransport):
    """A link that has been down past the offline grace, on the clock only: ``lapse()`` makes the agent's stop read as
    ``disconnected`` live, with nothing applied yet that the agent reacts to."""

    def lapse(self) -> None:
        stop = self.host._stopper.state
        stop.connection(True)
        stop.connection(False)
        grace = stop._grace
        stop._clock = lambda: time.monotonic() + 10 * grace + 1


async def run_in_task(stop: str, run) -> tuple[dict, LapsingTransport]:
    """Run `run(ctx, trip)` as the handler of task T1 on a real MeshAgent, where `trip()` stops it as `stop` says, as it
    would inside a tool. Returns what the run returned or raised (``returned``/``raised``) with its ``ctx``, and the
    transport.

    The test is skipped when a framework the run needs is not installed.
    """
    transport = LapsingTransport()
    seen: dict = {}

    async def handler(ctx):
        def trip():
            if STOPS[stop] == "agent":
                transport.lapse()
            else:
                ctx._run.halt()

        seen["ctx"] = ctx
        try:
            seen["returned"] = await run(ctx, trip)
        except BaseException as exc:
            seen["raised"] = exc
            raise
        return seen["returned"]

    agent, _ = await start_agent(handler, transport)
    try:
        await transport.deliver("T1", "Route claim C-1")
        for _ in range(1000):
            if "T1" in transport.finished:
                break
            await asyncio.sleep(0.01)
    finally:
        await agent.stop()
    if isinstance(seen.get("raised"), pytest.skip.Exception):
        raise seen["raised"]
    assert "T1" in transport.finished, seen
    return seen, transport


def bare(exc: BaseException) -> bool:
    return isinstance(exc, TaskHalted) and type(exc).__module__ == "sam_link_sdk.context" and exc.task_id == "T1"


async def run_langchain(ctx, trip):
    pytest.importorskip("langchain_core")
    from langchain_core.messages import AIMessage
    from langchain_core.tools import tool

    from sam_link_sdk.langchain import run_agent

    from .test_callbacks import ToolCallingFake

    @tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        trip()
        return "fast-track"

    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(content="", tool_calls=[{"id": "c1", "name": "route_claim", "args": {"claim_id": "C-1"}}]),
                AIMessage(content="Routed."),
            ]
        )
    )
    return await run_agent(ctx, model, [route_claim])


async def run_openai(ctx, trip):
    pytest.importorskip("agents")
    from agents import Agent, function_tool

    from sam_link_sdk.openai_agents import run_agent

    from .test_openai_agents import ScriptedModel, call, message, usage

    @function_tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        trip()
        return "fast-track"

    model = ScriptedModel(
        [([call("c1", "route_claim", {"claim_id": "C-1"})], usage(1, 1)), ([message("Routed.")], usage(1, 1))]
    )
    return await run_agent(ctx, Agent(name="claims", instructions="x", model=model, tools=[route_claim]))


async def run_adk(ctx, trip):
    pytest.importorskip("google.adk")
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.genai import types

    from sam_link_sdk.adk import run_agent, step_reporter

    from .test_adk import Scripted, fc, usage

    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        trip()
        return "fast-track"

    model = Scripted(
        turns=[
            ([fc("route_claim", {"claim_id": "C-1"}, call_id="c1")], usage(1, 1)),
            ([types.Part(text="Routed.")], usage(1, 1)),
        ]
    )
    agent = LlmAgent(name="claims", model=model, instruction="x", tools=[route_claim])
    return await run_agent(ctx, InMemoryRunner(agent=agent, app_name="claims", plugins=[step_reporter()]))


async def run_strands(ctx, trip):
    pytest.importorskip("strands")
    from strands import Agent, tool

    from sam_link_sdk.strands import run_agent

    from .test_strands import ScriptedModel, call, text, usage

    @tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        trip()
        return "fast-track"

    model = ScriptedModel(
        [([call("c1", "route_claim", {"claim_id": "C-1"})], usage(1, 1)), ([text("Routed.")], usage(1, 1))]
    )
    return await run_agent(ctx, lambda c: Agent(model=model, tools=[route_claim], callback_handler=None))


async def run_agent_framework(ctx, trip):
    pytest.importorskip("agent_framework")
    from agent_framework import tool

    from sam_link_sdk.agent_framework import run_agent

    from .test_agent_framework import ScriptedClient, agent_of, call, text, usage

    @tool(approval_mode="never_require")
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        trip()
        return "fast-track"

    client = ScriptedClient(
        [([call("c1", "route_claim", {"claim_id": "C-1"})], usage(1, 1)), ([text("Routed.")], usage(1, 1))]
    )
    return await run_agent(ctx, agent_of(client, [route_claim]))


async def run_crewai(ctx, trip):
    """A real crew (text tool calling) on CrewAI's worker thread: its next model call is stopped by the crew agent's
    guarded LLM and CrewAI's before-LLM hook."""
    pytest.importorskip("crewai")
    from crewai import Agent, Crew, Task
    from crewai.llms.base_llm import BaseLLM
    from crewai.tools import tool as crew_tool

    from sam_link_sdk.crewai import run_agent

    class Scripted(BaseLLM):
        def __init__(self):
            super().__init__(model="fake-model")
            self.calls = 0

        def call(self, messages, tools=None, callbacks=None, available_functions=None, **kwargs):
            self.calls += 1
            if self.calls == 1:
                return 'Thought: route it\nAction: route_claim\nAction Input: {"claim_id": "C-1"}'
            return "Thought: done\nFinal Answer: Routed."

        def supports_function_calling(self):
            return False

        def supports_stop_words(self):
            return False

        def get_context_window_size(self):
            return 8000

    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        trip()
        return "fast-track"

    router = Agent(
        role="router",
        goal="g",
        backstory="b",
        llm=Scripted(),
        verbose=False,
        max_iter=3,
        tools=[crew_tool("route_claim")(route_claim)],
    )
    crew = Crew(
        agents=[router], tasks=[Task(description="Route {request}", expected_output="x", agent=router)], verbose=False
    )
    return await run_agent(ctx, lambda _: crew)


async def run_hermes(ctx, trip, monkeypatch=None):
    """Hermes at its hook boundary (the stand-in agent of test_hermes_hooks): the stop blocks the next tool call and
    Hermes' turn goes on to its end; the run must still end in the stop.

    A Hermes hook cannot refuse a model call: the agent is interrupted.
    """
    from sam_link_sdk.hermes import run_agent

    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)

    def turn(h, agent):
        h.model_call("sam-T1", [("c1", "route_claim")])
        trip()
        h.call_tool("sam-T1", "route_claim", "c1")
        h.model_call("sam-T1")

    try:
        return await run_agent(ctx, lambda c: hermes.agent(["route_claim"], turn, session="sam-T1"))
    finally:
        for _ in range(100):
            if hermes.hard_interrupts:
                break
            await asyncio.sleep(0.01)
        assert hermes.hard_interrupts == ["sam-T1"]


async def run_otel(ctx, trip):
    """The generic adapter: `run` is the framework; it wraps the stop its own code met, as frameworks do."""
    pytest.importorskip("opentelemetry.sdk")
    from sam_link_sdk.otel_steps import run_agent

    def run(text: str) -> str:
        trip()
        try:
            ctx.check_running()
        except TaskHalted as halted:
            raise RuntimeError("the framework's own error") from halted
        return "Routed."

    return await run_agent(ctx, run)


async def run_langgraph(ctx, trip):
    """A graph whose model node falls back on any error, so the halt our guard raises never leaves the graph."""
    pytest.importorskip("langgraph")
    from langchain_core.messages import AIMessage
    from langgraph.graph import START, MessagesState, StateGraph

    from sam_link_sdk.langchain import run_graph

    from .test_callbacks import ToolCallingFake

    model = ToolCallingFake(messages=iter([AIMessage(content="Routed.")]))

    async def call_model(state: MessagesState):
        trip()
        try:
            return {"messages": [await model.ainvoke(state["messages"])]}
        except Exception:
            return {"messages": [AIMessage(content="done")]}

    graph = StateGraph(MessagesState)
    graph.add_node("model", call_model)
    graph.add_edge(START, "model")
    return await run_graph(ctx, graph.compile(), stream_text=False)


RUNS = {
    "langchain": run_langchain,
    "langgraph": run_langgraph,
    "openai_agents": run_openai,
    "adk": run_adk,
    "strands": run_strands,
    "agent_framework": run_agent_framework,
    "crewai": run_crewai,
    "hermes": run_hermes,
    "otel": run_otel,
}


@pytest.mark.parametrize("stop", list(STOPS))
@pytest.mark.parametrize("adapter", list(RUNS))
async def test_run_agent_ends_in_the_bare_halt_of_its_task_however_the_framework_passed_it_on(
    adapter, stop, monkeypatch
):
    run = RUNS[adapter]
    seen, _ = await run_in_task(stop, (lambda ctx, trip: run(ctx, trip, monkeypatch)) if adapter == "hermes" else run)
    assert "raised" in seen, f"{adapter}: the run returned {seen.get('returned')!r}"
    raised = seen["raised"]
    assert bare(raised), f"{adapter}: {type(raised).__module__}.{type(raised).__name__}: {raised}"
    assert raised.reason == stop


def test_the_halt_is_found_anywhere_in_a_chain_or_a_group():
    from sam_link_sdk.context import halt_of_task

    halted = TaskHalted("T1", "stopped")
    try:
        try:
            raise halted
        except TaskHalted as inner:
            raise RuntimeError("wrapped") from inner
    except RuntimeError as wrapped:
        group = ExceptionGroup("tools", [ValueError("other"), wrapped])
    assert halt_of_task(group, "T1") is halted
    assert halt_of_task(group, "T2") is None
    assert halt_of_task(ValueError("plain"), "T1") is None


@pytest.mark.parametrize("adapter", list(RUNS))
async def test_a_stop_that_lapses_inside_a_tool_is_answered_as_the_stop_not_as_a_crash(adapter, caplog, monkeypatch):
    """The reviewer's shape: SAM's stop lapses (disconnected, read live) while a tool runs, through a real MeshAgent."""
    run = RUNS[adapter]
    with caplog.at_level(logging.INFO):
        _, transport = await run_in_task(
            "disconnected", (lambda ctx, trip: run(ctx, trip, monkeypatch)) if adapter == "hermes" else run
        )
    answer = transport.finished["T1"]
    errors = [r.getMessage() for r in caplog.records if r.levelname == "ERROR"]
    assert answer.state == "failed" and "lost contact with SAM" in (answer.text or ""), json.dumps(
        {"state": answer.state, "text": answer.text, "errors": errors}, default=str
    )
    assert not [e for e in errors if "handler failed" in e]


async def test_adk_two_deliveries_of_a_run_based_task_at_once_keep_their_own_sessions():
    """A run-based task's ADK session is dropped when its run ends; a delivery of the same task still running must not
    lose its session to the other's end (the session was the task id alone)."""
    pytest.importorskip("google.adk")
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.genai import types

    from sam_link_sdk.adk import run_agent, step_reporter

    from .test_adk import Scripted, fc, usage

    release = asyncio.Event()

    async def slow_lookup(policy_id: str) -> str:
        """Look up a policy slowly."""
        await release.wait()
        return "gold"

    def model():
        return Scripted(
            turns=[
                ([fc("slow_lookup", {"policy_id": "P-1"}, call_id="c1")], usage(1, 1)),
                ([types.Part(text="Gold.")], usage(1, 1)),
            ]
        )

    runner = InMemoryRunner(
        agent=LlmAgent(name="claims", model=model(), instruction="x", tools=[slow_lookup]),
        app_name="claims",
        plugins=[step_reporter()],
    )
    quick_runner = InMemoryRunner(
        agent=LlmAgent(
            name="claims", model=Scripted(turns=[([types.Part(text="Quick.")], usage(1, 1))]), instruction="x"
        ),
        app_name="claims",
        plugins=[step_reporter()],
    )
    quick_runner.session_service = runner.session_service

    async with (
        adapter_task("T-peer", "P-1", context_id="", session_behavior=RUN) as first,
        adapter_task("T-peer", "P-1", context_id="", session_behavior=RUN) as again,
    ):
        assert first.ctx.run_based and again.ctx.run_based
        slow = asyncio.create_task(run_agent(first.ctx, runner))
        await asyncio.sleep(0.1)
        assert await run_agent(again.ctx, quick_runner) == "Quick."
        release.set()
        assert await asyncio.wait_for(slow, 10) == "Gold."


async def answer_langchain(ctx, trip):
    """halts_bare raises after a normal return only when one of our hooks or guards met the halt during the run (a
    framework swallowed it). A stop first read after the run's last hook (here: while the model writes its final answer)
    did not touch the run, and its completed answer stands; what a stopped agent may still send is the core's to decide.
    """
    pytest.importorskip("langchain_core")
    from langchain_core.messages import AIMessage

    from sam_link_sdk.langchain import run_agent

    from .test_callbacks import ToolCallingFake

    def final():
        trip()
        yield AIMessage(content="Routed.")

    return await run_agent(ctx, ToolCallingFake(messages=final()), [])


async def answer_openai(ctx, trip):
    pytest.importorskip("agents")
    from agents import Agent

    from sam_link_sdk.openai_agents import run_agent

    from .test_openai_agents import ScriptedModel, message, usage

    class Tripping(ScriptedModel):
        async def get_response(self, *args, **kwargs):
            trip()
            return await super().get_response(*args, **kwargs)

    return await run_agent(
        ctx, Agent(name="claims", instructions="x", model=Tripping([([message("Routed.")], usage(1, 1))]))
    )


async def answer_adk(ctx, trip):
    pytest.importorskip("google.adk")
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.genai import types

    from sam_link_sdk.adk import run_agent, step_reporter

    from .test_adk import Scripted, usage

    class Tripping(Scripted):
        async def generate_content_async(self, llm_request, stream=False):
            trip()
            async for response in super().generate_content_async(llm_request, stream):
                yield response

    agent = LlmAgent(
        name="claims", model=Tripping(turns=[([types.Part(text="Routed.")], usage(1, 1))]), instruction="x"
    )
    return await run_agent(ctx, InMemoryRunner(agent=agent, app_name="claims", plugins=[step_reporter()]))


async def answer_strands(ctx, trip):
    pytest.importorskip("strands")
    from strands import Agent

    from sam_link_sdk.strands import run_agent

    from .test_strands import ScriptedModel, text, usage

    class Tripping(ScriptedModel):
        async def stream(self, *args, **kwargs):
            trip()
            async for event in super().stream(*args, **kwargs):
                yield event

    model = Tripping([([text("Routed.")], usage(1, 1))])
    return await run_agent(ctx, lambda c: Agent(model=model, callback_handler=None))


async def answer_agent_framework(ctx, trip):
    pytest.importorskip("agent_framework")
    from sam_link_sdk.agent_framework import run_agent

    from .test_agent_framework import ScriptedClient, agent_of, text, usage

    return await run_agent(ctx, agent_of(ScriptedClient([([text("Routed.")], usage(1, 1))], on_response=trip)))


async def answer_crewai(ctx, trip):
    pytest.importorskip("crewai")
    from crewai import Agent, Crew, Task
    from crewai.llms.base_llm import BaseLLM

    from sam_link_sdk.crewai import run_agent

    class Tripping(BaseLLM):
        def __init__(self):
            super().__init__(model="fake-model")

        def call(self, messages, tools=None, callbacks=None, available_functions=None, **kwargs):
            trip()
            return "Thought: done\nFinal Answer: Routed."

        def supports_function_calling(self):
            return False

        def supports_stop_words(self):
            return False

        def get_context_window_size(self):
            return 8000

    router = Agent(role="router", goal="g", backstory="b", llm=Tripping(), verbose=False, max_iter=2)
    return await run_agent(
        ctx,
        lambda _: Crew(
            agents=[router],
            verbose=False,
            tasks=[Task(description="Route {request}", expected_output="x", agent=router)],
        ),
    )


async def answer_hermes(ctx, trip, monkeypatch=None):
    from sam_link_sdk.hermes import run_agent

    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)

    def turn(h, agent):
        h.model_call(agent.session_id)
        trip()

    return await run_agent(ctx, lambda c: hermes.agent([], turn))


async def answer_otel(ctx, trip):
    pytest.importorskip("opentelemetry.sdk")
    from sam_link_sdk.otel_steps import run_agent

    def run(text: str) -> str:
        trip()
        return "Routed."

    return await run_agent(ctx, run)


ANSWERS = {
    "langchain": answer_langchain,
    "openai_agents": answer_openai,
    "adk": answer_adk,
    "strands": answer_strands,
    "agent_framework": answer_agent_framework,
    "crewai": answer_crewai,
    "hermes": answer_hermes,
    "otel": answer_otel,
}


@pytest.mark.parametrize("stop", list(STOPS))
@pytest.mark.parametrize("adapter", list(ANSWERS))
async def test_a_stop_no_hook_met_leaves_a_completed_answer_alone(adapter, stop, monkeypatch):
    run = ANSWERS[adapter]
    seen, _ = await run_in_task(stop, (lambda ctx, trip: run(ctx, trip, monkeypatch)) if adapter == "hermes" else run)
    assert "raised" not in seen, f"{adapter}: {seen.get('raised')!r}"
    assert seen["returned"] in ("Routed.", "done")
    assert seen["ctx"]._run.halt_reason
