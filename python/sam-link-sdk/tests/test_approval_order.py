# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""An approval asks the user last: after the stop and the answer (the task can still run), the scopes, and whatever
else would refuse the call anyway and can be known before asking (a tool's own usage or invocation limit, arguments the
tool cannot read). A user is never asked to approve a call that then does not run for a reason known beforehand, and
no "approved" record is written for it."""

from __future__ import annotations

import asyncio
from types import SimpleNamespace

import pytest

from sam_link_sdk.context import bind_task

from .adapter_task import adapter_task
from .test_scope_guard import SCOPE, SCOPES


def _counting(ctx, approved=True):
    asked: list[str] = []

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(name)
        return SimpleNamespace(approved=approved, outcome="approved" if approved else "denied")

    ctx.request_approval = request_approval
    return asked


async def test_crewai_does_not_ask_about_a_call_past_the_tools_usage_limit():
    pytest.importorskip("crewai")
    from sam_link_sdk.crewai import with_approval

    from .test_refusal_budgets import _crew_route_payment

    ran: list[str] = []
    inner = _crew_route_payment(ran)
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        ctx = task.ctx
        asked = _counting(ctx)
        with bind_task(ctx):
            [tool] = with_approval(ctx, [inner])
            first = await asyncio.to_thread(tool.run, claim_id="C-1")
            second = await asyncio.to_thread(tool.run, claim_id="C-2")
    assert first == "routed" and "usage limit of 1" in str(second)
    assert asked == ["route_claim"] and ran == ["C-1"]


async def test_agent_framework_does_not_ask_about_a_call_past_the_tools_invocation_limit():
    pytest.importorskip("agent_framework")
    from agent_framework import tool

    from sam_link_sdk.agent_framework import run_agent, with_approval

    from .test_agent_framework import ScriptedClient, agent_of, call, text, usage

    ran: list[str] = []

    @tool(approval_mode="never_require", max_invocations=1)
    def route_claim(claim_id: str) -> str:
        """Route a claim."""
        ran.append(claim_id)
        return "routed"

    client = ScriptedClient(
        [
            ([call("c1", "route_claim", {"claim_id": "C-1"})], usage(1, 1)),
            ([call("c2", "route_claim", {"claim_id": "C-2"})], usage(1, 1)),
            ([text("Done.")], usage(1, 1)),
        ]
    )
    async with adapter_task() as task:
        ctx = task.ctx
        asked = _counting(ctx)
        assert (
            await run_agent(ctx, agent_of(client, [route_claim]), middleware=[with_approval(ctx, [route_claim])])
            == "Done."
        )
    assert asked == ["route_claim"] and ran == ["C-1"]


async def test_crewai_reads_the_spent_limit_through_with_scopes_and_answers_it_without_calling_the_tool():
    """with_approval(ctx, with_scopes(...)), the order the docs show: the limit is the wrapped tool's."""
    pytest.importorskip("crewai")
    from sam_link_sdk.crewai import with_approval, with_scopes

    from .test_refusal_budgets import _crew_route_payment

    ran: list[str] = []
    inner = _crew_route_payment(ran)
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        ctx = task.ctx
        asked = _counting(ctx)
        with bind_task(ctx):
            [tool] = with_approval(ctx, with_scopes([inner], SCOPES))
            first = await asyncio.to_thread(tool.run, claim_id="C-1")
            second = await asyncio.to_thread(tool.run, claim_id="C-2")
    assert first == "routed" and "usage limit of 1" in str(second)
    assert asked == ["route_claim"] and ran == ["C-1"]


