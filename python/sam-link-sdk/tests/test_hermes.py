# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Hermes Agent adapter: a real Hermes AIAgent driven offline by a scripted OpenAI-compatible server.

Hermes hides tools behind a search tool once many are enabled; the tests want the model to see them directly.

Hermes keeps its state (config, sessions, caches) under HERMES_HOME; it is pointed at a scratch directory before Hermes loads.
"""

from __future__ import annotations

import asyncio
import json
import os
import tempfile
import threading
from types import SimpleNamespace

import pytest

os.environ["HERMES_HOME"] = tempfile.mkdtemp(prefix="sam-link-sdk-hermes-test-")
os.environ.setdefault("HERMES_DISABLE_TELEMETRY", "1")
with open(os.path.join(os.environ["HERMES_HOME"], "config.yaml"), "w") as config:
    config.write("tools:\n  tool_search:\n    enabled: off\napprovals:\n  mode: manual\n")

pytest.importorskip("run_agent")

from run_agent import AIAgent
from tools.registry import registry

from sam_link_sdk._model import RUN
from sam_link_sdk.events import ModelFinished, ModelStarted, Thinking, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.hermes import agent_defaults, run_agent

from .adapter_task import adapter_task, steps
from .hermes_fake_llm import FakeLLM

TEST_TOOLSET = "sam_test"
CALLS: list[tuple[str, dict]] = []


def _register(name, handler, description, properties):
    registry.register(
        name=name,
        toolset=TEST_TOOLSET,
        description=description,
        schema={
            "name": name,
            "description": description,
            "parameters": {"type": "object", "properties": properties, "required": list(properties)},
        },
        handler=handler,
    )


def _lookup(args, **kw):
    CALLS.append(("lookup_policy", args))
    return json.dumps({"policy_id": args.get("policy_id"), "tier": "gold"})


def _broken(args, **kw):
    CALLS.append(("broken", args))
    return json.dumps({"error": "backend down"})


_register("lookup_policy", _lookup, "Look up a policy.", {"policy_id": {"type": "string"}})
_register("broken", _broken, "Always fails.", {"policy_id": {"type": "string"}})


@pytest.fixture
def llm():
    servers = []

    def make(turns):
        servers.append(FakeLLM(turns))
        return servers[-1]

    yield make
    for s in servers:
        s.close()


@pytest.fixture(autouse=True)
def reset_calls():
    CALLS.clear()


def builder(server, toolsets=(TEST_TOOLSET,), **extra):
    def build(ctx):
        return AIAgent(
            **agent_defaults(ctx),
            base_url=server.base_url,
            api_key="test-key",
            model="fake-model",
            provider="custom",
            enabled_toolsets=list(toolsets),
            **extra,
        )

    return build


def ending_turn(build, turn_over):
    """``build``, with the agent's turn setting ``turn_over`` when it ends: a cancelled run_agent no longer waits for
    its turn's thread, and a test that asserts what the turn did not do waits for it."""

    def wrapped(ctx):
        agent = build(ctx)
        conversation = agent.run_conversation

        def run_conversation(*args, **kwargs):
            try:
                return conversation(*args, **kwargs)
            finally:
                turn_over.set()

        agent.run_conversation = run_conversation
        return agent

    return wrapped


async def stop_task(task, halt: str) -> None:
    """Stop the task while its run works: SAM's cancel ("cancelled"), or a stop such as a limit ("halted")."""
    if halt == "cancelled":
        await task.cancel()
    else:
        task.ctx._run.halt()


async def test_parallel_tool_calls_pair_by_call_id_and_usage_sums(llm):
    server = llm(
        [
            {
                "text": "Looking both up.",
                "calls": [
                    ("call_a", "lookup_policy", {"policy_id": "P-1"}),
                    ("call_b", "lookup_policy", {"policy_id": "P-2"}),
                ],
                "usage": (800, 40),
            },
            {"text": "Both are gold.", "usage": (900, 20)},
        ]
    )
    async with adapter_task("T1") as task:
        assert await run_agent(task.ctx, builder(server)) == "Both are gold."
        sent = steps(task)

    kinds = [type(e) for e in sent]
    assert kinds[:2] == [ModelStarted, ModelFinished]
    assert sorted(k.__name__ for k in kinds[2:6]) == ["ToolFinished", "ToolFinished", "ToolStarted", "ToolStarted"]
    assert kinds[6:] == [ModelStarted, ModelFinished]
    assert sent[0] == ModelStarted("fake-model")

    response = sent[1]
    assert response.text == "Looking both up."
    assert response.tool_calls == (
        ToolCall("call_a", "lookup_policy", {"policy_id": "P-1"}),
        ToolCall("call_b", "lookup_policy", {"policy_id": "P-2"}),
    )
    assert response.usage.input_tokens == 800 and response.usage.output_tokens == 40

    starts = {e.call_id: e for e in sent if isinstance(e, ToolStarted)}
    results = {e.call_id: e for e in sent if isinstance(e, ToolFinished)}
    assert set(starts) == set(results) == {"call_a", "call_b"}
    assert starts["call_a"].args == {"policy_id": "P-1"}
    assert results["call_b"].result == {"policy_id": "P-2", "tier": "gold"}

    tu = task.ctx._run.usage.final_token_usage()
    assert (tu["total_input_tokens"], tu["total_output_tokens"]) == (1700, 60)
    assert tu["last_input_tokens"] == 900 and list(tu["by_model"]) == ["fake-model"]


