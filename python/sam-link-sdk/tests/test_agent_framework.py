# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Microsoft Agent Framework adapter, driven offline by a scripted chat client."""

from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace

import pytest

pytest.importorskip("agent_framework")

from agent_framework import (
    Agent,
    BaseChatClient,
    ChatMiddlewareLayer,
    ChatResponse,
    ChatResponseUpdate,
    Content,
    FunctionInvocationLayer,
    FunctionMiddleware,
    Message,
    tool,
)

from sam_link_sdk import AgentStopped, PeerResult
from sam_link_sdk.agent_framework import (
    ask_user_tool,
    mesh_agent_tools,
    run_agent,
    step_reporter,
    stop_guard,
    tool_infos,
    with_approval,
)
from sam_link_sdk.delegation import AgentCard
from sam_link_sdk.events import ModelStarted, Thinking, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.memory import ConversationMemory
from sam_link_sdk.request_context import RequestContext

from .adapter_task import adapter_task, steps


@tool(approval_mode="never_require")
def lookup_policy(policy_id: str) -> dict:
    """Look up a policy."""
    return {"policy_id": policy_id, "tier": "gold"}


@tool(approval_mode="never_require")
def broken(policy_id: str) -> dict:
    """Always fails."""
    raise RuntimeError("backend down")


def call(call_id, name, args):
    return Content.from_function_call(call_id, name, arguments=args)


def text(value):
    return Content.from_text(value)


def usage(i, o, cached=0):
    return {
        "input_token_count": i,
        "output_token_count": o,
        "total_token_count": i + o,
        "cache_read_input_token_count": cached,
    }


class ScriptedClient(FunctionInvocationLayer, ChatMiddlewareLayer, BaseChatClient):
    """Each turn is (contents, usage). Records every request in `requests`; `on_response` runs after each reply."""

    def __init__(self, turns, model="fake-model", on_response=None):
        super().__init__()
        self.turns = list(turns)
        self.model = model
        self.requests: list[dict] = []
        self.on_response = on_response

    def _inner_get_response(self, *, messages, stream, options, **kwargs):
        self.requests.append(
            {
                "messages": [(m.role, m.text) for m in messages],
                "tools": [getattr(t, "name", t) for t in (options or {}).get("tools") or []],
                "contents": [[c.type for c in m.contents] for m in messages],
            }
        )
        contents, u = self.turns.pop(0)

        async def respond():
            if self.on_response:
                self.on_response()
            return ChatResponse(
                messages=[Message(role="assistant", contents=contents)], model=self.model, usage_details=u
            )

        if stream:

            async def updates():
                if self.on_response:
                    self.on_response()
                yield ChatResponseUpdate(
                    contents=[*contents, Content.from_usage(u)], role="assistant", model=self.model
                )

            return self._build_response_stream(updates())
        return respond()


def agent_of(client, tools=(), instructions="You triage claims."):
    return Agent(client, instructions, name="triage", tools=list(tools))


async def test_parallel_tool_calls_pair_by_call_id_and_usage_sums():
    client = ScriptedClient(
        [
            (
                [
                    text("Looking both up."),
                    call("call_a", "lookup_policy", {"policy_id": "P-1"}),
                    call("call_b", "lookup_policy", {"policy_id": "P-2"}),
                ],
                usage(800, 40, cached=100),
            ),
            ([text("Both are gold.")], usage(900, 20)),
        ]
    )
    async with adapter_task() as task:
        assert await run_agent(task.ctx, agent_of(client, [lookup_policy])) == "Both are gold."
        sent = steps(task)
        tu = task.ctx._run.usage.final_token_usage()

    kinds = [e.kind for e in sent]
    assert kinds[:2] == ["model_started", "model_finished"]
    assert sorted(kinds[2:6]) == ["tool_finished", "tool_finished", "tool_started", "tool_started"]
    assert kinds[6:] == ["model_started", "model_finished"]

    inv, resp = sent[0], sent[1]
    assert inv == ModelStarted("fake-model")
    assert (resp.model, resp.text) == ("fake-model", "Looking both up.")
    assert resp.tool_calls == (
        ToolCall("call_a", "lookup_policy", {"policy_id": "P-1"}),
        ToolCall("call_b", "lookup_policy", {"policy_id": "P-2"}),
    )
    assert (resp.usage.input_tokens, resp.usage.output_tokens, resp.usage.cached_input_tokens) == (800, 40, 100)

    starts = {e.call_id: e for e in sent if isinstance(e, ToolStarted)}
    results = {e.call_id: e for e in sent if isinstance(e, ToolFinished)}
    assert set(starts) == set(results) == {"call_a", "call_b"}
    assert starts["call_a"].args == {"policy_id": "P-1"}
    assert results["call_b"].result == {"policy_id": "P-2", "tier": "gold"}

    assert (tu["total_input_tokens"], tu["total_output_tokens"], tu["total_cached_input_tokens"]) == (1700, 60, 100)
    assert tu["last_input_tokens"] == 900 and list(tu["by_model"]) == ["fake-model"]


