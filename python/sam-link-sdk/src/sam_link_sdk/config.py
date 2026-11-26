# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Agent configuration."""

from __future__ import annotations

import re
from collections.abc import Callable
from dataclasses import KW_ONLY, dataclass, field
from typing import Any

_NAME_RE = re.compile(r"[A-Za-z0-9_-]+")


@dataclass(frozen=True)
class Skill:
    id: str
    name: str
    description: str


@dataclass(frozen=True)
class ToolInfo:
    """A tool the agent has, described to SAM (on the agent link) or listed on its card (the sam/tools extension) so
    the web UI and evaluations can show it. The list is the agent's own declaration; the mesh does not check it
    against what the agent really runs. `required_scopes` are enforced per task as SAM-Go agents enforce a tool's
    scopes: a user without them does not get the tool (``TaskContext.check_tool``, applied by every adapter's
    ``run_agent``). SAM checks the user against them and sends, with each task, the ones the user holds
    (``granted_tool_scopes``); a task that grants none gets no scoped tool, and once a tool here has scopes, a tool the run
    offers under a name not listed here is refused too. Each adapter's ``tool_infos(tools)`` builds the list with the
    scopes ``with_scopes`` gave the tools (Hermes: ``tool_infos(tools, scopes=...)``); scopes written here by hand are
    enforced by the adapters' hooks only, not by the tool itself."""

    name: str
    description: str = ""
    required_scopes: list[str] = field(default_factory=list)


@dataclass(frozen=True)
class Suggestion:
    """A starter prompt the web UI offers on the agent's welcome screen."""

    label: str
    prompt: str
    auto_send: bool = False


@dataclass(frozen=True)
class DataAccess:
    """A system the agent reads or writes, listed on its card (sam/provenance) so the console shows what it can reach.
    It is the owner's declaration; the mesh does not check it against what the agent really does."""

    system: str
    classification: str = ""
    access: str = "read"


@dataclass(frozen=True)
class McpServer:
    """An MCP server the agent uses, listed on its card (sam/provenance) so the inventory shows which agents depend on
    which server. The owner's declaration, like DataAccess."""

    name: str
    url: str = ""


@dataclass(frozen=True)
class AgentConfig:
    name: str
    description: str
    _: KW_ONLY
    display_name: str = ""
    version: str = "1.0.0"
    skills: list[Skill] = field(default_factory=list)
    tools: list[ToolInfo] = field(default_factory=list)
    input_schema: dict[str, Any] | None = None
    output_schema: dict[str, Any] | None = None
    welcome_message: str = ""
    suggestions: list[Suggestion] = field(default_factory=list)
    peers: list[str] = field(default_factory=list)
    owner: str = ""
    team: str = ""
    framework: str = ""
    data_access: list[DataAccess] = field(default_factory=list)
    mcp_servers: list[McpServer] = field(default_factory=list)
    streaming: bool = True
    """Only what the card advertises: the agent streams whenever the task's caller takes a stream."""
    max_call_depth: int = 3
    """A task whose call depth is over max_call_depth is refused, as SAM-Go agents refuse it; 0 disables the check."""
    keepalive_seconds: float = 120.0
    """Emit an agent_progress_update when a task has been silent this long, so the gateway's 300s idle timer never fires on
    a slow task. 0 disables it.
    """
    spend_model_keys: dict[str, str] = field(default_factory=dict)
    """Model name -> the SAM model configuration (alias or id) whose prices cost it. A model with no entry is filed with
    its tokens and no cost.
    """
    max_concurrent_tasks: int = 0
    max_inline_file_bytes: int = 7_000_000
    """Budget for the file bytes in one answer. SAM's broker, behind the entrypoint, rejects messages over its max-msg-size
    (10,000,000 bytes by default), so files beyond the budget are left out and the answer says so.
    """
    required_scopes: list[str] = field(default_factory=list)
    """Scopes a caller must hold to invoke the agent, described to SAM, which checks each user against them before it
    delivers a task.
    """
    shutdown_grace_seconds: float = 20.0
    """On shutdown, how long running tasks may finish before the rest are abandoned unacked (redelivered to another
    replica), and how long an answer held for SAM to deliver its task again waits for it before it is sent as it is,
    so a stop can take the whole grace with no task running. SAM-Go agents wait up to 20 s.
    """
    stream_thinking: bool = False
    """Stream the model's reasoning text (thinking_content) to the chat. Off by default: it is model-generated content,
    shown in the UI and kept in the task event log.
    """
    max_llm_calls: int = 20
    """Per-task limits. A task that makes more than max_llm_calls model calls, or runs longer than task_timeout_seconds, is
    stopped and answered with a failed Task that says why. SAM-Go agents stop at 20 model calls per task. 0 turns a
    limit off. (A CrewAI crew runs in a worker thread that cannot be interrupted: the task is answered at the limit, and
    the crew stops at its next model call.) The timeout defaults to 600 s, SAM-Go's sub-task and workflow-request
    timeout: the keepalive above stops the gateway's idle timer, so without it a hung tool would hold the task open for
    good. Time spent waiting for a person (an approval, a question) doesn't count: the prompt's own timeout bounds it.
    """
    task_timeout_seconds: float = 600.0
    audit_sink: Callable[[Any], None] | None = None
    metrics: Any = None
    offline_grace_seconds: float = 900.0
    """SAM's stop (sam_link_sdk.stop): SAM can stop this agent from its console. A stop is saved under state_dir (default
    ~/.sam_link_sdk) so it survives a restart, and a link down longer than offline_grace_seconds counts as stopped until it
    is back (fail closed). exit_on_stop also ends run_forever, so a process that only serves the mesh exits.
    """
    state_dir: str = ""
    exit_on_stop: bool = False

    def __post_init__(self) -> None:
        if not _NAME_RE.fullmatch(self.name):
            raise ValueError("agent name must match [A-Za-z0-9_-]+ (SAM names agents so)")
