# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Make SAM's kill switch stop a Google ADK agent wherever it runs, not only on the mesh."""

from __future__ import annotations

from typing import TYPE_CHECKING

from google.adk.plugins.base_plugin import BasePlugin

if TYPE_CHECKING:
    from ..agent import MeshAgent


class StopPlugin(BasePlugin):
    """Raises AgentStopped when a run starts, and before every model and tool call, while SAM has stopped the agent
    (what ``stop_guard`` returns)."""

    def __init__(self, agent: MeshAgent):
        super().__init__(name="sam_link_kill_switch")
        self._agent = agent

    async def before_run_callback(self, *, invocation_context) -> None:
        self._agent.check()

    async def before_model_callback(self, *, callback_context, llm_request) -> None:
        self._agent.check()

    async def before_tool_callback(self, *, tool, tool_args, tool_context) -> None:
        self._agent.check()


def stop_guard(agent: MeshAgent) -> StopPlugin:
    """A plugin that raises AgentStopped when a run starts, and before every model and tool call, while SAM has
    stopped `agent`.

    Add it to every Runner that runs the agent, including runners that never touch the mesh (your own API, a
    scheduled job): ``Runner(agent=..., plugins=[step_reporter(), stop_guard(mesh_agent)])``. ADK wraps a plugin's
    exception in ``RuntimeError("Error in plugin ...")``; the AgentStopped is its ``__cause__``.
    """
    return StopPlugin(agent)
