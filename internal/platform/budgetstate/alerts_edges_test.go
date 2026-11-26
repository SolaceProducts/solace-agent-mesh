// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (f *fixture) unlimitMesh() {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), `UPDATE budgets SET amount = NULL WHERE subject_type = 'mesh'`); err != nil {
		f.t.Fatalf("unlimit the mesh: %v", err)
	}
}

func (f *fixture) meshAlertState() store.BudgetAlertState {
	f.t.Helper()
	for _, row := range f.alertStates() {
		if row.SubjectType == store.BudgetSubjectMesh {
			return row
		}
	}
	f.t.Fatal("no mesh alert state recorded")
	return store.BudgetAlertState{}
}

func TestAlerts_AMeshMadeUnlimitedIsLoweredAndReArmedWhenLimitedAgain(t *testing.T) {
	r := newAlertRig(t)
	r.limitMesh(100)
	r.limitUserDefault(1_000)
	r.spend(testNow, 100)
	r.start(1)
	r.recomputed()
	assertLevel(t, r.onlyAlert("the mesh limit"), budget.LevelLimit)

	r.unlimitMesh()
	r.recomputeOnSignal()
	r.recomputeOnSignal()
	r.noAlert("the mesh made unlimited")
	if row := r.meshAlertState(); row.AlertedLevel != string(budget.LevelNone) {
		t.Errorf("mesh alert state = %+v, want it lowered to none", row)
	}
	if n := r.logs.count(t, "WARN", "budget alert state could not be recorded; a later recompute may alert again"); n != 0 {
		t.Errorf("recording the unlimited mesh failed %d times", n)
	}

	r.limitMesh(100)
	r.recomputeOnSignal()
	assertLevel(t, r.onlyAlert("the mesh limited again"), budget.LevelLimit)
}

type flakyAdmins struct {
	calls atomic.Int32
}

func (a *flakyAdmins) BudgetAdmins(context.Context) ([]Recipient, error) {
	if a.calls.Add(1) == 1 {
		return nil, errors.New("not ready")
	}
	return []Recipient{testAdmin}, nil
}

func TestAlerts_AnAdminAlertThatCannotResolveItsRecipientsIsRetried(t *testing.T) {
	r := newAlertRig(t)
	admins := &flakyAdmins{}
	r.mon.alerting.Admins = admins
	r.limitMesh(100)
	r.spend(testNow, 100)
	r.start(1)
	r.recomputed()
	r.noAlert("administrators not resolvable")
	if rows := r.alertStates(); len(rows) != 0 {
		t.Fatalf("alert states = %+v, want the crossing left unrecorded", rows)
	}

	r.recomputeOnSignal()
	assertLevel(t, r.onlyAlert("the retried mesh limit"), budget.LevelLimit)
}

func TestAlerts_AnAdminWhoCannotReadBudgetsGetsNoLinkToThem(t *testing.T) {
	r := newAlertRig(t)
	r.admins = staticAdmins{{PlatformUserID: testAdmin.PlatformUserID, LoginID: testAdmin.LoginID}}
	r.limitMesh(100)
	r.spend(testNow, 100)
	r.start(1)
	r.recomputed()

	if alert := r.onlyAlert("the mesh limit"); alert.Metadata["link_path"] != nil {
		t.Errorf("alert metadata = %v, want no budgets link", alert.Metadata)
	}
}

func TestAlerts_ASystemUserBookedByRowAndByNameIsAlertedOnce(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	id := r.systemUser("system:nightly")
	r.spendAs(id, "", testNow, 100)
	r.spendAs("", "system:nightly", testNow, 100)
	r.start(1)
	r.recomputed()

	assertLevel(t, r.onlyAlert("the service account's limit"), budget.LevelLimit)
	if rows := r.alertStates(); len(rows) != 1 {
		t.Errorf("alert states = %+v, want one for the service account", rows)
	}
}

func TestAlerts_ARunOfPassesCutOffByTheDeadlineLogsAtInfoOnce(t *testing.T) {
	const cutOff = "budget alert pass reached its deadline; the remaining subjects are alerted on a later recompute"
	r := newAlertRig(t)
	r.mon.logger = slog.New(slog.NewJSONHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.limitUserDefault(100)
	r.mon.broker = &slowPublish{Broker: r.brk, clock: r.clock, step: alertPassDeadline}
	r.start(1)
	r.recomputed()
	spendNow := func(emails ...string) {
		for _, id := range r.sortedHumans(emails...) {
			r.spendAs(id, "", testNow, 100)
		}
	}
	recompute := func() {
		r.at = r.clock.Now()
		r.recomputeOnSignal()
	}

	spendNow("a@example.com", "b@example.com", "c@example.com")
	recompute()
	recompute()
	if info, debug := r.logs.count(t, "INFO", cutOff), r.logs.count(t, "DEBUG", cutOff); info != 1 || debug != 1 {
		t.Fatalf("two consecutive cut-off passes logged %d INFO and %d DEBUG lines, want one of each", info, debug)
	}
	recompute()

	spendNow("d@example.com", "e@example.com")
	recompute()
	if n := r.logs.count(t, "INFO", cutOff); n != 2 {
		t.Errorf("a cut-off pass after one that finished logged %d INFO lines in all, want a second", n)
	}
}
