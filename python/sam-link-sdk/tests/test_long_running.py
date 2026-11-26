# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""What an agent process that runs for days needs: a token it can rotate and gives up on visibly when SAM keeps
refusing it, a first connect that outlasts SAM's restart, held results bounded by size, and usage counted safely
across the threads frameworks report from."""

from __future__ import annotations

import asyncio
import logging
import sys
import threading
import time
from http import HTTPStatus

import pytest

from sam_link_sdk import MeshAgent, TaskContext
from sam_link_sdk.link import LinkAuthRefused, LinkConfig, LinkError, LinkTransport, transport as link_transport
from sam_link_sdk.memory import ConversationMemory

from .test_link import AGENT, TOKEN, LinkServer, agent_config, entrypoint, eventually, link_config


class RevokingServer(LinkServer):
    """An entrypoint that welcomes the agent and then closes the connection with 4401."""

    async def _handle(self, ws):
        asyncio.get_running_loop().call_later(0.3, lambda: asyncio.ensure_future(ws.close(4401, "revoked")))
        await super()._handle(ws)


@pytest.fixture(autouse=True)
def private_state_dir(tmp_path, monkeypatch):
    monkeypatch.setenv("HOME", str(tmp_path))


@pytest.fixture
async def server():
    s = await entrypoint().start()
    yield s
    await s.stop()


def token_file(tmp_path, text: str = TOKEN):
    path = tmp_path / "agent.link-token"
    path.write_text(text + "\n")
    return path


def agent_for(server, **link) -> MeshAgent:
    return MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(link_config(server, **link)))


async def test_the_token_file_is_read_again_on_every_connection(server, tmp_path):
    path = token_file(tmp_path)
    agent = agent_for(server, token="", token_file=str(path))
    await agent.start()
    path.write_text("rotated-away")
    await agent._transport._connection.drop("test")
    await eventually(lambda: len(server.attempts) >= 3)
    path.write_text(TOKEN)
    await eventually(lambda: len(server.conns) >= 2 and agent._transport._connection.connected)
    await agent.stop()


async def test_the_token_file_is_read_from_the_environment(tmp_path, monkeypatch):
    path = token_file(tmp_path, "from-the-file")
    monkeypatch.setenv("SAM_LINK_URL", "ws://localhost:1/x")
    monkeypatch.setenv("SAM_AGENT_NAME", AGENT)
    monkeypatch.delenv("SAM_LINK_TOKEN", raising=False)
    monkeypatch.setenv("SAM_LINK_TOKEN_FILE", str(path))
    config = LinkConfig.from_env()
    assert config.token_file == str(path)
    assert config._current_token() == "from-the-file"
    path.write_text("next")
    assert config._current_token() == "next"


async def test_an_unreadable_token_file_falls_back_to_the_token(tmp_path):
    config = LinkConfig(url="ws://x", agent=AGENT, token="fallback", token_file=str(tmp_path / "missing"))
    assert config._current_token() == "fallback"


async def test_the_agent_stops_with_an_error_after_repeated_refusals_of_its_token(server, tmp_path, caplog):
    path = token_file(tmp_path)
    agent = agent_for(server, token="", token_file=str(path), max_auth_refusals=3)
    caplog.set_level(logging.ERROR, logger="sam_link_sdk.link")
    running = asyncio.create_task(agent.run_forever())
    await eventually(lambda: server.of("consume"))
    path.write_text("revoked-token")
    await agent._transport._connection.drop("test")
    with pytest.raises(LinkAuthRefused):
        await asyncio.wait_for(running, 10)
    refusals = [r for r in caplog.records if "refused the agent's token" in r.getMessage()]
    assert len(refusals) >= 3
    assert agent._transport._connection.failure is not None


async def test_an_agent_on_a_loop_that_cannot_handle_signals_serves_and_stops_when_cancelled(
    server, monkeypatch, caplog
):
    loop = asyncio.get_running_loop()

    def off_the_main_thread(*_args):
        raise RuntimeError("set_wakeup_fd only works in main thread of the main interpreter")

    monkeypatch.setattr(loop, "add_signal_handler", off_the_main_thread)
    agent = agent_for(server)
    running = asyncio.create_task(agent.run_forever(exit_when_stuck=False))
    await eventually(lambda: server.of("consume"))
    assert any("cannot stop on SIGINT or SIGTERM" in r.getMessage() for r in caplog.records)
    running.cancel()
    with pytest.raises(asyncio.CancelledError):
        await running
    assert not agent._transport._connection.connected


async def test_close_code_4401_counts_as_a_refusal():
    server = RevokingServer(welcome={"limits": None})
    server.after_welcome = []
    await server.start()
    try:
        agent = agent_for(server, max_auth_refusals=3)
        running = asyncio.create_task(agent.run_forever())
        with pytest.raises(LinkAuthRefused):
            await asyncio.wait_for(running, 10)
        assert len(server.conns) == 3
    finally:
        await server.stop()


async def test_a_connection_that_stayed_up_starts_the_refusal_count_again(monkeypatch):
    monkeypatch.setattr("sam_link_sdk.link.connection.AUTH_STABLE_SECONDS", 0.0)
    server = RevokingServer(welcome={"limits": None})
    server.after_welcome = []
    await server.start()
    try:
        agent = agent_for(server, max_auth_refusals=2)
        await agent.start()
        await eventually(lambda: len(server.conns) >= 4)
        assert agent._transport._connection.failure is None
        await agent.stop()
    finally:
        await server.stop()


async def test_the_first_connect_is_tried_again_until_the_entrypoint_is_up(server):
    """Refused with 502, which only the offline grace tries again: a 503 is tried again without one too."""
    server.refusing, server.refusal = True, HTTPStatus.BAD_GATEWAY
    agent = MeshAgent(
        agent_config(offline_grace_seconds=10), lambda ctx: "ok", transport=LinkTransport(link_config(server))
    )
    starting = asyncio.create_task(agent.start())
    await eventually(lambda: len(server.attempts) >= 3)
    assert not starting.done()
    server.refusing = False
    await asyncio.wait_for(starting, 5)
    assert agent._transport._connection.connected
    await agent.stop()


async def test_the_first_connect_gives_up_after_the_offline_grace(server):
    server.refusing, server.refusal = True, HTTPStatus.BAD_GATEWAY
    agent = MeshAgent(
        agent_config(offline_grace_seconds=0.3), lambda ctx: "ok", transport=LinkTransport(link_config(server))
    )
    started = time.monotonic()
    with pytest.raises(LinkError, match="HTTP 502"):
        await agent.start()
    assert len(server.attempts) > 1
    assert time.monotonic() - started < 3


async def test_a_refused_token_on_the_first_connect_fails_at_once(server):
    agent = MeshAgent(
        agent_config(offline_grace_seconds=30),
        lambda ctx: "ok",
        transport=LinkTransport(link_config(server, token="wrong")),
    )
    started = time.monotonic()
    with pytest.raises(LinkAuthRefused):
        await agent.start()
    assert len(server.attempts) == 1
    assert time.monotonic() - started < 2


def big_frame(task_id: str, size: int) -> dict:
    return {"type": "result", "taskId": task_id, "state": "completed", "text": "x" * size}


async def test_finals_are_bounded_by_size_and_the_newest_are_kept(monkeypatch):
    transport = LinkTransport(LinkConfig(url="ws://x", agent=AGENT, token="t"))
    transport._finals.max_bytes = 1_000_000
    for n in range(30):
        transport._keep_final(f"T{n}", big_frame(f"T{n}", 300_000))
    kept = list(transport._finals)
    assert sum(size for _, _, size, _ in transport._finals.values()) <= 1_000_000
    assert kept[-1] == "T29"
    assert "T0" not in kept
    assert kept == [f"T{n}" for n in range(30 - len(kept), 30)]


async def test_a_single_result_over_the_bound_is_still_kept(monkeypatch):
    transport = LinkTransport(LinkConfig(url="ws://x", agent=AGENT, token="t"))
    transport._finals.max_bytes = 1_000
    transport._keep_final("T1", big_frame("T1", 5_000))
    assert list(transport._finals) == ["T1"]


async def test_unsent_results_are_bounded_by_size_and_a_drop_is_logged(monkeypatch, caplog):
    caplog.set_level(logging.WARNING, logger="sam_link_sdk.link")
    transport = LinkTransport(LinkConfig(url="ws://x", agent=AGENT, token="t"))
    transport._unsent.max_bytes = 1_000_000
    for n in range(30):
        transport._keep_unsent(f"T{n}", [big_frame(f"T{n}", 300_000)])
    kept = list(transport._unsent)
    assert transport._unsent.total_bytes <= 1_000_000
    assert set(transport._unsent.sizes()) == set(kept)
    assert kept[-1] == "T29"
    assert "T0" not in kept
    dropped = [r.getMessage() for r in caplog.records if r.levelno == logging.WARNING and "task=T0" in r.getMessage()]
    assert dropped


@pytest.fixture
def fast_threads():
    interval = sys.getswitchinterval()
    sys.setswitchinterval(1e-6)
    yield
    sys.setswitchinterval(interval)


def run_threads(count: int, work) -> None:
    barrier = threading.Barrier(count)

    def go():
        barrier.wait()
        work()

    threads = [threading.Thread(target=go) for _ in range(count)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()


class SlowUsage:
    """A usage report whose input count is slow to read, so unlocked counting loses updates every time."""

    output_tokens = 1
    cached_input_tokens = 0
    cache_write_tokens = 0
    reasoning_tokens = 0
    total_tokens = 0

    @property
    def input_tokens(self) -> int:
        time.sleep(0.001)
        return 2

    def recorded(self) -> bool:
        return True


def test_usage_recorded_from_many_threads_sums_exactly():
    ctx = TaskContext(task_id="T", context_id="c", text="", agent_name=AGENT)
    threads, per_thread = 8, 15

    def work():
        for _ in range(per_thread):
            ctx.record_usage("m", SlowUsage())

    run_threads(threads, work)
    calls = threads * per_thread
    assert ctx._run.usage.llm_calls == calls
    assert ctx._run.usage.total.input_tokens == 2 * calls
    assert ctx._run.usage.total.output_tokens == calls
    assert ctx._run.usage.by_model["m"].input_tokens == 2 * calls


def test_memory_used_from_many_threads_does_not_raise(fast_threads):
    memory = ConversationMemory(max_turns=3, max_conversations=5)
    errors: list[BaseException] = []

    def work():
        try:
            for n in range(2000):
                key = f"c{n % 12}"
                memory.remember(key, [n], "u", "a")
                memory.history(key)
                memory.transcript(key)
                if n % 7 == 0:
                    memory.forget(key)
                len(memory)
        except BaseException as exc:
            errors.append(exc)

    run_threads(16, work)
    assert errors == []
    assert len(memory) <= 5


async def test_held_results_are_released_when_the_link_disconnects_and_after_their_resend_window(monkeypatch):
    monkeypatch.setattr(link_transport, "FINAL_HOLD_SECONDS", 0.05)
    transport = LinkTransport(LinkConfig(url="ws://x", agent=AGENT, token="t"))
    transport._keep_final("T1", big_frame("T1", 1_000))
    await asyncio.sleep(0.15)
    assert len(transport._finals) == 0
    transport._keep_final("T2", big_frame("T2", 1_000))
    await transport._disconnect()
    assert len(transport._finals) == 0 and transport._final_timers == {}
