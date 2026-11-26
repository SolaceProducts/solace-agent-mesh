# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Report a run's model and tool steps to the mesh from the OpenTelemetry spans its framework already emits.

Many agent frameworks trace themselves with the OpenTelemetry GenAI semantic conventions: a ``chat`` span per model
call (``gen_ai.request.model``, ``gen_ai.usage.*``) and an ``execute_tool`` span per tool call (``gen_ai.tool.name``,
``gen_ai.tool.call.id``). The step reporter, a span processor, turns those spans into the steps and usage records the
dedicated adapters produce, for a framework that has no adapter. The names are the adapters' own:

    step_reporter()  # once, before the framework creates its tracer

    async def handle(ctx):
        return await run_agent(ctx, my_agent.run)  # my_agent.run(ctx.text), its spans reported to the task

`run_agent` binds the run to its mesh task; every span started under it (in the same context, or as a child of
one of its spans on any thread) is reported to that task, and spans of runs that are not bound are ignored. A model
call is announced when its span starts if the span already names the model (else when it ends), and answered when it
ends; a tool call is reported when its span ends, because frameworks add the arguments after the span starts.

Prompts, completions, tool arguments and results are reported only when the span carries them, and pass through the
same redaction and size caps as the dedicated adapters. They are never logged.

What it cannot do: spans are records of what happened, so it cannot ask the user before a tool runs, refuse a call to
a tool the user lacks the scopes for (the one place a declared tool scope is not enforced: ``run_agent`` logs a warning
when the agent declares any), or tell a tool its call id. Use the framework's own hook for those, or the thin helpers
(``ctx.request_approval``, ``ctx.check_tool``, ``ctx.call_agent``) inside your tools.

Attribute names (checked against opentelemetry-semantic-conventions 0.66b2, Strands Agents 1.61.1 in both its
default and ``gen_ai_latest_experimental`` modes, Microsoft Agent Framework 1.22.0 and Google ADK 2.14.1):

- operation: ``gen_ai.operation.name`` = ``chat`` / ``generate_content`` / ``text_completion`` (model call) or
  ``execute_tool``; a span without it is classified by its name (``chat ...``, ``execute_tool ...``).
- model: ``gen_ai.request.model``, else ``gen_ai.response.model``.
- usage: ``gen_ai.usage.input_tokens`` (older ``gen_ai.usage.prompt_tokens``), ``gen_ai.usage.output_tokens``
  (older ``gen_ai.usage.completion_tokens``), ``gen_ai.usage.cache_read.input_tokens`` (Strands' older
  ``gen_ai.usage.cache_read_input_tokens``), ``gen_ai.usage.cache_creation.input_tokens`` (Strands' older
  ``gen_ai.usage.cache_write_input_tokens``), ``gen_ai.usage.reasoning.output_tokens``, ``gen_ai.usage.total_tokens``.
- model output: ``gen_ai.output.messages`` as a span attribute (Agent Framework, Strands with span attributes only)
  or on a span event (Strands, latest conventions); Strands' default ``gen_ai.choice`` event (``message``: its
  content blocks); the flattened ``gen_ai.completion.<i>.*`` attributes of older instrumentations (not checked
  against an installed one).
- tool arguments: ``gen_ai.tool.call.arguments``; the ``tool_call`` part of ``gen_ai.input.messages``; Strands'
  default ``gen_ai.tool.message`` event (``content``); ADK's ``gcp.vertex.agent.tool_call_args``.
- tool result: ``gen_ai.tool.call.result``; the ``tool_call_response`` part of ``gen_ai.output.messages``; Strands'
  default ``gen_ai.choice`` event (``message``); ADK's ``gcp.vertex.agent.tool_response``. A tool span with status
  ERROR (or ``gen_ai.tool.status`` = ``error``) is a failed tool.
- failed model call: a model-call span with status ERROR. Its category for the task's error reply comes from
  ``error.type``, else the ``exception.type`` of the exception recorded on the span; its provider from
  ``gen_ai.provider.name`` (older ``gen_ai.system``).
