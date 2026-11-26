# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""OpenTelemetry metrics under SAM-Go's names, units, attributes and buckets."""

from __future__ import annotations

import pytest
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import InMemoryMetricReader
from opentelemetry.sdk.metrics.view import ExplicitBucketHistogramAggregation, View

from sam_link_sdk import TaskResult, Usage
from sam_link_sdk.events import ModelFailed, ModelFinished, ModelStarted, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.metrics import HISTOGRAM_BUCKETS, OtelMetrics

from .agent_rig import start_agent


class Recorder:
    def __init__(self):
        self.counters: list[tuple[str, float, dict]] = []
        self.histograms: list[tuple[str, dict]] = []
        self.gauge = None

    def counter(self, name, value, attributes):
        self.counters.append((name, value, dict(attributes)))

    def histogram(self, name, seconds, attributes):
        assert seconds >= 0
        self.histograms.append((name, dict(attributes)))

    def active_executions(self, counts):
        self.gauge = counts


async def run(handler, metrics, task_id="T1"):
    agent, transport = await start_agent(handler, display_name="Claims Triage", metrics=metrics)
    await transport.deliver(task_id)
    await agent.stop()


async def model_and_tool_turn(ctx):
    await ctx.emit_event(ModelStarted("claude-sonnet-5"))
    await ctx.emit_event(
        ModelFinished(
            "claude-sonnet-5",
            "",
            (ToolCall("c1", "lookup_policy", {}),),
            Usage(input_tokens=120, output_tokens=30),
        )
    )
    await ctx.emit_event(ToolStarted("c1", "lookup_policy", {"policy_id": "P-1"}))
    await ctx.emit_event(ToolFinished("c1", "lookup_policy", {"tier": "gold"}))
    await ctx.emit_event(ToolStarted("c2", "route_claim", {}))
    await ctx.emit_event(ToolFinished("c2", "route_claim", {"error": "queue closed"}, failed=True))
    return "done"


async def test_a_task_records_tokens_model_time_tool_time_and_its_own_time():
    rec = Recorder()
    await run(model_and_tool_turn, rec)

    assert rec.counters == [
        (
            "gen_ai.tokens.used",
            120,
            {
                "gen_ai.request.model": "claude-sonnet-5",
                "gen_ai.token.type": "input",
                "component.name": "agent-Claims Triage",
            },
        ),
        (
            "gen_ai.tokens.used",
            30,
            {
                "gen_ai.request.model": "claude-sonnet-5",
                "gen_ai.token.type": "output",
                "component.name": "agent-Claims Triage",
            },
        ),
    ]
    assert rec.histograms == [
        ("gen_ai.client.operation.duration", {"gen_ai.request.model": "claude-sonnet-5", "error.type": ""}),
        (
            "operation.duration",
            {"type": "tool", "component.name": "lookup_policy", "operation.name": "execute", "error.type": ""},
        ),
        (
            "operation.duration",
            {"type": "tool", "component.name": "route_claim", "operation.name": "execute", "error.type": "error"},
        ),
        (
            "operation.duration",
            {"type": "agent", "component.name": "Claims Triage", "operation.name": "execute", "error.type": ""},
        ),
    ]
    assert rec.gauge() == {"Claims Triage": 0}


async def test_a_failed_model_call_is_recorded_with_its_category():
    rec = Recorder()

    class RateLimitError(Exception):
        pass

    RateLimitError.__module__ = "openai"

    async def handler(ctx):
        await ctx.emit_event(ModelStarted("claude-sonnet-5"))
        raise RateLimitError("429 rate limit")

    await run(handler, rec)
    assert rec.histograms == [
        ("gen_ai.client.operation.duration", {"gen_ai.request.model": "claude-sonnet-5", "error.type": "rate_limit"}),
        (
            "operation.duration",
            {
                "type": "agent",
                "component.name": "Claims Triage",
                "operation.name": "execute",
                "error.type": "rate_limit",
            },
        ),
    ]


