# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""LangChain adapter for sam_link_sdk."""

import importlib.util

from .._drift import require
from .._frameworks import adopt

TESTED_VERSION = "1.6.9"
LANGGRAPH_TESTED_VERSION = "1.2.14"

require(
    "sam_link_sdk.langchain",
    "LangChain",
    "langchain-core",
    TESTED_VERSION,
    [
        "langchain_core.callbacks:AsyncCallbackHandler.on_chat_model_start",
        "langchain_core.callbacks:AsyncCallbackHandler.on_llm_start",
        "langchain_core.callbacks:AsyncCallbackHandler.on_llm_end",
        "langchain_core.callbacks:AsyncCallbackHandler.on_llm_error",
        "langchain_core.callbacks:AsyncCallbackHandler.on_tool_start",
        "langchain_core.callbacks:AsyncCallbackHandler.on_tool_end",
        "langchain_core.callbacks:AsyncCallbackHandler.on_tool_error",
        "langchain_core.callbacks:BaseCallbackHandler",
        "langchain_core.messages:AIMessage.tool_calls",
        "langchain_core.messages:AIMessage.usage_metadata",
        "langchain_core.messages.ai:UsageMetadata.input_tokens",
        "langchain_core.messages.ai:UsageMetadata.output_tokens",
        "langchain_core.messages.ai:UsageMetadata.total_tokens",
        "langchain_core.messages.ai:UsageMetadata.input_token_details",
        "langchain_core.messages.ai:UsageMetadata.output_token_details",
        "langchain_core.messages.ai:InputTokenDetails.cache_read",
        "langchain_core.messages.ai:InputTokenDetails.cache_creation",
        "langchain_core.messages.ai:OutputTokenDetails.reasoning",
        "langchain_core.messages:ToolMessage.status",
        "langchain_core.outputs:ChatGeneration",
        "langchain_core.outputs:LLMResult",
        "langchain_core.tools:BaseTool",
        "langchain_core.tools:BaseTool._to_args_and_kwargs",
        "langchain_core.tools:StructuredTool",
        "langchain_core.tools:ToolException",
        "langchain_core.tools:InjectedToolCallId",
        "langchain_core.language_models:BaseChatModel.bind_tools",
    ],
)

if importlib.util.find_spec("langgraph") is not None:
    require(
        "sam_link_sdk.langchain",
        "LangGraph",
        "langgraph",
        LANGGRAPH_TESTED_VERSION,
        ["langgraph.errors:GraphInterrupt"],
    )

from .peer_tools import mesh_agent_tools  # noqa: E402
from .run import run_agent, run_graph  # noqa: E402
from .steps import SamLinkCallbackHandler, step_reporter  # noqa: E402
from .stop import StopGuard, stop_guard  # noqa: E402
from .tools import ask_user_tool, tool_infos, with_approval, with_scopes  # noqa: E402

adopt("LangChain")

__all__ = [
    "SamLinkCallbackHandler",
    "StopGuard",
    "ask_user_tool",
    "mesh_agent_tools",
    "run_agent",
    "run_graph",
    "step_reporter",
    "stop_guard",
    "tool_infos",
    "with_approval",
    "with_scopes",
]
