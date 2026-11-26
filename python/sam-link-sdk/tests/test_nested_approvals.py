# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A tool a nested agent runs needs the user's approval: what each framework's adapter does, pinned.

OpenAI Agents (a handoff and an agent run as a tool), ADK (a sub-agent and an ``AgentTool``) and Hermes (a ``delegate_task`` subagent) ask the
user through SAM exactly as the outer agent's tools do, and a refusal does not run the tool. CrewAI asks only where the
nested crew has the tools ``with_approval`` returned: a crew given the plain tool runs it unasked (the wrapper is what
asks, docs/frameworks/crewai.md), so that is pinned too. Agent Framework's ``as_tool`` is not covered here.

The prompt is answered by ``ctx.request_approval``, as the other adapters' approval tests do."""

from __future__ import annotations

import json
from types import SimpleNamespace

import pytest

from sam_link_sdk.events import ToolFinished

from .adapter_task import adapter_task
from .test_adapter_conformance import NESTED_ANSWER, Tools

ARGS = {"claim_id": "C-1"}
ANSWERS = pytest.mark.parametrize("approve", [True, False], ids=["approved", "denied"])


class Nested:
    """What a nested run did: the approvals asked, whether the tool ran, and what the task sent for it."""

    def __init__(self, approve: bool):
        self.approve, self.asked, self.tools = approve, [], Tools()

    async def request_approval(self, name, args, *, message="", timeout_seconds=None):
        self.asked.append((name, dict(args)))
        return SimpleNamespace(approved=self.approve, outcome="approved" if self.approve else "denied")

    @property
    def route(self):
        return self.tools.by_name["route_claim"]

    async def run(self, drive):
        async with adapter_task() as task:
            task.ctx.request_approval = self.request_approval
            answer = await task.run(lambda ctx: drive(ctx, self))
            self.answer = answer
            self.finished = [e for e in task.events if isinstance(e, ToolFinished) and e.name == "route_claim"]
        return self

    @property
    def ran(self) -> list[str]:
        return [name for name in self.tools.ran if name == "route_claim"]

    def refused_as_not_approved(self) -> bool:
        return all(
            e.failed and "did not approve this call to route_claim" in json.dumps(e.result) for e in self.finished
        ) and bool(self.finished)


def assert_asked_and_obeyed(nested: Nested) -> None:
    assert nested.asked == [("route_claim", ARGS)]
    if nested.approve:
        assert nested.ran == ["route_claim"] and not nested.refused_as_not_approved()
    else:
        assert nested.ran == [] and nested.refused_as_not_approved()


@ANSWERS
@pytest.mark.parametrize("held", ["needs_approval", "with_approval"])
@pytest.mark.parametrize("nesting", ["handoff", "agent_tool"])
async def test_openai_agents_asks_for_a_tool_of_an_agent_it_hands_off_to_or_runs_as_a_tool(approve, held, nesting):
    """An agent run as a tool (Agent.as_tool) holding route_claim with the SDK's own flag ends its nested run with an
    interruption the calling run takes over: run_agent asks, and the SDK resumes the nested run with the answer.

    The SDK itself rejects a call it holds for approval, so only the wrapper's refusal is a step of the task.
    """
    pytest.importorskip("agents")
    from agents import Agent, function_tool, handoff

    from sam_link_sdk.openai_agents import run_agent, with_approval

    from .test_openai_agents import ScriptedModel, call as fn_call, message, usage

    async def drive(ctx, nested):
        first = (
            fn_call("call_o", "transfer_to_router", {})
            if nesting == "handoff"
            else fn_call("call_o", "ask_router", {"input": "route C-1"})
        )
        model = ScriptedModel(
            [
                ([first], usage(1, 1)),
                ([fn_call("call_r", "route_claim", ARGS)], usage(1, 1)),
                ([message("Routing was refused.")], usage(1, 1)),
                *([([message("Routing was refused.")], usage(1, 1))] if nesting == "agent_tool" else []),
            ]
        )
        scripted = model.get_response
        nested.told = ""

        async def get_response(*args, **kwargs):
            nested.told += json.dumps(kwargs.get("input") or args[1], default=str)
            return await scripted(*args, **kwargs)

        model.get_response = get_response
        tools = (
            [function_tool(nested.route, needs_approval=True)]
            if held == "needs_approval"
            else with_approval(ctx, [function_tool(nested.route)], names=["route_claim"])
        )
        router = Agent(name="router", instructions="x", model=model, tools=tools)
        if nesting == "agent_tool":
            front = Agent(name="front", instructions="x", model=model, tools=[router.as_tool("ask_router", "Routes.")])
        else:
            front = Agent(name="front", instructions="x", model=model, handoffs=[handoff(router)])
        return await run_agent(ctx, front)

    nested = await Nested(approve).run(drive)
    assert nested.asked == [("route_claim", ARGS)]
    assert nested.ran == (["route_claim"] if approve else [])
    assert ("did not approve this call to route_claim" in nested.told) is not approve
    if held == "with_approval" and not approve:
        assert nested.refused_as_not_approved()