async def test_the_answer_is_the_final_message_not_the_text_of_every_turn():
    client = ScriptedClient(
        [
            ([text("Looking."), call("c1", "lookup_policy", {"policy_id": "P-1"})], usage(1, 1)),
            ([text("Gold.")], usage(1, 1)),
        ]
    )
    async with adapter_task() as task:
        assert await run_agent(task.ctx, agent_of(client, [lookup_policy])) == "Gold."


async def test_tool_failure_is_reported_and_run_continues():
    client = ScriptedClient(
        [
            ([call("call_x", "broken", {"policy_id": "P-9"})], usage(10, 2)),
            ([text("Sorry, the backend is down.")], usage(12, 3)),
        ]
    )
    async with adapter_task() as task:
        assert await run_agent(task.ctx, agent_of(client, [broken])) == "Sorry, the backend is down."
        result = [e for e in steps(task) if isinstance(e, ToolFinished)][0]
    assert result.call_id == "call_x"
    assert "backend down" in json.dumps(result.result) and "error" in result.result


async def test_a_task_without_streaming_still_counts_usage():
    async with adapter_task(streaming=False) as task:
        assert await run_agent(task.ctx, agent_of(ScriptedClient([([text("hi")], usage(5, 1))]))) == "hi"
        assert steps(task) == [] and task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 5


async def test_one_agent_serves_concurrent_tasks_without_mixing_their_steps():
    agent = agent_of(ScriptedClient([([text("a")], usage(1, 1)), ([text("b")], usage(1, 1))]))
    async with adapter_task("T1", "one") as t1, adapter_task("T2", "two") as t2:
        answers = await asyncio.gather(run_agent(t1.ctx, agent), run_agent(t2.ctx, agent))
        assert sorted(answers) == ["a", "b"]
        assert [len(steps(t)) for t in (t1, t2)] == [2, 2]


async def test_a_conversation_is_replayed_per_session_and_a_run_based_task_has_no_history():
    memory = ConversationMemory()
    async with adapter_task("T1", "I am Ana", context_id="CHAT") as task:
        await run_agent(task.ctx, agent_of(ScriptedClient([([text("Hi Ana.")], usage(5, 1))])), memory=memory)

    second = ScriptedClient([([text("You are Ana.")], usage(9, 2))])
    async with adapter_task("T2", "Who am I?", context_id="CHAT") as task:
        assert await run_agent(task.ctx, agent_of(second), memory=memory) == "You are Ana."
    seen = json.dumps(second.requests[0]["messages"])
    assert "I am Ana" in seen and "Hi Ana." in seen and "Who am I?" in seen

    third = ScriptedClient([([text("I do not know.")], usage(4, 2))])
    async with adapter_task("T3", "Who am I?", context_id="CHAT", session_behavior="run") as task:
        await run_agent(task.ctx, agent_of(third), memory=memory)
    assert "Ana" not in json.dumps(third.requests[0]["messages"])
    assert len(memory.history("CHAT")) == 4


ROUTE_SCOPES = {"route_claim": ["tool:claims:route"]}


