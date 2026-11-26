# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Make SAM's kill switch stop a Microsoft Agent Framework agent wherever it runs, not only on the mesh."""

from __future__ import annotations

from collections.abc import Callable
from typing import TYPE_CHECKING, Any

from agent_framework import AgentMiddleware, ChatMiddleware, FunctionMiddleware, MiddlewareFailure

from ..stop import AgentStopped

if TYPE_CHECKING:
    from ..agent import MeshAgent


class _MiddlewareStopped(MiddlewareFailure, AgentStopped):
    """AgentStopped raised from function middleware. The framework turns an ordinary exception there into a tool
    error the model reads and carries on; a MiddlewareFailure aborts the run, so the stop is both."""

    def __init__(self, reason: str):
        MiddlewareFailure.__init__(self, f"agent stopped by SAM ({reason})")
        self.reason = reason


class _Agent(AgentMiddleware):
    def __init__(self, agent: MeshAgent):
        self._agent = agent

    async def process(self, context: Any, call_next: Callable[[], Any]) -> None:
        self._agent.check()
        await call_next()


class _Chat(ChatMiddleware):
    def __init__(self, agent: MeshAgent):
        self._agent = agent

    async def process(self, context: Any, call_next: Callable[[], Any]) -> None:
        self._agent.check()
        await call_next()


class _Function(FunctionMiddleware):
    def __init__(self, agent: MeshAgent):
        self._agent = agent

    async def process(self, context: Any, call_next: Callable[[], Any]) -> None:
        try:
            self._agent.check()
        except AgentStopped as stop:
            raise _MiddlewareStopped(stop.reason) from stop
        await call_next()


def stop_guard(agent: MeshAgent) -> list[Any]:
    """Middleware that raises AgentStopped when an agent runs, and before every model and tool call, while SAM has
    stopped `agent`.

    Add it to agents that run outside the mesh (your own API, a scheduled job):
    ``Agent(client, instructions, middleware=stop_guard(mesh_agent))``. Mesh tasks run through ``run_agent``, which
    the agent stops on its own.
    """
    return [_Agent(agent), _Chat(agent), _Function(agent)]
