# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""ADK tools for human-in-the-loop and the card's tool manifest.

An ADK Runner and its agent are built once, while approvals belong to a task. So these are toolsets: ADK asks them
for tools on every run, and they act for the mesh task that ``run_agent`` is serving (as ``mesh_agent_tools`` does).
``LlmAgent(..., tools=[*with_approval([send_email], names=["send_email"]), ask_user_tool()])``.
"""

from __future__ import annotations

import inspect
from collections.abc import Callable, Iterable
from typing import Any

from google.adk.agents.readonly_context import ReadonlyContext
from google.adk.tools import BaseTool
from google.adk.tools.base_toolset import BaseToolset
from google.adk.tools.function_tool import FunctionTool
from google.adk.tools.tool_context import ToolContext
from google.genai import types

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
    error_result,
    mark_scoped,
    mark_wrapper,
    named_tool_infos,
    scope_map,
    scope_refusal,
    scopes_of,
)
from ..config import ToolInfo
from ..context import current_task

ToolLike = BaseTool | Callable[..., Any]
UNAVAILABLE = "Approval is unavailable here: this tool can only run inside a mesh task, so it was not run."


def _as_tool(tool: ToolLike) -> BaseTool:
    return tool if isinstance(tool, BaseTool) else FunctionTool(tool)


def tool_infos(tools: Iterable[ToolLike]) -> list[ToolInfo]:
    """A card tool manifest (``AgentConfig(tools=...)``) from the agent's tools (functions or ADK tools, and the
    tools inside ``with_approval``), each with the scopes ``with_scopes`` gave it. Other toolsets resolve their tools
    at run time, so they are not listed."""
    infos = []
    for tool in tools:
        if isinstance(tool, _ApprovalToolset):
            infos.extend(tool_infos(tool.tools))
        elif isinstance(tool, BaseToolset):
            continue
        elif isinstance(tool, BaseTool):
            infos.extend(named_tool_infos([tool]))
        else:
            infos.append(ToolInfo(getattr(tool, "__name__", "tool"), (inspect.getdoc(tool) or "").split("\n\n")[0]))
    return infos


class _ScopedTool(BaseTool):
    """A tool that checks the current mesh task's grants where it runs (see ``with_scopes``)."""

    def __init__(self, inner: BaseTool, scopes: Iterable[str]):
        super().__init__(name=inner.name, description=inner.description, is_long_running=inner.is_long_running)
        self.inner = inner
        mark_wrapper(mark_scoped(self, scopes), inner)

    def _get_declaration(self) -> types.FunctionDeclaration | None:
        return self.inner._get_declaration()

    async def run_async(self, *, args: dict[str, Any], tool_context: ToolContext) -> Any:
        refused = scope_refusal(self.name, scopes_of(self))
        if refused:
            return error_result(refused)
        return await self.inner.run_async(args=args, tool_context=tool_context)


TOOLSET_SCOPES = (
    "; a toolset's tools (an MCP toolset's) resolve only at run time and cannot be wrapped here: declare "
    "their scopes in the card (ToolInfo), and the step reporter enforces them"
)
TOOLSET_APPROVALS = (
    "; a toolset's tools (an MCP toolset's) resolve only at run time and cannot be covered by with_approval"
)


def with_scopes(tools: Iterable[ToolLike | BaseToolset], scopes: ToolScopeMap) -> list[Any]:
    """`tools` (functions or ADK tools) as ADK tools, each one `scopes` names (tool name -> the scopes a user needs
    for it, ``docs/security.md``) checking them where it runs: called for a mesh task whose user lacks them, for a
    stopped task, or outside any mesh task (a run no plugin sees), it refuses with an error result and its body does
    not run. ``tool_infos`` tells SAM the scopes, and the step reporter also hides the tool from such a user and
    reports the refusal. A name that is none of the tools is a ValueError. A toolset among them (an MCP toolset) is
    passed on unchanged: its tools resolve at run time and cannot be given scopes here (the step reporter enforces the
    scopes the card declares for them), and naming one is a ValueError that says so; this module's with_approval
    toolset is looked into, its tools given the scopes."""
    tools = list(tools)
    inside = [t for ts in tools if isinstance(ts, _ApprovalToolset) for t in ts.tools]
    plain = [_as_tool(t) for t in tools if not isinstance(t, BaseToolset)]
    others = any(isinstance(t, BaseToolset) and not isinstance(t, _ApprovalToolset) for t in tools)
    required = scope_map([*plain, *inside], scopes, hint=TOOLSET_SCOPES if others else "")

    def scoped(tool: Any) -> Any:
        if isinstance(tool, _ApprovalToolset):
            return tool.over([scoped(t) for t in tool.tools])
        if isinstance(tool, BaseToolset):
            return tool
        tool = _as_tool(tool)
        return _ScopedTool(tool, required[tool.name]) if tool.name in required else tool

    return [scoped(t) for t in tools]


