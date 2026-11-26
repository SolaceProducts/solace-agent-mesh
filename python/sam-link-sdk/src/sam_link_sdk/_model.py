# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The core's data model and the seam between the agent core and the way it reaches SAM.

The core (``MeshAgent``, ``TaskContext``) knows tasks, step events, prompts, peers and its own description. A
``Transport`` turns those into whatever SAM understands and back: ``sam_link_sdk.link.LinkTransport``, the agent link to
SAM's entrypoint, is the only one. Nothing here names a frame, a topic or a SAM signal.

The data types mirror the agent link protocol, version 2 (``docs/link-protocol.md``), one to one. The step events a
handler reports are in ``sam_link_sdk.events``; this module is internal, the seam between the core and the link, not an
extension point.

What a transport must supply: each task's ``granted_tool_scopes``, exactly the tool scopes SAM granted the task's user
(SAM checks the user; the agent does not). A tool that requires a scope the list lacks is refused, and a task that
comes with no list is granted none: the agent fails closed.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable, Mapping
from dataclasses import KW_ONLY, dataclass, field
from typing import Any, Protocol, runtime_checkable

from .audit import AuditLogger
from .config import AgentConfig, DataAccess, McpServer, Skill, Suggestion, ToolInfo
from .events import StepEvent
from .request_context import RequestContext
from .usage import Usage


@dataclass
class File:
    """A file the agent returns; the gateway stores it as an artifact in the user's session."""

    name: str
    content: bytes
    _: KW_ONLY
    mime_type: str = ""


@dataclass
class InputFile:
    """A file sent with the task. `content` holds its bytes; `uri` is set instead when the gateway could not
    inline it (for example a gateway without artifact-access support)."""

    name: str
    content: bytes | None = None
    _: KW_ONLY
    mime_type: str = ""
    uri: str | None = None


SESSION = "session"
RUN = "run"


@dataclass(frozen=True)
class Workflow:
    """The SAM workflow node that runs the task."""

    name: str
    node_id: str


@dataclass
class IncomingTask:
    """A task for the agent (v2 ``task``). ``text`` is the user's words, or a workflow node's instruction (empty when
    the node has none: the core then prompts with the node's input)."""

    task_id: str
    context_id: str = ""
    text: str = ""
    files: list[InputFile] = field(default_factory=list)
    input: Any = None
    workflow: Workflow | None = None
    session_behavior: str = SESSION
    user_id: str = ""
    call_depth: int = 0
    request_context: RequestContext = field(default_factory=RequestContext)
    metadata: dict[str, Any] = field(default_factory=dict)
    granted_tool_scopes: frozenset[str] = frozenset()
    can_prompt_user: bool = True
    streaming: bool = True


ANSWERED, CANCELLED, TIMED_OUT = "answered", "cancelled", "timed_out"


@dataclass(frozen=True)
class PromptOption:
    label: str
    description: str = ""
    value: str = ""


@dataclass(frozen=True)
class PromptQuestion:
    question: str
    header: str = ""
    options: tuple[PromptOption, ...] = ()
    multi_select: bool = False


class PromptTooLarge(ValueError):
    """A prompt the transport cannot carry as asked (on the agent link: more questions or options per question than
    the entrypoint takes, or approval arguments too many or too large to show), raised to the code that asked rather
    than shown cut short."""


@dataclass(frozen=True)
class Approval:
    tool: str
    args: Mapping[str, Any] = field(default_factory=dict)
    message: str = ""


@dataclass(frozen=True)
class Prompt:
    """Ask the task's user: exactly one of ``approval`` or ``questions``."""

    request_id: str
    timeout_seconds: float
    approval: Approval | None = None
    questions: tuple[PromptQuestion, ...] = ()
    message: str = ""


@dataclass(frozen=True)
class PromptAnswer:
    """The user's answer. ``approved`` is None when an approval came back unreadable; ``answers`` has one string per
    question when the questions were answered."""

    request_id: str
    status: str
    approved: bool | None = None
    answers: tuple[str, ...] = ()
    truncated: bool = False


@dataclass(frozen=True)
class TaskError:
    """The task failed with an error rather than an answer; ``category`` and ``provider`` name a failed model call
    (sam_link_sdk.llm_errors), both empty for any other failure, which ``message`` describes to the user ("" for the
    generic "internal error")."""

    category: str = ""
    provider: str = ""
    message: str = ""


