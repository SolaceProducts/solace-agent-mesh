// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type IngestEnvelope struct {
	Events []IngestEvent `json:"events"`

	Platform *IngestPlatform `json:"platform,omitempty"`
}

type IngestPlatform struct {
	OS         string `json:"os,omitempty"`
	Arch       string `json:"arch,omitempty"`
	AppVersion string `json:"app_version,omitempty"`
}

type IngestEvent struct {
	SessionID  string         `json:"session_id,omitempty"`
	OccurredAt string         `json:"occurred_at,omitempty"`
	Name       EventName      `json:"event"`
	Properties map[string]any `json:"properties,omitempty"`
}

const maxIngestEnvelopeEvents = 64

const receiveErrorBackoff = time.Second

var brokerIngestEvents = map[EventName]bool{
	EventInstallInventory: true,
}

var brokerCLIIngestEvents = map[EventName]bool{
	EventCLICommandRun: true,
}

type BrokerIngest struct {
	broker  broker.Broker
	svc     *Service
	topic   string
	surface string
	accept  map[EventName]bool
	logger  *slog.Logger

	queue  broker.Queue
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewBrokerIngest(brk broker.Broker, svc *Service, namespace string, logger *slog.Logger) *BrokerIngest {
	return newBrokerIngest(brk, svc, a2a.ProductAnalyticsIngestTopic(namespace), SurfaceServer, brokerIngestEvents, logger)
}

func NewCLIBrokerIngest(brk broker.Broker, svc *Service, namespace string, logger *slog.Logger) *BrokerIngest {
	return newBrokerIngest(brk, svc, a2a.ProductAnalyticsCLIIngestTopic(namespace), SurfaceCLI, brokerCLIIngestEvents, logger)
}

func newBrokerIngest(brk broker.Broker, svc *Service, topic, surface string, accept map[EventName]bool, logger *slog.Logger) *BrokerIngest {
	if brk == nil || svc == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BrokerIngest{broker: brk, svc: svc, topic: topic, surface: surface, accept: accept, logger: logger}
}

func (b *BrokerIngest) Init(context.Context, config.Config) error { return nil }

func (b *BrokerIngest) Health() error { return nil }

func (b *BrokerIngest) Start(ctx context.Context) error {
	queue, err := b.broker.CreateDirectSubscription()
	if err != nil {
		b.logger.Warn("product analytics broker ingest unavailable, no subscription", "error", SanitizedError(err))
		return nil
	}
	topic := b.topic
	if err := b.broker.Subscribe(queue, topic); err != nil {
		b.logger.Warn("product analytics broker ingest unavailable, subscribe failed", "error", SanitizedError(err))
		return nil
	}
	b.queue = queue

	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	b.cancel = cancel
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.receiveLoop(loopCtx)
	}()
	b.logger.Info("product analytics broker ingest subscribed", "topic", topic)
	return nil
}

func (b *BrokerIngest) Stop(ctx context.Context) error {
	if b.cancel != nil {
		b.cancel()
	}
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		b.logger.Warn("product analytics broker ingest stop deadline reached with the receive loop still running")
	}
	return nil
}

func (b *BrokerIngest) receiveLoop(ctx context.Context) {
	for ctx.Err() == nil {
		b.receiveOnce(ctx)
	}
}

func (b *BrokerIngest) receiveOnce(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			b.logger.Warn("product analytics broker ingest recovered from panic mid-receive", "panic", SanitizedPanic(rec))
		}
	}()
	msg, err := b.broker.Receive(ctx, b.queue)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		b.logger.Debug("product analytics broker ingest receive failed", "error", SanitizedError(err))
		select {
		case <-ctx.Done():
		case <-time.After(receiveErrorBackoff):
		}
		return
	}
	if msg == nil {
		return
	}
	b.handle(ctx, msg.Payload)
}

const maxIngestEnvelopeBytes = 1 << 20

func publisherPlatform(p *IngestPlatform) ClientPlatform {
	if p == nil {
		return ClientPlatform{}
	}
	return ParseClientPlatform(p.OS, p.Arch, p.AppVersion)
}

func (b *BrokerIngest) handle(ctx context.Context, payload []byte) {
	if len(payload) > maxIngestEnvelopeBytes {
		b.logger.Debug("product analytics broker ingest payload refused, too large", "bytes", len(payload))
		return
	}
	var env IngestEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		b.logger.Debug("product analytics broker ingest payload not decoded", "error", SanitizedError(err))
		return
	}
	if len(env.Events) > maxIngestEnvelopeEvents {
		b.logger.Debug("product analytics broker ingest envelope refused, too many events", "count", len(env.Events))
		return
	}
	raws := make([]RawEvent, 0, len(env.Events))
	for _, e := range env.Events {
		if !b.accept[e.Name] {
			b.logger.Debug("product analytics broker ingest event refused, not accepted on this topic", "event", e.Name)
			continue
		}
		raws = append(raws, RawEvent{
			SessionID:  e.SessionID,
			OccurredAt: e.OccurredAt,
			Name:       e.Name,
			Properties: e.Properties,
		})
	}
	if len(raws) == 0 {
		return
	}
	b.svc.EnqueueRelayed(ctx, b.surface, publisherPlatform(env.Platform), raws)
}
