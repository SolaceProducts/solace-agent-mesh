# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Every example under examples/ runs one task end to end: its own entry point (``main()``, a template's
``build_agent``, a request-driven script's ``main("wake")``) joins an in-process agent link entrypoint, a scripted model
stands in for the real one, and the task goes through the tool the example guards with an approval, which the
entrypoint approves.

The example's model constructor is the one thing replaced."""

from __future__ import annotations

import asyncio
import json
import sys
import types
from types import SimpleNamespace

import pytest

from sam_link_sdk._model import RUN
from sam_link_sdk.link import LinkTransport

from . import test_link
from .adapter_task import adapter_task
from .conftest import EXAMPLES, load_example
from .test_link import AGENT, TOKEN, entrypoint, eventually, results, task


def APPROVE(f):
    return [
        {
            "type": "prompt_answer",
            "taskId": f["taskId"],
            "requestId": f["requestId"],
            "status": "answered",
            "approved": True,
        }
    ]


CLAIM_TURNS = [
    ("", [("call_a", "lookup_policy", {"policy_id": "P-1182"})], (300, 20)),
    ("", [("call_b", "route_claim", {"policy_id": "P-1182", "queue": "fast-track"})], (340, 25)),
    ("P-1182 is routed to fast-track.", [], (380, 15)),
]
ROUTE_SCOPE = "tool:claims:route"


@pytest.fixture(autouse=True)
def environment_restored():
    """The CrewAI example turns off CrewAI's (and OpenTelemetry's) export in os.environ as it loads, as it should when
    run; the rest of the suite must not inherit that."""
    import os

    saved = dict(os.environ)
    yield
    os.environ.clear()
    os.environ.update(saved)


@pytest.fixture
async def link(monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))
    server = await entrypoint().start()
    server.responders["prompt"] = APPROVE
    for name, value in {
        "SAM_LINK_URL": server.url,
        "SAM_LINK_TOKEN": TOKEN,
        "SAM_AGENT_NAME": AGENT,
        "LLM_SERVICE_ENDPOINT": "https://models.example.com/v1",
        "LLM_SERVICE_API_KEY": "not-a-real-key",
        "LLM_SERVICE_GENERAL_MODEL_NAME": "openai/fake-model",
        "CREWAI_DISABLE_TELEMETRY": "true",
        "CREWAI_TRACING_ENABLED": "false",
    }.items():
        monkeypatch.setenv(name, value)
    for name in (
        "SAM_LINK_TRUST_STORE_PATH",
        "BEDROCK_MODEL_ID",
        "CLAIMS_PEER_AGENTS",
        "CLAIMS_ASK_USER",
        "SAM_PAY_CLAIM_SCOPE",
        "OTEL_SDK_DISABLED",
    ):
        monkeypatch.delenv(name, raising=False)
    yield server
    await server.stop()


async def serve_one_task(server, start, text: str, granted: list[str] | None = None) -> dict:
    """Start the agent (`start` returns a coroutine that runs it, or a started MeshAgent), send one task, and return
    its result once the agent has answered."""
    runner = asyncio.create_task(start())
    try:
        await eventually(lambda: server.of("consume"), 10)
        await server.send(task(1, "T1", text, grantedToolScopes=granted or []))
        [result] = await eventually(lambda: results(server, "T1"), 20)
        return result
    finally:
        runner.cancel()
        await asyncio.gather(runner, return_exceptions=True)


def tool_results(server) -> dict[str, object]:
    return {
        f["event"]["name"]: f["event"].get("result")
        for f in server.of("event")
        if f["event"].get("kind") == "tool_finished"
    }


async def run_until_cancelled(agent):
    await agent.start()
    try:
        await asyncio.Event().wait()
    finally:
        await agent.stop()


def langchain_model(turns=CLAIM_TURNS):
    from langchain_core.messages import AIMessage

    from .test_callbacks import ToolCallingFake

    return ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content=t,
                    tool_calls=[{"id": c, "name": n, "args": a} for c, n, a in calls],
                    usage_metadata={"input_tokens": i, "output_tokens": o, "total_tokens": i + o},
                )
                for t, calls, (i, o) in turns
            ]
        )
    )


