# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""CrewAI event-bus listener that reports a crew's model and tool steps to the mesh task.

CrewAI has one process-wide event bus and runs synchronous handlers on a thread pool, so two events can be
handled out of order. The listener therefore only captures each event (in the handler thread, with the
crew's context) and hands it to the task's asyncio loop; a pump there releases events in CrewAI's
`emission_sequence` order before publishing. The threads that emit events wait at each model call and tool hook
for the handlers of the earlier events to finish, so no event is released behind a later one. Events reach the right
mesh task through a context variable set by `run_agent`: CrewAI copies the emitting thread's context into each
handler.

CrewAI tool events carry no tool-call id (CrewAI knows it while it runs the call but does not put it on the event).
Two keys are used instead:

- A tool's end or error event names its start event (``started_event_id``, set by CrewAI's event scopes, which follow
  each parallel call on its own thread), so a result always pairs with the start it belongs to.
- A start event is matched to a call in the model's last completion for the same crew agent: the call with the same
  tool name and arguments, else the first remaining call to that tool. Two calls that agree on both are
  indistinguishable, so either assignment is right. Each completion replaces the agent's calls that were never run
  (CrewAI may run only the first call of a batch), so a later call cannot take a stale id.

A call to a tool the task's user lacks the scopes for is refused by a before-tool-call hook (``crewai.hooks``; every
CrewAI tool-calling path runs it, and the tool does not run when it returns False). CrewAI then gives the model its own
"blocked by hook" text, which the after-tool-call hook replaces with SAM's refusal. CrewAI reports such a call with
tool events on one path and none on another, so the before hook emits its own event, ``ToolRefusedEvent``, which the
listener reports as the refused call, and the listener leaves out CrewAI's own events for a refused tool. CrewAI emits
its start event before the hook runs, so that start can be handled before the tool is known to be refused; the
refusal then finishes the start already reported rather than starting the call a second time.

