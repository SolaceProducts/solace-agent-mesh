# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""SAM's stop on the agent link: kept across restarts, failing closed when the link stays down, never running a task
while stopped, and the card left to the entrypoint."""

from __future__ import annotations

import asyncio
import json

import pytest
from websockets.asyncio.server import serve

from sam_link_sdk import AgentStopped, MeshAgent, ToolInfo
from sam_link_sdk.events import Progress
from sam_link_sdk.link import LinkConfig, LinkError, LinkTransport
from sam_link_sdk.link.giveback import NOT_TAKEN_TWICE
from sam_link_sdk.link.transport import RETIRE_SECONDS
from sam_link_sdk.stop import stop_state_path

from .test_link import (
    PEERS,
    RELEASE,
    STOP,
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


@pytest.fixture
async def server():
    s = await entrypoint().start()
    yield s
    await s.stop()


def stopped_at_welcome(server: LinkServer) -> None:
    """SAM has the agent stopped: the welcome says so and a control follows, as SAM-Go's entrypoint sends them."""
    server.welcome["enabled"] = False
    server.after_welcome = [STOP, PEERS]


def released_at_welcome(server: LinkServer) -> None:
    server.welcome["enabled"] = True
    server.after_welcome = [PEERS]


async def test_an_agent_whose_link_never_connected_still_stops_cleanly():
    """The removal cannot go out; the rest of the shutdown still runs."""
    agent = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(link_config(LinkServer())))
    assert agent._transport._peers() == [] and agent._transport._usage_sink("T1") is None
    assert await agent._transport._send_event("T1", Progress("p")) is False
    await agent.stop()


async def test_a_stopped_agent_describes_itself_then_consumes_and_never_retires_for_the_stop(server):
    stopped_at_welcome(server)
    ran = []
    agent = await started(server, lambda ctx: ran.append(ctx.task_id) or "ran")
    await eventually(lambda: not agent.allowed)
    assert types(server, 0)[:2] == ["describe", "consume"]

    await server.send(task(1, "T1"))
    [refused] = await eventually(lambda: results(server, "T1"))
    assert refused["text"] == "Agent stopped: SAM has stopped this agent" and ran == []

    heard = []
    agent.on_stop_change(lambda allowed, reason: heard.append((allowed, reason)))
    await server.send(RELEASE)
    await eventually(lambda: agent.allowed)
    await server.send(STOP)
    await eventually(lambda: not agent.allowed)
    await asyncio.sleep(0.1)
    assert server.of("retire") == [] and len(server.of("describe")) == 1
    assert heard == [(True, "released_by_sam"), (False, "stopped_by_sam")]
    await agent.stop()
    assert types(server, 0)[-1] == "retire"


async def test_after_a_long_outage_the_agent_describes_itself_before_it_consumes_again(server):
    agent = await started(server, offline_grace_seconds=0.1)
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent.allowed)
    await asyncio.sleep(0.2)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    assert types(server, 1)[:2] == ["describe", "consume"]
    await eventually(lambda: agent.allowed)
    await agent.stop()


async def test_every_connection_describes_and_consumes_even_when_the_welcome_names_no_connection():
    server = await entrypoint(connection=None).start()
    try:
        agent = await started(server)
        await server.conns[0].close(code=1011)
        await eventually(lambda: server.of("consume", conn=1))
        assert types(server, 1)[:2] == ["describe", "consume"]
        await agent.stop()
    finally:
        await server.stop()


def saved_stop(agent: MeshAgent):
    return stop_state_path(agent.config, agent._transport._stop_key)


