# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims triage with CrewAI, joined to Solace Agent Mesh through the agent link.

The agent is a one-agent crew with ``@tool`` tools, its model CrewAI's own ``LLM`` (CrewAI's client for the model
name's provider; install the matching extra, for Anthropic ``uv add "crewai[anthropic]"``). A crew is stateful and runs
its tools on a worker thread, so each task gets a fresh crew, built by ``build(ctx)`` with the tools that act for that
task. CrewAI keeps no conversation between kickoffs, so ``run_agent(..., memory=...)`` puts the earlier turns in the
first task's description. CrewAI's own telemetry and trace export are off.

    uv run --extra crewai python examples/claims_triage_crewai.py

Environment and scenario: examples/claims_common.py.
"""

from __future__ import annotations

import asyncio
import logging
import os

# CrewAI sends anonymous telemetry and execution traces to its own service by default; keep task content in.
for _var, _value in (
    ("CREWAI_DISABLE_TELEMETRY", "true"),
    ("CREWAI_TRACING_ENABLED", "false"),
    ("OTEL_SDK_DISABLED", "true"),
):
    os.environ.setdefault(_var, _value)

from claims_common import INSTRUCTIONS, Settings, agent_config, fraud_screen_blocking, lookup_policy, route_claim  # noqa: E402
from crewai import LLM, Agent, Crew, Task  # noqa: E402
from crewai.llms.base_llm import BaseLLM  # noqa: E402
from crewai.tools import tool  # noqa: E402

from sam_link_sdk import ConversationMemory, MeshAgent, TaskContext  # noqa: E402
from sam_link_sdk.crewai import ask_user_tool, mesh_agent_tools, run_agent, tool_infos, with_approval, with_scopes  # noqa: E402

TOOLS = [
    tool("lookup_policy")(lookup_policy),
    tool("route_claim")(route_claim),
    tool("fraud_screen")(fraud_screen_blocking),
]


def scoped_tools(settings: Settings) -> list:
    """TOOLS, with route_claim requiring CLAIMS_ROUTE_SCOPE when it is set (``with_scopes``): the tool checks the task
    user's grants where it runs, whatever runs it, and the card (``tool_infos``) tells SAM the scope."""
    return with_scopes(TOOLS, settings.tool_scopes)


def build_model(settings: Settings) -> LLM:
    # CrewAI's own model clients take context_window_size from 1.15.34. Before that it reaches the provider's API as an
    # argument the API refuses, so it is passed only to a release that has it.
    extra: dict[str, int] = {}
    if settings.llm_context_window and "context_window_size" in BaseLLM.model_fields:
        extra["context_window_size"] = settings.llm_context_window
    return LLM(
        model=settings.llm_model,
        api_key=settings.llm_api_key,
        base_url=settings.llm_base_url or None,
        **extra,
    )


def blocked_word_guardrail(word: str):
    """A task guardrail that rejects an answer containing `word`; after its retries the crew fails."""

    def guardrail(output):
        if word.lower() in str(output.raw).lower():
            return False, f"the answer must not contain {word}"
        return True, output

    return guardrail


def build_crew(model, tools: list, reply: bool = False, blocked_word: str = "") -> Crew:
    """With `reply`, a second agent writes the reply to the claimant from the triage result, and the crew's answer is
    its reply. With `blocked_word`, the last task has a guardrail that rejects an answer containing it. The task
    template names no ``{history}`` or ``{request_context}``: ``run_agent`` puts the conversation and the gateway's
    request context at the head of the first task itself."""
    triager = Agent(
        role="Claims triager",
        goal="Route every insurance claim to the right work queue",
        backstory=INSTRUCTIONS,
        llm=model,
        tools=tools,
        verbose=False,
    )
    guard = {"guardrail": blocked_word_guardrail(blocked_word), "guardrail_max_retries": 1} if blocked_word else {}
    triage = Task(
        description="Handle this request: {request}",
        expected_output="One or two sentences naming the queue and the ticket.",
        agent=triager,
        **({} if reply else guard),
    )
    if not reply:
        return Crew(agents=[triager], tasks=[triage], verbose=False)
    correspondent = Agent(
        role="Claims correspondent",
        goal="Tell the claimant where their claim went",
        backstory="You write short, plain replies to claimants.",
        llm=model,
        verbose=False,
    )
    answer = Task(
        description="Write the reply to the claimant from the triage result.",
        expected_output="One or two sentences for the claimant.",
        agent=correspondent,
        context=[triage],
        **guard,
    )
    return Crew(agents=[triager, correspondent], tasks=[triage, answer], verbose=False)


def make_handler(model, settings: Settings | None = None):
    settings = settings or Settings()
    own_tools = scoped_tools(settings)
    memory = ConversationMemory()
    context = {"include_request_context": True} if settings.request_context else {}
    limits = {"max_model_calls": settings.max_turns} if settings.max_turns else {}

    def build(ctx: TaskContext) -> Crew:
        # Built per task: peers follow the mesh and the agents the user may invoke, and prompts belong to this task.
        # With CLAIMS_ROUTE_SCOPE set, run_agent also refuses route_claim to a user without that scope in CrewAI's
        # tool hook (and, with native tool calling, keeps it away from the model).
        tools = [*own_tools, *(mesh_agent_tools(ctx) if settings.use_peers else [])]
        if settings.approval_tools:
            tools = with_approval(ctx, tools, names=settings.approval_tools)
        if settings.ask_user:
            tools.append(ask_user_tool(ctx))
        return build_crew(model, tools, settings.crew_reply, settings.blocked_output_word)

    async def handle(ctx: TaskContext) -> str:
        await ctx.progress("Triaging claim")
        streaming = {} if settings.stream_text else {"stream_text": False}
        return await run_agent(ctx, build, memory=memory, **limits, **context, **streaming)

    return handle


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    settings = Settings.from_env()
    config = agent_config(settings, tool_infos(scoped_tools(settings)), framework="CrewAI")
    agent = MeshAgent(
        config,
        make_handler(build_model(settings), settings),
    )
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
