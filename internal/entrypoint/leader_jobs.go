// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

const (
	asyncIndexBuildEvery = 10 * time.Minute

	retentionTick         = time.Minute
	retentionOverdueAfter = 6 * time.Hour

	leaderIngressTick = 5 * time.Second

	schedulerOverdueAfter = 2 * time.Minute

	mcpOAuthExpiryEvery = time.Minute
)

var _ leader.JobSource = (*GatewayExecutor)(nil)

func (e *GatewayExecutor) LeaderJobs() []leader.Job {
	e.leaderIngress.Store(true)
	e.mu.RLock()
	for _, mg := range e.gws {
		mg.core.UseLeaderIngress()
	}
	e.mu.RUnlock()
	return []leader.Job{
		{Name: leader.JobGatewayDataRetention, Every: e.retentionEvery(), OverdueAfter: retentionOverdueAfter, Run: e.sweepRetention()},
		{Name: leader.JobGatewayTaskRecordCleanup, Every: taskRecordSweepInterval, Run: e.forEachRunning((*GatewayCore).cleanUpTaskRecords)},
		{Name: leader.JobGatewayAsyncIndexes, Every: asyncIndexBuildEvery, OverdueAfter: leader.NeverOverdue, Run: e.asyncIndexBuilder()},
		{Name: leader.JobGatewayLeaderIngress, Every: leaderIngressTick, Run: e.runLeaderIngress},
		{Name: leader.JobGatewayScheduler, Every: schedulerTick, OverdueAfter: schedulerOverdueAfter, Run: e.forEachRunning((*GatewayCore).runScheduler)},
		{Name: leader.JobGatewayMCPOAuthExpiry, Every: mcpOAuthExpiryEvery, Run: e.forEachRunning((*GatewayCore).deleteExpiredMCPOAuth)},
	}
}

func (e *GatewayExecutor) runLeaderIngress(ctx context.Context, term leader.Term) error {
	for _, g := range e.running() {
		if ingress, ok := g.adapter.(LeaderIngressAdapter); ok {
			ingress.RunIngress(ctx, term.Number)
		}
	}
	return nil
}

func (e *GatewayExecutor) asyncIndexBuilder() func(context.Context, leader.Term) error {
	var warnedTerm uint64
	build := e.forEachRunning((*GatewayCore).buildAsyncIndexes)
	return func(ctx context.Context, term leader.Term) error {
		if term.Number != warnedTerm {
			warnedTerm = term.Number
			for _, g := range e.running() {
				if g.db != nil {
					g.db.WarnGivenUpAsyncIndexes(ctx)
				}
			}
		}
		return build(ctx, term)
	}
}

func (e *GatewayExecutor) running() []*GatewayCore {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var cores []*GatewayCore
	for _, mg := range e.gws {
		if mg.getStatus() == StatusRunning {
			cores = append(cores, mg.core)
		}
	}
	return cores
}

func (e *GatewayExecutor) forEachRunning(run func(*GatewayCore, context.Context) error) func(context.Context, leader.Term) error {
	return func(ctx context.Context, _ leader.Term) error {
		var errs []error
		for _, g := range e.running() {
			if err := run(g, ctx); err != nil {
				errs = append(errs, fmt.Errorf("gateway %s: %w", g.GatewayID(), err))
			}
		}
		return errors.Join(errs...)
	}
}

func (e *GatewayExecutor) sweepRetention() func(context.Context, leader.Term) error {
	historySwept := map[*DataRetentionService]time.Time{}
	var entrypointsSwept time.Time
	return func(ctx context.Context, _ leader.Term) error {
		now := time.Now()
		var errs []error
		var services []*DataRetentionService
		for _, g := range e.running() {
			dr := g.dataRetention
			if dr == nil {
				continue
			}
			services = append(services, dr)
			if !retentionDue(historySwept[dr], dr.cleanupInterval(), now) {
				continue
			}
			if err := dr.cleanupTaskHistory(ctx, now); err != nil {
				errs = append(errs, fmt.Errorf("gateway %s: %w", g.GatewayID(), err))
			}
			if ctx.Err() == nil {
				historySwept[dr] = now
			}
		}
		if every, outboundRetention := entrypointRetention(services); len(services) > 0 && retentionDue(entrypointsSwept, every, now) {
			for _, dr := range services {
				if err := dr.cleanupEntrypointTables(ctx, now, outboundRetention); err != nil {
					errs = append(errs, fmt.Errorf("entrypoint tables: %w", err))
				}
			}
			if ctx.Err() == nil {
				entrypointsSwept = now
			}
		}
		return errors.Join(errs...)
	}
}

func (e *GatewayExecutor) retentionEvery() time.Duration {
	e.mu.RLock()
	defer e.mu.RUnlock()
	every := retentionTick
	for _, mg := range e.gws {
		if dr := mg.core.dataRetention; dr != nil {
			every = min(every, dr.cleanupInterval())
		}
	}
	return every
}

func retentionDue(last time.Time, interval time.Duration, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= interval
}

func entrypointRetention(services []*DataRetentionService) (every, outboundRetention time.Duration) {
	for i, s := range services {
		if i == 0 || s.cleanupInterval() < every {
			every = s.cleanupInterval()
		}
		outboundRetention = max(outboundRetention, s.whatsAppOutboundRetention())
	}
	return every, outboundRetention
}

func (g *GatewayCore) cleanUpTaskRecords(ctx context.Context) error {
	var errs []error
	if tasks, ok := g.tasks.(taskRecordSweeper); ok {
		errs = append(errs, g.reapUnownedTaskRecords(ctx, tasks.records()))
	}
	errs = append(errs, g.pruneInbound(ctx))
	return errors.Join(errs...)
}

func (g *GatewayCore) deleteExpiredMCPOAuth(ctx context.Context) error {
	if g.db == nil {
		return nil
	}
	return store.NewMCPOAuthRepo(g.db).DeleteExpired(ctx, time.Now().UnixMilli())
}

func (g *GatewayCore) runScheduler(ctx context.Context) error {
	if g.scheduledTasks == nil {
		return nil
	}
	return g.scheduledTasks.RunPasses(ctx)
}

func (g *GatewayCore) buildAsyncIndexes(ctx context.Context) error {
	if g.db == nil {
		return nil
	}
	if err := g.db.BuildAsyncIndexes(ctx); err != nil && !errors.Is(err, dbutil.ErrAsyncIndexesBusy) {
		return fmt.Errorf("build queued indexes: %w", err)
	}
	return nil
}
