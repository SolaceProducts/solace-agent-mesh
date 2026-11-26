// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	defaultFlushBatchSize = 100
	defaultFlushInterval  = 50 * time.Millisecond
	defaultMaxUnflushed   = 50000

	readRetryMin          = 500 * time.Millisecond
	readRetryMax          = 30 * time.Second
	browserStreamRetryMin = 200 * time.Millisecond
	browserStreamRetryMax = 5 * time.Second
	stuckAttempts         = 8

	defaultDBCallTimeout = 10 * time.Second
)

type sseEventStore interface {
	Append(ctx context.Context, row *store.SSEEventBufferRow) error
	AppendBatch(ctx context.Context, rows []*store.SSEEventBufferRow) (int64, error)
	GetByTask(ctx context.Context, taskID string, fromSeq int) ([]*store.SSEEventBufferRow, error)
	GetMaxSequence(ctx context.Context, taskID string) (int, error)
	SequenceState(ctx context.Context, taskID string) (maxSeq int, completed bool, err error)
	SequenceStates(ctx context.Context, taskIDs []string) (map[string]store.TaskLogState, error)
	BrokerMessageSequence(ctx context.Context, taskID, brokerMessageID string) (int, error)
	FindSequenceAfterTimestamp(ctx context.Context, taskID string, epochMs int64) (int, error)
	MarkCompleted(ctx context.Context, taskID string) error
	MarkCompletedBatch(ctx context.Context, taskIDs []string) error
	IsCompleted(ctx context.Context, taskID string) (bool, error)
	DeleteByTask(ctx context.Context, taskID string) error
}

type eventsBufferedMarker interface {
	MarkEventsBuffered(ctx context.Context, taskID string) error
	MarkEventsBufferedBatch(ctx context.Context, taskIDs []string) error
}

type PersistentEventLog struct {
	repo          sseEventStore
	taskRepo      eventsBufferedMarker
	pollInterval  time.Duration
	dbCallTimeout time.Duration
	logger        *slog.Logger

	notifyReg *notifyRegistry

	taskRunning func(taskID string) (running, known bool)

	onStored func(taskID string, rows []StoredRow)

	mu              sync.Mutex
	completed       map[string]bool
	remoteGen       map[string]uint64
	storedDone      map[string]int
	sweepRunning    bool
	sweepStop       chan struct{}
	sweepStopOnce   sync.Once
	pendingComplete map[string][]func()
	markRetryAt     time.Time
	sequences       map[string]int
	buffered        map[string]bool
	rejected        map[string]map[int]bool

	emptyKeySkips atomic.Int64

	dupSkips atomic.Int64

	async          bool
	flushMu        sync.Mutex
	tail           map[string][]pendingEvent
	unflushed      int
	flushBatchSize int
	flushInterval  time.Duration
	maxUnflushed   int
	notify         chan struct{}
	stopCh         chan struct{}
	doneCh         chan struct{}
	stopped        bool
}

type pendingEvent struct {
	seq             int
	sessionID       string
	userID          string
	eventType       string
	payload         []byte
	createdAt       int64
	brokerMessageID string
	acks            []func()
}

func NewPersistentEventLog(repo *store.SSEEventRepo, taskRepo *store.TaskRepo, logger *slog.Logger) *PersistentEventLog {
	p := &PersistentEventLog{
		repo:            repo,
		pollInterval:    time.Second,
		dbCallTimeout:   defaultDBCallTimeout,
		logger:          logger,
		notifyReg:       newNotifyRegistry(),
		completed:       make(map[string]bool),
		remoteGen:       make(map[string]uint64),
		storedDone:      make(map[string]int),
		pendingComplete: make(map[string][]func()),
		rejected:        make(map[string]map[int]bool),
		sweepStop:       make(chan struct{}),
		sequences:       make(map[string]int),
		buffered:        make(map[string]bool),
	}
	if taskRepo != nil {
		p.taskRepo = taskRepo
	}
	return p
}

type StoredRow struct {
	Sequence  int64
	EventType string
}

func (p *PersistentEventLog) SetOnStored(fn func(taskID string, rows []StoredRow)) {
	p.onStored = fn
}

func (p *PersistentEventLog) Completed(ctx context.Context, taskID string) (bool, error) {
	p.mu.Lock()
	_, pending := p.pendingComplete[taskID]
	done := p.completed[taskID] || pending
	p.mu.Unlock()
	if done {
		return true, nil
	}
	return p.repo.IsCompleted(ctx, taskID)
}

func (p *PersistentEventLog) Wake(taskID string) {
	p.mu.Lock()
	if p.notifyReg.hasWaiters(taskID) {
		p.remoteGen[taskID]++
	}
	p.mu.Unlock()
	p.notifyReg.signal(taskID)
}

