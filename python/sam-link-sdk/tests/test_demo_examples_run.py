# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Every agent of the claims desk demo (``examples/demo/*.py``) serves one task end to end: its own ``main()`` joins an
in-process agent link entrypoint (through the agent link `MeshAgent` builds from the environment, as the agent), a
scripted model stands in for the real one, and the task goes through the agent's tools, the tool it guards with an
approval (approved by the entrypoint), and the peers it calls (answered by the entrypoint as scripted peers). The
agent joins with the peers ``examples/demo/stack/agent_link.yaml`` lets it call.

The model constructor (each module's ``build_model``) is the thing replaced. Hermes is not installed here: as in
``test_examples_run``, stand-ins for its modules run the turn through Hermes' hooks.
"""

from __future__ import annotations

import asyncio
import importlib.util
import json
import sys
import types
from types import SimpleNamespace

import pytest

from . import test_link
from .conftest import EXAMPLES
from .test_demo_helpers import link_agents
from .test_examples_run import APPROVE, tool_results
from .test_link import entrypoint, eventually, results, task

DEMO = EXAMPLES / "demo"
SHARED_MODULES = ("common",)


@pytest.fixture(autouse=True)
def environment_restored():
    """The CrewAI agents turn off CrewAI's (and OpenTelemetry's) export in os.environ as they load."""
    import os

    saved = dict(os.environ)
    yield
    os.environ.clear()
    os.environ.update(saved)


@pytest.fixture
def load_demo():
    """Import a demo module as ``python examples/demo/<path>`` would (its directory on the path); the shared modules
    it imports by bare name are forgotten afterwards, so each test gets fresh demo data."""
    saved_path = list(sys.path)

    def load(path: str):
        file = DEMO / path
        sys.path.insert(0, str(file.parent))
        spec = importlib.util.spec_from_file_location(f"demo_{path.replace('/', '_')[:-3]}", file)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    yield load
    sys.path[:] = saved_path
    for name in SHARED_MODULES:
        sys.modules.pop(name, None)


@pytest.fixture
async def link(monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))
    server = await entrypoint().start()
    server.responders["prompt"] = APPROVE
    for name, value in {
        "SAM_LINK_URL": server.url,
        "SAM_LINK_TOKEN": test_link.TOKEN,
        "LLM_SERVICE_ENDPOINT": "https://models.example.com/v1",
        "LLM_SERVICE_API_KEY": "not-a-real-key",
        "LLM_SERVICE_GENERAL_MODEL_NAME": "openai/fake-model",
        "PARTS_SUPPLIER_SECONDS": "0",
        "CREWAI_DISABLE_TELEMETRY": "true",
        "CREWAI_TRACING_ENABLED": "false",
        "NO_PROXY": "127.0.0.1,localhost",
        "no_proxy": "127.0.0.1,localhost",
    }.items():
        monkeypatch.setenv(name, value)
    for name in (
        "SAM_LINK_TRUST_STORE_PATH",
        "DEMO_OTLP_ENDPOINT",
        "OTEL_SDK_DISABLED",
        "DEMO_OPENAI_BASE_URL",
        "DEMO_OPENAI_MODEL",
        "HERMES_HOME",
        "SAM_AGENT_NAME",
    ):
        monkeypatch.delenv(name, raising=False)
    yield server
    await server.stop()


def as_agent(monkeypatch, server, name: str, peers: dict[str, str]) -> None:
    """The entrypoint knows the link token as `name`'s, lists `peers` to it, and answers each peer call as that peer."""
    monkeypatch.setattr(test_link, "AGENT", name)
    server.after_welcome = [
        {
            "type": "peers",
            "peers": [
                {
                    "name": peer,
                    "displayName": peer.replace("-", " ").title(),
                    "description": f"The {peer} agent",
                    "skills": [],
                    "onDemand": False,
                }
                for peer in peers
            ],
        }
    ]
    server.responders["peer_call"] = lambda f: [
        {"type": "peer_result", "subtaskId": f["subtaskId"], "state": "completed", "text": peers[f["peer"]]}
    ]


