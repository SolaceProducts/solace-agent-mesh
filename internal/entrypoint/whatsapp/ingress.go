// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	goruntime "runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

const spoolBudget = 5 * time.Second

const defaultClaimLease = 15 * time.Minute

const maxTransientAttempts = 50

const ingressDownGrace = 30 * time.Second

const ingressFailureWarnWindow = 5 * time.Minute

const ingressReceivePoll = 30 * time.Second

const maxAuthzDenialAttempts = 6

const ingressWorkerQueueCap = 512

const (
	minIngressWorkers = 4
	maxIngressWorkers = 16
)

const defaultTransientRetryDelay = 2 * time.Second

const maxTransientRetryDelay = 5 * time.Minute

func retryPace(base time.Duration, attempts int) time.Duration {
	if base <= 0 {
		return 0
	}
	const maxShift = 8
	pace := base << min(max(attempts, 0), maxShift)
	return min(pace, maxTransientRetryDelay)
}

const (
	defaultInFlightClaimWait = 30 * time.Second
	defaultInFlightClaimPoll = 2 * time.Second
)

type inboundOutcome int

const (
	outcomeSubmitted inboundOutcome = iota

	outcomeDeclined

	outcomeUnauthorized

	outcomeHILUnauthorized

	outcomeAuthzUnavailable

	outcomeStalled

	outcomeTransient
)

func (o inboundOutcome) LogValue() slog.Value { return slog.StringValue(o.String()) }

func (o inboundOutcome) String() string {
	switch o {
	case outcomeSubmitted:
		return "submitted"
	case outcomeDeclined:
		return "declined"
	case outcomeTransient:
		return "transient"
	case outcomeUnauthorized:
		return "unauthorized"
	case outcomeHILUnauthorized:
		return "unauthorized_hil"
	case outcomeAuthzUnavailable:
		return "authz_unavailable"
	case outcomeStalled:
		return "stalled"
	}
	return "unknown"
}

func inboundRetryPolicy(outcome inboundOutcome) (maxAttempts int, decline, kind string) {
	switch outcome {
	case outcomeUnauthorized:
		return maxAuthzDenialAttempts, msgNotAuthorized, "unauthorized"
	case outcomeHILUnauthorized:
		return maxAuthzDenialAttempts, msgHILSubmitFailed, "unauthorized"
	case outcomeAuthzUnavailable:
		return maxTransientAttempts, msgTemporaryFailure, "authz_unavailable"
	case outcomeStalled:
		return stalledRetryBudget, msgTemporaryFailure, store.WhatsAppStalledFailure
	}
	return maxTransientAttempts, msgTemporaryFailure, "transient"
}

const stalledRetryBudget = 5

func spentBudget(claim store.WhatsAppClaim, outcome inboundOutcome) bool {
	if claim.AttemptsTotal >= maxTransientAttempts {
		return true
	}
	budget, _, kind := inboundRetryPolicy(outcome)
	return claim.FailureKind == kind && claim.Attempts >= budget
}

func retryable(outcome inboundOutcome) bool {
	switch outcome {
	case outcomeTransient, outcomeUnauthorized, outcomeHILUnauthorized, outcomeAuthzUnavailable, outcomeStalled:
		return true
	}
	return false
}

type ingressEnvelope struct {
	Context  webhookChangeValue `json:"context"`
	Message  inboundMessage     `json:"message"`
	Identity ingressIdentity    `json:"identity,omitempty"`

	authorized       *auth.Subject
	authorizedSource string

	ackReason string
}

type ingressIdentity struct {
	LookupValue string `json:"lookup_value,omitempty"`

	LookupValueExact bool `json:"lookup_value_exact"`

	ContinuityToken string `json:"continuity_token,omitempty"`
}

const (
	metricWhatsAppInboundMessages  = "whatsapp_inbound_messages"
	metricWhatsAppAckedWithoutTask = "whatsapp_acked_without_task"
)

