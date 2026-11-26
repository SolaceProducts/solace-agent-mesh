# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""ADK plugin that reports model and tool steps of a Runner to the mesh task.

The step events map directly from what ADK hands over (`content.role: "model"`, `function_call`,
`usage_metadata`). ADK registers plugins per Runner, not per run, so the plugin routes callbacks to the
mesh task that started the run: `run_agent` binds the task's context, and `before_run_callback` records it
under the ADK invocation id.

ADK assigns `adk-…` function-call ids after `after_model_callback`, so `llm_response` is built from the model
event (`on_event_callback`), which carries the final ids — the same ids the tool callbacks see.

A tool the task's user lacks the scopes for is taken out of each model request's function declarations, so the model
is not offered it; ADK still knows the tool, so a call to it anyway reaches `before_tool_callback`, which refuses it
(its returned dict is the call's result, and the tool never runs).
"""

from __future__ import annotations

import contextvars
import logging
from typing import Any

from google.adk.plugins.base_plugin import BasePlugin
from google.adk.tools.agent_tool import AgentTool

from .._adapter import (
    NestedRunRefused,
    Steps,
    ToolScopes,
    check_running,
    check_usage_read,
    error_result,
    may_be_lost,
    model_finished,
    new_call_id,
    over,
    token_count,
    unwrapped,
    usage_of,
)
from ..context import TaskContext, _current_task
from ..usage import Usage

log = logging.getLogger("sam_link_sdk.adk")

_current = _current_task
_request_prompt: contextvars.ContextVar[str] = contextvars.ContextVar("sam_link_adk_request_prompt", default="")
_answers: contextvars.ContextVar[dict[str, str | None]] = contextvars.ContextVar("sam_link_adk_answers")
_HOLD_CHECK_WARNED = False
NO_ANSWER = "The user did not approve this call to {name} (no answer was recorded), so it was not run."


async def _holds(tool: Any, args: dict, tool_context: Any) -> bool:
    """Whether ADK will hold this call for the user's confirmation (``require_confirmation``) instead of running it."""
    global _HOLD_CHECK_WARNED
    try:
        return await unwrapped(tool).check_require_confirmation(args, tool_context) is True
    except Exception:
        if not _HOLD_CHECK_WARNED:
            _HOLD_CHECK_WARNED = True
            log.warning(
                "check_require_confirmation raised for the tool %s; treating its calls as not held for confirmation "
                "(logged once)",
                getattr(tool, "name", ""),
                exc_info=True,
            )
        return False


def _usage_from(meta: Any, cache_write: Any = None) -> Usage:
    """Gemini's usage metadata, as ADK hands it over: ``prompt_token_count`` holds the cached content
    (``cached_content_token_count``); tool-use prompt tokens and, from Gemini itself, thinking tokens are counted
    apart (the total adds them), so they are added to the input and the output. Through LiteLLM, ADK copies
    ``prompt_tokens`` (which holds the cache reads and writes) and ``completion_tokens`` (which holds the reasoning),
    and the writes ride on the response (`cache_write`)."""
    if meta is None:
        return Usage()
    prompt = token_count(getattr(meta, "prompt_token_count", 0)) + token_count(
        getattr(meta, "tool_use_prompt_token_count", 0)
    )
    candidates = token_count(getattr(meta, "candidates_token_count", 0))
    thoughts = token_count(getattr(meta, "thoughts_token_count", 0))
    total = token_count(getattr(meta, "total_token_count", 0))
    output = candidates + thoughts if thoughts and total >= prompt + candidates + thoughts else candidates
    return usage_of(
        input=prompt,
        output=output,
        cache_read=getattr(meta, "cached_content_token_count", 0),
        cache_write=cache_write,
        reasoning=thoughts,
        total=total,
        input_includes_cache=True,
    )


def _text(content: Any) -> str:
    return "".join(
        p.text
        for p in (getattr(content, "parts", None) or [])
        if getattr(p, "text", None) and not getattr(p, "thought", False)
    )


def _hide_tools(llm_request: Any, scopes: ToolScopes) -> None:
    """Take the declarations of the tools the user may not use out of the request the model gets."""
    for tool in getattr(getattr(llm_request, "config", None), "tools", None) or []:
        declarations = getattr(tool, "function_declarations", None)
        if declarations:
            tool.function_declarations = [d for d in declarations if scopes.permits(str(d.name or ""))]


class _Run:
    def __init__(self, ctx: TaskContext):
        self.ctx = ctx
        self.scopes = ToolScopes(ctx)
        self.steps = Steps(ctx)
        self.model = "default"
        self.started: set[str] = set()
        """Calls whose start was reported. ADK runs the after-tool callbacks even for a call a before-tool callback
        answered itself (a refused call), which must not show as a tool that ran.
        """

    def finish(self, call_id: str) -> bool:
        if call_id not in self.started:
            return False
        self.started.discard(call_id)
        return True


class SamLinkPlugin(BasePlugin):
    """Reports the model and tool steps of the Runner's runs to the mesh task each serves, and refuses a call to a
    tool the task's user lacks the scopes for (what ``step_reporter`` returns). Add once to the Runner; run tasks with
    `run_agent`, which refuses a Runner without it."""

    def __init__(self, name: str = "sam_link_sdk"):
        super().__init__(name=name)
        self._runs: dict[str, _Run] = {}

    def _run(self, invocation_id: str | None) -> _Run | None:
        run = self._runs.get(invocation_id or "")
        if run is None:
            ctx = _current.get()
            if ctx is not None and invocation_id:
                run = self._runs[invocation_id] = _Run(ctx)
        return run

    async def _safe(self, ctx: TaskContext, coro) -> None:
        """Run a step report so that losing it cannot fail the run.

        An exception in an ADK plugin aborts the agent run; a lost step must not.
        """
        with may_be_lost(ctx):
            await coro

    async def before_run_callback(self, *, invocation_context) -> None:
        self._run(invocation_context.invocation_id)
        return None

    async def after_run_callback(self, *, invocation_context) -> None:
        self._runs.pop(invocation_context.invocation_id, None)
        return None

    async def on_run_error_callback(self, *, invocation_context, error: Exception) -> None:
        """ADK skips after_run_callback when a run raises, and the plugin lives as long as the process."""
        self._runs.pop(invocation_context.invocation_id, None)

    def _forget(self, ctx: TaskContext) -> None:
        """Drop the runs of a task, for a run ended in a way ADK calls neither run-end callback for (a cancel from
        outside the run)."""
        for invocation_id in [i for i, run in self._runs.items() if run.ctx is ctx]:
            self._runs.pop(invocation_id, None)

    async def before_model_callback(self, *, callback_context, llm_request) -> None:
        run = self._run(callback_context.invocation_id)
        if run is not None:
            check_running(run.ctx)
            run.model = str(getattr(llm_request, "model", None) or "default")
            prompt = _request_prompt.get()
            if prompt:
                llm_request.append_instructions([prompt])
            _hide_tools(llm_request, run.scopes)
            await self._safe(run.ctx, run.steps.model_call(run.model))
        return None

    async def on_event_callback(self, *, invocation_context, event) -> None:
        run = self._run(invocation_context.invocation_id)
        content = getattr(event, "content", None)
        if (
            run is None
            or content is None
            or getattr(content, "role", None) != "model"
            or getattr(event, "partial", False)
        ):
            return None
        calls = []
        for part in content.parts or []:
            fc = getattr(part, "function_call", None)
            if fc is not None:
                calls.append({"id": fc.id or new_call_id(), "name": fc.name, "args": dict(fc.args or {})})
        meta = getattr(event, "usage_metadata", None)
        usage = _usage_from(meta, getattr(event, "cache_creation_token_count", None))
        check_usage_read("sam_link_sdk.adk", run.model, meta, usage, _text(content), calls)
        reasoning = "".join(
            p.text for p in content.parts or [] if getattr(p, "thought", False) and getattr(p, "text", None)
        )
        if reasoning:
            await self._safe(run.ctx, run.ctx.emit_thinking(reasoning))
            await self._safe(run.ctx, run.ctx.end_thinking())
        await self._safe(run.ctx, run.ctx.emit_event(model_finished(run.model, _text(content), calls, usage)))
        return None

    async def before_tool_callback(self, *, tool, tool_args, tool_context) -> dict | None:
        """Check a tool call before ADK runs it and report its start.

        A returned dict replaces the tool's result, which is how a call is refused, as SAM-Go refuses it at dispatch:
        when the task is over (a stopped task publishes nothing more, and an answered one cannot), when the user lacks
        the tool's scopes, or when the user declined it.

        An agent tool that runs without the Runner's plugins, which can be a toolset handed over at run time that
        `run_agent` could not see beforehand, would run unseen, so the run ends there (ADK ends a run whose plugin
        raises).

        A confirmed call is always sent to ADK as a confirmation and decided here, because ADK words a rejection in its
        own fixed text; a call with no recorded answer is refused.
        """
        run = self._run(tool_context.invocation_id)
        if run is None:
            return None
        call_id = tool_context.function_call_id or ""
        ended = over(run.ctx, tool.name)
        if ended:
            return error_result(ended)
        if isinstance(unwrapped(tool), AgentTool) and not getattr(unwrapped(tool), "include_plugins", False):
            raise NestedRunRefused(
                f"the agent tool {tool.name} runs its agent without the Runner's plugins "
                "(AgentTool(include_plugins=False)), so SAM would neither see nor check that run"
            )
        if not run.scopes.permits(tool.name):
            refused = await run.steps.refuse(tool.name, dict(tool_args or {}), call_id)
            return refused
        if tool_context.tool_confirmation is None and await _holds(tool, tool_args or {}, tool_context):
            return None
        run.started.add(call_id)
        await self._safe(run.ctx, run.steps.tool_start(tool.name, dict(tool_args or {}), call_id))
        if tool_context.tool_confirmation is not None:
            unanswered = _answers.get({}).get(call_id, NO_ANSWER.format(name=tool.name))
            if unanswered:
                return error_result(unanswered)
        return None

    async def after_tool_callback(self, *, tool, tool_args, tool_context, result) -> None:
        run = self._run(tool_context.invocation_id)
        call_id = tool_context.function_call_id or ""
        if run is not None and run.finish(call_id):
            await self._safe(run.ctx, run.steps.tool_end(tool.name, result, call_id))
        return None

    async def on_tool_error_callback(self, *, tool, tool_args, tool_context, error) -> None:
        run = self._run(tool_context.invocation_id)
        call_id = tool_context.function_call_id or ""
        if run is not None and run.finish(call_id):
            await self._safe(run.ctx, run.steps.tool_end(tool.name, error_result(str(error)), call_id))
        return None


def step_reporter() -> SamLinkPlugin:
    """The plugin that reports model and tool steps to the mesh and enforces per-tool scopes: add it once where the
    Runner is built, ``Runner(..., plugins=[step_reporter()])``, and run tasks with ``run_agent`` (which refuses a
    Runner without it). ADK takes plugins per Runner, not per run, so it takes no task: ``run_agent`` tells it which
    task a run serves."""
    return SamLinkPlugin()
