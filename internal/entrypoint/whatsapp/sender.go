// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const senderIdleTimeout = 60 * time.Second

const senderErrorBackoff = 5 * time.Second

const senderRecoveryLease = 6 * time.Minute

const senderHardLeaseFactor = 5

const senderRecoveryInterval = time.Minute

const senderSettleTimeout = 10 * time.Second

const senderStopGrace = 10 * time.Second

const senderSettleGrace = 5 * time.Second

const metricWhatsAppSends = "whatsapp_sends"

type senderQueue interface {
	Enqueue(ctx context.Context, q dbutil.Querier, row *store.WhatsAppOutbound) error
	ClaimNext(ctx context.Context, gatewayID, recipient string, nowMs int64) (*store.WhatsAppOutbound, int64, error)
	MarkSent(ctx context.Context, q dbutil.Querier, id, wamid string, nowMs int64) error
	Requeue(ctx context.Context, id string, notBeforeMs, nowMs int64, errorCode int, errorDetail string) error
	MarkFailed(ctx context.Context, q dbutil.Querier, id string, errorCode int, errorDetail string, nowMs int64) error
	Park(ctx context.Context, req store.WhatsAppParkRequest) (supersededID string, yielded bool, err error)
	Supersede(ctx context.Context, id string, nowMs int64) error
	Get(ctx context.Context, id string) (*store.WhatsAppOutbound, error)
	WithTx(ctx context.Context, fn func(q dbutil.Querier) error) error
	RecoverInFlight(ctx context.Context, gatewayID string, maxAttempts int, lease, hardLease time.Duration, nowMs int64, skipRecipients []string) (requeued, exhausted int, err error)
	RenewClaims(ctx context.Context, gatewayID string, ids []string, nowMs int64) (int, error)
	ActiveRecipients(ctx context.Context, gatewayID string) ([]string, error)
}

type sendAttempt func(ctx context.Context, row *store.WhatsAppOutbound) (wamid string, err error)

type sendCommit func(ctx context.Context, q dbutil.Querier, row *store.WhatsAppOutbound, wamid string, sendErr error) (afterCommit func(), err error)

type senderPool struct {
	queue     senderQueue
	attempt   sendAttempt
	gatewayID string
	logger    *slog.Logger
	metrics   runtime.Metrics

	pace              time.Duration
	idleTimeout       time.Duration
	recoveryInterval  time.Duration
	settleTimeout     time.Duration
	recoveryHardLease time.Duration
	recoveryLease     time.Duration

	now    func() time.Time
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func(time.Duration) time.Duration

	onResult func(row *store.WhatsAppOutbound, wamid string, err error)

	onCommit sendCommit

	onRetired func(row *store.WhatsAppOutbound)

	sendCtx     context.Context
	sendCancel  context.CancelFunc
	claimCtx    context.Context
	claimCancel context.CancelFunc
	wg          sync.WaitGroup
	startOnce   sync.Once
	stopOnce    sync.Once
	started     atomic.Bool

	mu       sync.Mutex
	workers  map[string]chan struct{}
	inFlight map[string]struct{}
}

type senderConfig struct {
	Queue             senderQueue
	Attempt           sendAttempt
	GatewayID         string
	Logger            *slog.Logger
	Metrics           runtime.Metrics
	Pace              time.Duration
	IdleTimeout       time.Duration
	RecoveryInterval  time.Duration
	RecoveryLease     time.Duration
	RecoveryHardLease time.Duration
	SettleTimeout     time.Duration
	Now               func() time.Time
	Sleep             func(ctx context.Context, d time.Duration) error
	Jitter            func(time.Duration) time.Duration
	OnResult          func(row *store.WhatsAppOutbound, wamid string, err error)
	OnCommit          sendCommit
	OnRetired         func(row *store.WhatsAppOutbound)
}

