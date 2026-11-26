# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The rule for every wait this SDK puts before a guarded body: the check that preceded it is made again after it.

An approval prompt is such a wait. The task can end while the user has not answered: SAM stops the agent, the task is
cancelled, or the handler answers while work it left running waits on the prompt. A crew's worker thread and a Hermes
turn's thread go on waiting; the stop does not cancel them. Here the approval itself comes back approved after the task
ended (the shape of an answer already on its way, or a channel that resolves it late), through a real MeshAgent, so
these pin the adapters' own re-check after the wait, whatever the transport does with open prompts. The body does not
run.
"""

from __future__ import annotations

import asyncio
import threading

import pytest

from sam_link_sdk._model import Prompt, PromptAnswer

from .agent_rig import Asked, RecordingTransport, start_agent

ENDINGS = ["stopped_by_sam", "cancelled", "answered"]
ENDED_AS = {"stopped_by_sam": "failed", "cancelled": "canceled", "answered": "completed"}


class ApprovesLate(RecordingTransport):
    """SAM's side of a task whose approval comes back approved, but only once the task has ended `how`: SAM's stop or
    cancel is sent while the prompt is open, or the handler answers meanwhile."""

    def __init__(self, how: str):
        super().__init__()
        self.how = how
        self.asking = asyncio.Event()

    async def _ask(self, task_id: str, prompt: Prompt) -> PromptAnswer:
        self.asked.append(Asked(task_id, prompt, asyncio.get_running_loop().create_future()))
        self.asking.set()
        if self.how == "stopped_by_sam":
            await self.control(False, "stopped_by_sam")
        elif self.how == "cancelled":
            await self.cancel(task_id)
        for _ in range(1000):
            if task_id in self.finished:
                break
            await asyncio.sleep(0.01)
        return PromptAnswer(prompt.request_id, "answered", approved=True)


async def serve(how, call) -> ApprovesLate:
    """Serve task T1 with a handler that makes the guarded call (`call(ctx, done)`, which runs the framework's thread
    and sets `done` there once the guarded call returned); for "answered" the handler leaves the call running and
    answers at once. Returns once the guarded call is over on its thread."""
    transport = ApprovesLate(how)
    done = threading.Event()

    async def run(ctx):
        """A stopped run ends as the stop; what matters is that the body did not run."""
        try:
            return await call(ctx, done)
        except Exception:
            return None

    background = []

    async def handler(ctx):
        if how == "answered":
            background.append(asyncio.ensure_future(run(ctx)))
            await asyncio.wait_for(transport.asking.wait(), 10)
            return "Answered while the prompt was open."
        return await run(ctx)

    agent, _ = await start_agent(handler, transport)
    try:
        await transport.deliver("T1", "Close claim C-1")
        assert await asyncio.to_thread(done.wait, 10), "the guarded call did not return"
    finally:
        if how == "stopped_by_sam":
            await transport.control(True, "released_by_sam")
        await agent.stop()
    assert [p.approval.tool for p in transport.prompts("T1")] == ["close_claim"]
    assert transport.finished["T1"].state == ENDED_AS[how]
    return transport


@pytest.mark.parametrize("how", ENDINGS)
async def test_crewai_an_approval_that_comes_after_the_task_ended_runs_no_tool_on_the_crews_thread(how):
    pytest.importorskip("crewai")
    from crewai.tools import tool

    from sam_link_sdk.crewai import with_approval

    ran: list[str] = []

    @tool("close_claim")
    def close_claim(claim_id: str) -> str:
        """Close a claim."""
        ran.append(claim_id)
        return "closed"

    async def call(ctx, done):
        [guarded] = with_approval(ctx, [close_claim])

        def on_the_crews_thread():
            try:
                return guarded.run(claim_id="C-1")
            finally:
                done.set()

        return await asyncio.to_thread(on_the_crews_thread)

    await serve(how, call)
    assert ran == []


@pytest.mark.parametrize("how", ENDINGS)
async def test_hermes_an_approval_that_comes_after_the_task_ended_runs_no_tool_on_the_turns_thread(how, monkeypatch):
    """Hermes at its hook boundary (test_hermes covers a real AIAgent): the turn's thread waits on the approval asked
    from pre_tool_call."""
    from sam_link_sdk.hermes import run_agent, with_approval

    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)

    async def call(ctx, done):
        def turn(h, agent):
            try:
                h.call_tool(agent.session_id, "close_claim", "c1", {"claim_id": "C-1"})
            finally:
                done.set()

        return await run_agent(
            ctx, lambda c: hermes.agent(["close_claim"], turn), approval=with_approval(names=["close_claim"])
        )

    await serve(how, call)
    assert hermes.ran == []
