# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The test vehicle (tests/agent_rig.py) itself. (The adapter conformance suite runs on it in
tests/test_adapter_conformance.py.)"""

from __future__ import annotations

from sam_link_sdk._model import PeerInfo, PeerResult
from sam_link_sdk.config import ToolInfo
from sam_link_sdk.events import Progress, Text

from .agent_rig import RecordingTransport, start_agent, task_context


async def test_the_recording_transport_records_a_task_from_delivery_to_answer():
    async def handler(ctx):
        await ctx.progress("Checking the policy")
        return "Claim C-1 is approved."

    agent, transport = await start_agent(handler)
    assert transport.descriptions and transport.descriptions[0].description == "Triages insurance claims."
    await transport.deliver("T1", "What about claim C-1?")
    assert transport.events("T1", Progress) == [Progress("Checking the policy")]
    assert (transport.finished["T1"].state, transport.finished["T1"].text) == ("completed", "Claim C-1 is approved.")
    await agent.stop()


async def test_a_test_answers_prompts_and_peer_calls_as_sam_would():
    async def handler(ctx):
        decision = await ctx.request_approval("close_claim", {"claim_id": "C-1"})
        peer = await ctx.call_agent("policy-desk", "Is P-1 active?")
        return f"{decision.outcome} / {peer.text}"

    agent, transport = await start_agent(handler, RecordingTransport([PeerInfo("policy-desk")]))
    await transport.deliver("T1")
    [prompt] = transport.prompts("T1")
    assert prompt.approval.tool == "close_claim"
    await transport.answer(prompt, approved=True)
    [call] = transport.peer_calls("T1")
    assert (call.peer, call.text) == ("policy-desk", "Is P-1 active?")
    await transport.reply(0, PeerResult("policy-desk", "completed", "P-1 is active."))
    assert transport.finished["T1"].text == "approved / P-1 is active."
    await agent.stop()


async def test_a_task_context_is_the_real_one_of_a_task_the_agent_runs():
    async with task_context(
        "T",
        user_id="alice@example.com",
        granted_tool_scopes=["claims:case:read"],
        tools=[
            ToolInfo("lookup", required_scopes=["claims:case:read"]),
            ToolInfo("route", required_scopes=["claims:case:update"]),
        ],
    ) as task:
        assert task.ctx.task_id == "T" and task.ctx.user_id == "alice@example.com"
        assert task.ctx.check_tool("lookup") and not task.ctx.check_tool("route")

        async def step(ctx):
            await ctx.progress("step")
            return "done"

        assert await task.run(step) == "done"
        assert task.events == [Progress("step"), Text("done")]
    assert (task.answer.state, task.answer.text) == ("completed", "done")
