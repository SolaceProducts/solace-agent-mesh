# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The driver: the SDK's own MeshAgent over LinkTransport (agent link protocol 2), exactly as an agent author runs it.

The agent reports each step as a semantic step event (``TaskContext.emit_event``), with the values as the script gives
them: nothing is redacted or capped on the agent's side, so what SAM records is what SAM's entrypoint made of it.

All agents run on one background event loop, so the scenarios stay plain synchronous tests that drive SAM over HTTP
while the agents serve tasks.
"""

from __future__ import annotations

import asyncio
import threading
from collections.abc import Coroutine
from typing import Any

from sam_link_sdk import AgentConfig, MeshAgent, Option, Question, TaskContext, TaskResult, ToolInfo, Usage
from sam_link_sdk.events import ModelFinished, ModelStarted, ToolCall as StepToolCall, ToolFinished, ToolStarted
from sam_link_sdk.link import LinkConfig, LinkError, LinkTransport
from sam_link_sdk.link.connection import LinkConnection

from .driver import AgentSpec, ConnectOutcome, LinkDriver, PeerOutcome, RunningAgent, ScriptTask, ToolCall

LOOP_CALL_TIMEOUT_SECONDS = 60


class _BackgroundLoop:
    def __init__(self) -> None:
        self.loop = asyncio.new_event_loop()
        self._thread = threading.Thread(target=self.loop.run_forever, name="contract-agents", daemon=True)
        self._thread.start()

    def run(self, coro: Coroutine[Any, Any, Any], timeout: float = LOOP_CALL_TIMEOUT_SECONDS) -> Any:
        return asyncio.run_coroutine_threadsafe(coro, self.loop).result(timeout)

    def close(self) -> None:
        self.loop.call_soon_threadsafe(self.loop.stop)
        self._thread.join(timeout=10)


async def ask_questions(ctx: TaskContext, questions, timeout_seconds: float):
    asked = [
        Question(q[0], tuple(Option(label) for label in q[1]), multi_select=len(q) > 2 and bool(q[2]))
        for q in questions
    ]
    answer = await ctx.ask_user(asked, timeout_seconds=timeout_seconds)
    return answer.answers if answer.answered else None


class _V2Task(ScriptTask):
    def __init__(self, ctx: TaskContext, agent: MeshAgent):
        self._ctx = ctx
        self._agent = agent
        self.text = ctx.text
        self.task_id = ctx.task_id
        self.user_id = ctx.user_id
        node = ctx.invocation
        self.workflow = (node["workflow_name"], node["node_id"]) if node else None
        self.input = ctx.structured_input
        self.files = tuple((f.name, f.content is not None, f.uri or "") for f in ctx.files)
        self.file_bytes = {f.name: f.content for f in ctx.files if f.content is not None}
        self.output = None

    async def model_call(
        self,
        model: str,
        *,
        text: str = "",
        tool_calls: tuple[ToolCall, ...] = (),
        input_tokens: int = 0,
        output_tokens: int = 0,
        cached_input_tokens: int = 0,
        cache_write_tokens: int = 0,
    ) -> None:
        """Report a model call as the framework adapters do: its response, carrying its usage."""
        ctx = self._ctx
        await ctx.emit_event(ModelStarted(model))
        usage = Usage(
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            cached_input_tokens=cached_input_tokens,
            cache_write_tokens=cache_write_tokens,
        )
        calls = tuple(StepToolCall(c.id, c.name, c.args) for c in tool_calls)
        await ctx.emit_event(ModelFinished(model, text, calls, usage))

    def record_usage(self, model: str, *, input_tokens: int = 0, output_tokens: int = 0) -> None:
        self._ctx.record_usage(model, Usage(input_tokens=input_tokens, output_tokens=output_tokens))

    async def tool_call(self, call: ToolCall, result: object) -> None:
        await self._ctx.emit_event(ToolStarted(call.id, call.name, call.args))
        await self._ctx.emit_event(ToolFinished(call.id, call.name, result))

    def attach(self, name: str, content: bytes, mime_type: str = "") -> None:
        self._ctx.attach(name, content, mime_type)

    async def stream(self, text: str) -> None:
        await self._ctx.emit_text(text)

    async def request_approval(self, tool: str, args: dict, timeout_seconds: float = 120) -> bool:
        result = await self._ctx.request_approval(tool, args, timeout_seconds=timeout_seconds)
        return result.approved

    async def ask_questions(self, questions, timeout_seconds: float = 120):
        return await ask_questions(self._ctx, questions, timeout_seconds)

    async def call_peer(
        self, peer: str, text: str, *, call_id: str = "", skip_local_checks: bool = False
    ) -> PeerOutcome:
        """Call a peer without checking it against SAM's list.

        The SDK only calls agents SAM has listed to it; this sends the call regardless.
        """
        if skip_local_checks:
            self._agent._transport._assume_peer(peer)
        result = await self._ctx.call_agent(peer, text, function_call_id=call_id)
        return PeerOutcome(result.ok, result.text)

    async def wait_cancelled(self, timeout_seconds: float) -> bool:
        await asyncio.sleep(timeout_seconds)
        return False

    def tool_permitted(self, tool: str) -> bool:
        return self._ctx.check_tool(tool)


class _V2Agent(RunningAgent):
    def __init__(self, driver: V2Driver, spec: AgentSpec, token: str):
        super().__init__()
        self.name = spec.name
        self._driver = driver
        self._closed = False
        config = AgentConfig(
            name=spec.name,
            description=spec.description,
            tools=[
                ToolInfo(name=n, description=d, required_scopes=list(spec.tool_scopes.get(n, ())))
                for n, d in spec.tools
            ],
            owner=spec.owner,
            framework=spec.framework,
            peers=list(spec.peers),
            keepalive_seconds=0,
            shutdown_grace_seconds=5,
            max_concurrent_tasks=spec.max_concurrent_tasks,
            **spec.config,
        )
        link = LinkTransport(driver.link_config(spec.name, token))
        connection = link._connection
        send = connection.send

        async def recorded(frame, **kw):
            self.sent.append((connection.connection_id, str(frame.get("type"))))
            return await send(frame, **kw)

        connection.send = recorded
        self.agent = MeshAgent(config, self._handle_with(spec), transport=link)
        self.agent.on_stop_change(lambda allowed, reason: self.control.append((allowed, reason)))

    def _handle_with(self, spec: AgentSpec):
        async def handle(ctx: TaskContext) -> str:
            rec = self.record(ctx.task_id, ctx.text)
            rec.started.set()
            try:
                task = _V2Task(ctx, self.agent)
                text = await spec.handler(task)
                return TaskResult(text, data=task.output) if task.output is not None else text
            except asyncio.CancelledError:
                rec.cancelled = True
                raise
            except Exception as exc:
                rec.error = f"{type(exc).__name__}: {exc}"
                raise
            finally:
                rec.finished.set()

        return handle

    async def start(self) -> None:
        await self.agent.start()

    def drop_connection(self, offline_seconds: float = 0.0) -> None:
        connection = self.agent._transport._connection

        async def drop() -> None:
            if offline_seconds:
                reopen = connection._open

                async def after_the_outage() -> None:
                    connection._open = reopen
                    await asyncio.sleep(offline_seconds)
                    await reopen()

                connection._open = after_the_outage
            await connection._ws.close(code=1011)

        self._driver.loop.run(drop())

    def leave(self, timeout: float = 30) -> None:
        if not self._closed:
            self._closed = True
            self._driver.loop.run(self.agent.stop(), timeout)

    def close(self, timeout: float = 30) -> None:
        """A failed stop must not hide the scenario's own result."""
        try:
            self.leave(timeout)
        except Exception:
            pass


class V2Driver(LinkDriver):
    def __init__(self, link_url: str, namespace: str):
        super().__init__(link_url, namespace)
        self.loop = _BackgroundLoop()
        self._agents: list[_V2Agent] = []

    def link_config(self, agent: str, token: str) -> LinkConfig:
        return LinkConfig(url=self.link_url, agent=agent, token=token, connect_timeout_seconds=10)

    def start_agent(self, spec: AgentSpec, token: str) -> RunningAgent:
        agent = _V2Agent(self, spec, token)
        self.loop.run(agent.start())
        self._agents.append(agent)
        return agent

    def try_connect(self, agent: str, token: str) -> ConnectOutcome:
        async def attempt() -> ConnectOutcome:
            connection = LinkConnection(self.link_config(agent, token))
            try:
                await connection.connect()
            except LinkError as exc:
                return ConnectOutcome(False, str(exc))
            await connection.disconnect()
            return ConnectOutcome(True, f"connected on protocol {connection.protocol}")

        return self.loop.run(attempt())

    def close(self) -> None:
        for agent in self._agents:
            agent.close()
        self.loop.close()