func newSenderPool(cfg senderConfig) *senderPool {
	p := &senderPool{
		queue:             cfg.Queue,
		attempt:           cfg.Attempt,
		gatewayID:         cfg.GatewayID,
		logger:            cfg.Logger,
		metrics:           runtime.MetricsOrNoop(cfg.Metrics),
		pace:              cfg.Pace,
		idleTimeout:       cfg.IdleTimeout,
		recoveryInterval:  cfg.RecoveryInterval,
		recoveryLease:     cfg.RecoveryLease,
		recoveryHardLease: cfg.RecoveryHardLease,
		settleTimeout:     cfg.SettleTimeout,
		now:               cfg.Now,
		sleep:             cfg.Sleep,
		jitter:            cfg.Jitter,
		onResult:          cfg.OnResult,
		onCommit:          cfg.OnCommit,
		onRetired:         cfg.OnRetired,
		workers:           make(map[string]chan struct{}),
		inFlight:          make(map[string]struct{}),
	}
	if p.idleTimeout <= 0 {
		p.idleTimeout = senderIdleTimeout
	}
	if p.recoveryInterval <= 0 {
		p.recoveryInterval = senderRecoveryInterval
	}
	if p.recoveryLease <= 0 {
		p.recoveryLease = senderRecoveryLease
	}
	if p.recoveryHardLease <= 0 {
		p.recoveryHardLease = p.recoveryLease * senderHardLeaseFactor
	}
	if p.settleTimeout <= 0 {
		p.settleTimeout = senderSettleTimeout
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.sleep == nil {
		p.sleep = sleepUntilCancelled
	}
	if p.jitter == nil {
		p.jitter = randomJitter
	}
	p.sendCtx, p.sendCancel = context.WithCancel(context.Background())
	p.claimCtx, p.claimCancel = context.WithCancel(p.sendCtx)
	return p
}

func sleepUntilCancelled(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (p *senderPool) Start(ctx context.Context) {
	p.started.Store(true)
	p.recover(ctx)
	p.resume(ctx)
	p.startOnce.Do(func() {
		p.wg.Go(p.maintenanceLoop)
	})
}

func (p *senderPool) running() bool {
	return p.started.Load() && p.claimCtx.Err() == nil
}

func (p *senderPool) recover(ctx context.Context) {
	requeued, exhausted, err := p.queue.RecoverInFlight(ctx, p.gatewayID, maxSendAttempts,
		p.recoveryLease, p.recoveryHardLease, p.now().UnixMilli(), p.liveRecipients())
	if err != nil {
		p.logger.Error("failed to recover in-flight whatsapp sends", "error", err)
		return
	}
	if requeued == 0 && exhausted == 0 {
		return
	}
	p.logger.Warn("recovered whatsapp sends abandoned by a previous run",
		"requeued", requeued, "exhausted", exhausted)
	p.count(requeued, "recovered_requeue")
	p.count(exhausted, "recovered_exhausted")
}

func (p *senderPool) resume(ctx context.Context) {
	recipients, err := p.queue.ActiveRecipients(ctx, p.gatewayID)
	if err != nil {
		p.logger.Error("failed to list recipients with queued whatsapp sends", "error", err)
		return
	}
	for _, r := range recipients {
		p.notify(r)
	}
	if len(recipients) > 0 {
		p.logger.Info("resuming queued whatsapp sends", "recipients", len(recipients))
	}
}

func (p *senderPool) maintenanceLoop() {
	ticker := time.NewTicker(p.recoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.claimCtx.Done():
			return
		case <-ticker.C:
			p.renewClaims()
			p.recover(p.claimCtx)
			p.resume(p.claimCtx)
		}
	}
}

func (p *senderPool) renewClaims() {
	p.mu.Lock()
	ids := make([]string, 0, len(p.inFlight))
	for id := range p.inFlight {
		ids = append(ids, id)
	}
	p.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	if _, err := p.queue.RenewClaims(p.sendCtx, p.gatewayID, ids, p.now().UnixMilli()); err != nil {
		p.logger.Warn("failed to renew whatsapp send claims", "rows", len(ids), "error", err)
	}
}

func (p *senderPool) liveRecipients() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.workers))
	for recipient := range p.workers {
		out = append(out, recipient)
	}
	return out
}

