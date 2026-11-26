# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A link agent registered on the platform is a native agent to every platform surface: it is created and deployed
through the management API, joins as the instanceName the platform gave it, and its tasks reach the same places a
native agent's do (spend ledger and cost, activity, feedback, budgets), and the platform's own
controls (edit and redeploy, disable, enable) act on it.

The other end-to-end tests configure their agents in SAM's agent_link file; here the only link agents the tests talk to
are the ones the API creates, on an agent_link entrypoint the API creates and deploys. One framework is enough
(LangChain, as in test_core_behaviour.py): what is under test is the platform, not the adapter. The model is scripted,
so the cost each test expects is exact.
"""

from __future__ import annotations

import contextlib
import importlib.util
import secrets
import sys
from collections.abc import Iterator
from dataclasses import dataclass
from decimal import Decimal

import pytest

from sam_link_sdk.cli import link_token

from ..contract.sam_client import SamClient, wait_until
from ..contract.sam_server import TOOL_SCOPE_HELD, LinkAgent, SamServer, SamUnavailable, locate_binary
from .agent_process import MODEL_API_KEY, AgentProcess
from .conftest import LISTED_TIMEOUT_SECONDS, unavailable
from .model_script import ScriptedModel
from .test_examples_on_agent_mesh import ANSWER, ASK, TASK_TIMEOUT_SECONDS, TICKET, recorded, steps, triage_turns

EXAMPLE = "claims_triage_langchain.py"
MODEL = "fake-model"
SLUG = "parity-link"
PRICED_ALIAS = "parity-priced"
PRICE_INPUT, PRICE_OUTPUT = Decimal("1"), Decimal("5")
"""Dollars per million input and output tokens of the model configuration the priced agent's pricing key names."""
PER_MILLION = Decimal(1_000_000)
MESH_BUDGET_ID = "00000000-0000-0000-0000-00000000b001"
"""The platform's default mesh budget, which every deployment has."""
USED_UP_BUDGET = "0"
BUDGET_REFUSED = -32029
SPEND_ROLLUP_WAIT_SECONDS = 120
"""The usage API reads rollups the platform refreshes about once a minute."""
DEPLOY_WAIT_SECONDS = 30
REFUSED_WAIT_SECONDS = 30
PLACEHOLDER_AGENT = "e2e-parity-placeholder"
"""SAM's agent_link file must name an agent; none of the tests use this one."""


@dataclass
class Registered:
    """A link agent the platform API created: its id, its name on the mesh and its display name."""

    id: str
    instance_name: str
    name: str
    token: str


