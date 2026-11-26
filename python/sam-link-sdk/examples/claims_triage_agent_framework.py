# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims triage with Microsoft Agent Framework, joined to Solace Agent Mesh through the agent link.

The agent is an ``agent_framework.Agent`` with ``@tool`` tools, its model ``OpenAIChatCompletionClient`` against any
OpenAI-compatible endpoint. One agent serves every task: it keeps no conversation of its own, and ``run_agent`` gives
each run the tools and middleware that act for that task.

    uv run --extra agent-framework python examples/claims_triage_agent_framework.py

Environment and scenario: examples/claims_common.py.
"""

from __future__ import annotations

import asyncio
import logging

from agent_framework import Agent, tool
from agent_framework.openai import OpenAIChatCompletionClient
from claims_common import INSTRUCTIONS, Settings, agent_config, fraud_screen, lookup_policy, route_claim

from sam_link_sdk import ConversationMemory, MeshAgent, TaskContext
from sam_link_sdk.agent_framework import (
    ask_user_tool,
    mesh_agent_tools,
    run_agent,
    tool_infos,
    with_approval,
    with_scopes,
)

# SAM asks for approval inside the run (with_approval); CLAIMS_NATIVE_APPROVAL has route_claim use the framework's own
# flag instead (approval_mode="always_require"), and run_agent asks the user for the request it hands back.
TOOLS = [tool(approval_mode="never_require")(f) for f in (lookup_policy, route_claim, fraud_screen)]


def scoped_tools(settings: Settings) -> list:
    """TOOLS, with route_claim requiring CLAIMS_ROUTE_SCOPE when it is set (``with_scopes``): the tool checks the task
    user's grants where it runs, whatever runs it, and the card (``tool_infos``) tells SAM the scope. With
    CLAIMS_NATIVE_APPROVAL, route_claim needs approval by the framework's own flag."""
    tools = [
        tool(approval_mode="always_require")(route_claim) if settings.native_approval and t.name == "route_claim" else t
        for t in TOOLS
    ]
    return with_scopes(tools, settings.tool_scopes)


def build_model(settings: Settings) -> OpenAIChatCompletionClient:
    limits = {"function_invocation_configuration": {"max_iterations": settings.max_turns}} if settings.max_turns else {}
    return OpenAIChatCompletionClient(
        model=settings.llm_model, api_key=settings.llm_api_key, base_url=settings.llm_base_url, **limits
    )


def make_handler(model, settings: Settings | None = None):
    settings = settings or Settings()
    own_tools = scoped_tools(settings)
    triage = Agent(model, INSTRUCTIONS, name="claims-triage", tools=own_tools)
    memory = ConversationMemory()
    context = {"include_request_context": True} if settings.request_context else {}

    async def handle(ctx: TaskContext) -> str:
        await ctx.progress("Triaging claim")
        extra = [
            *(mesh_agent_tools(ctx) if settings.use_peers else []),
            *([ask_user_tool(ctx)] if settings.ask_user else []),
        ]
        approvals = (
            [with_approval(ctx, [*own_tools, *extra], names=settings.approval_tools)] if settings.approval_tools else []
        )
        streaming = {} if settings.stream_text else {"stream_text": False}
        return await run_agent(
            ctx, triage, memory=memory, extra_tools=extra, middleware=approvals, **context, **streaming
        )

    return handle


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    settings = Settings.from_env()
    config = agent_config(settings, tool_infos(scoped_tools(settings)), framework="Microsoft Agent Framework")
    agent = MeshAgent(
        config,
        make_handler(build_model(settings), settings),
    )
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
