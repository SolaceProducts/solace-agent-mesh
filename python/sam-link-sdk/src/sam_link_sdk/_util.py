# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Helpers the core and the agent link share: spawned work that is tracked and whose failure is logged, a dict that
keeps only the newest entries, and a bounded wait that a cancel always ends."""

from __future__ import annotations

import asyncio
import logging
from collections import OrderedDict
from collections.abc import Awaitable, Callable, Iterator
from typing import Any, Generic, TypeVar

K = TypeVar("K")
V = TypeVar("V")
T = TypeVar("T")


async def within(seconds: float | None, awaited: Awaitable[T]) -> T:
    """Await ``awaited`` for at most ``seconds`` (``TimeoutError`` past them; no bound when None), and let a cancel of
    the caller through whatever else happens. ``asyncio.wait_for`` does not on Python 3.11: when what it waits for
    completes as the cancel arrives, it returns the result and the caller goes on uncancelled."""
    async with asyncio.timeout(seconds):
        return await awaited


class TrackedTasks:
    """Work spawned in the background that a shutdown can wait for or cancel. A task that fails is logged with the error
    type (``failed``), never left as "Task exception was never retrieved"."""

    def __init__(self, log: logging.Logger, failed: str) -> None:
        self._log = log
        self._failed = failed
        self._tasks: set[asyncio.Task[Any]] = set()

    def spawn(self, work: Awaitable[Any]) -> asyncio.Task[Any]:
        task = asyncio.ensure_future(work)
        self._tasks.add(task)
        task.add_done_callback(self._ended)
        return task

    def _ended(self, task: asyncio.Task[Any]) -> None:
        self._tasks.discard(task)
        if not task.cancelled() and task.exception() is not None:
            self._log.error("%s err=%s", self._failed, type(task.exception()).__name__)

    def running_here(self) -> bool:
        """Whether the caller is itself spawned work: a listener that stops the agent runs inside an announcement."""
        return asyncio.current_task() in self._tasks

    def others(self) -> list[asyncio.Task[Any]]:
        """What is running besides the caller, which never waits for or cancels itself."""
        return [t for t in self._tasks if t is not asyncio.current_task()]

    async def wait(self, timeout: float) -> int:
        """Wait up to ``timeout`` for the others to end; returns how many are still running."""
        pending = self.others()
        if not pending:
            return 0
        _, unfinished = await asyncio.wait(pending, timeout=timeout)
        return len(unfinished)

    async def cancel(self) -> None:
        pending = self.others()
        for task in pending:
            task.cancel()
        await asyncio.gather(*pending, return_exceptions=True)


class BoundedMap(Generic[K, V]):
    """A dict that drops its oldest entries past ``max_items``, and past ``max_bytes`` when each entry is given a size
    (the newest is always kept). ``on_evict(key, value)`` is called for each entry dropped that way."""

    def __init__(
        self,
        max_items: int,
        max_bytes: int | None = None,
        on_evict: Callable[[K, V], None] | None = None,
    ) -> None:
        self.max_items = max_items
        self.max_bytes = max_bytes
        self._on_evict = on_evict
        self._items: OrderedDict[K, V] = OrderedDict()
        self._sizes: dict[K, int] = {}

    def put(self, key: K, value: V, size: int = 0) -> None:
        self._items[key] = value
        self._items.move_to_end(key)
        self._sizes[key] = size
        while len(self._items) > 1 and (
            len(self._items) > self.max_items or (self.max_bytes is not None and self.total_bytes > self.max_bytes)
        ):
            dropped, value = self._items.popitem(last=False)
            self._sizes.pop(dropped, None)
            if self._on_evict is not None:
                self._on_evict(dropped, value)

    def replace(self, key: K, value: V) -> None:
        """Change the value of an entry that is held, keeping its age and size."""
        if key in self._items:
            self._items[key] = value

    def get(self, key: K, default: V | None = None) -> V | None:
        return self._items.get(key, default)

    def pop(self, key: K, default: V | None = None) -> V | None:
        self._sizes.pop(key, None)
        return self._items.pop(key, default)

    def drop_oldest(self) -> None:
        key, _ = self._items.popitem(last=False)
        self._sizes.pop(key, None)

    def oldest(self) -> V:
        return next(iter(self._items.values()))

    def clear(self) -> None:
        self._items.clear()
        self._sizes.clear()

    @property
    def total_bytes(self) -> int:
        return sum(self._sizes.values())

    def sizes(self) -> dict[K, int]:
        return dict(self._sizes)

    def __getitem__(self, key: K) -> V:
        return self._items[key]

    def __contains__(self, key: object) -> bool:
        return key in self._items

    def __iter__(self) -> Iterator[K]:
        return iter(self._items)

    def __len__(self) -> int:
        return len(self._items)

    def values(self) -> Iterator[V]:
        return iter(self._items.values())

    def items(self) -> list[tuple[K, V]]:
        return list(self._items.items())
