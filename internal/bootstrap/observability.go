// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"flag"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability/exporters"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"go.opentelemetry.io/otel/sdk/resource"
)

func ManagementPortFromConfig(rootCfg map[string]any) (int, bool) {
	mgmt, ok := rootCfg["management_server"].(map[string]any)
	if !ok {
		return 0, false
	}
	var port int
	switch v := mgmt["port"].(type) {
	case int:
		port = v
	case int64:
		port = int(v)
	case float64:
		port = int(v)
	default:
		return 0, false
	}
	if port <= 0 {
		return 0, false
	}
	return port, true
}

func ResolveManagementAddr(rootCfg map[string]any, flagAddr string, flagSet bool) string {
	if flagSet {
		return flagAddr
	}
	if port, ok := ManagementPortFromConfig(rootCfg); ok {
		return fmt.Sprintf(":%d", port)
	}
	return flagAddr
}

func ResolveHealthAddrFlag(rootCfg map[string]any, flagAddr string) string {
	flagSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "health-addr" {
			flagSet = true
		}
	})
	return ResolveManagementAddr(rootCfg, flagAddr, flagSet)
}

func MountMetricsOnManagement(ms *runtime.ManagementServer, rt *runtime.Runtime, logger *slog.Logger) {
	if ms == nil || rt == nil {
		return
	}
	reg, ok := rt.Services().Metrics().(*observability.Registry)
	if !ok || reg == nil {
		return
	}
	path := reg.Path()
	if path == "" {
		path = "/metrics"
	}
	ms.Handle(path, reg.Handler())
	logger.Info("metrics endpoint registered on management server", "path", path)
}

func createMetricsRecorder(rootCfg map[string]any, appCfgs []map[string]any, logger *slog.Logger, serviceName string) runtime.Metrics {
	if reg := buildRecorderFromContainer(rootCfg, logger, serviceName); reg != nil {
		return reg
	}

	for _, appCfg := range appCfgs {
		if _, ok := appCfg["management_server"].(map[string]any); !ok {
			continue
		}
		logger.Warn("management_server inside app_config is deprecated; move it to the YAML root (sibling of apps:)")
		if reg := buildRecorderFromContainer(appCfg, logger, serviceName); reg != nil {
			return reg
		}
	}
	return nil
}

func buildRecorderFromContainer(container map[string]any, logger *slog.Logger, serviceName string) *observability.Registry {
	if container == nil {
		return nil
	}
	mgmt, ok := container["management_server"].(map[string]any)
	if !ok {
		return nil
	}
	obsCfg, ok := mgmt["observability"].(map[string]any)
	if !ok {
		return nil
	}
	cfg := parseObservabilityConfig(obsCfg)
	if !cfg.Enabled {
		return nil
	}
	exporterReaders := buildExporterReaders(container, logger)
	regOpts := exporterReaders
	if res, err := observability.BuildResource(serviceName); err != nil {
		logger.Warn("build metric resource attributes failed; metrics will be untagged", "error", err)
	} else {
		regOpts = append([]observability.RegistryOption{observability.WithResource(res)}, regOpts...)
	}
	reg, err := observability.NewRegistry(cfg, logger, regOpts...)
	if err != nil {
		logger.Error("observability config error", "error", err)
		return nil
	}
	if reg == nil {
		return nil
	}
	observability.RegisterDBPoolGauges(reg, dbutil.PoolStats)
	logger.Info("observability enabled",
		"metricPrefix", reg.MetricPrefix(),
		"path", reg.Path(),
		"serviceName", serviceName,
		"otlpMetricExporters", len(exporterReaders),
	)
	return reg
}

func buildExporterReaders(appCfg map[string]any, logger *slog.Logger) []observability.RegistryOption {
	parsed := exporters.ParseExporters(appCfg, logger)
	if len(parsed) == 0 {
		return nil
	}
	var opts []observability.RegistryOption
	for idx, ec := range parsed {
		if !ec.Metrics {
			continue
		}
		exp, err := exporters.CreateOTLPMetricExporter(context.Background(), ec, logger)
		if err != nil {
			logger.Error("failed to create OTLP metric exporter — skipping",
				"index", idx, "endpoint", ec.Endpoint, "error", err)
			continue
		}
		opts = append(opts, observability.WithAdditionalReader(exporters.NewPeriodicReader(exp)))
		logger.Info("registered OTLP metric exporter",
			"index", idx, "endpoint", ec.Endpoint, "protocol", ec.Protocol)
	}
	return opts
}

