# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Conversation memory: the store's bounds, and replay in the LangChain loop and the OpenAI Agents runner."""

from __future__ import annotations

import pytest

from sam_link_sdk import ConversationMemory

from .adapter_task import adapter_task


class Clock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now


def test_turns_are_replayed_oldest_first_and_trimmed_by_whole_turns():
    memory = ConversationMemory(max_turns=2)
    memory.remember("c", ["u1", "a1"])
    memory.remember("c", ["u2", "tool", "a2"])
    memory.remember("c", ["u3", "a3"])
    assert memory.history("c") == ["u2", "tool", "a2", "u3", "a3"]


def test_conversations_are_separate_and_a_missing_id_stores_nothing():
    memory = ConversationMemory()
    memory.remember("a", ["x"])
    memory.remember("", ["ignored"])
    assert memory.history("b") == [] and memory.history("") == [] and memory.history("a") == ["x"]
    assert len(memory) == 1


def test_idle_conversations_expire():
    clock = Clock()
    memory = ConversationMemory(ttl_seconds=60)
    memory._clock = clock
    memory.remember("c", ["x"])
    clock.now = 59
    assert memory.history("c") == ["x"]
    clock.now = 61
    assert memory.history("c") == [] and len(memory) == 0


def test_the_least_recently_used_conversation_is_dropped():
    memory = ConversationMemory(max_conversations=2)
    memory.remember("a", ["a"])
    memory.remember("b", ["b"])
    memory.remember("a", ["a2"])
    memory.remember("c", ["c"])
    assert memory.history("b") == [] and memory.history("a") and memory.history("c")


def test_history_is_a_copy_and_forget_clears_it():
    memory = ConversationMemory()
    items = ["x"]
    memory.remember("c", items)
    items.append("changed later")
    memory.history("c").append("also changed")
    assert memory.history("c") == ["x"]
    memory.forget("c")
    assert memory.history("c") == []


def test_transcript_renders_the_exchanges():
    memory = ConversationMemory()
    memory.remember("c", [], "Triage P-1", "Fast-track.")
    memory.remember("c", [], "And P-2?", "Standard.")
    assert memory.transcript("c") == "User: Triage P-1\nAssistant: Fast-track.\nUser: And P-2?\nAssistant: Standard."
    assert memory.transcript("other") == ""


async def in_task(run, text, context_id="CTX", run_based=False, task_id="T"):
    """``run(ctx)`` in a task of a real agent: ``text`` delivered in conversation ``context_id``, as a run-based task
    (a peer call: no chat history) when ``run_based``."""
    behavior = {"session_behavior": "run"} if run_based else {}
    async with adapter_task(task_id, text, context_id=context_id, **behavior) as task:
        return await run(task.ctx)


pytest.importorskip("langchain_core")

from langchain_core.language_models import BaseChatModel  # noqa: E402
from langchain_core.messages import AIMessage  # noqa: E402
from langchain_core.outputs import ChatGeneration, ChatResult  # noqa: E402
from pydantic import Field  # noqa: E402

from sam_link_sdk.langchain import run_agent as run_langchain  # noqa: E402


class RecordingModel(BaseChatModel):
    replies: list[str]
    seen: list = Field(default_factory=list)

    @property
    def _llm_type(self) -> str:
        return "recording"

    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        self.seen.append([(m.type, m.content) for m in messages])
        return ChatResult(generations=[ChatGeneration(message=AIMessage(content=self.replies.pop(0)))])

    def bind_tools(self, tools, **kwargs):
        return self


async def test_langchain_loop_replays_earlier_turns_of_the_same_conversation():
    model, memory = RecordingModel(replies=["Fast-track.", "Standard."]), ConversationMemory()

    def triage(ctx):
        return run_langchain(ctx, model, [], system_prompt="You triage.", memory=memory)

    assert await in_task(triage, "Triage P-1") == "Fast-track."
    assert await in_task(triage, "And P-2?") == "Standard."

    assert model.seen[0] == [("system", "You triage."), ("human", "Triage P-1")]
    assert model.seen[1] == [
        ("system", "You triage."),
        ("human", "Triage P-1"),
        ("ai", "Fast-track."),
        ("human", "And P-2?"),
    ]


async def test_a_different_conversation_starts_fresh():
    model, memory = RecordingModel(replies=["a", "b"]), ConversationMemory()
    await in_task(lambda ctx: run_langchain(ctx, model, [], memory=memory), "first", "ONE")
    await in_task(lambda ctx: run_langchain(ctx, model, [], memory=memory), "second", "TWO")
    assert model.seen[1] == [("human", "second")]


