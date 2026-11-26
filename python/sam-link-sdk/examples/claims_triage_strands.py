# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims triage with Strands Agents, joined to Solace Agent Mesh through the agent link.

The agent is a ``strands.Agent`` with ``@tool`` tools, its model Strands' ``OpenAIModel`` against any OpenAI-compatible
endpoint. A Strands agent keeps its conversation and rejects concurrent runs, so ``run_agent`` takes a function that
builds the agent for each task, with the tools and approval hooks that act for that task.

    uv run --extra strands python examples/claims_triage_strands.py

Environment and scenario: examples/claims_common.py.
"""

from __future__ import annotations

import asyncio
import logging

from claims_common import INSTRUCTIONS, Settings, agent_config, fraud_screen, lookup_policy, route_claim
from strands import Agent, InterventionHandler, tool
from strands.interventions import Confirm, Proceed
from strands.models import Model
from strands.models.openai import OpenAIModel

from sam_link_sdk import ConversationMemory, MeshAgent, TaskContext
from sam_link_sdk.strands import ask_user_tool, mesh_agent_tools, run_agent, tool_infos, with_approval, with_scopes

TOOLS = [tool(lookup_policy), tool(route_claim), tool(fraud_screen)]


def scoped_tools(settings: Settings) -> list:
    """TOOLS, with route_claim requiring CLAIMS_ROUTE_SCOPE when it is set (``with_scopes``): the tool checks the task
    user's grants where it runs, whatever runs it, and the card (``tool_infos``) tells SAM the scope."""
    return with_scopes(TOOLS, settings.tool_scopes)


class ConfirmRoute(InterventionHandler):
    """Strands' own approval: the interrupt it raises ends the run, and ``run_agent`` asks the user through Agent Mesh,
    its name is the tool the user is asked about and the prompt is the message they see."""

    name = "route_claim"

    def before_tool_call(self, event, **kwargs):
        if event.tool_use["name"] != "route_claim":
            return Proceed()
        return Confirm(prompt=f"Route this claim: {event.tool_use.get('input')}")


def build_model(settings: Settings) -> OpenAIModel:
    return OpenAIModel(
        client_args={"base_url": settings.llm_base_url, "api_key": settings.llm_api_key}, model_id=settings.llm_model
    )


def make_handler(model: Model, settings: Settings | None = None):
    settings = settings or Settings()
    own_tools = scoped_tools(settings)
    memory = ConversationMemory()
    context = {"include_request_context": True} if settings.request_context else {}

    def build(ctx: TaskContext) -> Agent:
        # Built per task: peers follow the mesh, and prompts belong to this task. With CLAIMS_ROUTE_SCOPE set,
        # ctx.permitted_tools leaves route_claim out for a user without that scope (Strands cannot take a tool off a
        # built agent; the tool refuses such a user regardless). The approvals name the agent's tools, so they are
        # built over all of them, before any is left out.
        tools = [
            *own_tools,
            *(mesh_agent_tools(ctx) if settings.use_peers else []),
            *([ask_user_tool(ctx)] if settings.ask_user else []),
        ]
        hooks = [with_approval(ctx, tools, names=settings.approval_tools)] if settings.approval_tools else []
        tools = ctx.permitted_tools(tools)
        interventions = [ConfirmRoute()] if settings.native_approval else []
        return Agent(
            model=model,
            system_prompt=INSTRUCTIONS,
            tools=tools,
            hooks=hooks,
            interventions=interventions,
            callback_handler=None,
        )

    async def handle(ctx: TaskContext) -> str:
        await ctx.progress("Triaging claim")
        streaming = {} if settings.stream_text else {"stream_text": False}
        return await run_agent(ctx, build, memory=memory, **context, **streaming)

    return handle


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    settings = Settings.from_env()
    config = agent_config(settings, tool_infos(scoped_tools(settings)), framework="Strands Agents")
    agent = MeshAgent(
        config,
        make_handler(build_model(settings), settings),
    )
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