async def test_tool_failure_is_reported_and_run_continues(llm):
    server = llm([{"calls": [("call_x", "broken", {"policy_id": "P-9"})]}, {"text": "Sorry, the backend is down."}])
    async with adapter_task("T1") as task:
        assert await run_agent(task.ctx, builder(server)) == "Sorry, the backend is down."
        result = task.transport.events(task.task_id, ToolFinished)[0]
    assert result.call_id == "call_x" and result.result == {"error": "backend down"} and result.failed


async def test_a_task_without_streaming_still_counts_usage(llm):
    async with adapter_task("T1", streaming=False) as task:
        assert await run_agent(task.ctx, builder(llm([{"text": "hi", "usage": (5, 1)}]))) == "hi"
        assert task.events == [] and task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 5


async def test_the_agent_leaves_no_memory_or_stored_session_behind(llm):
    server = llm([{"text": "hi"}])
    async with adapter_task("T1") as task:
        await run_agent(task.ctx, builder(server))
    from hermes_constants import get_hermes_home

    home = get_hermes_home()
    assert not any((home / "memories").glob("*")) if (home / "memories").exists() else True
    assert not list(home.glob("**/*.db")) or all(
        "sam-T1" not in p.read_bytes().decode("latin-1") for p in home.glob("**/*.db")
    )


async def test_a_conversation_is_replayed_per_session_and_a_run_based_task_has_no_history(llm):
    from sam_link_sdk.memory import ConversationMemory

    memory = ConversationMemory()
    async with adapter_task("T1", "I am Ana", context_id="CHAT") as task:
        await run_agent(task.ctx, builder(llm([{"text": "Hi Ana."}])), memory=memory)

    second = llm([{"text": "You are Ana."}])
    async with adapter_task("T2", "Who am I?", context_id="CHAT") as task:
        assert await run_agent(task.ctx, builder(second), memory=memory) == "You are Ana."
    seen = json.dumps(second.requests[0]["messages"])
    assert "I am Ana" in seen and "Hi Ana." in seen and "Who am I?" in seen

    third = llm([{"text": "I do not know."}])
    async with adapter_task("T3", "Who am I?", context_id="CHAT", session_behavior=RUN) as task:
        await run_agent(task.ctx, builder(third), memory=memory)
    assert "Ana" not in json.dumps(third.requests[0]["messages"])
    assert len(memory.history("CHAT")) == 4


async def test_reasoning_is_streamed_only_when_stream_thinking_is_on(llm):
    turn = {"reasoning": "The policy is gold.", "text": "Fast-track."}
    async with adapter_task("T1", stream_thinking=True) as task:
        assert await run_agent(task.ctx, builder(llm([dict(turn)]))) == "Fast-track."
        thinking = task.transport.events(task.task_id, Thinking)
    assert thinking and thinking[0].text == "The policy is gold."
    assert thinking[-1].done is True

    async with adapter_task("T1") as task:
        await run_agent(task.ctx, builder(llm([dict(turn)])))
        assert not task.transport.events(task.task_id, Thinking)


async def test_the_gateways_request_context_reaches_the_model_only_on_request(llm):
    from sam_link_sdk.request_context import RequestContext

    for include in (True, False):
        server = llm([{"text": "ok"}])
        async with adapter_task("T1") as task:
            task.ctx.request_context = RequestContext(system_purpose="Help claims handlers.")
            await run_agent(task.ctx, builder(server), include_request_context=include)
        sent = json.dumps(server.requests[0]["messages"])
        assert ("## System Purpose" in sent and "Help claims handlers." in sent) is include


