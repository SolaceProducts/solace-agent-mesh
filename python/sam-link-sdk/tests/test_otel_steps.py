# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The generic OpenTelemetry adapter: GenAI spans from an in-memory tracer provider become the mesh task's steps."""

from __future__ import annotations

import asyncio
import dataclasses
import json
import logging
import threading

import pytest

pytest.importorskip("opentelemetry.sdk")

from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.trace import Status, StatusCode

from sam_link_sdk.events import ModelFinished, ModelStarted, Progress, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.otel_steps import SamLinkSpanProcessor, run_agent, step_reporter

from .adapter_task import adapter_task, steps
from .otel_scripted import ScriptedSpans


def kinds(events):
    return [type(e) for e in events]


TURN = [ModelStarted, ModelFinished]
TOOL_TURN = [ModelStarted, ModelFinished, ToolStarted, ToolFinished]


def lookup_policy(policy_id):
    return {"policy_id": policy_id, "tier": "gold"}


def broken(policy_id):
    raise RuntimeError("backend down")


TOOLS = {"lookup_policy": lookup_policy, "broken": broken}


@pytest.fixture
def provider():
    provider = TracerProvider()
    step_reporter(provider)
    return provider


async def test_a_model_call_with_tool_calls_becomes_paired_steps_with_usage(provider):
    agent = ScriptedSpans(
        provider,
        [
            ("Looking it up.", [("call_a", "lookup_policy", {"policy_id": "P-1"})], (800, 40)),
            ("P-1 is gold.", [], (900, 20)),
        ],
        TOOLS,
    )
    async with adapter_task() as task:
        ctx = task.ctx
        assert await run_agent(ctx, agent.run) == "P-1 is gold."
        sent = steps(task)

    assert kinds(sent) == TOOL_TURN + TURN
    assert sent[0] == ModelStarted("fake-model")
    assert (sent[1].model, sent[1].text, sent[1].tool_calls) == (
        "fake-model",
        "Looking it up.",
        (ToolCall("call_a", "lookup_policy", {"policy_id": "P-1"}),),
    )
    u = sent[1].usage
    assert (u.input_tokens, u.output_tokens, u.cached_input_tokens, u.cache_write_tokens, u.reasoning_tokens) == (
        800,
        40,
        0,
        0,
        0,
    )
    assert sent[2] == ToolStarted("call_a", "lookup_policy", {"policy_id": "P-1"})
    assert sent[3].result == {"policy_id": "P-1", "tier": "gold"} and sent[3].call_id == "call_a"
    usage = ctx._run.usage.final_token_usage()
    assert (usage["total_input_tokens"], usage["total_output_tokens"], list(usage["by_model"])) == (
        1700,
        60,
        ["fake-model"],
    )


async def test_parallel_tool_calls_on_worker_threads_pair_by_call_id(provider):
    agent = ScriptedSpans(
        provider,
        [
            (
                "",
                [("call_a", "lookup_policy", {"policy_id": "P-1"}), ("call_b", "lookup_policy", {"policy_id": "P-2"})],
                (5, 1),
            ),
            ("Both gold.", [], (6, 1)),
        ],
        TOOLS,
    )
    async with adapter_task() as task:
        await run_agent(task.ctx, agent.run)
        sent = steps(task)
    starts = {e.call_id: e for e in sent if isinstance(e, ToolStarted)}
    results = {e.call_id: e.result for e in sent if isinstance(e, ToolFinished)}
    assert results == {"call_a": {"policy_id": "P-1", "tier": "gold"}, "call_b": {"policy_id": "P-2", "tier": "gold"}}
    assert {e.args["policy_id"] for e in starts.values()} == {"P-1", "P-2"}


async def test_a_failed_tool_span_is_a_failed_step_and_the_run_goes_on(provider):
    agent = ScriptedSpans(
        provider,
        [
            ("", [("call_x", "broken", {"policy_id": "P-9"})], (5, 1)),
            ("The backend is down.", [], (6, 1)),
        ],
        TOOLS,
    )
    async with adapter_task() as task:
        assert await run_agent(task.ctx, agent.run) == "The backend is down."
        sent = steps(task)
    [result] = [e for e in sent if isinstance(e, ToolFinished)]
    assert result == ToolFinished("call_x", "broken", {"error": "backend down"}, failed=True)