class Platform:
    """A SAM whose link agents come from its management API, and the example agents started against them."""

    def __init__(self, server: SamServer, client: SamClient, root):
        self.server, self.client, self.root = server, client, root
        self.entrypoint_id = ""
        self.started: list[tuple[AgentProcess, ScriptedModel]] = []

    @property
    def link_url(self) -> str:
        return f"ws://127.0.0.1:{self.server.gateway_port}/gw/{SLUG}/v1/connect"

    def api(self, method: str, path: str, body=None, expect: tuple[int, ...] = (200, 201)):
        resp = self.client.request(method, path, body)
        assert resp.status in expect, f"{method} {path}: HTTP {resp.status}: {str(resp.body)[:800]}"
        body = resp.body
        return body.get("data", body) if isinstance(body, dict) else body

    def deploy_entrypoint(self) -> None:
        created = self.api(
            "POST",
            "/api/v2/platform/entrypoints",
            {
                "name": "Parity Link",
                "slug": SLUG,
                "description": "The agent link entrypoint the platform parity tests register agents on.",
                "entrypointType": "agent_link",
            },
        )
        self.entrypoint_id = created["id"]
        self.api(
            "POST", "/api/v2/platform/entrypointDeployments", {"entrypointId": self.entrypoint_id, "action": "deploy"}
        )
        wait_until(
            lambda: (
                self.api("GET", f"/api/v2/platform/entrypoints/{self.entrypoint_id}")["deploymentStatus"] == "deployed"
            ),
            DEPLOY_WAIT_SECONDS,
            "the agent_link entrypoint to be deployed",
        )

    def register(self, name: str) -> Registered:
        """Create a link agent with a token issued as an operator would (`sam-link-sdk link-token`), then deploy it."""
        path = self.root / "tokens" / f"{secrets.token_hex(4)}.link-token"
        path.parent.mkdir(mode=0o700, exist_ok=True)
        digest = link_token(path)
        token = path.read_text().strip()
        self.server._secrets.append(token)
        created = self.api(
            "POST",
            "/api/v2/platform/linkAgents",
            {
                "name": name,
                "description": "Claims triage, registered through the platform API",
                "entrypointId": self.entrypoint_id,
                "tokenSha256": digest,
                "owner": "alice@example.com",
                "team": "Claims",
            },
        )
        agent = Registered(created["id"], created["instanceName"], name, token)
        self.deploy(agent, "deploy")
        return agent

    def deploy(self, agent: Registered, action: str) -> None:
        self.api("POST", "/api/v2/platform/agentDeployments", {"agentId": agent.id, "action": action})
        wait_until(
            lambda: (
                self.link_agent(agent)["deploymentStatus"] == "deployed"
                and self.link_agent(agent).get("syncStatus") in (None, "in_sync")
            ),
            DEPLOY_WAIT_SECONDS,
            f"{agent.name} to be deployed",
        )

    def link_agent(self, agent: Registered) -> dict:
        return self.api("GET", f"/api/v2/platform/linkAgents/{agent.id}")

    def replace(self, agent: Registered, **changes) -> dict:
        body = {
            "name": agent.name,
            "description": "Claims triage, registered through the platform API",
            "entrypointId": self.entrypoint_id,
            "owner": "alice@example.com",
            "team": "Claims",
            **changes,
        }
        return self.api("PUT", f"/api/v2/platform/linkAgents/{agent.id}", body)

    def start(self, agent: Registered, model: ScriptedModel, **settings: str) -> AgentProcess:
        env = {
            "SAM_LINK_URL": self.link_url,
            "SAM_LINK_TOKEN": agent.token,
            "SAM_AGENT_NAME": agent.instance_name,
            "SAM_DISPLAY_NAME": agent.name,
            "CLAIMS_LLM_BASE_URL": model.base_url,
            "LLM_SERVICE_API_KEY": MODEL_API_KEY,
            "CLAIMS_LLM_MODEL": MODEL,
            "CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD,
            **settings,
        }
        process = AgentProcess(
            agent.instance_name, EXAMPLE, sys.executable, self.root, env, secrets=(agent.token, MODEL_API_KEY)
        )
        self.started.append((process, model))
        process.start()
        try:
            self.client.wait_listed(agent.instance_name, timeout=LISTED_TIMEOUT_SECONDS)
        except AssertionError as exc:
            raise AssertionError(f"{exc}\n{process.log_tail()}") from None
        return process

    def triage(self, agent: Registered, model: ScriptedModel):
        model.script(*triage_turns())
        task_id = self.client.send(agent.instance_name, ASK)
        return self.client.wait_done(task_id, timeout=TASK_TIMEOUT_SECONDS)

    def agent_usage(self, agent: Registered) -> dict | None:
        rows = self.api("GET", "/api/v2/platform/usage/agents")
        return next((row for row in rows if row.get("id") == agent.id), None)


@dataclass
class Running:
    agent: Registered
    model: ScriptedModel
    process: AgentProcess | None


@contextlib.contextmanager
def running_platform(tmp_path_factory, env: dict[str, str] | None = None) -> Iterator[Platform]:
    """A SAM started with env, its agent_link entrypoint deployed and the priced model configured; every agent
    process started on it is stopped on exit."""
    if importlib.util.find_spec("langchain_openai") is None:
        unavailable("the langchain example needs langchain_openai (uv sync installs the dev group)")
    try:
        binary = locate_binary()
    except SamUnavailable as exc:
        unavailable(f"no SAM-Go is available: {exc}")
    server = SamServer(
        binary,
        tmp_path_factory.mktemp("sam-platform"),
        [LinkAgent(PLACEHOLDER_AGENT)],
        env=env or {},
    )
    try:
        server.start()
    except BaseException:
        server.stop()
        raise
    client = SamClient(server.base_url, server.binary, server.home, server.root / "user-home")
    running = Platform(server, client, tmp_path_factory.mktemp("agents-platform"))
    try:
        running.deploy_entrypoint()
        running.api(
            "POST",
            "/api/v2/platform/models",
            {
                "alias": PRICED_ALIAS,
                "provider": "openai",
                "modelName": MODEL,
                "apiBase": "http://127.0.0.1:9/v1",
                "authType": "apikey",
                "authConfig": {"type": "apikey", "api_key": "placeholder"},
                "pricing": {"input": str(PRICE_INPUT), "output": str(PRICE_OUTPUT)},
            },
        )
        yield running
    finally:
        for process, model in running.started:
            process.stop()
            model.close()
        server.stop()


