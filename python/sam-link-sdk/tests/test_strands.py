# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Strands Agents adapter, driven offline by a scripted Model."""

from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace

import pytest

pytest.importorskip("strands")

from strands import Agent, tool
from strands.models import Model

from sam_link_sdk import AgentStopped, PeerResult
from sam_link_sdk.delegation import AgentCard
from sam_link_sdk.events import ModelStarted, Thinking, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.memory import ConversationMemory
from sam_link_sdk.strands import ask_user_tool, mesh_agent_tools, run_agent, tool_infos, with_approval
from sam_link_sdk.strands.stop import StopHooks

from .adapter_task import adapter_task, steps


@tool
def lookup_policy(policy_id: str) -> dict:
    """Look up a policy."""
    return {"policy_id": policy_id, "tier": "gold"}


@tool
def broken(policy_id: str) -> dict:
    """Always fails."""
    raise RuntimeError("backend down")


def call(call_id, name, args):
    return {"toolUse": {"toolUseId": call_id, "name": name, "input": args}}


def text(value):
    return {"text": value}


def usage(i, o, cached=0):
    return {"inputTokens": i, "outputTokens": o, "totalTokens": i + o, "cacheReadInputTokens": cached}


class ScriptedModel(Model):
    """Each turn is (content blocks, usage). Records the messages of every request in `requests`."""

    def __init__(self, turns, model_id="fake-model"):
        self.turns = list(turns)
        self.model_id = model_id
        self.requests: list[list] = []

    def update_config(self, **model_config):
        pass

    def get_config(self):
        return {"model_id": self.model_id}

    async def structured_output(self, output_model, prompt, system_prompt=None, **kwargs):
        raise NotImplementedError
        yield

    async def stream(self, messages, tool_specs=None, system_prompt=None, **kwargs):
        self.requests.append([json.loads(json.dumps(m, default=str)) for m in messages])
        blocks, u = self.turns.pop(0)
        yield {"messageStart": {"role": "assistant"}}
        for block in blocks:
            if "reasoning" in block:
                yield {"contentBlockStart": {"start": {}}}
                yield {"contentBlockDelta": {"delta": {"reasoningContent": {"text": block["reasoning"]}}}}
            elif "text" in block:
                yield {"contentBlockStart": {"start": {}}}
                yield {"contentBlockDelta": {"delta": {"text": block["text"]}}}
            else:
                use = block["toolUse"]
                yield {
                    "contentBlockStart": {"start": {"toolUse": {"toolUseId": use["toolUseId"], "name": use["name"]}}}
                }
                yield {"contentBlockDelta": {"delta": {"toolUse": {"input": json.dumps(use["input"])}}}}
            yield {"contentBlockStop": {}}
        yield {"messageStop": {"stopReason": "tool_use" if any("toolUse" in b for b in blocks) else "end_turn"}}
        yield {"metadata": {"usage": u, "metrics": {"latencyMs": 1}}}


def agent_of(model, tools=(), **kwargs):
    return lambda ctx: Agent(model=model, tools=list(tools), callback_handler=None, **kwargs)


