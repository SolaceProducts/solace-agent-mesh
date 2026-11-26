# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

import asyncio
import logging

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext


async def handle(ctx: TaskContext) -> str:
    await ctx.progress("Looking the claim up")
    return f"Received: {ctx.text}"


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