async def test_tools_a_user_lacks_scopes_for_are_hidden_from_the_model_and_the_denial_is_audited_once():
    records = []

    @tool(approval_mode="never_require")
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        return "fast-track"

    client = ScriptedClient(
        [([call("c1", "lookup_policy", {"policy_id": "P-1"})], usage(1, 1)), ([text("ok")], usage(1, 1))]
    )
    async with adapter_task(
        user_id="alice",
        scopes=ROUTE_SCOPES,
        declared=["lookup_policy"],
        granted=["claims:*:read"],
        audit=records.append,
    ) as task:
        await run_agent(task.ctx, agent_of(client, [lookup_policy, route_claim]))
    assert [r["tools"] for r in client.requests] == [["lookup_policy"], ["lookup_policy"]]
    assert [(r.message, r.fields["tool"]) for r in records if r.message == "tool access denied"] == [
        ("tool access denied", "route_claim")
    ]


async def test_a_call_to_a_hidden_tool_is_not_run_and_the_run_continues():
    ran = []

    @tool(approval_mode="never_require")
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    client = ScriptedClient(
        [([call("c1", "route_claim", {"claim_id": "P-1"})], usage(1, 1)), ([text("Cannot route.")], usage(1, 1))]
    )
    async with adapter_task(user_id="alice", scopes=ROUTE_SCOPES, declared=["lookup_policy"], granted=[]) as task:
        assert await run_agent(task.ctx, agent_of(client, [lookup_policy, route_claim])) == "Cannot route."
        sent = steps(task)
    assert ran == []
    assert [e for e in sent if isinstance(e, (ToolStarted, ToolFinished))] == [
        ToolStarted("c1", "route_claim", {"claim_id": "P-1"}),
        ToolFinished(
            "c1",
            "route_claim",
            {"error": "Access denied: you do not have the scopes required to use route_claim."},
            failed=True,
        ),
    ]


async def test_the_function_middleware_refuses_a_denied_tool_that_is_dispatched_anyway():
    context = SimpleNamespace(
        function=SimpleNamespace(name="route_claim"),
        arguments={"claim_id": "P-1"},
        metadata={"call_id": "c1"},
        result=None,
    )

    async def must_not_run():
        raise AssertionError("the tool ran")

    async with adapter_task(user_id="alice", scopes=ROUTE_SCOPES, declared=["lookup_policy"], granted=[]) as task:
        await next(m for m in step_reporter(task.ctx) if isinstance(m, FunctionMiddleware)).process(
            context, must_not_run
        )
        sent = steps(task)
    assert context.result == "Access denied: you do not have the scopes required to use route_claim."
    assert [(e.kind, e.call_id) for e in sent] == [("tool_started", "c1"), ("tool_finished", "c1")]


async def test_a_denied_approval_stops_the_tool_and_the_model_is_told():
    """Reported as a failed call, as every adapter reports a refused one (so the audit trail records it as failed)."""
    ran = []

    @tool(approval_mode="never_require")
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    client = ScriptedClient(
        [([call("c1", "route_claim", {"claim_id": "P-1"})], usage(1, 1)), ([text("Not routed.")], usage(1, 1))]
    )
    asked = []

    async def request_approval(name, args, *, message=""):
        asked.append((name, args, message))
        return SimpleNamespace(approved=False, outcome="denied")

    async with adapter_task() as task:
        ctx = task.ctx
        ctx.request_approval = request_approval
        assert (
            await run_agent(
                ctx,
                agent_of(client, [route_claim]),
                middleware=[with_approval(ctx, [route_claim], names=["route_claim"], message="Please check")],
            )
            == "Not routed."
        )
        sent = steps(task)
    assert ran == [] and asked == [("route_claim", {"claim_id": "P-1"}, "Please check")]
    kinds = [e.kind for e in sent]
    assert kinds.index("tool_started") < kinds.index("tool_finished")
    result = [e for e in sent if isinstance(e, ToolFinished)][0]
    assert "did not approve" in json.dumps(result.result)
    assert result.result == {"error": "The user did not approve this call to route_claim (denied), so it was not run."}


