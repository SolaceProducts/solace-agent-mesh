# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Google ADK adapter, driven offline by a scripted BaseLlm."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncGenerator
from types import SimpleNamespace

import pytest

pytest.importorskip("google.adk")

from google.adk.agents import LlmAgent
from google.adk.models.base_llm import BaseLlm
from google.adk.models.llm_request import LlmRequest
from google.adk.models.llm_response import LlmResponse
from google.adk.runners import InMemoryRunner
from google.genai import types

from sam_link_sdk.adk import SamLinkPlugin, run_agent
from sam_link_sdk.context import TaskContext
from sam_link_sdk.events import ModelStarted, ToolFinished, ToolStarted

from .adapter_task import adapter_task, steps


def lookup_policy(policy_id: str) -> dict:
    """Look up a policy."""
    return {"policy_id": policy_id, "tier": "gold"}


def broken(policy_id: str) -> dict:
    """Always fails."""
    raise RuntimeError("backend down")


def usage(i, o, cached=0, thoughts=0):
    return types.GenerateContentResponseUsageMetadata(
        prompt_token_count=i,
        candidates_token_count=o,
        total_token_count=i + o,
        cached_content_token_count=cached,
        thoughts_token_count=thoughts,
    )


def fc(name, args, call_id=None):
    return types.Part(function_call=types.FunctionCall(id=call_id, name=name, args=args))


class Scripted(BaseLlm):
    model: str = "fake-gemini"
    turns: list = []

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        parts, u = self.turns.pop(0)
        await asyncio.sleep(0)
        yield LlmResponse(content=types.Content(role="model", parts=parts), usage_metadata=u)


def runner_for(model, tools):
    agent = LlmAgent(name="claims_triage", model=model, instruction="Triage claims.", tools=tools)
    return InMemoryRunner(agent=agent, app_name="claims", plugins=[SamLinkPlugin()])


async def test_adk_ids_pair_even_when_model_gives_none():
    model = Scripted(
        turns=[
            (
                [
                    types.Part(text="Looking both up."),
                    fc("lookup_policy", {"policy_id": "P-1"}),
                    fc("lookup_policy", {"policy_id": "P-2"}),
                ],
                usage(800, 40, cached=100, thoughts=7),
            ),
            ([types.Part(text="Both are gold.")], usage(900, 20)),
        ]
    )
    async with adapter_task() as task:
        assert await run_agent(task.ctx, runner_for(model, [lookup_policy])) == "Both are gold."
        sent = steps(task)
        tu = task.ctx._run.usage.final_token_usage()

    kinds = [e.kind for e in sent]
    assert kinds[:2] == ["model_started", "model_finished"]
    assert sorted(kinds[2:6]) == ["tool_finished", "tool_finished", "tool_started", "tool_started"]
    assert kinds[6:] == ["model_started", "model_finished"]
    assert sent[0] == ModelStarted("fake-gemini")

    assert sent[1].text == "Looking both up."
    asked = [c.id for c in sent[1].tool_calls]
    assert len(asked) == 2 and all(i.startswith("adk-") for i in asked)
    u = sent[1].usage
    assert sent[1].model == "fake-gemini"
    assert (u.input_tokens, u.output_tokens, u.cached_input_tokens, u.cache_write_tokens, u.reasoning_tokens) == (
        800,
        40,
        100,
        0,
        7,
    )

    starts = {e.call_id: e for e in sent if isinstance(e, ToolStarted)}
    results = {e.call_id: e for e in sent if isinstance(e, ToolFinished)}
    assert set(starts) == set(results) == set(asked)
    assert {e.args["policy_id"] for e in starts.values()} == {"P-1", "P-2"}
    assert {r.result["tier"] for r in results.values()} == {"gold"}

    assert (tu["total_input_tokens"], tu["total_output_tokens"], tu["total_cached_input_tokens"]) == (1700, 60, 100)
    assert tu["last_input_tokens"] == 900 and list(tu["by_model"]) == ["fake-gemini"]


async def test_adk_model_supplied_ids_are_kept_and_tool_errors_reported():
    model = Scripted(
        turns=[
            ([fc("broken", {"policy_id": "P-9"}, call_id="call_x")], usage(10, 2)),
            ([types.Part(text="Sorry, the backend is down.")], usage(12, 3)),
        ]
    )
    agent = LlmAgent(name="claims_triage", model=model, instruction="x", tools=[broken])
    runner = InMemoryRunner(agent=agent, app_name="claims", plugins=[SamLinkPlugin()])
    async with adapter_task() as task:
        with pytest.raises(Exception):  # noqa: B017
            await run_agent(task.ctx, runner)
        sent = steps(task)
    assert sent[1].tool_calls[0].id == "call_x"
    result = [e for e in sent if isinstance(e, ToolFinished)][0]
    assert result == ToolFinished("call_x", "broken", {"error": "backend down"}, failed=True)