@dataclass
class TaskOutcome:
    state: str
    text: str = ""
    files: list[File] = field(default_factory=list)
    data: dict[str, Any] | None = None
    error: TaskError | None = None
    answer_to_cancel: bool = False


PEER_REFUSED = "SAM refused the peer call"
"""What a result says when SAM did not carry a peer call: it refused the call as not allowed (the agent may not call that
peer, the user may not use it), or it could not carry it. PeerResult.sam_decision tells the two apart.
"""
PEER_CALL_GRANTED = "granted"
PEER_CALL_DENIED = "denied"


@dataclass(frozen=True)
class PeerSkill:
    id: str = ""
    name: str = ""
    description: str = ""


@dataclass(frozen=True)
class PeerInfo:
    """A configured peer that is on the mesh now (v2 ``peers``)."""

    name: str
    display_name: str = ""
    description: str = ""
    skills: tuple[PeerSkill, ...] = ()
    on_demand: bool = False


@dataclass(frozen=True)
class PeerCall:
    """Ask a peer, on behalf of the running task ``parent_task_id`` (v2 ``peer_call``)."""

    parent_task_id: str
    peer: str
    text: str
    call_id: str = ""


_UNSUCCESSFUL_STATES = ("failed", "canceled", "rejected")


@dataclass
class PeerResult:
    """The outcome of a call to another agent (v2 ``peer_result``)."""

    agent: str
    state: str
    text: str = ""
    data: Any = None
    truncated: bool = False
    """The transport cut the result to fit: the peer's data dropped, its text or error message cut with SAM's truncation
    marker ("…[truncated N bytes]").
    """
    sam_decision: str = ""
    """What SAM decided about the call, for the audit trail: PEER_CALL_GRANTED when SAM carried it (the peer's answer came
    back), PEER_CALL_DENIED when SAM refused it as not allowed, "" when the agent saw neither (the call never left the
    agent, SAM could not carry it, or it ended on a timeout, a cancel or a disconnect).
    """

    @property
    def ok(self) -> bool:
        return self.state not in _UNSUCCESSFUL_STATES and self.state != "error"

    def summary(self) -> str:
        """The text a framework tool returns to the model, worded as SAM-Go's peer tool words it."""
        if self.state == "error":
            return self.text
        if not self.ok:
            return f'peer agent "{self.agent}" did not succeed: {self.text or "no text content"}'
        return self.text or "Peer agent responded (no text content)"


HEALTH_SEVERITIES = ("degraded", "fatal")


@dataclass(frozen=True)
class HealthProblem:
    id: str
    severity: str
    detail: str


@dataclass(frozen=True)
class Declared:
    """The agent's own statement about itself (SAM's sam/provenance); SAM does not check it."""

    framework: str = ""
    owner: str = ""
    team: str = ""
    sdk_name: str = "sam-link-sdk"
    sdk_version: str = ""
    data_access: tuple[DataAccess, ...] = ()
    mcp_servers: tuple[McpServer, ...] = ()


@dataclass(frozen=True)
class AgentDescription:
    """What the agent is. The agent's name is the authenticated one, never part of the description."""

    display_name: str = ""
    description: str = ""
    version: str = ""
    skills: tuple[Skill, ...] = ()
    tools: tuple[ToolInfo, ...] = ()
    required_scopes: tuple[str, ...] = ()
    input_schema: dict[str, Any] | None = None
    output_schema: dict[str, Any] | None = None
    welcome_message: str = ""
    suggestions: tuple[Suggestion, ...] = ()
    peers: tuple[str, ...] = ()
    streaming: bool = True
    declared: Declared = field(default_factory=Declared)
    health: tuple[HealthProblem, ...] = ()


class DeliveryWatch(Protocol):
    """Told about each delivery a transport takes from the agent's queue while a request-driven agent serves it."""

    def entered(self) -> None: ...

    def left(self) -> None: ...

    def fail(self, error: BaseException) -> None:
        """SAM will deliver nothing to this wake (it refused the agent's description): end it with ``error``."""
        ...


