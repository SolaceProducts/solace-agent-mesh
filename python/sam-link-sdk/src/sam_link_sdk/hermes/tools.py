# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Approvals and the card's tool manifest for Hermes agents.

``run_agent(ctx, build, approval=with_approval(names=["route_claim"]))`` makes those tools ask the user through SAM
before each call, with the tool's name and arguments shown. Hermes' own dangerous-command prompts (its terminal
tool) go to the same SAM prompt whenever a mesh task runs the agent.

The prompt is asked from Hermes' ``pre_tool_call`` hook, which Hermes bounds by ``plugins.hook_callback_timeout``
(30 s by default, at most 600 s): set it in Hermes' config.yaml to as long as a user may take to answer, or the
call is refused when the time runs out.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
from typing import Any

from .._adapter import ToolScopeMap, name_filter, scoped_infos
from ..config import ToolInfo
from ..context import tool_name


@dataclass(frozen=True)
class Approval:
    """Which tools ask for approval (None: every tool) and the note the user sees with the request."""

    names: frozenset[str] | None = None
    message: str = ""

    def covers(self, tool_name: str) -> bool:
        return self.names is None or tool_name in self.names


def with_approval(*, names: Iterable[str] | None = None, message: str = "") -> Approval:
    """Make chosen tools ask the user for approval before each call (``run_agent(..., approval=with_approval(...))``).

    Hermes builds its tools inside the agent, so this takes no tools: `names` limits it to those tools (default: every
    tool). A call that is denied, cancelled or times out is not run: the model is told so with an error result, which
    is also what the audit trail records.
    """
    if names is not None and not isinstance(names, (str, bytes)):
        names = list(names)
    name_filter(names)
    return Approval(frozenset(names) if names is not None else None, message)


def tool_infos(tools: Iterable[Any], *, scopes: ToolScopeMap | None = None) -> list[ToolInfo]:
    """A card tool manifest (``AgentConfig(tools=...)``) from an agent's tool list (``agent.tools``, OpenAI-format
    definitions).

    `scopes` maps a tool's name to the scopes a user needs to use it (``docs/security.md``): SAM is told
    them, and ``run_agent`` refuses the tool to a user without them. A name that is none of the tools is a
    ValueError.
    """
    infos = []
    for t in tools:
        spec = t.get("function") if isinstance(t, dict) and "function" in t else t
        if isinstance(spec, dict) and tool_name(t):
            infos.append(ToolInfo(tool_name(t), str(spec.get("description") or "")))
    return scoped_infos(infos, scopes)
