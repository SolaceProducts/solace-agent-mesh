// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bindings

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type Watcher struct {
	registry  *BindingsRegistry
	broker    broker.Broker
	namespace string
	logger    *slog.Logger

	stopping atomic.Bool
	sub      broker.Queue
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

type modelChangePayload struct {
	ModelID       string `json:"model_id"`
	Alias         string `json:"alias"`
	PreviousAlias string `json:"previous_alias"`
}

func NewWatcher(reg *BindingsRegistry, brk broker.Broker, namespace string, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		registry:  reg,
		broker:    brk,
		namespace: namespace,
		logger:    logger.With("logger_name", "model-bindings-watcher"),
	}
}

func (w *Watcher) Init(context.Context, config.Config) error { return nil }

func (w *Watcher) Start(ctx context.Context) error {
	if w.broker == nil || w.registry == nil {
		return nil
	}
	topic := a2a.ModelConfigUpdateTopic(w.namespace, ">")
	sub, err := w.broker.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create model change subscription: %w", err)
	}
	if err := w.broker.Subscribe(sub, topic); err != nil {
		if delErr := w.broker.DeleteQueue(sub.Name()); delErr != nil {
			w.logger.Warn("model change subscription cleanup after failed subscribe", "error", delErr)
		}
		return fmt.Errorf("subscribe to model changes: %w", err)
	}

	loopCtx, cancel := context.WithCancel(ctx)
	w.sub, w.cancel = sub, cancel
	w.wg.Go(func() {
		broker.RunReceiveLoop(loopCtx, w.broker, sub, w.logger,
			broker.ReceiveLoopOptions{Teardown: w.stopping.Load, LoopName: "modelBindings"},
			func(msg *broker.Message) {
				msg.Ack()
				w.handle(msg)
			})
	})
	w.logger.Info("watching model changes", "topic", topic)
	return nil
}

func (w *Watcher) handle(msg *broker.Message) {
	var change modelChangePayload
	if err := json.Unmarshal(msg.Payload, &change); err != nil || change.ModelID == "" {
		w.logger.Debug("model change message ignored", "topic", msg.Topic, "error", err)
		return
	}
	if msg.Topic != a2a.ModelConfigUpdateTopic(w.namespace, change.ModelID) &&
		msg.Topic != a2a.ModelConfigUpdateTopic(w.namespace, change.Alias) {
		w.logger.Debug("model change message ignored", "topic", msg.Topic)
		return
	}
	for _, key := range []string{change.ModelID, change.Alias, change.PreviousAlias} {
		w.registry.Notify(key)
	}
}

func (w *Watcher) Stop(context.Context) error {
	w.stopping.Store(true)
	if w.cancel != nil {
		w.cancel()
		w.wg.Wait()
		if err := w.broker.Unsubscribe(w.sub, a2a.ModelConfigUpdateTopic(w.namespace, ">")); err != nil {
			w.logger.Warn("model change unsubscribe failed", "error", err)
		}
		if err := w.broker.DeleteQueue(w.sub.Name()); err != nil {
			w.logger.Warn("model change subscription cleanup failed", "error", err)
		}
	}
	return nil
}

func (w *Watcher) Health() error { return nil }
