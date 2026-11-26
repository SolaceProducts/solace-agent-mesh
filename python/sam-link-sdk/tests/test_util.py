# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""``within``, the bounded wait the link uses where ``asyncio.wait_for`` would let a cancel go missing."""

from __future__ import annotations

import asyncio

import pytest

from sam_link_sdk._util import within


async def test_a_cancel_that_arrives_with_the_result_still_cancels_the_caller():
    """On Python 3.11 ``asyncio.wait_for`` returns the result when what it waits for completes as the cancel arrives,
    and the caller goes on uncancelled. The test passes on every version with ``within``; with ``wait_for`` it fails
    on 3.11."""
    finished = asyncio.get_running_loop().create_future()
    reached = []

    async def caller():
        result = await within(5, finished)
        reached.append(result)
        return result

    waiting = asyncio.ensure_future(caller())
    await asyncio.sleep(0)
    finished.set_result("done")
    waiting.cancel()
    with pytest.raises(asyncio.CancelledError):
        await waiting
    assert reached == []


async def test_within_ends_a_wait_that_runs_out_with_a_timeout_error():
    with pytest.raises(TimeoutError):
        await within(0.01, asyncio.Event().wait())


async def test_within_waits_without_a_bound_when_given_none():
    assert await within(None, asyncio.sleep(0.01, "waited")) == "waited"
