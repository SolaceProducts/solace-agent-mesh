# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""One agent process under load on a real SAM-Go: many tasks at once, of every kind, and the same again for minutes.

The agent is `tests/e2e/load_agent.py`, a minimal agent of the SDK's public API with max_concurrent_tasks 8. Each task
carries a token of its own and every answer and step echoes it, so an answer, a step or a stream that reaches the wrong
task shows in SAM's record of that task. A round sends LOAD_TASKS tasks at once: answers, tool calls, streamed answers,
failures and tasks the test cancels while they run or wait for a slot. After each round the test asserts on SAM's
records (every task ended, in the state its kind calls for, with its own answer, answered once, carrying no other
task's token), on what the agent reports (no handler ran twice, never more than 8 at once, nothing left in flight)
and on the agent process measured from outside with psutil (memory, open files and sockets, threads).

The load test runs LOAD_ROUNDS rounds (SAM_LOAD_ROUNDS, 6 by default) with the `e2e` marker. The soak test has the
`soak` marker, so neither the default run nor `-m e2e` selects it: it runs rounds for SAM_SOAK_SECONDS (60 by default)
and restarts SAM once half of a round's tasks have ended, every SAM_SOAK_RESTART_SECONDS (30 by default), so the agent's
link drops with tasks in flight. A restarted SAM has forgotten the tasks that were open: it refuses the agent's answers
to them and its gateway's idle timer fails them, and it ends once more, on that timer, a task whose answer it recorded
just before it stopped. Those count as lost or ended again by SAM, not as a problem; every other check holds across
the restart. Growth is measured from the round after the first restart, as the first reconnect allocates once. It prints each round and writes the report to SAM_SOAK_REPORT when that is set.
"""

from __future__ import annotations

import importlib.util
import itertools
import json
import os
import re
import secrets
import sys
import time
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from pathlib import Path

import pytest

from ..contract.sam_client import TERMINAL_STATES, SamClient, wait_until
from ..contract.sam_server import LinkAgent, SamServer, SamUnavailable, locate_binary
from .agent_process import AgentProcess
from .conftest import LISTED_TIMEOUT_SECONDS, unavailable

LOAD_AGENT_SCRIPT = str(Path(__file__).with_name("load_agent.py"))
AGENT = "e2e-load"
MAX_CONCURRENT = 8
LOAD_TASKS = int(os.environ.get("SAM_LOAD_TASKS") or 200)
LOAD_ROUNDS = int(os.environ.get("SAM_LOAD_ROUNDS") or 10)
SOAK_SECONDS = float(os.environ.get("SAM_SOAK_SECONDS") or 60)
SOAK_RESTART_SECONDS = float(os.environ.get("SAM_SOAK_RESTART_SECONDS") or 30)
SOAK_REPORT = os.environ.get("SAM_SOAK_REPORT", "")

MIX = ("echo",) * 8 + ("tool",) * 4 + ("stream",) * 3 + ("fail",) * 3 + ("hold",) * 2
"""Out of every 20 tasks: 8 answers, 4 tool calls, 3 streamed answers, 3 failures and 2 cancels."""
EXPECTED = {"echo": "completed", "tool": "completed", "stream": "completed", "fail": "failed", "hold": "canceled"}
TOKEN = re.compile(r"tok-[0-9a-f]{12}")
HOLD_CANCEL_AFTER_SECONDS = 2.0
"""A held task is cancelled once its handler runs, or after this long waiting for a slot, so both cancels happen."""
ROUND_TIMEOUT_SECONDS = 60
IN_FLIGHT_SETTLE_SECONDS = 15
HTTP_WORKERS = 32
GATEWAY_IDLE_SECONDS = 5
"""SAM forgets the tasks open when it restarts, and only its gateway's idle timer ends them (it cancels a task silent for
twice this long, with SAM_IDLE_FAILURE). No task of a round is silent that long otherwise, so outside a restart that
failure is a stall.
"""
SAM_IDLE_FAILURE = "The request timed out"
STATS_INTERVAL_SECONDS = 0.2
STATS_STALE_SECONDS = 3

OBJECTS_GROWTH_BOUND = 500
"""How far the agent process may move from the end of the baseline round (the second, see baseline()) to the end of the
last, measured with nothing in flight. The object count (what the garbage collector tracks) and the interpreter's
allocated blocks (strings and numbers too) are the precise ones: with no leak they move by up to about 300 and 400 over
20 rounds, so over eight rounds of 200 tasks a leak of one object for every third task, or one string for every task,
fails them. RSS on macOS moves by several MB either way as the OS compresses and reclaims pages, so its bound catches
only a large leak.
"""
BLOCKS_GROWTH_BOUND = 1000
RSS_GROWTH_BOUND_MB = 10.0
FDS_GROWTH_BOUND = 3
THREADS_GROWTH_BOUND = 2


@dataclass
class Task:
    kind: str
    token: str
    task_id: str = ""
    status: str = ""
    finals: int = 0
    sam_timeouts: int = 0
    answer: str = ""
    foreign: set[str] = field(default_factory=set)
    submit_error: str = ""
    error: str = ""


@dataclass
class Sample:
    rss_mb: float
    fds: int
    threads: int
    objects: int
    blocks: int


@dataclass
class Round:
    number: int
    tasks: list[Task]
    seconds: float
    sample: Sample
    stats: dict
    restarted: bool = False
    problems: list[str] = field(default_factory=list)
    statuses: Counter = field(default_factory=Counter)
    lost: int = 0
    ended_twice: int = 0
    before: Sample | None = None

    def _growth(self) -> str:
        if self.before is None:
            return ""
        rss, objects = self.sample.rss_mb - self.before.rss_mb, self.sample.objects - self.before.objects
        blocks = self.sample.blocks - self.before.blocks
        return f", since the round before: rss {rss:+.1f} MB, objects {objects:+d}, blocks {blocks:+d}"

    @property
    def per_second(self) -> float:
        return len(self.tasks) / self.seconds

    def line(self) -> str:
        restarted = (
            f" SAM restarted mid-round, {self.lost} tasks it lost, {self.ended_twice} it ended again"
            if self.restarted
            else ""
        )
        return (
            f"round {self.number}: {len(self.tasks)} tasks in {self.seconds:.1f} s ({self.per_second:.1f}/s), "
            f"rss {self.sample.rss_mb:.1f} MB, objects {self.sample.objects}, blocks {self.sample.blocks}, fds {self.sample.fds}, threads {self.sample.threads}, "
            f"{dict(sorted(self.statuses.items()))}, problems {len(self.problems)}{restarted}{self._growth()}"
        )


class LoadRig:
    """A SAM of its own and the load agent on it, driven from a pool of HTTP workers."""

    def __init__(self, server: SamServer, client: SamClient, root: Path):
        self.server, self.client, self.root = server, client, root
        self.stats_file = root / "load-stats.json"
        self.pool = ThreadPoolExecutor(HTTP_WORKERS, thread_name_prefix="load")
        self.agent: AgentProcess | None = None
        self.process = None

    def start_agent(self) -> None:
        import psutil

        env = {
            "SAM_LINK_URL": self.server.link_url,
            "SAM_LINK_TOKEN": self.server.token(AGENT),
            "SAM_AGENT_NAME": AGENT,
            "LOAD_MAX_CONCURRENT": str(MAX_CONCURRENT),
            "LOAD_STATS_FILE": str(self.stats_file),
            "LOAD_RECONNECT_SECONDS": "0.5",
            "LOAD_STATS_INTERVAL": str(STATS_INTERVAL_SECONDS),
            "LOAD_LOG_LEVEL": os.environ.get("LOAD_LOG_LEVEL", ""),
        }
        self.agent = AgentProcess(
            AGENT, LOAD_AGENT_SCRIPT, sys.executable, self.root, env, secrets=(self.server.token(AGENT),)
        )
        self.agent.start()
        self.process = psutil.Process(self.agent.process.pid)
        self.wait_listed()

    def wait_listed(self) -> None:
        try:
            self.client.wait_listed(AGENT, timeout=LISTED_TIMEOUT_SECONDS)
        except AssertionError as exc:
            raise AssertionError(f"{exc}\n{self.agent.log_tail()}") from None

    def stats(self) -> dict:
        try:
            return json.loads(self.stats_file.read_text())
        except (OSError, ValueError):
            return {}

    def sample(self, stats: dict) -> Sample:
        memory = self.process.memory_info().rss / 2**20
        objects, blocks = int(stats.get("objects", 0)), int(stats.get("blocks", 0))
        return Sample(memory, self.process.num_fds(), self.process.num_threads(), objects, blocks)

    def run_round(self, number: int, restart: bool = False) -> Round:
        """Send LOAD_TASKS tasks at once and follow each to its end. With `restart`, SAM is restarted once half of
        them have ended.

        The round ends by asking the agent to count its objects, which it does on its next write, with nothing in
        flight.
        """
        tasks = [Task(MIX[i % len(MIX)], f"tok-{secrets.token_hex(6)}") for i in range(LOAD_TASKS)]
        started = time.monotonic()
        list(self.pool.map(self._submit, tasks))
        submitted = time.monotonic()
        held = {t.token: t for t in tasks if t.kind == "hold" and t.task_id}
        pending = {t.task_id: t for t in tasks if t.task_id}
        restarted = False
        deadline = started + ROUND_TIMEOUT_SECONDS
        while (pending or held) and time.monotonic() < deadline:
            if restart and not restarted and len(pending) <= LOAD_TASKS // 2:
                self.server.restart()
                self.wait_listed()
                restarted = True
            running = set(self.stats().get("running", ()))
            due = [
                t
                for token, t in held.items()
                if token in running or time.monotonic() - submitted >= HOLD_CANCEL_AFTER_SECONDS
            ]
            for t in due:
                del held[t.token]
            list(self.pool.map(lambda t: self.client.cancel(t.task_id), due))
            done = [t for t in self.pool.map(self._poll, pending.values()) if t is not None]
            for t in done:
                del pending[t.task_id]
            if pending:
                time.sleep(0.2)
        seconds = time.monotonic() - started
        finished = [t for t in tasks if t.task_id and t.task_id not in pending]
        list(self.pool.map(self._read_events, finished))

        settled = self._wait_in_flight_zero()
        time.sleep(STATS_INTERVAL_SECONDS * 3)
        stats = self.stats()
        if time.time() - stats.get("at", 0) > STATS_STALE_SECONDS:
            settled = False
            stats["stale"] = True
        result = Round(number, tasks, seconds, self.sample(stats), stats, restarted)
        result.statuses = Counter(t.status or "unfinished" for t in tasks)
        if restarted:
            result.lost = sum(1 for t in tasks if _lost_by_sam(t))
            result.ended_twice = sum(1 for t in tasks if _ended_again_by_sam(t))
        result.problems = self._problems(tasks, stats, settled, restarted)
        return result

    def _submit(self, task: Task) -> None:
        try:
            task.task_id = self.client.send(AGENT, f"{task.kind} {task.token}")
        except Exception as exc:
            task.submit_error = f"{type(exc).__name__}: {exc}"[:300]

    def _poll(self, task: Task) -> Task | None:
        st = self.client.status(task.task_id) or {}
        status = str((st.get("task") or {}).get("status", ""))
        if not st.get("isRunning") and status in TERMINAL_STATES:
            task.status, task.error = status, str(st.get("errorMessage") or "")
            return task
        return None

    def _read_events(self, task: Task) -> None:
        def read() -> list[dict]:
            """The final event and the task row are written by separate paths: read until the final event is there."""
            entries = self.client.events(task.task_id)
            own = next((e for e in entries if e.get("taskId") == task.task_id), {})
            return entries if any(_is_final(e) for e in own.get("events") or []) else []

        try:
            entries = wait_until(read, 10, f"task {task.task_id}'s final event")
        except AssertionError:
            entries = self.client.events(task.task_id)
        own = next((e for e in entries if e.get("taskId") == task.task_id), {})
        finals = [e for e in own.get("events") or [] if _is_final(e)]
        task.finals = len(finals)
        task.sam_timeouts = sum(1 for e in finals if _is_sam_timeout(e))
        answers = [e for e in finals if not _is_sam_timeout(e)]
        if answers:
            result = answers[-1]["full_payload"].get("result") or {}
            parts = ((result.get("status") or {}).get("message") or {}).get("parts") or []
            task.answer = "".join(p.get("text", "") for p in parts if p.get("kind") == "text").strip()
        task.foreign = set(TOKEN.findall(json.dumps(entries))) - {task.token}

    def _wait_in_flight_zero(self) -> bool:
        try:
            wait_until(lambda: self.stats().get("in_flight") == 0, IN_FLIGHT_SETTLE_SECONDS, "in-flight count 0")
            return True
        except AssertionError:
            return False

    @staticmethod
    def _problems(tasks: list[Task], stats: dict, settled: bool, restarted: bool) -> list[str]:
        problems = []
        for t in tasks:
            where = f"{t.kind} task {t.task_id or '(not submitted)'} ({t.token})"
            if t.submit_error:
                problems.append(f"{where}: SAM refused it: {t.submit_error}")
            elif not t.status:
                problems.append(f"{where}: did not end within {ROUND_TIMEOUT_SECONDS} s")
            elif t.status != EXPECTED[t.kind] and not (restarted and _lost_by_sam(t)):
                problems.append(f"{where}: ended {t.status} ({t.error}), expected {EXPECTED[t.kind]}")
            elif t.finals != 1 and not (restarted and _ended_again_by_sam(t)):
                problems.append(f"{where}: SAM recorded {t.finals} final answers")
            elif t.status == "completed" and t.answer != f"answer {t.token}":
                problems.append(f"{where}: answered {t.answer!r}")
            if t.foreign:
                problems.append(f"{where}: carries other tasks' tokens {sorted(t.foreign)}")
        for token, runs in (stats.get("runs") or {}).items():
            problems.append(f"the handler ran {runs} times for {token}")
        if stats.get("peak_running", 0) > MAX_CONCURRENT:
            problems.append(f"{stats['peak_running']} handlers ran at once, over max_concurrent_tasks {MAX_CONCURRENT}")
        if stats.get("stale"):
            problems.append(f"the agent has not reported for over {STATS_STALE_SECONDS} s; its stats are stale")
        elif not settled:
            problems.append(f"the agent still holds {stats.get('in_flight')} tasks {IN_FLIGHT_SETTLE_SECONDS} s after")
        return problems


def _lost_by_sam(task: Task) -> bool:
    """SAM forgets the tasks open at its restart: the agent's answer to one is refused (`unknown_task`), and the
    restarted gateway fails it when its idle timer runs out."""
    return task.status == "failed" and SAM_IDLE_FAILURE in task.error


def _ended_again_by_sam(task: Task) -> bool:
    """A task the agent answered just before SAM stopped: the restarted gateway's idle timer ends it once more, so
    SAM's record holds the agent's answer and SAM's timeout."""
    return task.finals == 2 and task.sam_timeouts == 1


