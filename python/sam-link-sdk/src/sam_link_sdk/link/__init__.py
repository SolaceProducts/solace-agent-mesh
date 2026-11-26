# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The agent link: join the mesh through SAM's ``agent_link`` gateway entrypoint (SAM's broker stays behind it).

    from sam_link_sdk.link import LinkConfig, LinkTransport
    agent = MeshAgent(config, handle, transport=LinkTransport(LinkConfig.from_env(agent=config.name)))

The entrypoint is the only thing the agent reaches: one WebSocket (``wss://<gateway>/gw/<gateway_id>/v1/connect``)
authenticated with the agent's own token, speaking the agent link protocol (subprotocol ``sam-agent-link.v2``,
``docs/link-protocol.md``). The entrypoint consumes the agent's queue, builds SAM's messages from what the agent
reports, keeps its card and calls its peers for it, checking each message on the way. It verifies each user's token
and enforces SAM's stop itself; a ``control`` message still stops the agent locally, and the agent keeps that stop
across restarts and fails closed when it loses the link for longer than ``offline_grace_seconds`` (sam_link_sdk.stop).

An entrypoint that does not speak protocol 2 is refused loudly (``LinkProtocolMismatch``).
"""

from .config import (
    DEFAULT_MAX_FRAME_BYTES,
    LinkAuthRefused,
    LinkConfig,
    LinkConfigError,
    LinkError,
    LinkProtocolMismatch,
    LinkRateLimited,
    LinkUnavailable,
)
from .transport import DescriptionTooLong, LinkTransport, TooManyToolScopes

__all__ = [
    "DEFAULT_MAX_FRAME_BYTES",
    "DescriptionTooLong",
    "LinkAuthRefused",
    "LinkConfig",
    "LinkConfigError",
    "LinkError",
    "LinkProtocolMismatch",
    "LinkRateLimited",
    "LinkTransport",
    "LinkUnavailable",
    "TooManyToolScopes",
]