The answer streams to the chat from CrewAI's ``LLMStreamChunkEvent``, the one event CrewAI hands its handlers on the
thread that emits it, so a chunk reaches the task's loop in the order the model wrote it, behind every event before
its model call. Only the model calls made for the crew's last task stream (``_AnswerText`` has the rule for which of
a call's text is the answer).
"""

from __future__ import annotations

import ast
import asyncio
import contextvars
import json
import logging
import threading
from collections import deque
from collections.abc import Iterator
from contextlib import contextmanager
from typing import Any, Literal

from crewai.events import BaseEventListener, crewai_event_bus
from crewai.events.types.llm_events import (
    LLMCallCompletedEvent,
    LLMCallFailedEvent,
    LLMCallStartedEvent,
    LLMCallType,
    LLMStreamChunkEvent,
)
from crewai.events.types.tool_usage_events import (
    ToolUsageErrorEvent,
    ToolUsageEvent,
    ToolUsageFinishedEvent,
    ToolUsageStartedEvent,
)
from crewai.hooks import (
    HookAborted,
    ToolCallHookContext,
    register_after_tool_call_hook,
    register_before_llm_call_hook,
    register_before_tool_call_hook,
)
from crewai.llms.base_llm import call_stream_override

from .._adapter import (
    Steps,
    ToolScopes,
    access_denied,
    check_running,
    check_usage_read,
    error_result,
    may_be_lost,
    new_call_id,
    note_halt,
    over,
    parse_args,
    plain_result,
    refused_result,
    usage_of,
)
from ..context import TaskContext
from ..usage import Usage
from .stop import _CHECKS

log = logging.getLogger("sam_link_sdk.crewai")

REORDER_WINDOW_SECONDS = 0.05
SYNC_TIMEOUT_SECONDS = 5.0
_SYNC = object()

_current: contextvars.ContextVar[_Run | None] = contextvars.ContextVar("sam_link_crewai_run", default=None)
_answer: contextvars.ContextVar[_AnswerText | None] = contextvars.ContextVar("sam_link_crewai_answer", default=None)


def _check_current_task() -> None:
    """Before a model call of a crew's LLM: count the call, and raise TaskHalted in the crew's worker thread once its
    mesh task was stopped or the call would pass max_llm_calls. The thread runs in a copy of the context run_agent
    set, so it sees its own task; a crew run outside run_agent sees none and is not checked."""
    run = _current.get()
    if run is not None:
        run.begin_model_call()
        check_running(run.ctx)


def _tool_calls(response: Any) -> list[dict]:
    """Native tool calls from an LLM completion: OpenAI-style, Anthropic tool_use blocks, or dicts."""
    calls = []
    for item in response if isinstance(response, list) else []:
        get = item.get if isinstance(item, dict) else (lambda k, _i=item: getattr(_i, k, None))
        fn = get("function")
        fn_get = fn.get if isinstance(fn, dict) else (lambda k, _f=fn: getattr(_f, k, None))
        name = get("name") or (fn_get("name") if fn is not None else None)
        if not name:
            continue
        args = get("input")
        if args is None and fn is not None:
            args = fn_get("arguments")
        calls.append({"id": get("id") or new_call_id(), "name": name, "args": parse_args(args)})
    return calls


def _first(u: dict, *keys: str) -> Any:
    return next((u[k] for k in keys if u.get(k) is not None), 0)


def _usage(raw: dict | None) -> Usage:
    """The usage a CrewAI LLM call event carries, whose keys depend on the provider. CrewAI's own Anthropic, OpenAI and
    Gemini providers and LiteLLM give input counts that hold the cache reads (``cached_prompt_tokens``) and writes
    (``cache_creation_tokens``): Anthropic's is summed by CrewAI, Gemini's is ``prompt_token_count``. Its Bedrock
    provider hands over Bedrock's own usage, whose ``inputTokens`` leaves them out (``cacheReadInputTokens``,
    ``cacheWriteInputTokens``)."""
    u = raw if isinstance(raw, dict) else {}
    if "inputTokens" in u:
        return usage_of(
            input=u.get("inputTokens"),
            output=u.get("outputTokens"),
            cache_read=_first(u, "cacheReadInputTokens", "cacheReadInputTokenCount"),
            cache_write=_first(u, "cacheWriteInputTokens", "cacheWriteInputTokenCount"),
            total=u.get("totalTokens"),
            input_includes_cache=None,
        )
    return usage_of(
        input=_first(u, "input_tokens", "prompt_tokens", "prompt_token_count"),
        output=_first(u, "output_tokens", "completion_tokens", "candidates_token_count"),
        cache_read=_first(u, "cached_prompt_tokens", "cached_input_tokens"),
        cache_write=_first(u, "cache_creation_tokens"),
        reasoning=_first(u, "reasoning_tokens"),
        total=_first(u, "total_tokens", "total_token_count"),
        input_includes_cache=True,
    )


def _same_args(a: Any, b: Any) -> bool:
    return json.dumps(a, sort_keys=True, default=str) == json.dumps(b, sort_keys=True, default=str)


class ToolRefusedEvent(ToolUsageEvent):
    """A call to a tool the task's user lacks the scopes for, refused before it ran (emitted by the adapter's hook)."""

    type: Literal["sam_link_tool_refused"] = "sam_link_tool_refused"


class _Text:
    """Text of the answer for the pump to stream, in its place among the events."""

    def __init__(self, text: str):
        self.text = text


class _Run:
    def __init__(
        self, ctx: TaskContext, loop: asyncio.AbstractEventLoop, *, stream_text: bool = False, answer_task: Any = None
    ):
        self.ctx = ctx
        self.stream_text = stream_text
        self.answer_task = answer_task
        self.scopes = ToolScopes(ctx)
        self.steps = Steps(ctx)
        self.loop = loop
        self._refused_lock = threading.Lock()
        self._refused: set[str] = set()
        self.queue: asyncio.Queue = asyncio.Queue()
        self.pending: dict[str, list[dict]] = {}
        self.started: dict[str, tuple[str, str]] = {}
        self.unmatched: dict[str, deque[str]] = {}
        self._calls_lock = threading.Lock()
        self._calls = 0

    def begin_model_call(self) -> None:
        """Count a model call on the crew's thread before it goes out, and stop the task when it passes
        max_llm_calls. CrewAI's event for the call reaches the task's loop late (through a handler thread and the
        reorder window), and by then a fast model has taken several more calls."""
        self.sync_point()
        with self._calls_lock:
            self._calls += 1
            calls = self._calls
        if not self.ctx._run.passes_llm_call_limit(calls) or self.ctx._run.halt_reason:
            return

        async def stop() -> None:
            self.ctx._run.stop_at_llm_call_limit()

        try:
            asyncio.run_coroutine_threadsafe(stop(), self.loop).result(timeout=10)
        except Exception as exc:
            log.warning("model call limit not applied task=%s err=%s", self.ctx.task_id, type(exc).__name__)

    def refuses(self, *names: Any) -> bool:
        """Whether a call by any of `names` (CrewAI's sanitized name, the tool's own) is refused; called on the crew's
        threads. A refused name is remembered, so the listener can leave out CrewAI's events for it. A check that
        fails refuses: CrewAI would run the tool if the hook raised."""
        candidates = {str(n) for n in names if n}
        try:
            known = {n for n in candidates if self.ctx._run.tools.knows(n)}
            if all(self.scopes.permits(n) for n in known or candidates):
                return False
        except Exception as exc:
            log.warning(
                "refusing a tool call the scope check could not decide task=%s err=%s",
                self.ctx.task_id,
                type(exc).__name__,
            )
        with self._refused_lock:
            self._refused.update(candidates)
        return True

    def refused(self, name: str) -> bool:
        """Whether the hook refused calls by `name` in this run (CrewAI's own events for them are left out; a call to
        a denied tool that somehow ran without the hook is still reported as it ran)."""
        with self._refused_lock:
            return name in self._refused

    def capture(self, event: Any) -> None:
        """Called on a CrewAI handler thread."""
        seq = event.emission_sequence if event.emission_sequence is not None else 0
        self.loop.call_soon_threadsafe(self.queue.put_nowait, (seq, event))

    def sync_point(self) -> None:
        """Called on the thread that emits CrewAI's events, just before it emits the next one (a model call, a tool's
        end): wait until the handlers of every event emitted so far have captured theirs, then mark the queue. The
        pump releases what came before the mark, sorted, before anything after it, so a handler thread that
        delivers late cannot put an event behind one CrewAI emitted after it. The wait is bounded; past it the
        reorder window alone orders the events."""
        try:
            crewai_event_bus.flush(SYNC_TIMEOUT_SECONDS)
            self.loop.call_soon_threadsafe(self.queue.put_nowait, _SYNC)
        except Exception as exc:
            log.warning("step order not synchronized task=%s err=%s", self.ctx.task_id, type(exc).__name__)

    def answers(self, task: Any) -> bool:
        """Whether a model call made for `task` may write the answer, and so streams: one for the crew's last task."""
        return self.stream_text and task is not None and task is self.answer_task

    def emit_text(self, text: str) -> None:
        """Called on the crew's thread, in the order the model wrote the text."""
        self.loop.call_soon_threadsafe(self.queue.put_nowait, _Text(text))

    async def pump(self) -> None:
        """Report CrewAI's events to SAM in order, in batches.

        CrewAI reports from a thread-pool event bus, so events can arrive out of order. Those that arrive within a short
        window are sorted by their sequence number before they are reported. Text closes a batch as a sync mark does,
        because every event before its model call was captured before it.
        """
        done = False
        while not done:
            batch = [await self.queue.get()]
            while isinstance(batch[-1], tuple):
                try:
                    batch.append(await asyncio.wait_for(self.queue.get(), REORDER_WINDOW_SECONDS))
                except TimeoutError:
                    break
            done = batch[-1] is None
            for _, event in sorted((b for b in batch if isinstance(b, tuple)), key=lambda b: b[0]):
                with may_be_lost(self.ctx, f"step {type(event).__name__}"):
                    await self.handle(event)
            if isinstance(batch[-1], _Text):
                with may_be_lost(self.ctx, "text"):
                    await self.ctx.emit_text(batch[-1].text)

    def _take_call(self, event: Any, args: dict) -> str:
        """The model's call this tool start runs: same tool and arguments, else the first remaining call to it."""
        pending = self.pending.get(event.agent_id or "", [])
        same_tool = [c for c in pending if c["name"] == event.tool_name]
        match = next((c for c in same_tool if _same_args(c["args"], args)), same_tool[0] if same_tool else None)
        if match is None:
            return new_call_id()
        pending.remove(match)
        return match["id"]

    def _end_call(self, event: Any) -> tuple[str, str]:
        """The call id and the tool name the call's start was reported with: CrewAI's text (ReAct) tool calling starts a
        tool under the name the model wrote (peer_records-lookup) and finishes it under its sanitized one
        (peer_records_lookup)."""
        start_id = getattr(event, "started_event_id", None)
        if start_id is None:
            waiting = self.unmatched.get(event.tool_name)
            start_id = waiting[0] if waiting else None
        call_id, name = self.started.pop(start_id, (None, event.tool_name)) if start_id else (None, event.tool_name)
        waiting = self.unmatched.get(name)
        if waiting is not None and start_id is not None and start_id in waiting:
            waiting.remove(start_id)
        return call_id or new_call_id(), name

    async def handle(self, event: Any) -> None:
        """Report one CrewAI event as the matching step.

        CrewAI's own start for a call can be handled before the hook marks the tool refused, so the call is already
        reported as started; a refusal then finishes that start instead of starting a second.
        """
        if isinstance(event, ToolRefusedEvent):
            args = parse_args(event.tool_args)
            open_starts = self.unmatched.get(event.tool_name)
            if open_starts:
                call_id, _ = self.started.pop(open_starts.popleft())
                await self.steps.tool_end(event.tool_name, refused_result(event.tool_name), call_id)
            else:
                await self.steps.refuse(event.tool_name, args, self._take_call(event, args))
        elif isinstance(event, ToolUsageEvent) and self.refused(event.tool_name):
            return
        elif isinstance(event, LLMCallStartedEvent):
            await self.steps.model_call(event.model or "default")
        elif isinstance(event, LLMCallCompletedEvent):
            calls = _tool_calls(event.response) if event.call_type == LLMCallType.TOOL_CALL else []
            self.pending[event.agent_id or ""] = list(calls)
            text = event.response if isinstance(event.response, str) else ""
            model, usage = event.model or "default", _usage(event.usage)
            check_usage_read("sam_link_sdk.crewai", model, event.usage, usage, text, calls)
            await self.steps.model_answer(model, text, calls, usage)
        elif isinstance(event, LLMCallFailedEvent):
            log.warning("llm call failed task=%s", self.ctx.task_id)
        elif isinstance(event, ToolUsageStartedEvent):
            args = parse_args(event.tool_args)
            call_id = self._take_call(event, args)
            self.started[event.event_id] = (call_id, event.tool_name)
            self.unmatched.setdefault(event.tool_name, deque()).append(event.event_id)
            await self.steps.tool_start(event.tool_name, args, call_id)
        elif isinstance(event, (ToolUsageFinishedEvent, ToolUsageErrorEvent)):
            call_id, name = self._end_call(event)
            await self.steps.tool_end(name, _result_of(event), call_id)


