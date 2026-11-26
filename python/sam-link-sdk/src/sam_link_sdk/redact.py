# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Credential redaction for tool data on the wire: a port of SAM-Go's internal/tool/hilsurface/redact.go.

SAM-Go redacts tool arguments, tool results and model-call arguments before they are published, because those
events reach the web UI, the task event log and shared sessions. The rules:

* a key that names a credential (``password``, ``api_key``, ``accessToken`` ...) has its whole value replaced;
* a value under such a key (an auth blob) is replaced when it is long enough to be a secret, not an enum;
* text is scanned for ``name=value`` assignments, Authorization headers, Bearer/Basic tokens, JWTs, private
  keys, vendor token prefixes, webhook URLs and URL passwords or credential query parameters;
* only the secret is replaced, never its name, so a reader can still see which credential was set.

``tests/fixtures/go_redact_cases.json`` holds the Go redactor's own output for a set of inputs, and the tests
require this port to match it. One deliberate difference: the Go patterns run on a linear-time regex engine, so
the credential-name prefix and suffix here are bounded to 64 characters to keep Python's backtracking engine
fast on adversarial text.
"""

from __future__ import annotations

import json
import re
from collections.abc import Mapping
from typing import Any

from .captext import MAX_RESULT_STRING_BYTES, cap_text

REDACTED = "<REDACTED>"
UNSERIALIZABLE = "<unserializable value>"

_INHERITED_LEAF_MIN_BYTES = 8
"""A string leaf that only inherits credential context is redacted once it is long enough to be a secret."""

_NOT, _NAMED, _INHERITED = 0, 1, 2

_NAME_ALTERNATION = (
    r"secret|password|passwd|passphrase|pwd|token|credential"
    r"|authorization|auth|cookie|signature|sig|hmac|jwt"
    r"|api[_-]?key|access[_-]?key|private[_-]?key|signing[_-]?key|master[_-]?key"
    r"|encryption[_-]?key|account[_-]?key|license[_-]?key|deploy[_-]?key"
    r"|subscription[_-]?key|connection[_-]?string"
    r"|passcode|dsn|app[_-]?key|consumer[_-]?key|client[_-]?key"
    r"|session[_-]?key|shared[_-]?key"
)
_NAME = r"[a-z0-9_.-]{0,64}(?:" + _NAME_ALTERNATION + r")[0-9]*(?:[_.-][a-z0-9_.-]{0,64})?"

_DOUBLE_QUOTED = re.compile(r"(\"?\b" + _NAME + r"\"?[^\S\n]*[:=][^\S\n]*\")([^\"\n]{8,})(\")", re.I)
_SINGLE_QUOTED = re.compile(r"('?\b" + _NAME + r"'?[^\S\n]*[:=][^\S\n]*')([^'\n]{8,})(')", re.I)
_BARE = re.compile(r"(\b" + _NAME + r"[^\S\n]*[:=][^\S\n]*)([^\s\"',;&})\]><]{8,})", re.I)

_PLACEHOLDER = re.compile(r"^(?:\$\{[^}]*\}?|\{\{[^}]*\}{0,2}|%\([^)]*\)?s?|\$[A-Z_][A-Z0-9_]*)$")
_AUTH_SCHEME_VALUE = re.compile(r"\b(bearer|basic)(\s+)([A-Za-z0-9._~+/=-]{8,})", re.I)
_AUTH_HEADER_SPAN = re.compile(r"(\b(?:proxy-)?authorization[^\S\n]*[:=][^\S\n]*)([^\n]*)", re.I)
_AUTH_SCHEME_PREFIX = re.compile(r"^[A-Za-z][A-Za-z0-9._-]*[^\S\n]+")
_AUTH_PARAM_LIST = re.compile(
    r"^[A-Za-z][A-Za-z0-9_-]*[^\S\n]*=[^\S\n]*\"|,[^\S\n]*[A-Za-z][A-Za-z0-9_-]*[^\S\n]*=", re.I
)
_AUTH_PARAM = re.compile(r"^([A-Za-z][A-Za-z0-9_-]*)([^\S\n]*=[^\S\n]*)(\"?)([^\"]*)(\"?)$", re.I)
_AUTH_PARAM_CREDENTIALS = ("response", "cnonce", "opaque")

_NON_SECRET_ENUMS = frozenset(
    (
        "none",
        "null",
        "nil",
        "n/a",
        "na",
        "unset",
        "empty",
        "default",
        "basic",
        "bearer",
        "digest",
        "oauth",
        "oauth2",
        "apikey",
        "true",
        "false",
        "yes",
        "no",
        "on",
        "off",
        "0",
        "1",
        "required",
        "optional",
        "enabled",
        "disabled",
        "accepted",
    )
)

_OPAQUE_SECRETS = (
    re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.+?-----END [A-Z ]*PRIVATE KEY-----", re.S),
    re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}"),
    re.compile(
        r"\b(?:xox[abopsr]-[A-Za-z0-9-]{8,}|xapp-[A-Za-z0-9-]{8,}"
        r"|sk-[A-Za-z0-9_-]{8,}|sk_(?:live|test)_[A-Za-z0-9]{8,}"
        r"|github_pat_[A-Za-z0-9_]{16,}|gh[pousr]_[A-Za-z0-9]{16,}|glpat-[A-Za-z0-9_-]{16,}"
        r"|AKIA[0-9A-Z]{12,}|AIza[A-Za-z0-9_-]{20,})"
    ),
)
_WEBHOOK = re.compile(
    r"(https://(?:hooks\.slack\.com/services|discord(?:app)?\.com/api/webhooks)/)[A-Za-z0-9/_-]{8,}", re.I
)
_EMBEDDED_URL = re.compile(r"\b[a-z][a-z0-9+.-]*://[^\s\"'<>\\]+", re.I)

_KEY_NEEDLES = (
    "apikey",
    "accesskey",
    "secretkey",
    "privatekey",
    "clientsecret",
    "accesstoken",
    "refreshtoken",
    "idtoken",
    "sessiontoken",
    "bearertoken",
    "authtoken",
    "subscriptionkey",
    "licensekey",
    "deploykey",
    "signingkey",
    "masterkey",
    "encryptionkey",
    "accountkey",
    "connectionstring",
    "apisecret",
    "secretaccesskey",
    "appkey",
    "consumerkey",
    "clientkey",
    "sessionkey",
    "sharedkey",
    "bindpw",
)
_QUERY_KEY_WORDS = frozenset(("key", "sig", "session", "sessionid", "sid", "passcode"))
_KEY_WORDS = frozenset(
    (
        "secret",
        "secrets",
        "password",
        "passwords",
        "passwd",
        "passphrase",
        "passphrases",
        "pwd",
        "token",
        "auth",
        "authorization",
        "credential",
        "credentials",
        "cookie",
        "cookies",
        "signature",
        "signatures",
        "sig",
        "hmac",
        "jwt",
        "pat",
        "passcode",
        "pin",
        "dsn",
    )
)
"""The word "tokens" is deliberately absent: max_tokens, input_tokens and tokens_used are token counts."""
_METADATA_SUFFIXES = frozenset(
    (
        "url",
        "uri",
        "endpoint",
        "type",
        "kind",
        "name",
        "id",
        "scheme",
        "required",
        "enabled",
        "present",
        "count",
        "length",
        "expiry",
        "expires",
        "ttl",
        "method",
        "provider",
        "mode",
        "status",
        "algorithm",
        "alg",
        "version",
        "scope",
        "scopes",
        "policy",
        "domain",
        "path",
        "format",
        "at",
        "secure",
    )
)
"""A name whose last word is one of these describes a credential rather than holding one (token_url, auth_type)."""


def _is_non_secret_enum(value: str) -> bool:
    return value.strip().lower() in _NON_SECRET_ENUMS


def _is_lower_alpha_word(s: str) -> bool:
    return s != "" and all("a" <= ch <= "z" for ch in s)


def _split_key_words(key: str) -> list[str]:
    """Lowercase and split on separators and camelCase humps: X-Access-Token, accessToken, access_token."""
    words: list[str] = []
    current: list[str] = []

    def flush() -> None:
        if current:
            words.append("".join(current).lower())
            current.clear()

    def is_lower_at(i: int) -> bool:
        return 0 <= i < len(key) and "a" <= key[i] <= "z"

    for i, ch in enumerate(key):
        if "A" <= ch <= "Z":
            if is_lower_at(i - 1) or (i > 0 and is_lower_at(i + 1)):
                flush()
            current.append(ch)
        elif ("a" <= ch <= "z") or ("0" <= ch <= "9"):
            current.append(ch)
        else:
            flush()
    flush()
    return words


def _describes_credential_words(words: list[str]) -> bool:
    return bool(words) and words[-1] in _METADATA_SUFFIXES


def _describes_credential(key: str) -> bool:
    return _describes_credential_words(_split_key_words(key))


def _is_credential_key_words(words: list[str]) -> bool:
    if _describes_credential_words(words):
        return False
    normalized = "".join(words)
    if any(needle in normalized for needle in _KEY_NEEDLES):
        return True
    return any(word in _KEY_WORDS for word in words)


def is_credential_key(key: str) -> bool:
    return _is_credential_key_words(_split_key_words(key))


def _is_credential_query_key(key: str) -> bool:
    words = _split_key_words(key)
    return _is_credential_key_words(words) or (len(words) == 1 and words[0] in _QUERY_KEY_WORDS)


def _assigned_name(prefix: str) -> str:
    return prefix.rstrip(" \t:=\"'").strip("\"'")


def _ends_with_quote(prefix: str) -> bool:
    return prefix.endswith('"') or prefix.endswith("'")


def _redact_assigned_values(s: str, pattern: re.Pattern, keep_third: bool, owned_elsewhere=None) -> str:
    def replace(m: re.Match) -> str:
        """Redact an assigned value unless it is a placeholder, a non-credential name or code.

        An unquoted value ending in a call or an index is code reading a secret from elsewhere, so it is kept.
        """
        if _PLACEHOLDER.match(m.group(2)):
            return m.group(0)
        name = _assigned_name(m.group(1))
        if _describes_credential(name):
            return m.group(0)
        if owned_elsewhere is not None and owned_elsewhere(name):
            return m.group(0)
        if not _ends_with_quote(m.group(1)) and (m.group(2).endswith("(") or m.group(2).endswith("[")):
            return m.group(0)
        return m.group(1) + REDACTED + (m.group(3) if keep_third else "")

    return pattern.sub(replace, s)


def _is_authorization_header_name(name: str) -> bool:
    return name.lower() in ("authorization", "proxy-authorization")


def _redact_authorization_params(items: str) -> str:
    parts = items.split(",")
    for i, part in enumerate(parts):
        m = _AUTH_PARAM.match(part.strip())
        if m is None:
            continue
        name = m.group(1)
        if name.lower() not in _AUTH_PARAM_CREDENTIALS and not is_credential_key(name):
            continue
        lead = part[: len(part) - len(part.lstrip(" \t"))]
        parts[i] = lead + name + m.group(2) + m.group(3) + REDACTED + m.group(5)
    return ",".join(parts)


def _redact_authorization_value(value: str) -> str:
    prefix = _AUTH_SCHEME_PREFIX.match(value)
    scheme = prefix.group(0) if prefix else ""
    rest = value[len(scheme) :]
    if _AUTH_PARAM_LIST.search(rest):
        return scheme + _redact_authorization_params(rest)
    end = next((i for i, ch in enumerate(rest) if ch in "\"',;"), len(rest))
    if end == 0:
        return value
    return scheme + REDACTED + rest[end:]


def _redact_authorization_headers(s: str) -> str:
    return _AUTH_HEADER_SPAN.sub(lambda m: m.group(1) + _redact_authorization_value(m.group(2)), s)


def _redact_auth_scheme_values(s: str) -> str:
    def replace(m: re.Match) -> str:
        if _is_lower_alpha_word(m.group(3).rstrip(".")):
            return m.group(0)
        return m.group(1) + m.group(2) + REDACTED

    return _AUTH_SCHEME_VALUE.sub(replace, s)


def _redact_pair_list(items: str) -> str:
    if items == "":
        return items
    out: list[str] = []
    start = 0
    for i in range(len(items) + 1):
        if i < len(items) and items[i] not in "&;?":
            continue
        segment = items[start:i]
        eq = segment.find("=")
        if (
            0 < eq < len(segment) - 1
            and _is_credential_query_key(segment[:eq])
            and not _PLACEHOLDER.match(segment[eq + 1 :])
            and not _is_non_secret_enum(segment[eq + 1 :])
        ):
            out.append(segment[: eq + 1] + REDACTED)
        else:
            out.append(segment)
        if i < len(items):
            out.append(items[i])
        start = i + 1
    return "".join(out)


def _redact_url_credentials(raw: str) -> str:
    scheme = raw.find("://")
    if scheme < 0:
        return raw
    rest = raw[scheme + 3 :]
    authority_end = next((i for i, ch in enumerate(rest) if ch in "/?#"), -1)
    authority = rest if authority_end < 0 else rest[:authority_end]
    at = authority.rfind("@")
    if at >= 0:
        userinfo = authority[:at]
        colon = userinfo.find(":")
        if colon >= 0:
            userinfo = userinfo[: colon + 1] + REDACTED
        rest = userinfo + "@" + rest[at + 1 :]
    out = raw[: scheme + 3] + rest
    query = out.find("?")
    fragment = out.find("#")
    if query >= 0 and (fragment < 0 or query < fragment):
        if fragment < 0:
            return out[: query + 1] + _redact_pair_list(out[query + 1 :])
        return (
            out[: query + 1]
            + _redact_pair_list(out[query + 1 : fragment])
            + out[fragment : fragment + 1]
            + _redact_pair_list(out[fragment + 1 :])
        )
    if fragment >= 0 and "=" in out[fragment:]:
        return out[: fragment + 1] + _redact_pair_list(out[fragment + 1 :])
    return out


def redact_string(s: str) -> str:
    """Redact the secrets in a string and cap its size.

    The assignment patterns need a `:` or `=` separator, so skipping them otherwise keeps long plain text cheap.
    Redaction grows a string (`token=a` becomes `token=<REDACTED>`), so the result is capped again to keep the
    documented bound.
    """
    s = cap_text(s, MAX_RESULT_STRING_BYTES)

    def redact_url(m: re.Match) -> str:
        match = m.group(0)
        trimmed = match.rstrip(".,;:!?)]}'\"")
        return _redact_url_credentials(trimmed) + match[len(trimmed) :]

    if "://" in s:
        s = _EMBEDDED_URL.sub(redact_url, s)
    s = _redact_authorization_headers(s)
    if ":" in s or "=" in s:
        s = _redact_assigned_values(s, _DOUBLE_QUOTED, True)
        s = _redact_assigned_values(s, _SINGLE_QUOTED, True)
        s = _redact_assigned_values(s, _BARE, False, _is_authorization_header_name)
    s = _redact_auth_scheme_values(s)
    for pattern in _OPAQUE_SECRETS:
        s = pattern.sub(REDACTED, s)
    s = _WEBHOOK.sub(lambda m: m.group(1) + REDACTED, s)
    return cap_text(s, MAX_RESULT_STRING_BYTES)


def _child_scope(parent: int, key: str) -> int:
    if is_credential_key(key):
        return _NAMED
    if parent != _NOT and not _describes_credential(key):
        return _INHERITED
    return _NOT


def _redacts_leaf(scope: int, value: str) -> bool:
    if scope == _NAMED:
        return not _is_non_secret_enum(value)
    if scope == _INHERITED:
        return len(value) >= _INHERITED_LEAF_MIN_BYTES
    return False


def _json_default(value: Any) -> Any:
    if isinstance(value, (bytes, bytearray, memoryview)):
        return f"<binary {len(value)} bytes>"
    return str(value)


def _redact_node(value: Any, scope: int) -> Any:
    """Redact one node of a value, by the scope its key gives it.

    A numeric account key or passcode is still a credential, so a number under a named key is redacted.
    """
    if value is None:
        return None
    if isinstance(value, (bool, int, float)):
        return REDACTED if scope == _NAMED else value
    if isinstance(value, str):
        return REDACTED if _redacts_leaf(scope, value) else redact_string(value)
    if isinstance(value, Mapping):
        return {str(k): _redact_node(v, _child_scope(scope, str(k))) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [_redact_node(v, scope) for v in value]
    try:
        normalized = json.loads(json.dumps(value, default=_json_default))
    except (TypeError, ValueError, OverflowError):
        return UNSERIALIZABLE
    return _redact_node(normalized, scope)


def redact_value(value: Any) -> Any:
    """Return a redacted deep copy of a tool argument or result. The original is never changed: the
    framework is still using it, and a policy that matches on argument values must see the real ones."""
    return _redact_node(value, _NOT)
