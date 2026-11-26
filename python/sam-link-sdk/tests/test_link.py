# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The agent link's connection, against an in-process entrypoint speaking protocol 2 (``LinkServer``, which every link
test uses): connecting, refusing an entrypoint of another protocol, a reader that survives bad messages, the inbound
limit, and the link token never in a log."""

from __future__ import annotations

import asyncio
import copy
import hashlib
import json
import logging
import pickle
import time
from http import HTTPStatus
from itertools import pairwise

import pytest
from websockets.asyncio.server import serve
from websockets.exceptions import ConnectionClosed

from sam_link_sdk import AgentConfig, MeshAgent
from sam_link_sdk.cli import main as cli
from sam_link_sdk.link import (
    LinkConfig,
    LinkError,
    LinkProtocolMismatch,
    LinkRateLimited,
    LinkTransport,
    LinkUnavailable,
)
from sam_link_sdk.link.config import PROTOCOL_V2, PROTOCOL_VERSION
from sam_link_sdk.link.connection import LinkConnection, _retry_after

AGENT = "claims-triage"
TOKEN = "s3cret-link-token"
USER = "alice@example.com"
PROTOCOL_V1 = "sam-agent-link.v1"
LIMITS = {
    "maxFrameBytes": 8 * 1024 * 1024,
    "maxStringBytes": 16384,
    "maxIdBytes": 128,
    "maxNameBytes": 256,
    "maxToolCallsPerModelCall": 128,
    "maxModelsPerTask": 32,
    "maxOpenPeerCalls": 1024,
    "maxOpenPrompts": 1024,
    "maxToolScopes": 64,
    "maxFailureMessageBytes": 2048,
}
PEERS = {
    "type": "peers",
    "peers": [
        {
            "name": "policy-desk",
            "displayName": "Policy Desk",
            "description": "Answers policy questions",
            "skills": [{"id": "policy", "name": "Policy", "description": "Policy status"}],
            "onDemand": False,
        }
    ],
}
STOP = {"type": "control", "enabled": False, "reason": "stopped_by_sam"}
RELEASE = {"type": "control", "enabled": True, "reason": "released_by_sam"}


class LinkServer:
    """The entrypoint's side of the agent link, recording every message the agent sends. As SAM-Go's entrypoint, it
    closes a connection that selected no subprotocol it supports with 4400."""

    def __init__(self, supported=(PROTOCOL_V2,), protocol: int = PROTOCOL_VERSION, welcome: dict | None = None):
        self.frames: list[tuple[int, dict]] = []
        self.conns: list = []
        self.headers: list = []
        self.subprotocols: list[str | None] = []
        self.offered: list[list[str]] = []
        self.responders: dict = {}
        self.after_welcome: list = []
        self.refusing = False
        self.refusal = HTTPStatus.SERVICE_UNAVAILABLE
        self.refusal_headers: dict[str, str | list[str]] = {}
        """Headers of a refusal; a list sends the header once per value."""
        self.refusals_left: int | None = None
        """When set, the server stops refusing after this many more refusals."""
        self.closes_before_welcome = 0
        """How many of the next connections are closed with 1012, as one that lost the race to join is, before the
        welcome."""
        self.attempts: list[float] = []
        self.supported = list(supported)
        self.protocol = protocol
        self.server = None
        self.url = ""
        self.welcome = {"limits": dict(LIMITS), "enabled": True, **(welcome or {})}
        """More welcome fields; a field set to None is left out."""

    async def start(self) -> LinkServer:
        async def authorize(connection, req):
            self.attempts.append(time.monotonic())
            if self.refusing:
                refused = connection.respond(self.refusal, "refused\n")
                for name, values in self.refusal_headers.items():
                    for value in [values] if isinstance(values, str) else values:
                        refused.headers[name] = value
                if self.refusals_left is not None:
                    self.refusals_left -= 1
                    self.refusing = self.refusals_left > 0
                return refused
            if req.headers.get("Authorization") != f"Bearer {TOKEN}" or req.headers.get("Sam-Agent") != AGENT:
                return connection.respond(HTTPStatus.UNAUTHORIZED, "unauthorized\n")
            return None

        def select(_connection, offered):
            return next((p for p in self.supported if p in offered), None)

        self.server = await serve(self._handle, "127.0.0.1", 0, select_subprotocol=select, process_request=authorize)
        port = next(iter(self.server.sockets)).getsockname()[1]
        self.url = f"ws://127.0.0.1:{port}/gw/agent-link/v1/connect"
        return self

    async def stop(self) -> None:
        self.server.close()
        await self.server.wait_closed()

    async def _handle(self, ws):
        index = len(self.conns)
        self.conns.append(ws)
        self.headers.append(ws.request.headers)
        self.subprotocols.append(ws.subprotocol)
        self.offered.append([p.strip() for p in ws.request.headers.get("Sec-WebSocket-Protocol", "").split(",") if p])
        if ws.subprotocol not in self.supported:
            await ws.close(4400, f"subprotocol {' or '.join(self.supported)} required")
            return
        if self.closes_before_welcome > 0:
            self.closes_before_welcome -= 1
            await ws.close(1012, "this replica no longer serves agent links")
            return
        welcome = {
            "type": "welcome",
            "protocol": self.protocol,
            "agent": AGENT,
            "connection": f"c{index}",
            "maxInflight": 16,
            **self.welcome,
        }
        await ws.send(json.dumps({k: v for k, v in welcome.items() if v is not None}))
        try:
            for frame in self.after_welcome:
                await ws.send(frame if isinstance(frame, str) else json.dumps(frame))
            async for raw in ws:
                frame = json.loads(raw)
                self.frames.append((index, frame))
                for out in self.responders.get(frame["type"], lambda _f: [])(frame):
                    await ws.send(json.dumps(out))
        except ConnectionClosed:
            pass

    async def send(self, frame: dict | str, conn: int = -1) -> None:
        await self.conns[conn].send(frame if isinstance(frame, str) else json.dumps(frame))

    def of(self, kind: str, conn: int | None = None) -> list[dict]:
        return [f for i, f in self.frames if f["type"] == kind and (conn is None or i == conn)]


async def eventually(check, timeout: float = 5.0):
    deadline = time.monotonic() + timeout
    while True:
        value = check()
        if value:
            return value
        if time.monotonic() > deadline:
            raise AssertionError("condition not met in time")
        await asyncio.sleep(0.01)


@pytest.fixture(autouse=True)
def private_state_dir(tmp_path, monkeypatch):
    """On the link the agent saves SAM's stop under ~/.sam_link_sdk by default; keep each test's to itself."""
    monkeypatch.setenv("HOME", str(tmp_path))
    return tmp_path


def entrypoint(**welcome) -> LinkServer:
    server = LinkServer(welcome=welcome)
    server.after_welcome = [PEERS]
    return server


@pytest.fixture
async def server():
    s = await entrypoint().start()
    yield s
    await s.stop()


def link_config(server: LinkServer, **kw) -> LinkConfig:
    kw.setdefault("connect_timeout_seconds", 2.0)
    kw.setdefault("max_connects_per_minute", 10_000)
    kw.setdefault("reconnect_min_seconds", 0.01)
    kw.setdefault("reconnect_max_seconds", 0.05)
    return LinkConfig(url=server.url, agent=AGENT, token=kw.pop("token", TOKEN), **kw)


def agent_config(**kw) -> AgentConfig:
    kw.setdefault("shutdown_grace_seconds", 0)
    kw.setdefault("description", "d")
    return AgentConfig(name=AGENT, keepalive_seconds=0, **kw)


async def started(server: LinkServer, handler=lambda ctx: "ok", link: dict | None = None, **kw) -> MeshAgent:
    agent = MeshAgent(agent_config(**kw), handler, transport=LinkTransport(link_config(server, **(link or {}))))
    await agent.start()
    await eventually(lambda: server.of("consume"))
    return agent


def task(
    delivery: int, task_id: str, text: str = "What is the status of claim C-1?", redelivered: bool = False, **fields
) -> dict:
    body = {
        "contextId": "sess-1",
        "text": text,
        "files": [],
        "sessionBehavior": "session",
        "user": {"id": USER},
        "callDepth": 0,
        "trigger": "interactive",
        "traceId": "trace-1",
        "calledBy": "",
        "canPromptUser": True,
        "grantedToolScopes": [],
        "requestContext": {
            "systemPurpose": "",
            "responseFormat": "",
            "projectId": "",
            "conversationContext": "",
            "userProfile": "",
            "userTimezone": "",
        },
        "metadata": {},
    }
    body.update(fields)
    frame = {"type": "task", "delivery": delivery, "taskId": task_id, "task": body}
    if redelivered:
        frame["redelivered"] = True
    return frame


def results(server: LinkServer, task_id: str) -> list[dict]:
    return [f for f in server.of("result") if f["taskId"] == task_id]


def events(server: LinkServer, task_id: str, kind: str | None = None) -> list[dict]:
    return [
        f["event"]
        for f in server.of("event")
        if f["taskId"] == task_id and (kind is None or f["event"]["kind"] == kind)
    ]


def types(server: LinkServer, conn: int) -> list[str]:
    return [f["type"] for i, f in server.frames if i == conn]


def mismatches(caplog) -> list[logging.LogRecord]:
    return [r for r in caplog.records if r.levelno == logging.ERROR and "protocol mismatch" in r.getMessage()]


async def test_connects_with_the_agents_token_and_name_offering_protocol_2_only(server):
    agent = await started(server)
    assert server.headers[0]["Authorization"] == f"Bearer {TOKEN}"
    assert server.headers[0]["Sam-Agent"] == AGENT
    assert server.offered == [[PROTOCOL_V2]] and server.subprotocols == [PROTOCOL_V2]
    link = agent._transport
    assert (link._protocol, link._connection.subprotocol, link._welcome["connection"]) == (2, PROTOCOL_V2, "c0")
    await agent.stop()


async def test_a_wrong_token_is_refused_before_the_upgrade(server):
    with pytest.raises(LinkError, match="401"):
        await LinkConnection(link_config(server, token="wrong")).connect()


def test_config_from_env(monkeypatch):
    monkeypatch.setenv("SAM_LINK_URL", "https://gw.example.com/gw/agent-link/v1/connect")
    monkeypatch.setenv("SAM_AGENT_NAME", "claims-triage")
    monkeypatch.setenv("SAM_LINK_TOKEN", TOKEN)
    monkeypatch.setenv("SAM_LINK_TRUST_STORE_PATH", "/certs")
    cfg = LinkConfig.from_env()
    assert (cfg.url, cfg.agent, cfg.token, cfg.trust_store_path) == (
        "https://gw.example.com/gw/agent-link/v1/connect",
        "claims-triage",
        TOKEN,
        "/certs",
    )
    assert LinkConfig.from_env(agent="other").agent == "other"
    assert TOKEN not in repr(cfg)
    monkeypatch.delenv("SAM_LINK_TOKEN")
    with pytest.raises(ValueError, match="SAM_LINK_TOKEN"):
        LinkConfig.from_env()


def test_a_missing_token_names_both_variables_and_an_unreadable_token_file_names_the_file(monkeypatch, tmp_path):
    """The agent never connects in these cases, so SAM sees nothing: only the message tells the user what to fix."""
    monkeypatch.setenv("SAM_LINK_URL", "ws://127.0.0.1:8000/gw/agent-link/v1/connect")
    monkeypatch.setenv("SAM_AGENT_NAME", "claims-triage")
    monkeypatch.delenv("SAM_LINK_TOKEN", raising=False)
    monkeypatch.delenv("SAM_LINK_TOKEN_FILE", raising=False)
    with pytest.raises(ValueError, match=r"needs SAM_LINK_TOKEN or SAM_LINK_TOKEN_FILE$"):
        LinkConfig.from_env()
    missing = tmp_path / "claims-triage.link-tokn"
    monkeypatch.setenv("SAM_LINK_TOKEN_FILE", str(missing))
    with pytest.raises(ValueError, match=rf"SAM_LINK_TOKEN_FILE {missing} is empty or cannot be read"):
        LinkConfig.from_env()


async def test_disconnect_closes_without_reconnecting(server):
    connection = LinkConnection(link_config(server))
    await connection.connect()
    await connection.disconnect()
    await asyncio.sleep(0.1)
    assert len(server.conns) == 1
    with pytest.raises(LinkError):
        await connection.send({"type": "consume"})


async def test_a_send_cancelled_as_the_link_comes_back_is_cancelled_and_sends_nothing(server):
    """The cancel lands in the same turn of the loop as the reconnect the send waited for."""
    connection = LinkConnection(link_config(server))
    await connection.connect()
    server.refusing = True
    await server.conns[0].close(code=1012)
    await eventually(lambda: not connection.connected)
    sending = asyncio.ensure_future(connection.send({"type": "consume"}))
    await asyncio.sleep(0.05)

    async def cancel_the_send():
        sending.cancel()

    connection.on_open(cancel_the_send)
    server.refusing = False
    await eventually(lambda: connection.connected)
    with pytest.raises(asyncio.CancelledError):
        await sending
    await asyncio.sleep(0.1)
    assert server.of("consume") == []
    await connection.disconnect()


async def test_an_entrypoint_that_refuses_protocol_2_fails_the_start_with_an_error(caplog):
    old = await LinkServer(supported=(PROTOCOL_V1,)).start()
    try:
        agent = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(link_config(old)))
        with caplog.at_level(logging.ERROR, logger="sam_link_sdk.link"), pytest.raises(LinkProtocolMismatch):
            await agent.start()
        [record] = mismatches(caplog)
        assert f"refused {PROTOCOL_V2}" in record.getMessage() and old.offered == [[PROTOCOL_V2]]
        assert old.frames == []
        await agent.stop()
    finally:
        await old.stop()


@pytest.mark.parametrize(
    "welcome, protocol, why",
    [
        ({}, 1, "welcomed the agent with protocol 1"),
        ({"enabled": None}, PROTOCOL_VERSION, "does not say whether SAM has stopped the agent"),
    ],
)
async def test_a_welcome_of_another_protocol_is_refused_and_nothing_after_it_is_acted_on(
    caplog, welcome, protocol, why
):
    other = LinkServer(protocol=protocol, welcome=welcome)
    other.after_welcome = [task(1, "T1"), STOP]
    await other.start()
    ran = []
    try:
        agent = MeshAgent(
            agent_config(), lambda ctx: ran.append(ctx) or "ran", transport=LinkTransport(link_config(other))
        )
        heard = []
        agent.on_stop_change(lambda allowed, reason: heard.append(allowed))
        with caplog.at_level(logging.ERROR, logger="sam_link_sdk.link"), pytest.raises(LinkProtocolMismatch, match=why):
            await agent.start()
        assert len(mismatches(caplog)) == 1
        await asyncio.sleep(0.1)
        assert other.frames == [] and ran == [] and heard == []
        await agent.stop()
    finally:
        await other.stop()


async def test_a_reconnect_to_an_entrypoint_of_another_protocol_keeps_trying_and_logs_an_error_each_time(
    server, caplog
):
    agent = await started(server)
    server.supported = [PROTOCOL_V1]
    with caplog.at_level(logging.ERROR, logger="sam_link_sdk.link"):
        await server.conns[0].close(code=1011)
        await eventually(lambda: len(mismatches(caplog)) >= 2)
        refused = len(server.conns)
    assert all(frame_conn == 0 for frame_conn, _ in server.frames)
    server.supported = [PROTOCOL_V2]
    await eventually(lambda: server.of("consume", conn=len(server.conns) - 1) and len(server.conns) > refused)
    assert types(server, len(server.conns) - 1)[:2] == ["describe", "consume"]
    await agent.stop()


async def test_a_message_that_is_not_an_object_is_dropped_and_the_agent_keeps_serving(server, caplog):
    agent = await started(server, lambda ctx: "done")
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        await server.send("[1, 2]")
        await server.send('"a string"')
        await server.send({"type": "peers", "peers": 5})
        await server.send(task(1, "T1"))
        [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed"
    assert len(server.conns) == 1
    assert [r for r in caplog.records if "not a JSON object" in r.getMessage()]
    await agent.stop()


async def test_a_message_whose_handling_fails_is_logged_by_type_without_its_payload(server, caplog):
    agent = await started(server, lambda ctx: "done")

    def broken(frame):
        raise RuntimeError("bad peers")

    agent._transport._on_peers = broken
    with caplog.at_level(logging.ERROR, logger="sam_link_sdk.link"):
        await server.send({"type": "peers", "peers": [{"name": "C-SECRET-PAYLOAD"}]})
        await server.send(task(1, "T1"))
        await eventually(lambda: results(server, "T1"))
    [record] = [r for r in caplog.records if "message handling failed" in r.getMessage()]
    assert record.levelno == logging.ERROR and "type=peers" in record.getMessage()
    assert "C-SECRET-PAYLOAD" not in caplog.text
    await agent.stop()


async def test_a_reader_that_dies_is_logged_and_the_agent_reconnects(server, caplog):
    agent = await started(server)
    connection = agent._transport._connection
    original = connection._on_raw

    def dies_once(raw):
        connection._on_raw = original
        raise RuntimeError("reader bug")

    connection._on_raw = dies_once
    with caplog.at_level(logging.ERROR, logger="sam_link_sdk.link"):
        await server.send({"type": "peers", "peers": []})
        await eventually(lambda: server.of("consume", conn=1))
    assert [r for r in caplog.records if "reader failed" in r.getMessage()]
    assert types(server, 1)[:2] == ["describe", "consume"]
    await agent.stop()


async def test_an_entrypoint_that_may_send_more_than_the_agent_accepts_is_reported(caplog):
    big = await entrypoint(limits={"maxFrameBytes": 16 * 1024 * 1024, "maxStringBytes": 16384}).start()
    try:
        with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
            agent = await started(big, link={"max_frame_bytes": 4096})
            [warning] = [r for r in caplog.records if "SAM_LINK_MAX_FRAME_BYTES" in r.getMessage()]
            assert "entrypoint_bytes=16777216 agent_bytes=4096" in warning.getMessage()
            await big.send(task(1, "T1", text="x" * 10_000))
            await eventually(
                lambda: [
                    r
                    for r in caplog.records
                    if r.levelno == logging.ERROR and "larger than the agent accepts" in r.getMessage()
                ]
            )
            await eventually(lambda: big.of("consume", conn=1))
        await agent.stop()
    finally:
        await big.stop()


async def test_the_link_token_never_reaches_a_log_at_debug(server, caplog):
    with caplog.at_level(logging.DEBUG):
        agent = await started(server)
        await agent.stop()
    agent_side = [r for r in caplog.records if not r.name.startswith("websockets.server")]
    assert not [r for r in agent_side if TOKEN in r.getMessage()]
    assert [r for r in agent_side if r.name == "websockets.client" and "Authorization: [redacted]" in r.getMessage()], (
        "the client logged no handshake headers: the test proves nothing"
    )


async def test_an_error_message_is_logged_with_its_code_and_ref(server, caplog):
    agent = await started(server)
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        await server.send({"type": "error", "code": "bad_message", "message": "no such thing", "ref": "T7"})
        await eventually(lambda: [r for r in caplog.records if "refused" in r.getMessage()])
    [record] = [r for r in caplog.records if "refused" in r.getMessage()]
    assert "code=bad_message" in record.getMessage() and "ref=T7" in record.getMessage()
    await agent.stop()


def test_link_token_writes_a_private_token_and_prints_only_its_digest(tmp_path, capsys):
    out = tmp_path / "agent.link-token"
    assert cli(["link-token", "--out", str(out)]) == 0
    token = out.read_text()
    printed = capsys.readouterr().out.strip()
    assert printed == hashlib.sha256(token.encode()).hexdigest()
    assert token not in printed and len(token) >= 40
    assert out.stat().st_mode & 0o777 == 0o600

    assert cli(["link-token", "--out", str(out)]) == 1
    assert out.read_text() == token


def test_link_token_never_writes_through_a_link_already_at_its_path(tmp_path, capsys):
    """A dangling link planted at --out (in a shared directory, say) would otherwise send the new token to the file
    it names, created with whatever access its directory gives."""
    planted = tmp_path / "planted" / "token"
    planted.parent.mkdir()
    out = tmp_path / "agent.link-token"
    out.symlink_to(planted)

    assert cli(["link-token", "--out", str(out)]) == 1
    assert not planted.exists()
    assert "exists" in capsys.readouterr().err


async def test_an_entrypoint_refusing_for_the_connection_rate_is_backed_off_from_and_reported_once(
    server, caplog, monkeypatch
):
    """After a refusal for the connection rate the agent backs off and says so once.

    After the first refusal the waits are 0.3 s then 0.6 s, each jittered down to half, not the 0.01 s reconnect pace.
    """
    from sam_link_sdk.link import connection as link_connection

    monkeypatch.setattr(link_connection, "RATE_LIMITED_MIN_SECONDS", 0.3)
    agent = await started(server)
    server.refusing, server.refusal = True, HTTPStatus.TOO_MANY_REQUESTS
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        await server.conns[0].close(code=1011)
        await eventually(lambda: len(server.attempts) >= 4, timeout=5)
    refused = [r for r in caplog.records if "too many connections" in r.getMessage()]
    assert len(refused) == 1
    gaps = [later - earlier for earlier, later in zip(server.attempts[1:], server.attempts[2:], strict=False)]
    assert gaps[0] >= 0.15 and gaps[1] >= 0.3, gaps
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1), timeout=10)
    await agent.stop()


class ClosesAfterWelcome(LinkServer):
    """Closes every connection after the first right after its welcome: an agent that would reconnect at once each
    time."""

    async def _handle(self, ws):
        if self.conns:
            self.conns.append(ws)
            await ws.send(
                json.dumps(
                    {
                        "type": "welcome",
                        "protocol": self.protocol,
                        "agent": AGENT,
                        "connection": f"c{len(self.conns)}",
                        "maxInflight": 16,
                        **self.welcome,
                    }
                )
            )
            await ws.close(1011, "flapping")
            return
        await super()._handle(ws)


async def test_a_connection_that_keeps_dropping_is_reopened_no_faster_than_the_connection_rate():
    server = await ClosesAfterWelcome().start()
    try:
        agent = await started(server, link={"max_connects_per_minute": 4})
        await server.conns[0].close(code=1011)
        await asyncio.sleep(1.5)
        assert len(server.conns) <= 4
        await agent.stop()
    finally:
        await server.stop()


def test_the_still_trying_warning_repeats_no_more_than_once_a_minute_and_counts_down_to_giving_up(caplog):
    """The minute between two warnings cannot be waited out in an e2e run, so the clock is passed in."""
    connection = LinkConnection(LinkConfig(url="wss://sam.example.com/gw/g/v1/connect", agent="a", token="t"))
    error = LinkError("cannot open the agent link: TimeoutError")
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        for now in (100.0, 130.0, 159.9, 160.0, 400.0):
            connection._warn_unreachable(error, give_up_at=1000.0, now=now)

    lines = [r.getMessage() for r in caplog.records if "still trying" in r.getMessage()]
    assert lines == [
        "still trying to reach sam.example.com (cannot open the agent link: TimeoutError), giving up in 900 s",
        "still trying to reach sam.example.com (cannot open the agent link: TimeoutError), giving up in 840 s",
        "still trying to reach sam.example.com (cannot open the agent link: TimeoutError), giving up in 600 s",
    ]


def gaps_between(attempts: list[float]) -> list[float]:
    return [later - earlier for earlier, later in pairwise(attempts)]


async def test_a_replica_that_does_not_serve_agent_links_is_tried_again_after_its_retry_after_and_outside_the_pace(
    server, monkeypatch
):
    """Only one Agent Mesh replica serves agent links and the others answer 503 with Retry-After, so each 503 is tried
    again after that wait, jittered up by at most half: not at the 0.01 s reconnect pace, and not held to
    max_connects_per_minute, which three attempts in this minute would otherwise stop.

    The Retry-After of 1 s is held to a 0.2 s ceiling here, which keeps the test short and shows the bound."""
    from sam_link_sdk.link import connection as link_connection

    monkeypatch.setattr(link_connection, "UNAVAILABLE_RETRY_MAX_SECONDS", 0.2)
    agent = await started(server, link={"max_connects_per_minute": 3})
    server.refusing, server.refusal_headers = True, {"Retry-After": "1"}
    await server.conns[0].close(code=1012)
    await eventually(lambda: len(server.attempts) >= 6, timeout=10)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1), timeout=10)

    refused = gaps_between(server.attempts[1:6])
    assert all(0.2 <= gap < 0.5 for gap in refused), refused
    await agent.stop()


class EveryJitterAt:
    """Stands in for ``random`` in the link's connection module, so a wait is exactly what its jitter makes it."""

    def __init__(self, drawn: float):
        self.drawn = drawn

    def random(self) -> float:
        return self.drawn


