# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""MeshAgent: the description it gives SAM, how it runs a task and answers it, cancels, shutdown, the keepalive, the
concurrency slots, and what work left by an answered task may still reach. (On the wire: tests/test_link*.py.)"""

from __future__ import annotations

import asyncio

import pytest

from sam_link_sdk import TaskResult, Usage
from sam_link_sdk._model import RUN
from sam_link_sdk.events import ModelFailed, ModelFinished, ModelStarted, Progress, Text, ToolStarted

from .agent_rig import settle, start_agent


async def test_start_describes_the_agent_and_stop_retires_it():
    agent, transport = await start_agent(lambda ctx: "ok", display_name="Claims Triage")
    [description] = transport.descriptions
    assert (description.display_name, description.description) == ("Claims Triage", "Triages insurance claims.")
    assert transport.opened == [{"serving": True, "wait_for_peers": True}]
    await agent.stop()
    assert transport.withdrawn == 1 and transport.disconnected == 1


async def test_happy_path_steps_then_streamed_answer_then_answer():
    async def handler(ctx):
        assert ctx.text == "line one\nline two"
        await ctx.progress("Checking policy tier")
        ctx.record_usage("m", Usage(input_tokens=10, output_tokens=2))
        return "Routed."

    agent, transport = await start_agent(handler)
    await transport.deliver("T1", "line one\nline two", user_id="u1")

    assert transport.events("T1") == [Progress("Checking policy tier"), Text("Routed.")]
    answer = transport.finished["T1"]
    assert (answer.state, answer.text) == ("completed", "Routed.")
    assert transport.usage == [("T1", "m", Usage(input_tokens=10, output_tokens=2))]
    assert agent._runner.tasks == {}
    await agent.stop()


@pytest.mark.parametrize("behaviour, run_based", [("session", False), (RUN, True)])
async def test_a_run_based_task_has_no_session_id(behaviour, run_based):
    seen = []
    agent, transport = await start_agent(
        lambda ctx: seen.append((ctx.run_based, ctx.session_id, ctx.context_id)) or "ok"
    )
    await transport.deliver("t-rb", context_id="CTX", user_id="u1", session_behavior=behaviour)
    assert seen == [(run_based, "" if run_based else "u1:CTX", "CTX")]
    await agent.stop()


async def test_logical_failure_task():
    agent, transport = await start_agent(lambda ctx: _const(TaskResult("Policy not found.", state="failed")))
    await transport.deliver("T2")
    answer = transport.finished["T2"]
    assert (answer.state, answer.text) == ("failed", "Policy not found.")
    assert transport.events("T2") == []
    await agent.stop()


async def test_an_exception_becomes_a_failed_answer_without_its_text():
    async def handler(ctx):
        ctx.record_usage("m", Usage(input_tokens=3))
        raise RuntimeError("secret prompt text")

    agent, transport = await start_agent(handler)
    await transport.deliver("T3")
    answer = transport.finished["T3"]
    assert answer.state == "failed" and answer.error is not None and answer.text == ""
    assert "secret" not in repr(answer)
    assert transport.usage == [("T3", "m", Usage(input_tokens=3))]
    await agent.stop()


async def test_a_task_no_one_streams_still_answers_and_sends_no_steps():
    async def handler(ctx):
        await ctx.progress("x")
        return "ok"

    agent, transport = await start_agent(handler)
    await transport.deliver("T5", streaming=False)
    assert transport.events("T5") == [] and transport.finished["T5"].text == "ok"
    await agent.stop()


async def test_streamed_text_not_repeated():
    async def handler(ctx):
        await ctx.emit_text("Routed.")
        return "Routed."

    agent, transport = await start_agent(handler)
    await transport.deliver("T11")
    assert transport.events("T11") == [Text("Routed.")]
    await agent.stop()


async def test_cancel_answers_once_and_stops_the_run():
    started_evt = asyncio.Event()
    finished = []

    async def handler(ctx):
        ctx.record_usage("m", Usage(input_tokens=7, output_tokens=1))
        started_evt.set()
        await asyncio.sleep(30)
        finished.append(1)
        return "never"

    agent, transport = await start_agent(handler)
    await transport.deliver("T9")
    await started_evt.wait()
    await transport.cancel("T9")
    await transport.cancel("T9")

    assert [(t, o.state) for t, o in transport.answers] == [("T9", "canceled")]
    assert transport.finished["T9"].answer_to_cancel
    assert finished == [] and agent._runner.tasks == {}
    await agent.stop()


async def test_cancel_of_an_unknown_task_answers_nothing():
    agent, transport = await start_agent(lambda ctx: "x")
    await transport.cancel("X")
    assert transport.answers == []
    await agent.stop()


async def test_shutdown_leaves_an_inflight_task_unanswered():
    gate = asyncio.Event()

    async def handler(ctx):
        gate.set()
        await asyncio.sleep(30)
        return "x"

    agent, transport = await start_agent(handler, shutdown_grace_seconds=0.05)
    await transport.deliver("T10")
    await gate.wait()
    await agent.stop()
    await settle()
    assert transport.answers == []


async def test_a_task_waiting_for_its_user_gets_no_keepalive_while_a_silent_one_does():
    asked = asyncio.Event()

    async def handler(ctx):
        if ctx.task_id == "T1":
            asked.set()
            return (await ctx.ask_user("Which queue?", timeout_seconds=30)).status
        await asyncio.sleep(30)
        return "never"

    agent, transport = await start_agent(handler, keepalive_seconds=0.2, shutdown_grace_seconds=0)
    await transport.deliver("T1")
    await asyncio.wait_for(asked.wait(), 5)
    await transport.deliver("T2")
    for _ in range(300):
        if transport.events("T2", Progress):
            break
        await asyncio.sleep(0.01)
    assert transport.events("T2", Progress)
    assert transport.events("T1", Progress) == [] and transport.prompts("T1")
    await agent.stop()


async def test_work_that_outlives_the_answer_still_reports_on_the_task():
    late = asyncio.Event()

    background = []

    async def handler(ctx):
        async def later():
            await late.wait()
            await ctx.progress("late")

        background.append(asyncio.ensure_future(later()))
        return "done"

    agent, transport = await start_agent(handler)
    await transport.deliver("T1")
    assert transport.finished["T1"].text == "done"
    late.set()
    await settle()
    assert transport.events("T1")[-1] == Progress("late")
    await agent.stop()


async def test_a_task_whose_time_limit_runs_out_while_it_waits_for_a_slot_is_answered():
    first_running, release = asyncio.Event(), asyncio.Event()

    async def handler(ctx):
        if ctx.task_id == "T1":
            first_running.set()
            try:
                await release.wait()
            except asyncio.CancelledError:
                await asyncio.shield(release.wait())
                raise
        return "done " + ctx.task_id

    agent, transport = await start_agent(handler, max_concurrent_tasks=1, task_timeout_seconds=0.2)
    await transport.deliver("T1")
    await first_running.wait()
    await transport.deliver("T2")
    for _ in range(200):
        if "T2" in transport.finished:
            break
        await asyncio.sleep(0.01)
    answer = transport.finished["T2"]
    assert answer.state == "failed" and "ran longer than the limit" in answer.text
    assert "T2" not in agent._runner.tasks
    release.set()
    await agent.stop()


async def test_work_left_by_an_answered_task_reaches_nothing_of_the_same_task_id_sent_again():
    second_started, stale_done, runs = asyncio.Event(), asyncio.Event(), []

    async def leftover(ctx):
        await second_started.wait()
        await ctx.progress("A stale step of the first run.")
        stale_done.set()

    background = []

    async def handler(ctx):
        runs.append(ctx.task_id)
        if len(runs) == 1:
            background.append(asyncio.ensure_future(leftover(ctx)))
            return "The first answer."
        second_started.set()
        await stale_done.wait()
        return "Claim C-1 is approved."

    agent, transport = await start_agent(handler)
    await transport.deliver("T1")
    await transport.deliver("T1")
    await asyncio.wait_for(stale_done.wait(), 5)
    await settle()
    assert not any("stale" in repr(e).lower() for e in transport.events("T1"))
    assert [o.text for _, o in transport.answers] == ["The first answer.", "Claim C-1 is approved."]
    await agent.stop()


def test_the_give_backs_remembered_are_bounded_in_count_and_age():
    from sam_link_sdk.link.giveback import GIVEN_BACK_KEPT_SECONDS, MAX_GIVEN_BACK_KEPT, GiveBacks

    now = [0.0]
    remembered = GiveBacks(clock=lambda: now[0])
    assert remembered.first("T1") and not remembered.first("T1")
    for i in range(MAX_GIVEN_BACK_KEPT + 50):
        remembered.first(f"other-{i}")
    assert len(remembered) == MAX_GIVEN_BACK_KEPT and remembered.first("T1")
    now[0] += GIVEN_BACK_KEPT_SECONDS + 1
    assert remembered.first("T2") and len(remembered) == 1


async def _const(value):
    return value


async def test_the_keepalive_survives_a_failing_progress_and_keeps_other_tasks_alive(caplog):
    class Flaky:
        raised = False

    async def handler(ctx):
        await asyncio.sleep(30)
        return "never"

    agent, transport = await start_agent(handler, keepalive_seconds=0.2, shutdown_grace_seconds=0)
    original = transport._send_event

    async def send_event(task_id, event):
        if not Flaky.raised:
            Flaky.raised = True
            raise RuntimeError("metrics broke")
        return await original(task_id, event)

    transport._send_event = send_event
    await transport.deliver("T1")
    with caplog.at_level("ERROR"):
        for _ in range(300):
            if transport.events("T1", Progress):
                break
            await asyncio.sleep(0.01)
    assert Flaky.raised
    assert [e.text for e in transport.events("T1", Progress)][:1] == ["Still working"]
    assert any("keepalive failed" in r.getMessage() and "T1" in r.getMessage() for r in caplog.records)
    await agent.stop()


async def test_a_handler_that_swallows_the_cancel_at_the_timeout_is_answered_and_frees_its_slot(monkeypatch):
    monkeypatch.setattr("sam_link_sdk.tasks.CANCEL_DEADLINE_SECONDS", 0.1)
    release = asyncio.Event()

    async def handler(ctx):
        if ctx.task_id == "T2":
            return "second"
        while not release.is_set():
            try:
                await release.wait()
            except asyncio.CancelledError:
                continue
        return "late"

    agent, transport = await start_agent(handler, task_timeout_seconds=0.05, max_concurrent_tasks=1)
    try:
        await transport.deliver("T1")
        answer = await transport.answered("T1")
        assert (answer.state, answer.text) == (
            "failed",
            "Stopped: the task ran longer than the limit of 0.05 seconds.",
        )
        await transport.deliver("T2")
        assert (await transport.answered("T2")).text == "second"
    finally:
        release.set()
    await settle()
    assert [t for t, _ in transport.answers].count("T1") == 1
    await agent.stop()


async def test_a_health_problem_set_again_for_the_same_id_replaces_the_first():
    """SAM's card is only sent when it changes; what the agent describes is what SAM will show."""
    agent, _ = await start_agent(lambda ctx: "ok")
    agent.set_health_problem("db", "degraded", "slow")
    agent.set_health_problem("db", "fatal", "down")
    assert [(p.id, p.severity, p.detail) for p in agent._host.description().health] == [("db", "fatal", "down")]
    agent.set_health_problem("db", "degraded", "recovering")
    assert [(p.severity, p.detail) for p in agent._host.description().health] == [("degraded", "recovering")]


