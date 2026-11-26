# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Agent link protocol version 2: MeshAgent over LinkTransport against an in-process entrypoint speaking version 2
(docs/link-protocol.md, "Version 2")."""

from __future__ import annotations

import asyncio
import base64
import itertools
import json
import logging
import time

import pytest

from sam_link_sdk import AgentConfig, AgentStopped, MeshAgent, Option, Question, Skill, TaskResult, ToolInfo, Usage
from sam_link_sdk.context import File
from sam_link_sdk.events import ModelFailed, ModelFinished, ModelStarted, Thinking, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.link import (
    DescriptionTooLong,
    LinkConfig,
    LinkError,
    LinkTransport,
    TooManyToolScopes,
    describer as link_describer,
    transport as link_transport,
)
from sam_link_sdk.link.config import PROTOCOL_V2
from sam_link_sdk.stop import stop_state_path

from .test_link import (
    AGENT,
    LIMITS,
    PEERS,
    RELEASE,
    STOP,
    TOKEN,
    USER,
    LinkServer,
    agent_config,
    entrypoint,
    events,
    eventually,
    link_config,
    private_state_dir,  # noqa: F401  (autouse: SAM's stop is saved under a private HOME)
    results,
    started,
    task,
    types,
)

MODEL = "openai/gpt-4o"


def v2_server(**welcome) -> LinkServer:
    return entrypoint(**welcome)


@pytest.fixture
async def server():
    s = await v2_server().start()
    yield s
    await s.stop()


def config(server: LinkServer, **kw) -> LinkConfig:
    return link_config(server, **kw)


async def test_the_agent_speaks_protocol_2(server):
    agent = await started(server)
    link = agent._transport
    assert isinstance(link, LinkTransport)
    assert (link._protocol, link._connection.subprotocol) == (2, PROTOCOL_V2)
    assert server.offered == [[PROTOCOL_V2]]
    await agent.stop()


async def test_the_agent_describes_itself_before_it_consumes_and_retires_when_it_leaves(server):
    cfg = dict(
        display_name="Claims Triage",
        tools=[ToolInfo("lookup_claim", "Looks up a claim")],
        required_scopes=["agent:claims:invoke"],
        peers=["policy-desk"],
        owner="claims@example.com",
        framework="Contract Script",
    )
    agent = await started(server, **cfg)
    order = types(server, 0)
    assert order.index("describe") < order.index("consume")
    [describe] = server.of("describe")
    desc = describe["agent"]
    assert desc["displayName"] == "Claims Triage" and desc["description"] == "d"
    assert desc["tools"] == [{"name": "lookup_claim", "description": "Looks up a claim", "requiredScopes": []}]
    assert desc["requiredScopes"] == ["agent:claims:invoke"] and desc["peers"] == ["policy-desk"]
    assert desc["declared"]["owner"] == "claims@example.com" and desc["declared"]["framework"] == "Contract Script"
    assert desc["declared"]["sdk"]["name"] == "sam-link-sdk" and desc["health"] == []
    assert "name" not in desc
    await agent.stop()
    assert types(server, 0)[-1] == "retire"


async def test_a_changed_health_is_described_again(server, monkeypatch):
    monkeypatch.setattr(link_describer, "DESCRIPTION_CHECK_SECONDS", 0.05)
    agent = await started(server)
    agent.set_health_problem("claims-db", "degraded", "the claims database is slow")
    described = await eventually(lambda: [d for d in server.of("describe") if d["agent"]["health"]])
    assert described[0]["agent"]["health"] == [
        {"id": "claims-db", "severity": "degraded", "detail": "the claims database is slow"}
    ]
    count = len(server.of("describe"))
    await asyncio.sleep(0.2)
    assert len(server.of("describe")) == count
    await agent.stop()


async def test_an_unchanged_description_is_sent_again_on_a_slow_heartbeat(server, monkeypatch):
    monkeypatch.setattr(link_describer, "DESCRIPTION_CHECK_SECONDS", 0.02)
    monkeypatch.setattr(link_describer, "DESCRIBE_REFRESH_SECONDS", 0.1)
    agent = await started(server)
    await eventually(lambda: len(server.of("describe")) >= 3)
    assert len({json.dumps(d["agent"], sort_keys=True) for d in server.of("describe")}) == 1
    assert len(server.of("consume")) == 1
    await agent.stop()


SCOPED_TOOLS = [
    ToolInfo("lookup_claim", "Looks up a claim"),
    ToolInfo("pay_claim", "Pays a claim", required_scopes=["tool:claims:pay"]),
    ToolInfo("void_claim", "Voids a claim", required_scopes=["tool:claims:pay", "tool:claims:void"]),
    ToolInfo("any_claim", "Anything on a claim", required_scopes=["tool:claims:*"]),
]


async def test_a_tools_required_scopes_are_described_and_enforced_with_the_scopes_sam_granted(server):
    """A granted wildcard is the scope the agent named, not a grant of the scopes it would match."""
    allowed = {}

    async def handler(ctx):
        allowed[ctx.task_id] = [
            t for t in ("lookup_claim", "pay_claim", "void_claim", "any_claim") if ctx.check_tool(t)
        ]
        return "checked"

    agent = await started(server, handler, tools=SCOPED_TOOLS)
    [describe] = server.of("describe")
    assert {t["name"]: t["requiredScopes"] for t in describe["agent"]["tools"]} == {
        "lookup_claim": [],
        "pay_claim": ["tool:claims:pay"],
        "void_claim": ["tool:claims:pay", "tool:claims:void"],
        "any_claim": ["tool:claims:*"],
    }
    await server.send(task(1, "T1", grantedToolScopes=["tool:claims:pay"]))
    await server.send(task(2, "T2", grantedToolScopes=["tool:claims:*"]))
    await server.send(task(3, "T3"))
    await server.send(task(4, "T4", grantedToolScopes="tool:claims:pay"))
    await eventually(lambda: len(allowed) == 4)
    assert allowed == {
        "T1": ["lookup_claim", "pay_claim"],
        "T2": ["lookup_claim", "any_claim"],
        "T3": ["lookup_claim"],
        "T4": ["lookup_claim"],
    }
    await agent.stop()


async def test_a_tool_the_user_lacks_the_scopes_for_is_hidden_and_audited_as_denied(server):
    seen = []

    async def handler(ctx):
        seen.append([t.name for t in ctx.permitted_tools(SCOPED_TOOLS)])
        return "ok"

    denied = []
    agent = await started(server, handler, tools=SCOPED_TOOLS, audit_sink=denied.append)
    await server.send(task(1, "T1", grantedToolScopes=["tool:claims:pay", "tool:claims:void"]))
    await eventually(lambda: results(server, "T1"))
    assert seen == [["lookup_claim", "pay_claim", "void_claim"]]
    assert [e for e in denied if e.message == "tool access denied" and e.fields.get("tool") == "any_claim"]
    await agent.stop()


async def test_more_tool_scopes_than_the_entrypoint_checks_are_refused_at_start_up(server):
    many = [ToolInfo(f"tool_{i}", "d", required_scopes=[f"tool:claims:op{i}"]) for i in range(65)]
    agent = MeshAgent(
        AgentConfig(name=AGENT, description="d", keepalive_seconds=0, tools=many),
        lambda ctx: "ok",
        transport=LinkTransport(config(server)),
    )
    with pytest.raises(TooManyToolScopes, match="65 distinct scopes"):
        await agent.start()
    assert server.conns == []

    fewer = await v2_server(limits={**LIMITS, "maxToolScopes": 3}).start()
    try:
        agent = MeshAgent(
            AgentConfig(name=AGENT, description="d", keepalive_seconds=0, tools=many[:4]),
            lambda ctx: "ok",
            transport=LinkTransport(config(fewer)),
        )
        with pytest.raises(TooManyToolScopes, match="at most 3"):
            await agent.start()
        assert fewer.of("describe") == []
    finally:
        await fewer.stop()


@pytest.mark.parametrize(
    ("description", "skills", "refusal"),
    [
        ("x" * 4097, [], "description is over 4096 bytes"),
        ("d", [Skill(f"s{i}", "n", "d") for i in range(33)], "33 skills"),
        ("d", [Skill("s", "n" * 257, "d")], "name over 256 bytes"),
        ("d", [Skill("s", "n", "x" * 1025)], "description over 1024 bytes"),
    ],
)
async def test_a_description_longer_than_the_entrypoint_takes_is_refused_at_start_up(
    server, description, skills, refusal
):
    agent = MeshAgent(
        AgentConfig(name=AGENT, description=description, keepalive_seconds=0, skills=skills),
        lambda ctx: "ok",
        transport=LinkTransport(config(server)),
    )
    with pytest.raises(DescriptionTooLong, match=refusal):
        await agent.start()
    assert server.conns == []


async def test_a_description_is_held_to_the_limits_the_welcome_announces():
    lower = await v2_server(limits={**LIMITS, "maxDescriptionBytes": 10, "maxSkills": 1}).start()
    try:
        for config_, refusal in [
            (AgentConfig(name=AGENT, description="x" * 11, keepalive_seconds=0), "over 10 bytes"),
            (
                AgentConfig(
                    name=AGENT,
                    description="d",
                    keepalive_seconds=0,
                    skills=[Skill("a", "a", "d"), Skill("b", "b", "d")],
                ),
                "at most 1",
            ),
        ]:
            agent = MeshAgent(config_, lambda ctx: "ok", transport=LinkTransport(config(lower)))
            with pytest.raises(DescriptionTooLong, match=refusal):
                await agent.start()
        assert lower.of("describe") == []
    finally:
        await lower.stop()


async def test_a_task_reaches_the_handler_with_its_fields_and_is_answered_then_acknowledged(server):
    seen = {}

    async def handler(ctx):
        seen.update(
            text=ctx.text,
            user=ctx.user_id,
            files=[(f.name, f.mime_type, f.content, f.uri) for f in ctx.files],
            invocation=ctx.invocation,
            input=ctx.structured_input,
            run=ctx.run_based,
            depth=ctx.call_depth,
            purpose=ctx.request_context.system_purpose,
            context=ctx.context_id,
            metadata=ctx.metadata,
        )
        return TaskResult(
            "Coverage confirmed.", data={"covered": True}, files=[File("note.txt", b"covered", mime_type="text/plain")]
        )

    agent = await started(server, handler)
    files = [
        {"name": "a.txt", "mimeType": "text/plain", "bytes": base64.b64encode(b"hello").decode()},
        {"name": "b.pdf", "mimeType": "application/pdf", "uri": "artifact://b.pdf"},
    ]
    await server.send(
        task(
            1,
            "T1",
            text="",
            files=files,
            input={"claim": "C-1"},
            workflow={"name": "claims-intake", "nodeId": "coverage"},
            sessionBehavior="run",
            callDepth=1,
            calledBy="orchestrator",
            metadata={"k": "v"},
            requestContext={"systemPurpose": "Claims desk"},
        )
    )
    [result] = await eventually(lambda: results(server, "T1"))
    assert seen == {
        "text": '{"claim": "C-1"}',
        "user": USER,
        "files": [("a.txt", "text/plain", b"hello", None), ("b.pdf", "application/pdf", None, "artifact://b.pdf")],
        "invocation": {"workflow_name": "claims-intake", "node_id": "coverage"},
        "input": {"claim": "C-1"},
        "run": True,
        "depth": 1,
        "purpose": "Claims desk",
        "context": "sess-1",
        "metadata": {"k": "v"},
    }
    assert result["state"] == "completed" and result["text"] == "Coverage confirmed."
    assert result["data"] == {"covered": True}
    assert result["files"] == [
        {"name": "note.txt", "mimeType": "text/plain", "bytes": base64.b64encode(b"covered").decode()}
    ]
    await eventually(lambda: server.of("ack") == [{"type": "ack", "delivery": 1}])
    order = types(server, 0)
    assert order.index("result") < order.index("ack")
    assert not [t for t in order if t in ("reply", "spend", "card")]
    await agent.stop()


async def test_every_step_kind_becomes_an_event_and_usage_rides_exactly_one_model_finished(server):
    """Each step kind becomes an event, and a model call's usage rides exactly one `model_finished`.

    In SAM's convention `cachedInput` holds the cache reads and writes, and `cacheWrite` the written part.
    """

    async def handler(ctx):
        """As the framework adapters do it: the response carries the call's usage."""
        await ctx.emit_event(ModelStarted(MODEL))
        await ctx.emit_event(
            ModelFinished(
                MODEL,
                "",
                (ToolCall("c1", "lookup_claim", {"claim": "C-1"}),),
                Usage(
                    input_tokens=100, output_tokens=20, cached_input_tokens=10, cache_write_tokens=5, reasoning_tokens=3
                ),
            )
        )
        await ctx.emit_event(ToolStarted("c1", "lookup_claim", {"claim": "C-1"}))
        await ctx.emit_event(ToolFinished("c1", "lookup_claim", {"status": "open"}))
        await ctx.emit_event(ToolFinished("c2", "pay_claim", "no funds", failed=True))
        await ctx.progress("Looking up the claim")
        await ctx.emit_event(Thinking("The claim is open.", done=True))
        await ctx.emit_event(ModelFailed(MODEL, "rate_limit", "openai"))
        await ctx.emit_event(ModelFinished(MODEL, "It is open.", usage=Usage(input_tokens=30, output_tokens=4)))
        await ctx.emit_event(ModelFinished(MODEL, "no usage recorded for this one"))
        ctx.record_usage("anthropic/claude-sonnet-5", Usage(input_tokens=7, output_tokens=1))
        await ctx.emit_text("Claim C-1 is open.")
        return "Claim C-1 is open."

    agent = await started(server, handler, spend_model_keys={MODEL: "gpt-4o-prod"})
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    steps = events(server, "T1")
    assert [s["kind"] for s in steps] == [
        "model_started",
        "model_finished",
        "tool_started",
        "tool_finished",
        "tool_finished",
        "progress",
        "thinking",
        "model_failed",
        "model_finished",
        "model_finished",
        "text",
        "model_finished",
    ]
    first = steps[1]
    assert first["toolCalls"] == [{"id": "c1", "name": "lookup_claim", "args": {"claim": "C-1"}}]
    assert first["usage"] == {"input": 100, "output": 20, "cachedInput": 15, "cacheWrite": 5, "reasoning": 3}
    assert first["pricingKey"] == "gpt-4o-prod"
    assert steps[3] == {
        "kind": "tool_finished",
        "callId": "c1",
        "name": "lookup_claim",
        "result": {"status": "open"},
        "failed": False,
    }
    assert steps[4]["failed"] is True and steps[4]["result"] == "no funds"
    assert steps[6] == {"kind": "thinking", "text": "The claim is open.", "done": True}
    assert steps[7] == {"kind": "model_failed", "model": MODEL, "category": "rate_limit", "provider": "openai"}
    assert steps[8]["text"] == "It is open." and steps[8]["usage"]["input"] == 30
    assert "usage" not in steps[9]
    assert steps[11] == {
        "kind": "model_finished",
        "model": "anthropic/claude-sonnet-5",
        "usage": {"input": 7, "output": 1, "cachedInput": 0, "reasoning": 0},
    }
    used = [s["usage"] for s in steps if "usage" in s]
    assert sum(u["input"] for u in used) == 137 and sum(u["output"] for u in used) == 25
    assert server.of("result")[0]["state"] == "completed"
    await agent.stop()