func (p *senderPool) hold(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight[id] = struct{}{}
}

func (p *senderPool) release(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inFlight, id)
}

func (p *senderPool) Enqueue(ctx context.Context, q dbutil.Querier, row *store.WhatsAppOutbound) error {
	if err := p.queue.Enqueue(ctx, q, row); err != nil {
		return err
	}
	p.notify(row.Recipient)
	return nil
}

func (p *senderPool) notify(recipient string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.claimCtx.Err() != nil {
		return
	}
	if wake, ok := p.workers[recipient]; ok {
		select {
		case wake <- struct{}{}:
		default:
		}
		return
	}
	wake := make(chan struct{}, 1)
	p.workers[recipient] = wake
	p.wg.Go(func() { p.workerLoop(recipient, wake) })
}

func (p *senderPool) Stop(ctx context.Context, grace time.Duration) bool {
	drained := false
	p.stopOnce.Do(func() {
		p.claimCancel()

		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-done:
			p.sendCancel()
			drained = true
			return
		case <-ctx.Done():
			p.logger.Warn("whatsapp sender drain cut short by the shutdown deadline")
		case <-timer.C:
			p.logger.Warn("whatsapp sender drain timed out; cancelling in-flight sends",
				"graceMs", grace.Milliseconds())
		}
		p.sendCancel()

		settled := time.NewTimer(senderSettleGrace)
		defer settled.Stop()
		select {
		case <-done:
			drained = true
		case <-settled.C:
			p.logger.Warn("whatsapp sender did not finish recording its outcomes; abandoning the wait",
				"settleGraceMs", senderSettleGrace.Milliseconds())
		}
	})
	return drained
}

func (p *senderPool) workerLoop(recipient string, wake <-chan struct{}) {
	logger := p.logger.With("recipientFP", strutil.PIIFingerprint("whatsapp:"+recipient))
	for p.claimCtx.Err() == nil && p.sendStepSafely(logger, recipient, wake) {
	}
}

func (p *senderPool) sendStepSafely(logger *slog.Logger, recipient string, wake <-chan struct{}) (keepGoing bool) {
	defer func() {
		if r := recover(); r != nil {
			logRecoveredPanic(logger, "panic in whatsapp send worker", r)
			keepGoing = true
		}
	}()
	return p.sendStep(logger, recipient, wake)
}

func (p *senderPool) sendStep(logger *slog.Logger, recipient string, wake <-chan struct{}) bool {
	row, dueAtMs, err := p.queue.ClaimNext(p.claimCtx, p.gatewayID, recipient, p.now().UnixMilli())
	if err != nil {
		logger.Warn("failed to claim next whatsapp send", "error", err)
		return p.pause(senderErrorBackoff)
	}
	if row == nil {
		if dueAtMs > 0 {
			return p.pause(p.durationUntil(dueAtMs))
		}
		return !p.retire(recipient, wake)
	}

	if p.deliver(logger, row) && p.pace > 0 {
		return p.pause(p.pace)
	}
	return true
}