"""

from __future__ import annotations

import asyncio
import contextvars
import inspect
import json
import logging
import threading
import weakref
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from typing import Any, TypeVar

from ._drift import require

TESTED_VERSION = "1.45.2"

require(
    "sam_link_sdk.otel_steps",
    "OpenTelemetry SDK",
    "opentelemetry-sdk",
    TESTED_VERSION,
    [
        "opentelemetry.sdk.trace:SpanProcessor.on_start",
        "opentelemetry.sdk.trace:SpanProcessor.on_end",
        "opentelemetry.sdk.trace:TracerProvider.add_span_processor",
        "opentelemetry.sdk.trace:Span",
        "opentelemetry.sdk.trace:ReadableSpan.name",
        "opentelemetry.sdk.trace:ReadableSpan.attributes",
        "opentelemetry.sdk.trace:ReadableSpan.status",
        "opentelemetry.sdk.trace:ReadableSpan.events",
        "opentelemetry.sdk.trace:ReadableSpan.parent",
        "opentelemetry.sdk.trace:ReadableSpan.get_span_context",
        "opentelemetry.trace:get_tracer_provider",
        "opentelemetry.trace:set_tracer_provider",
        "opentelemetry.trace:StatusCode.ERROR",
        "opentelemetry.context:Context",
    ],
)

from opentelemetry import trace  # noqa: E402
from opentelemetry.context import Context  # noqa: E402
from opentelemetry.sdk.trace import ReadableSpan, Span, SpanProcessor, TracerProvider  # noqa: E402
from opentelemetry.trace import StatusCode  # noqa: E402

from ._adapter import (  # noqa: E402
    Steps,
    check_running,
    check_usage_read,
    halts_bare,
    json_or_text,
    new_call_id,
    parse_args,
    reported_result,
    usage_of,
)
from .context import TaskContext, bind_task  # noqa: E402
from .llm_errors import UNKNOWN, classify_error_type  # noqa: E402
from .usage import Usage  # noqa: E402

log = logging.getLogger("sam_link_sdk.otel_steps")

T = TypeVar("T")

MODEL_CALL = "model_call"
TOOL_CALL = "tool_call"
_MODEL_OPERATIONS = frozenset({"chat", "generate_content", "text_completion"})
_TOOL_OPERATION = "execute_tool"
_NO_CONTENT = object()


@dataclass
class _Run:
    """One bound run: its mesh task, and the queue that hands span data to the task's event loop in order."""

    ctx: TaskContext
    loop: asyncio.AbstractEventLoop
    queue: asyncio.Queue = field(default_factory=asyncio.Queue)
    closed: bool = False
    announced: set[int] = field(default_factory=set)
    reported: set[int] = field(default_factory=set)
    late_spans: int = 0
    calls: int = 0
    calls_lock: threading.Lock = field(default_factory=threading.Lock)

    def __post_init__(self) -> None:
        self.steps = Steps(self.ctx)

    def begin_model_call(self) -> None:
        """Count a model call as its span starts, before the request goes out, and stop the task when it passes
        max_llm_calls. The pump hears of the call later (through the queue), and by then a fast model has taken
        more calls."""
        with self.calls_lock:
            self.calls += 1
            calls = self.calls
        if not self.ctx._run.passes_llm_call_limit(calls) or self.ctx._run.halt_reason:
            return
        try:
            on_loop = asyncio.get_running_loop() is self.loop
        except RuntimeError:
            on_loop = False
        if on_loop:
            self.ctx._run.stop_at_llm_call_limit()
            return

        async def stop() -> None:
            self.ctx._run.stop_at_llm_call_limit()

        try:
            asyncio.run_coroutine_threadsafe(stop(), self.loop).result(timeout=10)
        except Exception as exc:
            log.warning("model call limit not applied task=%s err=%s", self.ctx.task_id, type(exc).__name__)

    def submit(self, item: tuple) -> None:
        """From any thread: queue `item` for the pump on the task's loop."""
        if self.closed:
            self.late_spans += 1
            return
        try:
            self.loop.call_soon_threadsafe(self.queue.put_nowait, item)
        except RuntimeError:
            self.closed = True

    async def pump(self) -> None:
        """Report the queued spans to SAM until the run closes.

        A span that cannot be reported is dropped with a warning: a lost step must not stop the run's reporting.
        """
        while (item := await self.queue.get()) is not None:
            try:
                await self._handle(item)
            except Exception as exc:
                log.warning("span dropped task=%s kind=%s err=%s", self.ctx.task_id, item[0], type(exc).__name__)

    async def _handle(self, item: tuple) -> None:
        kind, span_id = item[0], item[1]
        if kind == "model_started":
            self.announced.add(span_id)
            await self.steps.model_call(item[2])
            return
        if span_id in self.reported:
            return
        self.reported.add(span_id)
        if kind == MODEL_CALL:
            await self._model_call(span_id, item[2])
        else:
            await self._tool_call(item[2])

    async def _model_call(self, span_id: int, span: ReadableSpan) -> None:
        """Report a model-call span as the step the dedicated adapters report.

        As in the dedicated adapters, a failed model call has an invocation and no response, and its category goes in
        the task's error reply.
        """
        attrs = span.attributes or {}
        model = _model(attrs) or "default"
        if span_id not in self.announced:
            await self.steps.model_call(model)
        self.announced.discard(span_id)
        if span.status.status_code == StatusCode.ERROR:
            self._record_failure(span)
            return
        text, calls, reasoning = _model_output(span)
        usage = _usage(attrs)
        raw = {k: v for k, v in attrs.items() if k.startswith("gen_ai.usage.")}
        check_usage_read("sam_link_sdk.otel_steps", model, raw, usage, text, calls)
        await self.steps.model_answer(model, text, calls, usage, reasoning)

    def _record_failure(self, span: ReadableSpan) -> None:
        """Only a recognised category is recorded: an unrecognised one would hide the category the agent finds in the
        exception the run raises."""
        error_type = _error_type(span)
        if not error_type:
            return
        provider = _provider(span.attributes or {})
        if classify_error_type(error_type, provider).category != UNKNOWN:
            self.ctx.record_llm_error(error_type, provider)

    async def _tool_call(self, span: ReadableSpan) -> None:
        attrs = span.attributes or {}
        name = str(attrs.get("gen_ai.tool.name") or _name_after(span.name, _TOOL_OPERATION) or "tool")
        call_id = str(attrs.get("gen_ai.tool.call.id") or "")
        if call_id in ("", "unknown"):
            call_id = new_call_id()
        args = _tool_arguments(span)
        result = _tool_result(span)
        failed = span.status.status_code == StatusCode.ERROR or str(attrs.get("gen_ai.tool.status") or "") == "error"
        if failed and result is _NO_CONTENT:
            result = span.status.description or "the tool failed"
        data = reported_result(None if result is _NO_CONTENT else result, failed)
        await self.steps.tool_start(name, args, call_id)
        await self.steps.tool_end(name, data, call_id)