const (
	ackNoTaskStalled      = "stalled"
	ackNoTaskUntracked    = "untracked"
	ackNoTaskExhausted    = "retries_exhausted"
	ackNoTaskDeclined     = "declined"
	ackNoTaskUnauthorized = "unauthorized"
	ackNoTaskUndecodable  = "undecodable"
)

type messageClaims interface {
	Claim(ctx context.Context, gatewayID, wamid string, lease time.Duration) (store.WhatsAppClaim, error)
	MarkDone(ctx context.Context, gatewayID, wamid string, token int64) (bool, error)
	ReleaseForRetry(ctx context.Context, gatewayID, wamid string, token int64, kind string) (bool, error)
}

func ingressTopic(namespace, gatewayID string) string {
	return fmt.Sprintf("%s/sam/v1/gdk/whatsapp/ingress/%s", namespace, gatewayID)
}

func hashSenderID(s string) uint64 {
	const (
		offset = uint64(1469598103934665603)
		prime  = uint64(1099511628211)
	)
	h := offset
	for i := range len(s) {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func resolveIngressWorkers() int {
	return max(min(goruntime.GOMAXPROCS(0), maxIngressWorkers), minIngressWorkers)
}

const ingressBindProbeTimeout = 50 * time.Millisecond

func (a *Adapter) initIngress() error {
	brk := a.brokerOrNil()
	if brk == nil {
		return fmt.Errorf("whatsapp adapter: no broker configured; inbound messages cannot be durably queued")
	}
	if a.ingressKick == nil {
		a.ingressKick = make(chan struct{}, 1)
	}
	name := a.ingressQueueName()
	queue, err := brk.CreateQueue(name, broker.QueueOptions{
		Named:       true,
		Exclusive:   true,
		OnBindState: a.onIngressBindState,
		QuotaMB:     1024,
	})
	if err != nil {
		return fmt.Errorf("create whatsapp ingress queue %q: %w", name, err)
	}
	for _, topic := range []string{
		ingressTopic(a.core.Namespace(), a.core.GatewayID()),
		a2a.WhatsAppDoorbellTopic(a.core.Namespace(), a.core.GatewayID()),
	} {
		if err := brk.Subscribe(queue, topic); err != nil {
			return fmt.Errorf("subscribe whatsapp ingress queue to %q: %w", topic, err)
		}
	}
	a.ingressQueue = queue
	if bound := a.probeIngressBinding(); !bound {
		return nil
	}
	a.logger.Info("whatsapp ingress queue bound", "queue", name)
	return nil
}

func (a *Adapter) probeIngressBinding() (bound bool) {
	ctx, cancel := context.WithTimeout(a.ctx, ingressBindProbeTimeout)
	defer cancel()

	msg, err := a.brokerOrNil().Receive(ctx, a.ingressQueue)
	switch {
	case err == nil && msg != nil:
		a.pendingIngress = msg
		return true
	case err == nil, errors.Is(err, context.DeadlineExceeded):
		return true
	case a.ctx.Err() != nil:
		return true
	default:
		a.health.Set("ingress", health.Degraded, health.DetailIngressDown)
		if a.ingressDown.CompareAndSwap(false, true) {
			a.ingressUnboundSince = time.Now()
			a.logger.Error("whatsapp ingress queue did not bind; refusing inbound webhooks until it does",
				"queue", a.ingressQueue.Name(), "error", err)
			return false
		}
		a.logWithSuppression("whatsapp ingress queue still unbound", err, time.Since(a.ingressUnboundSince))
		return false
	}
}

func (a *Adapter) startIngressWorkers() context.Context {
	ctx, cancel := context.WithCancel(a.ctx)
	a.ingressCtx, a.ingressCancel = ctx, cancel

	workers := make([]chan *broker.Message, resolveIngressWorkers())
	for i := range workers {
		workers[i] = make(chan *broker.Message, ingressWorkerQueueCap)
		ch := workers[i]
		a.ingressWG.Go(func() { a.ingressWorkerLoop(ctx, ch) })
	}
	a.ingressWorkers = workers
	return ctx
}

func (a *Adapter) startIngressDispatch(ctx context.Context) {
	a.ingressWG.Go(func() { a.ingressDispatchLoop(ctx) })
	a.logger.Info("whatsapp ingress consumer started", "workers", len(a.ingressWorkers), "queueCap", ingressWorkerQueueCap)
}

func (a *Adapter) stopIngressConsumer() {
	if a.ingressCancel != nil {
		a.ingressCancel()
	}
	a.ingressWG.Wait()
	a.ingressWorkers = nil
}

func (a *Adapter) releaseIngressBind() {
	if a.ingressQueue == nil {
		return
	}
	if brk := a.brokerOrNil(); brk != nil {
		if err := brk.DeleteQueue(a.ingressQueue.Name()); err != nil {
			a.logger.Warn("release whatsapp ingress queue receiver", "queue", a.ingressQueue.Name(), "error", err)
		}
	}
	a.ingressQueue = nil
}

func (a *Adapter) DeprovisionAdapterQueues(_ context.Context) error {
	brk := a.brokerOrNil()
	if brk == nil || a.core == nil {
		return nil
	}
	var errs []error
	for _, name := range []string{
		a2a.WhatsAppIngressQueueNameV2(a.core.Namespace(), a.core.GatewayID()),
		a2a.WhatsAppIngressQueueName(a.core.Namespace(), a.core.GatewayID()),
	} {
		if err := brk.DeprovisionQueue(name); err != nil {
			errs = append(errs, fmt.Errorf("deprovision whatsapp ingress queue %q: %w", name, err))
			continue
		}
		a.logger.Info("deprovisioned whatsapp ingress queue", "queue", name)
	}
	return errors.Join(errs...)
}

func (a *Adapter) brokerOrNil() broker.Broker {
	if a.core == nil {
		return nil
	}
	svc := a.core.Services()
	if svc == nil {
		return nil
	}
	return svc.Broker()
}

func (a *Adapter) publishInbound(ctx context.Context, value *webhookChangeValue, msg *inboundMessage) error {
	if a.forDifferentNumber(value) {
		a.logForeignNumberDrop("ignoring inbound message for a different phone number", msg.ID, value)
		return nil
	}
	brk := a.brokerOrNil()
	if brk == nil || a.ingressQueue == nil {
		return errors.New("whatsapp ingress queue is not available")
	}
	if a.ingressDown.Load() {
		return errors.New("whatsapp ingress consumer is not receiving")
	}

	env := ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}
	env.Context.Messages = nil
	env.Context.Statuses = nil
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal inbound message %q: %w", msg.ID, err)
	}

	topic := ingressTopic(a.core.Namespace(), a.core.GatewayID())
	if err := brk.PublishGuaranteed(ctx, topic, &broker.Message{
		Topic:          topic,
		Payload:        payload,
		UserProperties: map[string]string{"senderId": msg.senderID()},
	}); err != nil {
		return fmt.Errorf("publish inbound message %q to ingress queue: %w", msg.ID, err)
	}
	a.logger.Debug("inbound message spooled", "wamid", msg.ID, "type", msg.Type)
	return nil
}