async def serve(server, main, text: str) -> dict:
    """Run `main()`, send one task, and return its result."""
    runner = asyncio.create_task(main())
    try:
        await eventually(lambda: server.of("consume") or runner.done(), 10)
        if runner.done():
            runner.result()
        await server.send(task(1, "T1", text))
        [result] = await eventually(lambda: results(server, "T1"), 20)
        return result
    finally:
        runner.cancel()
        await asyncio.gather(runner, return_exceptions=True)


def started_tools(server) -> list[str]:
    return [f["event"]["name"] for f in server.of("event") if f["event"].get("kind") == "tool_started"]


def approvals(server) -> list[str]:
    return [p["approval"]["tool"] for p in server.of("prompt")]


def card_tools(server) -> list[str]:
    return [t["name"] for t in server.of("describe")[0]["agent"]["tools"]]


def langchain_model(turns):
    """A turn is (text, [(call id, tool, args)])."""
    from langchain_core.messages import AIMessage

    from .test_callbacks import ToolCallingFake

    return ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content=t,
                    tool_calls=[{"id": c, "name": n, "args": a} for c, n, a in calls],
                    usage_metadata={"input_tokens": 10, "output_tokens": 2, "total_tokens": 12},
                )
                for t, calls in turns
            ]
        )
    )


