# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Behaviour that belongs to the SDK core, not to an adapter: keepalive, shutdown, the task timeout, delegation
limits, prompts raised by a called agent, waking a request-driven agent and the token usage a handler without a
framework reports. One framework is enough (LangChain, the example with the fewest moving parts), so each promise has
one test, on a SAM of its own; the handler without a framework is tests/e2e/robust_agent.py.

That SAM (not the session's) has short gateway idle timers, so the keepalive promise shows in seconds, and its
agents are started here, so a test may stop one without breaking another test's.
"""

from __future__ import annotations

import importlib.util
import json
import signal
import sys
from pathlib import Path

import pytest

from ..contract.sam_client import SamClient, wait_until
from ..contract.sam_server import TOOL_SCOPE_HELD, LinkAgent, SamServer, SamUnavailable, free_port, locate_binary
from .agent_process import EXAMPLES, MODEL_API_KEY, AgentProcess
from .conftest import LISTED_TIMEOUT_SECONDS, unavailable
from .model_script import ScriptedModel, Turn
from .test_examples_on_agent_mesh import (
    ANSWER,
    PROMPT_TIMEOUT_SECONDS,
    QUIET_AFTER_STOP_SECONDS,
    TASK_TIMEOUT_SECONDS,
    TICKET,
    steps,
    triage_turns,
)

FRAMEWORK = "langchain"
EXAMPLE = f"claims_triage_{FRAMEWORK}.py"
GATEWAY_IDLE_SECONDS = 2
SLOW_TOOL_SECONDS = 4 * GATEWAY_IDLE_SECONDS
KEEPALIVE_SECONDS = 1
SLOW_ASK = "Run a fraud check on P-1182"
STOP_WAIT_SECONDS = 30
TASK_LIMIT_SECONDS = 3
TASK_LIMIT_TOOL_SECONDS = 30
PEER_TOOL_SECONDS = 3

KEEPALIVE_AGENT, DRAIN_AGENT, TIMEOUT_AGENT = "e2e-core-keepalive", "e2e-core-drain", "e2e-core-timeout"
ABANDON_AGENT, CALLER, CALLEE, APPROVER, WAKE_AGENT = (
    "e2e-core-abandon",
    "e2e-core-caller",
    "e2e-core-callee",
    "e2e-core-approver",
    "e2e-core-wake",
)
PING, PONG = "e2e-core-ping", "e2e-core-pong"
FRAMEWORKLESS_AGENT = "e2e-core-frameworkless"
ROBUST_AGENT = str(Path(__file__).with_name("robust_agent.py"))


class Mesh:
    """A SAM with short gateway idle timers, and the example agents started on it."""

    def __init__(self, server: SamServer, client: SamClient, root):
        self.server, self.client, self.root = server, client, root
        self.runtime_port = 0
        self._caller: ScriptedModel | None = None
        self.started_processes: list[AgentProcess] = []
        self.started: list[tuple[AgentProcess, ScriptedModel]] = []

    def start_agent(self, name: str, model: ScriptedModel, **settings: str) -> AgentProcess:
        env = {
            "SAM_LINK_URL": self.server.link_url,
            "SAM_LINK_TOKEN": self.server.token(name),
            "SAM_AGENT_NAME": name,
            "CLAIMS_LLM_BASE_URL": model.base_url,
            "LLM_SERVICE_API_KEY": MODEL_API_KEY,
            "CLAIMS_LLM_MODEL": "fake-model",
            "CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD,
            **settings,
        }
        process = AgentProcess(
            name, EXAMPLE, sys.executable, self.root, env, secrets=(self.server.token(name), MODEL_API_KEY)
        )
        self.started.append((process, model))
        process.start()
        try:
            self.client.wait_listed(name, timeout=LISTED_TIMEOUT_SECONDS)
        except AssertionError as exc:
            raise AssertionError(f"{exc}\n{process.log_tail()}") from None
        return process

    def start_frameworkless(self, name: str) -> AgentProcess:
        """robust_agent.py: a handler on the SDK's public API, no framework and no model."""
        env = {"SAM_LINK_URL": self.server.link_url, "SAM_LINK_TOKEN": self.server.token(name), "SAM_AGENT_NAME": name}
        process = AgentProcess(name, ROBUST_AGENT, sys.executable, self.root, env, secrets=(self.server.token(name),))
        self.started_processes.append(process)
        process.start()
        try:
            self.client.wait_listed(name, timeout=LISTED_TIMEOUT_SECONDS)
        except AssertionError as exc:
            raise AssertionError(f"{exc}\n{process.log_tail()}") from None
        return process

    def caller(self) -> ScriptedModel:
        """The agent that delegates to the callee and the approver, started once; each test scripts its model."""
        if self._caller is None:
            model = ScriptedModel()
            self.start_agent(CALLER, model, CLAIMS_PEER_AGENTS="*")
            self._caller = model
        return self._caller


def peer_tool(name: str) -> str:
    return f"peer_{name}"


@pytest.fixture(scope="module")
def mesh(tmp_path_factory):
    if importlib.util.find_spec("langchain_openai") is None:
        unavailable("the langchain example needs langchain_openai (uv sync installs the dev group)")
    try:
        binary = locate_binary()
    except SamUnavailable as exc:
        unavailable(f"no SAM-Go is available: {exc}")
    names = (KEEPALIVE_AGENT, DRAIN_AGENT, TIMEOUT_AGENT, ABANDON_AGENT, CALLEE, APPROVER, FRAMEWORKLESS_AGENT)
    runtime_port = free_port()
    agents = [
        *(LinkAgent(n) for n in names),
        LinkAgent(CALLER, (CALLEE, APPROVER)),
        LinkAgent(PING, (PONG,)),
        LinkAgent(PONG, (PING,)),
        LinkAgent(WAKE_AGENT, wake_url=f"http://127.0.0.1:{runtime_port}/invocations"),
    ]
    server = SamServer(binary, tmp_path_factory.mktemp("sam-core"), agents, task_idle_seconds=GATEWAY_IDLE_SECONDS)
    try:
        server.start()
    except BaseException:
        server.stop()
        raise
    client = SamClient(server.base_url, server.binary, server.home, server.root / "user-home")
    running = Mesh(server, client, tmp_path_factory.mktemp("agents-core"))
    running.runtime_port = runtime_port
    try:
        yield running
    finally:
        for process, model in running.started:
            process.stop()
            model.close()
        for process in running.started_processes:
            process.stop()
        server.stop()


def slow_tool_turns() -> list[Turn]:
    return [
        Turn(calls=(("call_f", "fraud_screen", {"policy_id": "P-1182"}),)),
        Turn(text="P-1182 screened as low risk.", expects=("call_f",)),
    ]


def test_the_tokens_a_handler_without_a_framework_reports_on_its_model_step_reach_agent_mesh_cache_reads_and_writes_included(
    mesh,
):
    """docs/handlers.md: the handler reports its model call as ModelFinished(usage=Usage(...)) in Usage's convention
    (300 input tokens holding 100 cache reads and 40 cache writes); emitting the step records the tokens, and SAM files
    them with the call in its own: cached input the reads and the writes together, the writes again as cache writes."""
    mesh.start_frameworkless(FRAMEWORKLESS_AGENT)
    view = mesh.client.wait_done(
        mesh.client.send(FRAMEWORKLESS_AGENT, "usage of one model call"), timeout=TASK_TIMEOUT_SECONDS
    )

    assert (view.status, view.error_message) == ("completed", None)
    reported = [
        {key: value for key, value in s["usage"].items() if key != "model"}
        for s in view.signals()
        if s.get("type") == "llm_response"
    ]
    assert reported == [
        {
            "input_tokens": 300,
            "output_tokens": 20,
            "cached_input_tokens": 140,
            "cache_write_tokens": 40,
            "reasoning_tokens": 5,
        }
    ]
    assert (view.record["totalInputTokens"], view.record["totalOutputTokens"]) == (300, 20)


def test_a_task_silent_for_longer_than_the_gateways_idle_timeout_stays_alive_and_completes(mesh):
    """The gateway cancels a task silent for twice GATEWAY_IDLE_SECONDS (SAM-Go's own windows are 300 s and 3600 s).
    The agent's keepalive reports "Still working" while the slow tool runs, which is what keeps the task open."""
    model = ScriptedModel()
    mesh.start_agent(
        KEEPALIVE_AGENT,
        model,
        CLAIMS_FRAUD_SCREEN_SECONDS=str(SLOW_TOOL_SECONDS),
        CLAIMS_KEEPALIVE_SECONDS=str(KEEPALIVE_SECONDS),
    )
    model.script(*slow_tool_turns())
    view = mesh.client.wait_done(mesh.client.send(KEEPALIVE_AGENT, SLOW_ASK), timeout=TASK_TIMEOUT_SECONDS)

    assert model.violations == []
    assert (view.status, view.error_message) == ("completed", None)
    assert view.answer().strip() == "P-1182 screened as low risk."
    assert len(steps(view, "tool_result", "fraud_screen")) == 1
    kept_alive = [s for s in view.signals() if s.get("status_text") == "Still working"]
    assert len(kept_alive) >= SLOW_TOOL_SECONDS // 2 - 1, kept_alive


def test_an_agent_stopped_while_a_task_runs_leaves_the_mesh_and_finishes_the_task_within_its_grace(mesh):
    """docs/behaviour.md: on SIGTERM the agent retires, so no new work is routed to it, and running tasks finish for
    up to shutdown_grace_seconds. The task's tool result still reaches the model and the task completes."""
    model = ScriptedModel()
    agent = mesh.start_agent(
        DRAIN_AGENT,
        model,
        CLAIMS_FRAUD_SCREEN_SECONDS=str(SLOW_TOOL_SECONDS),
        CLAIMS_KEEPALIVE_SECONDS=str(KEEPALIVE_SECONDS),
    )
    model.script(*slow_tool_turns())
    task_id = mesh.client.send(DRAIN_AGENT, SLOW_ASK)
    wait_until(
        lambda: "tool_invocation_start" in json.dumps(mesh.client.events(task_id)),
        TASK_TIMEOUT_SECONDS,
        "the fraud screen to start",
    )

    agent.process.send_signal(signal.SIGTERM)
    mesh.client.wait_delisted(DRAIN_AGENT)
    assert mesh.client.status(task_id)["isRunning"], "the task ended before the agent had finished retiring"
    view = mesh.client.wait_done(task_id, timeout=TASK_TIMEOUT_SECONDS)
    exited = agent.process.wait(timeout=STOP_WAIT_SECONDS)

    assert model.violations == []
    assert (view.status, view.error_message) == ("completed", None)
    assert view.answer().strip() == "P-1182 screened as low risk."
    assert "P-1182" in model.requests[1].tool_results()["call_f"]
    assert exited == 0, agent.log_tail()


def test_a_task_that_runs_longer_than_the_task_timeout_fails_saying_so_and_the_tools_result_never_reaches_the_model(
    mesh,
):
    """The keepalive is on, so the gateway's idle timers do not end the task: only the agent's own limit can."""
    model = ScriptedModel()
    mesh.start_agent(
        TIMEOUT_AGENT,
        model,
        CLAIMS_FRAUD_SCREEN_SECONDS=str(TASK_LIMIT_TOOL_SECONDS),
        CLAIMS_KEEPALIVE_SECONDS=str(KEEPALIVE_SECONDS),
        CLAIMS_TASK_TIMEOUT_SECONDS=str(TASK_LIMIT_SECONDS),
    )
    model.script(*slow_tool_turns())
    view = mesh.client.wait_done(mesh.client.send(TIMEOUT_AGENT, SLOW_ASK), timeout=TASK_TIMEOUT_SECONDS)

    assert (view.status, view.error_message) == (
        "failed",
        f"Stopped: the task ran longer than the limit of {TASK_LIMIT_SECONDS} seconds.",
    )
    assert model.no_request_beyond(1, within=3), f"the model was called again: {len(model.requests)} requests"
    assert steps(view, "tool_result", "fraud_screen") == []


def delegated(text: str, call_id: str, to: str) -> Turn:
    return Turn(calls=((call_id, peer_tool(to), {"task_description": text}),))


def sub_task_of(view, agent: str) -> str:
    [sub] = [e["taskId"] for e in view.entries if (e.get("subject") or {}).get("name") == agent]
    return sub


def test_cancelling_a_task_cancels_the_sub_task_it_sent_to_a_peer_and_the_peer_does_no_more_work(mesh):
    """docs/delegation.md: when the task ends, its open peer calls are cancelled. The peer's fraud screen is short,
    so a peer that kept running would call its model again before QUIET_AFTER_STOP_SECONDS is over. The cancel is
    protected twice: the SDK sends `peer_cancel` when the call is abandoned, and the entrypoint cancels the sub-task
    when the parent's result arrives, so the test still passes with the SDK's send removed."""
    callee_model = ScriptedModel()
    mesh.start_agent(CALLEE, callee_model, CLAIMS_FRAUD_SCREEN_SECONDS=str(PEER_TOOL_SECONDS))
    caller_model = mesh.caller()
    caller_model.script(delegated(SLOW_ASK, "p0", CALLEE), Turn(text="screened", expects=("p0",)))
    callee_model.script(*slow_tool_turns())
    task_id = mesh.client.send(CALLER, "Ask the callee to screen P-1182")
    wait_until(
        lambda: "tool_invocation_start" in json.dumps(mesh.client.events(task_id)),
        TASK_TIMEOUT_SECONDS,
        "the callee's fraud screen to start",
    )

    cancel = mesh.client.cancel(task_id)
    assert cancel.status in (200, 202, 204), cancel
    view = mesh.client.wait_done(task_id, timeout=TASK_TIMEOUT_SECONDS)
    sub = sub_task_of(view, CALLEE)
    wait_until(
        lambda: (mesh.client.status(sub) or {}).get("task", {}).get("status") in ("canceled", "cancelled"),
        TASK_TIMEOUT_SECONDS,
        "the callee's sub-task to end canceled",
    )

    assert view.status in ("canceled", "cancelled"), view.error_message
    assert callee_model.no_request_beyond(1, within=QUIET_AFTER_STOP_SECONDS), (
        f"the callee called its model again after the cancel: {len(callee_model.requests)} requests"
    )
    assert len(caller_model.requests) == 1
    assert steps(view, "tool_result", "fraud_screen") == []


def test_a_call_chain_deeper_than_max_call_depth_is_refused_and_the_agent_that_would_run_it_never_calls_its_model(mesh):
    """Two agents that may call each other, scripted to call on at every turn: the chain is ping (depth 0), pong (1),
    ping (2), pong (3), and the call from pong to ping would run at depth 4, over the default limit of 3. The
    refusal is the SDK's; SAM hands the caller only its own "internal error" for it, so the reason is in ping's log."""
    ping_model, pong_model = ScriptedModel(), ScriptedModel()
    ping = mesh.start_agent(PING, ping_model, CLAIMS_PEER_AGENTS=PONG)
    mesh.start_agent(PONG, pong_model, CLAIMS_PEER_AGENTS=PING)
    ping_model.script(
        delegated("d0", "a0", PONG),
        delegated("d2", "a2", PONG),
        Turn(text="ping at depth 2 done", expects=("a2",)),
        Turn(text="ping at depth 0 done", expects=("a0",)),
    )
    pong_model.script(
        delegated("d1", "b1", PING),
        delegated("d3", "b3", PING),
        Turn(text="pong at depth 3 done", expects=("b3",)),
        Turn(text="pong at depth 1 done", expects=("b1",)),
    )
    view = mesh.client.wait_done(mesh.client.send(PING, "Start the chain"), timeout=TASK_TIMEOUT_SECONDS)

    assert ping_model.violations == [] and pong_model.violations == []
    assert (view.status, view.answer().strip()) == ("completed", "ping at depth 0 done")
    assert len(ping_model.requests) == 4 and len(pong_model.requests) == 4
    refused = pong_model.requests[2].tool_results()["b3"]
    assert refused.startswith(f'peer agent "{PING}":'), refused
    assert "ping at depth 2 done" not in refused
    assert len(view.entries) == 5, [e["initialRequestText"][-2:] for e in view.entries]
    [too_deep] = [e for e in view.entries if e["initialRequestText"].endswith("d3")]
    assert too_deep["events"][-1]["direction"] == "error_response"
    assert "call_depth=4 max=3" in ping.log_tail()


def test_a_prompt_raised_by_a_called_agent_reaches_the_users_own_task_and_the_called_agent_runs_the_tool_once_answered(
    mesh, tmp_path
):
    """docs/handlers.md: a prompt raised by an agent another agent called goes to the user's own chat and is answered
    by request id. The user sent the task to the caller and never addressed the approver."""
    approver_model = ScriptedModel()
    mesh.start_agent(APPROVER, approver_model, CLAIMS_APPROVAL_TOOLS="route_claim", CLAIMS_ASK_USER="true")
    caller_model = mesh.caller()
    caller_model.script(delegated("Triage claim P-1182", "p0", APPROVER), Turn(text="Routed.", expects=("p0",)))
    approver_model.script(*triage_turns())
    asked = mesh.client.cli_send(CALLER, "Have the approver triage P-1182", tmp_path / "ask", PROMPT_TIMEOUT_SECONDS)
    [prompt_file] = sorted((tmp_path / "ask").glob("hil_request_*.json"))
    prompt = json.loads(prompt_file.read_text())
    task_id = prompt["task_id"]

    assert prompt["source"] == "tool_approval", asked.stdout[-2000:]
    assert prompt["agent_name"] == CALLER
    assert APPROVER in json.dumps(prompt) and "route_claim" in json.dumps(prompt)
    [own] = [e for e in mesh.client.events(task_id) if e.get("taskId") == task_id]
    assert own["initialRequestText"] == "Have the approver triage P-1182"
    assert (mesh.client.status(task_id) or {}).get("isRunning") is True
    assert len(approver_model.requests) == 2
    assert TICKET not in approver_model.all_text() + json.dumps(mesh.client.events(task_id))

    answered = mesh.client.cli_answer_approval(prompt_file, True, tmp_path / "answer", PROMPT_TIMEOUT_SECONDS)
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    view = mesh.client.wait_done(task_id, timeout=TASK_TIMEOUT_SECONDS)

    assert approver_model.violations == [] and caller_model.violations == []
    assert (view.status, view.answer().strip()) == ("completed", "Routed.")
    assert len(approver_model.requests) == 3
    assert TICKET in approver_model.requests[2].tool_results()["call_b"]
    assert caller_model.requests[1].tool_results()["p0"] == ANSWER
    [ticket] = steps(view, "tool_result", "route_claim")
    assert TICKET in json.dumps(ticket["result_data"])


def test_sam_wakes_a_request_driven_agent_that_is_listed_while_asleep_and_the_woken_process_serves_the_task(mesh):
    """examples/serverless: the agent is announced once and nothing of it runs. SAM lists it from the announcement,
    calls the runtime when a task arrives, and the process the runtime starts connects, serves the task and is not the
    process that announced. Not covered here: that a stopped agent is not woken, because the pinned SAM-Go has no
    route to stop an agent (docs/contributing/testing.md)."""
    link = {
        "SAM_LINK_URL": mesh.server.link_url,
        "SAM_LINK_TOKEN": mesh.server.token(WAKE_AGENT),
        "SAM_AGENT_NAME": WAKE_AGENT,
    }
    echo = str(EXAMPLES / "serverless" / "echo_app.py")
    secrets = (link["SAM_LINK_TOKEN"],)
    announce = AgentProcess(
        "wake-announce", "serverless/echo_app.py", sys.executable, mesh.root, link, secrets=secrets, args=("announce",)
    )
    announce.start()
    assert announce.process.wait(timeout=TASK_TIMEOUT_SECONDS) == 0, announce.log_tail()
    card = mesh.client.wait_listed(WAKE_AGENT)
    assert any(x["uri"].endswith("/sam/on-demand") for x in card["capabilities"]["extensions"])

    runtime = AgentProcess(
        "wake-runtime",
        "serverless/runtime_standin.py",
        sys.executable,
        mesh.root,
        link,
        secrets=secrets,
        args=(echo, "--port", str(mesh.runtime_port)),
    )
    mesh.started_processes.append(runtime)
    runtime.start()
    wait_until(
        lambda: "runtime listening" in runtime.log_tail(), TASK_TIMEOUT_SECONDS, "the runtime stand-in to listen"
    )
    assert "wake: started" not in runtime.log_tail()
    view = mesh.client.wait_done(mesh.client.send(WAKE_AGENT, "hello"), timeout=TASK_TIMEOUT_SECONDS)

    assert (view.status, view.error_message) == ("completed", None)
    woken = view.answer().strip()
    assert woken.startswith("echo from a process started for this request (pid ") and woken.endswith("): hello")
    pid = woken.split("pid ")[1].split(")")[0]
    assert runtime.log_tail().count("wake: started agent process") == 1
    assert f"pid={pid}" in runtime.log_tail()
    assert pid != str(announce.process.pid)
