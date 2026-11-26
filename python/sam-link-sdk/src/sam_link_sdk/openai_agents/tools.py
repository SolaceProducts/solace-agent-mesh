# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""OpenAI Agents SDK tools for human-in-the-loop and the card's tool manifest.

Build them per task, as the agent is built per task here: ``agent.clone(tools=[*with_approval(ctx, agent.tools,
names=["route_claim"]), ask_user_tool(ctx)])``.
"""

from __future__ import annotations

import dataclasses
import json
from collections.abc import Awaitable, Callable, Iterable
from typing import Any

from agents import FunctionTool
from agents.tool import _FailureHandlingFunctionToolInvoker
from agents.tool_context import ToolContext

from .._adapter import (
    ASK_USER_DESCRIPTION,
    ASK_USER_PARAMETERS,
    ASK_USER_TOOL,
    ToolScopeMap,
    answer_text,
    approval_filter,
    approval_refusal,
    approve,
    ask,
    error_result,
    mark_scoped,
    mark_wrapper,
    named_tool_infos,
    parse_args,
    scope_map,
    scope_refusal,
    scopes_of,
)
from .._drift import require_members
from ..config import ToolInfo
from ..context import TaskContext, tool_name

REWRAP_ATTR = "_sam_link_rewrap"
"""On a tool `_checked` made: the same check put in front of another tool, so the step reporter can put it back in front of
the copy of an agent tool it runs on the task.
"""


def tool_infos(tools: Iterable[Any]) -> list[ToolInfo]:
    """A card tool manifest (``AgentConfig(tools=...)``) from the agent's tools, each with the scopes ``with_scopes``
    gave it."""
    return named_tool_infos(tools)


def with_scopes(tools: Iterable[Any], scopes: ToolScopeMap) -> list[Any]:
    """`tools`, each function tool `scopes` names (tool name -> the scopes a user needs for it,
    ``docs/security.md``) replaced by a copy that checks them where it runs: called for a mesh task whose user lacks
    them, for a stopped task, or outside any mesh task (a run no hook sees, a thread that lost the context), it
    refuses with an error result and its body does not run. ``tool_infos`` tells SAM the scopes, and ``run_agent``
    also hides the tool from such a user and reports the refusal. A name that is none of the tools, or a tool that is
    not a function tool (a hosted tool runs where it cannot be guarded), is a ValueError."""
    tools = list(tools)
    required = scope_map(tools, scopes)
    unguardable = sorted(n for n in required if not any(isinstance(t, FunctionTool) and t.name == n for t in tools))
    if unguardable:
        raise ValueError(
            f"with_scopes names tools that are not function tools, which it cannot guard: {', '.join(unguardable)}"
        )

    def guarded(tool: FunctionTool) -> FunctionTool:
        async def refusal(tool_ctx: ToolContext[Any], raw_args: str) -> str | None:
            return scope_refusal(tool.name, required[tool.name])

        return mark_scoped(_checked(tool, refusal), required[tool.name])

    return [guarded(t) if isinstance(t, FunctionTool) and t.name in required else t for t in tools]


def _checked(tool: FunctionTool, refusal: Callable[[ToolContext[Any], str], Awaitable[str | None]]) -> FunctionTool:
    """A copy of `tool` that runs `refusal` before each call and, when it gives a reason, answers that as an error
    result without running the tool. A function tool's invoker catches the tool's exception and formats it with the
    failure formatter of the tool it is bound to; the copy runs the tool's own implementation under an invoker of
    the same kind, so a failure is formatted, and seen (by the step reporter's copy of the tool), as the copy's."""
    inner: Any = tool.on_invoke_tool
    rebinds = isinstance(inner, _FailureHandlingFunctionToolInvoker)
    if rebinds:
        from . import TESTED_VERSION

        require_members(
            "sam_link_sdk.openai_agents",
            "openai-agents",
            "openai-agents",
            TESTED_VERSION,
            inner,
            "FunctionTool.on_invoke_tool",
            ("_invoke_tool_impl", "_on_handled_error"),
        )
        run = inner._invoke_tool_impl
    else:
        run = inner

    async def invoke(tool_ctx: ToolContext[Any], raw_args: str) -> Any:
        refused = await refusal(tool_ctx, raw_args)
        if refused:
            return json.dumps(error_result(refused))
        return await run(tool_ctx, raw_args)

    checked = dataclasses.replace(
        tool, on_invoke_tool=type(inner)(invoke, inner._on_handled_error) if rebinds else invoke
    )
    object.__setattr__(checked, REWRAP_ATTR, lambda other: _checked(other, refusal))
    return mark_wrapper(mark_scoped(checked, scopes_of(tool)), tool)


def with_approval(
    ctx: TaskContext, tools: Iterable[Any], *, names: Iterable[str] | None = None, message: str = ""
) -> list[Any]:
    """Make chosen function tools ask the user for approval before each call.

    `names` limits it to those tools (default: every function tool); a name that is none of `tools`, or names a tool
    that is not a function tool (a hosted tool runs where it cannot be guarded), is a ValueError.
    The user sees the tool's name and arguments (credentials redacted) plus `message`. A call that is denied,
    cancelled or times out is not run: the model is told so with an error result, which is also what the audit trail
    records. Tools that are not function tools (hosted tools) are returned unchanged.
    """
    tools = list(tools)
    wanted = approval_filter(tools, names)
    if names is not None:
        unguardable = sorted(tool_name(t) for t in tools if wanted(tool_name(t)) and not isinstance(t, FunctionTool))
        if unguardable:
            raise ValueError(
                f"with_approval names tools that are not function tools, which it cannot guard: "
                f"{', '.join(unguardable)}"
            )
    return [_guard(ctx, t, message) if isinstance(t, FunctionTool) and wanted(t.name) else t for t in tools]


def _guard(ctx: TaskContext, tool: FunctionTool, message: str) -> FunctionTool:
    async def refusal(tool_ctx: ToolContext[Any], raw_args: str) -> str | None:
        return approval_refusal(ctx, tool.name, scopes_of(tool)) or await approve(
            ctx, tool.name, parse_args(raw_args), message
        )

    return _checked(tool, refusal)


def ask_user_tool(ctx: TaskContext) -> FunctionTool:
    """A tool the model can call to ask the user a question and wait for the answer."""

    async def invoke(tool_ctx: ToolContext[Any], raw_args: str) -> str:
        args = parse_args(raw_args)
        options = args.get("options") if isinstance(args.get("options"), list) else []
        return answer_text(await ask(ctx, args.get("question", ""), options))

    return FunctionTool(
        name=ASK_USER_TOOL,
        description=ASK_USER_DESCRIPTION,
        params_json_schema=ASK_USER_PARAMETERS,
        on_invoke_tool=invoke,
        strict_json_schema=False,
    )
