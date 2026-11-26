# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""CrewAI adapter for sam_link_sdk."""

from .._drift import require
from .._frameworks import adopt

TESTED_VERSION = "1.15.27"

require(
    "sam_link_sdk.crewai",
    "CrewAI",
    "crewai",
    TESTED_VERSION,
    [
        "crewai.events:BaseEventListener",
        "crewai.events:crewai_event_bus.on",
        "crewai.events:crewai_event_bus.flush",
        "crewai.events.base_events:BaseEvent.emission_sequence",
        "crewai.events.base_events:BaseEvent.event_id",
        "crewai.events.base_events:BaseEvent.started_event_id",
        "crewai.events.base_events:BaseEvent.agent_id",
        "crewai.events.types.llm_events:LLMCallStartedEvent.model",
        "crewai.events.types.llm_events:LLMCallCompletedEvent.response",
        "crewai.events.types.llm_events:LLMCallCompletedEvent.call_type",
        "crewai.events.types.llm_events:LLMCallCompletedEvent.usage",
        "crewai.events.types.llm_events:LLMCallFailedEvent",
        "crewai.events.types.llm_events:LLMCallType.TOOL_CALL",
        "crewai.events.types.llm_events:LLMStreamChunkEvent.chunk",
        "crewai.events.types.llm_events:LLMStreamChunkEvent.tool_call",
        "crewai.events.types.llm_events:LLMStreamChunkEvent.call_type",
        "crewai.llms.base_llm:call_stream_override",
        "crewai.events.types.tool_usage_events:ToolUsageStartedEvent.tool_name",
        "crewai.events.types.tool_usage_events:ToolUsageStartedEvent.tool_args",
        "crewai.events.types.tool_usage_events:ToolUsageFinishedEvent.output",
        "crewai.events.types.tool_usage_events:ToolUsageFinishedEvent.failure",
        "crewai.events.types.tool_usage_events:ToolUsageErrorEvent.error",
        "crewai.tools:BaseTool",
        "crewai.hooks:register_before_tool_call_hook",
        "crewai.hooks:register_after_tool_call_hook",
        "crewai.hooks:register_before_llm_call_hook",
        "crewai.hooks:HookAborted",
        "crewai.hooks:ToolCallHookContext",
        "crewai.events.types.tool_usage_events:ToolUsageEvent",
    ],
)
from .peer_tools import mesh_agent_tools  # noqa: E402
from .run import run_agent  # noqa: E402
from .stop import stop_guard  # noqa: E402
from .tools import ask_user_tool, tool_infos, with_approval, with_scopes  # noqa: E402

adopt("CrewAI")

__all__ = [
    "ask_user_tool",
    "mesh_agent_tools",
    "run_agent",
    "stop_guard",
    "tool_infos",
    "with_approval",
    "with_scopes",
]
"""The names every adapter has (CrewAI needs no step_reporter: run_agent listens on CrewAI's process-wide event bus and
registers its process-wide tool hooks).
"""
