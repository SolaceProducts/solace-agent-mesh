# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Other mesh agents as OpenAI Agents SDK tools, named and described as SAM-Go's peer_<name> tools."""

from __future__ import annotations

from collections.abc import Iterable
from typing import Any

from agents import FunctionTool
from agents.tool_context import ToolContext

from .._adapter import delegate, parse_args, peer_cards
from ..context import TaskContext
from ..delegation import PEER_TOOL_PARAMETERS, AgentCard


def mesh_agent_tools(ctx: TaskContext, agents: Iterable[str] | None = None) -> list[FunctionTool]:
    """One tool per mesh agent this task's user may invoke, optionally limited to `agents`.

    Build them per task and add them to a copy of the agent:
    ``agent.clone(tools=[*agent.tools, *mesh_agent_tools(ctx)])``.
    """
    return [_peer_tool(ctx, card) for card in peer_cards(ctx, agents)]


def _peer_tool(ctx: TaskContext, card: AgentCard) -> FunctionTool:
    async def invoke(tool_ctx: ToolContext[Any], raw_args: str) -> str:
        return await delegate(ctx, card, parse_args(raw_args).get("task_description"), tool_ctx.tool_call_id or "")

    return FunctionTool(
        name=card.tool_name,
        description=card.tool_description(),
        params_json_schema=PEER_TOOL_PARAMETERS,
        on_invoke_tool=invoke,
    )
