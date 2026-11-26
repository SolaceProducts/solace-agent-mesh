// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
)

type discoveryManager struct {
	agents   *AgentRegistry
	gateways *GatewayRegistry
	brk      broker.Broker
	queue    broker.Queue
	logger   *slog.Logger

	namespace      string
	expiryInterval time.Duration
	expiryTTL      time.Duration
	cancel         context.CancelFunc
}

func newDiscoveryManager(
	agents *AgentRegistry,
	gateways *GatewayRegistry,
	brk broker.Broker,
	namespace string,
	expiryIntervalSecs int,
	expiryTTLSecs int,
	logger *slog.Logger,
) *discoveryManager {
	return &discoveryManager{
		agents:         agents,
		gateways:       gateways,
		brk:            brk,
		namespace:      namespace,
		expiryInterval: time.Duration(expiryIntervalSecs) * time.Second,
		expiryTTL:      time.Duration(expiryTTLSecs) * time.Second,
		logger:         logger,
	}
}

func (d *discoveryManager) Start(ctx context.Context) error {
	var err error
	d.queue, err = d.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create discovery queue: %w", err)
	}

	sub := a2a.DiscoverySubscription(d.namespace)
	if err := d.brk.Subscribe(d.queue, sub); err != nil {
		return fmt.Errorf("subscribe discovery %q: %w", sub, err)
	}

	d.logger.Info("subscribed to discovery topic", "topic", sub)

	loopCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	go d.receiveLoop(loopCtx)
	go d.expiryLoop(loopCtx)

	if err := discovery.AnswerCardRequests(loopCtx, d.brk, d.namespace, d.agents.cards, d.logger); err != nil {
		d.logger.Warn("agent card requests will not be answered", "error", err)
	}
	if err := discovery.RequestCards(loopCtx, d.brk, d.namespace, discovery.CardRequestWindow, d.expiryTTL, func(rc discovery.ReportedCard) {
		d.agents.cards.ObserveSeen(rc.Card, rc.InstanceID, rc.LastSeen)
	}); err != nil {
		d.logger.Warn("agent card request failed; agents appear at their next card", "error", err)
	}

	return nil
}

func (d *discoveryManager) Stop(_ context.Context) error {
	if d.cancel != nil {
		d.cancel()
	}
	if d.queue != nil {
		_ = d.brk.DeleteQueue(d.queue.Name())
	}
	return nil
}

func (d *discoveryManager) receiveLoop(ctx context.Context) {
	bo := backoff.Exponential{
		Initial: 100 * time.Millisecond,
		Max:     30 * time.Second,
		Jitter:  0.2,
	}
	for {
		msg, err := d.brk.Receive(ctx, d.queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			d.logger.Warn("discovery receive error", "error", err, "backoff", bo.Current())
			if waitErr := bo.Wait(ctx); waitErr != nil {
				return
			}
			continue
		}
		bo.Reset()
		if msg == nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		d.handleDiscovery(msg)
		msg.Ack()
	}
}

func (d *discoveryManager) handleDiscovery(msg *broker.Message) {
	m, err := discovery.ParseMessage(msg.Payload, msg.UserProperties)
	if err != nil {
		d.logger.Warn("invalid discovery card JSON", "error", err)
		return
	}

	if IsGatewayCard(m.Card) {
		ev := d.gateways.apply(m)
		switch {
		case ev.Removed:
			d.logger.Info("gateway deregistered", "name", ev.Name, "instanceID", m.InstanceID)
		case m.Removed && ev.Ignored:
			d.logger.Debug("ignored gateway removal card: gateway or instance not registered",
				"name", ev.Name, "instanceID", m.InstanceID)
		case m.Removed:
			d.logger.Info("gateway instance removed: gateway still published by another instance",
				"name", ev.Name, "instanceID", m.InstanceID, "remaining", ev.Remaining)
		case ev.Change == discovery.Added:
			d.logger.Info("discovered gateway", "name", ev.Name)
		case ev.Change != 0:
			d.logger.Debug("refreshed gateway", "name", ev.Name)
		}
		return
	}

	ev := d.agents.apply(m)
	switch {
	case ev.Removed:
		d.logger.Info("agent deregistered", "name", ev.Name, "instanceID", m.InstanceID)
	case m.Removed && ev.Ignored:
		d.logger.Debug("ignored agent removal card: agent or instance not registered",
			"name", ev.Name, "instanceID", m.InstanceID)
	case m.Removed:
		d.logger.Info("agent instance removed: agent still published by another instance",
			"name", ev.Name, "instanceID", m.InstanceID, "remaining", ev.Remaining)
	case ev.Change == discovery.Added:
		d.logger.Info("discovered agent", "name", ev.Name, "skills", len(m.Card.Skills))
	case ev.Change != 0:
		d.logger.Debug("refreshed agent", "name", ev.Name)
	}
}

func (d *discoveryManager) expiryLoop(ctx context.Context) {
	ticker := time.NewTicker(d.expiryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-d.expiryTTL)

			if removed := d.agents.ExpireOlderThan(cutoff); len(removed) > 0 {
				for _, name := range removed {
					d.logger.Debug("expired agent (TTL)", "name", name)
				}
				d.logger.Info("expired agents (TTL)", "count", len(removed))
			}

			if removed := d.gateways.ExpireOlderThan(cutoff); len(removed) > 0 {
				for _, id := range removed {
					d.logger.Debug("expired gateway (TTL)", "id", id)
				}
				d.logger.Info("expired gateways (TTL)", "count", len(removed))
			}
		}
	}
}
