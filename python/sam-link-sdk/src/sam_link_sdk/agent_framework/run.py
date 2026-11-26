# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Run a Microsoft Agent Framework agent on a mesh task."""

from __future__ import annotations

from collections.abc import Iterable, Sequence
from typing import Any

from agent_framework import Agent, Message

from .._adapter import halts_bare, input_text, request_prompt
from ..context import TaskContext, bind_task
from ..memory import ConversationMemory
from .steps import _call_id, _decide, _reporter, _Run
from .tools import _Approval, _Held


def _final_answer(response: Any) -> str:
    for message in reversed(response.messages):
        if message.role == "assistant" and message.text:
            return message.text.strip()
    return str(response.text or "").strip()


@halts_bare
async def run_agent(
    ctx: TaskContext,
    agent: Agent,
    *,
    input: str | None = None,
    memory: ConversationMemory | None = None,
    include_request_context: bool = False,
    stream_text: bool = True,
    middleware: Sequence[Any] = (),
    extra_tools: Iterable[Any] = (),
) -> str:
    """Run an Agent Framework agent on the task's input with its steps reported to SAM; returns the answer text.

    `middleware` and `extra_tools` are added to the agent's own for this run only, so build the ones that depend on
    the task with it: ``extra_tools=[*mesh_agent_tools(ctx), ask_user_tool(ctx)]``, ``middleware=[with_approval(ctx, tools, names=[...])]``.
    The agent is shared by all tasks: it holds no conversation of its own here (each run gets a session of its own). Agents run as
    tools (``inner.as_tool()``) are reported, checked and asked for approval as this one (see
    ``sam_link_sdk.agent_framework.steps``).

    With `memory`, the conversation's earlier turns (keyed by ``ctx.session_id``) are replayed before the new
    message, and this turn's messages are remembered once it finishes; a run-based task (a peer call or a workflow
    node) neither reads nor adds history. With `include_request_context`, the gateway's system purpose, response
    format, user profile and conversation context (``ctx.request_context``) are sent as a system message, as SAM-Go
    agents put them in the system prompt. Tools whose required scopes the task's user lacks are left out of
    every model request of the run, and a call to one anyway is refused.
    A cancelled or stopped task cancels the run, including an async tool in flight.

    With `stream_text` (the default) the agent is run with ``stream=True`` and the model's text goes to the user as it
    is written (``ctx.emit_text``); pass False to send the answer whole when the run ends.

    A tool the framework holds for approval itself (``@tool(approval_mode="always_require")``) asks the user through
    SAM, as ``with_approval`` does, and the run resumes with the answer, streamed the same way: a refusal is told to
    the model as ``with_approval``'s is. So does one in an agent run as a tool, which resumes without streaming.

    The framework honours an approval response only for a request its session remembers.
    """
    text = input_text(ctx, input)
    user = Message(role="user", contents=[text])
    prior = memory.history(ctx.session_id) if memory is not None else []
    system = []
    if prompt := request_prompt(ctx, include_request_context):
        system = [Message(role="system", contents=[prompt])]
    extra_tools = list(extra_tools)
    run = _Run(ctx)
    answers = run.answers
    run.approvals = [_Held(answers), *(m for m in middleware if isinstance(m, _Approval))]
    messages = [*system, *prior, user]
    added = [user]
    own_tools = [*(agent.default_options.get("tools") or []), *extra_tools]
    session = agent.create_session()
    with bind_task(ctx):
        options: dict[str, Any] = {"middleware": [*_reporter(run), _Held(answers), *middleware], "session": session}
        if extra_tools:
            options["tools"] = extra_tools
        while True:
            if stream_text:
                stream = agent.run(messages, stream=True, **options)
                try:
                    async for update in stream:
                        if update.text:
                            await ctx.emit_text(update.text)
                    response = await stream.get_final_response()
                finally:
                    run.unroute()
            else:
                response = await agent.run(messages, **options)
            added += response.messages
            requests = response.user_input_requests
            if not requests:
                break
            for request in requests:
                answers[_call_id(request)] = await _decide(ctx, request, own_tools)
            messages = [Message(role="user", contents=[r.to_function_approval_response(True) for r in requests])]
            added += messages
    answer = _final_answer(response)
    if memory is not None:
        memory.remember(ctx.session_id, added, text, answer)
    return answer
