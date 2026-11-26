# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims Intake: a LangChain agent that takes a filed claim through the desk by asking the other agents.

Started by stack/start_agents.sh (README.md); on its own: `uv run --extra openai python claims_intake_langchain.py`.
"""

from __future__ import annotations

import asyncio

from common import CLAIMS, mesh_settings, openai_compatible_model
from langchain_core.tools import tool
from langchain_openai import ChatOpenAI

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.langchain import mesh_agent_tools, run_agent, tool_infos


@tool
def get_claim(claim_id: str) -> dict:
    """Look up a filed claim: its policy, loss type, amount and summary."""
    return CLAIMS.get(claim_id.upper(), {"error": f"no claim {claim_id}"})


TOOLS = [get_claim]
SYSTEM = """You run intake on an insurance claims desk and coordinate the other agents.
For a claim: call get_claim. Then, in one step and at the same time, ask policy-desk for the coverage and payable
amount, fraud-screen for the fraud risk, and repair-estimator for the repair estimate, giving each the claim id.
When they have answered, ask settlement-review to settle the claim, giving it the claim id, the payable amount, the
fraud risk and the repair estimate. Finish with a short summary: coverage, payable amount, fraud risk, estimate and
the settlement result."""


def build_model() -> ChatOpenAI:
    return ChatOpenAI(**openai_compatible_model(), stream_usage=True)


async def main() -> None:
    model = build_model()

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, model, [*TOOLS, *mesh_agent_tools(ctx)], system_prompt=SYSTEM)

    config = AgentConfig(
        name="claims-intake",
        display_name="Claims Intake",
        description="Takes a filed claim through the desk by asking the other agents (built with LangChain)",
        framework="LangChain",
        peers=["policy-desk", "fraud-screen", "repair-estimator", "settlement-review"],
        skills=[
            Skill(
                id="intake",
                name="Process a claim",
                description="Coverage, fraud, estimate and settlement for a claim id",
            )
        ],
        tools=tool_infos(TOOLS),
        **mesh_settings(),
    )
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
