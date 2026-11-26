# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Peer tools for the OpenAI Agents, ADK and CrewAI adapters: names, schemas, call-id passing, delegation."""

from __future__ import annotations

import asyncio
from types import SimpleNamespace

from sam_link_sdk import PeerResult
from sam_link_sdk.delegation import AgentCard

CARDS = [AgentCard("policy-bot", "Answers policy questions.", "Policy Bot", ["Lookup"]), AgentCard("billing")]
DESCRIPTION = 'Delegate a task to the "Policy Bot" agent. Answers policy questions. Skills: Lookup'


def stub_ctx(calls):
    async def call_agent(agent, task_description, *, function_call_id=""):
        calls.append((agent, task_description, function_call_id))
        return PeerResult(agent, "completed", f"answer from {agent}")

    return SimpleNamespace(
        available_agents=lambda: list(CARDS),
        call_agent=call_agent,
        _run=SimpleNamespace(
            tools=SimpleNamespace(note_sdk_tools=lambda names: None, note_sdk_alias=lambda alias, name: None)
        ),
    )


async def test_openai_agents_tools_pass_the_tool_call_id():
    from sam_link_sdk.openai_agents import mesh_agent_tools

    calls = []
    tools = mesh_agent_tools(stub_ctx(calls))
    assert [t.name for t in tools] == ["peer_policy-bot", "peer_billing"]
    assert tools[0].description == DESCRIPTION
    assert tools[0].params_json_schema["required"] == ["task_description"]
    out = await tools[0].on_invoke_tool(SimpleNamespace(tool_call_id="call_3"), '{"task_description": "Check P-1"}')
    assert out == "answer from policy-bot"
    assert calls == [("policy-bot", "Check P-1", "call_3")]
    assert [t.name for t in mesh_agent_tools(stub_ctx([]), ["billing"])] == ["peer_billing"]


async def test_adk_toolset_answers_for_the_running_task():
    from sam_link_sdk.adk import mesh_agent_tools
    from sam_link_sdk.adk.steps import _current

    [toolset] = mesh_agent_tools(agents=["policy-bot"])
    assert await toolset.get_tools() == []

    calls = []
    token = _current.set(stub_ctx(calls))
    try:
        [tool] = await toolset.get_tools()
    finally:
        _current.reset(token)
    declaration = tool._get_declaration()
    assert (declaration.name, declaration.description) == ("peer_policy-bot", DESCRIPTION)
    assert declaration.parameters.required == ["task_description"]
    out = await tool.run_async(
        args={"task_description": "Check P-1"}, tool_context=SimpleNamespace(function_call_id="adk-1")
    )
    assert out == "answer from policy-bot"
    assert calls == [("policy-bot", "Check P-1", "adk-1")]


async def test_crewai_tools_run_on_the_mesh_loop_from_the_crew_thread():
    from sam_link_sdk.crewai import mesh_agent_tools

    calls = []
    tools = mesh_agent_tools(stub_ctx(calls))
    assert [t.name for t in tools] == ["peer_policy-bot", "peer_billing"]
    assert DESCRIPTION in tools[0].description
    assert list(tools[0].args_schema.model_fields) == ["task_description"]
    out = await asyncio.to_thread(tools[0].run, task_description="Check P-1")
    assert out == "answer from policy-bot"
    assert calls == [("policy-bot", "Check P-1", "")]


async def test_a_langchain_peer_tool_behind_an_approval_runs_once_approved_with_its_call_id():
    """The approval shows the user only what the model filled in (not the injected tool-call id), and the approved
    call reaches the peer tool as a whole tool call, so the peer's steps still nest under it."""
    from langchain_core.messages import AIMessage

    from sam_link_sdk.context import TaskContext
    from sam_link_sdk.langchain import mesh_agent_tools, run_agent, with_approval

    from .test_callbacks import ToolCallingFake

    calls, asked = [], []
    ctx = TaskContext(task_id="T", context_id="C", text="Ask billing", agent_name="claims-triage")
    stub = stub_ctx(calls)
    ctx.available_agents, ctx.call_agent = stub.available_agents, stub.call_agent

    async def request_approval(name, args, *, message=""):
        asked.append((name, args))
        return SimpleNamespace(approved=True, outcome="approved")

    ctx.request_approval = request_approval
    tools = with_approval(ctx, mesh_agent_tools(ctx, ["billing"]), names=["peer_billing"])
    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content="",
                    tool_calls=[{"id": "call_p", "name": "peer_billing", "args": {"task_description": "Owed?"}}],
                ),
                AIMessage(content="Nothing is owed."),
            ]
        )
    )
    assert await run_agent(ctx, model, tools) == "Nothing is owed."
    assert asked == [("peer_billing", {"task_description": "Owed?"})]
    assert calls == [("billing", "Owed?", "call_p")]