class TransportHost(Protocol):
    """What a transport sees of the agent core (``MeshAgent``)."""

    @property
    def config(self) -> AgentConfig: ...

    @property
    def audit(self) -> AuditLogger: ...

    @property
    def draining(self) -> bool:
        """The agent is shutting down: it takes no new task, and leaves new deliveries for another replica."""
        ...

    @property
    def stop_reason(self) -> str:
        """Why SAM does not let the agent run now ("" when it may)."""
        ...

    def description(self) -> AgentDescription: ...

    async def on_task(self, task: IncomingTask) -> None:
        """Start a task the transport accepted. The core answers it with ``Transport.finish``."""
        ...

    async def on_cancel(self, task_id: str) -> None: ...

    def on_control(self, enabled: bool, reason: str) -> Awaitable[None] | None:
        """SAM stopped (False) or released (True) the agent. ``reason`` is SAM's ("" when SAM gave none, as on
        connecting). Applied at once; returns what still tells the mesh and the listeners, for a caller that waits for
        it."""
        ...

    def on_connection(self, connected: bool) -> None:
        """The transport's connection to SAM dropped (False) or came back (True). Not called for a deliberate
        disconnect."""
        ...

    def wants_events(self, task_id: str) -> bool:
        """Whether the task is running and takes step events: nobody cancelled it."""
        ...

    def on_link_failure(self, error: Exception) -> None:
        """The transport gave up for good (the entrypoint kept refusing the agent's token): the core stops gracefully
        and raises ``error``."""
        ...


UsageSink = Callable[[str, Usage], None]


@runtime_checkable
class Transport(Protocol):
    """How the agent core reaches SAM. See the module doc."""

    _manages_listing: bool
    """SAM takes the agent's card down while it has the agent stopped and puts it back on release (the agent link
    entrypoint): the core neither withdraws nor republishes the description for a stop.
    """

    def _bind(self, host: TransportHost) -> None:
        """Called once, by the agent that uses the transport, before anything else."""
        ...

    async def _open(self, *, serving: bool = True, wait_for_peers: bool = True) -> None:
        """Connect and get ready. ``serving``: a long-running agent, which also takes tasks from now on and keeps
        its description fresh. Otherwise a request-driven one, which learns the mesh (its peers when
        ``wait_for_peers``, SAM's stop) before it serves a request.

        A transport may also have `_stop_key: str`, a name for where SAM's stop for this agent on this transport is kept
        (the agent link: the entrypoint's host, port and path and the agent's name). Without it the agent's name is
        used.
        """
        ...

    async def _consume(self, watch: DeliveryWatch | None = None) -> None:
        """Take tasks from now on (a request-driven agent that serves its queue)."""
        ...

    async def _connect(self) -> None:
        """Only connect (to withdraw the description of an agent that is going away)."""
        ...

    async def _stop_background(self) -> None:
        """Stop the transport's own periodic work (heartbeats, control renewals)."""
        ...

    async def _flush(self) -> None:
        """Let queued reports (spend) reach SAM before disconnecting."""
        ...

    async def _disconnect(self) -> None: ...

    async def _publish_description(self) -> None:
        """Publish the host's current description, so SAM lists the agent."""
        ...

    async def _withdraw_description(self) -> None:
        """Publish the agent's removal, so SAM stops listing it: the agent leaves for good, or (a transport that does
        not manage the listing) SAM stopped it."""
        ...

    async def _send_event(self, task_id: str, event: StepEvent) -> bool:
        """Put a step on the task's event stream. Returns whether it went out."""
        ...

    async def _ask(self, task_id: str, prompt: Prompt) -> PromptAnswer:
        """Show the prompt to the task's user and wait for the answer, or for ``timeout_seconds`` (TIMED_OUT)."""
        ...

    async def _finish(self, task_id: str, outcome: TaskOutcome) -> None:
        """Send the task's final outcome and settle its delivery."""
        ...

    async def _call_peer(self, call: PeerCall) -> PeerResult:
        """Ask a peer and wait for its final answer. Cancelling the wait cancels the call.

        A transport may also have `async def _give_back(self, task_id: str) -> None`, which gives an unanswered task
        back to SAM to deliver again (a negative acknowledgement) and forgets it: its open prompts and peer calls end,
        and a later delivery of it is taken as new. When the give-back cannot be sent, or a transport has none, the
        delivery returns to SAM when the connection closes. A task given back once already by this process is answered
        failed instead (see GiveBacks). Both shipped transports have it.
        """
        ...

    def _peers(self) -> list[PeerInfo]:
        """The peers the agent may call now."""
        ...

    def _usage_sink(self, task_id: str) -> UsageSink | None:
        """Where the task reports each model call's usage (callable from any thread), or None."""
        ...


def as_transport(obj: Any) -> Transport:
    if isinstance(obj, Transport):
        return obj
    raise TypeError(f"{type(obj).__name__} is not a Transport (sam_link_sdk.link.LinkTransport)")
