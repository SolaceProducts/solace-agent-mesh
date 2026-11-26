# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Google Agent Development Kit (ADK) adapter for sam_link_sdk."""

from .._drift import require
from .._frameworks import adopt

TESTED_VERSION = "2.14.1"

require(
    "sam_link_sdk.adk",
    "Google ADK",
    "google-adk",
    TESTED_VERSION,
    [
        "google.adk.plugins.base_plugin:BasePlugin.before_run_callback",
        "google.adk.plugins.base_plugin:BasePlugin.after_run_callback",
        "google.adk.plugins.base_plugin:BasePlugin.before_model_callback",
        "google.adk.plugins.base_plugin:BasePlugin.on_event_callback",
        "google.adk.plugins.base_plugin:BasePlugin.before_tool_callback",
        "google.adk.plugins.base_plugin:BasePlugin.after_tool_callback",
        "google.adk.plugins.base_plugin:BasePlugin.on_tool_error_callback",
        "google.adk.runners:Runner.run_async",
        "google.adk.runners:Runner.session_service",
        "google.adk.runners:Runner.plugin_manager",
        "google.adk.models.llm_request:LlmRequest.append_instructions",
        "google.adk.tools:BaseTool",
        "google.adk.tools.base_toolset:BaseToolset",
        "google.adk.tools.function_tool:FunctionTool",
        "google.adk.tools.agent_tool:AgentTool",
        "google.adk.tools.tool_context:ToolContext.function_call_id",
        "google.adk.tools.base_tool:BaseTool.check_require_confirmation",
        "google.adk.utils._function_call_names:REQUEST_CONFIRMATION_FUNCTION_CALL_NAME",
        "google.adk.agents.readonly_context:ReadonlyContext",
        "google.genai.types:Content",
        "google.genai.types:Part",
        "google.adk.models.llm_response:LlmResponse.usage_metadata",
        "google.adk.models.llm_response:LlmResponse.cache_creation_token_count",
        "google.genai.types:GenerateContentResponseUsageMetadata.prompt_token_count",
        "google.genai.types:GenerateContentResponseUsageMetadata.tool_use_prompt_token_count",
        "google.genai.types:GenerateContentResponseUsageMetadata.candidates_token_count",
        "google.genai.types:GenerateContentResponseUsageMetadata.thoughts_token_count",
        "google.genai.types:GenerateContentResponseUsageMetadata.cached_content_token_count",
        "google.genai.types:GenerateContentResponseUsageMetadata.total_token_count",
    ],
)

from .peer_tools import mesh_agent_tools  # noqa: E402
from .run import run_agent  # noqa: E402
from .steps import SamLinkPlugin, step_reporter  # noqa: E402
from .stop import StopPlugin, stop_guard  # noqa: E402
from .tools import ask_user_tool, tool_infos, with_approval, with_scopes  # noqa: E402

adopt("Google ADK")

__all__ = [
    "SamLinkPlugin",
    "StopPlugin",
    "ask_user_tool",
    "mesh_agent_tools",
    "run_agent",
    "step_reporter",
    "stop_guard",
    "tool_infos",
    "with_approval",
    "with_scopes",
]
