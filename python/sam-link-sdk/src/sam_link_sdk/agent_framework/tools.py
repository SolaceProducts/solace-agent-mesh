# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Agent Framework middleware and tools for human-in-the-loop, and the card's tool manifest.

Build them per task and give them to the run: ``run_agent(ctx, agent, extra_tools=[ask_user_tool(ctx)],
middleware=[with_approval(ctx, TOOLS, names=["route_claim"])])``. SAM's approval asks the user and waits for the answer
inside the run. A tool the framework holds for approval itself (``approval_mode="always_require"``) needs none of
this: ``run_agent`` asks the user through SAM for the request the framework hands back, and resumes the run.
"""

from __future__ import annotations

import copy
import json
from collections.abc import Callable, Iterable
from typing import Any

from agent_framework import FunctionInvocationContext, FunctionMiddleware, FunctionTool, tool

from .._adapter import (
    ASK_USER_DESCRIPTION,
    ASK_USER_TOOL,
    ToolScopeMap,
    answer_text,
    approval_filter,
    approve,
    ask,
    error_result,
    mark_scoped,
    mark_wrapper,
    named_tool_infos,
    scope_map,
    scope_refusal,
    scoped_class,
    scoped_variant,
    scopes_of,
)
from ..config import ToolInfo
from ..context import TaskContext
from .steps import REFUSED


def tool_infos(tools: Iterable[Any]) -> list[ToolInfo]:
    """A card tool manifest (``AgentConfig(tools=...)``) from the agent's tools (``@tool`` functions), each with the
    scopes ``with_scopes`` gave it."""
    return named_tool_infos(tools)


def _scoped_class(cls: type[FunctionTool]) -> type[FunctionTool]:
    """`cls` with the scope check in front of the call. It sits before FunctionTool's own ``__call__``, which counts
    the call against ``max_invocations`` before it runs the function: a refused call must not spend the tool's limit.
    Agent Framework runs a sync tool's ``__call__`` on a worker thread that carries the context."""

    def methods(cls: type[FunctionTool]) -> dict[str, Any]:
        def __call__(self, *args: Any, **kwargs: Any) -> Any:
            refused = scope_refusal(self.name, scopes_of(self))
            if refused:
                return json.dumps(error_result(refused))
            return cls.__call__(self, *args, **kwargs)

        return {"__call__": __call__}

    return scoped_class(cls, methods, __name__)


def _scoped(tool: FunctionTool, scopes: tuple[str, ...]) -> FunctionTool:
    """A copy of `tool` that checks the current mesh task's grants before each call (see ``with_scopes``)."""
    guarded = copy.copy(tool)
    object.__setattr__(guarded, "__class__", _scoped_class(type(tool)))
    return mark_wrapper(mark_scoped(guarded, scopes), tool)


def with_scopes(tools: Iterable[Any], scopes: ToolScopeMap) -> list[Any]:
    """`tools`, each one `scopes` names (tool name -> the scopes a user needs for it, ``docs/security.md``) replaced by
    a copy that checks them where it runs: called for a mesh task whose user lacks them, for a stopped task, or outside
    any mesh task (an agent no middleware sees), it refuses with an error result and the tool's body does not run.
    ``tool_infos`` tells SAM the scopes, and ``run_agent``'s middleware also hides the tool from such a user and
    reports the refusal. A name that is none of the tools, or one that is not a function tool with a function (a
    ``@tool``), is a ValueError."""
    tools = list(tools)
    required = scope_map(tools, scopes)
    unguardable = sorted(
        n
        for n in required
        if not any(isinstance(t, FunctionTool) and t.name == n and t.func is not None for t in tools)
    )
    if unguardable:
        raise ValueError(
            f"with_scopes names tools that are not function tools, which it cannot guard: {', '.join(unguardable)}"
        )
    return [
        _scoped(t, tuple(required[t.name])) if isinstance(t, FunctionTool) and t.name in required else t for t in tools
    ]


def _spent_limit(function: Any) -> str | None:
    """Agent Framework's own words for a function tool that has certainly spent a limit of its own
    (``max_invocations``, ``max_invocation_exceptions``), read without counting a call: only for its own FunctionTool
    (or with_scopes' copy of one), whose call enforces them; another class may run. The approval answers it without
    asking and without calling the tool."""
    stock = type(function) is FunctionTool or type(function) is scoped_variant(FunctionTool)
    if not stock:
        return None
    if function.max_invocations is not None and function.invocation_count >= function.max_invocations:
        return f"Function '{function.name}' has reached its maximum invocation limit, you can no longer use this tool."
    if (
        function.max_invocation_exceptions is not None
        and function.invocation_exception_count >= function.max_invocation_exceptions
    ):
        return (
            f"Function '{function.name}' has reached its maximum exception limit, you tried to use this tool too "
            "many times and it kept failing."
        )
    return None


class _Approval(FunctionMiddleware):
    def __init__(self, ctx: TaskContext, wanted: Callable[[str], bool], message: str):
        self._ctx = ctx
        self._wanted = wanted
        self._message = message

    async def process(self, context: FunctionInvocationContext, call_next: Callable[[], Any]) -> None:
        name = context.function.name
        if not self._wanted(name):
            await call_next()
            return
        refused = _spent_limit(context.function) or await approve(
            self._ctx, name, dict(context.arguments or {}), self._message
        )
        if refused:
            context.result = refused
            context.metadata[REFUSED] = True
            return
        await call_next()


class _Held(FunctionMiddleware):
    """Applies the user's answer to a call the framework held for approval (``approval_mode="always_require"``). The
    framework turns a rejection into its own fixed text, so every answer is sent to it as an approval and decided here:
    a refused call is not run and the model is told why, as with ``with_approval``. A call with no recorded answer is
    refused."""

    def __init__(self, answers: dict[str, str | None]):
        self._answers = answers

    async def process(self, context: FunctionInvocationContext, call_next: Callable[[], Any]) -> None:
        if getattr(context.function, "approval_mode", None) != "always_require":
            await call_next()
            return
        name = context.function.name
        refused = self._answers.get(str(context.metadata.get("call_id")), NO_ANSWER.format(name=name))
        if refused:
            context.result = refused
            context.metadata[REFUSED] = True
            return
        await call_next()


NO_ANSWER = "The user did not approve this call to {name} (no answer was recorded), so it was not run."


def with_approval(
    ctx: TaskContext, tools: Iterable[Any], *, names: Iterable[str] | None = None, message: str = ""
) -> FunctionMiddleware:
    """Make chosen tools ask the user for approval before each call: ``run_agent(..., middleware=[with_approval(ctx,
    tools, names=["route_claim"])])``.

    Agent Framework approves in middleware over the run's calls rather than by wrapping tools, so `tools` only says
    which tools the approval covers, as the tools the other adapters wrap: `names` limits it to those (default: all of
    `tools`). The user sees the tool's name and arguments (credentials redacted) plus `message`. A call that is denied,
    cancelled or times out is not run: the model is told so with an error result, which is also what the audit trail
    records.
    """
    return _Approval(ctx, approval_filter(tools, names), message)


def ask_user_tool(ctx: TaskContext) -> Any:
    """A tool the model can call to ask the user a question and wait for the answer."""

    @tool(name=ASK_USER_TOOL, description=ASK_USER_DESCRIPTION, approval_mode="never_require")
    async def ask_user_question(question: str, options: list[str] | None = None) -> str:
        """Ask the user a question.

        Args:
            question: The question to ask the user.
            options: Choices to offer. Leave out for a free-text answer.
        """
        return answer_text(await ask(ctx, question, options))

    return ask_user_question
