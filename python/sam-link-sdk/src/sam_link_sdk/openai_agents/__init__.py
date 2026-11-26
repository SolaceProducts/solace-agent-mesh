# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""OpenAI Agents SDK adapter for sam_link_sdk."""

from .._drift import require
from .._frameworks import adopt

TESTED_VERSION = "0.20.0"

require(
    "sam_link_sdk.openai_agents",
    "openai-agents",
    "openai-agents",
    TESTED_VERSION,
    [
        "agents:Agent.clone",
        "agents:Agent.tools",
        "agents:Agent.handoffs",
        "agents:Agent.instructions",
        "agents:Agent.output_guardrails",
        "agents:Agent.as_tool",
        "agents:AgentUpdatedStreamEvent.new_agent",
        "agents:RunConfig.output_guardrails",
        "agents.lifecycle:RunHooksBase",
        "agents:Handoff.on_invoke_handoff",
        "agents:RunConfig",
        "agents:RunConfig.tool_error_formatter",
        "agents:RunConfig.tool_not_found_behavior",
        "agents:Runner.run",
        "agents:RunHooks.on_agent_start",
        "agents:RunHooks.on_llm_start",
        "agents:RunHooks.on_llm_end",
        "agents:RunHooks.on_tool_start",
        "agents:RunHooks.on_tool_end",
        "agents:RunHooks.on_handoff",
        "agents:FunctionTool.on_invoke_tool",
        "agents:FunctionTool.timeout_seconds",
        "agents:FunctionTool.timeout_behavior",
        "agents:FunctionTool.timeout_error_function",
        "agents:FunctionTool.output_json_schema",
        "agents:FunctionTool._agent_instance",
        "agents:FunctionTool._is_agent_tool",
        "agents.tool:_FailureHandlingFunctionToolInvoker",
        "agents.tool:_FailureHandlingFunctionToolInvoker.__agents_bind_function_tool__",
        "agents.tool:resolve_function_tool_failure_error_function",
        "agents.tool:set_function_tool_failure_error_function",
        "agents.tool:default_tool_timeout_error_message",
        "agents.tool_context:ToolContext.tool_call_id",
        "agents.tool_context:ToolContext.tool_arguments",
        "agents.items:ModelResponse.usage",
        "agents.usage:Usage.input_tokens",
        "agents.usage:Usage.output_tokens",
        "agents.usage:Usage.total_tokens",
        "agents.usage:Usage.input_tokens_details",
        "agents.usage:Usage.output_tokens_details",
        "openai.types.responses.response_usage:InputTokensDetails.cached_tokens",
        "openai.types.responses.response_usage:OutputTokensDetails.reasoning_tokens",
    ],
)

from .peer_tools import mesh_agent_tools  # noqa: E402
from .run import run_agent  # noqa: E402
from .steps import SamLinkRunHooks, step_reporter  # noqa: E402
from .stop import StopHooks, stop_guard  # noqa: E402
from .tools import ask_user_tool, tool_infos, with_approval, with_scopes  # noqa: E402

adopt("OpenAI Agents SDK")

__all__ = [
    "SamLinkRunHooks",
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
