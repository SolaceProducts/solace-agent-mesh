# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Parts Pricing: an OpenAI Agents SDK agent that knows the repair catalogue.

Started by stack/start_agents.sh (README.md); on its own:
`uv run --extra openai-agents python parts_pricing_openai.py`.
"""

from __future__ import annotations

import asyncio
import os

from agents import Agent, OpenAIChatCompletionsModel, function_tool, set_tracing_disabled
from common import CATALOGUE, mesh_settings, openai_compatible_model
from openai import AsyncOpenAI

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.openai_agents import run_agent, tool_infos

# Stands in for a slow supplier feed, so a claim spends a while waiting here and the Agents page shows it working.
SUPPLIER_LOOKUP_SECONDS = float(os.environ.get("PARTS_SUPPLIER_SECONDS", "10"))


def find_item(item: str) -> tuple[float, float] | None:
    """The catalogue entry for an item, tolerating the model's wording ("lower kitchen cabinets" for "lower cabinets")."""
    wanted = item.lower().strip()
    if wanted in CATALOGUE:
        return CATALOGUE[wanted]
    words = set(wanted.split())
    for name, entry in CATALOGUE.items():
        if name in wanted or wanted in name or set(name.split()) <= words:
            return entry
    return None


@function_tool
async def catalogue_price(item: str) -> dict:
    """The catalogue price in USD of a part or repair item, such as 'rear bumper' or 'kitchen flooring'."""
    await asyncio.sleep(SUPPLIER_LOOKUP_SECONDS)
    entry = find_item(item)
    return {"item": item, "price_usd": entry[0]} if entry else {"item": item, "error": "not in the catalogue"}


@function_tool
def labour_hours(item: str) -> dict:
    """The standard labour hours to replace a part or repair item."""
    entry = find_item(item)
    return {"item": item, "hours": entry[1]} if entry else {"item": item, "error": "not in the catalogue"}


TOOLS = [catalogue_price, labour_hours]
INSTRUCTIONS = (
    "You are the parts desk of a repair network. For every item you are asked about, give its catalogue price and its "
    "standard labour hours, using the tools, as a short list, and add the total labour hours."
)


def build_model() -> OpenAIChatCompletionsModel:
    llm = openai_compatible_model()
    return OpenAIChatCompletionsModel(
        model=llm["model"], openai_client=AsyncOpenAI(base_url=llm["base_url"], api_key=llm["api_key"])
    )


async def main() -> None:
    set_tracing_disabled(True)
    agent = Agent(name="parts_pricing", instructions=INSTRUCTIONS, model=build_model(), tools=TOOLS)

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, agent)

    config = AgentConfig(
        name="parts-pricing",
        display_name="Parts Pricing",
        description="Catalogue prices and standard labour hours for repair items (built with the OpenAI Agents SDK)",
        framework="OpenAI Agents SDK",
        skills=[
            Skill(id="pricing", name="Price repair items", description="Catalogue price and labour hours per item")
        ],
        tools=tool_infos(TOOLS),
        **mesh_settings(),
    )
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
