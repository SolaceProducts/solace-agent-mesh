# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""OpenAI Agents SDK adapter, driven offline by a scripted fake Model."""

from __future__ import annotations

import asyncio
import json
import re

import pytest

from .adapter_task import adapter_task, steps

pytest.importorskip("agents")

from agents import Agent, ModelResponse, Usage, function_tool
from agents.models.interface import Model
from agents.usage import InputTokensDetails, OutputTokensDetails
from openai.types.responses import (
    Response,
    ResponseCompletedEvent,
    ResponseFunctionToolCall,
    ResponseOutputMessage,
    ResponseOutputText,
    ResponseTextDeltaEvent,
    ResponseUsage,
)
from openai.types.responses.response_usage import (
    InputTokensDetails as ResponseInputDetails,
    OutputTokensDetails as ResponseOutputDetails,
)

from sam_link_sdk.events import ModelStarted, Progress, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.openai_agents import run_agent


@function_tool
def lookup_policy(policy_id: str) -> dict:
    """Look up a policy."""
    return {"policy_id": policy_id, "tier": "gold"}


@function_tool
def broken(policy_id: str) -> dict:
    """Always fails."""
    raise RuntimeError("backend down")


def call(call_id, name, args):
    return ResponseFunctionToolCall(
        type="function_call",
        call_id=call_id,
        name=name,
        arguments=json.dumps(args),
        id=f"fc_{call_id}",
        status="completed",
    )


def message(text):
    return ResponseOutputMessage(
        type="message",
        id="msg",
        role="assistant",
        status="completed",
        content=[ResponseOutputText(type="output_text", text=text, annotations=[])],
    )


def usage(i, o, cached=0, reasoning=0):
    return Usage(
        requests=1,
        input_tokens=i,
        output_tokens=o,
        total_tokens=i + o,
        input_tokens_details=InputTokensDetails(cached_tokens=cached, cache_write_tokens=0),
        output_tokens_details=OutputTokensDetails(reasoning_tokens=reasoning),
    )


class ScriptedModel(Model):
    def __init__(self, turns):
        self.turns = list(turns)
        self.model = "fake-gpt"

    async def get_response(self, *args, **kwargs):
        output, u = self.turns.pop(0)
        return ModelResponse(output=output, usage=u, response_id=None)

    async def stream_response(self, *args, **kwargs):
        """The scripted response as the SDK's models stream it: each message's text in pieces, then the completed
        response carrying the output and the usage. It is get_response's, so a subclass that overrides that streams too."""
        response = await self.get_response(*args, **kwargs)
        sequence = 0
        for item in response.output:
            for part in getattr(item, "content", None) or []:
                for piece in [p for p in re.split(r"(\s)", part.text) if p]:
                    sequence += 1
                    yield ResponseTextDeltaEvent(
                        type="response.output_text.delta",
                        delta=piece,
                        item_id=item.id,
                        content_index=0,
                        output_index=0,
                        logprobs=[],
                        sequence_number=sequence,
                    )
        u = response.usage
        yield ResponseCompletedEvent(
            type="response.completed",
            sequence_number=sequence + 1,
            response=Response(
                id="resp",
                created_at=0,
                model=self.model,
                object="response",
                output=list(response.output),
                parallel_tool_calls=False,
                tool_choice="auto",
                tools=[],
                usage=ResponseUsage(
                    input_tokens=u.input_tokens,
                    output_tokens=u.output_tokens,
                    total_tokens=u.total_tokens,
                    input_tokens_details=ResponseInputDetails(
                        cached_tokens=u.input_tokens_details.cached_tokens,
                        cache_write_tokens=u.input_tokens_details.cache_write_tokens,
                    ),
                    output_tokens_details=ResponseOutputDetails(
                        reasoning_tokens=u.output_tokens_details.reasoning_tokens
                    ),
                ),
            ),
        )


