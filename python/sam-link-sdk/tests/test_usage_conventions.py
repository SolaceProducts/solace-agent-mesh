# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Token usage counted the same way from every framework to SAM's ledger.

Each adapter turns its framework's usage report into ``Usage`` (whose input holds the cache reads and the cache writes,
which are disjoint); the agent link's usage object carries it, and SAM's entrypoint derives the ledger's four buckets
from it, the same buckets ``Usage.buckets`` names. One call here read 600 tokens from the cache, wrote 100 to it, took 300 more uncached input tokens and
produced 50 output tokens (7 of them reasoning): every framework's spelling of that call must come out as CALL.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from sam_link_sdk.link.v2 import usage_object
from sam_link_sdk.usage import TokenBuckets, Usage

CALL = Usage(
    input_tokens=1000,
    output_tokens=50,
    cached_input_tokens=600,
    cache_write_tokens=100,
    reasoning_tokens=7,
    total_tokens=1050,
)
BUCKETS = {"uncached_input": 300, "output": 50, "cache_read": 600, "cache_write": 100}


def accounting(usage: Usage) -> tuple:
    """What SAM accounts from a Usage (the provider's total is informational)."""
    return (
        usage.input_tokens,
        usage.output_tokens,
        usage.cached_input_tokens,
        usage.cache_write_tokens,
        usage.reasoning_tokens,
    )


def test_langchain_input_tokens_hold_the_cache_tokens():
    """langchain-openai files a priority-tier request's cache reads under priority_cache_read."""
    pytest.importorskip("langchain_core")
    from langchain_core.messages import AIMessage

    from sam_link_sdk.langchain.steps import _usage_from

    message = AIMessage(
        content="",
        usage_metadata={
            "input_tokens": 1000,
            "output_tokens": 50,
            "total_tokens": 1050,
            "input_token_details": {"cache_read": 600, "cache_creation": 100},
            "output_token_details": {"reasoning": 7},
        },
    )
    assert accounting(_usage_from(message)) == accounting(CALL)
    priority = AIMessage(
        content="",
        usage_metadata={
            "input_tokens": 1000,
            "output_tokens": 50,
            "total_tokens": 1050,
            "input_token_details": {"priority_cache_read": 600, "cache_creation": 100, "priority": 300},
            "output_token_details": {"reasoning": 7},
        },
    )
    assert accounting(_usage_from(priority)) == accounting(CALL)


def test_openai_agents_input_tokens_hold_the_cache_tokens():
    pytest.importorskip("agents")
    from agents.usage import InputTokensDetails, OutputTokensDetails, Usage as AgentsUsage

    from sam_link_sdk.openai_agents.steps import _usage_from

    usage = AgentsUsage(
        requests=1,
        input_tokens=1000,
        output_tokens=50,
        total_tokens=1050,
        input_tokens_details=InputTokensDetails(cached_tokens=600, cache_write_tokens=100),
        output_tokens_details=OutputTokensDetails(reasoning_tokens=7),
    )
    assert accounting(_usage_from(SimpleNamespace(usage=usage))) == accounting(CALL)


def test_adk_gemini_and_litellm_reports():
    """ADK reports usage differently through Gemini itself and through LiteLLM.

    With Gemini the prompt holds the cached content, and thinking tokens are counted apart from the candidates. Through
    LiteLLM `prompt_tokens` holds the reads and writes and `completion_tokens` the reasoning; ADK puts the writes on the
    response.
    """
    pytest.importorskip("google.adk")
    from google.genai.types import GenerateContentResponseUsageMetadata as Meta

    from sam_link_sdk.adk.steps import _usage_from

    gemini = Meta(
        prompt_token_count=1000,
        cached_content_token_count=600,
        candidates_token_count=43,
        thoughts_token_count=7,
        total_token_count=1050,
    )
    assert accounting(_usage_from(gemini)) == (1000, 50, 600, 0, 7)
    litellm = Meta(
        prompt_token_count=1000,
        cached_content_token_count=600,
        candidates_token_count=50,
        thoughts_token_count=7,
        total_token_count=1050,
    )
    assert accounting(_usage_from(litellm, 100)) == accounting(CALL)