async def test_after_the_fast_retries_a_replica_that_still_answers_503_is_backed_off_from_as_any_failure(
    server, caplog, monkeypatch
):
    """Four fast retries, each the 0.2 s Agent Mesh asked for jittered up by half (0.3 s with every draw at its top);
    then the reconnect's own back-off, from its 0.05 s floor: the fast retries did not grow it. Had each doubled it,
    the first wait after them would be 1.6 s."""
    from sam_link_sdk.link import connection as link_connection

    monkeypatch.setattr(link_connection, "random", EveryJitterAt(1.0))
    monkeypatch.setattr(link_connection, "UNAVAILABLE_FAST_RETRIES", 4)
    agent = await started(server, link={"reconnect_min_seconds": 0.05, "reconnect_max_seconds": 5.0})
    server.refusing, server.refusal_headers = True, {"Retry-After": "0.2"}
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        await server.conns[0].close(code=1012)
        await eventually(lambda: len(server.attempts) >= 8, timeout=10)

    gaps = gaps_between(server.attempts[1:8])
    assert all(0.3 <= gap < 0.38 for gap in gaps[:4]), gaps
    assert 0.05 <= gaps[4] < 0.15, gaps
    assert 0.1 <= gaps[5] < 0.25, gaps
    assert any("agent link reconnect failed" in r.getMessage() and "HTTP 503" in r.getMessage() for r in caplog.records)
    assert len([r for r in caplog.records if "still trying to reach" in r.getMessage()]) == 1
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1), timeout=10)
    await agent.stop()


