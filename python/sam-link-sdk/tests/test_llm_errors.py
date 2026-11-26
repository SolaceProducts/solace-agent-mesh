# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Model-failure categories in failed answers, using the provider libraries' real exception classes."""

from __future__ import annotations

import anthropic
import httpx
import openai
import pytest

from sam_link_sdk._model import TaskError
from sam_link_sdk.llm_errors import (
    AUTH_FAILURE,
    CONTENT_FILTERED,
    CONTEXT_OVERFLOW,
    RATE_LIMIT,
    TIMEOUT,
    UNKNOWN,
    LLMFailure,
    categorize,
    classify_error_type,
    classify_llm_failure,
    provider_label,
)

from .agent_rig import start_agent

REQ = httpx.Request("POST", "https://api.example.com/v1/chat")


def openai_error(cls, status, message="boom", body=None):
    return cls(message, response=httpx.Response(status, request=REQ), body=body)


@pytest.mark.parametrize(
    "status,message,code,expected",
    [
        (429, "slow down", "", RATE_LIMIT),
        (401, "nope", "", AUTH_FAILURE),
        (403, "nope", "", AUTH_FAILURE),
        (408, "x", "", TIMEOUT),
        (504, "x", "", TIMEOUT),
        (400, "maximum context length is 8192 tokens", "", CONTEXT_OVERFLOW),
        (400, "anything", "context_length_exceeded", CONTEXT_OVERFLOW),
        (400, "blocked by the content filter", "", CONTENT_FILTERED),
        (500, "Incorrect API key provided", "", AUTH_FAILURE),
        (500, "request timed out", "", TIMEOUT),
        (500, "Too many requests, retry later", "", RATE_LIMIT),
        (500, "something else", "", UNKNOWN),
        (None, "", "", UNKNOWN),
    ],
)
def test_categories_follow_the_go_rules(status, message, code, expected):
    assert categorize(status, message, code) == expected


def test_a_non_400_with_context_words_is_not_context_overflow():
    assert categorize(500, "token limit reached", "") == UNKNOWN


def test_real_provider_exceptions_are_classified_with_their_provider():
    assert classify_llm_failure(openai_error(openai.RateLimitError, 429)) == LLMFailure(RATE_LIMIT, "openai")
    assert classify_llm_failure(openai_error(openai.AuthenticationError, 401)) == LLMFailure(AUTH_FAILURE, "openai")
    assert classify_llm_failure(
        openai_error(openai.BadRequestError, 400, "This model's maximum context length is 8192 tokens")
    ) == LLMFailure(CONTEXT_OVERFLOW, "openai")
    assert classify_llm_failure(openai.APITimeoutError(request=REQ)) == LLMFailure(TIMEOUT, "openai")
    anthropic_error = anthropic.RateLimitError("slow", response=httpx.Response(429, request=REQ), body=None)
    assert classify_llm_failure(anthropic_error) == LLMFailure(RATE_LIMIT, "anthropic")


def test_a_gemini_rate_limit_is_a_rate_limit():
    from google.genai import errors

    quota = errors.ClientError(429, {"error": {"code": 429, "message": "quota", "status": "RESOURCE_EXHAUSTED"}})
    assert classify_llm_failure(quota) == LLMFailure(RATE_LIMIT, "gemini")


def test_a_gemini_error_is_classified_by_its_http_status_whatever_its_text_says():
    from google.genai import errors

    def failure(code):
        return classify_llm_failure(
            errors.ClientError(code, {"error": {"code": code, "message": "nope", "status": "FOO"}})
        )

    assert failure(429) == LLMFailure(RATE_LIMIT, "gemini")
    assert failure(403) == LLMFailure(AUTH_FAILURE, "gemini")
    assert failure(408) == LLMFailure(TIMEOUT, "gemini")
    assert failure(418) == LLMFailure(UNKNOWN, "gemini")


def test_a_gemini_resource_exhausted_status_is_a_rate_limit_even_when_the_http_code_says_otherwise():
    from google.genai import errors

    exhausted = errors.ClientError(
        400, {"error": {"code": 400, "message": "out of capacity", "status": "RESOURCE_EXHAUSTED"}}
    )
    assert classify_llm_failure(exhausted) == LLMFailure(RATE_LIMIT, "gemini")


def test_a_litellm_error_names_no_provider_of_its_own():
    import litellm

    error = litellm.RateLimitError("slow down", llm_provider="openai", model="gpt-x")
    assert classify_llm_failure(error) == LLMFailure(RATE_LIMIT, "")
    assert classify_llm_failure(error, "openai") == LLMFailure(RATE_LIMIT, "openai")


def test_a_client_timeout_class_is_a_timeout_whatever_its_message():
    class SlowReadTimeout(openai.APIConnectionError):
        pass

    SlowReadTimeout.__module__ = "openai"
    assert classify_llm_failure(SlowReadTimeout(message="", request=REQ)) == LLMFailure(TIMEOUT, "openai")


