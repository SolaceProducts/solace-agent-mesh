# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Make SAM's kill switch stop a Hermes agent wherever it runs, not only on the mesh.

Mesh tasks stop on their own through ``run_agent``. A Hermes install does more than serve the mesh (a CLI, a messaging
gateway, scheduled jobs), so while SAM has the agent stopped this guard:

- refuses every tool call, whichever agent in the process makes it;
- drops inbound messages at Hermes' messaging gateway;
- interrupts the agents it watches that are in a turn, which stop at their next safe point. An idle agent is not
  interrupted: Hermes keeps an interrupt until a turn takes it, so its first turn after the release would end at once.

Hermes calls its hooks from its own code, so install the guard where the process starts, before the agents run.
"""

from __future__ import annotations

import threading
import weakref
from typing import TYPE_CHECKING, Any

from ..stop import AgentStopped
from .steps import install_hooks, remove_hooks

if TYPE_CHECKING:
    from ..agent import MeshAgent


class StopGuard:
    """While SAM has `agent` stopped, refuses every tool call and inbound gateway message in the process, and
    interrupts the turns of the Hermes agents it watches (what ``stop_guard`` returns). It installs itself when made."""

    def __init__(self, agent: MeshAgent):
        self._mesh = agent
        self._lock = threading.Lock()
        """Turns in progress per watched agent, and the agents interrupted during theirs. One lock orders a turn's start
        and end against a stop, so a stop interrupts exactly the turns it can still end.
        """
        self._in_turn: weakref.WeakKeyDictionary[Any, int] = weakref.WeakKeyDictionary()
        self._interrupted: weakref.WeakSet[Any] = weakref.WeakSet()
        self._hooks = [("pre_tool_call", self._pre_tool_call), ("pre_gateway_dispatch", self._pre_gateway_dispatch)]
        agent.on_stop_change(self._on_change)
        install_hooks(self._hooks)

    def uninstall(self) -> None:
        """Take the guard's hooks out of Hermes (it stays installed for the life of the process otherwise)."""
        remove_hooks(self._hooks)

    def watch(self, hermes_agent: Any) -> Any:
        """Refuse to start while stopped, and interrupt `hermes_agent`'s turn when SAM stops it. Returns the agent."""
        self._mesh.check()
        original = hermes_agent.run_conversation

        def guarded(*args: Any, **kwargs: Any) -> Any:
            with self._lock:
                self._mesh.check()
                self._in_turn[hermes_agent] = self._in_turn.get(hermes_agent, 0) + 1
            try:
                return original(*args, **kwargs)
            finally:
                self._end_turn(hermes_agent)

        hermes_agent.run_conversation = guarded
        return hermes_agent

    def check(self) -> None:
        """Raise AgentStopped while SAM has the agent stopped (for your own entry points)."""
        self._mesh.check()

    def _end_turn(self, hermes_agent: Any) -> None:
        """Forget a finished turn and undo an interrupt that outlived it.

        Hermes clears the interrupt when a turn ends; one that landed after that would end the next turn.
        """
        with self._lock:
            left = self._in_turn.pop(hermes_agent, 1) - 1
            if left:
                self._in_turn[hermes_agent] = left
                return
            if hermes_agent in self._interrupted:
                self._interrupted.discard(hermes_agent)
                if hermes_agent.is_interrupted:
                    hermes_agent.clear_interrupt()

    def _on_change(self, allowed: bool, reason: str) -> None:
        """Interrupt the agent's turns when SAM stops it.

        Listeners hear a change after it happened, so the stop is checked again before interrupting: a stop heard once
        SAM has released the agent again must not interrupt a turn the release let start.
        """
        if allowed:
            return
        with self._lock:
            try:
                self._mesh.check()
                return
            except AgentStopped:
                pass
            for hermes_agent in list(self._in_turn):
                self._interrupted.add(hermes_agent)
                hermes_agent.hard_interrupt()

    def _pre_tool_call(self, **kw: Any) -> dict | None:
        try:
            self._mesh.check()
        except AgentStopped as stop:
            return {
                "action": "block",
                "message": f"This agent was stopped by SAM ({stop.reason}), so the tool was not run.",
            }
        return None

    def _pre_gateway_dispatch(self, **kw: Any) -> dict | None:
        try:
            self._mesh.check()
        except AgentStopped as stop:
            return {"action": "skip", "reason": f"agent stopped by SAM ({stop.reason})"}
        return None


def stop_guard(agent: MeshAgent) -> StopGuard:
    """Install the guard for `agent` (see the module docstring) and return it: ``guard = stop_guard(mesh_agent);
    guard.watch(hermes_agent)``. Install it where the process starts, before the agents run."""
    return StopGuard(agent)