async def test_tools_a_user_lacks_scopes_for_are_hidden_from_the_model_and_the_denial_is_audited_once(llm):
    records = []
    server = llm([{"calls": [("c1", "lookup_policy", {"policy_id": "P-1"})]}, {"text": "ok"}])
    async with adapter_task(
        "T1",
        scopes={"broken": ["tool:claims:break"]},
        declared=["lookup_policy"],
        granted=[],
        user_id="alice",
        audit=records.append,
    ) as task:
        await run_agent(task.ctx, builder(server))
    offered = [[t["function"]["name"] for t in r["tools"]] for r in server.requests]
    assert offered == [["lookup_policy"], ["lookup_policy"]]
    assert [(r.message, r.fields["tool"]) for r in records if r.message == "tool access denied"] == [
        ("tool access denied", "broken")
    ]


async def test_a_call_to_a_denied_tool_is_refused_by_the_hook_and_reported_as_the_refusal():
    from sam_link_sdk.hermes import steps as hermes_steps

    async with adapter_task("T1", scopes={"broken": ["tool:claims:break"]}, granted=[], user_id="alice") as task:
        run = hermes_steps.Run(task.ctx, asyncio.get_running_loop(), "denied-1")
        hermes_steps.register_run(run)
        try:
            directive = await asyncio.to_thread(
                hermes_steps.pre_tool_call, session_id="denied-1", tool_name="broken", args={}, tool_call_id="c1"
            )
        finally:
            hermes_steps.unregister_run(run)
        sent = steps(task)
    assert directive == {
        "action": "block",
        "message": "Access denied: you do not have the scopes required to use broken.",
    }
    assert [(type(e), getattr(e, "result", None)) for e in sent] == [
        (ToolStarted, None),
        (ToolFinished, {"error": "Access denied: you do not have the scopes required to use broken."}),
    ]


async def test_a_denied_approval_stops_the_tool_and_the_model_is_told(llm):
    from sam_link_sdk.hermes import with_approval

    server = llm([{"calls": [("c1", "lookup_policy", {"policy_id": "P-1"})]}, {"text": "Not looked up."}])
    asked, timeouts = [], []

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append((name, args, message))
        timeouts.append(timeout_seconds)
        return SimpleNamespace(approved=False, outcome="denied")

    async with adapter_task("T1") as task:
        task.ctx.request_approval = request_approval
        answer = await run_agent(
            task.ctx, builder(server), approval=with_approval(names=["lookup_policy"], message="Please check")
        )
        sent = steps(task)
    assert answer == "Not looked up."
    assert CALLS == [] and asked == [("lookup_policy", {"policy_id": "P-1"}, "Please check")]
    assert timeouts[0] is not None and 0 < timeouts[0] < 600
    kinds = [type(e) for e in sent]
    assert kinds.index(ToolStarted) < kinds.index(ToolFinished)
    result = [e for e in sent if isinstance(e, ToolFinished)][0]
    assert "did not approve" in json.dumps(result.result)
    assert "did not approve" in json.dumps(server.requests[1]["messages"])


async def test_an_approved_tool_runs_and_an_unlisted_tool_is_not_asked_about(llm):
    from sam_link_sdk.hermes import with_approval

    asked = []

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        return SimpleNamespace(approved=True, outcome="approved")

    turns = lambda: [{"calls": [("c1", "lookup_policy", {"policy_id": "P-1"})]}, {"text": "Gold."}]  # noqa: E731
    async with adapter_task("T1") as task:
        ctx = task.ctx
        ctx.request_approval = request_approval
        assert await run_agent(ctx, builder(llm(turns())), approval=with_approval(names=["broken"])) == "Gold."
        assert asked == [] and CALLS == [("lookup_policy", {"policy_id": "P-1"})]
        assert await run_agent(ctx, builder(llm(turns())), approval=with_approval()) == "Gold."
        assert asked == ["lookup_policy"]


async def test_the_model_can_ask_the_user_a_question(llm):
    server = llm(
        [
            {"calls": [("c1", "clarify", {"questions": [{"question": "Which policy?", "choices": ["P-1", "P-2"]}]})]},
            {"text": "Thanks."},
        ]
    )
    asked = []

    async def ask_user(questions):
        asked.append([(q.text, [o.label for o in q.options]) for q in questions])
        return SimpleNamespace(answered=True, answers=("P-2",), status="answered")

    async with adapter_task("T1") as task:
        task.ctx.ask_user = ask_user
        assert await run_agent(task.ctx, builder(server, toolsets=(TEST_TOOLSET, "clarify"))) == "Thanks."
    assert asked == [[("Which policy?", ["P-1", "P-2"])]]
    assert "P-2" in json.dumps(server.requests[1]["messages"])


