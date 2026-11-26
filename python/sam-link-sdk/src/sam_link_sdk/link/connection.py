# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The agent link's WebSocket: connecting with the agent's token, the welcome, SAM's stop (``control``), reconnecting
with backoff, and handing every other message to the protocol codec (``LinkTransport``).

The agent speaks agent link protocol 2 only (``docs/link-protocol.md``). An Agent Mesh that refuses that offer, or
welcomes the agent with anything else, is a protocol mismatch: logged at ERROR every time, and the connection is
closed before any of its messages reaches the codec.
"""

from __future__ import annotations

import asyncio
import contextlib
import functools
import inspect
import json
import logging
import math
import random
import ssl
import time
import urllib.parse
from collections import deque
from collections.abc import Awaitable, Callable, Mapping
from typing import Any

from .._util import TrackedTasks, within
from .config import (
    PROTOCOL_V2,
    PROTOCOL_VERSION,
    LinkAuthRefused,
    LinkConfig,
    LinkConfigError,
    LinkError,
    LinkProtocolMismatch,
    LinkRateLimited,
    LinkUnavailable,
    sends_token_in_clear,
    ssl_context,
    websocket_url,
)
from .v2 import json_text

log = logging.getLogger("sam_link_sdk.link")

HEADER_AGENT = "Sam-Agent"
CLOSE_PROTOCOL_ERROR = 4400
CLOSE_CREDENTIALS_REVOKED = 4401
CLOSE_MESSAGE_TOO_BIG = 1009
CLOSE_SERVICE_RESTART = 1012
HTTP_TOO_MANY_REQUESTS = 429
HTTP_UNAUTHORIZED = 401
AUTH_STABLE_SECONDS = 60.0
"""A connection that stayed up this long was not refused for its token: the count of refusals starts again."""
RATE_LIMITED_MIN_SECONDS = 15.0
"""Back-off after Agent Mesh refused a connection for the agent's connection rate: at least this long, doubling up to
RATE_LIMITED_MAX_SECONDS.
"""
RATE_LIMITED_MAX_SECONDS = 60.0
RATE_WINDOW_SECONDS = 60.0
UNREACHABLE_WARNING_SECONDS = 60.0
"""How often a link that cannot reach Agent Mesh says so again."""
HTTP_NOT_FOUND = 404
HTTP_SERVICE_UNAVAILABLE = 503
UNAVAILABLE_RETRY_SECONDS = 1.0
"""How long the agent waits after HTTP 503 before it tries again, when the answer names no Retry-After in seconds.
Only one Agent Mesh replica serves agent links at a time, and the others answer 503, so the next attempt may well reach
the one that does."""
UNAVAILABLE_RETRY_MIN_SECONDS = 0.1
UNAVAILABLE_RETRY_MAX_SECONDS = 5.0
"""The bounds a Retry-After is held to, so no answer makes the agent spin or go quiet."""
UNAVAILABLE_FAST_RETRIES = 30
"""How many 503s in one reconnect are tried again this soon. Past them the agent backs off as for any other failure."""
UNAVAILABLE_CONNECT_SECONDS = 20.0
UNAVAILABLE_CONNECT_RETRIES = 15
"""A connection opened without a grace (a wake, ``announce()``, ``retire()``, a start with no offline grace) is tried
again only past a 503, for at most this long and this many times. Agent Mesh asks for 1 s, which the jitter makes 1
to 1.5 s, so the seconds allow 13 retries and the count 15: 14 to 16 attempts when each refusal comes back promptly.
That is every replica of a round-robin load balancer of about a dozen, a random one's leader with better than 99%
odds over three replicas and 95% over five, and the five seconds a gateway that has just started takes to lead. A
larger Retry-After runs into the seconds first and a tiny one into the count, so an outage costs a serverless
invocation about 20 s."""
SECRET_HEADERS = frozenset({"authorization", "proxy-authorization", "cookie", "set-cookie"})
REDACTED = "[redacted]"

ControlListener = Callable[[bool, str], "Awaitable[None] | None"]
ConnectionListener = Callable[[bool], "Awaitable[None] | None"]


class RedactingLogger(logging.LoggerAdapter):
    """The websockets client's logger without the agent's credentials: at DEBUG the client logs every handshake
    header, ``Authorization: Bearer <link token>`` among them."""

    def __init__(self, logger: logging.Logger, secret: str):
        super().__init__(logger, {})
        self.secret = secret

    def log(self, level: int, msg: Any, *args: Any, **kwargs: Any) -> None:
        if self.isEnabledFor(level):
            self.logger.log(level, msg, *self._redacted(args), **kwargs)

    def _redacted(self, args: tuple) -> tuple:
        if len(args) == 2 and isinstance(args[0], str) and args[0].lower() in SECRET_HEADERS:
            args = (args[0], REDACTED)
        return tuple(self._scrubbed(a) for a in args)

    def _scrubbed(self, value: Any) -> Any:
        if not self.secret:
            return value
        text = value if isinstance(value, str) else str(value)
        return text.replace(self.secret, REDACTED) if self.secret in text else value


def welcome_problem(welcome: Any, subprotocol: str) -> str:
    """Why a welcome is not one of the protocol this SDK speaks ("" when it is)."""
    if not isinstance(welcome, dict) or welcome.get("type") != "welcome":
        return "Agent Mesh's first message is not a welcome"
    if welcome.get("protocol") != PROTOCOL_VERSION:
        return f"Agent Mesh welcomed the agent with protocol {welcome.get('protocol')!r}, not {PROTOCOL_VERSION}"
    if subprotocol != PROTOCOL_V2:
        return f"Agent Mesh selected subprotocol {subprotocol or 'none'!r}, not {PROTOCOL_V2}"
    if not isinstance(welcome.get("enabled"), bool):
        return "the welcome does not say whether SAM has stopped the agent (enabled)"
    return ""


class StaleConnection(LinkError):
    """A message meant for one connection was not sent on another (an acknowledgement after a reconnect)."""


class RedirectRefused(LinkError):
    """Agent Mesh's address answered with a redirect to another scheme, host or port."""


