# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A run that outlives its connection: after a reconnect SAM may no longer hold the run's task (another replica
serves the agent now, or the same one after it stood down), and delivers the task's request again. Until that delivery
the run sends nothing for the task, and a refusal that SAM holds no such task is no cancel. Every such wait ends: with
the delivery, at its bound, when the link drops again, and when the agent disconnects."""

from __future__ import annotations

import asyncio
import logging
import time

import pytest

from sam_link_sdk import MeshAgent, Usage
from sam_link_sdk.events import ModelFinished
from sam_link_sdk.link import LinkError, LinkTransport, transport as link_transport

from .test_link import (
    LIMITS,
    TOKEN,
    LinkServer,
    agent_config,
    entrypoint,
    events,
    eventually,
    link_config,
    private_state_dir,  # noqa: F401  (autouse: SAM's stop is saved under a private HOME)
    results,
    started,
    task,
    types,
)
from .test_link_protocol import MODEL, usage_sum

UNKNOWN_TASK = {"type": "error", "code": "unknown_task", "ref": "T1", "message": "not an open task of this agent"}
DESCRIPTION_REFUSED = {
    "type": "error",
    "code": "limit",
    "ref": "",
    "frame": "describe",
    "message": "the agent's tools have named more than 64 distinct scopes",
}


@pytest.fixture
async def server():
    s = await entrypoint().start()
    yield s
    await s.stop()


class Run:
    """A handler that reports a step, waits to be let go on, reports another and answers; it records how it ended."""

    def __init__(self):
        self.started = asyncio.Event()
        self.go_on = asyncio.Event()
        self.cancelled = asyncio.Event()
        self.runs = 0

    async def __call__(self, ctx):
        self.runs += 1
        self.started.set()
        try:
            await self.go_on.wait()
            await ctx.progress("after the reconnect")
        except asyncio.CancelledError:
            self.cancelled.set()
            raise
        return "done"


async def running_across_a_reconnect(server: LinkServer, handler, run: Run, **agent) -> MeshAgent:
    """An agent whose run of T1 started on the first connection and is still going on the second, where SAM has
    not delivered T1 again."""
    agent = await started(server, handler, **agent)
    await server.send(task(1, "T1"))
    await run.started.wait()
    await server.conns[0].close(code=1012)
    await eventually(lambda: server.of("consume", conn=1))
    return agent


def answering_with_usage(run: Run):
    """A handler that, once let go on, reports a model call's usage and answers."""

    async def handler(ctx):
        run.runs += 1
        run.started.set()
        await run.go_on.wait()
        ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
        return "done"

    return handler


async def answered_while_the_link_was_down(server: LinkServer, run: Run, **agent) -> MeshAgent:
    """An agent whose run of T1 answered while the link was down for longer than a frame waits for it: the answer
    is held, and the link is still down."""
    agent = await started(server, answering_with_usage(run), link={"connect_timeout_seconds": 0.2}, **agent)
    await server.send(task(1, "T1"))
    await run.started.wait()
    server.refusing = True
    await server.conns[0].close(code=1011)
    run.go_on.set()
    await eventually(lambda: "T1" in agent._transport._unsent)
    return agent


def lost_usage(caplog) -> list[str]:
    return [r.getMessage() for r in caplog.records if "model usage lost" in r.getMessage()]


async def test_a_run_whose_task_sam_no_longer_holds_goes_on_and_answers_once_its_task_is_delivered_again(
    server, caplog
):
    run = Run()
    agent = await running_across_a_reconnect(server, run, run)
    with caplog.at_level(logging.DEBUG, logger="sam_link_sdk.link"):
        await server.send(UNKNOWN_TASK)
        await eventually(lambda: [r for r in caplog.records if "waiting for its redelivery" in r.getMessage()])
        assert not agent._runner.tasks["T1"].task.done(), "the refusal stopped a run that was still going"
        run.go_on.set()
        await asyncio.sleep(0.2)
    assert not run.cancelled.is_set()
    assert types(server, 1) == ["describe", "consume"], "the run sent a frame SAM would refuse"
    assert not [r for r in caplog.records if "stopping" in r.getMessage()]

    await server.send(task(1, "T1", redelivered=True))
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed" and result["text"] == "done"
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
    assert run.runs == 1 and len(results(server, "T1")) == 1
    await agent.stop()


def asking_the_user_and_a_peer(server: LinkServer, run: Run, got: dict):
    """A handler that, once let go on, asks the user and a peer at once; the server answers both."""

    async def handler(ctx):
        run.started.set()
        await run.go_on.wait()
        approval, peer = await asyncio.gather(
            ctx.request_approval("pay_claim", {"claim": "C-3"}), ctx.call_agent("policy-desk", "Status?")
        )
        got.update(approval=approval, peer=peer)
        return "asked"

    answer_prompts_and_peer_calls(server)
    return handler


def answer_prompts_and_peer_calls(server: LinkServer) -> None:
    server.responders["prompt"] = lambda f: [
        {"type": "prompt_answer", "taskId": "T1", "requestId": f["requestId"], "status": "answered", "approved": True}
    ]
    server.responders["peer_call"] = lambda f: [
        {"type": "peer_result", "subtaskId": f["subtaskId"], "state": "completed", "text": "active"}
    ]


def but_steps(sent: list[str]) -> list[str]:
    return [kind for kind in sent if kind != "event"]


async def asked_both_once_delivered_again(server: LinkServer, got: dict) -> None:
    assert types(server, 1) == ["describe", "consume"], "a prompt or a peer call went out before the redelivery"
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: results(server, "T1"))
    assert got["approval"].approved and got["peer"].ok and got["peer"].text == "active"
    assert sorted(t for t in types(server, 1) if t in ("prompt", "peer_call")) == ["peer_call", "prompt"]


async def test_a_run_waiting_for_its_redelivery_holds_its_prompt_and_its_peer_call_and_sends_them_after_it(server):
    run, got = Run(), {}
    agent = await running_across_a_reconnect(server, asking_the_user_and_a_peer(server, run, got), run)
    run.go_on.set()
    await asyncio.sleep(0.2)
    await asked_both_once_delivered_again(server, got)
    await agent.stop()


async def test_a_prompt_and_a_peer_call_made_while_the_link_is_down_wait_for_the_redelivery_too(server):
    run, got = Run(), {}
    agent = await started(server, asking_the_user_and_a_peer(server, run, got))
    await server.send(task(1, "T1"))
    await run.started.wait()
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    run.go_on.set()
    await eventually(lambda: agent._transport._asks and agent._transport._calls)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    await asyncio.sleep(0.2)
    await asked_both_once_delivered_again(server, got)
    await agent.stop()


async def test_a_prompt_of_a_run_that_ended_while_it_waited_for_its_redelivery_is_not_sent(server):
    async def handler(ctx):
        run.started.set()
        await run.go_on.wait()
        run.asking = asyncio.ensure_future(ctx.request_approval("pay_claim", {"claim": "C-3"}))
        await run.answer.wait()
        return "done"

    run = Run()
    run.answer = asyncio.Event()
    agent = await running_across_a_reconnect(server, handler, run)
    run.go_on.set()
    await eventually(lambda: agent._transport._asks)
    run.answer.set()
    await eventually(lambda: agent._transport._tasks["T1"].closed)

    await server.send(task(1, "T1", redelivered=True))
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed"
    assert not (await asyncio.wait_for(run.asking, 5)).approved
    assert "prompt" not in types(server, 1)
    await agent.stop()


def sam_no_longer_holds_the_task(server: LinkServer) -> None:
    """SAM refuses every frame that names T1, as it does for a task it closed or never held."""
    server.responders["event"] = server.responders["result"] = lambda f: [UNKNOWN_TASK]


def said(caplog, text: str) -> list[logging.LogRecord]:
    return [r for r in caplog.records if text in r.getMessage()]


@pytest.mark.parametrize("answers", ["after the wait ran out", "while it waits"])
async def test_a_run_whose_task_sam_holds_but_does_not_deliver_again_answers_once_the_wait_ran_out(
    server, monkeypatch, caplog, answers
):
    """A Solace broker's redelivery limit is spent, or SAM runs on the TCP dev broker, which delivers no request
    again: SAM still holds the task and takes the run's frames. (On the in-process dev broker SAM fails the task once
    the limit is spent, and refuses them.) So when the wait runs out the run sends on the new connection, and the
    task gets its one answer with its usage. A delivery that comes after all gets the result again; the task is not
    run again."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    run = Run()

    async def handler(ctx):
        run.runs += 1
        run.started.set()
        await run.go_on.wait()
        ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
        await ctx.emit_event(ModelFinished(MODEL, "It is open."))
        return "done"

    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        agent = await running_across_a_reconnect(server, handler, run)
        if answers == "while it waits":
            run.go_on.set()
            await eventually(lambda: agent._transport._tasks["T1"].closed)
        assert types(server, 1) == ["describe", "consume"], "a frame went out before the wait ran out"
        await eventually(lambda: said(caplog, "its run sends on this connection as it is task=T1 seconds=0.5"))
        run.go_on.set()
        [result] = await eventually(lambda: results(server, "T1"))
        await eventually(lambda: agent._transport._tasks == {})
    assert result["text"] == "done" and usage_sum(server, "T1") == (1, 40, 6) and lost_usage(caplog) == []

    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
    first, again = results(server, "T1")
    assert again == first and run.runs == 1 and usage_sum(server, "T1") == (1, 40, 6)
    await agent.stop()


async def test_a_run_whose_task_sam_no_longer_holds_is_stopped_by_the_refusal_of_its_first_frame_after_the_wait(
    server, monkeypatch, caplog
):
    """SAM closed the task, or gave it to another instance of the agent. Nothing the run sends reaches anyone, so
    it stops unanswered at the first refusal, and its usage, none of which SAM took, is reported lost once. A
    delivery that comes later runs the task afresh."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    sam_no_longer_holds_the_task(server)
    run = Run()

    async def handler(ctx):
        run.runs += 1
        if run.runs == 2:
            return "run afresh"
        ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
        run.started.set()
        try:
            await run.go_on.wait()
            ctx.record_usage(MODEL, Usage(input_tokens=10, output_tokens=2))
            await ctx.emit_event(ModelFinished(MODEL, "It is open."))
            await asyncio.sleep(30)
        except asyncio.CancelledError:
            run.cancelled.set()
            raise

    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        agent = await running_across_a_reconnect(server, handler, run)
        await eventually(lambda: said(caplog, "its run sends on this connection as it is"))
        run.go_on.set()
        await asyncio.wait_for(run.cancelled.wait(), 5)
        await eventually(lambda: agent._runner.tasks == {} and agent._transport._tasks == {})
        await asyncio.sleep(0.2)
    assert lost_usage(caplog) == ["model usage lost: not sent to SAM task=T1 calls=2 input=50 output=8"]
    assert results(server, "T1") == [] and usage_sum(server, "T1") == (0, 0, 0) and server.of("ack") == []

    server.responders.clear()
    await server.send(task(1, "T1", redelivered=True))
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["text"] == "run afresh" and run.runs == 2
    await agent.stop()


