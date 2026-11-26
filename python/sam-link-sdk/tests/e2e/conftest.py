# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Fixtures for the end-to-end tests: one real SAM-Go per session, and per framework its real example agents, each
in a process of its own, each with a scripted model of its own.

SAM's agent link configuration is read once, at boot, so every agent any test may start is in the roster below. A
framework's agents start together when its first test needs them and stop when its last test is done (pytest orders
the tests by framework because `framework` is a module-scoped parameter).
"""

from __future__ import annotations

import importlib.util
import os
import subprocess
import sys
import time
from dataclasses import dataclass, field
from pathlib import Path

import pytest

from ..contract.sam_client import SamClient
from ..contract.sam_server import (
    SECOND_USER,
    TOOL_SCOPE_HELD,
    TOOL_SCOPE_NOT_HELD,
    LinkAgent,
    SamServer,
    SamUnavailable,
    locate_binary,
)
from .agent_process import MODEL_API_KEY, AgentProcess
from .model_script import ScriptedModel

E2E_DIR = Path(__file__).parent
LISTED_TIMEOUT_SECONDS = 120
REQUIRED_ENV = ("SAM_E2E_REQUIRED", "SAM_CONTRACT_REQUIRED")
"""Set where the suite must run (CI): a SAM or a framework that cannot be found fails the run instead of skipping it."""
HERMES_PYTHON_ENV = "SAM_E2E_HERMES_PYTHON"
"""An interpreter with a Hermes checkout installed (Hermes needs Python 3.14 and is not on PyPI). The SDK is put on its
path by the suite.
"""
SHARD_ENV = "SAM_E2E_SHARD"
"""A value of "<n>/<of>" runs only the n-th of `of` slices of the frameworks (1-based), so CI can split the suite across jobs. Each
framework goes to one slice whole: its agents start once per module, and a new framework lands in a slice by itself.
Hermes is in none of them, because it runs in another interpreter that only its own job installs: "hermes" runs it
alone.
"""
FRAMEWORK_ENV = "SAM_E2E_FRAMEWORK"
"""A framework id (such as langgraph) runs only that framework's tests, for the compatibility matrix, which installs
one framework at a time at its lowest and its newest supported release. It replaces SHARD_ENV when both are set.
"""
HERMES_SHARD = "hermes"
CORE_SHARD = "core"

PLAIN, APPROVAL, BLOCKED, STOP, LIMITED, PEER, HANDLED, WHOLE, NATIVE, NESTED, REPLY, UNDECLARED = (
    "plain",
    "approval",
    "blocked",
    "stop",
    "limited",
    "peer",
    "handled",
    "whole",
    "native",
    "nested",
    "reply",
    "undeclared",
)
ADAPTER_ROLES = (PLAIN, APPROVAL, BLOCKED, STOP, PEER, UNDECLARED)
STREAMING_ROLES = (*ADAPTER_ROLES, WHOLE)
FRAMEWORK_MAX_TURNS = 3
"""The limited agent's own framework loop limit (CLAIMS_MAX_TURNS), on the examples that have one, and the words its OpenAI
Agents input and output guardrails block.
"""
BLOCKED_WORD = "embargoed"
BLOCKED_OUTPUT_WORD = "quarantined"
SYSTEM_PURPOSE = "Answer as the claims desk of Northwind Mutual."
"""What the web gateway tells every agent it is for; an example given CLAIMS_REQUEST_CONTEXT puts it in its prompt."""
PEER_ENTRYPOINT = (PLAIN, APPROVAL)
"""The agents the entrypoint lets the peer agent call, and the one of them its own setting (CLAIMS_PEER_AGENTS) lists: the
plain agent is the one it delegates to, the approval agent is allowed by SAM but must not be offered.
"""
PEER_LISTED = PLAIN
FRAUD_SCREEN_SECONDS = 6
"""The stop agent's fraud screen, short enough that a cancel can land inside it without a 30-second wait."""
STOP_MAX_LLM_CALLS = 2
"""The stop agent's model call limit: low enough to reach, high enough for the cancel tests' runs."""
NESTED_MAX_LLM_CALLS = 4
"""The nested agent's model call limit: the four calls of a run through its agent tool (the intake agent's two and the
triage agent's two) fit, a fifth does not.
"""


@dataclass(frozen=True)
class Framework:
    """One example: what it imports (to tell whether it can run here), the agents it is started as, and how its
    model name is spelled."""

    id: str
    imports: tuple[str, ...]
    roles: tuple[str, ...] = ADAPTER_ROLES
    provider_prefixed_model: bool = False

    @property
    def example(self) -> str:
        return f"claims_triage_{self.id}.py"

    def agent_name(self, role: str) -> str:
        return f"e2e-{self.id.replace('_', '-')}-{role}"

    @property
    def model(self) -> str:
        return "openai/fake-model" if self.provider_prefixed_model else "fake-model"


