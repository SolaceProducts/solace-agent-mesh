// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func savingCall(tokens, cost int64, edit func(*SpendLedgerEntry)) SpendLedgerEntry {
	return rollupCall(func(e *SpendLedgerEntry) {
		e.AvoidedInputTokens = new(tokens)
		if edit != nil {
			edit(e)
		}
		if e.Cost != nil {
			e.AvoidedInputCost = new(cost)
		}
	})
}

func TestSpendRollupRepo_RefreshDaySumsWhatSmartContextManagementSparedAndCountsTheMeasuredCalls(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	fileCalls(t, db,
		savingCall(100, 2_070_000, nil),
		savingCall(50, 1_000_000, nil),
		savingCall(30, 0, func(e *SpendLedgerEntry) { e.Cost = nil }),
		savingCall(0, 0, nil),
		rollupCall(nil),
	)

	refreshDay(t, repo, rollupTestDay, false)

	rows := dailyRollupRows(t, db, rollupTestDay)
	if len(rows) != 1 || rows[0].Calls != 5 || rows[0].MeasuredCalls != 4 || rows[0].PricedMeasured != 3 || rows[0].AvoidedTokens != 180 || rows[0].AvoidedCost != 3_070_000 {
		t.Errorf("daily rows = %+v, want one row of 5 calls, 4 measured, 3 of them priced, 180 avoided tokens and 3,070,000 avoided cost", rows)
	}
	models, err := dbutil.QueryRows[struct {
		Tokens   int64 `db:"avoided_input_tokens"`
		Cost     int64 `db:"avoided_input_cost"`
		Measured int64 `db:"measured_calls"`
		Priced   int64 `db:"priced_measured_calls"`
	}](t.Context(), db, `SELECT avoided_input_tokens, avoided_input_cost, measured_calls, priced_measured_calls FROM token_spend_daily_models WHERE day = ?`, rollupTestDay)
	if err != nil {
		t.Fatalf("read models rollup: %v", err)
	}
	if len(models) != 1 || models[0].Tokens != 180 || models[0].Cost != 3_070_000 || models[0].Measured != 4 || models[0].Priced != 3 {
		t.Errorf("models rows = %+v, want one row of 180 avoided tokens, 3,070,000 avoided cost, 4 measured calls and 3 priced", models)
	}
}

func TestSpendUsageRepo_SavingsSumASubjectsCallsOverTheWindow(t *testing.T) {
	f := newUsageFixture(t)
	f.calls(
		savingCall(100, 2_070_000, func(e *SpendLedgerEntry) { e.Cost = &spend.Cost{UncachedInput: 10} }),
		savingCall(40, 0, func(e *SpendLedgerEntry) { e.Cost = nil }),
		savingCall(7, 70, func(e *SpendLedgerEntry) { e.Cost = &spend.Cost{UncachedInput: 10}; e.UserSub = new("someone-else") }),
		rollupCall(func(e *SpendLedgerEntry) { e.UserSub = new("someone-else") }),
	)

	tenant, err := f.usage.Savings(t.Context(), UsageSubject{}, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("tenant savings: %v", err)
	}
	if tenant != (UsageSavings{AvoidedInputTokens: 147, AvoidedInputCost: 2_070_070, Calls: 4, MeasuredCalls: 3, PricedMeasuredCalls: 2, UnpricedCalls: 1}) {
		t.Errorf("tenant savings = %+v, want 147 tokens, 2,070,070 cost over 4 calls, 3 measured, 2 of them priced, 1 unpriced", tenant)
	}
	user, err := f.usage.Savings(t.Context(), UsageSubject{Kind: UsageUsers, Name: "user-1"}, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("user savings: %v", err)
	}
	if user != (UsageSavings{AvoidedInputTokens: 140, AvoidedInputCost: 2_070_000, Calls: 2, MeasuredCalls: 2, PricedMeasuredCalls: 1, UnpricedCalls: 1}) {
		t.Errorf("user-1 savings = %+v, want their two calls alone", user)
	}
	empty, err := f.usage.Savings(t.Context(), UsageSubject{}, rollupTestDay-SpendRollupDayMillis, rollupTestDay)
	if err != nil || empty != (UsageSavings{}) {
		t.Errorf("savings of an empty window = %+v, %v; want zeros", empty, err)
	}
}
