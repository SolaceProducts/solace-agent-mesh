// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"hash/fnv"
	"log/slog"
	"sync"
)

type QueueConfig struct {
	Capacity int
	Workers  int
	Key      func(RawMessage) string
	Handler  func(ctx context.Context, m RawMessage)
	Logger   *slog.Logger
}

type MessageQueue struct {
	cfg    QueueConfig
	chs    []chan RawMessage
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func NewMessageQueue(cfg QueueConfig) *MessageQueue {
	workers := max(cfg.Workers, 1)
	perWorker := max(cfg.Capacity/workers, 1)
	chs := make([]chan RawMessage, workers)
	for i := range chs {
		chs[i] = make(chan RawMessage, perWorker)
	}
	return &MessageQueue{cfg: cfg, chs: chs}
}

func (q *MessageQueue) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	q.cancel = cancel
	for _, ch := range q.chs {
		q.wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case m, ok := <-ch:
					if !ok {
						return
					}
					q.dispatch(ctx, m)
				}
			}
		})
	}
}

func (q *MessageQueue) Stop(_ context.Context) error {
	if q.cancel != nil {
		q.cancel()
	}
	q.wg.Wait()
	return nil
}

func (q *MessageQueue) Enqueue(ctx context.Context, m RawMessage) {
	select {
	case q.chs[q.worker(m)] <- m:
	case <-ctx.Done():
	}
}

func (q *MessageQueue) worker(m RawMessage) int {
	key := string(m.UID)
	if q.cfg.Key != nil {
		key = q.cfg.Key(m)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(len(q.chs)))
}

func (q *MessageQueue) dispatch(ctx context.Context, m RawMessage) {
	defer func() {
		if r := recover(); r != nil {
			q.cfg.Logger.Error("worker panic recovered",
				"eventType", "email.panic_recovered",
				"errorClass", "internal_bug",
				"msgUID", m.UID,
				"panic", r,
			)
		}
	}()
	q.cfg.Handler(ctx, m)
}
