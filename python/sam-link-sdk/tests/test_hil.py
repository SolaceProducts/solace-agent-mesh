# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Human-in-the-loop: the pause/answer round trip, what an answer means, the audit of each decision, and the time
limit while a person decides. (How a prompt is shown and an answer read off the wire is the agent link's: see
tests/test_link_protocol.py and tests/test_link_limits.py.)"""

from __future__ import annotations

import asyncio

import pytest

from sam_link_sdk import prompting as hil
from sam_link_sdk._model import PromptAnswer
from sam_link_sdk.context import bind_task
from sam_link_sdk.events import ToolFinished, ToolStarted
from sam_link_sdk.prompting import Option, Question

from .agent_rig import settle, start_agent


def bound(handler):
    """`handler` run with its task current (``current_task()``), as the agent's task runner runs every handler: a tool
    wrapped by with_approval asks only for the task it serves."""

    async def run(ctx):
        with bind_task(ctx):
            return await handler(ctx)

    return run


async def start(handler, **cfg):
    events = []
    agent, transport = await start_agent(bound(handler), audit_sink=events.append, **cfg)
    return agent, transport, events


@pytest.mark.parametrize(
    "answer, approved, outcome",
    [
        (PromptAnswer("r", "answered", approved=True), True, "approved"),
        (PromptAnswer("r", "answered", approved=False), False, "denied"),
        (PromptAnswer("r", "answered"), False, "unreadable"),
        (PromptAnswer("r", "cancelled", approved=True), False, "cancelled"),
        (PromptAnswer("r", "timed_out"), False, "timed_out"),
    ],
)
def test_only_an_explicit_boolean_approval_counts(answer, approved, outcome):
    assert hil.approval_result(answer) == hil.ApprovalResult(approved, outcome)


async def test_approval_pauses_the_task_then_resumes_on_the_answer():
    seen = {}

    async def handler(ctx):
        seen["decision"] = await ctx.request_approval(
            "send_email", {"to": "alice@example.com", "token": "abcdefghijkl"}
        )
        return "email handled"

    agent, transport, events = await start(handler)
    await transport.deliver("T1", user_id="u1")

    [prompt] = transport.prompts("T1")
    assert prompt.approval.tool == "send_email" and prompt.request_id.startswith("hil-approval-")
    assert "decision" not in seen and "T1" not in transport.finished

    await transport.answer(prompt, approved=True)

    assert seen["decision"] == hil.ApprovalResult(True, "approved")
    assert transport.finished["T1"].state == "completed"
    assert [e.message for e in events] == ["tool approval granted"]
    assert dict(events[0].fields) == {
        "userID": "u1",
        "taskID": "T1",
        "agentName": "claims-triage",
        "tool": "send_email",
    }
    await agent.stop()


async def test_a_denial_is_audited_as_a_refusal():
    async def handler(ctx):
        return str((await ctx.request_approval("delete_all")).outcome)

    agent, transport, events = await start(handler)
    await transport.deliver("T1")
    await transport.answer(transport.prompts("T1")[0], approved=False)
    assert transport.finished["T1"].text == "denied"
    assert events[0].message == "tool approval refused" and events[0].fields["approvalRefusal"] == "denied"
    await agent.stop()


async def test_ask_user_returns_the_typed_or_chosen_answers():
    got = {}

    async def handler(ctx):
        got["answer"] = await ctx.ask_user(
            [Question("Which env?", (Option("prod"), Option("stage"))), Question("Why?")]
        )
        return "ok"

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    [prompt] = transport.prompts("T1")
    assert prompt.approval is None and not prompt.request_id.startswith("hil-")
    assert [q.question for q in prompt.questions] == ["Which env?", "Why?"]
    assert [o.label for o in prompt.questions[0].options] == ["prod", "stage"]

    await transport.answer(prompt, answers=("dev", "audit"))
    assert got["answer"] == hil.UserAnswer("answered", ("dev", "audit"))
    await agent.stop()


async def test_a_plain_string_is_a_free_text_question():
    async def handler(ctx):
        return (await ctx.ask_user("What is the policy number?")).answers[0]

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    [prompt] = transport.prompts("T1")
    assert [q.question for q in prompt.questions] == ["What is the policy number?"] and prompt.questions[
        0
    ].options == ()
    await transport.answer(prompt, answers=("P-1182",))
    assert transport.finished["T1"].text == "P-1182"
    await agent.stop()


async def test_cancelled_and_timed_out_answers_are_not_approvals():
    outcomes = []

    async def handler(ctx):
        outcomes.append((await ctx.request_approval("t")).outcome)
        return "ok"

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    await transport.answer(transport.prompts("T1")[0], status="cancelled")
    await transport.deliver("T2")
    await transport.answer(transport.prompts("T2")[0], status="timed_out")
    assert outcomes == ["cancelled", "timed_out"]
    await agent.stop()


async def test_no_answer_within_the_timeout_ends_the_wait_without_asking_again():
    async def handler(ctx):
        return (await ctx.request_approval("t", timeout_seconds=0.05)).outcome

    agent, transport, events = await start(handler)
    await transport.deliver("T1")
    await asyncio.sleep(0.1)
    await settle()
    assert transport.finished["T1"].text == "timed_out"
    assert len(transport.prompts("T1")) == 1 and events[0].fields["approvalRefusal"] == "timed_out"
    await agent.stop()


async def test_cancelling_the_task_while_it_waits_ends_the_prompt():
    async def handler(ctx):
        await ctx.request_approval("t")
        return "never"

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    [asked] = transport.asked
    assert not asked.answer.done()
    await transport.cancel("T1")
    assert asked.answer.done() and asked.answer.result().status == "cancelled"
    assert transport.finished["T1"].state == "canceled"
    await agent.stop()


async def test_waiting_for_a_person_does_not_count_against_the_task_time_limit():
    async def handler(ctx):
        await asyncio.sleep(0.05)
        decision = await ctx.request_approval("pay_claim", {"claim_id": "C-1"})
        return "paid" if decision.approved else "not paid"

    agent, transport, _ = await start(handler, task_timeout_seconds=0.3)
    await transport.deliver("T1")
    for _ in range(100):
        if transport.prompts("T1"):
            break
        await asyncio.sleep(0.01)
    [prompt] = transport.prompts("T1")
    await asyncio.sleep(0.6)
    assert "T1" not in transport.finished, "the task is still waiting, not stopped at its limit"
    await transport.answer(prompt, approved=True)
    for _ in range(100):
        if "T1" in transport.finished:
            break
        await asyncio.sleep(0.01)
    assert transport.finished["T1"].state == "completed"
    await agent.stop()


async def test_the_time_limit_resumes_after_the_answer():
    async def handler(ctx):
        await ctx.request_approval("pay_claim")
        await asyncio.sleep(5)
        return "never"

    agent, transport, _ = await start(handler, task_timeout_seconds=0.2)
    await transport.deliver("T1")
    [prompt] = transport.prompts("T1")
    await asyncio.sleep(0.3)
    await transport.answer(prompt, approved=True)
    for _ in range(100):
        if "T1" in transport.finished:
            break
        await asyncio.sleep(0.01)
    answer = transport.finished["T1"]
    assert answer.state == "failed" and "longer than the limit" in answer.text
    await agent.stop()


pytest.importorskip("langchain_core")

from langchain_core.tools import tool  # noqa: E402

from sam_link_sdk.langchain import ask_user_tool, with_approval  # noqa: E402


async def test_with_approval_runs_an_approved_tool_and_blocks_a_denied_one():
    ran = []

    @tool
    def send_email(to: str, body: str) -> str:
        """Send an email."""
        ran.append(to)
        return f"sent to {to}"

    @tool
    def lookup(policy_id: str) -> str:
        """Look up a policy."""
        return "gold"

    async def handler(ctx):
        guarded = with_approval(ctx, [send_email, lookup], names=["send_email"], message="Sends real email.")
        assert guarded[1] is lookup and guarded[0].name == "send_email" and guarded[0].description == "Send an email."
        first = await guarded[0].ainvoke({"to": "alice@example.com", "body": "hi"})
        second = await guarded[0].ainvoke({"to": "bob@example.com", "body": "hi"})
        return f"{first} | {second}"

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    first = (await transport.prompted("T1"))[0]
    assert first.approval.message == "Sends real email." and first.approval.tool == "send_email"
    await transport.answer(first, approved=True)
    second = (await transport.prompted("T1", 2))[1]
    await transport.answer(second, approved=False)

    assert (await transport.answered("T1")).text == (
        "sent to alice@example.com | The user did not approve this call to send_email (denied), so it was not run."
    )
    assert ran == ["alice@example.com"]
    await agent.stop()


async def test_an_approved_tool_is_reported_and_audited_once_not_twice():
    """Found live: LangChain passes the running callbacks to the nested call, which reported the tool twice."""
    from sam_link_sdk.langchain import SamLinkCallbackHandler

    @tool
    def route_claim(policy_id: str) -> str:
        """Route a claim."""
        return "CLM-1"

    async def handler(ctx):
        guarded = with_approval(ctx, [route_claim])[0]
        call = {"name": "route_claim", "args": {"policy_id": "P-1"}, "id": "call_1", "type": "tool_call"}
        await guarded.ainvoke(call, config={"callbacks": [SamLinkCallbackHandler(ctx)]})
        return "done"

    agent, transport, events = await start(handler)
    await transport.deliver("T1")
    await transport.answer((await transport.prompted("T1"))[0], approved=True)
    await transport.answered("T1")

    assert len(transport.events("T1", ToolStarted)) == 1 and len(transport.events("T1", ToolFinished)) == 1
    assert [e.message for e in events].count("tool execution succeeded") == 1
    await agent.stop()


async def test_a_refused_tool_is_audited_as_failed_and_reported_as_an_error():
    from sam_link_sdk.langchain import SamLinkCallbackHandler

    @tool
    def route_claim(policy_id: str) -> str:
        """Route a claim."""
        return "CLM-1"

    async def handler(ctx):
        guarded = with_approval(ctx, [route_claim])[0]
        call = {"name": "route_claim", "args": {"policy_id": "P-1"}, "id": "call_1", "type": "tool_call"}
        result = await guarded.ainvoke(call, config={"callbacks": [SamLinkCallbackHandler(ctx)]})
        return f"{result.status}: {result.content}"

    agent, transport, events = await start(handler)
    await transport.deliver("T1")
    await transport.answer((await transport.prompted("T1"))[0], approved=False)

    answer = await transport.answered("T1")
    assert answer.text.startswith("error: The user did not approve this call to route_claim (denied)")
    assert [e.message for e in events] == ["tool approval refused", "tool execution failed"]
    await agent.stop()


async def test_the_model_can_ask_the_user_with_the_ask_tool():
    async def handler(ctx):
        return await ask_user_tool(ctx).ainvoke({"question": "Which queue?", "options": ["fast-track", "standard"]})

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    [prompt] = transport.prompts("T1")
    assert [o.label for o in prompt.questions[0].options] == ["fast-track", "standard"]
    await transport.answer(prompt, answers=("standard",))
    assert transport.finished["T1"].text == "standard"
    await agent.stop()
