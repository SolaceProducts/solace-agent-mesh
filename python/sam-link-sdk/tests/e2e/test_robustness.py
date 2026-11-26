# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""SDK robustness that only a real SAM-Go and a real agent process can show: how long a shutdown takes, a token that is
rotated or refused while the agent runs, and a handler that ignores its cancel.

The agent is `tests/e2e/robust_agent.py`, a minimal agent of the SDK's public API (the claims examples cannot set up
a handler that ignores its cancel, or the link's timers). It runs as a process of its own and SAM is the real SAM-Go,
on a SAM of this module's own, because the token tests restart SAM: SAM reads its agent link configuration once, at
boot, so a token is rotated or revoked by booting SAM again with the digest of the new one. Every assertion is on
what SAM records, on what the process does (its exit code and when it exits) or on what it logs.
"""

from __future__ import annotations

import importlib.util
import signal
import subprocess
import sys
import time
from pathlib import Path

import pytest

from ..contract.sam_client import SamClient, wait_until
from ..contract.sam_server import LinkAgent, SamServer, SamUnavailable, locate_binary
from .agent_process import AgentProcess
from .conftest import LISTED_TIMEOUT_SECONDS, unavailable

ROBUST_AGENT = str(Path(__file__).with_name("robust_agent.py"))
TASK_WAIT_SECONDS = 30
RECONNECT_WAIT_SECONDS = 20

GRACE_SECONDS = 2
SHUTDOWN_EXTRA_SECONDS = 8
TIMING_SLACK_SECONDS = 2.0
TASK_LIMIT_SECONDS = 2
CANCEL_DEADLINE_SECONDS = 5
MAX_AUTH_REFUSALS = 3
RECONNECT_SECONDS = 0.3
STUCK_WAIT_SECONDS = TASK_LIMIT_SECONDS + CANCEL_DEADLINE_SECONDS + TIMING_SLACK_SECONDS + 1

(
    SHUTDOWN_AGENT,
    LINK_DOWN_AGENT,
    ROTATED_AGENT,
    REVOKED_AGENT,
    STUCK_AGENT,
    BLOCKING_AGENT,
    MISMATCHED_AGENT,
    SWALLOW_AGENT,
    NONE_AGENT,
    MODEL_FAIL_AGENT,
    JOINED_AGENT,
) = (
    "e2e-robust-shutdown",
    "e2e-robust-link-down",
    "e2e-robust-rotated",
    "e2e-robust-revoked",
    "e2e-robust-stuck",
    "e2e-robust-blocking",
    "e2e-robust-mismatched",
    "e2e-robust-swallow",
    "e2e-robust-none",
    "e2e-robust-model-fail",
    "e2e-robust-joined",
)
OTHER_LINK_NAME = "e2e-robust-other-name"
BLOCKED_SECONDS = 120


class Robust:
    """A SAM of its own, and the robust agents started on it."""

    def __init__(self, server: SamServer, client: SamClient, root: Path):
        self.server, self.client, self.root = server, client, root
        self.started: list[AgentProcess] = []

    def start(self, name: str, *, token_file: bool = False, **env: str) -> AgentProcess:
        """Start the agent and wait until SAM lists it. With `token_file` it reads its token from a file of its own
        (SAM_LINK_TOKEN_FILE), which holds SAM's token for it now."""
        link = {"SAM_LINK_URL": self.server.link_url, "SAM_AGENT_NAME": name}
        if token_file:
            link["SAM_LINK_TOKEN_FILE"] = str(self.token_file(name))
            self.token_file(name).write_text(self.server.token(name))
        else:
            link["SAM_LINK_TOKEN"] = self.server.token(name)
        process = AgentProcess(
            name,
            ROBUST_AGENT,
            sys.executable,
            self.root,
            {**link, **{f"ROBUST_{k}": v for k, v in env.items()}},
            secrets=(self.server.token(name),),
        )
        self.started.append(process)
        process.start()
        try:
            self.client.wait_listed(name, timeout=LISTED_TIMEOUT_SECONDS)
        except AssertionError as exc:
            raise AssertionError(f"{exc}\n{process.log_tail()}") from None
        return process

    def token_file(self, name: str) -> Path:
        return self.root / f"{name}.token-file"

    def running(self, task_id: str) -> bool:
        return bool((self.client.status(task_id) or {}).get("isRunning"))