async def test_parallel_tool_calls_pair_by_call_id_and_usage_sums():
    model = ScriptedModel(
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
        assert await run_agent(task.ctx, agent_of(model, [lookup_policy])) == "Both are gold."
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


async def test_tool_failure_is_reported_and_run_continues():
    model = ScriptedModel(
        [
            ([call("call_x", "broken", {"policy_id": "P-9"})], usage(10, 2)),
            ([text("Sorry, the backend is down.")], usage(12, 3)),
        ]
    )
    async with adapter_task() as task:
        assert await run_agent(task.ctx, agent_of(model, [broken])) == "Sorry, the backend is down."
        result = [e for e in steps(task) if isinstance(e, ToolFinished)][0]
    assert result.call_id == "call_x"
    assert "backend down" in json.dumps(result.result) and "error" in result.result


async def test_reasoning_is_streamed_only_when_stream_thinking_is_on():
    def turn():
        return ScriptedModel([([{"reasoning": "The policy is gold."}, text("Fast-track.")], usage(5, 3))])

    async with adapter_task(stream_thinking=True) as task:
        assert await run_agent(task.ctx, agent_of(turn())) == "Fast-track."
        thinking = [e for e in steps(task) if isinstance(e, Thinking)]
    assert thinking[0].text == "The policy is gold."
    assert thinking[-1].done is True

    async with adapter_task() as task:
        await run_agent(task.ctx, agent_of(turn()))
        assert not [e for e in steps(task) if isinstance(e, Thinking)]


async def test_the_gateways_request_context_is_added_to_the_system_prompt_on_request():
    from sam_link_sdk.request_context import RequestContext

    seen = []

    class Spy(ScriptedModel):
        async def stream(self, messages, tool_specs=None, system_prompt=None, **kwargs):
            seen.append(system_prompt)
            async for e in super().stream(messages, tool_specs, system_prompt, **kwargs):
                yield e

    for include in (True, False):
        async with adapter_task(request_context=RequestContext(system_purpose="Help claims handlers.")) as task:
            await run_agent(
                task.ctx,
                agent_of(Spy([([text("ok")], usage(1, 1))]), system_prompt="You triage claims."),
                include_request_context=include,
            )
    assert seen[0].startswith("You triage claims.") and "## System Purpose\n\nHelp claims handlers." in seen[0]
    assert seen[1] == "You triage claims."


async def test_a_failed_model_call_is_recorded_with_its_category_for_the_error_reply(openai_rate_limited):
    import openai

    response = openai_rate_limited

    class Failing(ScriptedModel):
        async def stream(self, messages, tool_specs=None, system_prompt=None, **kwargs):
            raise openai.RateLimitError("Rate limit reached", response=response, body=None)
            yield

    async with adapter_task() as task:
        with pytest.raises(openai.RateLimitError):
            await run_agent(task.ctx, agent_of(Failing([])))
        assert (task.ctx._run.llm_failure.category, task.ctx._run.llm_failure.provider) == ("rate_limit", "openai")


async def test_a_stream_that_ends_without_a_result_fails_saying_so():
    """A stream cut short (a cancel racing the end, a provider cutting off) yields no result event. SAM cannot be made
    to cut a Strands stream short, so the stream is cut here."""

    def build(ctx):
        agent = agent_of(ScriptedModel([]))(ctx)

        async def cut_short(prompt, **kwargs):
            yield {"data": "Claim P-1182 goes"}

        agent.stream_async = cut_short
        return agent

    async with adapter_task() as task:
        with pytest.raises(RuntimeError, match="ended without a result"):
            await run_agent(task.ctx, build)


async def test_a_task_without_streaming_still_counts_usage():
    async with adapter_task(streaming=False) as task:
        assert await run_agent(task.ctx, agent_of(ScriptedModel([([text("hi")], usage(5, 1))]))) == "hi"
        assert steps(task) == [] and task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 5


async def test_a_conversation_is_replayed_per_session_and_a_run_based_task_has_no_history():
    memory = ConversationMemory()
    first = ScriptedModel([([text("Hi Ana.")], usage(5, 1))])
    async with adapter_task("T1", "I am Ana", context_id="CHAT") as task:
        await run_agent(task.ctx, agent_of(first), memory=memory)

    second = ScriptedModel([([text("You are Ana.")], usage(9, 2))])
    async with adapter_task("T2", "Who am I?", context_id="CHAT") as task:
        assert await run_agent(task.ctx, agent_of(second), memory=memory) == "You are Ana."
    seen = json.dumps(second.requests[0])
    assert "I am Ana" in seen and "Hi Ana." in seen and "Who am I?" in seen

    third = ScriptedModel([([text("I do not know.")], usage(4, 2))])
    async with adapter_task("T3", "Who am I?", context_id="CHAT", session_behavior="run") as task:
        await run_agent(task.ctx, agent_of(third), memory=memory)
    assert "Ana" not in json.dumps(third.requests[0])
    assert len(memory.history("CHAT")) == 4


class OfferSpy(ScriptedModel):
    """Records the tool names offered to the model on each request."""

    def __init__(self, turns, **kwargs):
        super().__init__(turns, **kwargs)
        self.offered: list[list[str]] = []

    async def stream(self, messages, tool_specs=None, system_prompt=None, **kwargs):
        self.offered.append([s["name"] for s in tool_specs or []])
        async for e in super().stream(messages, tool_specs, system_prompt, **kwargs):
            yield e


@tool
def route_claim(claim_id: str) -> str:
    """Route a claim."""
    ROUTED.append(claim_id)
    return "fast-track"


ROUTED: list[str] = []

ROUTE_SCOPES = {"route_claim": ["tool:claims:route"]}


async def test_tools_a_user_lacks_scopes_for_stay_on_the_agent_and_nothing_is_audited_until_one_is_called():
    """Strands has no public way to take a tool off a built agent, so run_agent does not reach into the tool
    registry: the tool stays offered and the hook refuses a call to it (next test), which is when it is audited."""
    records = []
    model = OfferSpy([([text("ok")], usage(1, 1))])
    async with adapter_task(
        user_id="alice",
        scopes=ROUTE_SCOPES,
        declared=["lookup_policy"],
        granted=["claims:*:read"],
        audit=records.append,
    ) as task:
        await run_agent(task.ctx, agent_of(model, [lookup_policy, route_claim]))
    assert model.offered == [["lookup_policy", "route_claim"]]
    assert records == []


async def test_a_denied_tool_the_model_calls_through_run_agent_is_refused_and_audited_once():
    ROUTED.clear()
    records = []
    model = OfferSpy(
        [
            ([call("c1", "route_claim", {"claim_id": "P-1"})], usage(1, 1)),
            ([call("c2", "route_claim", {"claim_id": "P-2"})], usage(1, 1)),
            ([text("I may not route claims.")], usage(1, 1)),
        ]
    )
    async with adapter_task(
        user_id="alice", scopes=ROUTE_SCOPES, declared=["lookup_policy"], granted=[], audit=records.append
    ) as task:
        assert await run_agent(task.ctx, agent_of(model, [route_claim])) == "I may not route claims."
        sent = steps(task)
    assert ROUTED == []
    assert [e.kind for e in sent] == ["model_started", "model_finished", "tool_started", "tool_finished"] * 2 + [
        "model_started",
        "model_finished",
    ]
    told = [b["toolResult"] for m in model.requests[1] for b in m["content"] if "toolResult" in b]
    assert told[0]["status"] == "error" and "Access denied" in json.dumps(told[0]["content"])
    assert [(r.message, r.fields["tool"]) for r in records if r.message == "tool access denied"] == [
        ("tool access denied", "route_claim")
    ]


async def test_build_can_keep_denied_tools_out_of_the_models_list_with_permitted_tools():
    model = OfferSpy([([text("ok")], usage(1, 1))])
    async with adapter_task(user_id="alice", scopes=ROUTE_SCOPES, declared=["lookup_policy"], granted=[]) as task:
        await run_agent(
            task.ctx,
            lambda c: Agent(model=model, tools=c.permitted_tools([lookup_policy, route_claim]), callback_handler=None),
        )
    assert model.offered == [["lookup_policy"]]


async def test_a_call_to_a_denied_tool_is_refused_at_dispatch_too():
    """The hook refuses the call even on an agent run without run_agent.

    Not through run_agent, so the tool is still registered.
    """
    from sam_link_sdk.strands import SamLinkHooks

    ran = []

    @tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    model = ScriptedModel(
        [([call("c1", "route_claim", {"claim_id": "P-1"})], usage(1, 1)), ([text("Denied.")], usage(1, 1))]
    )
    async with adapter_task(user_id="alice", scopes=ROUTE_SCOPES, declared=["lookup_policy"], granted=[]) as task:
        agent = Agent(model=model, tools=[route_claim], callback_handler=None)
        agent.hooks.add_hook(SamLinkHooks(task.ctx))
        await agent.invoke_async("route it")
        sent = steps(task)
    assert ran == []
    assert [(e.kind, getattr(e, "result", None)) for e in sent if isinstance(e, (ToolStarted, ToolFinished))] == [
        ("tool_started", None),
        ("tool_finished", {"error": "Access denied: you do not have the scopes required to use route_claim."}),
    ]


async def test_a_denied_approval_stops_the_tool_and_the_model_is_told():
    ran = []

    @tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    model = ScriptedModel(
        [([call("c1", "route_claim", {"claim_id": "P-1"})], usage(1, 1)), ([text("Not routed.")], usage(1, 1))]
    )
    asked = []

    async def request_approval(name, args, *, message=""):
        asked.append((name, args, message))
        return SimpleNamespace(approved=False, outcome="denied")

    async with adapter_task() as task:
        task.ctx.request_approval = request_approval

        def build(c):
            return Agent(
                model=model,
                tools=[route_claim],
                hooks=[with_approval(c, [route_claim], names=["route_claim"], message="Please check")],
                callback_handler=None,
            )

        assert await run_agent(task.ctx, build) == "Not routed."
        sent = steps(task)
    assert ran == [] and asked == [("route_claim", {"claim_id": "P-1"}, "Please check")]
    kinds = [e.kind for e in sent]
    assert kinds.index("tool_started") < kinds.index("tool_finished")
    result = [e for e in sent if isinstance(e, ToolFinished)][0]
    assert "did not approve" in json.dumps(result.result)


async def test_an_approved_tool_runs():
    model = ScriptedModel(
        [([call("c1", "lookup_policy", {"policy_id": "P-1"})], usage(1, 1)), ([text("Gold.")], usage(1, 1))]
    )

    async def request_approval(name, args, *, message=""):
        return SimpleNamespace(approved=True, outcome="approved")

    async with adapter_task() as task:
        task.ctx.request_approval = request_approval

        def build(c):
            return Agent(
                model=model, tools=[lookup_policy], hooks=[with_approval(c, [lookup_policy])], callback_handler=None
            )

        assert await run_agent(task.ctx, build) == "Gold."


async def test_the_model_can_ask_the_user_a_question():
    model = ScriptedModel(
        [
            ([call("c1", "ask_user_question", {"question": "Which policy?", "options": ["P-1", "P-2"]})], usage(1, 1)),
            ([text("Thanks.")], usage(1, 1)),
        ]
    )
    questions = []

    async def ask_user(question):
        questions.append(question.text if hasattr(question, "text") else question)
        return SimpleNamespace(answered=True, answers=["P-2"], status="answered")

    async with adapter_task() as task:
        task.ctx.ask_user = ask_user
        await run_agent(task.ctx, lambda c: Agent(model=model, tools=[ask_user_tool(c)], callback_handler=None))
    assert len(questions) == 1
    assert "P-2" in json.dumps(model.requests[1])


async def test_mesh_agents_are_tools_that_pass_the_tool_use_id():
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
        assert [t.tool_name for t in tools] == ["peer_policy-bot", "peer_billing"]
        assert (
            tools[0].tool_spec["description"]
            == 'Delegate a task to the "Policy Bot" agent. Answers policy questions. Skills: Lookup'
        )
        assert tools[0].tool_spec["inputSchema"]["json"]["required"] == ["task_description"]
        assert [t.tool_name for t in mesh_agent_tools(ctx, ["billing"])] == ["peer_billing"]

        model = ScriptedModel(
            [
                ([call("tu_1", "peer_policy-bot", {"task_description": "Check P-1"})], usage(1, 1)),
                ([text("Done.")], usage(1, 1)),
            ]
        )
        assert await run_agent(ctx, lambda c: Agent(model=model, tools=tools, callback_handler=None)) == "Done."
    assert calls == [("policy-bot", "Check P-1", "tu_1")]


def test_tool_infos_lists_the_agents_tools():
    assert [(i.name, i.description) for i in tool_infos([lookup_policy])] == [("lookup_policy", "Look up a policy.")]


async def test_a_cancelled_task_cancels_the_agent_and_the_tool_stops_waiting():
    started, finished = asyncio.Event(), []

    @tool
    async def slow(policy_id: str) -> str:
        """Slow lookup."""
        started.set()
        try:
            await asyncio.sleep(30)
        finally:
            finished.append(True)
        return "late"

    model = ScriptedModel([([call("c1", "slow", {"policy_id": "P-1"})], usage(1, 1))])
    async with adapter_task() as mesh_task:
        task = asyncio.create_task(run_agent(mesh_task.ctx, agent_of(model, [slow])))
        await asyncio.wait_for(started.wait(), 5)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
    assert finished == [True] and len(model.turns) == 0


class Switch:
    """Stands in for MeshAgent: check() raises while stopped."""

    def __init__(self, stopped=False):
        self.stopped = stopped

    def check(self):
        if self.stopped:
            raise AgentStopped("stopped_by_sam")


async def test_a_strands_agent_refuses_to_run_and_stops_before_the_next_model_call():
    switch = Switch()

    @tool
    def stopping_lookup(policy_id: str) -> dict:
        """Look up a policy."""
        switch.stopped = True
        return {"tier": "gold"}

    model = ScriptedModel(
        [([call("c1", "stopping_lookup", {"policy_id": "P-1"})], usage(10, 2)), ([text("Gold.")], usage(12, 2))]
    )
    agent = Agent(model=model, tools=[stopping_lookup], hooks=[StopHooks(switch)], callback_handler=None)
    with pytest.raises(Exception) as stopped:
        await agent.invoke_async("look up P-1")
    assert isinstance(stopped.value, AgentStopped) or isinstance(stopped.value.__cause__, AgentStopped), repr(
        stopped.value
    )
    assert len(model.turns) == 1

    with pytest.raises(Exception) as refused:
        await Agent(
            model=ScriptedModel([([text("hi")], usage(1, 1))]), hooks=[StopHooks(switch)], callback_handler=None
        ).invoke_async("hi")
    assert isinstance(refused.value, AgentStopped) or isinstance(refused.value.__cause__, AgentStopped), repr(
        refused.value
    )


async def test_an_agent_run_as_a_tool_asks_the_outer_agent_s_approvals_and_is_reported_without_run_agent_too():
    """The step reporter routes an inner agent's run to the task: its steps and tokens, and the outer agent's
    with_approval hooks, also when the outer agent is run directly with the reporter in its hooks."""
    from sam_link_sdk.strands import step_reporter

    ran, asked = [], []

    @tool
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "fast-track"

    model = ScriptedModel(
        [
            ([call("c1", "router", {"input": "route C-1"})], usage(1, 1)),
            ([call("c2", "route_claim", {"claim_id": "C-1"})], usage(3, 1)),
            ([text("Not routed.")], usage(4, 1)),
            ([text("Done.")], usage(2, 1)),
        ]
    )
    router = Agent(name="router", description="Routes claims.", model=model, tools=[route_claim], callback_handler=None)

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        return SimpleNamespace(approved=False, outcome="denied")

    async with adapter_task() as task:
        ctx = task.ctx
        ctx.request_approval = request_approval
        outer = Agent(
            model=model,
            tools=[router],
            callback_handler=None,
            hooks=[step_reporter(ctx), with_approval(ctx, [route_claim], names=["route_claim"])],
        )
        await outer.invoke_async("route C-1")
        sent = steps(task)
        assert ctx._run.usage.final_token_usage()["total_input_tokens"] == 10
    assert asked == ["route_claim"] and ran == []
    assert [e.kind for e in sent].count("model_finished") == 4


async def test_a_tool_call_strands_holds_for_approval_is_refused_on_a_channel_that_cannot_prompt():
    """Agent Mesh shows no prompt there (the e2e harness has only the web gateway, which can), so the user's answer is
    a cancel: the call does not run and the model is told it was not approved."""
    from strands.interventions import Confirm, InterventionHandler, Proceed

    ran: list[str] = []

    @tool
    def route_claim(policy_id: str) -> str:
        """Route a claim."""
        ran.append(policy_id)
        return "routed"

    class ConfirmRoute(InterventionHandler):
        name = "route_claim"

        def before_tool_call(self, event, **kwargs):
            return Confirm(prompt="Route this claim?") if event.tool_use["name"] == "route_claim" else Proceed()

    model = ScriptedModel(
        [([call("c1", "route_claim", {"policy_id": "P-1"})], usage(1, 1)), ([text("Not routed.")], usage(1, 1))]
    )
    async with adapter_task(can_prompt_user=False) as task:
        assert (
            await run_agent(task.ctx, agent_of(model, [route_claim], interventions=[ConfirmRoute()])) == "Not routed."
        )
        [finished] = [e for e in steps(task) if isinstance(e, ToolFinished)]

    assert ran == []
    assert "did not approve this call to route_claim (cancelled), so it was not run." in json.dumps(model.requests[-1])
    assert finished.failed
