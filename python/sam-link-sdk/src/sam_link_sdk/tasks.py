# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Running tasks: the task registry, the handler run, per-task limits, concurrency, cancel and the final outcome."""

from __future__ import annotations

import asyncio
import inspect
import json
import logging
import time
import weakref
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

from ._model import (
    CANCELLED,
    RUN,
    File,
    IncomingTask,
    PeerCall,
    PeerInfo,
    PeerResult,
    Prompt,
    PromptAnswer,
    TaskError,
    TaskOutcome,
)
from ._run import HALT_ANSWERED, HALT_CANCELLED
from ._util import TrackedTasks
from .context import TaskContext, TaskResult, bind_task, halt_of_task
from .events import StepEvent
from .limits import limit_message
from .llm_errors import classify_llm_failure
from .metrics import OPERATION_DURATION, Metrics, llm_error_type
from .stop import REASON_DISCONNECTED, StopState

if TYPE_CHECKING:
    from ._model import Transport
    from ._run import _TaskRun
    from .audit import AuditLogger
    from .config import AgentConfig

log = logging.getLogger("sam_link_sdk.agent")

Handler = Callable[[TaskContext], Awaitable["str | TaskResult"]]

LIMITS = ("max_llm_calls", "task_timeout_seconds")
AGENT_STOPPED_TEXT = "Agent stopped: SAM has stopped this agent"
STOPPED_ANSWER_SECONDS = 5.0
CANCEL_DEADLINE_SECONDS = 5.0
"""How long a run gets to end after its cancel before the task is answered without it."""


@dataclass
class _TaskEntry:
    ctx: TaskContext
    task: asyncio.Task | None = None
    responded: bool = False
    limit_hit: str = ""
    timer: asyncio.TimerHandle | None = None
    waits: int = 0
    """While the task waits for a person (an approval, a question) its time limit is paused: the prompt's own timeout (45
    minutes by default) bounds the wait, as in SAM-Go. waits counts the open prompts; timer_left is the time limit that
    remained when the first one opened.
    """
    timer_left: float = 0.0
    abandoned: bool = False
    """The run no longer speaks for its task: it was given up at shutdown or the end of a wake (abandoned), or a later
    delivery of the same task id has an entry of its own (superseded). Nothing of it reaches the task any more.
    """
    superseded: bool = False
    holds_slot: bool = False

    @property
    def run(self) -> _TaskRun:
        return self.ctx._run

    def claim_response(self) -> bool:
        """Publish-once guard shared by completion, failure and cancel paths."""
        if self.responded:
            return False
        self.responded = True
        return True


class _Channel:
    """A run's way to its task on the transport, which knows tasks by id only. Tied to the run's entry, so a run that
    no longer speaks for the task (abandoned, superseded by a later delivery of the same id, answered, cancelled)
    never reaches a later delivery: it sends no step, asks nobody and calls no peer."""

    attached = True

    def __init__(self, runner: _TaskRunner, task_id: str, streaming: bool):
        self._runner = runner
        self._task_id = task_id
        self.streaming = streaming
        self._entry: _TaskEntry | None = None

    def _may_report(self) -> bool:
        """Steps (and streamed text, progress) go out while the run speaks for its task: registered, or answered by
        this run and not cancelled, while the transport still holds that answered task."""
        entry = self._entry
        return entry is not None and not (entry.abandoned or entry.superseded or entry.run.cancelled)

    def _may_act(self) -> bool:
        """Prompts and peer calls only while the run's entry is the task's registered, unanswered one."""
        entry = self._entry
        return (
            entry is not None
            and self._runner.tasks.get(self._task_id) is entry
            and not entry.responded
            and not (entry.abandoned or entry.superseded)
        )

    def _refused(self, what: str) -> None:
        if self._entry is not None:
            self._entry.run.note_late_work(what)

    async def send(self, event: StepEvent) -> bool:
        if not self._may_report():
            self._refused(f"step {event.kind}")
            return False
        return await self._runner.transport._send_event(self._task_id, event)

    async def ask(self, prompt: Prompt) -> PromptAnswer:
        entry = self._entry
        if entry is None or not self._may_act():
            self._refused("ask the user")
            return PromptAnswer(prompt.request_id, CANCELLED)
        self._runner.pause_time_limit(entry)
        try:
            return await self._runner.transport._ask(self._task_id, prompt)
        finally:
            self._runner.resume_time_limit(entry)

    async def call_peer(self, agent: str, task_description: str, call_id: str) -> PeerResult:
        if not self._may_act():
            self._refused("call another agent")
            return PeerResult(agent, "error", "the task's run no longer holds the task")
        return await self._runner.transport._call_peer(PeerCall(self._task_id, agent, task_description, call_id))

    def peers(self) -> list[PeerInfo]:
        peers = self._runner.transport._peers()
        declared = self._runner.config.peers
        return [p for p in peers if p.name in declared] if declared else peers


