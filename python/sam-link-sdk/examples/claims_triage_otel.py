# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims triage with no sam_link_sdk adapter, joined to Solace Agent Mesh through the agent link and reported from the
framework's own OpenTelemetry spans.

The agent is the Strands one from claims_triage_strands.py, conversation memory included, but the mesh learns about
every model and tool step only from the GenAI spans Strands emits anyway (``chat``, ``execute_tool``), read by
``sam_link_sdk.otel_steps``. This is how a framework without an adapter joins the mesh with step-level visibility. Spans
record what happened, so this gives up what needs a hook into the run: approval before a tool, refusing a call to a
tool the user lacks the scopes for, asking the user, peer tools (CLAIMS_APPROVAL_TOOLS, CLAIMS_ROUTE_SCOPE,
CLAIMS_ASK_USER and CLAIMS_PEER_AGENTS are not used here: no scope is declared that spans could not enforce;
docs/opentelemetry.md).

    uv run --extra strands --extra otel python examples/claims_triage_otel.py

Environment and scenario: examples/claims_common.py. Strands records model output and tool arguments and results on
its spans by default, and the mesh shows them (redacted for credentials, as with the adapters). With
``OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental,gen_ai_unredacted_attributes=`` Strands writes its own
``[REDACTED]`` placeholder instead, and that is what the mesh shows; names, ids and token counts are unaffected.
"""

from __future__ import annotations

import asyncio
import logging

from claims_common import INSTRUCTIONS, Settings, agent_config, fraud_screen, lookup_policy, route_claim
from strands import Agent, tool
from strands.models import Model
from strands.models.openai import OpenAIModel

from sam_link_sdk import ConversationMemory, MeshAgent, TaskContext
from sam_link_sdk.config import ToolInfo
from sam_link_sdk.otel_steps import run_agent, step_reporter

TOOLS = [tool(lookup_policy), tool(route_claim), tool(fraud_screen)]


def build_model(settings: Settings) -> OpenAIModel:
    return OpenAIModel(
        client_args={"base_url": settings.llm_base_url, "api_key": settings.llm_api_key}, model_id=settings.llm_model
    )


def make_handler(model: Model, settings: Settings | None = None):
    memory = ConversationMemory()

    async def handle(ctx: TaskContext) -> str:
        await ctx.progress("Triaging claim")
        # A Strands agent keeps its conversation and rejects concurrent runs, so each task gets its own, starting from
        # this chat's earlier turns (none for a peer call or a workflow node: their session_id is empty).
        prior = memory.history(ctx.session_id)
        agent = Agent(
            model=model, system_prompt=INSTRUCTIONS, tools=list(TOOLS), messages=list(prior), callback_handler=None
        )
        answer = str(await run_agent(ctx, agent.invoke_async)).strip()
        memory.remember(ctx.session_id, agent.messages[len(prior) :], ctx.text, answer)
        return answer

    return handle


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    step_reporter()
    settings = Settings.from_env()
    config = agent_config(
        settings,
        [ToolInfo(t.tool_name, t.tool_spec.get("description", "")) for t in TOOLS],
        framework="Strands Agents (OpenTelemetry)",
    )
    agent = MeshAgent(
        config,
        make_handler(build_model(settings), settings),
    )
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
