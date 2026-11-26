// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

const retention = time.Hour

const retentionSweepInterval = 10 * time.Minute

const (
	defaultResubscribeDelay = time.Second
	maxResubscribeDelay     = 30 * time.Second
	subscribeHealthKey      = "discovery:subscribe"
)

type Directory struct {
	brk              broker.Broker
	namespace        string
	logger           *slog.Logger
	registry         *Registry
	health           health.State
	resubscribeDelay time.Duration

	mu  sync.Mutex
	run *directoryRun
}

type directoryRun struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	queue  broker.Queue
}

func NewDirectory(brk broker.Broker, namespace string, logger *slog.Logger) *Directory {
	return &Directory{
		brk:              brk,
		namespace:        namespace,
		logger:           logger.With("logger_name", "discovery-directory"),
		registry:         NewRegistry(),
		resubscribeDelay: defaultResubscribeDelay,
	}
}

func (d *Directory) Registry() *Registry { return d.registry }

func (d *Directory) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.run != nil {
		return nil
	}
	if d.brk == nil {
		return errors.New("discovery directory has no broker")
	}
	q, err := d.subscribe()
	if err != nil {
		d.health.Set(subscribeHealthKey, health.Degraded, health.DetailTemporaryReceive)
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &directoryRun{cancel: cancel, queue: q}
	run.wg.Go(func() { d.receive(ctx, run) })
	run.wg.Go(func() { d.sweepRetention(ctx) })
	if answerErr := AnswerCardRequests(ctx, d.brk, d.namespace, d.registry, d.logger); answerErr != nil {
		d.logger.Warn("agent card requests will not be answered", "error", answerErr)
	}
	d.run = run
	return err
}

func (d *Directory) subscribe() (broker.Queue, error) {
	q, err := d.brk.CreateDirectSubscription()
	if err != nil {
		return nil, fmt.Errorf("create discovery subscription: %w", err)
	}
	if err := d.brk.Subscribe(q, a2a.DiscoveryAgentCardsTopic(d.namespace)); err != nil {
		_ = d.brk.DeleteQueue(q.Name())
		return nil, fmt.Errorf("subscribe discovery: %w", err)
	}
	return q, nil
}

func (d *Directory) receive(ctx context.Context, run *directoryRun) {
	bo := backoff.Exponential{Initial: d.resubscribeDelay, Max: maxResubscribeDelay, Jitter: 0.2}
	var lostQueue string
	lostAt := time.Now()
	for {
		if run.queue == nil {
			if bo.Wait(ctx) != nil {
				return
			}
			q, err := d.subscribe()
			if err != nil {
				d.logger.Warn("discovery resubscribe failed", "error", err, "backoff", bo.Current())
				continue
			}
			run.queue = q
			d.health.Clear(subscribeHealthKey)
			d.logger.Info("discovery subscription restored",
				"queue", q.Name(), "lostQueue", lostQueue, "outage_ms", time.Since(lostAt).Milliseconds())
		}
		subscribedAt := time.Now()
		broker.RunReceiveLoop(ctx, d.brk, run.queue, d.logger, broker.ReceiveLoopOptions{
			Health:       &d.health,
			Resubscribes: true,
			LoopName:     "discovery",
		}, func(msg *broker.Message) {
			d.Ingest(msg)
			msg.Ack()
		})
		if ctx.Err() != nil {
			return
		}
		if time.Since(subscribedAt) >= maxResubscribeDelay {
			bo.Reset()
		}
		lostQueue, lostAt = run.queue.Name(), time.Now()
		d.health.Clear("queue:" + lostQueue)
		d.health.Set(subscribeHealthKey, health.Degraded, health.DetailTemporaryReceive)
		run.queue = nil
	}
}

func (d *Directory) Close() error {
	d.mu.Lock()
	run := d.run
	d.run = nil
	d.mu.Unlock()
	if run == nil {
		return nil
	}
	run.cancel()
	run.wg.Wait()
	var err error
	if run.queue != nil {
		err = d.brk.DeleteQueue(run.queue.Name())
	}
	d.mu.Lock()
	if d.run == nil {
		d.health.Reset()
	}
	d.mu.Unlock()
	return err
}

func (d *Directory) HealthSnapshot() []health.Problem {
	return d.health.Snapshot()
}

func (d *Directory) Ingest(msg *broker.Message) Event {
	m, err := ParseMessage(msg.Payload, msg.UserProperties)
	if err != nil {
		d.logger.Warn("invalid agent card", "error", err,
			"instanceID", msg.UserProperties["instance_id"], "payload_bytes", len(msg.Payload))
		return Event{Ignored: true}
	}
	ev := d.registry.Apply(m)
	switch {
	case ev.Change == Added:
		d.logger.Debug("discovered agent card", "agentName", ev.Name)
	case ev.Removed:
		d.logger.Debug("agent card removed", "agentName", ev.Name, "instanceID", m.InstanceID)
	case m.Removed && ev.Ignored:
		d.logger.Debug("agent card removal ignored", "agentName", ev.Name, "instanceID", m.InstanceID)
	case m.Removed:
		d.logger.Debug("agent instance removed", "agentName", ev.Name, "instanceID", m.InstanceID, "remaining", ev.Remaining)
	}
	return ev
}

func (d *Directory) sweepRetention(ctx context.Context) {
	ticker := time.NewTicker(retentionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if gone := d.registry.ExpireOlderThan(time.Now().Add(-retention)); len(gone) > 0 {
				d.logger.Debug("dropped agent cards past retention", "count", len(gone))
			}
		}
	}
}
