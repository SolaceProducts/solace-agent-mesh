// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

var today = time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)

func day(offset int) int64 {
	return today.AddDate(0, 0, offset).UnixMilli()
}

type recordingRollups struct {
	Rollups
	refreshed []int64
	rebuilt   []int64
	attempts  int
	failDay   int64
	failAll   bool
	onRefresh func()
}

func (r *recordingRollups) RefreshDay(ctx context.Context, d int64, removeStale bool) error {
	r.attempts++
	if r.onRefresh != nil {
		r.onRefresh()
	}
	if d == r.failDay || r.failAll {
		return errors.New("refresh failed")
	}
	if removeStale {
		r.rebuilt = append(r.rebuilt, d)
	} else {
		r.refreshed = append(r.refreshed, d)
	}
	return r.Rollups.RefreshDay(ctx, d, removeStale)
}

type rollupHarness struct {
	t       *testing.T
	db      *store.DB
	rollups *recordingRollups
	jobs    *rollupJobs
	now     time.Time
}

func newRollupHarness(t *testing.T) *rollupHarness {
	db := store.TestDB(t)
	h := &rollupHarness{t: t, db: db, rollups: &recordingRollups{Rollups: store.NewSpendRollupRepo(db)}, now: today.Add(10 * time.Hour)}
	h.jobs = &rollupJobs{rollups: h.rollups, logger: slog.Default(), now: func() time.Time { return h.now }}
	return h
}

func (h *rollupHarness) file(at int64) {
	h.t.Helper()
	_, err := store.NewSpendLedgerRepo(h.db).Insert(h.t.Context(), []store.SpendLedgerEntry{{
		ID: idgen.New(), CompletedTime: at, UserSub: new("user-1"), Surface: "agent",
		ModelName: new("m"), TraceID: new("t1"), UncachedInputTokens: 1,
		Cost: &spend.Cost{UncachedInput: 10},
	}})
	if err != nil {
		h.t.Fatalf("file a call: %v", err)
	}
}

func (h *rollupHarness) rolledCalls(d int64) int64 {
	h.t.Helper()
	n, err := dbutil.QueryVal[int64](h.t.Context(), h.db, `SELECT COALESCE(SUM(calls), 0) FROM token_spend_daily WHERE day = ?`, d)
	if err != nil {
		h.t.Fatalf("read rollup: %v", err)
	}
	return n
}

func (h *rollupHarness) runRecent() {
	h.t.Helper()
	if err := h.jobs.refreshRecent(h.t.Context(), leader.Term{Number: 1}); err != nil {
		h.t.Fatalf("recent pass: %v", err)
	}
}

func (h *rollupHarness) runReconcile() {
	h.t.Helper()
	if err := h.jobs.reconcile(h.t.Context(), leader.Term{Number: 1}); err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}
}

func (h *rollupHarness) resetRecords() {
	h.rollups.refreshed, h.rollups.rebuilt = nil, nil
}

func TestRecentPass_BringsYesterdayAndTodayUpToDate(t *testing.T) {
	h := newRollupHarness(t)
	h.file(day(0) + 1)
	h.file(day(-1) + 1)
	h.file(day(-2) + 1)

	h.runRecent()

	if h.rolledCalls(day(0)) != 1 || h.rolledCalls(day(-1)) != 1 {
		t.Errorf("rolled calls = today %d, yesterday %d; want 1 each", h.rolledCalls(day(0)), h.rolledCalls(day(-1)))
	}
	if h.rolledCalls(day(-2)) != 0 {
		t.Error("the recent pass wrote the day before yesterday, which is the reconcile's")
	}
	refreshed, err := dbutil.QueryVal[int64](t.Context(), h.db, `SELECT refreshed_time FROM token_spend_rollup_state WHERE id = 1`)
	if err != nil || refreshed != h.now.UnixMilli() {
		t.Errorf("refreshed_time = %d, %v; want the pass's time %d", refreshed, err, h.now.UnixMilli())
	}
}

func TestRecentPass_FilesACallForYesterdayThatArrivesAfterMidnight(t *testing.T) {
	h := newRollupHarness(t)
	h.now = today.Add(-time.Minute)
	h.file(day(-1) + 1)
	h.runRecent()

	h.now = today.Add(30 * time.Second)
	h.file(day(0) - 1)
	h.runRecent()

	if got := h.rolledCalls(day(-1)); got != 2 {
		t.Errorf("yesterday's rolled calls = %d, want 2", got)
	}
}

