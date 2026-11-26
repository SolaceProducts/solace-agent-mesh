// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

const (
	metricEventsDropped     = "governor.spend_events_dropped"
	metricEventsHeld        = "governor.spend_events_held"
	metricEventsRefusalHeld = "governor.spend_events_refusal_held"
	metricEventsNacked      = "governor.spend_events_nacked"
	metricEventsUnpriced    = "governor.spend_events_unpriced"
)

const RetentionPeriod = spend.RetentionWindow

const (
	maxBatch       = store.MaxSpendLedgerBatch
	purgeBatchSize = 5000
)

type timing struct {
	flushInterval   time.Duration
	retryBackoff    time.Duration
	maxRetryBackoff time.Duration

	attemptsBeforeIsolating int

	failuresBeforeStoppingIsolation int

	failuresBeforeNack int

	writeTimeout time.Duration

	drainTimeout time.Duration

	deliveredIdle time.Duration

	outageAlertAfter time.Duration

	intakeDownAfter time.Duration

	repeatWarnAfter time.Duration
}

var defaultTiming = timing{
	flushInterval:                   500 * time.Millisecond,
	retryBackoff:                    time.Second,
	maxRetryBackoff:                 30 * time.Second,
	attemptsBeforeIsolating:         8,
	failuresBeforeStoppingIsolation: 3,
	failuresBeforeNack:              5,
	writeTimeout:                    30 * time.Second,
	drainTimeout:                    10 * time.Second,
	deliveredIdle:                   100 * time.Millisecond,
	outageAlertAfter:                5 * time.Minute,
	intakeDownAfter:                 time.Minute,
	repeatWarnAfter:                 time.Hour,
}

type Ledger interface {
	Insert(ctx context.Context, entries []store.SpendLedgerEntry) (int64, error)
	InsertDetails(ctx context.Context, details []store.SpendLedgerDetail) (int64, error)
	ResolveNames(ctx context.Context, table store.ComponentTable, ids []string) (map[string]string, error)
	DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error)
}

type Model struct {
	ConfigID string
	Name     string
	Provider string
	Price    *pricing.ModelPrice
}

type ModelRef struct {
	ConfigID string
	Key      string
}

func modelRefOf(e spend.Event) ModelRef {
	if e.ModelConfigID != "" {
		return ModelRef{ConfigID: e.ModelConfigID}
	}
	return ModelRef{Key: e.ModelKey}
}

type Prices interface {
	Lookup(ctx context.Context, ref ModelRef) (Model, error)
}

type Config struct {
	Namespace string
	Broker    broker.Broker
	Ledger    Ledger
	Prices    Prices
	Logger    *slog.Logger
	Metrics   runtime.Metrics

	FlushInterval time.Duration
}

type Governor struct {
	cfg     Config
	logger  *slog.Logger
	metrics runtime.Metrics
	now     func() time.Time
	timing  timing
	pending chan pendingEvent
	queue   broker.Queue
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	stopReceiving context.CancelFunc
	receiving     sync.WaitGroup
	stopOnce      sync.Once

	stopping chan context.Context

	repairWarnings *logThrottle
	unknownModels  *logThrottle
	unkeyedModels  *logThrottle
	dropWarnings   *logThrottle
	nackWarnings   *logThrottle
	ledgerRefusals *logThrottle
	ledgerFaults   *logThrottle
	signalFailures *logThrottle

	nacked *recentIDs

	schemas schemas
	held    *heldEvents

	details detailWriter

	intakeHealth health.State
}

type pendingEvent struct {
	msg   *broker.Message
	event spend.Event

	failures    int
	lastFailure string

	returning bool

	refusalHeld bool
}

