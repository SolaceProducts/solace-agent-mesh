# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Strands Agents hooks that report model and tool steps to the mesh task.

Strands agents hold their conversation and are not re-entrant, so a mesh task gets a fresh agent: `run_agent` takes
a function that builds the agent for the task (``build(ctx)``) and adds `SamLinkHooks` to it. The hooks run before
the agent's own (``SAM_ORDER``), so SAM sees a tool call before an approval hook asks about it.

A call to a tool the task's user lacks the scopes for is refused by the before-tool hook (``cancel_tool``), so the tool
never runs. Strands has no public way to take a tool off a built agent, so the model is still offered it unless
``build(ctx)`` leaves it out (``tools=ctx.permitted_tools(TOOLS)``).

An agent given to another as a tool (``tools=[inner]``, or ``inner.as_tool()``) runs with its own hooks, not the
outer agent's. So when a run starts, the hooks find every such agent among the tools, at any depth, and give each a
router (once; agents may be shared): while the run lasts, the router reports that agent's model and tool steps to the
same task, applies the same scope checks, and asks the same approvals (the outer agent's ``with_approval`` hooks).
Outside a run with these hooks the router does nothing.
"""

from __future__ import annotations

import contextvars
import logging
from collections.abc import Iterator
from typing import Any

from strands import Agent
from strands.hooks import (
    AfterInvocationEvent,
    AfterModelCallEvent,
    AfterToolCallEvent,
    BeforeInvocationEvent,
    BeforeModelCallEvent,
    BeforeToolCallEvent,
    HookProvider,
    HookRegistry,
    MessageAddedEvent,
)

from .._adapter import (
    NestedRunRefused,
    Steps,
    ToolScopes,
    access_denied,
    check_running,
    check_usage_read,
    json_or_text,
    over,
    reported_result,
    unwrapped,
    usage_of,
)
from .._drift import require_members
from ..context import TaskContext
from ..usage import Usage

log = logging.getLogger("sam_link_sdk.strands")

SAM_ORDER = -10


def _model_name(agent: Agent) -> str:
    model = getattr(agent, "model", None)
    get_config = getattr(model, "get_config", None)
    config = get_config() if callable(get_config) else None
    name = (
        (config.get("model_id") or config.get("model"))
        if isinstance(config, dict)
        else getattr(config, "model_id", None)
    )
    return str(name or getattr(model, "model_id", None) or type(model).__name__ or "default")


def _usage_from(message: Any) -> Usage:
    """Strands' ``Usage``: whether ``inputTokens`` holds ``cacheReadInputTokens`` and ``cacheWriteInputTokens``
    depends on the model provider. Its Anthropic and Bedrock models leave them out and count them in
    ``totalTokens``; its OpenAI, LiteLLM and Gemini models include them (``totalTokens`` is input plus output). The
    total tells them apart (``usage_of``)."""
    usage = (message.get("metadata") or {}).get("usage") or {}
    return usage_of(
        input=usage.get("inputTokens"),
        output=usage.get("outputTokens"),
        cache_read=usage.get("cacheReadInputTokens"),
        cache_write=usage.get("cacheWriteInputTokens"),
        total=usage.get("totalTokens"),
        input_includes_cache=None,
    )


def _result_data(result: Any) -> Any:
    """What the tool gave the model, as the reported result; a failed tool is {"error": ...}."""
    blocks = [json_or_text(b["text"]) if "text" in b else b.get("json", b) for b in result.get("content") or []]
    data = blocks[0] if len(blocks) == 1 else blocks
    return reported_result(data, result.get("status") == "error")


class SamLinkHooks(HookProvider):
    """Reports each model call and its response, and each tool's start and result, for one agent run, refuses a call
    to a tool the user lacks the scopes for (what ``step_reporter`` returns), and collects the messages the run added
    (``added``) for conversation memory. Strands pairs a tool's events by its toolUseId."""

    def __init__(self, ctx: TaskContext, scopes: ToolScopes | None = None):
        self.ctx = ctx
        self._steps = Steps(ctx)
        self.added: list[Any] = []
        self._model = "default"
        self._started: set[str] = set()
        self.calls: dict[str, tuple[str, dict]] = {}
        self.refusals: dict[str, str] = {}
        self.scopes = scopes or ToolScopes(ctx)
        self._nested_token: contextvars.Token | None = None

    def register_hooks(self, registry: HookRegistry, **kwargs: Any) -> None:
        registry.add_callback(BeforeInvocationEvent, self._before_invocation, order=SAM_ORDER)
        registry.add_callback(AfterInvocationEvent, self._after_invocation)
        registry.add_callback(BeforeModelCallEvent, self._before_model, order=SAM_ORDER)
        registry.add_callback(AfterModelCallEvent, self._after_model)
        registry.add_callback(BeforeToolCallEvent, self._before_tool, order=SAM_ORDER)
        registry.add_callback(AfterToolCallEvent, self._after_tool)
        registry.add_callback(MessageAddedEvent, self._message_added)

    async def _before_model(self, event: BeforeModelCallEvent) -> None:
        check_running(self.ctx)
        self._model = _model_name(event.agent)
        await self._steps.model_call(self._model)

    async def _after_model(self, event: AfterModelCallEvent) -> None:
        if event.stop_response is None:
            if event.exception is not None:
                self.ctx.record_llm_error(event.exception)
            return
        message = event.stop_response.message
        text: list[str] = []
        reasoning: list[str] = []
        calls: list[dict] = []
        for block in message.get("content") or []:
            if "text" in block:
                text.append(block["text"])
            elif "toolUse" in block:
                use = block["toolUse"]
                calls.append({"id": use["toolUseId"], "name": use["name"], "args": dict(use.get("input") or {})})
            elif "reasoningContent" in block:
                reasoning.append(str((block["reasoningContent"].get("reasoningText") or {}).get("text") or ""))
        answer, usage = "".join(text), _usage_from(message)
        raw = (message.get("metadata") or {}).get("usage")
        check_usage_read("sam_link_sdk.strands", self._model, raw, usage, answer, calls)
        await self._steps.model_answer(self._model, answer, calls, usage, "\n".join(r for r in reasoning if r))

    async def _before_tool(self, event: BeforeToolCallEvent) -> None:
        """Check a tool call before Strands runs it and report its start.

        A call is cancelled, unreported, when the task is over (a stopped task publishes nothing more, and an answered
        one cannot) and cancelled and reported as refused when the user lacks the tool's scopes.
        """
        name, call_id = event.tool_use["name"], event.tool_use["toolUseId"]
        args = dict(event.tool_use.get("input") or {})
        ended = over(self.ctx, name)
        if ended:
            event.cancel_tool = ended
            return
        if not self.scopes.permits(name):
            event.cancel_tool = access_denied(name)
            await self._steps.refuse(name, args, call_id)
            return
        self.calls[call_id] = (name, args)
        if call_id in self._started:
            return
        self._started.add(call_id)
        await self._steps.tool_start(name, args, call_id)

    async def _after_tool(self, event: AfterToolCallEvent) -> None:
        """Report a tool's result.

        The hook that held a call words the cancellation as it likes, so a refused call's result is replaced with the
        refusal: the model is told what it is told of any call the user did not approve.
        """
        call_id = event.tool_use["toolUseId"]
        if call_id not in self._started:
            return
        self._started.discard(call_id)
        refusal = self.refusals.get(call_id)
        if refusal and event.cancel_message:
            event.result = {"toolUseId": call_id, "status": "error", "content": [{"text": refusal}]}
        await self._steps.tool_end(event.tool_use["name"], _result_data(event.result), call_id)

    def _message_added(self, event: MessageAddedEvent) -> None:
        self.added.append(event.message)

    def _before_invocation(self, event: BeforeInvocationEvent) -> None:
        """Refuse background tools and route the agents this one runs.

        Hooks run in the run's own task, so what is set here holds for the run, the agents it runs included.
        """
        _refuse_background_tasks(event.agent)
        inner = list(_nested_agents(event.agent))
        for agent in inner:
            _refuse_background_tasks(agent)
        if not inner or self._nested_token is not None:
            return
        for agent in inner:
            _route(agent)
        self._nested_token = _nested.set(_Nested(self, _approvals_of(event.agent)))

    def _after_invocation(self, event: AfterInvocationEvent) -> None:
        """Undo what `_before_invocation` set.

        The token is reset only in the context it was set in; when the run ended elsewhere there is nothing else to
        undo.
        """
        token, self._nested_token = self._nested_token, None
        if token is not None:
            try:
                _nested.reset(token)
            except ValueError:
                pass


def _refuse_background_tasks(agent: Any) -> None:
    """Refuse an agent that runs tools in the background (``Agent(background_tasks=...)``): such a tool outlives the
    model turn that called it, in an asyncio task a stop of the mesh task does not reach."""
    from . import TESTED_VERSION

    require_members(
        "sam_link_sdk.strands",
        "Strands Agents",
        "strands-agents",
        TESTED_VERSION,
        agent,
        "Agent",
        ("_background_tasks",),
    )
    if agent._background_tasks is not None:
        raise NestedRunRefused(
            f"agent {getattr(agent, 'name', None)!r} runs tools in the background (background_tasks), where a stop of "
            "the mesh task would not reach them: build it without background_tasks"
        )


def _nested_agents(agent: Any, seen: set[int] | None = None) -> Iterator[Agent]:
    """The agents among `agent`'s tools (agent-as-tool wrappers), and theirs, at any depth."""
    from . import TESTED_VERSION

    seen = set() if seen is None else seen
    require_members(
        "sam_link_sdk.strands", "Strands Agents", "strands-agents", TESTED_VERSION, agent, "Agent", ("tool_registry",)
    )
    require_members(
        "sam_link_sdk.strands",
        "Strands Agents",
        "strands-agents",
        TESTED_VERSION,
        agent.tool_registry,
        "Agent.tool_registry",
        ("registry",),
    )
    for tool in list(agent.tool_registry.registry.values()):
        inner = getattr(unwrapped(tool), "agent", None)
        if isinstance(inner, Agent) and id(inner) not in seen:
            seen.add(id(inner))
            yield inner
            yield from _nested_agents(inner, seen)


def _approvals_of(agent: Any) -> list[Any]:
    """The approval hooks (``with_approval``) on `agent`, to ask for the agents it runs as tools too."""
    from . import TESTED_VERSION
    from .tools import _ApprovalHooks

    require_members(
        "sam_link_sdk.strands",
        "Strands Agents",
        "strands-agents",
        TESTED_VERSION,
        agent.hooks,
        "HookRegistry",
        ("_registered_callbacks",),
    )
    entries = agent.hooks._registered_callbacks.get(BeforeToolCallEvent, [])
    owners = (getattr(getattr(entry, "callback", None), "__self__", None) for entry in entries)
    return [owner for owner in owners if isinstance(owner, _ApprovalHooks)]


class _Nested:
    """One run's reporting, scope checks and approvals, for the agents it runs as tools."""

    def __init__(self, outer: SamLinkHooks, approvals: list[Any]):
        self.outer = outer
        self.approvals = approvals
        self._hooks: dict[int, tuple[Agent, SamLinkHooks]] = {}

    def hooks_for(self, agent: Agent) -> SamLinkHooks:
        known = self._hooks.get(id(agent))
        if known is None:
            known = self._hooks[id(agent)] = (agent, SamLinkHooks(self.outer.ctx, scopes=self.outer.scopes))
        return known[1]


_nested: contextvars.ContextVar[_Nested | None] = contextvars.ContextVar("sam_link_strands_nested", default=None)
_ROUTED = "_sam_link_routed"


class _Router(HookProvider):
    """On an agent run as a tool: hands its events to the hooks of the run in progress (``_Nested``), if any."""

    def register_hooks(self, registry: HookRegistry, **kwargs: Any) -> None:
        registry.add_callback(BeforeModelCallEvent, self._before_model, order=SAM_ORDER)
        registry.add_callback(AfterModelCallEvent, self._after_model)
        registry.add_callback(BeforeToolCallEvent, self._before_tool, order=SAM_ORDER)
        registry.add_callback(AfterToolCallEvent, self._after_tool)

    async def _before_model(self, event: BeforeModelCallEvent) -> None:
        nested = _nested.get()
        if nested is not None:
            await nested.hooks_for(event.agent)._before_model(event)

    async def _after_model(self, event: AfterModelCallEvent) -> None:
        nested = _nested.get()
        if nested is not None:
            await nested.hooks_for(event.agent)._after_model(event)

    async def _before_tool(self, event: BeforeToolCallEvent) -> None:
        nested = _nested.get()
        if nested is None:
            return
        await nested.hooks_for(event.agent)._before_tool(event)
        for approval in nested.approvals:
            await approval._ask(event)

    async def _after_tool(self, event: AfterToolCallEvent) -> None:
        nested = _nested.get()
        if nested is not None:
            await nested.hooks_for(event.agent)._after_tool(event)


def _route(agent: Agent) -> None:
    if not getattr(agent, _ROUTED, False):
        agent.hooks.add_hook(_Router())
        setattr(agent, _ROUTED, True)


def step_reporter(ctx: TaskContext) -> SamLinkHooks:
    """The hooks that report one task's model and tool steps to the mesh: ``Agent(..., hooks=[step_reporter(ctx)])``
    (``run_agent`` adds them to the agent ``build(ctx)`` makes)."""
    return SamLinkHooks(ctx)
