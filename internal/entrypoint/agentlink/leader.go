// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const (
	followerRetryAfterSeconds = "1"
	msgNotLeading             = "another replica serves agent link connections"
	closedForHandover         = "another replica serves this agent now"
)

type leaderTerm struct {
	number uint64
	ctx    context.Context
	cancel context.CancelFunc
	ended  chan struct{}
	work   workGate
}

func (t *leaderTerm) run(fn func(context.Context)) bool {
	if !t.work.enter() {
		return false
	}
	go func() {
		defer t.work.leave()
		fn(t.ctx)
	}()
	return true
}

func (t *leaderTerm) finish() { t.work.closeAndWait() }

func (t *leaderTerm) hasEnded() bool {
	select {
	case <-t.ended:
		return true
	default:
		return false
	}
}

var _ entrypoint.LeaderIngressAdapter = (*Adapter)(nil)

func (a *Adapter) RunIngress(ctx context.Context, term uint64) {
	a.termMu.Lock()
	defer a.termMu.Unlock()
	if a.configErr != nil || !a.started.Load() || !a.leadsOnly() || a.stopping || a.ctx.Err() != nil || ctx.Err() != nil {
		return
	}
	previous := a.term
	if previous != nil && previous.number == term && !previous.hasEnded() {
		return
	}
	termCtx, cancel := context.WithCancel(ctx)
	stopWithAdapter := context.AfterFunc(a.ctx, cancel)
	current := &leaderTerm{number: term, ctx: termCtx, cancel: cancel, ended: make(chan struct{})}
	a.term = current
	if previous != nil {
		previous.cancel()
	}
	a.goRun(func(context.Context) {
		defer close(current.ended)
		defer cancel()
		defer stopWithAdapter()
		if previous != nil {
			<-previous.ended
		}
		if termCtx.Err() != nil {
			return
		}
		a.lead(current)
		<-termCtx.Done()
		a.standDown(current)
	})
}

func (a *Adapter) leadsOnly() bool { return a.core.IngressOnLeader() }

func (a *Adapter) serves() bool {
	return !a.leadsOnly() || a.leading.Load() != nil
}

func (s *agentState) goServing(fn func(context.Context)) bool {
	if !s.a.leadsOnly() {
		return s.goRun(fn)
	}
	term := s.a.leading.Load()
	return term != nil && s.runInTerm(term, fn)
}

func (s *agentState) runInTerm(term *leaderTerm, fn func(context.Context)) bool {
	if !s.work.enter() {
		return false
	}
	started := term.run(func(leading context.Context) {
		defer s.work.leave()
		ctx, cancel := context.WithCancel(leading)
		defer cancel()
		defer context.AfterFunc(s.ctx, cancel)()
		fn(ctx)
	})
	if !started {
		s.work.leave()
	}
	return started
}

func (a *Adapter) lead(term *leaderTerm) {
	a.roster.beginChange()
	defer a.roster.endChange()
	a.leading.Store(term)
	agents := a.roster.all()
	a.logger.Info("agent link leading: this replica serves agent connections", "term", term.number, "agents", len(agents))
	for _, s := range agents {
		s.lead(term)
	}
}

func (a *Adapter) standDown(term *leaderTerm) {
	a.leading.CompareAndSwap(term, nil)
	settled := a.standAgentsDown()
	term.finish()
	a.logger.Info(stoodDown, "term", term.number, "failed", settled.failed, "forgotten", settled.forgotten,
		"unanswered", settled.unanswered, "resultsDropped", settled.resultsDropped, "usageEventsDropped", settled.usageEventsDropped)
	if settled.lostWork() {
		a.logger.Warn(standDownLostWork, "term", term.number, "requestsLost", settled.requestsLost, "unanswered", settled.unanswered,
			"usageEventsDropped", settled.usageEventsDropped, "gives_up_after_ms", answerGiveUpAfter.Milliseconds())
	}
}

func (a *Adapter) contextOutlivingStop() context.Context {
	return context.WithoutCancel(a.ctx)
}

func (a *Adapter) endTermBeforeStopping(ctx context.Context) {
	a.termMu.Lock()
	a.stopping = true
	term := a.term
	a.termMu.Unlock()
	if term == nil {
		return
	}
	term.cancel()
	select {
	case <-term.ended:
	case <-ctx.Done():
	}
}

func (a *Adapter) standAgentsDown() standDownTally {
	a.roster.beginChange()
	defer a.roster.endChange()
	settle, cancel := context.WithTimeout(a.contextOutlivingStop(), a.settleWait)
	defer cancel()
	leaving := a.roster.all()
	tallies := make(chan standDownTally, len(leaving))
	var agents sync.WaitGroup
	for _, s := range leaving {
		agents.Go(func() { tallies <- s.standDown(settle, theTermEnded) })
	}
	agents.Wait()
	close(tallies)
	var settled standDownTally
	for tally := range tallies {
		settled = settled.plus(tally)
	}
	return settled
}

func (s *agentState) lead(term *leaderTerm) {
	if s.keepBound || s.isStopped() {
		s.ensureDispatch()
	}
	if s.wakeable() {
		s.runInTerm(term, s.watchWakes)
	}
	s.requestCardSync()
}

func (s *agentState) watchWakes(ctx context.Context) {
	s.runWake(ctx)
	if !s.keepBound {
		s.releaseQueue(s.wakeQueueName())
	}
}

func (s *agentState) releaseQueue(name string) {
	if err := s.brk().DeleteQueue(name); err != nil {
		s.a.logger.Debug("agent link could not release a queue", "agentName", s.cfg.Name, "queue", name, "error", err)
	}
}

func (s *agentState) stopDispatching() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dispatching {
		s.stopDispatch()
		s.dispatching = false
	}
	return s.dispatchDone
}

func (s *agentState) forgetDescription() {
	s.cardMu.Lock()
	defer s.cardMu.Unlock()
	s.cardUp, s.lastCardPublish = false, time.Time{}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.described, s.publishedBody, s.withdrawn = nil, nil, false
}

func (s *agentState) releaseRequestQueue() {
	if s.keepBound && s.a.ctx.Err() == nil {
		return
	}
	s.releaseQueue(s.queueName())
}

func (s *agentState) requestCardSync() {
	select {
	case s.cardSyncDue <- struct{}{}:
	default:
	}
}