func (p *PersistentEventLog) publishStored(rows []*store.SSEEventBufferRow) {
	if p.onStored == nil {
		return
	}
	for i := 0; i < len(rows); {
		var stored []StoredRow
		j := i
		for ; j < len(rows) && rows[j].TaskID == rows[i].TaskID; j++ {
			stored = append(stored, StoredRow{Sequence: int64(rows[j].EventSequence), EventType: rows[j].EventType})
		}
		p.onStored(rows[i].TaskID, stored)
		i = j
	}
}

func (p *PersistentEventLog) SetTaskRunningCheck(fn func(taskID string) (running, known bool)) {
	p.taskRunning = fn
}

func (p *PersistentEventLog) EnableAsyncFlush(batchSize int, interval time.Duration, maxUnflushed int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.async {
		return
	}
	p.async = true
	p.tail = make(map[string][]pendingEvent)
	p.flushBatchSize = cmp.Or(batchSize, defaultFlushBatchSize)
	p.flushInterval = interval
	if interval <= 0 {
		p.flushInterval = defaultFlushInterval
	}
	p.maxUnflushed = cmp.Or(maxUnflushed, defaultMaxUnflushed)
	p.notify = make(chan struct{}, 1)
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	go p.flushLoop()
}

func (p *PersistentEventLog) Stop() {
	p.sweepStopOnce.Do(func() { close(p.sweepStop) })
	p.mu.Lock()
	if !p.async || p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	close(p.stopCh)
	p.mu.Unlock()
	<-p.doneCh

	var redelivered, lost []string
	p.mu.Lock()
	for taskID, holds := range p.pendingComplete {
		if len(holds) > 0 {
			redelivered = append(redelivered, taskID)
		} else {
			lost = append(lost, taskID)
		}
	}
	p.mu.Unlock()
	if len(redelivered) > 0 {
		p.logger.Warn("sse event log: stopped before writing completion marks; the broker redelivers the final events",
			"tasks", len(redelivered), "taskIDs", firstIDs(redelivered))
	}
	if len(lost) > 0 {
		p.logger.Error("sse event log: stopped before writing the completion marks of tasks the gateway completed; they are not recorded",
			"tasks", len(lost), "taskIDs", firstIDs(lost))
	}
}

func (p *PersistentEventLog) Append(ctx context.Context, taskID, sessionID, userID, eventType string, payload []byte) (int64, error) {
	if taskID == "" || sessionID == "" {
		if n := p.emptyKeySkips.Add(1); n == 1 || n%100 == 0 {
			p.logger.Warn("sse event log: skipping durable persist for event with empty task/session key",
				"taskID", taskID, "sessionID", sessionID, "eventType", eventType, "totalSkipped", n)
		}
		return 0, nil
	}

	d := delivery.From(ctx)

	if d != nil && d.Redelivered && d.BrokerMessageID != "" {
		var stored int
		if err := p.retryForDelivery(ctx, d, taskID, "look up redelivered event", func(actx context.Context) (err error) {
			stored, err = p.storedSequence(actx, taskID, d)
			return err
		}); err != nil {
			return 0, fmt.Errorf("look up redelivered event: %w", err)
		}
		if stored > 0 {
			p.mu.Lock()
			if cur, ok := p.sequences[taskID]; ok && cur < stored {
				p.sequences[taskID] = stored
			}
			p.mu.Unlock()
			return int64(stored), nil
		}
	}

	if err := p.retryForDelivery(ctx, d, taskID, "read stored sequence", func(actx context.Context) error {
		return p.ensureSequence(actx, taskID)
	}); err != nil {
		return 0, fmt.Errorf("read stored sequence: %w", err)
	}

	var brokerMessageID string
	if d != nil {
		brokerMessageID = d.BrokerMessageID
	}

	p.mu.Lock()
	if _, pending := p.pendingComplete[taskID]; pending || p.completed[taskID] {
		p.mu.Unlock()
		return 0, fmt.Errorf("task %s: %w", taskID, ErrTaskCompleted)
	}
	p.sequences[taskID]++
	seq := p.sequences[taskID]

	if p.async && !p.stopped {
		buf := make([]byte, len(payload))
		copy(buf, payload)
		pe := pendingEvent{
			seq: seq, sessionID: sessionID, userID: userID,
			eventType: eventType, payload: buf, createdAt: time.Now().UnixMilli(),
			brokerMessageID: brokerMessageID,
		}
		if d != nil {
			pe.acks = []func(){d.Hold()}
		}
		p.tail[taskID] = append(p.tail[taskID], pe)
		p.unflushed++
		over := p.unflushed >= p.maxUnflushed
		batchFull := p.unflushed >= p.flushBatchSize
		p.mu.Unlock()
		if d == nil {
			p.flushWithDeadline(over)
			p.notifyReg.signal(taskID)
			return int64(seq), nil
		}
		if batchFull || seq == 1 {
			p.signalFlush()
		}
		p.notifyReg.signal(taskID)
		if over {
			p.flushWithDeadline(true)
		}
		return int64(seq), nil
	}

	firstEvent := !p.buffered[taskID]
	p.buffered[taskID] = true
	p.mu.Unlock()

	if firstEvent && p.taskRepo != nil {
		if err := p.taskRepo.MarkEventsBuffered(ctx, taskID); err != nil {
			p.logger.Warn("failed to mark task events_buffered", "taskID", taskID, "error", err)
		}
	}

	row := &store.SSEEventBufferRow{
		TaskID:          taskID,
		SessionID:       sessionID,
		UserID:          userID,
		EventSequence:   seq,
		EventType:       eventType,
		EventData:       string(payload),
		BrokerMessageID: brokerMessageID,
	}
	if err := p.retryForDelivery(ctx, d, taskID, "store event", func(actx context.Context) error {
		return p.repo.Append(actx, row)
	}); err != nil {
		p.mu.Lock()
		if p.sequences[taskID] == seq {
			p.sequences[taskID]--
			if firstEvent {
				p.buffered[taskID] = false
			}
		}
		p.mu.Unlock()
		return 0, fmt.Errorf("append event: %w", err)
	}
	p.notifyReg.signal(taskID)
	p.publishStored([]*store.SSEEventBufferRow{row})
	return int64(seq), nil
}

