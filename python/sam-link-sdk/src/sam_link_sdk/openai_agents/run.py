# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Run an OpenAI Agents SDK agent on a mesh task."""

from __future__ import annotations

import asyncio
import json
from typing import Any

from agents import Agent, AgentUpdatedStreamEvent, RawResponsesStreamEvent, RunConfig, Runner
from agents.result import RunResultBase
from openai.types.responses import ResponseTextDeltaEvent

from .._adapter import decide_approval, halts_bare, input_text, parse_args
from ..context import TaskContext, bind_task
from ..memory import ConversationMemory
from .steps import _result_data, step_reporter


async def _decide(ctx: TaskContext, item: Any) -> str | None:
    """None when the user approves the call the SDK held for approval (``needs_approval``), else why it is refused."""
    return await decide_approval(
        ctx, item.tool_name or "", parse_args(getattr(item.raw_item, "arguments", None)), "", item.agent.tools
    )


async def _run(
    ctx: TaskContext, hooks: Any, start: Any, run_config: RunConfig, max_model_calls: int, stream_text: bool
) -> Any:
    """Run until the agent answers, streaming its text when asked. A tool the agent's own approval flag holds ends a
    run with interruptions: the user is asked through SAM for each, and the run resumes from its state with the
    answers, streamed the same way.

    Text written while an output guardrail applies (the run config's, or the agent's then answering) is held until
    the run ends and sent only if no guardrail tripped: SAM has no way to take back text it was streamed. So is text
    written while an input guardrail that runs in parallel with the first turn (the Agents SDK's default) has not
    passed yet: it is sent once all of them have."""
    held: list[str] = []
    current = hooks.agent
    checking = [g for g in [*hooks.agent.input_guardrails, *(run_config.input_guardrails or [])] if g.run_in_parallel]
    while True:
        result: RunResultBase
        if stream_text:
            streamed = Runner.run_streamed(
                hooks.agent, start, hooks=hooks, run_config=hooks.refusing(run_config), max_turns=max_model_calls
            )
            result = streamed
            try:
                async for event in streamed.stream_events():
                    if isinstance(event, AgentUpdatedStreamEvent):
                        current = event.new_agent
                    elif isinstance(event, RawResponsesStreamEvent) and isinstance(event.data, ResponseTextDeltaEvent):
                        checking = _unpassed(checking, streamed.input_guardrail_results)
                        if run_config.output_guardrails or current.output_guardrails or checking:
                            held.append(event.data.delta)
                        else:
                            if held:
                                await ctx.emit_text("".join(held))
                                held = []
                            await ctx.emit_text(event.data.delta)
            except asyncio.CancelledError:
                result.cancel()
                raise
        else:
            result = await Runner.run(
                hooks.agent, start, hooks=hooks, run_config=hooks.refusing(run_config), max_turns=max_model_calls
            )
        if not result.interruptions:
            if held:
                await ctx.emit_text("".join(held))
            return result
        checking = []
        start = result.to_state()
        for item in result.interruptions:
            refused = await _decide(ctx, item)
            if refused is None:
                start.approve(item)
            else:
                start.reject(item, rejection_message=refused)


def _unpassed(guardrails: list[Any], results: list[Any]) -> list[Any]:
    """The guardrails among ``guardrails`` that have not passed yet: no result, or one whose tripwire fired."""
    passed = [r.guardrail for r in results if not r.output.tripwire_triggered]
    return [g for g in guardrails if not any(g is p for p in passed)]


@halts_bare
async def run_agent(
    ctx: TaskContext,
    agent: Agent,
    *,
    input: str | None = None,
    memory: ConversationMemory | None = None,
    include_request_context: bool = False,
    max_model_calls: int = 10,
    stream_text: bool = True,
    run_config: RunConfig | None = None,
) -> str:
    """Run an Agents SDK agent on the task's input with its steps reported to SAM.

    OpenAI trace export is off unless you pass your own ``run_config``: the SDK otherwise uploads every
    model input and tool result of the task to the OpenAI dashboard.

    With `memory`, the conversation's earlier turns (keyed by ``ctx.session_id``) are replayed before the new
    message, and this turn's items are remembered once it finishes; a run-based task (a peer call or a workflow
    node) neither reads nor adds history. With `include_request_context`, the gateway's
    system purpose, response format, user profile and conversation context (`ctx.request_context`) are appended to
    the instructions of the agent and of every agent it hands off to or runs as a tool, for this run, as SAM's own
    agents do.

    A run still calling tools after `max_model_calls` model calls fails the task saying so.

    A tool whose required scopes (AgentConfig.tools) the task's user lacks is left out of this run, in the agent and
    in every agent it hands off to, and a call to it anyway is refused (see ``sam_link_sdk.openai_agents.steps``). A call
    to a tool the agent does not have at all gives the model the SDK's "not found" error instead of failing the run.

    With `stream_text` (the default) the run is streamed (``Runner.run_streamed``) and the model's text goes to the
    user as it is written (``ctx.emit_text``); pass False to send the answer whole when the run ends. Text an output
    guardrail checks (the run config's, or the answering agent's) is held until the run ends and sent only when no
    guardrail tripped, so the user never sees an answer a guardrail blocked. Text the first turn writes while an input
    guardrail that runs in parallel with it (``run_in_parallel=True``, the default) has not passed is held until all
    of them have, and dropped when one trips.

    A tool the agent holds for approval itself (``function_tool(..., needs_approval=True)``) asks the user through
    SAM, as ``with_approval`` does, and the run resumes with the answer: a refusal is told to the model as
    ``with_approval``'s is.

    The result's `to_input_list()` is the whole conversation so far, so only what this turn added is remembered.
    """
    run_config = run_config or RunConfig(tracing_disabled=True)
    text = input_text(ctx, input)
    prior = memory.history(ctx.session_id) if memory is not None else []
    hooks = step_reporter(ctx, agent, run_config=run_config, include_request_context=include_request_context)
    run_input = [*prior, {"role": "user", "content": text}] if prior else text
    with bind_task(ctx):
        result = await _run(ctx, hooks, run_input, run_config, max_model_calls, stream_text)
    output = result.final_output
    answer = output if isinstance(output, str) else json.dumps(_result_data(output))
    if memory is not None:
        memory.remember(ctx.session_id, result.to_input_list()[len(prior) :], text, answer)
    return answer
