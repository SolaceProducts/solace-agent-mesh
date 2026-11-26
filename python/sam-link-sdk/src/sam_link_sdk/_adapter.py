# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""What the framework adapters share, so each adapter holds only what is particular to its framework.

- Step events (``sam_link_sdk.events``) built the same way in every adapter: arguments and results redacted and capped,
  a result of only ``{"error": ...}`` a failed tool. ``Steps`` reports one run's model
  and tool steps with them.
- Call ids: making one up when the framework has none, and pairing a tool start with the call the model made.
- Reading what frameworks hand over: arguments as JSON text or a dict, results as JSON text or plain values.
- Token usage: a framework's report as ``Usage`` (``usage_of``), cache tokens counted once whatever the framework's
  convention.
- Per-tool scopes: one run's view of which tools its user may use (``ToolScopes``), and the one way a call to a tool
  the user lacks the scopes for is refused: the text the model sees, the step reported and the audit record.
- The approval and ask-user flows, and the texts the model sees for a refused, denied or unanswered call.
- Peer cards a task may delegate to, and card tool manifests from named tools.
- What each adapter's run repeats: the approval decision for a call the framework holds (``decide_approval``), the guard
  that lets a lost step be logged and not end the run (``may_be_lost``), the body of a peer tool (``delegate``), the
  scoped tool class (``scoped_class``), and the task's text and request prompt.

Private to the adapters: users never import it. It depends on the core only, never on a transport.
"""

from __future__ import annotations

import asyncio
import contextvars
import functools
import json
import logging
import threading
import uuid
from collections import deque
from collections.abc import Awaitable, Callable, Iterable, Iterator, Mapping
from contextlib import contextmanager
from typing import Any, TypeVar

from ._run import HALT_ANSWERED
from .captext import cap_nested
from .config import ToolInfo
from .context import ASK_USER_TOOL as ASK_USER_TOOL, TaskContext, TaskHalted, current_task, halt_of_task, tool_name
from .delegation import AgentCard
from .events import ModelFinished, ModelStarted, ToolCall, ToolFinished, ToolStarted
from .prompting import ApprovalResult, Option, PromptTooLarge, Question, UserAnswer
from .redact import redact_value
from .usage import Usage

T = TypeVar("T")


def _str(value: Any) -> str:
    return value if isinstance(value, str) else ""


def model_started(model: str) -> ModelStarted:
    return ModelStarted(_str(model))


def model_finished(model: str, text: str, calls: Iterable[Mapping[str, Any]], usage: Usage) -> ModelFinished:
    """`calls` are ``{"id", "name", "args"}``; the arguments are redacted, as the model's request is shown to users.
    Emitting it records `usage` for the task."""
    tool_calls = []
    for call in calls:
        args = redact_value(call.get("args") or {})
        tool_calls.append(
            ToolCall(_str(call.get("id")), _str(call.get("name")), args if isinstance(args, Mapping) else {})
        )
    return ModelFinished(model=_str(model), text=_str(text), tool_calls=tuple(tool_calls), usage=usage)


def tool_started(name: str, args: Mapping[str, Any] | None, call_id: str) -> ToolStarted:
    shown = cap_nested(redact_value(dict(args))) if args is not None else None
    return ToolStarted(call_id=_str(call_id), name=_str(name), args=dict(shown) if isinstance(shown, Mapping) else None)


def tool_finished(name: str, result: Any, call_id: str) -> ToolFinished:
    """`result` is what the tool gave the model; ``{"error": <text>}`` and nothing else is a failed call (see
    ``reported_result``)."""
    data = cap_nested(redact_value(result))
    return ToolFinished(
        call_id=_str(call_id), name=_str(name), result=data, failed=isinstance(data, Mapping) and set(data) == {"error"}
    )


def error_result(text: Any) -> dict:
    """A failed tool's result: what the model was told, as `{"error": ...}`."""
    return {"error": text}