async def test_an_approved_tool_runs_and_an_unlisted_tool_is_not_asked_about():
    client = ScriptedClient(
        [([call("c1", "lookup_policy", {"policy_id": "P-1"})], usage(1, 1)), ([text("Gold.")], usage(1, 1))]
    )
    asked = []

    async def request_approval(name, args, *, message=""):
        asked.append(name)
        return SimpleNamespace(approved=True, outcome="approved")

    async with adapter_task() as task:
        ctx = task.ctx
        ctx.request_approval = request_approval
        assert (
            await run_agent(
                ctx, agent_of(client, [lookup_policy]), middleware=[with_approval(ctx, [lookup_policy], names=[])]
            )
            == "Gold."
        )
        assert asked == []
        client = ScriptedClient(
            [([call("c1", "lookup_policy", {"policy_id": "P-1"})], usage(1, 1)), ([text("Gold.")], usage(1, 1))]
        )
        assert (
            await run_agent(ctx, agent_of(client, [lookup_policy]), middleware=[with_approval(ctx, [lookup_policy])])
            == "Gold."
        )
        assert asked == ["lookup_policy"]


async def test_the_model_can_ask_the_user_a_question():
    client = ScriptedClient(
        [
            ([call("c1", "ask_user_question", {"question": "Which policy?", "options": ["P-1", "P-2"]})], usage(1, 1)),
            ([text("Thanks.")], usage(1, 1)),
        ]
    )
    questions = []

    async def ask_user(question):
        questions.append(question)
        return SimpleNamespace(answered=True, answers=["P-2"], status="answered")

    async with adapter_task() as task:
        task.ctx.ask_user = ask_user
        await run_agent(task.ctx, agent_of(client), extra_tools=[ask_user_tool(task.ctx)])
    assert len(questions) == 1
    assert "P-2" in json.dumps(client.requests[1], default=str) or client.requests[1]["contents"][-1] == [
        "function_result"
    ]


async def test_mesh_agents_are_tools_that_pass_the_call_id():
    calls = []
    cards = [AgentCard("policy-bot", "Answers policy questions.", "Policy Bot", ["Lookup"]), AgentCard("billing")]

    async def call_agent(agent, task_description, *, function_call_id=""):
        calls.append((agent, task_description, function_call_id))
        return PeerResult(agent, "completed", f"answer from {agent}")

    async with adapter_task() as task:
        ctx = task.ctx
        ctx.available_agents = lambda: list(cards)
        ctx.call_agent = call_agent

        tools = mesh_agent_tools(ctx)
        assert [t.name for t in tools] == ["peer_policy-bot", "peer_billing"]
        assert (
            tools[0].description
            == 'Delegate a task to the "Policy Bot" agent. Answers policy questions. Skills: Lookup'
        )
        assert tools[0].parameters()["required"] == ["task_description"]
        assert [t.name for t in mesh_agent_tools(ctx, ["billing"])] == ["peer_billing"]

        client = ScriptedClient(
            [
                ([call("tu_1", "peer_policy-bot", {"task_description": "Check P-1"})], usage(1, 1)),
                ([text("Done.")], usage(1, 1)),
            ]
        )
        assert await run_agent(ctx, agent_of(client), extra_tools=tools) == "Done."
    assert calls == [("policy-bot", "Check P-1", "tu_1")]


def test_tool_infos_lists_the_agents_tools():
    assert [(i.name, i.description) for i in tool_infos([lookup_policy])] == [("lookup_policy", "Look up a policy.")]


async def test_reasoning_is_streamed_only_when_stream_thinking_is_on():
    def turn():
        return ScriptedClient(
            [([Content.from_text_reasoning(text="The policy is gold."), text("Fast-track.")], usage(5, 3))]
        )

    async with adapter_task(stream_thinking=True) as task:
        assert await run_agent(task.ctx, agent_of(turn())) == "Fast-track."
        thinking = [e for e in steps(task) if isinstance(e, Thinking)]
    assert thinking and thinking[0].text == "The policy is gold."
    assert thinking[-1].done is True

    async with adapter_task() as task:
        await run_agent(task.ctx, agent_of(turn()))
        assert not [e for e in steps(task) if isinstance(e, Thinking)]


