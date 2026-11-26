# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Run a mesh agent as an Azure AI Foundry hosted agent (Invocations protocol).

    from sam_link_sdk.foundry import serve
    serve(build_agent)        # build_agent(link_url) -> MeshAgent

Nothing runs between requests. SAM's agent link entrypoint rings a doorbell: it POSTs to the hosted agent's invocation
endpoint when a task waits for the agent, Foundry starts a sandbox, and this handler connects back over the agent link
with ``MeshAgent.handle_wake``: it checks SAM's stop, serves what waits, and returns once nothing more comes.

The same file is also the agent's operator commands, run anywhere that reaches the entrypoint:

    python main.py announce   # after deploying, and after SAM's gateway restarts: SAM lists the agent again
    python main.py retire     # when deleting the deployment: SAM stops listing it

Needs the ``foundry`` extra (azure-ai-agentserver-invocations).
"""

from __future__ import annotations

import asyncio
import logging
import sys
import time

from ._drift import require
from .hosted import WAKE_TIMEOUT_SECONDS, BuildAgent, link_url_from, run_operator_command

__all__ = ["WAKE_TIMEOUT_SECONDS", "app", "serve"]

TESTED_VERSION = "1.1.0"

log = logging.getLogger(__name__)


def app(build_agent: BuildAgent, timeout_seconds: float = WAKE_TIMEOUT_SECONDS):
    """The hosted agent's server: one handler, the doorbell."""
    require(
        "sam_link_sdk.foundry",
        "Azure AI Foundry hosted agents",
        "azure-ai-agentserver-invocations",
        TESTED_VERSION,
        [
            "azure.ai.agentserver.invocations:InvocationAgentServerHost.invoke_handler",
            "azure.ai.agentserver.invocations:InvocationAgentServerHost.run",
        ],
    )
    from azure.ai.agentserver.invocations import InvocationAgentServerHost
    from starlette.requests import Request
    from starlette.responses import JSONResponse

    host = InvocationAgentServerHost()

    @host.invoke_handler
    async def wake(request: Request):
        """Serve the tasks waiting for the agent and report how many were taken.

        The tasks are served before the response goes out, because Foundry may suspend the sandbox once it is sent. The
        entrypoint may give up on the POST and ring again; the next sandbox finds the task already held here.
        """
        link_url = link_url_from()
        if not link_url:
            return JSONResponse({"served": False, "reason": "no agent link address (SAM_LINK_URL)"}, status_code=400)
        started = time.monotonic()
        try:
            taken = await asyncio.wait_for(build_agent(link_url).handle_wake(), timeout_seconds)
        except Exception as exc:  # noqa: BLE001 - the type goes back to the caller; details stay in the log
            log.exception("wake failed")
            return JSONResponse(
                {"served": False, "error": type(exc).__name__, "seconds": round(time.monotonic() - started, 1)},
                status_code=500,
            )
        return JSONResponse({"served": True, "messages": taken, "seconds": round(time.monotonic() - started, 1)})

    return host


def serve(build_agent: BuildAgent, argv: list[str] | None = None) -> None:
    """Entry point for the hosted agent's main.py: with no argument, run the server Foundry starts; with
    ``announce`` or ``retire``, describe the agent to SAM or take it off the mesh, and exit."""
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")
    args = sys.argv[1:] if argv is None else argv
    command = args[0] if args else "serve"
    if command == "serve":
        app(build_agent).run()
        return
    run_operator_command(build_agent, command)
