// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type STRRuntimeListener struct {
	registry  *STRRuntimeRegistry
	broker    broker.Broker
	namespace string
	logger    *slog.Logger

	stopping atomic.Bool
	sub      broker.Queue
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewSTRRuntimeListener(registry *STRRuntimeRegistry, brk broker.Broker, namespace string, logger *slog.Logger) *STRRuntimeListener {
	return &STRRuntimeListener{
		registry:  registry,
		broker:    brk,
		namespace: namespace,
		logger:    logger.With("logger_name", "str-runtime-listener"),
	}
}

func (l *STRRuntimeListener) topics() []string {
	return []string{remotetool.InitSubscription(l.namespace), remotetool.FailedDiscoverySubscription(l.namespace)}
}

func (l *STRRuntimeListener) Start(ctx context.Context) error {
	sub, err := l.broker.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create STR runtime subscription: %w", err)
	}
	for _, topic := range l.topics() {
		if err := l.broker.Subscribe(sub, topic); err != nil {
			if delErr := l.broker.DeleteQueue(sub.Name()); delErr != nil {
				l.logger.Warn("STR runtime subscription cleanup after failed subscribe", "error", delErr)
			}
			return fmt.Errorf("subscribe to %s: %w", topic, err)
		}
	}

	loopCtx, cancel := context.WithCancel(ctx)
	l.sub, l.cancel = sub, cancel
	l.wg.Go(func() {
		broker.RunReceiveLoop(loopCtx, l.broker, sub, l.logger,
			broker.ReceiveLoopOptions{Teardown: l.stopping.Load, LoopName: "strRuntime"},
			func(msg *broker.Message) {
				msg.Ack()
				l.observe(msg.Payload)
			})
	})
	l.logger.Info("STR runtime listener started")
	return nil
}

type strNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (l *STRRuntimeListener) observe(payload []byte) {
	var env strNotification
	if err := json.Unmarshal(payload, &env); err != nil {
		l.logger.Debug("STR runtime listener: invalid JSON-RPC message", "error", err)
		return
	}
	switch env.Method {
	case remotetool.MethodInit:
		var params remotetool.InitParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			l.logger.Debug("STR runtime listener: invalid tool init params", "error", err)
			return
		}
		l.registry.Observe(params.RuntimeOS, params.RuntimeArch, params.RuntimePythonVersion)
	case remotetool.MethodFailedDiscovery:
		var params remotetool.FailedDiscoveryParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			l.logger.Debug("STR runtime listener: invalid failed-discovery params", "error", err)
			return
		}
		l.registry.Observe(params.RuntimeOS, params.RuntimeArch, params.RuntimePythonVersion)
	}
}

func (l *STRRuntimeListener) Stop(context.Context) error {
	l.stopping.Store(true)
	if l.cancel == nil {
		return nil
	}
	l.cancel()
	l.wg.Wait()
	for _, topic := range l.topics() {
		if err := l.broker.Unsubscribe(l.sub, topic); err != nil {
			l.logger.Warn("STR runtime unsubscribe failed", "topic", topic, "error", err)
		}
	}
	if err := l.broker.DeleteQueue(l.sub.Name()); err != nil {
		l.logger.Warn("STR runtime subscription cleanup failed", "error", err)
	}
	return nil
}