async def test_a_handler_failure_is_an_error_with_a_message_and_a_model_failure_keeps_its_category(server):
    from sam_link_sdk.llm_errors import LLMFailure

    async def handler(ctx):
        if ctx.text == "boom":
            raise RuntimeError("the claims database is unreachable")
        ctx._run.llm_failure = LLMFailure("rate_limit", "openai")
        raise RuntimeError("provider said no")

    agent = await started(server, handler)
    await server.send(task(1, "T1", text="boom"))
    await server.send(task(2, "T2", text="model"))
    [plain] = await eventually(lambda: results(server, "T1"))
    [model] = await eventually(lambda: results(server, "T2"))
    assert plain == {
        "type": "result",
        "taskId": "T1",
        "state": "failed",
        "text": "",
        "error": {"message": "internal error"},
    }
    assert model["state"] == "failed" and model["error"] == {"category": "rate_limit", "provider": "openai"}
    assert "unreachable" not in json.dumps(server.of("result"))
    await agent.stop()


async def test_a_cancel_stops_the_handler_and_answers_canceled(server):
    halted = asyncio.Event()

    async def handler(ctx):
        try:
            await asyncio.sleep(30)
        except asyncio.CancelledError:
            halted.set()
            raise
        return "never"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    await server.send({"type": "cancel", "taskId": "T1"})
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "canceled"
    await asyncio.wait_for(halted.wait(), 5)
    await eventually(lambda: server.of("ack") == [{"type": "ack", "delivery": 1}])
    await agent.stop()


async def test_a_call_too_deep_is_refused_without_running_the_handler(server):
    ran = []
    agent = await started(server, lambda ctx: ran.append(ctx) or "ran")
    await server.send(task(1, "T1", callDepth=5))
    [result] = await eventually(lambda: results(server, "T1"))
    assert result == {
        "type": "result",
        "taskId": "T1",
        "state": "failed",
        "text": "Call depth 5 exceeds maximum 3",
        "error": {"message": "Call depth 5 exceeds maximum 3"},
    }
    await eventually(lambda: server.of("ack"))
    assert ran == []
    await agent.stop()


async def test_a_task_that_arrives_while_draining_is_left_for_another_replica(server):
    release = asyncio.Event()
    ran = []

    async def handler(ctx):
        ran.append(ctx.task_id)
        await release.wait()
        return "done"

    agent = await started(server, handler, shutdown_grace_seconds=5)
    await server.send(task(1, "T1"))
    await eventually(lambda: ran == ["T1"])
    stopping = asyncio.create_task(agent.stop())
    await eventually(lambda: agent._lifecycle.draining)
    await server.send(task(2, "T2"))
    await asyncio.sleep(0.2)
    release.set()
    await stopping
    assert ran == ["T1"]
    assert [f["delivery"] for f in server.of("ack")] == [1]