def openai_agents_model(turns=CLAIM_TURNS):
    from .test_openai_agents import ScriptedModel, call, message, usage

    return ScriptedModel(
        [
            ([*([message(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(i, o))
            for t, calls, (i, o) in turns
        ]
    )


def strands_model(turns=CLAIM_TURNS):
    from .test_strands import ScriptedModel, call, text, usage

    model = ScriptedModel(
        [
            ([*([text(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(i, o))
            for t, calls, (i, o) in turns
        ]
    )
    model.config = {"model_id": model.model_id}
    return model


def agent_framework_model(turns=CLAIM_TURNS):
    from .test_agent_framework import ScriptedClient, call, text, usage

    return ScriptedClient(
        [
            ([*([text(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(i, o))
            for t, calls, (i, o) in turns
        ]
    )


def adk_model(turns=CLAIM_TURNS):
    from google.genai import types as genai

    from .test_adk import Scripted, fc, usage

    return Scripted(
        turns=[
            ([*([genai.Part(text=t)] if t else []), *(fc(n, a, call_id=c) for c, n, a in calls)], usage(i, o))
            for t, calls, (i, o) in turns
        ]
    )


def crewai_model(turns=CLAIM_TURNS):
    """A CrewAI LLM answering in CrewAI's text (ReAct) tool-calling format."""
    from crewai.llms.base_llm import BaseLLM

    replies = [
        f"Thought: next\nAction: {n}\nAction Input: {json.dumps(a)}" for _, calls, _ in turns for _, n, a in calls
    ] + [f"Thought: done\nFinal Answer: {turns[-1][0]}"]

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


def fake_hermes(monkeypatch, turns=CLAIM_TURNS):
    """Stand-ins for the modules of a Hermes checkout the example and the adapter import, with an AIAgent whose turn
    follows `turns` through Hermes' hooks and the tools registered in its registry."""
    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)
    handlers: dict[str, object] = {}
    hermes_registry = SimpleNamespace(register=lambda name, handler, **kw: handlers.__setitem__(name, handler))
    sys.modules["tools.registry"].registry = hermes_registry
    interrupt = types.ModuleType("tools.interrupt")
    interrupt.is_interrupted = lambda: False
    run_agent_module = types.ModuleType("run_agent")

    class AIAgent:
        def __init__(self, session_id="", enabled_toolsets=(), ephemeral_system_prompt="", **kwargs):
            self.session_id = session_id
            self.ephemeral_system_prompt = ephemeral_system_prompt
            self.tools = [{"type": "function", "function": {"name": n}} for n in handlers]
            self.valid_tool_names = set(handlers)
            self.stream_delta_callback = None
            self.tool_gen_callback = None

        def run_conversation(self, text, conversation_history=None, task_id=""):
            for _, calls, usage in turns:
                hermes.model_call(self.session_id, [(c, n) for c, n, _ in calls], usage)
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
            return {"final_response": turns[-1][0], "messages": [], "completed": True}

        def hard_interrupt(self):
            pass

    run_agent_module.AIAgent = AIAgent
    monkeypatch.setitem(sys.modules, "tools.interrupt", interrupt)
    monkeypatch.setitem(sys.modules, "run_agent", run_agent_module)


CLAIMS = {
    "claims_triage_langchain": ("langchain_core", langchain_model),
    "claims_triage_langgraph": ("langgraph", langchain_model),
    "claims_triage_openai_agents": ("agents", openai_agents_model),
    "claims_triage_adk": ("google.adk", adk_model),
    "claims_triage_crewai": ("crewai", crewai_model),
    "claims_triage_strands": ("strands", strands_model),
    "claims_triage_agent_framework": ("agent_framework", agent_framework_model),
    "claims_triage_hermes": (None, None),
    "claims_triage_otel": ("strands", strands_model),
}


def test_every_claims_triage_example_runs_here():
    assert sorted(CLAIMS) == sorted(p.stem for p in EXAMPLES.glob("claims_triage_*.py"))


@pytest.mark.parametrize("name", sorted(CLAIMS))
async def test_a_claims_triage_example_serves_a_task_through_its_approval_and_scope(name, link, monkeypatch):
    """main() as it is, joined through the agent link, with route_claim behind an approval and a scope the task's user
    was granted (the OpenTelemetry example declares no scope and asks no approval: spans could not enforce them)."""
    need, model = CLAIMS[name]
    if need:
        pytest.importorskip(need)
    if name == "claims_triage_hermes":
        fake_hermes(monkeypatch)
    otel = name == "claims_triage_otel"
    if not otel:
        monkeypatch.setenv("CLAIMS_APPROVAL_TOOLS", "route_claim")
        monkeypatch.setenv("CLAIMS_ROUTE_SCOPE", ROUTE_SCOPE)
    example = load_example(name)
    monkeypatch.setattr(example, "build_model", lambda settings: model() if model else {"model": "fake-model"})
    if otel:
        from opentelemetry.sdk.trace import TracerProvider

        monkeypatch.setattr(
            example,
            "step_reporter",
            lambda: __import__("sam_link_sdk.otel_steps").otel_steps.step_reporter(TracerProvider()),
        )
    result = await serve_one_task(link, example.main, "Triage claim P-1182", granted=[ROUTE_SCOPE])
    assert result["text"] == "P-1182 is routed to fast-track."
    if not otel:
        assert [p["approval"]["tool"] for p in link.of("prompt")] == ["route_claim"]
        assert "CLM-" in json.dumps(tool_results(link)["route_claim"])
        [described] = link.of("describe")[:1]
        scoped = [t for t in described["agent"]["tools"] if t["name"] == "route_claim"]
        assert scoped and scoped[0].get("requiredScopes") == [ROUTE_SCOPE]


MINIMAL_TURNS = [
    ("", [("call_a", "lookup_policy", {"policy_id": "P-1182"})], (300, 20)),
    ("P-1182 is active.", [], (340, 15)),
]
MINIMAL = {
    "minimal_plain": (None, None),
    "minimal_langchain": ("langchain_core", langchain_model),
    "minimal_langgraph": ("langgraph", langchain_model),
    "minimal_openai_agents": ("agents", openai_agents_model),
    "minimal_adk": ("google.adk", adk_model),
    "minimal_crewai": ("crewai", crewai_model),
    "minimal_strands": ("strands", strands_model),
    "minimal_agent_framework": ("agent_framework", agent_framework_model),
    "minimal_hermes": (None, None),
}


def test_every_minimal_example_runs_here():
    assert sorted(MINIMAL) == sorted(p.stem for p in EXAMPLES.glob("minimal_*.py"))


@pytest.mark.parametrize("name", sorted(MINIMAL))
async def test_a_minimal_example_starts_and_answers_a_task_on_a_scripted_model(name, link, monkeypatch):
    """main() as a reader copies it from the docs, joined through the agent link, with only the model scripted."""
    need, model = MINIMAL[name]
    if need:
        pytest.importorskip(need)
    if name == "minimal_hermes":
        fake_hermes(monkeypatch, MINIMAL_TURNS)
        monkeypatch.setenv("OPENAI_API_KEY", "not-a-real-key")
    example = load_example(name)
    if model:
        monkeypatch.setattr(example, "build_model", lambda: model(MINIMAL_TURNS))
    text = "Is policy P-1182 active?"
    result = await serve_one_task(link, example.main, text)
    if name == "minimal_plain":
        assert result["text"] == f"Received: {text}"
        assert [f["event"]["text"] for f in link.of("event") if f["event"].get("kind") == "progress"] == [
            "Looking the claim up"
        ]
    else:
        assert result["text"] == "P-1182 is active."
        assert "active" in json.dumps(tool_results(link)["lookup_policy"])


def load(path: str):
    import importlib.util

    sys.path.insert(0, str((EXAMPLES / path).parent))
    try:
        spec = importlib.util.spec_from_file_location(f"example_{path.replace('/', '_')[:-3]}", EXAMPLES / path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    finally:
        sys.path.pop(0)


def strands_turns(*turns):
    from .test_strands import ScriptedModel, call, text, usage

    return ScriptedModel(
        [([*([text(t)] if t else []), *(call(c, n, a) for c, n, a in calls)], usage(1, 1)) for t, calls in turns]
    )


@pytest.mark.parametrize("runtime", ["foundry", "agentcore"])
async def test_a_serverless_template_pays_a_claim_after_the_user_approves(runtime, link, monkeypatch):
    """Foundry's _model() is the model; AgentCore's is (the model, its name in SAM's cost ledger)."""
    pytest.importorskip("strands")
    monkeypatch.setenv("SAM_PAY_CLAIM_SCOPE", "tool:claims:pay")
    template = load(f"serverless/{runtime}/agent.py")
    model = strands_turns(("", [("call_p", "pay_claim", {"claim_id": "C-1", "amount": 100.0})]), ("Paid C-1.", []))
    monkeypatch.setattr(template, "_model", lambda: model if runtime == "foundry" else (model, "fake-model"))
    agent = template.build_agent(link.url)
    assert isinstance(agent._transport, LinkTransport)
    result = await serve_one_task(
        link, lambda: run_until_cancelled(agent), "Pay claim C-1, 100 USD", granted=["tool:claims:pay"]
    )
    assert result["text"] == "Paid C-1."
    assert [p["approval"]["tool"] for p in link.of("prompt")] == ["pay_claim"]
    assert tool_results(link)["pay_claim"]["reference"] == "PAY-C-1"


SCRIPTS = {
    "serverless/tester_strands.py": (
        "File a note: called back",
        [("", [("call_n", "file_note", {"text": "called back"})]), ("Filed.", [])],
        ["file_note"],
        "Filed.",
    ),
    "serverless/coverage_explainer_strands.py": (
        "What does gold cover?",
        [("", [("call_c", "coverage", {"tier": "gold"})]), ("Gold covers water damage.", [])],
        [],
        "Gold covers water damage.",
    ),
    "serverless/echo_app.py": ("hello", [], [], None),
}


@pytest.mark.parametrize("script", sorted(SCRIPTS))
async def test_a_serverless_script_serves_a_task(script, link, monkeypatch):
    """The script as the runtime starts it (``<script> wake``): it joins the agent link as itself and serves the task
    waiting for it."""
    text, turns, approvals, answer = SCRIPTS[script]
    if turns:
        pytest.importorskip("strands")
    module = load(script)
    monkeypatch.setattr(test_link, "AGENT", module.NAME)
    if turns:
        monkeypatch.setattr(module, "OpenAIModel", lambda **kw: strands_turns(*turns))
    result = await serve_one_task(link, lambda: module.main("wake"), text)
    assert result["text"] == answer if answer else text in result["text"]
    assert [p["approval"]["tool"] for p in link.of("prompt")] == approvals


def test_the_runtime_stand_in_starts_a_wake_for_each_post_to_its_invocation_route(monkeypatch):
    import threading
    import urllib.error
    import urllib.request
    from http.server import ThreadingHTTPServer

    standin = load("serverless/runtime_standin.py")
    started: list[tuple[str, str]] = []
    monkeypatch.setattr(standin, "spawn", lambda script, mode: started.append((script, mode)) or 4242)
    server = ThreadingHTTPServer(("127.0.0.1", 0), standin.handler("coverage_explainer_strands.py"))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{server.server_address[1]}"
    try:
        for _ in range(2):
            with urllib.request.urlopen(
                urllib.request.Request(f"{base}/invocations", data=b"{}", method="POST"), timeout=5
            ) as reply:
                assert reply.status == 202
        with pytest.raises(urllib.error.HTTPError, match="404"):
            urllib.request.urlopen(urllib.request.Request(f"{base}/other", data=b"{}", method="POST"), timeout=5)
    finally:
        server.shutdown()
        server.server_close()
    assert started == [("coverage_explainer_strands.py", "wake")] * 2


def test_every_serverless_agent_runs_here():
    agents = {
        str(p.relative_to(EXAMPLES)) for p in (EXAMPLES / "serverless").rglob("*.py") if "def build" in p.read_text()
    }
    assert agents == {*SCRIPTS, "serverless/foundry/agent.py", "serverless/agentcore/agent.py"}


async def test_the_langgraph_example_gives_each_delivery_of_a_run_based_task_a_thread_of_its_own():
    """A run-based task (a peer call, a workflow node) runs on a thread dropped afterwards. A run given up may still
    write to its thread while SAM delivers the task again, so each delivery gets its own: not the task id alone."""
    pytest.importorskip("langgraph")
    from langgraph.checkpoint.memory import MemorySaver

    threads: list[str] = []

    class Recording(MemorySaver):
        async def aput(self, config, *args, **kwargs):
            threads.append(config["configurable"]["thread_id"])
            return await super().aput(config, *args, **kwargs)

    example = load_example("claims_triage_langgraph")

    for _ in range(2):
        handler = example.make_handler(langchain_model(), checkpointer=Recording())
        async with adapter_task(
            task_id="T-peer", context_id="", text="Triage claim P-1182", session_behavior=RUN
        ) as delivery:
            await delivery.run(handler)
    delivered = sorted(set(threads))
    assert len(delivered) == 2 and all(t.startswith("T-peer-") for t in delivered)