def test_the_stop_is_kept_per_entrypoint_and_agent_name_in_a_filename_safe_key():
    """The stop is kept per entrypoint and agent name, under a filename-safe key.

    Two gateways behind one host and port are two entrypoints, each with its own word on the agent. A key too long for a
    file name keeps the agent's name behind a digest of the whole key.
    """

    def key(url, agent="claims-triage"):
        return LinkTransport(LinkConfig(url=url, agent=agent, token=TOKEN))._stop_key

    assert key("ws://sam.example.com:8443") == "sam.example.com_8443__claims-triage"
    assert (
        key("wss://sam.example.com/gw/claims/v1/connect") == "sam.example.com_443_gw/claims/v1/connect__claims-triage"
    )
    assert key("ws://sam.example.com:8443/agent-link") != key("ws://sam.example.com:9443/agent-link")
    assert key("wss://sam.example.com/gw/claims/v1/connect") != key("wss://sam.example.com/gw/billing/v1/connect")
    assert stop_state_path(agent_config(), key("ws://[::1]:8443/gw/x/v1/connect", "a/b")).name == (
        "__1_8443_gw_x_v1_connect__a_b.stop.json"
    )
    long_a, long_b = (
        stop_state_path(agent_config(), key(f"wss://sam.example.com/{'p' * 300}/{gateway}")).name
        for gateway in ("claims", "billing")
    )
    assert len(long_a) < 255 and long_a != long_b and long_a.endswith("__claims-triage.stop.json")


async def test_a_stop_is_saved_and_a_restarted_agent_starts_stopped_until_the_link_says_otherwise(server):
    first = await started(server)
    await server.send(STOP)
    await eventually(lambda: not first.allowed)
    assert json.loads(saved_stop(first).read_text()) == {"stopped": True}
    await first.stop()

    stopped_at_welcome(server)
    restarted = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(link_config(server)))
    assert not restarted.allowed
    with pytest.raises(AgentStopped):
        restarted.check()
    await restarted.start()
    await asyncio.sleep(0.1)
    assert not restarted.allowed
    await restarted.stop()

    released_at_welcome(server)
    heard = []
    released = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(link_config(server)))
    released.on_stop_change(lambda allowed, reason: heard.append((allowed, reason)))
    assert not released.allowed
    await released.start()
    await eventually(lambda: released.allowed, timeout=1)
    released.check()
    await eventually(lambda: heard == [(True, "")])
    assert json.loads(saved_stop(released).read_text()) == {"stopped": False}
    await released.stop()


def test_agents_of_the_same_name_on_one_host_never_share_a_half_written_stop_file(tmp_path, monkeypatch, caplog):
    import os

    from sam_link_sdk.stop import StopState

    config = agent_config(state_dir=str(tmp_path))
    first, second = StopState(config), StopState(config)
    real_replace = os.replace
    interleaved = []

    def replace(src, dst):
        if not interleaved:
            interleaved.append(True)
            second.control(False)
        return real_replace(src, dst)

    monkeypatch.setattr(os, "replace", replace)
    with caplog.at_level("ERROR", logger="sam_link_sdk.agent"):
        first.control(False)
    assert interleaved and not [r for r in caplog.records if "not saved" in r.getMessage()]
    assert json.loads(stop_state_path(config).read_text()) == {"stopped": True}
    assert [p.name for p in tmp_path.iterdir()] == [stop_state_path(config).name]


async def test_a_link_down_longer_than_the_grace_period_stops_the_agent_until_it_is_back(server):
    """The agent may run again at once; its listeners are told right after, on a task of their own."""
    running, halted = asyncio.Event(), asyncio.Event()

    async def slow(ctx):
        running.set()
        try:
            await asyncio.sleep(30)
        except asyncio.CancelledError:
            halted.set()
            raise
        return "never"

    heard = []
    agent = await started(server, slow, offline_grace_seconds=0.3)
    agent.on_stop_change(lambda allowed, reason: heard.append((allowed, reason)))
    await server.send(task(1, "T1"))
    await asyncio.wait_for(running.wait(), 5)

    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    agent.check()
    await eventually(lambda: not agent.allowed)
    with pytest.raises(AgentStopped) as stopped:
        agent.check()
    assert stopped.value.reason == "disconnected"
    await eventually(lambda: heard == [(False, "disconnected")])
    await asyncio.wait_for(halted.wait(), 5)

    server.refusing = False
    await eventually(lambda: agent.allowed)
    agent.check()
    await eventually(lambda: len(heard) == 2)
    assert heard == [(False, "disconnected"), (True, "")]
    await agent.stop()