def _is_sam_timeout(event: dict) -> bool:
    error = (event.get("full_payload") or {}).get("error")
    return isinstance(error, dict) and SAM_IDLE_FAILURE in str(error.get("message", ""))


def _is_final(event: dict) -> bool:
    payload = event.get("full_payload")
    return event.get("direction") in ("task", "error") or (
        event.get("direction") != "request" and isinstance(payload, dict) and "error" in payload
    )


@pytest.fixture(scope="module")
def rig(tmp_path_factory):
    if importlib.util.find_spec("websockets") is None:
        unavailable("the agent link needs websockets")
    if importlib.util.find_spec("psutil") is None:
        unavailable("measuring the agent process needs psutil (uv sync installs the dev group)")
    try:
        binary = locate_binary()
    except SamUnavailable as exc:
        unavailable(f"no SAM-Go is available: {exc}")
    server = SamServer(
        binary, tmp_path_factory.mktemp("sam-load"), [LinkAgent(AGENT)], task_idle_seconds=GATEWAY_IDLE_SECONDS
    )
    try:
        server.start()
    except BaseException:
        server.stop()
        raise
    client = SamClient(server.base_url, server.binary, server.home, server.root / "user-home")
    running = LoadRig(server, client, tmp_path_factory.mktemp("agents-load"))
    try:
        running.start_agent()
        yield running
    finally:
        running.pool.shutdown(cancel_futures=True)
        if running.agent is not None:
            running.agent.stop()
        server.stop()