@pytest.fixture(scope="module")
def robust(tmp_path_factory):
    if importlib.util.find_spec("websockets") is None:
        unavailable("the agent link needs websockets")
    try:
        binary = locate_binary()
    except SamUnavailable as exc:
        unavailable(f"no SAM-Go is available: {exc}")
    names = (
        SHUTDOWN_AGENT,
        LINK_DOWN_AGENT,
        ROTATED_AGENT,
        REVOKED_AGENT,
        STUCK_AGENT,
        BLOCKING_AGENT,
        MISMATCHED_AGENT,
        SWALLOW_AGENT,
        NONE_AGENT,
        MODEL_FAIL_AGENT,
        JOINED_AGENT,
    )
    server = SamServer(binary, tmp_path_factory.mktemp("sam-robust"), [LinkAgent(n) for n in names])
    try:
        server.start()
    except BaseException:
        server.stop()
        raise
    client = SamClient(server.base_url, server.binary, server.home, server.root / "user-home")
    running = Robust(server, client, tmp_path_factory.mktemp("agents-robust"))
    try:
        yield running
    finally:
        for process in running.started:
            process.stop()
        server.stop()


def exit_after_sigterm(agent: AgentProcess, wait: float) -> tuple[int, float]:
    """Send SIGTERM; the process's exit code and the seconds it took to exit."""
    signalled = time.monotonic()
    agent.process.send_signal(signal.SIGTERM)
    try:
        exited = agent.process.wait(timeout=wait)
    except subprocess.TimeoutExpired:
        raise AssertionError(f"still running {wait:.0f} s after SIGTERM\n{agent.log_tail()}") from None
    return exited, time.monotonic() - signalled


def test_an_agent_sent_sigterm_while_a_task_outlasts_its_grace_gives_the_task_its_grace_then_leaves_the_mesh_and_exits_zero(
    robust,
):
    """docs/behaviour.md: a shutdown takes at most shutdown_grace_seconds plus 8 s. The task is a cancellable wait,
    so the abandon at the end of the grace is clean and the process ends by itself."""
    agent = robust.start(SHUTDOWN_AGENT, SHUTDOWN_GRACE=str(GRACE_SECONDS))
    task_id = robust.client.send(SHUTDOWN_AGENT, "slow task")
    wait_until(lambda: "task accepted" in agent.log_tail(), TASK_WAIT_SECONDS, "the agent to take the task")

    signalled = time.monotonic()
    agent.process.send_signal(signal.SIGTERM)
    exited = agent.process.wait(timeout=GRACE_SECONDS + SHUTDOWN_EXTRA_SECONDS + 20)
    took = time.monotonic() - signalled

    assert exited == 0, agent.log_tail()
    assert GRACE_SECONDS - 0.5 <= took <= GRACE_SECONDS + SHUTDOWN_EXTRA_SECONDS + TIMING_SLACK_SECONDS, (
        agent.log_tail()
    )
    robust.client.wait_delisted(SHUTDOWN_AGENT)
    assert "abandoning in-flight tasks after the grace period" in agent.log_tail()
    assert robust.running(task_id), "an abandoned task is not answered: SAM keeps it for another replica"


