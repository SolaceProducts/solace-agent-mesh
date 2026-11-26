# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Coverage Explainer: a Strands Agents agent on a request-driven runtime. Nothing runs between requests: SAM's agent
link entrypoint calls the runtime when a request waits for the agent, and the runtime starts this script, which
connects back over the agent link, serves what waits and exits.

    python coverage_explainer_strands.py wake       # serve what waits, then exit (the runtime does this)
    python coverage_explainer_strands.py announce   # describe the agent to SAM once, after deploying

Mesh: SAM_LINK_URL and SAM_LINK_TOKEN, this agent's own token (docs/agent-link.md).

Model: an OpenAI-compatible endpoint from DEMO_OPENAI_BASE_URL / LLM_SERVICE_ENDPOINT, LLM_SERVICE_API_KEY and
DEMO_OPENAI_MODEL / LLM_SERVICE_GENERAL_MODEL_NAME. The policy wordings below are fictional.
"""

from __future__ import annotations

import asyncio
import logging
import os
import sys

from strands import Agent, tool
from strands.models.openai import OpenAIModel

from sam_link_sdk import AgentConfig, DataAccess, MeshAgent, Skill, TaskContext
from sam_link_sdk.strands import run_agent, tool_infos

NAME = "coverage-explainer"
WORDINGS = {
    "gold": {
        "water damage": "covered, including burst pipes and seepage",
        "deductible_usd": 250,
        "temporary housing": "up to 30 nights",
        "contents": "replacement value",
    },
    "silver": {
        "water damage": "sudden bursts only; seepage excluded",
        "deductible_usd": 1000,
        "temporary housing": "up to 10 nights",
        "contents": "replacement value up to 20,000 USD",
    },
    "bronze": {
        "water damage": "excluded",
        "deductible_usd": 2500,
        "temporary housing": "not covered",
        "contents": "actual cash value",
    },
}


@tool
def coverage(tier: str) -> dict:
    """What a policy tier (gold, silver or bronze) covers: water damage, deductible, temporary housing, contents."""
    return WORDINGS.get(tier.strip().lower(), {"error": f"no tier {tier}"})


TOOLS = [coverage]
PROMPT = (
    "You explain home insurance coverage in plain language. Use the coverage tool for the tier asked about, and "
    "answer in two or three sentences, quoting the deductible."
)


def build() -> MeshAgent:
    base_url = os.environ.get("DEMO_OPENAI_BASE_URL") or os.environ["LLM_SERVICE_ENDPOINT"]
    model_id = os.environ.get("DEMO_OPENAI_MODEL") or os.environ["LLM_SERVICE_GENERAL_MODEL_NAME"].removeprefix(
        "openai/"
    )
    model = OpenAIModel(
        client_args={"api_key": os.environ["LLM_SERVICE_API_KEY"], "base_url": base_url}, model_id=model_id
    )

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(
            ctx, lambda _ctx: Agent(model=model, tools=TOOLS, system_prompt=PROMPT, callback_handler=None)
        )

    config = AgentConfig(
        name=NAME,
        display_name="Coverage Explainer",
        description="Explains what a policy tier covers (built with Strands Agents; runs only when a request arrives)",
        skills=[Skill(id="coverage", name="Explain coverage", description="What gold, silver or bronze cover")],
        tools=tool_infos(TOOLS),
        owner="admin@example.com",
        team="Claims",
        framework="Strands Agents (serverless)",
        data_access=[DataAccess("Policy wordings", "public")],
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