func New(cfg Config) *Governor {
	g := &Governor{
		cfg:      cfg,
		logger:   cfg.Logger.With("logger_name", "governor"),
		metrics:  runtime.MetricsOrNoop(cfg.Metrics),
		now:      time.Now,
		pending:  make(chan pendingEvent, 2*maxBatch),
		stopping: make(chan context.Context, 1),
		nacked:   newRecentIDs(maxBatch),
		schemas:  defaultSchemas(),
		held:     &heldEvents{},
		details:  detailWriter{names: newNameCache()},
	}
	t := defaultTiming
	if cfg.FlushInterval > 0 {
		t.flushInterval = cfg.FlushInterval
	}
	g.setTiming(t)
	return g
}

func (g *Governor) setTiming(t timing) {
	g.timing = t
	g.repairWarnings = newLogThrottle(t.repeatWarnAfter)
	g.unknownModels = newLogThrottle(t.repeatWarnAfter)
	g.unkeyedModels = newLogThrottle(t.repeatWarnAfter)
	g.dropWarnings = newLogThrottle(t.repeatWarnAfter)
	g.nackWarnings = newLogThrottle(t.repeatWarnAfter)
	g.ledgerRefusals = newLogThrottle(t.repeatWarnAfter)
	g.ledgerFaults = newLogThrottle(t.repeatWarnAfter)
	g.signalFailures = newLogThrottle(t.repeatWarnAfter)
	g.held.warnings = newLogThrottle(t.repeatWarnAfter)
	g.held.alerts = newLogThrottle(t.repeatWarnAfter)
	g.details.gaps = newDetailGaps(t.repeatWarnAfter)
}

func (g *Governor) Start(ctx context.Context) error {
	brk := g.cfg.Broker
	queueName, queueOpts := spend.EventQueue(g.cfg.Namespace)
	q, err := brk.CreateQueue(queueName, queueOpts)
	if err != nil {
		return fmt.Errorf("create spend queue %q: %w", queueName, err)
	}
	if err := brk.Subscribe(q, a2a.SpendEventTopic(g.cfg.Namespace)); err != nil {
		return fmt.Errorf("subscribe spend events: %w", err)
	}

	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	receiveCtx, stopReceiving := context.WithCancel(loopCtx)
	g.queue, g.cancel, g.stopReceiving = q, cancel, stopReceiving
	g.intakeHealth.Reset()
	g.receiving.Go(func() {
		broker.RunReceiveLoop(receiveCtx, brk, q, g.logger,
			broker.ReceiveLoopOptions{LoopName: "spend", Health: &g.intakeHealth}, g.enqueue(receiveCtx))
	})
	g.wg.Go(func() { g.writeLoop(loopCtx) })
	g.wg.Go(func() { g.watchIntake(loopCtx) })
	g.logger.Info("governor started", "queue", queueName, "retention", RetentionPeriod)
	return nil
}

func (g *Governor) Health() error {
	if since, down := g.intakeDownSince(); down {
		return fmt.Errorf("spend queue not bound since %s, so spend is not being filed: %w",
			since.UTC().Format(time.RFC3339), runtime.ErrDegraded)
	}
	return nil
}

func (g *Governor) intakeDownSince() (time.Time, bool) {
	for _, problem := range g.intakeHealth.Snapshot() {
		if time.Since(problem.Since) >= g.timing.intakeDownAfter {
			return problem.Since, true
		}
	}
	return time.Time{}, false
}

func (g *Governor) watchIntake(ctx context.Context) {
	ticker := time.NewTicker(max(g.timing.intakeDownAfter/4, 10*time.Millisecond))
	defer ticker.Stop()
	reported := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		since, down := g.intakeDownSince()
		switch {
		case down && !reported:
			reported = true
			g.logger.Error("spend queue is not bound, so spend is not being filed and no budget can be reached; check the broker permissions for the queue",
				"queue", g.queue.Name(), "downSince", since.UTC())
		case reported && len(g.intakeHealth.Snapshot()) == 0:
			reported = false
			g.logger.Info("spend queue is bound again")
		}
	}
}

