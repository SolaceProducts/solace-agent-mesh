# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims triage with LangGraph, joined to Solace Agent Mesh through the agent link.

The agent is an explicit LangGraph ``StateGraph`` (model node, then a ``ToolNode``, back to the model until it
answers). ``run_graph`` puts the step reporter in the graph's run config; LangGraph hands it to every model and tool
call inside the graph. A checkpointer keeps each conversation by thread id (the task's session).

    uv run --extra langgraph --extra openai python examples/claims_triage_langgraph.py

Environment and scenario: examples/claims_common.py.
"""

from __future__ import annotations

import asyncio
import functools
import logging
from collections.abc import Sequence

from claims_common import INSTRUCTIONS, Settings, agent_config, fraud_screen, lookup_policy, route_claim
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import SystemMessage
from langchain_core.runnables import RunnableConfig
from langchain_core.tools import BaseTool, StructuredTool, ToolException, tool
from langchain_openai import ChatOpenAI
from langgraph.checkpoint.memory import MemorySaver
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import ToolNode, tools_condition
from langgraph.types import interrupt

from sam_link_sdk import MeshAgent, TaskContext
from sam_link_sdk.langchain import (
    ask_user_tool,
    mesh_agent_tools,
    run_graph,
    tool_infos,
    with_approval,
    with_scopes,
)

TOOLS = [tool(lookup_policy), tool(route_claim), tool(fraud_screen)]


async def route_claim_after_approval(policy_id: str, queue: str) -> dict:
    """Route a claim to a work queue: fast-track, standard, or manual-review."""
    # LangGraph's own approval: the graph pauses here, run_graph asks the user through Agent Mesh and resumes with
    # True, or with the text to give the model when the user did not approve.
    decision = interrupt(
        {"tool": "route_claim", "args": {"policy_id": policy_id, "queue": queue}, "message": "Route this claim"}
    )
    if decision is not True:
        return {"error": decision}
    return route_claim(policy_id, queue)


def _raising_tool_exception(fn):
    @functools.wraps(fn)
    def run(*args, **kwargs):
        try:
            return fn(*args, **kwargs)
        except ValueError as error:
            raise ToolException(str(error)) from error

    return run


# With CLAIMS_HANDLE_TOOL_ERRORS the tools raise ToolException and are built with handle_tool_error: LangChain then
# hands the failure to the model as an error ToolMessage, where a tool that raises otherwise ends the graph's run.
TOOLS_HANDLING_ERRORS = [
    *(
        StructuredTool.from_function(_raising_tool_exception(fn), handle_tool_error=True)
        for fn in (lookup_policy, route_claim)
    ),
    StructuredTool.from_function(coroutine=fraud_screen, handle_tool_error=True),
]


def scoped_tools(settings: Settings) -> list:
    """TOOLS, with route_claim requiring CLAIMS_ROUTE_SCOPE when it is set (``with_scopes``): the tool checks the task
    user's grants where it runs, whatever runs it, and the card (``tool_infos``) tells SAM the scope. With
    CLAIMS_NATIVE_APPROVAL, route_claim interrupts the graph for approval, LangGraph's own way."""
    tools = TOOLS_HANDLING_ERRORS if settings.handle_tool_errors else TOOLS
    tools = [
        tool("route_claim")(route_claim_after_approval) if settings.native_approval and t.name == "route_claim" else t
        for t in tools
    ]
    return with_scopes(tools, settings.tool_scopes)


def build_model(settings: Settings) -> ChatOpenAI:
    return ChatOpenAI(
        base_url=settings.llm_base_url, api_key=settings.llm_api_key, model=settings.llm_model, stream_usage=True
    )


def build_graph(model: BaseChatModel, tools: Sequence[BaseTool], checkpointer=None):
    bound = model.bind_tools(list(tools))

    async def agent(state: MessagesState, config: RunnableConfig):
        return {"messages": [await bound.ainvoke([SystemMessage(INSTRUCTIONS), *state["messages"]], config)]}

    graph = StateGraph(MessagesState)
    graph.add_node("agent", agent)
    graph.add_node("tools", ToolNode(list(tools)))
    graph.add_edge(START, "agent")
    graph.add_conditional_edges("agent", tools_condition)
    graph.add_edge("tools", "agent")
    # The checkpointer keeps each conversation's messages by thread id, so follow-up questions in the same chat see
    # the earlier turns. It lives in this process; use a shared one (Postgres, Redis) for replicas or restarts.
    return graph.compile(checkpointer=checkpointer or MemorySaver())


def make_handler(model: BaseChatModel, settings: Settings | None = None, checkpointer=None):
    settings = settings or Settings()
    saver = checkpointer or MemorySaver()
    own_tools = scoped_tools(settings)

    async def handle(ctx: TaskContext) -> str:
        await ctx.progress("Triaging claim")
        # Built per task: peers follow the mesh, and prompts belong to this task. With CLAIMS_ROUTE_SCOPE set,
        # ctx.permitted_tools leaves route_claim out for a user without that scope (the tool refuses such a user
        # regardless); the approvals are built over every tool first, as they name the agent's tools.
        tools = [*own_tools, *(mesh_agent_tools(ctx) if settings.use_peers else [])]
        if settings.approval_tools:
            tools = with_approval(ctx, tools, names=settings.approval_tools)
        tools = ctx.permitted_tools(tools)
        if settings.ask_user:
            tools.append(ask_user_tool(ctx))
        graph = build_graph(model, tools, saver)
        # The thread is the chat's session; a peer call or workflow node gets a thread of its own, dropped afterwards.
        result = await run_graph(
            ctx, graph, config={"recursion_limit": settings.max_turns or 16}, stream_text=settings.stream_text
        )
        return result["messages"][-1].text

    return handle


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    settings = Settings.from_env()
    config = agent_config(settings, tool_infos(scoped_tools(settings)), framework="LangGraph")
    agent = MeshAgent(
        config,
        make_handler(build_model(settings), settings),
    )
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
