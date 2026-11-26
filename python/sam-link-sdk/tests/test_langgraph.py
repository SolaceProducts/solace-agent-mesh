# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The LangGraph example, driven end to end with a fake tool-calling model in a task of a real agent."""

from __future__ import annotations

import asyncio

import pytest

pytest.importorskip("langgraph")

from langchain_core.language_models.fake_chat_models import GenericFakeChatModel
from langchain_core.messages import AIMessage

from sam_link_sdk.events import ModelFinished, ToolFinished, ToolStarted

from .adapter_task import adapter_task, steps
from .agent_rig import RecordingTransport, start_agent
from .conftest import load_example as load_example_module


def load_example():
    return load_example_module("claims_triage_langgraph")


class ToolCallingFake(GenericFakeChatModel):
    disable_streaming: bool = True
    """The fake's stream drops an AIMessage's tool_calls, so it answers whole; run_graph streams the text it sends."""

    def bind_tools(self, tools, **kwargs):
        return self


def usage(i, o):
    return {"input_tokens": i, "output_tokens": o, "total_tokens": i + o}


class ContendedTransport(RecordingTransport):
    """Holds a lock of the agent's loop around every step, long enough for the steps the threads report to contend,
    and notes every step sent from another loop than the agent's."""

    def __init__(self):
        super().__init__()
        self._lock = asyncio.Lock()
        self._loop = asyncio.get_running_loop()
        self.from_another_loop: list[str] = []

    async def _send_event(self, task_id, event):
        if asyncio.get_running_loop() is not self._loop:
            self.from_another_loop.append(event.kind)
        async with self._lock:
            await asyncio.sleep(0.005)
            return await super()._send_event(task_id, event)


async def answered(transport, task_id):
    while task_id not in transport.finished:
        await asyncio.sleep(0.01)
    return transport.finished[task_id]


async def test_parallel_sync_tools_in_a_prebuilt_react_agent_do_not_deadlock_the_task():
    """LangGraph runs sync tools on worker threads and LangChain calls the async callback handler there under a
    new event loop. The task's channel belongs to the agent's loop, so the handler's work must be handed to that
    loop; otherwise the transport's lock is taken from another loop and the task never answers."""
    from langchain_core.tools import tool
    from langgraph.prebuilt import create_react_agent

    from sam_link_sdk.langchain import SamLinkCallbackHandler

    @tool
    def part_price(part: str) -> dict:
        """Price of a part."""
        return {"part": part, "price_usd": 640}

    @tool
    def labour_hours(part: str) -> dict:
        """Labour hours for a part."""
        return {"part": part, "hours": 3.0}

    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content="",
                    tool_calls=[
                        {"id": "call_1", "name": "part_price", "args": {"part": "rear bumper"}},
                        {"id": "call_2", "name": "labour_hours", "args": {"part": "rear bumper"}},
                        {"id": "call_3", "name": "part_price", "args": {"part": "tail light"}},
                        {"id": "call_4", "name": "labour_hours", "args": {"part": "tail light"}},
                    ],
                    usage_metadata=usage(500, 40),
                ),
                AIMessage(content="Estimate: $1,400.", usage_metadata=usage(700, 20)),
            ]
        )
    )

    async def estimate(ctx):
        graph = create_react_agent(model, [part_price, labour_hours])
        result = await graph.ainvoke(
            {"messages": [("user", ctx.text)]}, config={"callbacks": [SamLinkCallbackHandler(ctx)]}
        )
        return result["messages"][-1].text

    agent, transport = await start_agent(estimate, ContendedTransport(), name="repair-estimator")
    try:
        await transport.deliver("T", "Estimate")
        outcome = await asyncio.wait_for(answered(transport, "T"), 10)
    finally:
        await agent.stop()

    assert (outcome.state, outcome.text) == ("completed", "Estimate: $1,400.")
    assert transport.from_another_loop == []
    assert sorted(e.call_id for e in transport.events("T", ToolStarted)) == ["call_1", "call_2", "call_3", "call_4"]
    assert sorted(e.call_id for e in transport.events("T", ToolFinished)) == ["call_1", "call_2", "call_3", "call_4"]


async def test_graph_emits_paired_steps_and_usage():
    example = load_example()
    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content="Looking it up.",
                    tool_calls=[{"id": "call_a", "name": "lookup_policy", "args": {"policy_id": "P-1182"}}],
                    usage_metadata=usage(800, 40),
                ),
                AIMessage(
                    content="",
                    tool_calls=[
                        {"id": "call_b", "name": "route_claim", "args": {"policy_id": "P-1182", "queue": "fast-track"}}
                    ],
                    usage_metadata=usage(900, 30),
                ),
                AIMessage(content="Claim P-1182 routed to fast-track.", usage_metadata=usage(950, 20)),
            ]
        )
    )
    async with adapter_task(text="Triage claim P-1182") as task:
        answer = await example.make_handler(model)(task.ctx)
        sent = steps(task)
        tu = task.ctx._run.usage.final_token_usage()

    assert answer == "Claim P-1182 routed to fast-track."
    assert [e.kind for e in sent] == [
        "progress",
        "model_started",
        "model_finished",
        "tool_started",
        "tool_finished",
        "model_started",
        "model_finished",
        "tool_started",
        "tool_finished",
        "model_started",
        "model_finished",
    ]
    asked = [c.id for e in sent if isinstance(e, ModelFinished) for c in e.tool_calls]
    started = [e.call_id for e in sent if isinstance(e, ToolStarted)]
    finished = [e.call_id for e in sent if isinstance(e, ToolFinished)]
    assert asked == started == finished == ["call_a", "call_b"]
    assert [e.name for e in sent if isinstance(e, ToolFinished)] == ["lookup_policy", "route_claim"]
    assert sent[4].result["tier"] == "gold"

    assert (tu["total_input_tokens"], tu["total_output_tokens"], tu["last_input_tokens"]) == (2650, 90, 950)


