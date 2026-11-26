# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Fixtures for the contract tests: one embedded SAM-Go per session, and the link driver (agent link protocol 2).

A SAM under test that does not speak protocol 2 fails the session's link fixture with the reason.
"""

from __future__ import annotations

import asyncio
import json
import os
import time
from dataclasses import dataclass
from pathlib import Path

import pytest

from sam_link_sdk.link.config import PROTOCOL_V2, PROTOCOL_VERSION

from .driver import AgentSpec, LinkDriver, RunningAgent
from .driver_link import V2Driver
from .roster import REGISTERED_OWNER, REGISTERED_TEAM, ROLES, agent_name
from .sam_client import SamClient
from .sam_server import LinkAgent, SamServer, SamUnavailable, locate_binary

CONTRACT_DIR = Path(__file__).parent
PROBE_TIMEOUT_SECONDS = 10


def pytest_collection_modifyitems(config, items):
    for item in items:
        if CONTRACT_DIR in Path(str(item.fspath)).parents:
            item.add_marker(pytest.mark.contract)


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    report = outcome.get_result()
    if report.when != "call" or not report.failed:
        return
    server = getattr(item.funcargs.get("link"), "server", None) or item.funcargs.get("sam_server")
    if isinstance(server, SamServer):
        report.sections.append(("SAM log (tail, secrets removed)", server.log_tail()))


REQUIRED_ENV = "SAM_CONTRACT_REQUIRED"
"""Set where the contract tests must run (CI): a SAM that cannot be found or built fails the run instead of skipping every
scenario and leaving the job green.
"""


def sam_unavailable(why: str) -> None:
    if os.environ.get(REQUIRED_ENV, "").lower() in ("1", "true", "yes"):
        pytest.fail(f"{REQUIRED_ENV} is set and no SAM-Go is available: {why}")
    pytest.skip(why)


@pytest.fixture(scope="session")
def sam_server(tmp_path_factory) -> SamServer:
    try:
        binary = locate_binary()
    except SamUnavailable as exc:
        sam_unavailable(str(exc))
    agents = [
        LinkAgent(agent_name(role), tuple(agent_name(p) for p in peers), owner=REGISTERED_OWNER, team=REGISTERED_TEAM)
        for role, peers in ROLES.items()
    ]
    server = SamServer(binary, tmp_path_factory.mktemp("sam"), agents)
    try:
        server.start()
    except BaseException:
        server.stop()
        raise
    yield server
    server.stop()


@pytest.fixture(scope="session")
def sam_client(sam_server) -> SamClient:
    return SamClient(sam_server.base_url, sam_server.binary, sam_server.home, sam_server.root / "user-home")


@dataclass
class Link:
    """The SAM under test as the agents see it: the driver, the agents' names and tokens, and SAM's API."""

    driver: LinkDriver
    server: SamServer
    client: SamClient

    def name(self, role: str) -> str:
        return agent_name(role)

    def token(self, role: str) -> str:
        return self.server.token(self.name(role))


class Mesh:
    """A test's handle on the link: starts agents by role and stops them all when the test ends."""

    def __init__(self, link: Link):
        self.link = link
        self.client = link.client
        self.driver = link.driver
        self._started: list[RunningAgent] = []

    def name(self, role: str) -> str:
        return self.link.name(role)

    def token(self, role: str) -> str:
        return self.link.token(role)

    def start(self, role: str, handler, **spec_fields) -> RunningAgent:
        agent = self.link.driver.start_agent(AgentSpec(self.name(role), handler, **spec_fields), self.token(role))
        self._started.append(agent)
        return agent

    def close(self) -> None:
        for agent in reversed(self._started):
            agent.close()


def _server_speaks(server: SamServer) -> tuple[bool, str]:
    """Offer the protocol's subprotocol, as the agent does, and read the welcome."""
    from websockets.asyncio.client import connect
    from websockets.exceptions import ConnectionClosed, InvalidHandshake, InvalidStatus

    name = agent_name("probe")
    protocol = PROTOCOL_VERSION

    async def probe() -> tuple[bool, str]:
        """A SAM answers 503 until it leads its agent links, which it does moments after it starts."""
        deadline = time.monotonic() + PROBE_TIMEOUT_SECONDS
        while True:
            speaks, why = await probe_once()
            if speaks or not why.startswith("HTTP 503 ") or time.monotonic() > deadline:
                return speaks, why
            await asyncio.sleep(0.2)

    async def probe_once() -> tuple[bool, str]:
        try:
            async with connect(
                server.link_url,
                subprotocols=[PROTOCOL_V2],
                additional_headers={"Authorization": f"Bearer {server.token(name)}", "Sam-Agent": name},
                open_timeout=PROBE_TIMEOUT_SECONDS,
            ) as ws:
                welcome = json.loads(await asyncio.wait_for(ws.recv(), PROBE_TIMEOUT_SECONDS))
        except InvalidStatus as exc:
            return False, f"HTTP {exc.response.status_code} to a {PROTOCOL_V2} upgrade"
        except ConnectionClosed as exc:
            closed = exc.rcvd
            return False, f"closed {closed.code if closed else '?'} ({closed.reason if closed else ''})"
        except (TimeoutError, InvalidHandshake, OSError, ValueError) as exc:
            return False, f"{type(exc).__name__} on a {PROTOCOL_V2} upgrade"
        spoken = welcome.get("protocol") if isinstance(welcome, dict) else None
        if spoken != protocol:
            return False, f"welcome.protocol is {spoken!r} to a {PROTOCOL_V2} offer"
        return True, ""

    return asyncio.run(probe())


@pytest.fixture(scope="session")
def link(sam_server, sam_client) -> Link:
    speaks, why = _server_speaks(sam_server)
    if not speaks:
        pytest.fail(f"the SAM under test does not speak agent link protocol {PROTOCOL_VERSION}: {why}")
    driver = V2Driver(sam_server.link_url, sam_server.namespace)
    yield Link(driver, sam_server, sam_client)
    driver.close()


@pytest.fixture
def mesh(link) -> Mesh:
    handle = Mesh(link)
    yield handle
    handle.close()
