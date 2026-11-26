// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type ResourceEventHandler interface {
	OnToolInit(ctx context.Context, params remotetool.InitParams)
	OnToolRemoved(ctx context.Context, toolName string)
	OnToolFailed(ctx context.Context, params remotetool.FailedDiscoveryParams)
	OnSkillInit(ctx context.Context, params remotetool.SkillInitParams)
	OnSkillRemoved(ctx context.Context, skillName string)
}

type ResourceTracker struct {
	brk       broker.Broker
	queue     broker.Queue
	namespace string
	handler   ResourceEventHandler
	logger    *slog.Logger
	cancel    context.CancelFunc
}

func NewResourceTracker(brk broker.Broker, namespace string, handler ResourceEventHandler, logger *slog.Logger) *ResourceTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResourceTracker{
		brk:       brk,
		namespace: namespace,
		handler:   handler,
		logger:    logger.With("logger_name", "resource-tracker"),
	}
}

func (rt *ResourceTracker) Start(ctx context.Context) error {
	var err error
	rt.queue, err = rt.brk.CreateQueue(a2a.QueueName(rt.namespace, "platform", "resource_tracker"), broker.QueueOptions{
		Named:         true,
		Exclusive:     true,
		MaxRedelivery: 5,
	})
	if err != nil {
		return fmt.Errorf("create resource tracker queue: %w", err)
	}

	subs := []string{
		remotetool.InitSubscription(rt.namespace),
		remotetool.RemovedSubscription(rt.namespace),
		remotetool.FailedDiscoverySubscription(rt.namespace),
		remotetool.SkillInitSubscription(rt.namespace),
		remotetool.SkillRemovedSubscription(rt.namespace),
	}
	for _, sub := range subs {
		if subErr := rt.brk.Subscribe(rt.queue, sub); subErr != nil {
			return fmt.Errorf("subscribe %q: %w", sub, subErr)
		}
		rt.logger.Debug("resource tracker subscribed", "topic", sub)
	}

	loopCtx, cancel := context.WithCancel(ctx)
	rt.cancel = cancel

	go rt.receiveLoop(loopCtx)

	return nil
}

func (rt *ResourceTracker) Stop(_ context.Context) error {
	if rt.cancel != nil {
		rt.cancel()
	}
	if rt.queue != nil {
		_ = rt.brk.DeleteQueue(rt.queue.Name())
	}
	return nil
}

type jsonRPCEnvelope struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (rt *ResourceTracker) receiveLoop(ctx context.Context) {
	bo := backoff.Exponential{
		Initial: 100 * time.Millisecond,
		Max:     30 * time.Second,
		Jitter:  0.2,
	}
	for {
		msg, err := rt.brk.Receive(ctx, rt.queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				rt.logger.Info("resource tracker receive loop exiting: queue no longer exists", "queue", rt.queue.Name())
				return
			}
			rt.logger.Warn("resource tracker receive error", "error", err, "backoff", bo.Current())
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

		rt.handleMessage(ctx, msg)
		msg.Ack()
	}
}

func (rt *ResourceTracker) handleMessage(ctx context.Context, msg *broker.Message) {
	var env jsonRPCEnvelope
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		rt.logger.Warn("resource tracker: invalid JSON-RPC message", "error", err)
		return
	}

	switch env.Method {
	case remotetool.MethodInit:
		var params remotetool.InitParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			rt.logger.Warn("resource tracker: invalid tool init params", "error", err)
			return
		}
		rt.handler.OnToolInit(ctx, params)

	case remotetool.MethodRemoved:
		var params remotetool.RemovedParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			rt.logger.Warn("resource tracker: invalid tool removed params", "error", err)
			return
		}
		rt.handler.OnToolRemoved(ctx, params.ToolName)

	case remotetool.MethodFailedDiscovery:
		var params remotetool.FailedDiscoveryParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			rt.logger.Warn("resource tracker: invalid failed-discovery params", "error", err)
			return
		}
		rt.handler.OnToolFailed(ctx, params)

	case remotetool.MethodSkillInit:
		var params remotetool.SkillInitParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			rt.logger.Warn("resource tracker: invalid skill init params", "error", err)
			return
		}
		rt.handler.OnSkillInit(ctx, params)

	case remotetool.MethodSkillRemoved:
		var params remotetool.SkillRemovedParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			rt.logger.Warn("resource tracker: invalid skill removed params", "error", err)
			return
		}
		rt.handler.OnSkillRemoved(ctx, params.SkillName)

	default:
	}
}
