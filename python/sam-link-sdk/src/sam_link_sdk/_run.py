# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A task's run, as the core and the adapters see it: the state ``TaskContext`` keeps out of the handler's API.

``TaskContext`` is what a handler holds (the input, ``emit_*``, ``ask_user``, ``call_agent``). ``_TaskRun`` is the
core's side of the same task: where its run stands (``RunState``), its channel to the transport, the usage and model
and tool timings it collects, its limits and its tool scopes. The agent core and the adapters reach it as
``ctx._run``.
"""

from __future__ import annotations

import asyncio
import enum
import logging
import threading
import time
from collections.abc import Callable, Coroutine, Iterable, Mapping
from types import MappingProxyType
from typing import Any, Protocol, TypeVar

from . import prompting as hil
from ._model import PEER_CALL_GRANTED, File, PeerInfo, PeerResult, Prompt, PromptAnswer
from .audit import AuditLogger
from .delegation import AgentCard, card_of
from .events import ModelFailed, ModelFinished, ModelStarted, StepEvent, Text, ToolFinished, ToolStarted
from .llm_errors import LLMFailure, classify_error_type, classify_llm_failure
from .metrics import LLM_DURATION, OPERATION_DURATION, TOKENS_USED, Metrics, NoMetrics, llm_error_type
from .usage import Usage, UsageAccumulator

log = logging.getLogger("sam_link_sdk.task")

T = TypeVar("T")

ASK_USER_TOOL = "ask_user_question"
NOT_ATTACHED = "this task was not started by a MeshAgent; it cannot {}"

HALT_CANCELLED = "cancelled"
HALT_STOPPED = "stopped"
HALT_ANSWERED = "answered"


class RunState(enum.Enum):
    """How a task's run stands. ``RUNNING`` until something ends it early: a limit or SAM's stop (``HALTED``), or the
    caller's cancel (``CANCELLED``, which wins over a halt). The answer going out does not change it: whether the run
    was answered is ``_TaskRun.answered``, and a cancelled or halted run is answered afterwards too."""

    RUNNING = "running"
    HALTED = "halted"
    CANCELLED = "cancelled"


class TaskChannel(Protocol):
    """Where a task's steps, prompts and peer calls go: the agent's transport, for a task a MeshAgent runs."""

    streaming: bool
    attached: bool

    async def send(self, event: StepEvent) -> bool: ...

    async def ask(self, prompt: Prompt) -> PromptAnswer: ...

    async def call_peer(self, agent: str, task_description: str, call_id: str) -> PeerResult: ...

    def peers(self) -> list[PeerInfo]: ...


class _NoChannel:
    """A context built without a channel: steps go nowhere."""

    streaming = False
    attached = False

    async def send(self, event: StepEvent) -> bool:
        return False

    async def ask(self, prompt: Prompt) -> PromptAnswer:
        raise RuntimeError(NOT_ATTACHED.format("ask the user"))

    async def call_peer(self, agent: str, task_description: str, call_id: str) -> PeerResult:
        raise RuntimeError(NOT_ATTACHED.format("call other agents"))

    def peers(self) -> list[PeerInfo]:
        return []


def tool_name(tool: Any) -> str:
    """A tool's name as each framework spells it: ``tool_name`` (Strands), ``name`` (the other frameworks' tool
    objects), ``function.name`` or ``name`` of an OpenAI-format definition (a dict), else a function's ``__name__``."""
    if isinstance(tool, Mapping):
        function = tool.get("function")
        return str((function.get("name") if isinstance(function, Mapping) else tool.get("name")) or "")
    for attribute in ("tool_name", "name", "__name__"):
        value = getattr(tool, attribute, None)
        if isinstance(value, str) and value:
            return value
    return ""


