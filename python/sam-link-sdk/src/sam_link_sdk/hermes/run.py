# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Run a Hermes Agent on a mesh task.

A Hermes ``AIAgent`` keeps its conversation and runs a turn on a plain (blocking) thread, so a mesh task gets a fresh
agent from ``build(ctx)`` and `run_agent` runs its turn on a worker thread. Hermes is built to remember: it keeps
long-term memory, learns skills from what it does and stores every session. Those belong to the Hermes install, not
to the people who use it through the mesh, so `agent_defaults` turns them off for a mesh task and the conversation
comes from the task's ``contextId`` instead (``memory=``).
"""

from __future__ import annotations

import asyncio
import contextvars
import logging
import uuid
from collections.abc import Callable, Iterable
from typing import Any

from .._adapter import check_names_known, halts_bare, input_text, join_prompts, may_be_lost, over, request_prompt
from .._drift import FrameworkDrift, installed_version, require, require_members
from ..context import TaskContext, bind_task, tool_name
from ..limits import LimitReached
from ..memory import ConversationMemory
from ..prompting import Option, PromptTooLarge, Question
from .peer_tools import MeshTool, add_to_agent
from .steps import (
    HERMES,
    HOOKS,
    SIGNAL_TIMEOUT_SECONDS,
    Run,
    claim_history,
    close_run,
    end_turn,
    install_hooks,
    register_run,
)
from .tools import Approval

log = logging.getLogger("sam_link_sdk.hermes")

MESH_PLATFORM = "sam_link_sdk"
GATE_TIMEOUT_NOTE = "plugins.hook_callback_timeout"


def agent_defaults(ctx: TaskContext) -> dict[str, Any]:
    """``AIAgent`` keyword arguments for a mesh task's agent: ``AIAgent(**agent_defaults(ctx), model=..., ...)``.

    The agent gets a session id of its own, new for each delivery of the task (hooks tell runs apart by it: a run
    given up whose tool has not returned yet keeps its session, and a later delivery of the same task must not collide
    with it or reach its hooks), no long-term memory, no skill learning from the task, and none of the working
    directory's context files.
    """
    return {
        "session_id": f"sam-{ctx.task_id}-{uuid.uuid4().hex[:12]}",
        "platform": MESH_PLATFORM,
        "user_id": ctx.user_id or "",
        "skip_memory": True,
        "skip_background_review": True,
        "skip_context_files": True,
        "quiet_mode": True,
    }


AGENT_MEMBERS = (
    "session_id",
    "tools",
    "valid_tool_names",
    "ephemeral_system_prompt",
    "run_conversation",
    "hard_interrupt",
    "stream_delta_callback",
    "tool_gen_callback",
)
"""What run_agent reads or calls on the AIAgent. It also sets two attributes Hermes reads: `clarify_callback`, and
`_persist_disabled`, a private switch (Hermes has no public way to keep a session out of its own store; its background
reviewers set this one). Only reading can be checked: if Hermes stops reading them, the setting is lost without an
error, which is why the Hermes checkout is pinned (README).
"""


def check_hermes(agent: Any) -> None:
    """Raise FrameworkDrift if this Hermes checkout lacks a hook, module or agent member the adapter uses."""
    require(
        *HERMES,
        [
            "hermes_cli.plugins:get_plugin_manager",
            "tools.approval_context:set_hermes_interactive_context",
            "tools.approval_context:reset_hermes_interactive_context",
            "tools.terminal_tool:set_approval_callback",
            "tools.registry:registry.register",
            "hermes_cli.plugins:VALID_HOOKS",
            "hermes_cli.plugins:_resolve_hook_callback_timeout",
            "tools.async_delegation:interrupt_for_session",
            "agent.message_sanitization:uniquify_tool_call_ids",
        ],
    )
    require_members(*HERMES, agent, "AIAgent", AGENT_MEMBERS)
    from hermes_cli.plugins import VALID_HOOKS

    missing = sorted(set(HOOKS) - set(VALID_HOOKS))
    if missing:
        raise FrameworkDrift(
            HERMES[0],
            HERMES[1],
            installed_version(HERMES[2]),
            HERMES[3],
            [f"hermes_cli.plugins.VALID_HOOKS[{name!r}]" for name in missing],
        )


def _check_approval(approval: Approval | None, agent: Any, tools: list[MeshTool]) -> None:
    """A name the approval covers that is none of this agent's tools is a ValueError: next to the real tool, a
    misspelt name would leave that tool unguarded. Checked once the run's tools are known, before any is hidden."""
    if approval is None or approval.names is None:
        return
    known = {tool_name(t) for t in agent.tools or []} | set(agent.valid_tool_names or ()) | {t.name for t in tools}
    check_names_known(approval.names, known, "with_approval names")


