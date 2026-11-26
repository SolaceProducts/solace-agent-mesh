# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Repair Estimator: a LangGraph agent that totals a repair, asking the parts-pricing agent for every price.

Started by stack/start_agents.sh (README.md); on its own:
`uv run --extra langgraph --extra openai python repair_estimator_langgraph.py`.
"""

from __future__ import annotations

import asyncio

from common import DAMAGE, LABOUR_RATE_USD, mesh_settings, openai_compatible_model
from langchain_core.tools import tool
from langchain_openai import ChatOpenAI
from langgraph.prebuilt import create_react_agent

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.langchain import mesh_agent_tools, run_graph, tool_infos


@tool
def damage_items(claim_id: str) -> dict:
    """The items a claim says were damaged and must be repaired or replaced."""
    return {"claim_id": claim_id, "items": DAMAGE.get(claim_id.upper(), [])}


@tool
def estimate_total(item_prices_usd: list[float], labour_hours: float) -> dict:
    """Total a repair: the sum of the item prices plus labour at the company's hourly rate."""
    parts = round(sum(item_prices_usd), 2)
    labour = round(labour_hours * LABOUR_RATE_USD, 2)
    return {"parts_usd": parts, "labour_usd": labour, "labour_rate_usd": LABOUR_RATE_USD, "total_usd": parts + labour}


TOOLS = [damage_items, estimate_total]
PROMPT = (
    "You estimate repair costs. Get the claim's damaged items, ask the parts-pricing agent for the catalogue price and "
    "standard labour hours of every item in one request, total them with estimate_total, and give a short itemised estimate."
)


def build_model() -> ChatOpenAI:
    return ChatOpenAI(**openai_compatible_model(), stream_usage=True)


async def main() -> None:
    model = build_model()

    async def handle(ctx: TaskContext) -> str:
        graph = create_react_agent(model, [*TOOLS, *mesh_agent_tools(ctx)], prompt=PROMPT)
        result = await run_graph(ctx, graph, config={"recursion_limit": 16})
        return result["messages"][-1].text

    config = AgentConfig(
        name="repair-estimator",
        display_name="Repair Estimator",
        description="Estimates what a claim's repair costs, by part and labour (built with LangGraph)",
        framework="LangGraph",
        peers=["parts-pricing"],
        skills=[Skill(id="estimate", name="Estimate a repair", description="Itemised repair estimate for a claim id")],
        tools=tool_infos(TOOLS),
        **mesh_settings(),
    )
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