@pytest.mark.parametrize("first_frame", ["a prompt", "a peer call"])
async def test_a_run_whose_first_frame_after_the_wait_is_a_prompt_or_a_peer_call_sam_refuses_is_stopped_too(
    server, monkeypatch, caplog, first_frame
):
    """SAM closed the task, or another instance of the agent runs it by now. The run must not go on as if its user
    had declined or its peer had failed: it stops, as it does when a step of it is refused."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    server.responders["prompt"] = lambda f: [{**UNKNOWN_TASK, "requestId": f["requestId"]}]
    server.responders["peer_call"] = lambda f: [{**UNKNOWN_TASK, "ref": f["subtaskId"]}]
    run = Run()

    async def handler(ctx):
        ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
        run.started.set()
        try:
            await run.go_on.wait()
            if first_frame == "a prompt":
                await ctx.request_approval("pay_claim", {"claim": "C-3"})
            else:
                await ctx.call_agent("policy-desk", "Status?")
            await asyncio.sleep(30)
        finally:
            run.cancelled.set()

    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        agent = await running_across_a_reconnect(server, handler, run)
        await eventually(lambda: said(caplog, "its run sends on this connection as it is"))
        run.go_on.set()
        await asyncio.wait_for(run.cancelled.wait(), 5)
        await eventually(lambda: agent._runner.tasks == {} and agent._transport._tasks == {})
    assert lost_usage(caplog) == ["model usage lost: not sent to SAM task=T1 calls=1 input=40 output=6"]
    assert results(server, "T1") == [] and len(server.of("prompt", conn=1) + server.of("peer_call", conn=1)) == 1
    assert said(caplog, "or holds it for another connection of this agent; stopping it task=T1")
    await agent.stop()


@pytest.mark.parametrize("held_since", ["its run ended during the wait", "the link was down"])
async def test_an_answer_sam_refuses_once_the_wait_ran_out_has_its_usage_reported_lost_once(
    server, monkeypatch, caplog, held_since
):
    """The answer goes out when the wait runs out, and SAM, which no longer holds the task, refuses its usage and
    its result. If SAM delivers the task later after all, it gets the result, which is still held, and the task is
    not run again."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    sam_no_longer_holds_the_task(server)
    run = Run()
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        if held_since == "the link was down":
            agent = await answered_while_the_link_was_down(server, run)
            server.refusing = False
        else:
            agent = await running_across_a_reconnect(server, answering_with_usage(run), run)
            run.go_on.set()
        await eventually(lambda: results(server, "T1") and lost_usage(caplog))
        await asyncio.sleep(0.2)
    assert lost_usage(caplog) == ["model usage lost: not sent to SAM task=T1 calls=1 input=40 output=6"]
    assert said(caplog, "the result was not delivered: SAM no longer holds the task")
    assert len(results(server, "T1")) == 1 and agent._transport._tasks == {}

    server.responders.clear()
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
    first, again = results(server, "T1")
    assert again == first and run.runs == 1
    await agent.stop()


async def test_an_answer_sam_refuses_part_way_has_all_of_its_usage_reported_lost(server, monkeypatch, caplog):
    """The refusal of the first usage frame arrives while the second is on its way. SAM took neither, so both are
    lost, and the result is not sent after them."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    run = Run()

    async def handler(ctx):
        run.started.set()
        await run.go_on.wait()
        ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
        ctx.record_usage(MODEL, Usage(input_tokens=10, output_tokens=2))
        return "done"

    agent = await running_across_a_reconnect(server, handler, run)
    connection = agent._transport._connection
    with_usage, send = [], connection.send

    async def refused_under_the_second_usage(frame, *, only_on=None):
        if frame["type"] == "event" and "usage" in frame["event"]:
            with_usage.append(frame)
            if len(with_usage) == 2:
                await server.send(UNKNOWN_TASK)
                await eventually(lambda: agent._transport._tasks["T1"].unknown_to_sam)
        return await send(frame, only_on=only_on)

    connection.send = refused_under_the_second_usage
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        run.go_on.set()
        await eventually(lambda: agent._runner.tasks == {} and agent._transport._tasks == {})
    assert lost_usage(caplog) == ["model usage lost: not sent to SAM task=T1 calls=2 input=50 output=8"]
    assert results(server, "T1") == [] and "T1" not in agent._transport._answered
    await agent.stop()


async def test_a_task_delivered_again_after_its_wait_ran_out_reaches_the_run_that_is_still_going(
    server, monkeypatch, caplog
):
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    run = Run()
    with caplog.at_level(logging.INFO, logger="sam_link_sdk.link"):
        agent = await running_across_a_reconnect(server, run, run)
        await eventually(lambda: said(caplog, "its run sends on this connection as it is"))
        await server.send(task(1, "T1", redelivered=True))
        await eventually(lambda: len(agent._transport._tasks["T1"].deliveries) == 2)
    assert said(caplog, "task delivered again after the reconnect; its run goes on task=T1")
    assert not said(caplog, "duplicate delivery"), "an expected delivery was logged as an unexpected copy"

    run.go_on.set()
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
    assert [r["text"] for r in results(server, "T1")] == ["done"] and run.runs == 1
    await agent.stop()


async def test_a_peer_call_of_a_run_that_ended_while_it_waited_for_its_redelivery_is_not_sent(server):
    run = Run()
    run.answer = asyncio.Event()

    async def handler(ctx):
        run.started.set()
        await run.go_on.wait()
        run.calling = asyncio.ensure_future(ctx.call_agent("policy-desk", "Status?"))
        await run.answer.wait()
        return "done"

    agent = await running_across_a_reconnect(server, handler, run)
    run.go_on.set()
    await eventually(lambda: agent._transport._calls)
    run.answer.set()
    await eventually(lambda: agent._transport._tasks["T1"].closed)
    await server.send(task(1, "T1", redelivered=True))
    answer = await asyncio.wait_for(run.calling, 5)
    assert not answer.ok and answer.text == "task T1 is not running"
    assert "peer_call" not in types(server, 1) and agent._transport._calls == {}
    await agent.stop()


def working_until_stopped(run: Run):
    """A handler that reports a step now and then until it is stopped; a second run of it answers at once."""

    async def handler(ctx):
        run.runs += 1
        run.started.set()
        if run.runs > 1:
            return "done"
        try:
            while True:
                await ctx.progress("working")
                await asyncio.sleep(0.05)
        except asyncio.CancelledError:
            run.cancelled.set()
            raise

    return handler


async def refused_and_not_yet_stopped(server: LinkServer, run: Run, monkeypatch) -> tuple[MeshAgent, asyncio.Event]:
    """An agent whose run of T1 sent a step on the second connection once its wait ran out, which SAM refused as
    naming no task; the run is stopped only once the returned event is set."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.2)
    agent = await started(server, working_until_stopped(run))
    stopping, stop = asyncio.Event(), asyncio.Event()
    cancel_the_run = agent._runner.on_cancel

    async def cancel_once_let(task_id):
        stopping.set()
        await stop.wait()
        await cancel_the_run(task_id)

    monkeypatch.setattr(agent._runner, "on_cancel", cancel_once_let)
    await server.send(task(1, "T1"))
    await run.started.wait()
    await server.conns[0].close(code=1012)
    await eventually(lambda: server.of("consume", conn=1))
    sam_no_longer_holds_the_task(server)
    await asyncio.wait_for(stopping.wait(), 5)
    return agent, stop


async def test_a_run_sam_refused_does_not_wait_again_when_the_link_reconnects_before_it_has_stopped(
    server, monkeypatch
):
    run = Run()
    agent, stop = await refused_and_not_yet_stopped(server, run, monkeypatch)
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 30.0)
    await server.conns[1].close(code=1012)
    await eventually(lambda: server.of("consume", conn=2))
    stop.set()
    await eventually(lambda: agent._transport._tasks == {})
    assert results(server, "T1") == []
    await agent.stop()


