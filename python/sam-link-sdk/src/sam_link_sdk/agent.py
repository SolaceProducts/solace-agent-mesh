# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""MeshAgent: joins a SAM mesh and serves its tasks with a user handler.

The agent core knows tasks, steps, prompts, peers and its own description; its transport is how it reaches SAM:
``sam_link_sdk.link.LinkTransport``, the agent link to SAM's entrypoint.
"""

from __future__ import annotations

import asyncio
import functools
import importlib
import logging
import os
import signal
import sys
from collections.abc import Awaitable, Callable
from typing import Any

from ._frameworks import described
from ._health import Health
from ._model import AgentDescription, Declared, IncomingTask, Transport, as_transport
from ._util import TrackedTasks
from .audit import AuditLogger
from .config import AgentConfig
from .metrics import NoMetrics
from .serverless import WAKE_IDLE_SECONDS, _WakeHandler
from .stop import StopState, _StopController
from .tasks import Handler, _TaskRunner

__all__ = ["Handler", "MeshAgent"]

log = logging.getLogger("sam_link_sdk.agent")


@functools.cache
def sdk_version() -> str:
    """The installed version of the distribution that ships this module, whatever the distribution is named."""
    from importlib import metadata

    try:
        for dist in metadata.packages_distributions().get(__name__.partition(".")[0], ()):
            return metadata.version(dist)
    except metadata.PackageNotFoundError:
        pass
    return "0.0.0"


def _link_from_env(name: str) -> Transport:
    """The agent link transport, configured from the environment.

    Imported by name because the core does not import the link package (tests/test_layering.py); the default transport
    is its one use.
    """
    link = importlib.import_module("sam_link_sdk.link")
    return as_transport(link.LinkTransport(link.LinkConfig.from_env(agent=name)))


def _describe(config: AgentConfig, health: Health) -> AgentDescription:
    return AgentDescription(
        display_name=config.display_name,
        description=config.description,
        version=config.version,
        skills=tuple(config.skills),
        tools=tuple(config.tools),
        required_scopes=tuple(config.required_scopes),
        input_schema=config.input_schema,
        output_schema=config.output_schema,
        welcome_message=config.welcome_message,
        suggestions=tuple(config.suggestions),
        peers=tuple(config.peers),
        streaming=config.streaming,
        declared=Declared(
            framework=described(config.framework),
            owner=config.owner,
            team=config.team,
            sdk_version=sdk_version(),
            data_access=tuple(config.data_access),
            mcp_servers=tuple(config.mcp_servers),
        ),
        health=health.problems(),
    )


class _Lifecycle:
    """Where the agent is between start and stop: what its stop, its wake and its transport's host read."""

    def __init__(self) -> None:
        self.stopping = False
        self.stopped = asyncio.Event()
        self.serving_ended = False
        self.exit = asyncio.Event()
        """Set to end ``run_forever``: a signal, SAM's stop with ``exit_on_stop``, or the link giving up."""
        self.link_failure: Exception | None = None

    @property
    def draining(self) -> bool:
        return self.stopping or self.serving_ended

    def fail(self, error: Exception) -> None:
        """The transport gave up for good (the entrypoint kept refusing the agent's token): ``run_forever`` stops
        gracefully and raises ``error``, so a supervisor sees the process fail."""
        self.link_failure = error
        self.exit.set()


class _MeshHost:
    """The agent core as its transport sees it (``TransportHost``): the callbacks it calls, kept off MeshAgent's
    public API, each handed to the collaborator that serves it."""

    def __init__(
        self,
        *,
        config: AgentConfig,
        audit: AuditLogger,
        health: Health,
        lifecycle: _Lifecycle,
        runner: _TaskRunner,
        stopper: _StopController,
    ):
        self.config = config
        self.audit = audit
        self._health = health
        self._lifecycle = lifecycle
        self._runner = runner
        self._stopper = stopper

    @property
    def draining(self) -> bool:
        return self._lifecycle.draining

    @property
    def stop_reason(self) -> str:
        return self._stopper.reason

    def description(self) -> AgentDescription:
        return _describe(self.config, self._health)

    async def on_task(self, task: IncomingTask) -> None:
        await self._runner.on_task(task)

    async def on_cancel(self, task_id: str) -> None:
        await self._runner.on_cancel(task_id)

    def on_control(self, enabled: bool, reason: str) -> Awaitable[None] | None:
        return self._stopper.on_control(enabled, reason)

    def on_connection(self, connected: bool) -> None:
        self._stopper.on_connection(connected)

    def wants_events(self, task_id: str) -> bool:
        return self._runner.wants_events(task_id)

    def on_link_failure(self, error: Exception) -> None:
        self._lifecycle.fail(error)