func (a *Adapter) ingressDispatchLoop(ctx context.Context) {
	bo := backoff.Exponential{
		Initial: 100 * time.Millisecond,
		Max:     5 * time.Second,
		Jitter:  0.2,
	}
	brk := a.brokerOrNil()
	if held := a.pendingIngress; held != nil {
		a.pendingIngress = nil
		if !a.dispatch(ctx, held) {
			return
		}
	}
	var failingSince time.Time
	for {
		recvCtx, cancel := context.WithTimeout(ctx, a.pollInterval())
		msg, err := brk.Receive(recvCtx, a.ingressQueue)
		cancel()
		if err != nil && ctx.Err() != nil {
			return
		}
		if err != nil && errors.Is(err, context.DeadlineExceeded) {
			bo.Reset()
			failingSince = time.Time{}
			a.clearIngressDown()
			continue
		}
		if err != nil {
			if failingSince.IsZero() {
				failingSince = time.Now()
			}
			a.markIngressDown(time.Since(failingSince), err)
			if waitErr := bo.Wait(ctx); waitErr != nil {
				return
			}
			continue
		}
		bo.Reset()
		failingSince = time.Time{}
		a.clearIngressDown()
		if msg == nil {
			continue
		}
		if !a.dispatch(ctx, msg) {
			return
		}
	}
}

