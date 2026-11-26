# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Token usage: one model call's tokens (``Usage``), SAM's four token buckets (``TokenBuckets``), and the totals of
a task (``UsageAccumulator``, the token totals of a task's answer).

The totals mirror tokenUsageTotals / buildByModelUsage in SAM-Go's internal/agent/agent.go.
"""

from __future__ import annotations

import threading
from dataclasses import dataclass, field


@dataclass(frozen=True)
class TokenBuckets:
    """A model call's tokens as SAM counts and prices them: four disjoint buckets, so each token is in exactly one and
    their sum is the call's whole token count (SAM-Go's spend.Tokens)."""

    uncached_input: int = 0
    output: int = 0
    cache_read: int = 0
    cache_write: int = 0

    @property
    def input(self) -> int:
        """Every input token: what SAM calls the prompt tokens (its ``PromptTokens``, the link's ``input``)."""
        return self.uncached_input + self.cache_read + self.cache_write

    @property
    def cached(self) -> int:
        """The input tokens read from or written to the cache: SAM's ``CachedTokens`` (the link's ``cachedInput``),
        which holds the cache writes."""
        return self.cache_read + self.cache_write


@dataclass
class Usage:
    """One model call's tokens. Every framework adapter fills it the same way, and every consumer reads it through
    ``_buckets()``, so a call is counted alike everywhere SAM counts it.

    - ``input_tokens``: every input token of the call, cached ones included: the tokens read from the provider's cache
      and the tokens written to it are part of it. That is OpenAI's ``prompt_tokens``, LangChain's ``input_tokens``
      and Gemini's ``prompt_token_count``; Anthropic's ``input_tokens`` and Bedrock's ``inputTokens`` leave the cache
      tokens out, so an adapter adds them back.
    - ``cached_input_tokens``: the part of ``input_tokens`` read from the cache (cache hits). Cache writes are not in
      it.
    - ``cache_write_tokens``: the part of ``input_tokens`` written to the cache. Disjoint from
      ``cached_input_tokens``: ``input_tokens`` = uncached input + ``cached_input_tokens`` + ``cache_write_tokens``.
    - ``output_tokens``: every output token, reasoning included.
    - ``reasoning_tokens``: the part of ``output_tokens`` spent reasoning.
    - ``total_tokens``: the provider's own total when it gives one, else 0. Not used for accounting.

    SAM's convention nests the cache tokens differently (its cached count holds the writes); ``_buckets()`` is the one
    translation, used by the agent link's usage object and by a task's token totals.
    """

    input_tokens: int = 0
    output_tokens: int = 0
    cached_input_tokens: int = 0
    cache_write_tokens: int = 0
    reasoning_tokens: int = 0
    total_tokens: int = 0

    def _add(self, other: Usage) -> None:
        self.input_tokens += other.input_tokens
        self.output_tokens += other.output_tokens
        self.cached_input_tokens += other.cached_input_tokens
        self.cache_write_tokens += other.cache_write_tokens
        self.reasoning_tokens += other.reasoning_tokens
        self.total_tokens += other.total_tokens

    def _recorded(self) -> bool:
        return any(
            (
                self.input_tokens,
                self.output_tokens,
                self.cached_input_tokens,
                self.cache_write_tokens,
                self.reasoning_tokens,
                self.total_tokens,
            )
        )

    def _buckets(self) -> TokenBuckets:
        """SAM's four buckets. A report that breaks the nesting is clamped into it, as SAM-Go's TokensFromUsage clamps
        one: no bucket is negative, the cache tokens never exceed the input (writes first, then reads), and no token
        is counted twice."""
        prompt = max(self.input_tokens, 0)
        cache_write = min(max(self.cache_write_tokens, 0), prompt)
        cache_read = min(max(self.cached_input_tokens, 0), prompt - cache_write)
        return TokenBuckets(
            uncached_input=prompt - cache_write - cache_read,
            output=max(self.output_tokens, 0),
            cache_read=cache_read,
            cache_write=cache_write,
        )

    def _source_entry(self) -> dict[str, int]:
        """The call's counts as SAM-Go's token_usage entries hold them: ``cached_input_tokens`` holds the writes."""
        b = self._buckets()
        return {
            "input_tokens": b.input,
            "output_tokens": b.output,
            "cached_input_tokens": b.cached,
            "cache_write_tokens": b.cache_write,
            "reasoning_tokens": max(self.reasoning_tokens, 0),
        }


@dataclass
class UsageAccumulator:
    total: Usage = field(default_factory=Usage)
    by_model: dict[str, Usage] = field(default_factory=dict)
    last_input_tokens: int = 0
    llm_calls: int = 0
    _lock: threading.Lock = field(default_factory=threading.Lock, repr=False, compare=False)
    """Frameworks report usage from their own worker threads, and the counts are read-modify-write."""

    def record(self, model: str, usage: Usage) -> None:
        with self._lock:
            self.llm_calls += 1
            self.total._add(usage)
            self.by_model.setdefault(model, Usage())._add(usage)
            self.last_input_tokens = usage.input_tokens

    def _recorded(self) -> bool:
        return self.total._recorded()

    def _totals(self) -> dict:
        u = self.total
        entry = u._source_entry()
        return {
            "total_input_tokens": entry["input_tokens"],
            "total_output_tokens": entry["output_tokens"],
            "total_cached_input_tokens": entry["cached_input_tokens"],
            "total_cache_write_tokens": entry["cache_write_tokens"],
            "total_reasoning_tokens": entry["reasoning_tokens"],
            "total_tokens": max(entry["input_tokens"] + entry["output_tokens"], u.total_tokens),
            "by_source": {"agent": entry},
            "last_input_tokens": self.last_input_tokens,
        }

    def final_token_usage(self) -> dict | None:
        """The task's token totals, overall and per model, or None when nothing was recorded."""
        with self._lock:
            if not self._recorded():
                return None
            out = self._totals()
            out["by_model"] = {model: u._source_entry() for model, u in self.by_model.items()}
            return out
