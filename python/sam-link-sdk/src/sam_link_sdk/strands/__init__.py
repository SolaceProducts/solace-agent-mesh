# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Strands Agents adapter for sam_link_sdk."""

from .._drift import require
from .._frameworks import adopt

TESTED_VERSION = "1.61.1"

require(
    "sam_link_sdk.strands",
    "Strands Agents",
    "strands-agents",
    TESTED_VERSION,
    [
        "strands:Agent.invoke_async",
        "strands:Agent.cancel",
        "strands:Agent.tool_names",
        "strands:Agent.hooks",
        "strands:Agent.messages",
        "strands:Agent.system_prompt",
        "strands:tool",
        "strands.hooks:HookProvider",
        "strands.hooks:HookRegistry.add_callback",
        "strands.hooks:BeforeInvocationEvent",
        "strands.hooks:AfterInvocationEvent",
        "strands.hooks:BeforeModelCallEvent",
        "strands.hooks:AfterModelCallEvent.stop_response",
        "strands.hooks:AfterModelCallEvent.exception",
        "strands.hooks:BeforeToolCallEvent.tool_use",
        "strands.hooks:BeforeToolCallEvent.cancel_tool",
        "strands.hooks:AfterToolCallEvent.tool_use",
        "strands.hooks:AfterToolCallEvent.result",
        "strands.hooks:MessageAddedEvent.message",
        "strands.types._events:ToolResultEvent",
        "strands.types.tools:AgentTool.stream",
        "strands.agent._agent_as_tool:_AgentAsTool.agent",
        "strands.types.event_loop:Usage.inputTokens",
        "strands.types.event_loop:Usage.outputTokens",
        "strands.types.event_loop:Usage.cacheReadInputTokens",
        "strands.types.event_loop:Usage.cacheWriteInputTokens",
        "strands.types.event_loop:Usage.totalTokens",
    ],
)

from .peer_tools import mesh_agent_tools  # noqa: E402
from .run import run_agent  # noqa: E402
from .steps import SamLinkHooks, step_reporter  # noqa: E402
from .stop import StopHooks, stop_guard  # noqa: E402
from .tools import ask_user_tool, tool_infos, with_approval, with_scopes  # noqa: E402

adopt("Strands Agents")

__all__ = [
    "SamLinkHooks",
    "StopHooks",
    "ask_user_tool",
    "mesh_agent_tools",
    "run_agent",
    "step_reporter",
    "stop_guard",
    "tool_infos",
    "with_approval",
    "with_scopes",
]