_FINAL_ANSWER = "Final Answer:"
_REACT_STARTS = ("Thought:", "Action:", _FINAL_ANSWER)


class _AnswerText:
    """Which of one model call's text is the answer, decided chunk by chunk on the crew's thread as the model writes it.

    CrewAI does not say while a call streams whether it will be the final answer: that is known only when the response
    ends without a tool call. So a call's text streams from the point it is known not to be a tool call:

    - A call offered tools (native tool calling) answers in text or calls a tool. Its text streams as written, and
      stops at the first tool-call chunk. A model that writes a sentence before a tool call in the same response has
      that sentence streamed: nothing tells the two apart until the response ends.
    - A call offered no tools is parsed by CrewAI (``crewai.agents.parser``): a response in its ReAct form
      (``Thought:``, ``Action:``, ``Final Answer:``) is a tool call or a final answer, whose answer is the text after
      ``Final Answer:``, stripped; any other response is the answer as written. The text is held while its start could
      still be one of those words; then it streams whole, or from after ``Final Answer:`` with the whitespace around it
      left out, or not at all.
    """

    def __init__(self, run: _Run, parsed: bool):
        self.run = run
        self.mode = "undecided" if parsed else "answer"
        self.held = ""
        self.ended = False
        self.final_started = False

    def chunk(self, event: LLMStreamChunkEvent) -> None:
        if self.ended or event.tool_call is not None or event.call_type == LLMCallType.TOOL_CALL:
            self.ended = True
            return
        if not event.chunk:
            return
        if self.mode == "answer":
            self.run.emit_text(event.chunk)
            return
        self.held += event.chunk
        if self.mode == "undecided":
            head = self.held.lstrip()
            if any(start.startswith(head) for start in _REACT_STARTS):
                return
            if not head.startswith(_REACT_STARTS):
                self.mode, text, self.held = "answer", self.held, ""
                self.run.emit_text(text)
                return
            self.mode = "react"
        if self.mode == "react":
            at = self.held.find(_FINAL_ANSWER)
            if at < 0:
                return
            self.mode, self.held = "final", self.held[at + len(_FINAL_ANSWER) :]
        if not self.final_started:
            self.held = self.held.lstrip()
        body = self.held.rstrip()
        if body:
            self.final_started = True
            self.run.emit_text(body)
            self.held = self.held[len(body) :]


