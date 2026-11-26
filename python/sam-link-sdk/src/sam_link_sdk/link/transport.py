# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""``LinkTransport``: the agent core on SAM's agent link entrypoint, agent link protocol 2.

The transport implements the core's ``Transport`` over the link's WebSocket (``LinkConnection``) with the frames of
``v2.py``: a task frame becomes an ``IncomingTask``, a step event an ``event`` frame, a prompt a ``prompt`` frame, and
so on (``docs/link-protocol.md``). The entrypoint verifies each user and the agent's required scopes before it
delivers a task, redacts and caps tool arguments and results, records spend from each ``model_finished``, adds the
token totals to the final answer, and keeps the agent's card: it lists the agent from its description, takes the card
down while SAM has the agent stopped and puts it back on release. The card is published by ``DescriptionPublisher``
(``describer.py``), the token usage kept by ``UsageLedger`` (``usage_ledger.py``) and a result fitted to the link's
limits by ``v2.fit_result``; this module routes the entrypoint's frames to them and holds the tasks, prompts, peer calls
and answers waiting for a reconnect.

On every connection the agent describes itself first and only then takes tasks (``consume``), stopped or not.
``retire`` is sent only when the agent leaves for good.

Delivery: a task is acknowledged once it is answered; one that arrives while the agent drains is left unacknowledged,
so the entrypoint gives it back when the agent disconnects; a copy of a running task (redelivered after a reconnect)
is held and acknowledged with the run. After a reconnect a run sends nothing for its task until that copy has come on
the new connection, and an answer the link could not carry goes out when it comes, or when the wait for it ran out
(``REDELIVERY_WAIT_SECONDS``). A stopping agent goes on waiting while it drains, because ``retire`` takes only the card
down and SAM still delivers on the connection; at the end of ``shutdown_grace_seconds`` what still waits goes out as it
is, before the disconnect (``_end_redelivery_waits``).

Per-tool scopes: the entrypoint checks the user against the scopes the agent's described tools require and sends,
with each task, the ones the user holds (``grantedToolScopes``); ``TaskContext.check_tool`` enforces them. Every
frame is built within the limits the welcome announces (``Limits``).
"""

from __future__ import annotations

import asyncio
import logging
import urllib.parse
from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Any

from .._model import (
    CANCELLED,
    PEER_CALL_DENIED,
    PEER_REFUSED,
    TIMED_OUT,
    DeliveryWatch,
    IncomingTask,
    PeerCall,
    PeerInfo,
    PeerResult,
    Prompt,
    PromptAnswer,
    PromptTooLarge,
    TaskError,
    TaskOutcome,
    TransportHost,
    UsageSink,
)
from .._util import BoundedMap, within
from ..config import AgentConfig
from ..events import ModelFailed, ModelFinished, ModelStarted, StepEvent
from ..ids import uuid7_str
from .config import LinkConfig, LinkError
from .connection import LinkConnection, StaleConnection
from .describer import DescriptionPublisher
from .giveback import NOT_TAKEN_TWICE, OUTSIDE_CANCEL_TWICE, GiveBacks
from .usage_ledger import TaskUsage, UsageClaim, UsageLedger, report_lost
from .v2 import (
    UNKNOWN_MODEL,
    Limits,
    cap_strings,
    encode,
    event_body,
    fit_id,
    fit_result,
    fit_tool_payload,
    fits,
    incoming_task,
    is_delivery,
    overlong_description,
    peer_info,
    peer_result,
    prompt_answer,
    prompt_frame,
    prompt_overflow,
    prompt_problem,
    result_frame,
    tool_scopes,
)

log = logging.getLogger("sam_link_sdk.link")

ANSWER_GRACE_SECONDS = 5.0
"""A prompt's own timeout bounds its wait; the entrypoint answers timed_out then. This much longer, the agent stops waiting
by itself (an answer lost while the link was down, or an entrypoint restart).
"""
DEFAULT_PROMPT_TIMEOUT_SECONDS = 45 * 60.0
RETIRE_SECONDS = 2.0
"""A shutdown sends its retire at most this long: a link that is down would otherwise hold it for connect_timeout_seconds
waiting for a reconnect, out of the time a supervisor gives the process before it kills it.
"""
FINAL_RESEND_TRIES = 3
FINAL_RESEND_DELAY_SECONDS = 1.0
FINAL_HOLD_SECONDS = 600.0
"""How long a sent result is held, to send again when SAM asks for it (publish_failed) or delivers its task again after
a gateway restart; MAX_HELD_BYTES bounds what is held."""
MAX_ANSWERED_KEPT = 256
UNDELIVERABLE = "The agent's answer could not be delivered"
"""What a task is answered with when SAM refused its result, followed by SAM's reason."""
REFUSED_RESULT_CODES = ("bad_message", "limit")
"""The codes of a refusal that sending the same result again cannot cure."""
MAX_HELD_BYTES = 64 * 1024 * 1024
"""Results can carry files (up to max_inline_file_bytes each), so what is held for a task SAM asks to have again, and what
waits for a reconnect, is also bounded by size: the oldest go first, the newest is always kept.
"""
UNREADABLE_TASK_TEXT = "The agent could not read this task, so it did not run it."
HELD_ANSWER_POLL_SECONDS = 0.02
FINAL_SEND_SECONDS = 1.0
"""How long a stopping agent gives the answers still held for a redelivery, once its grace has run out, to go out on
the current connection. It is a fixed second, not what is left of the grace: an answer is only still held at that
point when the grace ran out waiting for its task to be delivered again."""
REDELIVERY_WAIT_SECONDS = 90.0
"""How long after a reconnect a run waits for SAM to deliver its task again before it sends on the new connection all
the same. SAM delivers a task again once it has taken the request back from the connection that dropped: at once when
it saw that connection close, and otherwise only when it notices by itself that the connection is dead, which takes it
up to 60 s. The wait is longer than that, so a run sends nothing while its request is still on its way back. A task
not delivered again by then was closed at SAM, went to another instance of the agent, or is not delivered again at
all: its redelivery limit is spent, or SAM's broker cannot deliver a request again (the TCP dev broker). With a
Solace broker's limit spent, and on the TCP dev broker, SAM still holds the task and takes the run's frames, which is
why the run sends them. Otherwise SAM refuses them and the run stops: so it is on the in-process dev broker, where
SAM fails a task at its next sweep once the limit is spent. The wait runs only while the link is up: it ends when the
link drops and starts over on the next connection."""


class TooManyToolScopes(LinkError):
    """The agent's tools require more distinct scopes than the entrypoint checks (``welcome.limits.maxToolScopes``)."""


def check_tool_scopes(config: AgentConfig, limits: Limits) -> None:
    """Refuse a configuration whose tools name more distinct scopes than the entrypoint takes: it would refuse the
    description, and with it every task."""
    scopes = tool_scopes(config.tools)
    if len(scopes) > limits.max_tool_scopes:
        raise TooManyToolScopes(
            f"the agent's tools require {len(scopes)} distinct scopes; the agent link checks at most "
            f"{limits.max_tool_scopes}. Share scopes between tools, or move scopes every user of the agent needs to "
            "AgentConfig.required_scopes."
        )


class DescriptionTooLong(LinkError):
    """The agent's description, its skills, or a skill's name or description, is longer than the entrypoint takes
    (``welcome.limits``: ``maxDescriptionBytes``, ``maxSkills``, ``maxNameBytes``, ``maxSkillDescriptionBytes``)."""


