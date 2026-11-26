// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"runtime"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

const (
	inventoryStartupDelay = time.Minute

	inventoryInterval = 24 * time.Hour

	inventoryTimeout = 30 * time.Second

	jitterDivisor = 4
)

func jittered(d time.Duration) time.Duration {
	spread := d / jitterDivisor
	if spread <= 0 {
		return d
	}
	return d - spread + time.Duration(rand.Int64N(int64(2*spread)+1))
}

type inventoryEmitter interface {
	emit(ctx context.Context, raw productanalytics.RawEvent)
}

type localEmitter struct {
	analytics *productanalytics.Service
}

func (e localEmitter) emit(ctx context.Context, raw productanalytics.RawEvent) {
	e.analytics.Enqueue(ctx, productanalytics.SurfaceServer, []productanalytics.RawEvent{raw})
}

type brokerEmitter struct {
	broker broker.Broker
	topic  string
	logger *slog.Logger
}

func (e brokerEmitter) emit(ctx context.Context, raw productanalytics.RawEvent) {
	payload, err := json.Marshal(productanalytics.IngestEnvelope{
		Platform: &productanalytics.IngestPlatform{
			OS:         runtime.GOOS,
			Arch:       runtime.GOARCH,
			AppVersion: samversion.Version,
		},
		Events: []productanalytics.IngestEvent{{
			SessionID:  raw.SessionID,
			OccurredAt: raw.OccurredAt,
			Name:       raw.Name,
			Properties: raw.Properties,
		}},
	})
	if err != nil {
		e.logger.Debug("product analytics inventory snapshot not encoded", "error", productanalytics.SanitizedError(err))
		return
	}
	if err := e.broker.Publish(ctx, e.topic, &broker.Message{Payload: payload}); err != nil {
		e.logger.Debug("product analytics inventory snapshot not published", "error", productanalytics.SanitizedError(err))
		return
	}
	e.logger.Debug("product analytics inventory snapshot published for the install identity holder")
}

func inventoryEmitterFor(analytics *productanalytics.Service, brk broker.Broker, namespace string, logger *slog.Logger) inventoryEmitter {
	if analytics != nil {
		return localEmitter{analytics: analytics}
	}
	if brk == nil || !buildedition.ProductAnalytics {
		return nil
	}
	return brokerEmitter{
		broker: brk,
		topic:  a2a.ProductAnalyticsIngestTopic(namespace),
		logger: logger,
	}
}

type inventoryReporter struct {
	repo    *store.InventoryRepo
	emitter inventoryEmitter
	logger  *slog.Logger

	startupDelay time.Duration
	interval     time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newInventoryReporter(db *store.DB, emitter inventoryEmitter, logger *slog.Logger) *inventoryReporter {
	if db == nil || emitter == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &inventoryReporter{
		repo:         store.NewInventoryRepo(db),
		emitter:      emitter,
		logger:       logger,
		startupDelay: inventoryStartupDelay,
		interval:     inventoryInterval,
	}
}

func (r *inventoryReporter) Start(ctx context.Context) error {
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.cancel = cancel
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.run(loopCtx)
	}()
	return nil
}

func (r *inventoryReporter) Stop(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *inventoryReporter) keepPlatformAliveOnAnalyticsPanic() {
	if rec := recover(); rec != nil {
		r.logger.Warn("product analytics inventory snapshot recovered from panic", "panic", productanalytics.SanitizedPanic(rec))
	}
}

func (r *inventoryReporter) run(ctx context.Context) {
	defer r.keepPlatformAliveOnAnalyticsPanic()

	timer := time.NewTimer(jittered(r.startupDelay))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		r.reportOnce(ctx)
		timer.Reset(jittered(r.interval))
	}
}

func (r *inventoryReporter) reportOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, inventoryTimeout)
	defer cancel()

	inv, err := r.repo.Snapshot(ctx)
	if err != nil {
		r.logger.Debug("product analytics inventory snapshot failed", "error", productanalytics.SanitizedError(err))
		return
	}
	r.emitter.emit(ctx, productanalytics.RawEvent{
		OccurredAt: time.Now().UTC().Format(productanalytics.TimestampLayout),
		Name:       productanalytics.EventInstallInventory,
		Properties: inventoryProperties(inv),
	})
}

func inventoryProperties(inv store.Inventory) map[string]any {
	return map[string]any{
		productanalytics.PropAgentCount:                  inv.Agents,
		productanalytics.PropWorkflowCount:               inv.Workflows,
		productanalytics.PropConnectorCount:              inv.Connectors,
		productanalytics.PropEntrypointCount:             inv.Gateways,
		productanalytics.PropToolsetCount:                inv.Toolsets,
		productanalytics.PropConfiguredModelCount:        inv.ConfiguredModels,
		productanalytics.PropAgentsWithCustomToolsets:    inv.AgentsWithCustomToolsets,
		productanalytics.PropWorkflowsWithCustomToolsets: inv.WorkflowsWithCustomToolsets,
		productanalytics.PropConnectorCategories:         inv.ConnectorTypes,
		productanalytics.PropEntrypointTypes:             inv.GatewayTypes,

		productanalytics.PropDeployedAgentCount:                  inv.DeployedAgents,
		productanalytics.PropDeployedWorkflowCount:               inv.DeployedWorkflows,
		productanalytics.PropDeployedAgentsWithCustomToolsets:    inv.DeployedAgentsWithCustomToolsets,
		productanalytics.PropDeployedWorkflowsWithCustomToolsets: inv.DeployedWorkflowsWithCustomToolsets,

		productanalytics.PropBuiltinAgentCount:         inv.BuiltinAgents,
		productanalytics.PropDeployedBuiltinAgentCount: inv.DeployedBuiltinAgents,
	}
}

var _ BackgroundService = (*inventoryReporter)(nil)
