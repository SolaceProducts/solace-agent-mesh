# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The agent's card on the link: describe the agent to the entrypoint on each connection, again when its description
changes and now and then anyway, and keep what the entrypoint refused.

``DescriptionPublisher`` owns the description's state for ``LinkTransport``: which connection the entrypoint holds a
description on (``generation``), the description it holds, the one it refused and why. It has its own lock, so two
describes never overlap, and paces them on a connection so a refusal always belongs to the latest one sent.
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
from collections.abc import Awaitable, Callable, Mapping
from typing import Any

from .._model import TransportHost
from .._util import BoundedMap
from .config import LinkError
from .connection import LinkConnection
from .v2 import MAX_DESCRIBE_BYTES, Limits, describe_object, overlong_description, tool_scopes

log = logging.getLogger("sam_link_sdk.link")

DESCRIPTION_CHECK_SECONDS = 5.0
DESCRIBE_REFRESH_SECONDS = 300.0
DESCRIBE_SPACING_SECONDS = 1.0
"""The entrypoint sends no verdict for an accepted describe, and answers a refused one at once, in order. So that a refusal
always belongs to the latest describe sent, a describe goes out no sooner than this after the previous one on the same
connection; one asked for sooner waits, and then sends the description as it is by then.
"""
REPUBLISH_MIN_SECONDS = 1.0
"""A describe SAM could not publish (publish_failed) is sent again after this, doubling up to the maximum."""
REPUBLISH_MAX_SECONDS = 30.0
REFUSAL_RELOG_SECONDS = 600.0
"""A description refused again (on a later connection) is logged at ERROR again only this long after the last time, for the
same message from SAM.
"""
MAX_REFUSALS_KEPT = 16