async def test_the_wait_of_a_run_given_back_does_not_stop_the_run_of_its_next_delivery(server, monkeypatch):
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 1.0)
    runs, stopped, go_on = [], [], asyncio.Event()

    async def handler(ctx):
        runs.append(len(runs) + 1)
        number = runs[-1]
        try:
            await go_on.wait()
        except asyncio.CancelledError:
            stopped.append(number)
            raise
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: runs)
    await server.conns[0].close(code=1012)
    await eventually(lambda: server.of("consume", conn=1))
    wait = agent._transport._tasks["T1"].redelivery_wait
    agent._runner.tasks["T1"].task.cancel()
    await eventually(lambda: agent._transport._tasks == {})
    assert wait.cancelled(), "the wait of a run given back is still running"
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: runs == [1, 2])
    await asyncio.sleep(1.3)
    assert stopped == [1]
    go_on.set()
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed"
    await agent.stop()


async def test_a_delivery_that_comes_while_a_run_sam_refused_is_stopping_is_given_back_and_the_next_one_runs_afresh(
    server, monkeypatch
):
    run = Run()
    agent, stop = await refused_and_not_yet_stopped(server, run, monkeypatch)
    server.responders.clear()
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: len(agent._transport._tasks["T1"].deliveries) == 2)
    stop.set()
    await eventually(lambda: server.of("nack", conn=1) == [{"type": "nack", "delivery": 1}])
    assert results(server, "T1") == [] and server.of("ack") == []

    await server.send(task(2, "T1", redelivered=True))
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed" and run.runs == 2
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 2}])
    await agent.stop()


async def test_a_redelivery_ends_the_wait_of_its_run(server):
    run = Run()
    agent = await running_across_a_reconnect(server, run, run)
    wait = agent._transport._tasks["T1"].redelivery_wait
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: len(agent._transport._tasks["T1"].deliveries) == 2)
    assert wait.cancelled() and agent._transport._tasks["T1"].redelivery_wait is None
    run.go_on.set()
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed"
    await agent.stop()


async def test_the_wait_for_a_redelivery_ends_when_the_link_drops_and_starts_over_at_each_reconnect(server):
    run = Run()
    agent = await started(server, run)
    await server.send(task(1, "T1"))
    await run.started.wait()
    server.refusing = True
    await server.conns[0].close(code=1012)
    await eventually(lambda: server.conns[0].state.name == "CLOSED")
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    first_wait = agent._transport._tasks["T1"].redelivery_wait

    server.refusing = True
    await server.conns[1].close(code=1012)
    await eventually(lambda: not agent._transport._connection.connected)
    assert first_wait.cancelled() and agent._transport._tasks["T1"].redelivery_wait is None
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=2))
    assert agent._transport._tasks["T1"].redelivery_wait is not None

    await server.send(task(1, "T1", redelivered=True))
    run.go_on.set()
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed" and run.runs == 1
    await agent.stop()


async def test_a_cancel_for_a_run_waiting_for_its_redelivery_cancels_it_and_is_answered_once_it_is_delivered(server):
    run = Run()
    agent = await running_across_a_reconnect(server, run, run)
    await server.send({"type": "cancel", "taskId": "T1"})
    await asyncio.wait_for(run.cancelled.wait(), 5)
    await asyncio.sleep(0.2)
    assert results(server, "T1") == []

    await server.send(task(1, "T1", redelivered=True))
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "canceled"
    await agent.stop()


async def test_a_step_of_a_run_waiting_for_its_redelivery_is_dropped_and_its_later_steps_are_sent(server):
    async def handler(ctx):
        run.started.set()
        await run.go_on.wait()
        await ctx.progress("while SAM does not hold the task")
        await run.delivered_again.wait()
        await ctx.progress("once SAM holds it again")
        return "done"

    run = Run()
    run.delivered_again = asyncio.Event()
    agent = await running_across_a_reconnect(server, handler, run)
    run.go_on.set()
    await asyncio.sleep(0.2)
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: len(agent._transport._tasks["T1"].deliveries) == 2)
    run.delivered_again.set()
    await eventually(lambda: results(server, "T1"))
    assert [e["text"] for e in events(server, "T1", "progress")] == ["once SAM holds it again"]
    await agent.stop()


async def test_a_refusal_for_a_task_delivered_on_the_current_connection_still_stops_its_run(server):
    run = Run()
    agent = await started(server, run)
    await server.send(task(1, "T1"))
    await run.started.wait()
    await server.send(UNKNOWN_TASK)
    await asyncio.wait_for(run.cancelled.wait(), 5)
    await eventually(lambda: agent._runner.tasks == {})
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "canceled", "SAM delivered the task here, so the run answers as for a cancel"
    await eventually(lambda: server.of("ack") == [{"type": "ack", "delivery": 1}])
    await agent.stop()


def sam_binding_tasks_to_connections(server: LinkServer) -> tuple[dict[str, int], list[tuple[int, dict]]]:
    """Make the server take a task's frames as SAM does: only from the connection the task is bound to, or from
    another one once that connection is gone, which then holds the task. Any other is refused ``unknown_task``. An
    accepted prompt is approved. Returns the bindings (task id -> connection number) and the frames taken."""
    bound: dict[str, int] = {}
    taken: list[tuple[int, dict]] = []

    def take_or_refuse(frame: dict) -> list[dict]:
        sender = server.frames[-1][0]
        task_id = frame.get("taskId") or frame["parentTaskId"]
        holder = bound.get(task_id)
        if holder is not None and holder != sender and server.conns[holder].state.name == "OPEN":
            refusal = {**UNKNOWN_TASK, "ref": frame.get("subtaskId", task_id)}
            return [{**refusal, "requestId": frame["requestId"]} if frame["type"] == "prompt" else refusal]
        bound[task_id] = sender
        taken.append((sender, frame))
        if frame["type"] == "prompt":
            answered = {"type": "prompt_answer", "taskId": task_id, "requestId": frame["requestId"]}
            return [{**answered, "status": "answered", "approved": True}]
        return []

    for kind in ("event", "prompt", "result", "peer_call"):
        server.responders[kind] = take_or_refuse
    return bound, taken