func TestRecentPass_SkipsDaysTheLedgerHasNotGrown(t *testing.T) {
	h := newRollupHarness(t)
	h.file(day(0) + 1)
	h.runRecent()
	h.resetRecords()

	h.runRecent()

	if len(h.rollups.refreshed) != 0 {
		t.Errorf("an unchanged ledger refreshed days %v, want none", h.rollups.refreshed)
	}
}

func TestReconcile_RepairsACallFiledLateForAnOlderDay(t *testing.T) {
	h := newRollupHarness(t)
	h.file(day(-10) + 1)
	h.runReconcile()
	h.file(day(-10) + 2)

	h.runReconcile()

	if got := h.rolledCalls(day(-10)); got != 2 {
		t.Errorf("rolled calls ten days back = %d, want 2", got)
	}
}

func TestReconcile_BackfillsAnEmptyRollupNewestFirst(t *testing.T) {
	h := newRollupHarness(t)
	for _, offset := range []int{-80, -30, -2, -1, 0} {
		h.file(day(offset) + 1)
	}

	h.runReconcile()

	if want := []int64{day(-2), day(-30), day(-80)}; !slices.Equal(h.rollups.rebuilt, want) {
		t.Errorf("rebuilt days = %v, want %v", h.rollups.rebuilt, want)
	}
	if h.rolledCalls(day(-1)) != 0 || h.rolledCalls(day(0)) != 0 {
		t.Error("the reconcile wrote a recent day")
	}
	h.resetRecords()
	h.runReconcile()
	if len(h.rollups.rebuilt) != 0 {
		t.Errorf("a second reconcile rebuilt %v, want nothing", h.rollups.rebuilt)
	}
}

func TestReconcile_RebuildsOnlyDaysWhollyInsideRetention(t *testing.T) {
	h := newRollupHarness(t)
	floor := governor.RetentionFloor(h.now)
	h.file(floor - dayMillis + 1)
	h.file(floor + 1)

	h.runReconcile()

	if want := []int64{floor}; !slices.Equal(h.rollups.rebuilt, want) {
		t.Errorf("rebuilt days = %v, want only the first wholly retained day %v", h.rollups.rebuilt, want)
	}
	if h.rolledCalls(floor-dayMillis) != 0 {
		t.Error("the day before the retention floor was rebuilt")
	}
}

func TestReconcile_RebuildsRowsBuiltAtAnotherGrain(t *testing.T) {
	h := newRollupHarness(t)
	h.file(day(-5) + 1)
	h.runReconcile()
	if _, err := h.db.ExecContext(t.Context(), `UPDATE token_spend_rollup_state SET grain_version = ?`, store.SpendRollupGrainVersion-1); err != nil {
		t.Fatalf("change the grain: %v", err)
	}

	h.runReconcile()

	if got := h.rolledCalls(day(-5)); got != 1 {
		t.Errorf("rolled calls after the grain change = %d, want the day rebuilt", got)
	}
	if want := []int64{day(-5), day(-5)}; !slices.Equal(h.rollups.rebuilt, want) {
		t.Errorf("rebuilt days = %v, want the day built, cleared and built again", h.rollups.rebuilt)
	}
}

func TestRollupJobs_AreTheRecentAndReconcileJobs(t *testing.T) {
	jobs := RollupJobs(store.NewSpendRollupRepo(store.TestDB(t)), slog.Default())
	if len(jobs) != 2 || jobs[0].Name != leader.JobPlatformSpendRollup || jobs[0].Every != time.Minute ||
		jobs[1].Name != leader.JobPlatformSpendRollupReconcile || jobs[1].Every != time.Hour {
		t.Errorf("jobs = %+v, want the minute recent job and the hourly reconcile", jobs)
	}
}

func (h *rollupHarness) state(column string) *int64 {
	h.t.Helper()
	v, err := dbutil.QueryVal[*int64](h.t.Context(), h.db, `SELECT `+column+` FROM token_spend_rollup_state WHERE id = 1`)
	if err != nil {
		h.t.Fatalf("read %s: %v", column, err)
	}
	return v
}