async def test_mesh_agents_are_tools_that_pass_the_call_id(llm):
    from sam_link_sdk import PeerResult
    from sam_link_sdk.delegation import AgentCard
    from sam_link_sdk.hermes import mesh_agent_tools

    calls = []
    cards = [AgentCard("policy-bot", "Answers policy questions.", "Policy Bot", ["Lookup"]), AgentCard("billing")]

    async def call_agent(agent, task_description, *, function_call_id=""):
        calls.append((agent, task_description, function_call_id))
        return PeerResult(agent, "completed", f"answer from {agent}")

    async with adapter_task("T1") as task:
        ctx = task.ctx
        ctx.available_agents = lambda: list(cards)
        ctx.call_agent = call_agent

        tools = mesh_agent_tools(ctx)
        assert [t.name for t in tools] == ["peer_policy-bot", "peer_billing"]
        assert tools[0].description == (
            'Delegate a task to the "Policy Bot" agent. Answers policy questions. Skills: Lookup'
        )
        assert [t.name for t in mesh_agent_tools(ctx, ["billing"])] == ["peer_billing"]

        server = llm([{"calls": [("tu_1", "peer_policy-bot", {"task_description": "Check P-1"})]}, {"text": "Done."}])
        assert await run_agent(ctx, builder(server), extra_tools=tools) == "Done."
    assert calls == [("policy-bot", "Check P-1", "tu_1")]
    assert "answer from policy-bot" in json.dumps(server.requests[1]["messages"])


async def test_a_hermes_agent_outside_any_mesh_task_cannot_call_a_peer_a_mesh_task_registered(llm):
    """Peer tools stay in Hermes' process-wide registry after the task that added them, under the sam_link_sdk toolset,
    so another Hermes agent in the process (its CLI, a scheduled job) can be offered one. SAM never sees that agent,
    since it is no mesh task: the tool answers that it is not available, and no peer is called."""
    from sam_link_sdk import PeerResult
    from sam_link_sdk.delegation import AgentCard
    from sam_link_sdk.hermes import mesh_agent_tools
    from sam_link_sdk.hermes.peer_tools import TOOLSET

    calls = []

    async def call_agent(agent, task_description, *, function_call_id=""):
        calls.append(agent)
        return PeerResult(agent, "completed", f"answer from {agent}")

    async with adapter_task("T-peer-registered") as task:
        task.ctx.available_agents = lambda: [AgentCard("outside-bot")]
        task.ctx.call_agent = call_agent
        tools = mesh_agent_tools(task.ctx)
        assert await run_agent(task.ctx, builder(llm([{"text": "Nothing to delegate."}])), extra_tools=tools) == (
            "Nothing to delegate."
        )

    server = llm([{"calls": [("tu_1", "peer_outside-bot", {"task_description": "Check P-1"})]}, {"text": "Done."}])
    outsider = AIAgent(
        session_id="cli-session",
        base_url=server.base_url,
        api_key="test-key",
        model="fake-model",
        provider="custom",
        enabled_toolsets=[TOOLSET],
        quiet_mode=True,
        skip_memory=True,
        skip_context_files=True,
    )
    assert "peer_outside-bot" in outsider.valid_tool_names
    await asyncio.to_thread(outsider.run_conversation, "Ask the outside bot.", task_id="cli-session")
    assert calls == []
    assert "This tool is not available in this task." in json.dumps(server.requests[1]["messages"])


def test_tool_infos_lists_the_agents_tools():
    from sam_link_sdk.hermes import tool_infos

    schemas = [{"type": "function", "function": {"name": "lookup_policy", "description": "Look up a policy."}}]
    assert [(i.name, i.description) for i in tool_infos(schemas)] == [("lookup_policy", "Look up a policy.")]


async def test_a_retried_model_call_is_reported_once(llm):
    server = llm([{"status": 429, "message": "Rate limit reached"}, {"text": "ok", "usage": (7, 1)}])
    async with adapter_task("T1") as task:
        assert await run_agent(task.ctx, builder(server)) == "ok"
        kinds = [type(e) for e in steps(task)]
    assert kinds == [ModelStarted, ModelFinished] and len(server.requests) == 2
    assert task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 7


async def test_a_failed_model_call_is_recorded_with_its_category_and_the_task_fails(llm):
    server = llm([{"status": 401, "message": "Incorrect API key provided"}])
    async with adapter_task("T1") as task:
        with pytest.raises(RuntimeError):
            await run_agent(task.ctx, builder(server))
    assert (task.ctx._run.llm_failure.category, task.ctx._run.llm_failure.provider) == ("auth_failure", "other")