def openai_agents_model(turns):
    from .test_openai_agents import ScriptedModel, call, message, usage

    return ScriptedModel(
        [([*([message(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(10, 2)) for t, calls in turns]
    )


def strands_model(turns):
    from .test_strands import ScriptedModel, call, text, usage

    model = ScriptedModel(
        [([*([text(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(10, 2)) for t, calls in turns]
    )
    model.config = {"model_id": model.model_id}
    return model


def agent_framework_model(turns):
    from .test_agent_framework import ScriptedClient, call, text, usage

    return ScriptedClient(
        [([*([text(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(10, 2)) for t, calls in turns]
    )


def adk_model(turns):
    from google.genai import types as genai

    from .test_adk import Scripted, fc, usage

    return Scripted(
        turns=[
            ([*([genai.Part(text=t)] if t else []), *(fc(n, a, call_id=c) for c, n, a in calls)], usage(10, 2))
            for t, calls in turns
        ]
    )


def crewai_model(turns):
    """A CrewAI LLM answering in CrewAI's text (ReAct) format: one action per reply, then the final answer."""
    from crewai.llms.base_llm import BaseLLM

    replies = []
    for t, calls in turns:
        replies += [f"Thought: next\nAction: {n}\nAction Input: {json.dumps(a)}" for _, n, a in calls]
        if t:
            replies.append(f"Thought: done\nFinal Answer: {t}")

    class Scripted(BaseLLM):
        def __init__(self):
            super().__init__(model="fake-model")

        def call(self, messages, tools=None, callbacks=None, available_functions=None, **kwargs):
            return replies.pop(0)

        def supports_function_calling(self):
            return False

        def supports_stop_words(self):
            return False

        def get_context_window_size(self):
            return 8000

    return Scripted()


def fake_hermes(monkeypatch, conversations):
    """Stand-ins for the Hermes modules the agent and the adapter import, with an AIAgent whose each
    ``run_conversation`` plays the next of `conversations` through Hermes' hooks and the registered tools."""
    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)
    sys.modules["hermes_cli.plugins"].VALID_HOOKS |= {"pre_gateway_dispatch"}
    handlers: dict[str, object] = {}
    sys.modules["tools.registry"].registry = SimpleNamespace(
        register=lambda name, handler, **kw: handlers.__setitem__(name, handler)
    )
    interrupt = types.ModuleType("tools.interrupt")
    interrupt.is_interrupted = lambda: False
    run_agent_module = types.ModuleType("run_agent")
    script = list(conversations)

    class AIAgent:
        def __init__(self, session_id="", enabled_toolsets=(), ephemeral_system_prompt="", **kwargs):
            self.session_id = session_id
            self.ephemeral_system_prompt = ephemeral_system_prompt
            self.tools = [{"type": "function", "function": {"name": n}} for n in handlers]
            self.valid_tool_names = set(handlers)
            self.stream_delta_callback = None
            self.tool_gen_callback = None

        def run_conversation(self, text, conversation_history=None, task_id=""):
            answer = ""
            for answer, calls in script.pop(0):  # noqa: B007
                hermes.model_call(self.session_id, [(c, n) for c, n, _ in calls])
                for c, n, args in calls:
                    if (
                        hermes.fire("pre_tool_call", session_id=self.session_id, tool_name=n, args=args, tool_call_id=c)
                        is None
                    ):
                        result = handlers[n](args, session_id=self.session_id)
                        hermes.fire(
                            "post_tool_call",
                            session_id=self.session_id,
                            tool_name=n,
                            tool_call_id=c,
                            result=result,
                            status="success",
                        )
            return {"final_response": answer, "messages": [], "completed": True}

        def hard_interrupt(self):
            pass

    run_agent_module.AIAgent = AIAgent
    monkeypatch.setitem(sys.modules, "tools.interrupt", interrupt)
    monkeypatch.setitem(sys.modules, "run_agent", run_agent_module)


def scripted(monkeypatch, module, model) -> None:
    monkeypatch.setattr(module, "build_model", lambda: model)


async def test_claims_intake_asks_the_desk(link, load_demo, monkeypatch):
    pytest.importorskip("langchain_openai")
    peers = {
        "policy-desk": "Covered, $7,400 payable.",
        "fraud-screen": "Low risk.",
        "repair-estimator": "Estimate $6,700.",
        "settlement-review": "Settled, ticket CLM-90411.",
    }
    as_agent(monkeypatch, link, "claims-intake", peers)
    intake = load_demo("claims_intake_langchain.py")
    ask = {"task_description": "Claim C-7781"}
    scripted(
        monkeypatch,
        intake,
        langchain_model(
            [
                ("", [("c1", "get_claim", {"claim_id": "C-7781"})]),
                (
                    "",
                    [
                        ("c2", "peer_policy-desk", ask),
                        ("c3", "peer_fraud-screen", ask),
                        ("c4", "peer_repair-estimator", ask),
                    ],
                ),
                ("", [("c5", "peer_settlement-review", {"task_description": "Settle C-7781 for $7,400, low risk"})]),
                ("C-7781: covered, low risk, settled (CLM-90411).", []),
            ]
        ),
    )
    result = await serve(link, intake.main, "Process claim C-7781")
    assert result["text"] == "C-7781: covered, low risk, settled (CLM-90411)."
    assert sorted(c["peer"] for c in link.of("peer_call")) == sorted(peers)
    assert tool_results(link)["get_claim"]["policy_id"] == "P-1182"


async def test_policy_desk_asks_the_regulations_agent(link, load_demo, monkeypatch):
    pytest.importorskip("google.adk")
    as_agent(monkeypatch, link, "policy-desk", {"regulations": "Rule W-14: settle within 15 days."})
    desk = load_demo("policy_desk_adk.py")
    scripted(
        monkeypatch,
        desk,
        adk_model(
            [
                ("", [("c1", "get_claim", {"claim_id": "C-7781"}), ("c2", "get_policy", {"policy_id": "P-1182"})]),
                (
                    "",
                    [
                        (
                            "c3",
                            "check_coverage",
                            {"policy_id": "P-1182", "loss_type": "water damage", "amount_usd": 8400},
                        )
                    ],
                ),
                (
                    "",
                    [
                        (
                            "c4",
                            "peer_regulations",
                            {"task_description": "Settlement deadline in Westmarch for water damage?"},
                        )
                    ],
                ),
                ("Covered, $7,400 payable; Rule W-14 applies.", []),
            ]
        ),
    )
    result = await serve(link, desk.main, "Check coverage for claim C-7781")
    assert result["text"] == "Covered, $7,400 payable; Rule W-14 applies."
    assert tool_results(link)["get_policy"]["jurisdiction"] == "Westmarch"
    assert tool_results(link)["check_coverage"]["payable_usd"] == 7400
    assert [c["peer"] for c in link.of("peer_call")] == ["regulations"]
    assert "Rule W-14" in str(tool_results(link)["peer_regulations"])


async def test_regulations_cites_a_rule(link, load_demo, monkeypatch):
    """Hermes is not installed here: stand-ins for its modules run each turn through its hooks (see fake_hermes)."""
    fake_hermes(
        monkeypatch,
        [
            [
                ("", [("c1", "lookup_rule", {"jurisdiction": "Westmarch", "topic": "settlement deadline"})]),
                ("Rule W-14: within 15 days.", []),
            ]
        ],
    )
    as_agent(monkeypatch, link, "regulations", {})
    regulations = load_demo("regulations_hermes.py")
    result = await serve(link, regulations.main, "Settlement deadline in Westmarch?")
    assert result["text"] == "Rule W-14: within 15 days."
    assert "Rule W-14" in str(tool_results(link)["lookup_rule"])


async def test_fraud_screen_asks_the_records_agent(link, load_demo, monkeypatch):
    pytest.importorskip("crewai")
    as_agent(monkeypatch, link, "fraud-screen", {"records-lookup": "Two earlier claims: C-7655, C-7701."})
    screen = load_demo("fraud_screen_crewai.py")
    scripted(
        monkeypatch,
        screen,
        crewai_model(
            [
                (
                    "",
                    [
                        ("c1", "fraud_signals", {"claim_id": "C-7802"}),
                        ("c2", "peer_records-lookup", {"task_description": "Earlier claims on policy P-3301"}),
                    ],
                ),
                ("High risk: lapsed policy, two recent claims.", []),
            ]
        ),
    )
    result = await serve(link, screen.main, "Screen claim C-7802")
    assert result["text"] == "High risk: lapsed policy, two recent claims."
    signals = tool_results(link)["fraud_signals"]
    assert signals["policy_lapsed"] is True and signals["policy_age_days"] == 379
    assert [c["peer"] for c in link.of("peer_call")] == ["records-lookup"]
    assert "C-7655" in str(tool_results(link)["peer_records-lookup"])


async def test_records_lookup_lists_earlier_claims_and_redacts_the_archive_token(link, load_demo, monkeypatch):
    pytest.importorskip("strands")
    as_agent(monkeypatch, link, "records-lookup", {})
    records = load_demo("records_lookup_strands.py")
    scripted(
        monkeypatch,
        records,
        strands_model(
            [
                ("", [("c1", "policy_of_claim", {"claim_id": "C-7802"})]),
                ("", [("c2", "prior_claims", {"policy_id": "P-3301"})]),
                ("C-7655 (fire) and C-7701 (storm).", []),
            ]
        ),
    )
    result = await serve(link, records.main, "Earlier claims for C-7802")
    assert result["text"] == "C-7655 (fire) and C-7701 (storm)."
    assert [c["claim_id"] for c in tool_results(link)["prior_claims"]] == ["C-7655", "C-7701"]
    lookup = tool_results(link)["policy_of_claim"]
    assert lookup["policy_id"] == "P-3301" and lookup["archive_api_token"] == "<REDACTED>"
    assert records.ARCHIVE_API_TOKEN not in json.dumps([f for _, f in link.frames])


async def test_repair_estimator_asks_parts_pricing(link, load_demo, monkeypatch):
    pytest.importorskip("langgraph")
    pytest.importorskip("langchain_openai")
    as_agent(monkeypatch, link, "repair-estimator", {"parts-pricing": "Rear bumper $640, 3 h; tail light $185, 0.5 h."})
    estimator = load_demo("repair_estimator_langgraph.py")
    scripted(
        monkeypatch,
        estimator,
        langchain_model(
            [
                ("", [("c1", "damage_items", {"claim_id": "C-7790"})]),
                ("", [("c2", "peer_parts-pricing", {"task_description": "Price: rear bumper, tail light"})]),
                ("", [("c3", "estimate_total", {"item_prices_usd": [640, 185], "labour_hours": 3.5})]),
                ("Estimate $1,140.", []),
            ]
        ),
    )
    result = await serve(link, estimator.main, "Estimate claim C-7790")
    assert result["text"] == "Estimate $1,140."
    assert [c["peer"] for c in link.of("peer_call")] == ["parts-pricing"]
    assert tool_results(link)["estimate_total"]["total_usd"] == 1140.0


async def test_parts_pricing_prices_items(link, load_demo, monkeypatch):
    pytest.importorskip("agents")
    as_agent(monkeypatch, link, "parts-pricing", {})
    pricing = load_demo("parts_pricing_openai.py")
    scripted(
        monkeypatch,
        pricing,
        openai_agents_model(
            [
                (
                    "",
                    [
                        ("c1", "catalogue_price", {"item": "rear bumper"}),
                        ("c2", "labour_hours", {"item": "rear bumper"}),
                    ],
                ),
                ("Rear bumper: $640, 3 h.", []),
            ]
        ),
    )
    result = await serve(link, pricing.main, "Price a rear bumper")
    assert result["text"] == "Rear bumper: $640, 3 h."
    assert tool_results(link)["catalogue_price"] == {"item": "rear bumper", "price_usd": 640}
    assert tool_results(link)["labour_hours"] == {"item": "rear bumper", "hours": 3.0}


async def test_settlement_review_pays_a_large_settlement_after_the_user_approves(link, load_demo, monkeypatch):
    pytest.importorskip("agent_framework")
    as_agent(monkeypatch, link, "settlement-review", {})
    review = load_demo("settlement_review_maf.py")
    scripted(
        monkeypatch,
        review,
        agent_framework_model(
            [
                ("", [("c1", "settle_large", {"claim_id": "C-7781", "amount_usd": 7400})]),
                ("Settled C-7781 for $7,400, ticket CLM-90411.", []),
            ]
        ),
    )
    result = await serve(link, review.main, "Settle C-7781: $7,400 payable, low risk")
    assert result["text"] == "Settled C-7781 for $7,400, ticket CLM-90411."
    assert approvals(link) == ["settle_large"]
    assert tool_results(link)["settle_large"]["status"] == "payment scheduled"


async def test_settlement_review_settles_a_small_one_without_asking(link, load_demo, monkeypatch):
    pytest.importorskip("agent_framework")
    as_agent(monkeypatch, link, "settlement-review", {})
    review = load_demo("settlement_review_maf.py")
    scripted(
        monkeypatch,
        review,
        agent_framework_model(
            [
                ("", [("c1", "settle_small", {"claim_id": "C-7790", "amount_usd": 2650})]),
                ("Settled C-7790 for $2,650, ticket CLM-90411.", []),
            ]
        ),
    )
    result = await serve(link, review.main, "Settle C-7790: $2,650 payable, low risk")
    assert result["text"] == "Settled C-7790 for $2,650, ticket CLM-90411."
    assert approvals(link) == []
    assert started_tools(link) == ["settle_small"]


AGENT_MODULES = sorted(str(p.relative_to(DEMO)) for p in DEMO.glob("*.py") if "MeshAgent(" in p.read_text())


def test_every_demo_agent_is_run_here():
    assert set(AGENT_MODULES) == set(AGENT_NAMES)


@pytest.mark.parametrize("path", AGENT_MODULES)
def test_a_demo_agent_joins_through_the_link_meshagent_builds_from_the_environment(path):
    source = (DEMO / path).read_text()
    assert "LinkTransport" not in source and "transport=" not in source
    assert "async def main()" in source and 'if __name__ == "__main__":\n    asyncio.run(main())' in source


def test_without_a_link_url_or_token_the_demo_refuses_to_start_and_names_what_is_missing(monkeypatch):
    from sam_link_sdk import AgentConfig, MeshAgent

    config = AgentConfig(name="policy-desk", description="d")
    monkeypatch.delenv("SAM_LINK_URL", raising=False)
    monkeypatch.setenv("SAM_LINK_TOKEN", "not-a-real-token")
    with pytest.raises(
        ValueError, match=r"needs SAM_LINK_URL; SAM_LINK_URL has the form wss://<sam-host>/gw/<gateway_id>/v1/connect"
    ):
        MeshAgent(config, lambda ctx: "ok")
    monkeypatch.setenv("SAM_LINK_URL", "wss://sam.example.com/gw/agent-link/v1/connect")
    monkeypatch.delenv("SAM_LINK_TOKEN")
    with pytest.raises(ValueError, match=r"needs SAM_LINK_TOKEN or SAM_LINK_TOKEN_FILE$"):
        MeshAgent(config, lambda ctx: "ok")


def test_with_a_link_url_the_agent_joins_through_the_agent_link_as_its_config_name(monkeypatch):
    from sam_link_sdk import AgentConfig, MeshAgent
    from sam_link_sdk.link import LinkTransport

    monkeypatch.setenv("SAM_LINK_URL", "wss://sam.example.com/gw/agent-link/v1/connect")
    monkeypatch.setenv("SAM_LINK_TOKEN", "not-a-real-token")
    monkeypatch.setenv("SAM_AGENT_NAME", "someone-else")
    transport = MeshAgent(AgentConfig(name="policy-desk", description="d"), lambda ctx: "ok")._transport
    assert isinstance(transport, LinkTransport) and transport.config.agent == "policy-desk"


AGENT_NAMES = {
    "claims_intake_langchain.py": ("claims-intake", "langchain_openai"),
    "policy_desk_adk.py": ("policy-desk", "google.adk"),
    "regulations_hermes.py": ("regulations", None),
    "fraud_screen_crewai.py": ("fraud-screen", "crewai"),
    "records_lookup_strands.py": ("records-lookup", "strands"),
    "repair_estimator_langgraph.py": ("repair-estimator", "langgraph"),
    "parts_pricing_openai.py": ("parts-pricing", "agents"),
    "settlement_review_maf.py": ("settlement-review", "agent_framework"),
}


@pytest.mark.parametrize("path", AGENT_MODULES)
async def test_a_demo_agent_joins_as_itself_with_its_own_model(path, link, load_demo, monkeypatch):
    """The demo's own setup: main() as it is, with its own model built from the LLM_SERVICE_* settings, joins
    the agent link under its own name, describes itself and asks for its tasks."""
    name, need = AGENT_NAMES[path]
    if need:
        pytest.importorskip(need)
    if path == "regulations_hermes.py":
        fake_hermes(monkeypatch, [])
    monkeypatch.setattr(test_link, "AGENT", name)
    module = load_demo(path)
    runner = asyncio.create_task(module.main())
    try:
        await eventually(lambda: link.of("consume") or runner.done(), 10)
        if runner.done():
            runner.result()
        assert [h["Sam-Agent"] for h in link.headers] == [name]
        assert [f["type"] for _, f in link.frames][:2] == ["describe", "consume"]
        described = link.of("describe")[0]["agent"]
        assert described["description"]
        assert sorted(described["peers"]) == sorted(link_agents()[name].get("peers", []))
    finally:
        runner.cancel()
        await asyncio.gather(runner, return_exceptions=True)


def test_the_demo_exports_its_metrics_where_demo_otlp_endpoint_says(load_demo, monkeypatch):
    """With DEMO_OTLP_ENDPOINT set (start_agents.sh passes it on), the agents export OpenTelemetry metrics."""
    import inspect

    from sam_link_sdk import metrics

    pytest.importorskip("opentelemetry.sdk")
    real = inspect.signature(metrics.otlp_meter_provider)
    asked = []
    monkeypatch.setattr(metrics, "otlp_meter_provider", lambda *a, **kw: asked.append(real.bind(*a, **kw).arguments))
    monkeypatch.setenv("DEMO_OTLP_ENDPOINT", "http://localhost:4318")
    settings = load_demo("common.py").mesh_settings()
    assert asked == [{"endpoint": "http://localhost:4318/v1/metrics", "export_interval_seconds": 10}]
    assert isinstance(settings["metrics"], metrics.OtelMetrics)


@pytest.mark.parametrize("path", AGENT_MODULES)
def test_a_demo_agents_own_model_builds_from_the_demo_settings(path, link, load_demo, monkeypatch):
    """The one thing the tests above replace, built as the demo builds it (no request is made)."""
    _, need = AGENT_NAMES[path]
    if need:
        pytest.importorskip(need)
    if path == "regulations_hermes.py":
        fake_hermes(monkeypatch, [])
    monkeypatch.setenv("DEMO_OPENAI_BASE_URL", "https://openai-compatible.example.com/v1/")
    model = load_demo(path).build_model()
    assert model is not None
    if path == "regulations_hermes.py":
        assert model == {
            "base_url": "https://openai-compatible.example.com/v1/",
            "api_key": "not-a-real-key",
            "model": "openai/fake-model",
        }
