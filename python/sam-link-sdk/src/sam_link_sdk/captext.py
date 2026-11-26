# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Cutting long strings the way SAM-Go does (internal/tool/captext.go)."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

MAX_RESULT_STRING_BYTES = 16384
"""status.go caps tool_result and tool_invocation_start strings at 16 KiB."""
TRUNCATION_MARKER_BYTES = 32
"""Worst-case width of the "…[truncated N bytes]" marker."""


def well_formed(value: str) -> str:
    """The string with each lone surrogate (which no UTF-8 can carry) replaced by U+FFFD, as Go writes one."""
    try:
        value.encode("utf-8")
    except UnicodeEncodeError:
        return value.encode("utf-16", "surrogatepass").decode("utf-16", "replace")
    return value


def cap_text(value: str, max_bytes: int = MAX_RESULT_STRING_BYTES) -> str:
    """Cut a string to at most `max_bytes` UTF-8 bytes, marker included, as SAM-Go's tool.CapText does: keep
    max-32 bytes of valid UTF-8, then append "…[truncated N bytes]"."""
    value = well_formed(value)
    raw = value.encode("utf-8")
    if max_bytes <= 0 or len(raw) <= max_bytes:
        return value
    keep = max_bytes if max_bytes <= 2 * TRUNCATION_MARKER_BYTES else max_bytes - TRUNCATION_MARKER_BYTES
    kept = raw[:keep].decode("utf-8", errors="ignore")
    return f"{kept}…[truncated {len(raw) - len(kept.encode('utf-8'))} bytes]"


def cap_nested(value: Any) -> Any:
    """Cap every string inside a tool's arguments or result (SAM-Go's truncateStatusText). Returns a copy, so the
    object the framework is still using is never changed."""
    if isinstance(value, str):
        return cap_text(value)
    if isinstance(value, Mapping):
        return {k: cap_nested(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [cap_nested(v) for v in value]
    return value