async def test_a_cancelled_task_interrupts_the_agent_and_it_stops_before_the_next_model_call(llm):
    import time

    from tools.interrupt import is_interrupted

    from sam_link_sdk.hermes import steps as bridge

    started, finished = threading.Event(), threading.Event()

    def slow(args, **kw):
        started.set()
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline and not is_interrupted():
            time.sleep(0.05)
        finished.set()
        return json.dumps({"error": "interrupted"})

    _register("slow_lookup", slow, "Slow lookup.", {"policy_id": {"type": "string"}})
    server = llm([{"calls": [("c1", "slow_lookup", {"policy_id": "P-1"})]}, {"text": "late"}])
    sessions = []

    def build(c):
        agent = builder(server)(c)
        sessions.append(agent.session_id)
        return agent

    async with adapter_task("T-cancel") as mesh_task:
        task = asyncio.create_task(run_agent(mesh_task.ctx, build))
        await asyncio.wait_for(asyncio.to_thread(started.wait, 20), 25)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert await asyncio.wait_for(asyncio.to_thread(finished.wait, 15), 20)
        for _ in range(100):
            if bridge.run_for(sessions[0]) is None:
                break
            await asyncio.sleep(0.1)
    assert sessions[0].startswith("sam-T-cancel-")
    assert bridge.run_for(sessions[0]) is None and len(server.requests) == 1


class Switch:
    """Stands in for MeshAgent: check() raises while stopped; on_stop_change listeners hear changes."""

    def __init__(self, stopped=False):
        self.stopped = stopped
        self.listeners = []

    def check(self):
        from sam_link_sdk import AgentStopped

        if self.stopped:
            raise AgentStopped("stopped_by_sam")

    def on_stop_change(self, listener):
        self.listeners.append(listener)

    def stop(self):
        self.stopped = True
        for listener in self.listeners:
            listener(False, "stopped_by_sam")

    def release(self):
        self.stopped = False
        for listener in self.listeners:
            listener(True, "")


async def test_a_watched_hermes_agent_refuses_to_run_while_stopped_and_its_turn_is_interrupted(llm):
    import time

    from tools.interrupt import is_interrupted

    from sam_link_sdk import AgentStopped
    from sam_link_sdk.hermes.stop import StopGuard

    started, finished = threading.Event(), threading.Event()

    def slow(args, **kw):
        started.set()
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline and not is_interrupted():
            time.sleep(0.05)
        finished.set()
        return json.dumps({"error": "interrupted"})

    _register("slow_guarded", slow, "Slow lookup.", {"policy_id": {"type": "string"}})
    switch = Switch()
    guard = StopGuard(switch)
    try:
        async with adapter_task("T-ks") as task, adapter_task("T-ks2") as second:
            server = llm([{"calls": [("c1", "slow_guarded", {"policy_id": "P-1"})]}, {"text": "late"}])
            agent = guard.watch(builder(server)(task.ctx))
            turn = asyncio.create_task(asyncio.to_thread(agent.run_conversation, "go"))
            assert await asyncio.wait_for(asyncio.to_thread(started.wait, 20), 25)
            switch.stop()
            result = await asyncio.wait_for(turn, 25)
            assert finished.is_set() and result["interrupted"] and len(server.requests) == 1
            with pytest.raises(AgentStopped):
                agent.run_conversation("hello")
            with pytest.raises(AgentStopped):
                guard.watch(builder(llm([{"text": "hi"}]))(second.ctx))
    finally:
        guard.uninstall()


async def test_an_agent_stopped_while_idle_runs_its_next_turn_after_the_release(llm):
    from sam_link_sdk.hermes.stop import StopGuard

    switch = Switch()
    guard = StopGuard(switch)
    try:
        async with adapter_task("T-idle") as task:
            server = llm([{"text": "hi"}])
            agent = guard.watch(builder(server)(task.ctx))
            switch.stop()
            switch.release()
            result = agent.run_conversation("hello")
        assert not result.get("interrupted"), f"interrupted without a model call: {len(server.requests)} requests"
        assert result["final_response"] == "hi" and len(server.requests) == 1
    finally:
        guard.uninstall()