def test_the_wait_after_a_503_is_what_agent_mesh_asked_for_jittered_up_by_at_most_half(monkeypatch):
    from sam_link_sdk.link import connection as link_connection

    refused = LinkUnavailable("Agent Mesh refused the agent link: HTTP 503", 2.0)
    monkeypatch.setattr(link_connection, "random", EveryJitterAt(0.0))
    assert link_connection._wait_before_trying_another_replica(refused) == 2.0
    monkeypatch.setattr(link_connection, "random", EveryJitterAt(1.0))
    assert link_connection._wait_before_trying_another_replica(refused) == 3.0


@pytest.mark.parametrize("copied", [copy.copy, copy.deepcopy, lambda error: pickle.loads(pickle.dumps(error))])
def test_a_link_unavailable_error_survives_a_copy_and_a_pickle(copied):
    """A process pool hands an error back to its parent pickled; without its own reduction the copy would be built
    from the message alone and fail for want of ``retry_after``."""
    refused = LinkUnavailable("Agent Mesh refused the agent link: HTTP 503", 2.5)
    again = copied(refused)
    assert type(again) is LinkUnavailable and again is not refused
    assert (str(again), again.retry_after) == ("Agent Mesh refused the agent link: HTTP 503", 2.5)


def test_the_bounds_of_the_503_retry_are_the_documented_ones():
    """docs/link-protocol.md, docs/operations.md and the CHANGELOG state these numbers."""
    from sam_link_sdk.link import connection as link_connection

    assert link_connection.UNAVAILABLE_RETRY_SECONDS == 1.0
    assert link_connection.UNAVAILABLE_RETRY_MIN_SECONDS == 0.1
    assert link_connection.UNAVAILABLE_RETRY_MAX_SECONDS == 5.0
    assert link_connection.UNAVAILABLE_FAST_RETRIES == 30
    assert link_connection.UNAVAILABLE_CONNECT_SECONDS == 20.0
    assert link_connection.UNAVAILABLE_CONNECT_RETRIES == 15