func TestReconcile_KeepsGoingPastADayThatFails(t *testing.T) {
	h := newRollupHarness(t)
	for _, offset := range []int{-40, -20, -5} {
		h.file(day(offset) + 1)
	}
	h.rollups.failDay = day(-20)

	if err := h.jobs.reconcile(t.Context(), leader.Term{Number: 1}); err == nil {
		t.Fatal("reconcile reported success with a day that failed")
	}

	if h.rolledCalls(day(-40)) != 1 || h.rolledCalls(day(-5)) != 1 {
		t.Error("the days around the failed one were not rebuilt")
	}
	if got := h.state("reconciled_time"); got != nil {
		t.Errorf("reconciled_time = %d after a failed day, want none", *got)
	}

	h.rollups.failDay = 0
	h.runReconcile()
	if got := h.state("reconciled_time"); got == nil || *got != h.now.UnixMilli() {
		t.Errorf("reconciled_time = %v once every day succeeded, want %d", got, h.now.UnixMilli())
	}
}

func TestReconcile_NamesTheFirstFailedDaysAndCountsTheRest(t *testing.T) {
	h := newRollupHarness(t)
	for offset := -10; offset <= -3; offset++ {
		h.file(day(offset) + 1)
	}
	h.rollups.failAll = true

	err := h.jobs.reconcile(t.Context(), leader.Term{Number: 1})

	if err == nil || strings.Count(err.Error(), "refresh failed") != reportedFailures || !strings.Contains(err.Error(), "5 more days failed") {
		t.Errorf("err = %v, want %d days named and 5 more counted", err, reportedFailures)
	}
}

func TestReconcile_StopsWhenCancelledAndClaimsNothing(t *testing.T) {
	h := newRollupHarness(t)
	for _, offset := range []int{-6, -5, -4} {
		h.file(day(offset) + 1)
	}
	ctx, cancel := context.WithCancel(t.Context())
	h.rollups.onRefresh = cancel

	err := h.jobs.reconcile(ctx, leader.Term{Number: 1})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation", err)
	}
	if len(h.rollups.rebuilt) > 1 {
		t.Errorf("rebuilt %d days after the cancellation, want at most the one under way", len(h.rollups.rebuilt))
	}
	if got := h.state("reconciled_time"); got != nil {
		t.Errorf("reconciled_time = %d after a cancelled run, want none", *got)
	}
}

func TestRecentPass_KeepsGoingPastADayThatFailsAndDoesNotClaimFreshness(t *testing.T) {
	h := newRollupHarness(t)
	h.file(day(-1) + 1)
	h.file(day(0) + 1)
	h.rollups.failDay = day(-1)

	if err := h.jobs.refreshRecent(t.Context(), leader.Term{Number: 1}); err == nil {
		t.Fatal("recent pass reported success with a day that failed")
	}

	if h.rolledCalls(day(0)) != 1 {
		t.Error("today was not refreshed because yesterday failed")
	}
	if got := h.state("refreshed_time"); got != nil {
		t.Errorf("refreshed_time = %d with yesterday failed, want none", *got)
	}
}

func TestRecentPass_RetriesAFailedPassOnTheNextTick(t *testing.T) {
	h := newRollupHarness(t)
	h.file(day(-1) + 1)
	h.rollups.failDay = day(-1)
	h.rollups.onRefresh = func() { h.now = h.now.Add(25 * time.Second) }

	for tick := range 3 {
		if err := h.jobs.refreshRecent(t.Context(), leader.Term{Number: 1}); err == nil {
			t.Fatalf("tick %d reported success with yesterday failing", tick)
		}
		if h.rollups.attempts != tick+1 {
			t.Fatalf("after tick %d, %d refresh attempts; want each tick to retry", tick, h.rollups.attempts)
		}
		h.now = h.now.Add(time.Minute)
	}

	h.rollups.failDay = 0
	h.runRecent()
	attempts := h.rollups.attempts
	h.now = h.now.Add(time.Minute)
	if err := h.jobs.refreshRecent(t.Context(), leader.Term{Number: 1}); err != nil || h.rollups.attempts != attempts {
		t.Errorf("a tick inside a successful pass's spacing = %v after %d new attempts, want nil and none", err, h.rollups.attempts-attempts)
	}
}