def reported_result(result: Any, failed: bool) -> Any:
    """The one rule every adapter reports a tool's outcome by: a call failed when the framework says so (the tool
    raised, or the framework marked the result failed) or when the result is ``{"error": ...}`` and nothing else.
    A failed call is reported as ``{"error": <what the model was given>}``; any other result, one that merely has an
    ``error`` key among others included, is a success."""
    if failed and not (isinstance(result, Mapping) and set(result) == {"error"}):
        return error_result(result)
    return result


class Steps:
    """Reports one run's model and tool steps to its task. (SAM groups the tool calls of one model response from
    its ``model_finished``.)"""

    def __init__(self, ctx: TaskContext):
        self.ctx = ctx

    async def model_call(self, model: str) -> None:
        await self.ctx.emit_event(model_started(model))

    async def model_answer(self, model: str, text: str, calls: list[dict], usage: Usage, reasoning: str = "") -> None:
        """A model response: its reasoning (to the chat, when the agent streams it), then the response and its
        usage."""
        if reasoning:
            await self.ctx.emit_thinking(reasoning)
            await self.ctx.end_thinking()
        shown = [{**call, "name": self._shown(call.get("name"))} for call in calls]
        await self.ctx.emit_event(model_finished(model, text, shown, usage))

    async def tool_start(self, name: str, args: Mapping[str, Any] | None, call_id: str) -> None:
        await self.ctx.emit_event(tool_started(self._shown(name), args, call_id))

    async def tool_end(self, name: str, result: Any, call_id: str) -> None:
        await self.ctx.emit_event(tool_finished(self._shown(name), result, call_id))

    def _shown(self, name: Any) -> Any:
        return self.ctx._run.tools.shown_name(name) if isinstance(name, str) else name

    async def refuse(self, name: str, args: Mapping[str, Any] | None, call_id: str) -> dict:
        """Report a call refused because the user lacks the tool's scopes (``refusal_steps``); returns the result the
        model is given."""
        for event in refusal_steps(self._shown(name), args, call_id):
            await self.ctx.emit_event(event)
        return refused_result(name)


def token_count(value: Any) -> int:
    """A token count a framework reported: a non-negative int, 0 for anything else."""
    try:
        return max(int(value or 0), 0)
    except (TypeError, ValueError):
        return 0


def usage_of(
    *,
    input: Any,
    output: Any,
    cache_read: Any = 0,
    cache_write: Any = 0,
    reasoning: Any = 0,
    total: Any = 0,
    input_includes_cache: bool | None = True,
) -> Usage:
    """A framework's token report as ``Usage``, whose ``input_tokens`` hold the cache reads and writes.

    `input_includes_cache` says whether the framework's input count already holds them: True (OpenAI's
    ``prompt_tokens``, LangChain's ``input_tokens``), False (Anthropic's ``input_tokens``, Bedrock's ``inputTokens``),
    or None where it depends on the model provider behind the framework. Then the report decides: a `total` that counts
    the cache tokens on top of input and output, or cache tokens outnumbering the input, show they were left out;
    otherwise they are taken as included, so a token is never counted twice (at worst cache tokens are under-counted
    as input, never over-counted)."""
    tokens_in, tokens_out = token_count(input), token_count(output)
    read, write, reported_total = token_count(cache_read), token_count(cache_write), token_count(total)
    cache = read + write
    if input_includes_cache is None and cache:
        input_includes_cache = not (reported_total >= tokens_in + tokens_out + cache or cache > tokens_in)
    if input_includes_cache is False:
        tokens_in += cache
    return Usage(
        input_tokens=tokens_in,
        output_tokens=tokens_out,
        cached_input_tokens=read,
        cache_write_tokens=write,
        reasoning_tokens=token_count(reasoning),
        total_tokens=reported_total,
    )


USAGE_FIELDS_SHOWN = 32
USAGE_FIELD_NAME_CHARS = 64
_USAGE_DEPTH = 4
_unread_usage_warned: set[str] = set()
_unread_usage_lock = threading.Lock()


