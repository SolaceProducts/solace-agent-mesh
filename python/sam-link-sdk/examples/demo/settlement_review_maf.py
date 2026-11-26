# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Settlement Review: a Microsoft Agent Framework agent that settles a claim. A large settlement waits for a person.

Started by stack/start_agents.sh (README.md); on its own:
`uv run --extra agent-framework python settlement_review_maf.py`.
"""

from __future__ import annotations

import asyncio

from agent_framework import Agent, tool
from agent_framework.openai import OpenAIChatCompletionClient
from common import SMALL_SETTLEMENT_USD, mesh_settings, next_ticket, openai_compatible_model

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.agent_framework import run_agent, tool_infos, with_approval


@tool(approval_mode="never_require")
def settle_small(claim_id: str, amount_usd: float) -> dict:
    """Settle a claim for an amount up to $5,000: schedule the payment and return its ticket."""
    return {"claim_id": claim_id, "paid_usd": amount_usd, "ticket": next_ticket(), "status": "payment scheduled"}


@tool(approval_mode="never_require")
def settle_large(claim_id: str, amount_usd: float) -> dict:
    """Settle a claim for an amount above $5,000: schedule the payment and return its ticket."""
    return {"claim_id": claim_id, "paid_usd": amount_usd, "ticket": next_ticket(), "status": "payment scheduled"}


TOOLS = [settle_small, settle_large]
INSTRUCTIONS = f"""You review and settle insurance claims. You are given a claim id, the payable amount, the fraud risk and
a repair estimate. Settle the claim for the payable amount as soon as it is covered (payable amount above zero) and the
fraud risk is not high: use settle_small for amounts up to ${SMALL_SETTLEMENT_USD:,} and settle_large above that. The
repair estimate and any rules you are told about are for your information; they are not conditions, so do not hold a claim
for them. If the claim is not covered or the fraud risk is high, do not settle and say why. Answer in two sentences with
the result and the ticket."""


def build_model() -> OpenAIChatCompletionClient:
    llm = openai_compatible_model()
    return OpenAIChatCompletionClient(model=llm["model"], api_key=llm["api_key"], base_url=llm["base_url"])


async def main() -> None:
    agent = Agent(build_model(), INSTRUCTIONS, name="settlement_review", tools=TOOLS)

    async def handle(ctx: TaskContext) -> str:
        approval = with_approval(
            ctx, TOOLS, names=["settle_large"], message="This settlement pays out more than $5,000."
        )
        return await run_agent(ctx, agent, middleware=[approval])

    config = AgentConfig(
        name="settlement-review",
        display_name="Settlement Review",
        description="Settles covered, low-risk claims; a large payout waits for a person (built with Microsoft Agent Framework)",
        framework="Microsoft Agent Framework",
        skills=[Skill(id="settle", name="Settle a claim", description="Pays a covered claim after the checks")],
        tools=tool_infos(TOOLS),
        **mesh_settings(),
    )
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
