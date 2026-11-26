// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"math"
	"testing"
)

func TestLevelOf_WarnsAtThePercentageOfTheEffectiveCapAndLimitsAtTheCap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		spent  int64
		amount *int64
		topUp  int64
		warn   *int
		want   Level
	}{
		{"unlimited never warns", math.MaxInt64, nil, 0, new(80), LevelNone},
		{"below the warning", 7_900, new(int64(10_000)), 0, new(80), LevelNone},
		{"exactly the warning", 8_000, new(int64(10_000)), 0, new(80), LevelWarn},
		{"past the warning", 9_999, new(int64(10_000)), 0, new(80), LevelWarn},
		{"exactly the limit", 10_000, new(int64(10_000)), 0, new(80), LevelLimit},
		{"no warning percentage only limits", 9_999, new(int64(10_000)), 0, nil, LevelNone},
		{"no warning percentage still limits", 10_000, new(int64(10_000)), 0, nil, LevelLimit},
		{"the warning is of the topped-up cap", 8_000, new(int64(10_000)), 10_000, new(80), LevelNone},
		{"the warning is of the topped-up cap, past it", 16_000, new(int64(10_000)), 10_000, new(80), LevelWarn},
		{"a zero cap is at its limit with nothing spent", 0, new(int64(0)), 0, new(80), LevelLimit},
		{"a warning at 100% is the limit", 10_000, new(int64(10_000)), 0, new(100), LevelLimit},
	} {
		if got := LevelOf(tc.spent, tc.amount, tc.topUp, tc.warn); got != tc.want {
			t.Errorf("%s: LevelOf = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLevelOf_AgreesWithExhaustedOnTheLimit(t *testing.T) {
	for _, spent := range []int64{0, 99, 100, 101} {
		limited := LevelOf(spent, new(int64(100)), 0, new(50)) == LevelLimit
		if limited != Exhausted(spent, new(int64(100)), 0) {
			t.Errorf("at %d spent the level says limit=%v, Exhausted says %v", spent, limited, !limited)
		}
	}
}

func TestLevel_AboveOrdersNoneWarnLimit(t *testing.T) {
	if !LevelLimit.Above(LevelWarn) || !LevelWarn.Above(LevelNone) || LevelNone.Above(LevelNone) || LevelWarn.Above(LevelLimit) {
		t.Error("levels are not ordered none < warn < limit")
	}
}
