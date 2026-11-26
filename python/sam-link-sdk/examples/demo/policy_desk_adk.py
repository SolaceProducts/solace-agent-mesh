# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Policy Desk: a Google ADK agent that checks coverage and asks the regulations agent which rule applies.

Started by stack/start_agents.sh (README.md); on its own: `uv run --extra adk python policy_desk_adk.py`.
"""

from __future__ import annotations

import asyncio

from common import CLAIMS, JURISDICTIONS, POLICIES, litellm_model, mesh_settings
from google.adk.agents import LlmAgent
from google.adk.models.lite_llm import LiteLlm
from google.adk.runners import InMemoryRunner

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.adk import mesh_agent_tools, run_agent, step_reporter, tool_infos


def get_claim(claim_id: str) -> dict:
    """Look up a filed claim: its policy, loss type, amount and summary."""
    return CLAIMS.get(claim_id.upper(), {"error": f"no claim {claim_id}"})


def get_policy(policy_id: str) -> dict:
    """Look up a policy: holder, product, tier, status, deductible, limit, covered perils and jurisdiction."""
    policy = POLICIES.get(policy_id.upper())
    return (
        {**policy, "jurisdiction": JURISDICTIONS[policy_id.upper()]} if policy else {"error": f"no policy {policy_id}"}
    )


def check_coverage(policy_id: str, loss_type: str, amount_usd: float) -> dict:
    """Decide whether a loss is covered and how much the policy pays after the deductible."""
    policy = POLICIES.get(policy_id.upper())
    if policy is None:
        return {"error": f"no policy {policy_id}"}
    covered = policy["status"] == "active" and loss_type.lower() in policy["covers"]
    payable = min(max(amount_usd - policy["deductible_usd"], 0), policy["limit_usd"]) if covered else 0
    return {
        "covered": covered,
        "policy_status": policy["status"],
        "deductible_usd": policy["deductible_usd"],
        "payable_usd": payable,
    }


TOOLS = [get_claim, get_policy, check_coverage]
INSTRUCTION = """You are the policy desk of an insurance company. For a claim, look up the claim and its policy and check
coverage. Then ask the regulations agent which settlement rule applies in the policyholder's jurisdiction for this kind
of loss, and mention it. Answer in three sentences: covered or not, the payable amount, and the rule."""


def build_model() -> LiteLlm:
    llm = litellm_model()
    return LiteLlm(model=llm["model"], api_key=llm["api_key"], api_base=llm["base_url"])


async def main() -> None:
    agent = LlmAgent(
        name="policy_desk", model=build_model(), instruction=INSTRUCTION, tools=[*TOOLS, *mesh_agent_tools()]
    )
    runner = InMemoryRunner(agent=agent, app_name="policy-desk", plugins=[step_reporter()])

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, runner)

    config = AgentConfig(
        name="policy-desk",
        display_name="Policy Desk",
        description="Checks whether a claim is covered, how much is payable and which rule applies (built with Google ADK)",
        framework="Google ADK",
        peers=["regulations"],
        skills=[
            Skill(id="coverage", name="Check coverage", description="Coverage, payable amount and rule for a claim id")
        ],
        tools=tool_infos(TOOLS),
        **mesh_settings(),
    )
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