async def test_an_approval_is_asked_and_the_answer_reaches_the_handler(server):
    async def handler(ctx):
        approval = await ctx.request_approval("pay_claim", {"claim": "C-3", "amount": 120}, message="Pay it?")
        return "paid" if approval.approved else "not paid"

    server.responders["prompt"] = lambda f: [
        {
            "type": "prompt_answer",
            "taskId": f["taskId"],
            "requestId": f["requestId"],
            "status": "answered",
            "approved": True,
        }
    ]
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    [result] = await eventually(lambda: results(server, "T1"))
    [prompt] = server.of("prompt")
    assert prompt["approval"] == {"tool": "pay_claim", "args": {"claim": "C-3", "amount": 120}, "message": "Pay it?"}
    assert prompt["taskId"] == "T1" and prompt["timeoutSeconds"] == 45 * 60 and "questions" not in prompt
    assert result["text"] == "paid"
    await agent.stop()


async def test_questions_are_asked_and_a_choice_comes_back_as_its_value(server):
    got = []

    async def handler(ctx):
        answer = await ctx.ask_user(
            [
                Question("Which tier?", options=(Option("Gold tier", value="gold"), Option("Silver tier"))),
                Question("Why?"),
            ],
            message="Two things first.",
        )
        got.append(answer)
        return "thanks"

    server.responders["prompt"] = lambda f: [
        {
            "type": "prompt_answer",
            "taskId": f["taskId"],
            "requestId": f["requestId"],
            "status": "answered",
            "answers": ["Gold tier", "renewal"],
        }
    ]
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    [prompt] = server.of("prompt")
    assert [q["question"] for q in prompt["questions"]] == ["Which tier?", "Why?"]
    assert prompt["message"] == "Two things first."
    assert prompt["questions"][0]["options"] == [
        {"label": "Gold tier", "description": ""},
        {"label": "Silver tier", "description": ""},
    ]
    assert got[0].answered and got[0].answers == ("gold", "renewal")
    await agent.stop()


async def test_a_refused_prompt_is_matched_by_its_request_id_and_fails_at_once(server):
    got = {}
    asked = []

    async def handler(ctx):
        first = asyncio.ensure_future(ctx.request_approval("pay_claim", {"claim": "C-3"}, timeout_seconds=30))
        await eventually(lambda: len(server.of("prompt")) == 1)
        second = asyncio.ensure_future(ctx.request_approval("void_claim", {"claim": "C-3"}, timeout_seconds=30))
        got["second"] = await second
        got["first_open"] = not first.done()
        first.cancel()
        return "asked"

    def refuse_the_second(frame):
        """The same task, both prompts open: only the requestId says which one was refused."""
        asked.append(frame["requestId"])
        if len(asked) < 2:
            return []
        return [
            {
                "type": "error",
                "code": "bad_message",
                "message": "approval.tool is at most 256 bytes",
                "ref": frame["taskId"],
                "requestId": frame["requestId"],
            }
        ]

    server.responders["prompt"] = refuse_the_second
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await asyncio.wait_for(eventually(lambda: results(server, "T1")), 5)
    assert got["second"].outcome == "cancelled" and got["first_open"] is True
    await agent.stop()


async def test_a_task_that_cannot_prompt_its_user_is_answered_cancelled_without_a_prompt(server):
    got = []

    async def handler(ctx):
        got.append(ctx.can_prompt_user)
        got.append(await ctx.request_approval("pay_claim", {"claim": "C-3"}))
        got.append(await ctx.ask_user("Which claim?"))
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1", canPromptUser=False))
    await eventually(lambda: results(server, "T1"))
    assert got[0] is False and got[1].outcome == "cancelled" and not got[2].answered
    assert server.of("prompt") == []
    await agent.stop()


@pytest.mark.parametrize(
    "wire, outcome",
    [("answered", "approved"), ("canceled", "cancelled"), ("timed_out", "timed_out"), ("no-such-status", "cancelled")],
)
async def test_each_prompt_status_on_the_wire_reads_as_the_sdks_outcome(server, wire, outcome):
    """The wire spells canceled with one l, as A2A's task states do; the SDK's outcome keeps cancelled."""
    got = []

    async def handler(ctx):
        got.append(await ctx.request_approval("pay_claim", {"claim": "C-3"}))
        return "asked"

    server.responders["prompt"] = lambda f: [
        {"type": "prompt_answer", "taskId": f["taskId"], "requestId": f["requestId"], "status": wire, "approved": True}
    ]
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    assert got[0].outcome == outcome and got[0].approved is (wire == "answered")
    await agent.stop()


async def test_a_prompt_whose_answer_never_comes_times_out(server, monkeypatch):
    monkeypatch.setattr(link_transport, "ANSWER_GRACE_SECONDS", 0.05)
    got = []

    async def handler(ctx):
        got.append(await ctx.request_approval("pay_claim", {}, timeout_seconds=0.1))
        return "asked"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    assert got[0].outcome == "timed_out"
    await agent.stop()


def answer_peer(state="completed", text="Policy P-9 is active.", **extra):
    return lambda f: [{"type": "peer_result", "subtaskId": f["subtaskId"], "state": state, "text": text, **extra}]


async def test_a_peer_call_goes_out_and_its_result_comes_back(server):
    got = []

    async def handler(ctx):
        got.append([c.name for c in ctx.available_agents()])
        got.append(await ctx.call_agent("policy-desk", "Is policy P-9 active?", function_call_id="call-1"))
        return "asked"

    server.responders["peer_call"] = answer_peer(data={"active": True})
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    [call] = server.of("peer_call")
    assert (call["parentTaskId"], call["peer"], call["text"], call["callId"]) == (
        "T1",
        "policy-desk",
        "Is policy P-9 active?",
        "call-1",
    )
    assert got[0] == ["policy-desk"]
    assert got[1].ok and got[1].text == "Policy P-9 is active." and got[1].data == {"active": True}
    await agent.stop()


async def test_a_peer_the_entrypoint_has_not_listed_is_not_called(server):
    got = []

    async def handler(ctx):
        got.append(await ctx.call_agent("outsider", "Approve claim C-4."))
        return "asked"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    assert not got[0].ok and "not available" in got[0].text
    assert server.of("peer_call") == []
    await agent.stop()


async def test_a_peer_call_the_entrypoint_refuses_fails_at_once(server):
    got = []

    async def handler(ctx):
        agent._transport._assume_peer("outsider")
        got.append(await ctx.call_agent("outsider", "Approve claim C-4."))
        return "asked"

    server.responders["peer_call"] = lambda f: [
        {"type": "error", "code": "forbidden", "message": "not a configured peer of this agent", "ref": f["subtaskId"]}
    ]
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await asyncio.wait_for(eventually(lambda: results(server, "T1")), 3)
    assert not got[0].ok
    assert got[0].text == 'peer agent "outsider": SAM refused the peer call: not a configured peer of this agent'


async def test_a_peers_error_reads_with_its_code(server):
    got = []

    async def handler(ctx):
        got.append(await ctx.call_agent("policy-desk", "Status?"))
        return "asked"

    server.responders["peer_call"] = answer_peer(
        "failed",
        "Agent stopped: SAM has stopped this agent",
        error={"code": -32600, "message": "Agent stopped: SAM has stopped this agent"},
    )
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    assert got[0].state == "error"
    assert got[0].text == 'peer agent "policy-desk": Agent stopped: SAM has stopped this agent (code -32600)'
    await agent.stop()


PEER_ENDINGS = {
    "answer": lambda f: {
        "type": "peer_result",
        "subtaskId": f["subtaskId"],
        "state": "completed",
        "text": "Policy P-9 is active.",
    },
    "answer nothing": lambda f: {"type": "peer_result", "subtaskId": f["subtaskId"], "state": "completed", "text": ""},
    "fail": lambda f: {"type": "peer_result", "subtaskId": f["subtaskId"], "state": "failed", "text": "no such policy"},
    "raise": lambda f: {
        "type": "peer_result",
        "subtaskId": f["subtaskId"],
        "state": "failed",
        "text": "",
        "error": {"code": -32600, "message": "Agent stopped"},
    },
    "forbidden": lambda f: {
        "type": "error",
        "code": "forbidden",
        "message": "the user may not invoke this peer",
        "ref": f["subtaskId"],
    },
    "undeliverable": lambda f: {
        "type": "error",
        "code": "publish_failed",
        "message": "the peer request could not be delivered",
        "ref": f["subtaskId"],
    },
    "stay silent": lambda f: None,
}
"""What the peer is asked -> what the entrypoint sends back for the call (None: nothing, ever)"""