async def test_one_runner_routes_concurrent_tasks_to_their_own_context():
    def model_for(text):
        return [([types.Part(text=text)], usage(5, 1))]

    model = Scripted(turns=model_for("one") + model_for("two"))
    runner = runner_for(model, [])
    async with adapter_task("T1", context_id="S1") as t1, adapter_task("T2", context_id="S2") as t2:
        a1, a2 = await asyncio.gather(run_agent(t1.ctx, runner), run_agent(t2.ctx, runner))
        assert {a1, a2} == {"one", "two"}
        assert [e.kind for e in steps(t1)] == ["model_started", "model_finished"]
        assert [e.kind for e in steps(t2)] == ["model_started", "model_finished"]
        assert t1.ctx._run.usage.llm_calls == 1 and t2.ctx._run.usage.llm_calls == 1


async def test_a_run_based_task_gets_a_throwaway_session_and_leaves_the_chat_alone():
    model = Scripted(turns=[([types.Part(text="Gold.")], usage(5, 1)), ([types.Part(text="Checked.")], usage(5, 1))])
    runner = runner_for(model, [])
    async with adapter_task("T1", context_id="C") as chat:
        await run_agent(chat.ctx, runner)
    sessions = runner.session_service
    before = len((await sessions.get_session(app_name="claims", user_id="sam-link-sdk", session_id="C")).events)

    async with adapter_task("T2", context_id="C", session_behavior="run") as peer_call:
        assert await run_agent(peer_call.ctx, runner) == "Checked."

    assert len((await sessions.get_session(app_name="claims", user_id="sam-link-sdk", session_id="C")).events) == before
    assert await sessions.get_session(app_name="claims", user_id="sam-link-sdk", session_id="T2") is None


async def test_a_call_refused_for_missing_scopes_is_reported_once_as_the_refusal_and_never_runs():
    """ADK runs the after-tool callbacks even for a call a before-tool callback answered, so the plugin must not report
    the refused call a second time: it is the refusal every adapter reports, its start and a failed result."""
    ran = []

    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    records = []
    model = Scripted(
        turns=[
            ([fc("route_claim", {"claim_id": "C-1"}, call_id="call_r")], usage(5, 1)),
            ([types.Part(text="I may not route claims.")], usage(6, 1)),
        ]
    )
    async with adapter_task(
        text="Route C-1",
        user_id="alice",
        scopes={"route_claim": ["tool:claims:route"]},
        granted=[],
        audit=records.append,
    ) as task:
        assert await run_agent(task.ctx, runner_for(model, [route_claim])) == "I may not route claims."
        sent = steps(task)
    assert ran == []
    assert [e.kind for e in sent] == [
        "model_started",
        "model_finished",
        "tool_started",
        "tool_finished",
        "model_started",
        "model_finished",
    ]
    assert [e.result for e in sent if isinstance(e, ToolFinished)] == [
        {"error": "Access denied: you do not have the scopes required to use route_claim."}
    ]
    assert [r.message for r in records] == ["tool access denied", "tool execution failed"]


async def test_run_agent_refuses_a_runner_without_the_step_reporter():
    """Without the plugin the mesh would see no steps and nothing would refuse a tool the user lacks the scopes for."""
    ran = []

    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    model = Scripted(turns=[([fc("route_claim", {"claim_id": "C-1"})], usage(5, 1))])
    agent = LlmAgent(name="claims_triage", model=model, instruction="Triage claims.", tools=[route_claim])
    async with adapter_task() as task:
        with pytest.raises(ValueError, match="plugins=\\[step_reporter\\(\\)\\]"):
            await run_agent(task.ctx, InMemoryRunner(agent=agent, app_name="claims"))
        assert ran == [] and steps(task) == [] and model.turns


async def test_an_unanswered_question_is_text_for_the_model_as_in_every_adapter():
    from sam_link_sdk.adk import ask_user_tool
    from sam_link_sdk.adk.steps import _current
    from sam_link_sdk.prompting import UserAnswer

    async def ask_user(question, **kwargs):
        return UserAnswer(status="timed_out", answers=())

    async with adapter_task() as task:
        ctx = task.ctx
        ctx.ask_user = ask_user
        [tool] = await ask_user_tool().get_tools()
        token = _current.set(ctx)
        try:
            assert await tool.run_async(args={"question": "Which queue?"}, tool_context=SimpleNamespace()) == (
                "The user did not answer (timed_out)."
            )
        finally:
            _current.reset(token)