def baseline(rounds: list[Round]) -> Round:
    """The round growth is measured from: the second, as the first fills the bounded records of answered tasks (256,
    the SDK's and the load agent's own) only part way, or with a restart, the one after the first restart, as the
    agent's first reconnect allocates once (about 700 objects) what later ones reuse."""
    restart = next((i for i, r in enumerate(rounds) if r.restarted), None)
    start = 1 if restart is None else restart + 1
    return rounds[min(start, len(rounds) - 1)]


def report(rounds: list[Round], header: str) -> str:
    calm = [r for r in rounds if not r.restarted]
    start = baseline(rounds)
    first, last = start.sample, rounds[-1].sample
    lines = [
        header,
        *(r.line() for r in rounds),
        f"{sum(len(r.tasks) for r in rounds)} tasks in {len(rounds)} rounds, "
        f"{sum(len(r.tasks) for r in calm) / max(sum(r.seconds for r in calm), 1e-9):.1f} tasks/s in rounds without a "
        f"restart, {sum(r.lost for r in rounds)} lost and {sum(r.ended_twice for r in rounds)} ended again by SAM in "
        f"{len(rounds) - len(calm)} restarts; from the end of round {start.number} to the end of round {rounds[-1].number}: rss {last.rss_mb - first.rss_mb:+.1f} MB, objects "
        f"{last.objects - first.objects:+d}, blocks {last.blocks - first.blocks:+d}, fds {last.fds - first.fds:+d}, threads {last.threads - first.threads:+d}",
    ]
    for r in rounds:
        lines += [f"  round {r.number}: {p}" for p in r.problems[:20]]
    return "\n".join(lines)