async def test_each_way_a_peer_call_ends_reads_to_the_model_as_sam_words_it_and_is_audited_as_sam_decided(server):
    """The summary is what every adapter's peer tool returns to the model. The audit trail says granted only for a
    call SAM carried and denied only for one SAM refused as not allowed: a call that never left the agent, one SAM
    could not carry and one nobody answered are no decision of SAM's, so they leave no record."""
    audited, said = [], {}

    async def handler(ctx):
        for ask in PEER_ENDINGS:
            said[ask] = (await ctx.call_agent("policy-desk", ask)).summary()
        said["outsider"] = (await ctx.call_agent("outsider", "Approve claim C-4.")).summary()
        return "asked"

    def respond(frame):
        answer = PEER_ENDINGS[frame["text"]](frame)
        return [answer] if answer else []

    server.responders["peer_call"] = respond
    agent = await started(server, handler, link={"peer_call_timeout_seconds": 0.2}, audit_sink=audited.append)
    await server.send(task(1, "T1"))
    await asyncio.wait_for(eventually(lambda: results(server, "T1")), 5)

    assert said == {
        "answer": "Policy P-9 is active.",
        "answer nothing": "Peer agent responded (no text content)",
        "fail": 'peer agent "policy-desk" did not succeed: no such policy',
        "raise": 'peer agent "policy-desk": Agent stopped (code -32600)',
        "forbidden": 'peer agent "policy-desk": SAM refused the peer call: the user may not invoke this peer',
        "undeliverable": 'peer agent "policy-desk": SAM refused the peer call: the peer request could not be delivered',
        "stay silent": 'peer agent "policy-desk": no answer within 0.2 s',
        "outsider": 'peer agent "outsider" is not available on the mesh',
    }
    assert [f["text"] for f in server.of("peer_call")] == list(PEER_ENDINGS)
    assert [e.message for e in audited] == ["agent delegation granted"] * 4 + ["agent delegation denied"]
    assert dict(audited[-1].fields) == {"userID": USER, "taskID": "T1", "agentName": "policy-desk"}
    await agent.stop()


async def test_a_peer_call_given_up_is_cancelled_at_the_entrypoint(server):
    async def handler(ctx):
        try:
            await asyncio.wait_for(ctx.call_agent("policy-desk", "Status?"), 0.2)
        except TimeoutError:
            return "gave up"
        return "answered"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    [result] = await eventually(lambda: results(server, "T1"))
    [call] = server.of("peer_call")
    assert server.of("peer_cancel") == [{"type": "peer_cancel", "subtaskId": call["subtaskId"]}]
    assert result["text"] == "gave up"
    await agent.stop()


async def test_answers_held_across_a_disconnect_reach_their_prompt_and_peer_call_after_the_welcome(server):
    """The user answers and the peer finishes while the agent has no connection: the entrypoint holds both and sends them
    right after the next welcome, before anything else.
    """
    got = {}

    async def handler(ctx):
        approval, peer = await asyncio.gather(
            ctx.request_approval("pay_claim", {"claim": "C-3"}), ctx.call_agent("policy-desk", "Status?")
        )
        got.update(approval=approval, peer=peer)
        return "asked"

    agent = await started(server, handler, link={"connect_timeout_seconds": 1})
    await server.send(task(1, "T1"))
    [prompt] = await eventually(lambda: server.of("prompt"))
    [call] = await eventually(lambda: server.of("peer_call"))
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    server.after_welcome = [
        PEERS,
        {
            "type": "prompt_answer",
            "taskId": "T1",
            "requestId": prompt["requestId"],
            "status": "answered",
            "approved": True,
        },
        {"type": "peer_result", "subtaskId": call["subtaskId"], "state": "completed", "text": "Policy P-9 is active."},
    ]
    server.responders["consume"] = lambda f: [task(2, "T1", redelivered=True)]
    server.refusing = False
    await asyncio.wait_for(eventually(lambda: results(server, "T1")), 5)
    assert got["approval"].approved and got["peer"].ok and got["peer"].text == "Policy P-9 is active."
    await agent.stop()


async def test_a_prompt_and_a_peer_call_a_task_left_open_end_when_the_task_is_answered(server):
    """The call ended because its task did: SAM decided nothing about it, so nothing is audited as SAM's decision."""
    left, audited = {}, []

    async def handler(ctx):
        async def wait_for(name, pending):
            left[name] = await pending

        left["waiting"] = [
            asyncio.ensure_future(wait_for("approval", ctx.request_approval("pay_claim", {"claim": "C-3"}))),
            asyncio.ensure_future(wait_for("peer", ctx.call_agent("policy-desk", "Status?"))),
        ]
        await eventually(lambda: server.of("prompt") and server.of("peer_call"))
        return "answered without waiting"

    agent = await started(server, handler, audit_sink=audited.append)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    await asyncio.wait_for(asyncio.gather(*left["waiting"]), 2)
    assert left["approval"].outcome == "cancelled"
    assert (left["peer"].state, left["peer"].text) == ("error", "peer request cancelled")
    assert agent._transport._asks == {} and agent._transport._calls == {}
    assert [e.message for e in audited if e.message.startswith("agent delegation")] == []
    await agent.stop()


async def test_prompts_and_peer_calls_cancelled_because_their_task_ended_end_quietly(server, caplog):
    """A prompt and a peer call cancelled because their task ended end quietly.

    The user cancels: the entrypoint sends cancel, then cancels the task's open prompt. Once the result closes the task
    the entrypoint ends what is left of it, so nothing is open any more. The cancel and the prompt's answer race (the
    cancel is handled on a task of its own): either the run is cancelled while it waits, or the answer resumes it first
    and its next step, the peer call, comes back refused because the cancel has answered the task by then.
    """
    entered = asyncio.Event()
    outcome = {}

    async def handler(ctx):
        entered.set()
        try:
            outcome["approval"] = await ctx.request_approval("pay_claim", {"claim": "C-3"})
            outcome["peer"] = await ctx.call_agent("policy-desk", "Status?")
        except asyncio.CancelledError:
            outcome["cancelled"] = True
            raise
        return "asked"

    agent = await started(server, handler)
    with caplog.at_level("WARNING", logger="sam_link_sdk"):
        await server.send(task(1, "T1"))
        [prompt] = await eventually(lambda: server.of("prompt"))
        await server.send({"type": "cancel", "taskId": "T1"})
        await server.send(
            {"type": "prompt_answer", "taskId": "T1", "requestId": prompt["requestId"], "status": "canceled"}
        )
        [result] = await eventually(lambda: results(server, "T1"))
        await server.send(
            {"type": "prompt_answer", "taskId": "T1", "requestId": prompt["requestId"], "status": "canceled"}
        )
        await server.send(
            {
                "type": "peer_result",
                "subtaskId": "0192-never-open",
                "state": "canceled",
                "text": "peer request cancelled",
            }
        )
        await asyncio.sleep(0.1)
    assert result["state"] == "canceled"
    assert outcome.get("cancelled") is True or (
        outcome["approval"].outcome == "cancelled"
        and outcome["peer"].state == "error"
        and outcome["peer"].text == "the task was already answered"
    )
    assert agent._transport._asks == {} and agent._transport._calls == {}
    assert [r for r in caplog.records if r.name.startswith("sam_link_sdk.link")] == []
    await agent.stop()


async def test_a_peer_call_sent_while_the_link_reconnects_waits_for_its_answer(server):
    go = asyncio.Event()
    got = []

    async def handler(ctx):
        await go.wait()
        got.append(await ctx.call_agent("policy-desk", "Status?"))
        return "asked"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    go.set()
    await eventually(lambda: agent._transport._calls)
    server.responders["consume"] = lambda f: [task(2, "T1", redelivered=True)]
    server.refusing = False
    [call] = await eventually(lambda: server.of("peer_call", conn=1))
    await asyncio.sleep(0.5)
    await server.send({"type": "peer_result", "subtaskId": call["subtaskId"], "state": "completed", "text": "active"})
    await eventually(lambda: results(server, "T1"))
    assert got[0].ok and got[0].text == "active"
    await agent.stop()


async def test_cancelling_the_parent_task_ends_its_open_peer_call(server):
    ended = []

    async def handler(ctx):
        try:
            return (await ctx.call_agent("policy-desk", "Status?")).text
        except asyncio.CancelledError:
            ended.append(ctx.task_id)
            raise

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: server.of("peer_call"))
    await server.send({"type": "cancel", "taskId": "T1"})
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "canceled" and ended == ["T1"]
    assert agent._transport._calls == {}
    await agent.stop()


async def test_a_peer_the_entrypoint_drops_from_the_list_is_no_longer_offered(server):
    seen = []

    async def handler(ctx):
        seen.append([p.name for p in ctx.available_agents()])
        return "ok"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    await server.send({"type": "peers", "peers": []})
    await server.send(task(2, "T2"))
    await eventually(lambda: results(server, "T2"))
    assert seen == [["policy-desk"], []]
    await agent.stop()


def usage_sum(server: LinkServer, task_id: str) -> tuple[int, int, int]:
    used = [e["usage"] for e in events(server, task_id, "model_finished") if "usage" in e]
    return len(used), sum(u["input"] for u in used), sum(u["output"] for u in used)


async def test_usage_of_a_step_the_link_could_not_send_goes_with_the_answer_once_the_task_is_delivered_again(server):
    down = asyncio.Event()

    async def handler(ctx):
        await down.wait()
        ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
        await ctx.emit_event(ModelFinished(MODEL, "It is open."))
        return "Claim C-1 is open."

    agent = await started(server, handler, link={"connect_timeout_seconds": 0.2})
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    down.set()
    await eventually(lambda: "T1" in agent._transport._unsent, timeout=5)
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=1))
    assert usage_sum(server, "T1") == (1, 40, 6)
    assert types(server, 1) == ["describe", "consume", "event", "result", "ack"]
    await agent.stop()