def _retry_after(value: str | None) -> float:
    """A 503's Retry-After in seconds, held to the SDK's bounds; the default when it is absent or not a number of
    seconds (HTTP also allows a date there)."""
    try:
        seconds = float(value) if value is not None else UNAVAILABLE_RETRY_SECONDS
    except ValueError:
        seconds = UNAVAILABLE_RETRY_SECONDS
    if not math.isfinite(seconds):
        seconds = UNAVAILABLE_RETRY_SECONDS
    return min(max(seconds, UNAVAILABLE_RETRY_MIN_SECONDS), UNAVAILABLE_RETRY_MAX_SECONDS)


def _wait_before_trying_another_replica(refused: LinkUnavailable) -> float:
    """What Agent Mesh asked for, jittered up by at most half so agents refused together do not return together."""
    return refused.retry_after * (1 + random.random() / 2)


@functools.cache
def _connect_without_leaving_the_origin() -> Any:
    """websockets' ``connect``, refusing a redirect to another scheme, host or port. websockets follows such a
    redirect and sends the same headers there, so the agent's token would reach whatever the redirect names."""
    from websockets.asyncio.client import connect
    from websockets.uri import parse_uri

    class SameOriginConnect(connect):
        def process_redirect(self, exc: Exception) -> Exception | str:
            target = super().process_redirect(exc)
            if not isinstance(target, str):
                return target
            old, new = parse_uri(self.uri), parse_uri(target)
            if (old.secure, old.host, old.port) != (new.secure, new.host, new.port):
                return RedirectRefused(
                    f"Agent Mesh's address redirected the agent link to {'wss' if new.secure else 'ws'}://"
                    f"{new.host}:{new.port}; the agent's token is sent only to the address it was given"
                )
            return target

    return SameOriginConnect