def check_description(host: TransportHost, limits: Limits) -> None:
    """Refuse an agent whose description the entrypoint would refuse, and with it every task: its tool scopes
    (``TooManyToolScopes``) and the length of its text (``DescriptionTooLong``)."""
    check_tool_scopes(host.config, limits)
    overlong = overlong_description(host.description(), limits)
    if overlong:
        raise DescriptionTooLong(f"{overlong}. Shorten AgentConfig.description or the skills.")


@dataclass
class _Task:
    """A task delivered to this agent and not answered yet."""

    deliveries: list[tuple[str, int]]
    sends_on: str
    """The connection the run's frames go out on: the one SAM last delivered the task on, or a later one SAM
    delivers nothing again on (the agent takes no tasks there, or the wait for the delivery ran out)."""
    usage: TaskUsage = field(default_factory=TaskUsage)
    closed: bool = False
    can_prompt: bool = True
    delivered_here: asyncio.Event = field(default_factory=asyncio.Event)
    """Clear while the run waits, on a connection that has just opened, for SAM to deliver the task again."""
    unknown_to_sam: bool = False
    """SAM refused a frame of the run as naming no task, on a connection it had not delivered the task on: nothing
    more is sent for it."""
    redelivery_wait: asyncio.TimerHandle | None = None
    """Runs out ``REDELIVERY_WAIT_SECONDS`` after the wait began; held only while the run waits."""

    @property
    def undelivered_here(self) -> bool:
        """Whether the run sends on a connection SAM did not deliver its task on, where SAM may not hold the task."""
        return self.sends_on != self.deliveries[-1][0]


@dataclass
class _Ask:
    task_id: str
    prompt: Prompt
    future: asyncio.Future


@dataclass
class _Call:
    parent_task_id: str
    peer: str
    future: asyncio.Future


