# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Make SAM's kill switch stop a Strands agent wherever it runs, not only on the mesh."""

from __future__ import annotations

from typing import TYPE_CHECKING, Any

from strands.hooks import BeforeInvocationEvent, BeforeModelCallEvent, BeforeToolCallEvent, HookProvider, HookRegistry

if TYPE_CHECKING:
    from ..agent import MeshAgent

GUARD_ORDER = -20


class StopHooks(HookProvider):
    """Raises AgentStopped when an agent is invoked, and before every model and tool call, while SAM has stopped it
    (what ``stop_guard`` returns)."""

    def __init__(self, agent: MeshAgent):
        self._agent = agent

    def register_hooks(self, registry: HookRegistry, **kwargs: Any) -> None:
        for event in (BeforeInvocationEvent, BeforeModelCallEvent, BeforeToolCallEvent):
            registry.add_callback(event, self._check, order=GUARD_ORDER)

    def _check(self, event: Any) -> None:
        self._agent.check()


def stop_guard(agent: MeshAgent) -> StopHooks:
    """Hooks that raise AgentStopped when an agent is invoked, and before every model and tool call, while SAM has
    stopped `agent`.

    Add them to agents that run outside the mesh (your own API, a scheduled job):
    ``Agent(model=..., hooks=[stop_guard(mesh_agent)])``. Mesh tasks run through ``run_agent``, which the agent stops
    on its own.
    """
    return StopHooks(agent)