async def test_spans_of_runs_not_bound_to_a_mesh_task_are_ignored(provider):
    unbound = ScriptedSpans(provider, [("hi", [], (1, 1))], TOOLS)
    assert await unbound.run() == "hi"

    bound_started, unbound_done = asyncio.Event(), asyncio.Event()

    async def bound_run(text):
        bound_started.set()
        await unbound_done.wait()
        return await ScriptedSpans(provider, [("mine", [], (2, 1))], TOOLS).run()

    async def unbound_meanwhile():
        await bound_started.wait()
        await ScriptedSpans(provider, [("not mine", [], (3, 1))], TOOLS).run()
        unbound_done.set()

    async with adapter_task() as task:
        ctx = task.ctx
        await asyncio.gather(run_agent(ctx, bound_run), unbound_meanwhile())
        sent = steps(task)
    assert kinds(sent) == TURN
    assert (sent[1].text, sent[1].tool_calls) == ("mine", ())
    assert ctx._run.usage.final_token_usage()["total_input_tokens"] == 2


async def test_two_tasks_at_once_each_get_only_their_own_steps(provider):
    def turns(tag):
        return [
            ("", [(f"call_{tag}", "lookup_policy", {"policy_id": f"P-{tag}"})], (10, 1)),
            (f"done {tag}", [], (20, 1)),
        ]

    async with adapter_task("T1") as t1, adapter_task("T2") as t2:
        a1, a2 = await asyncio.gather(
            run_agent(t1.ctx, ScriptedSpans(provider, turns("1"), TOOLS).run),
            run_agent(t2.ctx, ScriptedSpans(provider, turns("2"), TOOLS).run),
        )
        assert (a1, a2) == ("done 1", "done 2")
        for task, tag in ((t1, "1"), (t2, "2")):
            sent = steps(task)
            assert len(sent) == 6
            assert {e.call_id for e in sent if isinstance(e, (ToolStarted, ToolFinished))} == {f"call_{tag}"}
            assert {c.id for e in sent if isinstance(e, ModelFinished) for c in e.tool_calls} == {f"call_{tag}"}
            assert [t for t, _ in task.transport.sent] == [f"T{tag}"] * 6


async def test_a_span_that_ends_on_another_thread_after_its_parent_reaches_the_task(provider):
    tracer = provider.get_tracer("t")

    async def work(text):
        with tracer.start_as_current_span("invoke_agent x", attributes={"gen_ai.operation.name": "invoke_agent"}):
            span = tracer.start_span(
                "chat m", attributes={"gen_ai.operation.name": "chat", "gen_ai.request.model": "m"}
            )

        def finish():
            span.set_attributes({"gen_ai.usage.input_tokens": 3, "gen_ai.usage.output_tokens": 1})
            span.end()

        thread = threading.Thread(target=finish)
        thread.start()
        await asyncio.to_thread(thread.join)

    async with adapter_task() as task:
        await run_agent(task.ctx, work)
        assert kinds(steps(task)) == TURN
    assert task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 3


async def test_without_content_on_the_spans_the_steps_carry_only_names_ids_and_usage(provider):
    agent = ScriptedSpans(
        provider,
        [
            ("Looking it up.", [("call_a", "lookup_policy", {"policy_id": "P-1"})], (8, 4)),
            ("P-1 is gold.", [], (9, 2)),
        ],
        TOOLS,
        content=False,
    )
    async with adapter_task() as task:
        await run_agent(task.ctx, agent.run)
        sent = steps(task)
    assert kinds(sent) == TOOL_TURN + TURN
    assert (sent[1].text, sent[1].tool_calls) == ("", ())
    assert sent[2].args is None and sent[2].call_id == "call_a"
    assert sent[3].result is None
    assert task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 17


async def test_content_is_redacted_and_capped_like_the_other_adapters_and_never_logged(provider, caplog):
    secret, long_note = "hunter2-secret-value", "x" * 40_000
    tools = {"login": lambda username, password: {"note": long_note, "token": "tok_live_abcdefghijklmnop"}}
    agent = ScriptedSpans(
        provider,
        [
            ("", [("call_l", "login", {"username": "alice", "password": secret})], (5, 1)),
            ("Logged in.", [], (6, 1)),
        ],
        tools,
    )
    async with adapter_task() as task:
        with caplog.at_level(logging.DEBUG):
            await run_agent(task.ctx, agent.run)
        sent = steps(task)
    [start] = [e for e in sent if isinstance(e, ToolStarted)]
    assert start.args == {"username": "alice", "password": "<REDACTED>"}
    [call] = sent[1].tool_calls
    assert call.args["password"] == "<REDACTED>"
    [result] = [e.result for e in sent if isinstance(e, ToolFinished)]
    assert result["token"] == "<REDACTED>"
    assert len(result["note"]) < 17_000 and "truncated" in result["note"]
    assert secret not in caplog.text and "alice" not in caplog.text and long_note[:100] not in caplog.text