func (a *Adapter) dispatch(ctx context.Context, msg *broker.Message) bool {
	if a2a.IsWhatsAppDoorbellTopic(msg.Topic) {
		a.ring(string(msg.Payload))
		msg.Ack()
		return true
	}
	return a.routeToWorker(ctx, msg)
}

func (a *Adapter) ring(recipient string) {
	if recipient == "" {
		return
	}
	if pool := a.runningSender(); pool != nil {
		pool.notify(recipient)
	}
}

func (a *Adapter) grace() time.Duration {
	if a.ingressGrace > 0 {
		return a.ingressGrace
	}
	return ingressDownGrace
}

func (a *Adapter) pollInterval() time.Duration {
	if a.receivePoll > 0 {
		return a.receivePoll
	}
	return ingressReceivePoll
}

func (a *Adapter) markIngressDown(failingFor time.Duration, err error) {
	if failingFor < a.grace() {
		a.logger.Warn("whatsapp ingress receive error", "error", err, "failingFor", failingFor)
		return
	}
	a.health.Set("ingress", health.Degraded, health.DetailIngressDown)
	if a.ingressDown.CompareAndSwap(false, true) {
		a.logger.Error("whatsapp ingress consumer cannot receive; refusing inbound webhooks until it recovers",
			"queue", a.ingressQueue.Name(), "failingFor", failingFor, "error", err)
		return
	}
	a.logWithSuppression("whatsapp ingress receive still failing", err, failingFor)
}

func (a *Adapter) logWithSuppression(msg string, err error, failingFor time.Duration) {
	warn, suppressed, _ := a.ingressFailures.shouldWarn(msg, ingressFailureWarnWindow)
	if !warn {
		a.logger.Debug(msg, "error", err, "failingFor", failingFor)
		return
	}
	a.logger.Warn(msg, "error", err, "failingFor", failingFor, "suppressedSinceLast", suppressed)
}

func (a *Adapter) clearIngressDown() {
	a.health.Clear("ingress")
	if a.ingressDown.CompareAndSwap(true, false) {
		a.logger.Info("whatsapp ingress consumer recovered; accepting inbound webhooks again",
			"queue", a.ingressQueue.Name())
	}
}

func (a *Adapter) routeToWorker(ctx context.Context, msg *broker.Message) bool {
	idx := hashSenderID(msg.UserProperties["senderId"]) % uint64(len(a.ingressWorkers))
	select {
	case a.ingressWorkers[idx] <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *Adapter) ingressWorkerLoop(ctx context.Context, ch <-chan *broker.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			a.handleIngressMessageSafely(ctx, msg)
		}
	}
}

type ingressSettlement int

const (
	settleAck ingressSettlement = iota
	settleNack
	settleAbandon
)

func (s ingressSettlement) String() string {
	switch s {
	case settleAck:
		return "ack"
	case settleNack:
		return "nack"
	case settleAbandon:
		return "abandon"
	}
	return "unknown"
}

func (a *Adapter) handleIngressMessageSafely(ctx context.Context, msg *broker.Message) {
	defer func() {
		if r := recover(); r != nil {
			a.logPanic("panic outside inbound message processing", r, "")
			msg.Nack()
		}
	}()
	a.handleIngressMessage(ctx, msg)
}

func (a *Adapter) logPanic(msg string, recovered any, wamid string) {
	logRecoveredPanic(a.logger, msg, recovered, "wamid", wamid)
}

func logRecoveredPanic(logger *slog.Logger, msg string, recovered any, attrs ...any) {
	logger.Error(msg, append(attrs,
		"panicType", fmt.Sprintf("%T", recovered),
		"stack", string(debug.Stack()))...)
}

