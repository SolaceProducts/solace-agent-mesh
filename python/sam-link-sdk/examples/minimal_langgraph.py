# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

import asyncio
import logging

from langchain_core.tools import tool
from langchain_openai import ChatOpenAI
from langgraph.checkpoint.memory import MemorySaver
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import ToolNode, tools_condition

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.langchain import run_graph


@tool
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> ChatOpenAI:
    return ChatOpenAI(model="gpt-4o-mini", stream_usage=True)


def build_graph(model: ChatOpenAI):
    bound = model.bind_tools([lookup_policy])

    async def agent(state: MessagesState, config):
        return {"messages": [await bound.ainvoke(state["messages"], config)]}

    graph = StateGraph(MessagesState)
    graph.add_node("agent", agent)
    graph.add_node("tools", ToolNode([lookup_policy]))
    graph.add_edge(START, "agent")
    graph.add_conditional_edges("agent", tools_condition)
    graph.add_edge("tools", "agent")
    return graph.compile(checkpointer=MemorySaver())


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    graph = build_graph(build_model())

    async def handle(ctx: TaskContext) -> str:
        result = await run_graph(ctx, graph)
        return result["messages"][-1].text

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