async def test_a_second_instrumentation_of_the_same_model_call_is_not_counted_twice(provider):
    tracer = provider.get_tracer("t")

    async def work(text):
        attributes = {"gen_ai.operation.name": "chat", "gen_ai.request.model": "m"}
        with tracer.start_as_current_span("chat m", attributes=attributes) as outer:
            with tracer.start_as_current_span("chat m", attributes=attributes) as client_library_span:
                client_library_span.set_attributes({"gen_ai.usage.input_tokens": 7, "gen_ai.usage.output_tokens": 1})
            outer.set_attributes({"gen_ai.usage.input_tokens": 7, "gen_ai.usage.output_tokens": 1})

    async with adapter_task() as task:
        await run_agent(task.ctx, work)
        assert kinds(steps(task)) == TURN
    assert task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 7


async def test_a_failed_model_call_is_announced_and_not_answered(provider):
    tracer = provider.get_tracer("t")

    async def work(text):
        with tracer.start_as_current_span(
            "chat m",
            attributes={"gen_ai.operation.name": "chat", "gen_ai.request.model": "m"},
            record_exception=False,
            set_status_on_exception=False,
        ) as span:
            span.set_status(Status(StatusCode.ERROR, "rate limited"))

    async with adapter_task() as task:
        await run_agent(task.ctx, work)
        assert steps(task) == [ModelStarted("m")]
    assert task.ctx._run.llm_failure is None


@pytest.mark.parametrize(
    "attributes,exception,expected",
    [
        ({"error.type": "openai.RateLimitError"}, None, ("rate_limit", "openai")),
        ({"error.type": "429", "gen_ai.provider.name": "aws.bedrock"}, None, ("rate_limit", "bedrock")),
        ({"error.type": "timeout", "gen_ai.system": "anthropic"}, None, ("timeout", "anthropic")),
        ({"gen_ai.system": "openai"}, "anthropic.AuthenticationError", ("auth_failure", "openai")),
        ({"error.type": "_OTHER", "gen_ai.provider.name": "openai"}, None, None),
    ],
)
async def test_a_failed_model_call_s_category_reaches_the_task_s_error_reply(provider, attributes, exception, expected):
    tracer = provider.get_tracer("t")

    async def work(text):
        with tracer.start_as_current_span(
            "chat m",
            attributes={"gen_ai.operation.name": "chat", "gen_ai.request.model": "m", **attributes},
            record_exception=False,
            set_status_on_exception=False,
        ) as span:
            if exception:
                span.add_event("exception", {"exception.type": exception, "exception.message": "SECRET PROMPT"})
            span.set_status(Status(StatusCode.ERROR, "failed"))

    async with adapter_task() as task:
        ctx = task.ctx
        await run_agent(ctx, work)
        assert steps(task) == [ModelStarted("m")]
    got = (ctx._run.llm_failure.category, ctx._run.llm_failure.provider) if ctx._run.llm_failure is not None else None
    assert got == expected


