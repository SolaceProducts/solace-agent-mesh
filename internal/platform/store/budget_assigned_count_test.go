// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"maps"
	"slices"
	"testing"
)

func TestBudgetsRepo_AssignedCount(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		two := createBudget(t, f.budgets, &Budget{Name: "Zz Two", Amount: new(int64(100))})
		one := createBudget(t, f.budgets, &Budget{Name: "Zz One", Amount: new(int64(100))})
		none := createBudget(t, f.budgets, &Budget{Name: "Zz None", Amount: new(int64(100))})
		f.assign(t, refA, two.ID)
		f.assign(t, refB, two.ID)
		moving := f.assign(t, refC, one.ID)

		counts := func() map[string]int64 {
			t.Helper()
			page, err := f.budgets.List(ctx, BudgetFilter{Name: "Zz", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]int64{}
			for _, b := range page {
				got[b.Name] = b.AssignedCount
			}
			return got
		}
		if got := counts(); got["Zz Two"] != 2 || got["Zz One"] != 1 || got["Zz None"] != 0 {
			t.Errorf("counts on a page = %v, want two 2, one 1, none 0", got)
		}
		if b, err := f.budgets.GetByID(ctx, two.ID); err != nil || b.AssignedCount != 2 {
			t.Errorf("GetByID count = %v, %v; want 2", b, err)
		}
		if b, err := f.budgets.GetByName(ctx, "zz one"); err != nil || b.AssignedCount != 1 {
			t.Errorf("GetByName count = %v, %v; want 1", b, err)
		}

		if _, err := f.repo.Update(ctx, moving.ID, BudgetAssignmentUpdate{BudgetID: none.ID, Actor: "mover", NowMs: moving.UpdatedTime + 1}); err != nil {
			t.Fatalf("move: %v", err)
		}
		if got := counts(); got["Zz One"] != 0 || got["Zz None"] != 1 {
			t.Errorf("after a move, counts = %v, want one 0, none 1", got)
		}
	})
}

func TestBudgetsRepo_DefaultCountsOnlyExplicitAssignments(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		tierDefault, err := f.budgets.GetByID(ctx, SeededDefaultUserBudgetID)
		if err != nil || tierDefault == nil {
			t.Fatalf("read the seeded default: %v, %v", tierDefault, err)
		}
		if tierDefault.AssignedCount != 0 {
			t.Errorf("unassigned default count = %d, want 0", tierDefault.AssignedCount)
		}
		f.assign(t, refA, tierDefault.ID)
		if b, err := f.budgets.GetByID(ctx, tierDefault.ID); err != nil || b.AssignedCount != 1 {
			t.Errorf("default with one explicit assignment = %v, %v; want 1", b, err)
		}
	})
}

func TestUsersRepo_ListUsersByBudget(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		f.assign(t, refA, eng.ID)
		f.assign(t, refB, SeededDefaultUserBudgetID)

		list := func(match *UserBudgetMatch) []string {
			t.Helper()
			users, total, err := NewUsersRepo(f.db).ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 100, Budget: match})
			if err != nil {
				t.Fatal(err)
			}
			if total != len(users) {
				t.Errorf("total = %d, want %d", total, len(users))
			}
			ids := make([]string, len(users))
			for i, u := range users {
				ids[i] = u.ID
			}
			return ids
		}

		if got := list(&UserBudgetMatch{BudgetID: eng.ID}); !slices.Equal(got, []string{refA}) {
			t.Errorf("Engineering = %v, want only refA", got)
		}
		onDefault := list(&UserBudgetMatch{BudgetID: SeededDefaultUserBudgetID, IsDefault: true})
		for _, want := range []string{refB, refC, refD} {
			if !slices.Contains(onDefault, want) {
				t.Errorf("default = %v, missing %s (pinned or unassigned)", onDefault, want)
			}
		}
		if slices.Contains(onDefault, refA) {
			t.Errorf("default = %v, includes refA, who is on Engineering", onDefault)
		}
		if got := list(&UserBudgetMatch{BudgetID: SeededDefaultUserBudgetID}); !slices.Equal(got, []string{refB}) {
			t.Errorf("default treated as an ordinary budget = %v, want only refB", got)
		}
	})
}

func TestBudgetAssignmentsRepo_UserBudgetIDs(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		f.assign(t, refA, eng.ID)
		f.assign(t, refB, SeededDefaultUserBudgetID)
		got, err := f.repo.UserBudgetIDs(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{refA: eng.ID, refB: SeededDefaultUserBudgetID}
		if !maps.Equal(got, want) {
			t.Errorf("UserBudgetIDs = %v, want %v", got, want)
		}
	})
}
