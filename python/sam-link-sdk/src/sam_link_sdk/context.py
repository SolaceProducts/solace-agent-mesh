# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Per-task context handed to the user's handler."""

from __future__ import annotations

import contextlib
import contextvars
from collections.abc import Callable, Iterable, Mapping
from dataclasses import dataclass, field
from typing import Any, Literal, TypeVar

from . import prompting as hil
from ._model import File, InputFile, PeerResult
from ._run import (
    ASK_USER_TOOL as ASK_USER_TOOL,
    HALT_ANSWERED,
    HALT_CANCELLED,
    HALT_STOPPED,
    TaskChannel,
    _TaskRun,
    _ToolPolicy,
    tool_name as tool_name,
)
from .audit import AuditLogger
from .delegation import AgentCard
from .events import Progress, StepEvent, Thinking
from .metrics import Metrics
from .request_context import RequestContext
from .stop import AgentStopped
from .usage import Usage

__all__ = ["File", "InputFile", "TaskContext", "TaskHalted", "TaskResult", "bind_task", "current_task"]

T = TypeVar("T")


class TaskHalted(RuntimeError):
    """Raised by ``TaskContext.check_running`` in work that must not go on: its task was cancelled, stopped (a limit,
    SAM's stop) or already answered, or SAM has stopped the agent. ``reason`` says which."""

    def __init__(self, task_id: str, reason: str = HALT_STOPPED):
        """Build the error for a task that was stopped or already answered.

        Calls `RuntimeError.__init__` directly, not `super()`: in the agent-stopped subclass `super()` is AgentStopped,
        which words its own message.
        """
        RuntimeError.__init__(
            self,
            f"task {task_id} was stopped ({reason})"
            if reason != HALT_ANSWERED
            else f"task {task_id} was already answered",
        )
        self.task_id = task_id
        self.reason = reason


class _AgentStoppedTaskHalted(TaskHalted, AgentStopped):
    """The agent is stopped: caught as either TaskHalted or AgentStopped."""

    def __init__(self, task_id: str, reason: str):
        RuntimeError.__init__(self, f"agent stopped by SAM ({reason})")
        self.task_id = task_id
        self.reason = reason


MAX_CHAIN_WALK = 64


def halt_of_task(exc: BaseException, task_id: str) -> TaskHalted | None:
    """The TaskHalted of task `task_id` that `exc` is, or that it wraps: through ``__cause__``, ``__context__`` and
    exception group members, as frameworks wrap a hook's or plugin's exception (ADK in a RuntimeError, Strands in an
    EventLoopException). Another task's TaskHalted is not this task's stop. Bounded and safe against cycles.
    """
    seen: set[int] = set()
    pending: list[BaseException | None] = [exc]
    while pending and len(seen) < MAX_CHAIN_WALK:
        current = pending.pop(0)
        if current is None or id(current) in seen:
            continue
        seen.add(id(current))
        if isinstance(current, TaskHalted):
            if current.task_id == task_id:
                return current
            continue
        pending.append(current.__cause__)
        pending.append(current.__context__)
        if isinstance(current, BaseExceptionGroup):
            pending.extend(current.exceptions)
    return None


TASK_RESULT_STATES = ("completed", "failed")


@dataclass
class TaskResult:
    """What a handler returns when it needs more than plain text. ``state`` is "completed" or "failed" (a logical
    failure, answered as a failed task with ``text``)."""

    text: str
    state: Literal["completed", "failed"] = "completed"
    files: list[File] = field(default_factory=list)
    data: dict[str, Any] | None = None

    def __post_init__(self) -> None:
        if self.state not in TASK_RESULT_STATES:
            raise ValueError(f"TaskResult.state must be one of {TASK_RESULT_STATES}, not {self.state!r}")