class _ToolPolicy:
    """Which tools a task's user may use: the scopes the agent's card declares for each tool, against the scopes SAM
    granted this user for this task."""

    def __init__(
        self,
        *,
        task_id: str,
        user_id: str,
        audit: AuditLogger | None = None,
        scopes: Mapping[str, list[str]] | None = None,
        declared_names: Iterable[str] | None = None,
        granted: Iterable[str] | None = None,
    ):
        self._task_id = task_id
        self._user_id = user_id
        self._audit = audit
        self._scopes: dict[str, tuple[str, ...]] = {name: tuple(s) for name, s in (scopes or {}).items()}
        self._declared_names: frozenset[str] = frozenset(declared_names or ()) | self._scopes.keys()
        self._granted: frozenset[str] = frozenset(granted or ())
        self._sdk_names: set[str] = {ASK_USER_TOOL}
        self._shown_names: dict[str, str] = {}
        self._undeclared_noted: set[str] = set()

    @property
    def declared_scopes(self) -> Mapping[str, tuple[str, ...]]:
        """The scopes each tool the agent declared with scopes requires (``AgentConfig.tools``), by tool name. Empty
        when no declared tool requires a scope."""
        return MappingProxyType(self._scopes)

    def permitted(self, name: str) -> bool:
        if self._undeclared(name):
            return False
        required = self._scopes.get(name)
        if not required:
            return True
        return all(scope in self._granted for scope in required)

    def _undeclared(self, name: str) -> bool:
        return bool(self._scopes) and name not in self._declared_names and name not in self._sdk_names

    def knows(self, name: str) -> bool:
        return name in self._declared_names or name in self._sdk_names

    def note_sdk_tools(self, names: Iterable[str]) -> None:
        """Tools this SDK built for this task under the names the framework gave them: on no card, and not to be
        refused for that."""
        self._sdk_names.update(names)

    def note_sdk_alias(self, alias: str, name: str) -> None:
        """A framework offers the SDK's tool `name` to the model as `alias` (CrewAI makes names lowercase a-z0-9_). Its
        steps are reported under `name`, the one SAM knows: SAM renames a step whose ``peer_`` tool names no peer the
        agent may call."""
        self._sdk_names.update((alias, name))
        if alias != name:
            self._shown_names[alias] = name

    def shown_name(self, name: str) -> str:
        """The name a tool's steps are reported under: the SDK's own name for an alias the framework gave it."""
        return self._shown_names.get(name, name)

    def check(self, name: str) -> bool:
        """``permitted``, auditing a denial."""
        if self.permitted(name):
            return True
        if self._undeclared(name):
            if name not in self._undeclared_noted:
                self._undeclared_noted.add(name)
                log.error(
                    "tool refused: the agent's card does not declare it, and the card declares tools with scopes "
                    "task=%s tool=%s declared=%s",
                    self._task_id,
                    name,
                    sorted(self._declared_names),
                )
        else:
            log.warning("tool denied: user lacks its scopes task=%s tool=%s", self._task_id, name)
        if self._audit is not None:
            self._audit.tool_access(
                user_id=self._user_id,
                task_id=self._task_id,
                tool=name,
                allowed=False,
                scopes=self._scopes.get(name, ()),
            )
        return False

    def filter(self, tools: Iterable[T]) -> list[T]:
        return [t for t in tools if self.check(tool_name(t))]


