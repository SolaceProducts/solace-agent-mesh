# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""How to reach Agent Mesh over the agent link."""

from __future__ import annotations

import ipaddress
import os
import ssl
import urllib.parse
from dataclasses import dataclass, field
from pathlib import Path

PROTOCOL_V2 = "sam-agent-link.v2"
"""The WebSocket subprotocol the agent offers, and the version Agent Mesh's welcome names for it (docs/link-protocol.md,
"Negotiation"). The agent speaks this protocol only.
"""
PROTOCOL_VERSION = 2
DEFAULT_MAX_FRAME_BYTES = 8 * 1024 * 1024


class LinkError(ConnectionError):
    """A message could not be sent over the agent link, or the link could not be opened."""


class LinkConfigError(LinkError):
    """The link's address cannot be right (a path Agent Mesh does not serve, TLS to a plain HTTP port): raised at once
    by the process's first connection, which retrying would not mend."""


class LinkAuthRefused(LinkError):
    """Agent Mesh refused the agent's token: HTTP 401 at connect, or close code 4401 on an open link."""


class LinkRateLimited(LinkError):
    """Agent Mesh refused the connection (HTTP 429): the agent opened more connections in a minute than it
    allows (``max_connects_per_minute``)."""


class LinkUnavailable(LinkError):
    """Agent Mesh answered HTTP 503: the replica reached does not serve agent links now (another one does, or it has
    just started or is stopping). ``retry_after`` is how long it asked the agent to wait, in seconds, within the SDK's
    bounds."""

    def __init__(self, message: str, retry_after: float):
        super().__init__(message)
        self.retry_after = retry_after

    def __reduce__(self) -> tuple[type[LinkUnavailable], tuple[str, float]]:
        return type(self), (str(self), self.retry_after)


class LinkProtocolMismatch(LinkError):
    """Agent Mesh does not speak the agent link protocol this SDK speaks: it refused the offer, or welcomed the
    agent with another protocol."""