async def test_a_short_drop_does_not_stop_the_agent(server):
    heard = []
    agent = await started(server, offline_grace_seconds=5)
    agent.on_stop_change(lambda allowed, reason: heard.append((allowed, reason)))
    await server.conns[0].close(code=1011)
    await eventually(lambda: server.of("consume", conn=1))
    await asyncio.sleep(0.2)
    assert agent.allowed and heard == []
    await agent.stop()


async def test_a_shutdown_while_the_link_is_down_does_not_wait_for_a_reconnect_to_retire(server):
    agent = await started(server, link={"connect_timeout_seconds": 10.0}, offline_grace_seconds=30)
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    await asyncio.wait_for(agent.stop(), timeout=RETIRE_SECONDS + 1)


async def test_a_stop_read_right_after_a_task_was_taken_answers_the_task_before_it_ran(server):
    ran = []
    agent = await started(server, lambda ctx: ran.append(ctx.task_id) or "ok")
    reader = agent._transport._connection
    reader._on_raw(json.dumps(task(1, "T1")))
    await asyncio.sleep(0)
    assert "T1" in agent._runner.tasks and not agent._runner.tasks["T1"].task.done()
    reader._on_raw(json.dumps(STOP))

    answer = (await eventually(lambda: results(server, "T1")))[0]
    assert answer["state"] == "failed" and "an operator stopped this agent" in answer["text"]
    await eventually(lambda: [f for f in server.of("ack") if f["delivery"] == 1])
    assert ran == [] and agent._runner.tasks == {} and agent._transport._tasks == {}
    await agent.stop()


async def test_two_copies_of_a_task_read_back_to_back_run_it_once_and_acknowledge_both(server):
    gate, runs = asyncio.Event(), []

    async def handler(ctx):
        runs.append(ctx.task_id)
        await gate.wait()
        return "Claim C-1 is approved."

    agent = await started(server, handler)
    reader = agent._transport._connection
    reader._on_raw(json.dumps(task(1, "T1")))
    reader._on_raw(json.dumps(task(2, "T1", redelivered=True)))
    await eventually(lambda: runs)
    gate.set()

    await eventually(lambda: len(server.of("ack")) == 2)
    assert runs == ["T1"] and len(results(server, "T1")) == 1
    await agent.stop()


async def test_a_request_driven_agent_serves_although_a_stop_was_saved(server):
    first = await started(server)
    await server.send(STOP)
    await eventually(lambda: not first.allowed)
    await first.stop()

    server.responders["consume"] = lambda f: [task(1, "T1")]
    woken = MeshAgent(agent_config(), lambda ctx: "done", transport=LinkTransport(link_config(server)))
    taken = await asyncio.wait_for(woken.handle_wake(idle_seconds=0.2), 10)
    assert taken == 1
    assert results(server, "T1")[-1]["state"] == "completed"


async def test_a_request_driven_agent_stopped_by_sam_describes_itself_then_consumes_and_runs_nothing(server):
    stopped_at_welcome(server)
    server.responders["consume"] = lambda f: [task(1, "T1")]
    ran = []
    woken = MeshAgent(
        agent_config(), lambda ctx: ran.append(ctx) or "ran", transport=LinkTransport(link_config(server))
    )
    await asyncio.wait_for(woken.handle_wake(idle_seconds=0.2), 10)
    assert types(server, 0)[:2] == ["describe", "consume"]
    assert ran == [] and results(server, "T1")[-1]["text"] == "Agent stopped: SAM has stopped this agent"
    assert server.of("retire") == []


