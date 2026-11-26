# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Work a handler leaves running after its task is answered is noted once: one WARNING per task, shared between what
the adapters refuse (a guarded tool, a hook's model or tool call) and what the core refuses (a late question, a late
peer call)."""

from __future__ import annotations

import asyncio
import logging

from sam_link_sdk import _adapter
from sam_link_sdk.context import TaskHalted

from .agent_rig import start_agent


async def test_late_work_after_the_answer_logs_one_warning(caplog):
    done = asyncio.Event()

    async def leftover(ctx):
        while not ctx._run.answered:
            await asyncio.sleep(0.01)
        try:
            _adapter.check_running(ctx)
        except TaskHalted:
            pass
        _adapter.over(ctx, "route_claim")
        await ctx.call_agent("billing", "late")
        done.set()

    background = []

    async def handler(ctx):
        background.append(asyncio.ensure_future(leftover(ctx)))
        return "Claim C-1 is approved."

    with caplog.at_level(logging.DEBUG):
        agent, transport = await start_agent(handler)
        await transport.deliver("T1", "Approve C-1")
        await asyncio.wait_for(done.wait(), 5)
    await agent.stop()
    assert transport.finished["T1"].text == "Claim C-1 is approved." and transport.peer_calls("T1") == []
    late = [
        r.getMessage()
        for r in caplog.records
        if r.levelname == "WARNING" and "no longer held the task" in r.getMessage()
    ]
    assert len(late) == 1, late
