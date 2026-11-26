# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""LangChain callback handler that reports model and tool steps to the mesh task, and refuses a call to a tool the
task's user lacks the scopes for.

LangChain and LangGraph hand a run config's callbacks to every model and tool call inside a runnable or graph, so the
handler sees each tool start before the tool runs. A callback cannot replace a tool's result, only end the call by
raising; so a refused call is reported (its start and a failed result) and the handler raises ``ToolAccessDenied``,
which ``run_agent`` gives the model as an error result. In a LangGraph graph it is the tool's exception: a ``ToolNode``
with ``handle_tool_errors`` gives it to the model, the default one ends the run with it, as with any failing tool.

``on_tool_start`` and the model-start callbacks are synchronous: LangChain runs a synchronous tool (and its callbacks,
and whatever it runs in turn) on a worker thread even from ``ainvoke``, and there it lets an asynchronous callback's
exception pass silently, the call going ahead anyway; a synchronous callback's exception ends the call on every path.
So a stopped task's run, nested ones included, ends at its next model or tool call, and a refused tool does not run.
"""

from __future__ import annotations

import asyncio
import json
import logging
from collections.abc import Awaitable
from typing import Any
from uuid import UUID

from langchain_core.callbacks import AsyncCallbackHandler
from langchain_core.messages import AIMessage, BaseMessage, ToolMessage
from langchain_core.outputs import ChatGeneration, LLMResult

from .._adapter import (
    PendingCalls,
    Steps,
    ToolAccessDenied,
    ToolScopes,
    check_running,
    check_usage_read,
    error_result,
    may_be_lost,
    new_call_id,
    plain_result,
    usage_of,
)
from ..context import TaskContext
from ..usage import Usage

log = logging.getLogger("sam_link_sdk.langchain")

STEP_TIMEOUT_SECONDS = 30.0


def _model_name(serialized: dict | None, kwargs: dict) -> str:
    metadata = kwargs.get("metadata") or {}
    params = kwargs.get("invocation_params") or {}
    ser_kwargs = (serialized or {}).get("kwargs") or {}
    for value in (
        metadata.get("ls_model_name"),
        params.get("model"),
        params.get("model_name"),
        ser_kwargs.get("model"),
        ser_kwargs.get("model_name"),
    ):
        if value:
            return str(value)
    return "unknown"


def _detail(details: dict, kind: str) -> int:
    """A token detail summed over its service tiers: langchain-openai files a priority or flex request's cache reads
    and reasoning as ``priority_cache_read`` / ``flex_reasoning`` and so on rather than ``cache_read`` / ``reasoning``."""
    return sum(int(v or 0) for k, v in details.items() if (k == kind or k.endswith("_" + kind)) and isinstance(v, int))


def _usage_from(message: AIMessage) -> Usage:
    """LangChain's ``usage_metadata``: ``input_tokens`` is the sum of all input token types, so it holds the
    ``input_token_details`` cache reads (``cache_read``) and writes (``cache_creation``)."""
    meta = getattr(message, "usage_metadata", None) or {}
    in_details = meta.get("input_token_details") or {}
    out_details = meta.get("output_token_details") or {}
    return usage_of(
        input=meta.get("input_tokens"),
        output=meta.get("output_tokens"),
        cache_read=_detail(in_details, "cache_read"),
        cache_write=_detail(in_details, "cache_creation"),
        reasoning=_detail(out_details, "reasoning"),
        total=meta.get("total_tokens"),
        input_includes_cache=True,
    )


def _message_text(message: BaseMessage) -> str:
    content = message.content
    if isinstance(content, str):
        return content
    chunks = []
    for block in content or []:
        if isinstance(block, str):
            chunks.append(block)
        elif isinstance(block, dict) and block.get("type") == "text":
            chunks.append(str(block.get("text", "")))
    return "".join(chunks)


def _reasoning_text(message: BaseMessage) -> str:
    """The model's reasoning in an AI message: Anthropic-style thinking blocks, or reasoning_content."""
    chunks = []
    content = message.content
    for block in content if isinstance(content, list) else []:
        if isinstance(block, dict) and block.get("type") in ("thinking", "reasoning"):
            chunks.append(str(block.get("thinking") or block.get("reasoning") or block.get("text") or ""))
    extra = getattr(message, "additional_kwargs", {}).get("reasoning_content")
    if isinstance(extra, str):
        chunks.append(extra)
    return "".join(chunks)


