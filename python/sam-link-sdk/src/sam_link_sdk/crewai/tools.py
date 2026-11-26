# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""CrewAI tools for human-in-the-loop and the card's tool manifest.

CrewAI runs tools synchronously in the crew's worker thread, so each tool here hands the prompt to the mesh task's
event loop and blocks that worker thread until the user answers (the crew is paused meanwhile, which is the point).
Build the tools in the handler, on the mesh task's loop, and give them to the crew built for that task.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Iterable
from typing import Any

from crewai.tools import BaseTool
from crewai.tools.base_tool import Tool
from crewai.tools.structured_tool import CrewStructuredTool
from crewai.tools.tool_failure import ToolFailure, ToolFailureReason
from pydantic import BaseModel, ConfigDict, Field, PrivateAttr

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
    error_result,
    mark_scoped,
    mark_wrapper,
    named_tool_infos,
    scope_map,
    scope_refusal,
    scopes_of,
    unwrapped,
)
from ..config import ToolInfo
from ..context import TaskContext, tool_name

WRAP_FIRST = "a crew tool (BaseTool) is needed: make a function one with @tool first"


def tool_infos(tools: Iterable[BaseTool]) -> list[ToolInfo]:
    """A card tool manifest (``AgentConfig(tools=...)``) from the crew's tools, each with the scopes ``with_scopes``
    gave it."""
    return named_tool_infos(tools)


ON_LOOP = (
    "{} needs the user, and this crew runs its tools on the event loop that would carry the question "
    "(CrewAI's async native tool calling), where waiting for the answer would block it for good; it was not run."
)


def _on_loop(loop: asyncio.AbstractEventLoop) -> bool:
    """Whether this thread runs `loop`: the prompt is handed to that loop and waited for, which cannot be done from
    the loop's own thread."""
    try:
        return asyncio.get_running_loop() is loop
    except RuntimeError:
        return False


STOCK_RUNS = (BaseTool.run, Tool.run)


def _spent_limit(tool: BaseTool) -> ToolFailure | None:
    """CrewAI's own usage-limit failure when the tool behind `tool` (through with_scopes) has certainly spent its
    limit, read without claiming a use: only where the call goes through CrewAI's own claim (BaseTool.run, or Tool.run
    of an ``@tool`` function: both claim a use before running the body and return the failure instead when it is
    spent), so a tool that claims its own way is asked. The approval answers it without asking and without calling the
    tool."""
    behind = unwrapped(tool)
    if (
        not isinstance(behind, BaseTool)
        or type(behind)._claim_usage is not BaseTool._claim_usage
        or type(behind).run not in STOCK_RUNS
    ):
        return None
    if behind.max_usage_count is None or behind.current_usage_count < behind.max_usage_count:
        return None
    return ToolFailure(
        message=f"Tool '{behind.name}' has reached its usage limit of {behind.max_usage_count} times "
        "and cannot be used anymore.",
        reason=ToolFailureReason.USAGE_LIMIT,
    )


def _refused(reason: str) -> str:
    """The result a refused call gives the model, as a JSON error.

    The JSON `{"error": ...}` is how the listener and the audit trail recognise a failed tool.
    """
    return json.dumps(error_result(reason))


def _never_cache(arguments: Any, result: Any) -> bool:
    return False


CARRIED_FIELDS = (
    "name",
    "description",
    "env_vars",
    "args_schema",
    "result_schema",
    "result_as_answer",
    "tool_failure_policy",
)
"""The BaseTool fields a wrapper takes from the tool it wraps (crewai 1.15). Left out: `description_updated` (set, so
CrewAI does not prefix the already formatted description again), `cache_function` (see _Wrapper), and the usage limit
(`max_usage_count`, `current_usage_count`): CrewAI claims a use on the tool it calls before the tool runs, so on the
wrapper a refused call would spend it; the wrapped tool's own run() claims it only for a call that runs.
"""
USAGE_FIELDS = ("max_usage_count", "current_usage_count")


class _InTaskContext(CrewStructuredTool):
    """The structured tool a crew agent runs a wrapper through. CrewAI's ainvoke runs a sync tool on an executor
    thread without the caller's context, where the wrapper would find no mesh task and refuse every call; this runs
    the sync invoke (usage, parsing, the call) on a thread that carries it."""

    async def ainvoke(self, input: Any, config: dict[str, Any] | None = None, **kwargs: Any) -> Any:
        return await asyncio.to_thread(self.invoke, input, config, **kwargs)


class _Wrapper(BaseTool):
    """A crew tool standing in for the tool it wraps, under the same name, arguments and answer policy; the wrapped
    tool keeps its own usage limit, which only a call that runs spends (see CARRIED_FIELDS).

    Its results are never cached: CrewAI reads its tool-result cache before it calls a tool, so a cached result would
    answer a later call (another task's, another user's) without the wrapper's check, and a cached refusal would refuse
    a user who is allowed."""

    model_config = ConfigDict(arbitrary_types_allowed=True)
    _inner: BaseTool = PrivateAttr()

    def __init__(self, inner: BaseTool):
        super().__init__(
            **{f: getattr(inner, f) for f in CARRIED_FIELDS if hasattr(inner, f)},
            description_updated=True,
            cache_function=_never_cache,
        )
        self._inner = inner
        mark_wrapper(mark_scoped(self, scopes_of(inner)), inner)

    def to_structured_tool(self) -> CrewStructuredTool:
        structured = super().to_structured_tool()
        object.__setattr__(structured, "__class__", _InTaskContext)
        return structured