_bound: contextvars.ContextVar[_Run | None] = contextvars.ContextVar("sam_link_otel_run", default=None)


@dataclass(frozen=True)
class _Tracked:
    run: _Run
    kind: str | None
    inside_model_call: bool


class SamLinkSpanProcessor(SpanProcessor):
    """Routes GenAI spans to the mesh task whose run started them (see the module docstring).

    Add it to the SDK TracerProvider the framework traces with (``step_reporter()`` does). It keeps only the routing
    of spans in flight, and hands everything else to the bound task's event loop, so it is safe on any thread.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._spans: dict[int, _Tracked] = {}
        _processors.add(self)

    def on_start(self, span: Span, parent_context: Context | None = None) -> None:
        span_context = span.get_span_context()
        if span_context is None:
            return
        parent_id = span.parent.span_id if span.parent is not None else None
        with self._lock:
            parent = self._spans.get(parent_id) if parent_id is not None else None
        run = parent.run if parent is not None else _bound.get()
        if run is None or run.closed:
            return
        attrs = span.attributes or {}
        kind = _kind(attrs, span.name)
        inside = parent is not None and (parent.kind == MODEL_CALL or parent.inside_model_call)
        with self._lock:
            self._spans[span_context.span_id] = _Tracked(run, kind, inside)
        if kind == MODEL_CALL and not inside:
            run.begin_model_call()
            model = _model(attrs)
            if model:
                run.submit(("model_started", span_context.span_id, model))

    def on_end(self, span: ReadableSpan) -> None:
        span_context = span.get_span_context()
        if span_context is None:
            return
        with self._lock:
            tracked = self._spans.pop(span_context.span_id, None)
        if tracked is None or tracked.kind is None or tracked.inside_model_call:
            return
        tracked.run.submit((tracked.kind, span_context.span_id, span))

    def forget(self, run: _Run) -> None:
        """Drop the routing of `run`'s spans that never ended."""
        with self._lock:
            for span_id in [k for k, t in self._spans.items() if t.run is run]:
                del self._spans[span_id]

    def shutdown(self) -> None:
        with self._lock:
            self._spans.clear()

    def force_flush(self, timeout_millis: int = 30000) -> bool:
        return True