async def test_the_gateways_request_context_is_sent_as_a_system_message_on_request():
    for include in (True, False):
        client = ScriptedClient([([text("ok")], usage(1, 1))])
        async with adapter_task(request_context=RequestContext(system_purpose="Help claims handlers.")) as task:
            await run_agent(task.ctx, agent_of(client), include_request_context=include)
        system = [t for r, t in client.requests[0]["messages"] if r == "system"]
        assert (include and any("## System Purpose\n\nHelp claims handlers." in t for t in system)) or (
            not include and not any("Help claims" in t for t in system)
        )


async def test_a_failed_model_call_is_recorded_with_its_category_for_the_error_reply(openai_rate_limited):
    import openai

    response = openai_rate_limited

    class Failing(ScriptedClient):
        def _inner_get_response(self, *, messages, stream, options, **kwargs):
            async def fail():
                raise openai.RateLimitError("Rate limit reached", response=response, body=None)

            return fail()

    async with adapter_task() as task:
        with pytest.raises(openai.RateLimitError):
            await run_agent(task.ctx, agent_of(Failing([])))
        assert (task.ctx._run.llm_failure.category, task.ctx._run.llm_failure.provider) == ("rate_limit", "openai")


async def test_a_cancelled_task_cancels_the_run_and_the_tool_stops_waiting():
    started, finished = asyncio.Event(), []

    @tool(approval_mode="never_require")
    async def slow(policy_id: str) -> str:
        """Slow lookup."""
        started.set()
        try:
            await asyncio.sleep(30)
        finally:
            finished.append(True)
        return "late"

    client = ScriptedClient([([call("c1", "slow", {"policy_id": "P-1"})], usage(1, 1))])
    async with adapter_task() as mesh_task:
        task = asyncio.create_task(run_agent(mesh_task.ctx, agent_of(client, [slow])))
        await asyncio.wait_for(started.wait(), 5)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
    assert finished == [True]


class Switch:
    """Stands in for MeshAgent: check() raises while stopped."""

    def __init__(self, stopped=False):
        self.stopped = stopped

    def check(self):
        if self.stopped:
            raise AgentStopped("stopped_by_sam")


async def test_an_agent_framework_agent_refuses_to_run_and_stops_before_the_next_model_call():
    switch, ran = Switch(), []

    @tool(approval_mode="never_require")
    def stopping_lookup(policy_id: str) -> dict:
        """Look up a policy."""
        ran.append(policy_id)
        switch.stopped = True
        return {"tier": "gold"}

    client = ScriptedClient(
        [([call("c1", "stopping_lookup", {"policy_id": "P-1"})], usage(10, 2)), ([text("Gold.")], usage(12, 2))]
    )
    agent = Agent(client, "x", name="t", tools=[stopping_lookup], middleware=stop_guard(switch))
    with pytest.raises(AgentStopped):
        await agent.run("look up P-1")
    assert ran == ["P-1"] and len(client.turns) == 1

    with pytest.raises(AgentStopped):
        await Agent(ScriptedClient([([text("hi")], usage(1, 1))]), "x", name="t", middleware=stop_guard(switch)).run(
            "hi"
        )


async def test_a_stop_before_a_tool_runs_aborts_the_run_instead_of_becoming_a_tool_error():
    switch, ran = Switch(), []

    @tool(approval_mode="never_require")
    def lookup(policy_id: str) -> dict:
        """Look up a policy."""
        ran.append(policy_id)
        return {"tier": "gold"}

    client = ScriptedClient(
        [([call("c1", "lookup", {"policy_id": "P-1"})], usage(10, 2)), ([text("Gold.")], usage(12, 2))],
        on_response=lambda: setattr(switch, "stopped", True),
    )
    agent = Agent(client, "x", name="t", tools=[lookup], middleware=stop_guard(switch))
    with pytest.raises(AgentStopped):
        await agent.run("look up P-1")
    assert ran == [] and len(client.turns) == 1