func (p *senderPool) retire(recipient string, wake <-chan struct{}) bool {
	timer := time.NewTimer(p.idleTimeout)
	defer timer.Stop()
	select {
	case <-p.claimCtx.Done():
		return true
	case <-wake:
		return false
	case <-timer.C:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(wake) > 0 {
		return false
	}
	delete(p.workers, recipient)
	return true
}

func (p *senderPool) pause(d time.Duration) bool {
	if d <= 0 {
		return p.claimCtx.Err() == nil
	}
	return p.sleep(p.claimCtx, d) == nil
}

func (p *senderPool) durationUntil(atMs int64) time.Duration {
	return max(time.Duration(atMs-p.now().UnixMilli())*time.Millisecond, 0)
}

func (p *senderPool) deliver(logger *slog.Logger, row *store.WhatsAppOutbound) bool {
	p.hold(row.ID)
	defer p.release(row.ID)

	wamid, err := p.attempt(p.sendCtx, row)
	nowMs := p.now().UnixMilli()
	if err == nil {
		p.report(row, wamid, nil)
		p.countDelivered(p.recordSent(logger, row, wamid, nowMs), "sent")
		return true
	}

	if errors.Is(err, errRequestAccepted) {
		logger.Warn("whatsapp message accepted but its response was lost; recorded as sent without a message id",
			append([]any{"outboundID", row.ID}, safeGraphErrFields(err)...)...)
		p.report(row, "", nil)
		p.countDelivered(p.recordSent(logger, row, "", nowMs), "sent_unconfirmed")
		return true
	}

	if p.sendCtx.Err() != nil {
		logger.Info("whatsapp send interrupted by shutdown", "outboundID", row.ID)
		return false
	}

	code, detail, retryable, usage := classifySendError(err)
	if errors.Is(err, errOutboundStale) {
		p.retireStale(logger, row, nowMs)
		return false
	}
	if isWindowClosed(err) {
		p.park(logger, row, code, detail, nowMs)
		return false
	}
	if retryable && row.Attempts < maxSendAttempts {
		wait := p.jitter(retryDelay(usage, code, row.Attempts-1))
		logger.Warn("whatsapp send failed; retrying",
			append([]any{"outboundID", row.ID, "attempt", row.Attempts, "waitMs", wait.Milliseconds()},
				safeGraphErrFields(err)...)...)
		retryCtx, cancelRetry := p.settleCtx()
		defer cancelRetry()
		if reqErr := p.queue.Requeue(retryCtx, row.ID, nowMs+wait.Milliseconds(), nowMs, code, detail); reqErr != nil {
			logger.Error("failed to requeue whatsapp send", "outboundID", row.ID, "error", reqErr)
		}
		p.count(1, "retry")
		return false
	}

	logger.Error("whatsapp message delivery failed permanently",
		append([]any{"outboundID", row.ID, "taskID", row.TaskID, "traceID", row.TraceID,
			"kind", row.Kind, "attempts", row.Attempts}, safeGraphErrFields(err)...)...)
	settleCtx, cancelSettle := p.settleCtx()
	defer cancelSettle()
	if failErr := p.settle(settleCtx, row, sendOutcome{err: err, code: code, detail: detail}, nowMs); failErr != nil {
		logger.Error("failed to record terminal whatsapp send failure", "outboundID", row.ID, "error", failErr)
	}
	p.count(1, "failed")
	p.report(row, "", err)
	return false
}

func randomJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d + time.Duration(rand.Int64N(int64(d/4)+1))
}

func (p *senderPool) settleCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(p.sendCtx), p.settleTimeout)
}

func (p *senderPool) park(logger *slog.Logger, row *store.WhatsAppOutbound, code int, detail string, nowMs int64) {
	ctx, cancel := p.settleCtx()
	defer cancel()
	supersededID, yielded, err := p.queue.Park(ctx, store.WhatsAppParkRequest{
		GatewayID:      p.gatewayID,
		ID:             row.ID,
		Recipient:      row.Recipient,
		MayYieldToTask: yieldsToOwnTask(row),
		ErrorCode:      code,
		ErrorDetail:    detail,
		NowMs:          nowMs,
	})
	if err != nil {
		logger.Error("failed to park a whatsapp reply outside the customer-service window",
			"outboundID", row.ID, "taskID", row.TaskID, "error", err)
		return
	}
	if yielded {
		logger.Info("dropped an attachment rather than displace its own parked reply",
			"outboundID", row.ID, "taskID", row.TaskID, "kind", row.Kind)
		p.metrics.Counter(metricWhatsAppParkedSuperseded, 1, "gatewayId", p.gatewayID, "reason", parkRetireYielded)
		p.reportRetired(ctx, logger, row.ID)
		return
	}
	logger.Info("parked a whatsapp reply until the customer-service window reopens",
		"outboundID", row.ID, "taskID", row.TaskID, "traceID", row.TraceID,
		"kind", row.Kind, "metaErrorCode", code)
	p.metrics.Counter(metricWhatsAppParkedReplies, 1, "gatewayId", p.gatewayID, "outcome", "parked")
	if supersededID == "" {
		return
	}
	logger.Info("dropped an older parked whatsapp reply; one park per recipient",
		"outboundID", supersededID, "replacedBy", row.ID)
	p.metrics.Counter(metricWhatsAppParkedSuperseded, 1, "gatewayId", p.gatewayID, "reason", parkRetireDisplaced)
	p.reportRetired(ctx, logger, supersededID)
}