def test_the_sdk_version_is_that_of_the_distribution_that_ships_the_module_whatever_it_is_named(monkeypatch):
    from importlib import metadata

    from sam_link_sdk import agent as agent_module

    versions = {"sam-link-sdk": "9.8.7"}
    monkeypatch.setattr(metadata, "packages_distributions", lambda: {"sam_link_sdk": ["sam-link-sdk"]})

    def version(name: str) -> str:
        if name not in versions:
            raise metadata.PackageNotFoundError(name)
        return versions[name]

    monkeypatch.setattr(metadata, "version", version)
    clear = getattr(agent_module.sdk_version, "cache_clear", lambda: None)
    clear()
    try:
        assert agent_module.sdk_version() == "9.8.7"
        monkeypatch.setattr(metadata, "packages_distributions", lambda: pytest.fail("read the metadata twice"))
        assert agent_module.sdk_version() == "9.8.7"
    finally:
        clear()


def test_an_agent_name_with_a_trailing_newline_is_refused():
    """A name with a trailing newline is refused.

    A name read from a file (SAM_AGENT_NAME) ends in one, and the name becomes a header and a file name.
    """
    from sam_link_sdk import AgentConfig

    with pytest.raises(ValueError, match="agent name"):
        AgentConfig(name="claims-triage\n", description="d")


async def test_a_model_failure_a_retry_got_past_is_not_the_cause_of_a_later_tool_failure():
    async def handler(ctx):
        await ctx.emit_event(ModelStarted("m"))
        await ctx.emit_event(ModelFailed("m", "rate_limit", "openai"))
        await ctx.emit_event(ModelStarted("m"))
        await ctx.emit_event(ModelFinished("m"))
        await ctx.emit_event(ToolStarted("c1", "lookup_policy"))
        raise KeyError("policy")

    agent, transport = await start_agent(handler)
    try:
        await transport.deliver("T1")
        answer = await transport.answered("T1")
        assert answer.state == "failed"
        assert answer.error is not None and answer.error.category == ""
    finally:
        await agent.stop()