async def test_a_request_driven_agent_is_not_stopped_by_its_own_disconnects(server):
    server.responders["consume"] = lambda f: [task(1, "T1")]
    woken = MeshAgent(
        agent_config(offline_grace_seconds=0.05), lambda ctx: "done", transport=LinkTransport(link_config(server))
    )
    await asyncio.wait_for(woken.handle_wake(idle_seconds=0.1), 10)
    await asyncio.sleep(0.1)
    woken.check()


async def test_a_wake_that_cannot_describe_the_agent_fails_instead_of_consuming(server):
    agent = MeshAgent(
        agent_config(tools=[ToolInfo(f"tool_{i}", "x" * 10_000) for i in range(30)]),
        lambda ctx: "ok",
        transport=LinkTransport(link_config(server)),
    )
    with pytest.raises(LinkError, match="could not describe"):
        await agent.handle_wake(idle_seconds=0.1)
    assert server.of("consume") == []


class Clock:
    def __init__(self):
        self.now = 1000.0

    def __call__(self) -> float:
        return self.now


@pytest.mark.parametrize(
    "events, reason",
    [
        ([("contact", True)], ""),
        ([("control", False)], "stopped_by_sam"),
        ([("contact", True), ("drop", None), ("wait", 9)], ""),
        ([("contact", True), ("drop", None), ("wait", 11)], "disconnected"),
        ([("drop", None), ("wait", 60)], ""),
        ([("contact", True), ("drop", None), ("wait", 11), ("contact", True)], ""),
        ([("contact", True), ("drop", None), ("wait", 8), ("contact", True), ("drop", None), ("wait", 8)], ""),
        (
            [("contact", True), ("drop", None), ("wait", 8), ("contact", True), ("drop", None), ("wait", 11)],
            "disconnected",
        ),
        ([("contact", True), ("drop", None), ("wait", 6), ("drop", None), ("wait", 5)], "disconnected"),
        ([("control", False), ("contact", True), ("drop", None), ("wait", 1)], "stopped_by_sam"),
        ([("control", False), ("contact", True), ("control", True)], ""),
    ],
)
def test_the_stop_state_reads_what_each_sequence_of_events_leaves(tmp_path, events, reason):
    """Each case is a sequence of (event, argument) in order; the clock advances by "wait", and the grace is 10 s.

    A drop before the first contact does not count, because SAM's state is not known yet. Contact clears the outage at
    once, whatever comes before SAM's word is applied. A new contact and a drop right after it count the outage from
    that drop, and two drops without contact in between keep the first one's time. SAM's stop holds through contact and
    outage alike, until SAM releases it.
    """
    from sam_link_sdk.stop import StopState

    clock = Clock()
    state = StopState(agent_config(state_dir=str(tmp_path), offline_grace_seconds=10), clock=clock)
    for event, value in events:
        if event == "contact":
            state.connection(True)
        elif event == "drop":
            remaining = state.connection(False)
            assert remaining is None or 0 <= remaining <= 10
        elif event == "control":
            state.control(value)
        else:
            clock.now += value
    assert state.reason == reason


def test_a_drop_always_says_how_long_until_the_grace_runs_out(tmp_path):
    from sam_link_sdk.stop import StopState

    clock = Clock()
    state = StopState(agent_config(state_dir=str(tmp_path), offline_grace_seconds=10), clock=clock)
    assert state.connection(False) is None
    state.connection(True)
    assert state.connection(False) == 10
    clock.now += 4
    assert state.connection(False) == 6
    state.connection(True)
    assert state.connection(False) == 10


def test_a_stop_is_saved_before_it_takes_effect(tmp_path, monkeypatch):
    from sam_link_sdk import stop

    state = stop.StopState(agent_config(state_dir=str(tmp_path)))
    seen = []
    real_save = stop.StopState._save

    def save(self, stopped):
        seen.append((stopped, self.stopped))
        return real_save(self, stopped)

    monkeypatch.setattr(stop.StopState, "_save", save)
    state.control(False)
    state.control(True)
    assert seen == [(True, False), (False, True)]