@contextmanager
def answer_call(llm: Any, kwargs: dict) -> Iterator[None]:
    """Around one model call of a crew's LLM: when its text may be the task's answer, stream the call (for this call
    only: the LLM may serve other tasks at the same time) and route its chunks to the answer."""
    run = _current.get()
    if run is None or not run.answers(kwargs.get("from_task")):
        yield
        return
    token = _answer.set(_AnswerText(run, parsed=not kwargs.get("tools")))
    try:
        with call_stream_override(llm, True):
            yield
    finally:
        _answer.reset(token)


def _result_of(event: Any) -> Any:
    """What the agent was given, as the tool_result data; a tool that raised, or declared a failure (CrewAI's
    ToolFailure), is {"error": ...}."""
    if isinstance(event, ToolUsageErrorEvent):
        return error_result(str(event.error))
    if getattr(event, "failure", None) is not None:
        return error_result(event.output if isinstance(event.output, str) else _output(event.output))
    return _output(event.output)


_MAX_LITERAL_CHARS = 1 << 20
"""Past this, a result is reported as the text CrewAI gave the agent: reading it back is for the common case of a tool
returning a small dict, not for megabytes of text that happens to start with a brace.
"""


def _output(value: Any) -> Any:
    """CrewAI hands a tool's result to its event as ``str(result)``, so a dict arrives as Python's text of it
    (``{'error': 'no policy P-404'}``), which is not JSON. Read such text back into the data the tool returned, so
    the result is reported as data and ``{"error": ...}`` counts as a failed call as it does for every other adapter.
    Only literals are read (``ast.literal_eval`` runs no code), and only ones that are JSON data."""
    if isinstance(value, str):
        text = value.strip()
        if text[:1] in ("{", "[") and len(text) <= _MAX_LITERAL_CHARS:
            try:
                parsed = ast.literal_eval(text)
                json.dumps(parsed)
            except (ValueError, TypeError, SyntaxError, MemoryError, RecursionError):
                pass
            else:
                if isinstance(parsed, (dict, list)):
                    return parsed
    return plain_result(value)