class TaskContext:
    """Everything a handler needs: the input, and a way to report progress to the mesh.

    The core's side of the task (its run state, channel, usage, limits and tool scopes) is ``_run``, a ``_TaskRun``."""

    def __init__(
        self,
        *,
        task_id: str,
        context_id: str,
        text: str,
        agent_name: str,
        user_id: str = "",
        files: list[InputFile] | None = None,
    ):
        """A MeshAgent builds the context of each task it runs. Built by hand (to test a handler), it reports
        nowhere: steps go nowhere, and asking the user or calling a peer raises."""
        self.task_id = task_id
        self.context_id = context_id
        self.text = text
        self.agent_name = agent_name
        self.files = files or []
        self.metadata: dict[str, Any] = {}
        self.request_context = RequestContext()
        self.structured_input: Any = None
        self.invocation: dict[str, Any] | None = None
        self.call_depth = 0
        self.run_based = False
        """A peer call (sessionBehavior RUN_BASED) or a workflow node runs in a throwaway session, as on SAM-Go agents: it
        neither sees nor extends the history of the caller's conversation.
        """
        self._run = _TaskRun(task_id=task_id, context_id=context_id, agent_name=agent_name, user_id=user_id)

    @classmethod
    def _new(
        cls,
        *,
        task_id: str,
        context_id: str,
        text: str,
        agent_name: str,
        channel: TaskChannel | None = None,
        files: list[InputFile] | None = None,
        user_id: str = "",
        call_depth: int = 0,
        structured_input: Any = None,
        invocation: Mapping[str, Any] | None = None,
        audit: AuditLogger | None = None,
        stream_thinking: bool = False,
        max_llm_calls: int = 0,
        on_limit: Callable[[str], None] | None = None,
        metadata: Mapping[str, Any] | None = None,
        request_context: RequestContext | None = None,
        metrics: Metrics | None = None,
        display_name: str = "",
        run_based: bool = False,
        tool_scopes: Mapping[str, list[str]] | None = None,
        declared_tool_names: Iterable[str] | None = None,
        granted_tool_scopes: Iterable[str] | None = None,
        can_prompt_user: bool = True,
        agent_stop_reason: Callable[[], str] | None = None,
    ) -> TaskContext:
        """The context of a task the core runs, with what the core sets up beside the handler's input."""
        ctx = cls(
            task_id=task_id, context_id=context_id, text=text, agent_name=agent_name, user_id=user_id, files=files
        )
        ctx.metadata = dict(metadata or {})
        ctx.request_context = request_context or RequestContext()
        ctx.structured_input = structured_input
        ctx.invocation = dict(invocation) if invocation is not None else None
        ctx.call_depth = call_depth
        ctx.run_based = run_based
        ctx._run = _TaskRun(
            task_id=task_id,
            context_id=context_id,
            agent_name=agent_name,
            user_id=user_id,
            display_name=display_name,
            channel=channel,
            audit=audit,
            metrics=metrics,
            stream_thinking=stream_thinking,
            max_llm_calls=max_llm_calls,
            on_limit=on_limit,
            can_prompt_user=can_prompt_user,
            agent_stop_reason=agent_stop_reason,
            tools=_ToolPolicy(
                task_id=task_id,
                user_id=user_id,
                audit=audit,
                scopes=tool_scopes,
                declared_names=declared_tool_names,
                granted=granted_tool_scopes,
            ),
        )
        return ctx

    @property
    def session_id(self) -> str:
        """The key for this conversation's history (memory, checkpointer thread, framework session): the user and
        the SAM contextId, as SAM-Go keys a session by user and context, so knowing another user's contextId does
        not reach their history. "" for a run-based task, which must start without history and leave none behind."""
        if self.run_based or not self.context_id:
            return ""
        return f"{self.user_id}:{self.context_id}" if self.user_id else self.context_id

    def check_running(self) -> None:
        """Raise TaskHalted when this task's work must not go on: it was cancelled, stopped (a limit, SAM's stop) or
        answered, or SAM stopped the agent (then the exception is also an AgentStopped). ``reason`` says which."""
        reason = self._run.halt_reason
        if not reason:
            return
        if reason in (HALT_CANCELLED, HALT_STOPPED, HALT_ANSWERED):
            raise TaskHalted(self.task_id, reason)
        raise _AgentStoppedTaskHalted(self.task_id, reason)

    def permitted_tools(self, tools: Iterable[T]) -> list[T]:
        """The tools this task's user may use (matched by ``tool_name``), as SAM-Go hides a tool from the model when
        the user lacks their scopes: always the tools that declare no required scopes, else only those whose every
        scope SAM granted for this user (``granted_tool_scopes``, an exact match: SAM named each scope the user
        holds, so no scope the agent declared stands for another). A task SAM granted nothing is refused every tool
        that requires a scope. When the card declares a tool with scopes, a tool whose name the card does not declare
        at all is refused too (it may be one that needs a scope under a name the card does not know), except the tools
        this SDK adds itself (``ask_user_question``, the ``peer_<name>`` tools). A dropped tool is audited as denied;
        give the model the returned list."""
        return self._run.tools.filter(tools)

    def check_tool(self, name: str) -> bool:
        """Whether this task's user may use the tool `name`, by the rules of ``permitted_tools``, auditing a denial:
        for a framework that can only refuse a call as it is made."""
        return self._run.tools.check(name)

    @property
    def streaming(self) -> bool:
        return self._run.channel.streaming

    @property
    def can_prompt_user(self) -> bool:
        """Whether this task's user can be asked (``ask_user``, ``request_approval``): false for a task whose
        channel cannot show a prompt (no status stream, or an entrypoint such as MCP or a webhook). Asking then
        comes back cancelled at once."""
        return self._run.can_prompt_user

    @property
    def user_id(self) -> str:
        """The task's user, as SAM names them."""
        return self._run.user_id

    async def emit_event(self, event: StepEvent) -> None:
        """Report a step (a model call, a tool run, progress) to the mesh. A ``ModelFinished`` with ``usage`` records
        the call's tokens (``record_usage``), so SAM files them with the call."""
        await self._run.emit_event(event)

    async def emit_thinking(self, text: str) -> None:
        """Stream a chunk of the model's reasoning to the chat, if `stream_thinking` is on."""
        if self._run.stream_thinking and text:
            await self.emit_event(Thinking(text))

    async def end_thinking(self) -> None:
        if self._run.stream_thinking:
            await self.emit_event(Thinking("", done=True))

    async def ask_user(
        self,
        questions: list[hil.Question] | hil.Question | str,
        *,
        message: str = "",
        timeout_seconds: float = hil.DEFAULT_TIMEOUT_SECONDS,
    ) -> hil.UserAnswer:
        """Pause the task and ask the user one or more questions (in the web UI, Slack and so on).

        A string is a free-text question; give a Question with options for a choice (an "Other" choice is added).
        Returns once the user answers, cancels, or `timeout_seconds` (default 45 minutes) pass.
        """
        asked = (
            [hil.Question(questions)]
            if isinstance(questions, str)
            else [questions]
            if isinstance(questions, hil.Question)
            else list(questions)
        )
        return hil.user_answer(await self._run.ask(hil.question_prompt(asked, message, timeout_seconds)))

    async def request_approval(
        self,
        tool_name: str,
        tool_args: Mapping[str, Any] | None = None,
        *,
        message: str = "",
        timeout_seconds: float = hil.DEFAULT_TIMEOUT_SECONDS,
    ) -> hil.ApprovalResult:
        """Pause the task and ask the user to approve running a tool. The arguments shown to the user are
        redacted. Anything but an explicit approval (denied, cancelled, timed out) comes back not approved."""
        result = hil.approval_result(
            await self._run.ask(hil.approval_prompt(tool_name, tool_args, message, timeout_seconds))
        )
        self._run.record_approval(tool_name, result.outcome)
        return result

    async def emit_text(self, text: str) -> None:
        await self._run.emit_text(text)

    async def progress(self, status_text: str) -> None:
        await self.emit_event(Progress(status_text))

    def attach(self, name: str, content: bytes, mime_type: str = "") -> None:
        """Return a file with the final answer (for tools that produce files mid-run)."""
        self._run.output_files.append(File(name, content, mime_type=mime_type))

    def available_agents(self) -> list[AgentCard]:
        """Agents on the mesh this task's user may delegate to."""
        return self._run.available_agents()

    async def call_agent(self, agent: str, task_description: str, *, function_call_id: str = "") -> PeerResult:
        """Delegate a sub-task to another agent on the mesh and wait for its answer.

        The peer's steps appear under this task in the Activity Monitor; `function_call_id` ties them to
        the tool call that made the request. Cancelling this task cancels the peer's sub-task.
        """
        return await self._run.call_peer(agent, task_description, function_call_id)

    def record_llm_error(self, error: BaseException | str, provider: str = "") -> None:
        """Note that a model call failed, so the task's error reply carries the failure's category and provider.
        Errors raised by a provider's client library are recognised without this call.

        `error` is the exception, or, where only a description of it is at hand (an OpenTelemetry span's
        ``error.type``), a string: a category (``"rate_limit"``), an HTTP status code (``"429"``) or an exception
        type (``"openai.RateLimitError"``); see ``sam_link_sdk.llm_errors.classify_error_type``."""
        self._run.record_llm_error(error, provider)

    def record_usage(self, model: str, usage: Usage) -> None:
        """Record a model call's tokens for the task, for usage no ``ModelFinished`` step carries (a step's own usage
        is recorded as it is emitted)."""
        self._run.record_usage(model, usage)


_current_task: contextvars.ContextVar[TaskContext | None] = contextvars.ContextVar(
    "sam_link_current_task", default=None
)
"""The mesh task the running code serves: set for the length of a run (``bind_task``) and carried wherever the framework
carries the context (asyncio tasks, threads started with a copied context). A tool that declares scopes reads it where
it runs, so it can refuse whatever path called it.
"""


def current_task() -> TaskContext | None:
    """The mesh task the running code serves, or None outside one."""
    return _current_task.get()


@contextlib.contextmanager
def bind_task(ctx: TaskContext):
    """Make `ctx` the current task (``current_task()``) for the code run inside, and whatever it starts with the
    context copied."""
    token = _current_task.set(ctx)
    try:
        yield ctx
    finally:
        _current_task.reset(token)
