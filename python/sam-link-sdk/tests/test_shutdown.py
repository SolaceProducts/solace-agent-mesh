# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Graceful shutdown: the agent retires first, running tasks finish within the grace period, the rest are left
unanswered for SAM to deliver again. (A task delivered while the agent drains is the transport's to leave: see
tests/test_link_protocol.py.)"""

from __future__ import annotations

import asyncio

import pytest

from sam_link_sdk import MeshAgent

from .agent_rig import RecordingTransport, agent_config, settle, start_agent


async def test_a_running_task_finishes_and_is_answered_during_the_drain():
    release = asyncio.Event()
    running = asyncio.Event()

    async def handler(ctx):
        running.set()
        await release.wait()
        return "done"

    agent, transport = await start_agent(handler, shutdown_grace_seconds=5)
    await transport.deliver("T1")
    await running.wait()
    stopping = asyncio.create_task(agent.stop())
    await settle()
    assert transport.withdrawn == 1 and transport.connected and not stopping.done() and agent._lifecycle.draining
    release.set()
    await stopping
    assert transport.finished["T1"].state == "completed"
    assert not transport.connected


async def test_a_task_that_outlasts_the_grace_period_is_left_unanswered():
    async def handler(ctx):
        await asyncio.sleep(30)
        return "never"

    agent, transport = await start_agent(handler, shutdown_grace_seconds=0.05)
    await transport.deliver("T1")
    await agent.stop()
    assert transport.answers == [] and transport.given_back == []
    assert not transport.connected


async def test_no_grace_period_means_no_waiting():
    async def handler(ctx):
        await asyncio.sleep(30)
        return "x"

    agent, transport = await start_agent(handler, shutdown_grace_seconds=0)
    await transport.deliver("T1")
    await asyncio.wait_for(agent.stop(), timeout=1)
    assert transport.answers == []


async def test_answers_under_way_and_stopped_tasks_share_one_wait_after_the_drain(monkeypatch):
    from sam_link_sdk import tasks

    monkeypatch.setattr(tasks, "STOPPED_ANSWER_SECONDS", 0.2)
    transport = RecordingTransport()
    never = asyncio.Event()

    async def finish_never_sent(task_id, outcome):
        await never.wait()

    transport._finish = finish_never_sent
    agent, transport = await start_agent(lambda ctx: "done", transport, shutdown_grace_seconds=0.05)
    await transport.deliver("T1")
    await settle()
    await asyncio.wait_for(agent.stop(), timeout=1)
    assert not transport.connected


class _SlowToWithdraw(RecordingTransport):
    """The removal card takes a while to go out, as it does with the link down, so a stop is in its drain for a
    moment."""

    def __init__(self) -> None:
        super().__init__()
        self.withdrawing = asyncio.Event()
        self.go = asyncio.Event()

    async def _withdraw_description(self) -> None:
        self.withdrawing.set()
        await self.go.wait()
        await super()._withdraw_description()


async def test_a_second_stop_waits_for_the_first_to_disconnect_the_link():
    """Inside one process: the process can exit as soon as stop() returns, so the second caller must not return early."""
    transport = _SlowToWithdraw()
    agent = MeshAgent(agent_config(shutdown_grace_seconds=0), lambda ctx: "ok", transport=transport)
    await agent.start()
    first = asyncio.create_task(agent.stop())
    await transport.withdrawing.wait()
    second = asyncio.create_task(agent.stop())
    await settle()
    assert not second.done() and transport.disconnected == 0
    transport.go.set()
    await asyncio.gather(first, second)
    assert transport.disconnected == 1 and not transport.connected


async def test_a_stop_cancelled_by_a_timeout_still_disconnects_and_a_retry_is_then_a_no_op():
    transport = _SlowToWithdraw()
    agent = MeshAgent(agent_config(shutdown_grace_seconds=0), lambda ctx: "ok", transport=transport)
    await agent.start()
    with pytest.raises(asyncio.TimeoutError):
        await asyncio.wait_for(agent.stop(), 0.05)
    assert transport.disconnected == 1 and not transport.connected
    await asyncio.wait_for(agent.stop(), 1)
    assert transport.disconnected == 1