async def test_a_task_answered_as_failed_is_an_error():
    rec = Recorder()
    await run(lambda ctx: TaskResult("no", state="failed"), rec)
    assert rec.histograms[-1][1]["error.type"] == "error"


async def test_names_units_and_buckets_match_sam_go_through_opentelemetry():
    """sam.agent.active_executions drops its series at zero running tasks, as SAM-Go's does."""
    reader = InMemoryMetricReader()
    views = [
        View(instrument_name=f"sam.{n}", aggregation=ExplicitBucketHistogramAggregation(list(b)))
        for n, b in HISTOGRAM_BUCKETS.items()
    ]
    provider = MeterProvider(metric_readers=[reader], views=views)
    await run(model_and_tool_turn, OtelMetrics(provider.get_meter("test")))

    metrics = {
        m.name: m for rm in reader.get_metrics_data().resource_metrics for sm in rm.scope_metrics for m in sm.metrics
    }
    assert set(metrics) == {"sam.gen_ai.tokens.used", "sam.gen_ai.client.operation.duration", "sam.operation.duration"}
    assert metrics["sam.gen_ai.tokens.used"].unit == "1" and metrics["sam.operation.duration"].unit == "s"
    tokens = {p.attributes["gen_ai.token.type"]: p.value for p in metrics["sam.gen_ai.tokens.used"].data.data_points}
    assert tokens == {"input": 120.0, "output": 30.0}
    llm = metrics["sam.gen_ai.client.operation.duration"].data.data_points[0]
    assert list(llm.explicit_bounds) == [0.5, 1.0, 2.0, 5.0, 10.0, 20.0, 30.0, 60.0, 120.0]
    ops = metrics["sam.operation.duration"].data.data_points
    assert {(p.attributes["type"], p.attributes["component.name"]) for p in ops} == {
        ("tool", "lookup_policy"),
        ("tool", "route_claim"),
        ("agent", "Claims Triage"),
    }
    assert list(ops[0].explicit_bounds) == list(HISTOGRAM_BUCKETS["operation.duration"])


async def test_active_executions_counts_running_tasks_by_display_name():
    reader = InMemoryMetricReader()
    seen = []

    async def handler(ctx):
        data = reader.get_metrics_data()
        seen.extend(
            (m.name, p.attributes, p.value)
            for rm in data.resource_metrics
            for sm in rm.scope_metrics
            for m in sm.metrics
            for p in m.data.data_points
        )
        return "ok"

    await run(handler, OtelMetrics(MeterProvider(metric_readers=[reader]).get_meter("test")))
    assert seen == [("sam.agent.active_executions", {"agent": "Claims Triage"}, 1)]


def test_without_opentelemetry_installed_the_error_says_what_to_install(monkeypatch):
    import builtins

    real_import = builtins.__import__

    def no_otel(name, *args, **kwargs):
        if name.startswith("opentelemetry"):
            raise ImportError(name)
        return real_import(name, *args, **kwargs)

    monkeypatch.setattr(builtins, "__import__", no_otel)
    with pytest.raises(ImportError, match=r"sam-link-sdk\[otel\]"):
        OtelMetrics()


async def test_a_failed_model_call_is_timed_with_its_error_type_and_the_retry_is_timed_on_its_own():
    recorder = Recorder()

    async def handler(ctx):
        await ctx.emit_event(ModelStarted("m"))
        await ctx.emit_event(ModelFailed("m", category="rate_limit", provider="openai"))
        await ctx.emit_event(ModelStarted("m"))
        await ctx.emit_event(ModelFinished("m", "ok"))
        return "ok"

    await run(handler, recorder)
    timed = [attrs["error.type"] for name, attrs in recorder.histograms if name == "gen_ai.client.operation.duration"]
    assert timed == ["rate_limit", ""]
