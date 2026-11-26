# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The request-driven runtime wrappers (sam_link_sdk.foundry, sam_link_sdk.agentcore) woken by a real SAM-Go through the
runtime's own server: Foundry's invocations host and AgentCore's app, run locally as their own process
(`tests/e2e/hosted_agent.py`), each POST building a fresh agent that joins over the agent link. SAM wakes them the way
it wakes any agent configured with `wake`, so what is asserted is what SAM records (the task, and its log of a wake it
was refused) and what the runtime's process did (how many agents it built for wakes).

Not covered here: SAM stopping the agent during a wake, because the pinned SAM-Go has no route to stop an agent; a
doorbell with a malformed body, because SAM always sends `{}` (AgentCore's server refuses one with 400 before the
wrapper runs; the Foundry wrapper does not read the body).
"""

from __future__ import annotations

import importlib.util
import re
import socket
import sys
from pathlib import Path

import pytest

from ..contract.sam_client import SamClient, wait_until
from ..contract.sam_server import LinkAgent, SamServer, SamUnavailable, free_port, locate_binary
from .agent_process import AgentProcess
from .conftest import LISTED_TIMEOUT_SECONDS, unavailable
from .hosted_agent import WAKE_MARKER

HOSTED_AGENT = str(Path(__file__).with_name("hosted_agent.py"))
RUNTIMES = ("foundry", "agentcore")
RUNTIME_SDK = {"foundry": "azure.ai.agentserver.invocations", "agentcore": "bedrock_agentcore"}
SCENARIOS = ("served", "refused", "unlinked", "outlasted", "cancelled")
WAIT_SECONDS = 60
SAM_WAKE_TIMEOUT_SECONDS = 2
SLOW_SECONDS = 3 * SAM_WAKE_TIMEOUT_SECONDS
WAKE_REFUSED = "agent link wake call refused"
NOT_THE_TOKEN = "not-this-agents-link-token"
TIMESTAMPED = re.compile(r"\d{4}-\d\d-\d\d \d\d:")


def agent_name(runtime: str, scenario: str) -> str:
    return f"e2e-hosted-{runtime}-{scenario}"


class Hosted:
    """A SAM of its own, with an on-demand agent per runtime and scenario, each with a runtime port of its own."""

    def __init__(self, server: SamServer, client: SamClient, root: Path, ports: dict[str, int]):
        self.server, self.client, self.root, self.ports = server, client, root, ports
        self.started: list[AgentProcess] = []

    def link(self, name: str) -> dict[str, str]:
        return {"SAM_LINK_URL": self.server.link_url, "SAM_LINK_TOKEN": self.server.token(name), "SAM_AGENT_NAME": name}

    def announce(self, runtime: str, name: str) -> None:
        """`python main.py announce`, through the wrapper's serve(): SAM lists the agent, on-demand, while it
        sleeps; a task for an agent SAM does not list would wake nothing."""
        announce = AgentProcess(
            f"{name}-announce",
            HOSTED_AGENT,
            sys.executable,
            self.root,
            self.link(name),
            secrets=(self.server.token(name),),
            args=(runtime, "announce"),
        )
        announce.start()
        assert announce.process.wait(timeout=WAIT_SECONDS) == 0, log(announce)
        card = self.client.wait_listed(name, timeout=LISTED_TIMEOUT_SECONDS)
        assert any(x["uri"].endswith("/sam/on-demand") for x in card["capabilities"]["extensions"])

    def runtime(self, runtime: str, name: str, env: dict[str, str]) -> AgentProcess:
        """The runtime's server, listening on the port SAM's wake for the agent calls."""
        port = self.ports[name]
        process = AgentProcess(
            f"{name}-runtime",
            HOSTED_AGENT,
            sys.executable,
            self.root,
            {**env, "SAM_AGENT_NAME": name, "PORT": str(port)},
            secrets=(self.server.token(name),),
            args=(runtime,),
        )
        self.started.append(process)
        process.start()
        wait_until(lambda: listening(port) or not process.alive(), WAIT_SECONDS, f"the {runtime} server to listen")
        assert process.alive(), log(process)
        return process

    def refused(self, name: str) -> list[int]:
        """The HTTP statuses SAM logged for wakes of the agent that the runtime refused."""
        return [
            int(m.group(1))
            for line in self.server.log_text().splitlines()
            if WAKE_REFUSED in line and name in line
            for m in [re.search(r"httpStatus\W+(\d+)", line)]
            if m
        ]


def installed(package: str) -> bool:
    try:
        return importlib.util.find_spec(package) is not None
    except ModuleNotFoundError:
        return False


def listening(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.settimeout(0.5)
        return sock.connect_ex(("127.0.0.1", port)) == 0


def log(process: AgentProcess, lines: int = 80) -> str:
    """The process's own log lines, its last `lines`, without the JSON copies Foundry's console exporter prints of
    each record, and with the link token removed."""
    try:
        text = process.log_path.read_text(errors="replace")
    except OSError:
        return "(no log)"
    kept = "\n".join([x for x in text.splitlines() if TIMESTAMPED.match(x)][-lines:])
    for secret in process.secrets:
        kept = kept.replace(secret, "<redacted>")
    return kept


def answers(runtime: AgentProcess) -> list[str]:
    """How the runtime's server answered each wake, from its own log: Foundry's host logs each POST's status,
    AgentCore's app whether the invocation completed."""
    text = log(runtime, lines=100_000)
    found = re.findall(r'"POST /invocations 1\.1" (\d{3})|Invocation (completed successfully|failed)', text)
    return [
        "served" if status == "200" or outcome == "completed successfully" else "failed" for status, outcome in found
    ]