async def test_crewai_asks_about_a_call_to_a_tool_whose_usage_claim_is_its_own():
    """A tool that claims usage its own way may run past CrewAI's count: the limit is not certain, so it is asked."""
    pytest.importorskip("crewai")
    from crewai.tools import BaseTool

    from sam_link_sdk.crewai import with_approval

    ran: list[str] = []

    class Renewing(BaseTool):
        name: str = "route_claim"
        description: str = "Route a claim."

        def _claim_usage(self):
            return None

        def _run(self, claim_id: str) -> str:
            ran.append(claim_id)
            return "routed"

    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        ctx = task.ctx
        asked = _counting(ctx)
        with bind_task(ctx):
            [tool] = with_approval(ctx, [Renewing(max_usage_count=1, current_usage_count=1)])
            assert await asyncio.to_thread(tool.run, claim_id="C-1") == "routed"
    assert asked == ["route_claim"] and ran == ["C-1"]


async def test_openai_agents_asks_about_arguments_it_cannot_read_and_runs_nothing_unapproved():
    """Whether a tool can read its arguments is the tool's business (a hand-built FunctionTool reads anything): the
    approval asks, with the raw arguments, and a denial runs nothing."""
    pytest.importorskip("agents")
    from agents import FunctionTool
    from agents.tool_context import ToolContext

    from sam_link_sdk.openai_agents import with_approval

    ran: list[str] = []

    async def close_all_claims(tool_ctx, raw_args: str) -> str:
        ran.append(raw_args)
        return "closed every open claim"

    tool = FunctionTool(
        name="close_all_claims",
        description="Close every open claim.",
        params_json_schema={"type": "object", "properties": {}, "additionalProperties": False},
        on_invoke_tool=close_all_claims,
        strict_json_schema=False,
    )
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        ctx = task.ctx
        asked = _counting(ctx, approved=False)
        with bind_task(ctx):
            [guarded] = with_approval(ctx, [tool])
            for raw in ("{}", "null", "[]", "{oops", ""):
                await guarded.on_invoke_tool(
                    ToolContext(context=None, tool_name="close_all_claims", tool_call_id="c1", tool_arguments=raw), raw
                )
    assert ran == [] and asked == ["close_all_claims"] * 5


async def test_adk_asks_about_a_call_whatever_its_arguments_and_runs_nothing_unapproved():
    """ADK checks a function tool's arguments after its own preprocessing, which a subclass may change (an alias for
    an argument): the approval asks, and a denial runs nothing."""
    pytest.importorskip("google.adk")
    from google.adk.tools import FunctionTool

    from sam_link_sdk.adk import with_approval

    ran: list[str] = []

    def close_claim(claim_id: str) -> str:
        """Close a claim."""
        ran.append(claim_id)
        return "closed"

    class AliasTool(FunctionTool):
        def _preprocess_args(self, args):
            args = dict(args)
            if "id" in args:
                args["claim_id"] = args.pop("id")
            return super()._preprocess_args(args)

    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        ctx = task.ctx
        asked = _counting(ctx, approved=False)
        with bind_task(ctx):
            [tool] = await with_approval([AliasTool(close_claim)])[0].get_tools()
            for args in ({"id": "C-1"}, {}, {"claim_id": "C-2"}):
                await tool.run_async(args=args, tool_context=None)
    assert ran == [] and asked == ["close_claim"] * 3


async def test_crewai_a_spent_tool_function_is_answered_without_asking():
    """An @tool function is CrewAI's Tool, whose run claims a use through CrewAI's own claim before the body, as
    BaseTool.run does (crewai 1.15): a spent one is answered with CrewAI's failure, nobody asked, the body not run."""
    pytest.importorskip("crewai")
    from crewai.tools import tool
    from crewai.tools.base_tool import BaseTool, Tool

    from sam_link_sdk.crewai import with_approval

    ran: list[str] = []

    @tool("close_claim", max_usage_count=1)
    def close_claim(claim_id: str) -> str:
        """Close a claim."""
        ran.append(claim_id)
        return "closed"

    assert type(close_claim) is Tool and Tool._claim_usage is BaseTool._claim_usage
    close_claim.current_usage_count = 1
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        ctx = task.ctx
        asked = _counting(ctx)
        with bind_task(ctx):
            [guarded] = with_approval(ctx, [close_claim])
            result = await asyncio.to_thread(guarded.run, claim_id="C-1")
    assert asked == [] and ran == [] and "usage limit of 1" in str(result)
