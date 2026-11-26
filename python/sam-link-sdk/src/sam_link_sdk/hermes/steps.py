# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Hermes Agent hooks that report a mesh task's model and tool steps to the task.

Hermes runs an agent turn on a plain thread and tells plugins about it through process-wide hooks, so one set of
callbacks serves every task: each hook payload carries the agent's session id, which names the task's `Run`.
`run_agent` registers a run for the length of a turn; a hook for any other session (a Hermes agent that is not a
mesh task) is ignored, except a tool call: refused while a mesh run is open (it may be a subagent SAM was not told
about), and always for a session that was ever a mesh run's subagent.

A subagent (``delegate_task``) belongs to its parent's run from ``subagent_start`` to ``subagent_stop``. Hermes runs
a top-level agent's delegations in the background, so a subagent can outlive its parent's turn: once the turn ends the
run is closed, its subagents are interrupted, and until they stop every tool call they make is refused (their model
calls can no longer be reported to the finished task).

The hooks run on the agent's thread, so they hand work to the mesh's event loop and wait for it: steps keep their
order, and a refused tool call is decided before the tool runs.
"""

from __future__ import annotations

import asyncio
import logging
import threading
import time
from collections import OrderedDict, defaultdict, deque
from collections.abc import Callable, Coroutine, Iterable
from typing import Any

from .._adapter import (
    SessionInUse,
    ToolScopes,
    access_denied,
    approve,
    check_usage_read,
    error_result,
    is_answered,
    may_be_lost,
    model_finished,
    model_started,
    new_call_id,
    note_halt,
    over,
    parse_args,
    plain_result,
    refusal_steps,
    reported_result,
    tool_finished,
    tool_started,
    usage_of,
)
from .._drift import require_members
from ..context import TaskContext
from ..events import StepEvent
from ..llm_errors import LLMFailure, categorize, provider_label
from ..usage import Usage

log = logging.getLogger("sam_link_sdk.hermes")

SIGNAL_TIMEOUT_SECONDS = 30.0

HERMES = ("sam_link_sdk.hermes", "Hermes Agent", "hermes-agent", "checkout b65e078c")
"""Hermes is not a published library: the adapter is tested against one checkout, and reaches internals it does not promise
to keep (the plugin manager's hook registry, and a switch on the agent; see run.py).
"""


class Run:
    """One mesh task's turn: what the hooks need to report it and to refuse tool calls."""

    def __init__(self, ctx: TaskContext, loop: asyncio.AbstractEventLoop, session_id: str, approval: Any = None):
        self.ctx = ctx
        self.loop = loop
        self.session_id = session_id
        self.approval = approval
        self.model = "default"
        self.scopes = ToolScopes(ctx)
        self.tools: dict[str, Any] = {}
        self.closed = False
        """The turn ended or was stopped: only its subagents may still call, and are refused."""
        self.closed_at = 0.0
        self.interrupt: Callable[[], None] | None = None
        """Interrupts the agent (run_agent sets it): a Hermes hook can block a tool call but not a model call, so a task
        stopped while its turn runs is ended by interrupting the agent, once.
        """
        self._interrupted = False
        self._started: set[str] = set()
        self._call_ids: dict[str, deque[str]] = defaultdict(deque)
        self.claimed: set[str] = set()
        """The tool call ids this run's sessions hold until its turn ends (claim_call_ids); under _lock."""

    def stop_if_halted(self) -> bool:
        """Interrupt the agent once its task's work must not go on (stopped, cancelled, answered, or the agent
        stopped); whether it must not.

        The interrupt is sent on a thread of its own, because Hermes' hard stop may wait for a compression commit in
        flight, which must not hold up the hook (and the conversation thread) that saw the stop. A closed run interrupts
        nothing: its agent object may already serve another run.
        """
        if not self.ctx._run.halt_reason:
            return False
        note_halt()
        with _lock:
            fire = not self.closed and not self._interrupted and self.interrupt is not None
            self._interrupted = self._interrupted or fire
        if fire:
            threading.Thread(
                target=self._interrupt, name=f"sam-link-sdk-interrupt-{self.ctx.task_id}", daemon=True
            ).start()
        return True

    def _interrupt(self) -> None:
        interrupt = self.interrupt
        try:
            if interrupt is not None:
                interrupt()
        except Exception as exc:
            log.warning(
                "interrupting a stopped Hermes agent failed task=%s err=%s", self.ctx.task_id, type(exc).__name__
            )

    def call(self, coro: Coroutine[Any, Any, Any], timeout: float | None = None) -> Any:
        """Run `coro` on the mesh's loop and wait for it, from the agent's thread."""
        return asyncio.run_coroutine_threadsafe(coro, self.loop).result(timeout)

    def emit(self, event: StepEvent) -> None:
        """Report a step; a lost step must not stop the agent."""
        with may_be_lost(self.ctx):
            self.call(self.ctx.emit_event(event), SIGNAL_TIMEOUT_SECONDS)

    @property
    def stopped(self) -> bool:
        return bool(self.ctx._run.halt_reason)

    def start_call(self, name: str, call_id: str) -> None:
        self._started.add(call_id)
        self._call_ids[name].append(call_id)

    def finish_call(self, call_id: str) -> bool:
        """True when this call was announced (so its result is reported once)."""
        if call_id in self._started:
            self._started.discard(call_id)
            return True
        return False

    def take_call_id(self, name: str) -> str:
        """The id of the oldest announced call to `name` (a tool handler asks for it; Hermes does not pass it)."""
        queue = self._call_ids.get(name)
        return queue.popleft() if queue else ""


_runs: dict[str, Run] = {}
"""Session id -> the mesh task's run it belongs to: the task's own agent, and the subagents Hermes delegates to from it
(``delegate_task``), each registered when Hermes announces it (``subagent_start``). Guarded by _lock.
"""
_children: dict[str, set[str]] = defaultdict(set)
_ever_children: OrderedDict[str, float] = OrderedDict()
"""Every session that was a mesh run's subagent -> when it was last heard of (_clock), oldest first; under _lock."""
_lock = threading.Lock()
_clock = time.monotonic

CLOSED_RUN_SECONDS = 3600.0
"""Bounds for a long-lived process. A closed run's subagents that never report subagent_stop are let go (with the run's
TaskContext) an hour after it closed; they stay refused as former subagents. A former subagent is remembered for a day
after it was last heard of, and at most the most recent MAX_CHILD_SESSIONS; one forgotten is an unknown session again:
refused while any mesh run is open in the process, not SAM's to judge once none is.
"""
CHILD_MEMORY_SECONDS = 86400.0
MAX_CHILD_SESSIONS = 10_000


def _prune() -> None:
    """Apply the bounds above; called under _lock."""
    now = _clock()
    for session, run in list(_runs.items()):
        if run.closed and session != run.session_id and now - run.closed_at > CLOSED_RUN_SECONDS:
            del _runs[session]
            children = _children.get(run.session_id)
            if children is not None:
                children.discard(session)
                if not children:
                    del _children[run.session_id]
    for session, seen in list(_ever_children.items()):
        if now - seen <= CHILD_MEMORY_SECONDS:
            break
        if session not in _runs:
            del _ever_children[session]
    excess = len(_ever_children) - MAX_CHILD_SESSIONS
    for session in [s for s in _ever_children if s not in _runs][: max(excess, 0)]:
        del _ever_children[session]


def _heard_of(child: str) -> None:
    """Note a former or current subagent as just heard of; called under _lock."""
    _ever_children[child] = _clock()
    _ever_children.move_to_end(child)


UNKNOWN_SESSION = (
    "This agent's session belongs to no mesh task while mesh tasks run in this process, so SAM cannot "
    "check this tool call; it was not run."
)
CLOSED_RUN = (
    "The mesh task this agent was delegated from has ended, so SAM can no longer check this tool call; it was not run."
)


def register_run(run: Run) -> None:
    """Route the hooks of `run`'s session to it. A session id another live run holds is refused (SessionInUse)."""
    with _lock:
        held = _runs.get(run.session_id)
        if held is not None and held is not run:
            raise SessionInUse(
                f"session {run.session_id} is in use by another mesh task's agent: build a new agent "
                "per task, with agent_defaults(ctx)"
            )
        _runs[run.session_id] = run


def close_run(run: Run) -> None:
    """Close `run` (its task was stopped): every session mapped to it, its own included, is refused from now on,
    and it no longer counts as an open run (``runs_active``), so a tool that ignores the interrupt does not hold up
    other Hermes agents in the process."""
    with _lock:
        run.closed = True
        run.closed_at = _clock()


def end_turn(run: Run) -> list[str]:
    """`run`'s turn has returned: its own session is let go, and the subagents still running stay mapped to the
    closed run (refused) until they stop. Returns their session ids, to interrupt."""
    with _lock:
        run.closed, run.closed_at = True, _clock()
        run.claimed.clear()
        if _runs.get(run.session_id) is run:
            del _runs[run.session_id]
        children = sorted(c for c in _children.get(run.session_id, set()) if _runs.get(c) is run)
        if not children:
            _children.pop(run.session_id, None)
        return children


def unregister_run(run: Run) -> None:
    end_turn(run)


def run_for(session_id: Any) -> Run | None:
    with _lock:
        _prune()
        return _runs.get(str(session_id or ""))


def was_child(session_id: Any) -> bool:
    with _lock:
        _prune()
        session = str(session_id or "")
        if session in _ever_children:
            _heard_of(session)
            return True
        return False


def runs_active() -> bool:
    """Whether a mesh run is open (a closed run's subagents do not count: they are refused by their run)."""
    with _lock:
        return any(not run.closed for run in _runs.values())


def subagent_start(**kw: Any) -> None:
    """A subagent Hermes delegates to from a mesh task's agent belongs to that task: its model calls and tool calls
    are reported, checked and stopped as the parent's (it inherits the parent's toolsets, scoped ones included)."""
    parent, child = str(kw.get("parent_session_id") or ""), str(kw.get("child_session_id") or "")
    with _lock:
        run = _runs.get(parent)
        if run is None or not child:
            return
        _runs[child] = run
        _children[run.session_id].add(child)
        _heard_of(child)
        _prune()


def subagent_stop(**kw: Any) -> None:
    child = str(kw.get("child_session_id") or "")
    with _lock:
        if child in _ever_children:
            _heard_of(child)
        run = _runs.get(child)
        if run is not None and child != run.session_id:
            del _runs[child]
            children = _children.get(run.session_id)
            if children is not None:
                children.discard(child)
                if not children and run.closed:
                    del _children[run.session_id]


def _call_id_of(tool_call: Any) -> str:
    """A tool call's id as Hermes pairs it with its result (``call_id`` before ``id``, the call half of a composite
    ``call|item`` id)."""
    if isinstance(tool_call, dict):
        raw = tool_call.get("call_id") or tool_call.get("id")
    else:
        raw = getattr(tool_call, "call_id", None) or getattr(tool_call, "id", None)
    return str(raw or "").strip().split("|", 1)[0].strip()


def claim_history(run: Run, messages: Iterable[Any]) -> None:
    """Hold for `run` the tool call ids of the conversation it replays, as Hermes counts them taken."""
    ids = {
        _call_id_of(call)
        for message in messages
        if isinstance(message, dict) and message.get("role") == "assistant"
        for call in message.get("tool_calls") or ()
    }
    with _lock:
        run.claimed.update(i for i in ids if i)


def claim_call_ids(run: Run, tool_calls: list) -> None:
    """Give each tool call of a model response an id that no open mesh run in this process holds, its own included,
    and hold it until the run's turn ends.

    Hermes runs a ``pre_tool_call`` callback once at a time per tool call id across the process, and refuses a call
    whose id is already in that hook (hermes_cli/plugins_dispatch.py): an approval waits there, and providers that
    number their calls (``call_0``) give two conversations the same ids. A taken id is renamed in place by Hermes' own
    duplicate repair (``<id>_d<n>``), as Hermes already does for an id its session has used, before Hermes reads the
    calls, so the steps, the hooks and the result the model is told all carry the new id."""
    with _lock:
        taken = set().union(*(r.claimed for r in set(_runs.values())))
        ids = [_call_id_of(tc) for tc in tool_calls]
        if any(i in taken for i in ids if i) or len(set(ids)) < len(ids):
            from agent.message_sanitization import uniquify_tool_call_ids

            uniquify_tool_call_ids(tool_calls, taken=taken)
            ids = [_call_id_of(tc) for tc in tool_calls]
        run.claimed.update(i for i in ids if i)


def _failed(status: Any) -> bool:
    """Whether Hermes marked the call failed."""
    return str(status or "").lower() in ("error", "failed")


def _usage_from(raw: Any) -> Usage:
    """Hermes' usage payload. Hermes is not a published package, so whether its ``input_tokens`` holds
    ``cache_read_tokens`` and ``cache_write_tokens`` cannot be checked against an installed version: a
    ``prompt_tokens`` count is taken as holding them, and otherwise the total decides (``usage_of``), so a cache token
    is never counted twice."""
    u = raw if isinstance(raw, dict) else {}
    prompt = u.get("prompt_tokens")
    return usage_of(
        input=prompt if prompt is not None else u.get("input_tokens"),
        output=u.get("output_tokens"),
        cache_read=u.get("cache_read_tokens"),
        cache_write=u.get("cache_write_tokens"),
        reasoning=u.get("reasoning_tokens"),
        total=u.get("total_tokens"),
        input_includes_cache=True if prompt is not None else None,
    )


def _block(message: str) -> dict:
    return {"action": "block", "message": message}


def pre_api_request(**kw: Any) -> None:
    """Report a model call when Hermes starts one.

    Hermes retrying the same call (a rate limit, say) is not another model call, so a retry is not reported.
    """
    run = run_for(kw.get("session_id"))
    if run is None or run.closed or run.stop_if_halted():
        return
    run.model = str(kw.get("model") or run.model)
    if int(kw.get("retry_count") or 0) > 0:
        return
    run.emit(model_started(run.model))


def post_api_request(**kw: Any) -> None:
    run = run_for(kw.get("session_id"))
    if run is None or run.closed or is_answered(run.ctx):
        return
    model = str(kw.get("response_model") or kw.get("model") or run.model)
    message = kw.get("assistant_message")
    tool_calls = getattr(message, "tool_calls", None) or []
    if tool_calls:
        claim_call_ids(run, tool_calls)
    calls = [{"id": tc.id or new_call_id(), "name": tc.name, "args": parse_args(tc.arguments)} for tc in tool_calls]
    usage = _usage_from(kw.get("usage"))
    text = str(getattr(message, "content", None) or "")
    check_usage_read("sam_link_sdk.hermes", model, kw.get("usage"), usage, text, calls)
    reasoning = getattr(message, "reasoning", None) or getattr(message, "reasoning_content", None)
    if reasoning:
        run.call(run.ctx.emit_thinking(str(reasoning)), SIGNAL_TIMEOUT_SECONDS)
        run.call(run.ctx.end_thinking(), SIGNAL_TIMEOUT_SECONDS)
    run.emit(model_finished(model, text, calls, usage))


def api_request_error(**kw: Any) -> None:
    """Note a failed model call so the task's error reply carries its category (the last attempt wins)."""
    run = run_for(kw.get("session_id"))
    if run is None or run.closed or is_answered(run.ctx):
        return
    raw_error = kw.get("error")
    error = raw_error if isinstance(raw_error, dict) else {}
    status = kw.get("status_code")
    category = categorize(
        status if isinstance(status, int) else None, str(error.get("message") or ""), type_=str(error.get("type") or "")
    )
    run.ctx._run.llm_failure = LLMFailure(category, provider_label(str(kw.get("provider") or "")))


def _permits(run: Run, name: str) -> bool:
    """The scope check, refusing when it cannot decide rather than raising, which would leave it to Hermes whether the
    tool runs."""
    try:
        return run.scopes.permits(name)
    except Exception as exc:
        log.warning(
            "refusing a tool call the scope check could not decide task=%s err=%s", run.ctx.task_id, type(exc).__name__
        )
        return False


def pre_tool_call(**kw: Any) -> dict | None:
    """Check a tool call before Hermes runs it, report its start and ask for approval where one is needed.

    Returns a block result when the call must not run: the task is over, the user lacks the tool's scopes, or approval
    was declined or failed. The check fails closed: an unknown session while mesh tasks run may be one of their
    subagents SAM was not told about, and one that was a mesh run's subagent stays one.
    """
    session = kw.get("session_id")
    run = run_for(session)
    if run is None:
        return _block(UNKNOWN_SESSION) if was_child(session) or runs_active() else None
    if run.closed:
        return _block(CLOSED_RUN)
    name = str(kw.get("tool_name") or "")
    raw_args = kw.get("args")
    args = raw_args if isinstance(raw_args, dict) else {}
    call_id = str(kw.get("tool_call_id") or new_call_id())
    ended = over(run.ctx, name)
    if ended:
        run.stop_if_halted()
        return _block(ended)
    if not _permits(run, name):
        for event in refusal_steps(name, dict(args), call_id):
            run.emit(event)
        return _block(access_denied(name))
    run.start_call(name, call_id)
    run.emit(tool_started(name, dict(args), call_id))
    approval = run.approval
    if approval is not None and approval.covers(name):
        try:
            refused = run.call(approve(run.ctx, name, args, approval.message, approval_timeout()))
        except Exception as exc:
            log.warning(
                "refusing a tool call whose approval failed task=%s err=%s", run.ctx.task_id, type(exc).__name__
            )
            refused = APPROVAL_FAILED
        if refused:
            run.finish_call(call_id)
            run.emit(tool_finished(name, error_result(refused), call_id))
            return _block(refused)
    return None


APPROVAL_FAILED = "The approval could not be asked, so the tool was not run."


def post_tool_call(**kw: Any) -> None:
    run = run_for(kw.get("session_id"))
    call_id = str(kw.get("tool_call_id") or "")
    if run is None or not run.finish_call(call_id):
        return
    data = plain_result(kw.get("result"))
    failed = _failed(kw.get("status"))
    if failed and kw.get("error_message"):
        data = kw.get("error_message")
    run.emit(tool_finished(str(kw.get("tool_name") or ""), reported_result(data, failed), call_id))


APPROVAL_MARGIN_SECONDS = 5.0
ANSWER_GRACE_SECONDS = 5.0


def approval_timeout() -> float | None:
    """How long an approval asked from ``pre_tool_call`` may wait for the user: a little less than Hermes lets that
    hook run (``plugins.hook_callback_timeout``), less the grace ``approve`` waits past it for an answer already on its
    way. Past the hook limit Hermes abandons the hook, refuses the call and skips this callback process-wide for a
    while; ending the prompt first, grace included, refuses the one call cleanly and reports it. None when Hermes does
    not bound the hook."""
    from hermes_cli.plugins import _resolve_hook_callback_timeout

    limit = _resolve_hook_callback_timeout()
    return max(limit - APPROVAL_MARGIN_SECONDS - ANSWER_GRACE_SECONDS, 1.0) if limit > 0 else None


HOOKS: dict[str, Callable[..., Any]] = {
    "pre_api_request": pre_api_request,
    "post_api_request": post_api_request,
    "api_request_error": api_request_error,
    "pre_tool_call": pre_tool_call,
    "post_tool_call": post_tool_call,
    "subagent_start": subagent_start,
    "subagent_stop": subagent_stop,
}


def _hook_registry() -> dict[str, list[Callable[..., Any]]]:
    """Hermes' process-wide hook lists. Hermes offers plugins a decorator for their own hooks but no way to add or
    remove a callback at run time, so the adapter edits the plugin manager's registry, checked here first."""
    from hermes_cli.plugins import get_plugin_manager

    manager = get_plugin_manager()
    require_members(*HERMES, manager, "PluginManager", ("_hooks",))
    return manager._hooks


def remove_hooks(extra: Iterable[tuple[str, Callable[..., Any]]]) -> None:
    """Take `extra` (hook name, callback) pairs back out of Hermes."""
    registry = _hook_registry()
    for name, callback in extra:
        if callback in registry.get(name, []):
            registry[name].remove(callback)


def install_hooks(extra: Iterable[tuple[str, Callable[..., Any]]] = ()) -> None:
    """Register the hooks with Hermes (idempotent), plus any `extra` (hook name, callback) pairs. Ours go first, so
    a tool call is announced before any other hook can hold it."""
    registry = _hook_registry()
    for name, callback in [*HOOKS.items(), *extra]:
        callbacks = registry.setdefault(name, [])
        if callback not in callbacks:
            callbacks.insert(0, callback)