_processors: weakref.WeakSet[SamLinkSpanProcessor] = weakref.WeakSet()
_installed: weakref.WeakKeyDictionary[TracerProvider, SamLinkSpanProcessor] = weakref.WeakKeyDictionary()
_install_lock = threading.Lock()


def step_reporter(tracer_provider: TracerProvider | None = None) -> SamLinkSpanProcessor:
    """The span processor that reports runs' steps to their mesh tasks, added to `tracer_provider` (default: the global
    one) once per provider and returned. It takes no task: ``run_agent`` tells it which task a run serves.

    When no SDK TracerProvider is set globally yet, one is created and set, so call this before the framework creates
    its tracer (some, like Strands, keep the tracer they first get). Exporters you add to the provider are unaffected.
    """
    with _install_lock:
        provider = tracer_provider or trace.get_tracer_provider()
        if not isinstance(provider, TracerProvider):
            provider = TracerProvider()
            trace.set_tracer_provider(provider)
            if trace.get_tracer_provider() is not provider:
                raise RuntimeError(
                    "the global OpenTelemetry tracer provider is not an SDK TracerProvider and cannot be "
                    "replaced; pass the provider your framework traces with to step_reporter()"
                )
        processor = _installed.get(provider)
        if processor is None:
            processor = SamLinkSpanProcessor()
            provider.add_span_processor(processor)
            _installed[provider] = processor
        return processor


@halts_bare
async def run_agent(ctx: TaskContext, run: Callable[[str], Awaitable[T] | T], *, input: str | None = None) -> T:
    """Run ``run(<the task's text, or input>)`` (awaited if it returns an awaitable) with its GenAI spans reported to
    `ctx`'s task; returns what `run` returns.

    Run blocking work through ``asyncio.to_thread`` inside `run`: it copies the context, so its spans are routed too.
    Spans that end after `run` returns are not reported. Spans only record what happened, so they cannot refuse a
    tool whose required scopes (AgentConfig.tools) the task's user lacks: this logs a warning when the agent
    declares tool scopes. A tool guarded by a framework adapter's ``with_scopes`` still refuses such a user itself
    (`run` runs with this task current); otherwise check the scopes in your tools with ``ctx.check_tool``.
    """
    check_running(ctx)
    _warn_unenforced_scopes(ctx)
    return await _reported(ctx, run, input if input is not None else ctx.text)


def _warn_unenforced_scopes(ctx: TaskContext) -> None:
    declared = sorted(ctx._run.tools.declared_scopes)
    if declared:
        log.warning(
            "tool scopes are not enforced by the OpenTelemetry adapter: guard the tools with a framework "
            "adapter's with_scopes, or check them with ctx.check_tool task=%s tools=%s",
            ctx.task_id,
            ",".join(declared),
        )


async def _reported(ctx: TaskContext, fn: Callable[[str], Awaitable[T] | T], text: str) -> T:
    """Run `fn` on the task's text and report the spans it produces.

    The reporting pump is stopped behind the spans other threads have already handed to this loop, so they are still
    reported.
    """
    run = _Run(ctx, asyncio.get_running_loop())
    pump = asyncio.create_task(run.pump(), name=f"sam-link-sdk-otel-{ctx.task_id}")
    token = _bound.set(run)
    try:
        with bind_task(ctx):
            result = fn(text)
            if inspect.isawaitable(result):
                result = await result
        return result
    finally:
        _bound.reset(token)
        run.closed = True
        run.loop.call_soon(run.queue.put_nowait, None)
        try:
            await pump
        finally:
            for processor in list(_processors):
                processor.forget(run)
            if run.late_spans:
                log.debug("spans ended after the run task=%s count=%d", ctx.task_id, run.late_spans)


def _name_after(span_name: str, operation: str) -> str:
    return span_name[len(operation) :].strip() if span_name.startswith(operation) else ""


def _kind(attrs: Mapping[str, Any], span_name: str) -> str | None:
    operation = str(attrs.get("gen_ai.operation.name") or "")
    if not operation:
        operation = span_name.split(" ", 1)[0]
    if operation in _MODEL_OPERATIONS:
        return MODEL_CALL
    if operation == _TOOL_OPERATION:
        return TOOL_CALL
    return None


def _model(attrs: Mapping[str, Any]) -> str:
    return str(attrs.get("gen_ai.request.model") or attrs.get("gen_ai.response.model") or "")


