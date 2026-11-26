// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

type NotifyDeliverOptions struct {
	GatewayType  string
	Interactable bool
	ApproverSets bool
}

func (g *GatewayCore) StartNotifyDeliverConsumer(ctx context.Context, opts NotifyDeliverOptions, logger *slog.Logger, handle func(a2a.NotifyDeliver)) {
	gatewayType := opts.GatewayType
	if g == nil || g.svc == nil {
		return
	}
	reg := g.svc.FeatureRegistry()
	if reg == nil || !reg.IsEnabled(features.KeyNotifyService) {
		return
	}
	brk := g.svc.Broker()
	if brk == nil {
		return
	}
	queueName := a2a.QueueName(g.Namespace(), "gdk", "notify_deliver", gatewayType)
	q, err := brk.CreateQueue(queueName, broker.QueueOptions{
		Named:         true,
		Exclusive:     false,
		MaxRedelivery: 3,
	})
	if err != nil {
		logger.Error("notify deliver: create queue failed; proactive delivery disabled", "gatewayType", gatewayType, "error", err)
		return
	}
	if err := brk.Subscribe(q, a2a.NotifyDeliverSubscription(g.Namespace(), gatewayType)); err != nil {
		logger.Error("notify deliver: subscribe failed; proactive delivery disabled", "gatewayType", gatewayType, "error", err)
		return
	}
	g.notifyCaps.Store(&a2a.NotifyCapabilities{
		ChannelType:  gatewayType,
		Notifiable:   true,
		Interactable: opts.Interactable,
		ApproverSets: opts.ApproverSets && opts.Interactable && reg.IsEnabled(features.KeyHILApproverSets),
	})
	go consumeNotifyDeliver(ctx, brk, q, logger, handle)
	logger.Info("proactive delivery enabled", "queue", queueName, "gatewayType", gatewayType)
}

func (g *GatewayCore) AdvertisedNotifyCapabilities() *a2a.NotifyCapabilities {
	return g.notifyCaps.Load()
}

func consumeNotifyDeliver(ctx context.Context, brk broker.Broker, q broker.Queue, logger *slog.Logger, handle func(a2a.NotifyDeliver)) {
	receiveNotifyMessages(ctx, brk, q, logger, func(msg *broker.Message) { dispatchNotifyDeliver(msg, logger, handle) })
}

func receiveNotifyMessages(ctx context.Context, brk broker.Broker, q broker.Queue, logger *slog.Logger, dispatch func(*broker.Message)) {
	const maxBackoff = 5 * time.Second
	backoff := 50 * time.Millisecond
	for {
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warn("notify: receive error; backing off", "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, maxBackoff)
			continue
		}
		backoff = 50 * time.Millisecond
		if msg == nil {
			continue
		}
		dispatch(msg)
	}
}

func dispatchNotifyDeliver(msg *broker.Message, logger *slog.Logger, handle func(a2a.NotifyDeliver)) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("notify deliver: panic handling command; acking", "panic", r)
			msg.Ack()
		}
	}()
	var d a2a.NotifyDeliver
	if err := json.Unmarshal(msg.Payload, &d); err != nil {
		logger.Error("notify deliver: malformed command; dropping", "error", err)
		msg.Ack()
		return
	}
	handle(d)
	msg.Ack()
}