async def test_a_tool_node_can_give_the_model_the_refusal_text_as_it_is():
    """The documented way to keep LangGraph's own error template off the refusal (docs/frameworks/langchain.md)."""
    from langchain_core.messages import AIMessage, HumanMessage, ToolMessage
    from langchain_core.tools import tool
    from langgraph.prebuilt import ToolNode, create_react_agent

    from sam_link_sdk.context import TaskContext
    from sam_link_sdk.langchain import step_reporter

    from .test_callbacks import ToolCallingFake

    @tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        return "fast-track"

    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content="", tool_calls=[{"id": "call_r", "name": "route_claim", "args": {"claim_id": "C-1"}}]
                ),
                AIMessage(content="Not routed."),
            ]
        )
    )
    graph = create_react_agent(model, ToolNode([route_claim], handle_tool_errors=lambda error: str(error)))
    ctx = TaskContext._new(
        task_id="T",
        context_id="C",
        text="x",
        agent_name="claims-triage",
        tool_scopes={"route_claim": ["tool:claims:route"]},
        granted_tool_scopes=[],
    )
    result = await graph.ainvoke({"messages": [HumanMessage("route C-1")]}, config={"callbacks": [step_reporter(ctx)]})
    [told] = [m.content for m in result["messages"] if isinstance(m, ToolMessage)]
    assert told == "Access denied: you do not have the scopes required to use route_claim."


async def test_a_graph_interrupt_for_approval_is_refused_on_a_channel_that_cannot_prompt():
    """Agent Mesh shows no prompt there (the e2e harness has only the web gateway, which can), so the user's answer is
    a cancel: the call does not run and the model is told it was not approved."""
    example = load_example()
    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content="",
                    tool_calls=[
                        {"id": "call_b", "name": "route_claim", "args": {"policy_id": "P-1182", "queue": "fast-track"}}
                    ],
                    usage_metadata=usage(900, 30),
                ),
                AIMessage(content="Not routed.", usage_metadata=usage(950, 20)),
            ]
        )
    )
    async with adapter_task(text="Triage claim P-1182", can_prompt_user=False) as task:
        answer = await example.make_handler(model, example.Settings(native_approval=True))(task.ctx)
        [finished] = [e for e in steps(task) if isinstance(e, ToolFinished)]

    assert answer == "Not routed."
    assert "did not approve this call to route_claim (cancelled), so it was not run." in str(finished.result)
    assert "CLM-" not in str(finished.result)


async def test_a_graph_interrupt_that_is_not_an_approval_request_fails_the_run_saying_so():
    from langchain_core.messages import HumanMessage
    from langgraph.checkpoint.memory import MemorySaver
    from langgraph.graph import START, MessagesState, StateGraph
    from langgraph.types import interrupt

    from sam_link_sdk.langchain import run_graph

    def ask(state: MessagesState):
        interrupt("Which queue?")
        return {}

    graph = StateGraph(MessagesState)
    graph.add_node("ask", ask)
    graph.add_edge(START, "ask")
    compiled = graph.compile(checkpointer=MemorySaver())
    async with adapter_task() as task:
        with pytest.raises(ValueError, match="not an approval request"):
            await run_graph(
                task.ctx,
                compiled,
                input={"messages": [HumanMessage("hi")]},
                config={"configurable": {"thread_id": "t1"}},
            )


async def test_run_graph_keeps_a_chat_on_its_session_thread_and_drops_a_run_based_tasks_thread():
    """Which threads a graph's checkpointer keeps is inside the agent: SAM sees only the answers."""
    from langgraph.checkpoint.memory import MemorySaver
    from langgraph.graph import START, MessagesState, StateGraph

    from sam_link_sdk.langchain import run_graph

    def answer(state: MessagesState):
        return {"messages": [AIMessage("done")]}

    graph = StateGraph(MessagesState)
    graph.add_node("answer", answer)
    graph.add_edge(START, "answer")
    saver = MemorySaver()
    compiled = graph.compile(checkpointer=saver)

    def threads():
        return {c.config["configurable"]["thread_id"] for c in saver.list(None)}

    async with adapter_task("T1", "hi", context_id="CHAT", user_id="alice") as chat:
        await run_graph(chat.ctx, compiled, stream_text=False)
    async with adapter_task("T2", "hi", context_id="CHAT", session_behavior="run") as peer_call:
        result = await run_graph(peer_call.ctx, compiled, stream_text=False)

    assert result["messages"][-1].text == "done"
    assert threads() == {"alice:CHAT"}
