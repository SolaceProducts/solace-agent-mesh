# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A run that its framework stopped on purpose: at its own loop limit, an adapter's, or a guardrail. The task fails with
a message that says so, instead of SAM's generic "internal error"."""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

_FRAMEWORK_LIMITS = {
    ("agents.exceptions", "MaxTurnsExceeded"): "the OpenAI Agents limit on model calls (max_model_calls)",
    ("langgraph.errors", "GraphRecursionError"): "the LangGraph limit on steps (recursion_limit)",
    (
        "google.adk.agents.invocation_context",
        "LlmCallsLimitExceededError",
    ): "the ADK limit on model calls (max_model_calls)",
    ("strands.types.exceptions", "MaxTokensReachedException"): "the model's limit on output tokens (max_tokens)",
}
"""The exception a framework raises at its own loop limit, by module and class (matched by name, so no framework is
imported), and the limit as the user is told it.
"""


def _guardrail_name(err: Any) -> str:
    guardrail = getattr(getattr(err, "guardrail_result", None), "guardrail", None)
    get_name = getattr(guardrail, "get_name", None)
    return str(get_name()) if callable(get_name) else "a guardrail"


_GUARDRAILS: dict[tuple[str, str], Callable[[Any], str]] = {
    ("agents.exceptions", "InputGuardrailTripwireTriggered"): lambda err: (
        f"Stopped: the guardrail {_guardrail_name(err)} blocked this request."
    ),
    ("agents.exceptions", "OutputGuardrailTripwireTriggered"): lambda err: (
        f"Stopped: the guardrail {_guardrail_name(err)} blocked the agent's answer."
    ),
}


class LimitReached(Exception):
    """Raised by an adapter whose own loop stopped at a limit; its message is the task's failure message."""


def limit_message(exc: BaseException) -> str | None:
    """The failure message for a run its framework stopped on purpose (`exc`, or an exception it wraps), else None."""
    seen: set[int] = set()
    err: BaseException | None = exc
    while err is not None and id(err) not in seen:
        seen.add(id(err))
        if isinstance(err, LimitReached):
            return str(err)
        key = (type(err).__module__, type(err).__name__)
        if key in _FRAMEWORK_LIMITS:
            return f"Stopped: the agent reached {_FRAMEWORK_LIMITS[key]}."
        if key in _GUARDRAILS:
            return _GUARDRAILS[key](err)
        err = err.__cause__ or err.__context__
    return None