func (p *senderPool) reportRetired(ctx context.Context, logger *slog.Logger, id string) {
	if p.onRetired == nil {
		return
	}
	row, err := p.queue.Get(ctx, id)
	if err != nil || row == nil {
		if err != nil {
			logger.Error("failed to read a retired whatsapp row", "outboundID", id, "error", err)
		}
		return
	}
	p.onRetired(row)
}

func (p *senderPool) retireStale(logger *slog.Logger, row *store.WhatsAppOutbound, nowMs int64) {
	ctx, cancel := p.settleCtx()
	defer cancel()
	if err := p.queue.Supersede(ctx, row.ID, nowMs); err != nil {
		logger.Error("failed to retire a stale whatsapp reply", "outboundID", row.ID, "error", err)
		return
	}
	logger.Info("retired a whatsapp reply that outlived its conversation",
		"outboundID", row.ID, "taskID", row.TaskID, "kind", row.Kind,
		"ageMs", nowMs-row.CreatedAt)
	p.metrics.Counter(metricWhatsAppParkedSuperseded, 1, "gatewayId", p.gatewayID, "reason", parkRetireStale)
}

type sendOutcome struct {
	wamid  string
	err    error
	code   int
	detail string
}

func (p *senderPool) settle(ctx context.Context, row *store.WhatsAppOutbound, out sendOutcome, nowMs int64) error {
	var afterCommit func()
	err := p.queue.WithTx(ctx, func(q dbutil.Querier) error {
		afterCommit = nil
		writeErr := p.settleState(ctx, q, row, out, nowMs)
		if writeErr != nil || p.onCommit == nil {
			return writeErr
		}
		after, commitErr := p.onCommit(ctx, q, row, out.wamid, out.err)
		afterCommit = after
		return commitErr
	})
	if err != nil {
		return err
	}
	if afterCommit != nil {
		afterCommit()
	}
	return nil
}

func (p *senderPool) settleState(ctx context.Context, q dbutil.Querier, row *store.WhatsAppOutbound, out sendOutcome, nowMs int64) error {
	if out.err != nil {
		return p.queue.MarkFailed(ctx, q, row.ID, out.code, out.detail, nowMs)
	}
	return p.queue.MarkSent(ctx, q, row.ID, out.wamid, nowMs)
}

func (p *senderPool) recordSent(logger *slog.Logger, row *store.WhatsAppOutbound, wamid string, nowMs int64) bool {
	ctx, cancel := p.settleCtx()
	defer cancel()
	if err := p.settle(ctx, row, sendOutcome{wamid: wamid}, nowMs); err == nil {
		return true
	}
	fallbackCtx, cancelFallback := p.settleCtx()
	defer cancelFallback()
	if fallbackErr := p.settle(fallbackCtx, row, sendOutcome{}, nowMs); fallbackErr != nil {
		logger.Error("whatsapp message sent but not recorded", "outboundID", row.ID, "error", fallbackErr)
		return false
	}
	logger.Warn("whatsapp message recorded as sent without its message id; delivery receipts for it cannot be correlated",
		"outboundID", row.ID)
	return true
}

func (p *senderPool) report(row *store.WhatsAppOutbound, wamid string, err error) {
	if p.onResult != nil {
		p.onResult(row, wamid, err)
	}
}

func (p *senderPool) countDelivered(recorded bool, outcome string) {
	if !recorded {
		outcome = "sent_unrecorded"
	}
	p.count(1, outcome)
}

func (p *senderPool) count(n int, outcome string) {
	if n <= 0 {
		return
	}
	p.metrics.Counter(metricWhatsAppSends, int64(n), "gatewayId", p.gatewayID, "outcome", outcome)
}
