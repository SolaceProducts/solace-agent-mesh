# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Run a LangChain chat model with tools on a mesh task: a minimal tool-calling agent loop on langchain-core, and a
LangGraph graph."""

from __future__ import annotations

import uuid
from collections.abc import Mapping, Sequence
from typing import Any

from langchain_core.callbacks import BaseCallbackManager
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import AIMessage, AIMessageChunk, BaseMessage, HumanMessage, SystemMessage, ToolMessage
from langchain_core.runnables import Runnable, RunnableConfig
from langchain_core.tools import BaseTool

from .._adapter import ToolAccessDenied, decide_approval, halts_bare, input_text, join_prompts, request_prompt
from ..context import TaskContext, TaskHalted, bind_task
from ..limits import LimitReached
from ..memory import ConversationMemory
from ..stop import AgentStopped
from .steps import SamLinkCallbackHandler, _message_text, step_reporter


@halts_bare
async def run_agent(
    ctx: TaskContext,
    model: BaseChatModel,
    tools: Sequence[BaseTool] = (),
    *,
    input: str | None = None,
    memory: ConversationMemory | None = None,
    include_request_context: bool = False,
    max_model_calls: int = 10,
    stream_text: bool = True,
    system_prompt: str = "",
) -> str:
    """Run model → tools → model on the task's input (or `input`) until the model answers without tool calls;
    returns the answer text.

    Every model and tool step is reported to the mesh (``step_reporter``). With `include_request_context`,
    the gateway's system purpose, response format, user profile and conversation context (`ctx.request_context`)
    are appended to the system prompt as SAM-Go agents do. With `memory`, the conversation's earlier turns (keyed
    by ``ctx.session_id``) are replayed before the new message, and this turn is remembered once it finishes; a
    run-based task (a peer call or a workflow node) neither reads nor adds history. A tool whose required scopes
    (AgentConfig.tools) the task's user lacks is not offered to the model, and a call to it anyway is refused. A run
    that is still calling tools after `max_model_calls` model calls fails the task saying so.

    With `stream_text` (the default) the model is streamed and its text goes to the user as it is written
    (``ctx.emit_text``); pass False to send the answer whole when the run ends.
    """
    with bind_task(ctx):
        return await _run(
            ctx, model, tools, system_prompt, input, memory, include_request_context, max_model_calls, stream_text
        )


async def _call_model(
    ctx: TaskContext, model: Runnable, messages: list[BaseMessage], config: RunnableConfig, stream_text: bool
) -> AIMessage:
    if not stream_text:
        ai = await model.ainvoke(messages, config=config)
        assert isinstance(ai, AIMessage)
        return ai
    whole: AIMessageChunk | None = None
    async for chunk in model.astream(messages, config=config):
        whole = chunk if whole is None else whole + chunk
        delta = _message_text(chunk)
        if delta:
            await ctx.emit_text(delta)
    if whole is None:
        raise RuntimeError("the model streamed no chunks")
    return whole


async def _run(
    ctx: TaskContext,
    model: BaseChatModel,
    tools: Sequence[BaseTool],
    system_prompt: str,
    input: str | None,
    memory: ConversationMemory | None,
    include_request_context: bool,
    max_model_calls: int,
    stream_text: bool,
) -> str:
    reporter = step_reporter(ctx)
    config: RunnableConfig = {"callbacks": [reporter]}
    by_name = {t.name: t for t in tools}
    offered = reporter.scopes.visible(tools)
    bound = model.bind_tools(offered) if offered else model
    messages: list[BaseMessage] = []
    system_prompt = join_prompts(system_prompt, request_prompt(ctx, include_request_context))
    if system_prompt:
        messages.append(SystemMessage(system_prompt))
    prior = memory.history(ctx.session_id) if memory is not None else []
    turn_start = len(messages) + len(prior)
    text = input_text(ctx, input)
    messages.extend(prior)
    messages.append(HumanMessage(text))

    def finish(answer: str) -> str:
        if memory is not None:
            memory.remember(ctx.session_id, messages[turn_start:], text, answer)
        return answer

    for _ in range(max_model_calls):
        ai = await _call_model(ctx, bound, messages, config, stream_text)
        messages.append(ai)
        if not ai.tool_calls:
            return finish(_message_text(ai))
        for call in ai.tool_calls:
            tool = by_name.get(call["name"])
            if tool is None:
                messages.append(ToolMessage(f"unknown tool {call['name']}", tool_call_id=call["id"], status="error"))
                continue
            try:
                result = await tool.ainvoke(call, config=config)
            except ToolAccessDenied as denied:
                result = ToolMessage(str(denied), tool_call_id=call["id"], status="error")
            except (TaskHalted, AgentStopped):
                raise
            except Exception as exc:
                result = ToolMessage(f"tool error: {exc}", tool_call_id=call["id"], status="error")
            messages.append(result)
    raise LimitReached(f"Stopped: the agent reached its limit of {max_model_calls} model calls (max_model_calls).")


INTERRUPT_KEY = "__interrupt__"
NOT_AN_APPROVAL = (
    "the graph interrupted with a value that is not an approval request ({value!r}): interrupt with "
    '{{"tool": <name>, "args": {{...}}, "message": <text>}} to ask the user to approve a call'
)


async def _decide(ctx: TaskContext, value: Any) -> str | None:
    """None when the user approves the call the interrupt asks about, else why it is refused."""
    if not isinstance(value, Mapping) or not value.get("tool"):
        raise ValueError(NOT_AN_APPROVAL.format(value=value))
    name = str(value["tool"])
    return await decide_approval(ctx, name, dict(value.get("args") or {}), str(value.get("message") or ""))


async def _invoke(ctx: TaskContext, graph: Any, input: Any, config: Mapping[str, Any], stream_text: bool) -> Any:
    """Run the graph and return what `ainvoke` would: the last full state.

    The interrupts the run ended on are added under INTERRUPT_KEY. With `stream_text`, the model's text is sent as it
    streams; only AI messages are, so a tool's output or the user's input never is.
    """
    if not stream_text:
        return await graph.ainvoke(input, config)
    latest: Any = None
    interrupts: list[Any] = []
    async for mode, payload in graph.astream(input, config, stream_mode=["messages", "updates", "values"]):
        if mode == "messages":
            message, _ = payload
            delta = _message_text(message) if isinstance(message, AIMessage) else ""
            if delta:
                await ctx.emit_text(delta)
        elif mode == "updates":
            if isinstance(payload, Mapping) and payload.get(INTERRUPT_KEY) is not None:
                interrupts.extend(payload[INTERRUPT_KEY])
        elif mode == "values":
            latest = payload
    if not interrupts:
        return latest
    return {**latest, INTERRUPT_KEY: interrupts} if isinstance(latest, Mapping) else {INTERRUPT_KEY: interrupts}


def _with_reporter(ctx: TaskContext, config: Mapping[str, Any]) -> dict[str, Any]:
    """`config` with this task's step reporter among its callbacks, unless one is there already."""
    config = dict(config)
    callbacks = config.get("callbacks")
    if isinstance(callbacks, BaseCallbackManager):
        if not any(isinstance(h, SamLinkCallbackHandler) for h in callbacks.handlers):
            callbacks = callbacks.copy()
            callbacks.add_handler(step_reporter(ctx))
    else:
        handlers = list(callbacks or [])
        if not any(isinstance(h, SamLinkCallbackHandler) for h in handlers):
            handlers.append(step_reporter(ctx))
        callbacks = handlers
    config["callbacks"] = callbacks
    return config


