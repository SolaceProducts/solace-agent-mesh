# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""What SAM tells an agent about the request beyond the user's words, on the handler's TaskContext. (How the agent
link carries it: tests/test_link_protocol.py.)"""

from __future__ import annotations

from sam_link_sdk.request_context import RequestContext

from .agent_rig import start_agent


def test_sections_render_the_way_sam_go_renders_them():
    ctx = RequestContext(
        system_purpose="Help with claims.",
        response_format="Short answers.",
        conversation_context="> earlier message",
        project_id="p1",
        user_profile='{\n  "name": "A. Rivera",\n  "role": "adjuster"\n}',
        user_timezone="America/Toronto",
    )
    assert ctx.context_prompt() == (
        "## System Purpose\n\nHelp with claims.\n\n"
        "## Desired Response Format\n\nShort answers.\n\n"
        '## Inquiring User Profile\n\n{\n  "name": "A. Rivera",\n  "role": "adjuster"\n}\n\n'
        "## Conversation Context\n\n> earlier message"
    )
    assert RequestContext().context_prompt() == ""


async def test_the_handler_sees_the_request_metadata_and_context():
    seen = {}

    async def handler(ctx):
        seen.update(
            metadata=ctx.metadata,
            prompt=ctx.request_context.context_prompt(),
            timezone=ctx.request_context.user_timezone,
        )
        return "ok"

    agent, transport = await start_agent(handler)
    await transport.deliver(
        "T1",
        "hi",
        metadata={"sessionBehavior": "PERSISTENT"},
        request_context=RequestContext(system_purpose="Help with claims.", user_timezone="UTC"),
    )
    assert seen == {
        "metadata": {"sessionBehavior": "PERSISTENT"},
        "prompt": "## System Purpose\n\nHelp with claims.",
        "timezone": "UTC",
    }
    await agent.stop()