@dataclass(frozen=True, kw_only=True)
class LinkConfig:
    url: str
    agent: str
    token: str = field(repr=False)
    trust_store_path: str = ""
    connect_timeout_seconds: float = 10.0
    heartbeat_seconds: float = 20.0
    """Agent Mesh drops a connection that sends nothing for 60 s; the client's pings keep it alive."""
    heartbeat_timeout_seconds: float = 20.0
    max_frame_bytes: int = DEFAULT_MAX_FRAME_BYTES
    """The largest message accepted either way; Agent Mesh's max_frame_bytes (8 MiB by default)."""
    reconnect_min_seconds: float = 0.5
    reconnect_max_seconds: float = 30.0
    max_connects_per_minute: int = 20
    """At most this many connection attempts in any 60 s, whatever drops them: Agent Mesh refuses an agent that opens more
    than its max_connects_per_minute (60 by default), so the client stays well under it.
    """
    peer_call_timeout_seconds: float = 7200.0
    """The longest a peer call waits for its answer, whatever happens to the link. Agent Mesh's own bound is its
    task_ttl_seconds (7200 by default), which a gateway restart forgets.
    """
    token_file: str = ""
    """A file holding the token, read again on every connection attempt so a rotated token is picked up without a restart.
    When set, it wins over ``token``, which stays the fallback while the file cannot be read.
    """
    max_auth_refusals: int = 10
    """After this many refusals of the token in a row (HTTP 401, or close code 4401) a reconnecting agent stops with
    LinkAuthRefused instead of retrying for ever. A refusal on the first connection always fails at once.
    """
    allow_insecure: bool = False
    """A plain ws:// (http://) address off this machine sends the token unencrypted, so connecting to one is refused unless
    this is set (SAM_LINK_ALLOW_INSECURE=1): a network the deployment trusts, such as a cluster's own.
    """

    def _current_token(self) -> str:
        """The token to present now: the token file's content, else ``token``."""
        return read_token_file(self.token_file) or self.token

    @classmethod
    def from_env(cls, agent: str = "") -> LinkConfig:
        """SAM_LINK_URL, SAM_LINK_TOKEN or SAM_LINK_TOKEN_FILE, the agent's name (else SAM_AGENT_NAME),
        SAM_LINK_TRUST_STORE_PATH and SAM_LINK_ALLOW_INSECURE."""
        url = os.environ.get("SAM_LINK_URL", "")
        name = agent or os.environ.get("SAM_AGENT_NAME", "")
        token_file = os.environ.get("SAM_LINK_TOKEN_FILE", "")
        token = os.environ.get("SAM_LINK_TOKEN", "")
        if token_file and token:
            raise ValueError(
                "SAM_LINK_TOKEN and SAM_LINK_TOKEN_FILE are both set, and only one can say which token the agent "
                "presents: unset one of them (SAM_LINK_TOKEN holds the token itself, SAM_LINK_TOKEN_FILE the path "
                "of a file that holds it)"
            )
        if token_file:
            token = read_token_file(token_file)
        token_name = (
            f"SAM_LINK_TOKEN (SAM_LINK_TOKEN_FILE {token_file} is empty or cannot be read)"
            if token_file
            else "SAM_LINK_TOKEN or SAM_LINK_TOKEN_FILE"
        )
        missing = [k for k, v in (("SAM_LINK_URL", url), ("SAM_AGENT_NAME", name), (token_name, token)) if not v]
        url_missing = not url
        if missing:
            raise ValueError(
                f"the agent link needs {', '.join(missing)}"
                + (
                    "; SAM_LINK_URL has the form wss://<sam-host>/gw/<gateway_id>/v1/connect "
                    "(ws:// for a local SAM without TLS)"
                    if url_missing
                    else ""
                )
            )
        max_frame = os.environ.get("SAM_LINK_MAX_FRAME_BYTES", "")
        return cls(
            url=url,
            agent=name,
            token=token,
            token_file=token_file,
            trust_store_path=os.environ.get("SAM_LINK_TRUST_STORE_PATH", ""),
            max_frame_bytes=int(max_frame) if max_frame else DEFAULT_MAX_FRAME_BYTES,
            allow_insecure=os.environ.get("SAM_LINK_ALLOW_INSECURE", "").strip().lower() in ("1", "true", "yes"),
        )


def read_token_file(path: str) -> str:
    """The token in the file ("" when there is no path, or the file cannot be read or is empty)."""
    if not path:
        return ""
    try:
        return Path(path).read_text(encoding="utf-8").strip()
    except OSError:
        return ""


def websocket_url(url: str) -> str:
    for http, ws in (("https://", "wss://"), ("http://", "ws://")):
        if url.startswith(http):
            return ws + url[len(http) :]
    return url


def sends_token_in_clear(url: str) -> bool:
    """Whether the link's address is plain ``ws://`` to a host other than this machine's loopback."""
    parsed = urllib.parse.urlsplit(websocket_url(url))
    if parsed.scheme != "ws":
        return False
    host = parsed.hostname or ""
    if host == "localhost":
        return False
    try:
        return not ipaddress.ip_address(host).is_loopback
    except ValueError:
        return True


def ssl_context(url: str, trust_store_path: str) -> ssl.SSLContext | None:
    """The TLS context for a `wss://` address, or None for a plain one.

    A trust store that is a directory is read as plain PEM files, each loaded by itself, because OpenSSL's capath needs
    hashed names.
    """
    if not url.startswith("wss://"):
        return None
    context = ssl.create_default_context()
    if not trust_store_path:
        return context
    path = Path(trust_store_path)
    if path.is_dir():
        for cert in sorted(p for p in path.iterdir() if p.is_file() and p.suffix in (".pem", ".crt", ".cer")):
            context.load_verify_locations(cafile=str(cert))
    else:
        context.load_verify_locations(cafile=str(path))
    return context
