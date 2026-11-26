# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Other mesh agents as LangChain tools, named and described as SAM-Go's peer_<name> tools."""

from __future__ import annotations

from collections.abc import Iterable
from typing import Annotated

from langchain_core.tools import BaseTool, InjectedToolCallId, StructuredTool
from pydantic import BaseModel, Field

from .._adapter import delegate, peer_cards
from ..context import TaskContext
from ..delegation import TASK_DESCRIPTION_HELP, AgentCard


class _PeerArgs(BaseModel):
    task_description: str = Field(description=TASK_DESCRIPTION_HELP)
    tool_call_id: Annotated[str, InjectedToolCallId]


def mesh_agent_tools(ctx: TaskContext, agents: Iterable[str] | None = None) -> list[BaseTool]:
    """One tool per mesh agent this task's user may invoke, optionally limited to `agents`.

    Build them per task: the list follows the user's scopes and the agents currently on the mesh.
    """
    return [_peer_tool(ctx, card) for card in peer_cards(ctx, agents)]


def _peer_tool(ctx: TaskContext, card: AgentCard) -> BaseTool:
    async def invoke(task_description: str, tool_call_id: str) -> str:
        return await delegate(ctx, card, task_description, tool_call_id)

    return StructuredTool.from_function(
        coroutine=invoke, name=card.tool_name, description=card.tool_description(), args_schema=_PeerArgs
    )
