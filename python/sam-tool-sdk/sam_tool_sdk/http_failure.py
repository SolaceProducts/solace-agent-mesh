# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Coded results for tool failures that carry an HTTP status.

A tool that lets a ``requests`` or ``httpx`` HTTP error escape would otherwise
reach the agent as an uncoded execution error, losing the status. The SDK has
no HTTP client dependency, so the status is read by duck typing from
``exc.response.status_code``.
"""

from __future__ import annotations

import re
import traceback
from typing import Any, Dict, Optional

from .tool_result import ToolResult

_URL_SCHEME = r"\b[a-zA-Z][a-zA-Z0-9+.-]*://"
_URL_USERINFO = re.compile(rf"({_URL_SCHEME})[^/\s@?#]*@")
_URL_QUERY_OR_FRAGMENT = re.compile(
    rf"({_URL_SCHEME}[^\s?#]*)[?#]\S*?(?=['\"]?(?:\s|$))"
)
_URL_AUTHORITY_AT_END = re.compile(rf"({_URL_SCHEME})[^/\s]*$")

_MAX_DETAIL_CHARS = 4096

def http_failure_code(status: int) -> Optional[str]:
    """Return the tool_result error code for an HTTP failure status, or None.

    Mirrors Go's ``tool.HTTPFailureCode``. A 401 is ``DISPATCH_AUTH_REQUIRED``,
    never ``AUTH_REQUIRED``: that code starts the OAuth flow, which a tool
    calling a keyed API cannot complete.
    """
    if status == 401:
        return "DISPATCH_AUTH_REQUIRED"
    if status == 403:
        return "PERMISSION_DENIED"
    if status in (408, 504):
        return "DISPATCH_TIMEOUT"
    if status == 429:
        return "RATE_LIMITED"
    if 400 <= status <= 499:
        return "DISPATCH_BACKEND_4XX"
    if 500 <= status <= 599:
        return "DISPATCH_BACKEND_5XX"
    return None

def _http_status(exc: BaseException) -> Optional[int]:
    try:
        status = getattr(getattr(exc, "response", None), "status_code", None)
    except Exception:
        return None
    if isinstance(status, int) and 400 <= status <= 599:
        return status
    return None

def _redact_url_queries(text: str) -> str:
    """Drop the userinfo, query string and fragment of every URL in ``text``.

    ``requests`` and ``httpx`` put the full request URL in the error text, and
    those parts often carry credentials or API keys. A query runs to the next
    whitespace, less a closing quote, because neither client encodes ``'``.
    """
    return _URL_QUERY_OR_FRAGMENT.sub(r"\1", _URL_USERINFO.sub(r"\1", text))

def _capped(text: str) -> str:
    """Cut ``text`` to ``_MAX_DETAIL_CHARS``.

    A URL cut inside its authority loses the authority too, since the part
    before the cut may be the start of a password.
    """
    if len(text) <= _MAX_DETAIL_CHARS:
        return text
    return _URL_AUTHORITY_AT_END.sub(r"\1", text[:_MAX_DETAIL_CHARS]) + " [truncated]"

def execution_failure(exc: BaseException) -> Dict[str, Any]:
    """Build the runner envelope for an uncaught tool exception.

    Call it from inside the ``except`` block: an exception without an HTTP
    failure status keeps the uncoded ``{"error": ...}`` envelope with the
    active traceback.
    """
    status = _http_status(exc)
    if status is None:
        return {"error": f"Execution error: {exc}\n{traceback.format_exc()}"}
    message = _redact_url_queries(f"HTTP {status}: {_capped(str(exc))}")
    return {
        "result": ToolResult.error(
            message, code=http_failure_code(status)
        ).to_serializable()
    }
