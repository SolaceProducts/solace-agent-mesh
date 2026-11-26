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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

type cliIngestRelay interface {
	relay(ctx context.Context, host productanalytics.ClientPlatform, raws []productanalytics.RawEvent)
}

type localCLIRelay struct {
	analytics *productanalytics.Service
}

func (r localCLIRelay) relay(ctx context.Context, host productanalytics.ClientPlatform, raws []productanalytics.RawEvent) {
	r.analytics.EnqueueRelayed(ctx, productanalytics.SurfaceCLI, host, raws)
}

type brokerCLIRelay struct {
	broker broker.Broker
	topic  string
	logger *slog.Logger
}

func (r brokerCLIRelay) relay(ctx context.Context, host productanalytics.ClientPlatform, raws []productanalytics.RawEvent) {
	events := make([]productanalytics.IngestEvent, 0, len(raws))
	for _, raw := range raws {
		events = append(events, productanalytics.IngestEvent{
			SessionID:  raw.SessionID,
			OccurredAt: raw.OccurredAt,
			Name:       raw.Name,
			Properties: raw.Properties,
		})
	}
	payload, err := json.Marshal(productanalytics.IngestEnvelope{
		Platform: &productanalytics.IngestPlatform{
			OS:         host.OS,
			Arch:       host.Arch,
			AppVersion: host.AppVersion,
		},
		Events: events,
	})
	if err != nil {
		r.logger.Debug("product analytics cli report not encoded", "error", productanalytics.SanitizedError(err))
		return
	}
	if err := r.broker.Publish(ctx, r.topic, &broker.Message{Payload: payload}); err != nil {
		r.logger.Debug("product analytics cli report not published", "error", productanalytics.SanitizedError(err))
		return
	}
	r.logger.Debug("product analytics cli report published for the install identity holder")
}

func cliIngestRelayFor(analytics *productanalytics.Service, brk broker.Broker, namespace string, logger *slog.Logger) cliIngestRelay {
	if analytics != nil {
		return localCLIRelay{analytics: analytics}
	}
	if brk == nil || !buildedition.ProductAnalytics {
		return nil
	}
	return brokerCLIRelay{
		broker: brk,
		topic:  a2a.ProductAnalyticsCLIIngestTopic(namespace),
		logger: logger,
	}
}

type relayIngester struct {
	relay cliIngestRelay
}

func (r relayIngester) Ingest(ctx context.Context, batch CLIIngestBatch) {
	raws := make([]productanalytics.RawEvent, 0, len(batch.Events))
	for _, e := range batch.Events {
		raws = append(raws, productanalytics.RawEvent{
			EventID:    e.EventID,
			SessionID:  e.SessionID,
			OccurredAt: e.OccurredAt,
			Name:       e.Event,
			Properties: e.Properties,
		})
	}
	host := productanalytics.ParseClientPlatform(batch.OS, batch.Arch, batch.AppVersion)
	r.relay.relay(ctx, host, raws)
}
