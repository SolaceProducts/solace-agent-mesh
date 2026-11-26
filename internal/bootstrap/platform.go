// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/openapispec"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func NewPlatformCore(svc runtime.Services, appConfig map[string]any, ext platform.ExtensionPoints) *platform.PlatformCore {
	core := platform.NewPlatformCore(appConfig, ext)
	core.SetServices(svc)
	return core
}

func BuildPlatformRuntimeOptions(ctx context.Context, rootCfg map[string]any, apps []samapp.AppEntry, logger *slog.Logger) []runtime.Option {
	opts := []runtime.Option{runtime.WithLogger(logger)}
	var platformApp samapp.AppEntry
	if len(apps) > 0 {
		platformApp = apps[0]
	}
	if metrics := createMetricsRecorder(rootCfg, []map[string]any{platformApp.AppConfig}, logger, "sam-platform"); metrics != nil {
		opts = append(opts, runtime.WithMetrics(metrics))
	}
	opts = append(opts, PlatformUsageReporterOptions(ctx, apps, logger)...)
	return opts
}

type PlatformMountable interface {
	SetOnHandlerReady(cb func(http.Handler))
}

func MountPlatform(proxy *entrypoint.GatewayProxy, core PlatformMountable) {
	core.SetOnHandlerReady(func(h http.Handler) {
		proxy.RegisterPrefixHandler(platform.V1Prefix, h)
		proxy.RegisterPrefixHandler(platform.APIV2Prefix, h)
		proxy.RegisterPrefixHandler(platform.InternalPrefix, h)
		proxy.RegisterPrefixHandler(openapispec.Path, h)
		proxy.RegisterPrefixHandler(openapispec.ViewerPath, h)
	})
}