async def test_a_released_run_whose_task_a_second_instance_of_the_agent_runs_is_refused_and_stops(
    server, monkeypatch, caplog
):
    """Two instances of one agent. The first runs T1 when the link drops, and SAM delivers T1 again to the second.
    When the first one's wait runs out its run sends on its new connection, and SAM, which holds the task for the
    second instance's connection, refuses it: the user is asked once, the task gets one answer and its usage once,
    and the first run stops with its own usage reported lost."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    bound, taken = sam_binding_tasks_to_connections(server)
    runs = {"first": Run(), "second": Run()}

    def asking_before_it_answers(run: Run):
        async def handler(ctx):
            run.started.set()
            try:
                await run.go_on.wait()
                ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
                await ctx.request_approval("pay_claim", {"claim": "C-3"})
                return "paid"
            finally:
                run.cancelled.set()

        return handler

    first = await started(server, asking_before_it_answers(runs["first"]))
    second = await started(server, asking_before_it_answers(runs["second"]))
    await eventually(lambda: len(server.of("consume")) == 2)
    bound["T1"] = 0
    await server.send(task(1, "T1"), conn=0)
    await runs["first"].started.wait()
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        await server.conns[0].close(code=1012)
        await eventually(lambda: server.of("consume", conn=2))
        bound["T1"] = 1
        await server.send(task(1, "T1", redelivered=True), conn=1)
        await runs["second"].started.wait()
        await eventually(lambda: said(caplog, "its run sends on this connection as it is task=T1"))
        runs["first"].go_on.set()
        await asyncio.wait_for(runs["first"].cancelled.wait(), 5)
        await eventually(lambda: first._runner.tasks == {} and first._transport._tasks == {})
        runs["second"].go_on.set()
        await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
        await asyncio.sleep(0.2)
    assert [(sender, frame["type"]) for sender, frame in taken if frame["type"] in ("prompt", "result")] == [
        (1, "prompt"),
        (1, "result"),
    ]
    assert [frame["event"]["usage"]["input"] for _, frame in taken if "usage" in frame.get("event", {})] == [40]
    assert len(server.of("prompt", conn=2)) == 1 and server.of("result", conn=2) == []
    assert lost_usage(caplog) == ["model usage lost: not sent to SAM task=T1 calls=1 input=40 output=6"]
    await first.stop()
    await second.stop()


def test_a_run_waits_ninety_seconds_for_its_redelivery():
    """docs/link-protocol.md, docs/operations.md and the CHANGELOG state this number. It has to stay above the 60 s
    Agent Mesh takes to notice a connection that died without a close, since only then does it deliver again what it
    had delivered on that connection."""
    assert link_transport.REDELIVERY_WAIT_SECONDS == 90.0


async def test_an_answer_that_waits_when_the_link_drops_again_and_stays_down_holds_up_no_stop(server, caplog):
    """The link reconnects, the run answers and waits for its task, and the link drops for good before SAM delivers
    it: the answer then waits for the link as any frame does, and is held."""
    run = Run()
    agent = await running_across_a_reconnect(server, run, run, link={"connect_timeout_seconds": 0.3})
    run.go_on.set()
    await eventually(lambda: agent._transport._tasks["T1"].closed)
    server.refusing = True
    await server.conns[1].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    stopping_at = time.monotonic()
    with caplog.at_level(logging.ERROR, logger="sam_link_sdk.link"):
        await agent.stop()
    assert time.monotonic() - stopping_at < 3
    assert agent._settling.others() == [] and agent._runner.tasks == {} and agent._transport._tasks == {}
    assert [r for r in caplog.records if "result not sent: the agent link is closed task=T1" in r.getMessage()]


async def test_a_prompt_and_a_peer_call_that_wait_end_when_the_link_drops_again_and_stays_down(server):
    run, got = Run(), {}
    handler = asking_the_user_and_a_peer(server, run, got)
    agent = await running_across_a_reconnect(server, handler, run, link={"connect_timeout_seconds": 0.3})
    run.go_on.set()
    await eventually(lambda: agent._transport._asks and agent._transport._calls)
    server.refusing = True
    await server.conns[1].close(code=1011)
    await eventually(lambda: "peer" in got)
    assert not got["approval"].approved and not got["peer"].ok and "not connected" in got["peer"].text
    await eventually(lambda: "T1" in agent._transport._unsent)
    assert "prompt" not in types(server, 1) and "peer_call" not in types(server, 1)
    await agent.stop()


async def test_a_wake_whose_link_drops_after_a_reconnect_and_stays_down_ends(server):
    """A serverless invocation must not stay up for a task SAM can no longer deliver to it."""
    run = Run()
    server.responders["consume"] = lambda f: [task(1, "T1")] if len(server.conns) == 1 else []
    woken = MeshAgent(agent_config(), run, transport=LinkTransport(link_config(server, connect_timeout_seconds=0.3)))
    waking = asyncio.ensure_future(woken.handle_wake(idle_seconds=0.5))
    await run.started.wait()
    await server.conns[0].close(code=1012)
    await eventually(lambda: server.of("consume", conn=1))
    run.go_on.set()
    await eventually(lambda: woken._transport._tasks["T1"].closed)
    server.refusing = True
    await server.conns[1].close(code=1011)
    dropped_at = time.monotonic()
    assert await asyncio.wait_for(waking, 5) == 1
    assert time.monotonic() - dropped_at < 3


def sam_holding_the_task_until_it_gives_it_back(server: LinkServer) -> tuple[list[str], asyncio.Event]:
    """SAM refuses every frame that names T1 until it has delivered T1 again, as it does for a task it gave back to
    its broker. Returns the kinds of the frames it took and the event that makes it take them."""
    delivered, taken = asyncio.Event(), []

    def take_once_delivered(frame: dict) -> list[dict]:
        if not delivered.is_set():
            return [UNKNOWN_TASK]
        taken.append(frame["type"])
        return []

    server.responders["event"] = server.responders["result"] = take_once_delivered
    return taken, delivered


def answering_after_a_step(run: Run):
    async def handler(ctx):
        run.started.set()
        await run.go_on.wait()
        await ctx.progress("after the reconnect")
        return "done"

    return handler


@pytest.mark.parametrize("handler_of", [answering_after_a_step, answering_with_usage], ids=["step", "answer-only"])
async def test_a_stop_leaves_the_grace_for_sam_to_deliver_the_task_again_before_the_answer_goes_out(server, handler_of):
    """``retire`` takes the card down and SAM goes on delivering on the connection, so a task SAM gave back and delivers
    again a moment after the run finished is taken: the answer is not sent first, to be refused."""
    taken, delivered = sam_holding_the_task_until_it_gives_it_back(server)
    run = Run()
    agent = await running_across_a_reconnect(server, handler_of(run), run, shutdown_grace_seconds=3.0)
    stopping = asyncio.ensure_future(agent.stop())
    await eventually(lambda: server.of("retire", conn=1))
    run.go_on.set()
    await eventually(lambda: agent._transport._tasks["T1"].closed)
    await asyncio.sleep(0.1)
    assert results(server, "T1") == [] and "event" not in types(server, 1)
    delivered.set()
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: [f["delivery"] for f in server.of("ack", conn=1)] == [1])
    await asyncio.wait_for(stopping, 2)
    assert taken[-1] == "result" and [r["state"] for r in results(server, "T1")] == ["completed"]


async def test_a_stop_sends_the_answer_that_waits_for_its_redelivery_as_it_is_at_the_end_of_the_grace(
    server, monkeypatch, caplog
):
    monkeypatch.setattr("sam_link_sdk.tasks.STOPPED_ANSWER_SECONDS", 0.2)
    run = Run()
    agent = await running_across_a_reconnect(server, answering_with_usage(run), run, shutdown_grace_seconds=0.5)
    run.go_on.set()
    await eventually(lambda: agent._transport._tasks["T1"].closed)
    wait = agent._transport._tasks["T1"].redelivery_wait
    began = time.monotonic()
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk"):
        stopping = asyncio.ensure_future(agent.stop())
        await asyncio.sleep(0.2)
        assert results(server, "T1") == [], "the answer waits for SAM's delivery for the grace"
        await asyncio.wait_for(stopping, 3)
    assert 0.45 < time.monotonic() - began < 2
    assert wait.cancelled()
    assert [r["state"] for r in results(server, "T1")] == ["completed"] and "result" in types(server, 1)
    assert usage_sum(server, "T1") == (1, 40, 6)
    assert not [r for r in caplog.records if r.levelno >= logging.ERROR]


async def test_an_answer_made_while_the_link_is_down_goes_out_once_with_its_usage_after_its_redelivery(server, caplog):
    """The answer was already on its way when the link came back, so it has to be held back on the new connection
    too: nothing for the task before SAM delivers it there, then its usage and its one result."""
    run = Run()
    agent = await started(server, answering_with_usage(run))
    await server.send(task(1, "T1"))
    await run.started.wait()
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    run.go_on.set()
    await eventually(lambda: agent._transport._tasks["T1"].closed)
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        server.refusing = False
        await eventually(lambda: server.of("consume", conn=1))
        await asyncio.sleep(0.2)
        assert types(server, 1) == ["describe", "consume"], "part of the answer went out before the redelivery"
        await server.send(task(1, "T1", redelivered=True))
        await eventually(lambda: server.of("ack", conn=1))
        await asyncio.sleep(0.2)
    assert types(server, 1) == ["describe", "consume", "event", "result", "ack"]
    assert usage_sum(server, "T1") == (1, 40, 6) and results(server, "T1")[0]["text"] == "done"
    assert not [r for r in caplog.records if "not delivered" in r.getMessage()] and lost_usage(caplog) == []
    await agent.stop()


async def test_usage_that_went_out_before_the_link_failed_under_the_result_is_not_sent_again(server):
    """SAM recorded the spend when it took the usage, so what waits for the task's next delivery is the result
    alone."""
    run = Run()
    agent = await started(server, answering_with_usage(run))
    connection = agent._transport._connection
    failures, send = [LinkError("the agent link dropped")], connection.send

    async def dropping_under_the_result(frame, *, only_on=None):
        if frame["type"] == "result" and failures:
            await server.conns[0].close(code=1011)
            raise failures.pop()
        return await send(frame, only_on=only_on)

    connection.send = dropping_under_the_result
    await server.send(task(1, "T1"))
    run.go_on.set()
    await eventually(lambda: server.of("consume", conn=1))
    assert usage_sum(server, "T1") == (1, 40, 6) and results(server, "T1") == []

    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
    assert types(server, 1) == ["describe", "consume", "result", "ack"] and run.runs == 1
    assert usage_sum(server, "T1") == (1, 40, 6)
    await agent.stop()


async def answer_waiting_on_a_connection_replaced_under_its_second_usage(server: LinkServer, run: Run, **agent):
    """Two model calls' usage goes with the answer. The first reaches SAM; the link is replaced under the second,
    and the answer waits for its task on the new connection."""

    async def handler(ctx):
        run.started.set()
        await run.go_on.wait()
        ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
        ctx.record_usage(MODEL, Usage(input_tokens=10, output_tokens=2))
        return "done"

    agent = await started(server, handler, **agent)
    connection = agent._transport._connection
    with_usage, send = [], connection.send

    async def reconnecting_under_the_second_usage(frame, *, only_on=None):
        if frame["type"] == "event" and "usage" in frame["event"]:
            with_usage.append(frame)
            if len(with_usage) == 2:
                await server.conns[0].close(code=1012)
                await eventually(lambda: connection.connection_id == "c1")
        return await send(frame, only_on=only_on)

    connection.send = reconnecting_under_the_second_usage
    await server.send(task(1, "T1"))
    await run.started.wait()
    run.go_on.set()
    await eventually(lambda: server.of("consume", conn=1))
    await eventually(lambda: not agent._transport._tasks["T1"].delivered_here.is_set())
    return agent


async def test_only_the_usage_that_never_went_out_is_reported_lost_when_an_answer_is_given_up_part_way(
    server, monkeypatch, caplog
):
    monkeypatch.setattr("sam_link_sdk.tasks.STOPPED_ANSWER_SECONDS", 0.2)
    run = Run()
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        agent = await answer_waiting_on_a_connection_replaced_under_its_second_usage(server, run)
        [answering] = agent._settling.others()
        answering.cancel()
        await asyncio.gather(answering, return_exceptions=True)
        await eventually(lambda: agent._transport._tasks == {} and lost_usage(caplog))
    assert lost_usage(caplog) == ["model usage lost: not sent to SAM task=T1 calls=1 input=10 output=2"]
    assert usage_sum(server, "T1") == (1, 40, 6) and results(server, "T1") == []
    await agent.stop()


async def test_the_usage_of_an_answer_still_waiting_when_the_agent_stops_goes_out_with_it_and_is_not_lost(
    server, monkeypatch, caplog
):
    monkeypatch.setattr("sam_link_sdk.tasks.STOPPED_ANSWER_SECONDS", 0.5)
    run = Run()
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk"):
        agent = await answer_waiting_on_a_connection_replaced_under_its_second_usage(
            server, run, shutdown_grace_seconds=0.3
        )
        await agent.stop()
    assert lost_usage(caplog) == []
    assert usage_sum(server, "T1") == (2, 50, 8)
    assert [r["state"] for r in results(server, "T1")] == ["completed"]


async def test_an_answer_made_after_the_link_gave_up_is_reported_lost_with_its_usage(server, tmp_path, caplog):
    """SAM refused the agent's token until the link closed for good: nothing reconnects, so the answer cannot be
    held for a later connection."""
    token = tmp_path / "agent.link-token"
    token.write_text(TOKEN)
    run = Run()
    link = {"token": "", "token_file": str(token), "max_auth_refusals": 2}
    agent = await started(server, answering_with_usage(run), link=link)
    await server.send(task(1, "T1"))
    await run.started.wait()
    token.write_text("a-token-sam-refuses")
    await server.conns[0].close(code=1011)
    await eventually(lambda: agent._transport._connection.failure is not None)
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        run.go_on.set()
        await eventually(lambda: agent._runner.tasks == {} and agent._transport._tasks == {})
    assert [r for r in caplog.records if r.getMessage() == "result not sent: the agent link is closed task=T1"]
    assert lost_usage(caplog) == ["model usage lost: not sent to SAM task=T1 calls=1 input=40 output=6"]
    assert "T1" not in agent._transport._unsent
    await agent.stop()


@pytest.mark.parametrize(
    ("stuck_under", "lost"),
    [("event", ["model usage lost: not sent to SAM task=T1 calls=1 input=40 output=6"]), ("result", [])],
)
async def test_a_held_answer_cut_off_by_a_stop_while_it_goes_out_again_is_reported_lost(
    server, caplog, stuck_under, lost
):
    """What the stop cut off is said, and of its usage only what had not gone out by then."""
    run = Run()
    agent = await answered_while_the_link_was_down(server, run)
    connection = agent._transport._connection
    stuck, send = asyncio.Event(), connection.send

    async def stuck_on_its_way_out(frame, *, only_on=None):
        if frame["type"] == stuck_under:
            stuck.set()
            await asyncio.Event().wait()
        return await send(frame, only_on=only_on)

    connection.send = stuck_on_its_way_out
    server.responders["consume"] = lambda f: [task(1, "T1", redelivered=True)]
    server.refusing = False
    await asyncio.wait_for(stuck.wait(), 5)
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        await agent.stop()
    assert lost_usage(caplog) == lost and said(caplog, "answer not sent: its sending was cancelled task=T1")
    assert results(server, "T1") == [] and server.of("ack") == []


async def test_a_held_answer_whose_task_sam_holds_but_does_not_deliver_again_goes_out_once_the_wait_ran_out(
    server, monkeypatch, caplog
):
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    run = Run()
    agent = await answered_while_the_link_was_down(server, run)
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        server.refusing = False
        await eventually(lambda: server.of("consume", conn=1))
        assert types(server, 1) == ["describe", "consume"], "the answer went out before the wait ran out"
        await eventually(lambda: results(server, "T1"))
    assert types(server, 1) == ["describe", "consume", "event", "result"] and "T1" not in agent._transport._unsent
    assert usage_sum(server, "T1") == (1, 40, 6) and lost_usage(caplog) == []
    assert said(caplog, "sending their answers on this connection as they are answers=1 seconds=0.5")

    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
    first, again = results(server, "T1")
    assert again == first and run.runs == 1 and usage_sum(server, "T1") == (1, 40, 6)
    await agent.stop()
    assert len(agent._transport._sent_undelivered) == 0


async def test_a_held_answer_its_task_takes_while_another_goes_out_is_sent_once_and_the_rest_still_go_out(
    server, monkeypatch
):
    """Three answers are held. The wait runs out, the first is slow to go out, and SAM delivers the second one's
    task meanwhile: that delivery sends the answer, the loop that was about to must not send it again, and it must
    go on to the third."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    run = Run()
    agent = await started(server, answering_with_usage(run), link={"connect_timeout_seconds": 0.2})
    for delivery, task_id in enumerate(("T1", "T2", "T3"), start=1):
        await server.send(task(delivery, task_id))
    await eventually(lambda: run.runs == 3)
    server.refusing = True
    await server.conns[0].close(code=1011)
    run.go_on.set()
    await eventually(lambda: len(agent._transport._unsent) == 3)
    first, second, third = agent._transport._unsent
    connection = agent._transport._connection
    stuck, let_go, send = asyncio.Event(), asyncio.Event(), connection.send

    async def slow_under_the_first_answer(frame, *, only_on=None):
        if frame.get("taskId") == first and not let_go.is_set():
            stuck.set()
            await let_go.wait()
        return await send(frame, only_on=only_on)

    connection.send = slow_under_the_first_answer
    server.refusing = False
    await asyncio.wait_for(stuck.wait(), 5)
    await server.send(task(7, second, redelivered=True))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 7}])
    let_go.set()
    await eventually(lambda: results(server, first) and results(server, third))
    await asyncio.sleep(0.2)
    assert [len(results(server, answered)) for answered in (first, second, third)] == [1, 1, 1]
    assert [usage_sum(server, answered) for answered in (first, second, third)] == [(1, 40, 6)] * 3
    await agent.stop()


