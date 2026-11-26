# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Microsoft Agent Framework adapter for sam_link_sdk."""

from .._drift import require
from .._frameworks import adopt

TESTED_VERSION = "1.22.0"

require(
    "sam_link_sdk.agent_framework",
    "Microsoft Agent Framework",
    "agent-framework-core",
    TESTED_VERSION,
    [
        "agent_framework:Agent.run",
        "agent_framework:ChatMiddleware",
        "agent_framework:ChatContext",
        "agent_framework:ChatResponse",
        "agent_framework:FunctionMiddleware",
        "agent_framework:FunctionInvocationContext",
        "agent_framework:AgentMiddleware",
        "agent_framework:MiddlewareFailure",
        "agent_framework:Message",
        "agent_framework:tool",
        "agent_framework:UsageDetails.input_token_count",
        "agent_framework:UsageDetails.output_token_count",
        "agent_framework:UsageDetails.cache_read_input_token_count",
        "agent_framework:UsageDetails.cache_creation_input_token_count",
        "agent_framework:UsageDetails.reasoning_output_token_count",
        "agent_framework:UsageDetails.total_token_count",
    ],
)
from .peer_tools import mesh_agent_tools  # noqa: E402
from .run import run_agent  # noqa: E402
from .steps import step_reporter  # noqa: E402
from .stop import stop_guard  # noqa: E402
from .tools import ask_user_tool, tool_infos, with_approval, with_scopes  # noqa: E402

adopt("Microsoft Agent Framework")

__all__ = [
    "ask_user_tool",
    "mesh_agent_tools",
    "run_agent",
    "step_reporter",
    "stop_guard",
    "tool_infos",
    "with_approval",
    "with_scopes",
]
