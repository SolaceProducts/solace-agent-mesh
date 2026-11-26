# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""SAM's stop, as the agent core keeps it: whether the agent may run, and what changes when it may not.

SAM can stop an agent from its console. The transport tells the core (``TransportHost.on_control``); while stopped
the agent takes no tasks, stops the ones running, leaves the mesh, and ``agent.check()`` / ``agent.guard`` refuse
work in every code path that uses them, including the agent's own API outside SAM. It is cooperative: code that does
not use these checks keeps running.

The core keeps the stop the transport reports:

- a stop is saved under ``state_dir``, so a restarted long-running agent starts stopped until the transport reports
  SAM's current state;
- it fails closed: once connected, if the transport has been disconnected for longer than ``offline_grace_seconds``
  the agent counts as stopped until it reconnects and the transport reports SAM's current state.

A request-driven agent connects before it serves a request and learns SAM's state then, so neither applies to it.
"""

from __future__ import annotations

import asyncio
import contextlib
import functools
import hashlib
import inspect
import json
import logging
import os
import pathlib
import re
import tempfile
import time
from collections.abc import Callable
from typing import TYPE_CHECKING, Any

from .config import AgentConfig

__all__ = ["AgentStopped"]

if TYPE_CHECKING:
    from ._health import Health
    from ._model import Transport
    from ._util import TrackedTasks
    from .audit import AuditLogger
    from .tasks import _TaskRunner

log = logging.getLogger("sam_link_sdk.agent")

REASON_STOPPED_BY_SAM = "stopped_by_sam"
REASON_DISCONNECTED = "disconnected"
STOP_SAVE_RETRY_SECONDS = 5.0
STOP_UNSAVED_PROBLEM = "stop_state_unsaved"

_UNSAFE = re.compile(r"[^A-Za-z0-9_.-]")
_MAX_KEY_CHARS = 200


class AgentStopped(RuntimeError):
    """Raised by MeshAgent.check() and guarded calls while SAM has stopped the agent."""

    def __init__(self, reason: str):
        super().__init__(f"agent stopped by SAM ({reason})")
        self.reason = reason


def stop_state_path(config: AgentConfig, key: str = "") -> pathlib.Path:
    """Where the agent keeps SAM's stop: ``<state_dir>/<key>.stop.json``, ``key`` naming the agent on its transport
    (the transport's ``_stop_key``; the agent's name when the transport has none), made filename-safe.

    A file name has a length limit (255 bytes on most file systems), so a long key keeps its end, which names the agent,
    behind a digest of the whole key.
    """
    base = pathlib.Path(config.state_dir).expanduser() if config.state_dir else pathlib.Path.home() / ".sam_link_sdk"
    name = _UNSAFE.sub("_", key or config.name)
    if len(name) > _MAX_KEY_CHARS:
        digest = hashlib.sha256((key or config.name).encode()).hexdigest()[:16]
        name = f"{digest}_{name[-(_MAX_KEY_CHARS - len(digest) - 1) :]}"
    return base / f"{name}.stop.json"


class StopState:
    """SAM's stop as the core keeps it (see the module doc), as a small state machine.

    State: ``stopped`` (SAM's last word, saved under ``state_dir``), ``contact`` (a connection SAM's state is known
    on) and ``offline_since`` (when the last contact was lost; None while in contact, or before the first contact).

    Events, each applied at once, in the order the transport reports them:

    ============================ ===================================================================================
    ``control(enabled)``         save the stop, then ``stopped = not enabled`` (a welcome's ``enabled``, a control)
    ``connection(True)``         contact: ``offline_since = None``; the caller cancels the offline timer
    ``connection(False)``        contact lost: ``offline_since = now`` (kept if already set); returns how long until
                                 the grace runs out, and the caller (re)arms the offline timer for it, every time
    ``forget_saved()``           a request-driven agent: neither a saved stop nor an outage applies, and nothing
                                 is saved from then on
    ============================ ===================================================================================

    A write that fails leaves the change in effect in memory and ``unsaved`` set; ``save_again()`` tries the write
    again (the agent calls it on every event and on a timer) until it succeeds.

    ``reason`` is ``stopped_by_sam`` while stopped, ``disconnected`` once ``offline_since`` is older than the grace,
    else "". A drop before the first contact does not count: the agent has not learnt SAM's state yet, and starts
    from the saved one.
    """

    def __init__(self, config: AgentConfig, clock: Callable[[], float] = time.monotonic, key: str = ""):
        self._path = stop_state_path(config, key)
        self._grace = config.offline_grace_seconds
        self._clock = clock
        self.stopped = self._load()
        self.unsaved = False
        """The file does not hold ``stopped``: its last write failed."""
        self._persist = True
        self._offline_since: float | None = None
        self._contacted_once = False

    @property
    def reason(self) -> str:
        if self.stopped:
            return REASON_STOPPED_BY_SAM
        if self._offline_since is not None and self._clock() - self._offline_since > self._grace:
            return REASON_DISCONNECTED
        return ""

    def control(self, enabled: bool) -> None:
        """SAM's current word on the agent. A stop is saved before it takes effect, so a crash in between leaves the
        agent stopped on restart; a save that fails does not hold the stop back, and is tried again (``unsaved``)."""
        if self.stopped == enabled:
            self.unsaved = not self._save(not enabled)
            self.stopped = not enabled
        else:
            self.save_again()

    def save_again(self) -> bool:
        """Write the state if its last write failed. Returns whether the file now holds it."""
        if self.unsaved:
            self.unsaved = not self._save(self.stopped)
            if not self.unsaved:
                log.info("stop state saved after earlier failures path=%s stopped=%s", self._path, self.stopped)
        return not self.unsaved

    def connection(self, connected: bool) -> float | None:
        """Note contact made or lost; for a loss, returns how long until it counts as a stop (None: it does not)."""
        if connected:
            self._contacted_once = True
            self._offline_since = None
            return None
        if not self._contacted_once:
            return None
        if self._offline_since is None:
            self._offline_since = self._clock()
        return max(self._grace - (self._clock() - self._offline_since), 0.0)

    def forget_saved(self) -> None:
        """A request-driven agent learns SAM's state when it connects, before it serves anything, so it neither reads
        nor writes the file (which a long-running agent of the same name on this host may share)."""
        self.stopped = False
        self.unsaved = False
        self._persist = False
        self._offline_since = None
        self._contacted_once = False

    def _load(self) -> bool:
        """Read whether the agent was stopped when it last ran.

        An unreadable state file must not silently release a stopped agent, so it reads as stopped.
        """
        try:
            raw = json.loads(self._path.read_text())
            return bool(raw.get("stopped")) if isinstance(raw, dict) else True
        except FileNotFoundError:
            return False
        except (ValueError, OSError) as exc:
            log.error("stop state unreadable, staying stopped path=%s err=%s", self._path, type(exc).__name__)
            return True

    def _save(self, stopped: bool) -> bool:
        """Write the state to a temporary file of its own, then rename it over the state file: agents of the same name
        on one host (replicas) share the state file, never a half-written one. Logged at ERROR once per run of
        failures."""
        if not self._persist:
            return True
        tmp = ""
        try:
            self._path.parent.mkdir(parents=True, exist_ok=True)
            fd, tmp = tempfile.mkstemp(dir=self._path.parent, prefix=f".{self._path.name}.", suffix=".tmp")
            with os.fdopen(fd, "w") as out:
                out.write(json.dumps({"stopped": stopped}))
            os.replace(tmp, self._path)
        except OSError as exc:
            log.log(
                logging.DEBUG if self.unsaved else logging.ERROR,
                "stop state not saved; a restart starts from the earlier state until it is, retrying path=%s err=%s",
                self._path,
                type(exc).__name__,
            )
            if tmp:
                with contextlib.suppress(OSError):
                    os.unlink(tmp)
            return False
        return True


class _StopController:
    """The agent's side of SAM's stop: the public check/guard/listener API and the reaction to a change.

    ``state`` is what the transport's reports add up to (``StopState``); the controller applies a change: it stops the
    running tasks, tells the mesh and the listeners, keeps the saved stop written and arms the offline timer."""

    def __init__(
        self,
        *,
        config: AgentConfig,
        transport: Transport,
        state: StopState,
        runner: _TaskRunner,
        audit: AuditLogger,
        health: Health,
        settling: TrackedTasks,
        request_exit: Callable[[], None],
    ):
        self._config = config
        self._transport = transport
        self.state = state
        self._runner = runner
        self._audit = audit
        self._health = health
        self._settling = settling
        self._request_exit = request_exit
        self._listeners: list[Callable[[bool, str], Any]] = []
        self._offline_timer: asyncio.TimerHandle | None = None
        self._save_retry: asyncio.TimerHandle | None = None
        self._announced_allowed = True
        self.request_driven = False
        """Set by a wake: the agent is asleep between requests, so a stop must not take its card down."""
        if state.stopped:
            log.warning("agent starts stopped: SAM stopped it before this restart")
            self._announced_allowed = False

    @property
    def allowed(self) -> bool:
        """Whether SAM lets this agent run."""
        return not self.reason

    @property
    def reason(self) -> str:
        return self.state.reason

    def check(self) -> None:
        """Raise AgentStopped while SAM has stopped this agent. Call it in your own entry points (an API, a
        script, a scheduler) so the agent stops there too, not only on the mesh."""
        reason = self.reason
        if reason:
            raise AgentStopped(reason)

    def guard(self, fn: Callable[..., Any]) -> Callable[..., Any]:
        """Decorate a function (sync or async) so it raises AgentStopped while SAM has stopped this agent."""
        if inspect.iscoroutinefunction(fn):

            @functools.wraps(fn)
            async def guarded_async(*args, **kwargs):
                self.check()
                return await fn(*args, **kwargs)

            return guarded_async

        @functools.wraps(fn)
        def guarded(*args, **kwargs):
            self.check()
            return fn(*args, **kwargs)

        return guarded

    def on_change(self, listener: Callable[[bool, str], Any]) -> None:
        """Call listener(allowed, reason) when SAM stops or releases this agent, e.g. to close your own API."""
        self._listeners.append(listener)

    def begin_request_driven(self) -> None:
        """A request-driven agent connects before it serves: the saved stop does not apply, and SAM's word on
        connecting is news only if it is a stop. A reused agent keeps what its listeners last heard, so a release
        reaches them."""
        reused = self.request_driven
        self.request_driven = True
        self.state.forget_saved()
        if not reused:
            self._announced_allowed = True

    def cancel_timers(self, *, forget: bool = False) -> None:
        """Cancel the offline timer and the save retry, for a disconnect; ``forget`` also lets later events arm them
        again (a wake that ends)."""
        if self._offline_timer is not None:
            self._offline_timer.cancel()
        if self._save_retry is not None:
            self._save_retry.cancel()
        if forget:
            self._offline_timer = None
            self._save_retry = None

    def on_control(self, enabled: bool, reason: str) -> asyncio.Task | None:
        """SAM's word, applied at once: a stop stops running tasks before this returns. Returns the task that tells
        the mesh and the listeners, for a caller that waits for them (None when nothing changed)."""
        self.state.control(enabled)
        self._watch_stop_saved()
        announced = self._settle_stop(reason)
        if announced is not None:
            self._audit.agent_control(
                agent_name=self._config.name,
                operator="",
                stopped=not enabled,
                reason=reason or (REASON_STOPPED_BY_SAM if not enabled else ""),
            )
        return announced

    def _watch_stop_saved(self) -> None:
        """Report a stop state the file does not hold, and keep trying to write it until it does."""
        if not self.state.save_again():
            if self._save_retry is None:
                self._health.set(
                    STOP_UNSAVED_PROBLEM, "degraded", "SAM's stop could not be saved; a restart would not know it"
                )
                self._save_retry = asyncio.get_running_loop().call_later(STOP_SAVE_RETRY_SECONDS, self._retry_stop_save)
            return
        if self._save_retry is not None:
            self._save_retry.cancel()
            self._save_retry = None
        self._health.clear(STOP_UNSAVED_PROBLEM)

    def _retry_stop_save(self) -> None:
        self._save_retry = None
        self._watch_stop_saved()

    def on_connection(self, connected: bool) -> None:
        """Track contact with SAM and arm the offline grace when it is lost.

        The timer fires a hair past the grace period, so the state reads as stopped when it does.
        """
        self._watch_stop_saved()
        remaining = self.state.connection(connected)
        if self._offline_timer is not None:
            self._offline_timer.cancel()
            self._offline_timer = None
        if connected:
            self._settle_stop()
            return
        if remaining is None:
            return
        log.warning(
            "lost contact with SAM; the agent stops if it lasts longer than %ss", self._config.offline_grace_seconds
        )
        self._offline_timer = asyncio.get_running_loop().call_later(remaining + 0.01, self._settle_stop)

    def _settle_stop(self, release_reason: str = "") -> asyncio.Task | None:
        allowed = self.allowed
        if allowed == self._announced_allowed:
            return None
        return self._apply_stop(allowed, release_reason if allowed else self.reason)

    def _apply_stop(self, allowed: bool, reason: str) -> asyncio.Task:
        """Take a change of SAM's stop into effect at once (running tasks are stopped before this returns), then
        tell the mesh and the listeners."""
        self._announced_allowed = allowed
        if not allowed:
            log.warning(
                "agent stopped reason=%s: leaving the mesh and stopping running tasks count=%d",
                reason,
                self._runner.active,
            )
            self._runner.stop_all(reason)
        return self._settling.spawn(self._announce_stop(allowed, reason))

    async def _announce_stop(self, allowed: bool, reason: str) -> None:
        """Tell the transport and the listeners that the agent is stopped or may run again.

        A transport that manages the listing (the agent link) leaves the card to SAM: the entrypoint takes it down for a
        stop and puts it back on release. A request-driven agent keeps its card, because it is asleep when SAM releases
        it and could never publish the card again, so nothing would route a request to wake it; SAM lists it as stopped
        and refuses its tasks. A listener's failure is logged and must not keep the agent running.
        """
        listing = not self._transport._manages_listing
        if allowed:
            log.info("agent may run again: rejoining the mesh reason=%s", reason)
            if listing:
                await self._transport._publish_description()
        elif listing and not self.request_driven:
            await self._transport._withdraw_description()
        for listener in list(self._listeners):
            try:
                out = listener(allowed, reason)
                if inspect.isawaitable(out):
                    await out
            except Exception as exc:
                log.error("kill-switch listener failed err=%s", type(exc).__name__)
        if not allowed and self._config.exit_on_stop:
            self._request_exit()