async def test_a_task_delivered_again_while_its_held_answer_goes_out_is_acknowledged_and_not_answered_twice(
    server, monkeypatch, caplog
):
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    run = Run()
    agent = await answered_while_the_link_was_down(server, run)
    connection = agent._transport._connection
    stuck, let_go, send = asyncio.Event(), asyncio.Event(), connection.send

    async def slow_under_the_result(frame, *, only_on=None):
        if frame["type"] == "result" and not let_go.is_set():
            stuck.set()
            await let_go.wait()
        return await send(frame, only_on=only_on)

    connection.send = slow_under_the_result
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        server.refusing = False
        await asyncio.wait_for(stuck.wait(), 5)
        await server.send(task(1, "T1", redelivered=True))
        await eventually(lambda: agent._transport._going_out["T1"])
        let_go.set()
        await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
        await asyncio.sleep(0.2)
    assert len(results(server, "T1")) == 1 and usage_sum(server, "T1") == (1, 40, 6) and lost_usage(caplog) == []
    assert agent._transport._going_out == {}
    await agent.stop()


async def a_delivery_during_a_held_answers_send(server: LinkServer, run: Run, monkeypatch):
    """An agent whose held answer of T1 is stuck on its way out, under its result, once its wait ran out, and whose
    task SAM delivered again meanwhile. Returns the agent and the event that lets the send go on, to fail."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    agent = await answered_while_the_link_was_down(server, run)
    connection = agent._transport._connection
    stuck, let_go, failures, send = (
        asyncio.Event(),
        asyncio.Event(),
        [LinkError("the agent link dropped")],
        connection.send,
    )

    async def failing_under_the_result(frame, *, only_on=None):
        if frame["type"] == "result" and failures:
            stuck.set()
            await let_go.wait()
            raise failures.pop()
        return await send(frame, only_on=only_on)

    connection.send = failing_under_the_result
    server.refusing = False
    await asyncio.wait_for(stuck.wait(), 5)
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: agent._transport._going_out["T1"])
    return agent, let_go


async def test_a_task_delivered_while_its_held_answer_fails_on_its_way_out_is_not_acknowledged(server, monkeypatch):
    """The answer did not go out, so acknowledging the delivery that came meanwhile would lose the task: SAM would
    never deliver it again. The answer is held again and goes out, once, with the task's next delivery."""
    run = Run()
    agent, let_go = await a_delivery_during_a_held_answers_send(server, run, monkeypatch)
    let_go.set()
    await eventually(lambda: "T1" in agent._transport._unsent)
    await asyncio.sleep(0.1)
    assert agent._transport._going_out == {} and server.of("ack") == [] and results(server, "T1") == []

    await server.send(task(2, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 2}])
    assert len(results(server, "T1")) == 1 and usage_sum(server, "T1") == (1, 40, 6) and run.runs == 1
    await agent.stop()


async def test_a_stop_while_a_held_answer_goes_out_leaves_no_task_marked_as_going_out(server, monkeypatch):
    """Left behind, the mark would swallow the task's next delivery: neither run nor acknowledged."""
    run = Run()
    agent, _ = await a_delivery_during_a_held_answers_send(server, run, monkeypatch)
    await agent.stop()
    assert agent._transport._going_out == {} and server.of("ack") == [] and results(server, "T1") == []


