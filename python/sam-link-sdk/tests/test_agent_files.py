# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Files: what a task brings in (``ctx.files``), what the handler returns or attaches, and the budget an answer's
files are kept within."""

from __future__ import annotations

from sam_link_sdk import File, TaskResult
from sam_link_sdk._model import InputFile

from .agent_rig import start_agent


async def test_files_over_the_inline_budget_are_left_out_with_a_note():
    async def handler(ctx):
        return TaskResult("Done.", files=[File("small.txt", b"x" * 30), File("huge.bin", b"y" * 300)])

    agent, transport = await start_agent(handler, max_inline_file_bytes=100)
    await transport.deliver("T10")
    answer = transport.finished["T10"]
    assert [f.name for f in answer.files] == ["small.txt"]
    assert answer.text.endswith("1 file(s) were too large to return and were not included: huge.bin.")
    await agent.stop()


async def test_files_in_and_out():
    received = {}

    async def handler(ctx):
        received["files"] = ctx.files
        ctx.attach("notes.txt", b"from a tool", "text/plain")
        return TaskResult("Summary attached.", files=[File("summary.md", b"# Claim", mime_type="text/markdown")])

    agent, transport = await start_agent(handler)
    files = [
        InputFile("claim.pdf", b"%PDF", mime_type="application/pdf"),
        InputFile("big.pdf", uri="artifact://ns/u/s/big.pdf?version=0"),
    ]
    await transport.deliver("T9", "Summarize", files=files)

    assert [(f.name, f.mime_type, f.content, f.uri) for f in received["files"]] == [
        ("claim.pdf", "application/pdf", b"%PDF", None),
        ("big.pdf", "", None, "artifact://ns/u/s/big.pdf?version=0"),
    ]
    answer = transport.finished["T9"]
    assert [(f.name, f.content, f.mime_type) for f in answer.files] == [
        ("notes.txt", b"from a tool", "text/plain"),
        ("summary.md", b"# Claim", "text/markdown"),
    ]
    assert answer.text == "Summary attached."
    await agent.stop()
