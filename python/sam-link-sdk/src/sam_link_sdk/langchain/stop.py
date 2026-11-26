# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Make SAM's kill switch stop a LangChain or LangGraph agent wherever it runs, not only on the mesh."""

from __future__ import annotations

from typing import TYPE_CHECKING, Any

from langchain_core.callbacks import BaseCallbackHandler

if TYPE_CHECKING:
    from ..agent import MeshAgent


class StopGuard(BaseCallbackHandler):
    """Raises AgentStopped at the start of every chain, model call and tool while SAM has stopped the agent (what
    ``stop_guard`` returns)."""

    raise_error = True
    """LangChain swallows callback errors otherwise."""
    run_inline = True

    def __init__(self, agent: MeshAgent):
        self._agent = agent

    def on_chain_start(self, serialized: dict[str, Any] | None, inputs: Any, **kwargs: Any) -> None:
        self._agent.check()

    def on_chat_model_start(self, serialized: dict[str, Any] | None, messages: Any, **kwargs: Any) -> None:
        self._agent.check()

    def on_llm_start(self, serialized: dict[str, Any] | None, prompts: Any, **kwargs: Any) -> None:
        self._agent.check()

    def on_tool_start(self, serialized: dict[str, Any] | None, input_str: str, **kwargs: Any) -> None:
        self._agent.check()


def stop_guard(agent: MeshAgent) -> StopGuard:
    """A callback handler that raises AgentStopped at the start of every chain, model call and tool while SAM has
    stopped `agent`.

    Attach it where the agent's model or graph is built, so every run is covered, including runs that never touch
    the mesh (your own API, a notebook, a scheduled job): ``ChatOpenAI(..., callbacks=[stop_guard(agent)])``, or
    ``graph.with_config(callbacks=[stop_guard(agent)])``.
    """
    return StopGuard(agent)
