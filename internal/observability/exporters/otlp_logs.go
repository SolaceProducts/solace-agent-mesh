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

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"google.golang.org/grpc/credentials"
)

const LogExportInterval = 1 * time.Second

func CreateOTLPLogExporter(ctx context.Context, cfg ExporterConfig, logger *slog.Logger) (sdklog.Exporter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	switch cfg.Protocol {
	case ProtocolHTTP:
		return createOTLPHTTPLog(ctx, cfg, logger)
	case ProtocolGRPC:
		return createOTLPGRPCLog(ctx, cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", cfg.Protocol)
	}
}

func createOTLPHTTPLog(ctx context.Context, cfg ExporterConfig, logger *slog.Logger) (sdklog.Exporter, error) {
	endpoint := buildLogsEndpoint(cfg.Endpoint)

	opts := []otlploghttp.Option{
		otlploghttp.WithEndpointURL(endpoint),
		otlploghttp.WithTimeout(time.Duration(cfg.Timeout * float64(time.Second))),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(cfg.Headers))
	}
	if comp := mapHTTPLogCompression(cfg.Compression, logger); comp != otlploghttp.NoCompression {
		opts = append(opts, otlploghttp.WithCompression(comp))
	}

	exp, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP HTTP log exporter: %w", err)
	}
	logger.Info("created OTLP HTTP log exporter",
		"endpoint", endpoint,
		"compression", cfg.Compression,
	)
	return exp, nil
}

func createOTLPGRPCLog(ctx context.Context, cfg ExporterConfig, logger *slog.Logger) (sdklog.Exporter, error) {
	opts := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(stripScheme(cfg.Endpoint)),
		otlploggrpc.WithTimeout(time.Duration(cfg.Timeout * float64(time.Second))),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlploggrpc.WithHeaders(cfg.Headers))
	}
	if cfg.Insecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	} else if cfg.CertificateFile != "" {
		creds, err := credentials.NewClientTLSFromFile(cfg.CertificateFile, "")
		if err != nil {
			return nil, fmt.Errorf("load certificate %q: %w", cfg.CertificateFile, err)
		}
		opts = append(opts, otlploggrpc.WithTLSCredentials(creds))
	}
	if comp := mapGRPCCompression(cfg.Compression, logger); comp != "" {
		opts = append(opts, otlploggrpc.WithCompressor(comp))
	}

	exp, err := otlploggrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP gRPC log exporter: %w", err)
	}
	logger.Info("created OTLP gRPC log exporter",
		"endpoint", cfg.Endpoint,
		"compression", cfg.Compression,
		"insecure", cfg.Insecure,
	)
	return exp, nil
}

func NewLogBatchProcessor(exp sdklog.Exporter) *sdklog.BatchProcessor {
	return sdklog.NewBatchProcessor(exp, sdklog.WithExportInterval(LogExportInterval))
}

func buildLogsEndpoint(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1/logs") {
		return base
	}
	if strings.Contains(base, "/otlp/v1/logs") || strings.Contains(base, "/api/v2/otlp/v1/logs") {
		return base
	}
	return base + "/v1/logs"
}

func mapHTTPLogCompression(s string, logger *slog.Logger) otlploghttp.Compression {
	switch s {
	case CompressionNone, "":
		return otlploghttp.NoCompression
	case CompressionGzip:
		return otlploghttp.GzipCompression
	case CompressionDeflate:
		logger.Warn("HTTP log exporter: deflate compression is not supported by the OTLP HTTP exporter, falling back to none")
		return otlploghttp.NoCompression
	default:
		logger.Warn("unknown compression value, falling back to none", "value", s)
		return otlploghttp.NoCompression
	}
}