func (a *Adapter) handleIngressMessage(ctx context.Context, msg *broker.Message) {
	var env ingressEnvelope
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		a.logger.Error("undecodable whatsapp ingress message discarded", "error", err, "payloadBytes", len(msg.Payload))
		a.countAckedWithoutTask(ackNoTaskUndecodable)
		msg.Ack()
		return
	}
	if msg.Redelivered {
		a.logger.Info("whatsapp ingress message redelivered", "wamid", env.Message.ID, "type", env.Message.Type)
	}
	switch a.deliverInbound(ctx, &env) {
	case settleAck:
		msg.Ack()
	case settleNack:
		msg.Nack()
	case settleAbandon:
	}
}

func (a *Adapter) deliverInbound(ctx context.Context, env *ingressEnvelope) ingressSettlement {
	msg := &env.Message
	if msg.senderID() == "" {
		a.countInbound(msg)
		a.countAckedWithoutTask(refusalSenderNoValue)
		a.logger.Warn("whatsapp inbound message dropped: it names no sender",
			"wamid", msg.ID, "messageType", msg.Type)
		return settleAck
	}
	claim, ok := a.claimInbound(ctx, msg.ID)
	if !ok {
		a.pauseBeforeRetry(ctx, 0)
		return settleNack
	}
	if claim.Outcome != store.ClaimAcquired {
		return settleAck
	}
	firstDelivery := claim.Attempts == 0
	if firstDelivery {
		a.countInbound(msg)
	}
	if claim.FailureKind == store.WhatsAppStalledFailure && spentBudget(claim, outcomeStalled) {
		a.logger.Error("whatsapp inbound message keeps killing its consumer; declining without processing",
			"wamid", msg.ID, "type", msg.Type, "attempts", claim.Attempts)
		a.reply("", msg.senderID(), msgTemporaryFailure)
		a.countAckedWithoutTask(ackNoTaskStalled)
		a.completeClaim(ctx, msg.ID, claim.Token)
		return settleAck
	}
	if claim.Attempts == 0 && !a.admitMessageRate(msg) {
		a.countAckedWithoutTask(refusalRateLimited)
		a.completeClaim(ctx, msg.ID, claim.Token)
		return settleAck
	}
	if hook := a.afterClaim; hook != nil && !hook(msg.ID) {
		return settleAbandon
	}

	outcome := a.processInboundSafely(ctx, env, firstDelivery)
	ackReason := ""
	if retryable(outcome) && msg.ID == "" {
		a.logger.Error("inbound message without a wamid failed retryably; declining rather than retrying untracked",
			"type", msg.Type, "outcome", outcome)
		a.reply("", msg.senderID(), msgTemporaryFailure)
		outcome = outcomeDeclined
		ackReason = ackNoTaskUntracked
	}
	if retryable(outcome) && spentBudget(claim, outcome) {
		ceilingFired := claim.AttemptsTotal >= maxTransientAttempts
		_, decline, _ := inboundRetryPolicy(outcome)
		if ceilingFired {
			decline = msgTemporaryFailure
		}
		queued := a.replyQueued("", msg.senderID(), decline)
		fields := []any{
			"wamid", msg.ID, "type", msg.Type, "outcome", outcome,
			"failureKind", claim.FailureKind, "attempts", claim.Attempts,
			"attemptsTotal", claim.AttemptsTotal, "declineQueued", queued,
		}
		refused := !ceilingFired && (outcome == outcomeUnauthorized || outcome == outcomeHILUnauthorized)
		if refused {
			a.logger.Warn("whatsapp inbound message refused after its retries; declining", fields...)
		} else {
			a.logger.Error("whatsapp inbound message exhausted its retries; declining", fields...)
		}
		outcome = outcomeDeclined
		ackReason = ackNoTaskExhausted
		if refused {
			ackReason = ackNoTaskUnauthorized
		}
	}

	if retryable(outcome) {
		_, _, kind := inboundRetryPolicy(outcome)
		streak := claim.Attempts
		if claim.FailureKind != kind {
			streak = 0
		}
		a.releaseClaim(ctx, msg.ID, claim.Token, kind)
		a.logger.Warn("whatsapp inbound message failed retryably; awaiting redelivery",
			"wamid", msg.ID, "type", msg.Type, "outcome", outcome, "attempts", streak+1)
		a.pauseBeforeRetry(ctx, max(streak, claim.AttemptsTotal))
		return settleNack
	}
	if outcome != outcomeSubmitted {
		a.countAckedWithoutTask(cmp.Or(ackReason, env.ackReason, ackNoTaskDeclined))
	}
	a.completeClaim(ctx, msg.ID, claim.Token)
	return settleAck
}

