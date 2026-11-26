// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

const (
	presenceMaxRedelivery = 1
	presenceSpoolQuotaMB  = 256
)

func (n *NotifyCore) startPresenceSubscriber(loopCtx context.Context) error {
	brk := n.svc.Broker()
	queueName := a2a.QueueName(n.cfg.Namespace, "notify", "presence")
	q, err := brk.CreateQueue(queueName, broker.QueueOptions{
		Named:         true,
		Exclusive:     false,
		MaxRedelivery: presenceMaxRedelivery,
		QuotaMB:       presenceSpoolQuotaMB,
	})
	if err != nil {
		return fmt.Errorf("create notify presence queue %q: %w", queueName, err)
	}
	if err := brk.Subscribe(q, a2a.NotifyPresenceSubscription(n.cfg.Namespace)); err != nil {
		return fmt.Errorf("subscribe notify presence: %w", err)
	}
	n.wg.Go(func() { n.presenceLoop(loopCtx, q) })
	n.logger.Info("notify presence subscriber started", "presenceQueue", queueName)
	return nil
}

func (n *NotifyCore) presenceLoop(ctx context.Context, q broker.Queue) {
	const maxBackoff = 5 * time.Second
	backoff := 50 * time.Millisecond
	for {
		msg, err := n.svc.Broker().Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			n.logger.Warn("notify: presence receive error; backing off", "error", err, "backoff", backoff)
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
		n.handlePresence(ctx, msg)
	}
}

func (n *NotifyCore) handlePresence(ctx context.Context, msg *broker.Message) {
	defer func() {
		if r := recover(); r != nil {
			n.logger.Error("notify: panic handling presence; acking to avoid poison-pill redelivery",
				"panic", r, "topic", msg.Topic)
			msg.Ack()
		}
	}()

	var p a2a.NotifyPresence
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		n.logger.Error("notify: malformed presence event; dropping", "error", err, "topic", msg.Topic)
		msg.Ack()
		return
	}
	if p.UserID == "" || p.ChannelType == "" || len(p.Address) == 0 || string(p.Address) == "null" {
		n.logger.Warn("notify: presence event missing user_id/channel_type/address; dropping", "topic", msg.Topic)
		msg.Ack()
		return
	}
	if !gatewayDeliverable(p.ChannelType) {
		n.logger.Warn("notify: presence event names a channel type no gateway can deliver to; dropping",
			"topic", msg.Topic, "channelTypeLength", len(p.ChannelType))
		msg.Ack()
		return
	}
	if n.resolvesAddress(p.ChannelType) {
		n.logger.Debug("notify: presence event for a channel whose address comes from the login email; dropping",
			"topic", msg.Topic, "channelType", p.ChannelType)
		msg.Ack()
		return
	}

	uc := store.UserChannel{
		UserID:      p.UserID,
		ChannelType: p.ChannelType,
		Address:     string(p.Address),
		Email:       p.Email,
		GatewayID:   p.GatewayID,
	}
	if err := n.channels.Upsert(ctx, uc); err != nil {
		n.logger.Warn("notify: failed to upsert presence; will relearn on next inbound message",
			"userID", p.UserID, "channelType", p.ChannelType, "error", err)
		msg.Ack()
		return
	}
	n.logger.Debug("notify: presence recorded", "userID", p.UserID, "channelType", p.ChannelType, "gatewayID", p.GatewayID)
	msg.Ack()
}