def _usage_leaves(raw: Any, prefix: str = "", depth: int = 0) -> Iterable[tuple[str, Any]]:
    """Each field of a framework's usage report as (dotted name, value), through dicts, lists, pydantic models,
    dataclasses and plain objects."""
    if depth > _USAGE_DEPTH or raw is None:
        return
    if not isinstance(raw, Mapping):
        dump = getattr(raw, "model_dump", None)
        if callable(dump):
            try:
                raw = dump()
            except Exception:
                return
        elif isinstance(raw, (list, tuple)):
            for item in raw:
                yield from _usage_leaves(item, prefix + "[]", depth + 1)
            return
        elif hasattr(raw, "__dict__") and not isinstance(raw, type):
            raw = {k: v for k, v in vars(raw).items() if not k.startswith("_")}
        else:
            yield prefix, raw
            return
    for key, value in raw.items():
        name = f"{prefix}.{key}" if prefix else str(key)
        if isinstance(value, (Mapping, list, tuple)) or hasattr(value, "model_dump") or hasattr(value, "__dict__"):
            yield from _usage_leaves(value, name, depth + 1)
        else:
            yield name, value


def _token_count_field(name: str, value: Any) -> bool:
    """Whether a usage field is a positive token count.

    Only a field named for tokens counts: Hermes' `request_count` and the OpenAI Agents SDK's `requests` are 1 on every
    call, whether or not the provider reported any usage.
    """
    return "token" in name.lower() and isinstance(value, (int, float)) and not isinstance(value, bool) and value > 0


def check_usage_read(adapter: str, model: str, raw: Any, usage: Usage, text: str, calls: list) -> None:
    """Warn, once per adapter and process, when a model response that produced output (text or tool calls) was read
    as having no input or no output tokens although the usage the framework handed over (`raw`) holds a token count
    (a field named for tokens, above zero): a framework or provider release renamed a usage field, and SAM's usage
    and cost reports would go on short of it with nothing failing. The warning names the usage's fields, never their values. A response whose usage holds no
    count at all (a local or scripted model, a stream that carried no usage) is not warned about: no count was lost.
    Nothing is made up: the usage stays as read."""
    if not (text.strip() or calls):
        return
    tokens = usage._buckets()
    if tokens.input and tokens.output:
        return
    fields = list(_usage_leaves(raw))
    counted = any(_token_count_field(name, value) for name, value in fields)
    if not counted:
        return
    with _unread_usage_lock:
        if adapter in _unread_usage_warned:
            return
        _unread_usage_warned.add(adapter)
    missing = " and ".join(kind for kind, count in (("input", tokens.input), ("output", tokens.output)) if not count)
    names = sorted({name[:USAGE_FIELD_NAME_CHARS] for name, _ in fields})
    shown = ", ".join(names[:USAGE_FIELDS_SHOWN]) + (" ..." if len(names) > USAGE_FIELDS_SHOWN else "")
    log.warning(
        "token usage unread: %s read no %s tokens from a model response that produced output, though its usage holds "
        "token counts, so SAM records the call without them; a framework or provider release may have renamed a "
        "usage field (logged once per process) model=%s usage_fields=%s",
        adapter,
        missing,
        model,
        shown,
    )


def new_call_id() -> str:
    """An id for a call the framework gave none, in the form OpenAI's ids take."""
    return "call_" + uuid.uuid4().hex[:24]


class PendingCalls:
    """The call ids a model response asked for, by tool name, until the framework starts each call. A framework that
    tells the tool start its call id has that id taken; one that does not gets the oldest waiting id for the tool."""

    def __init__(self) -> None:
        self._by_tool: dict[str, deque[str]] = {}

    def expect(self, calls: Iterable[Mapping[str, Any]]) -> None:
        for call in calls:
            self._by_tool.setdefault(call["name"], deque()).append(call["id"])

    def take(self, name: str, call_id: str = "") -> str:
        pending = self._by_tool.get(name)
        if call_id and pending and call_id in pending:
            pending.remove(call_id)
        elif not call_id:
            call_id = pending.popleft() if pending else new_call_id()
        return call_id


def parse_args(raw: Any) -> dict:
    """A tool call's arguments, given as a dict or JSON text; anything else is ``{"input": <it>}``."""
    if isinstance(raw, dict):
        return dict(raw)
    try:
        parsed = json.loads(raw) if raw else {}
    except (TypeError, ValueError):
        return {"input": raw}
    return parsed if isinstance(parsed, dict) else {"input": parsed}