@pytest.mark.parametrize("code", [1012, 1013])
async def test_a_connection_closed_for_a_restart_or_for_load_is_reopened_within_the_reconnect_floor(server, code):
    """1012 is how an Agent Mesh replica that stops serving agent links closes them. With the default back-off the
    first reconnect is 0.25 to 0.5 s after the close."""
    connection = LinkConnection(LinkConfig(url=server.url, agent=AGENT, token=TOKEN, connect_timeout_seconds=2.0))
    await connection.connect()
    try:
        closed_at = time.monotonic()
        await server.conns[0].close(code=code)
        await eventually(lambda: len(server.conns) == 2 and connection.connected)
        assert server.attempts[1] - closed_at < 1.0
    finally:
        await connection.disconnect()


async def test_the_first_connect_reaches_the_serving_replica_through_the_ones_that_answer_503(server):
    server.refusing, server.refusal_headers = True, {"Retry-After": "0.1"}
    connection = LinkConnection(link_config(server))
    connecting = asyncio.create_task(connection.connect(retry_seconds=10))
    try:
        await eventually(lambda: len(server.attempts) >= 3)
        assert not connecting.done()
        server.refusing = False
        await asyncio.wait_for(connecting, 5)
        assert connection.connected
    finally:
        await connection.disconnect()