async def test_a_lone_surrogate_reaches_sam_as_a_replacement_character_as_go_writes_it(server):
    async def handler(ctx):
        await ctx.emit_text("half an emoji: \ud83d")
        return "done \udc00"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    assert results(server, "T1")[0]["state"] == "completed"
    texts = [e.get("text", "") for e in events(server, "T1", "text")]
    assert any("half an emoji: \ufffd" in t for t in texts)
    await agent.stop()


async def test_usage_stays_within_what_the_entrypoint_takes_so_a_refusal_after_the_answer_names_the_result(server):
    async def handler(ctx):
        await ctx.emit_event(ModelFinished(MODEL, "a", usage=Usage(input_tokens=20_000_000, output_tokens=6)))
        ctx.record_usage(MODEL, Usage(input_tokens=9_000_000, output_tokens=1))
        for _ in range(10):
            ctx.record_usage(MODEL, Usage(input_tokens=9_000_000, output_tokens=0))
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    used = [
        f["event"]["usage"] for f in server.of("event") if f.get("taskId") == "T1" and "usage" in f.get("event", {})
    ]
    assert used[0]["input"] == 10_000_000
    assert all(u["input"] <= 10_000_000 and u["cachedInput"] <= 10_000_000 for u in used)
    assert sum(u["input"] + u["output"] for u in used) <= 100_000_000
    assert len(used) == 10
    await agent.stop()


async def test_usage_of_a_step_too_large_to_send_still_goes_out(caplog):
    server = await v2_server(limits={"maxFrameBytes": 4096, "maxStringBytes": 100}).start()
    try:

        async def handler(ctx):
            ctx.record_usage(MODEL, Usage(input_tokens=500, output_tokens=50))
            calls = tuple(ToolCall(f"c{i}", "lookup_claim", {"claim": f"C-{i}"}) for i in range(400))
            await ctx.emit_event(ModelFinished(MODEL, "", calls))
            return "done"

        agent = await started(server, handler)
        await server.send(task(1, "T1"))
        await eventually(lambda: results(server, "T1"))
        assert usage_sum(server, "T1") == (1, 500, 50)
        [bare] = events(server, "T1", "model_finished")
        assert "toolCalls" not in bare
        await agent.stop()
    finally:
        await server.stop()


async def test_usage_reported_while_the_answer_goes_out_is_sent_or_reported_lost(server, caplog):
    sink = []

    async def handler(ctx):
        ctx.record_usage(MODEL, Usage(input_tokens=10, output_tokens=2))
        sink.append(ctx._run.on_usage)
        return "done"

    agent = await started(server, handler)
    real_send = agent._transport._send

    async def send(frame, **only_on):
        if frame.get("type") == "event" and frame["event"]["kind"] == "model_finished" and sink:
            sink.pop()(MODEL, Usage(input_tokens=7, output_tokens=1))
        return await real_send(frame, **only_on)

    agent._transport._send = send
    with caplog.at_level("WARNING", logger="sam_link_sdk.link"):
        await server.send(task(1, "T1"))
        await eventually(lambda: results(server, "T1"))
    calls, sent_in, _ = usage_sum(server, "T1")
    lost = [r for r in caplog.records if "model usage lost" in r.getMessage()]
    assert sent_in == 10 and calls == 1
    assert len(lost) == 1 and "task=T1" in lost[0].getMessage() and "input=7 output=1" in lost[0].getMessage()
    await agent.stop()


async def test_usage_left_when_the_agent_disconnects_is_reported_lost(server, caplog):
    release = asyncio.Event()

    async def handler(ctx):
        await release.wait()
        ctx.record_usage(MODEL, Usage(input_tokens=30, output_tokens=3))
        return "done"

    agent = await started(server, handler, link={"connect_timeout_seconds": 0.2})
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    server.refusing = True
    await server.conns[0].close(code=1011)
    release.set()
    await eventually(lambda: "T1" in agent._transport._unsent, timeout=5)
    with caplog.at_level("WARNING", logger="sam_link_sdk.link"):
        await agent.stop()
    [lost] = [r for r in caplog.records if "model usage lost" in r.getMessage()]
    assert "task=T1 calls=1 input=30 output=3" in lost.getMessage()


async def test_usage_of_a_task_abandoned_at_shutdown_is_reported_lost(server, caplog):
    recorded = asyncio.Event()

    async def handler(ctx):
        ctx.record_usage(MODEL, Usage(input_tokens=12, output_tokens=2))
        recorded.set()
        await asyncio.sleep(30)
        return "never"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await asyncio.wait_for(recorded.wait(), 5)
    with caplog.at_level("WARNING", logger="sam_link_sdk.link"):
        await agent.stop()
    [lost] = [r for r in caplog.records if "model usage lost" in r.getMessage()]
    assert "task=T1 calls=1 input=12 output=2" in lost.getMessage()
    assert server.of("ack") == []


async def test_a_task_given_back_by_a_dropped_connection_is_held_and_acknowledged_with_the_run(server):
    release = asyncio.Event()
    runs = []

    async def handler(ctx):
        runs.append(ctx.task_id)
        await release.wait()
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: runs == ["T1"])
    await server.conns[0].close(code=1011)
    await eventually(lambda: server.of("consume", conn=1))
    assert types(server, 1)[:2] == ["describe", "consume"]
    await server.send(task(1, "T1", redelivered=True))
    await eventually(lambda: len(agent._transport._tasks["T1"].deliveries) == 2)
    release.set()
    await eventually(lambda: results(server, "T1"))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 1}])
    assert runs == ["T1"]

    await server.send(task(2, "T1", redelivered=True))
    await eventually(lambda: [f["delivery"] for f in server.of("ack", conn=1)] == [1, 2])
    first, again = results(server, "T1")
    assert runs == ["T1"] and again == first
    await agent.stop()


def answering_with_usage(ctx):
    ctx.record_usage(MODEL, Usage(input_tokens=40, output_tokens=6))
    return "done"


async def test_an_answered_task_delivered_again_gets_its_result_again_and_its_usage_is_recorded_once(server):
    """A gateway restart: the result reached a replica that no longer held the task, so SAM delivers it again. SAM
    recorded the spend when it took the usage, whichever replica took it, so the usage is not sent again."""
    agent = await started(server, answering_with_usage)
    await server.send(task(1, "T1"))
    await eventually(lambda: server.of("ack") == [{"type": "ack", "delivery": 1}])
    await server.send(task(2, "T1", redelivered=True))
    await eventually(lambda: [f["delivery"] for f in server.of("ack")] == [1, 2])
    frames = [f for _, f in server.frames if f["type"] in ("result", "ack")]
    assert [f["type"] for f in frames] == ["result", "ack", "result", "ack"] and frames[2] == frames[0]
    assert usage_sum(server, "T1") == (1, 40, 6)
    await agent.stop()


async def test_a_result_the_link_could_not_carry_goes_out_once_its_task_is_delivered_again_after_the_reconnect(server):
    """Not before: the replica that serves the new connection may never have held the task, and would refuse it."""
    release = asyncio.Event()

    async def handler(ctx):
        await release.wait()
        return "done"

    agent = await started(server, handler, link={"connect_timeout_seconds": 0.3})
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    server.refusing = True
    await server.conns[0].close(code=1011)
    release.set()
    await eventually(lambda: "T1" in agent._transport._unsent, timeout=5)
    assert results(server, "T1") == []
    server.refusing = False
    await eventually(lambda: server.of("consume", conn=1))
    await asyncio.sleep(0.2)
    assert types(server, 1) == ["describe", "consume"]
    await server.send(task(3, "T1", redelivered=True))
    await eventually(lambda: server.of("ack", conn=1) == [{"type": "ack", "delivery": 3}])
    assert types(server, 1) == ["describe", "consume", "result", "ack"] and "T1" not in agent._transport._unsent
    await agent.stop()


async def test_a_result_sam_could_not_publish_is_sent_again(server, monkeypatch):
    monkeypatch.setattr(link_transport, "FINAL_RESEND_DELAY_SECONDS", 0.01)
    tries = []

    def refuse_first(frame):
        tries.append(frame)
        if len(tries) == 1:
            return [{"type": "error", "code": "publish_failed", "message": "send it again", "ref": frame["taskId"]}]
        return []

    server.responders["result"] = refuse_first
    agent = await started(server, answering_with_usage)
    await server.send(task(1, "T1"))
    await eventually(lambda: len(results(server, "T1")) == 2)
    assert results(server, "T1")[0] == results(server, "T1")[1]
    assert usage_sum(server, "T1") == (1, 40, 6), "SAM holds the task and its usage: only the result is sent again"
    await agent.stop()