def test_an_agent_sent_sigterm_with_the_link_down_and_a_run_that_ignores_its_cancel_stops_within_the_grace_plus_8_seconds(
    robust,
):
    """The worst case the bound covers. A task has passed its time limit and its run ignores the cancel, so the
    agent's answer for it is due 5 s after the limit; then SAM goes away and the agent is sent SIGTERM. Its retire
    has no link to go out on (it would wait connect_timeout_seconds, 30 s here, for one), and the answer waits for the
    link too. The retire gets 2 s, and the tasks already stopped and the answers going out share 5 s. SAM cannot be
    asked about an agent while it is down, so what is asserted is what the process did: it stopped inside the bound
    and exited zero. Each limit alone is too long: without the retire's 2 s it takes 30 s, without the shared 5 s
    it takes 13 s. The run never ends, so the process ends only because the agent ends it."""
    agent = robust.start(
        LINK_DOWN_AGENT,
        SHUTDOWN_GRACE="1",
        TASK_TIMEOUT=str(TASK_LIMIT_SECONDS),
        CONNECT_TIMEOUT="30",
        RECONNECT_SECONDS=str(RECONNECT_SECONDS),
    )
    robust.client.send(LINK_DOWN_AGENT, "stuck task")
    wait_until(lambda: "task stopped at limit" in agent.log_tail(), TASK_WAIT_SECONDS, "the task to pass its limit")
    robust.server.stop()
    try:
        exited, took = exit_after_sigterm(agent, 60)
    finally:
        robust.server.start()

    assert exited == 0, agent.log_tail()
    assert took <= 1 + SHUTDOWN_EXTRA_SECONDS + TIMING_SLACK_SECONDS, f"exited after {took:.1f} s\n{agent.log_tail()}"
    assert "removal card publish failed" in agent.log_tail()


def issue_and_restart(robust, name: str) -> str:
    """Issue the agent a new token and boot SAM again with only that one valid: the agent's link drops, and the
    token it holds is refused from then on. Returns the new token."""
    new = robust.server.reissue_token(name).read_text().strip()
    robust.server.restart()
    return new


def test_an_agent_reads_its_token_file_again_when_its_link_drops_and_reconnects_with_the_rotated_token_to_serve_another_task(
    robust,
):
    """SAM-Go's agent link takes one token per agent, fixed at boot, so a rotation is the token file replaced and SAM
    booted again with the new digest. The agent was started with only SAM_LINK_TOKEN_FILE: the token it presents on
    the new connection can only come from the file as it is now."""
    agent = robust.start(ROTATED_AGENT, token_file=True, RECONNECT_SECONDS=str(RECONNECT_SECONDS))
    first = robust.client.wait_done(robust.client.send(ROTATED_AGENT, "before"), timeout=TASK_WAIT_SECONDS)
    assert (first.status, first.answer().strip()) == ("completed", "echo: before")
    pid = agent.process.pid

    new = robust.server.reissue_token(ROTATED_AGENT).read_text().strip()
    agent.secrets += (new,)
    robust.token_file(ROTATED_AGENT).write_text(new)
    robust.server.restart()
    robust.client.wait_listed(ROTATED_AGENT, timeout=RECONNECT_WAIT_SECONDS)
    second = robust.client.wait_done(robust.client.send(ROTATED_AGENT, "after"), timeout=TASK_WAIT_SECONDS)

    assert (second.status, second.answer().strip()) == ("completed", "echo: after")
    assert agent.alive() and agent.process.pid == pid, agent.log_tail()
    assert "agent link dropped" in agent.log_tail()
    assert "refused the agent's token" not in agent.log_tail()


