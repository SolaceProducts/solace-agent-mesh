// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func TestBudgetAlertStatesRepo_SetReplacesASubjectsStateWithinItsPeriod(t *testing.T) {
	repo := NewBudgetAlertStatesRepo(TestDB(t))
	const october, november = int64(1_790_000_000_000), int64(1_792_000_000_000)
	set := func(s BudgetAlertState) {
		t.Helper()
		if err := repo.SetAlertState(t.Context(), s); err != nil {
			t.Fatalf("set %+v: %v", s, err)
		}
	}
	set(BudgetAlertState{SubjectType: BudgetSubjectUser, SubjectRef: "id:a", PeriodStart: october, AlertedLevel: "warn", CapKey: "k1", UpdatedTime: 1})
	set(BudgetAlertState{SubjectType: BudgetSubjectUser, SubjectRef: "id:a", PeriodStart: october, AlertedLevel: "limit", CapKey: "k2", UpdatedTime: 2})
	set(BudgetAlertState{SubjectType: BudgetSubjectMesh, SubjectRef: BudgetMeshSubjectRef, PeriodStart: october, AlertedLevel: "warn", CapKey: "m", UpdatedTime: 3})
	set(BudgetAlertState{SubjectType: BudgetSubjectUser, SubjectRef: "id:a", PeriodStart: november, AlertedLevel: "warn", CapKey: "k2", UpdatedTime: 4})

	got, err := repo.AlertStates(t.Context(), october)
	if err != nil {
		t.Fatalf("alert states: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("october's states = %+v, want the user and the mesh", got)
	}
	for _, s := range got {
		if s.SubjectRef == "id:a" && (s.AlertedLevel != "limit" || s.CapKey != "k2" || s.UpdatedTime != 2) {
			t.Errorf("user state = %+v, want the second write", s)
		}
	}

	deleted, err := repo.DeleteAlertStatesBefore(t.Context(), november)
	if err != nil || deleted != 2 {
		t.Fatalf("deleted %d (err %v), want october's two", deleted, err)
	}
	if left, _ := repo.AlertStates(t.Context(), november); len(left) != 1 {
		t.Errorf("november's states = %+v, want them kept", left)
	}
}

func TestBudgetAlertStatesRepo_RefusesAnUnknownLevelOrSubject(t *testing.T) {
	repo := NewBudgetAlertStatesRepo(TestDB(t))
	for _, s := range []BudgetAlertState{
		{SubjectType: BudgetSubjectUser, SubjectRef: "id:a", PeriodStart: 1, AlertedLevel: "panic", CapKey: "k", UpdatedTime: 1},
		{SubjectType: "team", SubjectRef: "t", PeriodStart: 1, AlertedLevel: "warn", CapKey: "k", UpdatedTime: 1},
	} {
		if err := repo.SetAlertState(t.Context(), s); err == nil {
			t.Errorf("state %+v was stored", s)
		}
	}
}