class LinkConnection:
    def __init__(self, config: LinkConfig):
        self.config = config
        self._url = websocket_url(config.url)
        self._ws: Any = None
        self._ws_logger = RedactingLogger(logging.getLogger("websockets.client"), config.token)
        self._connected = asyncio.Event()
        self._closing = False
        self._runner: asyncio.Task | None = None
        self._callbacks = TrackedTasks(log, "agent link callback failed")
        self._frame_handler: Callable[[dict], None] | None = None
        self._open_handlers: list[Callable[[], Awaitable[None]]] = []
        self._control_listeners: list[ControlListener] = []
        self._connection_listeners: list[ConnectionListener] = []
        self._warned_inbound_limit = False
        self._warned_clear_text = False
        self._attempts: deque[float] = deque()
        self._auth_refusals = 0
        self._opened_at = 0.0
        self._ever_connected = False
        self._unreachable_warned_at: float | None = None
        self._failure_listeners: list[Callable[[LinkError], Awaitable[None] | None]] = []
        self.failure: LinkError | None = None
        self.enabled = True
        self.welcome: dict = {}
        self.subprotocol = ""
        self.protocol = 0

    def on_frame(self, handler: Callable[[dict], None]) -> None:
        """The protocol codec: every message from Agent Mesh but welcome and control."""
        self._frame_handler = handler

    def on_open(self, handler: Callable[[], Awaitable[None]]) -> None:
        """Called after each (re)connect, once the welcome is in, to bring Agent Mesh up to date."""
        self._open_handlers.append(handler)

    def on_control(self, listener: ControlListener) -> None:
        """Call listener(enabled, reason) with SAM's state: from each welcome (reason ""), and when SAM stops (False)
        or releases (True) the agent (Agent Mesh's reason). Called synchronously, in the order the frames came;
        a welcome's state is reported before its contact (on_connection(True))."""
        self._control_listeners.append(listener)

    def on_connection(self, listener: ConnectionListener) -> None:
        """Call listener(False) when the connection drops and listener(True) when it is (re)established. A
        deliberate disconnect() is not reported."""
        self._connection_listeners.append(listener)

    def _on_failure(self, listener: Callable[[LinkError], Awaitable[None] | None]) -> None:
        """Call listener(error) when the link gives up for good: Agent Mesh refused the agent's token
        ``max_auth_refusals`` times in a row. The link is closed by then; ``failure`` holds the error."""
        self._failure_listeners.append(listener)

    @property
    def is_open(self) -> bool:
        return self._runner is not None and not self._closing

    @property
    def connected(self) -> bool:
        return self._connected.is_set()

    @property
    def connection_id(self) -> str:
        return str(self.welcome.get("connection") or "")

    async def connect(self, retry_seconds: float = 0.0) -> None:
        """Open the link. With ``retry_seconds``, an Agent Mesh that cannot be reached is tried again with the
        reconnect's backoff for that long before the error is raised; a refused token, another protocol, and (while
        this link has never connected) an address that cannot be right (HTTP 404, TLS to a plain HTTP port) fail
        at once. Without ``retry_seconds`` only an HTTP 503 is tried again, briefly: a replica that does not serve
        agent links answered, and another one does. A ``disconnect()`` meanwhile ends it with ``LinkError``, and a
        connection that opened all the same is closed."""
        if self.is_open:
            return
        if sends_token_in_clear(self._url) and not self.config.allow_insecure:
            raise LinkError(
                f"the agent link address is not encrypted ({self._url.split(':', 1)[0]}:// to "
                f"{urllib.parse.urlsplit(self._url).hostname}): the agent's token would cross the network in clear "
                "text. Give SAM_LINK_URL as wss:// (https://), or set SAM_LINK_ALLOW_INSECURE=1 "
                "(LinkConfig.allow_insecure) where the network itself is trusted"
            )
        self._closing = False
        self.failure = None
        self._auth_refusals = 0
        if retry_seconds > 0:
            await self._reconnect(give_up_at=time.monotonic() + retry_seconds)
        else:
            await self._open_on_the_serving_replica()
        if self._closing:
            await self.disconnect()
            raise LinkError("the agent link was closed before it opened")
        self._runner = asyncio.create_task(self._run(), name="sam-link-sdk-link")

    async def _open_on_the_serving_replica(self) -> None:
        """Open the link, trying again only past replicas that answer HTTP 503, each after the wait it asked for,
        within ``UNAVAILABLE_CONNECT_SECONDS`` and ``UNAVAILABLE_CONNECT_RETRIES``. Any other failure is raised at
        once, and so is the last 503 when a bound is reached. A ``disconnect()`` during a wait ends it."""
        give_up_at = time.monotonic() + UNAVAILABLE_CONNECT_SECONDS
        retries = 0
        while True:
            try:
                await self._open()
                return
            except LinkUnavailable as refused:
                wait = _wait_before_trying_another_replica(refused)
                if self._closing or retries >= UNAVAILABLE_CONNECT_RETRIES or time.monotonic() + wait > give_up_at:
                    raise
                if retries == 0:
                    log.info(
                        "the Agent Mesh replica reached does not serve agent links (HTTP 503); trying the others "
                        "for up to %d s",
                        round(UNAVAILABLE_CONNECT_SECONDS),
                    )
                retries += 1
                await asyncio.sleep(wait)
                if self._closing:
                    raise LinkError("the agent link was closed before it opened") from refused

    async def disconnect(self) -> None:
        """Close the connection for good and stop everything started on it.

        The callbacks are cancelled too, because work a message started (a describe after a refused consume) would
        otherwise outlive the agent that stopped.
        """
        self._closing = True
        if self._runner is not None:
            self._runner.cancel()
            await asyncio.gather(self._runner, return_exceptions=True)
            self._runner = None
        await self._callbacks.cancel()
        ws, self._ws = self._ws, None
        self._connected.clear()
        if ws is not None:
            await _close_quietly(ws)
        log.info("agent link closed")

    async def _open(self) -> None:
        """Open one WebSocket, read the welcome and catch the agent up with it.

        Contact is recorded as soon as the welcome is read: SAM's state first, then the contact itself, both before
        anything is awaited, so a drop of this connection can only be reported after them (and re-arms the offline
        grace). A wait cancelled by a disconnect during a reconnect closes the socket here, because it is not stored yet
        and nothing else would close it, so its keepalive pings would run on. One failed catch-up must not leave the
        connection half open, so it closes the socket too.
        """
        from websockets.exceptions import ConnectionClosed, InvalidStatus, WebSocketException
        from websockets.typing import Subprotocol

        cfg = self.config
        token = cfg._current_token()
        self._ws_logger.secret = token
        if not self._warned_clear_text and sends_token_in_clear(self._url):
            self._warned_clear_text = True
            log.warning(
                "the agent link is not encrypted: the agent's token goes to %s in clear text, as "
                "SAM_LINK_ALLOW_INSECURE allows; give SAM_LINK_URL as wss:// (https://) to encrypt it",
                urllib.parse.urlsplit(self._url).hostname,
            )
        self._attempts.append(time.monotonic())
        connect = _connect_without_leaving_the_origin()
        try:
            ws = await connect(
                self._url,
                additional_headers={"Authorization": f"Bearer {token}", HEADER_AGENT: cfg.agent},
                subprotocols=[Subprotocol(PROTOCOL_V2)],
                ssl=ssl_context(self._url, cfg.trust_store_path),
                open_timeout=cfg.connect_timeout_seconds,
                ping_interval=cfg.heartbeat_seconds,
                ping_timeout=cfg.heartbeat_timeout_seconds,
                max_size=cfg.max_frame_bytes,
                logger=self._ws_logger,
            )
        except InvalidStatus as exc:
            if exc.response.status_code == HTTP_TOO_MANY_REQUESTS:
                raise LinkRateLimited(
                    "Agent Mesh refused the agent link: HTTP 429, too many connections in a minute"
                ) from exc
            if exc.response.status_code == HTTP_NOT_FOUND and not self._ever_connected:
                raise LinkConfigError(
                    f"Agent Mesh answered HTTP 404 at {self._where()}: nothing is served at that path. Check that "
                    "SAM_LINK_URL ends in /gw/<gateway_id>/v1/connect with the gateway_id of the agent_link entry in "
                    "SAM's configuration, and that SAM has that entry"
                ) from exc
            if exc.response.status_code == HTTP_UNAUTHORIZED:
                self._auth_refusals += 1
                log.error(
                    "Agent Mesh refused the agent's token: HTTP 401 refusals_in_a_row=%d limit=%d",
                    self._auth_refusals,
                    cfg.max_auth_refusals,
                )
                raise LinkAuthRefused(
                    "Agent Mesh refused the agent's token: HTTP 401. Check that: the agent's name "
                    f"({cfg.agent!r}) is the link agent's instanceName (a link agent registered on the platform) "
                    "or matches an entry in SAM's agent_link configuration; SAM_LINK_TOKEN holds the token itself, "
                    "not a path (SAM_LINK_TOKEN_FILE takes a path); the digest SAM holds is of this token; the link "
                    "agent is deployed, or SAM was restarted after the agent was added to its configuration"
                ) from exc
            if exc.response.status_code == HTTP_SERVICE_UNAVAILABLE:
                self._attempts.pop()
                asked = exc.response.headers.get_all("Retry-After")
                raise LinkUnavailable(
                    "Agent Mesh refused the agent link: HTTP 503", _retry_after(asked[0] if asked else None)
                ) from exc
            raise LinkError(f"Agent Mesh refused the agent link: HTTP {exc.response.status_code}") from exc
        except RedirectRefused:
            raise
        except ssl.SSLError as exc:
            if self._ever_connected:
                raise LinkError(f"cannot open the agent link: {type(exc).__name__}") from exc
            raise LinkConfigError(
                f"cannot open a TLS connection to {self._where()}: {exc.reason or exc}. If SAM is served over plain "
                "HTTP (a local SAM without TLS), use ws:// in SAM_LINK_URL; otherwise its certificate is not "
                "trusted: set SAM_LINK_TRUST_STORE_PATH to the CA that signed it"
            ) from exc
        except (TimeoutError, OSError, WebSocketException) as exc:
            raise LinkError(f"cannot open the agent link: {type(exc).__name__}") from exc
        try:
            raw = await asyncio.wait_for(ws.recv(), cfg.connect_timeout_seconds)
        except ConnectionClosed as exc:
            await _close_quietly(ws)
            if exc.rcvd is not None and exc.rcvd.code == CLOSE_PROTOCOL_ERROR:
                raise _mismatch(f"Agent Mesh refused {PROTOCOL_V2} ({exc.rcvd.reason or 'no reason given'})") from exc
            if exc.rcvd is not None and exc.rcvd.code == CLOSE_SERVICE_RESTART:
                raise LinkUnavailable(
                    "Agent Mesh closed the agent link before its welcome: close 1012, the replica reached stopped "
                    "serving agent links as the agent joined",
                    _retry_after(None),
                ) from exc
            raise LinkError(f"no welcome on the agent link: {type(exc).__name__}") from exc
        except (TimeoutError, WebSocketException) as exc:
            await _close_quietly(ws)
            raise LinkError(f"no welcome on the agent link: {type(exc).__name__}") from exc
        except BaseException:
            await _close_quietly(ws)
            raise
        try:
            welcome = json.loads(raw)
        except ValueError:
            welcome = None
        problem = welcome_problem(welcome, ws.subprotocol or "")
        if problem:
            with contextlib.suppress(Exception):
                await ws.close(code=CLOSE_PROTOCOL_ERROR, reason=f"{PROTOCOL_V2} required")
            raise _mismatch(problem)
        self.welcome, self.subprotocol, self.protocol = welcome, ws.subprotocol or "", PROTOCOL_VERSION
        self.enabled = welcome["enabled"]
        self._ws = ws
        self._opened_at = time.monotonic()
        self._connected.set()
        self._ever_connected = True
        self._unreachable_warned_at = None
        log.info("joined Agent Mesh as %s", cfg.agent)
        log.info(
            "agent link open agent=%s connection=%s protocol=%s max_inflight=%s",
            welcome.get("agent"),
            welcome.get("connection"),
            PROTOCOL_VERSION,
            welcome.get("maxInflight"),
        )
        self._check_inbound_limit(welcome)
        self._notify(self._control_listeners, self.enabled, "")
        self._notify(self._connection_listeners, True)
        for handler in list(self._open_handlers):
            try:
                await handler()
            except Exception as exc:
                log.error("agent link open handler failed err=%s", type(exc).__name__)

    def _check_inbound_limit(self, welcome: Mapping[str, Any]) -> None:
        """The inbound limit is fixed when the WebSocket opens, before the welcome says what Agent Mesh may send.
        It is kept, not raised to Agent Mesh's: the configured limit is what the agent was sized for. A task
        larger than it closes the connection (1009) and comes back again, so the mismatch is reported up front."""
        limits = welcome.get("limits")
        announced = limits.get("maxFrameBytes") if isinstance(limits, Mapping) else None
        if (
            isinstance(announced, int)
            and not isinstance(announced, bool)
            and announced > self.config.max_frame_bytes
            and not self._warned_inbound_limit
        ):
            self._warned_inbound_limit = True
            log.warning(
                "Agent Mesh may send larger messages than the agent accepts; set SAM_LINK_MAX_FRAME_BYTES "
                "to Agent Mesh's max_frame_bytes entrypoint_bytes=%d agent_bytes=%d",
                announced,
                self.config.max_frame_bytes,
            )

    async def _run(self) -> None:
        """Read Agent Mesh's messages; when the connection drops, or reading fails for any reason, reconnect with
        backoff until disconnect()."""
        while not self._closing:
            try:
                code = await self._read(self._ws)
                if self._closing:
                    return
                self._dropped(code)
                await self._reconnect()
            except asyncio.CancelledError:
                raise
            except Exception as exc:
                log.error("agent link reader failed; reconnecting err=%s", type(exc).__name__)
                if self._ws is not None:
                    await _close_quietly(self._ws)
                if self._connected.is_set():
                    self._dropped(None)
                await self._reconnect()

    async def _read(self, ws: Any) -> int | None:
        from websockets.exceptions import ConnectionClosed

        try:
            async for raw in ws:
                self._on_raw(raw)
            return ws.close_code
        except ConnectionClosed as exc:
            if exc.sent is not None and exc.sent.code == CLOSE_MESSAGE_TOO_BIG:
                log.error(
                    "a message from Agent Mesh was larger than the agent accepts; set "
                    "SAM_LINK_MAX_FRAME_BYTES to Agent Mesh's max_frame_bytes agent_bytes=%d",
                    self.config.max_frame_bytes,
                )
            return exc.rcvd.code if exc.rcvd is not None else None

    def _dropped(self, code: int | None) -> None:
        self._connected.clear()
        self._notify(self._connection_listeners, False)
        if code == CLOSE_CREDENTIALS_REVOKED:
            if time.monotonic() - self._opened_at >= AUTH_STABLE_SECONDS:
                self._auth_refusals = 0
            self._auth_refusals += 1
            log.error(
                "Agent Mesh revoked the agent's credentials; reconnecting with backoff refusals_in_a_row=%d limit=%d",
                self._auth_refusals,
                self.config.max_auth_refusals,
            )
        else:
            self._auth_refusals = 0
            log.warning("agent link dropped code=%s; reconnecting", code)

    async def _reconnect(self, give_up_at: float | None = None) -> None:
        """Reconnect with exponential back-off and jitter, never more often than ``max_connects_per_minute``
        (a connection that drops right after it opens does not reset the pace), and backing off for longer when the
        Agent Mesh says the agent connects too often. An HTTP 503 is the exception: the replica reached does not
        serve agent links, so the agent tries again after the answer's Retry-After (about a second), without growing
        the back-off and without counting the attempt against the pace, up to ``UNAVAILABLE_FAST_RETRIES`` times.
        After ``max_auth_refusals`` refusals of the token in a row the link gives up (``failure``). With
        ``give_up_at`` (the first connection), the first attempt is at once, a refused token or another protocol
        raises, and so does the last error once that time is reached."""
        first = give_up_at is not None
        delay = self.config.reconnect_min_seconds
        rate_limited_delay = 0.0
        fast_retries = 0
        retry_in: float | None = None
        last: LinkError | None = None
        while not self._closing:
            if not first and self._auth_refusals >= self.config.max_auth_refusals:
                self._fail(
                    LinkAuthRefused(
                        f"Agent Mesh refused the agent's token {self._auth_refusals} times in a row; "
                        "the agent link is closed"
                    )
                )
                return
            if retry_in is not None:
                wait, retry_in = retry_in, None
            else:
                wait = max(0.0 if first and last is None else delay * (0.5 + random.random() / 2), self._rate_wait())
                if rate_limited_delay:
                    wait = max(wait, rate_limited_delay * (0.5 + random.random() / 2))
            if give_up_at is not None and last is not None and time.monotonic() + wait > give_up_at:
                raise last
            await asyncio.sleep(wait)
            try:
                await self._open()
                return
            except LinkUnavailable as exc:
                last = exc
                self._warn_unreachable(exc, give_up_at)
                if fast_retries < UNAVAILABLE_FAST_RETRIES:
                    fast_retries += 1
                    retry_in = _wait_before_trying_another_replica(exc)
                    continue
                log.warning("agent link %s failed err=%s", "connect" if first else "reconnect", exc)
            except LinkRateLimited as exc:
                last = exc
                if not rate_limited_delay:
                    log.warning("agent link reconnect refused: too many connections; backing off err=%s", exc)
                rate_limited_delay = min(
                    max(rate_limited_delay * 2, RATE_LIMITED_MIN_SECONDS), RATE_LIMITED_MAX_SECONDS
                )
            except (LinkAuthRefused, LinkProtocolMismatch, LinkConfigError) as exc:
                if first:
                    raise
                last = exc
            except LinkError as exc:
                last = exc
                log.warning("agent link %s failed err=%s", "connect" if first else "reconnect", exc)
                self._warn_unreachable(exc, give_up_at)
            except Exception as exc:
                if first:
                    raise
                log.error("agent link reconnect failed err=%s", type(exc).__name__)
            delay = min(delay * 2, self.config.reconnect_max_seconds)

    def _where(self) -> str:
        """The link's address as the agent was given it (no credentials are ever in it)."""
        return self._url

    def _warn_unreachable(self, error: Exception, give_up_at: float | None, now: float | None = None) -> None:
        """At most once a minute while the link is down: where the agent keeps trying, why it fails and, on the first
        connection, how long until it gives up."""
        now = time.monotonic() if now is None else now
        if self._unreachable_warned_at is not None and now - self._unreachable_warned_at < UNREACHABLE_WARNING_SECONDS:
            return
        self._unreachable_warned_at = now
        host = urllib.parse.urlsplit(self._url).netloc.rpartition("@")[2]
        if give_up_at is None:
            log.warning("still trying to reach %s (%s)", host, error)
        else:
            log.warning(
                "still trying to reach %s (%s), giving up in %d s", host, error, max(0, round(give_up_at - now))
            )

    def _fail(self, error: LinkError) -> None:
        log.error("%s", error)
        self.failure = error
        self._closing = True
        self._connected.clear()
        self._notify(self._failure_listeners, error)

    def _rate_wait(self) -> float:
        """How long until another connection attempt keeps within ``max_connects_per_minute``."""
        now = time.monotonic()
        while self._attempts and now - self._attempts[0] >= RATE_WINDOW_SECONDS:
            self._attempts.popleft()
        limit = max(self.config.max_connects_per_minute, 1)
        if len(self._attempts) < limit:
            return 0.0
        return self._attempts[len(self._attempts) - limit] + RATE_WINDOW_SECONDS - now

    async def drop(self, reason: str) -> None:
        """Close the current connection as if it had dropped: the agent reconnects with back-off, and Agent Mesh
        gives back what it delivered on it and the agent had not acknowledged."""
        ws = self._ws
        if ws is not None and not self._closing:
            log.warning("closing the agent link connection to start afresh reason=%s", reason)
            with contextlib.suppress(Exception):
                await ws.close(code=1000, reason=reason[:100])

    async def send(self, frame: Mapping[str, Any], *, only_on: str | None = None) -> str:
        """Send one message; returns the id of the connection it went out on. With ``only_on``, the message is sent
        only on that connection (StaleConnection otherwise), checked after waiting out a reconnect. A frame that is
        not strict JSON (a float that is not finite) is refused here, as the entrypoint would refuse it without naming
        it."""
        try:
            text = json_text(frame)
        except ValueError as exc:
            raise LinkError(f"{frame.get('type')} message is not valid JSON: {exc}") from exc
        if len(text.encode("utf-8")) > self.config.max_frame_bytes:
            raise LinkError(f"{frame.get('type')} message larger than the link's max_frame_bytes")
        if not self._connected.is_set():
            if self._closing or (self._runner is None and self._ws is None):
                raise LinkError("the agent link is not connected")
            try:
                await within(self.config.connect_timeout_seconds, self._connected.wait())
            except TimeoutError as exc:
                raise LinkError("the agent link is not connected") from exc
        from websockets.exceptions import ConnectionClosed

        ws, connection = self._ws, self.connection_id
        if ws is None:
            raise LinkError("the agent link is not connected")
        if only_on is not None and connection != only_on:
            raise StaleConnection(f"{frame.get('type')} belongs to an earlier connection")
        try:
            await ws.send(text)
        except ConnectionClosed as exc:
            raise LinkError("the agent link dropped") from exc
        return connection

    def _on_raw(self, raw: str | bytes) -> None:
        """Parse one incoming message and hand it to the control or frame handler.

        A message that cannot be parsed or handled is logged and dropped: one bad message must not stop the reader.
        """
        try:
            frame = json.loads(raw)
        except ValueError:
            log.warning("dropping an unparseable message from Agent Mesh bytes=%d", len(raw))
            return
        if not isinstance(frame, dict):
            log.warning(
                "dropping a message from Agent Mesh that is not a JSON object json_type=%s", type(frame).__name__
            )
            return
        kind = frame.get("type")
        if kind == "control":
            self._on_control(frame)
        elif kind == "welcome":
            log.warning("ignoring a repeated welcome on the agent link")
        elif self._frame_handler is None:
            log.warning("dropping a message no protocol codec takes type=%s", _kind(kind))
        else:
            try:
                self._frame_handler(frame)
            except Exception as exc:
                log.error("agent link message handling failed type=%s err=%s", _kind(kind), type(exc).__name__)

    def _on_control(self, frame: dict) -> None:
        enabled = frame.get("enabled")
        if not isinstance(enabled, bool):
            log.warning("ignoring a control message without 'enabled'")
            return
        reason = frame.get("reason")
        reason = reason if isinstance(reason, str) else ""
        if enabled:
            log.info("SAM released the agent reason=%s", reason)
        else:
            log.warning("SAM stopped the agent reason=%s", reason)
        self.enabled = enabled
        self._notify(self._control_listeners, enabled, reason)

    def _notify(self, listeners: list, *args: Any) -> None:
        """Call each listener, spawning any that is async.

        A listener's failure is logged and does not stop the others or the link's reader.
        """
        for listener in list(listeners):
            try:
                out = listener(*args)
            except Exception as exc:
                log.error("agent link listener failed err=%s", type(exc).__name__)
                continue
            if inspect.isawaitable(out):
                self.spawn(out)

    def spawn(self, coro: Awaitable[Any]) -> None:
        self._callbacks.spawn(coro)


def _mismatch(problem: str) -> LinkProtocolMismatch:
    log.error("agent link protocol mismatch: %s; this SDK speaks %s only", problem, PROTOCOL_V2)
    return LinkProtocolMismatch(f"{problem}; this SDK speaks {PROTOCOL_V2} only")


def _kind(kind: Any) -> str:
    return kind[:64] if isinstance(kind, str) else type(kind).__name__


async def _close_quietly(ws: Any) -> None:
    with contextlib.suppress(Exception):
        await ws.close()