async def test_a_stop_that_cannot_be_saved_still_applies_is_reported_and_is_saved_once_it_can_be(
    server, monkeypatch, caplog, tmp_path
):
    from sam_link_sdk import stop

    monkeypatch.setattr(stop, "STOP_SAVE_RETRY_SECONDS", 0.05)
    real_replace, failing = stop.os.replace, [True]

    def replace(src, dst):
        if failing[0]:
            raise PermissionError("read-only state directory")
        real_replace(src, dst)

    monkeypatch.setattr(stop.os, "replace", replace)
    agent = await started(server)
    with caplog.at_level("ERROR", logger="sam_link_sdk.agent"):
        await server.send(STOP)
        await eventually(lambda: not agent.allowed)
        assert [p.id for p in agent._host.description().health] == ["stop_state_unsaved"]
        await asyncio.sleep(0.3)
    unsaved = [r for r in caplog.records if "stop state not saved" in r.getMessage()]
    assert len(unsaved) == 1

    failing[0] = False
    await eventually(lambda: agent._host.description().health == ())
    assert json.loads(saved_stop(agent).read_text()) == {"stopped": True}
    await agent.stop()


def test_a_wake_does_not_write_back_a_stop_it_forgot(tmp_path, monkeypatch):
    from sam_link_sdk import stop

    state = stop.StopState(agent_config(state_dir=str(tmp_path)))
    monkeypatch.setattr(stop.os, "replace", lambda src, dst: (_ for _ in ()).throw(PermissionError("read-only")))
    state.control(False)
    assert state.unsaved and state.stopped
    state.forget_saved()
    monkeypatch.undo()
    assert state.save_again() and not stop_state_path(agent_config(state_dir=str(tmp_path))).exists()


class DropsAfterWelcome(LinkServer):
    """Closes its second connection right after the welcome, then refuses every connection: a gateway going down
    just as the agent got back in."""

    async def _handle(self, ws):
        if len(self.conns) == 1:
            self.conns.append(ws)
            await ws.send(
                json.dumps(
                    {
                        "type": "welcome",
                        "protocol": self.protocol,
                        "agent": "claims-triage",
                        "connection": "c1",
                        "maxInflight": 16,
                        **self.welcome,
                    }
                )
            )
            self.refusing = True
            await ws.close(1012, "restarting")
            return
        await super()._handle(ws)


async def test_a_connection_dropped_right_after_its_welcome_still_stops_the_agent_after_the_grace():
    server = await DropsAfterWelcome().start()
    server.after_welcome = [PEERS]
    halted = asyncio.Event()

    async def handler(ctx):
        try:
            await asyncio.sleep(30)
        except asyncio.CancelledError:
            halted.set()
            raise
        return "never"

    try:
        agent = await started(server, handler, offline_grace_seconds=0.3)
        await server.send(task(1, "T1"))
        await eventually(lambda: "T1" in agent._runner.tasks)
        await server.conns[0].close(code=1011)
        await eventually(lambda: len(server.conns) == 2)
        await eventually(lambda: not agent.allowed, timeout=5)
        with pytest.raises(AgentStopped) as stopped:
            agent.check()
        assert stopped.value.reason == "disconnected"
        await asyncio.wait_for(halted.wait(), 5)
        await agent.stop()
    finally:
        await server.stop()


async def test_a_wake_cut_short_by_the_runtime_stops_the_work_it_was_running(server):
    started_work, cancelled = asyncio.Event(), asyncio.Event()

    async def handler(ctx):
        started_work.set()
        try:
            await asyncio.sleep(30)
        except asyncio.CancelledError:
            cancelled.set()
            raise
        return "never"

    server.responders["consume"] = lambda f: [task(1, "T1")]
    woken = MeshAgent(agent_config(), handler, transport=LinkTransport(link_config(server)))
    wake = asyncio.ensure_future(woken.handle_wake(idle_seconds=0.2))
    await asyncio.wait_for(started_work.wait(), 5)
    wake.cancel()
    await asyncio.gather(wake, return_exceptions=True)
    await asyncio.wait_for(cancelled.wait(), 5)
    assert woken._runner.running() == [] or all(t.done() for t in woken._runner.running())
    assert server.of("ack") == []


