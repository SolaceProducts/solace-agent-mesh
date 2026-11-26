// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
)

func TestUsageBudgets_ResolveAsAppliedUserBudgetsDoes(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}
	user := func(sub string) string {
		id := idgen.New()
		exec(`INSERT INTO users (id, issuer, sub, created_by, updated_by, created_time, updated_time)
			VALUES (?, 'https://idp', ?, 'test', 'test', 0, 0)`, id, sub)
		return id
	}
	userBudget := func(name string, amount *int64) string {
		id := idgen.New()
		exec(`INSERT INTO budgets (id, name, name_lower, subject_type, amount, warn_percentage, created_by, updated_by, created_time, updated_time)
			VALUES (?, ?, ?, 'user', ?, NULL, 'test', 'test', 0, 0)`, id, name, name, amount)
		return id
	}
	assign := func(userID, budgetID string) {
		exec(`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
			VALUES (?, 'user', ?, ?, 'test', 'test', 0, 0)`, idgen.New(), userID, budgetID)
	}
	topUp := func(userID string, amount, expires int64) {
		exec(`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
			VALUES (?, 'user', ?, ?, ?, 'test', 'test', 0, 0)`, idgen.New(), userID, amount, expires)
	}
	now := time.Date(2026, time.October, 20, 15, 0, 0, 0, time.UTC)
	nextPeriod := budget.PeriodEnd(now).UnixMilli()
	capped := int64(450_0000000000)
	exec(`UPDATE budgets SET amount = ? WHERE subject_type = 'user' AND is_default = 1`, int64(100_0000000000))
	procurement, uncapped := userBudget("Procurement", &capped), userBudget("No Cap", nil)
	assigned, defaulted, unlimited, topped, lapsed, toppedUnlimited, system :=
		user("assigned"), user("defaulted"), user("unlimited"), user("topped"), user("lapsed"), user("topped-unlimited"), user("system:pipeline")
	exec(`UPDATE users SET provisioned_via = 'system' WHERE id = ?`, system)
	for _, id := range []string{assigned, topped, lapsed} {
		assign(id, procurement)
	}
	assign(unlimited, uncapped)
	assign(toppedUnlimited, uncapped)
	topUp(topped, 50_0000000000, nextPeriod)
	topUp(lapsed, 50_0000000000, budget.PeriodStart(now).UnixMilli())
	topUp(toppedUnlimited, 50_0000000000, nextPeriod)
	ids := []string{assigned, defaulted, unlimited, topped, lapsed, toppedUnlimited, system}

	want, err := appliedUserBudgets(ctx, store.NewBudgetAssignmentsRepo(db), store.NewBudgetsRepo(db), ids, now.UnixMilli())
	if err != nil {
		t.Fatalf("applied budgets: %v", err)
	}
	got, err := usage.Budgets{
		Assignments: store.NewBudgetAssignmentsRepo(db), Budgets: store.NewBudgetsRepo(db), TopUps: store.NewBudgetTopUpsRepo(db),
	}.Users(ctx, ids, now)
	if err != nil {
		t.Fatalf("usage budgets: %v", err)
	}

	for _, id := range ids {
		var g *usage.Budget
		if b, ok := got[id]; ok {
			g = &b
		}
		assertSameBudget(t, id, g, want[id])
	}
}

