# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The token usage of each task on the link, until a frame carries it to SAM.

Frameworks report a model call's usage from their own threads, so every field here is guarded by one lock. A model call's
usage rides its ``model_finished`` frame, so the entrypoint records spend and the task's totals exactly once: it is
taken (``take``) before that frame is built and settled (``UsageClaim.settle``) by the send's outcome, which puts it
back when the frame did not go out. What no ``model_finished`` carried (a bare ``record_usage``, a step that could not
be sent) goes out in frames of its own when the task is answered (``remaining``).
"""

from __future__ import annotations

import asyncio
import logging
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from typing import Any

from .._model import UsageSink
from ..usage import Usage
from .v2 import MAX_TOKENS_PER_TASK, UNKNOWN_MODEL, Limits, fit_name, usage_model_finished, usage_object

log = logging.getLogger("sam_link_sdk.link")

USAGE_SETTLE_SECONDS = 15.0


@dataclass
class TaskUsage:
    """What the ledger holds for one task: ``usage`` waiting for a frame, ``sending`` frames carrying some, and
    ``collected`` once the task is answered and nothing more is taken."""

    usage: list[tuple[str, Usage]] = field(default_factory=list)
    sending: int = 0
    collected: bool = False
    settled: asyncio.Event = field(default_factory=asyncio.Event)
    models: set[str] = field(default_factory=set)
    models_capped: bool = False
    tokens_sent: int = 0


class UsageClaim:
    """One model call's usage, set aside while the frame that carries it is sent. Settled once: by the send's
    outcome, or by whoever took it when no send was started."""

    def __init__(self, ledger: UsageLedger, task_id: str, state: TaskUsage, usage: tuple[str, Usage]) -> None:
        self._ledger = ledger
        self._task_id = task_id
        self._state = state
        self.usage = usage
        self._settled = False

    def settle(self, sent: bool) -> None:
        if self._settled:
            return
        self._settled = True
        self._ledger._settle(self._task_id, self._state, self.usage, sent)


class UsageLedger:
    def __init__(self, limits: Callable[[], Limits], spend_model_keys: Callable[[], Mapping[str, str]]) -> None:
        self._limits = limits
        self._spend_model_keys = spend_model_keys
        self._lock = threading.Lock()

    def sink(self, task_id: str, state: TaskUsage, is_current: Callable[[], bool]) -> UsageSink:
        """Where the task reports each model call's usage (callable from any thread)."""

        def record(model: str, usage: Usage) -> None:
            """File one model call's usage with this delivery of the task.

            The sink is bound to the delivery: usage reported after it ended never lands on a later delivery of the same
            id.
            """
            with self._lock:
                if is_current() and not state.collected:
                    state.usage.append((model, usage))
                    return
            log.warning(
                "model usage lost: reported after the task's answer task=%s calls=1 input=%d output=%d",
                task_id,
                usage.input_tokens,
                usage.output_tokens,
            )

        return record

    def take(self, task_id: str, state: TaskUsage, model: str) -> UsageClaim | None:
        """The usage a model_finished for ``model`` carries, set aside while the frame is sent: only a recording for
        that model, since SAM prices and files the usage under the frame's model. Another model's usage goes out in a
        frame of its own (``remaining``)."""
        with self._lock:
            if state.collected:
                return None
            index = next((i for i, (m, _) in enumerate(state.usage) if (m or UNKNOWN_MODEL) == model), None)
            if index is None:
                return None
            state.sending += 1
            state.settled.clear()
            return UsageClaim(self, task_id, state, state.usage.pop(index))

    def _settle(self, task_id: str, state: TaskUsage, usage: tuple[str, Usage], sent: bool) -> None:
        with self._lock:
            state.sending -= 1
            lost = not sent and state.collected
            if not sent and not state.collected:
                state.usage.insert(0, usage)
            if state.sending == 0:
                state.settled.set()
        if lost:
            log.warning(
                "model usage lost: not sent before the task's answer task=%s calls=1 input=%d output=%d",
                task_id,
                usage[1].input_tokens,
                usage[1].output_tokens,
            )

    def collect(self, state: TaskUsage) -> list[tuple[str, Usage]]:
        """Close the task to more usage and take what waits for a frame."""
        with self._lock:
            state.collected = True
            pending, state.usage = state.usage, []
        return pending

    async def remaining(self, task_id: str, state: TaskUsage) -> list[dict]:
        """The frames for the usage no model_finished carried, once the model_finished frames on their way have
        settled."""
        if state.sending:
            try:
                await asyncio.wait_for(state.settled.wait(), USAGE_SETTLE_SECONDS)
            except TimeoutError:
                log.warning("a model call's usage was still being sent when the task was answered task=%s", task_id)
        frames = [self.frame(task_id, state, entry) for entry in self.collect(state)]
        return [f for f in frames if "usage" in f["event"]]

    def lost_frames(self, task_id: str, state: TaskUsage) -> list[dict]:
        """The frames for the usage of a task that will not be answered, to report as lost."""
        return [self.frame(task_id, state, entry) for entry in self.collect(state)]

    def reported_model(self, task_id: str, state: TaskUsage, model: str, limits: Limits) -> str:
        """The model a task's event names. The entrypoint takes at most ``maxModelsPerTask`` distinct ones per task:
        one place is kept for UNKNOWN_MODEL, under which any model past the others is reported (its usage priced by
        its configured key, else by its own name as the pricingKey), so a task never has a step refused for naming
        one model too many."""
        name = fit_name(model or UNKNOWN_MODEL, limits.max_name_bytes)
        named = len(state.models - {UNKNOWN_MODEL})
        if name in state.models or name == UNKNOWN_MODEL or named < limits.max_models_per_task - 1:
            state.models.add(name)
            return name
        if not state.models_capped:
            state.models_capped = True
            log.warning(
                "task names more models than the agent link carries; reporting the rest as %s task=%s limit=%d",
                UNKNOWN_MODEL,
                task_id,
                limits.max_models_per_task,
            )
        state.models.add(UNKNOWN_MODEL)
        return UNKNOWN_MODEL

    def fields(
        self, task_id: str, state: TaskUsage, model: str, usage: tuple[str, Usage], limits: Limits, reported: str
    ) -> dict[str, Any]:
        """The usage a model_finished carries, and the key SAM prices it by: the configured one, else, for a call
        reported as UNKNOWN_MODEL past the per-task cap, its real model's name (SAM files a key that matches no
        model configuration unpriced, as it would ``unknown``). Nothing once the task's usage would pass
        MAX_TOKENS_PER_TASK, which the entrypoint would refuse."""
        usage_model, tokens = usage
        body = usage_object(tokens)
        total = body["input"] + body["output"]
        with self._lock:
            within = state.tokens_sent + total <= MAX_TOKENS_PER_TASK
            if within:
                state.tokens_sent += total
        if not within:
            log.warning(
                "model usage lost: the task reports more than %d tokens task=%s input=%d output=%d",
                MAX_TOKENS_PER_TASK,
                task_id,
                body["input"],
                body["output"],
            )
            return {}
        fields: dict[str, Any] = {"usage": body}
        keys = self._spend_model_keys()
        pricing_key = keys.get(usage_model) or keys.get(model)
        real = usage_model or model
        if not pricing_key and reported == UNKNOWN_MODEL and real and real != UNKNOWN_MODEL:
            pricing_key = real
        if pricing_key:
            fields["pricingKey"] = fit_name(pricing_key, limits.max_name_bytes)
        return fields

    def frame(self, task_id: str, state: TaskUsage, usage: tuple[str, Usage]) -> dict:
        """A model_finished frame that carries only ``usage``."""
        limits = self._limits()
        reported = self.reported_model(task_id, state, usage[0], limits)
        body = usage_model_finished(reported, limits)
        body.update(self.fields(task_id, state, usage[0], usage, limits, reported=reported))
        return {"type": "event", "taskId": task_id, "event": body}


def report_lost(task_id: str, frames: list[dict]) -> None:
    """Log the usage the frames carry, which will never reach SAM."""
    used = [
        f["event"]["usage"]
        for f in frames
        if f.get("type") == "event" and isinstance(f.get("event"), dict) and "usage" in f["event"]
    ]
    if used:
        log.warning(
            "model usage lost: not sent to SAM task=%s calls=%d input=%d output=%d",
            task_id,
            len(used),
            sum(u.get("input", 0) for u in used),
            sum(u.get("output", 0) for u in used),
        )