@pytest.fixture(scope="module")
def platform(tmp_path_factory):
    with running_platform(tmp_path_factory) as running:
        yield running


@pytest.fixture(scope="module")
def priced(platform) -> Running:
    """A link agent whose pricing key names the priced model configuration, its process not started yet."""
    agent = platform.register("Parity Claims")
    return Running(agent, ScriptedModel(), None)


@pytest.fixture(scope="module")
def priced_task(platform, priced):
    """The priced agent's one triage task, run once for the tests that read where it was recorded."""
    if priced.process is None:
        priced.process = platform.start(priced.agent, priced.model, CLAIMS_PRICING_KEY=PRICED_ALIAS)
    view = platform.triage(priced.agent, priced.model)
    assert view.status == "completed", priced.process.log_tail()
    return view


def triage_cost() -> tuple[int, int, Decimal]:
    """The tokens the scripted triage reports over its three model calls, and what they cost at the priced model's
    rates."""
    turns = triage_turns()
    tokens_in = sum(t.usage[0] for t in turns)
    tokens_out = sum(t.usage[1] for t in turns)
    return tokens_in, tokens_out, (tokens_in * PRICE_INPUT + tokens_out * PRICE_OUTPUT) / PER_MILLION


def test_a_registered_link_agent_reads_disconnected_until_its_process_joins_as_its_instance_name_then_running(
    platform, priced
):
    record = platform.link_agent(priced.agent)
    assert record["instanceName"] == priced.agent.instance_name
    assert record["connectPath"] == f"/gw/{SLUG}/v1/connect"
    assert record["deploymentStatus"] == "deployed"
    assert record["runtimeStatus"]["status"] == "disconnected"

    priced.process = platform.start(priced.agent, priced.model, CLAIMS_PRICING_KEY=PRICED_ALIAS)

    wait_until(
        lambda: platform.link_agent(priced.agent)["runtimeStatus"]["status"] == "running",
        DEPLOY_WAIT_SECONDS,
        "the link agent to read running",
    )
    assert platform.client.agent_card(priced.agent.instance_name) is not None


def test_its_task_completes_through_its_tools_as_a_native_agents_does(priced_task):
    assert [s["tool_name"] for s in priced_task.signals() if s.get("type") == "tool_invocation_start"] == [
        "lookup_policy",
        "route_claim",
    ]
    assert steps(priced_task, "tool_result", "route_claim")
    assert TICKET in recorded(priced_task)
    assert priced_task.answer() == ANSWER


def test_its_model_calls_are_priced_from_the_model_configuration_its_pricing_key_names(platform, priced, priced_task):
    tokens_in, tokens_out, cost = triage_cost()

    row = wait_until(
        lambda: (r := platform.agent_usage(priced.agent)) and r["calls"] >= 3 and r,
        SPEND_ROLLUP_WAIT_SECONDS,
        "the priced agent's spend in the usage API",
    )

    assert row["name"] == priced.agent.name
    assert row["calls"] == 3
    assert row["unpricedCalls"] == 0
    assert (row["tokens"]["uncachedInput"], row["tokens"]["output"]) == (tokens_in, tokens_out)
    assert Decimal(row["cost"]) == cost
    assert row["activities"] == 1


def test_a_link_agent_without_a_pricing_key_has_its_tokens_recorded_and_its_calls_left_unpriced(platform):
    agent = platform.register("Parity Unpriced")
    model = ScriptedModel()
    process = platform.start(agent, model)
    view = platform.triage(agent, model)
    assert view.status == "completed", process.log_tail()
    tokens_in, tokens_out, _ = triage_cost()

    row = wait_until(
        lambda: (r := platform.agent_usage(agent)) and r["calls"] >= 3 and r,
        SPEND_ROLLUP_WAIT_SECONDS,
        "the unpriced agent's spend in the usage API",
    )

    assert row["unpricedCalls"] == row["calls"] == 3
    assert (row["tokens"]["uncachedInput"], row["tokens"]["output"]) == (tokens_in, tokens_out)
    assert Decimal(row["cost"]) == 0