func BuildOTLPLogProvider(ctx context.Context, apps []map[string]any, res *resource.Resource, logger *slog.Logger) (*exporters.LogProvider, error) {
	var combined []exporters.ExporterConfig
	for _, appCfg := range apps {
		parsed := exporters.ParseExporters(appCfg, logger)
		for _, ec := range parsed {
			if ec.Logs {
				combined = append(combined, ec)
			}
		}
	}
	if len(combined) == 0 {
		return nil, nil
	}
	return exporters.NewLogProvider(ctx, combined, res, logger)
}

func AttachOTLPLogExporters(ctx context.Context, apps []samapp.AppEntry, res *resource.Resource, logger *slog.Logger) (*slog.Logger, *exporters.LogProvider, error) {
	appConfigs := make([]map[string]any, len(apps))
	for i, a := range apps {
		appConfigs[i] = a.AppConfig
	}
	provider, err := BuildOTLPLogProvider(ctx, appConfigs, res, logger)
	if err != nil {
		return logger, nil, err
	}
	if provider == nil {
		return logger, nil, nil
	}
	merged := samlog.AttachHandlers(logger, provider.Handlers()...)
	logger.Info("OTLP log exporters attached",
		"exporterCount", provider.ExporterCount(),
	)
	return merged, provider, nil
}

type LogProviderRegistrar interface {
	Register(name string, c runtime.Component)
}

func InstallOTLPLogs(ctx context.Context, apps []samapp.AppEntry, serviceName string, logger *slog.Logger) (*slog.Logger, func(LogProviderRegistrar), error) {
	res, resErr := observability.BuildResource(serviceName)
	if resErr != nil {
		logger.Warn("build log resource attributes failed; logs will be untagged", "error", resErr)
	}
	merged, provider, err := AttachOTLPLogExporters(ctx, apps, res, logger)
	if err != nil {
		return logger, nil, fmt.Errorf("install OTLP log exporters: %w", err)
	}
	if provider != nil {
		slog.SetDefault(merged)
	}
	register := func(r LogProviderRegistrar) {
		if provider != nil {
			r.Register("otlp_logs", provider)
		}
	}
	return merged, register, nil
}

func parseObservabilityConfig(raw map[string]any) observability.Config {
	cfg := observability.Config{}

	cfg.Enabled, _ = raw["enabled"].(bool)
	cfg.MetricPrefix, _ = raw["metric_prefix"].(string)
	cfg.Path, _ = raw["path"].(string)

	if dm, ok := raw["distribution_metrics"].(map[string]any); ok {
		cfg.DistributionMetrics = parseDistributionMetrics(dm)
	}
	if vm, ok := raw["value_metrics"].(map[string]any); ok {
		cfg.ValueMetrics = parseValueMetrics(vm)
	}

	return cfg
}

func parseDistributionMetrics(raw map[string]any) map[string]observability.DistributionMetricConfig {
	result := make(map[string]observability.DistributionMetricConfig, len(raw))
	for name, v := range raw {
		mc, ok := v.(map[string]any)
		if !ok {
			continue
		}
		dc := observability.DistributionMetricConfig{
			Buckets:       parseFloatSlice(mc["buckets"]),
			ExcludeLabels: parseStringSlice(mc["exclude_labels"]),
		}
		result[name] = dc
	}
	return result
}

func parseValueMetrics(raw map[string]any) map[string]observability.ValueMetricConfig {
	result := make(map[string]observability.ValueMetricConfig, len(raw))
	for name, v := range raw {
		mc, ok := v.(map[string]any)
		if !ok {
			continue
		}
		vc := observability.ValueMetricConfig{
			ExcludeLabels: parseStringSlice(mc["exclude_labels"]),
		}
		result[name] = vc
	}
	return result
}

func parseFloatSlice(v any) []float64 {
	slice, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]float64, 0, len(slice))
	for _, item := range slice {
		switch n := item.(type) {
		case float64:
			result = append(result, n)
		case int:
			result = append(result, float64(n))
		case int64:
			result = append(result, float64(n))
		default:
			continue
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func parseStringSlice(v any) []string {
	slice, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