def assert_bounded(rounds: list[Round], text: str) -> None:
    first, last = baseline(rounds).sample, rounds[-1].sample
    objects, rss = last.objects - first.objects, last.rss_mb - first.rss_mb
    fds, threads = last.fds - first.fds, last.threads - first.threads
    blocks = last.blocks - first.blocks
    assert objects <= OBJECTS_GROWTH_BOUND, f"the agent holds {objects} more objects after the baseline round\n{text}"
    assert blocks <= BLOCKS_GROWTH_BOUND, (
        f"the agent holds {blocks} more memory blocks after the baseline round\n{text}"
    )
    assert rss <= RSS_GROWTH_BOUND_MB, f"rss grew {rss:.1f} MB after the baseline round\n{text}"
    assert fds <= FDS_GROWTH_BOUND, f"open files grew by {fds} after the baseline round\n{text}"
    assert threads <= THREADS_GROWTH_BOUND, f"threads grew by {threads} after the baseline round\n{text}"


FAILED_ROUND_LOG_LINES = 200


def logged_around_a_failed_round(rig: LoadRig) -> str:
    """What SAM and the agent logged up to the end of a round with problems: SAM's warnings and errors since it
    started and the last lines of both logs, so a failure in CI can be told from the job's output alone."""
    sam = rig.server.log_text().splitlines()
    raised = [line for line in sam if '"level":"WARN"' in line or '"level":"ERROR"' in line]
    return "\n".join(
        [
            f"SAM's warnings and errors (the last {FAILED_ROUND_LOG_LINES} of {len(raised)}):",
            *raised[-FAILED_ROUND_LOG_LINES:],
            f"SAM's log, its last {FAILED_ROUND_LOG_LINES} lines:",
            *sam[-FAILED_ROUND_LOG_LINES:],
            "the agent's log, its last lines:",
            rig.agent.log_tail() if rig.agent is not None else "(no agent)",
        ]
    )


