# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""LangChain callbacks → step events, driven by a fake chat model that emits tool calls."""

from __future__ import annotations

import asyncio
import json
import re
from uuid import uuid4

import pytest
from langchain_core.language_models.fake_chat_models import GenericFakeChatModel
from langchain_core.messages import AIMessage, AIMessageChunk
from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, LLMResult
from langchain_core.tools import tool

from sam_link_sdk.context import TaskHalted
from sam_link_sdk.events import ModelStarted, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.langchain import SamLinkCallbackHandler, run_agent

from .adapter_task import adapter_task, steps


class ToolCallingFake(GenericFakeChatModel):
    def bind_tools(self, tools, **kwargs):
        return self

    def _stream(self, messages, stop=None, run_manager=None, **kwargs):
        """The scripted message as a real model streams it: its text in pieces, then one chunk with the tool calls and
        usage (GenericFakeChatModel's own stream drops both)."""
        message = self._generate(messages, stop=stop, run_manager=run_manager, **kwargs).generations[0].message
        for piece in [p for p in re.split(r"(\s)", message.content) if p]:
            chunk = ChatGenerationChunk(message=AIMessageChunk(content=piece, id=message.id))
            if run_manager:
                run_manager.on_llm_new_token(piece, chunk=chunk)
            yield chunk
        calls = [
            {"name": c["name"], "args": json.dumps(c["args"]), "id": c["id"], "index": n}
            for n, c in enumerate(message.tool_calls)
        ]
        yield ChatGenerationChunk(
            message=AIMessageChunk(
                content="",
                id=message.id,
                tool_call_chunks=calls,
                usage_metadata=message.usage_metadata,
                chunk_position="last",
            )
        )


@tool
def lookup_policy(policy_id: str) -> dict:
    """Look up a policy."""
    return {"tier": "gold", "policy_id": policy_id}


@tool
def broken(policy_id: str) -> str:
    """Always fails."""
    raise RuntimeError("backend down")


def usage(i, o):
    return {
        "input_tokens": i,
        "output_tokens": o,
        "total_tokens": i + o,
        "input_token_details": {"cache_read": 3},
        "output_token_details": {"reasoning": 2},
    }


async def test_steps_and_matching_function_call_ids():
    model = ToolCallingFake(
        model_name="fake-model",
        messages=iter(
            [
                AIMessage(
                    content="I'll look up the policy.",
                    tool_calls=[{"id": "call_7f3", "name": "lookup_policy", "args": {"policy_id": "P-1182"}}],
                    usage_metadata=usage(812, 41),
                ),
                AIMessage(content="Claim P-1182 routed to fast-track.", usage_metadata=usage(838, 79)),
            ]
        ),
    )
    async with adapter_task(text="Triage claim P-1182") as task:
        answer = await run_agent(task.ctx, model, [lookup_policy])
        sent = steps(task)
        tu = task.ctx._run.usage.final_token_usage()
    assert answer == "Claim P-1182 routed to fast-track."

    assert [e.kind for e in sent] == [
        "model_started",
        "model_finished",
        "tool_started",
        "tool_finished",
        "model_started",
        "model_finished",
    ]
    inv, resp, start, result = sent[:4]
    assert isinstance(inv, ModelStarted) and inv.model
    assert resp.text == "I'll look up the policy."
    assert resp.tool_calls == (ToolCall("call_7f3", "lookup_policy", {"policy_id": "P-1182"}),)
    u = resp.usage
    assert resp.model == inv.model
    assert (u.input_tokens, u.output_tokens, u.cached_input_tokens, u.cache_write_tokens, u.reasoning_tokens) == (
        812,
        41,
        3,
        0,
        2,
    )
    assert start == ToolStarted("call_7f3", "lookup_policy", {"policy_id": "P-1182"})
    assert result == ToolFinished("call_7f3", "lookup_policy", {"tier": "gold", "policy_id": "P-1182"})

    assert tu["total_input_tokens"] == 1650 and tu["total_output_tokens"] == 120
    assert tu["total_cached_input_tokens"] == 6 and tu["last_input_tokens"] == 838


async def test_parallel_calls_share_group_and_missing_ids_generated():
    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content="",
                    tool_calls=[
                        {"id": "", "name": "lookup_policy", "args": {"policy_id": "A"}},
                        {"id": "", "name": "lookup_policy", "args": {"policy_id": "B"}},
                    ],
                ),
                AIMessage(content="done"),
            ]
        ),
    )
    async with adapter_task() as task:
        await run_agent(task.ctx, model, [lookup_policy])
        sent = steps(task)
        assert task.ctx._run.usage.final_token_usage() is None
    ids = [c.id for c in sent[1].tool_calls]
    assert len(ids) == 2 and all(i.startswith("call_") for i in ids) and ids[0] != ids[1]
    starts = [e for e in sent if isinstance(e, ToolStarted)]
    results = [e for e in sent if isinstance(e, ToolFinished)]
    assert [e.call_id for e in starts] == ids
    assert [e.call_id for e in results] == ids


async def test_tool_error_becomes_error_result():
    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(content="", tool_calls=[{"id": "c1", "name": "broken", "args": {"policy_id": "X"}}]),
                AIMessage(content="sorry"),
            ]
        )
    )
    async with adapter_task() as task:
        await run_agent(task.ctx, model, [broken])
        result = [e for e in steps(task) if isinstance(e, ToolFinished)][0]
    assert result.call_id == "c1"
    assert result.result == {"error": "backend down"}


