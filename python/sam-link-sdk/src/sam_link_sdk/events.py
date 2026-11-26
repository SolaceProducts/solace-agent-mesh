# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The step events a handler reports with ``TaskContext.emit_event``: a model call started, finished or failed, a tool
call started or finished, progress, streamed text and thinking. They mirror the agent link protocol's ``event`` frames
(``docs/link-protocol.md``); the agent link turns each into its frame."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Any

from .usage import Usage

__all__ = ["ModelFailed", "ModelFinished", "ModelStarted", "Progress", "ToolCall", "ToolFinished", "ToolStarted"]


@dataclass(frozen=True)
class ToolCall:
    id: str
    name: str
    args: Mapping[str, Any] = field(default_factory=dict)


@dataclass(frozen=True)
class ModelStarted:
    model: str
    kind = "model_started"


@dataclass(frozen=True)
class ModelFinished:
    """A model call answered. ``usage`` is the call's tokens: emitting the step records them for the task
    (``TaskContext.record_usage``), so SAM files them with this call."""

    model: str
    text: str = ""
    tool_calls: tuple[ToolCall, ...] = ()
    usage: Usage | None = None
    kind = "model_finished"


@dataclass(frozen=True)
class ModelFailed:
    model: str
    category: str = "unknown"
    provider: str = ""
    kind = "model_failed"


@dataclass(frozen=True)
class ToolStarted:
    call_id: str
    name: str
    args: Mapping[str, Any] | None = None
    kind = "tool_started"


@dataclass(frozen=True)
class ToolFinished:
    call_id: str
    name: str
    result: Any = None
    failed: bool = False
    kind = "tool_finished"


@dataclass(frozen=True)
class Progress:
    text: str
    kind = "progress"


@dataclass(frozen=True)
class Text:
    text: str
    kind = "text"


@dataclass(frozen=True)
class Thinking:
    text: str
    done: bool = False
    kind = "thinking"


StepEvent = ModelStarted | ModelFinished | ModelFailed | ToolStarted | ToolFinished | Progress | Text | Thinking
