# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

import asyncio
import json
import logging
import os

from run_agent import AIAgent
from tools.registry import registry

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.hermes import agent_defaults, run_agent

SCHEMA = {
    "name": "lookup_policy",
    "description": "Look up an insurance policy by id.",
    "parameters": {
        "type": "object",
        "properties": {"policy_id": {"type": "string"}},
        "required": ["policy_id"],
    },
}


def lookup_policy(args: dict, **kwargs) -> str:
    return json.dumps({"policy_id": args["policy_id"], "status": "active", "covers": "water damage"})


def build_model() -> dict:
    return {
        "base_url": os.environ.get("OPENAI_BASE_URL"),
        "api_key": os.environ["OPENAI_API_KEY"],
        "model": "gpt-4o-mini",
    }


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    registry.register(
        name="lookup_policy",
        toolset="claims",
        description=SCHEMA["description"],
        handler=lookup_policy,
        schema=SCHEMA,
    )
    model = build_model()

    def build(ctx: TaskContext) -> AIAgent:
        return AIAgent(
            **agent_defaults(ctx),
            **model,
            enabled_toolsets=["claims"],
            ephemeral_system_prompt="You triage insurance claims.",
        )

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, build)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