const (
	ownRowAttempts  = 3
	ownRowRetryWait = 200 * time.Millisecond
)

func (p *PersistentEventLog) retryForDelivery(ctx context.Context, d *delivery.Delivery, taskID, what string, fn func(context.Context) error) error {
	try := func() error {
		actx, cancel := context.WithTimeout(ctx, p.dbCallTimeout)
		defer cancel()
		return fn(actx)
	}
	err := try()
	if err == nil {
		return nil
	}
	if d == nil {
		for attempt := 1; attempt < ownRowAttempts && !dbutil.IsRejected(err); attempt++ {
			select {
			case <-ctx.Done():
				return err
			case <-time.After(ownRowRetryWait):
			}
			if err = try(); err == nil {
				return nil
			}
		}
		return err
	}
	wait := readRetryMin
	for attempt := 1; ; attempt++ {
		if dbutil.IsRejected(err) {
			p.logger.Error("sse event log: database rejected the event; dropping it",
				"taskID", taskID, "operation", what, "error", err)
			return fmt.Errorf("%w: %w", ErrEventRejected, err)
		}
		log := p.logger.Warn
		if attempt == stuckAttempts {
			log = p.logger.Error
		}
		log("sse event log: database call failed; holding the task's events until it succeeds",
			"taskID", taskID, "operation", what, "attempt", attempt, "retryIn", wait, "error", err)
		select {
		case <-ctx.Done():
			d.Keep()
			return fmt.Errorf("%w: %w", ErrHeldForRedelivery, err)
		case <-time.After(wait):
		}
		if err = try(); err == nil {
			return nil
		}
		wait = min(2*wait, readRetryMax)
	}
}

func (p *PersistentEventLog) storedSequence(ctx context.Context, taskID string, d *delivery.Delivery) (int, error) {
	if p.async {
		p.mu.Lock()
		evs := p.tail[taskID]
		for i := range evs {
			if evs[i].brokerMessageID == d.BrokerMessageID {
				evs[i].acks = append(evs[i].acks, d.Hold())
				seq := evs[i].seq
				p.mu.Unlock()
				return seq, nil
			}
		}
		p.mu.Unlock()
	}
	return p.repo.BrokerMessageSequence(ctx, taskID, d.BrokerMessageID)
}

func (p *PersistentEventLog) ensureSequence(ctx context.Context, taskID string) error {
	p.mu.Lock()
	_, seeded := p.sequences[taskID]
	_, markPending := p.pendingComplete[taskID]
	p.mu.Unlock()
	if seeded || markPending {
		return nil
	}
	maxSeq, completed, err := p.repo.SequenceState(ctx, taskID)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if evs := p.tail[taskID]; len(evs) > 0 {
		maxSeq = max(maxSeq, evs[len(evs)-1].seq)
	}
	if cur, ok := p.sequences[taskID]; !ok || cur < maxSeq {
		p.sequences[taskID] = maxSeq
	}
	if maxSeq > 0 {
		p.buffered[taskID] = true
	}
	if completed {
		p.completed[taskID] = true
	}
	p.mu.Unlock()
	return nil
}

