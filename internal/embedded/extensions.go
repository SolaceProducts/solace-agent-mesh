// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type ExtensionProvider interface {
	Bootstrap(ctx context.Context, fullConfig map[string]any, aweSessionStore session.Store, brk broker.Broker, logger *slog.Logger) ([]runtime.Option, runtime.Component, error)

	ConfigureGateway(gwCore *entrypoint.GatewayCore, appConfig map[string]any, logger *slog.Logger) error

	GatewayHooks() *entrypoint.GatewayHooks

	Stop(ctx context.Context) error
}

type PlatformProvider interface {
	PlatformExtensions(appConfig map[string]any, logger *slog.Logger) platform.ExtensionPoints
}

type GatewayReceiver interface {
	SetInProcessGateway(gateway *entrypoint.GatewayExecutor)
}

type PlatformConfigurer interface {
	ConfigurePlatformAuth(appConfig map[string]any, logger *slog.Logger) func(http.Handler) http.Handler
}

type PlatformWrapper interface {
	WrapPlatformCore(core *platform.PlatformCore, logger *slog.Logger) runtime.Component
}
