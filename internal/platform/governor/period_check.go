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
	"maps"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	periodCheckInterval = 24 * time.Hour
	periodCheckTimeout  = 10 * time.Minute
)

const (
	periodDriftMsg  = "budget period spend disagrees with the ledger; budgets read the period rows"
	periodAgreesMsg = "budget period spend agrees with the ledger"
)

type PeriodDriftReader interface {
	PeriodDrift(ctx context.Context, periodStart int64, timeout time.Duration) ([]store.BudgetPeriodDrift, error)
}

type periodChecker struct {
	repo   PeriodDriftReader
	logger *slog.Logger
	now    func() time.Time
}

func PeriodCheckJob(repo PeriodDriftReader, logger *slog.Logger) leader.Job {
	c := periodChecker{repo: repo, logger: logger.With("logger_name", "governor-period-check"), now: time.Now}
	return leader.Job{
		Name:  leader.JobPlatformBudgetPeriodCheck,
		Every: periodCheckInterval,
		Run:   c.run,
	}
}

func (c periodChecker) run(ctx context.Context, _ leader.Term) error {
	now := c.now()
	current := budget.PeriodStart(now)
	errs := []error{c.check(ctx, current, false)}
	if now.Sub(current) < periodCheckInterval {
		errs = append(errs, c.check(ctx, budget.PeriodStart(current.Add(-time.Millisecond)), true))
	}
	return errors.Join(errs...)
}

func (c periodChecker) check(ctx context.Context, periodStart time.Time, closed bool) error {
	drift, err := c.repo.PeriodDrift(ctx, periodStart.UnixMilli(), periodCheckTimeout)
	if err != nil {
		return fmt.Errorf("check budget period spend for %s: %w", periodStart.Format(time.DateOnly), err)
	}
	if len(drift) > 0 {
		c.report(ctx, periodStart, closed, drift)
		return nil
	}
	c.logger.Info(periodAgreesMsg, "periodStart", periodStart.Format(time.DateOnly), "periodClosed", closed)
	return nil
}

func (c periodChecker) report(ctx context.Context, periodStart time.Time, closed bool, drift []store.BudgetPeriodDrift) {
	var users int
	var mesh *store.BudgetPeriodDrift
	differing := map[string]bool{}
	for i, d := range drift {
		if d.Subject == store.BudgetPeriodMeshSubject {
			mesh = &drift[i]
		} else {
			users++
		}
		for _, column := range d.Columns() {
			differing[column] = true
		}
	}
	attrs := []any{
		"periodStart", periodStart.Format(time.DateOnly),
		"periodClosed", closed,
		"users", users,
		"meshRow", mesh != nil,
		"columns", slices.Sorted(maps.Keys(differing)),
	}
	if mesh != nil {
		attrs = append(attrs, "meshStoredTotalCost", mesh.Stored.TotalCost, "meshLedgerTotalCost", mesh.Ledger.TotalCost)
	}
	level := slog.LevelError
	if closed {
		level = slog.LevelWarn
	}
	c.logger.Log(ctx, level, periodDriftMsg, attrs...)
}
