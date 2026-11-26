# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A refusal by our wrappers or hooks (scopes, a denied approval, a stopped or answered task) spends nothing a real
call owns: a tool's usage limit stays for the calls that run, and still holds for them.

Surveyed in the tested versions (docs/contributing/adapters.md, "What a refusal spends"): CrewAI's ``max_usage_count`` and
Agent Framework's ``max_invocations`` are a tool's own limits, claimed before the tool runs, and are tested here; the
other counters a refusal meets count the model's requests (a run's turns or iterations), as any failed call does.
"""

from __future__ import annotations

import asyncio
from types import SimpleNamespace

import pytest

from sam_link_sdk.context import bind_task

from .adapter_task import adapter_task
from .agent_rig import settle
from .test_scope_guard import SCOPE, SCOPES


def _approving(ctx, decisions):
    async def request_approval(name, args, *, message="", timeout_seconds=None):
        approved = decisions.pop(0)
        return SimpleNamespace(approved=approved, outcome="approved" if approved else "denied")

    ctx.request_approval = request_approval
    return ctx


def _crew_route_payment(ran):
    from crewai.tools import BaseTool

    class RoutePayment(BaseTool):
        name: str = "route_claim"
        description: str = "Route a claim."

        def _run(self, claim_id: str) -> str:
            ran.append(claim_id)
            return "routed"

    return RoutePayment(max_usage_count=1)


CREW_PATHS = {
    "react_sync": lambda tool, args: asyncio.to_thread(tool.to_structured_tool().invoke, args),
    "react_async": lambda tool, args: tool.to_structured_tool().ainvoke(args),
    "native_sync": lambda tool, args: asyncio.to_thread(tool.run, **args),
    "native_on_loop": lambda tool, args: _now(tool.run(**args)),
}


async def _now(value):
    return value


@pytest.mark.parametrize("path", list(CREW_PATHS))
@pytest.mark.parametrize("refusal", ["approval_denied", "scope_refused", "stopped", "answered"])
async def test_crewai_a_refusal_never_spends_the_tools_usage_limit_and_the_limit_holds_for_calls_that_run(
    refusal, path
):
    pytest.importorskip("crewai")
    if refusal == "approval_denied" and path == "native_on_loop":
        pytest.skip("no approval can be waited for on the loop: test_crewai_an_approval_on_the_event_loop_...")
    from sam_link_sdk.crewai import with_approval, with_scopes

    ran: list[str] = []
    inner = _crew_route_payment(ran)
    call = CREW_PATHS[path]
    async with (
        adapter_task("T1", scopes=SCOPES, granted=[] if refusal == "scope_refused" else [SCOPE]) as refused,
        adapter_task("T2", scopes=SCOPES, granted=[SCOPE]) as allowed,
    ):
        refused_ctx, allowed_ctx = refused.ctx, allowed.ctx
        if refusal == "approval_denied":
            allowed_ctx = refused_ctx = _approving(refused_ctx, [False, True, True])
        if refusal == "stopped":
            refused_ctx._run.halt()
        if refusal == "answered":
            await refused.run(lambda ctx: "Claim C-1 is routed.")
            await settle()
            assert refused.answer is not None and refused_ctx._run.answered
        with bind_task(refused_ctx):
            tool = (
                with_approval(refused_ctx, [inner]) if refusal == "approval_denied" else with_scopes([inner], SCOPES)
            )[0]
            first = await call(tool, {"claim_id": "C-1"})
        with bind_task(allowed_ctx):
            second = await call(tool, {"claim_id": "C-2"})
            try:
                third = await call(tool, {"claim_id": "C-3"})
            except Exception as exc:
                third = exc
    assert "error" in str(first) and second == "routed" and ran == ["C-2"]
    assert "usage limit" in str(third) or "maximum usage limit" in str(third)


async def test_agent_framework_a_scope_refusal_never_spends_the_tools_max_invocations():
    pytest.importorskip("agent_framework")
    from agent_framework import tool

    from sam_link_sdk.agent_framework import with_scopes

    ran: list[str] = []

    @tool(approval_mode="never_require", max_invocations=1)
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "routed"

    [guarded] = with_scopes([route_claim], SCOPES)
    async with (
        adapter_task("T1", scopes=SCOPES, granted=[]) as denied,
        adapter_task("T2", scopes=SCOPES, granted=[SCOPE]) as granted,
    ):
        with bind_task(denied.ctx):
            refused = await guarded.invoke(arguments={"claim_id": "C-1"})
        with bind_task(granted.ctx):
            ran_once = await guarded.invoke(arguments={"claim_id": "C-2"})
            with pytest.raises(Exception, match="maximum invocation limit"):
                await guarded.invoke(arguments={"claim_id": "C-3"})
    assert "Access denied" in str(refused[0].text) and ran_once[0].text == "routed" and ran == ["C-2"]


async def test_crewai_an_approval_on_the_event_loop_refuses_at_once_instead_of_blocking_it():
    """CrewAI's async native tool calling runs a tool on the event loop's own thread; the approval's prompt goes to
    that loop, so waiting there would block it for good. It refuses at once, spending nothing."""
    pytest.importorskip("crewai")
    from sam_link_sdk.crewai import ask_user_tool, with_approval

    ran: list[str] = []
    inner = _crew_route_payment(ran)
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        ctx = _approving(task.ctx, [True])
        with bind_task(ctx):
            [tool] = with_approval(ctx, [inner])
            refused = await asyncio.wait_for(_now(tool.run(claim_id="C-1")), 5)
            asked = ask_user_tool(ctx).run(question="Which queue?")
            approved = await asyncio.to_thread(tool.run, claim_id="C-2")
    assert "event loop" in refused and "event loop" in asked and approved == "routed" and ran == ["C-2"]