func (p *PersistentEventLog) signalFlush() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *PersistentEventLog) flushLoop() {
	defer close(p.doneCh)
	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			p.mu.Lock()
			p.markRetryAt = time.Time{}
			p.mu.Unlock()
			p.flushWithDeadline(false)
			return
		case <-p.notify:
			p.flushWithDeadline(false)
		case <-ticker.C:
			p.flushWithDeadline(false)
		}
	}
}

func (p *PersistentEventLog) flushWithDeadline(underPressure bool) {
	ctx, cancel := context.WithTimeout(context.Background(), p.dbCallTimeout)
	defer cancel()
	p.flushOnce(ctx, underPressure)
}

func (p *PersistentEventLog) flushOnce(ctx context.Context, underPressure bool) {
	p.flushMu.Lock()
	defer p.flushMu.Unlock()

	p.mu.Lock()
	if p.unflushed == 0 && len(p.pendingComplete) == 0 {
		p.mu.Unlock()
		return
	}
	rows := make([]*store.SSEEventBufferRow, 0, p.unflushed)
	var toMark []string
	for taskID, evs := range p.tail {
		for i := range evs {
			e := &evs[i]
			rows = append(rows, &store.SSEEventBufferRow{
				TaskID: taskID, SessionID: e.sessionID, UserID: e.userID,
				EventSequence: e.seq, EventType: e.eventType,
				EventData: string(e.payload), CreatedAt: e.createdAt,
				BrokerMessageID: e.brokerMessageID,
			})
		}
		if !p.buffered[taskID] {
			toMark = append(toMark, taskID)
		}
	}
	p.mu.Unlock()

	flushedMax := make(map[string]int, len(toMark))
	var stored []*store.SSEEventBufferRow
	var flushErr error
	for start := 0; start < len(rows); start += p.flushBatchSize {
		end := min(start+p.flushBatchSize, len(rows))
		inserted, err := p.repo.AppendBatch(ctx, rows[start:end])
		if err == nil {
			p.recordDupSkips(rows[start:end], inserted)
			markFlushed(flushedMax, rows[start:end]...)
			stored = append(stored, rows[start:end]...)
			continue
		}
		if !dbutil.IsRejected(err) {
			flushErr = err
			break
		}
		if flushErr = p.flushRowsOneByOne(ctx, rows[start:end], flushedMax, &stored); flushErr != nil {
			break
		}
	}
	if flushErr != nil {
		p.logger.Warn("sse event log flush failed; retaining un-flushed events for retry",
			"error", flushErr, "rowsAttempted", len(rows), "tasksPartiallyFlushed", len(flushedMax))
	}

	var nowBuffered []string
	for _, taskID := range toMark {
		if _, ok := flushedMax[taskID]; ok {
			nowBuffered = append(nowBuffered, taskID)
		}
	}
	unbuffered := p.markEventsBuffered(ctx, nowBuffered)

	var acks []func()
	p.mu.Lock()
	for taskID, maxSeq := range flushedMax {
		if _, tracked := p.sequences[taskID]; tracked && !unbuffered[taskID] {
			p.buffered[taskID] = true
		}
		acks = append(acks, p.trimTailLocked(taskID, maxSeq)...)
	}
	var toComplete []string
	if !time.Now().Before(p.markRetryAt) {
		for taskID := range p.pendingComplete {
			if len(p.tail[taskID]) == 0 {
				toComplete = append(toComplete, taskID)
			}
		}
	}
	if flushErr != nil && underPressure && p.unflushed >= 2*p.maxUnflushed {
		p.dropOldestLocked()
	}
	p.mu.Unlock()

	acks = append(acks, p.markCompleted(ctx, toComplete)...)
	for _, ack := range acks {
		ack()
	}

	for taskID := range flushedMax {
		p.notifyReg.signal(taskID)
	}
	p.publishStored(stored)
}

func (p *PersistentEventLog) markEventsBuffered(ctx context.Context, taskIDs []string) (failed map[string]bool) {
	if len(taskIDs) == 0 || p.taskRepo == nil {
		return nil
	}
	if p.taskRepo.MarkEventsBufferedBatch(ctx, taskIDs) == nil {
		return nil
	}
	for i, taskID := range taskIDs {
		err := p.taskRepo.MarkEventsBuffered(ctx, taskID)
		switch {
		case err == nil:
		case dbutil.IsRejected(err):
			p.logger.Error("sse event log: database rejected a task's events_buffered mark; dropping it",
				"taskID", taskID, "error", err)
		default:
			rest := taskIDs[i:]
			p.logger.Warn("failed to mark tasks events_buffered; retrying with each task's next row",
				"tasks", len(rest), "taskIDs", firstIDs(rest), "error", err)
			failed = make(map[string]bool, len(rest))
			for _, id := range rest {
				failed[id] = true
			}
			return failed
		}
	}
	return nil
}