func (g *Governor) Stop(ctx context.Context) error {
	if g.cancel != nil {
		g.stopOnce.Do(func() {
			g.stopReceiving()
			g.receiving.Wait()
			g.takeDelivered(ctx)
			g.stopping <- ctx
			g.cancel()
		})
	}
	g.wg.Wait()
	return nil
}

func (g *Governor) enqueue(ctx context.Context) func(*broker.Message) {
	return func(msg *broker.Message) {
		p, ok := g.admit(msg)
		if !ok {
			return
		}
		select {
		case g.pending <- p:
			return
		default:
		}
		select {
		case g.pending <- p:
		case <-ctx.Done():
		}
	}
}

func (g *Governor) takeDelivered(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, g.timing.drainTimeout)
	defer cancel()
	for len(g.pending) < cap(g.pending) {
		receiveCtx, cancelReceive := context.WithTimeout(ctx, g.timing.deliveredIdle)
		msg, err := g.cfg.Broker.Receive(receiveCtx, g.queue)
		cancelReceive()
		if err != nil || msg == nil {
			return
		}
		if p, ok := g.admit(msg); ok {
			g.pending <- p
		}
	}
}

func (g *Governor) admit(msg *broker.Message) (pendingEvent, bool) {
	event, verdict := g.decode(msg)
	switch verdict {
	case dropEvent:
		msg.Ack()
		return pendingEvent{}, false
	case holdEvent:
		return pendingEvent{}, false
	}
	p := pendingEvent{msg: msg, event: event}
	if g.nacked.contains(event.ID) {
		p.returning = true
		p.failures = g.timing.failuresBeforeNack - 1
	}
	return p, true
}

type verdict int

const (
	fileEvent verdict = iota
	dropEvent
	holdEvent
)

func (g *Governor) decode(msg *broker.Message) (spend.Event, verdict) {
	now := g.now()
	version, event, err := g.schemas.decode(msg.Payload)
	switch {
	case errors.Is(err, errNewerSchema), errors.Is(err, errNoUpconverter):
		g.hold(version, err, now)
		return event, holdEvent
	case err != nil:
		g.metrics.Counter(metricEventsDropped, 1, "reason", "not_decodable")
		cause := decodeFailure(err)
		if ok, suppressed := g.dropWarnings.admit(fmt.Sprint("not decodable: ", cause), now); ok {
			g.logger.Error("spend event dropped: not decodable",
				append([]any{"payloadBytes", len(msg.Payload), "schemaVersion", version, "suppressed", suppressed}, cause...)...)
		}
		return event, dropEvent
	}
	repairs, err := event.Sanitize(now)
	if err != nil {
		g.metrics.Counter(metricEventsDropped, 1, "reason", "invalid")
		if ok, suppressed := g.dropWarnings.admit(err.Error(), now); ok {
			g.eventLogger(event).Error("spend event dropped: invalid",
				append(spendFields(event), "reason", err.Error(), "repairs", repairs, "suppressed", suppressed)...)
		}
		return event, dropEvent
	}
	if due := g.dueRepairs(repairs, now); len(due) > 0 {
		g.eventLogger(event).Warn("spend event repaired", "repairs", due)
	}
	return event, fileEvent
}

func decodeFailure(err error) []any {
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return []any{"field", typeErr.Field, "wantType", typeErr.Type.String()}
	}
	if syntaxErr, ok := errors.AsType[*json.SyntaxError](err); ok {
		return []any{"syntaxOffset", syntaxErr.Offset}
	}
	return nil
}

func (g *Governor) dueRepairs(repairs []string, now time.Time) []string {
	var due []string
	for _, note := range repairs {
		if g.repairWarnings.allow(note, now) {
			due = append(due, note)
		}
	}
	return due
}