async def test_an_answer_held_while_the_link_is_down_starts_its_wait_at_the_reconnect(server, monkeypatch):
    """A wait begun on a link that is down would run out early on the next connection, before SAM, which can take
    60 s to notice a dead connection, has had the chance to deliver the task again."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 1.0)
    run = Run()
    agent = await answered_while_the_link_was_down(server, run)
    assert agent._transport._unsent_wait is None, "the wait runs while the link is down"
    await asyncio.sleep(0.7)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    reconnected_at = time.monotonic()
    await eventually(lambda: results(server, "T1"))
    assert time.monotonic() - reconnected_at > 0.8, "the answer went out before its wait on the new connection ran out"
    await agent.stop()


async def test_answers_held_one_after_the_other_on_a_connection_share_one_wait(server):
    agent = await started(server, lambda ctx: "done")
    connection = agent._transport._connection
    failures, send = [LinkError("the agent link dropped"), LinkError("the agent link dropped")], connection.send

    async def failing_under_two_results(frame, *, only_on=None):
        if frame["type"] == "result" and failures:
            raise failures.pop()
        return await send(frame, only_on=only_on)

    connection.send = failing_under_two_results
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._transport._unsent)
    wait = agent._transport._unsent_wait
    await server.send(task(2, "T2"))
    await eventually(lambda: "T2" in agent._transport._unsent)
    assert wait is not None and agent._transport._unsent_wait is wait
    await agent.stop()


async def test_an_answer_held_on_a_connection_that_stays_up_goes_out_when_its_wait_runs_out(server, monkeypatch):
    """The link failed under the result and went on as the same connection, so no reconnect starts the wait for the
    held answer: holding it does."""
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.5)
    run = Run()
    agent = await started(server, answering_with_usage(run))
    connection = agent._transport._connection
    failures, send = [LinkError("the agent link dropped")], connection.send

    async def failing_once_under_the_result(frame, *, only_on=None):
        if frame["type"] == "result" and failures:
            raise failures.pop()
        return await send(frame, only_on=only_on)

    connection.send = failing_once_under_the_result
    await server.send(task(1, "T1"))
    run.go_on.set()
    await eventually(lambda: "T1" in agent._transport._unsent)
    assert results(server, "T1") == []
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["text"] == "done" and usage_sum(server, "T1") == (1, 40, 6) and len(server.conns) == 1
    await agent.stop()


async def test_a_held_answer_that_went_out_is_not_sent_again_when_its_wait_runs_out(server, monkeypatch, caplog):
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 1.0)
    run = Run()
    agent = await answered_while_the_link_was_down(server, run)
    server.responders["consume"] = lambda f: [task(1, "T1", redelivered=True)]
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        server.refusing = False
        await eventually(lambda: server.of("ack", conn=1))
        await asyncio.sleep(1.3)
    assert len(results(server, "T1")) == 1 and usage_sum(server, "T1") == (1, 40, 6) and lost_usage(caplog) == []

    await server.send(task(2, "T1", redelivered=True))
    await eventually(lambda: [f["delivery"] for f in server.of("ack", conn=1)] == [1, 2])
    assert run.runs == 1 and len(results(server, "T1")) == 2, "the answered task was run again"
    await agent.stop()


async def test_the_wait_of_a_held_answer_ends_when_the_link_drops_and_starts_over_on_the_next_connection(server):
    """Left running, the wait would run out while the link is down and send the answer the moment the link is back,
    before SAM has delivered anything on the new connection."""
    run = Run()
    agent = await answered_while_the_link_was_down(server, run)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    wait = agent._transport._unsent_wait
    server.refusing = True
    await server.conns[1].close(code=1012)
    await eventually(lambda: not agent._transport._connection.connected)
    assert wait.cancelled() and "T1" in agent._transport._unsent
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=2))
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=2) == [{"type": "ack", "delivery": 1}])
    assert types(server, 2) == ["describe", "consume", "event", "result", "ack"] and run.runs == 1
    await agent.stop()


async def test_a_stop_leaves_no_wait_for_a_held_answer_running(server):
    run = Run()
    agent = await answered_while_the_link_was_down(server, run)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    wait = agent._transport._unsent_wait
    await agent.stop()
    assert wait.cancelled() and agent._transport._unsent_wait is None


async def test_a_stop_sends_an_answer_held_for_a_redelivery_as_it_is_at_the_end_of_the_grace(server, caplog):
    """Held across a reconnect and not delivered again, the answer is sent on the connection before the disconnect,
    where SAM takes it if it still holds the task, instead of being lost with 'result not sent'."""
    run = Run()
    agent = await answered_while_the_link_was_down(server, run, shutdown_grace_seconds=0.4)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    began = time.monotonic()
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk"):
        stopping = asyncio.ensure_future(agent.stop())
        await asyncio.sleep(0.15)
        assert results(server, "T1") == [], "the answer waits for SAM's delivery for the grace"
        await asyncio.wait_for(stopping, 3)
    assert 0.35 < time.monotonic() - began < 2
    assert [r["state"] for r in results(server, "T1")] == ["completed"] and "result" in types(server, 1)
    assert usage_sum(server, "T1") == (1, 40, 6)
    assert not [r for r in caplog.records if r.levelno >= logging.ERROR or "model usage lost" in r.getMessage()]


async def test_a_stop_takes_the_delivery_of_a_held_answers_task_that_comes_within_the_grace(server):
    taken, delivered = sam_holding_the_task_until_it_gives_it_back(server)
    run = Run()
    agent = await answered_while_the_link_was_down(server, run, shutdown_grace_seconds=3.0)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    stopping = asyncio.ensure_future(agent.stop())
    await eventually(lambda: server.of("retire", conn=1))
    await asyncio.sleep(0.1)
    assert results(server, "T1") == []
    delivered.set()
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
    await asyncio.wait_for(stopping, 2)
    assert taken[-1] == "result" and len(results(server, "T1")) == 1 and usage_sum(server, "T1") == (1, 40, 6)


async def test_a_task_delivered_again_after_it_was_answered_is_taken_while_the_agent_drains(server):
    """The result SAM refused as unknown, or never took, is held until its task is delivered again: that delivery is
    work the agent already took, so it is not dropped as a new task is while draining."""
    second_started, finish_second = asyncio.Event(), asyncio.Event()

    async def handler(ctx):
        if ctx.task_id == "T2":
            second_started.set()
            await finish_second.wait()
        return "done"

    agent = await started(server, handler, shutdown_grace_seconds=5)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    await eventually(lambda: server.of("ack") == [{"type": "ack", "delivery": 1}])
    await server.send(task(2, "T2"))
    await second_started.wait()
    stopping = asyncio.ensure_future(agent.stop())
    await eventually(lambda: server.of("retire", conn=0))
    await server.send(task(3, "T1", redelivered=True))
    await eventually(lambda: {"type": "ack", "delivery": 3} in server.of("ack"))
    assert len(results(server, "T1")) == 2, "the result held for it is sent again"
    finish_second.set()
    await asyncio.wait_for(stopping, 2)


@pytest.mark.parametrize("answers_while_the_link_is_down", [False, True])
async def test_a_run_still_going_when_the_agent_stops_answers_on_a_connection_that_takes_no_tasks(
    server, caplog, answers_while_the_link_is_down
):
    """A graceful stop lets a running task finish. The agent has retired by then, so a connection that opens
    meanwhile sends no consume and SAM delivers nothing again on it: the run must not wait for that, nor the log
    say it does."""
    run = Run()
    agent = await started(server, run, shutdown_grace_seconds=5)
    await server.send(task(1, "T1"))
    await run.started.wait()
    stopping = asyncio.ensure_future(agent.stop())
    await eventually(lambda: server.of("retire", conn=0))
    server.refusing = answers_while_the_link_is_down
    with caplog.at_level(logging.INFO, logger="sam_link_sdk.link"):
        await server.conns[0].close(code=1012)
        if answers_while_the_link_is_down:
            await eventually(lambda: not agent._transport._connection.connected)
            run.go_on.set()
            await eventually(lambda: agent._transport._tasks["T1"].closed)
            server.refusing = False
        else:
            await eventually(lambda: len(server.conns) == 2 and agent._transport._connection.connected)
            run.go_on.set()
        [result] = await eventually(lambda: results(server, "T1"))
        await asyncio.wait_for(stopping, 2)
    assert result["state"] == "completed" and but_steps(types(server, 1)) == ["result"]
    assert ("event" in types(server, 1)) is not answers_while_the_link_is_down
    assert not [r for r in caplog.records if "waiting for SAM to deliver again" in r.getMessage()]


async def test_what_sam_delivers_again_while_the_agent_stops_reaches_its_run_and_its_held_answer(server):
    """The agent took tasks on the new connection before it began to stop, so SAM delivers them again while it
    drains: the copy of a task still running is the run's, and the copy of an answered one gets the held answer."""
    second_started, finish_second = asyncio.Event(), asyncio.Event()
    run = Run()

    async def handler(ctx):
        if ctx.task_id == "T2":
            second_started.set()
            await finish_second.wait()
            return "second"
        run.started.set()
        await run.go_on.wait()
        return "first"

    agent = await started(server, handler, link={"connect_timeout_seconds": 0.2}, shutdown_grace_seconds=5)
    await server.send(task(1, "T1"))
    await server.send(task(2, "T2"))
    await run.started.wait()
    await second_started.wait()
    server.refusing = True
    await server.conns[0].close(code=1011)
    run.go_on.set()
    await eventually(lambda: "T1" in agent._transport._unsent)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))

    stopping = asyncio.ensure_future(agent.stop())
    await eventually(lambda: server.of("retire", conn=1))
    await server.send(task(1, "T1", redelivered=True))
    await server.send(task(2, "T2", redelivered=True))
    finish_second.set()
    await eventually(lambda: [f["delivery"] for f in server.of("ack", conn=1)] == [1, 2])
    await asyncio.wait_for(stopping, 2)
    assert [r["text"] for r in results(server, "T1") + results(server, "T2")] == ["first", "second"]


