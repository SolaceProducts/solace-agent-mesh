# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A guard fails closed in every adapter: the guarded tool's body does not run, whatever the framework does with the hook
or wrapper the guard sits in.

The risk this pins is a framework release that keeps a hook's name but stops honouring it (Strands' ``cancel_tool``,
Agent Framework's middleware that skips ``call_next``, Hermes' ``{"action": "block"}``, a wrapper a framework calls
another way). Every cell runs the real installed framework, driven by the scripted model of its own tests, as the
handler of a real task (tests/adapter_task.py), and asserts on the tool body itself: a list the body appends to, not on
the steps the run reported.

Cells, per adapter:

- a, approval denied: the body did not run, the model got the refusal and the run carried on to its answer.
- b, the approval prompt fails (it raises) or times out (the transport's own timeout, nobody answers): the body did not
  run; after a timeout the model got the refusal and the run carried on.
- c, the user lacks the tool's scope (``with_scopes``), CrewAI only: the other adapters' cell is
  ``test_adapter_conformance[<adapter>-denied_tool]``, which asserts the body did not run. Its fake crew calls CrewAI's
  hooks and a plain function, so only this module runs a real crew through the scoped tool.
- d, the task was stopped while the model was answering, so its tool call arrives after the stop: the body did not
  run, under an approval the user would give and under a scope the user holds.

Hermes is driven at its hook boundary (``FakeHermes``, which calls the process-wide hooks the way Hermes does), so
these cells run in the unit job too; the real Hermes agent is run by tests/test_hermes.py where Hermes is installed.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
from types import SimpleNamespace
from typing import Any

import pytest

from sam_link_sdk.context import TaskContext

from .adapter_task import adapter_task
from .test_scope_guard import SCOPE

SCOPED = {"close_claim": [SCOPE]}
CLAIM = {"claim_id": "C-1"}
ADAPTERS = ["langchain", "openai_agents", "adk", "crewai", "strands", "agent_framework", "hermes"]
REFUSED = "did not approve"


class Kit:
    """One run: a scripted model that calls ``close_claim`` on its first turn (and ``close_claim`` again after a stop
    if `stop_on` names that turn), then answers "Done.". ``body`` is what the tool's body appended to; ``saw`` is what
    each model request carried, as text."""

    def __init__(self, ctx: TaskContext, *, approval: bool, scoped: bool, stop_on: int | None = None):
        self.ctx, self.approval, self.scoped, self.stop_on = ctx, approval, scoped, stop_on
        self.body: list[str] = []
        self.saw: list[str] = []

    def model_turn(self, request: Any) -> None:
        if self.stop_on == len(self.saw):
            self.ctx._run.halt()
        self.saw.append(str(request))

    def close_claim(self, claim_id: str) -> str:
        """Close a claim."""
        self.body.append(claim_id)
        return "closed"

    async def run(self) -> str:
        raise NotImplementedError


class LangChainKit(Kit):
    async def run(self):
        from langchain_core.messages import AIMessage
        from langchain_core.tools import tool

        from sam_link_sdk.langchain import run_agent, with_approval, with_scopes

        from .test_callbacks import ToolCallingFake

        kit = self
        replies = [
            AIMessage(
                content="",
                tool_calls=[{"id": "c1", "name": "close_claim", "args": CLAIM}],
                usage_metadata={"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
            ),
            AIMessage(content="Done.", usage_metadata={"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}),
        ]

        class Recording(ToolCallingFake):
            def _generate(self, messages, *args, **kwargs):
                kit.model_turn(messages)
                return super()._generate(messages, *args, **kwargs)

        tools = [tool(self.close_claim)]
        if self.scoped:
            tools = with_scopes(tools, SCOPED)
        if self.approval:
            tools = with_approval(self.ctx, tools)
        return await run_agent(self.ctx, Recording(messages=iter(replies)), tools)


class OpenAIAgentsKit(Kit):
    async def run(self):
        from agents import Agent, function_tool

        from sam_link_sdk.openai_agents import run_agent, with_approval, with_scopes

        from .test_openai_agents import ScriptedModel, call, message, usage

        kit = self

        class Recording(ScriptedModel):
            async def get_response(self, system_instructions, input, *args, **kwargs):
                kit.model_turn(input)
                return await super().get_response()

        tools = [function_tool(self.close_claim)]
        if self.scoped:
            tools = with_scopes(tools, SCOPED)
        if self.approval:
            tools = with_approval(self.ctx, tools)
        model = Recording([([call("c1", "close_claim", CLAIM)], usage(1, 1)), ([message("Done.")], usage(1, 1))])
        return await run_agent(self.ctx, Agent(name="claims", instructions="x", model=model, tools=tools))


class AdkKit(Kit):
    async def run(self):
        from google.adk.agents import LlmAgent
        from google.adk.runners import InMemoryRunner
        from google.genai import types

        from sam_link_sdk.adk import run_agent, step_reporter, with_approval, with_scopes

        from .test_adk import Scripted, fc, usage

        kit = self

        class Recording(Scripted):
            async def generate_content_async(self, llm_request, stream=False):
                kit.model_turn(llm_request.contents)
                async for response in super().generate_content_async(llm_request, stream):
                    yield response

        tools = [self.close_claim]
        if self.scoped:
            tools = with_scopes(tools, SCOPED)
        if self.approval:
            tools = with_approval(tools)
        model = Recording(
            model="fake",
            turns=[([fc("close_claim", CLAIM, call_id="c1")], usage(1, 1)), ([types.Part(text="Done.")], usage(1, 1))],
        )
        agent = LlmAgent(name="claims", model=model, instruction="x", tools=tools)
        return await run_agent(self.ctx, InMemoryRunner(agent=agent, app_name="claims", plugins=[step_reporter()]))


class CrewAIKit(Kit):
    async def run(self):
        from crewai import Agent, Crew, Task
        from crewai.tools import tool

        from sam_link_sdk.crewai import run_agent, with_approval, with_scopes

        from .test_kill_switch_frameworks import ScriptedLLM

        kit = self

        class Recording(ScriptedLLM):
            def call(self, messages, *args, **kwargs):
                kit.model_turn(messages)
                return super().call(messages, *args, **kwargs)

        tools = [tool("close_claim")(self.close_claim)]
        if self.scoped:
            tools = with_scopes(tools, SCOPED)
        if self.approval:
            tools = with_approval(self.ctx, tools)
        llm = Recording(
            [
                f"Thought: close it\nAction: close_claim\nAction Input: {json.dumps(CLAIM)}",
                "Thought: finished\nFinal Answer: Done.",
            ]
        )
        agent = Agent(role="r", goal="g", backstory="b", llm=llm, tools=tools, verbose=False, max_iter=4)
        crew = Crew(agents=[agent], tasks=[Task(description="Close C-1", expected_output="x", agent=agent)])
        return await run_agent(self.ctx, lambda _: crew)


class StrandsKit(Kit):
    async def run(self):
        from strands import Agent, tool

        from sam_link_sdk.strands import run_agent, with_approval, with_scopes

        from .test_strands import ScriptedModel, call, text, usage

        kit = self

        class Recording(ScriptedModel):
            async def stream(self, messages, *args, **kwargs):
                kit.model_turn(messages)
                async for event in super().stream(messages, *args, **kwargs):
                    yield event

        tools = [tool(self.close_claim)]
        if self.scoped:
            tools = with_scopes(tools, SCOPED)
        hooks = [with_approval(self.ctx, tools, names=["close_claim"])] if self.approval else []
        model = Recording([([call("c1", "close_claim", CLAIM)], usage(1, 1)), ([text("Done.")], usage(1, 1))])
        return await run_agent(self.ctx, lambda c: Agent(model=model, tools=tools, callback_handler=None, hooks=hooks))


class AgentFrameworkKit(Kit):
    async def run(self):
        from agent_framework import tool

        from sam_link_sdk.agent_framework import run_agent, with_approval, with_scopes

        from .test_agent_framework import ScriptedClient, agent_of, call, text, usage

        kit = self

        class Recording(ScriptedClient):
            def _inner_get_response(self, *, messages, **kwargs):
                kit.model_turn([c.to_dict() for m in messages for c in m.contents])
                return super()._inner_get_response(messages=messages, **kwargs)

        tools = [tool(approval_mode="never_require", name="close_claim")(self.close_claim)]
        if self.scoped:
            tools = with_scopes(tools, SCOPED)
        middleware = [with_approval(self.ctx, tools)] if self.approval else []
        client = Recording([([call("c1", "close_claim", CLAIM)], usage(1, 1)), ([text("Done.")], usage(1, 1))])
        return await run_agent(self.ctx, agent_of(client, tools), middleware=middleware)


class HermesKit(Kit):
    monkeypatch: Any = None

    async def run(self):
        from sam_link_sdk.hermes import run_agent, with_approval

        from .test_hermes_hooks import FakeHermes

        kit = self
        hermes = FakeHermes(self.monkeypatch)

        def turn(h, agent):
            """Hermes runs the tool unless a hook returned exactly {"action": "block", "message": ...}."""
            kit.model_turn("the task")
            fired = h.fire(
                "pre_tool_call", session_id=agent.session_id, tool_name="close_claim", args=CLAIM, tool_call_id="c1"
            )
            blocked = isinstance(fired, dict) and fired.get("action") == "block"
            if not blocked:
                kit.close_claim(CLAIM["claim_id"])
            kit.model_turn(json.dumps(fired))

        approval = with_approval(names=["close_claim"]) if self.approval else None
        return await run_agent(self.ctx, lambda c: hermes.agent(["close_claim"], turn), approval=approval)


KITS: dict[str, type[Kit]] = {
    "langchain": LangChainKit,
    "openai_agents": OpenAIAgentsKit,
    "adk": AdkKit,
    "crewai": CrewAIKit,
    "strands": StrandsKit,
    "agent_framework": AgentFrameworkKit,
    "hermes": HermesKit,
}


def users_answer(ctx: TaskContext, how: str) -> None:
    """Make the task's approval prompt come back as `how`: a user who denies or approves, a prompt that raises, or one
    nobody answers before the transport's own timeout."""

    if how == "times_out":
        asked = ctx.request_approval

        async def request_approval(name, args, *, message="", timeout_seconds=None):
            return await asked(name, args, message=message, timeout_seconds=0.05)

    else:

        async def request_approval(name, args, *, message="", timeout_seconds=None):
            if how == "raises":
                raise RuntimeError("the prompt channel failed")
            return SimpleNamespace(approved=how == "approves", outcome="approved" if how == "approves" else "denied")

    ctx.request_approval = request_approval


async def drive(
    adapter: str,
    monkeypatch,
    *,
    approval: bool = False,
    scoped: bool = False,
    granted: bool = True,
    user: str | None = None,
    stop_on: int | None = None,
) -> tuple[Kit, str | None, BaseException | None]:
    """Run the adapter's scripted run as a task handler; returns the kit, the answer and the exception the run ended
    with (a framework may end a refused or stopped run with one: what matters is the body)."""
    async with adapter_task(
        "T1",
        "Close claim C-1",
        scopes=SCOPED if scoped else None,
        granted=[SCOPE] if granted else [],
    ) as task:
        if user:
            users_answer(task.ctx, user)
        kit = KITS[adapter](task.ctx, approval=approval, scoped=scoped, stop_on=stop_on)
        kit.monkeypatch = monkeypatch
        outcome: tuple[str | None, BaseException | None] = (None, None)
        with contextlib.suppress(asyncio.CancelledError):
            try:
                outcome = (await asyncio.wait_for(task.run(lambda ctx: kit.run()), 30), None)
            except Exception as exc:
                outcome = (None, exc)
    return kit, *outcome


def answered(answer: str | None) -> bool:
    return (answer or "").rstrip(".").lower() == "done"


def imported(adapter: str) -> None:
    module = {
        "langchain": "langchain_core",
        "openai_agents": "agents",
        "adk": "google.adk",
        "hermes": "sam_link_sdk",
    }.get(adapter, adapter)
    pytest.importorskip(module)


@pytest.mark.parametrize("adapter", ADAPTERS)
async def test_a_denied_approval_runs_no_body_and_the_model_is_told(adapter, monkeypatch):
    imported(adapter)
    kit, answer, error = await drive(adapter, monkeypatch, approval=True, user="denies")
    assert kit.body == []
    assert error is None and answered(answer)
    assert any(REFUSED in seen for seen in kit.saw[1:])


@pytest.mark.parametrize("adapter", ADAPTERS)
async def test_an_approval_that_times_out_runs_no_body_and_the_model_is_told(adapter, monkeypatch):
    imported(adapter)
    kit, answer, error = await drive(adapter, monkeypatch, approval=True, user="times_out")
    assert kit.body == []
    assert error is None and answered(answer)
    assert any("timed_out" in seen for seen in kit.saw[1:])


@pytest.mark.parametrize("adapter", ADAPTERS)
async def test_an_approval_prompt_that_fails_runs_no_body(adapter, monkeypatch):
    imported(adapter)
    kit, _, _ = await drive(adapter, monkeypatch, approval=True, user="raises")
    assert kit.body == []


async def test_crewai_a_tool_the_user_lacks_the_scope_for_runs_no_body_and_the_model_is_told(monkeypatch):
    imported("crewai")
    kit, answer, error = await drive("crewai", monkeypatch, scoped=True, granted=False)
    assert kit.body == []
    assert error is None and answered(answer)
    assert any("Access denied" in seen for seen in kit.saw[1:])


GUARDS = {
    "approved": {"approval": True, "user": "approves"},
    "scoped": {"scoped": True},
    "approved_and_scoped": {"approval": True, "scoped": True, "user": "approves"},
}


@pytest.mark.parametrize("guard", list(GUARDS))
@pytest.mark.parametrize("adapter", ADAPTERS)
async def test_a_tool_call_that_arrives_after_the_task_was_stopped_runs_no_body(adapter, guard, monkeypatch):
    imported(adapter)
    kit, _, _ = await drive(adapter, monkeypatch, stop_on=0, **GUARDS[guard])
    assert kit.body == []
    assert kit.ctx._run.halt_reason == "stopped"