async def test_a_connection_closed_with_1012_before_its_welcome_is_tried_again_like_a_503(server, monkeypatch):
    """The replica admitted the connection and then stopped serving agent links: the same answer as a 503, so a
    connect without a grace goes on to the replica that serves them."""
    from sam_link_sdk.link import connection as link_connection

    monkeypatch.setattr(link_connection, "UNAVAILABLE_RETRY_SECONDS", 0.05)
    monkeypatch.setattr(link_connection, "UNAVAILABLE_RETRY_MIN_SECONDS", 0.05)
    server.closes_before_welcome = 2
    connection = LinkConnection(link_config(server))
    try:
        await asyncio.wait_for(connection.connect(), 5)
        assert connection.connected and len(server.conns) == 3
    finally:
        await connection.disconnect()


async def test_a_connection_closed_with_1012_before_its_welcome_raises_link_unavailable_once_the_retries_are_spent(
    server, monkeypatch
):
    from sam_link_sdk.link import connection as link_connection

    monkeypatch.setattr(link_connection, "UNAVAILABLE_CONNECT_RETRIES", 0)
    server.closes_before_welcome = 1
    with pytest.raises(LinkUnavailable, match="close 1012") as refused:
        await LinkConnection(link_config(server)).connect()
    assert refused.value.retry_after == link_connection.UNAVAILABLE_RETRY_SECONDS