_PROVIDERS = {
    "aws.bedrock": "bedrock",
    "azure.ai.inference": "azure",
    "azure.ai.openai": "azure",
    "az.ai.inference": "azure",
    "az.ai.openai": "azure",
    "gcp.gemini": "gemini",
    "gcp.gen_ai": "gemini",
    "gcp.vertex_ai": "vertex",
    "mistral_ai": "mistral",
    "x_ai": "xai",
}
"""The GenAI conventions' provider names that differ from the names SAM-Go reports."""


def _provider(attrs: Mapping[str, Any]) -> str:
    name = str(attrs.get("gen_ai.provider.name") or attrs.get("gen_ai.system") or "").strip().lower()
    return _PROVIDERS.get(name, name)


def _error_type(span: ReadableSpan) -> str:
    """``error.type`` on the span, else the type of the exception recorded on it."""
    error_type = (span.attributes or {}).get("error.type")
    if error_type:
        return str(error_type)
    for event in span.events or ():
        attributes = event.attributes or {}
        if event.name == "exception" and attributes.get("exception.type"):
            return str(attributes["exception.type"])
    return ""


def _int(attrs: Mapping[str, Any], *names: str) -> int:
    for name in names:
        value = attrs.get(name)
        if value is not None:
            try:
                return int(value)
            except (TypeError, ValueError):
                continue
    return 0


def _usage(attrs: Mapping[str, Any]) -> Usage:
    """A span's usage attributes. Whether ``gen_ai.usage.input_tokens`` holds the cache reads and writes is not
    settled across instrumentations (Strands copies its model provider's ``inputTokens``, which leaves them out for
    Anthropic and Bedrock), so ``gen_ai.usage.total_tokens`` decides when a span has it (``usage_of``)."""
    return usage_of(
        input=_int(attrs, "gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens"),
        output=_int(attrs, "gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens"),
        cache_read=_int(attrs, "gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_read_input_tokens"),
        cache_write=_int(
            attrs,
            "gen_ai.usage.cache_creation.input_tokens",
            "gen_ai.usage.cache_write_input_tokens",
            "gen_ai.usage.cache_creation_input_tokens",
        ),
        reasoning=_int(attrs, "gen_ai.usage.reasoning.output_tokens"),
        total=_int(attrs, "gen_ai.usage.total_tokens"),
        input_includes_cache=None,
    )


def _json(value: Any) -> Any:
    """A span attribute's value as the dedicated adapters read a framework's value (the kit's ``json_or_text``: text
    holding a JSON object or array parsed, other text as it is), once the attribute's own encoding is undone: an
    instrumentation that records a text as JSON writes a JSON string literal, which is decoded first."""
    if not isinstance(value, str):
        return value
    stripped = value.strip()
    if stripped[:1] == '"':
        try:
            decoded = json.loads(stripped)
        except ValueError:
            return value
        return json_or_text(decoded) if isinstance(decoded, str) else decoded
    return json_or_text(value)


def _args_dict(value: Any) -> dict:
    if value in (None, "", "None"):
        return {}
    return parse_args(_json(value))


def _content(span: ReadableSpan, name: str) -> Any:
    """`name` as a span attribute, else on the first span event that carries it."""
    attrs = span.attributes or {}
    if name in attrs:
        return attrs[name]
    for event in span.events or ():
        event_attrs = event.attributes or {}
        if name in event_attrs:
            return event_attrs[name]
    return _NO_CONTENT


def _event_attribute(span: ReadableSpan, event_name: str, name: str) -> Any:
    for event in span.events or ():
        event_attrs = event.attributes or {}
        if event.name == event_name and name in event_attrs:
            return event_attrs[name]
    return _NO_CONTENT


def _new_call(call_id: Any, name: Any, args: Any) -> dict:
    return {"id": str(call_id or new_call_id()), "name": str(name or "tool"), "args": _args_dict(args)}


def _from_parts(messages: Any) -> tuple[str, list[dict], str] | None:
    """Text, tool calls and reasoning from GenAI ``gen_ai.output.messages`` (``[{role, parts: [...]}]``)."""
    if not isinstance(messages, list):
        return None
    text, calls, reasoning = [], [], []
    for message in messages:
        for part in (message.get("parts") or []) if isinstance(message, dict) else []:
            kind = part.get("type") if isinstance(part, dict) else None
            if kind == "text":
                text.append(str(part.get("content") or ""))
            elif kind in ("reasoning", "thinking"):
                reasoning.append(str(part.get("content") or ""))
            elif kind == "tool_call":
                calls.append(_new_call(part.get("id"), part.get("name"), part.get("arguments")))
    return "".join(text), calls, "\n".join(r for r in reasoning if r)


