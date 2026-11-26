# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Request-driven runtimes: a wake serves what waits for the agent and leaves; announce and retire. (On the agent
link's wire: tests/test_link_protocol.py and tests/test_link_transport.py.)"""

from __future__ import annotations

import asyncio

import pytest

from sam_link_sdk import MeshAgent
from sam_link_sdk._model import IncomingTask
from sam_link_sdk.link import LinkTransport

from .agent_rig import RecordingTransport, agent_config, start_agent
from .test_link import (
    agent_config as link_agent_config,
    entrypoint,
    eventually,
    link_config,
    private_state_dir,  # noqa: F401  (autouse: SAM's stop is saved under a private HOME)
    results,
    task,
    types,
)


async def test_a_wake_serves_every_task_waiting_for_the_agent_then_leaves():
    server = await entrypoint().start()
    try:
        seen = []

        async def handler(ctx):
            seen.append(ctx.task_id)
            return f"answer to {ctx.text}"

        server.responders["consume"] = lambda f: [task(1, "T1", "first"), task(2, "T2", "second")]
        woken = MeshAgent(link_agent_config(), handler, transport=LinkTransport(link_config(server)))
        taken = await asyncio.wait_for(woken.handle_wake(idle_seconds=0.2), 10)

        assert taken == 2 and sorted(seen) == ["T1", "T2"]
        for task_id, text in (("T1", "first"), ("T2", "second")):
            assert [r["text"] for r in results(server, task_id)] == [f"answer to {text}"]
        assert sorted(f["delivery"] for f in server.of("ack")) == [1, 2]
        assert server.of("retire") == []
        assert not woken._transport._connection.connected
    finally:
        await server.stop()


async def test_a_wake_that_finds_nothing_waiting_returns_without_running_anything():
    transport = RecordingTransport()
    ran = []
    woken = MeshAgent(agent_config(), lambda ctx: ran.append(ctx) or "unused", transport=transport)
    assert await woken.handle_wake(idle_seconds=0.05) == 0
    assert ran == [] and transport.answers == [] and not transport.connected


class _SlowToConnect(RecordingTransport):
    """A cold sandbox: connecting takes longer than the quiet period, and the first task arrives a moment after the
    agent starts consuming."""

    def __init__(self, backlog: list[str]) -> None:
        super().__init__()
        self.backlog = backlog

    async def _open(self, *, serving: bool = True, wait_for_peers: bool = True) -> None:
        await asyncio.sleep(0.3)
        await super()._open(serving=serving, wait_for_peers=wait_for_peers)

    async def _consume(self, watch=None) -> None:
        await super()._consume(watch)

        async def later():
            await asyncio.sleep(0.02)
            for task_id in self.backlog:
                watch.entered()
                try:
                    await self.host.on_task(IncomingTask(task_id=task_id, text=task_id))
                finally:
                    watch.left()

        asyncio.get_running_loop().create_task(later())


async def test_a_slow_connect_does_not_use_up_the_quiet_period_before_the_first_task_arrives():
    async def handler(ctx):
        await asyncio.sleep(0.05)
        return f"answer to {ctx.text}"

    transport = _SlowToConnect(["T1"])
    taken = await MeshAgent(agent_config(), handler, transport=transport).handle_wake(idle_seconds=0.1)
    assert taken == 1
    assert (transport.finished["T1"].state, transport.finished["T1"].text) == ("completed", "answer to T1")


async def test_announce_describes_the_agent_once_and_disconnects():
    transport = RecordingTransport()
    agent = MeshAgent(agent_config(), lambda ctx: "ok", transport=transport)
    await agent.announce()
    assert len(transport.descriptions) == 1
    assert transport.opened == [{"serving": False, "wait_for_peers": False}]
    assert transport.consumed == 0 and not transport.connected


async def test_retire_withdraws_the_description_and_disconnects():
    transport = RecordingTransport()
    await MeshAgent(agent_config(), lambda ctx: "unused", transport=transport).retire()
    assert transport.withdrawn == 1 and transport.disconnected == 1 and not transport.connected


async def test_an_agent_announced_once_and_then_started_takes_tasks():
    transport = RecordingTransport()
    agent = MeshAgent(agent_config(), lambda ctx: "Claim C-1 is approved.", transport=transport)
    await agent.announce()
    await agent.start()
    await transport.deliver("T1", "Approve C-1")
    assert transport.finished["T1"].state == "completed"
    await agent.stop()


