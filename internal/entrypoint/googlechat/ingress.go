// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	goruntime "runtime"
	"runtime/debug"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

type inboundOutcome int

const (
	outcomeSubmitted inboundOutcome = iota
	outcomeDeclined
	outcomeIgnored
	outcomeUnauthorized
	outcomeAuthzUnavailable
	outcomeTransient
	outcomeStalled
)

func (o inboundOutcome) String() string {
	switch o {
	case outcomeSubmitted:
		return "submitted"
	case outcomeDeclined:
		return "declined"
	case outcomeIgnored:
		return "ignored"
	case outcomeUnauthorized:
		return "unauthorized"
	case outcomeAuthzUnavailable:
		return "authz_unavailable"
	case outcomeTransient:
		return "transient"
	case outcomeStalled:
		return "stalled"
	}
	return "unknown"
}

func (o inboundOutcome) LogValue() slog.Value { return slog.StringValue(o.String()) }

func (o inboundOutcome) retryable() bool {
	switch o {
	case outcomeUnauthorized, outcomeAuthzUnavailable, outcomeTransient, outcomeStalled:
		return true
	}
	return false
}

func retryPolicy(o inboundOutcome) (budget int, kind string) {
	switch o {
	case outcomeUnauthorized:
		return maxAuthzDenialAttempts, "unauthorized"
	case outcomeAuthzUnavailable:
		return maxTransientAttempts, "authz_unavailable"
	case outcomeStalled:
		return stalledRetryBudget, store.StalledClaimFailure
	}
	return maxTransientAttempts, "transient"
}

func budgetSpent(claim store.DedupClaim, o inboundOutcome, click bool) bool {
	return claim.AttemptsTotal+1 >= attemptCeiling(click) || streakSpent(claim, o)
}

func attemptCeiling(click bool) int {
	if click {
		return maxClickAttempts
	}
	return maxTransientAttempts
}

func consumersKilled(claim store.DedupClaim, click bool) bool {
	if claim.FailureKind != store.StalledClaimFailure {
		return false
	}
	return stallStreakSpent(claim) || !click && claim.AttemptsTotal >= maxTransientAttempts
}

func stallStreakSpent(claim store.DedupClaim) bool {
	return claim.FailureKind == store.StalledClaimFailure && claim.Attempts >= stalledRetryBudget
}

func streakSpent(claim store.DedupClaim, o inboundOutcome) bool {
	budget, kind := retryPolicy(o)
	streak := 1
	if claim.FailureKind == kind {
		streak = claim.Attempts + 1
	}
	return streak >= budget
}

const ackedRetriesExhausted = "retries_exhausted"

func exhaustedReason(claim store.DedupClaim, o inboundOutcome) string {
	if (o == outcomeUnauthorized || o == outcomeStalled) && streakSpent(claim, o) {
		return o.String()
	}
	return ackedRetriesExhausted
}

func retryPace(attempts int) time.Duration {
	return min(defaultTransientRetryDelay<<min(attempts, 8), maxTransientRetryDelay, maxNackDelay)
}

type eventClaims interface {
	Claim(ctx context.Context, gatewayID, eventKey string, lease time.Duration) (store.DedupClaim, error)
	MarkDone(ctx context.Context, gatewayID, eventKey string, token int64) (bool, error)
	ReleaseForRetry(ctx context.Context, gatewayID, eventKey string, token int64, kind string) (bool, error)
}

type processResult struct {
	outcome inboundOutcome
	decline string
}

type settlement struct {
	ack   bool
	delay time.Duration
}

var settleAck = settlement{ack: true}

func settleNack(delay time.Duration) settlement { return settlement{delay: delay} }

type ingressJob struct {
	msg pulledMessage
	ev  inboundEvent
	err error
}

func resolveIngressWorkers() int {
	return max(min(goruntime.GOMAXPROCS(0), maxIngressWorkers), minIngressWorkers)
}