async def test_older_attribute_names_and_strands_default_events_are_read(provider):
    """No total on the span, and 6 cache tokens fit in the 11 input tokens: they are taken as part of them, 4 reads and 2
    writes.
    """
    tracer = provider.get_tracer("t")

    async def work(text):
        with tracer.start_as_current_span("chat", attributes={"gen_ai.operation.name": "chat"}) as span:
            span.add_event(
                "gen_ai.choice",
                {
                    "finish_reason": "tool_use",
                    "message": json.dumps(
                        [
                            {"text": "Checking."},
                            {"toolUse": {"toolUseId": "tu_1", "name": "lookup_policy", "input": {"policy_id": "P-1"}}},
                        ]
                    ),
                },
            )
            span.set_attributes(
                {
                    "gen_ai.request.model": "us.anthropic.claude",
                    "gen_ai.usage.prompt_tokens": 11,
                    "gen_ai.usage.completion_tokens": 3,
                    "gen_ai.usage.cache_read_input_tokens": 4,
                    "gen_ai.usage.cache_write_input_tokens": 2,
                }
            )
        with tracer.start_as_current_span(
            "execute_tool lookup_policy",
            attributes={
                "gen_ai.operation.name": "execute_tool",
                "gen_ai.tool.name": "lookup_policy",
                "gen_ai.tool.call.id": "tu_1",
            },
        ) as span:
            span.add_event(
                "gen_ai.tool.message", {"role": "tool", "content": json.dumps({"policy_id": "P-1"}), "id": "tu_1"}
            )
            span.add_event("gen_ai.choice", {"message": json.dumps([{"text": '{"tier": "gold"}'}]), "id": "tu_1"})
        with tracer.start_as_current_span(
            "chat legacy-model",
            attributes={
                "gen_ai.request.model": "legacy-model",
                "gen_ai.completion.0.content": "Gold.",
                "gen_ai.usage.input_tokens": 12,
                "gen_ai.usage.output_tokens": 2,
            },
        ):
            pass

    async with adapter_task() as task:
        ctx = task.ctx
        await run_agent(ctx, work)
        sent = steps(task)
    assert kinds(sent) == TOOL_TURN + TURN
    assert sent[0] == ModelStarted("us.anthropic.claude")
    assert sent[1].text == "Checking." and [c.id for c in sent[1].tool_calls] == ["tu_1"]
    assert (sent[1].usage.input_tokens, sent[1].usage.cached_input_tokens, sent[1].usage.cache_write_tokens) == (
        11,
        4,
        2,
    )
    assert sent[2].args == {"policy_id": "P-1"} and sent[3].result == {"tier": "gold"}
    assert (sent[5].text, sent[5].tool_calls) == ("Gold.", ())
    usage = ctx._run.usage.final_token_usage()
    assert (usage["total_input_tokens"], usage["total_cache_write_tokens"]) == (23, 2)


async def test_instrument_adds_one_processor_per_provider():
    provider = TracerProvider()
    first = step_reporter(provider)
    assert isinstance(first, SamLinkSpanProcessor) and step_reporter(provider) is first


async def test_spans_that_end_after_the_run_are_not_reported(provider):
    tracer = provider.get_tracer("t")
    left_open = []

    async def work(text):
        left_open.append(
            tracer.start_span("chat m", attributes={"gen_ai.operation.name": "chat", "gen_ai.request.model": "m"})
        )

    async with adapter_task() as task:
        await run_agent(task.ctx, work)
        left_open[0].end()
        await asyncio.sleep(0.01)
        assert steps(task) == [ModelStarted("m")]


def _tool_step(event) -> bool:
    return isinstance(event, (ToolStarted, ToolFinished))


def _in_call_order(events):
    """Strands runs the two tools concurrently: compare each call's steps, in model-call order."""
    tools = sorted((e for e in events if _tool_step(e)), key=lambda e: (e.call_id, isinstance(e, ToolFinished)))
    return [e for e in events if not _tool_step(e)], tools


@pytest.fixture(params=["default_conventions", "latest_conventions"])
def strands_spans(request, monkeypatch, provider):
    """Strands' own tracer, writing into the test's in-memory provider."""
    tracer_module = pytest.importorskip("strands.telemetry.tracer")
    if request.param == "latest_conventions":
        monkeypatch.setenv("OTEL_SEMCONV_STABILITY_OPT_IN", "gen_ai_latest_experimental")
    else:
        monkeypatch.delenv("OTEL_SEMCONV_STABILITY_OPT_IN", raising=False)
    tracer = tracer_module.Tracer()
    tracer.tracer_provider, tracer.tracer = provider, provider.get_tracer("strands.telemetry.tracer")
    monkeypatch.setattr(tracer_module, "_tracer_instance", tracer)
    return request.param


