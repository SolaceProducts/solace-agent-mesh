# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""LangChain tools for human-in-the-loop (approve a tool call before it runs, let the model ask the user) and the
card's tool manifest."""

from __future__ import annotations

from collections.abc import Iterable
from typing import Any

from langchain_core.tools import BaseTool, InjectedToolCallId, StructuredTool, ToolException
from pydantic import BaseModel, Field

from .._adapter import (
    ASK_USER_DESCRIPTION,
    ASK_USER_TOOL,
    OPTIONS_HELP,
    QUESTION_HELP,
    ToolScopeMap,
    answer_text,
    approval_filter,
    approval_refusal,
    approve,
    ask,
    check_kind,
    mark_scoped,
    mark_wrapper,
    named_tool_infos,
    scope_map,
    scope_refusal,
    scoped_class,
    scopes_of,
)
from ..config import ToolInfo
from ..context import TaskContext, tool_name

WRAP_FIRST = "a LangChain tool (BaseTool) is needed: make a function one with @tool first"


def tool_infos(tools: Iterable[BaseTool]) -> list[ToolInfo]:
    """A card tool manifest (``AgentConfig(tools=...)``) from the agent's own LangChain tools, each with the scopes
    ``with_scopes`` gave it."""
    return named_tool_infos(tools)


class _ScopeRefused(ToolException):
    """A call to a scoped tool refused where the tool runs (see ``with_scopes``)."""


def _scoped_class(cls: type[BaseTool]) -> type[BaseTool]:
    """`cls` with the scope check where LangChain runs a tool: it turns the call's input into the tool's arguments
    (on the sync and async paths alike) inside the try that turns a ToolException into the call's error, before the
    tool's body runs."""

    def methods(cls: type[BaseTool]) -> dict[str, Any]:
        def _to_args_and_kwargs(self, tool_input, tool_call_id):
            refused = scope_refusal(self.name, scopes_of(self))
            if refused:
                raise _ScopeRefused(refused)
            return cls._to_args_and_kwargs(self, tool_input, tool_call_id)

        return {"_to_args_and_kwargs": _to_args_and_kwargs}

    return scoped_class(cls, methods, __name__)


def with_scopes(tools: Iterable[BaseTool], scopes: ToolScopeMap) -> list[BaseTool]:
    """`tools`, each one `scopes` names (tool name -> the scopes a user needs for it, ``docs/security.md``) replaced by
    a copy that checks them where it runs: called for a mesh task whose user lacks them, for a stopped task, or
    outside any mesh task (a run no hook sees, a thread that lost the context), it refuses and its body does not run.
    ``tool_infos`` tells SAM the scopes, and ``run_agent`` also hides the tool from such a user and reports the
    refusal. A name that is none of the tools is a ValueError."""
    tools = list(tools)
    required = scope_map(tools, scopes)
    check_kind(tools, required, BaseTool, "with_scopes names", WRAP_FIRST)
    out: list[BaseTool] = []
    for tool in tools:
        if tool_name(tool) not in required:
            out.append(tool)
            continue
        guarded = tool.model_copy()
        object.__setattr__(guarded, "__class__", _scoped_class(type(tool)))
        out.append(mark_wrapper(mark_scoped(guarded, required[tool.name]), tool))
    return out


def with_approval(
    ctx: TaskContext, tools: Iterable[BaseTool], *, names: Iterable[str] | None = None, message: str = ""
) -> list[BaseTool]:
    """Wrap tools so each call pauses the task and asks the user to approve it first.

    `names` limits it to those tools (default: all of them). The user
    sees the tool's name and the arguments the model gave (with credentials redacted), plus `message`. A call that is
    denied, cancelled or times out is not run: the model is told so as a tool error, the tool run is recorded as
    failed, and the decision is audited.
    """
    tools = list(tools)
    wanted = approval_filter(tools, names)
    check_kind(
        tools, [tool_name(t) for t in tools if wanted(tool_name(t))], BaseTool, "with_approval covers", WRAP_FIRST
    )
    return [_guard(ctx, t, message) if wanted(tool_name(t)) else t for t in tools]


def _schema_fields(schema: Any) -> set[str]:
    if isinstance(schema, dict):
        return set(schema.get("properties") or {})
    return set(getattr(schema, "model_fields", None) or {})


def _call_id_field(tool: BaseTool) -> str | None:
    """The argument the tool takes its tool-call id in (``InjectedToolCallId``), if any."""
    for name, field in (getattr(tool.args_schema, "model_fields", None) or {}).items():
        if any(m is InjectedToolCallId or isinstance(m, InjectedToolCallId) for m in field.metadata):
            return name
    return None


def _guard(ctx: TaskContext, tool: BaseTool, message: str) -> BaseTool:
    """Wrap a tool so it asks the user for approval before it runs.

    The wrapper shows the model only the arguments it fills in, not injected ones. It takes the tool's own arguments,
    injected ones included, so LangChain and LangGraph inject them into it as into the tool. `handle_tool_error` makes
    LangChain turn a refusal into an error result for the model; without it LangGraph's ToolNode lets the exception
    escape and the whole task fails.
    """
    shown = _schema_fields(tool.tool_call_schema)
    call_id_field = _call_id_field(tool)

    async def run(**arguments) -> Any:
        """Refuse or approve the call, then run the wrapped tool.

        No config is passed: the tool takes the wrapper's run config, step reporter and stop guard included, so whatever
        it runs in turn (a sub-agent, another tool) is reported, checked and stopped as the task. The step reporter
        reports the tool's own start and result once, as the wrapper's (it knows the wrapped call by its parent run). A
        tool that takes its call id (a peer tool) must be invoked with a whole tool call carrying it.
        """
        refused = approval_refusal(ctx, tool.name, scopes_of(tool))
        if refused:
            raise ToolException(refused)
        refused = await approve(ctx, tool.name, {k: v for k, v in arguments.items() if k in shown}, message)
        if refused:
            raise ToolException(refused)
        if call_id_field is None:
            return await tool.ainvoke(arguments)
        call_id = arguments.pop(call_id_field)
        return await tool.ainvoke({"type": "tool_call", "name": tool.name, "args": arguments, "id": call_id})

    return mark_wrapper(
        mark_scoped(
            StructuredTool.from_function(
                coroutine=run,
                name=tool.name,
                description=tool.description,
                args_schema=tool.args_schema or tool.get_input_schema(),
                handle_tool_error=True,
            ),
            scopes_of(tool),
        ),
        tool,
    )


class _AskArgs(BaseModel):
    question: str = Field(description=QUESTION_HELP)
    options: list[str] | None = Field(default=None, description=OPTIONS_HELP)


def ask_user_tool(ctx: TaskContext) -> BaseTool:
    """A tool the model can call to ask the user a question and wait for the answer."""

    async def ask_user(question: str, options: list[str] | None = None) -> str:
        return answer_text(await ask(ctx, question, options))

    return StructuredTool.from_function(
        coroutine=ask_user, name=ASK_USER_TOOL, args_schema=_AskArgs, description=ASK_USER_DESCRIPTION
    )