async def test_parallel_tool_calls_pair_by_call_id_and_usage_sums():
    model = ScriptedModel(
        [
            (
                [
                    message("Looking both up."),
                    call("call_a", "lookup_policy", {"policy_id": "P-1"}),
                    call("call_b", "lookup_policy", {"policy_id": "P-2"}),
                ],
                usage(800, 40, cached=100, reasoning=5),
            ),
            ([message("Both are gold.")], usage(900, 20)),
        ]
    )
    agent = Agent(name="claims-triage", instructions="x", model=model, tools=[lookup_policy])
    async with adapter_task() as task:
        assert await run_agent(task.ctx, agent) == "Both are gold."
        sent = steps(task)
        tu = task.ctx._run.usage.final_token_usage()

    kinds = [e.kind for e in sent]
    assert kinds[:2] == ["model_started", "model_finished"]
    assert sorted(kinds[2:6]) == ["tool_finished", "tool_finished", "tool_started", "tool_started"]
    assert kinds[6:] == ["model_started", "model_finished"]

    inv, resp = sent[0], sent[1]
    assert inv == ModelStarted("fake-gpt")
    assert (resp.model, resp.text) == ("fake-gpt", "Looking both up.")
    assert resp.tool_calls == (
        ToolCall("call_a", "lookup_policy", {"policy_id": "P-1"}),
        ToolCall("call_b", "lookup_policy", {"policy_id": "P-2"}),
    )
    u = resp.usage
    assert (u.input_tokens, u.output_tokens, u.cached_input_tokens, u.cache_write_tokens, u.reasoning_tokens) == (
        800,
        40,
        100,
        0,
        5,
    )

    starts = {e.call_id: e for e in sent if isinstance(e, ToolStarted)}
    results = {e.call_id: e for e in sent if isinstance(e, ToolFinished)}
    assert set(starts) == set(results) == {"call_a", "call_b"}
    assert starts["call_a"].args == {"policy_id": "P-1"}
    assert results["call_b"].result == {"policy_id": "P-2", "tier": "gold"}

    assert (tu["total_input_tokens"], tu["total_output_tokens"], tu["total_cached_input_tokens"]) == (1700, 60, 100)
    assert tu["total_reasoning_tokens"] == 5 and tu["last_input_tokens"] == 900
    assert list(tu["by_model"]) == ["fake-gpt"]


async def test_tool_failure_is_reported_and_run_continues():
    """The Agents SDK replaces the exception with its own safe message before any hook sees it; the step reports exactly
    what the model was given.
    """
    model = ScriptedModel(
        [
            ([call("call_x", "broken", {"policy_id": "P-9"})], usage(10, 2)),
            ([message("Sorry, the backend is down.")], usage(12, 3)),
        ]
    )
    agent = Agent(name="claims-triage", instructions="x", model=model, tools=[broken])
    async with adapter_task() as task:
        assert await run_agent(task.ctx, agent) == "Sorry, the backend is down."
        result = [e for e in steps(task) if isinstance(e, ToolFinished)][0]
    assert result.call_id == "call_x"
    assert "error occurred" in json.dumps(result.result).lower()


async def test_a_task_without_streaming_still_counts_usage():
    model = ScriptedModel([([message("hi")], usage(5, 1))])
    async with adapter_task(streaming=False) as task:
        assert await run_agent(task.ctx, Agent(name="a", instructions="x", model=model)) == "hi"
        assert steps(task) == []
        assert task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 5


def _custom_failure_text(ctx, error):
    return f"lookup unavailable ({type(error).__name__})"


@function_tool(failure_error_function=_custom_failure_text)
def broken_with_custom_message(policy_id: str) -> dict:
    """Always fails, with the tool's own failure text."""
    raise RuntimeError("backend down")


@function_tool
def says_error_but_works(policy_id: str) -> str:
    """Succeeds with a result that reads like the SDK's failure text."""
    return "An error occurred while running the tool. (This is the policy's free-text note, not a failure.)"


@function_tool(timeout=0.05)
async def too_slow(policy_id: str) -> str:
    """Times out."""
    await asyncio.sleep(5)
    return "late"


async def _tool_result_of(tool, args=None):
    model = ScriptedModel(
        [
            ([call("call_x", tool.name, args or {"policy_id": "P-9"})], usage(10, 2)),
            ([message("Done.")], usage(12, 3)),
        ]
    )
    async with adapter_task() as task:
        assert (
            await run_agent(task.ctx, Agent(name="claims-triage", instructions="x", model=model, tools=[tool]))
            == "Done."
        )
        return [e for e in steps(task) if isinstance(e, ToolFinished)][0].result


async def test_a_custom_failure_message_is_still_reported_as_a_failure_with_the_text_the_model_saw():
    assert await _tool_result_of(broken_with_custom_message) == {"error": "lookup unavailable (RuntimeError)"}


async def test_a_successful_result_that_reads_like_the_sdk_failure_text_is_a_result():
    data = await _tool_result_of(says_error_but_works)
    assert data == "An error occurred while running the tool. (This is the policy's free-text note, not a failure.)"


async def test_a_tool_timeout_given_to_the_model_as_text_is_reported_as_a_failure():
    assert await _tool_result_of(too_slow) == {"error": "Tool 'too_slow' timed out after 0.05 seconds."}


async def test_the_default_failure_text_is_reported_as_a_failure_and_the_users_tool_is_left_alone():
    from agents.tool import resolve_function_tool_failure_error_function

    data = await _tool_result_of(broken)
    assert set(data) == {"error"} and data["error"].startswith("An error occurred while running the tool.")
    assert resolve_function_tool_failure_error_function(broken).__name__ == "default_tool_error_function"