async def test_once_sam_releases_the_agent_a_watched_hermes_agent_runs_its_tools_again(llm):
    """SAM-Go's stop API is not in the build the e2e suite runs (docs/contributing/testing.md), so the release is driven here: the
    guard lets the agent's entry points and tools run again. The agent was idle, so neither the stop nor the release
    interrupts it."""
    from sam_link_sdk import AgentStopped
    from sam_link_sdk.hermes.stop import StopGuard

    switch = Switch()
    guard = StopGuard(switch)
    try:
        server = llm([{"calls": [("c1", "lookup_policy", {"policy_id": "P-1"})]}, {"text": "P-1 is gold."}])
        async with adapter_task("T-ks-release") as task:
            agent = guard.watch(builder(server)(task.ctx))
            interrupted = []
            agent.hard_interrupt = lambda *a, **k: interrupted.append(True)
            switch.stop()
            with pytest.raises(AgentStopped):
                guard.check()
            switch.release()
            assert interrupted == []
            guard.check()
            result = await asyncio.to_thread(agent.run_conversation, "Look up P-1.")
        assert result["final_response"] == "P-1 is gold."
        assert CALLS == [("lookup_policy", {"policy_id": "P-1"})]
    finally:
        guard.uninstall()


async def test_a_stopped_agent_refuses_every_tool_call_and_drops_gateway_messages(llm):
    from sam_link_sdk.hermes.stop import StopGuard

    switch = Switch()
    guard = StopGuard(switch)
    try:
        assert (
            guard._pre_tool_call(tool_name="lookup_policy") is None and guard._pre_gateway_dispatch(event=None) is None
        )
        switch.stop()
        refused = guard._pre_tool_call(tool_name="lookup_policy")
        assert refused["action"] == "block" and "stopped by SAM" in refused["message"]
        assert guard._pre_gateway_dispatch(event=None)["action"] == "skip"
    finally:
        guard.uninstall()


async def test_a_stopped_guard_refuses_a_tool_call_inside_a_running_hermes_agent(llm):
    from sam_link_sdk.hermes.stop import StopGuard

    switch = Switch()
    guard = StopGuard(switch)
    try:

        def stopping(args, **kw):
            switch.stop()
            return json.dumps({"tier": "gold"})

        _register("stopping_lookup", stopping, "Look up, then SAM stops us.", {"policy_id": {"type": "string"}})
        server = llm(
            [
                {"calls": [("c1", "stopping_lookup", {"policy_id": "P-1"})]},
                {"calls": [("c2", "lookup_policy", {"policy_id": "P-1"})]},
                {"text": "done"},
            ]
        )
        async with adapter_task("T-ks3") as task:
            agent = guard.watch(builder(server)(task.ctx))
            agent.run_conversation("go")
        assert CALLS == []
    finally:
        guard.uninstall()


async def test_hermes_own_dangerous_command_prompt_goes_through_sams_approval(llm, tmp_path):
    victim = tmp_path / "scratch"
    victim.mkdir()
    (victim / "keep.txt").write_text("x")
    command = f"rm -rf {victim}"
    asked = []

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append((name, args["command"]))
        return SimpleNamespace(approved=False, outcome="denied")

    server = llm([{"calls": [("c1", "terminal", {"command": command})]}, {"text": "It was not approved."}])
    async with adapter_task("T-term") as task:
        task.ctx.request_approval = request_approval
        assert await run_agent(task.ctx, builder(server, toolsets=("terminal",))) == "It was not approved."
    assert asked and asked[0][0] == "terminal" and victim.name in asked[0][1]
    assert (victim / "keep.txt").exists()


SUBAGENT_GOAL = "Look up policy P-7 for the parent."


def _by_agent(body):
    """The subagent is the agent whose user message is the delegated goal (the parent's history holds the goal only as
    its delegate_task call's arguments)."""
    asked = [json.dumps(m.get("content")) for m in body.get("messages") or [] if m.get("role") == "user"]
    return "child" if any(SUBAGENT_GOAL in text for text in asked) else "parent"


async def test_a_background_subagent_that_outlives_the_mesh_task_runs_no_tool_and_is_interrupted(llm):
    """H4, on real Hermes: a top-level agent's delegate_task runs in the background, so the parent's turn ends while
    the child still runs. The child's tool calls after that are refused (it belongs to an ended task, whether or not
    another mesh task runs), and the child is interrupted.

    The child is still running after the parent's turn (unless the interrupt stopped it before its model call).
    """
    child_reached, release_child = threading.Event(), threading.Event()
    server = FakeLLM(
        {
            "parent": [{"calls": [("call_d", "delegate_task", {"goal": SUBAGENT_GOAL})]}, {"text": "Delegated."}],
            "child": [
                {
                    "calls": [("call_c", "lookup_policy", {"policy_id": "P-7"})],
                    "reached": child_reached,
                    "hold": release_child,
                },
                {"text": "P-7 is gold."},
            ],
        },
        route=_by_agent,
    )
    try:
        async with adapter_task("T1") as task:
            answer = await run_agent(task.ctx, builder(server, toolsets=(TEST_TOOLSET, "delegation")))
            assert answer == "Delegated."
            await asyncio.to_thread(child_reached.wait, 10)
    finally:
        release_child.set()
    for _ in range(250):
        if ("lookup_policy", {"policy_id": "P-7"}) in CALLS or len(server.requests) >= 4:
            break
        await asyncio.sleep(0.02)
    await asyncio.sleep(0.5)
    assert ("lookup_policy", {"policy_id": "P-7"}) not in CALLS


