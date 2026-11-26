# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Run a Google ADK Runner's agent on a mesh task."""

from __future__ import annotations

import uuid
from collections.abc import Iterator
from typing import Any

from google.adk.agents.run_config import RunConfig, StreamingMode
from google.adk.runners import Runner
from google.adk.tools.agent_tool import AgentTool
from google.adk.utils._function_call_names import REQUEST_CONFIRMATION_FUNCTION_CALL_NAME
from google.genai import types

from .._adapter import NestedRunRefused, decide_approval, halts_bare, input_text, request_prompt, unwrapped
from ..context import TaskContext, bind_task
from .steps import SamLinkPlugin, _answers, _request_prompt, _text
from .tools import _ApprovalToolset

_ANONYMOUS_USER = "sam-link-sdk"
"""The ADK user of a task SAM sent without a user id."""
NO_REPORTER = (
    "the Runner has no step_reporter() plugin: build it with Runner(..., plugins=[step_reporter()]). "
    "Without it the mesh sees no steps and per-tool scopes are not enforced, so run_agent refuses to run it"
)


def _own_tools(agent: Any) -> Iterator[Any]:
    """The tools of `agent` that can be seen before the run: its own, and those inside this adapter's approval
    toolsets (other toolsets resolve their tools at run time, where the plugin checks each call)."""
    for tool in getattr(agent, "tools", None) or []:
        yield from tool.tools if isinstance(tool, _ApprovalToolset) else [tool]


def _tools_seen(agent: Any) -> Iterator[Any]:
    """`_own_tools`, unwrapped from the scope and approval wrappers."""
    for tool in _own_tools(agent):
        yield unwrapped(tool)


def _isolated_agent_tools(agent: Any, seen: set[int] | None = None) -> Iterator[str]:
    """The names of the AgentTools reachable from `agent` (through its tools, sub-agents and other agent tools) that
    run their agent without the Runner's plugins (``include_plugins=False``).

    The check fails closed: an AgentTool that no longer says whether it includes the plugins is treated as isolated.
    """
    seen = set() if seen is None else seen
    if agent is None or id(agent) in seen:
        return
    seen.add(id(agent))
    for tool in _tools_seen(agent):
        if isinstance(tool, AgentTool):
            if not getattr(tool, "include_plugins", False):
                yield tool.name
            yield from _isolated_agent_tools(getattr(tool, "agent", None), seen)
    for sub in getattr(agent, "sub_agents", None) or []:
        yield from _isolated_agent_tools(sub, seen)


def _refuse_isolated_agent_tools(agent: Any) -> None:
    """Refuse to run an agent that can run an AgentTool without the Runner's plugins: that nested run would report no
    steps or tokens to SAM and nothing would check its tools, its toolsets' (an MCP toolset's) included."""
    isolated = sorted(set(_isolated_agent_tools(agent)))
    if isolated:
        raise NestedRunRefused(
            f"the agent tool(s) {', '.join(isolated)} run their agent without the Runner's plugins "
            "(AgentTool(include_plugins=False)), so SAM would neither see nor check that run: let them include the "
            "plugins (the default)"
        )


def _confirmations(event: Any) -> Iterator[tuple[str, dict]]:
    """The confirmations ADK asks for in `event` (a call held by ``require_confirmation``): the id of the request,
    and the call that waits."""
    for call in event.get_function_calls() or []:
        if call.name == REQUEST_CONFIRMATION_FUNCTION_CALL_NAME and call.id in (event.long_running_tool_ids or ()):
            yield call.id, dict((call.args or {}).get("originalFunctionCall") or {})


async def _decide(ctx: TaskContext, runner: Runner, held: dict) -> str | None:
    """None when the user approves the call ADK held for confirmation, else why it is refused: asked through SAM,
    unless refused for a fact known before asking (a stopped task, the user's scopes)."""
    return await decide_approval(
        ctx, str(held.get("name") or ""), dict(held.get("args") or {}), "", _own_tools(runner.agent)
    )


