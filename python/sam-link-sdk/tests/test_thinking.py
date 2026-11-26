# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The opt-in reasoning stream: off unless `stream_thinking` is set. (How the LangChain adapter takes reasoning from
the framework: tests/test_callbacks.py.)"""

from __future__ import annotations

from sam_link_sdk.events import Thinking

from .agent_rig import task_context


async def test_reasoning_is_not_streamed_unless_enabled():
    async with task_context() as task:
        await task.ctx.emit_thinking("secret reasoning")
        await task.ctx.end_thinking()
        assert task.events == []


async def test_reasoning_is_streamed_when_enabled():
    async with task_context(config={"stream_thinking": True}) as task:
        await task.ctx.emit_thinking("first, ")
        await task.ctx.emit_thinking("then")
        await task.ctx.end_thinking()
        assert task.events == [Thinking("first, "), Thinking("then"), Thinking("", done=True)]
