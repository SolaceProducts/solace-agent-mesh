# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

import asyncio
import logging

from agent_framework import Agent, tool
from agent_framework.openai import OpenAIChatCompletionClient

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.agent_framework import run_agent


@tool(approval_mode="never_require")
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> OpenAIChatCompletionClient:
    return OpenAIChatCompletionClient(model="gpt-4o-mini")


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    triage = Agent(build_model(), "You triage insurance claims.", name="claims-triage", tools=[lookup_policy])

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, triage)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