def test_one_agent_given_many_tasks_at_once_answers_each_once_with_its_own_answer_and_holds_nothing_after(rig, capsys):
    rounds: list[Round] = []
    logged = ""
    for number in range(1, LOAD_ROUNDS + 1):
        rounds.append(rig.run_round(number))
        if len(rounds) > 1:
            rounds[-1].before = rounds[-2].sample
        if rounds[-1].problems and not logged:
            logged = logged_around_a_failed_round(rig)
    text = report(rounds, f"load: {LOAD_TASKS} tasks a round, max_concurrent_tasks {MAX_CONCURRENT}")
    with capsys.disabled():
        print(f"\n{text}")

    assert [p for r in rounds for p in r.problems] == [], f"{text}\n{logged}"
    cancelled = [0, *(r.stats.get("cancelled", 0) for r in rounds)]
    assert all(b > a for a, b in itertools.pairwise(cancelled)), f"a round cancelled no handler mid-run\n{text}"
    assert_bounded(rounds, text)
    assert rig.agent.alive(), rig.agent.log_tail()


@pytest.mark.soak
def test_one_agent_under_load_for_minutes_while_sam_restarts_answers_each_task_once_and_does_not_grow(rig, capsys):
    rounds: list[Round] = []
    started = last_restart = time.monotonic()
    number = 0
    while time.monotonic() - started < SOAK_SECONDS:
        number += 1
        restart = time.monotonic() - last_restart >= SOAK_RESTART_SECONDS
        rounds.append(rig.run_round(number, restart=restart))
        if len(rounds) > 1:
            rounds[-1].before = rounds[-2].sample
        if restart:
            last_restart = time.monotonic()
        with capsys.disabled():
            print(rounds[-1].line(), flush=True)
    text = report(rounds, f"soak: {SOAK_SECONDS:g} s, SAM restarted every {SOAK_RESTART_SECONDS:g} s")
    if SOAK_REPORT:
        Path(SOAK_REPORT).write_text(text + "\n")

    assert [p for r in rounds for p in r.problems] == [], text
    assert_bounded(rounds, text)
    assert rig.agent.alive(), rig.agent.log_tail()
