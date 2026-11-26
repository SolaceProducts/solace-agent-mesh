# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A minimal request-driven agent behind a runtime wrapper, run exactly as a hosted agent's main.py is:

    python hosted_agent.py foundry              the Foundry hosted agent's server (sam_link_sdk.foundry.serve), on PORT
    python hosted_agent.py agentcore            the AgentCore Runtime's server (sam_link_sdk.agentcore.app), on PORT
    python hosted_agent.py <runtime> announce   the operator command, through the wrapper's serve()

Each POST to the server's /invocations builds a fresh agent (it logs WAKE_MARKER) and serves what waits for it over
the agent link, from the environment (SAM_LINK_URL, SAM_LINK_TOKEN or SAM_LINK_TOKEN_FILE, SAM_AGENT_NAME).

    "slow <seconds> ..."   answers after that many seconds, and gives way to a cancel
    anything else          answers "woken in pid <pid>: <text>"

AgentCore's serve() listens on 0.0.0.0:8080, the runtime's contract, so its server is started here with the same
app() on a port the test chose; the Foundry host takes its port from PORT.
"""

from __future__ import annotations

import asyncio
import dataclasses
import logging
import os
import sys

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.link import LinkConfig, LinkTransport

WAKE_MARKER = "hosted agent built for a wake"


def build_agent(link_url: str) -> MeshAgent:
    async def handle(ctx: TaskContext) -> str:
        words = ctx.text.split()
        if words[:1] == ["slow"]:
            await asyncio.sleep(float(words[1]))
            return f"slow task done: {ctx.text}"
        return f"woken in pid {os.getpid()}: {ctx.text}"

    name = os.environ["SAM_AGENT_NAME"]
    logging.getLogger("hosted_agent").info("%s pid=%d", WAKE_MARKER, os.getpid())
    config = AgentConfig(
        name=name,
        display_name=name,
        description="A request-driven agent woken through its runtime's own server",
        skills=[Skill(id="echo", name="Echo", description="Echoes the request")],
        keepalive_seconds=0,
    )
    return MeshAgent(
        config, handle, transport=LinkTransport(dataclasses.replace(LinkConfig.from_env(agent=name), url=link_url))
    )


def main(runtime: str, args: list[str]) -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    if runtime == "foundry":
        from sam_link_sdk.foundry import serve

        serve(build_agent, args)
    elif args:
        from sam_link_sdk.agentcore import serve

        serve(build_agent, args)
    else:
        from sam_link_sdk.agentcore import app

        app(build_agent).run(port=int(os.environ["PORT"]), host="127.0.0.1")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2:])