async def test_strands_own_spans_give_the_same_steps_as_the_dedicated_strands_adapter(strands_spans):
    from strands import Agent

    from sam_link_sdk.strands import run_agent as run_strands

    from .test_strands import ScriptedModel, broken, call, lookup_policy, text, usage

    def script():
        return [
            (
                [
                    text("Checking."),
                    call("call_a", "lookup_policy", {"policy_id": "P-1"}),
                    call("call_b", "broken", {"policy_id": "P-2"}),
                ],
                usage(800, 40, cached=100),
            ),
            ([text("P-1 is gold; P-2 failed.")], usage(900, 20)),
        ]

    class ConfiguredModel(ScriptedModel):
        def __init__(self, turns):
            super().__init__(turns)
            self.config = {"model_id": self.model_id}

    def agent(model):
        return Agent(model=model, tools=[lookup_policy, broken], callback_handler=None)

    async with adapter_task("T1") as spans_task, adapter_task("T2") as dedicated_task:
        via_spans, dedicated = spans_task.ctx, dedicated_task.ctx
        assert await run_agent(via_spans, agent(ConfiguredModel(script())).invoke_async)
        await run_strands(dedicated, lambda c: agent(ConfiguredModel(script())))
        span_steps, dedicated_steps = steps(spans_task), steps(dedicated_task)

    assert span_steps and _in_call_order(span_steps) == _in_call_order(dedicated_steps)
    assert via_spans._run.usage.final_token_usage() == dedicated._run.usage.final_token_usage()


@pytest.fixture
def agent_framework_spans(monkeypatch, provider):
    """Agent Framework's own telemetry, with sensitive data on, writing into the test's in-memory provider."""
    observability = pytest.importorskip("agent_framework.observability")
    settings = observability.OBSERVABILITY_SETTINGS
    monkeypatch.setattr(settings, "_enable_instrumentation", True)
    monkeypatch.setattr(settings, "_enable_sensitive_data", True)
    monkeypatch.setattr(observability, "get_tracer", lambda *a, **k: provider.get_tracer("agent_framework"))


def _as_sam_counts(events):
    """The steps with each model call's tokens as SAM counts them: the provider's own total is not one of them, and
    only the dedicated adapter sees it."""
    return [
        dataclasses.replace(e, usage=(e.usage._buckets(), e.usage.reasoning_tokens))
        if isinstance(e, ModelFinished) and e.usage is not None
        else e
        for e in events
    ]


async def test_agent_framework_own_spans_give_the_same_steps_as_the_dedicated_adapter(agent_framework_spans):
    from agent_framework import (
        Agent,
        BaseChatClient,
        ChatMiddlewareLayer,
        ChatResponse,
        ChatResponseUpdate,
        Content,
        FunctionInvocationLayer,
        Message,
    )
    from agent_framework.observability import ChatTelemetryLayer

    from sam_link_sdk.agent_framework import run_agent as run_af

    from .test_agent_framework import broken, call, lookup_policy, text, usage

    class TracedClient(FunctionInvocationLayer, ChatMiddlewareLayer, ChatTelemetryLayer, BaseChatClient):
        """test_agent_framework's ScriptedClient with the layers in the order the framework's own clients use
        (OpenAIChatClient): the telemetry layer innermost, one chat span per model call."""

        def __init__(self, turns, model="fake-model"):
            super().__init__()
            self.turns, self.model = list(turns), model

        def _inner_get_response(self, *, messages, stream, options, **kwargs):
            contents, u = self.turns.pop(0)

            async def respond():
                return ChatResponse(
                    messages=[Message(role="assistant", contents=contents)], model=self.model, usage_details=u
                )

            if stream:

                async def updates():
                    yield ChatResponseUpdate(
                        contents=[*contents, Content.from_usage(u)], role="assistant", model=self.model
                    )

                return self._build_response_stream(updates())
            return respond()

    def script():
        return [
            (
                [
                    text("Checking."),
                    call("call_a", "lookup_policy", {"policy_id": "P-1"}),
                    call("call_b", "broken", {"policy_id": "P-2"}),
                ],
                usage(800, 40, cached=100),
            ),
            ([text("P-1 is gold; P-2 failed.")], usage(900, 20)),
        ]

    def agent():
        return Agent(TracedClient(script()), "You triage claims.", name="triage", tools=[lookup_policy, broken])

    async with adapter_task("T1") as spans_task, adapter_task("T2") as dedicated_task:
        via_spans, dedicated = spans_task.ctx, dedicated_task.ctx
        triage = agent()
        assert await run_agent(via_spans, triage.run)
        await run_af(dedicated, agent())
        span_steps = _in_call_order(steps(spans_task))
        dedicated_steps = _in_call_order(steps(dedicated_task))

    assert span_steps[0] and _as_sam_counts(span_steps[0]) == _as_sam_counts(dedicated_steps[0])
    span_tools, dedicated_tools = span_steps[1], dedicated_steps[1]

    def shape(events):
        return [(type(e), e.call_id, e.args if isinstance(e, ToolStarted) else None) for e in events]

    assert shape(span_tools) == shape(dedicated_tools)
    span_results = {e.call_id: e.result for e in span_tools if isinstance(e, ToolFinished)}
    dedicated_results = {e.call_id: e.result for e in dedicated_tools if isinstance(e, ToolFinished)}
    assert span_results["call_a"] == dedicated_results["call_a"] == {"policy_id": "P-1", "tier": "gold"}
    assert set(span_results["call_b"]) == set(dedicated_results["call_b"]) == {"error"}
    assert via_spans._run.usage.final_token_usage() == dedicated._run.usage.final_token_usage()


