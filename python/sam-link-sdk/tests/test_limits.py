# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Per-task limits: model calls and wall-clock time."""

from __future__ import annotations

import asyncio

from sam_link_sdk import AgentConfig
from sam_link_sdk.events import ModelStarted

from .agent_rig import settle, start_agent


async def start(handler, **cfg):
    events = []
    agent, transport = await start_agent(handler, audit_sink=events.append, **cfg)
    return agent, transport, events


def reply(transport, task_id="T1"):
    return transport.finished[task_id]


async def wait_for_reply(transport, task_id="T1"):
    for _ in range(400):
        if task_id in transport.finished:
            return
        await asyncio.sleep(0.005)


async def call_model(ctx, times):
    for _ in range(times):
        await ctx.emit_event(ModelStarted("m"))
        await asyncio.sleep(0)


async def test_the_call_over_the_limit_stops_the_task_with_a_failed_reply():
    reached = []

    async def handler(ctx):
        await call_model(ctx, 10)
        reached.append("finished")
        return "never"

    agent, transport, events = await start(handler, max_llm_calls=3)
    await transport.deliver("T1", user_id="u1")

    result = reply(transport)
    assert (result.state, result.text) == ("failed", "Stopped: the task reached the limit of 3 model calls.")
    assert reached == []
    assert [e.message for e in events] == ["task limit reached"]
    assert dict(events[0].fields) == {
        "userID": "u1",
        "taskID": "T1",
        "agentName": "claims-triage",
        "limit": "max_llm_calls",
        "value": 3,
    }
    await agent.stop()


async def test_a_task_within_the_limit_completes():
    async def handler(ctx):
        await call_model(ctx, 3)
        return "done"

    agent, transport, events = await start(handler, max_llm_calls=3)
    await transport.deliver("T1")
    assert reply(transport).state == "completed" and events == []
    await agent.stop()


async def test_the_default_limit_matches_sam_go():
    assert AgentConfig(name="a", description="d").max_llm_calls == 20

    async def handler(ctx):
        await call_model(ctx, 21)
        return "never"

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    await wait_for_reply(transport)
    assert reply(transport).text.endswith("limit of 20 model calls.")
    await agent.stop()


async def test_zero_turns_the_limit_off():
    async def handler(ctx):
        await call_model(ctx, 50)
        return "done"

    agent, transport, _ = await start(handler, max_llm_calls=0)
    await transport.deliver("T1")
    await wait_for_reply(transport)
    assert reply(transport).state == "completed"
    await agent.stop()


async def test_the_timeout_stops_a_stuck_task():
    async def handler(ctx):
        await asyncio.sleep(30)
        return "never"

    agent, transport, events = await start(handler, task_timeout_seconds=0.05)
    await transport.deliver("T1")
    await asyncio.sleep(0.1)
    await settle()

    result = reply(transport)
    assert (result.state, result.text) == ("failed", "Stopped: the task ran longer than the limit of 0.05 seconds.")
    assert events[0].fields["limit"] == "task_timeout_seconds"
    assert agent._runner.tasks == {}
    await agent.stop()


async def test_a_task_that_finishes_before_the_timeout_is_untouched():
    agent, transport, events = await start(lambda ctx: "quick", task_timeout_seconds=5)
    await transport.deliver("T1")
    assert reply(transport).state == "completed" and events == []
    await agent.stop()


async def test_a_user_cancel_is_still_a_cancel_not_a_limit():
    started = asyncio.Event()

    async def handler(ctx):
        started.set()
        await asyncio.sleep(30)
        return "never"

    agent, transport, events = await start(handler, max_llm_calls=3)
    await transport.deliver("T1")
    await started.wait()
    await transport.cancel("T1")

    assert reply(transport).state == "canceled" and events == []
    assert len(transport.answers) == 1
    await agent.stop()


def test_a_framework_limit_wrapped_in_another_exception_still_gives_the_limit_message():
    from agents.exceptions import MaxTurnsExceeded

    from sam_link_sdk.limits import limit_message

    expected = "Stopped: the agent reached the OpenAI Agents limit on model calls (max_model_calls)."
    try:
        try:
            raise MaxTurnsExceeded("Max turns (10) exceeded")
        except MaxTurnsExceeded as limit:
            raise RuntimeError("agent step failed") from limit
    except RuntimeError as wrapped:
        assert limit_message(wrapped) == expected
    try:
        try:
            raise MaxTurnsExceeded("Max turns (10) exceeded")
        except MaxTurnsExceeded:
            raise RuntimeError("cleanup failed")  # noqa: B904
    except RuntimeError as during:
        assert limit_message(during) == expected
    assert limit_message(RuntimeError("tool crashed")) is None


def test_a_guardrail_that_trips_names_itself_and_what_it_blocked():
    from types import SimpleNamespace

    from agents.exceptions import InputGuardrailTripwireTriggered, OutputGuardrailTripwireTriggered

    from sam_link_sdk.limits import limit_message

    result = SimpleNamespace(guardrail=SimpleNamespace(get_name=lambda: "no_pii"))
    assert (
        limit_message(InputGuardrailTripwireTriggered(result)) == "Stopped: the guardrail no_pii blocked this request."
    )
    assert (
        limit_message(OutputGuardrailTripwireTriggered(result))
        == "Stopped: the guardrail no_pii blocked the agent's answer."
    )
    unnamed = SimpleNamespace(guardrail=object())
    assert (
        limit_message(OutputGuardrailTripwireTriggered(unnamed))
        == "Stopped: the guardrail a guardrail blocked the agent's answer."
    )
