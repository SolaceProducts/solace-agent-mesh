# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Run a mesh agent on Amazon Bedrock AgentCore Runtime (HTTP protocol).

    from sam_link_sdk.agentcore import serve
    serve(build_agent)        # build_agent(link_url) -> MeshAgent

Nothing runs between requests. SAM's agent link entrypoint rings a doorbell: it POSTs to the runtime's invocation
endpoint when a task waits for the agent, AgentCore starts (or reuses) a session, and this handler connects back over
the agent link with ``MeshAgent.handle_wake``: it checks SAM's stop, serves what waits, and returns once nothing more
comes.

The serving runs as an AgentCore async task, so the session reports HealthyBusy and stays up while the agent waits for
a person, after the caller has stopped waiting on the POST.

Secrets: any environment variable ``NAME_SECRET_ID`` names a Secrets Manager secret (its name or ARN) whose value
becomes ``NAME`` before the agent is built (for example SAM_LINK_TOKEN_SECRET_ID), read with the runtime's execution
role.

The same file is also the agent's operator commands (``announce``, ``retire``; see sam_link_sdk.hosted).

Needs the ``agentcore`` extra (bedrock-agentcore, boto3).
"""

from __future__ import annotations

import asyncio
import logging
import os
import sys
import time
from typing import Any

from ._drift import require
from .hosted import WAKE_TIMEOUT_SECONDS, BuildAgent, link_url_from, run_operator_command

__all__ = ["WAKE_TIMEOUT_SECONDS", "app", "load_secrets", "serve"]

TESTED_VERSION = "1.25"

log = logging.getLogger(__name__)
SECRET_SUFFIX = "_SECRET_ID"


def load_secrets(environ: dict[str, str] | None = None, client: Any = None) -> list[str]:
    """Resolve every ``NAME_SECRET_ID`` into ``NAME`` from Secrets Manager; return the names set (never the values)."""
    env = os.environ if environ is None else environ
    wanted = {k[: -len(SECRET_SUFFIX)]: v for k, v in env.items() if k.endswith(SECRET_SUFFIX) and v}
    if not wanted:
        return []
    if client is None:
        import boto3

        client = boto3.client("secretsmanager")
    for name, arn in wanted.items():
        env[name] = client.get_secret_value(SecretId=arn)["SecretString"]
    return sorted(wanted)


def app(build_agent: BuildAgent, timeout_seconds: float = WAKE_TIMEOUT_SECONDS):
    """The runtime's server: one entry point, the doorbell."""
    require(
        "sam_link_sdk.agentcore",
        "Amazon Bedrock AgentCore",
        "bedrock-agentcore",
        TESTED_VERSION,
        [
            "bedrock_agentcore.runtime:BedrockAgentCoreApp.entrypoint",
            "bedrock_agentcore.runtime:BedrockAgentCoreApp.add_async_task",
            "bedrock_agentcore.runtime:BedrockAgentCoreApp.complete_async_task",
            "bedrock_agentcore.runtime:BedrockAgentCoreApp.run",
        ],
    )
    from bedrock_agentcore.runtime import BedrockAgentCoreApp

    core = BedrockAgentCoreApp()
    serving: set[asyncio.Task] = set()

    async def serve_queue(link_url: str, task_id: int) -> int:
        try:
            return await asyncio.wait_for(build_agent(link_url).handle_wake(), timeout_seconds)
        finally:
            core.complete_async_task(task_id)

    @core.entrypoint
    async def wake(_payload: Any) -> dict:
        """Serve the tasks waiting for the agent and report how many were taken.

        The serving is shielded: if AgentCore drops the request because the caller stopped waiting, the serving carries
        on.
        """
        link_url = link_url_from()
        if not link_url:
            raise RuntimeError("no agent link address: set SAM_LINK_URL")
        started = time.monotonic()
        task = asyncio.ensure_future(serve_queue(link_url, core.add_async_task("mesh-wake")))
        serving.add(task)
        task.add_done_callback(serving.discard)
        taken = await asyncio.shield(task)
        return {"served": True, "messages": taken, "seconds": round(time.monotonic() - started, 1)}

    return core


def serve(build_agent: BuildAgent, argv: list[str] | None = None) -> None:
    """Entry point for the runtime's main.py: with no argument, run the server AgentCore starts; with ``announce``
    or ``retire``, describe the agent to SAM or take it off the mesh, and exit."""
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")
    args = sys.argv[1:] if argv is None else argv
    command = args[0] if args else "serve"
    loaded = load_secrets()
    if loaded:
        log.info("secrets loaded: %s", ", ".join(loaded))
    if command == "serve":
        app(build_agent).run(host="0.0.0.0")  # noqa: S104 - AgentCore's contract: the agent listens on 0.0.0.0:8080
        return
    run_operator_command(build_agent, command)