func (g *Governor) writeLoop(ctx context.Context) {
	ticker := time.NewTicker(g.timing.flushInterval)
	defer ticker.Stop()
	batch := make([]pendingEvent, 0, maxBatch)
	stranded := 0
	flush := func() {
		batch = g.topUp(batch, maxBatch)
		stranded += g.file(ctx, batch)
		batch = batch[:0]
	}
	for {
		select {
		case p := <-g.pending:
			batch = append(batch, p)
			if len(batch) == maxBatch {
				flush()
			}
		case <-ticker.C:
			if len(batch) > 0 {
				flush()
			}
			g.alertIfHeldTooLong(g.now())
		case <-ctx.Done():
			unfiled, err := g.drain(batch)
			if held := g.held.count(); unfiled+stranded+held > 0 {
				g.logger.Warn("governor stopped with spend unfiled; a durable queue redelivers it, a dev broker loses it",
					"events", unfiled+stranded, "held", held, "error", err)
			}
			return
		}
	}
}

func (g *Governor) file(ctx context.Context, batch []pendingEvent) (unacked int) {
	backoff := g.timing.retryBackoff
	var down outage
	for attempt := 1; len(batch) > 0; attempt++ {
		models, err := g.write(ctx, batch)
		if err == nil {
			ackAll(batch)
			g.filed(&down)
			g.writeDetails(ctx, batch, models)
			return 0
		}
		if ctx.Err() != nil {
			return len(batch)
		}
		permanent := isPermanent(err)
		g.reportUnfiled(&down, attempt, err, permanent, len(batch))
		if permanent || attempt >= g.timing.attemptsBeforeIsolating || slices.ContainsFunc(batch, isReturning) {
			unwritten, failed, filed, transient := g.fileIndividually(ctx, batch)
			if ctx.Err() != nil {
				return len(unwritten) + len(failed)
			}
			switch {
			case filed > 0:
				g.filed(&down)
				backoff = g.timing.retryBackoff
			case transient != nil:
				g.failed(&down, attempt, transient)
			}
			batch = append(g.topUp(unwritten, maxBatch-len(failed)), failed...)
			if len(batch) == 0 {
				return 0
			}
		}
		g.alertIfHeldTooLong(g.now())
		if !sleep(ctx, backoff) {
			return len(batch)
		}
		backoff = min(2*backoff, g.timing.maxRetryBackoff)
	}
	return 0
}

func (g *Governor) reportUnfiled(down *outage, attempt int, err error, permanent bool, events int) {
	if !permanent {
		g.logger.Warn("spend batch not filed", "error", err, "permanent", false, "attempt", attempt, "events", events)
		g.failed(down, attempt, err)
		return
	}
	if ok, suppressed := g.ledgerRefusals.admit("batch "+err.Error(), g.now()); ok {
		g.logger.Warn("spend batch not filed", "error", err, "permanent", true, "attempt", attempt, "events", events, "suppressed", suppressed)
	}
}

func (g *Governor) drain(batch []pendingEvent) (unfiled int, err error) {
	ctx, cancel := g.drainContext()
	defer cancel()
	for {
		batch = g.topUp(batch, maxBatch)
		if len(batch) == 0 {
			return 0, nil
		}
		if _, err := g.write(ctx, batch); err != nil {
			return len(batch) + len(g.pending), err
		}
		ackAll(batch)
		batch = batch[:0]
	}
}

func (g *Governor) drainContext() (context.Context, context.CancelFunc) {
	select {
	case ctx := <-g.stopping:
		if _, ok := ctx.Deadline(); ok {
			return context.WithCancel(ctx)
		}
	default:
	}
	return context.WithTimeout(context.Background(), g.timing.drainTimeout)
}

func isReturning(p pendingEvent) bool { return p.returning }

func (g *Governor) topUp(batch []pendingEvent, size int) []pendingEvent {
	for len(batch) < size {
		select {
		case p := <-g.pending:
			batch = append(batch, p)
		default:
			return batch
		}
	}
	return batch
}

type failedWrite struct {
	pendingEvent
	err error
}