async def test_a_tool_that_stops_its_own_run_ends_it_instead_of_telling_the_model_it_failed():
    """A tool that checks its task (``ctx.check_running()``) raises TaskHalted when the task was stopped. The loop must
    pass that on; as a tool error it would go on to ask the model, which answers a task that was stopped. SAM cannot
    see this: the stop's outcome is the agent's whichever way the loop goes."""
    calls = []

    @tool
    def stops(policy_id: str) -> str:
        """Stops its own run."""
        calls.append(policy_id)
        raise TaskHalted("T", "cancelled")

    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(content="", tool_calls=[{"id": "c1", "name": "stops", "args": {"policy_id": "X"}}]),
                AIMessage(content="sorry"),
            ]
        )
    )
    async with adapter_task("T") as task:
        with pytest.raises(TaskHalted):
            await run_agent(task.ctx, model, [stops])
    assert calls == ["X"]


async def test_a_task_without_streaming_gets_no_steps_but_usage_counts():
    model = ToolCallingFake(messages=iter([AIMessage(content="hi", usage_metadata=usage(5, 1))]))
    async with adapter_task(streaming=False) as task:
        assert await run_agent(task.ctx, model, []) == "hi"
        assert steps(task) == []
        assert task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 5


async def test_a_stopped_task_ends_its_run_at_the_first_model_call_and_publishes_nothing():
    model = ToolCallingFake(messages=iter([AIMessage(content="hi")]))
    async with adapter_task() as task:
        await task.cancel()
        with pytest.raises(TaskHalted):
            await run_agent(task.ctx, model, [])
        assert task.events == []


async def test_an_approved_tool_is_reported_once_and_what_it_runs_is_reported_and_checked():
    """with_approval runs the approved tool in the wrapper's run config (no longer with no callbacks): what the tool
    runs in turn reaches the step reporter, and the tool's own start and result are not reported a second time."""
    from types import SimpleNamespace

    from sam_link_sdk.langchain import with_approval

    inner_model = ToolCallingFake(messages=iter([AIMessage(content="inner says gold")]))

    @tool
    async def consult(question: str) -> str:
        """Ask another model."""
        return (await inner_model.ainvoke(question)).text

    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(content="", tool_calls=[{"id": "call_c", "name": "consult", "args": {"question": "tier?"}}]),
                AIMessage(content="Gold."),
            ]
        )
    )

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        return SimpleNamespace(approved=True, outcome="approved")

    async with adapter_task() as task:
        task.ctx.request_approval = request_approval
        assert await run_agent(task.ctx, model, with_approval(task.ctx, [consult], names=["consult"])) == "Gold."
        kinds = [e.kind for e in steps(task)]
    assert kinds.count("tool_started") == 1 and kinds.count("tool_finished") == 1
    assert kinds.count("model_finished") == 3


def test_usage_on_priority_and_flex_tiers_counts_reasoning_too():
    from sam_link_sdk.langchain.steps import _usage_from

    message = AIMessage(
        content="ok",
        usage_metadata={
            "input_tokens": 10,
            "output_tokens": 8,
            "total_tokens": 18,
            "input_token_details": {"priority_cache_read": 4},
            "output_token_details": {"priority_reasoning": 5},
        },
    )
    usage = _usage_from(message)
    assert (usage.cached_input_tokens, usage.reasoning_tokens) == (4, 5)


async def test_langchain_reasoning_blocks_stream_before_the_response_and_stay_out_of_the_answer():
    message = AIMessage(
        content=[{"type": "thinking", "thinking": "weigh the tiers"}, {"type": "text", "text": "Fast-track."}]
    )
    async with adapter_task(stream_thinking=True) as task:
        handler = SamLinkCallbackHandler(task.ctx)
        run_id = uuid4()
        handler.on_chat_model_start({}, [[]], run_id=run_id, metadata={"ls_model_name": "claude-sonnet-5"})
        await asyncio.sleep(0)
        await handler.on_llm_end(LLMResult(generations=[[ChatGeneration(message=message)]]), run_id=run_id)
        sent = steps(task)

    assert [e.kind for e in sent] == ["model_started", "thinking", "thinking", "model_finished"]
    assert sent[1].text == "weigh the tiers" and sent[2].done is True
    assert (sent[3].text, sent[3].tool_calls) == ("Fast-track.", ())


async def test_langchain_reasoning_is_silent_by_default():
    message = AIMessage(content=[{"type": "thinking", "thinking": "private"}, {"type": "text", "text": "ok"}])
    async with adapter_task() as task:
        handler = SamLinkCallbackHandler(task.ctx)
        run_id = uuid4()
        handler.on_chat_model_start({}, [[]], run_id=run_id, metadata={"ls_model_name": "claude-sonnet-5"})
        await asyncio.sleep(0)
        await handler.on_llm_end(LLMResult(generations=[[ChatGeneration(message=message)]]), run_id=run_id)
        sent = steps(task)
    assert sent and "private" not in repr(sent)


async def test_a_tool_call_without_an_id_is_given_one_the_tool_message_the_model_gets_back_carries_too():
    from langchain_core.messages import ToolMessage

    seen: list = []

    class Recording(ToolCallingFake):
        def _generate(self, messages, *args, **kwargs):
            seen.append(list(messages))
            return super()._generate(messages, *args, **kwargs)

    model = Recording(
        messages=iter(
            [
                AIMessage(content="", tool_calls=[{"id": None, "name": "lookup_policy", "args": {"policy_id": "A"}}]),
                AIMessage(content="done"),
            ]
        )
    )
    async with adapter_task() as task:
        await run_agent(task.ctx, model, [lookup_policy])
        sent = steps(task)
    [call_id] = [c.id for c in sent[1].tool_calls]
    assert call_id.startswith("call_")
    assert [e.call_id for e in sent if isinstance(e, (ToolStarted, ToolFinished))] == [call_id, call_id]
    [reply] = [m for m in seen[-1] if isinstance(m, ToolMessage)]
    assert reply.tool_call_id == call_id
