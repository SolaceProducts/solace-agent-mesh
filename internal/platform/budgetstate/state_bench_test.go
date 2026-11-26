// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
)

func benchEnforcementState(users int) budget.EnforcementState {
	state := budget.EnforcementState{BudgetsInUse: true, PeriodEnd: testPeriodEnd, ComputedAt: testNow}
	for i := range users {
		state.BlockedUsers = append(state.BlockedUsers, budget.UserKey(fmt.Sprintf("00000000-0000-4000-8000-%012d", i), ""))
	}
	slices.Sort(state.BlockedUsers)
	return state
}

func BenchmarkEnforcementState_EncodeDecode(b *testing.B) {
	for _, users := range []int{500, 10_000} {
		state := benchEnforcementState(users)
		payload, err := json.Marshal(state)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("users=%d/encode", users), func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(len(payload)), "bytes/op-payload")
			for b.Loop() {
				if _, err := json.Marshal(state); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("users=%d/decode", users), func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(len(payload)), "bytes/op-payload")
			for b.Loop() {
				if _, err := budget.DecodeState(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
