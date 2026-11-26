// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

var (
	midOctober                  = time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	firstOfNovember             = time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	octoberStart, novemberStart = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
)

func periodCheckEntry(completed time.Time, output int64) store.SpendLedgerEntry {
	return store.SpendLedgerEntry{
		ID:            idgen.New(),
		CompletedTime: completed.UnixMilli(),
		UserID:        new(idgen.New()),
		Surface:       "agent",
		ModelName:     new("m"),
		OutputTokens:  1,
		Cost:          &spend.Cost{Output: output},
	}
}

type periodCheckFixture struct {
	t    *testing.T
	db   *store.DB
	repo *store.SpendLedgerRepo
	logs logCapture
}

func newPeriodCheckFixture(t *testing.T) *periodCheckFixture {
	t.Helper()
	db := store.TestDB(t)
	return &periodCheckFixture{t: t, db: db, repo: store.NewSpendLedgerRepo(db)}
}

func (f *periodCheckFixture) file(entries ...store.SpendLedgerEntry) {
	f.t.Helper()
	if _, err := f.repo.Insert(f.t.Context(), entries); err != nil {
		f.t.Fatalf("insert: %v", err)
	}
}

func (f *periodCheckFixture) edit(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatalf("edit budget period rows: %v", err)
	}
}

func (f *periodCheckFixture) runAt(now time.Time) string {
	f.t.Helper()
	c := periodChecker{repo: f.repo, logger: f.logs.logger(), now: func() time.Time { return now }}
	if err := c.run(f.t.Context(), leader.Term{Number: 1}); err != nil {
		f.t.Fatalf("check: %v", err)
	}
	return f.logs.String()
}

func TestPeriodCheck_LogsAnErrorWhenTheCurrentPeriodsUserRowNoLongerMatches(t *testing.T) {
	f := newPeriodCheckFixture(t)
	entry := periodCheckEntry(midOctober, 5)
	f.file(entry, periodCheckEntry(midOctober, 6))
	f.edit(`UPDATE token_spend_budget_period SET total_cost = 50 WHERE subject_ref = ?`, *entry.UserID)

	out := f.runAt(midOctober)

	if strings.Count(out, periodDriftMsg) != 1 || !strings.Contains(out, "level=ERROR") {
		t.Fatalf("want one ERROR for the current period; logs:\n%s", out)
	}
	for _, want := range []string{"periodStart=2026-10-01", "periodClosed=false", "users=1", "meshRow=false", "columns=[total_cost]"} {
		if !strings.Contains(out, want) {
			t.Errorf("drift error lacks %q; logs:\n%s", want, out)
		}
	}
}

func TestPeriodCheck_ReportsTheMeshRowWithItsCosts(t *testing.T) {
	f := newPeriodCheckFixture(t)
	f.file(periodCheckEntry(midOctober, 5), periodCheckEntry(midOctober, 6))
	f.edit(`UPDATE token_spend_budget_period SET total_cost = 1 WHERE subject_kind = 'mesh'`)

	out := f.runAt(midOctober)

	for _, want := range []string{"users=0", "meshRow=true", "meshStoredTotalCost=1", "meshLedgerTotalCost=11"} {
		if !strings.Contains(out, want) {
			t.Errorf("drift error lacks %q; logs:\n%s", want, out)
		}
	}
}

func TestPeriodCheck_WarnsAboutTheJustClosedPeriodOnTheFirstDayOfTheNext(t *testing.T) {
	f := newPeriodCheckFixture(t)
	entry := periodCheckEntry(novemberStart.Add(-time.Hour), 5)
	f.file(entry, periodCheckEntry(firstOfNovember, 6))
	f.edit(`UPDATE token_spend_budget_period SET output_tokens = 9, unpriced_calls = 1 WHERE subject_ref = ?`, *entry.UserID)

	out := f.runAt(firstOfNovember)

	if strings.Count(out, periodDriftMsg) != 1 || !strings.Contains(out, "level=WARN") {
		t.Fatalf("want one WARN for the closed period; logs:\n%s", out)
	}
	for _, want := range []string{"periodStart=2026-10-01", "periodClosed=true", `columns="[output_tokens unpriced_calls]"`} {
		if !strings.Contains(out, want) {
			t.Errorf("drift warning lacks %q; logs:\n%s", want, out)
		}
	}
}

