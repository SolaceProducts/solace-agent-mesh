# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Make SAM's kill switch, and a stopped mesh task, stop a CrewAI crew at its next step.

CrewAI runs a crew in a worker thread that asyncio cannot cancel, and it swallows exceptions from step callbacks
and tools (a tool's exception becomes a tool error the model reads). An exception from the model client is what
ends a kickoff, so the checks run in each crew agent's LLM ``call`` and ``acall`` (a streamed call goes through
``call``). CrewAI's public before-LLM-call hook (``crewai.hooks``) cannot do it: it ends a kickoff only with CrewAI's
own ``HookAborted``, and swallows any other exception a hook raises and makes the call anyway, so ``AgentStopped`` or
``TaskHalted`` could never come out of it (checked with crewai 1.15.27).
"""

from __future__ import annotations

import inspect
from collections.abc import Callable, Iterable
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from ..agent import MeshAgent

_CHECKS = "_sam_link_checks"


def guard_llm(llm: Any, check: Callable[[], None]) -> None:
    """Run `check` before every call of this LLM instance (idempotent per check). A model given as a string has no
    instance yet and is skipped; CrewAI turns it into one when the Agent is built, so guard the built agents."""
    if llm is None or isinstance(llm, str) or not callable(getattr(llm, "call", None)):
        return
    checks: list[Callable[[], None]] | None = getattr(llm, _CHECKS, None)
    if checks is None:
        checks = []
        original = llm.call

        def guarded_call(*args, **kwargs):
            for c in list(checks):
                c()
            return original(*args, **kwargs)

        object.__setattr__(llm, "call", guarded_call)
        object.__setattr__(llm, _CHECKS, checks)
        original_acall = getattr(llm, "acall", None)
        if inspect.iscoroutinefunction(original_acall):

            async def guarded_acall(*args, **kwargs):
                for c in list(checks):
                    c()
                return await original_acall(*args, **kwargs)

            object.__setattr__(llm, "acall", guarded_acall)
    if check not in checks:
        checks.append(check)


def guard_agents(agents: Iterable[Any], check: Callable[[], None]) -> None:
    for a in agents:
        guard_llm(getattr(a, "llm", None), check)
        guard_llm(getattr(a, "function_calling_llm", None), check)


_MANAGER_CHECKS = "_sam_link_manager_checks"


def guard_crew(crew: Any, check: Callable[[], None]) -> None:
    """Run `check` before every model call of `crew`: its agents', and its manager's in a hierarchical crew. CrewAI
    builds the manager (from ``manager_llm``, often a model name) only when the kickoff starts, so the crew's
    ``_create_manager_agent`` is wrapped to guard the manager it makes (idempotent per check)."""
    guard_agents([*(getattr(crew, "agents", []) or []), getattr(crew, "manager_agent", None)], check)
    guard_llm(getattr(crew, "manager_llm", None), check)
    if not callable(getattr(crew, "_create_manager_agent", None)):
        return
    checks: list[Callable[[], None]] | None = getattr(crew, _MANAGER_CHECKS, None)
    if checks is None:
        checks = []
        create = crew._create_manager_agent

        def create_guarded_manager(*args: Any, **kwargs: Any) -> Any:
            made = create(*args, **kwargs)
            for c in list(checks):
                guard_agents([getattr(crew, "manager_agent", None)], c)
                guard_llm(getattr(crew, "manager_llm", None), c)
            return made

        object.__setattr__(crew, "_create_manager_agent", create_guarded_manager)
        object.__setattr__(crew, _MANAGER_CHECKS, checks)
    if check not in checks:
        checks.append(check)


def stop_guard(agent: MeshAgent, crew: Any) -> Any:
    """Make `crew` raise AgentStopped at kickoff and at its next model call while SAM has stopped `agent`; returns
    the crew.

    CrewAI has no hook object to attach, so this patches the crew you give it. Use it on crews run outside the mesh
    (your own API, a scheduled job): ``crew = stop_guard(mesh_agent, Crew(...))``. ``run_agent`` already stops a
    mesh task's crew.
    """
    guard_crew(crew, agent.check)
    if not getattr(crew, "_sam_link_kickoff_guarded", False):
        kickoff = crew.kickoff

        def guarded_kickoff(*args, **kwargs):
            agent.check()
            return kickoff(*args, **kwargs)

        object.__setattr__(crew, "kickoff", guarded_kickoff)
        object.__setattr__(crew, "_sam_link_kickoff_guarded", True)
    return crew