@ANSWERS
@pytest.mark.parametrize("nesting", ["sub_agent", "agent_tool"])
async def test_adk_asks_for_a_tool_of_a_sub_agent_and_of_an_agent_tool(approve, nesting):
    pytest.importorskip("google.adk")
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.adk.tools.agent_tool import AgentTool
    from google.genai import types

    from sam_link_sdk.adk import run_agent, step_reporter, with_approval

    from .test_adk import Scripted, fc, usage

    async def drive(ctx, nested):
        if nesting == "sub_agent":
            turns = [
                ([fc("transfer_to_agent", {"agent_name": "router"}, call_id="call_o")], usage(1, 1)),
                ([fc("route_claim", ARGS, call_id="call_r")], usage(1, 1)),
                ([types.Part(text=NESTED_ANSWER)], usage(1, 1)),
            ]
        else:
            turns = [
                ([fc("router", {"request": "route C-1"}, call_id="call_o")], usage(1, 1)),
                ([fc("route_claim", ARGS, call_id="call_r")], usage(1, 1)),
                ([types.Part(text="I may not route.")], usage(1, 1)),
                ([types.Part(text=NESTED_ANSWER)], usage(1, 1)),
            ]
        model = Scripted(model="fake-gemini", turns=turns)
        router = LlmAgent(
            name="router",
            model=model,
            instruction="Route claims.",
            description="Routes claims.",
            tools=with_approval([nested.route], names=["route_claim"]),
        )
        front = (
            LlmAgent(name="front", model=model, instruction="Ask the router.", sub_agents=[router])
            if nesting == "sub_agent"
            else LlmAgent(name="front", model=model, instruction="Ask the router.", tools=[AgentTool(agent=router)])
        )
        return await run_agent(ctx, InMemoryRunner(agent=front, app_name="claims", plugins=[step_reporter()]))

    assert_asked_and_obeyed(await Nested(approve).run(drive))


async def hierarchical_crew(ctx, nested, with_the_wrapper: bool):
    from crewai import Agent, Crew, Process, Task
    from crewai.events import crewai_event_bus
    from crewai.events.types.llm_events import LLMCallType
    from crewai.llms.base_llm import BaseLLM
    from crewai.tools import tool as crew_tool

    from sam_link_sdk.crewai import run_agent, with_approval

    from .test_crewai import llm_end, llm_start

    class Scripted(BaseLLM):
        def __init__(self, replies):
            super().__init__(model="fake-model")
            self.replies = list(replies)

        def call(self, messages, tools=None, callbacks=None, available_functions=None, **kwargs):
            reply = self.replies.pop(0)
            crewai_event_bus.emit(self, llm_start(f"L{id(self)}{len(self.replies)}", model="fake-model"))
            crewai_event_bus.emit(
                self,
                llm_end(
                    f"L{id(self)}{len(self.replies)}",
                    reply,
                    LLMCallType.LLM_CALL,
                    {"input_tokens": 1, "output_tokens": 1},
                    model="fake-model",
                ),
            )
            return reply

        def supports_function_calling(self):
            return False

        def supports_stop_words(self):
            return False

        def get_context_window_size(self):
            return 8000

    manager_llm = Scripted(
        [
            "Thought: ask the router\nAction: Delegate work to coworker\n"
            'Action Input: {"task": "route C-1", "context": "claim C-1", "coworker": "router"}',
            f"Thought: done\nFinal Answer: {NESTED_ANSWER}",
        ]
    )
    worker_llm = Scripted(
        [
            'Thought: route it\nAction: route_claim\nAction Input: {"claim_id": "C-1"}',
            "Thought: done\nFinal Answer: Routed.",
        ]
    )
    tool = crew_tool("route_claim")(nested.route)
    tools = with_approval(ctx, [tool], names=["route_claim"]) if with_the_wrapper else [tool]
    router = Agent(role="router", goal="g", backstory="b", llm=worker_llm, verbose=False, max_iter=3, tools=tools)
    manager = Agent(
        role="manager", goal="g", backstory="b", llm=manager_llm, allow_delegation=True, verbose=False, max_iter=3
    )
    crew = Crew(
        agents=[router],
        tasks=[Task(description="Route {request}", expected_output="x")],
        process=Process.hierarchical,
        manager_agent=manager,
        verbose=False,
    )
    return await run_agent(ctx, lambda _: crew)


@ANSWERS
async def test_crewai_asks_for_a_tool_of_a_coworker_given_the_with_approval_tool(approve, monkeypatch):
    pytest.importorskip("crewai")
    monkeypatch.setenv("CREWAI_DISABLE_TELEMETRY", "true")
    nested = await Nested(approve).run(lambda ctx, n: hierarchical_crew(ctx, n, True))
    assert nested.asked == [("route_claim", ARGS)]
    assert nested.ran == (["route_claim"] if approve else [])


async def test_crewai_runs_a_coworkers_plain_tool_unasked(monkeypatch):
    pytest.importorskip("crewai")
    monkeypatch.setenv("CREWAI_DISABLE_TELEMETRY", "true")
    nested = await Nested(False).run(lambda ctx, n: hierarchical_crew(ctx, n, False))
    assert nested.asked == [] and nested.ran == ["route_claim"]


@ANSWERS
async def test_hermes_asks_for_a_tool_of_a_subagent_delegate_task_starts(approve, monkeypatch):
    from sam_link_sdk.hermes import run_agent, with_approval

    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)

    async def drive(ctx, nested):
        def turn(h, agent):
            h.model_call("sam-T", [("call_o", "delegate_task")])
            h.fire("subagent_start", parent_session_id="sam-T", child_session_id="child-1")
            h.model_call("child-1", [("call_r", "route_claim")])
            h.call_tool("child-1", "route_claim", "call_r", ARGS)
            h.model_call("child-1", ())
            h.fire("subagent_stop", parent_session_id="sam-T", child_session_id="child-1")
            h.model_call("sam-T", ())

        await run_agent(
            ctx,
            lambda c: hermes.agent(["route_claim", "delegate_task"], turn),
            approval=with_approval(names=["route_claim"]),
        )
        return NESTED_ANSWER

    nested = await Nested(approve).run(drive)
    assert nested.asked == [("route_claim", ARGS)]
    assert hermes.ran == ([("child-1", "route_claim")] if approve else [])
    if not approve:
        assert all(e.failed and "did not approve" in json.dumps(e.result) for e in nested.finished) and nested.finished