async def test_hooks_used_without_run_agent_report_a_raising_tool_as_failed_and_audit_it_so():
    """Runner.run with step_reporter's hooks directly: the hooks prepare the agent they report on (hooks.agent), so a
    tool that raises is a failed step and a failed execution in the audit trail, with nothing else to remember.

    The agents it hands off to are prepared too.
    """
    from agents import RunConfig, Runner

    from sam_link_sdk.openai_agents import step_reporter

    model = ScriptedModel(
        [
            ([call("call_x", "broken_with_custom_message", {"policy_id": "P-9"})], usage(1, 1)),
            ([message("Done.")], usage(1, 1)),
        ]
    )
    helper = Agent(name="helper", instructions="x", model=model, tools=[broken_with_custom_message])
    front = Agent(name="front", instructions="x", model=model, handoffs=[helper])
    records = []
    async with adapter_task(audit=records.append) as task:
        hooks = step_reporter(task.ctx, helper)
        await Runner.run(hooks.agent, "x", hooks=hooks, run_config=RunConfig(tracing_disabled=True))
        result = [e for e in steps(task) if isinstance(e, ToolFinished)][0]
        assert result.result == {"error": "lookup unavailable (RuntimeError)"}
        assert [(r.message, r.fields["tool"]) for r in records] == [
            ("tool execution failed", "broken_with_custom_message")
        ]
        assert step_reporter(task.ctx, front).agent.handoffs[0].tools[0] is not broken_with_custom_message


async def test_hooks_given_an_agent_they_did_not_prepare_refuse_to_run_it():
    from agents import RunConfig, Runner

    from sam_link_sdk.openai_agents import step_reporter

    model = ScriptedModel([([message("Done.")], usage(1, 1))])
    agent = Agent(name="front", instructions="x", model=model, tools=[broken_with_custom_message])
    async with adapter_task() as task:
        with pytest.raises(RuntimeError, match=r"run hooks.agent"):
            await Runner.run(
                agent, "x", hooks=step_reporter(task.ctx, agent), run_config=RunConfig(tracing_disabled=True)
            )
    assert model.turns


async def test_a_tool_the_agent_holds_for_approval_is_refused_on_a_channel_that_cannot_prompt():
    """Agent Mesh shows no prompt there (the e2e harness has only the web gateway, which can), so the user's answer is
    a cancel: the call does not run and the model is told it was not approved."""
    ran: list[str] = []

    @function_tool(needs_approval=True)
    def route_claim(policy_id: str) -> str:
        """Route a claim."""
        ran.append(policy_id)
        return "routed"

    model = ScriptedModel(
        [
            ([call("call_a", "route_claim", {"policy_id": "P-1"})], usage(10, 5)),
            ([message("Not routed.")], usage(20, 5)),
        ]
    )
    seen: list = []
    original = model.get_response

    async def get_response(*args, **kwargs):
        seen.append(kwargs.get("input") or args[1])
        return await original(*args, **kwargs)

    model.get_response = get_response
    agent = Agent(name="claims-triage", instructions="x", model=model, tools=[route_claim])
    async with adapter_task(can_prompt_user=False) as task:
        assert await run_agent(task.ctx, agent) == "Not routed."

    assert ran == []
    assert "did not approve this call to route_claim (cancelled), so it was not run." in json.dumps(
        seen[-1], default=str
    )


class ExportRecorder:
    """What the SDK's tracing hands its processors, which is what it exports to OpenAI's dashboard."""

    def __init__(self):
        self.exported: list[str] = []

    def on_trace_start(self, trace):
        self.exported.append("trace")

    def on_trace_end(self, trace):
        pass

    def on_span_start(self, span):
        self.exported.append("span")

    def on_span_end(self, span):
        pass

    def shutdown(self):
        pass

    def force_flush(self):
        pass


@pytest.fixture
def trace_export(monkeypatch):
    """Tracing as a deployment has it (the suite switches it off by environment), with a recorder as the only exporter."""
    from agents.tracing import get_trace_provider

    provider = get_trace_provider()
    monkeypatch.setattr(provider, "_env_disabled", False)
    monkeypatch.setattr(provider, "_manual_disabled", None)
    monkeypatch.setattr(provider._multi_processor, "_processors", ())
    recorder = ExportRecorder()
    provider.register_processor(recorder)
    return recorder