def test_a_wrapped_provider_error_is_found():
    try:
        try:
            raise openai_error(openai.RateLimitError, 429)
        except openai.RateLimitError as inner:
            raise RuntimeError("agent step failed") from inner
    except RuntimeError as outer:
        assert classify_llm_failure(outer) == LLMFailure(RATE_LIMIT, "openai")


def test_errors_that_are_not_from_a_provider_are_not_model_failures():
    assert classify_llm_failure(ValueError("tool crashed")) is None
    assert classify_llm_failure(KeyError("x")) is None


def test_only_googles_model_clients_count_as_gemini_and_adks_own_errors_do_not():
    from google.adk.errors.not_found_error import NotFoundError
    from google.genai import errors

    gemini = errors.ClientError(429, {"error": {"code": 429, "message": "quota", "status": "RESOURCE_EXHAUSTED"}})
    assert classify_llm_failure(gemini).provider == "gemini"
    try:
        try:
            raise ValueError("malformed policy id X-1")
        except ValueError as tool_error:
            raise NotFoundError("tool failed") from tool_error
    except NotFoundError as adk_error:
        assert classify_llm_failure(adk_error) is None


def test_an_explicit_provider_overrides_the_library_and_unknown_names_become_other():
    err = openai_error(openai.RateLimitError, 429)
    assert classify_llm_failure(err, "azure") == LLMFailure(RATE_LIMIT, "azure")
    assert classify_llm_failure(err, "my-internal-gateway") == LLMFailure(RATE_LIMIT, "other")
    assert provider_label("") == "" and provider_label(" OpenAI ") == "openai"


async def run_failing(handler):
    agent, transport = await start_agent(handler)
    await transport.deliver("T1")
    answer = transport.finished["T1"]
    await agent.stop()
    assert answer.state == "failed" and answer.text == ""
    return answer


async def test_the_error_reply_carries_category_and_provider_but_not_the_provider_text():
    async def handler(ctx):
        raise openai_error(
            openai.RateLimitError, 429, "Rate limit reached for gpt-x: your prompt text was 'SECRET PROMPT'"
        )

    answer = await run_failing(handler)

    assert answer.error == TaskError("rate_limit", "openai")
    assert "SECRET PROMPT" not in repr(answer)


async def test_a_tool_bug_is_not_reported_as_a_model_failure():
    async def handler(ctx):
        raise ValueError("tool crashed")

    answer = await run_failing(handler)
    assert answer.error == TaskError()


async def test_a_failure_reported_by_an_adapter_is_used():
    async def handler(ctx):
        ctx.record_llm_error(
            anthropic.AuthenticationError("bad key", response=httpx.Response(401, request=REQ), body=None)
        )
        raise RuntimeError("wrapped without a cause")

    answer = await run_failing(handler)
    assert answer.error == TaskError("auth_failure", "anthropic")


@pytest.mark.parametrize(
    "error_type,provider,expected",
    [
        ("rate_limit", "", LLMFailure(RATE_LIMIT, "")),
        ("context_overflow", "openai", LLMFailure(CONTEXT_OVERFLOW, "openai")),
        ("429", "anthropic", LLMFailure(RATE_LIMIT, "anthropic")),
        ("401", "", LLMFailure(AUTH_FAILURE, "")),
        ("504", "", LLMFailure(TIMEOUT, "")),
        ("openai.RateLimitError", "", LLMFailure(RATE_LIMIT, "openai")),
        ("openai.AuthenticationError", "", LLMFailure(AUTH_FAILURE, "openai")),
        ("openai.PermissionDeniedError", "", LLMFailure(AUTH_FAILURE, "openai")),
        ("openai.APITimeoutError", "", LLMFailure(TIMEOUT, "openai")),
        ("anthropic.RateLimitError", "azure", LLMFailure(RATE_LIMIT, "azure")),
        ("botocore.errorfactory.ThrottlingException", "", LLMFailure(RATE_LIMIT, "bedrock")),
        ("TimeoutError", "", LLMFailure(TIMEOUT, "")),
        ("openai.BadRequestError", "", LLMFailure(UNKNOWN, "openai")),
        ("_OTHER", "my-gateway", LLMFailure(UNKNOWN, "other")),
        ("", "", LLMFailure(UNKNOWN, "")),
    ],
)
def test_a_failure_described_by_a_string_is_classified(error_type, provider, expected):
    assert classify_error_type(error_type, provider) == expected


async def test_a_failure_an_adapter_reports_as_a_string_is_used_and_the_exception_form_still_works():
    async def handler(ctx):
        ctx.record_llm_error("openai.RateLimitError")
        raise RuntimeError("the framework's own wrapper, no cause")

    answer = await run_failing(handler)
    assert answer.error == TaskError("rate_limit", "openai")

    async def by_category(ctx):
        ctx.record_llm_error(
            anthropic.AuthenticationError("bad key", response=httpx.Response(401, request=REQ), body=None)
        )
        ctx.record_llm_error("timeout", "bedrock")
        raise RuntimeError("wrapped")

    answer = await run_failing(by_category)
    assert answer.error == TaskError("timeout", "bedrock")
