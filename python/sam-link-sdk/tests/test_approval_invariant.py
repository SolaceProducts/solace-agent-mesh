# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The approval invariant, for every adapter with an approval path: a tool behind an approval never runs unless the
user approved that call.

A check before the prompt may only refuse with a result we build, for a fact that is certain (the task stopped or
answered, the user lacks the scopes, a stock tool's spent limit), or fall through to asking; it never hands the call to
the tool to refuse it. Each cell drives the guarded tool through the framework's real call path with a user who denies,
and with a user who is never asked (the task's channel cannot show a prompt), over inputs a check might mispredict:
normal, empty, null, a list, malformed JSON, missing and extra arguments, a hand-built tool or subclass that takes
anything, a spent usage limit, both nesting orders with with_scopes, sync and async entry points. The body never runs.
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator
from types import SimpleNamespace
from typing import Any

import pytest

from sam_link_sdk.context import TaskContext, bind_task

from .adapter_task import adapter_task
from .agent_rig import settle
from .test_scope_guard import SCOPE

USERS = ["denies", "cannot_be_asked"]
SCOPED = {"close_claim": [SCOPE]}


@contextlib.asynccontextmanager
async def user_task(user: str) -> AsyncIterator[TaskContext]:
    """The context of a running task whose user holds close_claim's scope and either denies every approval or, on a
    channel that cannot show a prompt, is never asked."""
    async with adapter_task(
        "T1", "Close claim C-1", scopes=SCOPED, granted=[SCOPE], can_prompt_user=user != "cannot_be_asked"
    ) as task:
        if user == "denies":

            async def request_approval(name, args, *, message="", timeout_seconds=None):
                return SimpleNamespace(approved=False, outcome="denied")

            task.ctx.request_approval = request_approval
        yield task.ctx


class Body:
    def __init__(self):
        self.ran: list[Any] = []


def _langchain_tools(body):
    from langchain_core.tools import BaseTool, tool

    @tool
    def close_claim(claim_id: str) -> str:
        """Close a claim."""
        body.ran.append(claim_id)
        return "closed"

    @tool
    async def close_claim_async(claim_id: str) -> str:
        """Close a claim."""
        body.ran.append(claim_id)
        return "closed"

    close_claim_async.name = "close_claim"

    class AnyArgs(BaseTool):
        name: str = "close_claim"
        description: str = "Close whatever it is given."

        def _run(self, *args: Any, **kwargs: Any) -> str:
            body.ran.append((args, kwargs))
            return "closed"

    return {"sync": close_claim, "async": close_claim_async, "any_args": AnyArgs()}


@pytest.mark.parametrize("user", USERS)
async def test_langchain_never_runs_an_unapproved_call(user):
    """LangChain rejects arguments it cannot validate: the body never sees them."""
    pytest.importorskip("langchain_core")
    from sam_link_sdk.langchain import with_approval, with_scopes

    body = Body()
    inputs = [{"claim_id": "C-1"}, {}, {"claim_id": "C-1", "extra": 1}, {"id": "C-1"}]
    async with user_task(user) as ctx:
        with bind_task(ctx):
            for raw in _langchain_tools(body).values():
                for guarded in (
                    with_approval(ctx, [raw])[0],
                    with_approval(ctx, with_scopes([raw], SCOPED))[0],
                    with_scopes(with_approval(ctx, [raw]), SCOPED)[0],
                ):
                    for args in inputs:
                        as_call = {"type": "tool_call", "name": "close_claim", "args": args, "id": "c1"}
                        for call in (guarded.ainvoke(args), guarded.ainvoke(as_call)):
                            try:
                                await call
                            except Exception:
                                pass
    assert body.ran == []


def _openai_tools(body):
    from agents import FunctionTool, function_tool

    @function_tool
    def close_claim(claim_id: str) -> str:
        """Close a claim."""
        body.ran.append(claim_id)
        return "closed"

    async def anything(tool_ctx, raw_args: str) -> str:
        body.ran.append(raw_args)
        return "closed"

    hand_built = FunctionTool(
        name="close_claim",
        description="Close a claim.",
        params_json_schema={"type": "object", "properties": {}, "additionalProperties": False},
        on_invoke_tool=anything,
        strict_json_schema=False,
    )
    return [close_claim, hand_built]


@pytest.mark.parametrize("user", USERS)
async def test_openai_agents_never_runs_an_unapproved_call(user):
    pytest.importorskip("agents")
    from agents.tool_context import ToolContext

    from sam_link_sdk.openai_agents import with_approval, with_scopes

    body = Body()
    raws = ['{"claim_id": "C-1"}', "{}", "null", "[]", "{oops", "", '{"claim_id": "C-1", "extra": 1}', '"C-1"']
    async with user_task(user) as ctx:
        with bind_task(ctx):
            for raw_tool in _openai_tools(body):
                for guarded in (
                    with_approval(ctx, [raw_tool])[0],
                    with_approval(ctx, with_scopes([raw_tool], SCOPED))[0],
                    with_scopes(with_approval(ctx, [raw_tool]), SCOPED)[0],
                ):
                    for raw in raws:
                        await guarded.on_invoke_tool(
                            ToolContext(context=None, tool_name="close_claim", tool_call_id="c1", tool_arguments=raw),
                            raw,
                        )
    assert body.ran == []


def _adk_tools(body):
    from google.adk.tools import BaseTool, FunctionTool
    from google.genai import types

    def close_claim(claim_id: str) -> str:
        """Close a claim."""
        body.ran.append(claim_id)
        return "closed"

    class Alias(FunctionTool):
        def _preprocess_args(self, args):
            args = dict(args)
            if "id" in args:
                args["claim_id"] = args.pop("id")
            return super()._preprocess_args(args)

    class AnyArgs(BaseTool):
        def __init__(self):
            super().__init__(name="close_claim", description="Close whatever it is given.")

        def _get_declaration(self):
            return types.FunctionDeclaration(name=self.name, description=self.description)

        async def run_async(self, *, args, tool_context):
            body.ran.append(args)
            return "closed"

    return [close_claim, Alias(close_claim), AnyArgs()]


@pytest.mark.parametrize("user", USERS)
async def test_adk_never_runs_an_unapproved_call(user):
    pytest.importorskip("google.adk")
    from sam_link_sdk.adk import with_approval, with_scopes

    body = Body()
    inputs = [{"claim_id": "C-1"}, {}, {"id": "C-1"}, {"claim_id": "C-1", "extra": 1}]
    async with user_task(user) as ctx:
        with bind_task(ctx):
            for raw in _adk_tools(body):
                for toolset in (
                    with_approval([raw])[0],
                    with_approval(with_scopes([raw], SCOPED))[0],
                    with_scopes(with_approval([raw]), SCOPED)[0],
                ):
                    [guarded] = await toolset.get_tools()
                    for args in inputs:
                        await guarded.run_async(args=args, tool_context=None)
    assert body.ran == []


def _crewai_tools(body):
    from crewai.tools import BaseTool, tool

    @tool("close_claim")
    def close_claim(claim_id: str) -> str:
        """Close a claim."""
        body.ran.append(claim_id)
        return "closed"

    class AnyArgs(BaseTool):
        name: str = "close_claim"
        description: str = "Close whatever it is given."

        def _run(self, **kwargs: Any) -> str:
            body.ran.append(kwargs)
            return "closed"

    class Spent(BaseTool):
        name: str = "close_claim"
        description: str = "Close a claim."

        def _run(self, claim_id: str) -> str:
            body.ran.append(claim_id)
            return "closed"

    class OwnClaim(Spent):
        def _claim_usage(self):
            return None

    return [
        close_claim,
        AnyArgs(),
        Spent(max_usage_count=1, current_usage_count=1),
        OwnClaim(max_usage_count=1, current_usage_count=1),
    ]


@pytest.mark.parametrize("user", USERS)
async def test_crewai_never_runs_an_unapproved_call(user):
    """CrewAI rejects arguments it cannot validate: the body never sees them."""
    pytest.importorskip("crewai")
    from sam_link_sdk.crewai import with_approval, with_scopes

    body = Body()
    inputs = [{"claim_id": "C-1"}, {"claim_id": "C-1", "extra": 1}]
    async with user_task(user) as ctx:
        with bind_task(ctx):
            for raw in _crewai_tools(body):
                for guarded in (
                    with_approval(ctx, [raw])[0],
                    with_approval(ctx, with_scopes([raw], SCOPED))[0],
                    with_scopes(with_approval(ctx, [raw]), SCOPED)[0],
                ):
                    structured = guarded.to_structured_tool()
                    for args in inputs:
                        for call in (
                            lambda guarded=guarded, args=args: asyncio.to_thread(guarded.run, **args),
                            lambda structured=structured, args=args: asyncio.to_thread(structured.invoke, args),
                            lambda structured=structured, args=args: structured.ainvoke(args),
                        ):
                            try:
                                await call()
                            except Exception:
                                pass
    assert body.ran == []


@pytest.mark.parametrize("user", USERS)
async def test_strands_never_runs_an_unapproved_call(user):
    pytest.importorskip("strands")
    from strands import Agent, tool
    from strands.types.tools import AgentTool

    from sam_link_sdk.strands import run_agent, with_approval, with_scopes

    from .test_strands import ScriptedModel, call, text, usage

    body = Body()

    @tool
    def close_claim(claim_id: str) -> str:
        """Close a claim."""
        body.ran.append(claim_id)
        return "closed"

    class AnyArgs(AgentTool):
        tool_name = "close_claim"
        tool_spec = {
            "name": "close_claim",
            "description": "Close whatever it is given.",
            "inputSchema": {"json": {"type": "object"}},
        }
        tool_type = "python"

        async def stream(self, tool_use, invocation_state, **kwargs):
            body.ran.append(tool_use.get("input"))
            from strands.types._events import ToolResultEvent

            yield ToolResultEvent(
                {"toolUseId": tool_use["toolUseId"], "status": "success", "content": [{"text": "closed"}]}
            )

    inputs = [{"claim_id": "C-1"}, {}, None, [], {"claim_id": "C-1", "extra": 1}]
    for raw in (close_claim, AnyArgs()):
        for tools in ([raw], with_scopes([raw], SCOPED)):
            model = ScriptedModel(
                [([call(f"c{n}", "close_claim", args)], usage(1, 1)) for n, args in enumerate(inputs)]
                + [([text("Done.")], usage(1, 1))]
            )
            async with user_task(user) as ctx:
                await run_agent(
                    ctx,
                    lambda c, model=model, tools=tools: Agent(
                        model=model,
                        tools=tools,
                        callback_handler=None,
                        hooks=[with_approval(c, tools, names=["close_claim"])],
                    ),
                )
    assert body.ran == []


@pytest.mark.parametrize("user", USERS)
async def test_agent_framework_never_runs_an_unapproved_call(user):
    pytest.importorskip("agent_framework")
    from agent_framework import FunctionTool, tool

    from sam_link_sdk.agent_framework import run_agent, with_approval, with_scopes

    from .test_agent_framework import ScriptedClient, agent_of, call, text, usage

    body = Body()

    def close(claim_id: str = "") -> str:
        """Close a claim."""
        body.ran.append(claim_id)
        return "closed"

    class Renewing(FunctionTool):
        def __call__(self, *args, **kwargs):
            self.invocation_count = 0
            return super().__call__(*args, **kwargs)

    plain = tool(approval_mode="never_require", name="close_claim")(close)
    spent = tool(approval_mode="never_require", name="close_claim", max_invocations=1)(close)
    spent.invocation_count = 1
    renewing = Renewing(name="close_claim", description="Close a claim.", func=close, max_invocations=1)
    renewing.invocation_count = 1
    inputs = [{"claim_id": "C-1"}, {}, {"claim_id": "C-1", "extra": 1}]
    for raw in (plain, spent, renewing):
        for tools in ([raw], with_scopes([raw], SCOPED)):
            client = ScriptedClient(
                [([call(f"c{n}", "close_claim", args)], usage(1, 1)) for n, args in enumerate(inputs)]
                + [([text("Done.")], usage(1, 1))]
            )
            async with user_task(user) as ctx:
                await run_agent(ctx, agent_of(client, tools), middleware=[with_approval(ctx, tools)])
    assert body.ran == []


@pytest.mark.parametrize("user", USERS)
async def test_hermes_never_runs_an_unapproved_call(user, monkeypatch):
    from sam_link_sdk.hermes import run_agent, with_approval

    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)
    inputs = [{"claim_id": "C-1"}, {}, {"claim_id": "C-1", "extra": 1}]

    def turn(h, agent):
        for n, args in enumerate(inputs):
            h.call_tool(agent.session_id, "close_claim", f"c{n}", args)

    async with user_task(user) as ctx:
        await run_agent(
            ctx, lambda c: hermes.agent(["close_claim"], turn), approval=with_approval(names=["close_claim"])
        )
    assert hermes.ran == []


HALTS = ["stopped", "cancelled", "answered"]


@contextlib.asynccontextmanager
async def approving_too_late(halt: str, seen: list[str]) -> AsyncIterator[TaskContext]:
    """A task whose user approves, but only after the task was stopped (a limit), cancelled (SAM's cancel) or answered
    (its handler returned) meanwhile. ``seen`` gets the task's halt reason as each approval comes back."""
    async with adapter_task("T1", "Close claim C-1", scopes=SCOPED, granted=[SCOPE]) as task:
        ctx = task.ctx

        async def request_approval(name, args, *, message="", timeout_seconds=None):
            if halt == "stopped":
                ctx._run.halt()
            elif halt == "cancelled":
                await task.cancel()
            elif not ctx._run.answered:
                await task.run(lambda c: "Claim C-1 is closed.")
                await settle()
            seen.append(ctx._run.halt_reason if halt == "stopped" or task.answer is not None else "not answered")
            return SimpleNamespace(approved=True, outcome="approved")

        ctx.request_approval = request_approval
        yield ctx


async def _ignoring(coro):
    """A framework may end a run whose task stopped; what matters is that the body did not run."""
    try:
        return await coro
    except Exception:
        return None


@pytest.mark.parametrize("halt", HALTS)
async def test_an_approval_that_arrives_after_the_task_ended_runs_nothing(halt, monkeypatch):
    """The approval is a wait: the stop, cancel or answer that came during it is checked again after it, in every
    adapter's approval path."""
    body = Body()
    ran_by: dict[str, list] = {}
    seen: list[str] = []

    if __import__("importlib").util.find_spec("langchain_core"):
        from sam_link_sdk.langchain import with_approval as lc_approval

        async with approving_too_late(halt, seen) as ctx:
            with bind_task(ctx):
                await _ignoring(lc_approval(ctx, [_langchain_tools(body)["sync"]])[0].ainvoke({"claim_id": "C-1"}))
        ran_by["langchain"] = list(body.ran)

    if __import__("importlib").util.find_spec("agents"):
        from agents.tool_context import ToolContext

        from sam_link_sdk.openai_agents import with_approval as oa_approval

        raw = '{"claim_id": "C-1"}'
        async with approving_too_late(halt, seen) as ctx:
            with bind_task(ctx):
                guarded = oa_approval(ctx, [_openai_tools(body)[0]])[0]
                await guarded.on_invoke_tool(
                    ToolContext(context=None, tool_name="close_claim", tool_call_id="c1", tool_arguments=raw), raw
                )
        ran_by["openai_agents"] = list(body.ran)

    if __import__("importlib").util.find_spec("google.adk"):
        from sam_link_sdk.adk import with_approval as adk_approval

        async with approving_too_late(halt, seen) as ctx:
            with bind_task(ctx):
                [guarded] = await adk_approval([_adk_tools(body)[0]])[0].get_tools()
                await guarded.run_async(args={"claim_id": "C-1"}, tool_context=None)
        ran_by["adk"] = list(body.ran)

    if __import__("importlib").util.find_spec("crewai"):
        from sam_link_sdk.crewai import with_approval as crew_approval

        async with approving_too_late(halt, seen) as ctx:
            with bind_task(ctx):
                [guarded] = crew_approval(ctx, [_crewai_tools(body)[0]])
                await asyncio.to_thread(guarded.run, claim_id="C-1")
        ran_by["crewai"] = list(body.ran)

    if __import__("importlib").util.find_spec("strands"):
        from strands import Agent, tool

        from sam_link_sdk.strands import run_agent as strands_run, with_approval as strands_approval

        from .test_strands import ScriptedModel, call, text, usage

        @tool
        def close_claim(claim_id: str) -> str:
            """Close a claim."""
            body.ran.append(claim_id)
            return "closed"

        model = ScriptedModel(
            [([call("c1", "close_claim", {"claim_id": "C-1"})], usage(1, 1)), ([text("Done.")], usage(1, 1))]
        )
        async with approving_too_late(halt, seen) as ctx:
            await _ignoring(
                strands_run(
                    ctx,
                    lambda c: Agent(
                        model=model,
                        tools=[close_claim],
                        callback_handler=None,
                        hooks=[strands_approval(c, [close_claim])],
                    ),
                )
            )
        ran_by["strands"] = list(body.ran)

    if __import__("importlib").util.find_spec("agent_framework"):
        from agent_framework import tool as af_tool

        from sam_link_sdk.agent_framework import run_agent as af_run, with_approval as af_approval

        from .test_agent_framework import ScriptedClient, agent_of, call, text, usage

        def close(claim_id: str) -> str:
            """Close a claim."""
            body.ran.append(claim_id)
            return "closed"

        plain = af_tool(approval_mode="never_require", name="close_claim")(close)
        client = ScriptedClient(
            [([call("c1", "close_claim", {"claim_id": "C-1"})], usage(1, 1)), ([text("Done.")], usage(1, 1))]
        )
        async with approving_too_late(halt, seen) as ctx:
            await _ignoring(af_run(ctx, agent_of(client, [plain]), middleware=[af_approval(ctx, [plain])]))
        ran_by["agent_framework"] = list(body.ran)

    from sam_link_sdk.hermes import run_agent as hermes_run, with_approval as hermes_approval

    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)
    async with approving_too_late(halt, seen) as ctx:
        await _ignoring(
            hermes_run(
                ctx,
                lambda c: hermes.agent(
                    ["close_claim"],
                    lambda h, agent: h.call_tool(agent.session_id, "close_claim", "c1", {"claim_id": "C-1"}),
                ),
                approval=hermes_approval(names=["close_claim"]),
            )
        )
    ran_by["hermes"] = list(hermes.ran)

    assert {name: ran for name, ran in ran_by.items() if ran} == {}
    assert seen == [halt] * len(ran_by)