func (a *Adapter) runIngress(ctx context.Context) {
	workers := make([]chan ingressJob, resolveIngressWorkers())
	for i := range workers {
		workers[i] = make(chan ingressJob, ingressWorkerQueueCap)
		ch := workers[i]
		a.ingressWG.Go(func() { a.ingressWorker(ctx, ch) })
	}
	defer func() {
		for _, ch := range workers {
			close(ch)
		}
	}()

	var failingSince time.Time
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		msgs, err := a.sub.Pull(ctx, pullMaxMessages)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if failingSince.IsZero() {
				failingSince = time.Now()
			}
			if time.Since(failingSince) >= ingressDownGrace {
				a.health.Set("ingress", health.Degraded, health.DetailIngressDown)
			}
			a.logger.Warn("google chat ingress pull failed", "error", err, "retryIn_ms", backoff.Milliseconds())
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, pullErrorBackoffMax)
			continue
		}
		if !failingSince.IsZero() {
			a.health.Clear("ingress")
			a.logger.Info("google chat ingress pull recovered")
			failingSince = time.Time{}
		}
		backoff = 100 * time.Millisecond
		if len(msgs) == 0 {
			continue
		}
		a.extendLeases(ctx, msgs)
		for _, m := range msgs {
			ev, parseErr := parseEvent(m.Data)
			job := ingressJob{msg: m, ev: ev, err: parseErr}
			select {
			case workers[workerFor(ev.SenderName, len(workers))] <- job:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (a *Adapter) extendLeases(ctx context.Context, msgs []pulledMessage) {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.AckID
	}
	if err := a.sub.Extend(ctx, ids, ackDeadline); err != nil {
		a.logger.Warn("google chat could not extend ingress ack deadlines; messages may be redelivered while processing", "count", len(ids), "error", err)
	}
}

func workerFor(sender string, n int) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sender))
	return int(h.Sum64() % uint64(n))
}

func (a *Adapter) ingressWorker(ctx context.Context, jobs <-chan ingressJob) {
	for job := range jobs {
		if ctx.Err() != nil {
			a.releaseUnprocessed(job.msg)
			continue
		}
		a.settle(ctx, job.msg, a.deliver(ctx, job))
	}
}

func (a *Adapter) releaseUnprocessed(msg pulledMessage) {
	releaseCtx, cancel := context.WithTimeout(context.Background(), claimStoreTimeout)
	defer cancel()
	if err := a.sub.Nack(releaseCtx, []string{msg.AckID}, 0); err != nil {
		a.logger.Debug("google chat could not release an unprocessed message on shutdown", "error", err)
	}
}

func (a *Adapter) settle(ctx context.Context, msg pulledMessage, s settlement) {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimStoreTimeout)
	defer cancel()
	if s.ack {
		if err := a.sub.Ack(settleCtx, []string{msg.AckID}); err != nil {
			a.logger.Warn("google chat ingress ack failed; the message will be redelivered", "ingressMessageID", msg.MessageID, "error", err)
		}
		return
	}
	if err := a.sub.Nack(settleCtx, []string{msg.AckID}, s.delay); err != nil {
		a.logger.Warn("google chat ingress nack failed; the message is redelivered when its deadline lapses", "ingressMessageID", msg.MessageID, "error", err)
	}
}

