// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/credentials"
	grpcEncoding "google.golang.org/grpc/encoding"
	_ "google.golang.org/grpc/encoding/gzip"
)

const MetricExportInterval = 60 * time.Second

func CreateOTLPMetricExporter(ctx context.Context, cfg ExporterConfig, logger *slog.Logger) (metric.Exporter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	switch cfg.Protocol {
	case ProtocolHTTP:
		return createOTLPHTTPMetric(ctx, cfg, logger)
	case ProtocolGRPC:
		return createOTLPGRPCMetric(ctx, cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", cfg.Protocol)
	}
}

func createOTLPHTTPMetric(ctx context.Context, cfg ExporterConfig, logger *slog.Logger) (metric.Exporter, error) {
	endpoint := buildMetricsEndpoint(cfg.Endpoint)

	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(endpoint),
		otlpmetrichttp.WithTimeout(time.Duration(cfg.Timeout * float64(time.Second))),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(cfg.Headers))
	}
	if comp := mapHTTPCompression(cfg.Compression, logger); comp != otlpmetrichttp.NoCompression {
		opts = append(opts, otlpmetrichttp.WithCompression(comp))
	}
	if requiresDeltaTemporality(endpoint) {
		opts = append(opts, otlpmetrichttp.WithTemporalitySelector(deltaTemporalitySelector))
	}

	exp, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP HTTP metric exporter: %w", err)
	}
	logger.Info("created OTLP HTTP metric exporter",
		"endpoint", endpoint,
		"compression", cfg.Compression,
	)
	return exp, nil
}

func createOTLPGRPCMetric(ctx context.Context, cfg ExporterConfig, logger *slog.Logger) (metric.Exporter, error) {
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(stripScheme(cfg.Endpoint)),
		otlpmetricgrpc.WithTimeout(time.Duration(cfg.Timeout * float64(time.Second))),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(cfg.Headers))
	}
	if cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	} else if cfg.CertificateFile != "" {
		creds, err := credentials.NewClientTLSFromFile(cfg.CertificateFile, "")
		if err != nil {
			return nil, fmt.Errorf("load certificate %q: %w", cfg.CertificateFile, err)
		}
		opts = append(opts, otlpmetricgrpc.WithTLSCredentials(creds))
	}
	if comp := mapGRPCCompression(cfg.Compression, logger); comp != "" {
		opts = append(opts, otlpmetricgrpc.WithCompressor(comp))
	}
	if requiresDeltaTemporality(cfg.Endpoint) {
		opts = append(opts, otlpmetricgrpc.WithTemporalitySelector(deltaTemporalitySelector))
	}

	exp, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP gRPC metric exporter: %w", err)
	}
	logger.Info("created OTLP gRPC metric exporter",
		"endpoint", cfg.Endpoint,
		"compression", cfg.Compression,
		"insecure", cfg.Insecure,
	)
	return exp, nil
}

func NewPeriodicReader(exp metric.Exporter) metric.Reader {
	return metric.NewPeriodicReader(exp, metric.WithInterval(MetricExportInterval))
}

func buildMetricsEndpoint(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1/metrics") {
		return base
	}
	if strings.Contains(base, "/otlp/v1/metrics") || strings.Contains(base, "/api/v2/otlp/v1/metrics") {
		return base
	}
	return base + "/v1/metrics"
}

func requiresDeltaTemporality(endpoint string) bool {
	endpoint = strings.ToLower(endpoint)
	for _, host := range []string{"datadoghq.com", "datadoghq.eu"} {
		if strings.Contains(endpoint, host) {
			return true
		}
	}
	return false
}

func deltaTemporalitySelector(kind metric.InstrumentKind) metricdata.Temporality {
	switch kind {
	case metric.InstrumentKindCounter,
		metric.InstrumentKindHistogram,
		metric.InstrumentKindObservableCounter:
		return metricdata.DeltaTemporality
	case metric.InstrumentKindUpDownCounter,
		metric.InstrumentKindObservableUpDownCounter,
		metric.InstrumentKindObservableGauge,
		metric.InstrumentKindGauge:
		return metricdata.CumulativeTemporality
	default:
		return metricdata.CumulativeTemporality
	}
}

func mapHTTPCompression(s string, logger *slog.Logger) otlpmetrichttp.Compression {
	switch s {
	case CompressionNone, "":
		return otlpmetrichttp.NoCompression
	case CompressionGzip:
		return otlpmetrichttp.GzipCompression
	case CompressionDeflate:
		logger.Warn("HTTP exporter: deflate compression is not supported by the OTLP HTTP exporter, falling back to none")
		return otlpmetrichttp.NoCompression
	default:
		logger.Warn("unknown compression value, falling back to none", "value", s)
		return otlpmetrichttp.NoCompression
	}
}

func mapGRPCCompression(s string, logger *slog.Logger) string {
	switch s {
	case CompressionNone, "":
		return ""
	case CompressionGzip:
		if grpcEncoding.GetCompressor("gzip") == nil {
			logger.Warn("gRPC gzip codec not registered, falling back to none")
			return ""
		}
		return "gzip"
	case CompressionDeflate:
		logger.Warn("gRPC does not support deflate compression, falling back to none")
		return ""
	default:
		logger.Warn("unknown compression value, falling back to none", "value", s)
		return ""
	}
}

func stripScheme(endpoint string) string {
	for _, p := range []string{"https://", "http://"} {
		if rest, ok := strings.CutPrefix(endpoint, p); ok {
			endpoint = rest
			break
		}
	}
	return strings.TrimRight(endpoint, "/")
}