@halts_bare
async def run_graph(
    ctx: TaskContext,
    graph: Any,
    *,
    input: Any = None,
    config: Mapping[str, Any] | None = None,
    stream_text: bool = True,
) -> Any:
    """``graph.ainvoke(input, config)`` for a mesh task, asking the user at each approval the graph interrupts with.
    `input` is the graph's input, by default ``{"messages": [HumanMessage(ctx.text)]}`` for a graph on LangGraph's
    ``MessagesState``.

    A node or tool asks by calling ``langgraph.types.interrupt({"tool": "route_claim", "args": {...}, "message":
    "..."})`` (`args` and `message` are optional). The run pauses there, as LangGraph pauses at any interrupt; this
    asks the task's user through SAM (as ``with_approval`` does, the arguments redacted) and resumes the graph on the
    same thread (``Command(resume=...)``), until it ends. The interrupt returns ``True`` when the user approved the
    call, else the text to tell the model, the same text ``with_approval`` gives for a refusal (the user denied or
    cancelled, the prompt timed out, the channel cannot show one, the task was stopped): the node or tool then does
    not run the call and returns that text. Several interrupts at once are asked in order.

    The run reports its steps to the mesh: the step reporter (``step_reporter(ctx)``) is added to `config`'s callbacks
    unless it is there already. Unless `config` names a thread (``{"configurable": {"thread_id": ...}}``), the thread
    is the SAM session (``ctx.session_id``), so a graph compiled with a checkpointer keeps the chat's conversation and
    can resume after an interrupt; a run-based task (a peer call or a workflow node) runs on a thread of its own, new
    for each delivery, which is deleted from the graph's checkpointer when the run ends. A tool the user lacks the
    scopes for is refused before its body runs (the step reporter), so it never reaches its interrupt. An interrupt
    that is not an approval request raises a ValueError. Returns what the graph returns.

    With `stream_text` (the default) the graph runs with ``astream`` and the text of the AI messages the model writes
    goes to the user as it is written (``ctx.emit_text``), a resumed run included; it needs a model that streams
    (``ChatOpenAI`` streams inside a node that calls ``ainvoke``, and reports usage with ``stream_usage=True``). Pass
    False to send the answer whole when the run ends.

    A run-based task's thread is new for each delivery because a run given up may still write to its thread while SAM
    delivers the task again.
    """
    from langgraph.checkpoint.base import BaseCheckpointSaver
    from langgraph.types import Command

    if input is None:
        input = {"messages": [HumanMessage(ctx.text)]}
    config = _with_reporter(ctx, config or {})
    configurable = dict(config.get("configurable") or {})
    throwaway = ""
    if not configurable.get("thread_id"):
        throwaway = "" if ctx.session_id else f"{ctx.task_id}-{uuid.uuid4().hex[:12]}"
        configurable["thread_id"] = ctx.session_id or throwaway
        config["configurable"] = configurable
    try:
        with bind_task(ctx):
            result = await _invoke(ctx, graph, input, config, stream_text)
            while isinstance(result, Mapping) and result.get(INTERRUPT_KEY):
                answers = {}
                for interrupt in result[INTERRUPT_KEY]:
                    answers[interrupt.id] = await _decide(ctx, interrupt.value) or True
                result = await _invoke(ctx, graph, Command(resume=answers), config, stream_text)
            return result
    finally:
        saver = getattr(graph, "checkpointer", None)
        if throwaway and isinstance(saver, BaseCheckpointSaver):
            await saver.adelete_thread(throwaway)