func (p *PersistentEventLog) markCompleted(ctx context.Context, taskIDs []string) []func() {
	if len(taskIDs) == 0 {
		return nil
	}
	if p.repo.MarkCompletedBatch(ctx, taskIDs) == nil {
		return p.takeCompletionHolds(taskIDs...)
	}
	var release []func()
	for i, taskID := range taskIDs {
		err := p.repo.MarkCompleted(ctx, taskID)
		switch {
		case err == nil:
			release = append(release, p.takeCompletionHolds(taskID)...)
		case dbutil.IsRejected(err):
			p.logger.Error("sse event log: database rejected a task's completion mark; dropping it",
				"taskID", taskID, "error", err)
			release = append(release, p.takeCompletionHolds(taskID)...)
		default:
			rest := taskIDs[i:]
			p.logger.Warn("sse event log: mark tasks completed after their final rows were written failed; retrying",
				"tasks", len(rest), "taskIDs", firstIDs(rest), "error", err)
			p.mu.Lock()
			p.markRetryAt = time.Now().Add(time.Second)
			p.mu.Unlock()
			return release
		}
	}
	return release
}

func (p *PersistentEventLog) takeCompletionHolds(taskIDs ...string) []func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	var holds []func()
	for _, taskID := range taskIDs {
		holds = append(holds, p.pendingComplete[taskID]...)
		delete(p.pendingComplete, taskID)
	}
	return holds
}

const logIDLimit = 10

func firstIDs(ids []string) []string {
	return ids[:min(len(ids), logIDLimit)]
}

func markFlushed(flushedMax map[string]int, rows ...*store.SSEEventBufferRow) {
	for _, r := range rows {
		if r.EventSequence > flushedMax[r.TaskID] {
			flushedMax[r.TaskID] = r.EventSequence
		}
	}
}

func (p *PersistentEventLog) flushRowsOneByOne(ctx context.Context, rows []*store.SSEEventBufferRow, flushedMax map[string]int, stored *[]*store.SSEEventBufferRow) error {
	for _, r := range rows {
		err := p.repo.Append(ctx, r)
		switch {
		case err == nil:
			*stored = append(*stored, r)
		case dbutil.IsRejected(err):
			p.logger.Error("sse event log: database rejected an event; dropping it",
				"taskID", r.TaskID, "sequence", r.EventSequence, "eventType", r.EventType, "error", err)
			p.mu.Lock()
			if p.rejected[r.TaskID] == nil {
				p.rejected[r.TaskID] = make(map[int]bool)
			}
			p.rejected[r.TaskID][r.EventSequence] = true
			p.mu.Unlock()
		default:
			return err
		}
		markFlushed(flushedMax, r)
	}
	return nil
}

func (p *PersistentEventLog) recordDupSkips(batch []*store.SSEEventBufferRow, inserted int64) {
	if inserted >= int64(len(batch)) {
		return
	}
	if n := p.dupSkips.Add(1); n == 1 || n%100 == 0 {
		p.logger.Warn("sse event log: skipped already-durable duplicate rows on flush",
			"candidateTaskIDs", distinctTaskIDs(batch, 10), "batchSkipped", int64(len(batch))-inserted, "batchSize", len(batch), "dupFlushCount", n)
	}
}

func distinctTaskIDs(batch []*store.SSEEventBufferRow, limit int) []string {
	seen := make(map[string]struct{}, len(batch))
	out := make([]string, 0, limit)
	for _, r := range batch {
		if _, ok := seen[r.TaskID]; ok {
			continue
		}
		seen[r.TaskID] = struct{}{}
		if len(out) < limit {
			out = append(out, r.TaskID)
		}
	}
	return out
}

func (p *PersistentEventLog) trimTailLocked(taskID string, maxSeq int) []func() {
	evs := p.tail[taskID]
	i := 0
	var acks []func()
	for i < len(evs) && evs[i].seq <= maxSeq {
		acks = append(acks, evs[i].acks...)
		i++
	}
	if i == 0 {
		return nil
	}
	p.unflushed -= i
	if i == len(evs) {
		delete(p.tail, taskID)
		return acks
	}
	rest := make([]pendingEvent, len(evs)-i)
	copy(rest, evs[i:])
	p.tail[taskID] = rest
	return acks
}

func (p *PersistentEventLog) dropOldestLocked() {
	dropped := 0
	for p.unflushed > p.maxUnflushed {
		for taskID, evs := range p.tail {
			if len(evs) == 0 {
				delete(p.tail, taskID)
				continue
			}
			p.tail[taskID] = evs[1:]
			p.unflushed--
			dropped++
			if p.unflushed <= p.maxUnflushed {
				break
			}
		}
	}
	p.logger.Warn("sse event log: dropping un-flushed events to bound memory (DB unavailable); reconnect replay may gap", "dropped", dropped)
}