@pytest.mark.parametrize("shape", ["named tool", "toolset", "deeper"])
async def test_run_agent_refuses_an_agent_tool_that_runs_without_the_runner_s_plugins(shape):
    """AgentTool(include_plugins=False) runs its agent without the step reporter: no steps, no tokens and no checks
    there, whatever the agent's tools are (an MCP toolset, the adapter's own with_approval toolset: tools unknown
    before the run). Such a run is refused up front; with the plugins (the default) it runs, and is covered."""
    from google.adk.tools.agent_tool import AgentTool

    from sam_link_sdk._adapter import NestedRunRefused
    from sam_link_sdk.adk import with_approval

    ran = []

    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    model = Scripted(turns=[([types.Part(text="never asked")], usage(1, 1))])
    router_tools = {"named tool": [route_claim], "toolset": with_approval([route_claim]), "deeper": []}[shape]
    router = LlmAgent(name="router", model=model, instruction="Route claims.", tools=router_tools)
    if shape == "deeper":
        inner = LlmAgent(name="inner", model=model, instruction="x", tools=[route_claim])
        router.tools = [AgentTool(agent=inner, include_plugins=False)]

    def runner(include_plugins):
        front = LlmAgent(
            name="front",
            model=model,
            instruction="x",
            tools=[AgentTool(agent=router, include_plugins=include_plugins or shape == "deeper")],
        )
        return InMemoryRunner(agent=front, app_name="claims", plugins=[SamLinkPlugin()])

    ctx = TaskContext._new(
        task_id="T",
        context_id="C",
        text="Route C-1",
        agent_name="claims-triage",
        tool_scopes={"route_claim": ["tool:claims:route"]},
        granted_tool_scopes=[],
    )
    with pytest.raises(NestedRunRefused, match="include_plugins"):
        await run_agent(ctx, runner(False))
    assert model.turns and ran == []
    if shape != "deeper":
        assert await run_agent(ctx, runner(True)) == "never asked"


async def test_a_tool_adk_holds_for_confirmation_is_refused_on_a_channel_that_cannot_prompt():
    """Agent Mesh shows no prompt there (the e2e harness has only the web gateway, which can), so the user's answer is
    a cancel: the call does not run and the model is told it was not approved."""
    from google.adk.tools import FunctionTool

    ran: list[str] = []

    def route_claim(policy_id: str) -> dict:
        """Route a claim."""
        ran.append(policy_id)
        return {"ticket": "CLM-1"}

    model = Scripted(
        turns=[
            ([fc("route_claim", {"policy_id": "P-1"}, call_id="c1")], usage(10, 2)),
            ([types.Part(text="Not routed.")], usage(12, 3)),
        ]
    )
    runner = runner_for(model, [FunctionTool(route_claim, require_confirmation=True)])
    async with adapter_task(can_prompt_user=False) as task:
        assert await run_agent(task.ctx, runner) == "Not routed."
        [finished] = [e for e in steps(task) if isinstance(e, ToolFinished)]

    assert ran == []
    assert finished.failed
    assert "did not approve this call to route_claim (cancelled), so it was not run." in str(finished.result)


def plugin_of(runner) -> SamLinkPlugin:
    return next(p for p in runner.plugin_manager.plugins if isinstance(p, SamLinkPlugin))


async def test_a_run_that_fails_leaves_no_task_held_by_the_plugin():
    """ADK skips after_run_callback when a run raises, and the plugin lives as long as the process, so a failed run
    must not leave its task's context behind."""
    loop = [([fc("lookup_policy", {"policy_id": f"P-{i}"}, f"c{i}")], usage(5, 1)) for i in range(3)]
    runner = runner_for(Scripted(turns=loop), [lookup_policy])
    async with adapter_task("T1", context_id="S1") as t:
        with pytest.raises(Exception, match=r"(?i)limit"):
            await run_agent(t.ctx, runner, max_model_calls=2)
        assert [r for r in plugin_of(runner)._runs.values() if r.ctx is t.ctx] == []


async def test_a_run_cancelled_from_outside_leaves_no_task_held_by_the_plugin():
    """A cancel from outside the run skips both of ADK's run-end callbacks."""
    started = asyncio.Event()

    class Hanging(BaseLlm):
        model: str = "fake-gemini"

        async def generate_content_async(self, llm_request, stream=False):
            started.set()
            await asyncio.sleep(60)
            yield LlmResponse(content=types.Content(role="model", parts=[types.Part(text="late")]))

    runner = runner_for(Hanging(), [])
    async with adapter_task("T1", context_id="S1") as t:
        run = asyncio.ensure_future(run_agent(t.ctx, runner))
        await asyncio.wait_for(started.wait(), 5)
        run.cancel()
        with pytest.raises(asyncio.CancelledError):
            await run
        assert [r for r in plugin_of(runner)._runs.values() if r.ctx is t.ctx] == []
