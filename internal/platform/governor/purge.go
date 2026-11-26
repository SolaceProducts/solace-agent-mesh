// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const purgeInterval = time.Hour

const topUpExpiryGrace = time.Minute

type EndedBudgetPeriods interface {
	DeleteBudgetPeriodsEndedBy(ctx context.Context, before time.Time) (int64, error)
}

type RollupDays interface {
	DeleteDaysBefore(ctx context.Context, day int64) (int64, error)
}

type ExpiredTopUps interface {
	DeleteExpired(ctx context.Context, nowMs int64) (int64, error)
}

type PastAlertStates interface {
	DeleteAlertStatesBefore(ctx context.Context, periodStart int64) (int64, error)
}

type PurgeTargets struct {
	Ledger      Ledger
	Periods     EndedBudgetPeriods
	Rollups     RollupDays
	TopUps      ExpiredTopUps
	AlertStates PastAlertStates
}

type purger struct {
	targets PurgeTargets
	logger  *slog.Logger
	now     func() time.Time
}

func PurgeJob(targets PurgeTargets, logger *slog.Logger) leader.Job {
	p := purger{targets: targets, logger: logger.With("logger_name", "governor-purge"), now: time.Now}
	return leader.Job{
		Name:  leader.JobPlatformTokenGovernancePurge,
		Every: purgeInterval,
		Run:   p.run,
	}
}

type purgeStep struct {
	what       string
	deleteRows func() (int64, error)
}

func (p purger) run(ctx context.Context, _ leader.Term) error {
	now := p.now()
	cutoff := now.Add(-RetentionPeriod)
	steps := []purgeStep{
		{"spend ledger", func() (int64, error) { return p.targets.Ledger.DeleteOlderThan(ctx, cutoff, purgeBatchSize) }},
		{"budget periods", func() (int64, error) { return p.targets.Periods.DeleteBudgetPeriodsEndedBy(ctx, cutoff) }},
		{"spend rollups", func() (int64, error) { return p.targets.Rollups.DeleteDaysBefore(ctx, RetentionFloor(now)) }},
		{"budget top-ups", func() (int64, error) {
			return p.targets.TopUps.DeleteExpired(ctx, now.Add(-topUpExpiryGrace).UnixMilli())
		}},
		{"budget alert states", func() (int64, error) {
			return p.targets.AlertStates.DeleteAlertStatesBefore(ctx, budget.PeriodStart(now).UnixMilli())
		}},
	}
	var errs []error
	for _, step := range steps {
		if ctx.Err() != nil {
			break
		}
		if err := p.purge(ctx, step); err != nil {
			errs = append(errs, err)
		}
	}
	err := errors.Join(errs...)
	if ctx.Err() != nil {
		p.warnUnreported(ctx, errs)
	}
	return err
}

func (p purger) warnUnreported(ctx context.Context, errs []error) {
	for _, err := range errs {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			p.logger.WarnContext(ctx, "token governance purge failed before the leader term ended", "error", err)
		}
	}
}

func (p purger) purge(ctx context.Context, step purgeStep) error {
	deleted, err := step.deleteRows()
	if err != nil {
		return fmt.Errorf("purge %s (deleted %d before failing): %w", step.what, deleted, err)
	}
	if deleted > 0 {
		p.logger.DebugContext(ctx, "token governance data purged", "data", step.what, "deleted", deleted)
	}
	return nil
}

func RetentionFloor(now time.Time) int64 {
	return store.SpendRollupDayEnd(now.Add(-RetentionPeriod).UnixMilli())
}