func (p *PersistentEventLog) Subscribe(ctx context.Context, taskID string, fromSeq int64) (<-chan Event, error) {
	dctx, cancel := context.WithTimeout(ctx, p.dbCallTimeout)
	maxSeq, err := p.repo.GetMaxSequence(dctx, taskID)
	cancel()
	if err != nil && ctx.Err() == nil {
		p.logger.Warn("sse event log: task check for a stream failed; following the task", "taskID", taskID, "error", err)
	}

	p.mu.Lock()
	hasSequence := p.sequences[taskID] > 0
	p.mu.Unlock()

	if err == nil && maxSeq == 0 && !hasSequence {
		if p.taskRunning == nil {
			return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		}
		if running, known := p.taskRunning(taskID); known && !running {
			return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		}
	}

	ch := make(chan Event, 64)
	go p.subscriberLoop(ctx, taskID, int(fromSeq), ch)
	return ch, nil
}

func (p *PersistentEventLog) subscriberLoop(ctx context.Context, taskID string, cursor int, ch chan<- Event) {
	defer close(ch)

	wake := p.notifyReg.register(taskID)
	defer p.notifyReg.unregister(taskID, wake)
	p.startSweep()

	readDB := true
	var lastGen uint64
	delivered, checkedDone := false, false
	failures := 0
	for {
		if ctx.Err() != nil {
			return
		}
		dctx, cancel := context.WithTimeout(ctx, p.dbCallTimeout)
		events, err := p.read(dctx, taskID, cursor, readDB)
		cancel()
		if err != nil {
			failures++
			if !p.waitToRetry(ctx, taskID, failures, err) {
				return
			}
			continue
		}
		failures = 0

		for _, evt := range events {
			select {
			case ch <- evt:
				cursor = int(evt.Sequence) + 1
				delivered = true
			case <-ctx.Done():
				return
			}
			if evt.EventType == "final_response" {
				return
			}
		}

		done, maxSeq := p.finished(taskID, &cursor)
		if !done && maxSeq == 0 && !checkedDone {
			checkedDone = true
			done, maxSeq = p.storedCompletion(ctx, taskID)
		}
		if !done && maxSeq == 0 && !delivered {
			if ended, gone := p.endedWithNoRow(ctx, taskID, cursor); ended {
				return
			} else if gone {
				readDB = true
				continue
			}
		}
		if done && cursor > maxSeq {
			return
		}

		select {
		case <-wake:
		case <-ctx.Done():
			return
		}
		readDB = p.hasNewRows(taskID, cursor, &lastGen)
	}
}

func (p *PersistentEventLog) waitToRetry(ctx context.Context, taskID string, failures int, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if failures == 1 || failures%10 == 0 {
		p.logger.Warn("sse event log: read for a stream failed; retrying", "taskID", taskID, "failures", failures, "error", err)
	}
	select {
	case <-time.After(min(time.Duration(failures)*browserStreamRetryMin, browserStreamRetryMax)):
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *PersistentEventLog) finished(taskID string, cursor *int) (done bool, maxSeq int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	done, maxSeq = p.completed[taskID], p.sequences[taskID]
	if stored, ok := p.storedDone[taskID]; ok {
		done, maxSeq = true, max(maxSeq, stored)
	}
	for done && *cursor <= maxSeq && p.rejected[taskID][*cursor] {
		*cursor++
	}
	return done, maxSeq
}

func (p *PersistentEventLog) storedCompletion(ctx context.Context, taskID string) (done bool, maxSeq int) {
	dctx, cancel := context.WithTimeout(ctx, p.dbCallTimeout)
	defer cancel()
	durable, err := p.repo.IsCompleted(dctx, taskID)
	if err != nil && ctx.Err() == nil {
		p.logger.Warn("sse event log: durable completion check failed", "taskID", taskID, "error", err)
	}
	if !durable {
		return false, 0
	}
	if dbMax, err := p.repo.GetMaxSequence(dctx, taskID); err == nil {
		maxSeq = dbMax
	}
	return true, maxSeq
}

func (p *PersistentEventLog) endedWithNoRow(ctx context.Context, taskID string, cursor int) (ended, gone bool) {
	if p.taskRunning == nil {
		return false, false
	}
	if running, known := p.taskRunning(taskID); !known || running {
		return false, false
	}
	dctx, cancel := context.WithTimeout(ctx, p.dbCallTimeout)
	defer cancel()
	rows, err := p.repo.GetByTask(dctx, taskID, cursor)
	return err == nil && len(rows) == 0, true
}

func (p *PersistentEventLog) hasNewRows(taskID string, cursor int, lastGen *uint64) bool {
	p.mu.Lock()
	gen, local := p.remoteGen[taskID], p.sequences[taskID]
	p.mu.Unlock()
	changed := gen != *lastGen
	*lastGen = gen
	return changed || cursor <= local
}

func (p *PersistentEventLog) startSweep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sweepRunning {
		return
	}
	p.sweepRunning = true
	go p.sweepLoop()
}