def json_or_text(text: str) -> Any:
    """Text that holds a JSON object or array, parsed; any other text as it is."""
    stripped = text.strip()
    if stripped[:1] in ("{", "["):
        try:
            return json.loads(stripped)
        except ValueError:
            pass
    return text


def plain_result(value: Any) -> Any:
    """A tool's result as JSON data: text through ``json_or_text``, JSON values as they are, anything else as text."""
    if isinstance(value, str):
        return json_or_text(value)
    if isinstance(value, (dict, list, int, float, bool)) or value is None:
        return value
    return str(value)


def name_filter(names: Iterable[str] | None) -> Callable[[str], bool]:
    """Whether a name is among `names`; every name is when `names` is None. `names` must be names: a string (whose
    characters would be the names) or anything but strings in it is a TypeError, so a misused argument cannot
    silently choose nothing."""
    if names is None:
        return lambda name: True
    if isinstance(names, (str, bytes)) or not isinstance(names, Iterable):
        raise TypeError(f"names must be a list of tool names, not {type(names).__name__}")
    names = list(names)
    strays = sorted({type(n).__name__ for n in names if not isinstance(n, str)})
    if strays:
        raise TypeError(f"names must be tool names (strings), not {', '.join(strays)}")
    wanted = frozenset(names)
    return lambda name: name in wanted


def approval_filter(tools: Iterable[Any], names: Iterable[str] | None, hint: str = "") -> Callable[[str], bool]:
    """Which tools an approval covers: those of `tools` in `names` (every one of `tools` when None). A name that is
    none of `tools` is a ValueError, as in ``with_scopes``: a misspelt name would leave the tool it meant unguarded."""
    tools = list(tools)
    if names is not None and not isinstance(names, (str, bytes)):
        names = list(names)
    wanted = name_filter(names)
    covered = {tool_name(t) for t in tools}
    if names is not None:
        check_names_known(names, covered, "with_approval names", hint)
    return lambda name: name in covered and wanted(name)


def check_names_known(names: Iterable[str], known: Iterable[str], what: str, hint: str = "") -> None:
    """A ValueError naming each of `names` that is none of `known` (with `hint`, why it may not be)."""
    unknown = sorted(set(names) - set(known))
    if unknown:
        raise ValueError(f"{what} tools that are not among the tools given: {', '.join(unknown)}{hint}")


def check_kind(tools: Iterable[Any], names: Iterable[str], kind: type | tuple[type, ...], what: str, fix: str) -> None:
    """A ValueError naming each of `names` whose tool is not of `kind` (what this adapter can wrap), with `fix`, so a
    tool of the wrong kind (a plain function, a hosted tool, a toolset) is a clear error, never a TypeError from deep
    inside the framework."""
    wanted = set(names)
    wrong = sorted({tool_name(t) for t in tools if tool_name(t) in wanted and not isinstance(t, kind)})
    if wrong:
        raise ValueError(f"{what} tools it cannot wrap: {', '.join(wrong)} ({fix})")


def peer_cards(ctx: TaskContext, agents: Iterable[str] | None = None) -> list[AgentCard]:
    """The mesh agents this task's user may invoke, limited to `agents` when given."""
    wanted = name_filter(agents)
    return [card for card in ctx.available_agents() if wanted(card.name)]


def named_tool_infos(tools: Iterable[Any]) -> list[ToolInfo]:
    """A card tool manifest from tools with a name (``tool_name``) and a ``description``, each with the scopes it was
    given (``with_scopes``); a tool without a name is left out."""
    return [
        ToolInfo(tool_name(t), str(getattr(t, "description", "") or ""), list(scopes_of(t)))
        for t in tools
        if tool_name(t)
    ]


ToolScopeMap = Mapping[str, Iterable[str]]