class _TaskRun:
    """The core's state for one task. Built by the agent core with the task (``TaskContext._new``), or with defaults for
    a context built by hand, where it reports nowhere."""

    def __init__(
        self,
        *,
        task_id: str,
        agent_name: str,
        context_id: str = "",
        user_id: str = "",
        display_name: str = "",
        channel: TaskChannel | None = None,
        audit: AuditLogger | None = None,
        metrics: Metrics | None = None,
        stream_thinking: bool = False,
        max_llm_calls: int = 0,
        on_limit: Callable[[str], None] | None = None,
        can_prompt_user: bool = True,
        agent_stop_reason: Callable[[], str] | None = None,
        tools: _ToolPolicy | None = None,
    ):
        try:
            self._loop: asyncio.AbstractEventLoop | None = asyncio.get_running_loop()
        except RuntimeError:
            self._loop = None
        self.task_id = task_id
        self.context_id = context_id
        self.user_id = user_id
        self.agent_name = agent_name
        self.display_name = display_name or agent_name
        self.channel: TaskChannel = channel or _NoChannel()
        self.audit = audit
        self.metrics: Metrics = metrics or NoMetrics()
        self.stream_thinking = stream_thinking
        self.max_llm_calls = max_llm_calls
        self.on_limit = on_limit
        self._can_prompt_user = can_prompt_user
        self.tools = tools or _ToolPolicy(task_id=task_id, user_id=user_id, audit=audit)

        self.state = RunState.RUNNING
        self.answered = False
        """Set once the agent answers the task (before the answer goes out): work left running after it (a background
        asyncio task, a framework's thread) may still report steps, but runs no guarded tool, asks nobody and calls no
        peer.
        """
        self._agent_stop_reason = agent_stop_reason
        """The agent's stop, read live: it reaches work whose task has already left the agent's table."""
        self._late_work_noted = False
        self._late_work_lock = threading.Lock()

        self.usage = UsageAccumulator()
        self.on_usage: Callable[[str, Usage], None] | None = None
        self.llm_failure: LLMFailure | None = None
        self.tool_after_model = False
        self.output_files: list[File] = []
        self.text_streamed = False
        self.events_published = 0
        self.last_event_at = time.monotonic()
        self._llm_invocations = 0
        self._llm_started: list[tuple[float, str]] = []
        self._tool_started: dict[str, tuple[float, str]] = {}

    @property
    def cancelled(self) -> bool:
        return self.state is RunState.CANCELLED

    def halt(self) -> None:
        """A limit, SAM's stop or the agent's shutdown ended the run: its work must not go on. A cancel stays a cancel."""
        if self.state is RunState.RUNNING:
            self.state = RunState.HALTED

    def cancel(self) -> None:
        self.state = RunState.CANCELLED

    def mark_answered(self) -> None:
        self.answered = True

    @property
    def halt_reason(self) -> str:
        """Why this task's work must not go on, or "" while it may: ``cancelled``, ``stopped`` (a limit, or SAM's stop
        while it ran), ``answered``, or SAM's reason for stopping the agent (``stopped_by_sam``, ``disconnected``), read
        live, also after the task was answered."""
        if self.state is RunState.CANCELLED:
            return HALT_CANCELLED
        if self.state is RunState.HALTED:
            return HALT_STOPPED
        agent_stopped = self._agent_stop_reason() if self._agent_stop_reason is not None else ""
        if agent_stopped:
            return agent_stopped
        return HALT_ANSWERED if self.answered else ""

    def note_late_work(self, what: str) -> bool:
        """Report that work of this task's run went on after the run stopped holding the task (answered, cancelled,
        given up, or the task id delivered again) and that `what` was not done. One WARNING per run, whoever notes
        it (the core, an adapter's hook), DEBUG after; returns True the first time."""
        with self._late_work_lock:
            first, self._late_work_noted = not self._late_work_noted, True
        log.log(
            logging.WARNING if first else logging.DEBUG,
            "work continued after its run no longer held the task; not done task=%s what=%s",
            self.task_id,
            what,
        )
        return first

    def refused_after_answer(self, what: str) -> bool:
        """Whether `what` must not be done because the task was answered (noted with note_late_work)."""
        if not self.answered:
            return False
        self.note_late_work(what)
        return True

    @property
    def can_prompt_user(self) -> bool:
        return self._can_prompt_user and self.channel.streaming

    async def on_own_loop(self, coro: Coroutine[Any, Any, Any]) -> Any:
        """Run `coro` on the event loop this task belongs to. Frameworks call back from worker threads with their
        own loops (LangChain runs a sync tool's callbacks in a thread pool under asyncio.run); this task's publish
        lock, waits and timers belong to the agent's loop, so the work is handed over to it."""
        try:
            running = asyncio.get_running_loop()
        except RuntimeError:
            running = None
        own = self._loop
        if own is None or running is own or own.is_closed():
            return await coro
        return await asyncio.wrap_future(asyncio.run_coroutine_threadsafe(coro, own))

    async def send(self, event: StepEvent) -> bool:
        """Put a step on this task's stream. Returns whether it went out."""
        if self.cancelled or not self.channel.streaming:
            return False
        if not await self.channel.send(event):
            return False
        self.last_event_at = time.monotonic()
        self.events_published += 1
        log.debug("status published task=%s kind=%s seq_events=%d", self.task_id, event.kind, self.events_published)
        return True

    async def emit_event(self, event: StepEvent) -> None:
        await self.on_own_loop(self._emit(event))

    async def _emit(self, event: StepEvent) -> None:
        if isinstance(event, ModelFinished) and event.usage is not None:
            self.record_usage(event.model, event.usage)
        if isinstance(event, ModelFailed):
            self.llm_failure = LLMFailure(event.category, event.provider)
        elif isinstance(event, ModelFinished):
            self.llm_failure = None
        self._count_llm_call(event)
        self._time_llm_call(event)
        self._track_tool(event)
        if isinstance(event, ModelStarted):
            self.tool_after_model = False
        elif isinstance(event, (ToolStarted, ToolFinished)):
            self.tool_after_model = True
        await self.send(event)

    async def emit_text(self, text: str) -> None:
        self.text_streamed = True
        await self.on_own_loop(self.send(Text(text)))

    async def ask(self, prompt: Prompt) -> PromptAnswer:
        if not self.channel.attached:
            raise RuntimeError(NOT_ATTACHED.format("ask the user"))
        if self.refused_after_answer("ask the user"):
            return PromptAnswer(prompt.request_id, hil.CANCELLED)
        if not self.can_prompt_user:
            log.info("not asking the user: the task's channel cannot show a prompt task=%s", self.task_id)
            return PromptAnswer(prompt.request_id, hil.CANCELLED)
        return await self.on_own_loop(self.channel.ask(prompt))

    def record_approval(self, tool: str, outcome: str) -> None:
        if self.audit is not None:
            self.audit.tool_approval(
                user_id=self.user_id, task_id=self.task_id, agent_name=self.agent_name, tool=tool, outcome=outcome
            )

    def available_agents(self) -> list[AgentCard]:
        cards = [card_of(p) for p in self.channel.peers()]
        self.tools.note_sdk_tools(card.tool_name for card in cards)
        return cards

    async def call_peer(self, agent: str, task_description: str, call_id: str) -> PeerResult:
        if not self.channel.attached:
            raise RuntimeError(NOT_ATTACHED.format("call other agents"))
        if self.refused_after_answer("call another agent"):
            return PeerResult(agent, "error", "the task was already answered")
        result = await self.on_own_loop(self.channel.call_peer(agent, task_description, call_id))
        if self.audit is not None and result.sam_decision:
            self.audit.agent_delegation(
                user_id=self.user_id,
                task_id=self.task_id,
                agent_name=agent,
                allowed=result.sam_decision == PEER_CALL_GRANTED,
            )
        return result

    def record_llm_error(self, error: BaseException | str, provider: str = "") -> None:
        failure = (
            classify_error_type(error, provider) if isinstance(error, str) else classify_llm_failure(error, provider)
        )
        self.llm_failure = failure or self.llm_failure

    def record_usage(self, model: str, usage: Usage) -> None:
        """Count a model call's tokens and file its usage with the task.

        Tokens are counted even after a cancel, because they were spent; the task's own totals stop at the cancel.
        """
        if self.on_usage is not None and usage._recorded():
            self.on_usage(model, usage)
        for token_type, count in (("input", usage.input_tokens), ("output", usage.output_tokens)):
            if count > 0:
                self.metrics.counter(
                    TOKENS_USED,
                    count,
                    {
                        "gen_ai.request.model": model,
                        "gen_ai.token.type": token_type,
                        "component.name": f"agent-{self.display_name}",
                    },
                )
        if not self.cancelled:
            self.usage.record(model, usage)

    def _track_tool(self, event: StepEvent) -> None:
        """Audit and time a tool run from its start and finish. Only names, timing and the outcome are recorded."""
        if isinstance(event, ToolStarted):
            self._tool_started[event.call_id] = (time.monotonic(), event.name)
        elif isinstance(event, ToolFinished):
            started, name = self._tool_started.pop(event.call_id, (None, event.name))
            elapsed = time.monotonic() - started if started else 0.0
            if started:
                self.metrics.histogram(
                    OPERATION_DURATION,
                    elapsed,
                    {
                        "type": "tool",
                        "component.name": name,
                        "operation.name": "execute",
                        "error.type": "error" if event.failed else "",
                    },
                )
            if self.audit is not None:
                self.audit.tool_execution(
                    user_id=self.user_id,
                    task_id=self.task_id,
                    agent_name=self.agent_name,
                    tool=name,
                    session_id=self.context_id,
                    duration_ms=int(elapsed * 1000),
                    success=not event.failed,
                )

    def _time_llm_call(self, event: StepEvent) -> None:
        """Time a model call from its start to its finish or failure.

        A failed call ends its own start, so a retry's duration does not include the failed attempt.
        """
        if isinstance(event, ModelStarted):
            self._llm_started.append((time.monotonic(), event.model))
        elif isinstance(event, (ModelFinished, ModelFailed)) and self._llm_started:
            index = next((i for i, (_, model) in enumerate(self._llm_started) if model == event.model), 0)
            started, model = self._llm_started.pop(index)
            self.metrics.histogram(
                LLM_DURATION,
                time.monotonic() - started,
                {
                    "gen_ai.request.model": event.model or model,
                    "error.type": llm_error_type(event.category) if isinstance(event, ModelFailed) else "",
                },
            )

    def close_llm_calls(self, error_type: str) -> None:
        """Record model calls still in flight when the task fails: they failed, or the task stopped them. A task
        that completed with a call unanswered has nothing to attribute, so those are dropped."""
        now = time.monotonic()
        for started, model in self._llm_started if error_type else ():
            self.metrics.histogram(
                LLM_DURATION, now - started, {"gen_ai.request.model": model, "error.type": error_type}
            )
        self._llm_started.clear()

    def _count_llm_call(self, event: StepEvent) -> None:
        """Count a model call as it starts, so the call over the limit is stopped before it is paid for."""
        if not isinstance(event, ModelStarted):
            return
        self._llm_invocations += 1
        if self.max_llm_calls > 0 and self._llm_invocations > self.max_llm_calls and self.on_limit is not None:
            self.on_limit("max_llm_calls")

    def passes_llm_call_limit(self, calls: int) -> bool:
        """Whether `calls` model calls pass max_llm_calls. For an adapter that counts its framework's calls before they
        go out, because the framework reports a call only once it is made (CrewAI)."""
        return self.max_llm_calls > 0 and calls > self.max_llm_calls

    def stop_at_llm_call_limit(self) -> None:
        """Stop the task at max_llm_calls, as the call over the limit would; run on the task's event loop."""
        if self.on_limit is not None:
            self.on_limit("max_llm_calls")
