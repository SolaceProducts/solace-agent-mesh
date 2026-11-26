// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type ModelConfigBootstrapListener struct {
	svc       *ModelConfigService
	broker    broker.Broker
	namespace string
	queue     broker.Queue
	cancel    context.CancelFunc
	logger    *slog.Logger
}

func NewModelConfigBootstrapListener(svc *ModelConfigService, brk broker.Broker, namespace string, logger *slog.Logger) *ModelConfigBootstrapListener {
	return &ModelConfigBootstrapListener{
		svc:       svc,
		broker:    brk,
		namespace: namespace,
		logger:    logger,
	}
}

func (l *ModelConfigBootstrapListener) Start(ctx context.Context) error {
	ctx, l.cancel = context.WithCancel(ctx)

	topic := a2a.ModelConfigBootstrapSubscription(l.namespace)
	queueName := a2a.QueueName(l.namespace, "platform", "model_config_bootstrap")

	queue, err := l.broker.CreateQueue(queueName, broker.QueueOptions{Named: true})
	if err != nil {
		return err
	}
	l.queue = queue

	if err := l.broker.Subscribe(queue, topic); err != nil {
		return err
	}

	go l.receiveLoop(ctx)

	l.logger.Info("model config bootstrap listener started", "topic", topic)
	return nil
}

func (l *ModelConfigBootstrapListener) Stop(ctx context.Context) error {
	if l.cancel != nil {
		l.cancel()
	}
	return nil
}

func (l *ModelConfigBootstrapListener) receiveLoop(ctx context.Context) {
	bo := backoff.Exponential{
		Initial: 100 * time.Millisecond,
		Max:     30 * time.Second,
		Jitter:  0.2,
	}
	for {
		msg, err := l.broker.Receive(ctx, l.queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				l.logger.Info("model config bootstrap receive loop exiting: queue no longer exists", "queue", l.queue.Name())
				return
			}
			l.logger.Debug("bootstrap receive error", "error", err, "backoff", bo.Current())
			if waitErr := bo.Wait(ctx); waitErr != nil {
				return
			}
			continue
		}
		bo.Reset()
		if msg == nil {
			continue
		}

		l.handleBootstrapRequest(ctx, msg)
		msg.Ack()
	}
}

type bootstrapRequest struct {
	ComponentID   string `json:"component_id"`
	ComponentType string `json:"component_type"`
	ReplyTo       string `json:"reply_to"`
	ModelID       string `json:"model_id"`
}

func (l *ModelConfigBootstrapListener) accepts(req bootstrapRequest) bool {
	sizes := []any{"modelIDLength", len(req.ModelID), "componentIDLength", len(req.ComponentID)}
	switch {
	case req.ModelID == "":
		l.logger.Warn("bootstrap request rejected: missing model_id", sizes...)
	case req.ComponentID == "":
		l.logger.Warn("bootstrap request rejected: missing component_id", sizes...)
	case req.ReplyTo != a2a.ModelConfigBootstrapResponseTopic(l.namespace, req.ModelID, req.ComponentID):
		l.logger.Warn("bootstrap request rejected: reply_to is not the bootstrap response topic", sizes...)
	default:
		return true
	}
	return false
}

func (l *ModelConfigBootstrapListener) handleBootstrapRequest(ctx context.Context, msg *broker.Message) {
	var req bootstrapRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		l.logger.Warn("invalid bootstrap request payload", "error", err)
		return
	}

	if !l.accepts(req) {
		return
	}

	l.logger.Debug("handling bootstrap request",
		"modelID", req.ModelID,
		"componentID", req.ComponentID,
		"replyTo", req.ReplyTo,
	)

	rawConfig, err := l.svc.GetByAliasOrIDRaw(ctx, req.ModelID)
	if err != nil {
		l.logger.Warn("failed to look up model config for bootstrap", "modelID", req.ModelID, "error", err)
		return
	}

	data, err := json.Marshal(map[string]any{"model_config": samapp.ForPythonAgent(rawConfig)})
	if err != nil {
		l.logger.Warn("failed to marshal bootstrap response", "error", err)
		return
	}

	responseMsg := &broker.Message{
		Topic:   req.ReplyTo,
		Payload: data,
	}
	if err := l.broker.Publish(ctx, req.ReplyTo, responseMsg); err != nil {
		l.logger.Error("failed to publish bootstrap response", "topic", req.ReplyTo, "error", err)
	}
}
