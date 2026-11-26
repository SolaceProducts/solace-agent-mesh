# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""OpenTelemetry metrics under SAM-Go's names, so SDK agents appear on the same dashboards as SAM agents.

A SAM-Go agent exports its own metrics from its own process (internal/observability); nothing in SAM derives them
from the messages it routes, so an SDK agent has to emit them itself. These are the agent-side ones, with SAM-Go's names,
units, attributes and histogram buckets:

- ``sam.gen_ai.tokens.used`` (counter, unit 1): ``gen_ai.request.model``, ``gen_ai.token.type`` (input/output),
  ``component.name`` = ``agent-<display name>``. SAM-Go's ``owner.id`` label is excluded by default, so it is not
  sent.
- ``sam.gen_ai.client.operation.duration`` (histogram, seconds): ``gen_ai.request.model``, ``error.type``.
- ``sam.operation.duration`` (histogram, seconds): ``type`` agent or tool, ``component.name`` (the agent's display
  name, or the tool name), ``operation.name`` = execute, ``error.type`` ("" on success).
- ``sam.agent.active_executions`` (gauge): running tasks, labelled ``agent``.

Labels carry names and categories only, never prompt or tool content.

    from sam_link_sdk.metrics import OtelMetrics, otlp_meter_provider
    otlp_meter_provider()                      # OTLP over HTTP; endpoint from OTEL_EXPORTER_OTLP_ENDPOINT
    AgentConfig(..., metrics=OtelMetrics())

Needs ``opentelemetry-sdk`` and ``opentelemetry-exporter-otlp-proto-http`` (the ``otel`` extra).
"""

from __future__ import annotations

from collections.abc import Callable, Mapping
from typing import Any, Protocol

__all__ = ["Metrics", "NoMetrics", "OtelMetrics", "otlp_meter_provider"]

TOKENS_USED = "gen_ai.tokens.used"
LLM_DURATION = "gen_ai.client.operation.duration"
OPERATION_DURATION = "operation.duration"
ACTIVE_EXECUTIONS = "agent.active_executions"

HISTOGRAM_BUCKETS = {
    LLM_DURATION: (0.5, 1.0, 2.0, 5.0, 10.0, 20.0, 30.0, 60.0, 120.0),
    OPERATION_DURATION: (0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0),
}
"""SAM-Go's DefaultDistributionMetrics (internal/observability/metrics.go), which match Python SAM's."""

_ERROR_TYPES = {
    "auth_failure": "auth_error",
    "rate_limit": "rate_limit",
    "timeout": "timeout",
    "context_overflow": "context_length_exceeded",
}


def llm_error_type(category: str) -> str:
    return _ERROR_TYPES.get(category, "error")


class Metrics(Protocol):
    def counter(self, name: str, value: float, attributes: Mapping[str, str]) -> None: ...

    def histogram(self, name: str, seconds: float, attributes: Mapping[str, str]) -> None: ...

    def active_executions(self, counts: Callable[[], Mapping[str, int]]) -> None: ...


class NoMetrics:
    def counter(self, name: str, value: float, attributes: Mapping[str, str]) -> None:
        pass

    def histogram(self, name: str, seconds: float, attributes: Mapping[str, str]) -> None:
        pass

    def active_executions(self, counts: Callable[[], Mapping[str, int]]) -> None:
        pass


def _otel_import_error() -> ImportError:
    return ImportError("sam_link_sdk.metrics needs OpenTelemetry: pip install 'sam-link-sdk[otel]'")


class OtelMetrics:
    """Records through an OpenTelemetry meter (the global meter provider's, unless one is given)."""

    def __init__(self, meter: Any = None, prefix: str = "sam"):
        if meter is None:
            try:
                from opentelemetry import metrics as otel_metrics
            except ImportError as exc:
                raise _otel_import_error() from exc
            meter = otel_metrics.get_meter("sam_link_sdk")
        self._meter = meter
        self._prefix = prefix
        self._counters: dict[str, Any] = {}
        self._histograms: dict[str, Any] = {}

    def _name(self, name: str) -> str:
        return f"{self._prefix}.{name}"

    def counter(self, name: str, value: float, attributes: Mapping[str, str]) -> None:
        instrument = self._counters.get(name)
        if instrument is None:
            instrument = self._counters[name] = self._meter.create_counter(
                self._name(name), unit="1", description=f"{name} counter"
            )
        instrument.add(float(value), dict(attributes))

    def histogram(self, name: str, seconds: float, attributes: Mapping[str, str]) -> None:
        instrument = self._histograms.get(name)
        if instrument is None:
            instrument = self._histograms[name] = self._meter.create_histogram(
                self._name(name), unit="s", description=f"{name} duration"
            )
        instrument.record(seconds, dict(attributes))

    def active_executions(self, counts: Callable[[], Mapping[str, int]]) -> None:
        from opentelemetry.metrics import Observation

        def observe(_options: Any) -> list:
            return [Observation(n, {"agent": name}) for name, n in counts().items() if n > 0]

        self._meter.create_observable_gauge(
            self._name(ACTIVE_EXECUTIONS), callbacks=[observe], unit="1", description=f"{ACTIVE_EXECUTIONS} gauge"
        )


def otlp_meter_provider(
    *,
    endpoint: str | None = None,
    export_interval_seconds: float = 60.0,
    headers: Mapping[str, str] | None = None,
    prefix: str = "sam",
    set_global: bool = True,
) -> Any:
    """A meter provider that exports over OTLP/HTTP with SAM-Go's histogram buckets. `endpoint` defaults to the
    standard OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_METRICS_ENDPOINT, and the resource to
    OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES, as for any OpenTelemetry SDK."""
    try:
        from opentelemetry import metrics as otel_metrics
        from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
        from opentelemetry.sdk.metrics import MeterProvider
        from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
        from opentelemetry.sdk.metrics.view import ExplicitBucketHistogramAggregation, View
    except ImportError as exc:
        raise _otel_import_error() from exc
    exporter = OTLPMetricExporter(endpoint=endpoint, headers=dict(headers) if headers else None)
    reader = PeriodicExportingMetricReader(exporter, export_interval_millis=int(export_interval_seconds * 1000))
    views = [
        View(instrument_name=f"{prefix}.{name}", aggregation=ExplicitBucketHistogramAggregation(list(buckets)))
        for name, buckets in HISTOGRAM_BUCKETS.items()
    ]
    provider = MeterProvider(metric_readers=[reader], views=views)
    if set_global:
        otel_metrics.set_meter_provider(provider)
    return provider