def scoped_infos(infos: list[ToolInfo], scopes: ToolScopeMap | None) -> list[ToolInfo]:
    """`infos` with the scopes `scopes` names for each tool (tool name -> the scopes it requires) added to its
    ``required_scopes``: what SAM checks each user against and every adapter enforces. A name that is none of the
    tools is a ValueError, since the tool it was meant for would run unguarded; a string where a list of scopes
    belongs is a TypeError."""
    if not scopes:
        return infos
    if not isinstance(scopes, Mapping):
        raise TypeError(f"scopes must map tool names to lists of scopes, not {type(scopes).__name__}")
    unknown = sorted(set(scopes) - {info.name for info in infos})
    if unknown:
        raise ValueError(f"scopes names tools that are not among the tools: {', '.join(map(str, unknown))}")
    scoped = []
    for info in infos:
        extra = scopes.get(info.name, ())
        if isinstance(extra, (str, bytes)) or not all(isinstance(scope, str) and scope for scope in extra):
            raise TypeError(f"the scopes of {info.name} must be a list of scope strings, not {extra!r}")
        required = [*info.required_scopes, *(scope for scope in extra if scope not in info.required_scopes)]
        scoped.append(ToolInfo(info.name, info.description, required))
    return scoped


SCOPES_ATTR = "_sam_link_scopes"
"""A tool given scopes (each adapter's ``with_scopes``) is wrapped in the framework's own tool type, and the wrapper checks
the task's grants where the tool runs, whatever path called it: the hooks hide the tool and report a refusal, but if
every hook is bypassed (a nested run no hook sees, a thread, a background run) the tool still does not run. The wrapper
finds its task through ``current_task()``; with none it refuses: a scoped tool never runs outside a mesh task, and it
refuses once the task is stopped or answered.
"""
log = logging.getLogger("sam_link_sdk.adapter")


def outside_task(name: str) -> str:
    return f"{name} runs only for a mesh task's user, and no mesh task is running here, so it was not run."


def stopped(name: str) -> str:
    return f"This task was stopped, so {name} was not run."


def answered(name: str) -> str:
    return f"This task has already been answered, so {name} was not run."


def is_answered(ctx: TaskContext) -> bool:
    """Whether the task has been answered: work it left running (an asyncio task, a background run, a thread with
    the context) must not act for it any more. The agent marks it (``TaskContext.answered``)."""
    return ctx._run.answered


def over(ctx: TaskContext, name: str) -> str | None:
    """Why `ctx`'s task can run nothing more, or None: it was stopped (cancelled, or the agent halted), or it has been
    answered. For an answered task this logs once per task and nothing else: the transport no longer holds the task, so
    no step or audit record can be sent for it."""
    reason = ctx._run.halt_reason
    if not reason:
        return None
    note_halt()
    if reason != HALT_ANSWERED:
        return stopped(name)
    note_late_work(ctx, f"call {name}")
    return answered(name)


def note_late_work(ctx: TaskContext, what: str) -> None:
    """Note that `what` was refused because the run no longer holds its task: one WARNING per run, shared with the
    core's own notes (a late question, a late peer call)."""
    ctx._run.note_late_work(what)


_halt_seen: contextvars.ContextVar[list[bool] | None] = contextvars.ContextVar("sam_link_halt_seen", default=None)
"""Whether, in the run halts_bare wraps, one of our hooks or guards refused for the halt; under the run's context."""


def note_halt() -> None:
    """Note that this run met its task's halt in one of our hooks or guards (see halts_bare)."""
    seen = _halt_seen.get()
    if seen is not None:
        seen[0] = True


def check_running(ctx: TaskContext) -> None:
    """``ctx.check_running()``, with an answered task's late call logged once: what a hook calls before a model or
    tool call."""
    try:
        ctx.check_running()
    except TaskHalted as halted:
        note_halt()
        if halted.reason == HALT_ANSWERED:
            note_late_work(ctx, "a model or tool call")
        raise


def _halt_now(ctx: TaskContext) -> TaskHalted | None:
    """The TaskHalted ``ctx.check_running()`` raises now, if any: the task was stopped while the run went on."""
    try:
        ctx.check_running()
    except TaskHalted as halted:
        return halted
    return None


R = TypeVar("R")