def request_driven(server: LinkServer) -> MeshAgent:
    return MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(link_config(server)))


def refuse_the_next(server: LinkServer, refusals: int, retry_after: str | list[str] = "0.1") -> None:
    server.refusing, server.refusals_left = True, refusals
    server.refusal_headers = {"Retry-After": retry_after}


async def test_announce_and_retire_reach_the_serving_replica_through_the_ones_that_answer_503(server):
    """A request-driven agent connects once per invocation, with no grace. Behind a plain load balancer most of its
    attempts reach a replica that does not serve agent links, so it tries the 503 again within the same invocation."""
    agent = request_driven(server)
    refuse_the_next(server, 2)
    await asyncio.wait_for(agent.announce(), 10)
    assert len(server.attempts) == 3 and types(server, 0) == ["describe"]

    refuse_the_next(server, 1)
    await asyncio.wait_for(agent.retire(), 10)
    assert len(server.attempts) == 5 and types(server, 1) == ["retire"]


async def test_a_wake_reaches_the_serving_replica_through_the_ones_that_answer_503(server):
    server.responders["consume"] = lambda f: [task(1, "T1")]
    woken = request_driven(server)
    refuse_the_next(server, 3)
    taken = await asyncio.wait_for(woken.handle_wake(idle_seconds=0.2), 10)
    assert taken == 1 and len(server.attempts) == 4
    assert results(server, "T1")[-1]["state"] == "completed"