def _hide_denied_tools(run: Run, agent: Any) -> None:
    """Leave out of this run the tools whose required scopes (AgentConfig.tools) the task's user lacks, so the
    model is never offered them (a denial is audited once); the hook refuses a call to one anyway."""
    visible = run.scopes.visible(agent.tools or [])
    if len(visible) != len(agent.tools or []):
        agent.tools = visible
        agent.valid_tool_names = {n for n in agent.valid_tool_names or set() if n not in run.scopes.denied}


def _clarify_callback(run: Run) -> Callable[[list[dict]], dict]:
    """Hermes' ``clarify`` tool asks the user through SAM's question prompt."""

    def clarify(questions: list[dict]) -> dict:
        asked = [
            Question(q["question"], tuple(Option(str(c)) for c in q.get("choices_offered") or ())) for q in questions
        ]
        try:
            answer = run.call(run.ctx.ask_user(asked))
        except PromptTooLarge as exc:
            log.warning("clarify not asked: %s task=%s", exc, run.ctx.task_id)
            return {"answers": {}, "outcome": "cancelled"}
        if answer.answered:
            return {
                "answers": {q["qid"]: a for q, a in zip(questions, answer.answers, strict=False)},
                "outcome": "submitted",
            }
        return {"answers": {}, "outcome": "timed_out" if answer.status == "timed_out" else "cancelled"}

    return clarify


def _command_approval(run: Run) -> Callable[..., str]:
    """Hermes' own dangerous-command prompt (its terminal tool) asks the user through SAM's approval prompt."""

    def approve(command: str, description: str, **_: Any) -> str:
        if over(run.ctx, "terminal"):
            return "deny"
        decision = run.call(run.ctx.request_approval("terminal", {"command": command}, message=description))
        return "once" if decision.approved and not over(run.ctx, "terminal") else "deny"

    return approve


class _AnswerStream:
    """Sends the answer to the task as Hermes streams it, through the agent's stream callbacks.

    Hermes also hands its stream callback the text a response writes once it has begun a tool call (to pick reasoning
    tags out of it), and an end marker (None) before the tools run. That text is not the answer, so from the call's
    start (``tool_gen_callback``) to the marker it is dropped. The break Hermes puts before the first text after a
    tool round is not part of the answer either, so leading blank lines are left out until text has been sent.
    Callbacks the agent was built with still get every call."""

    def __init__(self, run: Run, agent: Any):
        self._run = run
        self._calling_tool = False
        self._sent = False
        self._delta = agent.stream_delta_callback
        self._tool_gen = agent.tool_gen_callback
        agent.stream_delta_callback = self.on_delta
        agent.tool_gen_callback = self.on_tool_gen

    def on_tool_gen(self, name: str) -> None:
        self._calling_tool = True
        if self._tool_gen is not None:
            self._tool_gen(name)

    def on_delta(self, text: str | None) -> None:
        if self._delta is not None:
            self._delta(text)
        if text is None:
            self._calling_tool = False
            return
        if self._calling_tool or self._run.closed or self._run.stopped:
            return
        if not self._sent:
            text = text.lstrip()
            if not text:
                return
        self._sent = True
        with may_be_lost(self._run.ctx, "streamed text"):
            self._run.call(self._run.ctx.emit_text(text), SIGNAL_TIMEOUT_SECONDS)


def _turn(run: Run, agent: Any, text: str, prior: list[dict]) -> dict:
    """One conversation turn, on the worker thread, with the task's hooks and prompts bound to it.

    The turn runs under the run's own session id, new for each delivery: Hermes keys its terminal, browser and file
    caches by this id and tears them down when the turn ends, and a run given up must not end the redelivery's.
    """
    from tools.approval_context import reset_hermes_interactive_context, set_hermes_interactive_context
    from tools.terminal_tool import set_approval_callback

    set_approval_callback(_command_approval(run))
    interactive = set_hermes_interactive_context(True)
    try:
        return agent.run_conversation(
            text,
            conversation_history=prior or None,
            task_id=run.session_id,
        )
    finally:
        reset_hermes_interactive_context(interactive)
        set_approval_callback(None)
        if end_turn(run):
            _interrupt_subagents(agent, run)


