# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

import asyncio
import logging

from langchain_core.tools import tool
from langchain_openai import ChatOpenAI

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.langchain import run_agent


@tool
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> ChatOpenAI:
    return ChatOpenAI(model="gpt-4o-mini", stream_usage=True)


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    model = build_model()

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, model, [lookup_policy], system_prompt="You triage insurance claims.")

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