func (g *Governor) fileIndividually(ctx context.Context, batch []pendingEvent) (unwritten, failed []pendingEvent, filed int, transient error) {
	var failures, refused []failedWrite
	var filedEvents []pendingEvent
	models := make(map[ModelRef]Model)
	defer func() { g.writeDetails(ctx, filedEvents, models) }()
	inARow := 0
	for i, p := range batch {
		if inARow >= g.timing.failuresBeforeStoppingIsolation {
			unwritten = slices.Clone(batch[i:])
			break
		}
		resolved, err := g.write(ctx, []pendingEvent{p})
		switch {
		case err == nil:
			p.msg.Ack()
			filed++
			filedEvents = append(filedEvents, p)
			maps.Copy(models, resolved)
			inARow = 0
		case ctx.Err() != nil:
			unwritten = slices.Clone(batch[i:])
			for _, f := range slices.Concat(failures, refused) {
				unwritten = append(unwritten, f.pendingEvent)
			}
			return unwritten, nil, filed, nil
		case isPermanent(err):
			refused = append(refused, failedWrite{pendingEvent: p, err: err})
			inARow = 0
		default:
			failures = append(failures, failedWrite{pendingEvent: p, err: err})
			if transient == nil {
				transient = err
			}
			inARow++
		}
	}
	filedAny := filed > 0
	if hold, ledgerFault := g.holdsRefused(refused, failures, unwritten, filedAny); hold {
		for i := range refused {
			g.reportRefusalHeld(&refused[i], !ledgerFault)
		}
		failures = append(failures, refused...)
	} else {
		for _, r := range refused {
			g.drop(r.pendingEvent, r.err)
		}
	}
	for _, f := range failures {
		g.countFailure(&f, filedAny)
		if f.failures < g.timing.failuresBeforeNack {
			failed = append(failed, f.pendingEvent)
			continue
		}
		g.nack(f)
	}
	return unwritten, failed, filed, transient
}

func (g *Governor) holdsRefused(refused, failures []failedWrite, unwritten []pendingEvent, filedAny bool) (hold, ledgerFault bool) {
	if filedAny || len(refused) == 0 {
		return false, false
	}
	if len(refused) == 1 || len(failures) > 0 || len(unwritten) > 0 {
		return true, false
	}
	if ok, suppressed := g.ledgerFaults.admit(refused[0].err.Error(), g.now()); ok {
		g.logger.Error("the ledger refuses every event as invalid; holding them as a ledger fault rather than dropping them",
			"events", len(refused), "error", refused[0].err, "suppressed", suppressed)
	}
	return true, true
}

func (g *Governor) reportRefusalHeld(f *failedWrite, name bool) {
	if f.refusalHeld {
		return
	}
	f.refusalHeld = true
	g.metrics.Counter(metricEventsRefusalHeld, 1)
	if name {
		g.eventLogger(f.event).Warn("spend event refused by the ledger with nothing filed beside it; holding it until another event files",
			append(spendFields(f.event), "error", f.err)...)
	}
}

func (g *Governor) countFailure(f *failedWrite, filedAny bool) {
	if !filedAny {
		return
	}
	reason := f.err.Error()
	if f.lastFailure != "" && f.lastFailure != reason {
		f.failures = 0
	}
	f.failures++
	f.lastFailure = reason
}

func (g *Governor) nack(f failedWrite) {
	g.metrics.Counter(metricEventsNacked, 1)
	if ok, suppressed := g.nackWarnings.admit(f.err.Error(), g.now()); ok {
		g.eventLogger(f.event).Error("spend event nacked: it keeps failing while other events file",
			append(spendFields(f.event), "error", f.err, "attempts", f.failures, "suppressed", suppressed)...)
	}
	g.nacked.add(f.event.ID)
	f.msg.Nack()
}

type outage struct {
	since    time.Time
	reported bool
}

func (g *Governor) failed(o *outage, attempt int, err error) {
	now := g.now()
	if o.since.IsZero() {
		o.since = now
		return
	}
	if elapsed := now.Sub(o.since); !o.reported && elapsed >= g.timing.outageAlertAfter {
		o.reported = true
		g.logger.Error("spend has not been filed for a while; unfiled spend stays in a durable queue, a dev broker drops it once its queue fills",
			"attempt", attempt, "elapsed", elapsed, "error", err)
	}
}

