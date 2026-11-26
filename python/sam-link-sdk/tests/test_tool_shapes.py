# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""What each adapter's with_scopes and with_approval do with a tool they cannot wrap: a toolset or MCP tools resolved at
run time, a hosted tool, a handoff, a plain function where the framework's tool type is needed. Each is passed on
unchanged when it is not named, and naming one is a ValueError that says why, never a TypeError from inside the
framework."""

from __future__ import annotations

import pytest

from .adapter_task import adapter_task
from .test_scope_guard import SCOPE, Body


def route_claim(claim_id: str) -> str:
    """Route a claim."""
    return "fast-track"


def remote_lookup(policy_id: str) -> str:
    """Look up a policy remotely."""
    return "gold"


def _adk_remote():
    from google.adk.tools import BaseTool
    from google.adk.tools.base_toolset import BaseToolset
    from google.adk.tools.function_tool import FunctionTool

    class Remote(BaseToolset):
        """Stands in for an MCP toolset: its tools are known only when ADK asks for them at run time."""

        async def get_tools(self, readonly_context=None) -> list[BaseTool]:
            return [FunctionTool(remote_lookup)]

        async def close(self) -> None:
            return None

    return Remote()


async def test_adk_with_scopes_passes_another_toolset_on_and_says_why_it_cannot_scope_its_tools():
    pytest.importorskip("google.adk")
    from sam_link_sdk.adk import tool_infos, with_approval, with_scopes

    remote = _adk_remote()
    tools = with_scopes([route_claim, remote], {"route_claim": [SCOPE]})
    assert tools[1] is remote and tool_infos(tools)[0].required_scopes == [SCOPE]
    with pytest.raises(ValueError, match=r"remote_lookup.*resolve only at run time.*ToolInfo"):
        with_scopes([route_claim, remote], {"remote_lookup": [SCOPE]})
    over_approval = with_scopes(with_approval([route_claim, remote], names=["route_claim"]), {"route_claim": [SCOPE]})
    assert over_approval[1] is remote and tool_infos(over_approval)[0].required_scopes == [SCOPE]


async def test_adk_with_approval_leaves_another_toolset_outside_and_refuses_to_cover_it_silently():
    pytest.importorskip("google.adk")
    from sam_link_sdk.adk import with_approval

    remote = _adk_remote()
    approval, passed = with_approval([route_claim, remote], names=["route_claim"])
    assert passed is remote and [t.name for t in await approval.get_tools()] == ["route_claim"]
    with pytest.raises(ValueError, match="toolset and no names"):
        with_approval([route_claim, remote])
    with pytest.raises(ValueError, match=r"remote_lookup.*cannot be covered"):
        with_approval([route_claim, remote], names=["remote_lookup"])


def file_note(text: str) -> str:
    """File a note."""
    return "filed"


@pytest.mark.parametrize("adapter,framework", [("langchain", "langchain_core"), ("crewai", "crewai")])
async def test_a_plain_function_named_where_the_frameworks_tool_is_needed_is_a_clear_error(adapter, framework):
    pytest.importorskip(framework)
    import importlib

    from .test_scope_guard import KITS

    module = importlib.import_module(f"sam_link_sdk.{adapter}")
    lookup = KITS[adapter].make(Body().fn)
    with pytest.raises(ValueError, match=r"file_note.*BaseTool.*@tool"):
        module.with_scopes([lookup, file_note], {"file_note": [SCOPE]})
    async with adapter_task() as task:
        with pytest.raises(ValueError, match=r"file_note.*BaseTool.*@tool"):
            module.with_approval(task.ctx, [file_note])
        passed = module.with_scopes([file_note, lookup], {lookup.name: [SCOPE]})
        assert passed[0] is file_note
        assert module.with_approval(task.ctx, [file_note, lookup], names=[lookup.name])[0] is file_note


async def test_openai_agents_hosted_tools_and_handoffs_pass_on_and_naming_them_says_why():
    pytest.importorskip("agents")
    from agents import Agent, WebSearchTool, function_tool, handoff

    from sam_link_sdk.openai_agents import with_approval, with_scopes

    search, transfer, lookup = WebSearchTool(), handoff(Agent(name="billing")), function_tool(route_claim)
    tools = with_scopes([search, transfer, lookup], {"route_claim": [SCOPE]})
    assert tools[0] is search and tools[1] is transfer
    async with adapter_task() as task:
        for name in ("web_search", "transfer_to_billing"):
            with pytest.raises(ValueError, match=f"not function tools.*{name}"):
                with_scopes([search, transfer, lookup], {name: [SCOPE]})
            with pytest.raises(ValueError, match=f"not function tools.*{name}"):
                with_approval(task.ctx, [search, transfer, lookup], names=[name])


async def test_strands_a_tool_provider_passes_on_and_naming_a_tool_inside_it_says_it_was_not_given():
    pytest.importorskip("strands")
    from strands import tool
    from strands.tools.mcp import MCPClient

    from sam_link_sdk.strands import with_approval, with_scopes

    provider, lookup = MCPClient(lambda: None), tool(route_claim)
    tools = with_scopes([provider, lookup], {"route_claim": [SCOPE]})
    assert tools[0] is provider
    with pytest.raises(ValueError, match="remote_lookup"):
        with_scopes([provider, lookup], {"remote_lookup": [SCOPE]})
    async with adapter_task() as task:
        with pytest.raises(ValueError, match="remote_lookup"):
            with_approval(task.ctx, [provider, lookup], names=["remote_lookup"])


async def test_agent_framework_an_mcp_tool_passes_on_and_naming_it_says_why():
    pytest.importorskip("agent_framework")
    from agent_framework import MCPStdioTool, tool

    from sam_link_sdk.agent_framework import with_approval, with_scopes

    server, lookup = MCPStdioTool(name="files", command="false"), tool(approval_mode="never_require")(route_claim)
    tools = with_scopes([server, lookup], {"route_claim": [SCOPE]})
    assert tools[0] is server
    with pytest.raises(ValueError, match=r"not function tools.*files"):
        with_scopes([server, lookup], {"files": [SCOPE]})
    async with adapter_task() as task:
        with pytest.raises(ValueError, match="remote_lookup"):
            with_approval(task.ctx, [server, lookup], names=["remote_lookup"])
