// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const notificationCreatedEvent = "notification_created"

type notificationCreated struct {
	ID string `json:"id"`
}

type inboxWatch struct {
	brk    broker.Broker
	sse    *SSEManager
	logger *slog.Logger
	retry  backoff.Exponential
	topic  string
	name   string
	push   func(payload []byte)
	origin string

	stopping          atomic.Bool
	warnedUndecodable atomic.Bool
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	sub               broker.Queue
}

func newInboxWatch(brk broker.Broker, namespace string, sse *SSEManager, logger *slog.Logger) *inboxWatch {
	w := newTopicWatch(brk, sse, logger, a2a.NotifyInboxCreatedTopic(namespace), "notify inbox created")
	w.push = w.handle
	return w
}

func newUserEventWatch(brk broker.Broker, namespace, origin string, sse *SSEManager, logger *slog.Logger) *inboxWatch {
	w := newTopicWatch(brk, sse, logger, a2a.UserEventTopic(namespace), "user event")
	w.origin = origin
	w.push = w.handleUserEvent
	return w
}

func newTopicWatch(brk broker.Broker, sse *SSEManager, logger *slog.Logger, topic, name string) *inboxWatch {
	return &inboxWatch{
		brk: brk, sse: sse, logger: logger, topic: topic, name: name,
		retry: backoff.Exponential{Initial: time.Second, Max: time.Minute, Jitter: 0.2},
	}
}

func (w *inboxWatch) start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.wg.Go(func() { w.run(loopCtx) })
}

func (w *inboxWatch) run(ctx context.Context) {
	for attempt := 1; ; attempt++ {
		sub, err := w.subscribe()
		if err == nil {
			w.sub = sub
			if attempt > 1 {
				w.logger.Info("notification stream watch subscribed", "watch", w.name, "attempt", attempt)
			}
			break
		}
		w.logger.Warn("notification stream watch could not subscribe; until it does, open web UIs on this gateway miss the events another gateway sends and refresh only on refocus or reconnect",
			"watch", w.name, "error", err, "attempt", attempt, "retryIn", w.retry.Current())
		if w.retry.Wait(ctx) != nil {
			return
		}
	}
	broker.RunReceiveLoop(ctx, w.brk, w.sub, w.logger,
		broker.ReceiveLoopOptions{Teardown: w.stopping.Load, LoopName: w.name},
		func(msg *broker.Message) {
			msg.Ack()
			w.push(msg.Payload)
		})
}

func (w *inboxWatch) subscribe() (broker.Queue, error) {
	sub, err := w.brk.CreateDirectSubscription()
	if err != nil {
		return nil, fmt.Errorf("create %s subscription: %w", w.name, err)
	}
	if err := w.brk.Subscribe(sub, w.topic); err != nil {
		if delErr := w.brk.DeleteQueue(sub.Name()); delErr != nil {
			w.logger.Warn("notification stream watch subscription cleanup after failed subscribe", "watch", w.name, "error", delErr)
		}
		return nil, fmt.Errorf("subscribe to %s: %w", w.name, err)
	}
	return sub, nil
}

func (w *inboxWatch) handle(payload []byte) {
	var created a2a.NotifyInboxCreated
	err := json.Unmarshal(payload, &created)
	if err == nil {
		err = uuid.Validate(created.NotificationID)
	}
	if err != nil {
		w.dropUndecodable(err, len(payload))
		return
	}
	keys := []string{
		created.RecipientUserID,
		strings.ToLower(strings.TrimSpace(created.RecipientUserID)),
		strings.ToLower(created.RecipientPlatformUserID),
	}
	if err := w.sse.SendUserNotificationToKeys(keys, notificationCreatedEvent, notificationCreated{ID: created.NotificationID}); err != nil {
		w.logger.Warn("notification created event could not be sent", "error", err)
	}
}

func (w *inboxWatch) dropUndecodable(err error, payloadBytes int) {
	const msg = "notification stream message could not be decoded; dropped"
	if w.warnedUndecodable.CompareAndSwap(false, true) {
		w.logger.Warn(msg, "watch", w.name, "error", err, "payloadBytes", payloadBytes)
		return
	}
	w.logger.Debug(msg, "watch", w.name, "error", err, "payloadBytes", payloadBytes)
}

func (w *inboxWatch) stop() {
	w.stopping.Store(true)
	if w.cancel == nil {
		return
	}
	w.cancel()
	w.wg.Wait()
	if w.sub == nil {
		return
	}
	if err := w.brk.Unsubscribe(w.sub, w.topic); err != nil {
		w.logger.Warn("notification stream watch unsubscribe failed", "watch", w.name, "error", err)
	}
	if err := w.brk.DeleteQueue(w.sub.Name()); err != nil {
		w.logger.Warn("notification stream watch subscription cleanup failed", "watch", w.name, "error", err)
	}
}

func (w *inboxWatch) handleUserEvent(payload []byte) {
	var evt a2a.UserEvent
	err := json.Unmarshal(payload, &evt)
	if err == nil && (evt.Origin == "" || evt.Event == "" || len(evt.Keys) == 0) {
		err = errUserEventIncomplete
	}
	if err != nil {
		w.dropUndecodable(err, len(payload))
		return
	}
	if evt.Origin == w.origin {
		return
	}
	w.sse.sendUserNotificationJSON(evt.Keys, evt.Event, string(evt.Data))
}