async def test_the_otel_example_reports_a_strands_run_through_its_own_spans(strands_spans):
    """examples/claims_triage_otel.py: no dedicated adapter, only Strands' spans and run_agent."""
    from .conftest import load_example
    from .test_strands import ScriptedModel, call, text, usage

    example = load_example("claims_triage_otel")

    class ConfiguredModel(ScriptedModel):
        def __init__(self, turns):
            super().__init__(turns)
            self.config = {"model_id": self.model_id}

    model = ConfiguredModel(
        [
            ([call("c1", "lookup_policy", {"policy_id": "P-1182"})], usage(300, 20)),
            ([call("c2", "route_claim", {"policy_id": "P-1182", "queue": "fast-track"})], usage(340, 25)),
            ([text("P-1182 is routed to fast-track.")], usage(380, 15)),
        ]
    )
    async with adapter_task() as task:
        ctx = task.ctx
        assert await example.make_handler(model)(ctx) == "P-1182 is routed to fast-track."
        sent = steps(task)
    assert kinds(sent) == [Progress] + TOOL_TURN * 2 + TURN
    assert [e.result.get("tier") or e.result.get("queue") for e in sent if isinstance(e, ToolFinished)] == [
        "gold",
        "fast-track",
    ]
    assert ctx._run.usage.final_token_usage()["total_input_tokens"] == 1020


def test_step_reporter_refuses_a_global_provider_it_cannot_add_to(monkeypatch):
    from opentelemetry import trace

    class VendorProvider(trace.TracerProvider):
        def get_tracer(self, *args, **kwargs):
            return trace.NoOpTracer()

    vendor = VendorProvider()
    monkeypatch.setattr(trace, "get_tracer_provider", lambda: vendor)
    monkeypatch.setattr(trace, "set_tracer_provider", lambda provider: None)
    with pytest.raises(RuntimeError, match="pass the provider your framework traces with"):
        step_reporter()


async def test_run_agent_runs_on_the_task_s_text_or_the_input_given(provider):
    seen = []

    async def run(text):
        seen.append(text)
        return len(text)

    async with adapter_task() as task:
        ctx = task.ctx
        assert await run_agent(ctx, run) == len(ctx.text)
        assert await run_agent(ctx, lambda text: seen.append(text) or "sync", input="Triage P-9") == "sync"
    assert seen == [ctx.text, "Triage P-9"]


async def test_run_agent_warns_that_tool_scopes_are_not_enforced_when_the_agent_declares_them(provider, caplog):
    async with (
        adapter_task("T1") as plain,
        adapter_task("T2", scopes={"route_claim": ["tool:claims:route"]}, granted=[]) as scoped,
    ):
        with caplog.at_level(logging.WARNING, logger="sam_link_sdk.otel_steps"):
            await run_agent(plain.ctx, lambda text: "ok")
            assert caplog.records == []
            await run_agent(scoped.ctx, lambda text: "ok")
    [record] = caplog.records
    assert record.levelno == logging.WARNING and "not enforced" in record.getMessage()
    assert "route_claim" in record.getMessage()


@pytest.mark.parametrize(
    "attribute,expected",
    [
        ('{"tier": "gold"}', {"tier": "gold"}),
        ("[1, 2]", [1, 2]),
        ('"fast-track"', "fast-track"),
        ('"{\\"tier\\": \\"gold\\"}"', {"tier": "gold"}),
        ("fast-track", "fast-track"),
        ('"unterminated', '"unterminated'),
        (3, 3),
    ],
)
def test_a_span_attribute_is_read_as_the_adapters_read_a_tool_s_output(attribute, expected):
    from sam_link_sdk.otel_steps import _json

    assert _json(attribute) == expected
