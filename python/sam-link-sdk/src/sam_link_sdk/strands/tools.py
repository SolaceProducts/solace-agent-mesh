# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Strands tools and hooks for human-in-the-loop, and the card's tool manifest.

Build them per task, as the agent is built per task here: ``Agent(model=..., tools=[*tools, ask_user_tool(ctx)],
hooks=[with_approval(ctx, tools, names=["route_claim"])])``.
"""

from __future__ import annotations

import copy
from collections.abc import Callable, Iterable
from typing import Any

from strands import tool
from strands.hooks import BeforeToolCallEvent, HookProvider, HookRegistry
from strands.types._events import ToolResultEvent
from strands.types.tools import AgentTool, ToolGenerator, ToolUse

from .._adapter import (
    ASK_USER_DESCRIPTION,
    ASK_USER_TOOL,
    ToolScopeMap,
    answer_text,
    approval_filter,
    approve,
    ask,
    mark_scoped,
    mark_wrapper,
    named_tool_infos,
    scope_map,
    scope_refusal,
    scoped_class,
    scopes_of,
)
from ..config import ToolInfo
from ..context import TaskContext


def tool_infos(tools: Iterable[Any]) -> list[ToolInfo]:
    """A card tool manifest (``AgentConfig(tools=...)``) from the agent's tools (``@tool`` functions or AgentTools),
    each with the scopes ``with_scopes`` gave it."""
    infos: list[ToolInfo] = []
    for t in tools:
        spec = getattr(t, "tool_spec", None) or {}
        infos.extend(
            ToolInfo(i.name, str(spec.get("description") or i.description), i.required_scopes)
            for i in named_tool_infos([t])
        )
    return infos


def _scoped_class(cls: type[AgentTool]) -> type[AgentTool]:
    """`cls` with the scope check in front of ``stream``. A subclass of the tool's own class, so whatever Strands
    looks for on it still holds: an agent run as a tool keeps ``delegate`` (the delegation plugin checks the class),
    ``agent`` and its spec."""

    def methods(cls: type[AgentTool]) -> dict[str, Any]:
        async def stream(self, tool_use: ToolUse, invocation_state: dict[str, Any], **kwargs: Any) -> ToolGenerator:
            refused = scope_refusal(self.tool_name, scopes_of(self))
            if refused:
                yield ToolResultEvent(
                    {"toolUseId": tool_use["toolUseId"], "status": "error", "content": [{"text": refused}]}
                )
                return
            async for event in cls.stream(self, tool_use, invocation_state, **kwargs):
                yield event

        return {"stream": stream}

    return scoped_class(cls, methods, __name__)


def _scoped(tool: AgentTool, scopes: Iterable[str]) -> AgentTool:
    """A copy of `tool` that checks the current mesh task's grants where it runs (see ``with_scopes``)."""
    guarded = copy.copy(tool)
    object.__setattr__(guarded, "__class__", _scoped_class(type(tool)))
    return mark_wrapper(mark_scoped(guarded, scopes), tool)


def with_scopes(tools: Iterable[Any], scopes: ToolScopeMap) -> list[Any]:
    """`tools`, each one `scopes` names (tool name -> the scopes a user needs for it, ``docs/security.md``) replaced by
    a tool that checks them where it runs: called for a mesh task whose user lacks them, for a stopped task, or outside
    any mesh task (an agent no hook sees), it refuses with an error result and the tool's body does not run.
    ``tool_infos`` tells SAM the scopes, and ``run_agent``'s hooks also refuse the call and report the refusal. A name
    that is none of the tools, or one that is not a Strands tool object (``@tool`` functions are), is a ValueError."""
    tools = list(tools)
    required = scope_map(tools, scopes)
    unguardable = sorted(n for n in required if not any(isinstance(t, AgentTool) and t.tool_name == n for t in tools))
    if unguardable:
        raise ValueError(
            f"with_scopes names tools that are not Strands tools (AgentTool), which it cannot guard: "
            f"{', '.join(unguardable)}"
        )
    return [
        _scoped(t, required[t.tool_name]) if isinstance(t, AgentTool) and t.tool_name in required else t for t in tools
    ]


class _ApprovalHooks(HookProvider):
    def __init__(self, ctx: TaskContext, wanted: Callable[[str], bool], message: str):
        self._ctx = ctx
        self._wanted = wanted
        self._message = message

    def register_hooks(self, registry: HookRegistry, **kwargs: Any) -> None:
        registry.add_callback(BeforeToolCallEvent, self._ask)

    async def _ask(self, event: BeforeToolCallEvent) -> None:
        name = event.tool_use["name"]
        if event.cancel_tool or not self._wanted(name):
            return
        refused = await approve(self._ctx, name, event.tool_use.get("input") or {}, self._message)
        if refused:
            event.cancel_tool = refused


def with_approval(
    ctx: TaskContext, tools: Iterable[Any], *, names: Iterable[str] | None = None, message: str = ""
) -> HookProvider:
    """Make chosen tools ask the user for approval before each call: ``Agent(tools=tools, hooks=[with_approval(ctx,
    tools, names=["route_claim"])])``.

    Strands approves in a hook over the agent's calls rather than by wrapping tools, so `tools` only says which tools
    the approval covers, as the tools the other adapters wrap: `names` limits it to those (default: all of `tools`). The
    user sees the tool's name and arguments (credentials redacted) plus `message`. A call that is
    denied, cancelled or times out is not run: the model is told so with an error result, which is also what the
    audit trail records.
    """
    return _ApprovalHooks(ctx, approval_filter(tools, names), message)


def ask_user_tool(ctx: TaskContext) -> Any:
    """A tool the model can call to ask the user a question and wait for the answer."""

    @tool(name=ASK_USER_TOOL, description=ASK_USER_DESCRIPTION)
    async def ask_user_question(question: str, options: list[str] | None = None) -> str:
        """Ask the user a question.

        Args:
            question: The question to ask the user.
            options: Choices to offer. Leave out for a free-text answer.
        """
        return answer_text(await ask(ctx, question, options))

    return ask_user_question