def test_an_agent_whose_token_sam_no_longer_accepts_stops_after_its_refusals_with_link_auth_refused_and_a_failing_exit(
    robust,
):
    """The token file still holds the token SAM accepted at the start; SAM is booted with another one. The first
    connection of an agent that is refused fails at once, so the agent has to be serving when this happens: it
    reconnects, is refused max_auth_refusals times in a row and gives up, so a supervisor sees the process fail."""
    agent = robust.start(
        REVOKED_AGENT,
        token_file=True,
        RECONNECT_SECONDS=str(RECONNECT_SECONDS),
        MAX_AUTH_REFUSALS=str(MAX_AUTH_REFUSALS),
    )
    served = robust.client.wait_done(robust.client.send(REVOKED_AGENT, "while valid"), timeout=TASK_WAIT_SECONDS)
    assert (served.status, served.answer().strip()) == ("completed", "echo: while valid")

    agent.secrets += (issue_and_restart(robust, REVOKED_AGENT),)
    wait_until(lambda: not agent.alive(), RECONNECT_WAIT_SECONDS, "the agent to give up on the refused token")
    exited = agent.process.returncode
    log = agent.log_tail()

    assert exited != 0, log
    assert "LinkAuthRefused" in log, log
    assert f"refusals_in_a_row={MAX_AUTH_REFUSALS} limit={MAX_AUTH_REFUSALS}" in log, log
    assert robust.client.agent_card(REVOKED_AGENT) is None


def test_an_agent_whose_link_is_configured_for_another_name_than_its_config_refuses_to_start_naming_both(robust):
    """MeshAgent(config, handle, transport=...): the link connects as LinkConfig.agent (the Sam-Agent header, the key
    of SAM's stop) and the agent serves as AgentConfig.name, so a transport built for another name fails at start
    instead of joining under one name and answering as the other."""
    agent = AgentProcess(
        MISMATCHED_AGENT,
        ROBUST_AGENT,
        sys.executable,
        robust.root,
        {
            "SAM_LINK_URL": robust.server.link_url,
            "SAM_AGENT_NAME": MISMATCHED_AGENT,
            "SAM_LINK_TOKEN": robust.server.token(MISMATCHED_AGENT),
            "ROBUST_LINK_AGENT": OTHER_LINK_NAME,
        },
        secrets=(robust.server.token(MISMATCHED_AGENT),),
    )
    robust.started.append(agent)
    agent.start()
    wait_until(lambda: not agent.alive(), TASK_WAIT_SECONDS, "the agent to refuse to start")
    log = agent.log_tail()

    assert agent.process.returncode != 0, log
    assert "ValueError" in log and repr(MISMATCHED_AGENT) in log and repr(OTHER_LINK_NAME) in log, log
    assert robust.client.agent_card(MISMATCHED_AGENT) is None


def test_a_task_that_ignores_its_cancel_at_the_limit_is_answered_failed_within_the_cancel_deadline_and_the_next_task_gets_its_slot(
    robust, tmp_path
):
    """tasks.py: a run still going 5 s after its cancel is left behind, its task answered with the limit's message and
    its slot freed. max_concurrent_tasks is 1 and the run never ends, so the second task completes only if the slot
    was given back."""
    release = tmp_path / "release"
    agent = robust.start(
        STUCK_AGENT,
        TASK_TIMEOUT=str(TASK_LIMIT_SECONDS),
        MAX_CONCURRENT="1",
        RELEASE_FILE=str(release),
    )
    try:
        sent = time.monotonic()
        stuck = robust.client.wait_done(robust.client.send(STUCK_AGENT, "stuck task"), timeout=STUCK_WAIT_SECONDS)
        answered_after = time.monotonic() - sent

        assert (stuck.status, stuck.error_message) == (
            "failed",
            f"Stopped: the task ran longer than the limit of {TASK_LIMIT_SECONDS} seconds.",
        )
        assert answered_after >= TASK_LIMIT_SECONDS + CANCEL_DEADLINE_SECONDS - 1, answered_after
        assert answered_after <= TASK_LIMIT_SECONDS + CANCEL_DEADLINE_SECONDS + TIMING_SLACK_SECONDS, answered_after
        assert "handler ignores its cancel" in agent.log_tail()

        sent = time.monotonic()
        second = robust.client.wait_done(robust.client.send(STUCK_AGENT, "second task"), timeout=TASK_WAIT_SECONDS)

        assert (second.status, second.answer().strip()) == ("completed", "echo: second task")
        assert time.monotonic() - sent < TASK_LIMIT_SECONDS, "the second task waited for the slot"
    finally:
        release.touch()


