# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Where the agent sends its link token, shown by a real agent process (`tests/e2e/robust_agent.py`, the SDK's public
API only) and what it sends over the network. SAM-Go never redirects the agent link and is served on this machine's
loopback here, so these cases stand a plain HTTP server in front of the agent instead of SAM: one that answers the
WebSocket upgrade with a redirect, and one that records the headers of whatever reaches it.
"""

from __future__ import annotations

import asyncio
import secrets
import sys
import threading
import time
from collections.abc import Iterator
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from ..contract.sam_client import wait_until
from .agent_process import AgentProcess

ROBUST_AGENT = str(Path(__file__).with_name("robust_agent.py"))
ATTEMPTS_WAIT_SECONDS = 30
EXIT_WAIT_SECONDS = 10
CONNECT_PATH = "/gw/e2e/v1/connect"
OFF_MACHINE = "192.0.2.1"
"""TEST-NET-1 (RFC 5737): an address off this machine that nothing answers on."""


class Recorder(BaseHTTPRequestHandler):
    """Counts the requests that reach it, and those that carried the agent's token; redirects each, or refuses it with
    401. HTTP/1.1, as a WebSocket client takes no other answer."""

    protocol_version = "HTTP/1.1"

    def do_GET(self) -> None:
        server = self.server
        server.requests += 1
        if server.token in (self.headers.get("Authorization") or ""):
            server.tokens_seen += 1
        target = getattr(server, "redirect_to", "")
        if target:
            self.send_response(302)
            self.send_header("Location", target)
        else:
            self.send_response(getattr(server, "status", 401))
        self.send_header("Content-Length", "0")
        self.end_headers()

    def log_message(self, format: str, *args: object) -> None:
        return


@contextmanager
def http_server(token: str, redirect_to: str = "", status: int = 401) -> Iterator[ThreadingHTTPServer]:
    server = ThreadingHTTPServer(("127.0.0.1", 0), Recorder)
    server.requests, server.tokens_seen, server.token, server.redirect_to = 0, 0, token, redirect_to
    server.status = status
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()


def url_of(server: ThreadingHTTPServer, scheme: str = "http") -> str:
    return f"{scheme}://127.0.0.1:{server.server_address[1]}{CONNECT_PATH}"


def robust_agent(
    name: str, root: Path, link_url: str, token: str, link: dict[str, str] | None = None, **env: str
) -> AgentProcess:
    return AgentProcess(
        name,
        ROBUST_AGENT,
        sys.executable,
        root,
        {
            "SAM_LINK_URL": link_url,
            "SAM_LINK_TOKEN": token,
            "SAM_AGENT_NAME": name,
            **(link or {}),
            **{f"ROBUST_{k}": v for k, v in env.items()},
        },
        secrets=(token,),
    )


def test_an_agent_whose_link_address_redirects_to_another_port_never_sends_its_token_there(tmp_path):
    """websockets follows a redirect to a ws:// or wss:// address and sends the same headers there, so a redirect (an
    open redirect on the gateway's host, a proxy misconfigured or taken over) would hand the agent's bearer token to
    another server.
    The agent keeps trying its own address, and only that address ever sees the token."""
    token = secrets.token_urlsafe(32)
    with http_server(token) as elsewhere, http_server(token, redirect_to=url_of(elsewhere, "ws")) as entrypoint:
        agent = robust_agent("e2e-link-redirected", tmp_path, url_of(entrypoint), token, RECONNECT_SECONDS="0.2")
        agent.start()
        try:
            wait_until(
                lambda: entrypoint.requests >= 2 or elsewhere.requests,
                ATTEMPTS_WAIT_SECONDS,
                "the agent to try its address twice, or to follow the redirect",
            )
        finally:
            agent.stop()
        log = agent.log_tail()

        assert (elsewhere.requests, elsewhere.tokens_seen) == (0, 0), log
        assert entrypoint.tokens_seen >= 2, log
    assert "redirected the agent link to ws://127.0.0.1:" in log, log
    assert "not encrypted" not in log, "a link to this machine's loopback is not warned about"


def test_an_agent_given_a_plain_ws_address_off_this_machine_refuses_to_connect_and_exits_failing(tmp_path):
    """A `http://` or `ws://` SAM_LINK_URL to another host would put the bearer token on the network unencrypted. The
    agent never connects: it fails at start-up, naming the host and how to fix it, so a supervisor sees the process
    fail."""
    token = secrets.token_urlsafe(32)
    agent = robust_agent("e2e-link-clear-text", tmp_path, f"http://{OFF_MACHINE}:9{CONNECT_PATH}", token)
    agent.start()
    try:
        wait_until(lambda: not agent.alive(), ATTEMPTS_WAIT_SECONDS, "the agent to refuse its address and exit")
        exited = agent.process.returncode
    finally:
        agent.stop()
    log = agent.log_tail()

    assert exited != 0, log
    assert f"the agent link address is not encrypted (ws:// to {OFF_MACHINE})" in log, log
    assert "SAM_LINK_ALLOW_INSECURE=1" in log, log
    assert "agent link connect failed" not in log, "the agent tried to connect"


def test_an_agent_allowed_a_plain_ws_address_off_this_machine_connects_and_warns_once_that_its_token_goes_in_clear_text(
    tmp_path,
):
    """SAM_LINK_ALLOW_INSECURE=1 is the opt-in for a network the deployment trusts (a cluster's own, TLS ended at a
    proxy in front of it): the agent connects, and says once, naming the host, that the token is not encrypted."""
    token = secrets.token_urlsafe(32)
    agent = robust_agent(
        "e2e-link-clear-text-allowed",
        tmp_path,
        f"http://{OFF_MACHINE}:9{CONNECT_PATH}",
        token,
        link={"SAM_LINK_ALLOW_INSECURE": "1"},
        CONNECT_TIMEOUT="1",
        RECONNECT_SECONDS="0.2",
    )
    agent.start()
    try:
        wait_until(
            lambda: agent.log_tail().count("agent link connect failed") >= 2, ATTEMPTS_WAIT_SECONDS, "two attempts"
        )
    finally:
        agent.stop()
    log = agent.log_tail()

    warnings = [line for line in log.splitlines() if "not encrypted" in line]
    assert len(warnings) == 1, log
    assert "WARNING" in warnings[0] and OFF_MACHINE in warnings[0], log


def run_until_exit(agent: AgentProcess, why: str) -> tuple[int, float]:
    started = time.monotonic()
    agent.start()
    try:
        wait_until(lambda: not agent.alive(), EXIT_WAIT_SECONDS, why)
        return agent.process.returncode, time.monotonic() - started
    finally:
        agent.stop()


def test_an_agent_whose_link_path_gets_a_404_on_its_first_connect_exits_within_seconds_naming_the_address_and_the_fix(
    tmp_path,
):
    """A wrong gateway_id (or a SAM with no agent_link entry) answers the upgrade with 404 for ever, so retrying for
    offline_grace_seconds (15 minutes) only delays the news. The first connect of the process cannot tell this from a
    SAM that is still starting, which is why it is the only one that fails at once."""
    token = secrets.token_urlsafe(32)
    with http_server(token, status=404) as sam:
        url = url_of(sam, "ws")
        agent = robust_agent("e2e-link-404", tmp_path, url, token)
        exited, took = run_until_exit(agent, "the agent to give up on a path that is not served")
        log = agent.log_tail()

        assert exited != 0, log
        assert took < EXIT_WAIT_SECONDS, log
        assert f"HTTP 404 at {url}" in log, log
        assert "/gw/<gateway_id>/v1/connect" in log, log
        assert sam.requests == 1, f"the agent retried a path that is not served: {log}"


def test_an_agent_given_wss_for_a_sam_served_over_plain_http_exits_within_seconds_naming_ws_and_the_trust_store(
    tmp_path,
):
    token = secrets.token_urlsafe(32)
    with http_server(token) as sam:
        url = url_of(sam, "wss")
        agent = robust_agent("e2e-link-tls-to-http", tmp_path, url, token)
        exited, _ = run_until_exit(agent, "the agent to give up on TLS to a plain HTTP port")
        log = agent.log_tail()

    assert exited != 0, log
    assert f"cannot open a TLS connection to {url}" in log, log
    assert "use ws:// in SAM_LINK_URL" in log, log
    assert "SAM_LINK_TRUST_STORE_PATH" in log, log
    assert "agent link connect failed" not in log, "the agent retried"


def test_an_agent_whose_token_is_refused_says_what_to_check_and_exits(tmp_path):
    token = secrets.token_urlsafe(32)
    with http_server(token, status=401) as sam:
        agent = robust_agent("e2e-link-401", tmp_path, url_of(sam), token)
        exited, _ = run_until_exit(agent, "the agent to give up on its refused token")
        log = agent.log_tail()

    assert exited != 0, log
    assert "LinkAuthRefused" in log, log
    for step in (
        "is the link agent's instanceName (a link agent registered on the platform)",
        "matches an entry in SAM's agent_link configuration",
        "SAM_LINK_TOKEN holds the token itself, not a path (SAM_LINK_TOKEN_FILE takes a path)",
        "the digest SAM holds is of this token",
        "the link agent is deployed, or SAM was restarted after the agent was added to its configuration",
    ):
        assert step in log, f"{step!r} missing from\n{log}"
    assert "refusals_in_a_row=1 limit=10" in log, log


def test_an_agent_with_both_token_variables_set_refuses_to_start_instead_of_letting_the_file_win(tmp_path):
    token = secrets.token_urlsafe(32)
    token_file = tmp_path / "token"
    token_file.write_text(token)
    agent = robust_agent(
        "e2e-link-both-tokens",
        tmp_path,
        f"ws://127.0.0.1:9{CONNECT_PATH}",
        token,
        link={"SAM_LINK_TOKEN_FILE": str(token_file)},
    )
    exited, _ = run_until_exit(agent, "the agent to refuse two token settings")
    log = agent.log_tail()

    assert exited != 0, log
    assert "SAM_LINK_TOKEN and SAM_LINK_TOKEN_FILE are both set" in log, log
    assert "agent link connect failed" not in log, "the agent tried to connect"


def test_an_agent_without_a_url_is_told_the_form_to_give_it(tmp_path):
    token = secrets.token_urlsafe(32)
    agent = robust_agent("e2e-link-no-url", tmp_path, "", token)
    agent.env.pop("SAM_LINK_URL")
    exited, _ = run_until_exit(agent, "the agent to refuse a missing SAM_LINK_URL")
    log = agent.log_tail()

    assert exited != 0, log
    assert "the agent link needs SAM_LINK_URL" in log, log
    assert "wss://<sam-host>/gw/<gateway_id>/v1/connect" in log, log


def test_an_agent_that_cannot_reach_sam_says_so_once_a_minute_with_the_time_left_before_it_gives_up(tmp_path):
    """A 503 is the kind of failure that mends itself (SAM restarting behind a proxy), so the agent keeps trying: the
    log has to say it is still trying, and not once per attempt."""
    token = secrets.token_urlsafe(32)
    with http_server(token, status=503) as sam:
        agent = robust_agent("e2e-link-503", tmp_path, url_of(sam), token, RECONNECT_SECONDS="0.2")
        agent.start()
        try:
            wait_until(lambda: sam.requests >= 4, ATTEMPTS_WAIT_SECONDS, "four attempts")
            alive = agent.alive()
        finally:
            agent.stop()
        log = agent.log_tail()

    assert alive, log
    warnings = [line for line in log.splitlines() if "still trying to reach" in line]
    assert len(warnings) == 1, log
    assert f"still trying to reach 127.0.0.1:{sam.server_address[1]} (" in warnings[0], log
    assert "HTTP 503" in warnings[0] and "giving up in " in warnings[0] and "WARNING" in warnings[0], log


def test_a_link_that_has_connected_before_keeps_retrying_a_404_instead_of_giving_up(tmp_path):
    """After a connection succeeded a 404 is a gateway that has not loaded again yet (SAM restarting behind its
    proxy), not a wrong path: the link retries it as any other failure."""
    from sam_link_sdk.link import LinkConfig
    from sam_link_sdk.link.config import LinkConfigError
    from sam_link_sdk.link.connection import LinkConnection

    token = secrets.token_urlsafe(32)
    with http_server(token, status=404) as sam:
        connection = LinkConnection(
            LinkConfig(url=url_of(sam, "ws"), agent="a", token=token, reconnect_min_seconds=0.05)
        )
        connection._ever_connected = True
        with pytest.raises(ConnectionError, match="HTTP 404") as raised:
            asyncio.run(connection.connect(retry_seconds=1.0))

        assert not isinstance(raised.value, LinkConfigError)
        assert sam.requests >= 2
