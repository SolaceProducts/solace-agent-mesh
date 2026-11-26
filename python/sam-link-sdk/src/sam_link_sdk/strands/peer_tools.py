# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Other mesh agents as Strands tools, named and described as SAM-Go's peer_<name> tools."""

from __future__ import annotations

from collections.abc import Iterable
from typing import Any

from strands import tool

from .._adapter import delegate, peer_cards
from ..context import TaskContext
from ..delegation import PEER_TOOL_PARAMETERS, AgentCard


def mesh_agent_tools(ctx: TaskContext, agents: Iterable[str] | None = None) -> list[Any]:
    """One tool per mesh agent this task's user may invoke, optionally limited to `agents`.

    Build them per task and add them to the agent's tools: ``Agent(tools=[*my_tools, *mesh_agent_tools(ctx)])``.
    """
    return [_peer_tool(ctx, card) for card in peer_cards(ctx, agents)]


def _peer_tool(ctx: TaskContext, card: AgentCard) -> Any:
    @tool(
        name=card.tool_name,
        description=card.tool_description(),
        inputSchema={"json": PEER_TOOL_PARAMETERS},
        context=True,
    )
    async def invoke(task_description: str, tool_context: Any) -> str:
        return await delegate(ctx, card, task_description, tool_context.tool_use["toolUseId"])

    return invoke