def test_an_agent_sent_sigterm_while_a_run_blocks_a_worker_thread_exits_zero_within_the_grace_plus_8_seconds(robust):
    """A framework's synchronous run (CrewAI, Hermes, any blocking call) runs in a worker thread, which no cancel can
    stop. Python waits for that thread before the process ends, so without the agent ending the process itself it
    would stay up for as long as the run blocks, two minutes here."""
    agent = robust.start(BLOCKING_AGENT, SHUTDOWN_GRACE="1", SLOW_SECONDS=str(BLOCKED_SECONDS))
    task_id = robust.client.send(BLOCKING_AGENT, "blocking task")
    wait_until(lambda: "task accepted" in agent.log_tail(), TASK_WAIT_SECONDS, "the agent to take the task")

    exited, took = exit_after_sigterm(agent, BLOCKED_SECONDS / 2)

    assert exited == 0, agent.log_tail()
    assert took <= 1 + SHUTDOWN_EXTRA_SECONDS + TIMING_SLACK_SECONDS, f"exited after {took:.1f} s\n{agent.log_tail()}"
    assert "ending the process: a run left behind may still be going" in agent.log_tail()
    robust.client.wait_delisted(BLOCKING_AGENT)
    assert robust.running(task_id), "an abandoned task is not answered: SAM keeps it for another replica"


def test_a_handler_that_swallows_the_cancel_at_the_limit_is_still_answered_failed_with_the_limits_message(robust):
    """A handler (or a framework's retry loop) that catches the CancelledError of a stop and returns normally must
    not turn the stopped task into a success: SAM is told the limit's message, not the handler's own text."""
    robust.start(SWALLOW_AGENT, TASK_TIMEOUT=str(TASK_LIMIT_SECONDS))
    view = robust.client.wait_done(robust.client.send(SWALLOW_AGENT, "swallow task"), timeout=TASK_WAIT_SECONDS)

    assert (view.status, view.error_message) == (
        "failed",
        f"Stopped: the task ran longer than the limit of {TASK_LIMIT_SECONDS} seconds.",
    )
    assert "done anyway" not in view.answer()


def test_a_handler_that_returns_none_fails_the_task_instead_of_answering_none(robust):
    """docs/handlers.md: a handler answers with a str or a TaskResult. A forgotten return is a bug in the handler, so
    the task fails and the agent logs the TypeError; the user is not told "None"."""
    agent = robust.start(NONE_AGENT)
    view = robust.client.wait_done(robust.client.send(NONE_AGENT, "none task"), timeout=TASK_WAIT_SECONDS)

    assert view.status == "failed"
    assert "None" not in view.answer()
    assert "handler failed" in agent.log_tail() and "TypeError" in agent.log_tail()


def test_a_model_failed_step_a_handler_reports_gives_the_failed_task_its_error_category(robust):
    """ModelFailed(category="rate_limit") emitted by a handler that then raises a plain error: SAM's error for the
    task carries the category, as it does when the failure is read from a provider's exception."""
    robust.start(MODEL_FAIL_AGENT)
    view = robust.client.wait_done(robust.client.send(MODEL_FAIL_AGENT, "modelfail task"), timeout=TASK_WAIT_SECONDS)

    assert view.status == "failed"
    error = view.final()["error"]
    assert error["data"]["llm_error_category"] == "rate_limit"


def test_an_agent_that_joins_logs_one_line_saying_it_joined_agent_mesh_under_its_name(robust):
    agent = robust.start(JOINED_AGENT)
    log = agent.log_tail()

    assert log.count(f"joined Agent Mesh as {JOINED_AGENT}") == 1, log
    assert "INFO" in next(line for line in log.splitlines() if "joined Agent Mesh as" in line), log
