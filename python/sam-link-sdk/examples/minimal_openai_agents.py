# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

import asyncio
import logging

from agents import Agent, OpenAIChatCompletionsModel, function_tool, set_tracing_disabled
from openai import AsyncOpenAI

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.openai_agents import run_agent


@function_tool
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> OpenAIChatCompletionsModel:
    return OpenAIChatCompletionsModel(model="gpt-4o-mini", openai_client=AsyncOpenAI())


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    set_tracing_disabled(True)
    triage = Agent(
        name="claims-triage",
        instructions="You triage insurance claims.",
        model=build_model(),
        tools=[lookup_policy],
    )

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, triage)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
