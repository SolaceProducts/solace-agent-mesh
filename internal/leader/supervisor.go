// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package leader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	failuresBeforeDegraded = 3
	slowHandoverAfter      = 30 * time.Second
	overdueRuns            = 3

	outcomeOK       = "ok"
	outcomeError    = "error"
	outcomePanic    = "panic"
	outcomeCanceled = "canceled"
)

type termRun struct {
	term   Term
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (e *Elector) supervise(ctx context.Context) {
	var current *termRun
	for {
		select {
		case <-ctx.Done():
			if current != nil {
				e.endTerm(current)
			}
			return
		case <-e.wake:
		}
		leading, number := e.desired()
		if current != nil && (!leading || current.term.Number != number) {
			e.endTerm(current)
			current = nil
		}
		if leading && current == nil && ctx.Err() == nil {
			current = e.startTerm(ctx, number)
		}
	}
}

func (e *Elector) desired() (leading bool, number uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active && !e.stopped, e.term
}

func (e *Elector) startTerm(parent context.Context, number uint64) *termRun {
	ctx, cancel := context.WithCancel(parent)
	run := &termRun{term: Term{Number: number, Holder: e.holder}, cancel: cancel}
	e.mu.Lock()
	runners := e.runners
	e.mu.Unlock()
	for _, r := range runners {
		run.wg.Go(func() { r.run(ctx, run.term) })
	}
	e.metrics.Counter(MetricTransitions, 1, labelRole, string(e.role), labelState, broker.BindActive.String())
	e.metrics.Gauge(MetricActive, 1, labelRole, string(e.role))
	e.logger.Info("leader term started", "role", e.role, "queue", e.queueName, "term", number, "holder", e.holder, "jobs", len(runners))
	return run
}

func (e *Elector) endTerm(run *termRun) {
	run.cancel()
	done := make(chan struct{})
	go func() {
		run.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(slowHandoverAfter):
		e.logger.Warn("leader jobs of the previous term are still running", "role", e.role, "term", run.term.Number, "jobs", e.runningJobs())
		<-done
	}
	e.mu.Lock()
	runners := e.runners
	e.mu.Unlock()
	for _, r := range runners {
		r.forgetFailures()
	}
	e.metrics.Counter(MetricTransitions, 1, labelRole, string(e.role), labelState, broker.BindStandby.String())
	e.metrics.Gauge(MetricActive, 0, labelRole, string(e.role))
	e.logger.Info("leader term ended", "role", e.role, "queue", e.queueName, "term", run.term.Number)
}

type runner struct {
	job     Job
	metrics runtime.Metrics
	logger  *slog.Logger

	lastStart time.Time
	failures  atomic.Int64
	started   atomic.Int64
}

func (r *runner) run(ctx context.Context, term Term) {
	if r.job.Every == 0 {
		r.runUntilSuccess(ctx, term)
		return
	}
	timer := time.NewTimer(r.untilNext())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		start := time.Now()
		if err := r.attempt(ctx, term); err == nil || ctx.Err() == nil {
			r.lastStart = start
		}
		if ctx.Err() != nil {
			return
		}
		timer.Reset(r.untilNext())
	}
}

func (r *runner) forgetFailures() {
	r.failures.Store(0)
	r.metrics.Gauge(MetricJobConsecutiveFailures, 0, labelJob, string(r.job.Name))
}

func (r *runner) untilNext() time.Duration {
	if r.lastStart.IsZero() {
		return 0
	}
	return max(r.job.Every-time.Since(r.lastStart), 0)
}

func (r *runner) runUntilSuccess(ctx context.Context, term Term) {
	bo := backoff.Exponential{Initial: time.Second, Max: time.Minute, Jitter: 0.2}
	for ctx.Err() == nil {
		if r.attempt(ctx, term) == nil {
			return
		}
		if bo.Wait(ctx) != nil {
			return
		}
	}
}

func (r *runner) attempt(ctx context.Context, term Term) (err error) {
	start := time.Now()
	r.started.Store(start.UnixNano())
	defer r.started.Store(0)
	if limit := r.overdueAfter(); limit > 0 {
		overdue := time.AfterFunc(limit, func() {
			r.logger.Warn("leader job is overdue", "job", r.job.Name, "term", term.Number, "elapsed_ms", limit.Milliseconds())
		})
		defer overdue.Stop()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("leader job %s panicked: %v", r.job.Name, recovered)
			r.logger.Error("leader job panicked", "job", r.job.Name, "term", term.Number, "panic", recovered, "stack", string(debug.Stack()))
			r.finish(term, outcomePanic, start, nil)
		}
	}()
	err = r.job.Run(ctx, term)
	switch {
	case err == nil:
		r.finish(term, outcomeOK, start, nil)
	case ctx.Err() != nil:
		r.finish(term, outcomeCanceled, start, nil)
	default:
		r.finish(term, outcomeError, start, err)
	}
	return err
}

func (r *runner) overdueAfter() time.Duration {
	switch {
	case r.job.OverdueAfter < 0:
		return 0
	case r.job.OverdueAfter > 0:
		return r.job.OverdueAfter
	default:
		return overdueRuns * r.job.Every
	}
}

func (r *runner) overdue(now time.Time) (time.Duration, bool) {
	limit, startedNs := r.overdueAfter(), r.started.Load()
	if limit == 0 || startedNs == 0 {
		return 0, false
	}
	elapsed := now.Sub(time.Unix(0, startedNs))
	return elapsed, elapsed > limit
}

func (r *runner) finish(term Term, outcome string, start time.Time, err error) {
	r.metrics.Counter(MetricJobRuns, 1, labelJob, string(r.job.Name), labelOutcome, outcome)
	r.metrics.Histogram(MetricJobDuration, time.Since(start), labelJob, string(r.job.Name))
	switch outcome {
	case outcomeOK:
		if previous := r.failures.Swap(0); previous > 0 {
			r.logger.Info("leader job recovered", "job", r.job.Name, "term", term.Number, "failedRuns", previous)
		}
	case outcomeError, outcomePanic:
		failures := r.failures.Add(1)
		fields := []any{"job", r.job.Name, "term", term.Number, "consecutiveFailures", failures}
		if err != nil {
			fields = append(fields, errorFields(err)...)
			r.logger.Warn("leader job failed", fields...)
		}
		if failures == failuresBeforeDegraded {
			r.logger.Error("leader job keeps failing", fields...)
		}
	}
	r.metrics.Gauge(MetricJobConsecutiveFailures, float64(r.failures.Load()), labelJob, string(r.job.Name))
}

func errorFields(err error) []any {
	var fielder LogFielder
	if errors.As(err, &fielder) {
		return fielder.LogFields()
	}
	return []any{"error", err}
}
