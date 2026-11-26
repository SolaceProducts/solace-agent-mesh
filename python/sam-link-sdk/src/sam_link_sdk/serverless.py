# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Serving requests outside a long-running process: for request-driven runtimes (AWS AgentCore Runtime, Azure AI
Foundry hosted agents) that start the agent only when something calls its endpoint: SAM's entrypoint rings it when a
task waits for the agent."""

from __future__ import annotations

import asyncio
import logging
import time
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from ._model import Transport
    from .agent import _Lifecycle
    from .stop import _StopController
    from .tasks import _TaskRunner

log = logging.getLogger("sam_link_sdk.agent")

WAKE_IDLE_SECONDS = 3.0
WAKE_POLL_SECONDS = 0.05


class _DeliveryWatch:
    """Counts the deliveries a wake takes and when the last one came."""

    def __init__(self) -> None:
        self.received = 0
        self.arriving = 0
        """Deliveries taken whose task is not registered yet."""
        self.quiet_since = time.monotonic()
        self.failure: BaseException | None = None
        self._failed = asyncio.Event()

    def entered(self) -> None:
        self.received += 1
        self.arriving += 1

    def left(self) -> None:
        self.arriving -= 1
        self.quiet_since = time.monotonic()

    def fail(self, error: BaseException) -> None:
        self.failure = error
        self._failed.set()

    async def quiet_for(self, seconds: float) -> None:
        """Wait for ``seconds`` without a delivery, or less if the wake failed meanwhile."""
        try:
            await asyncio.wait_for(self._failed.wait(), seconds)
        except TimeoutError:
            pass


class _WakeHandler:
    """MeshAgent's request-driven entry points: what ``handle_wake``, ``announce`` and ``retire`` do."""

    def __init__(
        self,
        *,
        transport: Transport,
        stopper: _StopController,
        runner: _TaskRunner,
        lifecycle: _Lifecycle,
    ):
        self._transport = transport
        self._stopper = stopper
        self._runner = runner
        self._lifecycle = lifecycle

    async def serve(self, idle_seconds: float = WAKE_IDLE_SECONDS) -> int:
        """Serve the tasks waiting for the agent, then leave (``MeshAgent.handle_wake``).

        The quiet period starts once the tasks are taken, not when the wake begins, because connecting from a cold
        sandbox can take longer than it.
        """
        watch = _DeliveryWatch()
        await self._open_for_invocation()
        try:
            await self._transport._consume(watch)
            watch.quiet_since = time.monotonic()
            while True:
                if watch.failure is not None and not self._runner.running():
                    raise watch.failure
                if running := self._runner.running():
                    await asyncio.wait(running, timeout=idle_seconds)
                    watch.quiet_since = time.monotonic()
                    continue
                if watch.arriving:
                    await asyncio.sleep(WAKE_POLL_SECONDS)
                    continue
                remaining = watch.quiet_since + idle_seconds - time.monotonic()
                if remaining <= 0:
                    return watch.received
                await watch.quiet_for(remaining)
        finally:
            await self._close_invocation()

    async def announce(self) -> None:
        """Describe the agent once while it sleeps (``MeshAgent.announce``)."""
        await self._open_for_invocation(wait_for_peers=False)
        await self._close_invocation()

    async def retire(self) -> None:
        """Take the agent off the mesh (``MeshAgent.retire``)."""
        await self._transport._connect()
        try:
            await self._transport._withdraw_description()
        finally:
            await self._transport._disconnect()

    async def _open_for_invocation(self, wait_for_peers: bool = True) -> None:
        self._lifecycle.serving_ended = False
        self._stopper.begin_request_driven()
        await self._transport._open(serving=False, wait_for_peers=wait_for_peers)
        if self._stopper.allowed:
            await self._transport._publish_description()

    async def _close_invocation(self) -> None:
        """End a wake: stop what ran on its connection and disconnect.

        A delivery still being checked (its token) when the wake ends is left for SAM to deliver again, because it must
        not run after the disconnect. A link that dropped during the wake armed the offline grace, so that is cancelled:
        the agent sleeps now and no contact is due. Work still running when a wake is cut short (the runtime's time
        limit) is abandoned, because a deliberate disconnect starts no offline grace and nothing else would stop it.
        """
        self._lifecycle.serving_ended = True
        await self._transport._stop_background()
        await self._runner.let_stopped_answer()
        self._stopper.cancel_timers(forget=True)
        self._runner.cancel_keepalive()
        await self._transport._flush()
        self._runner.abandon_running()
        await self._transport._disconnect()
