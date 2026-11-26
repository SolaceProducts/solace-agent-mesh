# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The agent the load and soak tests drive: one process, built on the SDK's public API only, that answers each task by
the first word of its text and echoes the task's token, so an answer that reaches the wrong task shows.

    "echo <token>"     answers "answer <token>" after a short random wait
    "tool <token>"     reports a model step that calls `lookup`, the tool's step with the token as its result, then
                       answers "answer <token>"
    "stream <token>"   streams "answer " and the token in two chunks, then answers "answer <token>"
    "fail <token>"     raises, so the task fails
    "hold <token>"     waits LOAD_HOLD_SECONDS (cancellable); the test cancels it

The link comes from the environment, as every example's does (SAM_LINK_URL, SAM_LINK_TOKEN, SAM_AGENT_NAME).
LOAD_MAX_CONCURRENT is the agent's max_concurrent_tasks, LOAD_RECONNECT_SECONDS the link's reconnect delay (so a
restarted SAM is rejoined at once), LOAD_LOG_LEVEL the log level
(WARNING by default; INFO logs every task). Every LOAD_STATS_INTERVAL seconds the agent writes
LOAD_STATS_FILE (JSON): `at`, when it wrote it, `in_flight`, the tasks the agent holds (what it reports as sam.agent.active_executions),
`running`, the tokens whose handler is running now, `peak_running`, the most handlers that ran at once, `cancelled`,
how many handlers ended in a cancel, `objects`, the objects the garbage collector tracks, and `blocks`, the
memory blocks the interpreter has allocated (strings and numbers too, which the collector does not track), both
counted after a collection while the agent holds no task, and `runs`, how many times a handler ran per token, for the tokens run more
than once (among the latest RUNS_KEPT).
"""

from __future__ import annotations

import asyncio
import dataclasses
import gc
import json
import logging
import os
import random
import sys
import time
from collections import OrderedDict
from collections.abc import Callable, Mapping
from pathlib import Path

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext, ToolInfo, Usage
from sam_link_sdk.events import ModelFinished, ModelStarted, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.link import LinkConfig, LinkTransport

MODEL = "load-model"
TOOL = "lookup"
RUNS_KEPT = 256


class LoadFailure(Exception):
    pass


class Stats:
    """What the agent tells the test about itself. The in-flight count comes from the agent's own metrics hook."""

    def __init__(self, path: Path):
        self.path = path
        self.in_flight: Callable[[], Mapping[str, int]] = dict
        self.running: set[str] = set()
        self.peak_running = 0
        self.cancelled = 0
        self.runs: OrderedDict[str, int] = OrderedDict()
        self.ran_twice: dict[str, int] = {}
        self.objects = 0
        self.blocks = 0

    def ran(self, token: str) -> None:
        """Counts a handler run for the token. Only the latest RUNS_KEPT tokens are kept, so the agent's own bookkeeping
        does not grow with the tasks it serves, which the test measures; a task run again comes back within its round."""
        runs = self.runs.pop(token, 0) + 1
        self.runs[token] = runs
        if runs > 1:
            self.ran_twice[token] = runs
        while len(self.runs) > RUNS_KEPT:
            self.runs.popitem(last=False)

    def counter(self, name: str, value: float, attributes: Mapping[str, str]) -> None:
        pass

    def histogram(self, name: str, seconds: float, attributes: Mapping[str, str]) -> None:
        pass

    def active_executions(self, counts: Callable[[], Mapping[str, int]]) -> None:
        self.in_flight = counts

    def write(self) -> None:
        in_flight = sum(self.in_flight().values())
        if not in_flight and not self.running:
            gc.collect()
            self.objects = len(gc.get_objects())
            self.blocks = sys.getallocatedblocks()
        snapshot = {
            "at": time.time(),
            "in_flight": in_flight,
            "objects": self.objects,
            "blocks": self.blocks,
            "running": sorted(self.running),
            "peak_running": self.peak_running,
            "cancelled": self.cancelled,
            "runs": self.ran_twice,
        }
        partial = self.path.with_suffix(".partial")
        partial.write_text(json.dumps(snapshot))
        partial.replace(self.path)

    async def keep_writing(self, interval: float) -> None:
        while True:
            try:
                self.write()
            except Exception:
                logging.getLogger("load_agent").exception("stats not written")
            await asyncio.sleep(interval)


def build(stats: Stats) -> MeshAgent:
    hold = float(os.environ.get("LOAD_HOLD_SECONDS") or 60)

    async def handle(ctx: TaskContext) -> str:
        kind, _, token = ctx.text.partition(" ")
        stats.ran(token)
        stats.running.add(token)
        stats.peak_running = max(stats.peak_running, len(stats.running))
        try:
            await asyncio.sleep(random.uniform(0, 0.05))
            if kind == "tool":
                call = ToolCall(f"call-{token}", TOOL, {"token": token})
                await ctx.emit_event(ModelStarted(MODEL))
                await ctx.emit_event(ModelFinished(MODEL, "", (call,), Usage(input_tokens=10, output_tokens=5)))
                await ctx.emit_event(ToolStarted(call.id, TOOL, {"token": token}))
                await asyncio.sleep(random.uniform(0, 0.02))
                await ctx.emit_event(ToolFinished(call.id, TOOL, {"found": token}))
            elif kind == "stream":
                await ctx.emit_text("answer ")
                await asyncio.sleep(random.uniform(0, 0.02))
                await ctx.emit_text(token)
            elif kind == "fail":
                raise LoadFailure(token)
            elif kind == "hold":
                await asyncio.sleep(hold)
            return f"answer {token}"
        except asyncio.CancelledError:
            stats.cancelled += 1
            raise
        finally:
            stats.running.discard(token)

    name = os.environ["SAM_AGENT_NAME"]
    config = AgentConfig(
        name=name,
        display_name=name,
        description="Answers many tasks at once, each with its own token",
        skills=[Skill(id="echo", name="Echo", description="Echoes the task's token")],
        tools=[ToolInfo(name=TOOL, description="Looks a token up")],
        keepalive_seconds=0,
        max_concurrent_tasks=int(os.environ.get("LOAD_MAX_CONCURRENT") or 0),
        metrics=stats,
    )
    reconnect = float(os.environ.get("LOAD_RECONNECT_SECONDS") or 0.5)
    link = dataclasses.replace(
        LinkConfig.from_env(agent=name), reconnect_min_seconds=reconnect, reconnect_max_seconds=reconnect
    )
    return MeshAgent(config, handle, transport=LinkTransport(link))


async def main() -> None:
    logging.basicConfig(
        level=os.environ.get("LOAD_LOG_LEVEL") or "WARNING", format="%(asctime)s %(levelname)s %(name)s %(message)s"
    )
    stats = Stats(Path(os.environ["LOAD_STATS_FILE"]))
    agent = build(stats)
    writer = asyncio.create_task(stats.keep_writing(float(os.environ.get("LOAD_STATS_INTERVAL") or 0.2)))
    try:
        await agent.run_forever()
    finally:
        writer.cancel()


if __name__ == "__main__":
    asyncio.run(main())