async def test_an_agent_reused_after_a_wake_cut_short_serves_the_next_wake_and_the_task_again(server):
    first_run, runs = asyncio.Event(), []

    async def handler(ctx):
        runs.append(ctx.task_id)
        if len(runs) == 1:
            first_run.set()
            await asyncio.sleep(30)
        return "Claim C-1 is approved."

    server.responders["consume"] = lambda f: [task(1, "T1")]
    agent = MeshAgent(agent_config(), handler, transport=LinkTransport(link_config(server)))
    wake = asyncio.ensure_future(agent.handle_wake(idle_seconds=0.2))
    await asyncio.wait_for(first_run.wait(), 5)
    wake.cancel()
    await asyncio.gather(wake, return_exceptions=True)
    assert agent._runner.tasks == {} and agent._runner.running() == []

    server.responders["consume"] = lambda f: [task(2, "T1", redelivered=True)]
    assert await asyncio.wait_for(agent.handle_wake(idle_seconds=0.2), 5) == 1
    assert runs == ["T1", "T1"]
    assert [r["state"] for r in results(server, "T1")] == ["completed"]


async def test_a_reused_agent_stopped_on_one_wake_tells_its_listeners_when_a_later_wake_finds_it_released(server):
    heard = []
    stopped_at_welcome(server)
    agent = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(link_config(server)))
    agent.on_stop_change(lambda allowed, reason: heard.append(allowed))
    await asyncio.wait_for(agent.handle_wake(idle_seconds=0.1), 5)
    assert heard == [False]

    stopped_at_welcome(server)
    await asyncio.wait_for(agent.handle_wake(idle_seconds=0.1), 5)
    assert heard == [False]

    released_at_welcome(server)
    await asyncio.wait_for(agent.handle_wake(idle_seconds=0.1), 5)
    assert heard == [False, True] and agent.allowed


async def test_a_fresh_request_driven_agent_with_a_saved_stop_hears_only_what_sam_says_on_connecting(server):
    link = LinkTransport(link_config(server))
    saved = stop_state_path(agent_config(), link._stop_key)
    saved.parent.mkdir(parents=True, exist_ok=True)
    saved.write_text(json.dumps({"stopped": True}))
    heard = []
    stopped_at_welcome(server)
    agent = MeshAgent(agent_config(), lambda ctx: "ok", transport=link)
    agent.on_stop_change(lambda allowed, reason: heard.append(allowed))
    await asyncio.wait_for(agent.handle_wake(idle_seconds=0.1), 5)
    assert heard == [False]


async def test_a_run_that_ignores_its_abandonment_never_answers_the_task_delivered_again(server):
    first_run, old_returned, runs = asyncio.Event(), asyncio.Event(), []

    async def handler(ctx):
        runs.append(ctx.task_id)
        if len(runs) == 1:
            first_run.set()
            try:
                await asyncio.sleep(30)
            except asyncio.CancelledError:
                await asyncio.sleep(0.3)
                old_returned.set()
                return "A stale answer."
        await old_returned.wait()
        return "Claim C-1 is approved."

    server.responders["consume"] = lambda f: [task(1, "T1")]
    agent = MeshAgent(agent_config(), handler, transport=LinkTransport(link_config(server)))
    wake = asyncio.ensure_future(agent.handle_wake(idle_seconds=0.2))
    await asyncio.wait_for(first_run.wait(), 5)
    wake.cancel()
    await asyncio.gather(wake, return_exceptions=True)

    server.responders["consume"] = lambda f: [task(2, "T1", redelivered=True)]
    await asyncio.wait_for(agent.handle_wake(idle_seconds=0.2), 5)
    assert [r["text"] for r in results(server, "T1")] == ["Claim C-1 is approved."]
    texts = [e["text"] for e in events(server, "T1") if e["kind"] == "text"]
    assert texts == ["Claim C-1 is approved."]
    assert agent._runner.tasks == {}


