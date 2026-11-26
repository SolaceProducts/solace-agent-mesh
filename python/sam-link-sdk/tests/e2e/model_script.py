# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The only fake in the end-to-end suite: a scripted OpenAI-compatible model the example agent reaches over HTTP through
its framework's own client (``tests/hermes_fake_llm.py``'s ``FakeLLM``, plain and streamed).

What this adds to ``FakeLLM``: every request is kept with the time it arrived, a test can wait for requests instead of
sleeping, and a turn that follows a tool call can name the call ids whose results the request must carry. A request
without them is answered with an HTTP 500 and noted in ``violations``, so a framework that stops handing tool results
back to its model fails loudly instead of being scripted past.
"""

from __future__ import annotations

import json
import threading
import time
from dataclasses import dataclass, field
from typing import Any

from ..hermes_fake_llm import FakeLLM

UNSCRIPTED = "a request arrived after the script ran out"


@dataclass(frozen=True)
class Turn:
    """One scripted answer. `calls` are (call id, tool, arguments); `expects` are the call ids whose results the
    request this answers must carry; `hold`, when set, keeps the answer back until the event is set; `status`, when
    set, answers with that HTTP error and `message` (and error `code`) instead, as a failing provider does; `cached` and `cache_write`
    are the input tokens the usage reports as read from and written to the provider's cache (both within the input,
    as an OpenAI-compatible endpoint counts them); `reasoning` is the reasoning the model writes before its answer, and
    `reasoning_tokens` the output tokens the usage reports as spent reasoning (within the output);
    `usage_keys`, when set, are the names the usage gives the input and output counts in place of ``prompt_tokens`` and
    ``completion_tokens``, as a provider release that renamed them would (``total_tokens`` keeps its name)."""

    text: str = ""
    calls: tuple[tuple[str, str, dict], ...] = ()
    expects: tuple[str, ...] = ()
    usage: tuple[int, int] = (300, 20)
    hold: threading.Event | None = None
    reached: threading.Event | None = None
    status: int = 0
    message: str = ""
    code: str = ""
    cached: int = 0
    cache_write: int = 0
    reasoning: str = ""
    usage_keys: tuple[str, str] = ()
    reasoning_tokens: int = 0
    text_after_calls: str = ""
    finish: str = ""

    def as_fake_turn(self) -> dict:
        turn: dict[str, Any] = {"usage": self.usage, "cached": self.cached, "cache_write": self.cache_write}
        if self.reasoning:
            turn["reasoning"] = self.reasoning
        if self.usage_keys:
            turn["usage_keys"] = self.usage_keys
        if self.reasoning_tokens:
            turn["reasoning_tokens"] = self.reasoning_tokens
        if self.status:
            turn["status"], turn["message"], turn["code"] = self.status, self.message, self.code
        if self.text:
            turn["text"] = self.text
        if self.calls:
            turn["calls"] = list(self.calls)
        if self.text_after_calls:
            turn["text_after_calls"] = self.text_after_calls
        if self.finish:
            turn["finish"] = self.finish
        if self.hold is not None:
            turn["hold"] = self.hold
        if self.reached is not None:
            turn["reached"] = self.reached
        return turn


@dataclass
class Request:
    body: dict
    at: float

    def tool_results(self) -> dict[str, str]:
        """The tool results this request hands the model, by call id, as text."""
        out = {}
        for message in self.body.get("messages") or []:
            if message.get("role") == "tool":
                content = message.get("content")
                out[str(message.get("tool_call_id"))] = content if isinstance(content, str) else json.dumps(content)
        return out

    def offered_tools(self) -> list[str]:
        return sorted((t.get("function") or {}).get("name", "") for t in self.body.get("tools") or [])

    def text(self) -> str:
        return json.dumps(self.body)


class _Log(list):
    """FakeLLM appends each request body here; the arrival time is kept beside it."""

    def __init__(self, model: ScriptedModel):
        super().__init__()
        self._model = model

    def append(self, body: dict) -> None:
        super().append(body)
        self._model._arrived(body)


class _Script(list):
    """FakeLLM pops the next turn from here while it holds its lock, right after it logged the request."""

    def __init__(self, model: ScriptedModel, turns: list[Turn]):
        super().__init__(turns)
        self._model = model

    def __bool__(self) -> bool:
        return True

    def pop(self, index: int = -1) -> dict:
        request = self._model.requests[-1]
        if not len(self):
            self._model.violations.append(UNSCRIPTED)
            return {"status": 500, "message": UNSCRIPTED}
        turn: Turn = super().pop(index)
        missing = [cid for cid in turn.expects if cid not in request.tool_results()]
        if missing:
            why = f"request {len(self._model.requests)} carries no tool result for {', '.join(missing)}"
            self._model.violations.append(why)
            return {"status": 500, "message": why}
        return turn.as_fake_turn()


class _CachingFakeLLM(FakeLLM):
    """FakeLLM whose usage also carries cache tokens: reads as ``prompt_tokens_details.cached_tokens``, the field
    OpenAI-compatible endpoints use, and writes as ``cache_creation_input_tokens``, which LiteLLM and Anthropic-style
    gateways use, since the Chat Completions format has no field for them. With ``usage_keys`` the input and output
    counts go under those names instead."""

    @staticmethod
    def _usage(turn):
        usage = FakeLLM._usage(turn)
        if turn.get("usage_keys"):
            for old, new in zip(("prompt_tokens", "completion_tokens"), turn["usage_keys"], strict=True):
                usage[new] = usage.pop(old)
        if turn.get("cached"):
            usage["prompt_tokens_details"] = {"cached_tokens": turn["cached"]}
        if turn.get("cache_write"):
            usage["cache_creation_input_tokens"] = turn["cache_write"]
        if turn.get("reasoning_tokens"):
            usage["completion_tokens_details"] = {"reasoning_tokens": turn["reasoning_tokens"]}
        return usage


@dataclass
class ScriptedModel:
    """One scripted model per agent process. ``script()`` starts a test's script; ``requests`` are that script's."""

    _fake: FakeLLM = field(init=False)
    _all: list[Request] = field(default_factory=list)
    _start: int = 0
    violations: list[str] = field(default_factory=list)
    _arrival: threading.Condition = field(default_factory=threading.Condition)

    def __post_init__(self) -> None:
        self._fake = _CachingFakeLLM([])
        self._fake.requests = _Log(self)
        self._fake.turns = _Script(self, [])

    @property
    def base_url(self) -> str:
        return self._fake.base_url

    def close(self) -> None:
        self._fake.close()

    def script(self, *turns: Turn) -> None:
        with self._fake._lock, self._arrival:
            self._fake.turns = _Script(self, list(turns))
            self._start = len(self._all)
            self.violations = []

    def _arrived(self, body: dict) -> None:
        with self._arrival:
            self._all.append(Request(body, time.monotonic()))
            self._arrival.notify_all()

    @property
    def requests(self) -> list[Request]:
        with self._arrival:
            return self._all[self._start :]

    def wait_for_requests(self, count: int, timeout: float) -> list[Request]:
        """Wait until this script has seen at least `count` requests; fail with what it saw if it has not."""
        deadline = time.monotonic() + timeout
        with self._arrival:
            while len(self._all) - self._start < count:
                left = deadline - time.monotonic()
                if left <= 0:
                    raise AssertionError(
                        f"the model received {len(self._all) - self._start} request(s) within "
                        f"{timeout:g} s, expected {count}"
                    )
                self._arrival.wait(left)
            return self._all[self._start :]

    def no_request_beyond(self, count: int, within: float) -> bool:
        """True when no request past the first `count` arrives within `within` seconds: the one bounded wait that
        proves something did not happen."""
        deadline = time.monotonic() + within
        with self._arrival:
            while len(self._all) - self._start <= count:
                left = deadline - time.monotonic()
                if left <= 0:
                    return True
                self._arrival.wait(left)
            return False

    def all_text(self) -> str:
        return "\n".join(r.text() for r in self.requests)
