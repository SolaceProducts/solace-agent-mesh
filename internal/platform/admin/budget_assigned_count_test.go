// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"io"
	"log/slog"
	"testing"
)

func TestBudgetService_ResponsesCarryTheAssignedCount(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	budgets := NewBudgetService(f.budgets, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := t.Context()
	eng := f.userBudget(t, "Zz Engineering")
	for _, sub := range []string{"alice", "bob"} {
		if _, err := f.svc.Create(ctx, CreateBudgetAssignmentRequest{
			SubjectType: "user", SubjectID: f.user(t, sub, "jit", nil, nil), BudgetID: eng.ID,
		}, "assigner"); err != nil {
			t.Fatalf("assign %s: %v", sub, err)
		}
	}

	got, err := budgets.Get(ctx, eng.ID)
	if err != nil || got.AssignedCount != 2 {
		t.Errorf("Get = %+v, %v; want a count of 2", got, err)
	}
	page, err := budgets.List(ctx, BudgetListQuery{PageNumber: 1, PageSize: 20, SubjectType: "user", Name: "Zz"})
	if err != nil || len(page.Data) != 1 || page.Data[0].AssignedCount != 2 {
		t.Errorf("List = %+v, %v; want one budget with a count of 2", page, err)
	}
	renamed, err := budgets.Update(ctx, eng.ID, UpdateBudgetRequest{Name: new("Zz Renamed")}, "editor")
	if err != nil || renamed.AssignedCount != 2 {
		t.Errorf("Update = %+v, %v; want a count of 2", renamed, err)
	}
	promoted, err := budgets.SetDefault(ctx, eng.ID, "editor")
	if err != nil || promoted.AssignedCount != 2 {
		t.Errorf("SetDefault = %+v, %v; want a count of 2", promoted, err)
	}
}
