// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

var blockedUserCounts = []int{0, 500, 10_000}

func benchUserKey(i int) string {
	return UserKey(fmt.Sprintf("00000000-0000-4000-8000-%012d", i), "")
}

func benchGate(b *testing.B, blockedUsers int) (g *Gate, blocked, admitted string) {
	b.Helper()
	state := EnforcementState{BudgetsInUse: true, PeriodEnd: gatePeriodEnd, ComputedAt: time.Now()}
	for i := range blockedUsers {
		state.BlockedUsers = append(state.BlockedUsers, benchUserKey(i))
	}
	slices.Sort(state.BlockedUsers)
	g = NewGate(gateTestLogger)
	g.Apply(context.Background(), state)
	blocked = benchUserKey(0)
	admitted = benchUserKey(blockedUsers + 1)
	return g, blocked, admitted
}

func BenchmarkGate_CheckKnownBlock(b *testing.B) {
	for _, n := range blockedUserCounts {
		g, blocked, admitted := benchGate(b, n)
		lookups := []struct {
			name string
			user string
		}{{"miss", admitted}, {"hit", blocked}}
		for _, l := range lookups {
			if l.name == "hit" && n == 0 {
				continue
			}
			b.Run(fmt.Sprintf("blocked=%d/%s", n, l.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_ = g.CheckKnownBlock(l.user)
				}
			})
			b.Run(fmt.Sprintf("blocked=%d/%s/parallel", n, l.name), func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						_ = g.CheckKnownBlock(l.user)
					}
				})
			})
		}
	}
}

func BenchmarkGate_Check(b *testing.B) {
	for _, n := range blockedUserCounts {
		g, _, admitted := benchGate(b, n)
		b.Run(fmt.Sprintf("blocked=%d/admit", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = g.Check(admitted)
			}
		})
		b.Run(fmt.Sprintf("blocked=%d/admit/parallel", n), func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_ = g.Check(admitted)
				}
			})
		})
	}
}