class DescriptionPublisher:
    def __init__(
        self,
        connection: LinkConnection,
        host: Callable[[], TransportHost],
        limits: Callable[[], Limits],
        send: Callable[[Mapping[str, Any]], Awaitable[str]],
        on_refused: Callable[[str], None],
    ) -> None:
        self._connection = connection
        self._host = host
        self._limits = limits
        self._send = send
        self._on_refused = on_refused
        self.present = False
        self.generation = 0
        self.refused_description = ""
        self.refusal = ""
        self.described: tuple[int, str, float] | None = None
        self._refusals_logged: BoundedMap[str, float] = BoundedMap(MAX_REFUSALS_KEPT)
        self._describing = asyncio.Lock()
        self._last_describe: tuple[int, float] = (-1, 0.0)
        self._republish_delay = REPUBLISH_MIN_SECONDS

    def new_connection(self) -> None:
        """A new connection: nothing is described on it yet. One describe per connection even of a description refused
        before: what SAM refuses can change (another entrypoint, a lower count of tool scopes after a restart), and
        connections are paced."""
        self.generation += 1
        self.described = None
        self.refused_description = ""
        self._republish_delay = REPUBLISH_MIN_SECONDS

    def withdraw(self) -> None:
        """The agent leaves: it is no longer described."""
        self.present = False
        self.described = None

    def described_here(self) -> bool:
        """Whether the current connection has the agent's description (and so may take its tasks)."""
        return self.described is not None and self.described[0] == self.generation

    async def describe(self, refresh_after: float | None = None) -> bool:
        """Describe the agent on the current connection unless it already has this description. Returns whether
        the entrypoint has it."""
        async with self._describing:
            while True:
                verdict, agent, encoded = self._description_to_send(refresh_after)
                if verdict is not None:
                    return verdict
                wait = self._spacing_left()
                if wait <= 0:
                    return await self._send_description(agent, encoded)
                await asyncio.sleep(wait)

    def _spacing_left(self) -> float:
        connection, sent_at = self._last_describe
        if connection != self.generation:
            return 0.0
        return sent_at + DESCRIBE_SPACING_SECONDS - time.monotonic()

    def _description_to_send(self, refresh_after: float | None) -> tuple[bool | None, dict, str]:
        """The description and its encoding, with the verdict when none is to be sent (whether the entrypoint has
        it), else None."""
        host = self._host()
        description = host.description()
        agent = describe_object(description)
        encoded = json.dumps(agent, ensure_ascii=False, separators=(",", ":"))
        if len(encoded.encode("utf-8")) > MAX_DESCRIBE_BYTES:
            log.error("card publish failed err=%s", "the description is larger than the entrypoint takes")
            return False, agent, encoded
        if encoded == self.refused_description:
            return False, agent, encoded
        if len(tool_scopes(host.config.tools)) > self._limits().max_tool_scopes:
            log.error("card publish failed err=%s", "the tools require more distinct scopes than the entrypoint checks")
            return False, agent, encoded
        overlong = overlong_description(description, self._limits())
        if overlong:
            log.error("card publish failed err=%s", overlong)
            return False, agent, encoded
        described = self.described
        if (
            described is not None
            and described[0] == self.generation
            and described[1] == encoded
            and (refresh_after is None or time.monotonic() - described[2] < refresh_after)
        ):
            return True, agent, encoded
        return None, agent, encoded

    async def _send_description(self, agent: dict, encoded: str) -> bool:
        """Send a describe and record what was described and when.

        The connection generation is read before sending: a describe that waits out a reconnect counts for the
        connection it was meant for, so the new connection describes the agent again before it consumes.
        """
        generation = self.generation
        if self.described is None or self.described[1] != encoded:
            self._republish_delay = REPUBLISH_MIN_SECONDS
        try:
            await self._send({"type": "describe", "agent": agent})
        except LinkError as exc:
            log.error("card publish failed err=%s", exc)
            return False
        self._last_describe = (generation, time.monotonic())
        self.described = (generation, encoded, time.monotonic())
        self.refusal = ""
        return True

    async def describe_again_shortly(self, delay: float) -> None:
        await asyncio.sleep(delay)
        if self.present and self._connection.connected:
            await self.describe(refresh_after=0.0)

    async def watch(self, after_described: Callable[[], Awaitable[None]]) -> None:
        """Describe the agent again when its description changes (a health problem set or cleared), and now and then
        anyway, so a card the entrypoint lost comes back; ``after_described`` runs once a description got through.

        The watch outlives one bad description (a schema JSON cannot hold): a failure is logged and the next check tries
        again.
        """
        while True:
            await asyncio.sleep(DESCRIPTION_CHECK_SECONDS)
            if not (self.present and self._connection.connected):
                continue
            try:
                if await self.describe(refresh_after=DESCRIBE_REFRESH_SECONDS):
                    await after_described()
            except Exception as exc:
                log.error("card publish failed err=%s", type(exc).__name__)

    def on_refused(self, code: Any, message: str) -> None:
        """The entrypoint answered a describe with an error.

        A `publish_failed` is not a refusal of the description: the entrypoint keeps it (tasks still come) and publishes
        the card when the agent describes itself again, so it is retried with a growing delay. Any other error means SAM
        refused the description, and the agent takes no tasks until one is accepted.
        """
        if code == "publish_failed":
            delay = self._republish_delay
            self._republish_delay = min(delay * 2, REPUBLISH_MAX_SECONDS)
            log.warning(
                "SAM could not publish the agent's card; describing the agent again delay_seconds=%s message=%s",
                delay,
                message,
            )
            self._connection.spawn(self.describe_again_shortly(delay))
            return
        described = self.described
        now = time.monotonic()
        last_logged = self._refusals_logged.get(message)
        level = (
            logging.DEBUG if last_logged is not None and now - last_logged < REFUSAL_RELOG_SECONDS else logging.ERROR
        )
        if level == logging.ERROR:
            self._refusals_logged.put(message, now)
        log.log(
            level,
            "SAM refused the agent's description; the agent takes no tasks until a description is "
            "accepted, and does not send this one again on this connection code=%s message=%s",
            code,
            message,
        )
        if described is not None:
            self.refused_description = described[1]
        self.described = None
        self.refusal = message or str(code or "refused")
        self._on_refused(self.refusal)