func TestUsageBudgets_ResolveTheMeshAsTheBudgetsAPIDoes(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	now := time.Date(2026, time.October, 20, 15, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `UPDATE budgets SET amount = ? WHERE subject_type = 'mesh' AND is_default = 1`, int64(5_000_0000000000)); err != nil {
		t.Fatalf("cap the mesh: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, ?, ?, 'test', 'test', 0, 0)`, idgen.New(), store.BudgetSubjectMesh, store.BudgetMeshSubjectRef, int64(250_0000000000), budget.PeriodEnd(now).UnixMilli()); err != nil {
		t.Fatalf("top up the mesh: %v", err)
	}
	budgets, topUps := store.NewBudgetsRepo(db), store.NewBudgetTopUpsRepo(db)

	meshBudget, err := tierDefaultBudget(ctx, budgets, store.BudgetSubjectMesh)
	if err != nil {
		t.Fatalf("mesh budget: %v", err)
	}
	active, err := topUps.Active(ctx, store.BudgetSubjectMesh, store.BudgetMeshSubjectRef, now.UnixMilli())
	if err != nil || active == nil {
		t.Fatalf("mesh top-up = %v, %v; want the one filed", active, err)
	}
	want := meshSubjectResponse(meshBudget, &subjectTopUp{amount: active.Amount, expiresTime: active.ExpiresTime})
	if want.Budget == nil || want.TopUp == nil {
		t.Fatalf("budgets API mesh subject = %+v, want the capped default and its top-up", want)
	}
	got, err := usage.Budgets{Assignments: store.NewBudgetAssignmentsRepo(db), Budgets: budgets, TopUps: topUps}.Mesh(ctx, now)
	if err != nil {
		t.Fatalf("usage mesh budget: %v", err)
	}
	assertSameBudget(t, "mesh", got, want)
}

func assertSameBudget(t *testing.T, subject string, g *usage.Budget, w BudgetSubjectResponse) {
	t.Helper()
	if w.Budget == nil {
		if g != nil {
			t.Errorf("%s: usage resolved %+v, the budgets API none", subject, *g)
		}
		return
	}
	if g == nil {
		t.Fatalf("%s: usage resolved none, the budgets API %+v", subject, *w.Budget)
	}
	if g.ID != w.Budget.ID || string(g.Source) != w.Budget.Source {
		t.Errorf("%s: usage budget %s (%s), the budgets API's %s (%s)", subject, g.ID, g.Source, w.Budget.ID, w.Budget.Source)
	}
	for _, amount := range []struct {
		name      string
		got, want *string
	}{
		{"amount", formatBudgetAmount(g.Amount), w.Budget.Amount},
		{"effective amount", formatBudgetAmount(g.EffectiveAmount), w.EffectiveAmount},
	} {
		if (amount.got == nil) != (amount.want == nil) || (amount.got != nil && *amount.got != *amount.want) {
			t.Errorf("%s: usage %s %v, the budgets API's %v", subject, amount.name, amount.got, amount.want)
		}
	}
	if (g.TopUp == nil) != (w.TopUp == nil) {
		t.Errorf("%s: usage top-up %+v, the budgets API's %+v", subject, g.TopUp, w.TopUp)
	} else if g.TopUp != nil && (restTimestamp(g.TopUp.ExpiresTime) != w.TopUp.ExpiresTime || *formatBudgetAmount(&g.TopUp.Amount) != w.TopUp.Amount) {
		t.Errorf("%s: usage top-up %+v, the budgets API's %+v", subject, *g.TopUp, *w.TopUp)
	}
}

func TestUsageBudgets_AgreeWhenTheTierHasNoDefault(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	id := idgen.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, issuer, sub, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'https://idp', 'nobody', 'test', 'test', 0, 0)`, id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE budgets SET is_default = 0 WHERE subject_type = 'user'`); err != nil {
		t.Fatalf("drop the default: %v", err)
	}
	now := time.Date(2026, time.October, 20, 15, 0, 0, 0, time.UTC)

	want, err := appliedUserBudgets(ctx, store.NewBudgetAssignmentsRepo(db), store.NewBudgetsRepo(db), []string{id}, now.UnixMilli())
	if err != nil {
		t.Fatalf("applied budgets: %v", err)
	}
	got, err := usage.Budgets{
		Assignments: store.NewBudgetAssignmentsRepo(db), Budgets: store.NewBudgetsRepo(db), TopUps: store.NewBudgetTopUpsRepo(db),
	}.Users(ctx, []string{id}, now)
	if err != nil {
		t.Fatalf("usage budgets: %v", err)
	}
	if want[id].Budget != nil {
		t.Fatalf("the budgets API resolved %+v for a tier with no default", want[id].Budget)
	}
	if b, ok := got[id]; ok {
		t.Errorf("usage resolved %+v, the budgets API none", b)
	}
}