def test_crewai_reports_of_each_provider():
    pytest.importorskip("crewai")
    from sam_link_sdk.crewai.steps import _usage

    anthropic = {
        "input_tokens": 1000,
        "output_tokens": 50,
        "total_tokens": 1050,
        "cached_prompt_tokens": 600,
        "cache_creation_tokens": 100,
        "reasoning_tokens": 7,
    }
    litellm = {
        "prompt_tokens": 1000,
        "completion_tokens": 50,
        "total_tokens": 1050,
        "cached_prompt_tokens": 600,
        "cache_creation_tokens": 100,
        "reasoning_tokens": 7,
    }
    gemini = {
        "prompt_token_count": 1000,
        "candidates_token_count": 43,
        "completion_tokens": 50,
        "total_token_count": 1050,
        "cached_prompt_tokens": 600,
        "reasoning_tokens": 7,
    }
    bedrock = {
        "inputTokens": 300,
        "outputTokens": 50,
        "totalTokens": 1050,
        "cacheReadInputTokens": 600,
        "cacheWriteInputTokens": 100,
    }
    assert accounting(_usage(anthropic)) == accounting(CALL)
    assert accounting(_usage(litellm)) == accounting(CALL)
    assert accounting(_usage(gemini)) == (1000, 50, 600, 0, 7)
    assert accounting(_usage(bedrock)) == (1000, 50, 600, 100, 0)


def test_strands_reports_of_each_model_provider():
    """Strands reports usage differently for each model provider.

    With its Anthropic and Bedrock models `inputTokens` leaves the cache out and `totalTokens` counts it. With its
    OpenAI and LiteLLM models `inputTokens` is `prompt_tokens`, which holds the cache.
    """
    pytest.importorskip("strands")
    from sam_link_sdk.strands.steps import _usage_from

    def message(usage):
        return {"metadata": {"usage": usage}}

    excluding = {
        "inputTokens": 300,
        "outputTokens": 50,
        "totalTokens": 1050,
        "cacheReadInputTokens": 600,
        "cacheWriteInputTokens": 100,
    }
    including = {
        "inputTokens": 1000,
        "outputTokens": 50,
        "totalTokens": 1050,
        "cacheReadInputTokens": 600,
        "cacheWriteInputTokens": 100,
    }
    assert accounting(_usage_from(message(excluding))) == (1000, 50, 600, 100, 0)
    assert accounting(_usage_from(message(including))) == (1000, 50, 600, 100, 0)


def test_agent_framework_reports_of_either_kind_of_client():
    pytest.importorskip("agent_framework")
    from sam_link_sdk.agent_framework.steps import _usage_from

    openai = {
        "input_token_count": 1000,
        "output_token_count": 50,
        "total_token_count": 1050,
        "cache_read_input_token_count": 600,
        "cache_creation_input_token_count": 100,
        "reasoning_output_token_count": 7,
    }
    excluding = {**openai, "input_token_count": 300}
    assert accounting(_usage_from(openai)) == accounting(CALL)
    assert accounting(_usage_from(excluding)) == accounting(CALL)


def test_hermes_reports_with_and_without_a_prompt_count():
    from sam_link_sdk.hermes.steps import _usage_from

    base = {"output_tokens": 50, "cache_read_tokens": 600, "cache_write_tokens": 100, "reasoning_tokens": 7}
    assert accounting(_usage_from({**base, "prompt_tokens": 1000, "input_tokens": 300})) == accounting(CALL)
    assert accounting(_usage_from({**base, "input_tokens": 300, "total_tokens": 1050})) == accounting(CALL)
    assert accounting(_usage_from({**base, "input_tokens": 1000, "total_tokens": 1050})) == accounting(CALL)


