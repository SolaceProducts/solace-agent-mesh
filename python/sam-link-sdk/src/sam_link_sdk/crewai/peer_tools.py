# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Other mesh agents as CrewAI tools, named and described as SAM-Go's peer_<name> tools.

CrewAI runs tools synchronously in the crew's worker thread, so each tool hands the call to the mesh task's
event loop and waits for it there. CrewAI does not tell a tool its tool-call id, so the peer's forwarded steps
carry no function_call_id: the trace shows them under this agent rather than under the specific tool call.
"""

from __future__ import annotations

import asyncio
import re
from collections.abc import Iterable

from crewai.tools import BaseTool
from pydantic import BaseModel, ConfigDict, Field, PrivateAttr

from .._adapter import delegate, peer_cards
from ..context import TaskContext
from ..delegation import TASK_DESCRIPTION_HELP, AgentCard


class _PeerArgs(BaseModel):
    task_description: str = Field(description=TASK_DESCRIPTION_HELP)


class _MeshAgentTool(BaseTool):
    """Delegates to one mesh agent for one mesh task."""

    model_config = ConfigDict(arbitrary_types_allowed=True)
    args_schema: type[BaseModel] = _PeerArgs
    _ctx: TaskContext = PrivateAttr()
    _card: AgentCard = PrivateAttr()
    _loop: asyncio.AbstractEventLoop = PrivateAttr()

    def __init__(self, ctx: TaskContext, card: AgentCard, loop: asyncio.AbstractEventLoop):
        super().__init__(name=card.tool_name, description=card.tool_description())
        self._ctx = ctx
        self._card = card
        self._loop = loop

    def _run(self, task_description: str) -> str:
        return asyncio.run_coroutine_threadsafe(delegate(self._ctx, self._card, task_description), self._loop).result()


def mesh_agent_tools(ctx: TaskContext, agents: Iterable[str] | None = None) -> list[BaseTool]:
    """One tool per mesh agent this task's user may invoke, optionally limited to `agents`.

    Call it in the handler (on the mesh task's event loop) and give the tools to the crew built for that task:
    ``run_agent(ctx, lambda ctx: build_crew(extra_tools=mesh_agent_tools(ctx)))``.

    CrewAI offers the model a tool under a sanitized name (lowercase, a-z0-9_), and the run checks that name too; the
    steps are reported under the tool's own name (``peer_<agent>``), the one SAM knows.
    """
    loop = asyncio.get_running_loop()
    tools: list[BaseTool] = [_MeshAgentTool(ctx, card, loop) for card in peer_cards(ctx, agents)]
    for tool in tools:
        ctx._run.tools.note_sdk_alias(re.sub(r"[^a-z0-9_]", "_", tool.name.lower())[:64], tool.name)
    return tools