@halts_bare
async def run_agent(
    ctx: TaskContext,
    runner: Runner,
    *,
    input: str | None = None,
    include_request_context: bool = False,
    max_model_calls: int = 10,
    stream_text: bool = True,
    run_config: RunConfig | None = None,
) -> str:
    """Run the Runner's agent on the task's input (or `input`); returns the final response text.

    The SAM session (``ctx.session_id``) is the ADK session id, so turns in one SAM chat share ADK session state (ADK's
    session service is the conversation memory); a run-based task (a peer call or a workflow node) gets its own
    session, deleted when it finishes. The SAM user is the ADK user, so ADK's ``user:`` state and its memory service
    keep each user's own. The Runner must have the step reporter in its plugins (``plugins=[step_reporter()]``), else
    this raises ValueError: the plugin reports the steps and refuses a call to a tool the user lacks the scopes for.
    With `include_request_context`, the gateway's system purpose, response format, user profile and conversation
    context (`ctx.request_context`) are added to each model request's instructions, as SAM-Go agents do. `run_config` is
    passed to ``Runner.run_async`` with its ``max_llm_calls`` set to `max_model_calls`: a run that reaches it fails the
    task saying so.

    With `stream_text` (the default) the run uses ADK's SSE streaming mode (unless `run_config` already sets one) and
    the model's text goes to the user as it is written (``ctx.emit_text``); pass False to send the answer whole when the
    run ends.

    A tool ADK holds for confirmation (``FunctionTool(fn, require_confirmation=True)``) asks the user through SAM, as
    ``with_approval`` does: the run ends with an ``adk_request_confirmation`` call, and `run_agent` runs again with
    the answer, streamed the same way, until none is left. A refusal is told to the model as ``with_approval``'s is.

    A run-based task's session is new for each delivery, because a run given up may still be running when SAM delivers
    the task again, and its end must not delete the new run's session.
    """
    plugin = next((p for p in getattr(runner.plugin_manager, "plugins", []) if isinstance(p, SamLinkPlugin)), None)
    if plugin is None:
        raise ValueError(NO_REPORTER)
    _refuse_isolated_agent_tools(runner.agent)
    sessions = runner.session_service
    user_id = ctx.user_id or _ANONYMOUS_USER
    session_id = ctx.session_id or f"{ctx.task_id}-{uuid.uuid4().hex[:12]}"
    session = await sessions.get_session(app_name=runner.app_name, user_id=user_id, session_id=session_id)
    if session is None:
        await sessions.create_session(app_name=runner.app_name, user_id=user_id, session_id=session_id)

    run_config = (run_config or RunConfig()).model_copy(update={"max_llm_calls": max_model_calls})
    if stream_text and run_config.streaming_mode == StreamingMode.NONE:
        run_config = run_config.model_copy(update={"streaming_mode": StreamingMode.SSE})
    prompt_token = _request_prompt.set(request_prompt(ctx, include_request_context))
    answers: dict[str, str | None] = {}
    answers_token = _answers.set(answers)
    try:
        with bind_task(ctx):
            new_message = types.Content(role="user", parts=[types.Part(text=input_text(ctx, input))])
            while True:
                final = ""
                held: list[tuple[str, dict]] = []
                async for event in runner.run_async(
                    user_id=user_id, session_id=session_id, new_message=new_message, run_config=run_config
                ):
                    if stream_text and event.partial and event.content is not None:
                        delta = _text(event.content)
                        if delta:
                            await ctx.emit_text(delta)
                    if event.is_final_response() and event.content is not None:
                        final = _text(event.content) or final
                    held.extend(_confirmations(event))
                if not held:
                    return final
                parts = []
                for request_id, call in held:
                    answers[str(call.get("id"))] = await _decide(ctx, runner, call)
                    parts.append(
                        types.Part(
                            function_response=types.FunctionResponse(
                                id=request_id,
                                name=REQUEST_CONFIRMATION_FUNCTION_CALL_NAME,
                                response={"confirmed": True},
                            )
                        )
                    )
                new_message = types.Content(role="user", parts=parts)
    finally:
        plugin._forget(ctx)
        _answers.reset(answers_token)
        _request_prompt.reset(prompt_token)
        if not ctx.session_id:
            await sessions.delete_session(app_name=runner.app_name, user_id=user_id, session_id=session_id)
