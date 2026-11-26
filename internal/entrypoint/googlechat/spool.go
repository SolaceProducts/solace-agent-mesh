// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	spoolBudget      = 5 * time.Second
	spoolReceivePoll = 30 * time.Second
	spoolBindProbe   = 50 * time.Millisecond
)

func ingressTopic(namespace, gatewayID string) string {
	return fmt.Sprintf("%s/sam/v1/gdk/googlechat/ingress/%s", namespace, gatewayID)
}

type brokerSpool struct {
	brk    broker.Broker
	queue  broker.Queue
	topic  string
	logger *slog.Logger
	ctx    context.Context

	down    atomic.Bool
	next    atomic.Int64
	delayed sync.WaitGroup

	mu       sync.Mutex
	inflight map[string]*broker.Message
	pending  *broker.Message
}

func openBrokerSpool(ctx context.Context, brk broker.Broker, namespace, gatewayID string, logger *slog.Logger) (*brokerSpool, error) {
	name := a2a.GoogleChatIngressQueueName(namespace, gatewayID)
	queue, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
	if err != nil {
		return nil, fmt.Errorf("google_chat adapter: create ingress queue %q: %w", name, err)
	}
	topic := ingressTopic(namespace, gatewayID)
	if err := brk.Subscribe(queue, topic); err != nil {
		return nil, fmt.Errorf("google_chat adapter: subscribe ingress queue to %q: %w", topic, err)
	}
	s := &brokerSpool{brk: brk, queue: queue, topic: topic, logger: logger, ctx: ctx, inflight: map[string]*broker.Message{}}
	s.probe()
	return s, nil
}

func (s *brokerSpool) probe() {
	ctx, cancel := context.WithTimeout(s.ctx, spoolBindProbe)
	defer cancel()
	msg, err := s.brk.Receive(ctx, s.queue)
	switch {
	case err == nil && msg != nil:
		s.pending = msg
	case err == nil, errors.Is(err, context.DeadlineExceeded), s.ctx.Err() != nil:
	default:
		s.down.Store(true)
		s.logger.Error("google chat ingress queue did not bind; refusing HTTPS events until it does", "queue", s.queue.Name(), "error", err)
	}
}

func (s *brokerSpool) Publish(ctx context.Context, payload []byte, senderName string) error {
	if s.down.Load() {
		return errors.New("google chat ingress consumer is not receiving")
	}
	publishCtx, cancel := context.WithTimeout(ctx, spoolBudget)
	defer cancel()
	return s.brk.PublishGuaranteed(publishCtx, s.topic, &broker.Message{
		Topic:          s.topic,
		Payload:        payload,
		UserProperties: map[string]string{"senderId": senderName},
	})
}

func (s *brokerSpool) Pull(ctx context.Context, _ int) ([]pulledMessage, error) {
	s.mu.Lock()
	msg := s.pending
	s.pending = nil
	s.mu.Unlock()
	if msg == nil {
		receiveCtx, cancel := context.WithTimeout(ctx, spoolReceivePoll)
		defer cancel()
		var err error
		msg, err = s.brk.Receive(receiveCtx, s.queue)
		switch {
		case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
			s.down.Store(false)
			return nil, nil
		case err != nil:
			s.down.Store(true)
			return nil, fmt.Errorf("receive from %s: %w", s.queue.Name(), err)
		case msg == nil:
			return nil, nil
		}
	}
	s.down.Store(false)
	id := strconv.FormatInt(s.next.Add(1), 10)
	s.mu.Lock()
	s.inflight[id] = msg
	s.mu.Unlock()
	return []pulledMessage{{AckID: id, MessageID: id, Data: msg.Payload}}, nil
}

func (s *brokerSpool) take(id string) *broker.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := s.inflight[id]
	delete(s.inflight, id)
	return msg
}

func (s *brokerSpool) Ack(_ context.Context, ackIDs []string) error {
	for _, id := range ackIDs {
		if msg := s.take(id); msg != nil {
			msg.Ack()
		}
	}
	return nil
}

func (s *brokerSpool) Extend(context.Context, []string, time.Duration) error { return nil }

func (s *brokerSpool) Nack(_ context.Context, ackIDs []string, delay time.Duration) error {
	var msgs []*broker.Message
	for _, id := range ackIDs {
		if msg := s.take(id); msg != nil {
			msgs = append(msgs, msg)
		}
	}
	if delay <= 0 {
		nackAll(msgs)
		return nil
	}
	s.delayed.Go(func() {
		sleepCtx(s.ctx, delay)
		nackAll(msgs)
	})
	return nil
}

func nackAll(msgs []*broker.Message) {
	for _, msg := range msgs {
		msg.Nack()
	}
}

func (s *brokerSpool) waitDelayed() { s.delayed.Wait() }
