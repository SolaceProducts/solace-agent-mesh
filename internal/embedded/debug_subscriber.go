// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type debugSubscriber struct {
	brk      broker.Broker
	topics   []string
	filePath string
	logger   *slog.Logger

	queue    broker.Queue
	cancel   context.CancelFunc
	done     chan struct{}
	sinkFile *os.File
	sinkMu   sync.Mutex
}

func newDebugSubscriber(brk broker.Broker, topics []string, filePath string, logger *slog.Logger) *debugSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &debugSubscriber{
		brk:      brk,
		topics:   topics,
		filePath: filePath,
		logger:   logger.With("logger_name", "debug-subscribe"),
	}
}

func (d *debugSubscriber) Init(_ context.Context, _ config.Config) error { return nil }

func (d *debugSubscriber) Start(ctx context.Context) error {
	if d.filePath != "" {
		info, err := os.Lstat(d.filePath)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("debug subscriber: refusing symlink sink %q", d.filePath)
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("debug subscriber: inspect sink file %q: %w", d.filePath, err)
		}
		f, err := os.OpenFile(d.filePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("debug subscriber: create sink file %q: %w", d.filePath, err)
		}
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close()
			return fmt.Errorf("debug subscriber: secure sink file %q: %w", d.filePath, err)
		}
		d.sinkFile = f
		d.logger.Info("writing events", "file", d.filePath)
	}

	q, err := d.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("debug subscriber: create queue: %w", err)
	}
	d.queue = q

	for _, t := range d.topics {
		if err := d.brk.Subscribe(q, t); err != nil {
			return fmt.Errorf("debug subscriber: subscribe %q: %w", t, err)
		}
		d.logger.Info("subscribed", "topic", t)
	}

	loopCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.done = make(chan struct{})
	go d.loop(loopCtx)
	return nil
}

func (d *debugSubscriber) Stop(_ context.Context) error {
	if d.cancel != nil {
		d.cancel()
	}
	if d.done != nil {
		<-d.done
	}
	if d.sinkFile != nil {
		if err := d.sinkFile.Close(); err != nil {
			d.logger.Warn("close sink file", "error", err)
		}
		d.sinkFile = nil
	}
	return nil
}

func (d *debugSubscriber) Health() error { return nil }

func (d *debugSubscriber) loop(ctx context.Context) {
	defer close(d.done)
	for {
		msg, err := d.brk.Receive(ctx, d.queue)
		if err != nil {
			if ctx.Err() == nil {
				d.logger.Warn("receive error", "error", err)
			}
			return
		}
		if msg == nil {
			continue
		}
		d.record(msg)
		msg.Ack()
	}
}

func (d *debugSubscriber) record(msg *broker.Message) {
	record := map[string]any{
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"topic": msg.Topic,
	}
	if utf8.Valid(msg.Payload) {
		record["payload"] = string(msg.Payload)
	} else {
		record["payload_b64"] = base64.StdEncoding.EncodeToString(msg.Payload)
	}
	if len(msg.UserProperties) > 0 {
		record["user_properties"] = msg.UserProperties
	}

	data, err := json.Marshal(record)
	if err != nil {
		d.logger.Warn("marshal record", "error", err, "topic", msg.Topic)
		return
	}

	if d.sinkFile != nil {
		d.sinkMu.Lock()
		_, _ = d.sinkFile.Write(append(data, '\n'))
		_ = d.sinkFile.Sync()
		d.sinkMu.Unlock()
		return
	}

	d.logger.Debug("event",
		"topic", msg.Topic,
		"payload_bytes", len(msg.Payload),
	)
}
