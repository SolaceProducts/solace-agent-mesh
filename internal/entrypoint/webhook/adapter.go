// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func init() {
	entrypoint.RegisterAdapterFactory("webhook", func(cfg map[string]any) (entrypoint.GatewayAdapter, error) {
		return NewWebhookAdapter(cfg)
	})
}

type WebhookAdapter struct {
	core   *entrypoint.GatewayCore
	cfg    *Config
	router *router
	logger *slog.Logger

	onHandlerReady func(http.Handler)
}

func NewWebhookAdapter(adapterCfg map[string]any) (*WebhookAdapter, error) {
	cfg, err := ParseConfig(adapterCfg)
	if err != nil {
		return nil, fmt.Errorf("webhook config: %w", err)
	}
	return &WebhookAdapter{cfg: cfg}, nil
}

func (a *WebhookAdapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNamed}
}

func (a *WebhookAdapter) MachineIngress() bool { return true }

func (a *WebhookAdapter) EntrypointType() string { return "webhook" }

func (a *WebhookAdapter) SetOnHandlerReady(cb func(http.Handler)) {
	a.onHandlerReady = cb
}

func (a *WebhookAdapter) Capabilities() map[string]bool {
	return map[string]bool{tool.CapInteractiveUserInput: false}
}

func (a *WebhookAdapter) Init(_ context.Context, core *entrypoint.GatewayCore) error {
	a.core = core
	a.logger = core.Logger().With("logger_name", "webhook-adapter")

	a.router = buildRouter(a.cfg.Routes, a.dispatch)

	a.logger.Info("webhook adapter initialized",
		"gatewayID", core.GatewayID(),
		"routes", len(a.cfg.Routes),
	)

	if a.onHandlerReady != nil {
		a.onHandlerReady(a.router.Handler())
	}
	return nil
}

func (a *WebhookAdapter) Start(_ context.Context) error { return nil }

func (a *WebhookAdapter) Stop(_ context.Context) error { return nil }

func (a *WebhookAdapter) OnFinalResponse(_ context.Context, taskID string, _ *a2a.Task, _ []byte) {
	a.logger.Debug("terminal task response received; dropping (fire-and-forget)", "taskID", taskID)
}

func (a *WebhookAdapter) OnError(_ context.Context, taskID string, err *a2a.Error, _ []byte) {
	a.logger.Debug("terminal task error received; dropping (fire-and-forget)", "taskID", taskID, "code", err.Code)
}

func (a *WebhookAdapter) OnStatusUpdate(_ context.Context, _ string, _ *a2a.TaskStatusUpdateEvent, _ []byte) {
}

func (a *WebhookAdapter) OnArtifactUpdate(_ context.Context, _ string, _ *a2a.TaskArtifactUpdateEvent, _ []byte) {
}

func (a *WebhookAdapter) OnAgentDiscovered(_ *a2a.AgentCard) {
}

func (a *WebhookAdapter) OnAgentRemoved(_ string) {
}

var _ entrypoint.GatewayAdapter = (*WebhookAdapter)(nil)
var _ entrypoint.DeferredHTTPAdapter = (*WebhookAdapter)(nil)
var _ entrypoint.CapabilityProvider = (*WebhookAdapter)(nil)