func (g *Governor) filed(o *outage) {
	if o.reported {
		g.logger.Info("spend ledger is filing again", "elapsed", g.now().Sub(o.since))
	}
	*o = outage{}
}

func (g *Governor) drop(p pendingEvent, err error) {
	g.metrics.Counter(metricEventsDropped, 1, "reason", "refused")
	g.eventLogger(p.event).Error("spend event dropped: the ledger refused it", append(spendFields(p.event), "error", err)...)
	p.msg.Ack()
}

func (g *Governor) eventLogger(e spend.Event) *slog.Logger {
	return g.logger.With("traceID", loggable(e.TraceID), "taskID", loggable(e.TaskID), "eventID", loggable(e.ID))
}

func spendFields(e spend.Event) []any {
	return []any{
		"modelName", loggable(e.ModelName),
		"modelKey", loggable(e.ModelKey),
		"modelConfigID", loggable(e.ModelConfigID),
		"surface", loggable(string(e.Surface)),
		"uncachedInputTokens", e.Tokens.UncachedInput,
		"outputTokens", e.Tokens.Output,
		"cacheReadTokens", e.Tokens.CacheRead,
		"cacheWriteTokens", e.Tokens.CacheWrite,
	}
}

const unloggableValue = "(unloggable)"

func loggable(value string) string {
	if len(value) > spend.MaxFieldBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return unloggableValue
	}
	return value
}

func ackAll(batch []pendingEvent) {
	for _, p := range batch {
		p.msg.Ack()
	}
}

var permanentSQLStateClasses = []string{"22", "54"}

func isPermanent(err error) bool {
	if _, ok := errors.AsType[priceLookupError](err); ok {
		return false
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return len(pgErr.Code) >= 2 && slices.Contains(permanentSQLStateClasses, pgErr.Code[:2])
	}
	if sqliteErr, ok := errors.AsType[*sqlite.Error](err); ok {
		switch sqliteErr.Code() & 0xff {
		case sqlitelib.SQLITE_CONSTRAINT, sqlitelib.SQLITE_TOOBIG, sqlitelib.SQLITE_MISMATCH:
			return true
		}
	}
	return false
}

func (g *Governor) write(ctx context.Context, batch []pendingEvent) (map[ModelRef]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, g.timing.writeTimeout)
	defer cancel()
	models := make(map[ModelRef]Model)
	entries := make([]store.SpendLedgerEntry, 0, len(batch))
	unpriced := 0
	for _, p := range batch {
		model, err := g.model(ctx, models, p.event)
		if err != nil {
			return nil, err
		}
		if model.Price == nil {
			unpriced++
		}
		entry := entryFor(p.event, model)
		fileContextSavings(&entry, p.event, model)
		entries = append(entries, entry)
	}
	filed, err := g.cfg.Ledger.Insert(ctx, entries)
	if err != nil {
		return nil, err
	}
	if unpriced > 0 {
		g.metrics.Counter(metricEventsUnpriced, int64(unpriced))
	}
	g.logger.Debug("spend batch filed", "events", len(entries), "filed", filed, "unpriced", unpriced)
	if duplicates := int64(len(entries)) - filed; duplicates > 0 {
		g.logger.Debug("spend events already filed were skipped", "duplicates", duplicates)
	}
	if filed > 0 {
		g.signalSpendFiled(ctx)
	}
	return models, nil
}

func (g *Governor) signalSpendFiled(ctx context.Context) {
	if g.cfg.Broker == nil {
		return
	}
	topic := a2a.SpendFiledTopic(g.cfg.Namespace)
	err := g.cfg.Broker.Publish(ctx, topic, &broker.Message{Topic: topic})
	if err == nil {
		return
	}
	if ok, suppressed := g.signalFailures.admit("spendFiled", g.now()); ok {
		g.logger.Warn("spend filed signal not published; the budget state catches up at its next heartbeat",
			"error", err, "suppressed", suppressed)
	}
}