def test_opentelemetry_spans_with_and_without_a_total():
    from sam_link_sdk.otel_steps import _usage

    cache = {
        "gen_ai.usage.cache_read.input_tokens": 600,
        "gen_ai.usage.cache_creation.input_tokens": 100,
        "gen_ai.usage.output_tokens": 50,
        "gen_ai.usage.reasoning.output_tokens": 7,
    }
    assert accounting(_usage({**cache, "gen_ai.usage.input_tokens": 1000})) == accounting(CALL)
    assert accounting(
        _usage({**cache, "gen_ai.usage.input_tokens": 300, "gen_ai.usage.total_tokens": 1050})
    ) == accounting(CALL)
    assert accounting(
        _usage({**cache, "gen_ai.usage.input_tokens": 1000, "gen_ai.usage.total_tokens": 1050})
    ) == accounting(CALL)


def test_where_the_report_cannot_say_the_cache_tokens_are_never_counted_twice():
    """Where a report cannot say, the cache tokens are never counted twice.

    With no total and the cache fitting in the input, the cache is taken as part of the input (at worst under-counted,
    never doubled). With more cache tokens than input the input cannot hold them. Nothing negative comes out of a
    negative or unreadable count.
    """
    from sam_link_sdk._adapter import usage_of

    assert accounting(usage_of(input=1000, output=50, cache_read=600, cache_write=100, input_includes_cache=None)) == (
        1000,
        50,
        600,
        100,
        0,
    )
    assert usage_of(input=300, output=50, cache_read=600, input_includes_cache=None).input_tokens == 900
    assert accounting(usage_of(input=-5, output="x", cache_read=None)) == (0, 0, 0, 0, 0)


def sam_go_tokens_from_link_usage(body: dict) -> dict:
    """What SAM's entrypoint records from a link usage object: its llm.Usage (agentlink/events.go), split into the
    ledger's buckets by spend.TokensFromUsage (prompt holds cached, which holds the writes)."""
    prompt = max(body["input"], 0)
    cached = min(max(body["cachedInput"], 0), prompt)
    write = min(max(body.get("cacheWrite", 0), 0), cached)
    return {
        "uncached_input": prompt - cached,
        "output": max(body["output"], 0),
        "cache_read": cached - write,
        "cache_write": write,
    }


USAGES = [
    (CALL, BUCKETS),
    (
        Usage(input_tokens=900, output_tokens=10, cached_input_tokens=600),
        {"uncached_input": 300, "output": 10, "cache_read": 600, "cache_write": 0},
    ),
    (
        Usage(input_tokens=400, output_tokens=10, cache_write_tokens=400),
        {"uncached_input": 0, "output": 10, "cache_read": 0, "cache_write": 400},
    ),
    (
        Usage(input_tokens=500, output_tokens=10, cached_input_tokens=600, cache_write_tokens=100),
        {"uncached_input": 0, "output": 10, "cache_read": 400, "cache_write": 100},
    ),
]
"""A report that breaks the nesting: clamped the same way on both paths."""


@pytest.mark.parametrize("usage,buckets", USAGES)
def test_the_agent_link_reports_the_buckets_of_the_usage(usage, buckets):
    assert sam_go_tokens_from_link_usage(usage_object(usage)) == buckets


@pytest.mark.parametrize("usage,buckets", USAGES)
def test_the_usage_names_the_same_buckets_the_ledger_records(usage, buckets):
    b = usage._buckets()
    assert {
        "uncached_input": b.uncached_input,
        "output": b.output,
        "cache_read": b.cache_read,
        "cache_write": b.cache_write,
    } == buckets


def test_the_buckets_partition_the_call():
    b = CALL._buckets()
    assert b == TokenBuckets(uncached_input=300, output=50, cache_read=600, cache_write=100)
    assert (b.input, b.cached) == (1000, 700)
    assert b.uncached_input + b.cache_read + b.cache_write + b.output == CALL.input_tokens + CALL.output_tokens