def wakes(runtime: AgentProcess) -> int:
    return log(runtime, lines=100_000).count(WAKE_MARKER)


@pytest.fixture(scope="module")
def hosted(tmp_path_factory):
    for runtime, package in RUNTIME_SDK.items():
        if not installed(package):
            unavailable(f"the {runtime} wrapper needs {package} (uv sync installs the dev group)")
    try:
        binary = locate_binary()
    except SamUnavailable as exc:
        unavailable(f"no SAM-Go is available: {exc}")
    ports = {agent_name(r, s): free_port() for r in RUNTIMES for s in SCENARIOS}
    agents = [
        LinkAgent(
            name,
            wake_url=f"http://127.0.0.1:{port}/invocations",
            wake_timeout_seconds=SAM_WAKE_TIMEOUT_SECONDS if name.endswith("-outlasted") else 0,
        )
        for name, port in ports.items()
    ]
    server = SamServer(binary, tmp_path_factory.mktemp("sam-hosted"), agents)
    try:
        server.start()
    except BaseException:
        server.stop()
        raise
    client = SamClient(server.base_url, server.binary, server.home, server.root / "user-home")
    running = Hosted(server, client, tmp_path_factory.mktemp("agents-hosted"), ports)
    try:
        yield running
    finally:
        for process in running.started:
            process.stop()
        server.stop()


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_sam_wakes_the_agent_through_the_runtimes_own_server_and_the_agent_built_for_the_wake_serves_the_task(
    hosted, runtime
):
    name = agent_name(runtime, "served")
    hosted.announce(runtime, name)
    server = hosted.runtime(runtime, name, hosted.link(name))
    assert WAKE_MARKER not in log(server)

    view = hosted.client.wait_done(hosted.client.send(name, "hello"), timeout=WAIT_SECONDS)

    assert (view.status, view.error_message) == ("completed", None), log(server)
    assert view.answer().strip() == f"woken in pid {server.process.pid}: hello"
    built = wakes(server)
    assert built == 1, log(server)
    assert hosted.refused(name) == []


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_a_wake_whose_link_token_is_refused_fails_to_sam_and_the_next_wake_serves_what_waited(hosted, runtime):
    """The token is read from SAM_LINK_TOKEN_FILE at every wake, as a deployment that mounts its secret does. While
    it holds the wrong token the wrapper answers the doorbell 500 and SAM logs the wake refused, so the task waits;
    once the file holds the agent's token, the next request's wake serves both tasks."""
    name = agent_name(runtime, "refused")
    hosted.announce(runtime, name)
    token_file = hosted.root / f"{name}.token"
    token_file.write_text(NOT_THE_TOKEN)
    env = {**hosted.link(name), "SAM_LINK_TOKEN_FILE": str(token_file)}
    del env["SAM_LINK_TOKEN"]
    server = hosted.runtime(runtime, name, env)

    first = hosted.client.send(name, "first")
    wait_until(lambda: hosted.refused(name), WAIT_SECONDS, "SAM to log the refused wake")

    assert hosted.refused(name)[0] == 500, hosted.server.log_tail()
    assert "Agent Mesh refused the agent's token: HTTP 401" in log(server)
    assert (hosted.client.status(first) or {}).get("isRunning") is True

    token_file.write_text(hosted.server.token(name))
    second = hosted.client.send(name, "second")
    for task_id, text in ((first, "first"), (second, "second")):
        view = hosted.client.wait_done(task_id, timeout=WAIT_SECONDS)
        assert (view.status, view.error_message) == ("completed", None), log(server)
        assert view.answer().strip() == f"woken in pid {server.process.pid}: {text}"


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_a_runtime_deployed_without_the_link_address_refuses_the_wake_and_sam_logs_it(hosted, runtime):
    """No SAM_LINK_URL: no agent is built, the doorbell is refused (Foundry 400, AgentCore 500 from its own handler),
    and the task waits for a wake that can serve it."""
    name = agent_name(runtime, "unlinked")
    hosted.announce(runtime, name)
    env = hosted.link(name)
    del env["SAM_LINK_URL"]
    server = hosted.runtime(runtime, name, env)

    task_id = hosted.client.send(name, "hello")
    wait_until(lambda: hosted.refused(name), WAIT_SECONDS, "SAM to log the refused wake")

    assert hosted.refused(name)[0] == {"foundry": 400, "agentcore": 500}[runtime], hosted.server.log_tail()
    built, alive = wakes(server), server.alive()
    assert (built, alive) == (0, True), log(server)
    assert (hosted.client.status(task_id) or {}).get("isRunning") is True


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_a_wake_that_outlasts_sams_call_to_the_runtime_still_serves_its_task(hosted, runtime):
    """SAM gives up on the doorbell after its wake's timeout_seconds (15 minutes unless set; an approval can wait 45):
    the agent the wake built is connected by then and goes on serving, and SAM wakes nothing more for it."""
    name = agent_name(runtime, "outlasted")
    hosted.announce(runtime, name)
    server = hosted.runtime(runtime, name, hosted.link(name))

    ask = f"slow {SLOW_SECONDS} screen P-1182"
    view = hosted.client.wait_done(hosted.client.send(name, ask), timeout=WAIT_SECONDS)

    assert (view.status, view.error_message) == ("completed", None), log(server)
    assert view.answer().strip() == f"slow task done: {ask}"
    built = wakes(server)
    assert built == 1, log(server)
    assert hosted.refused(name) == []


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_a_task_cancelled_during_a_wake_ends_canceled_and_the_wake_answers_the_runtime_once_its_queue_is_quiet(
    hosted, runtime
):
    """A cancel is the stop SAM can send during a wake. The task's handler would run for ten minutes; the wake must not
    hold the runtime for that long (or until the wrapper's 50-minute limit), but end once nothing runs and nothing
    comes, and answer the runtime's call as served. Not asserted: that a later task gets a wake of its own. The
    embedded SAM's dev broker loses a request that reaches the agent's queue after a wake's connection has left (the
    link gives it back to the queue, and the dev broker cannot redeliver it), so a second wake of one agent is not
    tried here."""
    name = agent_name(runtime, "cancelled")
    hosted.announce(runtime, name)
    server = hosted.runtime(runtime, name, hosted.link(name))
    task_id = hosted.client.send(name, f"slow {WAIT_SECONDS * 10} never finishes")
    wait_until(lambda: "task accepted" in log(server), WAIT_SECONDS, "the woken agent to take the task")

    cancel = hosted.client.cancel(task_id)
    assert cancel.status in (200, 202, 204), cancel
    view = hosted.client.wait_done(task_id, timeout=WAIT_SECONDS)
    wait_until(lambda: answers(server), WAIT_SECONDS, "the wake to answer the runtime")

    assert view.status in ("canceled", "cancelled"), view.error_message
    assert answers(server)[0] == "served", log(server)
    assert "agent link closed" in log(server)
    assert hosted.refused(name) == []
