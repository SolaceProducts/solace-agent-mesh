# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A minimal agent for the robustness cases the claims examples cannot set up: a handler that ignores its cancel, the
link's reconnect timers, the token file and the shutdown grace. It is built on the SDK's public API only and runs as a
process of its own, exactly as `python robust_agent.py`; the link comes from the environment the way every example's
does (SAM_LINK_URL, SAM_LINK_TOKEN or SAM_LINK_TOKEN_FILE, SAM_AGENT_NAME).

What a task does depends on its text:

    "stuck ..."   ignores every cancel and runs until the file ROBUST_RELEASE_FILE exists
    "slow ..."    waits ROBUST_SLOW_SECONDS, and gives way to a cancel
    "blocking ..." blocks a worker thread for ROBUST_SLOW_SECONDS, as a framework's synchronous run does: a cancel
                  ends the wait on it, not the thread
    "swallow ..." waits ROBUST_SLOW_SECONDS, and when it is cancelled catches the cancel and answers "done anyway"
    "none ..."    returns None, as a handler with a forgotten return does
    "modelfail ..." reports a model call that failed with a rate limit (ModelFailed), then raises a plain error
    "usage ..."   reports one model call with its tokens (300 input holding 100 cache reads and 40 cache writes, 20
                  output holding 5 reasoning), as a handler without a framework does, and answers "counted"
    anything else answers "echo: <text>"

    ROBUST_SHUTDOWN_GRACE, ROBUST_TASK_TIMEOUT, ROBUST_MAX_CONCURRENT   the agent's own limits
    ROBUST_LINK_AGENT   the name the agent's own LinkConfig connects as, when it is not the agent's name
    ROBUST_RECONNECT_SECONDS, ROBUST_CONNECT_TIMEOUT, ROBUST_MAX_AUTH_REFUSALS   the link's timers and its refusal count
"""

from __future__ import annotations

import asyncio
import dataclasses
import logging
import os
import time
from pathlib import Path

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext, Usage
from sam_link_sdk.events import ModelFailed, ModelFinished, ModelStarted
from sam_link_sdk.link import LinkConfig, LinkTransport

MODEL = "fake-model"


def _number(name: str, default: float) -> float:
    return float(os.environ[name]) if os.environ.get(name) else default


def build() -> MeshAgent:
    release = Path(os.environ.get("ROBUST_RELEASE_FILE", "release-never"))
    slow = _number("ROBUST_SLOW_SECONDS", 60)

    async def handle(ctx: TaskContext) -> str:
        if ctx.text.startswith("stuck"):
            while not release.exists():
                try:
                    await asyncio.sleep(0.1)
                except asyncio.CancelledError:
                    logging.getLogger("robust_agent").info("handler ignores its cancel task=%s", ctx.task_id)
            return "the stuck handler ended"
        if ctx.text.startswith("blocking"):
            await asyncio.to_thread(time.sleep, slow)
            return "the blocking handler ended"
        if ctx.text.startswith("usage"):
            await ctx.emit_event(ModelStarted(MODEL))
            usage = Usage(
                input_tokens=300, output_tokens=20, cached_input_tokens=100, cache_write_tokens=40, reasoning_tokens=5
            )
            await ctx.emit_event(ModelFinished(MODEL, "counted", usage=usage))
            return "counted"
        if ctx.text.startswith("swallow"):
            try:
                await asyncio.sleep(slow)
            except asyncio.CancelledError:
                return "done anyway"
            return "the swallowing handler ended"
        if ctx.text.startswith("none"):
            return None  # type: ignore[return-value]
        if ctx.text.startswith("modelfail"):
            await ctx.emit_event(ModelStarted(MODEL))
            await ctx.emit_event(ModelFailed(MODEL, category="rate_limit", provider="openai"))
            raise RuntimeError("the provider failed")
        if ctx.text.startswith("slow"):
            await asyncio.sleep(slow)
            return "the slow handler ended"
        return f"echo: {ctx.text}"

    name = os.environ["SAM_AGENT_NAME"]
    config = AgentConfig(
        name=name,
        display_name=name,
        description="Handlers that stall, ignore a cancel or answer at once",
        skills=[Skill(id="echo", name="Echo", description="Echoes the request")],
        keepalive_seconds=0,
        shutdown_grace_seconds=_number("ROBUST_SHUTDOWN_GRACE", 20),
        task_timeout_seconds=_number("ROBUST_TASK_TIMEOUT", 600),
        max_concurrent_tasks=int(_number("ROBUST_MAX_CONCURRENT", 0)),
    )
    reconnect = _number("ROBUST_RECONNECT_SECONDS", 0.5)
    link = dataclasses.replace(
        LinkConfig.from_env(agent=os.environ.get("ROBUST_LINK_AGENT") or name),
        reconnect_min_seconds=reconnect,
        reconnect_max_seconds=reconnect,
        connect_timeout_seconds=_number("ROBUST_CONNECT_TIMEOUT", 10),
        max_auth_refusals=int(_number("ROBUST_MAX_AUTH_REFUSALS", 10)),
    )
    return MeshAgent(config, handle, transport=LinkTransport(link))


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    await build().run_forever()


if __name__ == "__main__":
    asyncio.run(main())
