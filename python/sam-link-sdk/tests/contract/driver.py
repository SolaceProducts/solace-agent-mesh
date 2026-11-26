# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The driver interface the contract scenarios are written against.

A scenario describes an agent (`AgentSpec`) and what it does with a task (an async handler taking a `ScriptTask`).
The driver (`driver_link`) turns that into a real agent: the SDK's `MeshAgent` over `LinkTransport`, agent link
protocol 2. Scenarios use only this interface, never SDK internals.
"""

from __future__ import annotations

import abc
import threading
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field


@dataclass(frozen=True)
class ToolCall:
    id: str
    name: str
    args: dict


@dataclass(frozen=True)
class PeerOutcome:
    ok: bool
    text: str


class ScriptTask(abc.ABC):
    """What a scripted agent can do with one task. No model is called: each step reports what a model or tool did."""

    text: str
    task_id: str
    user_id: str
    workflow: tuple[str, str] | None = None
    input: object = None
    files: tuple[tuple[str, bool, str], ...] = ()
    file_bytes: dict[str, bytes]
    output: dict | None = None

    @abc.abstractmethod
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
        """A model call started and returned with this text, tool calls and token usage (input_tokens holds the
        cache reads and writes, as the SDK's Usage does)."""

    @abc.abstractmethod
    def record_usage(self, model: str, *, input_tokens: int = 0, output_tokens: int = 0) -> None:
        """Report tokens a model spent that no model call's step carries."""

    @abc.abstractmethod
    async def tool_call(self, call: ToolCall, result: object) -> None:
        """A tool ran for the model's tool call and returned this result."""

    @abc.abstractmethod
    def attach(self, name: str, content: bytes, mime_type: str = "") -> None:
        """Return a file with the final answer."""

    @abc.abstractmethod
    async def stream(self, text: str) -> None:
        """Send a piece of the answer to the user as it is produced."""

    @abc.abstractmethod
    async def request_approval(self, tool: str, args: dict, timeout_seconds: float = 120) -> bool:
        """Ask the task's user to approve running a tool; True only for an explicit approval."""

    @abc.abstractmethod
    async def ask_questions(self, questions: tuple[tuple, ...], timeout_seconds: float = 120) -> tuple[str, ...] | None:
        """Ask the task's user questions, each (text, the labels of its choices; none for free text), or (text,
        labels, True) for a question whose user may pick several. Returns one answer per question, or None when the
        user did not answer."""

    @abc.abstractmethod
    async def call_peer(
        self, peer: str, text: str, *, call_id: str = "", skip_local_checks: bool = False
    ) -> PeerOutcome:
        """Ask another mesh agent. skip_local_checks sends the call even where the agent's own client would refuse it
        first (an agent it has not been told about), so the scenario sees what SAM itself does with it."""

    @abc.abstractmethod
    async def wait_cancelled(self, timeout_seconds: float) -> bool:
        """Block until SAM cancels this task (True) or the timeout passes (False)."""

    @abc.abstractmethod
    def tool_permitted(self, tool: str) -> bool:
        """Whether this task's user may use one of the agent's tools (its required scopes, as SAM granted them)."""


Handler = Callable[[ScriptTask], Awaitable[str]]


@dataclass
class AgentSpec:
    name: str
    handler: Handler
    description: str = "A scripted agent for the SDK contract tests."
    tools: tuple[tuple[str, str], ...] = ()
    tool_scopes: dict[str, tuple[str, ...]] = field(default_factory=dict)
    owner: str = ""
    framework: str = ""
    peers: tuple[str, ...] = ()
    max_concurrent_tasks: int = 0
    config: dict = field(default_factory=dict)


@dataclass
class TaskRecord:
    """What the agent saw of one task, for scenarios that need the agent's side too."""

    task_id: str
    text: str
    started: threading.Event = field(default_factory=threading.Event)
    finished: threading.Event = field(default_factory=threading.Event)
    cancelled: bool = False
    error: str = ""


class RunningAgent(abc.ABC):
    name: str

    def __init__(self) -> None:
        self.tasks: list[TaskRecord] = []
        self.control: list[tuple[bool, str]] = []
        self.sent: list[tuple[str, str]] = []
        self._lock = threading.Lock()
        self._task_seen = threading.Condition(self._lock)

    def record(self, task_id: str, text: str) -> TaskRecord:
        with self._task_seen:
            rec = TaskRecord(task_id, text)
            self.tasks.append(rec)
            self._task_seen.notify_all()
            return rec

    def wait_for_task(self, timeout: float = 20) -> TaskRecord:
        with self._task_seen:
            if not self._task_seen.wait_for(lambda: bool(self.tasks), timeout):
                raise AssertionError(f"{self.name} received no task within {timeout:g} s")
            return self.tasks[-1]

    @abc.abstractmethod
    def drop_connection(self, offline_seconds: float = 0.0) -> None:
        """Close the agent's connection under it, as a network failure would; it reconnects by itself, after
        ``offline_seconds`` when given (an outage of that length)."""

    @abc.abstractmethod
    def leave(self, timeout: float = 30) -> None:
        """Stop gracefully: the agent takes itself off the mesh."""

    @abc.abstractmethod
    def close(self, timeout: float = 30) -> None:
        """Stop, whatever state the agent is in. Safe to call twice."""


@dataclass(frozen=True)
class ConnectOutcome:
    accepted: bool
    detail: str


class LinkDriver(abc.ABC):
    def __init__(self, link_url: str, namespace: str):
        self.link_url = link_url
        self.namespace = namespace

    @abc.abstractmethod
    def start_agent(self, spec: AgentSpec, token: str) -> RunningAgent:
        """Connect an agent and return once SAM has taken its connection."""

    @abc.abstractmethod
    def try_connect(self, agent: str, token: str) -> ConnectOutcome:
        """Open one connection with these credentials and report whether SAM accepted it."""

    @abc.abstractmethod
    def close(self) -> None:
        """Stop every agent this driver started."""
