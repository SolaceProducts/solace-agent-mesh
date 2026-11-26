# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Reduce a model-provider failure to the closed categories SAM-Go reports (internal/llm/error_category.go).

When a model call fails, a SAM-Go agent's JSON-RPC error carries ``llm_error_category`` and ``llm_provider`` in its
data, so the failure can be counted and routed without the provider's own text, which may hold prompt content.
The SDK does the same for errors raised through the frameworks' provider clients.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any

AUTH_FAILURE = "auth_failure"
RATE_LIMIT = "rate_limit"
TIMEOUT = "timeout"
CONTEXT_OVERFLOW = "context_overflow"
CONTENT_FILTERED = "content_filtered"
UNKNOWN = "unknown"

PROVIDER_OTHER = "other"

_CONTENT_FILTER = (
    "content filter",
    "content_filter",
    "content policy",
    "content_policy",
    "content management policy",
    "safety system",
    "safety filter",
    "responsible ai",
    "guardrail",
    "moderation",
)
_AUTH = (
    "invalid api key",
    "incorrect api key",
    "invalid_api_key",
    "missing api key",
    "no api key",
    "api key not",
    "invalid x-api-key",
    "authentication",
    "unauthenticated",
    "unauthorized",
    "permission denied",
    "access denied",
    "forbidden",
    "invalid credentials",
    "invalid_client",
    "invalid_grant",
    "invalid grant",
    "expired token",
    "security token",
)
_TIMEOUT = ("timed out", "timeout", "deadline exceeded")
_RATE_LIMIT = (
    "rate limit",
    "rate_limit",
    "ratelimit",
    "too many requests",
    "quota exceeded",
    "throttl",
    "resource_exhausted",
    "resource exhausted",
)
_CONTEXT_LIMIT = (
    "too many tokens",
    "expected maxlength:",
    "input is too long",
    "prompt is too long",
    "prompt: length: 1..",
    "too many input tokens",
    "maximum context length",
    "context length exceeded",
    "context_length_exceeded",
    "exceeds the context window",
    "exceed context limit",
    "token limit",
)

KNOWN_PROVIDERS = frozenset(
    (
        "anthropic",
        "azure",
        "bedrock",
        "cerebras",
        "cohere",
        "elevenlabs",
        "gemini",
        "groq",
        "huggingface",
        "mistral",
        "nebius",
        "ollama",
        "openai",
        "openrouter",
        "parasail",
        "perplexity",
        "sgl",
        "vertex",
        "xai",
    )
)
_MODULE_PROVIDERS = {
    "openai": "openai",
    "anthropic": "anthropic",
    "cohere": "cohere",
    "mistralai": "mistral",
    "groq": "groq",
    "ollama": "ollama",
    "botocore": "bedrock",
    "vertexai": "vertex",
    "litellm": "",
    "google": "gemini",
}
_GOOGLE_CLIENTS = ("google.genai", "google.generativeai", "google.api_core", "google.auth")
"""Under `google` only these are model clients: ADK (google.adk) also raises from there, for a tool's or the agent's own
failure, and that is not a model failure.
"""


@dataclass(frozen=True)
class LLMFailure:
    category: str
    provider: str = ""


def _matches(text: str, patterns: tuple[str, ...]) -> bool:
    lower = text.lower()
    return any(p in lower for p in patterns)


def categorize(
    status_code: int | None, message: str, code: str = "", type_: str = "", is_timeout_class: bool = False
) -> str:
    """The category for a provider failure: status codes first, then message matching, broadest last."""
    if status_code == 429:
        return RATE_LIMIT
    if status_code in (401, 403):
        return AUTH_FAILURE
    if status_code in (408, 504) or is_timeout_class:
        return TIMEOUT
    fields = [f for f in (message, type_, code) if f]
    if code == "context_length_exceeded" or (status_code == 400 and _matches(message, _CONTEXT_LIMIT)):
        return CONTEXT_OVERFLOW
    for patterns, category in (
        (_CONTENT_FILTER, CONTENT_FILTERED),
        (_AUTH, AUTH_FAILURE),
        (_TIMEOUT, TIMEOUT),
        (_RATE_LIMIT, RATE_LIMIT),
    ):
        if any(_matches(f, patterns) for f in fields):
            return category
    return UNKNOWN


def provider_label(provider: str) -> str:
    name = provider.strip().lower()
    if not name:
        return ""
    return name if name in KNOWN_PROVIDERS else PROVIDER_OTHER


def _chain(exc: BaseException):
    seen: set[int] = set()
    current: BaseException | None = exc
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        yield current
        current = current.__cause__ or current.__context__


def _words(type_name: str) -> str:
    """``PermissionDeniedError`` as ``Permission Denied Error``, so a type name matches the message patterns."""
    return re.sub(r"(?<=[a-z0-9])(?=[A-Z])", " ", type_name)


CATEGORIES = frozenset((AUTH_FAILURE, RATE_LIMIT, TIMEOUT, CONTEXT_OVERFLOW, CONTENT_FILTERED, UNKNOWN))


def classify_error_type(error_type: str, provider: str = "") -> LLMFailure:
    """The failure class of a model call known to have failed, from a string naming what went wrong rather than the
    exception itself: one of the categories above, an HTTP status code (``"429"``), or an exception type, bare or
    qualified by its module (``"openai.RateLimitError"``, ``"TimeoutError"``), as OpenTelemetry's ``error.type``
    records it. The provider is `provider`, else the client library a qualified type names. Anything unrecognised is
    ``unknown``: the call did fail, only its kind is unknown."""
    text = error_type.strip()
    if text in CATEGORIES:
        return LLMFailure(text, provider_label(provider))
    if text.isdigit():
        return LLMFailure(categorize(int(text), ""), provider_label(provider))
    module, _, name = text.rpartition(".")
    library = _MODULE_PROVIDERS.get(module.split(".")[0], "") if module else ""
    category = categorize(None, _words(name), type_=name, is_timeout_class="timeout" in name.lower())
    return LLMFailure(category, provider_label(provider or library))


def classify_llm_failure(exc: BaseException, provider: str = "") -> LLMFailure | None:
    """The failure class if `exc` (or an exception it wraps) was raised by a model provider's client library,
    else None, so a bug in a tool or the agent's own code is not reported as a model failure.

    The HTTP status is read from `status_code`, the response, or an int `code`: Google's client keeps it in an int
    `code`, while other clients' `code` is a string error code.
    """
    for err in _chain(exc):
        module = type(err).__module__
        root = module.split(".")[0]
        if root not in _MODULE_PROVIDERS or (root == "google" and not module.startswith(_GOOGLE_CLIENTS)):
            continue
        raw_code = getattr(err, "code", None)
        status: Any = (
            getattr(err, "status_code", None)
            or getattr(getattr(err, "response", None), "status_code", None)
            or (raw_code if isinstance(raw_code, int) and not isinstance(raw_code, bool) else None)
        )
        body = getattr(err, "body", None)
        code = str(getattr(err, "code", "") or (body.get("code", "") if isinstance(body, dict) else "") or "")
        kind = str(getattr(err, "type", "") or "")
        message = str(err)
        is_timeout = "timeout" in type(err).__name__.lower()
        label = provider_label(provider or _MODULE_PROVIDERS[root])
        return LLMFailure(
            categorize(status if isinstance(status, int) else None, message, code, kind, is_timeout), label
        )
    return None
