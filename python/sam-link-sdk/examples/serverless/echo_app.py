# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The smallest request-driven agent: `python echo_app.py wake` connects over SAM's agent link, serves what waits for
it and exits (as the runtime stand-in starts it); `python echo_app.py announce` describes the agent to SAM once.

Mesh: SAM_LINK_URL and SAM_LINK_TOKEN, this agent's own token; SAM_AGENT_NAME (default echo-serverless)."""

from __future__ import annotations

import asyncio
import logging
import os
import sys

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext

NAME = os.environ.get("SAM_AGENT_NAME", "echo-serverless")


def build() -> MeshAgent:
    async def handle(ctx: TaskContext) -> str:
        return f"echo from a process started for this request (pid {os.getpid()}): {ctx.text}"

    config = AgentConfig(
        name=NAME,
        display_name="Echo (serverless)",
        description="Echoes the request; runs only when a request arrives",
        skills=[Skill(id="echo", name="Echo", description="Echoes the request")],
        keepalive_seconds=0,
    )
    return MeshAgent(config, handle)


async def main(mode: str) -> None:
    logging.basicConfig(level=logging.INFO, format=f"%(asctime)s [{mode} %(process)d] %(name)s %(message)s")
    agent = build()
    if mode == "announce":
        await agent.announce()
    else:
        await agent.handle_wake()


if __name__ == "__main__":
    asyncio.run(main(sys.argv[1] if len(sys.argv) > 1 else "wake"))
