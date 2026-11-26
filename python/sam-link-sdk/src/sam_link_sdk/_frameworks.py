# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The agent frameworks whose sam_link_sdk adapters this process imported.

An agent whose ``AgentConfig.framework`` is empty is described as built with the framework of the one adapter it
imported, so SAM can show what it is built with without the author naming it. With no adapter, or with adapters for
more than one framework, the framework stays unnamed: the SDK cannot tell which one the agent runs on.
"""

from __future__ import annotations

_adopted: dict[str, None] = {}


def adopt(framework: str) -> None:
    """Record that this process imported the adapter for `framework`, by the name SAM shows for it."""
    _adopted.setdefault(framework, None)


def adopted() -> tuple[str, ...]:
    """The frameworks recorded so far, in the order their adapters were imported."""
    return tuple(_adopted)


def inferred() -> str:
    """The framework of the only adapter this process imported, else an empty string."""
    return next(iter(_adopted)) if len(_adopted) == 1 else ""


def described(framework: str) -> str:
    """The framework an agent is described as built with: the one its author named, else the inferred one."""
    return framework or inferred()
