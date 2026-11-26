// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"context"
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
)

func TestAlerts_AMeshAlertWhoseContextEndsStopsThePassBeforeTheNextSubject(t *testing.T) {
	r := newAlertRig(t)
	r.limitMesh(100)
	r.limitUserDefault(100)
	r.spendAs(r.human("alice@example.com"), "", testNow, 100)
	r.spendAs(r.human("bob@example.com"), "", testNow, 100)
	ctx, cancel := context.WithCancel(t.Context())
	brk := &cancellingPublish{Broker: r.brk, cancel: cancel, delivered: true}
	r.mon.broker = brk

	r.mon.alert(ctx, testNow, r.assess())

	if n := brk.calls.Load(); n != 1 {
		t.Errorf("%d alerts started, want the pass to stop after the mesh alert its context ended in", n)
	}
	if got := recipientsOf(r.alerts()); len(got) != 1 || got[0] != testAdmin.PlatformUserID {
		t.Errorf("alerted %v, want only the budget admin", got)
	}
	if rows := r.alertStates(); len(rows) != 0 {
		t.Errorf("alert states = %+v, want nothing recorded once the context ended", rows)
	}
}

func TestMonitor_AMeshPeriodCheckWhoseContextEndedDoesNotWarn(t *testing.T) {
	const warning = "mesh budget period check could not read the period rows"
	for _, tc := range []struct {
		name     string
		ctx      func(t *testing.T) context.Context
		wantWarn int
	}{
		{"context ended", func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return ctx
		}, 0},
		{"read failed", func(t *testing.T) context.Context { return t.Context() }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newMonitorRig(t)
			if tc.wantWarn == 1 {
				if err := r.db.Close(); err != nil {
					t.Fatalf("close db: %v", err)
				}
			}

			r.mon.checkMeshPeriod(tc.ctx(t), testNow)

			if n := r.logs.count(t, "WARN", warning); n != tc.wantWarn {
				t.Errorf("%d warnings, want %d", n, tc.wantWarn)
			}
		})
	}
}

func TestMonitor_WarningListsAloneCountTowardTheLargeStateWarning(t *testing.T) {
	const warning = "budget state lists many users, so every broadcast of it is large"
	users := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("user-%d", i)
		}
		return out
	}
	r := newMonitorRig(t)

	r.mon.logUsers(budget.EnforcementState{WarnedUsers: users(largeUserListWarn / 2), UnwarnedUsers: users(largeUserListWarn / 2)})
	if n := r.logs.count(t, "WARN", warning); n != 0 {
		t.Fatalf("a state listing exactly the threshold warned %d times, want none", n)
	}
	r.mon.logUsers(budget.EnforcementState{WarnedUsers: users(largeUserListWarn / 2), UnwarnedUsers: users(largeUserListWarn/2 + 1)})

	if n := r.logs.count(t, "WARN", warning); n != 1 {
		t.Errorf("a state whose warning lists alone passed the threshold warned %d times, want once", n)
	}
}
