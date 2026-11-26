# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The tasks this process gave back to SAM."""

from __future__ import annotations

import time
from collections.abc import Callable

from .._util import BoundedMap

OUTSIDE_CANCEL_TWICE = "Stopped: this task's run was cancelled from outside the agent twice."
NOT_TAKEN_TWICE = "Stopped: the agent could not take this task twice."
MAX_GIVEN_BACK_KEPT = 256
GIVEN_BACK_KEPT_SECONDS = 3600.0


class GiveBacks:
    """The tasks this process gave back to SAM, by id, the last MAX_GIVEN_BACK_KEPT for GIVEN_BACK_KEPT_SECONDS. A
    cause that recurs (a library that cancels its own task, a watchdog) would otherwise run a task again on every
    redelivery, and a queue may redeliver without limit: a task is given back at most once per process, and answered
    failed the second time."""

    def __init__(self, clock: Callable[[], float] = time.monotonic) -> None:
        self._clock = clock
        self._at: BoundedMap[str, float] = BoundedMap(MAX_GIVEN_BACK_KEPT)

    def first(self, task_id: str) -> bool:
        """Note a give-back of `task_id`; False when this process gave it back already."""
        now = self._clock()
        while self._at and now - self._at.oldest() > GIVEN_BACK_KEPT_SECONDS:
            self._at.drop_oldest()
        if task_id in self._at:
            return False
        self._at.put(task_id, now)
        return True

    def __len__(self) -> int:
        return len(self._at)
