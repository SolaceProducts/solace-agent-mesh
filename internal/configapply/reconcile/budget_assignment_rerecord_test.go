// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type budgetRerecordPlatform struct {
	*rekeyPlatform
	budgetIDs map[string]string
}

func newBudgetRerecordPlatform(t *testing.T, wrap func(http.Handler) http.Handler) *budgetRerecordPlatform {
	t.Helper()
	p := &budgetRerecordPlatform{rekeyPlatform: newRekeyPlatform(t, wrap), budgetIDs: map[string]string{}}
	for _, name := range []string{"Engineering", "Operations"} {
		b, err := p.client.CreateBudget(t.Context(), wire.CreateBudgetRequest{Name: name, SubjectType: store.BudgetSubjectUser, Amount: wire.Patch[string]{Set: true, Null: true}})
		if err != nil {
			t.Fatalf("create budget %s: %v", name, err)
		}
		p.budgetIDs[name] = b.ID
	}
	return p
}

func (p *budgetRerecordPlatform) assign(t *testing.T, identity apiv2.BudgetAssignmentIdentity, budget string) string {
	t.Helper()
	a, err := p.client.CreateBudgetAssignment(t.Context(), wire.CreateBudgetAssignmentRequest{
		SubjectType: store.BudgetSubjectUser, BudgetID: p.budgetIDs[budget],
		Identity: &apiv2.BudgetAssignmentIdentityRequest{Type: identity.Type, Value: identity.Value, Issuer: identity.Issuer},
	})
	if err != nil {
		t.Fatalf("assign %+v to %s: %v", identity, budget, err)
	}
	return a.ID
}

func (p *budgetRerecordPlatform) plan(t *testing.T, files ...resolve.ResolvedResource) *PlanResult {
	t.Helper()
	stored, err := p.client.ListBudgets(t.Context())
	if err != nil {
		t.Fatalf("list budgets: %v", err)
	}
	out := &PlanResult{Budgets: mustPlanBudgets(t, files, stored)}
	if err := planAllBudgetAssignments(t.Context(), p.client, out); err != nil {
		t.Fatalf("plan budget users: %v", err)
	}
	return out
}

func (p *budgetRerecordPlatform) planErr(t *testing.T, files ...resolve.ResolvedResource) error {
	t.Helper()
	stored, err := p.client.ListBudgets(t.Context())
	if err != nil {
		t.Fatalf("list budgets: %v", err)
	}
	return planAllBudgetAssignments(t.Context(), p.client, &PlanResult{Budgets: mustPlanBudgets(t, files, stored)})
}

func (p *budgetRerecordPlatform) apply(t *testing.T, plan *PlanResult) *budgetAssignmentOutcome {
	t.Helper()
	out := applyBudgetAssignments(t.Context(), p.client, plan, nil, ApplyOptions{Prune: true})
	if len(out.Failures) > 0 {
		t.Fatalf("apply had failures: %+v", out.Failures)
	}
	return out
}

func (p *budgetRerecordPlatform) assignment(t *testing.T, id string) wire.BudgetAssignmentResponse {
	t.Helper()
	return p.assignmentWhere(t, "assignment "+id, func(a wire.BudgetAssignmentResponse) bool { return a.ID == id })
}

func (p *budgetRerecordPlatform) userAssignment(t *testing.T, user string) wire.BudgetAssignmentResponse {
	t.Helper()
	return p.assignmentWhere(t, "user "+user+"'s assignment", func(a wire.BudgetAssignmentResponse) bool {
		return a.Subject.ID != nil && *a.Subject.ID == user
	})
}

func (p *budgetRerecordPlatform) assignmentWhere(t *testing.T, what string, match func(wire.BudgetAssignmentResponse) bool) wire.BudgetAssignmentResponse {
	t.Helper()
	all, err := p.client.ListBudgetAssignments(t.Context())
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	i := slices.IndexFunc(all, match)
	if i < 0 {
		t.Fatalf("%s is gone", what)
	}
	return all[i]
}

func renderedAssignmentPlan(plan *PlanResult) string {
	var b bytes.Buffer
	renderBudgetAssignmentPlan(&b, plan.BudgetAssignments, FormatOptions{NoColor: true})
	return b.String()
}

func assertAssignmentPlan(t *testing.T, plan *PlanResult, creates, moves, rerecords, removes, unchanged int) {
	t.Helper()
	p := plan.BudgetAssignments
	got := []int{len(p.Creates), len(p.Moves), len(p.Rerecords), len(p.Removes), len(p.Unchanged)}
	if want := []int{creates, moves, rerecords, removes, unchanged}; !slices.Equal(got, want) {
		t.Errorf("plan = %v creates/moves/re-records/removes/unchanged, want %v\n%s", got, want, renderedAssignmentPlan(plan))
	}
}

var (
	aliceOld = apiv2.BudgetAssignmentIdentity{Type: store.BudgetIdentityEmail, Value: "alice@oldco.com"}
	aliceNew = apiv2.BudgetAssignmentIdentity{Type: store.BudgetIdentityEmail, Value: "alice@newco.com"}
)