func (p *PersistentEventLog) sweepLoop() {
	seen := make(map[string]int)
	failures := 0
	tick := time.NewTicker(p.pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
		case <-p.sweepStop:
			p.mu.Lock()
			p.sweepRunning = false
			p.mu.Unlock()
			return
		}
		p.mu.Lock()
		tasks := p.notifyReg.tasks()
		if len(tasks) == 0 {
			clear(p.remoteGen)
			clear(p.storedDone)
			p.sweepRunning = false
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), p.dbCallTimeout)
		states, err := p.repo.SequenceStates(ctx, tasks)
		cancel()
		if err != nil {
			failures++
			if failures == 1 || failures%60 == 0 {
				p.logger.Warn("sse event log: sweep of streamed tasks failed; retrying", "tasks", len(tasks), "failures", failures, "error", err)
			}
			continue
		}
		failures = 0

		active := make(map[string]bool, len(tasks))
		for _, taskID := range tasks {
			active[taskID] = true
			st, stored := states[taskID]
			wake := !stored
			p.mu.Lock()
			if stored {
				if _, ok := p.storedDone[taskID]; st.Completed && !ok {
					p.storedDone[taskID] = st.MaxSeq
					wake = true
				}
				if st.MaxSeq > p.sequences[taskID] && st.MaxSeq > seen[taskID] {
					p.remoteGen[taskID]++
					wake = true
				}
				seen[taskID] = st.MaxSeq
			}
			p.mu.Unlock()
			if wake {
				p.notifyReg.signal(taskID)
			}
		}
		p.mu.Lock()
		for taskID := range seen {
			if !active[taskID] {
				delete(seen, taskID)
			}
		}
		for taskID := range p.remoteGen {
			if !p.notifyReg.hasWaiters(taskID) {
				delete(p.remoteGen, taskID)
			}
		}
		for taskID := range p.storedDone {
			if !p.notifyReg.hasWaiters(taskID) {
				delete(p.storedDone, taskID)
			}
		}
		p.mu.Unlock()
	}
}

func (p *PersistentEventLog) Replay(ctx context.Context, taskID string, fromSeq int64) ([]Event, error) {
	return p.read(ctx, taskID, int(fromSeq), true)
}

func (p *PersistentEventLog) read(ctx context.Context, taskID string, fromSeq int, readDB bool) ([]Event, error) {
	from := max(fromSeq, 1)

	var tailSnap []pendingEvent
	covered := false
	if p.async {
		p.mu.Lock()
		if evs := p.tail[taskID]; len(evs) > 0 {
			first, last := evs[0].seq, evs[len(evs)-1].seq
			covered = first <= from && last-first == len(evs)-1
			for _, e := range evs {
				if e.seq >= from {
					tailSnap = append(tailSnap, e)
				}
			}
		}
		p.mu.Unlock()
	}
	if covered {
		return tailEvents(taskID, tailSnap, from-1), nil
	}
	if !readDB {
		return nil, nil
	}

	rows, err := p.repo.GetByTask(ctx, taskID, from)
	if err != nil {
		return nil, fmt.Errorf("replay events: %w", err)
	}
	events := make([]Event, 0, len(rows)+len(tailSnap))
	maxDB := 0
	for _, row := range rows {
		events = append(events, Event{
			Sequence:  int64(row.EventSequence),
			TaskID:    taskID,
			EventType: row.EventType,
			Payload:   []byte(row.EventData),
			Timestamp: row.CreatedAt,
		})
		maxDB = row.EventSequence
	}
	return append(events, tailEvents(taskID, tailSnap, max(from-1, maxDB))...), nil
}

func tailEvents(taskID string, tail []pendingEvent, after int) []Event {
	var events []Event
	for _, e := range tail {
		if e.seq > after {
			events = append(events, Event{
				Sequence: int64(e.seq), TaskID: taskID, EventType: e.eventType,
				Payload: append([]byte(nil), e.payload...), Timestamp: e.createdAt,
			})
		}
	}
	return events
}