func (a *Adapter) countInbound(msg *inboundMessage) {
	a.meter().Counter(metricWhatsAppInboundMessages, 1,
		"gatewayId", a.gatewayID(), "type", inboundTypeLabel(msg.Type))
}

func (a *Adapter) countAckedWithoutTask(reason string) {
	a.meter().Counter(metricWhatsAppAckedWithoutTask, 1, "gatewayId", a.gatewayID(), "reason", reason)
}

func (a *Adapter) processInboundSafely(ctx context.Context, env *ingressEnvelope, firstDelivery bool) (outcome inboundOutcome) {
	value, msg := &env.Context, &env.Message
	defer func() {
		if r := recover(); r != nil {
			a.logPanic("panic while processing an inbound whatsapp message", r, msg.ID)
			outcome = outcomeStalled
		}
	}()
	if hook := a.beforeProcess; hook != nil {
		hook(msg.ID)
	}
	sessionEpoch := a.recordInbound(ctx, value, msg)
	return a.processInboundMessage(env, sessionEpoch, firstDelivery)
}

func (a *Adapter) pauseBeforeRetry(ctx context.Context, attempts int) {
	pace := retryPace(a.retryDelay, attempts)
	if pace <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(pace):
	}
}

func (a *Adapter) claimInbound(ctx context.Context, wamid string) (store.WhatsAppClaim, bool) {
	if wamid == "" {
		a.logger.Warn("inbound message missing wamid, dedup skipped")
		return store.WhatsAppClaim{Outcome: store.ClaimAcquired}, true
	}
	deadline := time.Now().Add(a.claimWait)
	for {
		claimCtx, cancel := context.WithTimeout(ctx, claimStoreTimeout)
		claim, err := a.claims.Claim(claimCtx, a.core.GatewayID(), wamid, a.claimLease)
		cancel()
		if err != nil {
			a.logger.Error("failed to claim inbound message", "wamid", wamid, "error", err)
			return claim, false
		}
		switch claim.Outcome {
		case store.ClaimAcquired:
			return claim, true
		case store.ClaimDuplicate:
			a.logger.Info("duplicate inbound message ignored", "wamid", wamid)
			return claim, true
		case store.ClaimInFlight:
		}
		if time.Now().After(deadline) {
			a.logger.Warn("inbound message still claimed by another consumer; awaiting redelivery",
				"wamid", wamid, "waited", a.claimWait)
			return claim, false
		}
		select {
		case <-ctx.Done():
			return claim, false
		case <-time.After(min(a.claimPoll, a.claimWait)):
		}
	}
}

func (a *Adapter) completeClaim(ctx context.Context, wamid string, token int64) {
	if wamid == "" {
		return
	}
	ctx, cancel := settleCtx(ctx)
	defer cancel()
	owned, err := a.claims.MarkDone(ctx, a.core.GatewayID(), wamid, token)
	switch {
	case err != nil:
		a.logger.Error("failed to record inbound message as processed", "wamid", wamid, "error", err)
	case !owned:
		a.logger.Warn("inbound message claim was taken over before it could be completed",
			"wamid", wamid, "claimLease", a.claimLease)
	}
}