class _ScopedTool(_Wrapper):
    """A crew tool that checks the current mesh task's grants where it runs (see ``with_scopes``)."""

    def __init__(self, inner: BaseTool, scopes: Iterable[str]):
        super().__init__(inner)
        mark_scoped(self, scopes)

    def _run(self, **kwargs) -> Any:
        refused = scope_refusal(self.name, scopes_of(self))
        return _refused(refused) if refused else self._inner.run(**kwargs)

    async def _arun(self, **kwargs) -> Any:
        refused = scope_refusal(self.name, scopes_of(self))
        return _refused(refused) if refused else await self._inner.arun(**kwargs)


def with_scopes(tools: Iterable[BaseTool], scopes: ToolScopeMap) -> list[BaseTool]:
    """`tools`, each one `scopes` names (tool name -> the scopes a user needs for it, ``docs/security.md``) replaced by
    a tool that checks them where it runs: called for a mesh task whose user lacks them, for a stopped task, or outside
    any mesh task (a crew no hook sees, a thread that lost the context), it refuses with an error result and the
    tool's body does not run. ``tool_infos`` tells SAM the scopes, and ``run_agent`` also hides the tool from such a
    user and refuses the call in CrewAI's tool hook. A name that is none of the tools is a ValueError."""
    tools = list(tools)
    required = scope_map(tools, scopes)
    check_kind(tools, required, BaseTool, "with_scopes names", WRAP_FIRST)
    return [_ScopedTool(t, required[tool_name(t)]) if tool_name(t) in required else t for t in tools]


class _ApprovalGuard(_Wrapper):
    """A crew tool that asks the user for approval before each call to the tool it wraps. Outside the mesh task it
    was built for (a crew no hook sees, a thread that lost the context) it asks nobody and refuses."""

    _ctx: TaskContext = PrivateAttr()
    _loop: asyncio.AbstractEventLoop = PrivateAttr()
    _message: str = PrivateAttr(default="")

    def __init__(self, inner: BaseTool, ctx: TaskContext, loop: asyncio.AbstractEventLoop, message: str):
        super().__init__(inner)
        self._ctx, self._loop, self._message = ctx, loop, message

    def _run(self, **kwargs) -> Any:
        refused = approval_refusal(self._ctx, self.name, scopes_of(self))
        spent = None if refused else _spent_limit(self._inner)
        if spent is not None:
            return spent
        if not refused and _on_loop(self._loop):
            refused = ON_LOOP.format(self.name)
        if not refused:
            refused = asyncio.run_coroutine_threadsafe(
                approve(self._ctx, self.name, kwargs, self._message), self._loop
            ).result()
        return _refused(refused) if refused else self._inner.run(**kwargs)


def with_approval(
    ctx: TaskContext, tools: Iterable[BaseTool], *, names: Iterable[str] | None = None, message: str = ""
) -> list[BaseTool]:
    """Make chosen crew tools ask the user for approval before each call.

    `names` limits it to those tools (default: all). The user sees the
    tool's name and arguments (credentials redacted), plus `message`. A call that is denied, cancelled or times out is
    not run: the crew gets an error result, which is also how the audit trail records it. Call it from the handler
    (on the mesh task's loop).
    """
    loop = asyncio.get_running_loop()
    tools = list(tools)
    wanted = approval_filter(tools, names)
    check_kind(
        tools, [tool_name(t) for t in tools if wanted(tool_name(t))], BaseTool, "with_approval covers", WRAP_FIRST
    )
    return [_ApprovalGuard(t, ctx, loop, message) if wanted(tool_name(t)) else t for t in tools]


class _AskArgs(BaseModel):
    question: str = Field(description=QUESTION_HELP)
    options: list[str] | None = Field(default=None, description=OPTIONS_HELP)


class _AskUserTool(BaseTool):
    model_config = ConfigDict(arbitrary_types_allowed=True)
    args_schema: type[BaseModel] = _AskArgs
    _ctx: TaskContext = PrivateAttr()
    _loop: asyncio.AbstractEventLoop = PrivateAttr()

    def __init__(self, ctx: TaskContext, loop: asyncio.AbstractEventLoop):
        super().__init__(name=ASK_USER_TOOL, description=ASK_USER_DESCRIPTION)
        self._ctx, self._loop = ctx, loop

    def _run(self, question: str, options: list[str] | None = None) -> str:
        if _on_loop(self._loop):
            return ON_LOOP.format("asking the user")
        answer = asyncio.run_coroutine_threadsafe(ask(self._ctx, question, options), self._loop).result()
        return answer_text(answer)


def ask_user_tool(ctx: TaskContext) -> BaseTool:
    """A tool the crew's agent can call to ask the user a question and wait for the answer. Call it from the handler."""
    return _AskUserTool(ctx, asyncio.get_running_loop())
