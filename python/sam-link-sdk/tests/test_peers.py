# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Delegation to other mesh agents, as an adapter offers it to the model: the peer tools. (A call itself goes through the
transport: SAM's entrypoint forwards it and its result, see tests/test_link_protocol.py; the core's side, the call and its
audit record, is in tests/test_agent_rig.py and tests/test_audit.py.)"""

from __future__ import annotations

from types import SimpleNamespace

from langchain_core.messages import ToolMessage

from sam_link_sdk import AgentCard, PeerResult
from sam_link_sdk.langchain import mesh_agent_tools


async def test_langchain_peer_tools_pass_the_tool_call_id():
    calls = []

    async def call_agent(agent, task_description, *, function_call_id=""):
        calls.append((agent, task_description, function_call_id))
        return PeerResult(agent, "completed", "Tier 2")

    skills = [f"S{i}" for i in range(10)]
    ctx = SimpleNamespace(
        available_agents=lambda: [
            AgentCard("policy-bot", "Answers policy questions.", "Policy Bot", skills),
            AgentCard("billing"),
        ],
        call_agent=call_agent,
    )
    tools = mesh_agent_tools(ctx)
    assert [t.name for t in tools] == ["peer_policy-bot", "peer_billing"]
    assert tools[0].description == (
        'Delegate a task to the "Policy Bot" agent. Answers policy questions. Skills: S0, S1, S2, S3, S4, S5, S6, S7 and 2 more'
    )
    assert "tool_call_id" not in tools[0].tool_call_schema.model_json_schema()["properties"]
    assert [t.name for t in mesh_agent_tools(ctx, agents=["billing"])] == ["peer_billing"]

    out = await tools[0].ainvoke(
        {"name": "peer_policy-bot", "args": {"task_description": "Check P-1"}, "id": "call_9", "type": "tool_call"}
    )
    assert isinstance(out, ToolMessage) and out.content == "Tier 2" and out.tool_call_id == "call_9"
    assert calls == [("policy-bot", "Check P-1", "call_9")]