async def _progress(ctx):
    await ctx.progress("A stale step of the earlier run.")
    return None


async def _ask(ctx):
    return (await ctx.ask_user("Approve the stale claim?")).status


async def _call_peer(ctx):
    return (await ctx.call_agent("policy-desk", "Check the stale policy.")).state


async def _record_usage(ctx):
    from sam_link_sdk import Usage

    ctx.record_usage("openai/gpt-4o", Usage(input_tokens=9_999, output_tokens=77))
    return None


@pytest.mark.parametrize(
    "stale_work, outcome",
    [
        pytest.param(_progress, None, id="progress"),
        pytest.param(_ask, "cancelled", id="ask the user"),
        pytest.param(_call_peer, "error", id="call a peer"),
        pytest.param(_record_usage, None, id="record usage"),
    ],
)
async def test_an_abandoned_run_reaches_nothing_of_the_task_delivered_again(server, stale_work, outcome):
    first_run, new_run, stale_done, runs, got = asyncio.Event(), asyncio.Event(), asyncio.Event(), [], {}

    async def handler(ctx):
        """Ignores its abandonment, as a framework's thread would."""
        runs.append(ctx.task_id)
        if len(runs) == 1:
            first_run.set()
            try:
                await asyncio.sleep(30)
            except asyncio.CancelledError:
                await new_run.wait()
                got["outcome"] = await stale_work(ctx)
                stale_done.set()
                return "A stale answer."
        new_run.set()
        await stale_done.wait()
        return "Claim C-1 is approved."

    server.responders["consume"] = lambda f: [task(1, "T1")]
    agent = MeshAgent(agent_config(), handler, transport=LinkTransport(link_config(server)))
    wake = asyncio.ensure_future(agent.handle_wake(idle_seconds=0.2))
    await asyncio.wait_for(first_run.wait(), 5)
    wake.cancel()
    await asyncio.gather(wake, return_exceptions=True)

    server.responders["consume"] = lambda f: [task(2, "T1", redelivered=True)]
    await asyncio.wait_for(agent.handle_wake(idle_seconds=0.2), 5)
    assert got["outcome"] == outcome
    assert server.of("prompt") == [] and server.of("peer_call") == []
    assert not any("stale" in json.dumps(e).lower() for e in events(server, "T1"))
    assert "9999" not in json.dumps([f for _, f in server.frames])
    assert [r["text"] for r in results(server, "T1")] == ["Claim C-1 is approved."]


async def test_a_request_driven_agent_stopped_by_sam_saves_no_stop_file(server):
    stopped_at_welcome(server)
    agent = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(link_config(server)))
    await asyncio.wait_for(agent.handle_wake(idle_seconds=0.1), 5)
    with pytest.raises(AgentStopped) as stopped:
        agent.check()
    assert stopped.value.reason == "stopped_by_sam"
    assert not saved_stop(agent).exists()
    assert agent._stopper._save_retry is None


async def test_a_run_cancelled_from_outside_is_given_back_on_the_link_and_its_redelivery_runs(server):
    runs = []

    async def handler(ctx):
        runs.append(ctx.task_id)
        await asyncio.sleep(30)

    agent = await started(server, handler)
    await server.send(task(1, "A1"))
    await eventually(lambda: "A1" in agent._runner.tasks)
    agent._runner.tasks["A1"].task.cancel()
    await eventually(lambda: server.of("nack"))
    assert server.of("nack") == [{"type": "nack", "delivery": 1}] and agent._transport._tasks == {}
    assert results(server, "A1") == [] and server.of("ack") == []

    await server.send(task(2, "A1", redelivered=True))
    await eventually(lambda: runs == ["A1", "A1"])
    await agent.stop()