@pytest.mark.parametrize("code", ["bad_message", "limit"])
async def test_a_result_sam_refuses_is_followed_by_one_plain_failed_result(server, code):
    """The delivery is acknowledged and the task forgotten once the result is sent, so a result SAM refuses would leave
    the task unanswered until SAM's task TTL. One plain failed result follows it; if SAM refuses that too, nothing
    more is sent, so a refusal cannot loop."""

    def refuse(frame):
        return [{"type": "error", "code": code, "message": "a file carries its content", "ref": frame["taskId"]}]

    server.responders["result"] = refuse
    agent = await started(server, lambda ctx: TaskResult("done", data={"rows": 3}))
    await server.send(task(1, "T1"))
    await eventually(lambda: len(results(server, "T1")) == 2)
    first, fallback = results(server, "T1")
    assert first["state"] == "completed"
    assert fallback == {
        "type": "result",
        "taskId": "T1",
        "state": "failed",
        "text": "",
        "error": {"message": "The agent's answer could not be delivered: a file carries its content"},
    }
    await asyncio.sleep(0.3)
    assert len(results(server, "T1")) == 2
    await agent.stop()


async def test_a_result_that_cannot_be_encoded_is_answered_failed(server):
    """A file whose content is text, not bytes, cannot be encoded; the caller hears the task failed instead of
    waiting for SAM's task TTL."""
    agent = await started(server, lambda ctx: TaskResult("done", files=[File("a.md", "# text")]))
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    [answer] = results(server, "T1")
    assert answer["state"] == "failed"
    assert answer["error"] == {"message": "The agent's answer could not be delivered: it could not be encoded"}
    await agent.stop()


async def test_frames_over_the_welcomes_limit_are_cut_to_fit():
    server = await v2_server(limits={"maxFrameBytes": 4096, "maxStringBytes": 100}).start()
    try:

        async def handler(ctx):
            await ctx.emit_event(ToolFinished("c1", "export", {"rows": "x" * 10_000}))
            return TaskResult("Here is the export.", files=[File("export.csv", b"y" * 10_000, mime_type="text/csv")])

        agent = await started(server, handler)
        assert agent._transport._max_frame_bytes == 4096
        await server.send(task(1, "T1"))
        [result] = await eventually(lambda: results(server, "T1"))
        [step] = events(server, "T1", "tool_finished")
        assert len(step["result"]["rows"].encode()) <= 100 and "truncated" in step["result"]["rows"]
        assert "files" not in result and "export.csv" in result["text"] and result["state"] == "completed"
        assert all(len(json.dumps(f)) <= 4096 for _, f in server.frames)
        await agent.stop()
    finally:
        await server.stop()


async def test_the_welcome_says_the_agent_is_stopped_and_a_release_puts_it_back(tmp_path):
    """The agent is described and consuming although it is stopped: the entrypoint holds the description and keeps the card
    down.
    """
    server = await v2_server(enabled=False).start()
    server.after_welcome = [STOP, PEERS]
    try:
        heard = []
        agent = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(config(server)))
        agent.on_stop_change(lambda allowed, reason: heard.append((allowed, reason)))
        await agent.start()
        await eventually(lambda: not agent.allowed)
        with pytest.raises(AgentStopped):
            agent.check()
        await eventually(lambda: types(server, 0)[:2] == ["describe", "consume"])
        assert json.loads(stop_state_path(agent.config, agent._transport._stop_key).read_text()) == {"stopped": True}

        await server.send(task(1, "T1"))
        [refused] = await eventually(lambda: results(server, "T1"))
        assert refused["text"] == "Agent stopped: SAM has stopped this agent"
        await eventually(lambda: server.of("ack"))

        await server.send(RELEASE)
        await eventually(lambda: agent.allowed)
        await eventually(lambda: len(heard) == 2)
        assert heard == [(False, "stopped_by_sam"), (True, "released_by_sam")]
        assert len(server.of("describe")) == 1 and server.of("retire") == []
        await agent.stop()
    finally:
        await server.stop()


async def test_a_saved_stop_is_settled_by_the_welcome_without_waiting(server):
    """The welcome says SAM has not stopped the agent, so there is no settle period (30 s here) to wait out."""
    first = await started(server)
    await server.send(STOP)
    await eventually(lambda: not first.allowed)
    await first.stop()

    restarted = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(config(server)))
    assert not restarted.allowed
    await restarted.start()
    await eventually(lambda: restarted.allowed, timeout=1)
    await eventually(lambda: server.of("describe", conn=1))
    await restarted.stop()


async def test_a_stop_while_running_stops_the_task_and_leaves_the_card_to_the_entrypoint(server):
    async def handler(ctx):
        await asyncio.sleep(30)
        return "never"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    await server.send(STOP)
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "failed" and "operator stopped" in result["text"]
    await asyncio.sleep(0.1)
    assert server.of("retire") == []
    await agent.stop()


async def test_a_woken_agent_learns_its_peers_describes_itself_serves_its_queue_and_leaves(server):
    server.responders["consume"] = lambda f: [task(1, "T1")]
    peers_seen = []

    async def handler(ctx):
        peers_seen.append([p.name for p in ctx.available_agents()])
        return "done"

    woken = MeshAgent(agent_config(), handler, transport=LinkTransport(config(server)))
    taken = await asyncio.wait_for(woken.handle_wake(idle_seconds=0.2), 10)
    assert taken == 1 and peers_seen == [["policy-desk"]]
    assert types(server, 0) == ["describe", "consume", "event", "result", "ack"]


async def test_announce_describes_the_agent_and_retire_takes_it_down(server):
    agent = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(config(server)))
    await agent.announce()
    assert types(server, 0) == ["describe"]
    await agent.retire()
    assert types(server, 1) == ["retire"]


def test_the_hosted_runtimes_operator_commands_work_over_link_version_2(monkeypatch):
    """`python main.py announce|retire` of a Foundry or AgentCore agent (sam_link_sdk.hosted), with SAM_LINK_URL set."""
    import threading
    import time

    from sam_link_sdk.hosted import run_operator_command

    loop = asyncio.new_event_loop()
    thread = threading.Thread(target=loop.run_forever, daemon=True)
    thread.start()
    server = asyncio.run_coroutine_threadsafe(v2_server().start(), loop).result(5)
    try:
        monkeypatch.setenv("SAM_LINK_URL", server.url)
        built = []

        def build_agent(url: str) -> MeshAgent:
            built.append(url)
            return MeshAgent(
                agent_config(),
                lambda ctx: "ok",
                transport=LinkTransport(LinkConfig(url=url, agent=AGENT, token=TOKEN)),
            )

        run_operator_command(build_agent, "announce")
        run_operator_command(build_agent, "retire")
        deadline = time.monotonic() + 5
        while (types(server, 0), types(server, 1)) != (["describe"], ["retire"]) and time.monotonic() < deadline:
            time.sleep(0.01)
        assert (types(server, 0), types(server, 1)) == (["describe"], ["retire"])
        assert built == [server.url, server.url] and server.subprotocols == [PROTOCOL_V2, PROTOCOL_V2]
    finally:
        asyncio.run_coroutine_threadsafe(server.stop(), loop).result(5)
        loop.call_soon_threadsafe(loop.stop)
        thread.join(5)


CONSUME_REFUSED = {
    "type": "error",
    "code": "bad_message",
    "ref": "",
    "frame": "consume",
    "message": "send describe before consume: the agent's required scopes are checked from it",
}
SCOPES_REFUSED = {
    "type": "error",
    "code": "limit",
    "ref": "",
    "frame": "describe",
    "message": "the agent's tools have named more than 64 distinct scopes",
}


async def test_a_refused_consume_is_logged_and_retried_once_after_describing_again(server, caplog):
    refused = []

    def refuse_first_consume(frame):
        refused.append(frame)
        return [CONSUME_REFUSED] if len(refused) == 1 else []

    server.responders["consume"] = refuse_first_consume
    with caplog.at_level("ERROR", logger="sam_link_sdk.link"):
        agent = await started(server)
        await eventually(lambda: len(server.of("consume")) == 2)
    assert types(server, 0)[:4] == ["describe", "consume", "describe", "consume"]
    assert [r for r in caplog.records if "SAM refused to deliver tasks" in r.getMessage()]
    agent._transport._connection.spawn(agent._transport._describe_and_consume())
    await agent.stop()
    assert len(server.of("consume")) == 2
    assert not [t for t in asyncio.all_tasks() if "_describe_and_consume" in repr(t)], "nothing outlives the stop"


CONSUME_AFTER_REFUSAL = {
    "type": "error",
    "code": "bad_message",
    "ref": "",
    "frame": "consume",
    "message": "this connection's describe was refused: send one the entrypoint accepts before consume",
}


def refusing_descriptions(server) -> None:
    """The entrypoint refusing every describe, and so the consume that follows it on the connection."""
    server.responders["describe"] = lambda f: [SCOPES_REFUSED]
    server.responders["consume"] = lambda f: [CONSUME_AFTER_REFUSAL]


async def test_describe_and_consume_go_out_back_to_back_with_no_wait():
    server = await entrypoint().start()
    try:
        began = asyncio.get_running_loop().time()
        agent = MeshAgent(
            agent_config(),
            lambda ctx: "ok",
            transport=LinkTransport(LinkConfig(url=server.url, agent=AGENT, token=TOKEN)),
        )
        await agent.start()
        await eventually(lambda: server.of("consume"))
        assert asyncio.get_running_loop().time() - began < 0.5
        assert types(server, 0)[:2] == ["describe", "consume"]
        await agent.stop()
    finally:
        await server.stop()


