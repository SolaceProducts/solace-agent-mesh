# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Kick off a CrewAI crew for a mesh task."""

from __future__ import annotations

import asyncio
import inspect
import re
from collections.abc import Callable, Iterable
from typing import Any

from crewai.events import crewai_event_bus

from .._adapter import halts_bare
from ..context import TaskContext, bind_task, tool_name
from ..limits import LimitReached
from ..llm_errors import CONTEXT_OVERFLOW, LLMFailure, classify_llm_failure, provider_label
from ..memory import ConversationMemory
from .steps import _check_current_task, _current, _ensure_listener, _Run, answer_call
from .stop import guard_crew

_SHARED_CREW = (
    "run_agent takes build(ctx), which builds the crew for each task (run_agent(ctx, lambda ctx: Crew(...))), not a "
    "Crew: a kickoff changes the crew, so tasks that run at once must not share one"
)
_HIDES_TOOLS = "_sam_link_hides_denied_tools"
_STREAMS = "_sam_link_streams_answers"


def _offered(kwargs: dict) -> dict:
    """A model call's arguments without the tools the current mesh task's user lacks the scopes for."""
    run = _current.get()
    tools = kwargs.get("tools")
    if run is None or not isinstance(tools, list) or not tools:
        return kwargs
    return {**kwargs, "tools": [t for t in tools if run.scopes.permits(tool_name(t))]}


def _hide_denied_tools(llms: Iterable[Any]) -> None:
    """Leave the tools the current task's user may not use out of each model call these LLMs make with native tool
    calling (idempotent; an LLM outside a mesh task is left alone). The hook refuses a call to one regardless."""
    for llm in llms:
        if (
            llm is None
            or isinstance(llm, str)
            or getattr(llm, _HIDES_TOOLS, False)
            or not callable(getattr(llm, "call", None))
        ):
            continue
        original = llm.call

        def call(*args: Any, _original: Callable[..., Any] = original, **kwargs: Any) -> Any:
            return _original(*args, **_offered(kwargs))

        object.__setattr__(llm, "call", call)
        object.__setattr__(llm, _HIDES_TOOLS, True)
        original_acall = getattr(llm, "acall", None)
        if inspect.iscoroutinefunction(original_acall):

            async def acall(*args: Any, _original: Callable[..., Any] = original_acall, **kwargs: Any) -> Any:
                return await _original(*args, **_offered(kwargs))

            object.__setattr__(llm, "acall", acall)


def _stream_answers(llms: Iterable[Any]) -> None:
    """Stream each model call of these LLMs whose text may be the current task's answer (idempotent; see
    sam_link_sdk.crewai.steps.answer_call)."""
    for llm in llms:
        if (
            llm is None
            or isinstance(llm, str)
            or getattr(llm, _STREAMS, False)
            or not callable(getattr(llm, "call", None))
        ):
            continue
        original = llm.call

        def call(*args: Any, _original: Callable[..., Any] = original, _llm: Any = llm, **kwargs: Any) -> Any:
            with answer_call(_llm, kwargs):
                return _original(*args, **kwargs)

        object.__setattr__(llm, "call", call)
        object.__setattr__(llm, _STREAMS, True)
        original_acall = getattr(llm, "acall", None)
        if inspect.iscoroutinefunction(original_acall):

            async def acall(
                *args: Any, _original: Callable[..., Any] = original_acall, _llm: Any = llm, **kwargs: Any
            ) -> Any:
                with answer_call(_llm, kwargs):
                    return await _original(*args, **kwargs)

            object.__setattr__(llm, "acall", acall)


def _answer_task(crew: Any) -> Any:
    """The task whose model calls may write the crew's answer, its last; None when CrewAI may change that answer after
    the model wrote it (a guardrail, a structured output), so the answer is sent whole."""
    tasks = list(getattr(crew, "tasks", None) or [])
    last = tasks[-1] if tasks else None
    rewrites = ("guardrail", "guardrails", "output_pydantic", "output_json", "response_model")
    if last is None or any(getattr(last, name, None) for name in rewrites):
        return None
    return last


def _crew_llms(crew: Any) -> list[Any]:
    agents = getattr(crew, "agents", []) or []
    return [
        *(getattr(a, "llm", None) for a in agents),
        *(getattr(a, "function_calling_llm", None) for a in agents),
        getattr(crew, "manager_llm", None),
    ]


_PLACED_BY_RUN_AGENT = (
    ("history", "Conversation so far:\n{history}\n\n"),
    ("request_context", "{request_context}\n\n"),
)
_GUARDRAIL_FAILED = re.compile(r"(Task failed .*|Agent's guardrail failed )validation after \d+ retries")


def _templates(crew: Any) -> str:
    tasks = list(getattr(crew, "tasks", None) or [])
    agents = list(getattr(crew, "agents", None) or [])
    fields = [(t, "description") for t in tasks] + [(t, "expected_output") for t in tasks]
    fields += [(a, name) for a in agents for name in ("role", "goal", "backstory")]
    return "\n".join(str(getattr(owner, name, "") or "") for owner, name in fields)


def _place_inputs(crew: Any, inputs: dict) -> None:
    """Put the inputs `run_agent` adds on its own (the conversation, the request context) at the head of the crew's first
    task when no template of the crew names them: CrewAI fills in only the placeholders the templates have, so the
    text would otherwise never reach the model."""
    tasks = list(getattr(crew, "tasks", None) or [])
    if not tasks:
        return
    templates = _templates(crew)
    head = "".join(
        block for name, block in _PLACED_BY_RUN_AGENT if inputs.get(name) and "{" + name + "}" not in templates
    )
    if head:
        tasks[0].description = head + str(tasks[0].description)