func (g *Governor) model(ctx context.Context, seen map[ModelRef]Model, e spend.Event) (Model, error) {
	ref := modelRefOf(e)
	if ref == (ModelRef{}) {
		if ok, suppressed := g.unkeyedModels.admit(e.ModelName, g.now()); ok {
			g.eventLogger(e).Warn("spend names no model key, so it is filed unpriced; an inline model has no price",
				"modelName", loggable(e.ModelName), "suppressed", suppressed)
		}
		return Model{}, nil
	}
	if m, ok := seen[ref]; ok {
		return m, nil
	}
	m, err := g.cfg.Prices.Lookup(ctx, ref)
	if err != nil {
		return Model{}, priceLookupError{err}
	}
	if m.ConfigID == "" {
		if ok, suppressed := g.unknownModels.admit(cmp.Or(ref.ConfigID, ref.Key), g.now()); ok {
			named := []any{"modelKey", ref.Key}
			if ref.ConfigID != "" {
				named = []any{"modelConfigID", ref.ConfigID}
			}
			g.eventLogger(e).Warn("model matches no configuration; its spend is filed unpriced",
				append(named, "suppressed", suppressed)...)
		}
	}
	seen[ref] = m
	return m, nil
}

type priceLookupError struct{ err error }

func (e priceLookupError) Error() string { return "resolve model price: " + e.err.Error() }
func (e priceLookupError) Unwrap() error { return e.err }

func entryFor(e spend.Event, m Model) store.SpendLedgerEntry {
	entry := store.SpendLedgerEntry{
		ID:                  e.ID,
		CompletedTime:       e.CompletedTime,
		UserID:              optional(e.UserID),
		UserSub:             optional(e.UserSub),
		AgentID:             optional(e.AgentID),
		AgentName:           optional(e.AgentName),
		WorkflowID:          optional(e.WorkflowID),
		WorkflowName:        optional(e.WorkflowName),
		RootKind:            optional(string(e.RootKind)),
		RootID:              optional(e.RootID),
		RootName:            optional(e.RootName),
		RootNodeID:          optional(e.RootNodeID),
		EntrypointID:        optional(e.EntrypointID),
		EntrypointName:      optional(e.EntrypointName),
		Channel:             optional(string(e.Channel)),
		TriggerKind:         optional(string(e.TriggerKind)),
		TriggerID:           optional(e.TriggerID),
		Surface:             string(e.Surface),
		TaskID:              optional(e.TaskID),
		TraceID:             optional(e.TraceID),
		UncachedInputTokens: e.Tokens.UncachedInput,
		OutputTokens:        e.Tokens.Output,
		CacheReadTokens:     e.Tokens.CacheRead,
		CacheWriteTokens:    e.Tokens.CacheWrite,
	}
	if m.ConfigID != "" {
		entry.ModelConfigID = &m.ConfigID
	} else {
		entry.ModelName = optional(cmp.Or(e.ModelName, e.ModelKey))
	}
	if m.Price != nil {
		cost := spend.CostOf(e.Tokens, *m.Price)
		entry.Cost = &cost
	}
	return entry
}

func fileContextSavings(entry *store.SpendLedgerEntry, e spend.Event, m Model) {
	if e.AvoidedInputBytes == nil {
		return
	}
	tokens := spend.EstimatedTokens(*e.AvoidedInputBytes)
	entry.AvoidedInputTokens = &tokens
	if entry.Cost == nil || m.Price == nil {
		return
	}
	cost, ok := spend.AvoidedInputCost(tokens, e.Tokens, *entry.Cost)
	if !ok {
		cost = m.Price.Cost(pricing.BucketInput, tokens)
	}
	entry.AvoidedInputCost = &cost
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}