class _GuardedTool(BaseTool):
    """A tool that asks the current mesh task's user for approval before each call."""

    def __init__(self, inner: BaseTool, message: str = ""):
        super().__init__(name=inner.name, description=inner.description, is_long_running=inner.is_long_running)
        self.inner = inner
        self._message = message
        mark_wrapper(mark_scoped(self, scopes_of(inner)), inner)

    def _get_declaration(self) -> types.FunctionDeclaration | None:
        return self.inner._get_declaration()

    async def run_async(self, *, args: dict[str, Any], tool_context: ToolContext) -> Any:
        ctx = current_task()
        refused = approval_refusal(ctx, self.name, scopes_of(self))
        if refused or ctx is None:
            return error_result(UNAVAILABLE if ctx is None else refused)
        refused = await approve(ctx, self.name, args, self._message)
        if refused:
            return error_result(refused)
        return await self.inner.run_async(args=args, tool_context=tool_context)


class _ApprovalToolset(BaseToolset):
    """Wraps tools so each call pauses the task and asks the user to approve it first.

    `names` limits it to those tools (default: all of them). The user sees the tool's name and its arguments
    (credentials redacted), plus `message`. A call that is denied, cancelled or times out is not run: the model gets
    an error result, which is also how the audit trail records it.
    """

    def __init__(
        self, tools: Iterable[ToolLike], *, names: Iterable[str] | None = None, message: str = "", hint: str = ""
    ):
        super().__init__()
        self.tools = [_as_tool(t) for t in tools]
        self._wanted = approval_filter(self.tools, names, hint)
        self._message = message

    def over(self, tools: list[BaseTool]) -> _ApprovalToolset:
        """This approval over `tools` (the same tools, guarded by with_scopes)."""
        copy = _ApprovalToolset([])
        copy.tools, copy._wanted, copy._message = tools, self._wanted, self._message
        return copy

    async def get_tools(self, readonly_context: ReadonlyContext | None = None) -> list[BaseTool]:
        return [_GuardedTool(t, self._message) if self._wanted(t.name) else t for t in self.tools]

    async def close(self) -> None:
        return None


class _AskUserTool(BaseTool):
    """A tool the model can call to ask the user a question and wait for the answer."""

    def __init__(self) -> None:
        super().__init__(name=ASK_USER_TOOL, description=ASK_USER_DESCRIPTION)

    def _get_declaration(self) -> types.FunctionDeclaration:
        return types.FunctionDeclaration(
            name=self.name,
            description=self.description,
            parameters=types.Schema(
                type=types.Type.OBJECT,
                properties={
                    "question": types.Schema(type=types.Type.STRING, description=QUESTION_HELP),
                    "options": types.Schema(
                        type=types.Type.ARRAY, items=types.Schema(type=types.Type.STRING), description=OPTIONS_HELP
                    ),
                },
                required=["question"],
            ),
        )

    async def run_async(self, *, args: dict[str, Any], tool_context: ToolContext) -> Any:
        ctx = current_task()
        if ctx is None:
            return error_result("Asking the user is unavailable here: this tool can only run inside a mesh task.")
        options = args.get("options") if isinstance(args.get("options"), list) else []
        return answer_text(await ask(ctx, args.get("question", ""), options))


class _AskUserToolset(BaseToolset):
    """Offers the model a tool to ask the user questions."""

    def __init__(self) -> None:
        super().__init__()

    async def get_tools(self, readonly_context: ReadonlyContext | None = None) -> list[BaseTool]:
        return [_AskUserTool()]

    async def close(self) -> None:
        return None


def with_approval(
    tools: Iterable[ToolLike | BaseToolset], *, names: Iterable[str] | None = None, message: str = ""
) -> list[BaseToolset]:
    """`tools` (functions or ADK tools) as one toolset whose chosen tools ask for approval before each call:
    ``LlmAgent(..., tools=with_approval([lookup_policy, route_claim], names=["route_claim"]))``.

    ADK builds the agent once, not per task, so this takes no task context: each call asks the user of the mesh task
    ``run_agent`` is serving. A toolset among `tools` (an MCP toolset) is returned after the approval toolset,
    unchanged: its tools resolve at run time and cannot be covered, so name the tools to approve (no names with a
    toolset, or a name inside one, is a ValueError).
    """
    tools = list(tools)
    toolsets = [t for t in tools if isinstance(t, BaseToolset)]
    if toolsets and names is None:
        raise ValueError(
            "with_approval was given a toolset and no names: a toolset's tools (an MCP toolset's) resolve "
            "only at run time, so they cannot be covered; name the tools to approve, and give the "
            "toolset to the agent outside with_approval"
        )
    plain = [t for t in tools if not isinstance(t, BaseToolset)]
    return [
        _ApprovalToolset(plain, names=names, message=message, hint=TOOLSET_APPROVALS if toolsets else ""),
        *toolsets,
    ]


def ask_user_tool() -> BaseToolset:
    """The tool the model can call to ask the user a question and wait for the answer (as a toolset ADK asks for
    tools on every run): ``LlmAgent(..., tools=[*tools, ask_user_tool()])``. It asks the user of the mesh task
    ``run_agent`` is serving; outside a mesh task it answers that asking is unavailable."""
    return _AskUserToolset()
