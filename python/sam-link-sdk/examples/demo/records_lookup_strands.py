# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Records Lookup: a Strands Agents agent that returns a policy's earlier claims.

Started by stack/start_agents.sh (README.md); on its own: `uv run --extra strands python records_lookup_strands.py`.
"""

from __future__ import annotations

import asyncio

from common import CLAIM_HISTORY, CLAIMS, mesh_settings, openai_compatible_model
from strands import Agent, tool
from strands.models.openai import OpenAIModel

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.strands import run_agent, tool_infos

ARCHIVE_API_TOKEN = "arch_live_4f9a1c7e2b8d6035"


@tool
def prior_claims(policy_id: str) -> list:
    """The earlier claims filed on a policy: claim id, loss type, amount and date."""
    return CLAIM_HISTORY.get(policy_id.upper(), [])


@tool
def policy_of_claim(claim_id: str) -> dict:
    """The policy a claim was filed on."""
    claim = CLAIMS.get(claim_id.upper())
    if claim is None:
        return {"error": f"no claim {claim_id}"}
    # A careless archive client hands back its credential with the data. The SDK redacts credentials before it
    # reports a tool result, so SAM's record of this call shows <REDACTED> in its place.
    return {"policy_id": claim["policy_id"], "archive_api_token": ARCHIVE_API_TOKEN}


TOOLS = [prior_claims, policy_of_claim]
PROMPT = (
    "You are the records clerk of an insurance company. Given a policy id or a claim id, return the policy's earlier "
    "claims as a short list (claim id, loss type, amount, date), or say there are none. Never repeat a token."
)


def build_model() -> OpenAIModel:
    llm = openai_compatible_model()
    return OpenAIModel(client_args={"api_key": llm["api_key"], "base_url": llm["base_url"]}, model_id=llm["model"])


async def main() -> None:
    model = build_model()

    def build(ctx: TaskContext) -> Agent:
        return Agent(model=model, tools=TOOLS, system_prompt=PROMPT, callback_handler=None)

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, build)

    config = AgentConfig(
        name="records-lookup",
        display_name="Records Lookup",
        description="Returns the earlier claims filed on a policy (built with Strands Agents)",
        framework="Strands Agents",
        skills=[Skill(id="history", name="Claim history", description="Earlier claims for a policy or claim id")],
        tools=tool_infos(TOOLS),
        **mesh_settings(),
    )
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
