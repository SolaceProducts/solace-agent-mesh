# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""``otlp_meter_provider()`` exports over OTLP/HTTP to a real local receiver: the standard environment settings, SAM-Go's
buckets, no ``owner.id``, and nothing at all when OTEL_SDK_DISABLED is set."""

from __future__ import annotations

import gzip
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest

pytest.importorskip("opentelemetry.exporter.otlp.proto.http.metric_exporter")

from opentelemetry.proto.collector.metrics.v1.metrics_service_pb2 import ExportMetricsServiceRequest

from sam_link_sdk import Usage
from sam_link_sdk.events import ModelFinished, ModelStarted, ToolFinished, ToolStarted
from sam_link_sdk.metrics import HISTOGRAM_BUCKETS, OtelMetrics, otlp_meter_provider

from .agent_rig import start_agent


class Receiver:
    def __init__(self):
        self.requests: list[tuple[str, ExportMetricsServiceRequest]] = []
        receiver = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                body = self.rfile.read(int(self.headers["Content-Length"]))
                if self.headers.get("Content-Encoding") == "gzip":
                    body = gzip.decompress(body)
                request = ExportMetricsServiceRequest()
                request.ParseFromString(body)
                receiver.requests.append((self.path, request))
                self.send_response(200)
                self.send_header("Content-Type", "application/x-protobuf")
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, *args):
                pass

        self.server = HTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_port}"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *exc):
        self.server.shutdown()
        self.server.server_close()

    def metrics(self) -> dict:
        return {
            m.name: m
            for _, request in self.requests
            for rm in request.resource_metrics
            for sm in rm.scope_metrics
            for m in sm.metrics
        }


def attributes(kv) -> dict[str, str]:
    return {item.key: item.value.string_value for item in kv}


async def one_task(provider) -> None:
    async def handler(ctx):
        await ctx.emit_event(ModelStarted("claude-sonnet-5"))
        await ctx.emit_event(ModelFinished("claude-sonnet-5", "", (), Usage(input_tokens=120, output_tokens=30)))
        await ctx.emit_event(ToolStarted("c1", "lookup_policy", {}))
        await ctx.emit_event(ToolFinished("c1", "lookup_policy", {"tier": "gold"}))
        return "done"

    agent, transport = await start_agent(
        handler, display_name="Claims Triage", metrics=OtelMetrics(provider.get_meter("sam_link_sdk"))
    )
    await transport.deliver("T1")
    await agent.stop()
    provider.force_flush()
    provider.shutdown()


@pytest.fixture
def otlp_env(monkeypatch):
    for name in (
        "OTEL_EXPORTER_OTLP_ENDPOINT",
        "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
        "OTEL_SDK_DISABLED",
        "OTEL_EXPORTER_OTLP_PROTOCOL",
        "OTEL_EXPORTER_OTLP_COMPRESSION",
    ):
        monkeypatch.delenv(name, raising=False)
    monkeypatch.setenv("OTEL_SERVICE_NAME", "claims-triage")
    monkeypatch.setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=staging,team=claims")
    return monkeypatch


async def test_a_task_is_exported_over_otlp_http_with_the_environments_endpoint_and_resource(otlp_env):
    with Receiver() as receiver:
        otlp_env.setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.url)
        await one_task(otlp_meter_provider(set_global=False))

    assert {path for path, _ in receiver.requests} == {"/v1/metrics"}
    resource = attributes(receiver.requests[0][1].resource_metrics[0].resource.attributes)
    assert resource["service.name"] == "claims-triage"
    assert resource["deployment.environment"] == "staging" and resource["team"] == "claims"
    assert {"sam.gen_ai.tokens.used", "sam.gen_ai.client.operation.duration", "sam.operation.duration"} <= set(
        receiver.metrics()
    )


async def test_exported_histograms_have_sam_gos_buckets_and_no_series_carries_an_owner_id(otlp_env):
    with Receiver() as receiver:
        otlp_env.setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.url)
        await one_task(otlp_meter_provider(set_global=False))

    metrics = receiver.metrics()
    llm = metrics["sam.gen_ai.client.operation.duration"].histogram.data_points[0]
    assert list(llm.explicit_bounds) == list(HISTOGRAM_BUCKETS["gen_ai.client.operation.duration"])
    operation = metrics["sam.operation.duration"].histogram.data_points[0]
    assert list(operation.explicit_bounds) == list(HISTOGRAM_BUCKETS["operation.duration"])
    points = [*metrics["sam.gen_ai.tokens.used"].sum.data_points, llm, operation]
    assert points and all("owner.id" not in attributes(p.attributes) for p in points)


async def test_otel_sdk_disabled_turns_the_exported_metrics_off(otlp_env):
    with Receiver() as receiver:
        otlp_env.setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.url)
        otlp_env.setenv("OTEL_SDK_DISABLED", "true")
        await one_task(otlp_meter_provider(set_global=False))

    assert receiver.requests == []