async def test_a_held_answer_goes_out_at_once_on_a_connection_that_opens_while_the_agent_stops(server):
    """The agent retired while the link was down, so the connection that opens takes no tasks and SAM delivers
    nothing again on it: the answer held for a redelivery would never go out."""
    second_started, finish_second = asyncio.Event(), asyncio.Event()
    run = Run()

    async def handler(ctx):
        if ctx.task_id == "T2":
            second_started.set()
            await finish_second.wait()
            return "second"
        run.started.set()
        await run.go_on.wait()
        return "first"

    agent = await started(server, handler, link={"connect_timeout_seconds": 0.2}, shutdown_grace_seconds=5)
    await server.send(task(1, "T1"))
    await server.send(task(2, "T2"))
    await run.started.wait()
    await second_started.wait()
    server.refusing = True
    await server.conns[0].close(code=1011)
    run.go_on.set()
    await eventually(lambda: "T1" in agent._transport._unsent)
    stopping = asyncio.ensure_future(agent.stop())
    await eventually(lambda: agent._host.draining and not agent._transport._describer.present)

    server.refusing = False
    [first] = await eventually(lambda: results(server, "T1"))
    assert "T1" not in agent._transport._unsent, "the answer that went out is still held"
    finish_second.set()
    [second] = await eventually(lambda: results(server, "T2"))
    await asyncio.wait_for(stopping, 2)
    assert (first["text"], second["text"]) == ("first", "second") and but_steps(types(server, 1)) == ["result"] * 2


async def test_a_held_answer_that_fails_on_its_way_out_again_waits_for_the_next_delivery_of_its_task(server):
    """The link fails under the answer after its usage went out: the delivery is not acknowledged, so SAM delivers
    the task once more, and what is sent then is the rest of the answer, not all of it again."""
    run = Run()
    agent = await answered_while_the_link_was_down(server, run)
    connection = agent._transport._connection
    failures, send = [LinkError("the agent link dropped")], connection.send

    async def failing_once_under_the_result(frame, *, only_on=None):
        if frame["type"] == "result" and failures:
            raise failures.pop()
        return await send(frame, only_on=only_on)

    connection.send = failing_once_under_the_result
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: usage_sum(server, "T1") == (1, 40, 6) and "T1" in agent._transport._unsent)
    assert results(server, "T1") == [] and server.of("ack") == []

    await server.send(task(2, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 2}])
    assert types(server, 1) == ["describe", "consume", "event", "result", "ack"] and run.runs == 1
    await agent.stop()


async def test_a_run_on_a_connection_whose_description_sam_refused_does_not_wait_to_be_delivered_again(server):
    """SAM delivers nothing on a connection whose description it refused, and may still hold the task."""
    run = Run()
    agent = await running_across_a_reconnect(server, run, run)
    wait = agent._transport._tasks["T1"].redelivery_wait
    await server.send(DESCRIPTION_REFUSED)
    await eventually(lambda: agent._transport._describer.refusal)
    assert wait.cancelled()
    run.go_on.set()
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed" and but_steps(types(server, 1)) == ["describe", "consume", "result"]
    await agent.stop()


async def test_a_run_on_a_connection_the_agent_could_not_describe_itself_on_does_not_wait_to_be_delivered_again(
    server,
):
    """The new connection announces a lower limit than the agent's description fits, so the agent sends neither a
    describe nor a consume on it, and SAM delivers nothing again there."""
    run = Run()
    agent = await started(server, run, description="Triages insurance claims and routes them.")
    await server.send(task(1, "T1"))
    await run.started.wait()
    server.welcome["limits"] = {**LIMITS, "maxDescriptionBytes": 10}
    await server.conns[0].close(code=1012)
    await eventually(lambda: len(server.conns) == 2 and agent._transport._connection.connected)
    run.go_on.set()
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed" and but_steps(types(server, 1)) == ["result"]
    await agent.stop()


async def test_a_run_on_a_connection_whose_describe_raised_does_not_wait_to_be_delivered_again(
    server, monkeypatch, caplog
):
    """The agent's own description cannot be built, so neither a describe nor a consume goes out and SAM delivers
    nothing again. The failure is logged with its traceback, since the description is the author's code."""
    run = Run()
    agent = await started(server, run)
    await server.send(task(1, "T1"))
    await run.started.wait()

    def description_that_cannot_be_built():
        raise RuntimeError("a health problem that cannot be described")

    monkeypatch.setattr(agent._host, "description", description_that_cannot_be_built)
    with caplog.at_level(logging.ERROR, logger="sam_link_sdk.link"):
        await server.conns[0].close(code=1012)
        await eventually(lambda: len(server.conns) == 2 and agent._transport._connection.connected)
        run.go_on.set()
        [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed" and but_steps(types(server, 1)) == ["result"]
    [failed] = said(caplog, "the agent could not describe itself on the new connection")
    assert failed.exc_info is not None and failed.exc_info[0] is RuntimeError
    await agent.stop()


async def test_a_run_tries_no_frame_on_a_new_connection_while_the_agent_still_describes_itself_there(server):
    """The wait for a redelivery begins before the open of a connection awaits anything. A run that ends while the
    describe is on its way therefore waits for its task, and does not try, again and again, a frame that belongs
    to the connection that is gone."""
    run = Run()
    agent = await started(server, run)
    connection = agent._transport._connection
    tried, send = [], connection.send

    async def slow_to_describe(frame, *, only_on=None):
        tried.append(frame["type"])
        if frame["type"] == "describe":
            run.go_on.set()
            await asyncio.sleep(0.1)
        return await send(frame, only_on=only_on)

    await server.send(task(1, "T1"))
    await run.started.wait()
    connection.send = slow_to_describe
    await server.conns[0].close(code=1012)
    await eventually(lambda: server.of("consume", conn=1))
    await eventually(lambda: agent._transport._tasks["T1"].closed)
    assert tried == ["describe", "consume"]

    await server.send(task(1, "T1", redelivered=True))
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed"
    await agent.stop()


async def test_a_held_answer_goes_out_on_a_connection_whose_description_sam_refused(server):
    run = Run()
    agent = await answered_while_the_link_was_down(server, run)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    await server.send(DESCRIPTION_REFUSED)
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["text"] == "done" and usage_sum(server, "T1") == (1, 40, 6)
    await agent.stop()


async def test_the_wait_for_a_redelivery_is_said_once_at_info_and_only_an_unexpected_copy_warns(server, caplog):
    run = Run()
    with caplog.at_level(logging.INFO, logger="sam_link_sdk.link"):
        agent = await running_across_a_reconnect(server, run, run)
        await server.send(task(1, "T1", redelivered=True))
        await eventually(lambda: len(agent._transport._tasks["T1"].deliveries) == 2)
        levels = {r.getMessage(): r.levelno for r in caplog.records if r.levelno >= logging.INFO}
        waiting = (
            "agent link reconnected; waiting for SAM to deliver again the tasks still running and those whose "
            "answer was not sent runs=1 answers=0 seconds=90"
        )
        assert levels[waiting] == logging.INFO
        assert levels["task delivered again after the reconnect; its run goes on task=T1"] == logging.INFO
        assert not [m for m, level in levels.items() if level >= logging.WARNING and "task=T1" in m]

        await server.send(task(2, "T1", redelivered=True))
        await eventually(lambda: len(agent._transport._tasks["T1"].deliveries) == 3)
    copies = [r.levelno for r in caplog.records if r.getMessage() == "duplicate delivery of in-flight task=T1"]
    assert copies == [logging.WARNING]
    await agent.stop()


async def test_an_answer_cancelled_while_it_waits_for_its_redelivery_leaves_its_task_to_be_delivered_again(
    server, caplog
):
    """The loop is closing, or the agent disconnects, while the answer waits. No result went out, so acknowledging
    the task or remembering it as answered would lose it: its next delivery has to run."""
    run = Run()
    agent = await running_across_a_reconnect(server, answering_with_usage(run), run)
    run.go_on.set()
    await eventually(lambda: agent._transport._tasks["T1"].closed)
    wait = agent._transport._tasks["T1"].redelivery_wait
    [answering] = agent._settling.others()
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        answering.cancel()
        await asyncio.gather(answering, return_exceptions=True)
    assert agent._transport._tasks == {} and wait.cancelled()
    assert "T1" not in agent._transport._answered and "T1" not in agent._transport._finals
    assert said(caplog, "answer not sent: its sending was cancelled task=T1")
    assert lost_usage(caplog) == ["model usage lost: not sent to SAM task=T1 calls=1 input=40 output=6"]

    await server.send(task(1, "T1", redelivered=True))
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed" and run.runs == 2
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
    await agent.stop()


async def test_a_prompt_of_a_run_given_back_while_the_link_was_down_is_not_sent_after_the_reconnect(server):
    """The prompt waits for the link; the run is given back meanwhile, so the agent no longer holds the task and
    the connection that opens names nothing the prompt could go out on."""
    run = Run()

    async def handler(ctx):
        run.started.set()
        await run.go_on.wait()
        run.asking = asyncio.ensure_future(ctx.request_approval("pay_claim", {"claim": "C-3"}))
        await asyncio.sleep(30)

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await run.started.wait()
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    run.go_on.set()
    await eventually(lambda: agent._transport._asks)
    agent._runner.tasks["T1"].task.cancel()
    await eventually(lambda: agent._transport._tasks == {})
    server.refusing = False
    assert not (await asyncio.wait_for(run.asking, 5)).approved
    await eventually(lambda: server.of("consume", conn=1))
    assert "prompt" not in types(server, 1)
    await agent.stop()


async def test_every_frame_of_a_run_is_sent_only_on_the_connection_its_task_was_delivered_on(server):
    """A frame that waits out a reconnect inside the send must not go out on the new connection, where SAM may not
    hold the task: the send itself names the connection, for a step as for a prompt, a peer call and the answer."""

    async def handler(ctx):
        ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
        await ctx.emit_event(ModelFinished(MODEL, "It is open."))
        await ctx.progress("checking the claim")
        ctx.record_usage(MODEL, Usage(input_tokens=1, output_tokens=1))
        await asyncio.gather(
            ctx.request_approval("pay_claim", {"claim": "C-3"}), ctx.call_agent("policy-desk", "Status?")
        )
        return "done"

    answer_prompts_and_peer_calls(server)
    agent = await started(server, handler)
    connection = agent._transport._connection
    sent, send = [], connection.send

    async def recording(frame, *, only_on=None):
        sent.append((frame["type"], only_on))
        return await send(frame, only_on=only_on)

    connection.send = recording
    await server.send(task(1, "T1"))
    await eventually(lambda: server.of("ack"))
    assert {kind for kind, _ in sent} == {"event", "prompt", "peer_call", "result", "ack"}
    assert {only_on for _, only_on in sent} == {"c0"}
    await agent.stop()


async def test_a_peer_call_cancelled_while_it_waits_for_its_redelivery_gives_up_its_slot_and_sends_nothing(server):
    async def handler(ctx):
        run.started.set()
        await run.go_on.wait()
        run.calling = asyncio.ensure_future(ctx.call_agent("policy-desk", "Status?"))
        await asyncio.sleep(30)

    run = Run()
    agent = await running_across_a_reconnect(server, handler, run)
    run.go_on.set()
    await eventually(lambda: agent._transport._calls)
    run.calling.cancel()
    with pytest.raises(asyncio.CancelledError):
        await run.calling
    assert agent._transport._calls == {}
    assert server.of("peer_cancel") == [] and "peer_call" not in types(server, 1)
    await agent.stop()


async def several_answers_held_across_a_reconnect(server: LinkServer, count: int, **agent) -> MeshAgent:
    """An agent whose ``count`` runs answered while the link was down: every answer is held, the link is back, and SAM
    has delivered none of the tasks again."""
    go_on, running = asyncio.Event(), []

    async def handler(ctx):
        running.append(ctx.task_id)
        await go_on.wait()
        return "done"

    agent = await started(server, handler, link={"connect_timeout_seconds": 0.5}, **agent)
    for number in range(1, count + 1):
        await server.send(task(number, f"T{number}"))
    await eventually(lambda: len(running) == count)
    server.refusing = True
    await server.conns[0].close(code=1011)
    go_on.set()
    await eventually(lambda: len(agent._transport._unsent) == count)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    return agent


async def test_a_stop_whose_link_drops_while_it_waits_for_held_answers_ends_with_the_grace(server):
    """Each answer sent as it is waits for the link up to ``connect_timeout_seconds``: the stop did not wait that
    long once for each answer held."""
    grace, connect_timeout = 1.0, 0.5
    agent = await several_answers_held_across_a_reconnect(server, 4, shutdown_grace_seconds=grace)
    began = time.monotonic()
    stopping = asyncio.ensure_future(agent.stop())
    await eventually(lambda: server.of("retire", conn=1))
    await asyncio.sleep(0.3)
    server.refusing = True
    await server.conns[1].close(code=1011)
    await asyncio.wait_for(stopping, 5)
    assert time.monotonic() - began < grace + connect_timeout * 0.9


async def test_a_stop_gives_the_answers_it_sends_at_the_end_of_the_grace_no_more_than_that_time(server):
    """Held answers that go out slowly at the end of the grace are given ``FINAL_SEND_SECONDS`` and not all the time
    they take."""
    grace = 0.3
    agent = await several_answers_held_across_a_reconnect(server, 3, shutdown_grace_seconds=grace)
    connection = agent._transport._connection
    send = connection.send

    async def stuck_under_the_results(frame, *, only_on=None):
        if frame["type"] == "result":
            await asyncio.sleep(30)
        return await send(frame, only_on=only_on)

    connection.send = stuck_under_the_results
    began = time.monotonic()
    await asyncio.wait_for(agent.stop(), 5)
    took = time.monotonic() - began
    assert grace + link_transport.FINAL_SEND_SECONDS * 0.8 < took < grace + link_transport.FINAL_SEND_SECONDS + 0.5


async def test_a_stop_whose_grace_is_nearly_gone_still_sends_a_held_answer_that_takes_a_moment(server):
    """The grace ran out waiting for the redelivery: the answer that then takes 0.4 s to go out is still sent, within
    ``FINAL_SEND_SECONDS``."""
    agent = await several_answers_held_across_a_reconnect(server, 1, shutdown_grace_seconds=0.1)
    connection = agent._transport._connection
    send = connection.send

    async def slow_under_the_result(frame, *, only_on=None):
        if frame["type"] == "result":
            await asyncio.sleep(0.4)
        return await send(frame, only_on=only_on)

    connection.send = slow_under_the_result
    await asyncio.wait_for(agent.stop(), 5)
    assert len(results(server, "T1")) == 1


async def test_a_stop_sends_the_other_held_answers_when_one_cannot_go_out_on_a_link_that_is_up(server):
    """An answer the link refuses while it stays up (too large, say) does not keep the answers after it from going
    out at the end of the grace."""
    agent = await several_answers_held_across_a_reconnect(server, 3, shutdown_grace_seconds=0.2)
    connection = agent._transport._connection
    send = connection.send

    async def refusing_the_first_result(frame, *, only_on=None):
        if frame["type"] == "result" and frame.get("taskId") == "T1":
            raise LinkError("frame too large")
        return await send(frame, only_on=only_on)

    connection.send = refusing_the_first_result
    await asyncio.wait_for(agent.stop(), 5)
    assert len(results(server, "T2")) == 1 and len(results(server, "T3")) == 1


async def test_a_stop_waits_for_the_answer_a_redelivery_made_it_send_and_for_the_acknowledgement_after_it(server):
    """The held answer left ``_unsent`` when its task was delivered again, but it is still on its way out: the stop
    does not disconnect under it, which would lose the result, its usage, and the acknowledgement."""
    run = Run()
    agent = await answered_while_the_link_was_down(server, run, shutdown_grace_seconds=3.0)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    connection = agent._transport._connection
    send = connection.send

    async def slow_under_the_answer(frame, *, only_on=None):
        if frame["type"] in ("event", "result"):
            await asyncio.sleep(0.4)
        return await send(frame, only_on=only_on)

    connection.send = slow_under_the_answer
    stopping = asyncio.ensure_future(agent.stop())
    await eventually(lambda: server.of("retire", conn=1))
    await server.send(task(1, "T1", redelivered=True))
    await asyncio.wait_for(stopping, 5)
    assert len(results(server, "T1")) == 1 and usage_sum(server, "T1") == (1, 40, 6)
    assert server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}]


