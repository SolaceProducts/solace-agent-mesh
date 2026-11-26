# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Run a Strands agent on a mesh task."""

from __future__ import annotations

import asyncio
from collections.abc import Callable
from typing import Any

from strands import Agent

from .._adapter import decide_approval, halts_bare, input_text, join_prompts, request_prompt
from ..context import TaskContext, bind_task
from ..memory import ConversationMemory
from .steps import SamLinkHooks, step_reporter


def _with_request_context(agent: Agent, prompt: str) -> None:
    agent.system_prompt = join_prompts(agent.system_prompt or "", prompt)


def _message(reason: Any) -> str:
    return str(reason.get("message") or "") if isinstance(reason, dict) else str(reason or "")


async def _decide(ctx: TaskContext, agent: Agent, hooks: SamLinkHooks, interrupt: Any) -> tuple[str | None, str | None]:
    """The tool call an interrupt (Strands' own approval: a hook's ``event.interrupt``, a ``Confirm`` intervention)
    holds, and None when the user approves it, else why it is refused. The call is the one whose id the interrupt's id
    carries; when none is, the interrupt's name stands for the tool. Asked through SAM, unless refused for a fact known
    before asking (a stopped task, the user's scopes)."""
    call_id = next((c for c in hooks.calls if c in str(interrupt.id).split(":")), None)
    name, args = hooks.calls[call_id] if call_id else (str(interrupt.name), {})
    tool = agent.tool_registry.registry.get(name)
    refused = await decide_approval(ctx, name, args, _message(interrupt.reason), [tool] if tool else [])
    return call_id, refused


async def _invoke(ctx: TaskContext, agent: Agent, prompt: Any, stream_text: bool) -> Any:
    """One pass of the agent over `prompt` (the task's text, or the answers to its interrupts), streaming its text."""
    if not stream_text:
        return await agent.invoke_async(prompt)
    result = None
    async for event in agent.stream_async(prompt):
        delta = event.get("data")
        if isinstance(delta, str) and delta:
            await ctx.emit_text(delta)
        if "result" in event:
            result = event["result"]
    if result is None:
        raise RuntimeError("the agent ended without a result")
    return result


@halts_bare
async def run_agent(
    ctx: TaskContext,
    build: Callable[[TaskContext], Agent],
    *,
    input: str | None = None,
    memory: ConversationMemory | None = None,
    include_request_context: bool = False,
    stream_text: bool = True,
) -> str:
    """Run a Strands agent on the task's input with its steps reported to SAM; returns the answer text.

    `build(ctx)` makes the agent for this task (a Strands agent keeps its conversation and rejects concurrent
    runs, so it is not shared between tasks). Build per-task tools with it: ``mesh_agent_tools(ctx)``,
    ``ask_user_tool(ctx)`` and ``hooks=[with_approval(ctx, tools, names=[...])]``.

    With `memory`, the conversation's earlier turns (keyed by ``ctx.session_id``) are replayed before the new
    message, and this turn's messages are remembered once it finishes; a run-based task (a peer call or a workflow
    node) neither reads nor adds history. With `include_request_context`, the gateway's system purpose, response
    format, user profile and conversation context (``ctx.request_context``) are appended to the system prompt, as
    SAM-Go agents do. A call to a tool whose required scopes the task's user lacks is refused (audited once per
    run); Strands cannot take a tool off a built agent, so to keep such tools out of the model's list too, build the
    agent with ``ctx.permitted_tools(tools)``.
    A cancelled or stopped task cancels the agent, which stops at its next safe point.

    With `stream_text` (the default) the agent runs through ``stream_async`` and the model's text goes to the user as
    it is written (``ctx.emit_text``); pass False to send the answer whole when the run ends.

    A tool call Strands holds for approval itself (a hook's ``event.interrupt(...)``, a ``Confirm`` intervention) asks
    the user through SAM, as ``with_approval`` does, with the interrupt's reason as the message and its name as the
    tool when the call cannot be told, and the agent resumes with the answer, streamed the same way: ``True`` to
    approve, ``False`` to refuse. A refusal is told to the model as ``with_approval``'s is.
    """
    agent = build(ctx)
    if not isinstance(agent, Agent):
        raise TypeError("build(ctx) must return a strands.Agent")
    hooks = step_reporter(ctx)
    agent.hooks.add_hook(hooks)
    if prompt := request_prompt(ctx, include_request_context):
        _with_request_context(agent, prompt)
    prior = memory.history(ctx.session_id) if memory is not None else []
    if prior:
        agent.messages[:0] = prior
    text = input_text(ctx, input)
    try:
        with bind_task(ctx):
            result = await _invoke(ctx, agent, text, stream_text)
            while result.stop_reason == "interrupt":
                responses = []
                for interrupt in result.interrupts:
                    call_id, refused = await _decide(ctx, agent, hooks, interrupt)
                    if refused and call_id:
                        hooks.refusals[call_id] = refused
                    responses.append({"interruptResponse": {"interruptId": interrupt.id, "response": refused is None}})
                result = await _invoke(ctx, agent, responses, stream_text)
    except asyncio.CancelledError:
        agent.cancel()
        raise
    answer = str(result).strip()
    if memory is not None:
        memory.remember(ctx.session_id, hooks.added, text, answer)
    return answer
