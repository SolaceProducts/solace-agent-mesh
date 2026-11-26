# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

from sam_link_sdk.usage import Usage, UsageAccumulator


def test_nothing_recorded():
    acc = UsageAccumulator()
    assert acc.final_token_usage() is None


def test_sums_across_calls_and_models():
    acc = UsageAccumulator()
    acc.record("gpt-4.1", Usage(input_tokens=812, output_tokens=41, total_tokens=853))
    acc.record("gpt-4.1", Usage(input_tokens=838, output_tokens=79, cached_input_tokens=500, reasoning_tokens=7))
    acc.record("mini", Usage(input_tokens=10, output_tokens=2, cache_write_tokens=3))
    out = acc.final_token_usage()
    assert out == {
        "total_input_tokens": 1660,
        "total_output_tokens": 122,
        "total_cached_input_tokens": 503,
        "total_cache_write_tokens": 3,
        "total_reasoning_tokens": 7,
        "total_tokens": 1782,
        "last_input_tokens": 10,
        "by_source": {
            "agent": {
                "input_tokens": 1660,
                "output_tokens": 122,
                "cached_input_tokens": 503,
                "cache_write_tokens": 3,
                "reasoning_tokens": 7,
            }
        },
        "by_model": {
            "gpt-4.1": {
                "input_tokens": 1650,
                "output_tokens": 120,
                "cached_input_tokens": 500,
                "cache_write_tokens": 0,
                "reasoning_tokens": 7,
            },
            "mini": {
                "input_tokens": 10,
                "output_tokens": 2,
                "cached_input_tokens": 3,
                "cache_write_tokens": 3,
                "reasoning_tokens": 0,
            },
        },
    }
    assert acc.llm_calls == 3


def test_total_tokens_prefers_provider_total():
    acc = UsageAccumulator()
    acc.record("m", Usage(total_tokens=99))
    assert acc.final_token_usage()["total_tokens"] == 99
