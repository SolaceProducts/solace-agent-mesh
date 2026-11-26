// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	recentEvery       = time.Minute
	reconcileEvery    = time.Hour
	recentPassSpacing = 5
)

const dayMillis = store.SpendRollupDayMillis

type Rollups interface {
	LedgerCallsByDay(ctx context.Context, from, to int64) (map[int64]int64, error)
	RollupCallsByDay(ctx context.Context, from, to int64) (map[int64]int64, error)
	RefreshDay(ctx context.Context, day int64, removeStale bool) error
	EnsureGrainVersion(ctx context.Context) (bool, error)
	StoredGrainVersion(ctx context.Context) (int, error)
	MarkRefreshed(ctx context.Context, at int64) error
	MarkReconciled(ctx context.Context, at int64) error
}

type rollupJobs struct {
	rollups    Rollups
	logger     *slog.Logger
	now        func() time.Time
	nextRecent time.Time
}

var errRollupsFromNewerRelease = errors.New("the spend rollups were built by a newer release; this release leaves them to it")

func RollupJobs(rollups Rollups, logger *slog.Logger) []leader.Job {
	j := &rollupJobs{rollups: rollups, logger: logger.With("logger_name", "usage-rollup"), now: time.Now}
	return []leader.Job{
		{Name: leader.JobPlatformSpendRollup, Every: recentEvery, Run: j.refreshRecent},
		{Name: leader.JobPlatformSpendRollupReconcile, Every: reconcileEvery, Run: j.reconcile},
	}
}

func (j *rollupJobs) refreshRecent(ctx context.Context, _ leader.Term) error {
	start := j.now()
	if start.Before(j.nextRecent) {
		return nil
	}
	if err := j.passRecent(ctx, start); err != nil {
		return err
	}
	j.nextRecent = start.Add(recentPassSpacing * j.now().Sub(start))
	return nil
}

func (j *rollupJobs) passRecent(ctx context.Context, start time.Time) error {
	version, err := j.rollups.StoredGrainVersion(ctx)
	switch {
	case err != nil:
		return err
	case version > store.SpendRollupGrainVersion:
		return fmt.Errorf("%w (grain %d; this release builds %d)", errRollupsFromNewerRelease, version, store.SpendRollupGrainVersion)
	case version < store.SpendRollupGrainVersion:
		j.logger.DebugContext(ctx, "spend rollups are being rebuilt at a new grain; recent pass skipped", "grain", version)
		return nil
	}
	today := startOfDay(start.UnixMilli())
	stale, err := j.staleDays(ctx, today-dayMillis, today+dayMillis)
	if err != nil {
		return err
	}
	if err := j.refreshDays(ctx, stale, "refresh"); err != nil {
		return err
	}
	return j.rollups.MarkRefreshed(ctx, start.UnixMilli())
}

func (j *rollupJobs) reconcile(ctx context.Context, _ leader.Term) error {
	cleared, err := j.rollups.EnsureGrainVersion(ctx)
	if err != nil {
		return err
	}
	if cleared {
		j.logger.Info("spend rollups cleared for a new grain; rebuilding them from the ledger")
	}
	start := j.now()
	stale, err := j.staleDays(ctx, governor.RetentionFloor(start), startOfDay(start.UnixMilli())-dayMillis)
	if err != nil {
		return err
	}
	for i := range stale {
		stale[i].removeStale = true
	}
	slices.Reverse(stale)
	if errs := j.refreshDays(ctx, stale, "rebuild"); errs != nil {
		return errs
	}
	if len(stale) > 0 {
		j.logger.Debug("spend rollup days rebuilt", "days", len(stale))
	}
	return j.rollups.MarkReconciled(ctx, start.UnixMilli())
}

const reportedFailures = 3

func (j *rollupJobs) refreshDays(ctx context.Context, days []staleDay, verb string) error {
	var errs []error
	failed := 0
	joined := func() error {
		if more := failed - reportedFailures; more > 0 {
			errs = append(errs, fmt.Errorf("%s: %d more days failed", verb, more))
		}
		return errors.Join(errs...)
	}
	for _, d := range days {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			return joined()
		}
		err := j.rollups.RefreshDay(ctx, d.day, d.removeStale)
		if err == nil {
			continue
		}
		if failed++; failed <= reportedFailures {
			errs = append(errs, fmt.Errorf("%s %s: %w", verb, dayLabel(d.day), err))
		}
	}
	return joined()
}

type staleDay struct {
	day         int64
	removeStale bool
}

func (j *rollupJobs) staleDays(ctx context.Context, from, to int64) ([]staleDay, error) {
	ledger, err := j.rollups.LedgerCallsByDay(ctx, from, to)
	if err != nil {
		return nil, err
	}
	rolled, err := j.rollups.RollupCallsByDay(ctx, from, to)
	if err != nil {
		return nil, err
	}
	days := map[int64]bool{}
	for day := range maps.Keys(ledger) {
		days[day] = true
	}
	for day := range maps.Keys(rolled) {
		days[day] = true
	}
	var stale []staleDay
	for _, day := range slices.Sorted(maps.Keys(days)) {
		if rolled[day] != ledger[day] {
			stale = append(stale, staleDay{day: day, removeStale: rolled[day] > ledger[day]})
		}
	}
	return stale, nil
}

func startOfDay(ms int64) int64 {
	return ms - ms%dayMillis
}

func dayLabel(day int64) string {
	return time.UnixMilli(day).UTC().Format(time.DateOnly)
}
