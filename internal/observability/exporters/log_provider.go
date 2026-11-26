// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

const LogServiceName = "sam-enterprise"

type LogProvider struct {
	providers []*sdklog.LoggerProvider
	handlers  []slog.Handler

	mu      sync.Mutex
	stopped bool
	lastErr error
}

func NewLogProvider(ctx context.Context, configs []ExporterConfig, res *resource.Resource, logger *slog.Logger) (*LogProvider, error) {
	if logger == nil {
		logger = slog.Default()
	}

	var enabled []ExporterConfig
	for _, c := range configs {
		if c.Logs {
			enabled = append(enabled, c)
		}
	}
	if len(enabled) == 0 {
		return nil, nil
	}

	providers := make([]*sdklog.LoggerProvider, 0, len(enabled))
	handlers := make([]slog.Handler, 0, len(enabled))

	for idx, ec := range enabled {
		exp, err := CreateOTLPLogExporter(ctx, ec, logger)
		if err != nil {
			logger.Error("failed to create OTLP log exporter — skipping",
				"index", idx, "endpoint", ec.Endpoint, "error", err)
			continue
		}
		proc := NewLogBatchProcessor(exp)
		providerOpts := []sdklog.LoggerProviderOption{sdklog.WithProcessor(proc)}
		if res != nil {
			providerOpts = append(providerOpts, sdklog.WithResource(res))
		}
		provider := sdklog.NewLoggerProvider(providerOpts...)
		bridge := otelslog.NewHandler(LogServiceName,
			otelslog.WithLoggerProvider(provider),
			otelslog.WithSource(true),
		)
		level := LevelFromString(ec.LogLevel)

		providers = append(providers, provider)
		handlers = append(handlers, newLevelFilteringHandler(bridge, level))
	}

	if len(providers) == 0 {
		return nil, errors.New("no usable OTLP log exporters configured")
	}

	return &LogProvider{
		providers: providers,
		handlers:  handlers,
	}, nil
}

func (p *LogProvider) Handlers() []slog.Handler {
	if p == nil {
		return nil
	}
	return p.handlers
}

func (p *LogProvider) ExporterCount() int {
	if p == nil {
		return 0
	}
	return len(p.providers)
}

func (p *LogProvider) Init(_ context.Context, _ config.Config) error { return nil }

func (p *LogProvider) Start(_ context.Context) error { return nil }

func (p *LogProvider) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return p.lastErr
	}
	p.stopped = true
	var errs []error
	for i, prov := range p.providers {
		if err := prov.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown OTLP log provider %d: %w", i, err))
		}
	}
	if len(errs) > 0 {
		p.lastErr = errors.Join(errs...)
	}
	return p.lastErr
}

func (p *LogProvider) Health() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErr
}