async def test_a_run_through_the_adapter_exports_nothing_to_openai_tracing(trace_export):
    model = ScriptedModel(
        [
            ([call("call_a", "lookup_policy", {"policy_id": "P-1"})], usage(10, 5)),
            ([message("P-1 is gold.")], usage(20, 5)),
        ]
    )
    agent = Agent(name="claims-triage", instructions="x", model=model, tools=[lookup_policy])
    async with adapter_task() as task:
        assert await run_agent(task.ctx, agent) == "P-1 is gold."
    assert trace_export.exported == []


async def test_a_run_config_the_caller_passes_keeps_its_own_tracing_setting(trace_export):
    from agents import RunConfig

    model = ScriptedModel([([message("Done.")], usage(10, 5))])
    agent = Agent(name="claims-triage", instructions="x", model=model)
    async with adapter_task() as task:
        await run_agent(task.ctx, agent, run_config=RunConfig(tracing_disabled=False))
    assert "trace" in trace_export.exported


async def test_a_handoff_is_a_progress_update_naming_the_agent_handed_to():
    router_model = ScriptedModel([([message("Routed.")], usage(10, 5))])
    router = Agent(name="router", instructions="x", model=router_model)
    front_model = ScriptedModel([([call("call_h", "transfer_to_router", {})], usage(10, 5))])
    front = Agent(name="front", instructions="x", model=front_model, handoffs=[router])
    async with adapter_task() as task:
        assert await run_agent(task.ctx, front) == "Routed."
        progress = [e for e in task.events if isinstance(e, Progress)]
    assert progress == [Progress("Handing off to router")]


async def test_an_agent_tool_given_its_own_hooks_and_run_config_runs_on_the_task_and_still_calls_those_hooks():
    """The hooks and the run config given to Agent.as_tool are kept for the nested run: its hooks are called after
    SAM's, and its run config's model is the one the nested steps name."""
    from agents import RunConfig, RunHooks

    called: list[str] = []

    class Theirs(RunHooks):
        async def on_llm_start(self, context, agent, system_prompt, input_items):
            called.append(f"llm {agent.name}")

        async def on_tool_end(self, context, agent, tool, result):
            called.append(f"tool {tool.name}")

    inner_model = ScriptedModel(
        [([call("call_l", "lookup_policy", {"policy_id": "P-1"})], usage(3, 1)), ([message("Gold.")], usage(4, 1))]
    )
    inner_model.model = "inner-gpt"
    inner = Agent(name="router", instructions="x", tools=[lookup_policy])
    tool = inner.as_tool("ask_router", "Routes.", hooks=Theirs(), run_config=RunConfig(model=inner_model))
    model = ScriptedModel(
        [([call("call_o", "ask_router", {"input": "P-1"})], usage(1, 1)), ([message("It is gold.")], usage(2, 1))]
    )
    async with adapter_task() as task:
        assert (
            await run_agent(task.ctx, Agent(name="front", instructions="x", model=model, tools=[tool])) == "It is gold."
        )
        sent = steps(task)
    assert called == ["llm router", "tool lookup_policy", "llm router"]
    assert [e.model for e in sent if isinstance(e, ModelStarted)] == ["fake-gpt", "inner-gpt", "inner-gpt", "fake-gpt"]
    assert [(e.name, e.result) for e in sent if isinstance(e, ToolFinished)] == [
        ("lookup_policy", {"policy_id": "P-1", "tier": "gold"}),
        ("ask_router", "Gold."),
    ]


def slow_input_guardrail(trips: bool):
    """A guardrail that runs alongside the first model turn (the Agents SDK's default) and decides after the model has
    written its text."""
    from agents import GuardrailFunctionOutput, input_guardrail

    @input_guardrail(name="slow_check")
    async def check(context, agent, user_input):
        await asyncio.sleep(0.2)
        return GuardrailFunctionOutput(output_info=None, tripwire_triggered=trips)

    return check


@pytest.mark.parametrize("trips", [True, False], ids=["blocked", "passed"])
async def test_text_a_parallel_input_guardrail_checks_reaches_the_user_only_once_it_passed(trips):
    """SAM cannot take back text it was streamed, so the text of a first turn that a parallel input guardrail still
    checks is held until the guardrail has passed, and never sent when it trips."""
    from agents.exceptions import InputGuardrailTripwireTriggered

    from sam_link_sdk.events import Text

    model = ScriptedModel([([message("The confidential answer.")], usage(5, 3))])
    agent = Agent(name="triage", model=model, input_guardrails=[slow_input_guardrail(trips)])
    async with adapter_task("T1") as t:
        if trips:
            with pytest.raises(InputGuardrailTripwireTriggered):
                await run_agent(t.ctx, agent)
        else:
            assert await run_agent(t.ctx, agent) == "The confidential answer."
        sent = "".join(e.text for e in t.events if isinstance(e, Text))
    assert ("confidential" in sent) is not trips