def _exit_on_signals(loop: asyncio.AbstractEventLoop, exit_: Callable[[], None]) -> list[signal.Signals]:
    """Call ``exit_`` on SIGINT and SIGTERM, and return the signals handled. A loop that cannot handle signals (on
    Windows, or off the main thread) handles none: the agent still serves, and stops when its task is cancelled."""
    handled = []
    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, exit_)
        except (NotImplementedError, RuntimeError, ValueError) as exc:
            log.warning("the agent cannot stop on SIGINT or SIGTERM here; cancel its task to stop it err=%s", exc)
            break
        handled.append(sig)
    return handled


class MeshAgent:
    def __init__(self, config: AgentConfig, handler: Handler, *, transport: Transport | None = None):
        """``handler`` serves each task. The agent joins SAM through the agent link built from the environment
        (``LinkConfig.from_env``) as ``config.name``; ``transport`` replaces that with a ``sam_link_sdk.link.LinkTransport``
        built in code, whose ``LinkConfig.agent`` must be ``config.name`` (start refuses it otherwise).

        The agent is a few collaborators around its transport: ``_runner`` runs the tasks, ``_stopper`` applies SAM's
        stop, ``_waker`` serves a request-driven agent's wake, and ``_host`` is what the transport calls."""
        self._transport: Transport = as_transport(transport) if transport is not None else _link_from_env(config.name)
        self.config = config
        self._display_name = config.display_name or config.name
        self._metrics = config.metrics or NoMetrics()
        self._health = Health()
        self._lifecycle = _Lifecycle()
        audit = AuditLogger(config.audit_sink)
        settling = TrackedTasks(log, "background work failed")
        self._settling = settling
        stop_state = StopState(config, key=getattr(self._transport, "_stop_key", ""))
        self._runner = _TaskRunner(
            config=config,
            handler=handler,
            transport=self._transport,
            stop_state=stop_state,
            audit=audit,
            metrics=self._metrics,
            display_name=self._display_name,
            settling=settling,
        )
        self._stopper = _StopController(
            config=config,
            transport=self._transport,
            state=stop_state,
            runner=self._runner,
            audit=audit,
            health=self._health,
            settling=settling,
            request_exit=self._lifecycle.exit.set,
        )
        self._waker = _WakeHandler(
            transport=self._transport, stopper=self._stopper, runner=self._runner, lifecycle=self._lifecycle
        )
        self._host = _MeshHost(
            config=config,
            audit=audit,
            health=self._health,
            lifecycle=self._lifecycle,
            runner=self._runner,
            stopper=self._stopper,
        )
        self._transport._bind(self._host)

    async def start(self) -> None:
        """Connect to the mesh, publish the agent's description and start serving tasks.

        Clears the end of serving that an `announce()` before `start()` set, since that ended its own connection, not
        this one.
        """
        cfg = self.config
        self._lifecycle.serving_ended = False
        await self._transport._open(serving=True)
        self._metrics.active_executions(lambda: {self._display_name: self._runner.active})
        if self._stopper.allowed:
            await self._transport._publish_description()
        if cfg.keepalive_seconds > 0:
            self._runner.start_keepalive()
        log.info("agent started name=%s", cfg.name)

    async def stop(self) -> None:
        """Graceful shutdown, as SAM-Go agents do it: announce removal so no new work is routed here, let running
        tasks finish for up to `shutdown_grace_seconds`, then abandon what is left and disconnect.

        An answer held for SAM to deliver its task again waits for that for the grace as well, so a stop can take the
        whole grace with no task running; what still waits when it ends is sent as it is before the disconnect.

        A request still running at the deadline is left unacknowledged, so SAM redelivers it to another replica
        (which repeats the task's model and tool calls). Requests that arrive while draining are not started and
        go back to the queue when this agent disconnects.

        The card stays withdrawn until the agent starts again, because a later card would put it back on the mesh. A
        stop cancelled mid-drain (a timeout around it) still tears down, so a retry has nothing left to do.
        """
        life = self._lifecycle
        if self._settling.running_here():
            return
        if life.stopping:
            await life.stopped.wait()
            return
        life.stopping = True
        try:
            await self._transport._stop_background()
            await self._transport._withdraw_description()
            await self._runner.drain()
            await self._runner.let_stopped_answer()
            await self._transport._flush()
        finally:
            try:
                self._runner.cancel_keepalive()
                self._stopper.cancel_timers()
                self._runner.abandon_running()
                await self._transport._disconnect()
            finally:
                life.stopped.set()

    async def run_forever(self, *, exit_when_stuck: bool = True) -> None:
        """Start, serve until SIGINT/SIGTERM (or, with exit_on_stop, until SAM stops the agent), then stop
        gracefully. When the link gives up on a refused token, raises that error once stopped.

        A run the agent left behind (abandoned at the end of the shutdown grace, or answered without it after a limit)
        may still be going: a framework's run in a worker thread cannot be cancelled, and a handler can swallow its
        cancel. Python would wait for it before the process ends, for as long as it runs. So with ``exit_when_stuck``
        (the default), once stopped, an agent that left a run behind ends the process itself: exit code 0, or 1 after
        logging the error when the link gave up. Logging is flushed first; ``atexit`` handlers do not run. Pass False
        when the agent shares its process with other work that must outlive it."""
        loop = asyncio.get_running_loop()
        life = self._lifecycle
        handled = _exit_on_signals(loop, life.exit.set)
        try:
            await self.start()
            try:
                await life.exit.wait()
            finally:
                await self.stop()
        finally:
            for sig in handled:
                loop.remove_signal_handler(sig)
        if exit_when_stuck and self._runner.left_behind:
            self._end_process()
        if life.link_failure is not None:
            raise life.link_failure

    async def handle_wake(self, idle_seconds: float = WAKE_IDLE_SECONDS) -> int:
        """Serve the tasks waiting for the agent, then leave: for request-driven runtimes woken by a doorbell.

        The doorbell (the runtime's invocation endpoint, called when a task waits) carries nothing of the task. The
        agent connects, describes itself and takes its tasks the way a long-running agent does, answers each one,
        and returns once no task is running and nothing has been delivered for ``idle_seconds``. Answers to its
        prompts arrive on the same connection. Returns how many tasks were delivered; a wake that finds nothing
        waiting (an earlier wake took it) returns 0.
        """
        return await self._waker.serve(idle_seconds)

    async def announce(self) -> None:
        """Describe the agent once, for a request-driven agent that is not connected between requests, so SAM lists
        it while it sleeps (marked on-demand: SAM derives that from the entrypoint's ``wake`` configuration for the
        agent, not from the description). SAM's entrypoint keeps that listing in memory: after the gateway
        restarts, the agent is not listed until it announces again (or a wake describes it). A stopped agent is not
        announced."""
        await self._waker.announce()

    async def retire(self) -> None:
        """Take a request-driven agent off the mesh, so SAM stops listing it. Run when the runtime deployment is
        deleted; nothing else takes an on-demand listing down."""
        await self._waker.retire()

    @property
    def allowed(self) -> bool:
        """Whether SAM lets this agent run. Why it does not (``stopped_by_sam``, ``disconnected``) is the ``reason`` of
        the ``AgentStopped`` that ``check()`` raises, and the second argument of an ``on_stop_change`` listener."""
        return self._stopper.allowed

    def check(self) -> None:
        """Raise AgentStopped while SAM has stopped this agent. Call it in your own entry points (an API, a
        script, a scheduler) so the agent stops there too, not only on the mesh."""
        self._stopper.check()

    def guard(self, fn: Callable[..., Any]) -> Callable[..., Any]:
        """Decorate a function (sync or async) so it raises AgentStopped while SAM has stopped this agent."""
        return self._stopper.guard(fn)

    def on_stop_change(self, listener: Callable[[bool, str], Any]) -> None:
        """Call listener(allowed, reason) when SAM stops or releases this agent, e.g. to close your own API."""
        self._stopper.on_change(listener)

    def set_health_problem(self, id: str, severity: str, detail: str) -> None:
        self._health.set(id, severity, detail)

    def clear_health_problem(self, id: str) -> None:
        self._health.clear(id)

    def _end_process(self) -> None:
        failure = self._lifecycle.link_failure
        if failure is not None:
            log.error("agent link gave up err=%s: %s", type(failure).__name__, failure)
        log.warning(
            "ending the process: a run left behind may still be going and would keep it alive runs=%d",
            self._runner.left_behind,
        )
        logging.shutdown()
        for stream in (sys.stdout, sys.stderr):
            try:
                stream.flush()
            except (OSError, ValueError):
                pass
        os._exit(1 if failure is not None else 0)