func TestPeriodCheck_ReportsEachPeriodItFoundConsistentWhenTheGovernorWroteBoth(t *testing.T) {
	f := newPeriodCheckFixture(t)
	f.file(periodCheckEntry(novemberStart.Add(-time.Hour), 5), periodCheckEntry(firstOfNovember, 6))

	out := f.runAt(firstOfNovember)

	if strings.Contains(out, periodDriftMsg) || strings.Count(out, periodAgreesMsg) != 2 || strings.Count(out, "level=INFO") != 2 {
		t.Fatalf("want one INFO per checked period and no drift; logs:\n%s", out)
	}
	for _, want := range []string{"periodStart=2026-11-01 periodClosed=false", "periodStart=2026-10-01 periodClosed=true"} {
		if !strings.Contains(out, want) {
			t.Errorf("consistent period lines lack %q; logs:\n%s", want, out)
		}
	}
}

type driftFunc func(ctx context.Context, periodStart int64, timeout time.Duration) ([]store.BudgetPeriodDrift, error)

func (f driftFunc) PeriodDrift(ctx context.Context, periodStart int64, timeout time.Duration) ([]store.BudgetPeriodDrift, error) {
	return f(ctx, periodStart, timeout)
}

func checkedPeriods(t *testing.T, now time.Time, failing time.Time) ([]int64, error) {
	t.Helper()
	var checked []int64
	c := periodChecker{logger: discardLogger, now: func() time.Time { return now }, repo: driftFunc(
		func(_ context.Context, periodStart int64, _ time.Duration) ([]store.BudgetPeriodDrift, error) {
			checked = append(checked, periodStart)
			if periodStart == failing.UnixMilli() {
				return nil, errors.New("snapshot failed")
			}
			return nil, nil
		})}
	return checked, c.run(t.Context(), leader.Term{Number: 1})
}

func TestPeriodCheck_ChecksTheClosedPeriodOnlyDuringTheFirstDayOfTheNext(t *testing.T) {
	for name, c := range map[string]struct {
		now  time.Time
		want []int64
	}{
		"mid-period":                 {midOctober, []int64{octoberStart.UnixMilli()}},
		"first day of the next":      {firstOfNovember, []int64{novemberStart.UnixMilli(), octoberStart.UnixMilli()}},
		"just past the first day":    {novemberStart.Add(periodCheckInterval), []int64{novemberStart.UnixMilli()}},
		"the first millisecond":      {novemberStart, []int64{novemberStart.UnixMilli(), octoberStart.UnixMilli()}},
		"the last millisecond prior": {novemberStart.Add(-time.Millisecond), []int64{octoberStart.UnixMilli()}},
	} {
		t.Run(name, func(t *testing.T) {
			checked, err := checkedPeriods(t, c.now, time.Time{})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if !slices.Equal(checked, c.want) {
				t.Errorf("checked periods = %v, want %v", checked, c.want)
			}
		})
	}
}

func TestPeriodCheck_AFailedPeriodFailsTheRunButTheOtherIsStillChecked(t *testing.T) {
	checked, err := checkedPeriods(t, firstOfNovember, novemberStart)
	if err == nil || !strings.Contains(err.Error(), "2026-11-01") {
		t.Errorf("run error = %v, want the current period's failure named", err)
	}
	if !slices.Equal(checked, []int64{novemberStart.UnixMilli(), octoberStart.UnixMilli()}) {
		t.Errorf("checked periods = %v, want November then October", checked)
	}
}
