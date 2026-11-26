# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Hermes Agent (Nous Research) adapter for sam_link_sdk."""

from .._frameworks import adopt
from .peer_tools import MeshTool, mesh_agent_tools
from .run import agent_defaults, run_agent
from .stop import StopGuard, stop_guard
from .tools import Approval, tool_infos, with_approval

adopt("Hermes Agent")

__all__ = [
    "mesh_agent_tools",
    "run_agent",
    "stop_guard",
    "tool_infos",
    "with_approval",
    "Approval",
    "MeshTool",
    "StopGuard",
    "agent_defaults",
]
"""The names every adapter has (Hermes needs no step_reporter: run_agent installs process-wide hooks; and no ask_user_tool:
run_agent routes Hermes' own clarify tool to SAM).
"""
