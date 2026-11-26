# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The test vehicle: a real MeshAgent over a Transport that records what the agent tells SAM, as semantic values.

``RecordingTransport`` implements every Transport method and records it: the step events a task sends, its prompts,
its final answer, its peer calls, the agent's descriptions and withdrawals, the deliveries given back and the usage
reported. A test drives it the way SAM would: ``deliver`` a task, ``cancel`` it, ``answer`` a prompt, ``reply`` to a
peer call, ``control`` (SAM's stop and release), ``disconnect`` and ``reconnect``.

``start_agent`` builds a MeshAgent over it. ``task_context`` gives an adapter test the real TaskContext of a task that
real agent runs: steps the adapter reports reach the transport through the agent's own channel, and ``MeshTask.run``
runs a coroutine inside the task's run, as a handler would.

The real wire (frames, reconnects, held answers, limits) is tested against the in-process entrypoint in
tests/test_link.py instead.
"""

from __future__ import annotations

import asyncio
import contextlib
import inspect
import tempfile
from collections.abc import AsyncIterator, Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any

from sam_link_sdk import AgentConfig, MeshAgent
from sam_link_sdk._model import (
    AgentDescription,
    IncomingTask,
    PeerCall,
    PeerInfo,
    PeerResult,
    Prompt,
    PromptAnswer,
    TaskOutcome,
    TransportHost,
)
from sam_link_sdk.config import ToolInfo
from sam_link_sdk.context import TaskContext
from sam_link_sdk.events import StepEvent
from sam_link_sdk.usage import Usage

AGENT = "claims-triage"

Handler = Callable[[TaskContext], Any]


@dataclass
class Asked:
    """A prompt the agent sent for a task, waiting for its answer."""

    task_id: str
    prompt: Prompt
    answer: asyncio.Future = field(repr=False)


@dataclass
class Called:
    """A peer call the agent made for a task, waiting for its result."""

    call: PeerCall
    result: asyncio.Future = field(repr=False)


class RecordingTransport:
    """A Transport that records every call as a semantic value and lets a test act as SAM."""

    _manages_listing = True
    _stop_key = "recording"

    def __init__(self, peers: list[PeerInfo] | None = None) -> None:
        self.host: TransportHost | None = None
        self.connected = False
        self.opened: list[dict[str, Any]] = []
        self.consumed = 0
        self.disconnected = 0
        self.descriptions: list[AgentDescription] = []
        self.withdrawn = 0
        self.sent: list[tuple[str, StepEvent]] = []
        self.asked: list[Asked] = []
        self.calls: list[Called] = []
        self.finished: dict[str, TaskOutcome] = {}
        self.answers: list[tuple[str, TaskOutcome]] = []
        self.given_back: list[str] = []
        self.usage: list[tuple[str, str, Usage]] = []
        self.peer_list: list[PeerInfo] = list(peers or [])
        self.send_ok = True

    def _bind(self, host: TransportHost) -> None:
        self.host = host

    async def _open(self, *, serving: bool = True, wait_for_peers: bool = True) -> None:
        self.opened.append({"serving": serving, "wait_for_peers": wait_for_peers})
        self.connected = True

    async def _consume(self, watch: Any = None) -> None:
        self.consumed += 1

    async def _connect(self) -> None:
        self.connected = True

    async def _stop_background(self) -> None:
        pass

    async def _flush(self) -> None:
        pass

    async def _disconnect(self) -> None:
        self.disconnected += 1
        self.connected = False
        for asked in self.asked:
            if not asked.answer.done():
                asked.answer.set_result(PromptAnswer(asked.prompt.request_id, "cancelled"))
        for called in self.calls:
            if not called.result.done():
                called.result.set_result(PeerResult(called.call.peer, "error", "the agent disconnected"))

    async def _publish_description(self) -> None:
        self.descriptions.append(self.host.description())

    async def _withdraw_description(self) -> None:
        self.withdrawn += 1

    async def _send_event(self, task_id: str, event: StepEvent) -> bool:
        if not self.send_ok:
            return False
        self.sent.append((task_id, event))
        return True

    async def _ask(self, task_id: str, prompt: Prompt) -> PromptAnswer:
        asked = Asked(task_id, prompt, asyncio.get_running_loop().create_future())
        self.asked.append(asked)
        try:
            return await asyncio.wait_for(asyncio.shield(asked.answer), prompt.timeout_seconds)
        except TimeoutError:
            return PromptAnswer(prompt.request_id, "timed_out")

    async def _finish(self, task_id: str, outcome: TaskOutcome) -> None:
        self.finished[task_id] = outcome
        self.answers.append((task_id, outcome))
        for asked in self.asked:
            if asked.task_id == task_id and not asked.answer.done():
                asked.answer.set_result(PromptAnswer(asked.prompt.request_id, "cancelled"))
        for called in self.calls:
            if called.call.parent_task_id == task_id and not called.result.done():
                called.result.set_result(PeerResult(called.call.peer, "error", "peer request cancelled"))

    async def _call_peer(self, call: PeerCall) -> PeerResult:
        called = Called(call, asyncio.get_running_loop().create_future())
        self.calls.append(called)
        return await asyncio.shield(called.result)

    def _peers(self) -> list[PeerInfo]:
        return list(self.peer_list)

    def _usage_sink(self, task_id: str) -> Callable[[str, Usage], None]:
        return lambda model, usage: self.usage.append((task_id, model, usage))

    async def _give_back(self, task_id: str) -> None:
        self.given_back.append(task_id)

    def events(self, task_id: str | None = None, kind: type | tuple[type, ...] | None = None) -> list[StepEvent]:
        """The step events sent, for one task and of one kind (a StepEvent class or a tuple of them) when given."""
        return [e for t, e in self.sent if (task_id is None or t == task_id) and (kind is None or isinstance(e, kind))]

    def prompts(self, task_id: str | None = None) -> list[Prompt]:
        return [a.prompt for a in self.asked if task_id is None or a.task_id == task_id]

    def peer_calls(self, task_id: str | None = None) -> list[PeerCall]:
        return [c.call for c in self.calls if task_id is None or c.call.parent_task_id == task_id]

    async def until(self, check: Callable[[], Any], what: str, timeout: float = 5.0) -> Any:
        """``check()``'s value once it is truthy; fails naming ``what`` after ``timeout`` seconds. ``settle`` turns
        the event loop a fixed number of times, which cannot wait out work a framework runs on a worker thread (a sync
        tool, a sync callback): a test waits on what that work leads to instead."""
        deadline = asyncio.get_running_loop().time() + timeout
        while not (value := check()):
            if asyncio.get_running_loop().time() > deadline:
                raise AssertionError(f"{what} did not happen within {timeout}s")
            await asyncio.sleep(0.01)
        return value

    async def prompted(self, task_id: str = "T1", count: int = 1) -> list[Prompt]:
        """The task's prompts, once it has asked at least ``count``."""
        return await self.until(
            lambda: len(self.prompts(task_id)) >= count and self.prompts(task_id), f"prompt {count} of task {task_id}"
        )

    async def answered(self, task_id: str = "T1") -> TaskOutcome:
        """The task's final answer, once the agent has sent it."""
        return await self.until(lambda: self.finished.get(task_id), f"the answer to task {task_id}")

    async def deliver(self, task_id: str = "T1", text: str = "Triage claim P-1182", **fields: Any) -> None:
        """Deliver a task (``fields`` as IncomingTask takes them) and let its run start."""
        await self.host.on_task(IncomingTask(task_id=task_id, text=text, **fields))
        await settle()

    async def cancel(self, task_id: str = "T1") -> None:
        await self.host.on_cancel(task_id)
        await settle()

    async def answer(
        self,
        prompt: Prompt | str,
        answer: PromptAnswer | None = None,
        *,
        approved: bool | None = None,
        answers: tuple[str, ...] = (),
        status: str = "answered",
    ) -> None:
        """Answer a prompt (or its request id): ``approved`` for an approval, ``answers`` for questions."""
        request_id = prompt if isinstance(prompt, str) else prompt.request_id
        [asked] = [a for a in self.asked if a.prompt.request_id == request_id]
        if not asked.answer.done():
            asked.answer.set_result(answer or PromptAnswer(request_id, status, approved=approved, answers=answers))
        await settle()

    async def reply(self, call: PeerCall | int, result: PeerResult) -> None:
        """Resolve a peer call (or the n-th one) with the peer's result."""
        called = self.calls[call] if isinstance(call, int) else next(c for c in self.calls if c.call is call)
        if not called.result.done():
            called.result.set_result(result)
        await settle()

    async def control(self, enabled: bool, reason: str = "") -> None:
        """SAM's stop (False) or release (True), as the transport reports it; waits for the announcement."""
        reason = reason or ("released_by_sam" if enabled else "stopped_by_sam")
        announced = self.host.on_control(enabled, reason)
        if inspect.isawaitable(announced):
            await announced
        await settle()

    async def disconnect_from_sam(self) -> None:
        """The connection drops (not the agent's own disconnect)."""
        self.connected = False
        self.host.on_connection(False)
        await settle()

    async def reconnect(self) -> None:
        self.connected = True
        self.host.on_connection(True)
        await settle()


async def settle(rounds: int = 20) -> None:
    """Let every task that is ready run, a few times over."""
    for _ in range(rounds):
        await asyncio.sleep(0)


def agent_config(**fields: Any) -> AgentConfig:
    """An AgentConfig for a test agent; each gets a state_dir of its own, so a stop one test saves never starts
    another test's agent stopped."""
    fields.setdefault("state_dir", tempfile.mkdtemp(prefix="sam-link-sdk-rig-"))
    fields.setdefault("name", AGENT)
    fields.setdefault("description", "Triages insurance claims.")
    fields.setdefault("keepalive_seconds", 0)
    return AgentConfig(**fields)


async def start_agent(
    handler: Handler, transport: RecordingTransport | None = None, **config: Any
) -> tuple[MeshAgent, RecordingTransport]:
    """A started MeshAgent (``config`` as AgentConfig takes it) over a RecordingTransport."""
    transport = transport or RecordingTransport()
    agent = MeshAgent(agent_config(**config), handler, transport=transport)
    await agent.start()
    await settle()
    return agent, transport


class MeshTask:
    """A task a real MeshAgent runs, held open for an adapter test: ``ctx`` is its TaskContext. ``run(fn)`` runs
    ``fn(ctx)`` inside the task's run (as its handler) and returns what it returns; the agent then answers with it.
    Without ``run``, the task is answered "" when the test leaves the ``task_context`` block."""

    def __init__(self, agent: MeshAgent, transport: RecordingTransport, task_id: str):
        self.agent, self.transport, self.task_id = agent, transport, task_id
        self.ctx: TaskContext | None = None
        self._ready = asyncio.Event()
        self._work: asyncio.Queue = asyncio.Queue()

    async def _handler(self, ctx: TaskContext) -> Any:
        self.ctx = ctx
        self._ready.set()
        fn, done = await self._work.get()
        try:
            out = fn(ctx)
            if inspect.isawaitable(out):
                out = await out
        except asyncio.CancelledError:
            done.cancel()
            raise
        except BaseException as exc:
            if not done.done():
                done.set_exception(exc)
            raise
        if not done.done():
            done.set_result(out)
        return out

    async def run(self, fn: Callable[[TaskContext], Awaitable[Any] | Any]) -> Any:
        """Run ``fn(ctx)`` as the task's handler; returns its result (or raises what it raised)."""
        done = asyncio.get_running_loop().create_future()
        await self._work.put((fn, done))
        return await done

    @property
    def events(self) -> list[StepEvent]:
        return self.transport.events(self.task_id)

    @property
    def answer(self) -> TaskOutcome | None:
        return self.transport.finished.get(self.task_id)

    async def cancel(self) -> None:
        """SAM's cancel for the task: the agent answers it canceled and cancels its run."""
        await self.transport.cancel(self.task_id)


@contextlib.asynccontextmanager
async def task_context(
    task_id: str = "T",
    text: str = "Triage claims P-1 and P-2",
    *,
    tools: list[ToolInfo] | None = None,
    audit_sink: Callable[[Any], None] | None = None,
    config: dict[str, Any] | None = None,
    peers: list[PeerInfo] | None = None,
    **task: Any,
) -> AsyncIterator[MeshTask]:
    """The real TaskContext of a task a real MeshAgent runs over a RecordingTransport.

    ``tools`` (their ``required_scopes``) and ``audit_sink`` go to the agent's AgentConfig, with any other ``config``;
    ``task`` goes to the IncomingTask (``context_id``, ``user_id``, ``granted_tool_scopes``, ``can_prompt_user``,
    ``metadata``, ``files``, ...)."""
    config = dict(config or {})
    if tools is not None:
        config["tools"] = tools
    if audit_sink is not None:
        config["audit_sink"] = audit_sink
    task.setdefault("context_id", "C")
    transport = RecordingTransport(peers)
    holder: list[MeshTask] = []
    agent = MeshAgent(agent_config(**config), lambda ctx: holder[0]._handler(ctx), transport=transport)
    await agent.start()
    mesh_task = MeshTask(agent, transport, task_id)
    holder.append(mesh_task)
    await transport.host.on_task(IncomingTask(task_id=task_id, text=text, **task))
    await asyncio.wait_for(mesh_task._ready.wait(), 5)
    try:
        yield mesh_task
    finally:
        if mesh_task.answer is None and mesh_task._work.empty():
            await mesh_task._work.put((lambda ctx: "", asyncio.get_running_loop().create_future()))
        await settle()
        await agent.stop()
