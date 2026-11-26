// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	publisherBufferSize = 4096
	publishTimeout      = 10 * time.Second

	dropReasonBufferFull = "buffer full"
	dropReasonClosed     = "publisher closed"
	dropReasonRefused    = "broker kept refusing it"
)

type publisherTiming struct {
	summaryInterval time.Duration

	retryBackoff    time.Duration
	maxRetryBackoff time.Duration
	giveUpAfter     time.Duration

	provisionRetry time.Duration
}

var defaultPublisherTiming = publisherTiming{
	summaryInterval: time.Minute,
	retryBackoff:    100 * time.Millisecond,
	maxRetryBackoff: 5 * time.Second,
	giveUpAfter:     5 * time.Minute,
	provisionRetry:  time.Minute,
}

type Publisher struct {
	brk        broker.Broker
	namespace  string
	topic      string
	logger     *slog.Logger
	timing     publisherTiming
	events     chan Event
	workerCtx  context.Context
	stopWorker context.CancelFunc
	done       chan struct{}

	mu     sync.RWMutex
	closed bool

	drops   *tally
	repairs *tally

	unpublished int
	reportOnce  sync.Once
}

func NewPublisher(brk broker.Broker, namespace string, logger *slog.Logger) *Publisher {
	return newPublisher(brk, namespace, logger, defaultPublisherTiming)
}

func newPublisher(brk broker.Broker, namespace string, logger *slog.Logger, timing publisherTiming) *Publisher {
	workerCtx, stopWorker := context.WithCancel(context.Background())
	logger = logger.With("logger_name", "spend-publisher")
	p := &Publisher{
		brk:        brk,
		namespace:  namespace,
		topic:      a2a.SpendEventTopic(namespace),
		logger:     logger,
		timing:     timing,
		events:     make(chan Event, publisherBufferSize),
		workerCtx:  workerCtx,
		stopWorker: stopWorker,
		done:       make(chan struct{}),
		drops:      newTally(logger, slog.LevelError, "spend events dropped", timing.summaryInterval),
		repairs:    newTally(logger, slog.LevelWarn, "spend events repaired", timing.summaryInterval),
	}
	go p.run()
	return p
}

func (p *Publisher) Record(e Event) {
	if p == nil {
		return
	}
	e.SchemaVersion = EventSchemaVersion
	notes, err := e.Sanitize(time.Now())
	if err != nil {
		p.drops.note(err.Error(), refOf(e))
		return
	}
	for _, note := range notes {
		p.repairs.note(note, refOf(e))
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		p.drops.note(dropReasonClosed, refOf(e))
		return
	}
	select {
	case p.events <- e:
	default:
		p.drops.note(dropReasonBufferFull, refOf(e))
	}
}

func (p *Publisher) Dropped() int64 {
	if p == nil {
		return 0
	}
	return p.drops.count()
}

func (p *Publisher) Queued() int {
	if p == nil {
		return 0
	}
	return len(p.events)
}

func (p *Publisher) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.events)
	}
	p.mu.Unlock()

	var err error
	select {
	case <-p.done:
	case <-ctx.Done():
		p.stopWorker()
		<-p.done
		err = ctx.Err()
	}
	p.reportOnce.Do(func() {
		p.drops.flush()
		p.repairs.flush()
		if p.unpublished > 0 {
			p.logger.Error("spend events lost at shutdown", "events", p.unpublished)
			err = fmt.Errorf("spend publisher: %d events unpublished at shutdown: %w", p.unpublished, err)
		}
	})
	return err
}

func (p *Publisher) run() {
	defer close(p.done)
	defer p.stopWorker()
	provisioned := false
	var nextProvision time.Time
	for e := range p.events {
		if p.workerCtx.Err() != nil {
			p.unpublished++
			continue
		}
		if !provisioned && !time.Now().Before(nextProvision) {
			provisioned = p.provision()
			nextProvision = time.Now().Add(p.timing.provisionRetry)
		}
		p.publish(e)
	}
}

func (p *Publisher) provision() bool {
	name, opts := EventQueue(p.namespace)
	ctx, cancel := context.WithTimeout(p.workerCtx, publishTimeout)
	defer cancel()
	if err := broker.ProvisionQueue(ctx, p.brk, name, opts, p.topic); err != nil {
		p.logger.Warn("spend queue not provisioned; spend published before the governor first starts may be lost",
			"queue", name, "error", err)
		return false
	}
	return true
}

func (p *Publisher) publish(e Event) {
	payload, err := json.Marshal(e)
	if err != nil {
		p.logger.Warn("spend event dropped: not encodable", "eventID", e.ID, "taskID", e.TaskID, "error", err)
		return
	}
	start := time.Now()
	backoff := p.timing.retryBackoff
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(p.workerCtx, publishTimeout)
		err := p.brk.PublishGuaranteed(ctx, p.topic, &broker.Message{Payload: payload, TTL: RetentionWindow})
		cancel()
		switch {
		case err == nil:
			return
		case p.workerCtx.Err() != nil:
			p.unpublished++
			return
		case time.Since(start) >= p.timing.giveUpAfter:
			p.drops.note(dropReasonRefused, refOf(e))
			p.logger.Error("spend event lost: the broker kept refusing it",
				"eventID", e.ID, "taskID", e.TaskID, "attempts", attempt, "error", err)
			return
		}
		p.logger.Warn("spend event not published; retrying",
			"eventID", e.ID, "taskID", e.TaskID, "attempt", attempt, "error", err)
		select {
		case <-time.After(backoff):
		case <-p.workerCtx.Done():
			p.unpublished++
			return
		}
		backoff = min(2*backoff, p.timing.maxRetryBackoff)
	}
}

type eventRef struct {
	taskID  string
	traceID string
}

func refOf(e Event) eventRef { return eventRef{taskID: e.TaskID, traceID: e.TraceID} }

type tally struct {
	logger   *slog.Logger
	level    slog.Level
	msg      string
	interval time.Duration

	mu      sync.Mutex
	counts  map[string]int
	samples map[string]eventRef
	total   int64
	lastLog time.Time
}

func newTally(logger *slog.Logger, level slog.Level, msg string, interval time.Duration) *tally {
	return &tally{
		logger: logger, level: level, msg: msg, interval: interval,
		counts: make(map[string]int), samples: make(map[string]eventRef),
	}
}

func (t *tally) note(reason string, ref eventRef) {
	t.mu.Lock()
	t.counts[reason]++
	t.total++
	if _, seen := t.samples[reason]; !seen {
		t.samples[reason] = ref
	}
	now := time.Now()
	if now.Sub(t.lastLog) < t.interval {
		t.mu.Unlock()
		return
	}
	t.report(now)
}

func (t *tally) flush() {
	t.mu.Lock()
	if len(t.counts) == 0 {
		t.mu.Unlock()
		return
	}
	t.report(time.Now())
}

func (t *tally) report(now time.Time) {
	counts, samples, total := t.counts, t.samples, t.total
	t.counts = make(map[string]int)
	t.samples = make(map[string]eventRef)
	t.lastLog = now
	t.mu.Unlock()

	taskIDs := make(map[string]string, len(samples))
	traceIDs := make(map[string]string, len(samples))
	for reason, ref := range samples {
		taskIDs[reason], traceIDs[reason] = ref.taskID, ref.traceID
	}
	t.logger.Log(context.Background(), t.level, t.msg,
		"byReason", counts, "sampleTaskIDs", taskIDs, "sampleTraceIDs", traceIDs, "totalSinceStart", total)
}

func (t *tally) count() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}