def _guardrail_stop(exc: BaseException) -> LimitReached | None:
    """CrewAI raises a bare Exception when a task's (or an agent's) guardrail still fails after its retries."""
    if _GUARDRAIL_FAILED.match(str(exc)):
        return LimitReached("Stopped: a guardrail of the crew blocked its answer.")
    return None


def _note_context_overflow(ctx: TaskContext, exc: BaseException, crew: Any) -> None:
    """CrewAI 1.15.27 and 1.15.28 raise their LLMContextLengthExceededError `from None` once summarizing the
    conversation fails too, which drops the provider's error the category and the provider are read from. The failure
    is then recorded as a context overflow of the crew's model provider; from 1.15.29 the provider's error is kept and
    this does nothing."""
    if type(exc).__name__ != "LLMContextLengthExceededError" or ctx._run.llm_failure or classify_llm_failure(exc):
        return
    provider = next((p for llm in _crew_llms(crew) if isinstance(p := getattr(llm, "provider", None), str) and p), "")
    ctx._run.llm_failure = LLMFailure(CONTEXT_OVERFLOW, provider_label(provider))


async def _close_pump(run: _Run, pump: asyncio.Task) -> None:
    """Let in-flight handlers finish capturing before closing the pump."""
    try:
        await asyncio.to_thread(crewai_event_bus.flush, 5.0)
        run.loop.call_soon(run.queue.put_nowait, None)
        await pump
    finally:
        pump.cancel()


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
    kickoff_inputs: dict | None = None,
) -> str:
    """Kick off a crew for one mesh task and return its final output text.

    `build(ctx)` makes the crew for this task, with the tools that act for it (``mesh_agent_tools(ctx)``,
    ``with_approval(ctx, ...)``): a Crew is stateful (a kickoff fills its tasks' templates in place), so tasks that run
    at once must not share one, and a Crew itself is refused with a TypeError. `kickoff_inputs` are the kickoff inputs,
    with `request` set to the task's input (or `input`) for `{request}` in task templates unless they set it.
    With `include_request_context`, the gateway's system purpose, response format, user profile and conversation
    context (`ctx.request_context`) are added to the inputs as `request_context`; put `{request_context}` in a
    task description or backstory to use it (an input no template mentions is ignored).

    A crew keeps no conversation between kickoffs, so with `memory` the conversation's earlier turns (keyed by
    ``ctx.session_id``) are added to the inputs as `history` (``memory.transcript``; put `{history}` in a task
    description to use it), and this turn is remembered once it finishes; a run-based task (a peer call or a workflow
    node) neither reads nor adds history.

    A call to a tool the task's user lacks the scopes for (AgentConfig.tools) is refused by a CrewAI tool hook in
    every crew agent, and with native tool calling the model is not offered it (see sam_link_sdk.crewai.steps).

    Cancelling the mesh task stops its steps at once. CrewAI cannot interrupt a running kickoff, so once the
    agent stops the task (a cancel, a limit, or SAM's kill switch) the crew stops at its next model call: each crew
    agent's LLM checks the task first (see sam_link_sdk.crewai.stop). A model call already under way finishes.

    `max_model_calls` is set as the ``max_iter`` of each of the crew's agents (CrewAI counts per agent, not per
    crew): an agent that reaches it makes one more model call, without tools, for its final answer and the run goes
    on, as CrewAI does.

    With `stream_text` (the default) the model calls made for the crew's last task are streamed and the answer goes to
    the user as it is written (``ctx.emit_text``): not the earlier tasks' text, nor a call's tool calls or CrewAI's
    ReAct thoughts (see sam_link_sdk.crewai.steps._AnswerText). A last task with a guardrail or a structured output is
    not streamed, since CrewAI may change its answer after the model wrote it. Pass False to send the answer whole when
    the run ends.
    """
    if hasattr(build, "kickoff") or not callable(build):
        raise TypeError(_SHARED_CREW)
    _ensure_listener()
    crew_obj = build(ctx)
    for member in [*(getattr(crew_obj, "agents", None) or []), getattr(crew_obj, "manager_agent", None)]:
        if member is not None:
            member.max_iter = max_model_calls
    guard_crew(crew_obj, _check_current_task)
    _hide_denied_tools(_crew_llms(crew_obj))
    _stream_answers(_crew_llms(crew_obj))
    run = _Run(ctx, asyncio.get_running_loop(), stream_text=stream_text, answer_task=_answer_task(crew_obj))
    pump = asyncio.create_task(run.pump(), name=f"sam-link-sdk-crewai-{ctx.task_id}")
    token = _current.set(run)
    try:
        text = input if input is not None else ctx.text
        inputs = dict(kickoff_inputs or {})
        inputs.setdefault("request", text)
        if memory is not None:
            inputs.setdefault("history", memory.transcript(ctx.session_id))
        if include_request_context:
            inputs.setdefault("request_context", ctx.request_context.context_prompt())
        _place_inputs(crew_obj, inputs)
        with bind_task(ctx):
            result = await asyncio.to_thread(crew_obj.kickoff, inputs=inputs)
    except asyncio.CancelledError:
        pump.cancel()
        raise
    except Exception as exc:
        await _close_pump(run, pump)
        stop = _guardrail_stop(exc)
        if stop is not None:
            raise stop from exc
        _note_context_overflow(ctx, exc, crew_obj)
        raise
    finally:
        _current.reset(token)
    await _close_pump(run, pump)
    answer = getattr(result, "raw", None) or str(result)
    if memory is not None:
        memory.remember(ctx.session_id, [], text, answer)
    return answer
