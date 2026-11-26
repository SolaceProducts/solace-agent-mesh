# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

import asyncio
import logging

from strands import Agent, tool
from strands.models.openai import OpenAIModel

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.strands import run_agent


@tool
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> OpenAIModel:
    return OpenAIModel(model_id="gpt-4o-mini")


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    model = build_model()

    def build(ctx: TaskContext) -> Agent:
        return Agent(
            model=model,
            system_prompt="You triage insurance claims.",
            tools=[lookup_policy],
            callback_handler=None,
        )

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, build)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