FRAMEWORKS = {
    fw.id: fw
    for fw in (
        Framework("langchain", ("langchain_openai",), roles=(*STREAMING_ROLES, LIMITED)),
        Framework("langgraph", ("langgraph", "langchain_openai"), roles=(*STREAMING_ROLES, LIMITED, HANDLED, NATIVE)),
        Framework("openai_agents", ("agents",), roles=(*STREAMING_ROLES, LIMITED, NATIVE, NESTED)),
        Framework(
            "adk", ("google.adk", "litellm"), roles=(*STREAMING_ROLES, LIMITED, NATIVE), provider_prefixed_model=True
        ),
        Framework("crewai", ("crewai",), roles=(*STREAMING_ROLES, LIMITED, REPLY), provider_prefixed_model=True),
        Framework("strands", ("strands",), roles=(*STREAMING_ROLES, NATIVE)),
        Framework(
            "agent_framework", ("agent_framework", "agent_framework.openai"), roles=(*STREAMING_ROLES, LIMITED, NATIVE)
        ),
        Framework("otel", ("strands", "opentelemetry.sdk"), roles=(PLAIN, STOP)),
        Framework("hermes", ("run_agent",), roles=(*STREAMING_ROLES, LIMITED)),
    )
}
"""Spans record what happened, after it happened: the OpenTelemetry example cannot hold a tool for approval, refuse a tool
for want of a scope, or stop a run on its own (examples/claims_triage_otel.py), so it has only the plain and stop roles.
"""
ADAPTERS = [fw for fw in FRAMEWORKS if fw != "otel"]
EVERY_EXAMPLE = list(FRAMEWORKS)


def pytest_collection_modifyitems(config, items):
    """A soak run takes minutes, so `-m e2e` leaves it out; `-m soak` selects it."""
    for item in items:
        if E2E_DIR in Path(str(item.fspath)).parents and item.get_closest_marker("soak") is None:
            item.add_marker(pytest.mark.e2e)
    shard = _shard()
    if shard is None:
        return
    frameworks, rest = shard
    kept, dropped = [], []
    for item in items:
        callspec = getattr(item, "callspec", None)
        fw = callspec.params.get("framework") if callspec is not None else None
        in_shard = fw in frameworks if fw is not None else rest
        (kept if in_shard or E2E_DIR not in Path(str(item.fspath)).parents else dropped).append(item)
    if dropped:
        config.hook.pytest_deselected(items=dropped)
        items[:] = kept


def pytest_collection_finish(session):
    """pytest orders the tests by their parameters, which interleaves frameworks in a file whose tests take a parameter
    besides the framework, and each switch stops one framework's examples and starts the other's (about 25 s in CI).
    Run each file's tests one framework at a time instead."""
    order = {fw: i for i, fw in enumerate(FRAMEWORKS)}
    session.items.sort(key=lambda item: (str(item.fspath), order.get(_framework_of(item), -1)))


def _framework_of(item) -> str | None:
    callspec = getattr(item, "callspec", None)
    return callspec.params.get("framework") if callspec is not None else None


def _shard() -> tuple[set[str], bool] | None:
    """The frameworks of this run's slice, and whether it takes the e2e tests that are not per framework (the "core"
    slice); None when neither FRAMEWORK_ENV nor SHARD_ENV is set. Hermes is in the "hermes" slice only."""
    framework = os.environ.get(FRAMEWORK_ENV, "").strip()
    if framework:
        if framework not in FRAMEWORKS:
            raise pytest.UsageError(f"{FRAMEWORK_ENV}={framework!r}: expected one of {', '.join(FRAMEWORKS)}")
        return {framework}, False
    raw = os.environ.get(SHARD_ENV, "").strip()
    if not raw:
        return None
    if raw == HERMES_SHARD:
        return {"hermes"}, False
    if raw == CORE_SHARD:
        return set(), True
    try:
        n, of = (int(part) for part in raw.split("/"))
    except ValueError:
        raise pytest.UsageError(
            f"{SHARD_ENV}={raw!r}: expected <n>/<of>, such as 1/4, {HERMES_SHARD} or {CORE_SHARD}"
        ) from None
    if not 1 <= n <= of:
        raise pytest.UsageError(f"{SHARD_ENV}={raw!r}: n must be between 1 and {of}")
    return set([fw for fw in FRAMEWORKS if fw != "hermes"][n - 1 :: of]), False