func TestRecentPass_LeavesRollupsOfAnotherGrainAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		version int
		refused bool
	}{
		"older": {store.SpendRollupGrainVersion - 1, false},
		"newer": {store.SpendRollupGrainVersion + 1, true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newRollupHarness(t)
			if err := h.rollups.MarkRefreshed(t.Context(), 1_000); err != nil {
				t.Fatalf("mark refreshed: %v", err)
			}
			if _, err := h.db.ExecContext(t.Context(), `UPDATE token_spend_rollup_state SET grain_version = ?`, tc.version); err != nil {
				t.Fatalf("set the grain: %v", err)
			}
			h.file(day(0) + 1)

			err := h.jobs.refreshRecent(t.Context(), leader.Term{Number: 1})

			if refused := errors.Is(err, errRollupsFromNewerRelease); refused != tc.refused || (!tc.refused && err != nil) {
				t.Errorf("recent pass = %v, want refused %t", err, tc.refused)
			}
			if h.rolledCalls(day(0)) != 0 || h.rollups.attempts != 0 {
				t.Error("the recent pass wrote rows beside another grain's")
			}
			if got := h.state("refreshed_time"); got == nil || *got != 1_000 {
				t.Errorf("refreshed_time = %v, want the earlier refresh kept", got)
			}
		})
	}
}

func TestRecentPass_RemovesRowsTheLedgerDoesNotHave(t *testing.T) {
	h := newRollupHarness(t)
	h.file(day(0) + 1)
	h.runRecent()
	if _, err := h.db.ExecContext(t.Context(), `INSERT INTO token_spend_daily (day, grain_key, user_sub, model_name, calls, unpriced_calls,
		uncached_input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		uncached_input_cost, output_cost, cache_read_cost, cache_write_cost, total_cost)
		VALUES (?, 'old-grain', 'user-1', 'm', 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)`, day(0)); err != nil {
		t.Fatalf("plant an old-grain row: %v", err)
	}
	h.resetRecords()

	h.runRecent()

	if got := h.rolledCalls(day(0)); got != 1 {
		t.Errorf("today's rolled calls = %d, want the ledger's 1", got)
	}
	if !slices.Equal(h.rollups.rebuilt, []int64{day(0)}) {
		t.Errorf("rebuilt = %v, want today rebuilt with stale rows removed", h.rollups.rebuilt)
	}
}

func TestReconcile_ClearsADayOnlyTheRollupHas(t *testing.T) {
	h := newRollupHarness(t)
	ledgerRow := idgen.New()
	_, err := store.NewSpendLedgerRepo(h.db).Insert(t.Context(), []store.SpendLedgerEntry{{
		ID: ledgerRow, CompletedTime: day(-7) + 1, UserSub: new("user-1"), Surface: "agent", ModelName: new("m"), TraceID: new("t1"),
	}})
	if err != nil {
		t.Fatalf("file a call: %v", err)
	}
	h.runReconcile()
	if _, err = h.db.ExecContext(t.Context(), `DELETE FROM token_spend_ledger WHERE id = ?`, ledgerRow); err != nil {
		t.Fatalf("delete the call: %v", err)
	}

	h.runReconcile()

	if got := h.rolledCalls(day(-7)); got != 0 {
		t.Errorf("rolled calls on a day the ledger no longer has = %d, want 0", got)
	}
	activities, err := dbutil.QueryVal[int](t.Context(), h.db, `SELECT COUNT(*) FROM token_spend_daily_activities WHERE day = ?`, day(-7))
	if err != nil || activities != 0 {
		t.Errorf("activity rows on that day = %d, %v; want none", activities, err)
	}
}

func TestRecentPass_RollsUpACallStampedTomorrowOnceTomorrowBegins(t *testing.T) {
	h := newRollupHarness(t)
	h.now = today.Add(24*time.Hour - 2*time.Minute)
	h.file(day(1) + 1)

	h.runRecent()
	if h.rolledCalls(day(1)) != 0 {
		t.Fatal("a call for tomorrow was rolled up today")
	}

	h.now = today.Add(24*time.Hour + time.Minute)
	h.runRecent()
	if got := h.rolledCalls(day(1)); got != 1 {
		t.Errorf("rolled calls once tomorrow began = %d, want 1", got)
	}
}

func TestRecentPass_BacksOffInProportionToHowLongItTook(t *testing.T) {
	h := newRollupHarness(t)
	h.rollups.onRefresh = func() { h.now = h.now.Add(30 * time.Second) }
	start := h.now
	h.file(day(0) + 1)
	h.runRecent()

	h.file(day(0) + 2)
	h.resetRecords()
	h.now = start.Add(time.Minute)
	h.runRecent()
	if len(h.rollups.refreshed)+len(h.rollups.rebuilt) != 0 {
		t.Error("a pass a minute after a 30 s pass ran; want it held off")
	}

	h.now = start.Add(150 * time.Second)
	h.runRecent()
	if len(h.rollups.refreshed) != 1 {
		t.Errorf("refreshed = %v at five times the last pass's length, want today", h.rollups.refreshed)
	}
}
