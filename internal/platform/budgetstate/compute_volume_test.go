// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendvolume"
)

const computeVolumeUsersEnv = "SAM_TEST_BUDGETSTATE_VOLUME_USERS"

const (
	computeVolumeRuns  = 10
	tierDefaultAmount  = 1_000_000_000_000
	meshAmount         = 100_000_000_000_000_000
	topUpEvery         = 10
	usersPerSystemUser = 100
	periodCostUnit     = 10_000_000_000
	periodCostScramble = 37
	periodCostSpan     = 150
	budgetUnit         = 500_000_000_000
	budgetKinds        = 5
	topUpAmount        = 100_000_000_000
)

func TestComputeVolume(t *testing.T) {
	var counts []int
	for _, field := range strings.Split(os.Getenv(computeVolumeUsersEnv), ",") {
		if n, _ := strconv.Atoi(strings.TrimSpace(field)); n > 0 {
			counts = append(counts, n)
		}
	}
	if len(counts) == 0 {
		t.Skipf("set %s to user counts such as 500,10000 to time Compute", computeVolumeUsersEnv)
	}
	for _, users := range counts {
		timeCompute(t, users)
	}
}

func timeCompute(t *testing.T, users int) {
	t.Helper()
	systemUsers := max(users/usersPerSystemUser, 1)
	db, err := store.Open(store.TestDBURL(t), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedComputeVolume(t, db, users, systemUsers)

	var state budget.EnforcementState
	var durations []time.Duration
	for range computeVolumeRuns {
		durations = append(durations, spendvolume.Timed(func() {
			state, err = Compute(t.Context(), NewStore(db), testNow)
		}))
		if err != nil {
			t.Fatalf("compute at %d users: %v", users, err)
		}
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("encode state: %v", err)
	}

	cold := durations[0]
	slices.Sort(durations)
	attrs := []any{"dialect", db.Dialect(), "users", users, "systemUsers", systemUsers,
		"blockedUsers", len(state.BlockedUsers), "payloadBytes", len(payload)}
	t.Log(spendvolume.Format("budgetstate.Compute.cold", cold, attrs...))
	t.Log(spendvolume.Format("budgetstate.Compute.median", durations[len(durations)/2], attrs...))
	t.Log(spendvolume.Format("budgetstate.Compute.max", durations[len(durations)-1], attrs...))

	if !state.BudgetsInUse || state.UsersOmitted {
		t.Errorf("state at %d users: budgetsInUse = %t, usersOmitted = %t, want budgets in use and the user list sent", users, state.BudgetsInUse, state.UsersOmitted)
	}
	if want := expectedBlocked(users, systemUsers); len(state.BlockedUsers) != want {
		t.Errorf("blocked users at %d users = %d, want %d", users, len(state.BlockedUsers), want)
	}
}

func seedComputeVolume(t *testing.T, db *store.DB, users, systemUsers int) {
	t.Helper()
	ctx := t.Context()
	periodStart := budget.PeriodStart(testNow)
	steps := []struct {
		name string
		run  func() error
	}{
		{"users", func() error { return spendvolume.InsertUsers(ctx, db, spendvolume.Users{Count: users}) }},
		{"tier default", func() error { return spendvolume.SetTierDefault(ctx, db, tierDefaultAmount) }},
		{"budgets", func() error { return spendvolume.InsertBudgets(ctx, db, spendvolume.Budgets{}) }},
		{"assignments", func() error { return spendvolume.InsertAssignments(ctx, db, users) }},
		{"system users", func() error { return spendvolume.InsertSystemUsers(ctx, db, systemUsers) }},
		{"top-ups", func() error {
			return spendvolume.InsertTopUps(ctx, db, spendvolume.TopUps{Users: users, Every: topUpEvery, Amount: topUpAmount,
				Created: testNow.Add(-time.Hour), Expires: testPeriodEnd})
		}},
		{"period rows", func() error {
			return spendvolume.InsertPeriodRows(ctx, db, spendvolume.PeriodRows{Users: users, SystemUsers: systemUsers,
				Start: periodStart.UnixMilli(), End: testPeriodEnd.UnixMilli()})
		}},
		{"mesh limit", func() error { return setMeshLimit(t, db) }},
		{"analyze", func() error { return spendvolume.Analyze(ctx, db) }},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			t.Fatalf("seed %s: %v", s.name, err)
		}
	}
}

func setMeshLimit(t *testing.T, db *store.DB) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `UPDATE budgets SET amount = ? WHERE subject_type = 'mesh'`, int64(meshAmount))
	return err
}

func generatedCost(n int) int64 {
	return int64((n*periodCostScramble)%periodCostSpan) * periodCostUnit
}

func generatedBudget(kind int) *int64 {
	if kind == budgetKinds-1 {
		return nil
	}
	amount := int64(kind+1) * budgetUnit
	return &amount
}

func expectedBlocked(users, systemUsers int) int {
	defaultAmount := int64(tierDefaultAmount)
	blocked := 0
	for n := range users {
		amount, topUp := &defaultAmount, int64(0)
		if kind := n % budgetKinds; kind != 0 {
			amount = generatedBudget(kind)
		}
		if n%topUpEvery == 0 {
			topUp = topUpAmount
		}
		if budget.Exhausted(generatedCost(n), amount, topUp) {
			blocked++
		}
	}
	for n := range systemUsers {
		if budget.Exhausted(generatedCost(n), generatedBudget(n%budgetKinds), 0) {
			blocked++
		}
	}
	return blocked
}
