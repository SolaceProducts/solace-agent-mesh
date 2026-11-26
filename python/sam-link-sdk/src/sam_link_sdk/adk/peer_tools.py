# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Other mesh agents as ADK tools, named and described as SAM-Go's peer_<name> tools.

An ADK Runner and its agent are usually built once, while the agents a user may call depend on the task. So
peers come as a toolset: ADK asks it for tools on every run, and it answers for the mesh task that run_agent
is serving.
"""

from __future__ import annotations

from collections.abc import Iterable
from typing import Any

from google.adk.agents.readonly_context import ReadonlyContext
from google.adk.tools import BaseTool
from google.adk.tools.base_toolset import BaseToolset
from google.adk.tools.tool_context import ToolContext
from google.genai import types

from .._adapter import delegate, peer_cards
from ..context import TaskContext
from ..delegation import TASK_DESCRIPTION_HELP, AgentCard
from .steps import _current


class _MeshAgentToolset(BaseToolset):
    """A `peer_<name>` tool for each mesh agent the current task's user may invoke.

    Add it to the agent's tools: ``LlmAgent(..., tools=[lookup_policy, *mesh_agent_tools()])``. `agents`
    limits it to the named agents.
    """

    def __init__(self, agents: Iterable[str] | None = None):
        super().__init__()
        self._agents = list(agents) if agents is not None else None

    async def get_tools(self, readonly_context: ReadonlyContext | None = None) -> list[BaseTool]:
        ctx = _current.get()
        if ctx is None:
            return []
        return [_MeshAgentTool(ctx, card) for card in peer_cards(ctx, self._agents)]

    async def close(self) -> None:
        return None


class _MeshAgentTool(BaseTool):
    """Delegates to one mesh agent for one mesh task."""

    def __init__(self, ctx: TaskContext, card: AgentCard):
        super().__init__(name=card.tool_name, description=card.tool_description())
        self._ctx = ctx
        self._card = card

    def _get_declaration(self) -> types.FunctionDeclaration:
        return types.FunctionDeclaration(
            name=self.name,
            description=self.description,
            parameters=types.Schema(
                type=types.Type.OBJECT,
                properties={
                    "task_description": types.Schema(type=types.Type.STRING, description=TASK_DESCRIPTION_HELP)
                },
                required=["task_description"],
            ),
        )

    async def run_async(self, *, args: dict[str, Any], tool_context: ToolContext) -> Any:
        return await delegate(self._ctx, self._card, args.get("task_description"), tool_context.function_call_id or "")


def mesh_agent_tools(agents: Iterable[str] | None = None) -> list[BaseToolset]:
    """The `peer_<name>` tools as one toolset: ``LlmAgent(..., tools=[lookup_policy, *mesh_agent_tools()])``. ADK
    builds the agent once, so this takes no task context: on every run the toolset offers the agents the user of the
    mesh task ``run_agent`` is serving may invoke, limited to `agents` when given."""
    return [_MeshAgentToolset(agents)]