async def test_an_invocation_that_only_ever_gets_503_gives_up_when_its_time_is_up(server, monkeypatch):
    """0.5 s here, 20 s by default: a real outage must not hold a serverless invocation for long."""
    from sam_link_sdk.link import connection as link_connection

    monkeypatch.setattr(link_connection, "UNAVAILABLE_CONNECT_SECONDS", 0.5)
    server.refusing, server.refusal_headers = True, {"Retry-After": "0.1"}
    began = time.monotonic()
    with pytest.raises(LinkUnavailable, match="HTTP 503"):
        await request_driven(server).announce()
    assert 2 <= len(server.attempts) <= 6
    assert time.monotonic() - began < 2.0


async def test_an_invocation_that_only_ever_gets_503_gives_up_after_its_retries(server, monkeypatch):
    from sam_link_sdk.link import connection as link_connection

    monkeypatch.setattr(link_connection, "UNAVAILABLE_CONNECT_RETRIES", 2)
    server.refusing, server.refusal_headers = True, {"Retry-After": "0"}
    with pytest.raises(LinkUnavailable, match="HTTP 503"):
        await request_driven(server).handle_wake(idle_seconds=0.1)
    assert len(server.attempts) == 3


async def test_a_disconnect_while_an_invocation_waits_to_try_a_503_again_opens_no_connection(server, caplog):
    """The agent stops while its connect waits out a replica's Retry-After: the next replica would welcome it, and a
    connection opened then would outlive the agent."""
    refuse_the_next(server, 1, retry_after="0.3")
    connection = LinkConnection(link_config(server))
    with caplog.at_level(logging.INFO, logger="sam_link_sdk.link"):
        connecting = asyncio.create_task(connection.connect())
        await eventually(lambda: [r for r in caplog.records if "trying the others" in r.getMessage()])
    await connection.disconnect()
    with pytest.raises(LinkError, match="closed before it opened"):
        await asyncio.wait_for(connecting, 5)
    await asyncio.sleep(0.2)
    assert len(server.attempts) == 1 and server.conns == [] and not connection.is_open


