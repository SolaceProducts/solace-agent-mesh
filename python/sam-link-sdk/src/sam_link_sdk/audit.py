# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Audit records for an SDK agent, shaped like SAM-Go's structured audit log (internal/audit/logger.go).

SAM audits authentication, agent and tool access decisions, and tool executions inside the agents it runs
(per-process structured log lines, not messages on the mesh). An SDK agent has no such runtime, so this module writes
the same records: one structured line per decision, with a per-process strictly increasing ``seq``.

Records hold ids and outcomes only: user, task, agent and tool names, scopes, durations. They never hold prompts,
completions, tool arguments or results, or credentials. By default each record is logged as one JSON line on the
``sam_link_sdk.audit`` logger; pass ``AgentConfig(audit_sink=...)`` to send them elsewhere (a SIEM, a file).
"""

from __future__ import annotations

import itertools
import json
import logging
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from typing import Any

__all__ = ["AuditEvent", "log_sink"]

log = logging.getLogger("sam_link_sdk.audit")

DEBUG, INFO, WARNING = logging.DEBUG, logging.INFO, logging.WARNING
"""SAM-Go's levels: grants are DEBUG, denials WARN, successful tool runs INFO, failures WARN."""


@dataclass(frozen=True)
class AuditEvent:
    message: str
    level: int
    seq: int
    fields: Mapping[str, Any] = field(default_factory=dict)

    def as_dict(self) -> dict[str, Any]:
        return {"msg": self.message, "seq": self.seq, **self.fields}


AuditSink = Callable[[AuditEvent], None]


def log_sink(event: AuditEvent) -> None:
    """The default sink: one JSON line per record on the ``sam_link_sdk.audit`` logger."""
    log.log(event.level, json.dumps(event.as_dict(), sort_keys=True, ensure_ascii=False, default=str))


class AuditLogger:
    def __init__(self, sink: AuditSink | None = None):
        self._sink = sink or log_sink
        self._seq = itertools.count(1)

    def _emit(self, message: str, level: int, **fields: Any) -> None:
        """Send one audit event to the sink, dropping empty fields.

        A failing sink is logged and swallowed: an audit sink failing must not fail the task it describes.
        """
        event = AuditEvent(message, level, next(self._seq), {k: v for k, v in fields.items() if v not in (None, "")})
        try:
            self._sink(event)
        except Exception as exc:
            logging.getLogger("sam_link_sdk.agent").error("audit sink failed err=%s", type(exc).__name__)

    def agent_delegation(self, *, user_id: str, task_id: str, agent_name: str, allowed: bool) -> None:
        """A call this task made to another agent, when it ended with SAM's decision: granted when SAM carried it,
        denied when SAM refused it as not allowed."""
        message = "agent delegation granted" if allowed else "agent delegation denied"
        self._emit(message, DEBUG if allowed else WARNING, userID=user_id, taskID=task_id, agentName=agent_name)

    def agent_control(self, *, agent_name: str, operator: str, stopped: bool, reason: str = "") -> None:
        """SAM stopped or released this agent, applied (operator: the user SAM named, when it names one; reason:
        SAM's reason)."""
        if stopped:
            self._emit("agent stopped by SAM", WARNING, agentName=agent_name, operator=operator, reason=reason or None)
        else:
            self._emit("agent released by SAM", INFO, agentName=agent_name, operator=operator, reason=reason or None)

    def tool_access(self, *, user_id: str, task_id: str, tool: str, allowed: bool, scopes=()) -> None:
        """The decision on whether the user may use a tool (its required scopes), as SAM-Go's LogToolAccess."""
        if allowed:
            self._emit(
                "tool access granted", DEBUG, userID=user_id, taskID=task_id, tool=tool, scopes=list(scopes) or None
            )
        else:
            self._emit(
                "tool access denied",
                WARNING,
                userID=user_id,
                taskID=task_id,
                tool=tool,
                requiredScopes=list(scopes) or None,
            )

    def tool_approval(self, *, user_id: str, task_id: str, agent_name: str, tool: str, outcome: str) -> None:
        """The user's decision on a tool call that needed approval (outcome: approved, denied, cancelled,
        timed_out or unreadable)."""
        if outcome == "approved":
            self._emit("tool approval granted", INFO, userID=user_id, taskID=task_id, agentName=agent_name, tool=tool)
        else:
            self._emit(
                "tool approval refused",
                WARNING,
                userID=user_id,
                taskID=task_id,
                agentName=agent_name,
                tool=tool,
                approvalRefusal=outcome,
            )

    def limit_reached(self, *, user_id: str, task_id: str, agent_name: str, limit: str, value: float) -> None:
        """A task was stopped at a configured limit ("max_llm_calls" or "task_timeout_seconds")."""
        self._emit(
            "task limit reached",
            WARNING,
            userID=user_id,
            taskID=task_id,
            agentName=agent_name,
            limit=limit,
            value=value,
        )

    def tool_execution(
        self,
        *,
        user_id: str,
        task_id: str,
        agent_name: str,
        tool: str,
        session_id: str,
        duration_ms: int,
        success: bool,
        error: str = "",
    ) -> None:
        fields = dict(
            userID=user_id,
            taskID=task_id,
            agentName=agent_name,
            tool=tool,
            sessionID=session_id,
            duration_ms=duration_ms,
        )
        if success:
            self._emit("tool execution succeeded", INFO, **fields)
        else:
            self._emit("tool execution failed", WARNING, error=error or "tool_error", **fields)
