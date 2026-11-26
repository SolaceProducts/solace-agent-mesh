# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

import asyncio
import logging

from google.adk.agents import LlmAgent
from google.adk.models.lite_llm import LiteLlm
from google.adk.runners import InMemoryRunner

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.adk import run_agent, step_reporter


def lookup_policy(policy_id: str) -> dict:
    """Look up an insurance policy by id."""
    return {"policy_id": policy_id, "status": "active", "covers": "water damage"}


def build_model() -> LiteLlm:
    return LiteLlm(model="openai/gpt-4o-mini")


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    triage = LlmAgent(
        name="claims_triage",
        model=build_model(),
        instruction="You triage insurance claims.",
        tools=[lookup_policy],
    )
    runner = InMemoryRunner(agent=triage, app_name="claims_triage", plugins=[step_reporter()])

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, runner)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