async def test_a_disconnect_while_an_invocation_is_refused_with_503_does_not_wait_to_try_again(server, monkeypatch):
    refuse_the_next(server, 1, retry_after="5")
    connection = LinkConnection(link_config(server))
    refused_by_the_replica = connection._open

    async def refused_while_the_agent_stops():
        try:
            await refused_by_the_replica()
        except LinkUnavailable:
            await connection.disconnect()
            raise

    monkeypatch.setattr(connection, "_open", refused_while_the_agent_stops)
    began = time.monotonic()
    with pytest.raises(LinkUnavailable):
        await connection.connect()
    assert time.monotonic() - began < 2.0 and len(server.attempts) == 1


class SlowToWelcome(LinkServer):
    """An entrypoint that accepts the WebSocket and sends its welcome only when let."""

    def __init__(self):
        super().__init__()
        self.accepted, self.welcome_now = asyncio.Event(), asyncio.Event()

    async def _handle(self, ws):
        self.accepted.set()
        await self.welcome_now.wait()
        await super()._handle(ws)


@pytest.mark.parametrize("retry_seconds", [0.0, 10.0])
async def test_a_disconnect_while_the_link_waits_for_its_welcome_leaves_no_connection_open(retry_seconds):
    server = await SlowToWelcome().start()
    try:
        connection = LinkConnection(link_config(server))
        connecting = asyncio.create_task(connection.connect(retry_seconds))
        await server.accepted.wait()
        await connection.disconnect()
        server.welcome_now.set()
        with pytest.raises(LinkError, match="closed before it opened"):
            await asyncio.wait_for(connecting, 5)
        assert not connection.connected and not connection.is_open
        await eventually(lambda: server.conns[0].state.name == "CLOSED")
    finally:
        await server.stop()


async def test_an_invocation_tries_again_only_past_a_503(server):
    server.refusing, server.refusal = True, HTTPStatus.TOO_MANY_REQUESTS
    with pytest.raises(LinkRateLimited):
        await request_driven(server).announce()
    assert len(server.attempts) == 1


async def test_a_503_carries_the_wait_agent_mesh_asked_for_and_is_not_counted_against_the_pace(server, monkeypatch):
    from sam_link_sdk.link import connection as link_connection

    monkeypatch.setattr(link_connection, "UNAVAILABLE_CONNECT_RETRIES", 0)
    server.refusing, server.refusal_headers = True, {"Retry-After": "2"}
    connection = LinkConnection(link_config(server))
    with pytest.raises(LinkUnavailable, match="HTTP 503") as refused:
        await connection.connect()
    assert refused.value.retry_after == 2.0
    assert len(server.attempts) == 1 and not connection._attempts


async def test_a_503_that_names_retry_after_twice_is_read_by_its_first(server, monkeypatch):
    """A proxy may add a Retry-After of its own to the entrypoint's. Reading one of two must not raise the header
    collection's own error, which is no LinkError and would end a reconnect."""
    from sam_link_sdk.link import connection as link_connection

    monkeypatch.setattr(link_connection, "UNAVAILABLE_CONNECT_RETRIES", 0)
    server.refusing, server.refusal_headers = True, {"Retry-After": ["2", "4"]}
    with pytest.raises(LinkUnavailable) as refused:
        await LinkConnection(link_config(server)).connect()
    assert refused.value.retry_after == 2.0


@pytest.mark.parametrize(
    ("header", "seconds"),
    [
        ("1", 1.0),
        ("0.5", 0.5),
        (None, 1.0),
        ("", 1.0),
        ("Wed, 21 Oct 2026 07:28:00 GMT", 1.0),
        ("nan", 1.0),
        ("inf", 1.0),
        ("0", 0.1),
        ("-3", 0.1),
        ("3600", 5.0),
    ],
)
def test_a_retry_after_is_read_as_seconds_and_held_to_the_fast_retrys_bounds(header, seconds):
    assert _retry_after(header) == seconds