def _result_data(output: Any) -> Any:
    return plain_result(output.content if isinstance(output, ToolMessage) else output)


def _interrupts_graph(error: BaseException) -> bool:
    """Whether `error` is LangGraph pausing the graph at an interrupt (matched by name, so LangGraph is not imported;
    the adapter's import checks that ``langgraph.errors.GraphInterrupt`` exists when LangGraph is installed): the tool
    did not fail, it will run again when the graph resumes."""
    return any(c.__module__.startswith("langgraph.") and c.__name__ == "GraphInterrupt" for c in type(error).__mro__)


class SamLinkCallbackHandler(AsyncCallbackHandler):
    """Reports each model call and its response, and each tool's start and result, and refuses a call to a tool the
    user lacks the scopes for (what ``step_reporter`` returns; see the module docstring).

    Pass it in the run config: ``runnable.ainvoke(x, config={"callbacks": [step_reporter(ctx)]})``.
    The same function_call_id ties an llm_response's function_call to the tool's
    start and result steps, so the Activity Monitor nests tool steps under the
    LLM decision that asked for them.
    """

    raise_error = True
    """LangChain swallows a callback's exception otherwise, and the refusal must end the call. Reporting a step never
    raises (``_report``), so nothing else reaches the run.
    """

    def __init__(self, ctx: TaskContext):
        self.ctx = ctx
        try:
            self._loop: asyncio.AbstractEventLoop | None = asyncio.get_running_loop()
        except RuntimeError:
            self._loop = None
        self.scopes = ToolScopes(ctx)
        self._steps = Steps(ctx)
        self._models: dict[UUID, str] = {}
        self._pending = PendingCalls()
        self._tools: dict[UUID, tuple[str, str]] = {}
        self._wrapped: dict[UUID, str] = {}
        self._held: set[str] = set()

    async def _report(self, step: Awaitable[Any]) -> None:
        with may_be_lost(self.ctx):
            await step

    def _report_now(self, step: Awaitable[Any]) -> None:
        """Report a step from the synchronous callback, before the tool runs: LangChain calls it on a worker thread,
        which waits for the step on the task's loop; on the loop's own thread (a tool invoked synchronously there)
        the step is queued behind what the loop is doing."""
        try:
            running = asyncio.get_running_loop()
        except RuntimeError:
            running = None
        loop = self._loop
        if running is not None and (loop is None or running is loop):
            running.create_task(self._report(step))
        elif loop is not None and loop.is_running():
            try:
                asyncio.run_coroutine_threadsafe(self._report(step), loop).result(STEP_TIMEOUT_SECONDS)
            except TimeoutError:
                log.warning("step dropped task=%s err=TimeoutError", self.ctx.task_id)
        else:
            asyncio.run(self._report(step))

    def on_chat_model_start(self, serialized, messages, *, run_id: UUID, **kwargs: Any) -> None:  # type: ignore[override]
        """Report a model call's start.

        A plain method on purpose: LangChain runs it on a worker thread, before the tool runs, which an async override
        could not guarantee. The base class declares it async.
        """
        check_running(self.ctx)
        self._report_now(self._llm_start(serialized, run_id, kwargs))

    def on_llm_start(self, serialized, prompts, *, run_id: UUID, **kwargs: Any) -> None:  # type: ignore[override]
        check_running(self.ctx)
        self._report_now(self._llm_start(serialized, run_id, kwargs))

    async def _llm_start(self, serialized, run_id: UUID, kwargs: dict) -> None:
        model = _model_name(serialized, kwargs)
        self._models[run_id] = model
        await self._steps.model_call(model)

    async def on_llm_end(self, response: LLMResult, *, run_id: UUID, **kwargs: Any) -> None:
        await self._report(self._llm_end(response, run_id))

    async def _llm_end(self, response: LLMResult, run_id: UUID) -> None:
        model = self._models.pop(run_id, "unknown")
        generation = response.generations[0][0] if response.generations and response.generations[0] else None
        message = generation.message if isinstance(generation, ChatGeneration) else None
        if isinstance(message, AIMessage):
            text = _message_text(message)
            usage = _usage_from(message)
            calls = self._register_calls(message)
            check_usage_read("sam_link_sdk.langchain", model, message.usage_metadata, usage, text, calls)
        else:
            text = generation.text if generation is not None else ""
            usage = Usage()
            calls = []
        reasoning = _reasoning_text(message) if isinstance(message, AIMessage) else ""
        await self._steps.model_answer(model, text, calls, usage, reasoning)

    async def on_llm_error(self, error: BaseException, *, run_id: UUID, **kwargs: Any) -> None:
        self._models.pop(run_id, None)
        log.warning("llm call failed task=%s err=%s", self.ctx.task_id, type(error).__name__)
        self.ctx.record_llm_error(error)

    def _register_calls(self, message: AIMessage) -> list[dict]:
        """Give the model's tool calls ids and expect them, returning them as plain dicts.

        A missing id is written back to the message so the ToolMessage the agent builds carries the same id.
        """
        calls = []
        for tc in message.tool_calls:
            if not tc.get("id"):
                tc["id"] = new_call_id()
            calls.append({"id": tc["id"], "name": tc["name"], "args": tc.get("args") or {}})
        self._pending.expect(calls)
        return calls

    def on_tool_start(self, serialized, input_str: str, *, run_id: UUID, **kwargs: Any) -> None:  # type: ignore[override]
        """Check a tool call, refuse it when the user lacks the scopes, and report its start.

        Wrappers run the tool they wrap under the same name (an approval wrapper once approved, which may run the scoped
        tool, which runs the tool), so only the outermost wrapper's steps are this call's.
        """
        check_running(self.ctx)
        name = (serialized or {}).get("name") or kwargs.get("name") or "tool"
        call_id = self._pending.take(name, kwargs.get("tool_call_id") or "")
        args = kwargs.get("inputs")
        if not isinstance(args, dict):
            try:
                args = json.loads(input_str)
            except (TypeError, ValueError):
                args = {"input": input_str}
            if not isinstance(args, dict):
                args = {"input": args}
        if not self.scopes.permits(name):
            self._report_now(self._steps.refuse(name, args, call_id))
            raise ToolAccessDenied(name)
        parent_id = kwargs.get("parent_run_id")
        parent = self._tools[parent_id][0] if parent_id in self._tools else self._wrapped.get(parent_id)  # type: ignore[arg-type]
        if parent == name:
            self._wrapped[run_id] = name
            return
        self._tools[run_id] = (name, call_id)
        if call_id in self._held:
            self._held.discard(call_id)
            return
        self._report_now(self._steps.tool_start(name, args, call_id))

    async def on_tool_end(self, output: Any, *, run_id: UUID, **kwargs: Any) -> None:
        """Report a tool's result.

        A tool with `handle_tool_error` reports its failure as an error ToolMessage rather than raising, so that is
        reported as a failed call.
        """
        if self._unwrapped(run_id):
            return
        name, call_id = self._tool_for(run_id, kwargs)
        if isinstance(output, ToolMessage) and output.status == "error":
            await self._report(self._steps.tool_end(name, error_result(str(output.content)), call_id))
            return
        await self._report(self._steps.tool_end(name, _result_data(output), call_id))

    async def on_tool_error(self, error: BaseException, *, run_id: UUID, **kwargs: Any) -> None:
        """Report a tool's failure.

        langchain-core 1.8 also reports a tool cancelled with its task here; that tool did not fail, it never finished,
        so nothing is reported.
        """
        if self._unwrapped(run_id):
            return
        name, call_id = self._tool_for(run_id, kwargs)
        if _interrupts_graph(error):
            self._held.add(call_id)
            return
        if isinstance(error, asyncio.CancelledError):
            return
        await self._report(self._steps.tool_end(name, error_result(str(error)), call_id))

    def _unwrapped(self, run_id: UUID) -> bool:
        """Whether `run_id` is a wrapped tool's run (see on_tool_start), forgotten as it ends."""
        if run_id in self._wrapped:
            del self._wrapped[run_id]
            return True
        return False

    def _tool_for(self, run_id: UUID, kwargs: dict) -> tuple[str, str]:
        return self._tools.pop(run_id, (kwargs.get("name") or "tool", ""))


def step_reporter(ctx: TaskContext) -> SamLinkCallbackHandler:
    """The callback handler that reports one task's model and tool steps to the mesh and refuses a call to a tool the
    user lacks the scopes for: pass it in the run config, ``runnable.ainvoke(x, config={"callbacks":
    [step_reporter(ctx)]})`` (``run_agent`` does this)."""
    return SamLinkCallbackHandler(ctx)
