# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims triage with the OpenAI Agents SDK, joined to Solace Agent Mesh through the agent link.

The agent is an ``agents.Agent`` with ``function_tool`` tools, its model ``OpenAIChatCompletionsModel`` against any
OpenAI-compatible endpoint, so no OpenAI key is needed. OpenAI trace export is off: it would upload every model input
and tool result of the task to the OpenAI dashboard.

    uv run --extra openai-agents python examples/claims_triage_openai_agents.py

Environment and scenario: examples/claims_common.py.
"""

from __future__ import annotations

import asyncio
import logging

from agents import (
    Agent,
    GuardrailFunctionOutput,
    Model,
    OpenAIChatCompletionsModel,
    function_tool,
    input_guardrail,
    output_guardrail,
    set_tracing_disabled,
)
from claims_common import INSTRUCTIONS, Settings, agent_config, fraud_screen, lookup_policy, route_claim
from openai import AsyncOpenAI

from sam_link_sdk import ConversationMemory, MeshAgent, TaskContext, ToolInfo
from sam_link_sdk.openai_agents import (
    ask_user_tool,
    mesh_agent_tools,
    run_agent,
    tool_infos,
    with_approval,
    with_scopes,
)

TOOLS = [function_tool(lookup_policy), function_tool(route_claim), function_tool(fraud_screen)]


def scoped_tools(settings: Settings) -> list:
    """TOOLS, with route_claim requiring CLAIMS_ROUTE_SCOPE when it is set (``with_scopes``): the tool checks the task
    user's grants where it runs, whatever runs it, and the card (``tool_infos``) tells SAM the scope. With
    CLAIMS_NATIVE_APPROVAL, route_claim needs approval by the SDK's own flag: ``run_agent`` asks the user."""
    tools = [
        function_tool(route_claim, needs_approval=True) if settings.native_approval and t.name == "route_claim" else t
        for t in TOOLS
    ]
    return with_scopes(tools, settings.tool_scopes)


def build_model(settings: Settings) -> OpenAIChatCompletionsModel:
    client = AsyncOpenAI(base_url=settings.llm_base_url, api_key=settings.llm_api_key)
    return OpenAIChatCompletionsModel(model=settings.llm_model, openai_client=client)


def blocked_word_guardrail(word: str):
    """An input guardrail that blocks a request containing `word`. It runs before the model is called
    (run_in_parallel=False), so a blocked request costs no model call."""

    @input_guardrail(name="blocked_word", run_in_parallel=False)
    async def blocked_word(context, agent, user_input) -> GuardrailFunctionOutput:
        text = user_input if isinstance(user_input, str) else str(user_input)
        return GuardrailFunctionOutput(output_info=None, tripwire_triggered=word.lower() in text.lower())

    return blocked_word


def blocked_output_word_guardrail(word: str):
    """An output guardrail that blocks an answer containing `word`."""

    @output_guardrail(name="blocked_output_word")
    async def blocked_output_word(context, agent, output) -> GuardrailFunctionOutput:
        return GuardrailFunctionOutput(output_info=None, tripwire_triggered=word.lower() in str(output).lower())

    return blocked_output_word


def make_handler(model: Model, settings: Settings | None = None):
    settings = settings or Settings()
    own_tools = scoped_tools(settings)
    guardrails = [blocked_word_guardrail(settings.blocked_word)] if settings.blocked_word else []
    output_guardrails = (
        [blocked_output_word_guardrail(settings.blocked_output_word)] if settings.blocked_output_word else []
    )
    triage = Agent(
        name="claims-triage",
        instructions=INSTRUCTIONS,
        model=model,
        tools=own_tools,
        input_guardrails=guardrails,
        output_guardrails=output_guardrails,
    )
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
        agent = triage.clone(tools=tools)
        if settings.agent_tool:
            agent = intake_agent(model, agent)
        return await run_agent(ctx, agent, memory=memory, **limits, **context, **streaming)

    return handle


def intake_agent(model: Model, triage: Agent) -> Agent:
    """An agent that hands each claim to the triage agent run as its tool (``Agent.as_tool``) and answers the user.
    ``run_agent`` runs the triage agent on the same task: its model calls and tool calls are reported and checked as
    the intake agent's are."""
    return Agent(
        name="claims-intake",
        instructions="Hand each claim to triage_claim and tell the user what it decided.",
        model=model,
        tools=[triage.as_tool("triage_claim", "Triage an insurance claim: looks up the policy and routes the claim.")],
    )


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    set_tracing_disabled(True)
    settings = Settings.from_env()
    tools = tool_infos(scoped_tools(settings))
    if settings.agent_tool:
        tools.append(ToolInfo("triage_claim", "Triage an insurance claim: looks up the policy and routes the claim."))
    config = agent_config(settings, tools, framework="OpenAI Agents SDK")
    agent = MeshAgent(
        config,
        make_handler(build_model(settings), settings),
    )
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
