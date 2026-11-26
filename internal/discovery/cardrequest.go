// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const maxCardBatchBytes = 1 << 20

const CardRequestWindow = 10 * time.Second

type reportedCard struct {
	Card       json.RawMessage `json:"card"`
	InstanceID string          `json:"instance_id,omitempty"`
	LastSeenMs int64           `json:"last_seen_ms"`
}

type cardBatch struct {
	Cards []reportedCard `json:"cards"`
}

type ReportedCard struct {
	Card       a2a.AgentCard
	InstanceID string
	LastSeen   time.Time
}

func AnswerCardRequests(ctx context.Context, brk broker.Broker, namespace string, reg *Registry, logger *slog.Logger) error {
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create card request subscription: %w", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentCardRequestTopic(namespace)); err != nil {
		_ = brk.DeleteQueue(queue.Name())
		return fmt.Errorf("subscribe card requests: %w", err)
	}
	go func() {
		defer func() { _ = brk.DeleteQueue(queue.Name()) }()
		for {
			msg, err := brk.Receive(ctx, queue)
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				logger.Warn("card request subscription is gone; requests are no longer answered", "error", err)
				return
			}
			if err != nil || msg == nil {
				continue
			}
			msg.Ack()
			if msg.ReplyTo == "" {
				continue
			}
			for _, batch := range cardBatches(reg) {
				if err := brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: batch, CorrelationID: msg.CorrelationID}); err != nil {
					logger.Warn("card request reply failed", "error", err)
					break
				}
			}
		}
	}()
	return nil
}

func cardBatches(reg *Registry) [][]byte {
	var batches [][]byte
	var current []reportedCard
	size := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		if payload, err := json.Marshal(cardBatch{Cards: current}); err == nil {
			batches = append(batches, payload)
		}
		current, size = nil, 0
	}
	reg.EachInstance(func(card *a2a.AgentCard, instanceID string, lastSeen time.Time) {
		raw, err := json.Marshal(card)
		if err != nil {
			return
		}
		if size > 0 && size+len(raw) > maxCardBatchBytes {
			flush()
		}
		current = append(current, reportedCard{Card: raw, InstanceID: instanceID, LastSeenMs: lastSeen.UnixMilli()})
		size += len(raw)
	})
	flush()
	return batches
}

func RequestCards(ctx context.Context, brk broker.Broker, namespace string, window, maxAge time.Duration, load func(ReportedCard)) error {
	replyTo := fmt.Sprintf("%s/reply/%s", a2a.AgentCardRequestTopic(namespace), idgen.New())
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create card reply subscription: %w", err)
	}
	if err := brk.Subscribe(queue, replyTo); err != nil {
		_ = brk.DeleteQueue(queue.Name())
		return fmt.Errorf("subscribe card replies: %w", err)
	}
	if err := brk.Publish(ctx, a2a.AgentCardRequestTopic(namespace), &broker.Message{ReplyTo: replyTo}); err != nil {
		_ = brk.DeleteQueue(queue.Name())
		return fmt.Errorf("publish card request: %w", err)
	}
	go func() {
		defer func() { _ = brk.DeleteQueue(queue.Name()) }()
		waitCtx, cancel := context.WithTimeout(ctx, window)
		defer cancel()
		for {
			msg, err := brk.Receive(waitCtx, queue)
			if waitCtx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				return
			}
			if err != nil || msg == nil {
				continue
			}
			loadBatch(msg.Payload, func(rc ReportedCard) {
				now := time.Now()
				if rc.LastSeen.After(now) {
					rc.LastSeen = now
				}
				if now.Sub(rc.LastSeen) <= maxAge {
					load(rc)
				}
			})
		}
	}()
	return nil
}

func loadBatch(payload []byte, load func(ReportedCard)) {
	var batch cardBatch
	if json.Unmarshal(payload, &batch) != nil {
		return
	}
	for _, rc := range batch.Cards {
		if len(rc.Card) > maxCardBytes || len(rc.InstanceID) > maxInstanceIDSize {
			continue
		}
		var card a2a.AgentCard
		if json.Unmarshal(rc.Card, &card) != nil || card.Name == "" {
			continue
		}
		load(ReportedCard{Card: card, InstanceID: rc.InstanceID, LastSeen: time.UnixMilli(rc.LastSeenMs)})
	}
}