def _required() -> bool:
    return any(os.environ.get(name, "").lower() in ("1", "true", "yes") for name in REQUIRED_ENV)


def unavailable(why: str) -> None:
    if _required():
        pytest.fail(f"{' or '.join(REQUIRED_ENV)} is set and {why}")
    pytest.skip(why)


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    report = outcome.get_result()
    if report.when != "call" or not report.failed:
        return
    examples = item.funcargs.get("examples")
    if isinstance(examples, Examples):
        for running in examples.running.values():
            report.sections.append((f"{running.process.name} log (tail, secrets removed)", running.process.log_tail()))
        report.sections.append(("SAM log (tail, secrets removed)", examples.server.log_tail()))


@pytest.fixture(scope="session")
def e2e_server(tmp_path_factory) -> SamServer:
    try:
        binary = locate_binary()
    except SamUnavailable as exc:
        unavailable(f"no SAM-Go is available: {exc}")
    agents = [
        LinkAgent(fw.agent_name(role), tuple(fw.agent_name(p) for p in PEER_ENTRYPOINT) if role == PEER else ())
        for fw in FRAMEWORKS.values()
        for role in fw.roles
    ]
    server = SamServer(binary, tmp_path_factory.mktemp("sam"), agents, system_purpose=SYSTEM_PURPOSE)
    try:
        server.start()
    except BaseException:
        server.stop()
        raise
    yield server
    server.stop()


@pytest.fixture(scope="session")
def e2e_client(e2e_server) -> SamClient:
    return SamClient(e2e_server.base_url, e2e_server.binary, e2e_server.home, e2e_server.root / "user-home")


@pytest.fixture(scope="session")
def second_user(e2e_server) -> SamClient:
    """SAM's HTTP API as another user than `e2e_client`'s, with the same roles."""
    return SamClient(e2e_server.base_url, user=SECOND_USER)


def _python_for(fw: Framework) -> str:
    """The interpreter that runs the example, after checking it can import the framework."""
    if fw.id == "hermes":
        python = os.environ.get(HERMES_PYTHON_ENV, "").strip()
        if not python:
            unavailable(
                f"Hermes needs Python 3.14 and a checkout installed by hand; set {HERMES_PYTHON_ENV} to "
                "an interpreter that has it (docs/contributing/testing.md)"
            )
        probe = subprocess.run(
            [python, "-c", "import run_agent, sam_link_sdk.hermes"],
            capture_output=True,
            text=True,
            timeout=120,
            env={"PATH": os.environ.get("PATH", ""), "PYTHONPATH": str(E2E_DIR.parents[1] / "src")},
        )
        if probe.returncode != 0:
            pytest.fail(f"{HERMES_PYTHON_ENV}={python} cannot import Hermes and sam_link_sdk: {probe.stderr[-1000:]}")
        return python
    missing = [
        name
        for name in fw.imports
        if importlib.util.find_spec(name.split(".")[0]) is None or importlib.util.find_spec(name) is None
    ]
    if missing:
        unavailable(f"the {fw.id} example needs {', '.join(missing)} (uv sync installs the dev group)")
    return sys.executable


@dataclass
class RunningExample:
    """One example agent the tests talk to: its name on the mesh, its process and its scripted model."""

    name: str
    process: AgentProcess
    model: ScriptedModel


@dataclass
class Examples:
    """A framework's running example agents, by role."""

    framework: Framework
    server: SamServer
    client: SamClient
    running: dict[str, RunningExample] = field(default_factory=dict)

    def __getitem__(self, role: str) -> RunningExample:
        running = self.running[role]
        assert running.process.alive(), f"{running.name} exited:\n{running.process.log_tail()}"
        return running


