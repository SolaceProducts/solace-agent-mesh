# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Join a Solace Agent Mesh as a first-class agent.

The agent core (``MeshAgent``, ``TaskContext``) reaches SAM through ``sam_link_sdk.link.LinkTransport``, the agent link to
SAM's entrypoint (SAM's broker sits behind it).
"""

from __future__ import annotations

from typing import Any

from ._adapter import NestedRunRefused, SessionInUse, ToolAccessDenied
from ._drift import FrameworkDrift
from .agent import MeshAgent
from .config import AgentConfig, DataAccess, McpServer, Skill, Suggestion, ToolInfo
from .context import File, InputFile, TaskContext, TaskHalted, TaskResult, bind_task, current_task
from .delegation import AgentCard, PeerResult
from .memory import ConversationMemory
from .prompting import ApprovalResult, Option, PromptTooLarge, Question, UserAnswer
from .request_context import RequestContext
from .stop import AgentStopped
from .usage import Usage

__all__ = [
    "AgentCard",
    "AgentConfig",
    "AgentStopped",
    "ApprovalResult",
    "ConversationMemory",
    "DataAccess",
    "File",
    "FrameworkDrift",
    "InputFile",
    "LinkTransport",
    "McpServer",
    "MeshAgent",
    "NestedRunRefused",
    "Option",
    "PeerResult",
    "PromptTooLarge",
    "Question",
    "RequestContext",
    "SessionInUse",
    "Skill",
    "Suggestion",
    "TaskContext",
    "TaskHalted",
    "TaskResult",
    "ToolAccessDenied",
    "ToolInfo",
    "Usage",
    "UserAnswer",
    "bind_task",
    "current_task",
]

_LAZY = {
    "LinkTransport": "sam_link_sdk.link",
}


def __getattr__(name: str) -> Any:
    module = _LAZY.get(name)
    if module is None:
        raise AttributeError(f"module 'sam_link_sdk' has no attribute {name!r}")
    import importlib

    return getattr(importlib.import_module(module), name)