async def test_an_agent_run_as_a_tool_asks_the_run_s_approvals_and_reports_its_steps():
    ran, asked = [], []

    @tool(approval_mode="never_require")
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    inner = Agent(
        ScriptedClient(
            [([call("c2", "route_claim", {"claim_id": "C-1"})], usage(3, 1)), ([text("Not routed.")], usage(4, 1))]
        ),
        "Route.",
        name="router",
        tools=[route_claim],
    )
    outer = Agent(
        ScriptedClient([([call("c1", "router", {"task": "route C-1"})], usage(1, 1)), ([text("Done.")], usage(2, 1))]),
        "Ask.",
        name="front",
        tools=[inner.as_tool()],
    )

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        return SimpleNamespace(approved=False, outcome="denied")

    async with adapter_task() as task:
        ctx = task.ctx
        ctx.request_approval = request_approval
        assert (
            await run_agent(ctx, outer, middleware=[with_approval(ctx, [route_claim], names=["route_claim"])])
            == "Done."
        )
        sent = steps(task)
        assert ctx._run.usage.final_token_usage()["total_input_tokens"] == 10
    assert asked == ["route_claim"] and ran == []
    assert [e.kind for e in sent].count("model_finished") == 4


def test_an_agent_tool_whose_agent_cannot_be_found_refuses_the_run():
    from sam_link_sdk._adapter import NestedRunRefused
    from sam_link_sdk.agent_framework.steps import _nested_agents

    def _agent_wrapper(**kwargs):
        return ""

    with pytest.raises(NestedRunRefused):
        list(_nested_agents([SimpleNamespace(name="router", func=_agent_wrapper)]))


async def test_a_tool_the_framework_holds_for_approval_is_refused_on_a_channel_that_cannot_prompt():
    """Agent Mesh shows no prompt there (the e2e harness has only the web gateway, which can), so the user's answer is
    a cancel: the call does not run and the model is told it was not approved."""
    ran: list[str] = []

    @tool(approval_mode="always_require")
    def route_claim(policy_id: str) -> str:
        """Route a claim."""
        ran.append(policy_id)
        return "routed"

    client = ScriptedClient(
        [([call("c1", "route_claim", {"policy_id": "P-1"})], usage(1, 1)), ([text("Not routed.")], usage(1, 1))]
    )
    async with adapter_task(can_prompt_user=False) as task:
        assert await run_agent(task.ctx, agent_of(client, [route_claim])) == "Not routed."
        [finished] = [e for e in steps(task) if isinstance(e, ToolFinished)]

    assert ran == []
    assert "did not approve this call to route_claim (cancelled), so it was not run." in json.dumps(finished.result)


@pytest.mark.parametrize("approved", [True, False])
async def test_a_tool_the_framework_holds_for_approval_in_an_agent_run_as_a_tool_asks_the_user(approved):
    """The framework does not hand a sub-agent's approval request up (the agent tool fails on it), so a router on the
    sub-agent asks the user through SAM and runs it again with the answer."""
    ran: list[str] = []

    @tool(approval_mode="always_require")
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    inner_model = ScriptedClient(
        [([call("c2", "route_claim", {"claim_id": "C-1"})], usage(3, 1)), ([text("Inner done.")], usage(4, 1))]
    )
    inner = Agent(inner_model, "Route.", name="router", tools=[route_claim])
    outer = Agent(
        ScriptedClient([([call("c1", "router", {"task": "route C-1"})], usage(1, 1)), ([text("Done.")], usage(2, 1))]),
        "Ask.",
        name="front",
        tools=[inner.as_tool()],
    )
    asked = []

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append((name, args))
        return SimpleNamespace(approved=approved, outcome="approved" if approved else "denied")

    async with adapter_task() as task:
        task.ctx.request_approval = request_approval
        assert await run_agent(task.ctx, outer) == "Done."
        results = {e.call_id: e.result for e in steps(task) if isinstance(e, ToolFinished)}

    assert asked == [("route_claim", {"claim_id": "C-1"})]
    assert ran == (["C-1"] if approved else [])
    refusal = "The user did not approve this call to route_claim (denied), so it was not run."
    assert results["c1"] == "Inner done."
    assert (results["c2"] == "fast-track") if approved else (results["c2"] == {"error": refusal})
    assert len(inner_model.requests) == 2