async def test_a_refused_description_is_logged_not_resent_and_its_consume_refusal_is_expected(
    server, caplog, monkeypatch
):
    monkeypatch.setattr(link_describer, "DESCRIPTION_CHECK_SECONDS", 0.02)
    refusing_descriptions(server)
    with caplog.at_level("DEBUG", logger="sam_link_sdk.link"):
        agent = await started(server)
        await eventually(lambda: agent._transport._describer.refusal)
        await asyncio.sleep(0.2)
    assert (len(server.of("describe")), len(server.of("consume"))) == (1, 1)
    assert "more than 64 distinct scopes" in agent._transport._describer.refusal
    errors = [r.getMessage() for r in caplog.records if r.levelno >= logging.ERROR]
    assert len(errors) == 1 and "SAM refused the agent's description" in errors[0]
    server.responders.clear()
    agent.set_health_problem("claims-db", "degraded", "slow")
    await eventually(lambda: len(server.of("consume")) == 2)
    assert types(server, 0)[-2:] == ["describe", "consume"] and agent._transport._describer.refusal == ""
    await agent.stop()


async def test_a_description_refused_while_taking_tasks_keeps_the_connection_and_the_running_task(server, monkeypatch):
    monkeypatch.setattr(link_describer, "DESCRIPTION_CHECK_SECONDS", 0.02)
    release = asyncio.Event()

    async def handler(ctx):
        await release.wait()
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    refusing_descriptions(server)
    agent.set_health_problem("claims-db", "degraded", "slow")
    await eventually(lambda: agent._transport._describer.refusal)
    release.set()
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "completed" and len(server.conns) == 1
    await eventually(lambda: server.of("ack") == [{"type": "ack", "delivery": 1}])
    await agent.stop()


async def test_a_wake_whose_description_is_refused_ends_with_an_error_at_once(server):
    refusing_descriptions(server)
    woken = MeshAgent(agent_config(), lambda ctx: "ok", transport=LinkTransport(config(server)))
    with pytest.raises(LinkError, match="refused the agent's description"):
        await asyncio.wait_for(woken.handle_wake(idle_seconds=30), 5)


async def test_a_card_sam_could_not_publish_is_described_again_at_once(server):
    failed = []

    def fail_first(frame):
        failed.append(frame)
        return (
            [
                {
                    "type": "error",
                    "code": "publish_failed",
                    "ref": "",
                    "frame": "describe",
                    "message": "the card could not be published",
                }
            ]
            if len(failed) == 1
            else []
        )

    server.responders["describe"] = fail_first
    agent = await started(server)
    await eventually(lambda: len(server.of("describe")) == 2, timeout=3)
    assert len(server.of("consume")) == 1
    await agent.stop()


async def test_a_refusal_that_names_nothing_changes_neither_the_description_nor_the_tasks(server, caplog):
    """A refused event for a task whose id is too long to echo has no ref, so the refusal names nothing."""
    agent = await started(server)
    with caplog.at_level("WARNING", logger="sam_link_sdk.link"):
        await server.send({"type": "error", "code": "bad_message", "message": "a callId is at most 128 bytes"})
        await eventually(lambda: [r for r in caplog.records if "could not name" in r.getMessage()])
    assert agent._transport._describer.refused_description == "" and agent._transport._describer.described_here()
    await server.conns[0].close(code=1011)
    await eventually(lambda: types(server, 1)[:2] == ["describe", "consume"])
    await agent.stop()


async def test_a_result_sam_no_longer_holds_the_task_for_is_reported(server, caplog):
    server.responders["result"] = lambda f: [
        {"type": "error", "code": "unknown_task", "ref": f["taskId"], "message": "not an open task of this agent"}
    ]
    agent = await started(server, lambda ctx: "done")
    with caplog.at_level("WARNING", logger="sam_link_sdk.link"):
        await server.send(task(1, "T1"))
        await eventually(lambda: [r for r in caplog.records if "result was not delivered" in r.getMessage()])
    await agent.stop()


async def test_steps_are_kept_within_the_welcomes_name_id_and_tool_call_limits(server):
    long_model, long_tool, long_id = "m" * 300, "t" * 300, "c" * 200

    async def handler(ctx):
        calls = tuple(ToolCall(f"call-{i}", "lookup_claim", {}) for i in range(130)) + (
            ToolCall(long_id, long_tool, {}),
        )
        await ctx.emit_event(ModelFinished(long_model, "", calls))
        await ctx.emit_event(ToolStarted(long_id, long_tool, {}))
        await ctx.emit_event(ToolFinished(long_id, long_tool, "ok"))
        await ctx.emit_event(ToolStarted("", "lookup_claim", {}))
        await ctx.emit_event(ModelFailed(MODEL, "rate_limit", "p" * 300))
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    finished, started_, done, failed = [e for e in events(server, "T1") if e["kind"] != "text"]
    assert len(finished["model"].encode()) == 256 and len(finished["toolCalls"]) == 128
    assert len(started_["name"].encode()) == 256 and len(started_["callId"].encode()) <= 128
    assert started_["callId"] == done["callId"] and started_["callId"].startswith("h-")
    assert len(failed["provider"].encode()) == 256
    await agent.stop()


async def test_a_task_naming_more_models_than_the_link_carries_reports_the_rest_as_unknown(server):
    async def handler(ctx):
        for i in range(35):
            ctx.record_usage(f"model-{i}", Usage(input_tokens=1, output_tokens=1))
            await ctx.emit_event(ModelFinished(f"model-{i}", ""))
        return "done"

    agent = await started(server, handler, spend_model_keys={"model-34": "priced-34"})
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    finished = events(server, "T1", "model_finished")
    assert len({e["model"] for e in finished}) == 32 and finished[-1]["model"] == "unknown"
    assert finished[-1]["pricingKey"] == "priced-34" and sum(e["usage"]["input"] for e in finished) == 35
    await agent.stop()


async def test_prompts_and_peer_calls_over_the_open_limit_fail_at_once():
    server = await v2_server(limits={**LIMITS, "maxOpenPrompts": 1, "maxOpenPeerCalls": 1}).start()
    server.after_welcome = [PEERS]
    try:
        got = {}

        async def handler(ctx):
            first = asyncio.ensure_future(ctx.request_approval("pay_claim", {}, timeout_seconds=30))
            call = asyncio.ensure_future(ctx.call_agent("policy-desk", "Status?"))
            await eventually(lambda: server.of("prompt") and server.of("peer_call"))
            got["prompt"] = await ctx.request_approval("void_claim", {}, timeout_seconds=30)
            got["peer"] = await ctx.call_agent("policy-desk", "Again?")
            first.cancel()
            call.cancel()
            return "done"

        agent = await started(server, handler)
        await server.send(task(1, "T1"))
        await asyncio.wait_for(eventually(lambda: results(server, "T1")), 5)
        assert got["prompt"].outcome == "cancelled" and not got["peer"].ok and "open peer calls" in got["peer"].text
        assert (len(server.of("prompt")), len(server.of("peer_call"))) == (1, 1)
        await agent.stop()
    finally:
        await server.stop()


async def test_a_suggestions_auto_send_is_described(server):
    from sam_link_sdk import Suggestion

    agent = await started(server, suggestions=[Suggestion("Status", "What is open?", auto_send=True)])
    [describe] = server.of("describe")
    assert describe["agent"]["suggestions"] == [{"title": "Status", "prompt": "What is open?", "autoSend": True}]
    await agent.stop()


def test_a_failure_message_is_redacted_and_cut_to_the_welcomes_limit():
    from sam_link_sdk._model import TaskError, TaskOutcome
    from sam_link_sdk.link.v2 import Limits, result_frame

    outcome = TaskOutcome("failed", error=TaskError(message="db password=hunter2hunter2 " + "x" * 5000))
    frame = result_frame("T1", outcome, Limits(max_failure_message_bytes=200))
    assert "hunter2hunter2" not in frame["error"]["message"] and len(frame["error"]["message"].encode()) <= 200
    assert set(frame["error"]) == {"message"}
    model = result_frame("T2", TaskOutcome("failed", error=TaskError("rate_limit", "openai")), Limits())
    assert model["error"] == {"category": "rate_limit", "provider": "openai"}


async def test_a_task_waiting_for_a_slot_is_answered_and_acknowledged_when_its_time_limit_runs_out(server):
    async def handler(ctx):
        if ctx.task_id == "T1":
            await ctx.ask_user("Which queue?", timeout_seconds=30)
        return "done " + ctx.task_id

    agent = await started(server, handler, max_concurrent_tasks=1, task_timeout_seconds=0.3)
    await server.send(task(1, "T1"))
    await eventually(lambda: server.of("prompt"))
    await server.send(task(2, "T2"))
    [result] = await eventually(lambda: results(server, "T2"))
    assert result["state"] == "failed" and "ran longer than the limit" in result["text"]
    await eventually(lambda: 2 in [f["delivery"] for f in server.of("ack")])
    assert "T2" not in agent._runner.tasks and "T2" not in agent._transport._tasks
    await agent.stop()