func (a *Adapter) deliver(ctx context.Context, job ingressJob) settlement {
	gw := a.gatewayID
	if job.err != nil {
		a.logger.Warn("google chat event could not be decoded; dropping it", "ingressMessageID", job.msg.MessageID, "payloadLen", len(job.msg.Data), "error", job.err)
		a.meter().Counter(metricAckedWithoutTask, 1, "gatewayId", gw, "reason", "undecodable")
		return settleAck
	}
	ev := &job.ev
	if ev.Kind == kindIgnored || !ev.Addressed {
		reason := ev.IgnoreReason
		if reason == "" {
			reason = "not_addressed"
		}
		a.logger.Debug("google chat event ignored", "reason", reason, "ingressMessageID", job.msg.MessageID)
		return settleAck
	}
	a.meter().Counter(metricInboundEvents, 1, "gatewayId", gw, "isDm", fmt.Sprint(ev.IsDM))

	claimCtx, cancel := context.WithTimeout(ctx, claimStoreTimeout)
	claim, err := a.claims.Claim(claimCtx, gw, ev.EventKey, claimLease)
	cancel()
	if err != nil {
		a.logger.Warn("google chat event claim failed; redelivering", "eventKey", ev.EventKey, "error", err)
		return settleNack(retryPace(0))
	}
	switch claim.Outcome {
	case store.ClaimDuplicate:
		a.logger.Debug("google chat duplicate event dropped", "eventKey", ev.EventKey)
		return settleAck
	case store.ClaimInFlight:
		return settleNack(retryPace(1))
	}
	if consumersKilled(claim, ev.Kind == kindCardClick) {
		return a.declineUnprocessed(ctx, ev, claim)
	}

	res := a.processSafely(ctx, ev)
	if !res.outcome.retryable() {
		if res.outcome != outcomeSubmitted {
			a.meter().Counter(metricAckedWithoutTask, 1, "gatewayId", gw, "reason", res.outcome.String())
		}
		a.completeClaim(ctx, ev.EventKey, claim)
		return settleAck
	}

	_, kind := retryPolicy(res.outcome)
	if ctx.Err() != nil {
		released := a.releaseClaim(ctx, ev.EventKey, claim, kind)
		a.logger.Debug("google chat event left for a successor: the adapter is stopping", "eventKey", ev.EventKey, "outcome", res.outcome, "released", released)
		return settleNack(0)
	}
	if budgetSpent(claim, res.outcome, ev.Kind == kindCardClick) {
		if ev.Kind == kindCardClick {
			if res.outcome == outcomeStalled && streakSpent(claim, res.outcome) {
				a.declineCrashingClick(ctx, ev, claim, claim.Attempts+1)
				return settleAck
			}
			a.giveUpClick(ctx, ev, claim, res)
			return settleAck
		}
		reason := exhaustedReason(claim, res.outcome)
		a.meter().Counter(metricAckedWithoutTask, 1, "gatewayId", gw, "reason", reason)
		a.postNotice(ev.SpaceName, ev.ThreadName, cmp.Or(res.decline, msgTemporaryFailure))
		level := slog.LevelError
		if reason == outcomeUnauthorized.String() {
			level = slog.LevelWarn
		}
		a.logger.Log(ctx, level, "google chat event retries exhausted; telling the sender", "eventKey", ev.EventKey, "outcome", res.outcome, "reason", reason, "attemptsTotal", claim.AttemptsTotal+1)
		a.completeClaim(ctx, ev.EventKey, claim)
		return settleAck
	}
	a.releaseClaim(ctx, ev.EventKey, claim, kind)
	a.logger.Info("google chat event held for retry", "eventKey", ev.EventKey, "outcome", res.outcome, "attempt", claim.AttemptsTotal+1)
	return settleNack(retryPace(claim.AttemptsTotal + 1))
}

func (a *Adapter) declineUnprocessed(ctx context.Context, ev *inboundEvent, claim store.DedupClaim) settlement {
	if ev.Kind == kindCardClick {
		a.declineCrashingClick(ctx, ev, claim, claim.Attempts)
		return settleAck
	}
	reason := ackedRetriesExhausted
	if stallStreakSpent(claim) {
		reason = outcomeStalled.String()
	}
	a.meter().Counter(metricAckedWithoutTask, 1, "gatewayId", a.gatewayID, "reason", reason)
	a.postNotice(ev.SpaceName, ev.ThreadName, msgTemporaryFailure)
	fields := []any{"eventKey", ev.EventKey, "reason", reason, "attempts", claim.Attempts, "attemptsTotal", claim.AttemptsTotal}
	if reason == ackedRetriesExhausted {
		a.logger.Error("google chat event retries exhausted; declining it unprocessed", fields...)
	} else {
		a.logger.Error("google chat event keeps killing its consumer; declining it unprocessed", fields...)
	}
	a.completeClaim(ctx, ev.EventKey, claim)
	return settleAck
}