class _TaskRunner:
    """The agent's tasks: the registry, the handler run, per-task limits, concurrency, cancel and the final outcome."""

    def __init__(
        self,
        *,
        config: AgentConfig,
        handler: Handler,
        transport: Transport,
        stop_state: StopState,
        audit: AuditLogger,
        metrics: Metrics,
        display_name: str,
        settling: TrackedTasks,
    ):
        self.config = config
        self.transport = transport
        self._handler = handler
        self._stop_state = stop_state
        self._audit = audit
        self._metrics = metrics
        self._display_name = display_name
        self._settling = settling
        self.tasks: dict[str, _TaskEntry] = {}
        self.left_behind = 0
        """Runs the agent stopped waiting for: abandoned at shutdown, or answered without them after their cancel. One may
        still be going (a cancel cannot reach a framework's run in a thread) and keep the process alive.
        """
        self._holders: weakref.WeakValueDictionary[str, _TaskEntry] = weakref.WeakValueDictionary()
        """The entry that last held each task id, while anything still refers to it (its run's work, a channel): a later
        delivery of the id marks it superseded.
        """
        self._slots = asyncio.Semaphore(config.max_concurrent_tasks) if config.max_concurrent_tasks > 0 else None
        self._keepalive_task: asyncio.Task[None] | None = None

    @property
    def active(self) -> int:
        return len(self.tasks)

    async def on_task(self, task: IncomingTask) -> None:
        """Accept a task from any transport and start running its handler.

        A task is refused, answered failed, while SAM has the agent stopped and when its call depth is over the limit;
        the stop check lives here so it holds for every transport. An accepted task registers as the holder of its id,
        marking an earlier delivery of the same id superseded, and starts its time limit.
        """
        cfg = self.config
        stopped = self._stop_state.reason
        if stopped:
            log.warning("refusing task: SAM has stopped this agent task=%s reason=%s", task.task_id, stopped)
            await self.transport._finish(task.task_id, TaskOutcome("failed", AGENT_STOPPED_TEXT))
            return
        if cfg.max_call_depth > 0 and task.call_depth > cfg.max_call_depth:
            log.warning("rejecting task=%s call_depth=%d max=%d", task.task_id, task.call_depth, cfg.max_call_depth)
            await self.transport._finish(
                task.task_id,
                TaskOutcome("failed", f"Call depth {task.call_depth} exceeds maximum {cfg.max_call_depth}"),
            )
            return
        entry: _TaskEntry
        text = task.text
        if not text and task.input is not None:
            text = _input_as_prompt(task.input)
        invocation = {"workflow_name": task.workflow.name, "node_id": task.workflow.node_id} if task.workflow else None
        ctx = TaskContext._new(
            task_id=task.task_id,
            context_id=task.context_id,
            text=text,
            agent_name=cfg.name,
            channel=(channel := _Channel(self, task.task_id, task.streaming)),
            files=task.files,
            user_id=task.user_id,
            call_depth=task.call_depth,
            structured_input=task.input,
            invocation=invocation,
            audit=self._audit,
            stream_thinking=cfg.stream_thinking,
            max_llm_calls=cfg.max_llm_calls,
            on_limit=lambda reason: self._stop_at_limit(entry, reason),
            metadata=task.metadata,
            request_context=task.request_context,
            metrics=self._metrics,
            display_name=self._display_name,
            run_based=task.session_behavior == RUN,
            tool_scopes={t.name: list(t.required_scopes) for t in cfg.tools if t.required_scopes},
            declared_tool_names=[t.name for t in cfg.tools],
            granted_tool_scopes=task.granted_tool_scopes,
            can_prompt_user=task.can_prompt_user,
            agent_stop_reason=lambda: self._stop_state.reason,
        )
        ctx._run.on_usage = self.transport._usage_sink(task.task_id)
        entry = _TaskEntry(ctx=ctx)
        channel._entry = entry
        earlier = self._holders.get(task.task_id)
        if earlier is not None:
            earlier.superseded = True
        self._holders[task.task_id] = entry
        self.tasks[task.task_id] = entry
        if cfg.task_timeout_seconds > 0:
            entry.timer = asyncio.get_running_loop().call_later(
                cfg.task_timeout_seconds, self._stop_at_limit, entry, "task_timeout_seconds"
            )
        entry.task = asyncio.create_task(self._run_task(entry), name=f"sam-link-sdk-task-{task.task_id}")
        entry.task.add_done_callback(lambda done: self._run_ended(entry))
        log.info(
            "task accepted task=%s context=%s input_chars=%d files=%d",
            task.task_id,
            task.context_id,
            len(text),
            len(task.files),
        )
        if invocation is not None:
            log.info(
                "task is a workflow node task=%s workflow=%s node=%s",
                task.task_id,
                invocation["workflow_name"],
                invocation["node_id"],
            )

    async def on_cancel(self, task_id: str) -> None:
        entry = self.tasks.get(task_id)
        if entry is None:
            log.info("cancel for unknown task=%s", task_id)
            return
        if not entry.claim_response():
            log.info("cancel ignored, task already responding task=%s", task_id)
            return
        entry.run.cancel()
        if entry.task is not None:
            entry.task.cancel()
        log.info("task cancelled task=%s", task_id)
        await self._finish(entry, TaskOutcome("canceled", answer_to_cancel=True))

    def wants_events(self, task_id: str) -> bool:
        entry = self.tasks.get(task_id)
        return entry is not None and not entry.run.cancelled

    async def _run_task(self, entry: _TaskEntry) -> None:
        """Run the handler for one task and answer it, whatever the handler does.

        The answer follows what ended the run. A limit (time or model calls) answers failed with the limit's message. A
        stop answers as the stop does, including when the handler swallowed the cancel and returned or a framework
        wrapped the halt in its own exception; the stop's outcome stands whatever the agent has applied yet (the offline
        grace running out reads as stopped before `on_control`). A handler failure answers failed, classified as a model
        failure only when the model, not a tool, was the last step, since a tool may call S3 or another model API. Text
        is streamed before the final answer, as SAM's own agents do, so clients that render only streamed text (`sam
        task send`) see it.

        Some endings answer nothing. An abandoned run claimed nothing, and the caller's own cancel answers the task
        itself. A cancel from outside the agent (a loop shutting down, user code, a library that cancelled the run and
        swallowed the cancel) gives the delivery back unanswered for SAM to deliver again, which beats a failed answer;
        if the give-back cannot go out because the loop is closing, the delivery returns to SAM when the connection
        closes. A process that is ending leaves the delivery unacknowledged for the same reason.

        A cancellation the handler met while nothing cancelled the run (an inner task a framework cancelled) is a
        failure that is answered, so the delivery is not held unanswered. A task stopped while it waits for a slot is
        answered like one stopped while running.
        """
        ctx = entry.ctx
        started = time.monotonic()
        error_type: str | None = None
        try:
            try:
                if self._slots is not None:
                    await self._slots.acquire()
                    entry.holds_slot = True
                with bind_task(ctx):
                    out: Any = self._handler(ctx)
                    if inspect.isawaitable(out):
                        out = await out
                files: list[File] = list(ctx._run.output_files)
                data = None
                if isinstance(out, TaskResult):
                    text, state, data = out.text, out.state, out.data
                    files += out.files
                elif isinstance(out, str):
                    text, state = out, "completed"
                else:
                    raise TypeError(f"a handler answers with a str or a TaskResult, not {type(out).__name__}")
                if entry.limit_hit:
                    raise asyncio.CancelledError
                if state == "completed" and text and not ctx._run.text_streamed:
                    await ctx.emit_text(text)
                files, too_large = _within_budget(files, self.config.max_inline_file_bytes)
                if too_large:
                    log.warning("files left out of final task=%s count=%d", ctx.task_id, len(too_large))
                    text += f"\n\nNote: {len(too_large)} file(s) were too large to return and were not included: {', '.join(too_large)}."
                outcome = TaskOutcome(state, text, files, data)
                error_type = "" if state == "completed" else "error"
            except asyncio.CancelledError:
                if entry.limit_hit:
                    error_type = "timeout" if entry.limit_hit == "task_timeout_seconds" else "error"
                    outcome = TaskOutcome("failed", self._limit_message(entry.limit_hit))
                elif ctx._run.cancelled or entry.abandoned:
                    if ctx._run.cancelled:
                        error_type = "error"
                    return
                else:
                    own = asyncio.current_task()
                    if own is not None and own.cancelling() > 0:
                        log.warning(
                            "task run cancelled from outside the agent; given back for SAM to deliver again task=%s",
                            ctx.task_id,
                        )
                        self._give_back(entry)
                        raise
                    log.error("handler failed task=%s err=CancelledError", ctx.task_id)
                    error_type, outcome = "error", TaskOutcome("failed", error=TaskError())
            except (KeyboardInterrupt, SystemExit, GeneratorExit):
                raise
            except BaseException as exc:
                halted = halt_of_task(exc, ctx.task_id)
                if halted is not None:
                    if halted.reason == HALT_CANCELLED:
                        error_type = "error"
                    stopped = self._halted_outcome(entry, halted.reason)
                    if stopped is None:
                        return
                    error_type, outcome = stopped
                elif (limited := limit_message(exc)) is not None:
                    log.warning("task stopped at its framework's limit task=%s err=%s", ctx.task_id, type(exc).__name__)
                    error_type, outcome = "error", TaskOutcome("failed", limited)
                else:
                    log.error("handler failed task=%s err=%s", ctx.task_id, type(exc).__name__)
                    from_the_model = isinstance(exc, Exception) and not ctx._run.tool_after_model
                    failure = ctx._run.llm_failure or (classify_llm_failure(exc) if from_the_model else None)
                    error_type = llm_error_type(failure.category) if failure is not None else "error"
                    error = TaskError(failure.category, failure.provider) if failure is not None else TaskError()
                    outcome = TaskOutcome("failed", error=error)
            if entry.claim_response():
                await asyncio.shield(self._spawn_finish(entry, outcome))
        finally:
            self._release_slot(entry)
            if error_type is not None:
                ctx._run.close_llm_calls(error_type)
                self._metrics.histogram(
                    OPERATION_DURATION,
                    time.monotonic() - started,
                    {
                        "type": "agent",
                        "component.name": self._display_name,
                        "operation.name": "execute",
                        "error.type": error_type,
                    },
                )

    def _release_slot(self, entry: _TaskEntry) -> None:
        if entry.holds_slot and self._slots is not None:
            entry.holds_slot = False
            self._slots.release()

    def _halted_outcome(self, entry: _TaskEntry, reason: str) -> tuple[str, TaskOutcome] | None:
        """The (metrics error type, outcome) of a run that stopped at a TaskHalted, or None when it has nothing to
        answer: cancelled (the cancel answers), answered or abandoned (already answered, or SAM delivers it again)."""
        ctx = entry.ctx
        if reason in (HALT_CANCELLED, HALT_ANSWERED) or entry.abandoned:
            log.info("task run ended at a stop check task=%s reason=%s", ctx.task_id, reason)
            return None
        stop = entry.limit_hit or reason
        entry.limit_hit = stop
        ctx._run.halt()
        log.warning("task stopped at a stop check task=%s reason=%s", ctx.task_id, stop)
        error_type = "timeout" if stop == "task_timeout_seconds" else "error"
        return error_type, TaskOutcome("failed", self._limit_message(stop))

    def _limit_message(self, limit: str) -> str:
        if limit == "max_llm_calls":
            return f"Stopped: the task reached the limit of {self.config.max_llm_calls} model calls."
        if limit == "task_timeout_seconds":
            return f"Stopped: the task ran longer than the limit of {self.config.task_timeout_seconds:g} seconds."
        if limit == REASON_DISCONNECTED:
            return "Stopped: this agent lost contact with SAM for longer than it may run without it."
        return "Stopped: an operator stopped this agent from SAM."

    def stop_all(self, reason: str) -> None:
        """SAM stopped the agent: stop every running task, which its run loop answers as stopped."""
        for entry in list(self.tasks.values()):
            self._stop_at_limit(entry, reason)

    def _stop_at_limit(self, entry: _TaskEntry, limit: str) -> None:
        """Cancel a task that passed a limit, or that SAM's stop stopped (limit is then its reason); its run loop
        answers with a failed outcome."""
        if entry.limit_hit or entry.responded or entry.task is None:
            return
        entry.limit_hit = limit
        entry.run.halt()
        if limit in LIMITS:
            value = self.config.max_llm_calls if limit == "max_llm_calls" else self.config.task_timeout_seconds
            log.warning("task stopped at limit task=%s limit=%s value=%s", entry.ctx.task_id, limit, value)
            self._audit.limit_reached(
                user_id=entry.ctx.user_id,
                task_id=entry.ctx.task_id,
                agent_name=self.config.name,
                limit=limit,
                value=value,
            )
        else:
            log.warning("task stopped by the kill switch task=%s reason=%s", entry.ctx.task_id, limit)
        entry.task.cancel()
        asyncio.get_running_loop().call_later(CANCEL_DEADLINE_SECONDS, self._answer_uncancelled, entry)

    def _answer_uncancelled(self, entry: _TaskEntry) -> None:
        """A run that is still going after its cancel (a handler that swallows it, or is stuck in code that cannot be
        cancelled) is left behind: the task is answered with the limit's message and its slot is freed. The run
        answers nothing when it ends later, as the answer is claimed."""
        if entry.task is None or entry.task.done() or not entry.claim_response():
            return
        log.error(
            "task still running after its cancel; answering it without the run task=%s limit=%s",
            entry.ctx.task_id,
            entry.limit_hit,
        )
        self.left_behind += 1
        self._release_slot(entry)
        outcome = TaskOutcome("failed", self._limit_message(entry.limit_hit))
        self._spawn_finish(entry, outcome)

    async def _finish(self, entry: _TaskEntry, outcome: TaskOutcome) -> None:
        task_id = entry.ctx.task_id
        entry.run.mark_answered()
        try:
            await self.transport._finish(task_id, outcome)
        finally:
            if self.tasks.get(task_id) is entry:
                del self.tasks[task_id]
            if entry.timer is not None:
                entry.timer.cancel()
        log.info(
            "task finished task=%s llm_calls=%d events=%d",
            task_id,
            entry.run.usage.llm_calls,
            entry.run.events_published,
        )

    def pause_time_limit(self, entry: _TaskEntry) -> None:
        entry.waits += 1
        if entry.waits == 1 and entry.timer is not None:
            entry.timer_left = max(entry.timer.when() - asyncio.get_running_loop().time(), 0.0)
            entry.timer.cancel()
            entry.timer = None

    def resume_time_limit(self, entry: _TaskEntry) -> None:
        entry.waits -= 1
        if entry.waits == 0:
            entry.run.last_event_at = time.monotonic()
        if entry.waits == 0 and entry.timer_left > 0 and not entry.responded and not entry.limit_hit:
            entry.timer = asyncio.get_running_loop().call_later(
                entry.timer_left, self._stop_at_limit, entry, "task_timeout_seconds"
            )
            entry.timer_left = 0.0

    def _run_ended(self, entry: _TaskEntry) -> None:
        """The task's run is over, however it ended. A run cancelled before its first step never ran ``_run``'s own
        handlers: a task stopped at a limit or by SAM's stop is answered here, and an abandoned one (shutdown, a
        wake cut short) leaves the registry, so neither stays registered unanswered."""
        task_id = entry.ctx.task_id
        if self.tasks.get(task_id) is not entry or entry.responded:
            return
        if entry.limit_hit and entry.claim_response():
            outcome = TaskOutcome("failed", self._limit_message(entry.limit_hit))
            self._spawn_finish(entry, outcome)
            return
        self.tasks.pop(task_id, None)
        if entry.timer is not None:
            entry.timer.cancel()

    def _give_back(self, entry: _TaskEntry) -> None:
        """Return a task to SAM unanswered: the run no longer holds it (nothing of it reaches the task), it leaves the
        table, and the transport gives the delivery back, tracked so a shutdown waits for it."""
        if not entry.claim_response():
            return
        entry.abandoned = True
        entry.run.halt()
        task_id = entry.ctx.task_id
        if self.tasks.get(task_id) is entry:
            del self.tasks[task_id]
        if entry.timer is not None:
            entry.timer.cancel()
        give_back = getattr(self.transport, "_give_back", None)
        if give_back is None:
            return
        self._settling.spawn(give_back(task_id))

    def _spawn_finish(self, entry: _TaskEntry, outcome: TaskOutcome) -> asyncio.Task:
        """Answer the task, tracked so a shutdown waits for the answer to go out."""
        return self._settling.spawn(self._finish(entry, outcome))

    def abandon_running(self) -> None:
        """Stop every running task without answering it, before the agent disconnects: SAM gives each back to the
        queue (it was not acknowledged), and nothing of it keeps running without a link to report on. The runs leave
        the table at once and claim their answer, so a run that ignores the cancel never answers, nor holds up a
        later delivery of its task (the next wake of a reused agent)."""
        running = [
            entry
            for entry in self.tasks.values()
            if entry.task is not None and not entry.task.done() and entry.claim_response()
        ]
        if running:
            log.warning("abandoning running tasks; SAM delivers them again count=%d", len(running))
        self.left_behind += len(running)
        for entry in running:
            entry.run.halt()
            entry.abandoned = True
            del self.tasks[entry.ctx.task_id]
            if entry.timer is not None:
                entry.timer.cancel()
            if entry.task is not None:
                entry.task.cancel()

    async def let_stopped_answer(self) -> None:
        """Before a disconnect: tasks already stopped (SAM's stop, a limit) answer, and answers under way go out,
        within STOPPED_ANSWER_SECONDS for both together."""
        deadline = time.monotonic() + STOPPED_ANSWER_SECONDS
        stopping = [
            e.task
            for e in self.tasks.values()
            if e.limit_hit and not e.responded and e.task is not None and not e.task.done()
        ]
        if stopping:
            await asyncio.wait(stopping, timeout=STOPPED_ANSWER_SECONDS)
        unfinished = await self._settling.wait(max(0.0, deadline - time.monotonic()))
        if unfinished:
            log.warning("disconnecting before answers or stop announcements under way ended count=%d", unfinished)

    def running(self) -> list[asyncio.Task]:
        return [entry.task for entry in self.tasks.values() if entry.task is not None and not entry.task.done()]

    async def drain(self) -> None:
        """Let the running tasks finish for up to ``shutdown_grace_seconds``, then what the transport still holds for a
        delivery of its task that never came goes out as it is (``_end_redelivery_waits``, which the agent link has),
        so the grace is the time SAM has to deliver such a task again before its answer is sent."""
        grace = self.config.shutdown_grace_seconds
        deadline = time.monotonic() + grace
        running = self.running()
        if running and grace > 0:
            log.info("draining in-flight tasks count=%d grace_seconds=%s", len(running), grace)
            _, unfinished = await asyncio.wait(running, timeout=grace)
            if unfinished:
                log.warning("abandoning in-flight tasks after the grace period count=%d", len(unfinished))
        end_waits = getattr(self.transport, "_end_redelivery_waits", None)
        if end_waits is not None:
            await end_waits(max(0.0, deadline - time.monotonic()))

    def start_keepalive(self) -> None:
        self._keepalive_task = asyncio.create_task(self._keepalive(), name="sam-link-sdk-keepalive")

    def cancel_keepalive(self) -> None:
        if self._keepalive_task is not None:
            self._keepalive_task.cancel()

    async def _keepalive(self) -> None:
        """Report "Still working" on a task silent for ``keepalive_seconds``, as SAM's idle timeout would end it. Not
        while the task waits for its user: SAM's own agents send nothing then, and a working-state event would put
        an input-required task back to working and restart SAM's idle timeout while the question is still open."""
        idle = self.config.keepalive_seconds
        while True:
            await asyncio.sleep(min(idle / 4, 30.0))
            now = time.monotonic()
            for entry in list(self.tasks.values()):
                if entry.limit_hit or entry.responded or entry.waits != 0 or now - entry.run.last_event_at < idle:
                    continue
                try:
                    await entry.ctx.progress("Still working")
                except Exception:
                    log.exception("keepalive failed task=%s", entry.ctx.task_id)


def _input_as_prompt(structured_input: Any) -> str:
    """The prompt for a workflow node that has no instruction: the input's "message", else the input as JSON."""
    if isinstance(structured_input, dict) and isinstance(structured_input.get("message"), str):
        return structured_input["message"]
    return json.dumps(structured_input, sort_keys=True, ensure_ascii=False)


def _within_budget(files: list[File], budget: int) -> tuple[list[File], list[str]]:
    kept, dropped, used = [], [], 0
    for f in files:
        encoded = 4 * ((len(f.content) + 2) // 3)
        if used + encoded > budget:
            dropped.append(f.name or "unnamed")
            continue
        used += encoded
        kept.append(f)
    return kept, dropped