def _role_env(role: str, framework: Framework) -> dict[str, str]:
    """How each role's example is configured: plain and stop agents may route (the user holds the scope route_claim
    requires); the approval agent asks before it routes, may ask the user questions and sends the model's reasoning to the chat and puts the gateway's request context in the prompt; the blocked agent requires a
    scope the user lacks; the undeclared agent's card leaves out lookup_policy, which its framework still offers; the stop agent's fraud screen is short and its model call limit low; the limited agent's
    framework loop limit is low and it has an input and an output guardrail (OpenAI Agents); the peer agent may delegate to the
    one agent its setting names; the handled agent's tools hand their own errors to the model (LangGraph); the whole
    agent sends its answer when the run ends instead of streaming it; the native agent's route_claim needs approval by
    the framework's own flag, not by the SDK's with_approval; the nested agent runs the triage agent as a tool of an
    intake agent (OpenAI Agents' Agent.as_tool), with route_claim requiring a scope the user lacks and a low model call
    limit; the reply agent is a crew of two, whose second agent writes the answer (CrewAI)."""
    if role == NESTED:
        return {
            "CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_NOT_HELD,
            "CLAIMS_AGENT_TOOL": "true",
            "CLAIMS_MAX_LLM_CALLS": str(NESTED_MAX_LLM_CALLS),
        }
    if role == PEER:
        return {"CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD, "CLAIMS_PEER_AGENTS": framework.agent_name(PEER_LISTED)}
    if role == APPROVAL:
        return {
            "CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD,
            "CLAIMS_APPROVAL_TOOLS": "route_claim",
            "CLAIMS_ASK_USER": "true",
            "CLAIMS_STREAM_THINKING": "true",
            "CLAIMS_REQUEST_CONTEXT": "true",
        }
    if role == NATIVE:
        return {"CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD, "CLAIMS_NATIVE_APPROVAL": "true"}
    if role == BLOCKED:
        return {"CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_NOT_HELD}
    if role == UNDECLARED:
        return {"CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD, "CLAIMS_UNDECLARED_TOOL": "lookup_policy"}
    if role == STOP:
        return {
            "CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD,
            "CLAIMS_FRAUD_SCREEN_SECONDS": str(FRAUD_SCREEN_SECONDS),
            "CLAIMS_MAX_LLM_CALLS": str(STOP_MAX_LLM_CALLS),
        }
    if role == WHOLE:
        return {"CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD, "CLAIMS_STREAM_TEXT": "false"}
    if role == REPLY:
        return {"CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD, "CLAIMS_CREW_REPLY": "true"}
    if role == HANDLED:
        return {"CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD, "CLAIMS_HANDLE_TOOL_ERRORS": "true"}
    if role == LIMITED:
        return {
            "CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD,
            "CLAIMS_MAX_TURNS": str(FRAMEWORK_MAX_TURNS),
            "CLAIMS_BLOCKED_WORD": BLOCKED_WORD,
            "CLAIMS_BLOCKED_OUTPUT_WORD": BLOCKED_OUTPUT_WORD,
        }
    return {"CLAIMS_ROUTE_SCOPE": TOOL_SCOPE_HELD}


@pytest.fixture(scope="module")
def framework(request) -> Framework:
    return FRAMEWORKS[request.param]


@pytest.fixture(scope="module")
def examples(framework, e2e_server, e2e_client, tmp_path_factory) -> Examples:
    """Start the example agents of a framework, each as its own process with its own scripted model.

    LiteLLM (ADK) fetches its model price list from the network when imported, unless told not to. The scripted model is
    one no framework knows, so it is given the context window a current model has.
    """
    python = _python_for(framework)
    root = tmp_path_factory.mktemp(f"agents-{framework.id}")
    started = Examples(framework, e2e_server, e2e_client)
    try:
        for role in framework.roles:
            name = framework.agent_name(role)
            model = ScriptedModel()
            env = {
                "SAM_LINK_URL": e2e_server.link_url,
                "SAM_LINK_TOKEN": e2e_server.token(name),
                "SAM_AGENT_NAME": name,
                "CLAIMS_LLM_BASE_URL": model.base_url,
                "LLM_SERVICE_API_KEY": MODEL_API_KEY,
                "CLAIMS_LLM_MODEL": framework.model,
                "LITELLM_LOCAL_MODEL_COST_MAP": "True",
                "CREWAI_DISABLE_TELEMETRY": "true",
                "CREWAI_TRACING_ENABLED": "false",
                "CLAIMS_LLM_CONTEXT_WINDOW": "128000",
                **_role_env(role, framework),
            }
            if framework.id == "hermes":
                env["HERMES_HOME"] = str(root / f"{name}-hermes-home")
                env["HERMES_DISABLE_TELEMETRY"] = "1"
            process = AgentProcess(
                name, framework.example, python, root, env, secrets=(e2e_server.token(name), MODEL_API_KEY)
            )
            started.running[role] = RunningExample(name, process, model)
            process.start()
        deadline = time.monotonic() + LISTED_TIMEOUT_SECONDS
        for running in started.running.values():
            left = max(1.0, deadline - time.monotonic())
            try:
                e2e_client.wait_listed(running.name, timeout=left)
            except AssertionError as exc:
                raise AssertionError(f"{exc}\n{running.process.log_tail()}") from None
        yield started
    finally:
        for running in started.running.values():
            running.process.stop()
            running.model.close()