func TestApplyBudgetAssignments_RerecordsTheEmailAUserNowSignsInWith(t *testing.T) {
	p := newBudgetRerecordPlatform(t, nil)
	alice := p.signIn(t, rekeyIssuer, "sub-alice", "alice@oldco.com")
	assigned := p.assign(t, aliceOld, "Engineering")
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")

	plan := p.plan(t, budgetWithUsers("Engineering", "alice@newco.com"))
	assertAssignmentPlan(t, plan, 0, 0, 1, 0, 0)
	if want := "identity: alice@oldco.com → alice@newco.com (same user)"; !strings.Contains(renderedAssignmentPlan(plan), want) {
		t.Errorf("plan does not show %q:\n%s", want, renderedAssignmentPlan(plan))
	}
	if out := p.apply(t, plan); len(out.Rerecorded) != 1 {
		t.Errorf("apply = %+v, want one re-record", out)
	}

	got := p.assignment(t, assigned)
	if *got.Subject.Identity != aliceNew || *got.Subject.ID != alice || got.Budget.ID != p.budgetIDs["Engineering"] {
		t.Errorf("after apply: identity %+v, user %s, budget %s; want alice@newco.com, alice, Engineering", *got.Subject.Identity, *got.Subject.ID, got.Budget.Name)
	}
	assertAssignmentPlan(t, p.plan(t, budgetWithUsers("Engineering", "alice@newco.com")), 0, 0, 0, 0, 1)

	carol := p.signIn(t, rekeyIssuer, "sub-carol", "alice@oldco.com")
	plan = p.plan(t, budgetWithUsers("Engineering", "alice@newco.com"), budgetWithUsers("Operations", "alice@oldco.com"))
	assertAssignmentPlan(t, plan, 1, 0, 0, 0, 1)
	p.apply(t, plan)
	if got := p.assignment(t, assigned); got.Budget.ID != p.budgetIDs["Engineering"] {
		t.Errorf("alice moved to %s; the old address should have assigned whoever has it now", got.Budget.Name)
	}
	if got := p.userAssignment(t, carol); got.Budget.ID != p.budgetIDs["Operations"] {
		t.Errorf("carol assigned to %s, want Operations", got.Budget.Name)
	}
}

func TestPlanBudgetAssignments_RefusesBothAddressesBeforeTheRerecordWithAWayOut(t *testing.T) {
	p := newBudgetRerecordPlatform(t, nil)
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@oldco.com")
	assigned := p.assign(t, aliceOld, "Engineering")
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
	carol := p.signIn(t, rekeyIssuer, "sub-carol", "alice@oldco.com")
	both := []resolve.ResolvedResource{budgetWithUsers("Engineering", "alice@newco.com"), budgetWithUsers("Operations", "alice@oldco.com")}

	err := p.planErr(t, both...)
	if want := "apply with alice@newco.com alone first to re-record it, then add alice@oldco.com"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("plan error = %v, want it to contain %q", err, want)
	}

	p.apply(t, p.plan(t, budgetWithUsers("Engineering", "alice@newco.com")))
	p.apply(t, p.plan(t, both...))
	if got := p.assignment(t, assigned); got.Budget.ID != p.budgetIDs["Engineering"] {
		t.Errorf("alice moved to %s, want her kept on Engineering", got.Budget.Name)
	}
	if got := p.userAssignment(t, carol); got.Budget.ID != p.budgetIDs["Operations"] {
		t.Errorf("carol assigned to %s, want Operations", got.Budget.Name)
	}
}

func TestApplyBudgetAssignments_MovesAndRerecordsTogether(t *testing.T) {
	p := newBudgetRerecordPlatform(t, nil)
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@oldco.com")
	assigned := p.assign(t, aliceOld, "Engineering")
	bySub := map[string]any{"type": "sub", "value": "sub-alice", "issuer": rekeyIssuer}
	files := []resolve.ResolvedResource{budgetWithUsers("Engineering"), budgetWithUsers("Operations", bySub)}

	plan := p.plan(t, files...)
	assertAssignmentPlan(t, plan, 0, 1, 0, 0, 0)
	if want := "identity: alice@oldco.com → sub-alice (" + rekeyIssuer + ") (same user)"; !strings.Contains(renderedAssignmentPlan(plan), want) {
		t.Errorf("plan does not show %q:\n%s", want, renderedAssignmentPlan(plan))
	}
	p.apply(t, plan)

	got := p.assignment(t, assigned)
	if got.Budget.ID != p.budgetIDs["Operations"] || got.Subject.Identity.Type != store.BudgetIdentitySub {
		t.Errorf("after apply: %s by %+v, want Operations by sub", got.Budget.Name, *got.Subject.Identity)
	}
	assertAssignmentPlan(t, p.plan(t, files...), 0, 0, 0, 0, 1)
}

func rejectPatches(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestApplyBudgetAssignments_FailsOnA400(t *testing.T) {
	p := newBudgetRerecordPlatform(t, rejectPatches)
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@oldco.com")
	p.signIn(t, rekeyIssuer, "sub-bob", "bob@acme.test")
	p.assign(t, aliceOld, "Engineering")
	p.assign(t, apiv2.BudgetAssignmentIdentity{Type: store.BudgetIdentityEmail, Value: "bob@acme.test"}, "Engineering")
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")

	plan := p.plan(t, budgetWithUsers("Engineering", "alice@newco.com"), budgetWithUsers("Operations", "bob@acme.test"))
	assertAssignmentPlan(t, plan, 0, 1, 1, 0, 0)
	out := applyBudgetAssignments(t.Context(), p.client, plan, nil, ApplyOptions{Prune: true})
	if len(out.Failures) != 2 || len(out.Skipped) != 0 || len(out.Moved) != 0 || len(out.Rerecorded) != 0 {
		t.Errorf("apply = %+v, want bob's move and alice's re-record both failed", out)
	}
}