async def test_a_stop_gives_the_answers_what_is_left_of_the_grace_after_its_runs_ended(server):
    """One run ends 0.3 s into a 0.6 s grace: the answer held for a task SAM does not deliver again waits for the
    0.3 s that are left, not for another whole grace."""
    second_started, finish_second = asyncio.Event(), asyncio.Event()
    run = Run()

    async def handler(ctx):
        if ctx.task_id == "T2":
            second_started.set()
            await finish_second.wait()
            return "second"
        run.started.set()
        await run.go_on.wait()
        return "first"

    agent = await started(server, handler, link={"connect_timeout_seconds": 0.2}, shutdown_grace_seconds=0.6)
    await server.send(task(1, "T1"))
    await server.send(task(2, "T2"))
    await run.started.wait()
    await second_started.wait()
    server.refusing = True
    await server.conns[0].close(code=1011)
    run.go_on.set()
    await eventually(lambda: "T1" in agent._transport._unsent)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    began = time.monotonic()
    stopping = asyncio.ensure_future(agent.stop())
    await eventually(lambda: server.of("retire", conn=1))
    await asyncio.sleep(0.3)
    finish_second.set()
    await server.send(task(2, "T2", redelivered=True))
    await asyncio.wait_for(stopping, 5)
    assert time.monotonic() - began < 0.8
    assert [r["text"] for r in results(server, "T1") + results(server, "T2")] == ["first", "second"]


async def test_an_answer_sent_when_the_wait_ran_out_during_the_stop_is_not_cut_off_by_the_disconnect(
    server, monkeypatch, caplog
):
    monkeypatch.setattr(link_transport, "REDELIVERY_WAIT_SECONDS", 0.3)
    run = Run()
    agent = await answered_while_the_link_was_down(server, run, shutdown_grace_seconds=3.0)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    transport = agent._transport
    send_held = transport._send_held

    async def slow_held(task_id, frames, only_on=None):
        await asyncio.sleep(0.5)
        return await send_held(task_id, frames, only_on=only_on)

    transport._send_held = slow_held
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk"):
        stopping = asyncio.ensure_future(agent.stop())
        await eventually(lambda: server.of("retire", conn=1))
        await asyncio.wait_for(stopping, 5)
    assert len(results(server, "T1")) == 1 and usage_sum(server, "T1") == (1, 40, 6)
    assert not [r for r in caplog.records if "sending was cancelled" in r.getMessage() or r.levelno >= logging.ERROR]


async def test_a_stop_while_the_link_is_down_does_not_wait_for_the_redelivery_of_a_held_answer(server):
    run = Run()
    agent = await answered_while_the_link_was_down(server, run, shutdown_grace_seconds=5.0)
    began = time.monotonic()
    await asyncio.wait_for(agent.stop(), 4)
    assert time.monotonic() - began < 1.5


async def test_a_stop_without_a_grace_sends_a_held_answer_as_it_is(server, caplog):
    run = Run()
    agent = await answered_while_the_link_was_down(server, run, shutdown_grace_seconds=0)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk"):
        await asyncio.wait_for(agent.stop(), 4)
    assert [r["state"] for r in results(server, "T1")] == ["completed"] and usage_sum(server, "T1") == (1, 40, 6)
    assert not [r for r in caplog.records if r.levelno >= logging.ERROR]
