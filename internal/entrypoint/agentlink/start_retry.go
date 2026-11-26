// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"time"
)

const (
	startRetryFirst = 5 * time.Second
	startRetryMax   = 15 * time.Minute
	startRetryTick  = time.Second

	agentCouldNotStart       = "agent link could not start an agent; its token is answered 503 and the start is tried again"
	agentStartedAfterFailing = "agent link started an agent whose earlier starts had failed"
)

func startRetryDelay(failures int) time.Duration {
	if failures >= 20 {
		return startRetryMax
	}
	return min(startRetryFirst<<max(failures-1, 0), startRetryMax)
}

func (a *Adapter) retryFailedStarts(ctx context.Context) {
	t := time.NewTicker(startRetryTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.startAgentsDue(a.now())
		}
	}
}

func (a *Adapter) startAgentsDue(now time.Time) {
	if len(a.roster.startsDue(now)) == 0 || !a.roster.tryBeginChange() {
		return
	}
	defer a.roster.endChange()
	for _, due := range a.roster.startsDue(now) {
		if a.ctx.Err() != nil {
			return
		}
		if err := a.bringUp(a.ctx, newAgentState(a, due.cfg)); err != nil {
			a.warnOfFailedStart(due.cfg.Name, err)
			continue
		}
		a.logger.Info(agentStartedAfterFailing, "agentName", due.cfg.Name, "failures", due.failures)
	}
}

func (a *Adapter) warnOfFailedStart(name string, err error) {
	if a.ctx.Err() != nil {
		return
	}
	next, failed := a.roster.nextStartAttempt(name)
	if !failed {
		return
	}
	a.logger.Warn(agentCouldNotStart, "agentName", name, "retryIn", next.Sub(a.now()).Round(time.Second), "error", err)
}