async def test_without_memory_every_message_is_fresh():
    model = RecordingModel(replies=["a", "b"])
    await in_task(lambda ctx: run_langchain(ctx, model, []), "first")
    await in_task(lambda ctx: run_langchain(ctx, model, []), "second")
    assert model.seen[1] == [("human", "second")]


async def test_a_run_based_call_in_the_same_chat_neither_sees_nor_adds_history():
    model, memory = RecordingModel(replies=["Fast-track.", "Covered.", "Standard."]), ConversationMemory()

    await in_task(lambda ctx: run_langchain(ctx, model, [], memory=memory), "Triage P-1")
    await in_task(lambda ctx: run_langchain(ctx, model, [], memory=memory), "Check coverage", run_based=True)
    await in_task(lambda ctx: run_langchain(ctx, model, [], memory=memory), "And P-2?")

    assert model.seen[1] == [("human", "Check coverage")]
    assert model.seen[2] == [("human", "Triage P-1"), ("ai", "Fast-track."), ("human", "And P-2?")]


async def test_a_turn_that_fails_is_not_remembered():
    class Failing(RecordingModel):
        def _generate(self, messages, stop=None, run_manager=None, **kwargs):
            raise RuntimeError("provider down")

    memory = ConversationMemory()
    with pytest.raises(RuntimeError):
        await in_task(lambda ctx: run_langchain(ctx, Failing(replies=[]), [], memory=memory), "lost")
    assert memory.history("CTX") == []


pytest.importorskip("agents")

from agents import Agent, ModelResponse  # noqa: E402

from sam_link_sdk.openai_agents import run_agent  # noqa: E402

from .test_openai_agents import ScriptedModel, message, usage  # noqa: E402


class CapturingModel(ScriptedModel):
    def __init__(self, turns):
        super().__init__(turns)
        self.inputs = []

    async def get_response(self, system_instructions, input, *args, **kwargs):
        self.inputs.append(input)
        output, u = self.turns.pop(0)
        return ModelResponse(output=output, usage=u, response_id=None)


def texts(model_input):
    if isinstance(model_input, str):
        return [model_input]
    out = []
    for item in model_input:
        content = item.get("content") if isinstance(item, dict) else getattr(item, "content", None)
        if isinstance(content, str):
            out.append(content)
        elif isinstance(content, list):
            out.extend(
                part.get("text", "") if isinstance(part, dict) else getattr(part, "text", "") for part in content
            )
    return out


async def test_openai_agents_runner_replays_earlier_turns():
    model = CapturingModel([([message("Fast-track.")], usage(10, 2)), ([message("Standard.")], usage(12, 2))])
    agent, memory = Agent(name="claims-triage", instructions="x", model=model), ConversationMemory()

    assert await in_task(lambda ctx: run_agent(ctx, agent, memory=memory), "Triage P-1") == "Fast-track."
    assert await in_task(lambda ctx: run_agent(ctx, agent, memory=memory), "And P-2?") == "Standard."

    assert texts(model.inputs[0]) == ["Triage P-1"]
    assert texts(model.inputs[1]) == ["Triage P-1", "Fast-track.", "And P-2?"]
    assert len(memory.history("CTX")) == 4


pytest.importorskip("langgraph")

from .test_langgraph import load_example  # noqa: E402


async def test_the_langgraph_example_keeps_each_conversation_by_context_id():
    example = load_example()
    model = RecordingModel(replies=["Fast-track.", "Standard.", "Manual review."])
    handler = example.make_handler(model)

    await in_task(handler, "Triage P-1", "CHAT-A")
    await in_task(handler, "And P-2?", "CHAT-A")
    await in_task(handler, "Triage P-3", "CHAT-B")

    system = ("system", example.INSTRUCTIONS)
    assert model.seen[1] == [system, ("human", "Triage P-1"), ("ai", "Fast-track."), ("human", "And P-2?")]
    assert model.seen[2] == [system, ("human", "Triage P-3")]


async def test_the_langgraph_example_runs_a_peer_call_on_a_thread_it_then_drops():
    example = load_example()
    model, saver = RecordingModel(replies=["Fast-track.", "Covered.", "Standard."]), example.MemorySaver()
    handler = example.make_handler(model, checkpointer=saver)

    await in_task(handler, "Triage P-1", "CHAT-A")
    await in_task(handler, "Check coverage", "CHAT-A", run_based=True, task_id="PEER-1")
    await in_task(handler, "And P-2?", "CHAT-A")

    system = ("system", example.INSTRUCTIONS)
    assert model.seen[1] == [system, ("human", "Check coverage")]
    assert model.seen[2] == [system, ("human", "Triage P-1"), ("ai", "Fast-track."), ("human", "And P-2?")]
    assert saver.get_tuple({"configurable": {"thread_id": "PEER-1"}}) is None