func (p *PersistentEventLog) Complete(ctx context.Context, taskID string) error {
	d := delivery.From(ctx)
	p.mu.Lock()
	p.completed[taskID] = true
	flusherRunning := p.async && !p.stopped
	deferred := flusherRunning && (d != nil || len(p.tail[taskID]) > 0)
	if deferred {
		var holds []func()
		if d != nil {
			holds = append(holds, d.Hold())
		}
		p.pendingComplete[taskID] = append(p.pendingComplete[taskID], holds...)
	}
	p.mu.Unlock()

	p.notifyReg.signal(taskID)

	if deferred {
		p.signalFlush()
		return nil
	}
	if p.async && !flusherRunning {
		return p.completeAfterStop(ctx, taskID, d)
	}
	err := p.repo.MarkCompleted(ctx, taskID)
	if err == nil {
		return nil
	}
	if !flusherRunning || dbutil.IsRejected(err) {
		return fmt.Errorf("mark task completed: %w", err)
	}
	p.logger.Warn("sse event log: mark task completed failed; retrying on a later flush", "taskID", taskID, "error", err)
	p.mu.Lock()
	if _, pending := p.pendingComplete[taskID]; !pending {
		p.pendingComplete[taskID] = nil
	}
	p.mu.Unlock()
	p.signalFlush()
	return nil
}

func (p *PersistentEventLog) completeAfterStop(ctx context.Context, taskID string, d *delivery.Delivery) error {
	<-p.doneCh
	p.mu.Lock()
	unstored := len(p.tail[taskID])
	p.mu.Unlock()
	if unstored > 0 {
		if d == nil {
			return fmt.Errorf("task %s: %d events unstored at shutdown, so its completion is not recorded", taskID, unstored)
		}
		d.Keep()
		p.logger.Warn("sse event log: stopped with a completed task's events unstored; leaving its completion to the gateway the broker redelivers them to",
			"taskID", taskID, "unstored", unstored)
		return nil
	}
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.dbCallTimeout)
	defer cancel()
	err := p.repo.MarkCompleted(mctx, taskID)
	if err == nil {
		return nil
	}
	if d != nil && !dbutil.IsRejected(err) {
		d.Keep()
	}
	return fmt.Errorf("mark task completed: %w", err)
}

func (p *PersistentEventLog) Forget(taskID string) {
	p.mu.Lock()
	delete(p.completed, taskID)
	delete(p.sequences, taskID)
	delete(p.buffered, taskID)
	delete(p.rejected, taskID)
	p.mu.Unlock()
}

func (p *PersistentEventLog) Cleanup(ctx context.Context, taskID string) error {
	if p.async {
		p.flushMu.Lock()
		defer p.flushMu.Unlock()
	}
	p.mu.Lock()
	acks := p.pendingComplete[taskID]
	delete(p.completed, taskID)
	delete(p.pendingComplete, taskID)
	delete(p.sequences, taskID)
	delete(p.buffered, taskID)
	delete(p.rejected, taskID)
	if p.async {
		for _, e := range p.tail[taskID] {
			acks = append(acks, e.acks...)
		}
		p.unflushed -= len(p.tail[taskID])
		delete(p.tail, taskID)
	}
	p.mu.Unlock()
	for _, ack := range acks {
		ack()
	}

	return p.repo.DeleteByTask(ctx, taskID)
}

func (p *PersistentEventLog) FindSequenceAfterTimestamp(ctx context.Context, taskID string, epochMs int64) (int64, error) {
	tailSeq := 0
	nextSeq := 0
	if p.async {
		p.mu.Lock()
		for _, e := range p.tail[taskID] {
			if e.createdAt > epochMs {
				tailSeq = e.seq
				break
			}
		}
		nextSeq = p.sequences[taskID] + 1
		p.mu.Unlock()
	}

	dbSeq, err := p.repo.FindSequenceAfterTimestamp(ctx, taskID, epochMs)
	if err != nil {
		return 0, err
	}
	switch {
	case dbSeq != 0 && tailSeq != 0:
		return int64(min(dbSeq, tailSeq)), nil
	case dbSeq != 0:
		return int64(dbSeq), nil
	case tailSeq != 0:
		return int64(tailSeq), nil
	}

	dbMax, err := p.repo.GetMaxSequence(ctx, taskID)
	if err != nil {
		return 0, err
	}
	next := dbMax + 1
	if nextSeq > next {
		next = nextSeq
	}
	return int64(next), nil
}

func (p *PersistentEventLog) FindLastEventByDataType(ctx context.Context, taskID string, dataType string) []byte {
	needle := []byte(`"type":"` + dataType + `"`)

	if p.async {
		p.mu.Lock()
		evs := p.tail[taskID]
		for i := len(evs) - 1; i >= 0; i-- {
			if evs[i].eventType == "status_update" && bytes.Contains(evs[i].payload, needle) {
				out := append([]byte(nil), evs[i].payload...)
				p.mu.Unlock()
				return out
			}
		}
		p.mu.Unlock()
	}

	rows, err := p.repo.GetByTask(ctx, taskID, 0)
	if err != nil || len(rows) == 0 {
		return nil
	}
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.EventType != "status_update" {
			continue
		}
		if bytes.Contains([]byte(row.EventData), needle) {
			return []byte(row.EventData)
		}
	}
	return nil
}

var _ TaskEventLog = (*PersistentEventLog)(nil)