def _from_strands_blocks(blocks: Any) -> tuple[str, list[dict], str] | None:
    """Text, tool calls and reasoning from Strands' content blocks (its default ``gen_ai.choice`` event)."""
    if not isinstance(blocks, list) or not all(isinstance(b, dict) for b in blocks):
        return None
    text, calls, reasoning = [], [], []
    for block in blocks:
        if "text" in block:
            text.append(str(block["text"]))
        elif isinstance(block.get("toolUse"), dict):
            use = block["toolUse"]
            calls.append(_new_call(use.get("toolUseId"), use.get("name"), use.get("input")))
        elif isinstance(block.get("reasoningContent"), dict):
            reasoning.append(str((block["reasoningContent"].get("reasoningText") or {}).get("text") or ""))
    return "".join(text), calls, "\n".join(r for r in reasoning if r)


def _from_flattened_completion(attrs: Mapping[str, Any]) -> tuple[str, list[dict], str] | None:
    """Older instrumentations: ``gen_ai.completion.<i>.content`` and ``gen_ai.completion.<i>.tool_calls.<j>.*``."""
    if not any(k.startswith("gen_ai.completion.") for k in attrs):
        return None
    text, calls = [], []
    i = 0
    while f"gen_ai.completion.{i}.content" in attrs or f"gen_ai.completion.{i}.tool_calls.0.name" in attrs:
        if attrs.get(f"gen_ai.completion.{i}.content"):
            text.append(str(attrs[f"gen_ai.completion.{i}.content"]))
        j = 0
        while f"gen_ai.completion.{i}.tool_calls.{j}.name" in attrs:
            prefix = f"gen_ai.completion.{i}.tool_calls.{j}"
            calls.append(
                _new_call(attrs.get(f"{prefix}.id"), attrs.get(f"{prefix}.name"), attrs.get(f"{prefix}.arguments"))
            )
            j += 1
        i += 1
    return "".join(text), calls, ""


def _model_output(span: ReadableSpan) -> tuple[str, list[dict], str]:
    for found in (
        _from_parts(_json(_content(span, "gen_ai.output.messages"))),
        _from_strands_blocks(_json(_event_attribute(span, "gen_ai.choice", "message"))),
        _from_flattened_completion(span.attributes or {}),
    ):
        if found is not None:
            return found
    return "", [], ""


def _tool_arguments(span: ReadableSpan) -> dict | None:
    attrs = span.attributes or {}
    for name in ("gen_ai.tool.call.arguments", "gcp.vertex.agent.tool_call_args"):
        if name in attrs and attrs[name] not in ("N/A",):
            return _args_dict(attrs[name])
    messages = _json(_content(span, "gen_ai.input.messages"))
    for message in messages if isinstance(messages, list) else []:
        for part in (message.get("parts") or []) if isinstance(message, dict) else []:
            if isinstance(part, dict) and part.get("type") == "tool_call":
                return _args_dict(part.get("arguments"))
    legacy = _event_attribute(span, "gen_ai.tool.message", "content")
    return None if legacy is _NO_CONTENT else _args_dict(legacy)


def _unwrap_blocks(value: Any) -> Any:
    """A Strands tool result's content blocks as the dedicated adapter reports them; anything else as it is."""
    if (
        isinstance(value, list)
        and value
        and all(isinstance(b, dict) and len(b) == 1 and ("text" in b or "json" in b) for b in value)
    ):
        items = [_json(b["text"]) if "text" in b else b["json"] for b in value]
        return items[0] if len(items) == 1 else items
    return value


def _tool_result(span: ReadableSpan) -> Any:
    attrs = span.attributes or {}
    for name in ("gen_ai.tool.call.result", "gcp.vertex.agent.tool_response"):
        if name in attrs:
            return _unwrap_blocks(_json(attrs[name]))
    messages = _json(_content(span, "gen_ai.output.messages"))
    for message in messages if isinstance(messages, list) else []:
        for part in (message.get("parts") or []) if isinstance(message, dict) else []:
            if isinstance(part, dict) and part.get("type") == "tool_call_response":
                return _unwrap_blocks(_json(part.get("response")))
    legacy = _event_attribute(span, "gen_ai.choice", "message")
    return legacy if legacy is _NO_CONTENT else _unwrap_blocks(_json(legacy))