def halts_bare(run_agent: Callable[..., Awaitable[R]]) -> Callable[..., Awaitable[R]]:
    """An adapter's ``run_agent`` that ends in the bare TaskHalted when its task was halted, however the framework
    passed the halt on: wrapped in its own exception (found anywhere in the chain or a group), or swallowed (one of our
    hooks or guards refused for the halt during this run, and the run then returned, or failed for another reason).
    A stop that nothing in the run met leaves its outcome alone: a completed run's answer stands. The agent answers a
    bare TaskHalted with the stop's message; any other exception is a handler crash."""

    @functools.wraps(run_agent)
    async def run(ctx: TaskContext, *args: Any, **kwargs: Any) -> R:
        seen = [False]
        token = _halt_seen.set(seen)
        try:
            result = await run_agent(ctx, *args, **kwargs)
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            halted = halt_of_task(exc, ctx.task_id) or (_halt_now(ctx) if seen[0] else None)
            if halted is None or halted is exc:
                raise
            raise type(halted)(halted.task_id, halted.reason) from exc
        finally:
            _halt_seen.reset(token)
        if seen[0]:
            ctx.check_running()
        return result

    return run


def scope_map(tools: Iterable[Any], scopes: ToolScopeMap, hint: str = "") -> dict[str, tuple[str, ...]]:
    """`scopes` checked against `tools`: a name that is none of them is a ValueError (the tool it meant would run
    unguarded; `hint` says why it may not be); scopes not given as a list of scope strings are a TypeError."""
    if not isinstance(scopes, Mapping):
        raise TypeError(f"scopes must map tool names to lists of scopes, not {type(scopes).__name__}")
    check_names_known(scopes, {tool_name(t) for t in tools}, "with_scopes names", hint)
    checked: dict[str, tuple[str, ...]] = {}
    for name, required in scopes.items():
        if isinstance(required, (str, bytes)) or not all(isinstance(scope, str) and scope for scope in required):
            raise TypeError(f"the scopes of {name} must be a list of scope strings, not {required!r}")
        checked[name] = tuple(required)
    return checked


INNER_ATTR = "_sam_link_inner"


def mark_wrapper(wrapper: Any, inner: Any) -> Any:
    """Register `wrapper` as standing in for `inner`, so every walk that inspects tools (agent tools, toolsets,
    failure tracking) sees through it with ``unwrapped``. Every wrapper an adapter returns is registered here."""
    object.__setattr__(wrapper, INNER_ATTR, inner)
    return wrapper


def unwrapped(tool: Any) -> Any:
    """The tool behind this SDK's wrappers, however deeply they nest (scopes over an approval, or the other way)."""
    seen: set[int] = set()
    while id(tool) not in seen:
        seen.add(id(tool))
        inner = tool.__dict__.get(INNER_ATTR) if hasattr(tool, "__dict__") else None
        if inner is None:
            return tool
        tool = inner
    return tool


def scopes_of(tool: Any) -> tuple[str, ...]:
    return tuple(getattr(tool, SCOPES_ATTR, ()) or ())


def mark_scoped(tool: Any, scopes: Iterable[str]) -> Any:
    """Record a tool's required scopes on it and return it.

    Set with `object.__setattr__` because pydantic tools (LangChain's, CrewAI's) refuse an attribute that is not a
    field.
    """
    object.__setattr__(tool, SCOPES_ATTR, tuple(scopes))
    return tool


def scope_refusal(name: str, scopes: Iterable[str]) -> str | None:
    """Why a call to a tool requiring `scopes` must not run, or None: there is no current mesh task, the task was
    stopped, the agent's card does not declare the scopes (so SAM grants none of them), or the task's user lacks them
    (audited, as the hooks audit a refusal)."""
    scopes = tuple(scopes)
    ctx = current_task()
    if ctx is None:
        log.warning("refusing a scoped tool outside any mesh task tool=%s", name)
        return outside_task(name)
    ended = over(ctx, name)
    if ended:
        return ended
    if not set(scopes) <= set(ctx._run.tools.declared_scopes.get(name, ())):
        log.warning(
            "refusing a scoped tool whose scopes the agent's card does not declare task=%s tool=%s", ctx.task_id, name
        )
        return access_denied(name)
    return None if ctx.check_tool(name) else access_denied(name)