func (a *Adapter) releaseClaim(ctx context.Context, wamid string, token int64, kind string) {
	if wamid == "" {
		return
	}
	ctx, cancel := settleCtx(ctx)
	defer cancel()
	owned, err := a.claims.ReleaseForRetry(ctx, a.core.GatewayID(), wamid, token, kind)
	switch {
	case err != nil:
		a.logger.Error("failed to release inbound message claim", "wamid", wamid, "error", err)
	case !owned:
		a.logger.Warn("inbound message claim was taken over before it could be released", "wamid", wamid)
	}
}

func settleCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), claimStoreTimeout)
}

type memoryClaims struct {
	mu      sync.Mutex
	entries map[string]memoryClaim
}

type memoryClaim struct {
	state         string
	claimedAt     time.Time
	attempts      int
	failureKind   string
	attemptsTotal int
}

func newMemoryClaims() *memoryClaims {
	return &memoryClaims{entries: make(map[string]memoryClaim)}
}

func (m *memoryClaims) key(gatewayID, wamid string) string { return gatewayID + "\x00" + wamid }

func (m *memoryClaims) state(gatewayID, wamid string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries[m.key(gatewayID, wamid)].state
}

func (m *memoryClaims) evictExpiredLocked(now time.Time) {
	cutoff := now.Add(-entrypoint.WhatsAppClaimRetention)
	for k, entry := range m.entries {
		if entry.claimedAt.Before(cutoff) {
			delete(m.entries, k)
		}
	}
}

func (m *memoryClaims) Claim(_ context.Context, gatewayID, wamid string, lease time.Duration) (store.WhatsAppClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.evictExpiredLocked(now)

	k := m.key(gatewayID, wamid)
	entry, exists := m.entries[k]
	switch {
	case !exists:
	case entry.state == store.WhatsAppMessageDone:
		return store.WhatsAppClaim{Outcome: store.ClaimDuplicate}, nil
	case entry.state == store.WhatsAppMessageProcessing && now.Sub(entry.claimedAt) < lease:
		return store.WhatsAppClaim{Outcome: store.ClaimInFlight}, nil
	}
	claimedAt := now
	if exists && !claimedAt.After(entry.claimedAt) {
		claimedAt = entry.claimedAt.Add(time.Nanosecond)
	}
	attempts, kind, total := entry.attempts, entry.failureKind, entry.attemptsTotal
	if exists && entry.state == store.WhatsAppMessageProcessing {
		total++
		if kind == store.WhatsAppStalledFailure {
			attempts++
		} else {
			attempts, kind = 1, store.WhatsAppStalledFailure
		}
	}
	m.entries[k] = memoryClaim{
		state: store.WhatsAppMessageProcessing, claimedAt: claimedAt,
		attempts: attempts, failureKind: kind, attemptsTotal: total,
	}
	return store.WhatsAppClaim{
		Outcome:       store.ClaimAcquired,
		Token:         claimedAt.UnixNano(),
		Attempts:      attempts,
		FailureKind:   kind,
		AttemptsTotal: total,
	}, nil
}

func (m *memoryClaims) MarkDone(_ context.Context, gatewayID, wamid string, token int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(gatewayID, wamid)
	entry, ok := m.entries[k]
	if !ok || entry.state != store.WhatsAppMessageProcessing || entry.claimedAt.UnixNano() != token {
		return false, nil
	}
	entry.state = store.WhatsAppMessageDone
	m.entries[k] = entry
	return true, nil
}

func (m *memoryClaims) ReleaseForRetry(_ context.Context, gatewayID, wamid string, token int64, kind string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(gatewayID, wamid)
	entry, ok := m.entries[k]
	if !ok || entry.state != store.WhatsAppMessageProcessing || entry.claimedAt.UnixNano() != token {
		return false, nil
	}
	entry.state = store.WhatsAppMessageRetrying
	entry.attemptsTotal++
	if entry.failureKind == kind {
		entry.attempts++
	} else {
		entry.attempts, entry.failureKind = 1, kind
	}
	m.entries[k] = entry
	return true, nil
}

var _ messageClaims = (*memoryClaims)(nil)
var _ messageClaims = (*store.WhatsAppProcessedRepo)(nil)