async def test_a_task_waiting_for_a_slot_is_answered_and_acknowledged_when_sam_stops_the_agent(server):
    async def handler(ctx):
        await asyncio.sleep(30)
        return "never"

    agent = await started(server, handler, max_concurrent_tasks=1, task_timeout_seconds=0)
    await server.send(task(1, "T1"))
    await server.send(task(2, "T2"))
    await eventually(lambda: {"T1", "T2"} <= set(agent._runner.tasks))
    await server.send(STOP)
    for task_id in ("T1", "T2"):
        [result] = await eventually(lambda task_id=task_id: results(server, task_id))
        assert result["state"] == "failed" and "operator stopped" in result["text"]
    await eventually(lambda: sorted(f["delivery"] for f in server.of("ack")) == [1, 2])
    assert agent._runner.tasks == {} and agent._transport._tasks == {}
    await agent.stop()


async def test_an_acknowledgement_that_waited_out_a_reconnect_is_not_sent_on_the_new_connection(server):
    release = asyncio.Event()

    async def handler(ctx):
        await release.wait()
        return "done"

    agent = await started(server, handler, link={"connect_timeout_seconds": 1.0})
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    release.set()
    await eventually(lambda: "T1" in agent._transport._unsent, timeout=3)
    server.responders["consume"] = lambda f: [task(7, "T1", redelivered=True)]
    server.refusing = False
    await eventually(lambda: results(server, "T1"), timeout=3)
    await asyncio.sleep(0.2)
    assert server.of("ack", conn=1) == [{"type": "ack", "delivery": 7}]
    await agent.stop()


@pytest.mark.parametrize("waits_for", ["the user", "a peer"])
async def test_a_cancel_read_right_after_the_answer_a_run_waits_for_still_cancels_the_run(server, waits_for):
    """SAM answers the run's prompt, or its peer call, and cancels the task right after, and the agent reads both in
    one go. The run must stop where it waits, and not go on with the answer as if nobody had cancelled it (on Python
    3.11 ``asyncio.wait_for`` hands a result that came with the cancel back to its caller and drops the cancel)."""
    went_on, stopped = asyncio.Event(), asyncio.Event()

    async def handler(ctx):
        try:
            if waits_for == "the user":
                await ctx.request_approval("pay_claim", {"claim": "C-3"})
            else:
                await ctx.call_agent("policy-desk", "Status?")
            went_on.set()
            await asyncio.sleep(30)
        finally:
            stopped.set()

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    if waits_for == "the user":
        [asked] = await eventually(lambda: server.of("prompt"))
        answer = {"type": "prompt_answer", "taskId": "T1", "requestId": asked["requestId"], "status": "answered"}
        answer["approved"] = True
    else:
        [called] = await eventually(lambda: server.of("peer_call"))
        answer = {"type": "peer_result", "subtaskId": called["subtaskId"], "state": "completed", "text": "active"}
    reader = agent._transport._connection
    reader._on_raw(json.dumps(answer))
    reader._on_raw(json.dumps({"type": "cancel", "taskId": "T1"}))
    await asyncio.wait_for(stopped.wait(), 5)
    [result] = await eventually(lambda: results(server, "T1"))
    assert result["state"] == "canceled" and not went_on.is_set(), "the run went on after its task was cancelled"
    await agent.stop()


async def test_a_peer_call_is_not_waited_for_past_its_own_bound(server):
    got = []

    async def handler(ctx):
        got.append(await ctx.call_agent("policy-desk", "Status?"))
        return "asked"

    agent = await started(server, handler, link={"peer_call_timeout_seconds": 0.2})
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"), timeout=3)
    assert not got[0].ok and "no answer within 0.2 s" in got[0].text
    [call] = server.of("peer_call")
    assert server.of("peer_cancel") == [{"type": "peer_cancel", "subtaskId": call["subtaskId"]}]
    await agent.stop()


async def test_a_refusal_counts_for_the_describe_it_answers_when_the_description_changes_meanwhile(server, monkeypatch):
    monkeypatch.setattr(link_describer, "DESCRIPTION_CHECK_SECONDS", 0.01)
    monkeypatch.setattr(link_describer, "DESCRIBE_SPACING_SECONDS", 0.3)

    async def refuse_late():
        await asyncio.sleep(0.1)
        await server.send(SCOPES_REFUSED)

    server.responders["describe"] = lambda f: (
        (asyncio.ensure_future(refuse_late()), [])[1] if "slow" in json.dumps(f) else []
    )
    agent = await started(server)
    agent.set_health_problem("claims-db", "degraded", "slow")
    await eventually(lambda: any("slow" in json.dumps(f) for f in server.of("describe")))
    agent.clear_health_problem("claims-db")

    await eventually(lambda: len(server.of("describe")) == 3)
    await eventually(lambda: len(server.of("consume")) == 2)
    assert agent._transport._describer.refusal == ""
    await agent.stop()


async def test_a_refused_description_is_sent_again_on_a_new_connection_and_logged_again_only_later(server, caplog):
    refusing_descriptions(server)
    with caplog.at_level("DEBUG", logger="sam_link_sdk.link"):
        agent = await started(server)
        await eventually(lambda: agent._transport._describer.refusal)
        await server.conns[0].close(code=1011)
        await eventually(lambda: server.of("describe", conn=1))
        await eventually(
            lambda: len([r for r in caplog.records if "refused the agent's description" in r.getMessage()]) == 2
        )
    refusals = [r.levelno for r in caplog.records if "refused the agent's description" in r.getMessage()]
    assert refusals == [logging.ERROR, logging.DEBUG]
    await agent.stop()


async def test_a_card_sam_keeps_failing_to_publish_is_described_again_less_and_less_often(server, monkeypatch):
    monkeypatch.setattr(link_describer, "REPUBLISH_MIN_SECONDS", 0.05)
    monkeypatch.setattr(link_describer, "REPUBLISH_MAX_SECONDS", 0.2)
    monkeypatch.setattr(link_describer, "DESCRIBE_SPACING_SECONDS", 0.0)
    sent = []

    def publish_fails(frame):
        sent.append(asyncio.get_running_loop().time())
        return [
            {
                "type": "error",
                "code": "publish_failed",
                "ref": "",
                "frame": "describe",
                "message": "the card could not be published",
            }
        ]

    server.responders["describe"] = publish_fails
    agent = await started(server)
    await eventually(lambda: len(sent) >= 6, timeout=5)
    await agent.stop()
    gaps = [later - earlier for earlier, later in itertools.pairwise(sent)]
    assert gaps[0] >= 0.05 and gaps[1] >= 0.1 and gaps[2] >= 0.2, gaps
    assert all(gap < 0.4 for gap in gaps[3:5]), gaps


async def test_a_running_task_the_entrypoint_no_longer_holds_is_stopped(server):
    """The task was cancelled while the agent was away, and so never delivered again: the entrypoint refuses its steps."""
    stopped = asyncio.Event()

    async def handler(ctx):
        try:
            while True:
                await ctx.progress("Still checking claim C-1")
                await asyncio.sleep(0.05)
        except asyncio.CancelledError:
            stopped.set()
            raise

    server.responders["event"] = lambda f: [
        {"type": "error", "code": "unknown_task", "ref": f["taskId"], "message": "no open task"}
    ]
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await asyncio.wait_for(stopped.wait(), 5)
    await eventually(lambda: agent._runner.tasks == {})
    await agent.stop()


async def test_a_task_frame_that_cannot_be_read_is_answered_failed_and_acknowledged(server):
    runs = []
    agent = await started(server, lambda ctx: runs.append(ctx.task_id) or "done")
    await server.send(task(1, "T1", files=5))
    await eventually(lambda: results(server, "T1"))
    [result] = results(server, "T1")
    assert result["state"] == "failed" and "could not read this task" in result["text"]
    await eventually(lambda: server.of("ack") == [{"type": "ack", "delivery": 1}])
    assert runs == [] and agent._transport._tasks == {}

    await server.send(task(2, "T2"))
    await eventually(lambda: results(server, "T2"))
    assert results(server, "T2")[0]["state"] == "completed" and runs == ["T2"]
    await agent.stop()


async def test_steps_are_dropped_at_once_while_the_link_is_down_and_the_answer_follows_the_reconnect(server):
    down = asyncio.Event()
    emitted = []

    async def handler(ctx):
        await down.wait()
        started_at = time.monotonic()
        for i in range(20):
            await ctx.emit_text(f"chunk {i}")
        emitted.append(time.monotonic() - started_at)
        return "done"

    agent = await started(server, handler, link={"connect_timeout_seconds": 2.0})
    await server.send(task(1, "T1", sessionBehavior="run"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    down.set()
    await eventually(lambda: emitted)
    assert emitted[0] < 0.5
    server.responders["consume"] = lambda f: [task(2, "T1", redelivered=True)]
    server.refusing = False
    await eventually(lambda: results(server, "T1"), timeout=10)
    assert results(server, "T1")[0]["state"] == "completed"
    await agent.stop()
