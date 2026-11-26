# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A scripted agent loop that does nothing but emit OpenTelemetry GenAI spans, as an instrumented framework does.

Each turn is ``(text, [(call_id, tool_name, args), ...], (input_tokens, output_tokens))``. The model's output goes on
the chat span as ``gen_ai.output.messages`` (the latest GenAI conventions, as Agent Framework writes it); tool calls
of one turn run in parallel on worker threads, each in an ``execute_tool`` span. ``content=False`` leaves prompts,
outputs, arguments and results off the spans, as frameworks do by default when sensitive data is off.
"""

from __future__ import annotations

import asyncio
import inspect
import json
from collections.abc import Callable
from typing import Any

from opentelemetry import context as otel_context
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.trace import Status, StatusCode


class ScriptedSpans:
    def __init__(
        self,
        provider: TracerProvider,
        turns: list,
        tools: dict[str, Callable[..., Any]],
        *,
        model: str = "fake-model",
        content: bool = True,
    ):
        self.tracer = provider.get_tracer("scripted-framework")
        self.turns, self.tools, self.model, self.content = list(turns), tools, model, content
        self.ran: list[tuple[str, dict]] = []

    async def run(self, prompt: str = "") -> str:
        with self.tracer.start_as_current_span(
            "invoke_agent scripted", attributes={"gen_ai.operation.name": "invoke_agent"}
        ):
            while self.turns:
                text, calls, (i, o) = self.turns.pop(0)
                self._chat(text, calls, i, o)
                if not calls:
                    return text
                await asyncio.gather(*(self._tool(*call) for call in calls))
            return ""

    def _chat(self, text: str, calls: list, i: int, o: int) -> None:
        attributes = {"gen_ai.operation.name": "chat", "gen_ai.request.model": self.model}
        with self.tracer.start_as_current_span(f"chat {self.model}", attributes=attributes) as span:
            parts = ([{"type": "text", "content": text}] if text else []) + [
                {"type": "tool_call", "id": cid, "name": name, "arguments": args} for cid, name, args in calls
            ]
            if self.content:
                span.set_attribute("gen_ai.output.messages", json.dumps([{"role": "assistant", "parts": parts}]))
            span.set_attributes({"gen_ai.usage.input_tokens": i, "gen_ai.usage.output_tokens": o})

    def _tool_span(self, call_id: str, name: str, args: dict, parent: Any):
        attributes = {"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": name, "gen_ai.tool.call.id": call_id}
        span = self.tracer.start_as_current_span(
            f"execute_tool {name}",
            context=parent,
            attributes=attributes,
            record_exception=False,
            set_status_on_exception=False,
        )
        self.ran.append((name, args))
        return span

    def _finish(self, span: Any, args: dict, call: Callable[[], Any]) -> Any:
        if self.content:
            span.set_attribute("gen_ai.tool.call.arguments", json.dumps(args))
        try:
            result = call()
        except Exception as exc:
            span.set_status(Status(StatusCode.ERROR, str(exc)))
            return None
        if self.content:
            span.set_attribute("gen_ai.tool.call.result", json.dumps(result))
        return result

    async def _tool(self, call_id: str, name: str, args: dict) -> None:
        tool = self.tools[name]
        if inspect.iscoroutinefunction(tool):
            with self._tool_span(call_id, name, args, None) as span:
                if self.content:
                    span.set_attribute("gen_ai.tool.call.arguments", json.dumps(args))
                result = await tool(**args)
                if self.content:
                    span.set_attribute("gen_ai.tool.call.result", json.dumps(result))
            return
        parent = otel_context.get_current()

        def execute() -> None:
            with self._tool_span(call_id, name, args, parent) as span:
                self._finish(span, args, lambda: tool(**args))

        await asyncio.get_running_loop().run_in_executor(None, execute)
