// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

var notifyResolveDeadline = 4 * time.Second

const notifyResolveClockGrace = 15 * time.Second

func (g *GatewayCore) StartNotifyResolveConsumer(ctx context.Context, gatewayType string, logger *slog.Logger, resolve func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse) {
	if g == nil || g.svc == nil {
		return
	}
	caps := g.notifyCaps.Load()
	if caps == nil || caps.ChannelType != gatewayType {
		return
	}
	brk := g.svc.Broker()
	if brk == nil {
		return
	}
	queueName := a2a.QueueName(g.Namespace(), "gdk", "notify_resolve", gatewayType)
	topic := a2a.NotifyResolveSubscription(g.Namespace(), gatewayType)
	opts := broker.QueueOptions{Named: true, Exclusive: false, MaxRedelivery: 3}
	if err := broker.ProvisionQueue(ctx, brk, queueName, opts, topic); err != nil {
		logger.Error("notify resolve: provision queue failed; address resolution disabled", "gatewayType", gatewayType, "error", err)
		return
	}
	q, err := brk.CreateQueue(queueName, opts)
	if err != nil {
		logger.Error("notify resolve: create queue failed; address resolution disabled", "gatewayType", gatewayType, "error", err)
		return
	}
	if err := brk.Subscribe(q, topic); err != nil {
		logger.Error("notify resolve: subscribe failed; address resolution disabled", "gatewayType", gatewayType, "error", err)
		return
	}
	g.advertiseResolvesAddress()
	go receiveNotifyMessages(ctx, brk, q, logger, func(msg *broker.Message) {
		g.answerNotifyResolve(ctx, brk, msg, logger, resolve)
	})
	logger.Info("notify address resolution enabled", "queue", queueName, "gatewayType", gatewayType)
}

func (g *GatewayCore) NotifyResolverBound() bool {
	caps := g.notifyCaps.Load()
	return caps != nil && caps.ResolvesAddress
}

func (g *GatewayCore) advertiseResolvesAddress() {
	for {
		caps := g.notifyCaps.Load()
		withResolver := *caps
		withResolver.ResolvesAddress = true
		if g.notifyCaps.CompareAndSwap(caps, &withResolver) {
			return
		}
	}
}

func handBack(message, requestID, correlationID string, logger *slog.Logger, redeliver *bool) {
	logger.Info(message, "requestID", requestID, "correlationID", correlationID)
	*redeliver = true
}

func (g *GatewayCore) answerNotifyResolve(ctx context.Context, brk broker.Broker, msg *broker.Message, logger *slog.Logger, resolve func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse) {
	redeliver := false
	defer func() {
		if redeliver {
			msg.Nack()
			return
		}
		msg.Ack()
	}()
	if !a2a.ValidNotifyResolveReplyTopic(g.Namespace(), msg.ReplyTo) {
		logger.Warn("notify resolve: request names a reply topic outside the resolve reply prefix; dropping", "correlationID", msg.CorrelationID)
		return
	}
	var requestID string
	reply := func(resp a2a.NotifyResolveResponse) {
		payload, err := json.Marshal(resp)
		if err != nil {
			logger.Error("notify resolve: marshal response failed", "requestID", requestID, "error", err)
			return
		}
		if err := brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: payload, CorrelationID: msg.CorrelationID}); err != nil {
			logger.Warn("notify resolve: publish response failed", "requestID", requestID, "error", err)
		}
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Error("notify resolve: panic handling request; acking", "requestID", requestID, "panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
			reply(a2a.NotifyResolveResponse{Error: a2a.NotifyResolveUnavailable})
		}
	}()
	var req a2a.NotifyResolveRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil || req.Email == "" {
		logger.Warn("notify resolve: malformed request; answering unavailable", "correlationID", msg.CorrelationID, "decodeFailed", err != nil, "error", err)
		reply(a2a.NotifyResolveResponse{Error: a2a.NotifyResolveUnavailable})
		return
	}
	requestID = req.RequestID
	if req.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, req.ExpiresAt)
		switch {
		case err != nil:
			logger.Warn("notify resolve: request has an unreadable expires_at; treating it as none", "requestID", requestID, "length", len(req.ExpiresAt))
		case time.Since(expiresAt) > notifyResolveClockGrace:
			logger.Warn("notify resolve: request waited in the queue past its deadline; dropping", "requestID", requestID, "correlationID", msg.CorrelationID, "overdue", time.Since(expiresAt))
			return
		}
	}
	if ctx.Err() != nil {
		handBack("notify resolve: gateway is stopping; handing the request back", requestID, msg.CorrelationID, logger, &redeliver)
		return
	}
	lookupCtx, cancel := context.WithTimeout(ctx, notifyResolveDeadline)
	defer cancel()
	resp := resolve(lookupCtx, req)
	if ctx.Err() != nil {
		handBack("notify resolve: gateway stopped during the lookup; handing the request back", requestID, msg.CorrelationID, logger, &redeliver)
		return
	}
	reply(resp)
}
