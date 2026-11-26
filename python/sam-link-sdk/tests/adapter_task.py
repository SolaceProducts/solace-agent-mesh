# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The adapter tests' task: the real TaskContext of a task a real MeshAgent runs (``tests/agent_rig.task_context``),
set up the way adapter tests need it.

``declared`` names tools the card lists without scopes (a tool the framework offers that the card does not name is
refused once ``scopes`` declares a scoped tool); ``scopes`` maps a tool's name to the scopes it requires (the agent's card, ``AgentConfig.tools``); ``granted`` is what
SAM granted the task's user of them (``IncomingTask.granted_tool_scopes``). A test that gives ``scopes`` always grants
explicitly: with no ``granted`` it grants none. ``audit`` collects the agent's audit records, ``stream_thinking`` turns
the reasoning stream on; any other keyword goes to the IncomingTask (``user_id``, ``can_prompt_user``, ``metadata``,
``session_behavior``, ...).

``steps(task)`` is what the task sent, without ``Text`` (the answer as an adapter streamed it, or as the core streams it
before it answers: not a step of the run).
"""

from __future__ import annotations

import contextlib
from collections.abc import AsyncIterator, Callable, Iterable, Mapping
from typing import Any

from sam_link_sdk.config import ToolInfo
from sam_link_sdk.events import StepEvent, Text

from .agent_rig import MeshTask, task_context


@contextlib.asynccontextmanager
async def adapter_task(
    task_id: str = "T",
    text: str = "Triage claims P-1 and P-2",
    *,
    scopes: Mapping[str, Iterable[str]] | None = None,
    declared: Iterable[str] = (),
    granted: Iterable[str] | None = None,
    audit: Callable[[Any], None] | None = None,
    stream_thinking: bool = False,
    config: dict[str, Any] | None = None,
    **task: Any,
) -> AsyncIterator[MeshTask]:
    config = dict(config or {})
    if stream_thinking:
        config["stream_thinking"] = True
    tools = [
        *(ToolInfo(name, required_scopes=list(required)) for name, required in (scopes or {}).items()),
        *(ToolInfo(name) for name in declared if name not in (scopes or {})),
    ] or None
    if scopes is not None or granted is not None:
        task["granted_tool_scopes"] = frozenset(granted or ())
    async with task_context(task_id, text, tools=tools, audit_sink=audit, config=config, **task) as mesh_task:
        yield mesh_task


def steps(events: MeshTask | list[StepEvent], answer: str | None = None) -> list[StepEvent]:
    """The task's step events, without the ``Text`` the answer was streamed as: the pieces an adapter sent while the
    model wrote it, and the one the core sends for a completed answer (``answer`` when given, else any trailing Text)."""
    sent = [e for e in (events.events if isinstance(events, MeshTask) else events) if not isinstance(e, Text)]
    return sent
