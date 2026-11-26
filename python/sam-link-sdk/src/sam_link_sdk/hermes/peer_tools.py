# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Other mesh agents as Hermes tools, named and described as SAM-Go's peer_<name> tools.

Hermes keeps one process-wide tool registry, so each tool is registered there once with a handler that finds the
task that is calling it (by session id) and gives it that task's version of the tool.
"""

from __future__ import annotations

import asyncio
import threading
from collections.abc import Callable, Iterable
from dataclasses import dataclass
from typing import Any

from .._adapter import delegate, peer_cards
from ..context import TaskContext
from ..delegation import PEER_TOOL_PARAMETERS, AgentCard
from .steps import run_for

TOOLSET = "sam_link_sdk"


@dataclass(frozen=True)
class MeshTool:
    """A tool added to one task's agent. `handler(args, call_id)` runs on the agent's thread and returns the text
    the model sees; `call_id` is the model's id for the call (what SAM nests a peer's steps under)."""

    name: str
    description: str
    parameters: dict
    handler: Callable[[dict, str], str]


def mesh_agent_tools(ctx: TaskContext, agents: Iterable[str] | None = None) -> list[MeshTool]:
    """One tool per mesh agent this task's user may invoke, optionally limited to `agents`.

    Call it inside the task's handler and pass the result to ``run_agent(..., tools=...)``.
    """
    loop = asyncio.get_running_loop()
    return [_peer_tool(ctx, loop, card) for card in peer_cards(ctx, agents)]


def _peer_tool(ctx: TaskContext, loop: asyncio.AbstractEventLoop, card: AgentCard) -> MeshTool:
    def handler(args: dict, call_id: str) -> str:
        call = delegate(ctx, card, args.get("task_description"), call_id)
        return asyncio.run_coroutine_threadsafe(call, loop).result()

    return MeshTool(card.tool_name, card.tool_description(), PEER_TOOL_PARAMETERS, handler)


_registered: set[str] = set()
_registration_lock = threading.Lock()


def _dispatch(name: str) -> Callable[..., str]:
    def handler(args: dict, **kw: Any) -> str:
        run = run_for(kw.get("session_id"))
        tool = run.tools.get(name) if run is not None else None
        if run is None or tool is None:
            return '{"error": "This tool is not available in this task."}'
        return tool.handler(args if isinstance(args, dict) else {}, run.take_call_id(name))

    return handler


def register_tool(tool: MeshTool) -> None:
    """Make `tool` known to Hermes' registry (once per name; later tasks use the same entry)."""
    from tools.registry import registry

    with _registration_lock:
        if tool.name in _registered:
            return
        registry.register(
            name=tool.name,
            toolset=TOOLSET,
            schema={"name": tool.name, "description": tool.description, "parameters": tool.parameters},
            handler=_dispatch(tool.name),
            description=tool.description,
        )
        _registered.add(tool.name)


def add_to_agent(agent: Any, tool: MeshTool) -> None:
    """Offer `tool` to the model on this agent."""
    register_tool(tool)
    agent.tools = [
        *(agent.tools or []),
        {
            "type": "function",
            "function": {"name": tool.name, "description": tool.description, "parameters": tool.parameters},
        },
    ]
    agent.valid_tool_names = {*(agent.valid_tool_names or set()), tool.name}