class _KeepsItsOwnListing(RecordingTransport):
    """A transport that leaves the agent to publish and withdraw its own listing (the agent link does it for SAM)."""

    _manages_listing = False


async def test_a_request_driven_agent_keeps_its_listing_when_stopped_so_a_release_can_reach_it():
    asleep = MeshAgent(agent_config(), lambda ctx: "ok", transport=_KeepsItsOwnListing())
    await asleep.announce()
    await asleep._stopper.on_control(False, "stopped_by_sam")
    assert asleep._transport.withdrawn == 0

    long_running, transport = await start_agent(lambda ctx: "ok", _KeepsItsOwnListing())
    await long_running._stopper.on_control(False, "stopped_by_sam")
    for _ in range(50):
        if transport.withdrawn:
            break
        await asyncio.sleep(0.01)
    assert transport.withdrawn == 1
    listed = len(transport.descriptions)
    await long_running._stopper.on_control(True, "released_by_sam")
    for _ in range(50):
        if len(transport.descriptions) > listed:
            break
        await asyncio.sleep(0.01)
    assert len(transport.descriptions) == listed + 1
    await long_running.stop()


async def test_an_agent_reused_after_a_wake_serves_announce_without_consuming():
    server = await entrypoint().start()
    try:
        agent = MeshAgent(link_agent_config(), lambda ctx: "ok", transport=LinkTransport(link_config(server)))
        await agent.announce()
        await agent.announce()
        assert types(server, 0) == ["describe"] and types(server, 1) == ["describe"]
        assert server.of("consume") == []
    finally:
        await server.stop()


async def test_an_agentcore_wake_keeps_serving_when_the_runtime_drops_its_invocation(monkeypatch):
    """AgentCore may drop an invocation whose caller stopped waiting: SAM gives up on its wake call after the wake's
    timeout_seconds (15 minutes unless set), and an approval can wait 45. The runtime server the e2e suite runs locally
    never cancels a handler (tests/e2e/test_hosted_runtimes.py), so the drop is injected here: the task the wake took
    is still answered, and the session reports HealthyBusy until it is, then Healthy, so AgentCore can idle it out."""
    pytest.importorskip("bedrock_agentcore")
    from sam_link_sdk import agentcore

    server = await entrypoint().start()
    try:
        taken, release = asyncio.Event(), asyncio.Event()

        async def handler(ctx):
            taken.set()
            await release.wait()
            return "answered after the invocation was dropped"

        server.responders["consume"] = lambda f: [task(1, "T1")]
        monkeypatch.setenv("SAM_LINK_URL", server.url)
        core = agentcore.app(
            lambda url: MeshAgent(link_agent_config(), handler, transport=LinkTransport(link_config(server)))
        )
        invocation = asyncio.ensure_future(core.handlers["main"]({}))
        await asyncio.wait_for(taken.wait(), 5)

        invocation.cancel()
        await asyncio.gather(invocation, return_exceptions=True)
        assert core.get_current_ping_status().value == "HealthyBusy"
        release.set()

        await eventually(lambda: results(server, "T1"))
        assert [r["text"] for r in results(server, "T1")] == ["answered after the invocation was dropped"]
        await eventually(lambda: core.get_current_ping_status().value == "Healthy", timeout=10)
    finally:
        await server.stop()


async def test_a_link_that_dropped_during_a_wake_does_not_stop_the_sleeping_agent_when_the_grace_runs_out():
    """Nothing is connected between wakes, so a grace armed by a drop during one must not outlive it."""
    transport = RecordingTransport()
    heard: list[tuple[bool, str]] = []

    async def handler(ctx):
        await transport.reconnect()
        await transport.disconnect_from_sam()
        return "done"

    woken = MeshAgent(agent_config(offline_grace_seconds=0.6), handler, transport=transport)
    woken.on_stop_change(lambda allowed, reason: heard.append((allowed, reason)))
    await woken.announce()

    async def deliver_during_wake():
        await asyncio.sleep(0.05)
        await transport.deliver("T1")

    delivery = asyncio.create_task(deliver_during_wake())
    await woken.handle_wake(idle_seconds=0.1)
    await delivery
    await asyncio.sleep(0.9)

    assert heard == []
