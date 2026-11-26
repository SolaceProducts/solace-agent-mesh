# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Delegating work to other agents on the mesh, as the framework adapters see it: the peers a task may call
(``AgentCard``), the result of a call (``PeerResult``) and the shape of a peer tool's arguments.

How a call reaches the peer is the transport's business (on the agent link, SAM's entrypoint routes it).
"""

from __future__ import annotations

from dataclasses import dataclass, field

from ._model import PeerInfo, PeerResult

__all__ = ["AgentCard", "PeerResult"]

MAX_SKILLS_IN_DESCRIPTION = 8
TASK_DESCRIPTION_HELP = "Detailed description of the task for the peer agent."
PEER_TOOL_PARAMETERS = {
    "type": "object",
    "properties": {"task_description": {"type": "string", "description": TASK_DESCRIPTION_HELP}},
    "required": ["task_description"],
    "additionalProperties": False,
}


@dataclass
class AgentCard:
    """What this agent knows about another agent on the mesh, from its discovery card."""

    name: str
    description: str = ""
    display_name: str = ""
    skills: list[str] = field(default_factory=list)
    on_demand: bool = False

    @property
    def tool_name(self) -> str:
        return "peer_" + self.name

    def tool_description(self) -> str:
        """The description SAM-Go gives its peer_<name> tool."""
        desc = f'Delegate a task to the "{self.display_name or self.name}" agent.'
        if self.description:
            desc += " " + self.description
        if self.skills:
            desc += " Skills: " + ", ".join(self.skills[:MAX_SKILLS_IN_DESCRIPTION])
            if len(self.skills) > MAX_SKILLS_IN_DESCRIPTION:
                desc += f" and {len(self.skills) - MAX_SKILLS_IN_DESCRIPTION} more"
        return desc


def card_of(peer: PeerInfo) -> AgentCard:
    return AgentCard(
        name=peer.name,
        description=peer.description,
        display_name=peer.display_name,
        skills=[s.name for s in peer.skills],
        on_demand=peer.on_demand,
    )