func (a *Adapter) declineCrashingClick(ctx context.Context, ev *inboundEvent, claim store.DedupClaim, attempts int) {
	a.meter().Counter(metricAckedWithoutTask, 1, "gatewayId", a.gatewayID, "reason", outcomeStalled.String())
	a.logger.Error("google chat card click keeps killing its consumer; declining it for good", "eventKey", ev.EventKey,
		"action", ev.Action[actionMethodParam], "taskID", ev.Action[paramTaskID], "attempts", attempts, "attemptsTotal", claim.AttemptsTotal)
	a.postNotice(ev.SpaceName, ev.ThreadName, msgClickDeclined)
	a.completeClaim(ctx, ev.EventKey, claim)
}

func (a *Adapter) processSafely(ctx context.Context, ev *inboundEvent) (res processResult) {
	defer func() {
		if r := recover(); r != nil {
			a.logger.Error("google chat event processing panicked", "eventKey", ev.EventKey, "panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
			res = processResult{outcome: outcomeStalled}
		}
	}()
	return a.process(ctx, ev)
}

func (a *Adapter) completeClaim(ctx context.Context, key string, claim store.DedupClaim) {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimStoreTimeout)
	defer cancel()
	if owned, err := a.claims.MarkDone(settleCtx, a.gatewayID, key, claim.Token); err != nil {
		a.logger.Warn("google chat claim could not be marked done; a redelivery may be processed again", "eventKey", key, "error", err)
	} else if !owned {
		a.logger.Warn("google chat claim was taken over before it was marked done", "eventKey", key)
	}
}

func (a *Adapter) releaseClaim(ctx context.Context, key string, claim store.DedupClaim, kind string) bool {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimStoreTimeout)
	defer cancel()
	released, err := a.claims.ReleaseForRetry(settleCtx, a.gatewayID, key, claim.Token, kind)
	if err != nil {
		a.logger.Warn("google chat claim could not be released for retry; the lease will lapse instead", "eventKey", key, "error", err)
		return false
	}
	return released
}

func (a *Adapter) giveUpClick(ctx context.Context, ev *inboundEvent, claim store.DedupClaim, res processResult) {
	_, kind := retryPolicy(res.outcome)
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimStoreTimeout)
	defer cancel()
	action, taskID := ev.Action[actionMethodParam], ev.Action[paramTaskID]
	released, err := a.claims.ReleaseForRetry(settleCtx, a.gatewayID, ev.EventKey, claim.Token, kind)
	if err == nil && !released {
		a.logger.Debug("google chat card click give-up skipped: another replica took the click over", "eventKey", ev.EventKey, "action", action, "taskID", taskID)
		return
	}
	reason := exhaustedReason(claim, res.outcome)
	a.meter().Counter(metricAckedWithoutTask, 1, "gatewayId", a.gatewayID, "reason", reason)
	switch {
	case res.outcome == outcomeUnauthorized:
		level := slog.LevelError
		if reason == outcomeUnauthorized.String() {
			level = slog.LevelWarn
		}
		a.logger.Log(ctx, level, "google chat card click refused after retries", "eventKey", ev.EventKey, "action", action, "taskID", taskID, "reason", reason, "attemptsTotal", claim.AttemptsTotal+1, "reopened", err == nil)
		a.postNotice(ev.SpaceName, ev.ThreadName, cmp.Or(res.decline, msgClickFailed))
		return
	case err != nil:
		a.logger.Error("google chat card click given up after retries, and the click could not be reopened", "eventKey", ev.EventKey, "action", action, "taskID", taskID, "outcome", res.outcome, "attemptsTotal", claim.AttemptsTotal+1, "error", err)
		a.postNotice(ev.SpaceName, ev.ThreadName, msgClickFailedLater)
		return
	}
	a.logger.Error("google chat card click given up after retries; the user is asked to try again", "eventKey", ev.EventKey, "action", action, "taskID", taskID, "outcome", res.outcome, "attemptsTotal", claim.AttemptsTotal+1)
	a.postNotice(ev.SpaceName, ev.ThreadName, cmp.Or(res.decline, msgClickFailed))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
