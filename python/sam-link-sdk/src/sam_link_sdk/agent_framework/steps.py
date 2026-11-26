# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Microsoft Agent Framework middleware that reports model and tool steps to the mesh task.

An Agent Framework agent holds no conversation unless it is given a session, and its run takes per-run middleware and
tools, so one agent serves every mesh task: `run_agent` passes this task's middleware and tools to ``agent.run``.
Chat middleware wraps each model call and function middleware each tool call, in the order of the run's middleware
list, so SAM's come before the ones you add (an approval middleware asks only after SAM has seen the tool call).

A tool the task's user lacks the scopes for is taken out of every model request's tools (chat middleware), so the
model is not offered it, and a call to it anyway is refused before it runs (function middleware).

An agent given to another as a tool (``inner.as_tool()``) runs with its own middleware only: the run's middleware never
reaches it. So every such agent among the run's tools, at any depth, gets a router in its own middleware for as long
as the run lasts: those the run starts with (agent middleware), and those a model call is given (chat middleware: a
context provider may add tools for one run). The router reports that agent's model and tool steps to the same task,
applies the same scope checks and asks the same approvals (``run_agent``'s ``with_approval`` middleware); it does
nothing outside such a run, and nothing for the run's own top-level agent (whose steps the run reports). Agents may be
shared, so a router stays while any run needs it and is taken off when the last one ends. Only a plain ``Agent`` (or
``RawAgent``) is routed: the run of any other agent behind a tool (a ``WorkflowAgent``, whose agents run inside its
workflow, or a custom ``BaseAgent``) cannot be followed, so it refuses the run, as does a tool that looks like an agent
tool but whose agent cannot be found.
"""

from __future__ import annotations

import contextvars
import logging
import threading
from collections.abc import Callable, Iterable, Iterator
from typing import Any

from agent_framework import (
    Agent,
    AgentMiddleware,
    AgentResponse,
    BaseAgent,
    BaseChatClient,
    ChatContext,
    ChatMiddleware,
    ChatMiddlewareLayer,
    ChatResponse,
    FunctionInvocationContext,
    FunctionInvocationLayer,
    FunctionMiddleware,
    Message,
    RawAgent,
)

from .._adapter import (
    NestedRunRefused,
    Steps,
    ToolScopes,
    access_denied,
    check_running,
    check_usage_read,
    decide_approval,
    error_result,
    json_or_text,
    new_call_id,
    over,
    parse_args,
    unwrapped,
    usage_of,
)
from .._drift import FrameworkDrift, installed_version, require_members
from ..context import TaskContext, bind_task
from ..usage import Usage

log = logging.getLogger("sam_link_sdk.agent_framework")

REFUSED = "sam_link_refused"
"""Set in a function call's metadata by the approval middleware when the user did not approve the call: the model is told
why as the call's result, and the step is reported (and audited) as a failed call, as in the other adapters.
"""


_CHECKED_CONTEXTS: set[type] = set()


def _check_context(context: Any, label: str, names: tuple[str, ...]) -> None:
    """The middleware contexts' fields are set per instance, so they are checked on the first one of each type."""
    if type(context) in _CHECKED_CONTEXTS:
        return
    from . import TESTED_VERSION

    require_members(
        "sam_link_sdk.agent_framework",
        "Microsoft Agent Framework",
        "agent-framework-core",
        TESTED_VERSION,
        context,
        label,
        names,
    )
    _CHECKED_CONTEXTS.add(type(context))


def _client_model(client: Any) -> str:
    return str(
        getattr(client, "model", None) or getattr(client, "model_id", None) or type(client).__name__ or "default"
    )


def _usage_from(details: Any) -> Usage:
    """Agent Framework's ``UsageDetails``: the core does not say whether ``input_token_count`` holds the cache reads
    (``cache_read_input_token_count``) and writes (``cache_creation_input_token_count``); its OpenAI clients copy
    OpenAI's ``prompt_tokens``, which does, while a client for a provider that counts them apart may not. The total
    tells them apart (``usage_of``)."""
    d = details or {}
    return usage_of(
        input=d.get("input_token_count"),
        output=d.get("output_token_count"),
        cache_read=d.get("cache_read_input_token_count"),
        cache_write=d.get("cache_creation_input_token_count"),
        reasoning=d.get("reasoning_output_token_count"),
        total=d.get("total_token_count"),
        input_includes_cache=None,
    )


def _result_data(result: Any) -> Any:
    """What the tool gave the model, as the reported result."""
    if isinstance(result, str):
        return json_or_text(result)
    if isinstance(result, (list, tuple)):
        items = [
            json_or_text(c.text)
            if getattr(c, "type", None) == "text" and getattr(c, "text", None) is not None
            else str(c)
            for c in result
        ]
        return items[0] if len(items) == 1 else items
    if isinstance(result, (dict, int, float, bool)) or result is None:
        return result
    return str(result)


class _Run:
    """State shared by one task's chat and function middleware."""

    def __init__(self, ctx: TaskContext):
        self.ctx = ctx
        self.steps = Steps(ctx)
        self.model = "default"
        self.scopes = ToolScopes(ctx)
        self.approvals: list[FunctionMiddleware] = []
        self.answers: dict[str, str | None] = {}
        self.agent: Any = None
        self._routed: dict[int, BaseAgent] = {}

    def route(self, tools: Iterable[Any]) -> None:
        """Route the agents behind agent tools among `tools`, at any depth, to this run (see the module docstring)."""
        for agent in _nested_agents(tools):
            if id(agent) not in self._routed:
                _route(agent)
                self._routed[id(agent)] = agent

    def unroute(self) -> None:
        for agent in self._routed.values():
            _unroute(agent)
        self._routed.clear()


class _ChatSignals(ChatMiddleware):
    def __init__(self, run: _Run):
        self._run = run

    async def process(self, context: ChatContext, call_next: Callable[[], Any]) -> None:
        _check_context(context, "ChatContext", ("options", "client", "result"))
        run = self._run
        check_running(run.ctx)
        run.model = str((context.options or {}).get("model") or _client_model(context.client))
        self._hide_denied_tools(context)
        run.route((context.options or {}).get("tools") or [])
        await run.steps.model_call(run.model)
        if getattr(context, "stream", False):

            async def report(response: ChatResponse) -> ChatResponse:
                """Report a response once its stream is complete.

                A streamed call (an agent run as a tool streams) is reported when the stream has its final response.
                """
                await self._report(response)
                return response

            context.stream_result_hooks.append(report)
        try:
            await call_next()
        except Exception as exc:
            run.ctx.record_llm_error(exc)
            raise
        if not getattr(context, "stream", False) and isinstance(context.result, ChatResponse):
            await self._report(context.result)

    def _hide_denied_tools(self, context: ChatContext) -> None:
        options = context.options or {}
        tools = options.get("tools")
        if tools:
            context.options = {**options, "tools": self._run.scopes.visible(tools)}

    async def _report(self, response: ChatResponse) -> None:
        run = self._run
        model = str(response.model or run.model)
        text: list[str] = []
        reasoning: list[str] = []
        calls: list[dict] = []
        for message in response.messages:
            for content in message.contents:
                if content.type == "text":
                    text.append(content.text or "")
                elif content.type == "text_reasoning":
                    reasoning.append(content.text or "")
                elif content.type == "function_call":
                    calls.append(
                        {
                            "id": content.call_id or new_call_id(),
                            "name": content.name,
                            "args": parse_args(content.arguments),
                        }
                    )
        answer, usage = "".join(text), _usage_from(response.usage_details)
        check_usage_read("sam_link_sdk.agent_framework", model, response.usage_details, usage, answer, calls)
        await run.steps.model_answer(model, answer, calls, usage, "\n".join(r for r in reasoning if r))


class _FunctionSignals(FunctionMiddleware):
    def __init__(self, run: _Run):
        self._run = run

    async def process(self, context: FunctionInvocationContext, call_next: Callable[[], Any]) -> None:
        """Check a function call, report it and refuse it when the task is over or the user lacks its scopes.

        A stopped task publishes nothing more, and an answered one cannot, so a call after either is refused unreported.
        """
        _check_context(context, "FunctionInvocationContext", ("function", "arguments", "metadata", "result"))
        run = self._run
        name = context.function.name
        call_id = str(context.metadata.get("call_id") or new_call_id())
        ended = over(run.ctx, name)
        if ended:
            context.result = ended
            return
        if not run.scopes.permits(name):
            context.result = access_denied(name)
            await run.steps.refuse(name, dict(context.arguments or {}), call_id)
            return
        await run.steps.tool_start(name, dict(context.arguments or {}), call_id)
        try:
            await call_next()
        except Exception as exc:
            await run.steps.tool_end(name, error_result(str(exc)), call_id)
            raise
        refused = context.metadata.get(REFUSED)
        await run.steps.tool_end(
            name, error_result(context.result) if refused else _result_data(context.result), call_id
        )


AGENT_TOOL_FUNCTION = "_agent_wrapper"


_PLAIN_RUNS = (Agent.run, RawAgent.run)


def _agent_in_closure(func: Any) -> Any:
    for cell in getattr(func, "__closure__", None) or ():
        try:
            value = cell.cell_contents
        except ValueError:
            continue
        if isinstance(value, BaseAgent):
            return value
    return None


class _ProbeClient(FunctionInvocationLayer, ChatMiddlewareLayer, BaseChatClient):
    def _inner_get_response(self, **kwargs: Any) -> Any:
        raise NotImplementedError


_AS_TOOL_CHECKED = False


def _check_as_tool() -> None:
    """``as_tool`` is found by the name of the function it wraps the agent in and the agent in that function's closure,
    neither of which the framework promises, so the first lookup builds a throwaway agent and checks it still works
    that way."""
    global _AS_TOOL_CHECKED
    if _AS_TOOL_CHECKED:
        return
    from . import TESTED_VERSION

    probe = Agent(client=_ProbeClient(), name="probe")
    func = getattr(unwrapped(probe.as_tool()), "func", None)
    missing = []
    if getattr(func, "__name__", "") != AGENT_TOOL_FUNCTION:
        missing.append(f"Agent.as_tool()'s function {AGENT_TOOL_FUNCTION}")
    elif _agent_in_closure(func) is not probe:
        missing.append(f"the agent in the closure of Agent.as_tool()'s {AGENT_TOOL_FUNCTION}")
    if missing:
        raise FrameworkDrift(
            "sam_link_sdk.agent_framework",
            "Microsoft Agent Framework",
            installed_version("agent-framework-core"),
            TESTED_VERSION,
            missing,
        )
    _AS_TOOL_CHECKED = True


def _agent_of(tool: Any) -> BaseAgent | None:
    """The agent behind a tool made with ``as_tool`` (held by the closure of the tool's function), if it is one a
    router can follow (see the module docstring)."""
    _check_as_tool()
    func = getattr(unwrapped(tool), "func", None)
    if getattr(func, "__name__", "") != AGENT_TOOL_FUNCTION:
        if isinstance(getattr(unwrapped(tool), "_wrapped_agent", None), BaseAgent):
            raise NestedRunRefused(
                f"the tool {getattr(tool, 'name', '')!r} wraps an agent, but not the way SAM finds it, so SAM could "
                "not check or report its run"
            )
        return None
    value = _agent_in_closure(func)
    if value is not None:
        if not isinstance(value, RawAgent) or type(value).run not in _PLAIN_RUNS:
            raise NestedRunRefused(
                f"the tool {getattr(tool, 'name', '')!r} runs a {type(value).__name__}, whose run (and the agents "
                "it runs inside) SAM cannot follow, so it could not check or report them: give the agents to this "
                "one as plain Agent tools instead"
            )
        return value
    raise NestedRunRefused(
        f"the agent behind the tool {getattr(tool, 'name', '')!r} cannot be found, so SAM could not "
        "check or report its run"
    )


def _agent_tools(agent: Any) -> list[Any]:
    options = getattr(agent, "default_options", None) or {}
    return [*(options.get("tools") or []), *(getattr(agent, "mcp_tools", None) or [])]


def _nested_agents(tools: Iterable[Any], seen: set[int] | None = None) -> Iterator[BaseAgent]:
    """The agents behind agent tools among `tools`, and among theirs, at any depth."""
    seen = set() if seen is None else seen
    for tool in tools:
        inner = _agent_of(tool)
        if inner is not None and id(inner) not in seen:
            seen.add(id(inner))
            yield inner
            yield from _nested_agents(_agent_tools(inner), seen)


_current: contextvars.ContextVar[_Run | None] = contextvars.ContextVar("sam_link_agent_framework_run", default=None)


class _RunScope(AgentMiddleware):
    """Around the whole run: makes it the current mesh task's run and routes the agents it runs as tools to it for
    as long as it lasts (see the module docstring)."""

    def __init__(self, run: _Run):
        self._run = run

    async def process(self, context: Any, call_next: Callable[[], Any]) -> None:
        """Make this run current and its tools routed while the agent runs.

        A streamed run's middleware returns before the stream is read, so the run stays current and routed until the
        stream has been consumed (`run_agent` also unroutes when it ends, for a stream that never finishes).
        """
        run = self._run
        tools = context.tools if isinstance(context.tools, (list, tuple)) else [context.tools] if context.tools else []
        run.agent = context.agent
        if getattr(context, "stream", False):
            previous = _current.get()
            _current.set(run)
            run.route([*_agent_tools(context.agent), *tools])

            def finish() -> None:
                _current.set(previous)
                run.unroute()

            context.stream_cleanup_hooks.append(finish)
            await call_next()
            return
        token = _current.set(run)
        try:
            run.route([*_agent_tools(context.agent), *tools])
            with bind_task(run.ctx):
                await call_next()
        finally:
            _current.reset(token)
            run.unroute()


class _Router:
    """On an agent run as a tool: hands its calls to the run in progress, unless there is none or the agent is that
    run's own top-level agent."""

    def __init__(self, agent: BaseAgent):
        self._agent_id = id(agent)

    def _run(self) -> _Run | None:
        run = _current.get()
        return None if run is None or id(run.agent) == self._agent_id else run


class _RouterChat(_Router, ChatMiddleware):
    async def process(self, context: ChatContext, call_next: Callable[[], Any]) -> None:
        run = self._run()
        if run is None:
            await call_next()
        else:
            await _ChatSignals(run).process(context, call_next)


def _call_id(request: Any) -> str:
    return str(request.function_call.call_id)


async def _decide(ctx: TaskContext, request: Any, tools: Iterable[Any]) -> str | None:
    """None when the user approves the call the framework held for approval (``approval_mode="always_require"``),
    else why it is refused: asked through SAM, unless refused for a fact known before asking (a stopped task, the
    user's scopes)."""
    call = request.function_call
    return await decide_approval(ctx, call.name, call.parse_arguments() or {}, "", tools)


class _RouterAgent(_Router, AgentMiddleware):
    """On an agent run as a tool: a call the framework holds for approval ends the agent's run with a request, which
    the tool would fail on (the framework does not hand it up). Asks the user through SAM for each and runs the agent
    again, in the session of the run, with the answers."""

    async def process(self, context: Any, call_next: Callable[[], Any]) -> None:
        run = self._run()
        await call_next()
        if run is None:
            return

        async def resolve(response: AgentResponse) -> AgentResponse:
            requests = [r for r in response.user_input_requests if r.type == "function_approval_request"]
            if not requests:
                return response
            tools = [*_agent_tools(context.agent), *(context.tools if isinstance(context.tools, (list, tuple)) else [])]
            for request in requests:
                run.answers[_call_id(request)] = await _decide(run.ctx, request, tools)
            answer = Message(role="user", contents=[r.to_function_approval_response(True) for r in requests])
            return await context.agent.run([answer], session=context.session)

        if getattr(context, "stream", False):
            context.stream_result_hooks.append(resolve)
        else:
            context.result = await resolve(context.result)


class _RouterFunction(_Router, FunctionMiddleware):
    async def process(self, context: FunctionInvocationContext, call_next: Callable[[], Any]) -> None:
        run = self._run()
        if run is None:
            await call_next()
            return

        async def approvals(index: int = 0) -> None:
            if index == len(run.approvals):
                await call_next()
            else:
                await run.approvals[index].process(context, lambda: approvals(index + 1))

        await _FunctionSignals(run).process(context, approvals)


_routing_lock = threading.Lock()
_routing: dict[int, tuple[BaseAgent, tuple[Any, ...], int]] = {}


def _route(agent: BaseAgent) -> None:
    with _routing_lock:
        known = _routing.get(id(agent))
        if known is not None:
            _routing[id(agent)] = (agent, known[1], known[2] + 1)
            return
        routers = (_RouterAgent(agent), _RouterChat(agent), _RouterFunction(agent))
        agent.middleware = [*routers, *(agent.middleware or [])]
        _routing[id(agent)] = (agent, routers, 1)


def _unroute(agent: BaseAgent) -> None:
    with _routing_lock:
        known = _routing.get(id(agent))
        if known is None:
            return
        if known[2] > 1:
            _routing[id(agent)] = (agent, known[1], known[2] - 1)
            return
        del _routing[id(agent)]
        agent.middleware = [m for m in agent.middleware or [] if not any(m is r for r in known[1])]


def _reporter(run: _Run) -> list[Any]:
    return [_RunScope(run), _ChatSignals(run), _FunctionSignals(run)]


def step_reporter(ctx: TaskContext) -> list[Any]:
    """The middleware that reports one task's model and tool steps to the mesh and enforces per-tool scopes, in the
    run and in the agents it runs as tools: pass it first in the run's middleware, ``agent.run(text,
    middleware=[*step_reporter(ctx), ...])`` (``run_agent`` does this, and also asks its approvals in those agents)."""
    return _reporter(_Run(ctx))