class _Listener(BaseEventListener):
    """Registered once on CrewAI's global bus (run_agent does it); routes events to the running mesh task."""

    def setup_listeners(self, bus) -> None:
        def capture(source, event):
            run = _current.get()
            if run is not None:
                run.capture(event)

        def stream(source, event):
            answer = _answer.get()
            if answer is not None:
                answer.chunk(event)

        bus.on(LLMStreamChunkEvent)(stream)
        for event_type in (
            LLMCallStartedEvent,
            LLMCallCompletedEvent,
            LLMCallFailedEvent,
            ToolUsageStartedEvent,
            ToolUsageFinishedEvent,
            ToolUsageErrorEvent,
            ToolRefusedEvent,
        ):
            bus.on(event_type)(capture)


def _refuse_denied_tool(context: ToolCallHookContext) -> bool | None:
    """Before every tool call in the process: False (do not run it) for a mesh task's call to a tool its user lacks
    the scopes for. CrewAI runs a hook on the crew's thread, in the context run_agent set.

    The hook returns False rather than raising: CrewAI would run the tool if the hook raised.
    """
    run = _current.get()
    if run is not None:
        run.sync_point()
    if run is not None and over(run.ctx, context.tool_name):
        return False
    if run is None or not run.refuses(context.tool_name, getattr(context.tool, "name", None)):
        return None
    try:
        crewai_event_bus.emit(
            context.agent,
            ToolRefusedEvent(
                tool_name=context.tool_name, tool_args=dict(context.tool_input or {}), from_agent=context.agent
            ),
        )
    except Exception as exc:
        log.warning("refusal not reported task=%s err=%s", run.ctx.task_id, type(exc).__name__)
    return False


def _refusal_text(context: ToolCallHookContext) -> str | None:
    """After every tool call: SAM's refusal, instead of CrewAI's "blocked by hook", for a call the hook refused."""
    run = _current.get()
    if run is not None:
        run.sync_point()
    if run is None or not run.refused(context.tool_name):
        return None
    return access_denied(context.tool_name)


def _stop_if_halted(context: Any) -> None:
    """Before every model call in the process: end the kickoff of a mesh task that was stopped, whichever LLM makes
    the call (one run_agent could not wrap: a crew a tool kicks off, an LLM made at run time). CrewAI lets only its own
    HookAborted out of a hook; the crew's wrapped LLMs raise TaskHalted first where they can."""
    run = _current.get()
    if run is not None:
        run.sync_point()
    if run is None or not run.ctx._run.halt_reason:
        return
    note_halt()
    if _check_current_task in (getattr(getattr(context, "llm", None), _CHECKS, None) or ()):
        return
    raise HookAborted(reason=f"task {run.ctx.task_id} was stopped")


_listener: _Listener | None = None
_listener_lock = threading.Lock()


def _ensure_listener() -> None:
    """Register the listener and the tool-call hooks with CrewAI, once per process."""
    global _listener
    with _listener_lock:
        if _listener is None:
            _listener = _Listener()
            register_before_tool_call_hook(_refuse_denied_tool)
            register_after_tool_call_hook(_refusal_text)
            register_before_llm_call_hook(_stop_if_halted)
