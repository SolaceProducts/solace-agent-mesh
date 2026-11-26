# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Make SAM's kill switch stop an OpenAI Agents SDK agent wherever it runs, not only on the mesh."""

from __future__ import annotations

from typing import TYPE_CHECKING

from agents import RunHooks

if TYPE_CHECKING:
    from ..agent import MeshAgent


class StopHooks(RunHooks):
    """Raises AgentStopped when an agent starts, and before every model and tool call, while SAM has stopped it
    (what ``stop_guard`` returns)."""

    def __init__(self, agent: MeshAgent):
        self._agent = agent

    async def on_agent_start(self, context, agent) -> None:
        self._agent.check()

    async def on_llm_start(self, context, agent, system_prompt, input_items) -> None:
        self._agent.check()

    async def on_tool_start(self, context, agent, tool) -> None:
        self._agent.check()


def stop_guard(agent: MeshAgent) -> StopHooks:
    """Run hooks that raise AgentStopped when an agent starts, and before every model and tool call, while SAM has
    stopped `agent`.

    Pass them to runs outside the mesh (your own API, a scheduled job): ``Runner.run(my_agent, text,
    hooks=stop_guard(mesh_agent))``. Mesh tasks run through ``run_agent``, which the agent stops on its own.
    """
    return StopHooks(agent)