async def test_a_task_stopped_inside_a_tool_interrupts_hermes_and_its_run_ends_in_the_bare_halt(llm):
    """A Hermes hook can block a tool call but not a model call: once the task is stopped (here inside a tool, as a
    limit or SAM's lapsing stop does), the next hook interrupts the agent, its turn ends, and run_agent ends in the bare
    TaskHalted (Hermes reports the interrupted turn as failed, which is the stop, not a crash)."""
    from sam_link_sdk.context import TaskHalted

    async with adapter_task("T-stop") as task:

        def trip(args, **kw):
            CALLS.append(("trip", args))
            task.ctx._run.halt()
            return json.dumps({"ok": True})

        _register("trip_stop", trip, "Trip the stop.", {"policy_id": {"type": "string"}})
        server = llm(
            [{"calls": [("c1", "trip_stop", {"policy_id": "P-1"})]}]
            + [{"calls": [(f"c{n}", "lookup_policy", {"policy_id": "P-2"})]} for n in range(2, 8)]
            + [{"text": "never"}]
        )
        with pytest.raises(TaskHalted) as raised:
            await run_agent(task.ctx, builder(server))
    assert type(raised.value).__module__ == "sam_link_sdk.context" and raised.value.task_id == "T-stop"
    assert ("lookup_policy", {"policy_id": "P-2"}) not in CALLS and len(server.requests) <= 2


async def test_a_task_delivered_again_while_a_given_up_run_lingers_runs_in_a_session_of_its_own(llm):
    """A run given up (a wake cut short, a shutdown) keeps its session until its tool returns; SAM delivers the task
    again meanwhile. The new delivery gets a session of its own, so it runs, and the stale turn's hooks stay with the
    stale run (closed, refused) rather than reaching the new one."""
    started, release = threading.Event(), threading.Event()

    def stubborn(args, **kw):
        """A tool that does not poll `is_interrupted`, as a blocking call or a subprocess does."""
        started.set()
        release.wait(10)
        CALLS.append(("stubborn", args))
        return json.dumps({"ok": True})

    _register("stubborn_lookup", stubborn, "Slow lookup.", {"policy_id": {"type": "string"}})
    server = llm(
        [
            {"calls": [("c1", "stubborn_lookup", {"policy_id": "P-1"})]},
            {"text": "Claim C-1 is approved."},
            {"text": "late"},
        ]
    )
    try:
        async with adapter_task("T-again") as first_task:
            first_ctx = first_task.ctx
            first = asyncio.create_task(run_agent(first_ctx, builder(server)))
            await asyncio.wait_for(asyncio.to_thread(started.wait, 20), 25)
            first_ctx._run.halt()
            first.cancel()
            await asyncio.gather(first, return_exceptions=True)
            async with adapter_task("T-again") as second_task:
                second_ctx = second_task.ctx
                assert await run_agent(second_ctx, builder(server)) == "Claim C-1 is approved."
    finally:
        release.set()
    assert first_ctx.task_id == second_ctx.task_id