def _interrupt_subagents(agent: Any, run: Run) -> None:
    """Stop the subagents a turn leaves running: Hermes runs a top-level agent's delegations in the background, where
    they would outlive the mesh task. Hermes fans an interrupt out to an agent's live subagents, and stops the
    background delegations of a session that ends."""
    log.info("interrupting the subagents a finished turn left running task=%s", run.ctx.task_id)
    agent.hard_interrupt(SUBAGENTS_STOPPED)
    from tools.async_delegation import interrupt_for_session

    interrupt_for_session(parent_session_id=run.session_id, reason="mesh_task_ended")


SUBAGENTS_STOPPED = "The mesh task this agent works for has ended."


@halts_bare
async def run_agent(
    ctx: TaskContext,
    build: Callable[[TaskContext], Any],
    *,
    input: str | None = None,
    memory: ConversationMemory | None = None,
    include_request_context: bool = False,
    max_model_calls: int = 10,
    stream_text: bool = True,
    approval: Approval | None = None,
    extra_tools: Iterable[MeshTool] = (),
) -> str:
    """Run a Hermes agent on the task's input with its steps reported to SAM; returns the answer text.

    `build(ctx)` makes the agent for this task: ``AIAgent(**agent_defaults(ctx), model=..., ...)``. Enable only the
    toolsets the agent should have: Hermes can run commands and edit files on its host, and the mesh sees each
    step but does not sandbox it.

    `extra_tools` are added to the agent's own for this task (``mesh_agent_tools(ctx)``). `max_model_calls` is set as
    the agent's ``max_iterations``: a turn that reaches it fails the task saying so. `approval` (``with_approval(...)``) makes the
    chosen tools ask the user first. The model can ask the user a question with Hermes' ``clarify`` tool (enable its
    toolset); both prompts go through SAM.

    With `memory`, the conversation's earlier turns (keyed by ``ctx.session_id``) are replayed before the new
    message, and this turn's messages are remembered once it finishes; a run-based task (a peer call or a workflow
    node) neither reads nor adds history. With `include_request_context`, the gateway's system purpose, response
    format, user profile and conversation context (``ctx.request_context``) are added to the system prompt, as
    SAM-Go agents do. Tools whose required scopes the task's user lacks are left out of the run, and a call to one
    anyway is refused.
    With `stream_text` (the default) Hermes streams the model and the answer goes to the user as it is written (text
    a response writes once it has begun a tool call is not sent); with ``stream_text=False`` it is sent whole when the
    run ends.
    A cancelled or stopped task interrupts the agent, which stops at its next safe point.

    A turn given up keeps running in its thread until it stops; its hooks stay routed to the run, which is closed so
    they refuse.
    """
    agent = await asyncio.to_thread(build, ctx)
    check_hermes(agent)
    agent.max_iterations = max_model_calls
    tools = list(extra_tools)
    _check_approval(approval, agent, tools)
    install_hooks()
    run = Run(ctx, asyncio.get_running_loop(), str(agent.session_id), approval)
    run.interrupt = agent.hard_interrupt
    agent._persist_disabled = True
    for tool in tools:
        run.tools[tool.name] = tool
        add_to_agent(agent, tool)
    _hide_denied_tools(run, agent)
    agent.clarify_callback = _clarify_callback(run)
    if stream_text:
        _AnswerStream(run, agent)
    if prompt := request_prompt(ctx, include_request_context):
        agent.ephemeral_system_prompt = join_prompts(agent.ephemeral_system_prompt or "", prompt)

    text = input_text(ctx, input)
    prior = memory.history(ctx.session_id) if memory is not None else []
    claim_history(run, prior)
    with bind_task(ctx):
        thread_context = contextvars.copy_context()
    register_run(run)
    try:
        result = await asyncio.to_thread(thread_context.run, _turn, run, agent, text, prior)
    except asyncio.CancelledError:
        close_run(run)
        agent.hard_interrupt()
        raise
    if str(result.get("turn_exit_reason") or "").startswith("max_iterations_reached"):
        raise LimitReached(f"Stopped: the agent reached its limit of {max_model_calls} model calls (max_model_calls).")
    if result.get("failed") or result.get("interrupted") or not result.get("completed", True):
        raise RuntimeError(f"Hermes did not complete the task ({result.get('error') or 'interrupted'})")
    answer = str(result.get("final_response") or "").strip()
    if memory is not None:
        memory.remember(ctx.session_id, list(result.get("messages") or [])[len(prior) :], text, answer)
    return answer
