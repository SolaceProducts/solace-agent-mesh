# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""What every request-driven runtime wrapper shares (sam_link_sdk.foundry, sam_link_sdk.agentcore): the wake's time limit,
where the agent link's address comes from, and the operator commands a hosted agent's main.py also answers.

    python main.py announce   # after deploying, and after SAM's gateway restarts: SAM lists the agent again
    python main.py retire     # when deleting the deployment: SAM stops listing it
"""

from __future__ import annotations

import asyncio
import os
from collections.abc import Callable

from .agent import MeshAgent

WAKE_TIMEOUT_SECONDS = 50 * 60.0
"""Covers the longest wait for a person: an approval or a question times out after 45 minutes (sam_link_sdk.prompting)."""

BuildAgent = Callable[[str], MeshAgent]


def link_url_from() -> str:
    """The agent link's address the agent joins the mesh at: SAM_LINK_URL (the entrypoint's connect URL)."""
    return os.environ.get("SAM_LINK_URL", "")


def run_operator_command(build_agent: BuildAgent, command: str) -> None:
    """``announce`` or ``retire``: describe the agent to SAM, or retire it, then exit."""
    link_url = link_url_from()
    if not link_url:
        raise SystemExit("set SAM_LINK_URL and SAM_LINK_TOKEN to announce or retire the agent")
    if command == "announce":
        asyncio.run(build_agent(link_url).announce())
    elif command == "retire":
        asyncio.run(build_agent(link_url).retire())
    else:
        raise SystemExit(f"unknown command {command!r}: serve (default), announce or retire")