def approval_refusal(ctx: TaskContext | None, name: str, scopes: Iterable[str] = ()) -> str | None:
    """Why an approval wrapper must ask nobody and refuse: it runs for no mesh task, or for another task than the one
    it was built for (its user is the one who must approve), or its task was stopped, or the tool it guards requires
    `scopes` the task's user lacks (checked before the user is asked, not after)."""
    current = current_task()
    if current is None or (ctx is not None and current is not ctx):
        log.warning("refusing an approval-guarded tool outside its mesh task tool=%s", name)
        return outside_task(name)
    ended = over(current, name)
    if ended:
        return ended
    scopes = tuple(scopes)
    return scope_refusal(name, scopes) if scopes else None


def access_denied(name: str) -> str:
    """What the model is told about a call to a tool the user lacks the scopes for, as SAM-Go words it."""
    return f"Access denied: you do not have the scopes required to use {name}."


def refused_result(name: str) -> dict:
    """The result of a call refused for missing scopes: what the model is given and what the step records."""
    return error_result(access_denied(name))


def refusal_steps(name: str, args: Mapping[str, Any] | None, call_id: str) -> tuple[ToolStarted, ToolFinished]:
    """The steps of a call refused for missing scopes, the same in every adapter: the call's start, then a failed
    result carrying the refusal. The tool never runs; the audit trail records the denial (``tool access denied``,
    once per tool and run) and the call as failed (``tool execution failed``)."""
    return tool_started(name, args, call_id), tool_finished(name, refused_result(name), call_id)


class ToolAccessDenied(PermissionError):
    """A call to a tool the task's user lacks the required scopes for, refused before the tool ran. Raised where the
    framework has no way to give the model the refusal as the call's result, so the run ends instead."""

    def __init__(self, tool: str):
        super().__init__(access_denied(tool))
        self.tool = tool


class NestedRunRefused(RuntimeError):
    """A run that would start another run SAM cannot see: an agent the framework runs as a tool without the hooks
    (or plugins, or middleware) that report its steps and check its tools. Refused before the run starts, rather than
    let that nested run's tool and model calls go unchecked and unreported."""


class SessionInUse(RuntimeError):
    """A mesh task's Hermes agent has the session id of another task's agent still running: their tool calls could not
    be told apart, so the second run is refused."""


class ToolScopes:
    """Which tools one run's user may use, from the scopes each declares (``AgentConfig.tools``): a tool is permitted
    when it declares no scopes, otherwise only when SAM granted the task's user all of them. A denied tool is audited
    once per run, however often the run hides it or refuses a call to it. Safe to use from the framework's threads."""

    def __init__(self, ctx: TaskContext):
        self._ctx = ctx
        self._lock = threading.Lock()
        self._denied: set[str] = set()

    def permits(self, name: str) -> bool:
        with self._lock:
            if name in self._denied:
                return False
            if self._ctx.check_tool(name):
                return True
            self._denied.add(name)
            return False

    def visible(self, tools: Iterable[T]) -> list[T]:
        """The tools the user may use, to offer the model (each matched by ``tool_name``)."""
        return [t for t in tools if self.permits(tool_name(t))]

    @property
    def denied(self) -> frozenset[str]:
        """The tools this run has found the user may not use so far."""
        with self._lock:
            return frozenset(self._denied)


def join_prompts(*prompts: str) -> str:
    return "\n\n".join(p for p in prompts if p)


def request_prompt(ctx: TaskContext, include: bool) -> str:
    """The gateway's request context as prompt text when `include` (``include_request_context``), else empty."""
    return ctx.request_context.context_prompt() if include else ""


def input_text(ctx: TaskContext, input: str | None) -> str:
    """The text a run answers: `input` when the handler gave one, else the task's own text."""
    return input if input is not None else ctx.text


def refusal(name: str, decision: ApprovalResult) -> str:
    """What the model is told about a call the user did not approve."""
    return f"The user did not approve this call to {name} ({decision.outcome}), so it was not run."


