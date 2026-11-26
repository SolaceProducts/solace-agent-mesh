# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""OpenAI Agents SDK RunHooks that report model and tool steps to the mesh task, and the copy of the agent they need.

The SDK's hooks see neither a tool's exception (the tool turns it into the text the model reads) nor a way to refuse a
call, so the hooks come with a copy of the agent, and of every agent it hands off to, prepared for one task:

- each function tool reports its failures to the hooks (``_Guard._tool``);
- a streamed response asks the provider for its token usage unless the agent's model settings say otherwise;
- a tool the task's user lacks the scopes for is left out, so the model is not offered it; a call to it anyway is a
  tool the SDK does not find, which ``run_agent`` refuses with SAM's text, and one the SDK runs regardless (an MCP
  server's tool) ends the run before it runs (``ToolAccessDenied``);
- an agent used as a tool (``Agent.as_tool``) runs its own ``Runner``, which gets only the hooks given to
  ``as_tool``. So each call of such a tool runs, instead, a copy of that agent prepared like the first, with hooks of
  its own for that run that report to the same task (``SamLinkRunHooks._nested``), then the ``as_tool`` hooks if any:
  its model calls count toward ``max_llm_calls``, its tools are hidden, refused and asked about as the first agent's,
  and a stop of the task stops it. ``as_tool`` keeps none of what it was given anywhere but in the closure of the
  function the tool runs, so the copy runs that function's own code over a closure with the prepared agent, the hooks
  and the run config swapped in (``_check_as_tool`` fails the import's first use if that closure changes). Agents
  handed off to, directly or through ``handoff(...)``, run in the same run and are prepared like the first.

The hooks refuse to serve an agent they did not prepare, so a run cannot quietly go without all this.
"""

from __future__ import annotations

import copy
import dataclasses
import inspect
import logging
import types
from collections.abc import Callable
from typing import Any

from agents import Agent, FunctionTool, Handoff, ModelSettings, RunConfig, RunHooks
from agents.lifecycle import RunHooksBase
from agents.tool import (
    _FailureHandlingFunctionToolInvoker,
    default_tool_timeout_error_message,
    resolve_function_tool_failure_error_function,
    set_function_tool_failure_error_function,
)

from .._adapter import (
    INNER_ATTR,
    NestedRunRefused,
    PendingCalls,
    Steps,
    ToolAccessDenied,
    ToolScopes,
    access_denied,
    check_running,
    check_usage_read,
    join_prompts,
    mark_scoped,
    new_call_id,
    parse_args,
    plain_result,
    reported_result,
    request_prompt,
    scopes_of,
    unwrapped,
    usage_of,
)
from .._drift import FrameworkDrift, installed_version
from ..context import TaskContext, tool_name
from ..usage import Usage
from .tools import REWRAP_ATTR

log = logging.getLogger("sam_link_sdk.openai_agents")

UNPREPARED = (
    "the run hooks were given an agent step_reporter did not prepare: run hooks.agent, "
    "hooks = step_reporter(ctx, agent); Runner.run(hooks.agent, input, hooks=hooks)"
)


def _reporting_usage(settings: ModelSettings) -> ModelSettings:
    """A streamed response carries its token usage only if the request asks for it, and the SDK asks only of OpenAI's
    own endpoint: any other OpenAI-compatible one (LiteLLM, vLLM) would report none. An explicit setting is kept."""
    return settings if settings.include_usage is not None else dataclasses.replace(settings, include_usage=True)


def _model_name(agent: Agent, run_config: RunConfig | None) -> str:
    model = (run_config.model if run_config and run_config.model else None) or agent.model
    if model is None:
        return "default"
    if isinstance(model, str):
        return model
    return str(getattr(model, "model", None) or type(model).__name__)


def _usage_from(response: Any) -> Usage:
    """A response's usage in the SDK's own terms.

    The Agents SDK's Usage follows OpenAI's: `input_tokens` holds `input_tokens_details.cached_tokens` (reads) and
    `cache_write_tokens` (writes; its LiteLLM model fills them only when LiteLLM names them `cache_write_tokens`).
    """
    u = getattr(response, "usage", None)
    if u is None:
        return Usage()
    in_details = getattr(u, "input_tokens_details", None)
    out_details = getattr(u, "output_tokens_details", None)
    return usage_of(
        input=getattr(u, "input_tokens", 0),
        output=getattr(u, "output_tokens", 0),
        cache_read=getattr(in_details, "cached_tokens", 0),
        cache_write=getattr(in_details, "cache_write_tokens", 0),
        reasoning=getattr(out_details, "reasoning_tokens", 0),
        total=getattr(u, "total_tokens", 0),
        input_includes_cache=True,
    )


def _result_data(result: Any) -> Any:
    dump = getattr(result, "model_dump", None)
    if not isinstance(result, str) and callable(dump):
        return dump(mode="json")
    return plain_result(result)


def _tool_result_data(result: Any, failed: bool) -> Any:
    """The result to report: what the model was given. A failed tool is reported as {"error": <that text>}, so
    the Activity Monitor and the audit trail record it as failed."""
    return reported_result(result if failed and isinstance(result, str) else _result_data(result), failed)


def _is_agent_tool(tool: Any) -> bool:
    """Whether `tool` was made with ``Agent.as_tool`` (the SDK marks it so, privately)."""
    return isinstance(getattr(tool, "_agent_instance", None), Agent) or bool(getattr(tool, "_is_agent_tool", False))


AGENT_TOOL_CLOSURE = ("self", "hooks", "run_config")
"""What the function an ``Agent.as_tool`` tool runs holds in its closure: the agent, the hooks and the run config the
nested Runner is given.
"""
_AS_TOOL_CHECKED = False


def _closure(function: Any) -> dict[str, Any]:
    code = getattr(function, "__code__", None)
    cells = getattr(function, "__closure__", None) or ()
    return dict(zip(code.co_freevars, cells, strict=False)) if code is not None else {}


def _check_as_tool() -> None:
    """``Agent.as_tool`` keeps what it was given only in the closure of the function its tool runs, which the SDK does
    not promise, so the first lookup builds a throwaway agent tool and checks it still holds them that way."""
    global _AS_TOOL_CHECKED
    if _AS_TOOL_CHECKED:
        return
    from . import TESTED_VERSION

    probe, hooks, run_config = Agent(name="probe"), RunHooks(), RunConfig()
    invoker = probe.as_tool("probe", "probe", hooks=hooks, run_config=run_config).on_invoke_tool
    cells = _closure(getattr(invoker, "_invoke_tool_impl", None))
    held = {"self": probe, "hooks": hooks, "run_config": run_config}
    missing = []
    if not isinstance(invoker, _FailureHandlingFunctionToolInvoker):
        missing.append("Agent.as_tool()'s tool invoker _FailureHandlingFunctionToolInvoker")
    missing += [
        f"Agent.as_tool()'s {name} in the closure of the function its tool runs"
        for name in AGENT_TOOL_CLOSURE
        if name not in cells or cells[name].cell_contents is not held[name]
    ]
    if missing:
        raise FrameworkDrift(
            "sam_link_sdk.openai_agents", "openai-agents", installed_version("openai-agents"), TESTED_VERSION, missing
        )
    _AS_TOOL_CHECKED = True


def _with_closure(function: Any, **values: Any) -> Any:
    """A copy of `function` whose closure holds `values` in place of the variables of those names."""
    cells = tuple(
        types.CellType(values[name]) if name in values else cell
        for name, cell in zip(function.__code__.co_freevars, function.__closure__, strict=True)
    )
    copied = types.FunctionType(
        function.__code__, function.__globals__, function.__name__, function.__defaults__, cells
    )
    copied.__kwdefaults__ = function.__kwdefaults__
    copied.__dict__.update(function.__dict__)
    return copied


class _Both(RunHooks):
    """SAM's hooks for a nested run, then the hooks given to ``Agent.as_tool``."""

    def __init__(self, sam: RunHooks, theirs: RunHooks):
        self._sam = sam
        self._theirs = theirs


def _both(name: str) -> Callable[..., Any]:
    async def call(self: _Both, *args: Any, **kwargs: Any) -> None:
        await getattr(self._sam, name)(*args, **kwargs)
        await getattr(self._theirs, name)(*args, **kwargs)

    return call


for _hook in [name for name in vars(RunHooksBase) if name.startswith("on_")]:
    setattr(_Both, _hook, _both(_hook))


def _with_request_context(agent: Agent, prompt: str) -> Agent:
    base = agent.instructions
    if base is None or isinstance(base, str):
        return agent.clone(instructions=join_prompts(base or "", prompt))

    async def instructions(run_context, current_agent):
        resolved = base(run_context, current_agent)
        if inspect.isawaitable(resolved):
            resolved = await resolved
        return join_prompts(str(resolved or ""), prompt)

    return agent.clone(instructions=instructions)


class _Guard:
    """Prepares an agent, and the agents it hands off to, for one task's run (see the module docstring).

    Function tools copied here keep their own formatter, so the model gets the same text; the copy records the call
    id of a failed call first. The SDK catches a function tool's exception (and, with
    ``timeout_behavior="error_as_result"``, its timeout) inside the tool, so this is the only place a failure shows. A
    tool with an output schema (programmatic tool calling) is left as it is, because the SDK treats its default failure
    text specially; its failures show as results.
    """

    def __init__(
        self,
        scopes: ToolScopes,
        failed: set[str],
        nested: Callable[[Agent, RunConfig], SamLinkRunHooks],
        prompt: str = "",
    ):
        self._scopes = scopes
        self._prompt = prompt
        self._failed = failed
        self._nested = nested
        self._prepared: dict[int, tuple[Agent, Agent]] = {}
        """Id of an agent given -> (that agent, its prepared copy); the agent is held so its id is not reused."""
        self._copies: set[int] = set()

    def prepared(self, agent: Any) -> bool:
        return id(agent) in self._copies

    def agent(self, agent: Agent) -> Agent:
        if id(agent) in self._copies:
            return agent
        known = self._prepared.get(id(agent))
        if known is not None:
            return known[1]
        tracked = agent.clone(
            tools=[self._tool(t) for t in self._scopes.visible(agent.tools)],
            model_settings=_reporting_usage(agent.model_settings),
        )
        if self._prompt:
            tracked = _with_request_context(tracked, self._prompt)
        self._prepared[id(agent)] = (agent, tracked)
        self._copies.add(id(tracked))
        if agent.handoffs:
            tracked.handoffs = [self._handoff(h) for h in agent.handoffs]
        return tracked

    def _handoff(self, handoff: Any) -> Any:
        if isinstance(handoff, Agent):
            return self.agent(handoff)
        if not isinstance(handoff, Handoff):
            return handoff
        invoke = handoff.on_invoke_handoff
        guard = self

        async def to_prepared(context: Any, arguments: str) -> Any:
            return guard.agent(await invoke(context, arguments))

        prepared = copy.copy(handoff)
        prepared.on_invoke_handoff = to_prepared
        return prepared

    def _routed(self, tool: Any) -> Any:
        """`tool` with the agent behind it (``Agent.as_tool``) run on this task (see the module docstring), however
        many of this SDK's wrappers (``with_scopes``, ``with_approval``) stand in front of it; any other tool as it
        is."""
        behind = unwrapped(tool)
        if not _is_agent_tool(behind):
            return tool
        wrappers = []
        while tool is not behind:
            wrappers.append(tool)
            tool = tool.__dict__[INNER_ATTR]
        routed = self._route(behind)
        for wrapper in reversed(wrappers):
            rewrap = wrapper.__dict__.get(REWRAP_ATTR)
            if rewrap is None:
                raise NestedRunRefused(
                    f"the agent tool {tool_name(behind)!r} (Agent.as_tool) is behind a wrapper SAM cannot rebuild, so "
                    "SAM could not check or report the agent's run"
                )
            routed = mark_scoped(rewrap(routed), scopes_of(wrapper))
        return routed

    def _route(self, tool: FunctionTool) -> FunctionTool:
        _check_as_tool()
        invoker = tool.on_invoke_tool
        impl = getattr(invoker, "_invoke_tool_impl", None)
        cells = _closure(impl)
        target = cells["self"].cell_contents if "self" in cells else None
        if not isinstance(invoker, _FailureHandlingFunctionToolInvoker) or not (
            set(AGENT_TOOL_CLOSURE) <= set(cells) and isinstance(target, Agent)
        ):
            raise NestedRunRefused(
                f"the agent behind the tool {tool.name!r} cannot be found the way Agent.as_tool keeps it, so SAM "
                "could not check or report its run"
            )
        theirs: RunHooks | None = cells["hooks"].cell_contents
        own_config: RunConfig | None = cells["run_config"].cell_contents
        nested = self._nested

        async def run_on_the_task(tool_ctx: Any, raw_args: str) -> Any:
            """Run an `Agent.as_tool` tool's agent on the task, through hooks of its own.

            The nested run uses the config the tool was built with, else the calling run's (the tool context carries
            it).
            """
            base = own_config or getattr(tool_ctx, "run_config", None) or RunConfig(tracing_disabled=True)
            hooks = nested(target, base)
            run = _with_closure(
                impl,
                self=hooks.agent,
                hooks=hooks if theirs is None else _Both(hooks, theirs),
                run_config=hooks.refusing(base),
            )
            return await run(tool_ctx, raw_args)

        return dataclasses.replace(tool, on_invoke_tool=type(invoker)(run_on_the_task, invoker._on_handled_error))

    def _mark(self, context: Any) -> None:
        call_id = getattr(context, "tool_call_id", None)
        if call_id:
            self._failed.add(call_id)

    def _tool(self, tool: Any) -> Any:
        tool = self._routed(tool)
        if not isinstance(tool, FunctionTool) or tool.output_json_schema is not None:
            return tool
        tracked = copy.copy(tool)
        formatter = resolve_function_tool_failure_error_function(tool)
        if formatter is not None:

            def on_failure(context: Any, error: Exception) -> Any:
                self._mark(context)
                return formatter(context, error)

            set_function_tool_failure_error_function(tracked, on_failure)
        if tool.timeout_seconds is not None and tool.timeout_behavior == "error_as_result":
            on_timeout = tool.timeout_error_function
            timeout_seconds = tool.timeout_seconds

            def timed_out(context: Any, error: Exception) -> Any:
                self._mark(context)
                if on_timeout is not None:
                    return on_timeout(context, error)
                return default_tool_timeout_error_message(tool_name=tool.name, timeout_seconds=timeout_seconds)

            tracked.timeout_error_function = timed_out
        return tracked


def _reasoning_text(item: Any) -> str:
    return "\n".join(str(getattr(part, "text", "") or "") for part in getattr(item, "summary", None) or [])


class SamLinkRunHooks(RunHooks):
    """Reports each model call and its response, and each tool's start and result, for one Runner run of
    ``self.agent`` (what ``step_reporter`` returns; see the module docstring).

    The Agents SDK hands each function tool's call id to the tool hooks, so the tool steps pair with the model's
    function_call by id.
    """

    def __init__(
        self,
        ctx: TaskContext,
        agent: Agent,
        run_config: RunConfig | None = None,
        *,
        include_request_context: bool = False,
    ):
        self.ctx = ctx
        self.scopes = ToolScopes(ctx)
        self._steps = Steps(ctx)
        self._run_config = run_config
        self._model = "default"
        self._pending = PendingCalls()
        self._arguments: dict[str, dict] = {}
        self._failed: set[str] = set()
        self._guard = _Guard(self.scopes, self._failed, self._nested, request_prompt(ctx, include_request_context))
        self.agent: Agent = self._guard.agent(agent)
        """The agent to run with these hooks: a copy prepared for this task (see the module docstring)."""

    def _nested(self, agent: Agent, run_config: RunConfig) -> SamLinkRunHooks:
        """Hooks for one run of `agent` as a tool (``Agent.as_tool``) inside this one: they report to the same task,
        with the same scopes and failure tracking, and run `agent` prepared like this run's agents. A run of its own
        pairs its own model calls with its tool calls, so concurrent calls of the tool do not mix."""
        nested = copy.copy(self)
        nested._run_config = run_config
        nested._model = "default"
        nested._pending = PendingCalls()
        nested._arguments = {}
        nested.agent = self._guard.agent(agent)
        return nested

    def refusing(self, run_config: RunConfig) -> RunConfig:
        """`run_config` for this run, with a call to a tool the agent does not have given an error result instead of
        failing the run, as in the other adapters: SAM's refusal for a tool the user lacks the scopes for (it is not
        offered, so the SDK does not find it), else the SDK's own "not found" text (or `run_config`'s formatter)."""
        formatter = run_config.tool_error_formatter
        steps, scopes, pending, arguments = self._steps, self.scopes, self._pending, self._arguments

        async def format_error(args: Any) -> str | None:
            if args.kind == "tool_not_found" and args.tool_name in scopes.denied:
                call_id = pending.take(args.tool_name, args.call_id)
                await steps.refuse(args.tool_name, arguments.pop(call_id, None), call_id)
                return access_denied(args.tool_name)
            result = formatter(args) if formatter is not None else None
            return await result if inspect.isawaitable(result) else result

        replaced = copy.copy(run_config)
        replaced.tool_error_formatter = format_error
        replaced.tool_not_found_behavior = "return_error_to_model"
        return replaced

    async def on_agent_start(self, context, agent) -> None:
        if not self._guard.prepared(agent):
            raise RuntimeError(UNPREPARED)

    async def on_llm_start(self, context, agent, system_prompt, input_items) -> None:
        check_running(self.ctx)
        self._model = _model_name(agent, self._run_config)
        await self._steps.model_call(self._model)

    async def on_llm_end(self, context, agent, response) -> None:
        text_chunks: list[str] = []
        calls: list[dict] = []
        reasoning: list[str] = []
        for item in getattr(response, "output", None) or []:
            kind = getattr(item, "type", None)
            if kind == "reasoning":
                reasoning.append(_reasoning_text(item))
            elif kind == "message":
                for part in getattr(item, "content", None) or []:
                    if getattr(part, "type", None) == "output_text":
                        text_chunks.append(getattr(part, "text", "") or "")
            elif kind == "function_call":
                call_id = getattr(item, "call_id", None) or new_call_id()
                calls.append({"id": call_id, "name": item.name, "args": parse_args(getattr(item, "arguments", None))})
        self._pending.expect(calls)
        self._arguments.update((c["id"], c["args"]) for c in calls)
        text, usage = "".join(text_chunks), _usage_from(response)
        check_usage_read(
            "sam_link_sdk.openai_agents", self._model, getattr(response, "usage", None), usage, text, calls
        )
        await self._steps.model_answer(self._model, text, calls, usage, "\n".join(r for r in reasoning if r))

    async def on_tool_start(self, context, agent, tool) -> None:
        check_running(self.ctx)
        name = getattr(tool, "name", type(tool).__name__)
        call_id = self._pending.take(name, getattr(context, "tool_call_id", None) or "")
        args = parse_args(getattr(context, "tool_arguments", None))
        self._arguments.pop(call_id, None)
        if not self.scopes.permits(name):
            await self._steps.refuse(name, args, call_id)
            raise ToolAccessDenied(name)
        await self._steps.tool_start(name, args, call_id)

    async def on_tool_end(self, context, agent, tool, result) -> None:
        name = getattr(tool, "name", type(tool).__name__)
        call_id = getattr(context, "tool_call_id", None) or ""
        call_failed = call_id in self._failed
        self._failed.discard(call_id)
        await self._steps.tool_end(name, _tool_result_data(result, call_failed), call_id)

    async def on_handoff(self, context, from_agent, to_agent) -> None:
        await self.ctx.progress(f"Handing off to {getattr(to_agent, 'name', 'agent')}")


def step_reporter(
    ctx: TaskContext, agent: Agent, *, run_config: RunConfig | None = None, include_request_context: bool = False
) -> SamLinkRunHooks:
    """The run hooks that report one task's model and tool steps to the mesh, for a run of `agent`:
    ``hooks = step_reporter(ctx, agent); Runner.run(hooks.agent, text, hooks=hooks)`` (``run_agent`` does this).
    ``hooks.agent`` is `agent` prepared for this task (see the module docstring); the hooks refuse to run any other.
    `run_config` is the run's, so the hooks name the model it sets. With `include_request_context`, the gateway's
    request context is appended to the instructions of every agent the run prepares, handoff targets and agents run as
    tools included."""
    return SamLinkRunHooks(ctx, agent, run_config, include_request_context=include_request_context)
