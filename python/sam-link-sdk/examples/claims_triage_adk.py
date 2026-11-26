# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims triage with Google's Agent Development Kit (ADK), joined to Solace Agent Mesh through the agent link.

The agent is an ADK ``LlmAgent`` with plain-function tools, its model ADK's ``LiteLlm`` wrapper. ADK builds the agent
and its Runner once, not per task, so the tools that act for a task (peers, approvals, questions) are toolsets that
ask, on every run, which mesh task ``run_agent`` is serving. Each SAM chat is an ADK session, so follow-up questions
keep the conversation, and each SAM user is an ADK user, so the policy a user last looked up (ADK ``user:`` state) is
in the instructions of that user's later chats, and never in another user's.

    uv run --extra adk python examples/claims_triage_adk.py

Environment and scenario: examples/claims_common.py.
"""

from __future__ import annotations

import asyncio
import logging

from claims_common import INSTRUCTIONS, Settings, agent_config, fraud_screen, lookup_policy, route_claim
from google.adk.agents import LlmAgent
from google.adk.models import BaseLlm
from google.adk.models.lite_llm import LiteLlm
from google.adk.runners import InMemoryRunner
from google.adk.tools import FunctionTool

from sam_link_sdk import MeshAgent, TaskContext
from sam_link_sdk.adk import (
    ask_user_tool,
    mesh_agent_tools,
    run_agent,
    step_reporter,
    tool_infos,
    with_approval,
    with_scopes,
)

TOOLS = [lookup_policy, route_claim, fraud_screen]


def scoped_tools(settings: Settings) -> list:
    """TOOLS, with route_claim requiring CLAIMS_ROUTE_SCOPE when it is set (``with_scopes``): the tool checks the task
    user's grants where it runs, whatever runs it, and the card (``tool_infos``) tells SAM the scope. With
    CLAIMS_NATIVE_APPROVAL, route_claim needs approval by the framework's own flag (``require_confirmation``):
    ``run_agent`` asks the user."""
    tools = [
        FunctionTool(route_claim, require_confirmation=True) if settings.native_approval and t is route_claim else t
        for t in TOOLS
    ]
    return with_scopes(tools, settings.tool_scopes)


# ADK fills {user:last_policy?} in the instruction from the session's state, empty until the user has looked one up.
USER_INSTRUCTIONS = INSTRUCTIONS + "\nThe policy this user last looked up, if any: {user:last_policy?}"


def remember_policy(tool, args: dict, tool_context, tool_response) -> None:
    """Keep the policy the user looked up in ADK's ``user:`` state, which the user's every chat shares."""
    if tool.name == "lookup_policy" and isinstance(tool_response, dict) and "error" not in tool_response:
        tool_context.state["user:last_policy"] = str(args.get("policy_id", ""))


def build_model(settings: Settings) -> LiteLlm:
    return LiteLlm(model=settings.llm_model, api_base=settings.llm_base_url or None, api_key=settings.llm_api_key)


def make_handler(model: BaseLlm, settings: Settings | None = None):
    settings = settings or Settings()
    own_tools = scoped_tools(settings)
    tools = with_approval(own_tools, names=settings.approval_tools) if settings.approval_tools else own_tools
    if settings.use_peers:
        tools = [*tools, *mesh_agent_tools()]
    if settings.ask_user:
        tools = [*tools, ask_user_tool()]
    agent = LlmAgent(
        name="claims_triage",
        model=model,
        instruction=USER_INSTRUCTIONS,
        tools=tools,
        after_tool_callback=remember_policy,
    )
    runner = InMemoryRunner(agent=agent, app_name="claims_triage", plugins=[step_reporter()])
    limits = {"max_model_calls": settings.max_turns} if settings.max_turns else {}
    context = {"include_request_context": True} if settings.request_context else {}

    async def handle(ctx: TaskContext) -> str:
        await ctx.progress("Triaging claim")
        streaming = {} if settings.stream_text else {"stream_text": False}
        return await run_agent(ctx, runner, **limits, **context, **streaming)

    return handle


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    settings = Settings.from_env()
    config = agent_config(settings, tool_infos(scoped_tools(settings)), framework="Google ADK")
    agent = MeshAgent(
        config,
        make_handler(build_model(settings), settings),
    )
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