async def approve(
    ctx: TaskContext, name: str, args: Mapping[str, Any], message: str, timeout_seconds: float | None = None
) -> str | None:
    """Ask the task's user to approve a call. None when approved and the task can still run, else the refusal to give
    the model. With `timeout_seconds` the prompt closes then (timed out, so refused) instead of after the default wait.

    The rule for every wait this SDK puts before a guarded body: the check that preceded it is made again after it.
    The user may answer after SAM stopped the agent, the task was cancelled or it was answered (a worker thread the
    stop does not cancel, work the handler left running), so an approval then runs nothing. The scopes need no second
    look: a task's grants do not change."""
    timeout = {} if timeout_seconds is None else {"timeout_seconds": timeout_seconds}
    decision = await ctx.request_approval(name, dict(args), message=message, **timeout)
    return over(ctx, name) if decision.approved else refusal(name, decision)


async def decide_approval(
    ctx: TaskContext, name: str, args: Mapping[str, Any], message: str, tools: Iterable[Any] = ()
) -> str | None:
    """None when the user approves the call a framework holds for approval itself, else why it is refused: asked through
    SAM, unless refused for a fact known before asking (a stopped task, the scopes of the tool among `tools` that is
    called `name`)."""
    scopes = next((scopes_of(t) for t in tools if tool_name(t) == name), ())
    return approval_refusal(ctx, name, scopes) or await approve(ctx, name, args, message)


@contextmanager
def may_be_lost(ctx: TaskContext, what: str = "step") -> Iterator[None]:
    """Report a step or a piece of text inside it: a lost one (the transport failed) is logged and the run goes on."""
    try:
        yield
    except Exception as exc:
        log.warning("%s dropped task=%s err=%s", what, ctx.task_id, type(exc).__name__)


async def delegate(ctx: TaskContext, card: AgentCard, task_description: Any, call_id: str = "") -> str:
    """What every adapter's `peer_<name>` tool does: hand the task to the peer and give the model its summary."""
    result = await ctx.call_agent(card.name, str(task_description or ""), function_call_id=call_id)
    return result.summary()


_scoped_classes: dict[type, type] = {}


def scoped_class(cls: type, methods: Callable[[type], Mapping[str, Any]], module: str) -> type:
    """A subclass of the tool class `cls` with `methods(cls)` over its own, made once per class: the tool class a
    ``with_scopes`` copy gets, so the scope check sits where the framework runs the tool."""
    scoped = _scoped_classes.get(cls)
    if scoped is None:
        scoped = _scoped_classes[cls] = type(f"Scoped{cls.__name__}", (cls,), {**methods(cls), "__module__": module})
    return scoped


def scoped_variant(cls: type) -> type | None:
    """The class ``scoped_class`` made from `cls`, if one was."""
    return _scoped_classes.get(cls)


ASK_USER_DESCRIPTION = (
    "Ask the user a question when you need information or a decision only they can give. Waits for the answer."
)
QUESTION_HELP = "The question to ask the user."
OPTIONS_HELP = "Choices to offer. Leave out for a free-text answer."
ASK_USER_PARAMETERS = {
    "type": "object",
    "properties": {
        "question": {"type": "string", "description": QUESTION_HELP},
        "options": {"type": "array", "items": {"type": "string"}, "description": OPTIONS_HELP},
    },
    "required": ["question"],
}


NOT_ASKED = "not_asked"
"""The transport could not carry the question as the model asked it (PromptTooLarge)"""


async def ask(ctx: TaskContext, question: str, options: Iterable[Any] | None = None) -> UserAnswer:
    """Ask the user the model's question. A question the transport cannot carry (more options than the agent link
    takes) is not asked: the model is told why, so it can ask again within the limit."""
    try:
        return await ctx.ask_user(Question(str(question), tuple(Option(str(o)) for o in options or ())))
    except PromptTooLarge as exc:
        return UserAnswer(NOT_ASKED, (str(exc),))


def unanswered(answer: UserAnswer) -> str:
    if answer.status == NOT_ASKED:
        return f"The question was not asked: {answer.answers[0]}. Ask again within that limit."
    return f"The user did not answer ({answer.status})."


def answer_text(answer: UserAnswer) -> str:
    """What the model is told: the user's answer, or that there was none."""
    return answer.answers[0] if answer.answered else unanswered(answer)
