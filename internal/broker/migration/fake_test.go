// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

var (
	errTransient = errors.New("not connected")
	errRefused   = fmt.Errorf("refused: %w", broker.ErrPermissionDenied)
	errMissing   = fmt.Errorf("unknown queue: %w", broker.ErrQueueNotFound)
)

type fakeBroker struct {
	broker.Broker
	bindErr, removeErr, deleteErr, receiveErr error
	messages                                  []string
	calls                                     []string
	settled                                   []string
}

func (f *fakeBroker) BindExisting(name string) (broker.BoundQueue, error) {
	f.calls = append(f.calls, "bind "+name)
	if f.bindErr != nil {
		return nil, f.bindErr
	}
	return &fakeBound{f: f}, nil
}

func (f *fakeBroker) ConsumeExisting(_ string) (broker.Queue, error) {
	return nil, errors.ErrUnsupported
}

func (f *fakeBroker) DeprovisionQueue(name string) error {
	f.calls = append(f.calls, "delete "+name)
	return f.deleteErr
}

type fakeBound struct{ f *fakeBroker }

func (q *fakeBound) RemoveSubscription(topic string) error {
	q.f.calls = append(q.f.calls, "unsubscribe "+topic)
	return q.f.removeErr
}

func (q *fakeBound) Receive(ctx context.Context) (*broker.Message, error) {
	q.f.calls = append(q.f.calls, "receive")
	if q.f.receiveErr != nil {
		return nil, q.f.receiveErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(q.f.messages) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	payload := q.f.messages[0]
	q.f.messages = q.f.messages[1:]
	return broker.NewDeliveredMessage(payload, "ns/t", []byte(payload),
		func() { q.f.settled = append(q.f.settled, "ack "+payload) },
		func() {
			q.f.settled = append(q.f.settled, "nack "+payload)
			q.f.messages = append([]string{payload}, q.f.messages...)
		},
	), nil
}

func (q *fakeBound) Close() { q.f.calls = append(q.f.calls, "close") }

type noBinderBroker struct{ broker.Broker }

func captureLogs() (*slog.Logger, func() []string) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, func() []string {
		var levels []string
		for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var rec struct{ Level string }
			_ = json.Unmarshal([]byte(line), &rec)
			levels = append(levels, rec.Level)
		}
		return levels
	}
}
