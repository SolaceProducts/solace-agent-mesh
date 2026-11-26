// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/agentlink"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/email"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/slack"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/teams"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/webhook"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func NewGatewayAdapter(appConfig map[string]any) entrypoint.GatewayAdapter {
	if ga, ok := appConfig["gateway_adapter"].(map[string]any); ok {
		if adapterType, _ := ga["type"].(string); adapterType != "" {
			adapterCfg, _ := ga["adapter_config"].(map[string]any)
			adapter, err := entrypoint.CreateAdapter(adapterType, adapterCfg)
			if err != nil {
				slog.Default().With("logger_name", "bootstrap").Warn("failed to create adapter from registry, falling back to httpsse",
					"type", adapterType, "error", err)
				return httpsse.NewHTTPSSEAdapter()
			}
			return adapter
		}
	}

	if _, ok := appConfig["event_mesh_broker_config"]; ok {
		return eventmesh.NewEventMeshAdapter()
	}

	return httpsse.NewHTTPSSEAdapter()
}

func NewGatewayCore(svc runtime.Services, appConfig map[string]any) *entrypoint.GatewayCore {
	adapter := NewGatewayAdapter(appConfig)
	core := entrypoint.NewGatewayCore(adapter, appConfig)
	core.SetServices(svc)
	return core
}

func DefaultGatewayFactory(appConfig map[string]any, svc runtime.Services) (*entrypoint.GatewayCore, error) {
	adapter := NewGatewayAdapter(appConfig)
	core := entrypoint.NewGatewayCore(adapter, appConfig)
	core.SetServices(svc)
	return core, nil
}

func NewGatewayExecutor(svc runtime.Services, apps []samapp.AppEntry) (*entrypoint.GatewayExecutor, error) {
	exe := entrypoint.NewGatewayExecutor(svc, DefaultGatewayFactory)

	if len(apps) > 0 {
		if v, ok := apps[0].AppConfig["allow_runtime_gateway_management"].(bool); ok {
			exe.AllowRuntimeManagement = v
		}
	}

	for _, app := range apps {
		if err := exe.LoadConfig(app.Name, app.AppConfig); err != nil {
			return nil, fmt.Errorf("load gateway %q: %w", app.Name, err)
		}
	}

	return exe, nil
}

type BuildOpt func(*buildConfig)

type buildConfig struct {
	suppressUnknownFeatureWarn bool
	featureRegistryOpts        []features.RegistryOption
}

func WithFeatureRegistryOptions(opts ...features.RegistryOption) BuildOpt {
	return func(c *buildConfig) { c.featureRegistryOpts = opts }
}

func SuppressUnknownFeatureWarn() BuildOpt {
	return func(c *buildConfig) { c.suppressUnknownFeatureWarn = true }
}

func BuildGatewayRuntimeOptions(ctx context.Context, rootCfg map[string]any, apps []samapp.AppEntry, logger *slog.Logger, optFns ...BuildOpt) ([]runtime.Option, error) {
	if len(apps) == 0 {
		return nil, fmt.Errorf("BuildGatewayRuntimeOptions: no app entries")
	}
	cfg := buildConfig{}
	for _, fn := range optFns {
		fn(&cfg)
	}

	opts := []runtime.Option{
		runtime.WithLogger(logger),
	}

	for _, app := range apps {
		if app.ModelRaw == nil {
			continue
		}
		llmOpts, err := buildLLMOptions(ctx, app.ModelRaw, logger)
		if err != nil {
			return nil, fmt.Errorf("gateway LLM for %q: %w", app.Name, err)
		}
		opts = append(opts, llmOpts...)
		break
	}

	primary := apps[0]

	metrics := createMetricsRecorder(rootCfg, []map[string]any{primary.AppConfig}, logger, "sam-gateway")

	artifactConfigured := false
	for _, app := range apps {
		svcCfg, ok := app.AppConfig["artifact_service"].(map[string]any)
		if !ok {
			continue
		}
		svc := samapp.CreateArtifactService(svcCfg, app.AppConfig)
		if svc != nil {
			opts = append(opts, runtime.WithArtifacts(svc))
			artifactConfigured = true
		}
		break
	}
	if !artifactConfigured {
		logger.Warn("no artifact_service configured in any gateway app; artifact features will be disabled (chat attachments, file previews, embed resolution, slack/email uploads, session artifact listing)")
	}

	for _, app := range apps {
		svcCfg, ok := app.AppConfig["volume_service"].(map[string]any)
		if !ok {
			continue
		}
		if !samapp.VolumeServiceConfigEnabled(svcCfg) {
			break
		}
		svc, err := samapp.CreateVolumeService(svcCfg)
		if err != nil {
			return nil, fmt.Errorf("create volume service for app %q: %w", app.Name, err)
		}
		opts = append(opts, runtime.WithVolumes(svc))
		break
	}

	flagRegistry := features.NewRegistry(cfg.featureRegistryOpts...)
	if err := features.LoadFlags(flagRegistry); err != nil {
		return nil, fmt.Errorf("load feature flags: %w", err)
	}
	if !cfg.suppressUnknownFeatureWarn {
		for _, name := range flagRegistry.UnknownEnvOverrides() {
			logger.Warn("ignoring SAM_FEATURE_* env var that does not match any registered flag", "env", name)
		}
	}
	opts = append(opts, runtime.WithFeatureRegistry(flagRegistry))

	opts = append(opts, GatewayUsageReporterOptions(ctx, primary.AppConfig, logger)...)

	if metrics != nil {
		opts = append(opts, runtime.WithMetrics(metrics))
	}

	return opts, nil
}