async def test_a_given_up_runs_late_turn_end_leaves_the_redeliverys_task_resources_alone(llm):
    """Hermes keys a turn's terminal environment, browser session and file caches by the task id it is given, and
    tears them down when the turn ends. Each delivery gives its own (the run's session), so the late end of a given-up
    run's turn does not tear down the redelivery's."""
    from tools.terminal_tool import _active_environments, _env_lock

    started1, release1, started2, release2 = (threading.Event() for _ in range(4))
    ids: list[str] = []
    torn: list[str] = []

    class Env:
        def __init__(self, owner):
            self.owner = owner

        def cleanup(self, *args, **kwargs):
            torn.append(self.owner)

    def first_tool(args, **kw):
        ids.append(kw.get("task_id"))
        with _env_lock:
            _active_environments[kw.get("task_id")] = Env("first")
        started1.set()
        release1.wait(15)
        return json.dumps({"ok": True})

    def second_tool(args, **kw):
        ids.append(kw.get("task_id"))
        with _env_lock:
            _active_environments[kw.get("task_id")] = Env("second")
        started2.set()
        release2.wait(15)
        with _env_lock:
            ids.append(("still registered", kw.get("task_id") in _active_environments))
        return json.dumps({"ok": True})

    _register("first_lookup", first_tool, "Slow lookup.", {"policy_id": {"type": "string"}})
    _register("second_lookup", second_tool, "Slow lookup.", {"policy_id": {"type": "string"}})
    s1 = llm([{"calls": [("c1", "first_lookup", {"policy_id": "P-1"})]}, {"text": "late"}])
    s2 = llm([{"calls": [("c2", "second_lookup", {"policy_id": "P-1"})]}, {"text": "Claim C-1 is approved."}])
    try:
        async with adapter_task("T-again") as first_task, adapter_task("T-again") as second_task:
            first_ctx = first_task.ctx
            first = asyncio.create_task(run_agent(first_ctx, builder(s1)))
            await asyncio.wait_for(asyncio.to_thread(started1.wait, 20), 25)
            first_ctx._run.halt()
            first.cancel()
            await asyncio.gather(first, return_exceptions=True)
            second = asyncio.create_task(run_agent(second_task.ctx, builder(s2)))
            await asyncio.wait_for(asyncio.to_thread(started2.wait, 20), 25)
            release1.set()
            for _ in range(100):
                if "first" in torn:
                    break
                await asyncio.sleep(0.05)
            release2.set()
            assert await second == "Claim C-1 is approved."
    finally:
        release1.set()
        release2.set()
    assert ids[0] != ids[1] and all(i.startswith("sam-T-again-") for i in ids[:2])
    assert ("still registered", True) in ids and "second" not in torn[:1]


@pytest.mark.parametrize("halt", ["halted", "cancelled"])
async def test_an_approval_that_arrives_after_the_task_was_stopped_runs_no_tool(llm, halt):
    """The turn's thread waits on the approval asked from pre_tool_call; the task is stopped (or cancelled) while the
    prompt is open, and the user approves afterwards: the tool does not run."""
    from types import SimpleNamespace

    from sam_link_sdk.hermes import with_approval

    server = llm([{"calls": [("c1", "lookup_policy", {"policy_id": "P-1"})]}, {"text": "late"}, {"text": "late"}])
    turn_over = threading.Event()
    async with adapter_task("T-late") as task:

        async def request_approval(name, args, *, message="", timeout_seconds=None):
            await asyncio.sleep(0.05)
            await stop_task(task, halt)
            return SimpleNamespace(approved=True, outcome="approved")

        task.ctx.request_approval = request_approval
        try:
            await task.run(
                lambda ctx: run_agent(
                    ctx, ending_turn(builder(server), turn_over), approval=with_approval(names=["lookup_policy"])
                )
            )
        except (Exception, asyncio.CancelledError):
            pass
        assert await asyncio.to_thread(turn_over.wait, 20)
        if halt == "cancelled":
            assert task.answer.state == "canceled"
    assert ("lookup_policy", {"policy_id": "P-1"}) not in CALLS


@pytest.mark.parametrize("halt", ["halted", "cancelled"])
async def test_hermes_own_dangerous_command_prompt_approved_after_the_stop_runs_nothing(llm, tmp_path, halt):
    """Hermes' terminal tool asks SAM's approval for a dangerous command; the task is stopped (or cancelled) while the
    prompt is open and the user approves afterwards: the command does not run (a scratch directory under the test's
    tmp_path stays)."""
    from types import SimpleNamespace

    scratch = tmp_path / "scratch"
    scratch.mkdir()
    (scratch / "keep.txt").write_text("x")
    asked = []
    server = llm(
        [{"calls": [("c1", "terminal", {"command": f"rm -rf {scratch}"})]}, {"text": "late"}, {"text": "late"}]
    )
    turn_over = threading.Event()
    async with adapter_task("T-term-late") as task:

        async def request_approval(name, args, *, message="", timeout_seconds=None):
            asked.append(name)
            await asyncio.sleep(0.05)
            await stop_task(task, halt)
            return SimpleNamespace(approved=True, outcome="approved")

        task.ctx.request_approval = request_approval
        try:
            await task.run(lambda ctx: run_agent(ctx, ending_turn(builder(server, toolsets=("terminal",)), turn_over)))
        except (Exception, asyncio.CancelledError):
            pass
        assert await asyncio.to_thread(turn_over.wait, 20)
        if halt == "cancelled":
            assert task.answer.state == "canceled"
    assert asked == ["terminal"] and (scratch / "keep.txt").exists()