async def test_a_task_the_core_fails_to_take_is_given_back_on_the_link(server):
    runs = []
    agent = await started(server, lambda ctx: runs.append(ctx.task_id) or "Claim C-1 is approved.")
    take = agent._runner.on_task

    async def fails_once(task):
        agent._runner.on_task = take
        raise RuntimeError("the core could not take the task")

    agent._runner.on_task = fails_once
    await server.send(task(1, "A2"))
    await eventually(lambda: server.of("nack"))
    assert agent._transport._tasks == {}
    await server.send(task(2, "A2", redelivered=True))
    await eventually(lambda: results(server, "A2"))
    assert runs == ["A2"]
    await agent.stop()


async def test_a_task_the_core_fails_to_take_twice_is_answered_failed_on_the_link(server):
    agent = await started(server, lambda ctx: "never")

    async def never_takes(task):
        raise RuntimeError("the core could not take the task")

    agent._runner.on_task = never_takes
    server.responders["nack"] = lambda f: (
        [task(f["delivery"] + 1, "A3", redelivered=True)] if f["delivery"] < 10 else []
    )
    await server.send(task(1, "A3"))
    [answer] = await eventually(lambda: results(server, "A3"))
    await asyncio.sleep(0.1)
    assert server.of("nack") == [{"type": "nack", "delivery": 1}] and agent._transport._tasks == {}
    assert answer["state"] == "failed" and answer["text"] == NOT_TAKEN_TWICE
    assert server.of("ack") == [{"type": "ack", "delivery": 2}]
    await agent.stop()


async def test_a_task_whose_run_is_cancelled_from_outside_every_time_is_given_back_once_on_the_link(server):
    """The entrypoint delivers a task given back again at once (a queue that redelivers without limit)."""
    from .test_kill_switch import _swallows_its_own_cancel_then_meets_an_inner_one as _swallows_its_own_cancel

    runs = []

    async def handler(ctx):
        runs.append(ctx.task_id)
        await _swallows_its_own_cancel(ctx)

    server.responders["nack"] = lambda f: (
        [task(f["delivery"] + 1, "A1", redelivered=True)] if f["delivery"] < 10 else []
    )
    agent = await started(server, handler)
    await server.send(task(1, "A1"))
    [answer] = await eventually(lambda: results(server, "A1"))
    await asyncio.sleep(0.1)
    assert runs == ["A1", "A1"] and server.of("nack") == [{"type": "nack", "delivery": 1}]
    assert answer["state"] == "failed" and "cancelled from outside the agent twice" in answer["text"]
    assert server.of("ack") == [{"type": "ack", "delivery": 2}]
    await agent.stop()


async def test_a_connection_cancelled_while_it_waits_for_the_welcome_closes_its_socket_cleanly():
    """A connection cancelled while it waits for the welcome closes its socket cleanly.

    The socket is stored only once the welcome is read, so only the open itself can close it. Left alone it is dropped
    when it is collected, which the entrypoint sees as an abnormal close (1006), not a goodbye. The server's handler
    starts before the client's handshake returns, so the cancel must land in the read.
    """
    closed = asyncio.Event()
    opened = asyncio.Event()
    codes: list[int | None] = []

    async def silent(ws):
        opened.set()
        await ws.wait_closed()
        codes.append(ws.close_code)
        closed.set()

    def select(_connection, offered):
        return next(iter(offered), None)

    server = await serve(silent, "127.0.0.1", 0, select_subprotocol=select)
    try:
        port = next(iter(server.sockets)).getsockname()[1]
        config = LinkConfig(
            url=f"ws://127.0.0.1:{port}/gw/agent-link/v1/connect",
            agent="claims-triage",
            token="t",
            connect_timeout_seconds=30.0,
        )
        transport = LinkTransport(config)
        connecting = asyncio.create_task(transport._connect())
        await asyncio.wait_for(opened.wait(), 5)
        await asyncio.sleep(0.3)
        connecting.cancel()
        await asyncio.gather(connecting, return_exceptions=True)
        await asyncio.wait_for(closed.wait(), 5)
        assert codes == [1000]
    finally:
        server.close()
        await server.wait_closed()