def test_activity_lists_it_with_its_invocations_and_tokens(platform, priced, priced_task):
    tokens_in, tokens_out, _ = triage_cost()

    def row():
        rows = platform.api("GET", "/api/v1/activity/agents/performance")
        return next((r for r in rows if r["agent"]["id"] == priced.agent.id), None)

    found = wait_until(row, SPEND_ROLLUP_WAIT_SECONDS, "the priced agent in the activity API")

    assert found["agent"]["name"] == priced.agent.name
    assert found["invocationCount"] >= 1
    assert found["errorCount"] == 0
    assert found["totalInputTokens"] >= tokens_in
    assert found["totalOutputTokens"] >= tokens_out


def test_feedback_on_its_answer_is_filed_against_the_link_agent(platform, priced, priced_task):
    session_id = priced_task.record.get("sessionId") or priced_task.record.get("session_id")
    assert session_id, priced_task.record

    platform.api(
        "POST",
        "/api/v1/feedback",
        {
            "taskId": priced_task.task_id,
            "sessionId": session_id,
            "feedbackType": "up",
            "feedbackText": "Routed to the right queue.",
        },
    )

    rows = platform.api("GET", "/api/v1/feedback/all")
    mine = [r for r in rows if r.get("taskId") == priced_task.task_id]
    assert len(mine) == 1, rows
    assert (mine[0]["rating"], mine[0]["subjectType"], mine[0]["subjectId"]) == ("up", "agent", priced.agent.id)


def test_an_edit_reads_out_of_sync_until_redeployed_and_the_connected_agent_stays_listed(platform, priced):
    edited = platform.replace(priced.agent, team="Claims Operations")
    assert edited["syncStatus"] == "out_of_sync"

    platform.deploy(priced.agent, "update")

    record = platform.link_agent(priced.agent)
    assert record["team"] == "Claims Operations"
    assert record["syncStatus"] == "in_sync"
    assert record["runtimeStatus"]["status"] == "running"
    assert platform.client.agent_card(priced.agent.instance_name) is not None


def test_disabling_it_drops_its_link_and_refuses_it_until_enabled_then_the_same_process_rejoins(platform, priced):
    platform.replace(priced.agent, enabled=False)
    platform.deploy(priced.agent, "update")

    platform.client.wait_delisted(priced.agent.instance_name, timeout=REFUSED_WAIT_SECONDS)
    wait_until(
        lambda: "HTTP 403" in priced.process.log_tail(),
        REFUSED_WAIT_SECONDS,
        "the agent's reconnects to be refused with 403",
    )
    assert priced.process.alive(), priced.process.log_tail()
    refusals = [
        line
        for line in platform.server.log_text().splitlines()
        if "agent link connection refused" in line and priced.agent.instance_name in line
    ]
    assert refusals and all(f'"gatewayID":"{SLUG}"' in line for line in refusals), refusals
    refused = platform.client.submit(priced.agent.instance_name, ASK)
    assert refused.status != 200 or "error" in refused.body, refused

    platform.replace(priced.agent, enabled=True)
    platform.deploy(priced.agent, "update")

    platform.client.wait_listed(priced.agent.instance_name, timeout=LISTED_TIMEOUT_SECONDS)
    view = platform.triage(priced.agent, priced.model)
    assert view.status == "completed", priced.process.log_tail()
    assert view.answer() == ANSWER


def test_a_used_up_mesh_budget_refuses_its_tasks_as_it_refuses_a_native_agents(platform, priced, priced_task):
    platform.api("PATCH", f"/api/v2/platform/budgets/{MESH_BUDGET_ID}", {"amount": USED_UP_BUDGET})
    try:
        wait_until(
            lambda: platform.api("GET", "/api/v1/budget/status").get("meshLimitReached") is True,
            SPEND_ROLLUP_WAIT_SECONDS,
            "the mesh budget to read used up",
        )

        refused = platform.client.submit(priced.agent.instance_name, ASK)

        assert refused.status == 429, refused
        assert refused.body["error"]["code"] == BUDGET_REFUSED, refused.body
        assert "usage budget is used up" in refused.body["error"]["message"]
    finally:
        platform.api("PATCH", f"/api/v2/platform/budgets/{MESH_BUDGET_ID}", {"amount": None})
        wait_until(
            lambda: platform.api("GET", "/api/v1/budget/status").get("meshLimitReached") is False,
            SPEND_ROLLUP_WAIT_SECONDS,
            "the mesh budget to be lifted",
        )
