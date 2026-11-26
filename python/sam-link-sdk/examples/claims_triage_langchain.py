# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims triage with LangChain, joined to Solace Agent Mesh through the agent link.

The agent is ``sam_link_sdk.langchain.run_agent``: a minimal tool-calling loop over a LangChain chat model
(``ChatOpenAI`` against any OpenAI-compatible endpoint), reporting every model and tool step to the mesh.

    uv run --extra openai python examples/claims_triage_langchain.py

Environment and scenario: examples/claims_common.py.
"""

from __future__ import annotations

import asyncio
import logging

from claims_common import INSTRUCTIONS, Settings, agent_config, fraud_screen, lookup_policy, route_claim
from langchain_core.language_models import BaseChatModel
from langchain_core.tools import tool
from langchain_openai import ChatOpenAI

from sam_link_sdk import ConversationMemory, MeshAgent, TaskContext
from sam_link_sdk.langchain import ask_user_tool, mesh_agent_tools, run_agent, tool_infos, with_approval, with_scopes

TOOLS = [tool(lookup_policy), tool(route_claim), tool(fraud_screen)]


def scoped_tools(settings: Settings) -> list:
    """TOOLS, with route_claim requiring CLAIMS_ROUTE_SCOPE when it is set (``with_scopes``): the tool checks the task
    user's grants where it runs, whatever runs it, and the card (``tool_infos``) tells SAM the scope."""
    return with_scopes(TOOLS, settings.tool_scopes)


def build_model(settings: Settings) -> ChatOpenAI:
    return ChatOpenAI(
        base_url=settings.llm_base_url, api_key=settings.llm_api_key, model=settings.llm_model, stream_usage=True
    )


def make_handler(model: BaseChatModel, settings: Settings | None = None):
    settings = settings or Settings()
    own_tools = scoped_tools(settings)
    memory = ConversationMemory()
    context = {"include_request_context": True} if settings.request_context else {}

    async def handle(ctx: TaskContext) -> str:
        await ctx.progress("Triaging claim")
        tools = [*own_tools, *(mesh_agent_tools(ctx) if settings.use_peers else [])]
        if settings.approval_tools:
            tools = with_approval(ctx, tools, names=settings.approval_tools)
        if settings.ask_user:
            tools.append(ask_user_tool(ctx))
        limits = {"max_model_calls": settings.max_turns} if settings.max_turns else {}
        streaming = {} if settings.stream_text else {"stream_text": False}
        return await run_agent(
            ctx, model, tools, system_prompt=INSTRUCTIONS, memory=memory, **limits, **context, **streaming
        )

    return handle


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    settings = Settings.from_env()
    config = agent_config(settings, tool_infos(scoped_tools(settings)), framework="LangChain")
    agent = MeshAgent(
        config,
        make_handler(build_model(settings), settings),
    )
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