class LinkTransport:
    """The agent's Transport over the agent link (see the module doc)."""

    _manages_listing = True

    def __init__(self, config: LinkConfig):
        self.config = config
        self._connection = LinkConnection(config)
        self._connection.on_frame(self._on_frame)
        self._connection.on_open(self._on_connection_open)
        self._connection.on_connection(self._on_link_change)
        self._host: TransportHost = None  # type: ignore[assignment]
        """Set by bind(); open() refuses an unbound transport."""
        self._consuming = False
        self._consumed_on = -1
        self._consume_retried = -1
        self._watch: DeliveryWatch | None = None
        self._watcher: asyncio.Task | None = None
        self._describer = DescriptionPublisher(
            self._connection, lambda: self._host, lambda: self._limits, self._send, self._on_description_refused
        )
        self._ledger = UsageLedger(lambda: self._limits, lambda: self._config.spend_model_keys)
        self._tasks: dict[str, _Task] = {}
        self._given_back = GiveBacks()
        self._answered: BoundedMap[str, None] = BoundedMap(MAX_ANSWERED_KEPT)
        """Recently answered tasks: a copy the entrypoint delivers again is answered with the held result, else only
        acknowledged."""
        self._unsent: BoundedMap[str, list[dict]] = BoundedMap(
            MAX_ANSWERED_KEPT, MAX_HELD_BYTES, on_evict=self._drop_unsent
        )
        """Answers the link could not carry: each goes out when SAM delivers its task again, or at once on a
        connection that takes no tasks."""
        self._unsent_wait: asyncio.TimerHandle | None = None
        self._going_out: dict[str, list[tuple[str, int]]] = {}
        """The held answers ``_send_unsent`` is sending, each with the deliveries of its task that came meanwhile:
        acknowledged once the answer is out, where answering them too would send the result twice."""
        self._answering_again: set[str] = set()
        """The tasks whose held answer a delivery took and ``_answer_again`` is sending."""
        self._sent_undelivered: BoundedMap[str, list[dict]] = BoundedMap(MAX_ANSWERED_KEPT)
        """The usage frames of answers sent on a connection SAM had not delivered their task on: reported lost if SAM
        refuses the answer as naming no task."""
        self._finals: BoundedMap[str, tuple[dict, int, int, int]] = BoundedMap(
            MAX_ANSWERED_KEPT, MAX_HELD_BYTES, on_evict=lambda task_id, _: self._cancel_final_timer(task_id)
        )
        """Recent results, to send again when the entrypoint could not publish one or delivers its task again: task id
        -> (frame, tries, bytes, serial). The usage sent with a result is not held: SAM records spend from each usage
        frame it takes, whichever replica takes it, so sending one again would record that spend twice.
        """
        self._undeliverable: BoundedMap[str, None] = BoundedMap(MAX_ANSWERED_KEPT)
        """Tasks answered failed with UNDELIVERABLE after SAM refused their result: a refusal of that answer too is not
        answered again, so a refusal cannot loop."""
        self._final_serial = 0
        self._final_timers: dict[str, asyncio.TimerHandle] = {}
        self._asks: dict[str, _Ask] = {}
        self._calls: dict[str, _Call] = {}
        self._listed_peers: list[PeerInfo] = []
        self._listed_peers_known = asyncio.Event()
        self._assumed_peers: set[str] = set()

    def _bind(self, host: TransportHost) -> None:
        self._host = host
        self._connection.on_control(host.on_control)
        self._connection.on_connection(host.on_connection)
        self._connection._on_failure(host.on_link_failure)

    @property
    def _protocol(self) -> int:
        """The protocol version the entrypoint welcomed the agent with (0 before the first connection)."""
        return self._connection.protocol

    @property
    def _welcome(self) -> dict:
        return self._connection.welcome

    @property
    def _stop_key(self) -> str:
        """Where the agent keeps SAM's stop for this link: the entrypoint's host, port and path, and the agent's name
        (the path names the gateway, and two gateways can share a host and port)."""
        parsed = urllib.parse.urlsplit(self.config.url)
        port = parsed.port or (443 if parsed.scheme in ("https", "wss") else 80)
        path = parsed.path.strip("/")
        return f"{parsed.hostname or 'link'}_{port}{'_' + path if path else ''}__{self.config.agent}"

    @property
    def _config(self) -> AgentConfig:
        return self._host.config

    @property
    def _limits(self) -> Limits:
        """What the entrypoint takes, as the current welcome announces it (SAM-Go's defaults before one)."""
        return Limits.from_welcome(self._connection.welcome.get("limits"), self._connection.config.max_frame_bytes)

    @property
    def _max_frame_bytes(self) -> int:
        """The largest frame either side takes: the welcome's limit, at most the configured one."""
        return self._limits.max_frame_bytes

    async def _open(self, *, serving: bool = True, wait_for_peers: bool = True) -> None:
        if self._host is None:
            raise LinkError("LinkTransport is not bound to an agent")
        if self.config.agent != self._host.config.name:
            raise ValueError(
                f"the agent is named {self._host.config.name!r} (AgentConfig.name) but its link is configured for "
                f"{self.config.agent!r} (LinkConfig.agent): the link connects as one name and the agent serves as "
                "another; give both the same name, or leave transport out of MeshAgent to build the link from the "
                "environment with the agent's name"
            )
        check_description(self._host, Limits())
        self._describer.present = True
        self._consuming = serving
        await self._connection.connect(self._host.config.offline_grace_seconds if serving else 0.0)
        try:
            check_description(self._host, self._limits)
        except (TooManyToolScopes, DescriptionTooLong):
            await self._disconnect()
            raise
        if not self._describer.described_here():
            await self._disconnect()
            raise LinkError("the agent could not describe itself to the entrypoint, so it cannot take tasks")
        if serving:
            self._watcher = asyncio.create_task(
                self._describer.watch(self._consume_if_taking), name="sam-link-sdk-link-describe"
            )
            return
        if wait_for_peers:
            try:
                await asyncio.wait_for(self._listed_peers_known.wait(), self._connection.config.connect_timeout_seconds)
            except TimeoutError:
                log.warning("the entrypoint sent no peers list; the agent starts without peers")

    async def _consume(self, watch: DeliveryWatch | None = None) -> None:
        self._watch = watch
        self._consuming = True
        if not await self._describer.describe():
            refusal = self._describer.refusal
            raise LinkError(
                f"the agent could not describe itself to the entrypoint, so it cannot take tasks"
                f"{': SAM refused its description: ' + refusal if refusal else ''}"
            )
        await self._send_consume()

    async def _connect(self) -> None:
        await self._connection.connect()

    async def _stop_background(self) -> None:
        if self._watcher is not None:
            self._watcher.cancel()
            self._watcher = None

    async def _flush(self) -> None:
        """Nothing is queued: every frame is sent as it is made."""

    async def _disconnect(self) -> None:
        """Close the link for good: end what waits on it and report what could not be sent.

        A deliberate disconnect ends the session, so a later connect (retire after announce) starts afresh.
        """
        await self._stop_background()
        self._stop_waiting_for_redeliveries()
        for request_id in list(self._asks):
            self._answer(request_id, PromptAnswer(request_id, CANCELLED))
        for subtask_id in list(self._calls):
            self._fail_call(subtask_id, "the agent disconnected")
        for task_id, frames in self._unsent.items():
            log.error("result not sent: the agent link is closed task=%s", task_id)
            report_lost(task_id, frames)
        self._unsent.clear()
        self._sent_undelivered.clear()
        for timer in self._final_timers.values():
            timer.cancel()
        self._final_timers.clear()
        self._finals.clear()
        for task_id, task in list(self._tasks.items()):
            report_lost(task_id, self._ledger.lost_frames(task_id, task.usage))
        self._tasks.clear()
        self._consuming = False
        self._describer.withdraw()
        await self._connection.disconnect()

    async def _on_connection_open(self) -> None:
        """A new connection: describe the agent, then take tasks again. SAM then delivers again what the agent had not
        acknowledged, and the runs and the answers the dropped connection could not carry wait for that, from before
        anything is awaited here, so no run tries a frame on the new connection first. On a connection no consume
        went out on (the agent is leaving, or could not describe itself) nothing is delivered again, so they are sent
        as they are."""
        if self._takes_tasks():
            self._expect_redeliveries()
        self._describer.new_connection()
        try:
            if self._describer.present:
                await self._describer.describe()
            if self._takes_tasks():
                await self._send_consume()
        except Exception:
            log.exception("the agent could not describe itself on the new connection, so it takes no tasks on it")
        if self._consumed_on != self._describer.generation:
            self._expect_no_redeliveries()
            await self._send_unsent()

    def _takes_tasks(self) -> bool:
        return self._consuming and self._describer.present

    async def _send_consume(self) -> None:
        """Take tasks on the current connection, once the entrypoint holds the agent's description there."""
        generation = self._describer.generation
        if not self._describer.described_here() or self._consumed_on == generation:
            return
        try:
            await self._send({"type": "consume"})
        except LinkError as exc:
            log.warning("consume not sent err=%s", exc)
            return
        self._consumed_on = generation

    async def _publish_description(self) -> None:
        self._describer.present = True
        await self._describer.describe()

    async def _withdraw_description(self) -> None:
        """The agent leaves for good: the entrypoint removes its card once no other connection of the agent that has
        not retired remains."""
        self._describer.withdraw()
        self._consuming = False
        try:
            await asyncio.wait_for(self._send({"type": "retire"}), RETIRE_SECONDS)
        except TimeoutError:
            log.error("removal card publish failed err=not sent within %gs", RETIRE_SECONDS)
        except LinkError as exc:
            log.error("removal card publish failed err=%s", exc)

    async def _consume_if_taking(self) -> None:
        if self._consuming:
            await self._send_consume()

    async def _describe_and_consume(self) -> None:
        if await self._describer.describe():
            await self._consume_if_taking()

    def _on_frame(self, frame: dict) -> None:
        kind = frame.get("type")
        handler = (
            {
                "task": self._on_task_frame,
                "cancel": self._on_cancel_frame,
                "prompt_answer": self._on_prompt_answer,
                "peer_result": self._on_peer_result,
                "peers": self._on_peers,
                "error": self._on_error,
            }.get(kind)
            if isinstance(kind, str)
            else None
        )
        if handler is None:
            log.warning(
                "dropping an unknown message from the entrypoint type=%s",
                kind[:64] if isinstance(kind, str) else type(kind).__name__,
            )
            return
        handler(frame)

    def _on_task_frame(self, frame: dict) -> None:
        """Register a delivered task and start accepting it.

        The task is registered here, before the next message is read, so a second copy of it, or the wake's check for
        arriving work, sees it even before its acceptance runs. A delivery stays unacknowledged until the run ends, so
        the entrypoint gives the task back if this agent disconnects first. A task that is not taken (draining,
        unreadable) is acknowledged or left unacknowledged so it does not hold an in-flight slot and come back on every
        reconnect. A copy of a task still running here, one of a task whose answer waits to be sent, and one of a task
        already answered (its held result is sent again, or the delivery acknowledged) belong to work the agent already
        took, so they are taken while it drains too.
        """
        delivery, task_id, body = frame.get("delivery"), frame.get("taskId"), frame.get("task")
        if not is_delivery(delivery):
            log.warning("dropping a task without a delivery")
            return
        connection = self._connection.connection_id
        if not isinstance(task_id, str) or not task_id or not isinstance(body, dict):
            log.warning("dropping an unreadable task delivery=%s", delivery)
            self._connection.spawn(self._ack(connection, delivery))
            return
        running = self._tasks.get(task_id)
        if running is not None:
            if running.delivered_here.is_set() and not running.undelivered_here:
                log.warning("duplicate delivery of in-flight task=%s", task_id)
            else:
                log.info("task delivered again after the reconnect; its run goes on task=%s", task_id)
            running.deliveries.append((connection, delivery))
            running.sends_on = connection
            self._end_wait(running)
            return
        unsent = self._unsent.pop(task_id)
        if unsent is not None:
            log.info("task delivered again; sending the answer that waited for it task=%s", task_id)
            self._spawn_answer_again(task_id, unsent, connection, delivery)
            return
        if task_id in self._going_out:
            log.info("task delivered again while its held answer goes out task=%s", task_id)
            self._going_out[task_id].append((connection, delivery))
            return
        if self._host.draining and task_id not in self._answered:
            log.info("not taking a task while draining task=%s", task_id)
            return
        if frame.get("redelivered"):
            log.warning("task redelivered, possible poison pill task=%s", task_id)
        if task_id in self._answered:
            held = self._finals.get(task_id)
            if held is None:
                log.info("acknowledging a task already answered task=%s", task_id)
                self._connection.spawn(self._ack(connection, delivery))
            else:
                log.warning("task delivered again after it was answered; sending its result again task=%s", task_id)
                self._spawn_answer_again(task_id, [held[0]], connection, delivery)
            return
        watch = self._watch
        try:
            task = incoming_task(task_id, body)
        except Exception as exc:
            log.error("task frame could not be read; answering it failed task=%s err=%s", task_id, type(exc).__name__)
            if watch is not None:
                watch.entered()
            unreadable = self._tasks[task_id] = self._delivered(connection, delivery, can_prompt=False)
            self._connection.spawn(self._refuse_unreadable(task_id, unreadable, watch))
            return
        if watch is not None:
            watch.entered()
        self._tasks[task_id] = self._delivered(connection, delivery, can_prompt=task.can_prompt_user)
        self._connection.spawn(self._accept(task, watch))

    @staticmethod
    def _delivered(connection: str, delivery: int, *, can_prompt: bool) -> _Task:
        task = _Task([(connection, delivery)], connection, can_prompt=can_prompt)
        task.delivered_here.set()
        return task

    def _expect_redeliveries(self) -> None:
        """A new connection that takes tasks: SAM may no longer hold the tasks still running here, so each waits to
        be delivered again, and so does each answer the link could not carry.

        Until then a run sends nothing for its task: SAM would refuse the frame as naming no task. Steps are dropped,
        as they are while the link is down; a prompt and a peer call wait (``_send_once_delivered``) and so does the
        answer (``_send_answer``). Each is given ``REDELIVERY_WAIT_SECONDS``.
        """
        loop = asyncio.get_running_loop()
        runs = [(task_id, task) for task_id, task in self._tasks.items() if not task.unknown_to_sam]
        for task_id, task in runs:
            task.delivered_here.clear()
            task.redelivery_wait = loop.call_later(REDELIVERY_WAIT_SECONDS, self._wait_ran_out, task_id, task)
        answers = self._unsent.items()
        self._wait_for_unsent()
        if runs or answers:
            log.info(
                "agent link reconnected; waiting for SAM to deliver again the tasks still running and those whose "
                "answer was not sent runs=%d answers=%d seconds=%g",
                len(runs),
                len(answers),
                REDELIVERY_WAIT_SECONDS,
            )

    def _expect_no_redeliveries(self) -> None:
        """The agent takes no tasks on the current connection (it is leaving, it could not describe itself, or SAM
        refused its description), so SAM delivers nothing again on it. The runs still going send on it as it is: SAM
        may still hold their tasks."""
        connection = self._connection.connection_id
        for task in self._tasks.values():
            self._end_wait(task)
            task.sends_on = connection

    async def _end_redelivery_waits(self, seconds: float) -> None:
        """The agent stopped and has waited for its runs: what still waits for SAM to deliver its task again is sent
        on the current connection as it is, so SAM, which decides whether it still holds the task, takes it or refuses
        it, and the answer is not lost at the disconnect.

        ``retire`` takes the card down only: SAM goes on delivering on a connection it was told to deliver on, so the
        waits end here, at the end of the grace, and not when the agent begins to stop. A held answer is given what is
        left of the grace (``seconds``) to be taken by its delivery first. Nothing is sent when the link is down: the
        waits ended with it (``_on_link_change``), and the disconnect reports what was held.
        """
        if not self._connection.connected:
            return
        await self._wait_for_held_answers(seconds)
        if not self._connection.connected:
            return
        self._end_wait_of_unsent()
        connection = self._connection.connection_id
        for task in self._tasks.values():
            self._end_wait(task)
            task.sends_on = connection
        if self._unsent:
            log.warning(
                "SAM did not deliver answered tasks again while the agent stopped; sending their answers on this "
                "connection as they are answers=%d",
                len(self._unsent),
            )
            try:
                await within(FINAL_SEND_SECONDS, self._send_unsent(stop_when_the_link_drops=True))
            except TimeoutError:
                log.warning("answers still unsent at the end of the stop's grace answers=%d", len(self._unsent))

    async def _wait_for_held_answers(self, seconds: float) -> None:
        """Wait up to ``seconds`` until no answer is held for its task's delivery and none is being sent."""
        loop = asyncio.get_running_loop()
        deadline = loop.time() + seconds
        while loop.time() < deadline and (self._unsent or self._going_out or self._answering_again):
            await asyncio.sleep(HELD_ANSWER_POLL_SECONDS)

    def _on_link_change(self, connected: bool) -> None:
        """The link dropped: nothing is delivered on a connection that is gone, so the waits for a delivery end. What
        waited now waits for the link, as every frame does (``connect_timeout_seconds``), and the next connection
        starts the waits over."""
        if connected:
            return
        for task in self._tasks.values():
            self._end_wait(task)
        self._end_wait_of_unsent()

    def _stop_waiting_for_redeliveries(self) -> None:
        """The agent disconnects: no task is delivered again, so the waits for that end. The agent lets go of its
        tasks next, which is what ends a run that waited unanswered (``_may_send``)."""
        for task in self._tasks.values():
            self._end_wait(task)
        self._end_wait_of_unsent()

    @staticmethod
    def _end_wait(task: _Task) -> None:
        if task.redelivery_wait is not None:
            task.redelivery_wait.cancel()
            task.redelivery_wait = None
        task.delivered_here.set()

    def _end_wait_of_unsent(self) -> None:
        if self._unsent_wait is not None:
            self._unsent_wait.cancel()
            self._unsent_wait = None

    def _wait_for_unsent(self) -> None:
        if self._unsent and self._unsent_wait is None and self._connection.connected:
            loop = asyncio.get_running_loop()
            self._unsent_wait = loop.call_later(REDELIVERY_WAIT_SECONDS, self._unsent_wait_ran_out)

    def _wait_ran_out(self, task_id: str, task: _Task) -> None:
        """SAM did not deliver the task again: it closed the task, gave it to another instance of the agent, or holds
        it still and cannot have its request delivered again. The run sends on the current connection as it is, and
        SAM takes its frames or refuses them as naming no task (``_on_error``)."""
        log.warning(
            "SAM did not deliver a running task again after the agent link reconnected; its run sends on this "
            "connection as it is task=%s seconds=%g",
            task_id,
            REDELIVERY_WAIT_SECONDS,
        )
        task.sends_on = self._connection.connection_id
        self._end_wait(task)

    def _unsent_wait_ran_out(self) -> None:
        """Send the answers whose tasks SAM did not deliver again on the current connection as they are: SAM takes
        those of the tasks it still holds."""
        self._unsent_wait = None
        if self._unsent:
            log.warning(
                "SAM did not deliver answered tasks again after the agent link reconnected; sending their answers "
                "on this connection as they are answers=%d seconds=%g",
                len(self._unsent),
                REDELIVERY_WAIT_SECONDS,
            )
            self._connection.spawn(self._send_unsent())

    async def _may_send(self, task_id: str, task: _Task) -> bool:
        """Wait until SAM has delivered the task on the latest connection, or the wait for that ran out. False when
        SAM refused the task as unknown, or the agent let go of it meanwhile (a give-back, a disconnect)."""
        await task.delivered_here.wait()
        return not task.unknown_to_sam and self._tasks.get(task_id) is task

    async def _send_once_delivered(self, task_id: str, task: _Task, frame: Mapping[str, Any]) -> bool:
        """Send a run's prompt or peer call on a connection SAM delivered its task on, waiting for that delivery after
        a reconnect. False when the task was answered or let go of meanwhile, or never delivered again."""
        while await self._may_send(task_id, task) and not task.closed:
            try:
                await self._connection.send(frame, only_on=task.sends_on)
            except StaleConnection:
                await asyncio.sleep(0)
                continue
            return True
        return False

    async def _accept(self, task: IncomingTask, watch: DeliveryWatch | None) -> None:
        """Hand a registered task to the agent.

        When nothing runs it, the task is given back for SAM to deliver again, not held here.
        """
        try:
            try:
                await self._host.on_task(task)
            except Exception as exc:
                log.error("task not taken; given back task=%s err=%s", task.task_id, type(exc).__name__)
                await self._give_back(task.task_id, NOT_TAKEN_TWICE)
        finally:
            if watch is not None:
                watch.left()

    async def _refuse_unreadable(self, task_id: str, unreadable: _Task, watch: DeliveryWatch | None) -> None:
        answered: bool | None = None
        try:
            result = {"type": "result", "taskId": task_id, "state": "failed", "text": UNREADABLE_TASK_TEXT}
            answered = await self._send_answer(task_id, unreadable, [result])
        finally:
            await self._settle(task_id, unreadable, answered)
            if watch is not None:
                watch.left()

    def _on_cancel_frame(self, frame: dict) -> None:
        task_id = frame.get("taskId")
        if isinstance(task_id, str) and task_id:
            self._connection.spawn(self._host.on_cancel(task_id))
        else:
            log.warning("cancel without a task id")

    def _on_prompt_answer(self, frame: dict) -> None:
        request_id = str(frame.get("requestId") or "")
        ask = self._asks.get(request_id)
        if ask is None:
            log.debug("answer for a prompt that is not open request=%s", request_id)
            return
        answer = prompt_answer(ask.prompt, request_id, frame, self._limits)
        if answer.truncated:
            log.warning(
                "the entrypoint cut the user's answers to fit the agent link task=%s request=%s",
                ask.task_id,
                request_id,
            )
        self._answer(request_id, answer)

    def _on_peer_result(self, frame: dict) -> None:
        subtask_id = str(frame.get("subtaskId") or "")
        call = self._calls.pop(subtask_id, None)
        if call is None:
            log.debug("peer result for a call that is not open sub_task=%s", subtask_id)
            return
        if not call.future.done():
            result = peer_result(call.peer, frame)
            if result.truncated:
                log.warning(
                    "the entrypoint cut the peer's result (a string over its limit, or a result larger than a frame) "
                    "task=%s peer=%s sub_task=%s",
                    call.parent_task_id,
                    call.peer,
                    subtask_id,
                )
            call.future.set_result(result)

    def _on_peers(self, frame: dict) -> None:
        entries = frame.get("peers")
        if not isinstance(entries, list):
            log.warning("ignoring a peers message without a list of peers")
            return
        self._listed_peers = [p for p in (peer_info(entry) for entry in entries) if p is not None]
        self._listed_peers_known.set()

    def _on_error(self, frame: dict) -> None:
        """React to an error frame from the entrypoint, by what it refused.

        A refused describe or consume, prompt, peer call or result is routed to whatever waits on it. A frame that names
        nothing (a ref too long to echo, an unknown type) is only reported, and no state of the description or the
        delivery changes on it. `forbidden` is SAM's decision that a call is not allowed (the peer, the user, the call
        depth, a stop); any other code is a call SAM could not carry, which is no decision about access. A task the
        entrypoint closed while it still ran here (cancelled while this agent was disconnected, and so never delivered
        again) is stopped, whether SAM refused a step, a prompt, a peer call or the result of it, because nothing it
        does can reach anyone. SAM refuses the same way a task it holds for another connection of this agent that is
        still open.
        """
        code, ref, request_id = frame.get("code"), frame.get("ref"), frame.get("requestId")
        message = str(frame.get("message") or code or "")
        refused = frame.get("frame")
        if refused == "describe":
            self._describer.on_refused(code, message)
            return
        if refused == "consume":
            self._on_consume_refused(code, message)
            return
        if isinstance(request_id, str) and request_id in self._asks:
            log.warning(
                "the entrypoint refused a prompt code=%s task=%s request=%s message=%s", code, ref, request_id, message
            )
            self._answer(request_id, PromptAnswer(request_id, CANCELLED))
            if code != "unknown_task":
                return
        if not isinstance(ref, str) or not ref:
            log.warning("the entrypoint refused a message it could not name code=%s message=%s", code, message)
            return
        if ref in self._calls:
            log.warning("the entrypoint refused a peer call code=%s sub_task=%s message=%s", code, ref, message)
            parent_task_id = self._calls[ref].parent_task_id
            self._fail_call(
                ref, f"{PEER_REFUSED}: {message}", sam_decision=PEER_CALL_DENIED if code == "forbidden" else ""
            )
            if code != "unknown_task":
                return
            ref = parent_task_id
        if code in REFUSED_RESULT_CODES and ref in self._finals and ref not in self._tasks:
            self._answer_undeliverable(ref, code, message)
            return
        if code == "publish_failed" and ref in self._finals:
            frame_, tries, size, serial = self._finals[ref]
            if tries < FINAL_RESEND_TRIES:
                log.warning("SAM could not publish the result; sending it again task=%s attempt=%d", ref, tries + 1)
                self._finals.replace(ref, (frame_, tries + 1, size, serial))
                self._connection.spawn(self._resend(frame_, tries + 1))
            else:
                log.error("SAM could not publish the result; giving up task=%s", ref)
            return
        if code == "unknown_task" and ref in self._undeliverable and ref not in self._tasks:
            log.debug("SAM had closed the task before its failed answer came task=%s", ref)
            return
        if code == "unknown_task" and ref in self._finals and ref not in self._tasks:
            log.warning(
                "the result was not delivered: SAM no longer holds the task (it expired, or the gateway "
                "restarted), or holds it for another connection of this agent task=%s message=%s",
                ref,
                message,
            )
            report_lost(ref, self._sent_undelivered.pop(ref) or [])
            return
        if code == "unknown_task" and ref not in self._tasks:
            log.debug("the entrypoint has closed the task code=%s task=%s", code, ref)
            return
        if code == "unknown_task" and not self._tasks[ref].delivered_here.is_set():
            log.debug("SAM does not hold the task on this connection yet; waiting for its redelivery task=%s", ref)
            return
        if code == "unknown_task":
            log.warning(
                "SAM no longer holds a running task, or holds it for another connection of this agent; stopping it "
                "task=%s message=%s",
                ref,
                message,
            )
            if self._tasks[ref].undelivered_here:
                self._tasks[ref].unknown_to_sam = True
            self._connection.spawn(self._host.on_cancel(ref))
            return
        log.warning("the entrypoint refused a message code=%s ref=%s message=%s", code, ref, message)

    def _answer_undeliverable(self, task_id: str, code: Any, refusal: str) -> None:
        """Answer a task failed, once, after SAM refused a message naming it once it was answered.

        The task's delivery is acknowledged and the task forgotten once its result is sent, so a refused result would
        leave it open until SAM's task TTL. SAM's refusal names the task, not the frame, so the usage sent just before
        the result is kept within what the entrypoint takes (MAX_TOKENS_PER_MODEL_CALL, MAX_TOKENS_PER_TASK): what it
        refuses here is the result. The failed answer carries nothing SAM could refuse, so it is not answered again.
        """
        if task_id in self._undeliverable:
            log.error(
                "SAM refused the agent's failed answer too; the task stays open until SAM expires it "
                "task=%s code=%s message=%s",
                task_id,
                code,
                refusal,
            )
            return
        log.error(
            "SAM refused the result; answering the task failed task=%s code=%s message=%s", task_id, code, refusal
        )
        self._undeliverable.put(task_id, None)
        outcome = TaskOutcome("failed", error=TaskError(message=f"{UNDELIVERABLE}: {refusal}"))
        frame = result_frame(task_id, outcome, self._limits)
        self._keep_final(task_id, frame)
        self._connection.spawn(self._send_quietly(frame))

    def _on_description_refused(self, refusal: str) -> None:
        """Stop expecting tasks on this connection and fail the open that waits for the description.

        The entrypoint delivers nothing more on this connection, and refuses its consume, until it accepts a describe;
        the connection stays (running tasks finish, and no longer wait to be delivered again), and the next accepted
        describe is followed by a consume again.
        """
        self._consumed_on = -1
        self._expect_no_redeliveries()
        self._connection.spawn(self._send_unsent())
        watch = self._watch
        if watch is not None:
            watch.fail(LinkError(f"SAM refused the agent's description: {refusal}"))

    def _on_consume_refused(self, code: Any, message: str) -> None:
        """React to a refused consume.

        The consume that follows a refused describe is expected to be refused, so that is only logged quietly. Otherwise
        the agent is described again once, then the refusal is logged as an error.
        """
        describer = self._describer
        if describer.refusal:
            log.debug("SAM refused to deliver tasks: the description was refused code=%s message=%s", code, message)
            return
        if self._consume_retried == describer.generation or not describer.present or describer.described is None:
            log.error("SAM refused to deliver tasks code=%s message=%s", code, message)
            return
        log.error("SAM refused to deliver tasks; describing the agent again code=%s message=%s", code, message)
        self._consume_retried = describer.generation
        describer.described = None
        self._consumed_on = -1
        self._connection.spawn(self._describe_and_consume())

    async def _send_event(self, task_id: str, event: StepEvent) -> bool:
        """Send one step to the entrypoint, with the usage of a model call, if it can go out.

        A step is progress, not a result: it is dropped at once while the link is down, so a handler that awaits one per
        chunk does not wait out the connect timeout for each. The usage of a dropped model step stays with the task and
        goes with its answer. A model call's usage rides its `model_finished` (`TaskContext.record_usage` comes first),
        so the entrypoint records spend and the task's totals exactly once: usage is taken only once a frame carrying it
        went out.
        """
        task = self._tasks.get(task_id)
        if task is None or task.closed or not self._host.wants_events(task_id):
            return False
        if not self._connection.connected:
            log.debug("step dropped, the agent link is down task=%s kind=%s", task_id, event.kind)
            return False
        if not task.delivered_here.is_set():
            log.debug("step dropped, SAM has not delivered its task again task=%s kind=%s", task_id, event.kind)
            return False
        limits = self._limits
        ledger = self._ledger
        model = (
            ledger.reported_model(task_id, task.usage, event.model, limits)
            if isinstance(event, (ModelStarted, ModelFinished, ModelFailed))
            else ""
        )
        claim = (
            ledger.take(task_id, task.usage, event.model or UNKNOWN_MODEL)
            if isinstance(event, ModelFinished) and not task.undelivered_here
            else None
        )
        handed_over = False
        try:
            body = event_body(event, model, limits)
            if body is None:
                return False
            if claim is not None and isinstance(event, ModelFinished):
                body.update(ledger.fields(task_id, task.usage, event.model, claim.usage, limits, reported=model))
            body = fit_tool_payload(body, limits)
            frame = {"type": "event", "taskId": task_id, "event": body}
            if not self._fits(frame):
                frame["event"] = cap_strings(body, limits.max_string_bytes)
            if not self._fits(frame):
                log.warning("dropping a step larger than the agent link carries task=%s kind=%s", task_id, event.kind)
                if claim is not None:
                    usage_frame = ledger.frame(task_id, task.usage, claim.usage)
                    handed_over = True
                    await self._send_with_usage(task_id, task, claim, usage_frame)
                return False
            if claim is not None:
                handed_over = True
                return await self._send_with_usage(task_id, task, claim, frame)
            try:
                await self._send(frame, only_on=task.sends_on)
            except LinkError as exc:
                log.warning("step not sent task=%s kind=%s err=%s", task_id, event.kind, exc)
                return False
            return True
        finally:
            if claim is not None and not handed_over:
                claim.settle(sent=False)

    async def _send_with_usage(self, task_id: str, task: _Task, claim: UsageClaim, frame: dict) -> bool:
        """Send a frame that carries a model call's usage. A cancel of the step's caller does not interrupt the send,
        and the usage is settled by the send's own outcome, so it is neither sent twice nor lost unseen."""
        sending = asyncio.ensure_future(self._send(frame, only_on=task.sends_on))
        sending.add_done_callback(lambda done: claim.settle(sent=not done.cancelled() and done.exception() is None))
        try:
            await asyncio.shield(sending)
        except LinkError as exc:
            log.warning("model usage not sent; it goes with the task's answer task=%s err=%s", task_id, exc)
            return False
        return True

    def _usage_sink(self, task_id: str) -> UsageSink | None:
        delivered = self._tasks.get(task_id)
        if delivered is None:
            return None
        return self._ledger.sink(task_id, delivered.usage, lambda: self._tasks.get(task_id) is delivered)

    async def _ask(self, task_id: str, prompt: Prompt) -> PromptAnswer:
        """Ask the user a question through the entrypoint and wait for the answer.

        A prompt the entrypoint would answer cancelled at once (the task is over or cannot show one, it is too large,
        too many are open) is answered cancelled here without sending it. The wait is bounded by the prompt's own
        timeout plus a grace, after which the agent stops waiting by itself.
        """
        overflow = prompt_overflow(prompt, self._limits)
        if overflow:
            raise PromptTooLarge(overflow)
        task = self._tasks.get(task_id)
        if task is None or task.closed:
            return PromptAnswer(prompt.request_id, CANCELLED)
        if not task.can_prompt:
            log.info("not asking the user: the task's channel cannot show a prompt task=%s", task_id)
            return PromptAnswer(prompt.request_id, CANCELLED)
        limits = self._limits
        problem = prompt_problem(prompt, limits)
        if problem:
            log.warning("not asking the user: %s task=%s request=%s", problem, task_id, prompt.request_id)
            return PromptAnswer(prompt.request_id, CANCELLED)
        if len(self._asks) >= limits.max_open_prompts:
            log.warning(
                "not asking the user: the agent has as many open prompts as the agent link carries task=%s limit=%d",
                task_id,
                limits.max_open_prompts,
            )
            return PromptAnswer(prompt.request_id, CANCELLED)
        frame = prompt_frame(task_id, prompt, limits)
        if not self._fits(frame):
            frame = cap_strings(frame, limits.max_string_bytes)
            if not self._fits(frame):
                log.error("not asking the user: the prompt is larger than the agent link carries task=%s", task_id)
                return PromptAnswer(prompt.request_id, CANCELLED)
        future = asyncio.get_running_loop().create_future()
        self._asks[prompt.request_id] = _Ask(task_id, prompt, future)
        try:
            try:
                if not await self._send_once_delivered(task_id, task, frame):
                    return PromptAnswer(prompt.request_id, CANCELLED)
            except LinkError as exc:
                log.warning("prompt not sent task=%s request=%s err=%s", task_id, prompt.request_id, exc)
                return PromptAnswer(prompt.request_id, CANCELLED)
            log.info("waiting for the user task=%s request=%s", task_id, prompt.request_id)
            timeout = prompt.timeout_seconds if prompt.timeout_seconds > 0 else DEFAULT_PROMPT_TIMEOUT_SECONDS
            try:
                return await within(timeout + ANSWER_GRACE_SECONDS, asyncio.shield(future))
            except TimeoutError:
                log.warning(
                    "no answer from the entrypoint after the prompt's timeout task=%s request=%s",
                    task_id,
                    prompt.request_id,
                )
                return PromptAnswer(prompt.request_id, TIMED_OUT)
        finally:
            self._asks.pop(prompt.request_id, None)

    def _answer(self, request_id: str, answer: PromptAnswer) -> None:
        ask = self._asks.pop(request_id, None)
        if ask is not None and not ask.future.done():
            ask.future.set_result(answer)

    async def _finish(self, task_id: str, outcome: TaskOutcome) -> None:
        """Answer a task with its outcome and acknowledge its deliveries.

        The entrypoint drops the task's open prompts and cancels its open peer calls when the result closes it, so they
        end here too. The deliveries are acknowledged even when the result could not be built or sent, as SAM's own
        agents do: a redelivery would repeat every model and tool call of the task (``_settle`` has the two endings
        that acknowledge nothing).
        """
        task = self._tasks.get(task_id)
        if task is None:
            log.error("no open task to answer task=%s", task_id)
            return
        answered: bool | None = None
        try:
            for request_id in [r for r, a in self._asks.items() if a.task_id == task_id]:
                self._answer(request_id, PromptAnswer(request_id, CANCELLED))
            for subtask_id in [s for s, c in self._calls.items() if c.parent_task_id == task_id]:
                self._fail_call(subtask_id, "peer request cancelled", raw=True)
            task.closed = True
            usage = await self._ledger.remaining(task_id, task.usage)
            try:
                result = fit_result(result_frame(task_id, outcome, self._limits), self._limits)
            except Exception as exc:
                log.error(
                    "the result could not be encoded; answering the task failed task=%s err=%s",
                    task_id,
                    type(exc).__name__,
                )
                undeliverable = TaskOutcome(
                    "failed", error=TaskError(message=f"{UNDELIVERABLE}: it could not be encoded")
                )
                result = result_frame(task_id, undeliverable, self._limits)
            answered = await self._send_answer(task_id, task, usage + [result])
        except Exception as exc:
            log.error("result not sent task=%s err=%s", task_id, type(exc).__name__)
            answered = True
        finally:
            self._ledger.collect(task.usage)
            await self._settle(task_id, task, answered)

    async def _settle(self, task_id: str, task: _Task, answered: bool | None) -> None:
        """Forget a task and settle its deliveries by how its answer went. One whose answer was sent, or is held to be
        sent, is remembered as answered and acknowledged. One SAM refused as unknown (``answered`` False) is given
        back, which reaches only a delivery that came while its run was stopping, so that one starts the task afresh.
        One whose answer was cancelled on its way out (``answered`` None: the agent is disconnecting) is left as it
        is: its delivery returns to SAM when the connection closes, and acknowledging it would lose the task."""
        self._end_wait(task)
        self._tasks.pop(task_id, None)
        if answered:
            self._remember_answered(task_id)
            for connection, delivery in task.deliveries:
                await self._ack(connection, delivery)
        elif answered is False:
            await self._nack(task.deliveries)

    async def _give_back(self, task_id: str, failure: str = OUTSIDE_CANCEL_TWICE) -> None:
        """Give an unanswered task back to SAM to deliver again, and forget it here.

        The task's open prompts and peer calls end, and its open peer calls are cancelled at the entrypoint, because the
        task stays bound there and its calls are not wanted any more. A task given back once already by this process is
        answered failed instead.
        """
        task = self._tasks.get(task_id)
        if task is None:
            return
        if not self._given_back.first(task_id):
            log.warning("task given back once already by this agent; answered failed task=%s", task_id)
            await self._finish(task_id, TaskOutcome("failed", failure))
            return
        for request_id in [r for r, a in self._asks.items() if a.task_id == task_id]:
            self._answer(request_id, PromptAnswer(request_id, CANCELLED))
        calls = [s for s, c in self._calls.items() if c.parent_task_id == task_id]
        for subtask_id in calls:
            self._fail_call(subtask_id, "peer request cancelled", raw=True)
        lost = self._ledger.lost_frames(task_id, task.usage)
        self._end_wait(task)
        self._tasks.pop(task_id, None)
        report_lost(task_id, lost)
        for subtask_id in calls:
            await self._send_quietly({"type": "peer_cancel", "subtaskId": subtask_id})
        await self._nack(task.deliveries)

    async def _nack(self, deliveries: list[tuple[str, int]]) -> None:
        for connection, delivery in deliveries:
            try:
                await self._connection.send({"type": "nack", "delivery": delivery}, only_on=connection)
            except StaleConnection:
                log.debug("not giving back a delivery from an earlier connection: it went back when that one closed")
            except LinkError as exc:
                log.warning(
                    "give-back not sent; the delivery returns when the connection closes delivery=%s err=%s",
                    delivery,
                    exc,
                )

    async def _send_answer(self, task_id: str, task: _Task, frames: list[dict]) -> bool:
        """Send a task's last frames in order (its remaining usage, then its result), each on a connection SAM
        delivered the task on, waiting for that delivery after a reconnect as long as ``_may_send`` does. What the
        link cannot carry is kept and sent when SAM delivers the task again. False when SAM refused the task as
        unknown or the agent let go of it: what had not gone out by then is not sent, and its usage is reported lost,
        as it is when the send is cancelled."""
        self._keep_final(task_id, frames[-1])
        rest = frames
        try:
            while rest:
                if not await self._may_send(task_id, task):
                    self._lose_answer(task_id, frames if task.unknown_to_sam else rest)
                    return False
                try:
                    await self._send(rest[0], only_on=task.sends_on)
                except StaleConnection:
                    await asyncio.sleep(0)
                    continue
                except LinkError as exc:
                    self._keep_or_lose(task_id, rest, exc)
                    return True
                rest = rest[1:]
        except asyncio.CancelledError:
            log.warning("answer not sent: its sending was cancelled task=%s", task_id)
            self._lose_answer(task_id, rest)
            raise
        if task.undelivered_here:
            self._sent_undelivered.put(task_id, frames[:-1])
        return True

    def _lose_answer(self, task_id: str, unsent: list[dict]) -> None:
        self._forget_final(task_id)
        report_lost(task_id, unsent)

    async def _send_held(self, task_id: str, frames: list[dict], only_on: str | None = None) -> bool:
        """Send an answered task's frames in order; whether all went out. What the link cannot carry is kept for the
        task's next delivery, and what a cancel of the send leaves unsent is reported lost."""
        rest = frames
        try:
            while rest:
                await self._send(rest[0], only_on=only_on)
                rest = rest[1:]
        except LinkError as exc:
            self._keep_or_lose(task_id, rest, exc)
            return False
        except asyncio.CancelledError:
            log.warning("answer not sent: its sending was cancelled task=%s", task_id)
            report_lost(task_id, rest)
            raise
        return True

    async def _send_unsent(self, stop_when_the_link_drops: bool = False) -> None:
        """Send the answers the link could not carry, on a connection SAM delivers nothing again on. One that a
        delivery of its task took meanwhile is that delivery's to send. With ``stop_when_the_link_drops`` an answer
        that cannot go out because the link went down ends the send, as the rest would wait for the link as well; one
        that cannot go out on a link that is up (too large, say) does not stop the others."""
        for task_id in list(self._unsent):
            frames = self._unsent.pop(task_id)
            if frames is None:
                continue
            delivered_meanwhile = self._going_out[task_id] = []
            try:
                if not await self._send_held(task_id, frames):
                    if stop_when_the_link_drops and not self._connection.connected:
                        return
                    continue
                self._sent_undelivered.put(task_id, frames[:-1])
                for connection, delivery in delivered_meanwhile:
                    await self._ack(connection, delivery)
            finally:
                del self._going_out[task_id]

    def _keep_or_lose(self, task_id: str, unsent: list[dict], failure: LinkError) -> None:
        if self._connection.is_open:
            log.warning("result not sent; sending it after the agent link reconnects task=%s err=%s", task_id, failure)
            self._keep_unsent(task_id, unsent)
        else:
            log.error("result not sent: the agent link is closed task=%s", task_id)
            report_lost(task_id, unsent)

    def _keep_final(self, task_id: str, frame: dict) -> None:
        """Hold a result frame so it can be sent again if the entrypoint could not publish it or delivers its task
        again.

        A result frame can carry files up to MAX_HELD_BYTES, so it is held only as long as a resend can still happen.
        """
        self._final_serial += 1
        size = len(encode(frame))
        self._finals.put(task_id, (frame, 0, size, self._final_serial), size)
        self._cancel_final_timer(task_id)
        self._final_timers[task_id] = asyncio.get_running_loop().call_later(
            FINAL_HOLD_SECONDS, self._drop_final, task_id, self._final_serial
        )

    def _cancel_final_timer(self, task_id: str) -> None:
        timer = self._final_timers.pop(task_id, None)
        if timer is not None:
            timer.cancel()

    def _forget_final(self, task_id: str) -> None:
        self._finals.pop(task_id)
        self._cancel_final_timer(task_id)

    def _drop_final(self, task_id: str, serial: int) -> None:
        held = self._finals.get(task_id)
        if held is not None and held[3] == serial:
            self._finals.pop(task_id)
        self._final_timers.pop(task_id, None)

    def _keep_unsent(self, task_id: str, frames: list[dict]) -> None:
        self._unsent.put(task_id, frames, sum(len(encode(f)) for f in frames))
        self._wait_for_unsent()

    def _drop_unsent(self, task_id: str, frames: list[dict]) -> None:
        log.warning("result dropped: too many answers waiting for the agent link task=%s", task_id)
        report_lost(task_id, frames)

    def _spawn_answer_again(self, task_id: str, frames: list[dict], connection: str, delivery: int) -> None:
        self._answering_again.add(task_id)
        self._connection.spawn(self._answer_again(task_id, frames, connection, delivery))

    async def _answer_again(self, task_id: str, frames: list[dict], connection: str, delivery: int) -> None:
        """Send an answered task's held answer on the connection SAM delivered the task again on, then acknowledge
        that delivery: what never went out of it, or its result alone when all of it did.

        The entrypoint acknowledges a task it holds the result of itself, so a task it delivers again was not answered
        there: the answer was never sent, reached a replica that no longer held the task (a gateway restart), or was
        lost with the connection. The replica that has the task now takes it; one that had it already refuses it as
        unknown_task. An answer that does not go out now waits for the next delivery, which this one, left
        unacknowledged, becomes.
        """
        try:
            if await self._send_held(task_id, frames, only_on=connection):
                await self._ack(connection, delivery)
        finally:
            self._answering_again.discard(task_id)

    async def _resend(self, frame: dict, attempt: int) -> None:
        await asyncio.sleep(FINAL_RESEND_DELAY_SECONDS * attempt)
        await self._send_quietly(frame)

    async def _ack(self, connection: str, delivery: int) -> None:
        """A delivery number is the connection's own: it is acknowledged only on that connection, checked once the
        send has waited out any reconnect."""
        try:
            await self._connection.send({"type": "ack", "delivery": delivery}, only_on=connection)
        except StaleConnection:
            log.debug("not acknowledging a delivery from an earlier connection")
        except LinkError as exc:
            log.warning("ack not sent delivery=%s err=%s", delivery, exc)

    def _remember_answered(self, task_id: str) -> None:
        self._answered.put(task_id, None)

    def _peers(self) -> list[PeerInfo]:
        return list(self._listed_peers)

    def _assume_peer(self, name: str) -> None:
        """Let calls to ``name`` go out although the entrypoint has not listed it, so the entrypoint's own refusal
        can be seen (tests)."""
        self._assumed_peers.add(name)

    async def _call_peer(self, call: PeerCall) -> PeerResult:
        """Ask a peer through the entrypoint and wait for its final answer.

        The wait is also bounded by the task's own time limit and by the entrypoint's (a peer silent for
        `task_ttl_seconds` ends the call failed). An answer that finds the agent between connections is held and sent
        after the welcome; one lost to a gateway restart is not waited for past `peer_call_timeout_seconds`. Cancelling
        the wait cancels the call.
        """
        peer = call.peer
        task = self._tasks.get(call.parent_task_id)
        if task is None or task.closed:
            return PeerResult(peer, "error", f"task {call.parent_task_id} is not running")
        if not call.text:
            return PeerResult(peer, "error", "task_description is required")
        if peer not in self._assumed_peers and all(p.name != peer for p in self._listed_peers):
            return PeerResult(peer, "error", f'peer agent "{peer}" is not available on the mesh')
        limits = self._limits
        if len(self._calls) >= limits.max_open_peer_calls:
            return PeerResult(
                peer,
                "error",
                f'peer agent "{peer}" could not be called: the agent has as many open '
                f"peer calls as the agent link carries ({limits.max_open_peer_calls})",
            )
        subtask_id = uuid7_str()
        frame = {
            "type": "peer_call",
            "parentTaskId": call.parent_task_id,
            "subtaskId": subtask_id,
            "peer": peer,
            "text": call.text,
        }
        if call.call_id:
            frame["callId"] = fit_id(call.call_id, limits.max_id_bytes)
        if not self._fits(frame):
            return PeerResult(peer, "error", f'peer agent "{peer}": the request is larger than the agent link carries')
        pending = _Call(call.parent_task_id, peer, asyncio.get_running_loop().create_future())
        self._calls[subtask_id] = pending
        sent = False
        try:
            try:
                sent = await self._send_once_delivered(call.parent_task_id, task, frame)
            except LinkError as exc:
                self._calls.pop(subtask_id, None)
                log.error("peer call not sent task=%s peer=%s err=%s", call.parent_task_id, peer, exc)
                return PeerResult(
                    peer, "error", f'peer agent "{peer}" could not be called: the agent link is not connected'
                )
            if not sent:
                self._calls.pop(subtask_id, None)
                return PeerResult(peer, "error", f"task {call.parent_task_id} is not running")
            log.info(
                "peer call sent task=%s peer=%s sub_task=%s chars=%d",
                call.parent_task_id,
                peer,
                subtask_id,
                len(call.text),
            )
            timeout = self.config.peer_call_timeout_seconds
            try:
                result = await within(timeout if timeout > 0 else None, asyncio.shield(pending.future))
            except TimeoutError:
                self._calls.pop(subtask_id, None)
                await asyncio.shield(self._send_quietly({"type": "peer_cancel", "subtaskId": subtask_id}))
                log.warning(
                    "peer call unanswered; giving up task=%s peer=%s sub_task=%s seconds=%g",
                    call.parent_task_id,
                    peer,
                    subtask_id,
                    timeout,
                )
                return PeerResult(peer, "error", f'peer agent "{peer}": no answer within {timeout:g} s')
        except asyncio.CancelledError:
            if self._calls.pop(subtask_id, None) is not None and sent:
                await asyncio.shield(self._send_quietly({"type": "peer_cancel", "subtaskId": subtask_id}))
            raise
        log.info(
            "peer result received task=%s peer=%s sub_task=%s state=%s",
            call.parent_task_id,
            peer,
            subtask_id,
            result.state,
        )
        return result

    def _fail_call(self, subtask_id: str, reason: str, *, raw: bool = False, sam_decision: str = "") -> None:
        call = self._calls.pop(subtask_id, None)
        if call is None:
            return
        if not call.future.done():
            text = reason if raw else f'peer agent "{call.peer}": {reason}'
            call.future.set_result(PeerResult(call.peer, "error", text, sam_decision=sam_decision))

    def _fits(self, frame: Mapping[str, Any]) -> bool:
        return fits(frame, self._limits)

    async def _send(self, frame: Mapping[str, Any], *, only_on: str | None = None) -> str:
        """Send a frame; returns the id of the connection it went out on. With ``only_on``, only on that connection
        (``StaleConnection`` otherwise)."""
        if not self._fits(frame):
            raise LinkError(f"{frame.get('type')} message larger than the link's maxFrameBytes")
        return await self._connection.send(frame, only_on=only_on)

    async def _send_quietly(self, frame: Mapping[str, Any]) -> None:
        try:
            await self._send(frame)
        except LinkError as exc:
            log.warning("%s not sent err=%s", frame.get("type"), exc)
