// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func budgetWithUsers(name string, users ...any) resolve.ResolvedResource {
	return budgetFile(name, map[string]any{"subjectType": store.BudgetSubjectUser, "amount": nil, "users": users})
}

func concreteAssignment(id, budgetID, budgetName string, identity *apiv2.BudgetAssignmentIdentity, email *string) wire.BudgetAssignmentResponse {
	subjectID := "user-" + id
	return wire.BudgetAssignmentResponse{
		ID: id, State: store.BudgetAssignmentConcrete,
		Subject: apiv2.BudgetAssignmentSubject{Type: store.BudgetSubjectUser, ID: &subjectID, Identity: identity,
			User: &apiv2.BudgetAssignmentUser{Email: email, EmailShared: new(false)}},
		Budget: apiv2.BudgetRef{ID: budgetID, Name: budgetName},
	}
}

const testIssuer = "https://idp"

func signedInAssignment(id, budgetID, budgetName string, identity *apiv2.BudgetAssignmentIdentity, emailAddr, sub string) wire.BudgetAssignmentResponse {
	a := concreteAssignment(id, budgetID, budgetName, identity, &emailAddr)
	a.Subject.User.Sub, a.Subject.User.Issuer = &sub, new(testIssuer)
	return a
}

func subIdentity(v string) map[string]any {
	return map[string]any{"type": "sub", "value": v, "issuer": testIssuer}
}

func pendingAssignment(id, budgetID, budgetName string, identity apiv2.BudgetAssignmentIdentity) wire.BudgetAssignmentResponse {
	return wire.BudgetAssignmentResponse{
		ID: id, State: store.BudgetAssignmentPending,
		Subject: apiv2.BudgetAssignmentSubject{Type: store.BudgetSubjectUser, Identity: &identity},
		Budget:  apiv2.BudgetRef{ID: budgetID, Name: budgetName},
	}
}

func email(v string) *apiv2.BudgetAssignmentIdentity {
	return &apiv2.BudgetAssignmentIdentity{Type: store.BudgetIdentityEmail, Value: v}
}

func budgetsWithUsers(t *testing.T, desired ...resolve.ResolvedResource) *budgetPlan {
	t.Helper()
	stored := withBudget(withBudget(seededBudgets(),
		wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser}),
		wire.BudgetResponse{ID: "o1", Name: "Operations", SubjectType: store.BudgetSubjectUser})
	return mustPlanBudgets(t, desired, stored)
}

func mustPlanAssignments(t *testing.T, budgets *budgetPlan, actual []wire.BudgetAssignmentResponse) *budgetAssignmentPlan {
	t.Helper()
	plan, _, err := planBudgetAssignments(budgets, actual, map[string]bool{"default": true, "ci": true})
	if err != nil {
		t.Fatalf("planBudgetAssignments: %v", err)
	}
	return plan
}

func assignmentOpNames(ops []budgetAssignmentOp) string {
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = op.name()
	}
	return strings.Join(names, ", ")
}

func TestReadBudgetUsers(t *testing.T) {
	users, managed, err := readBudgetUsers(map[string]any{"users": []any{
		" Alice@Example.com ",
		"system:ci",
		map[string]any{"type": "sub", "value": "u-1", "issuer": "https://idp"},
		map[string]any{"type": "system", "value": "default"},
	}})
	if err != nil || !managed {
		t.Fatalf("readBudgetUsers = %v, %v, %v", users, managed, err)
	}
	want := []budgetIdentity{
		{Type: "email", Value: "alice@example.com"},
		{Type: "system", Value: "ci"},
		{Type: "sub", Value: "u-1", Issuer: "https://idp"},
		{Type: "system", Value: "default"},
	}
	if len(users) != len(want) {
		t.Fatalf("users = %+v, want %+v", users, want)
	}
	for i := range want {
		if users[i] != want[i] {
			t.Errorf("users[%d] = %+v, want %+v", i, users[i], want[i])
		}
	}

	if _, managed, err := readBudgetUsers(map[string]any{}); managed || err != nil {
		t.Errorf("no users key: managed = %v, %v; want unmanaged", managed, err)
	}
	if users, managed, err := readBudgetUsers(map[string]any{"users": nil}); !managed || err != nil || len(users) != 0 {
		t.Errorf("users: null = %v, %v, %v; want managed and empty", users, managed, err)
	}
}

func TestReadBudgetUsers_Refusals(t *testing.T) {
	for name, c := range map[string]struct {
		users any
		want  string
	}{
		"not a list":              {"alice@example.com", "must be a list"},
		"an empty entry":          {[]any{" "}, "is empty"},
		"a repeat":                {[]any{"a@example.com", "A@example.com"}, "repeats spec.users[0]"},
		"a legacy identity":       {[]any{map[string]any{"type": "legacy", "value": "a"}}, "type must be email, sub or system"},
		"a sub without an issuer": {[]any{map[string]any{"type": "sub", "value": "u"}}, "issuer is required"},
		"an issuer on an email":   {[]any{map[string]any{"type": "email", "value": "a@example.com", "issuer": "x"}}, "issuer is only for a sub"},
		"a system user as a sub":  {[]any{map[string]any{"type": "sub", "value": "system:ci", "issuer": "sam:system"}}, "write a service account as system:ci"},
		"an unknown key":          {[]any{map[string]any{"type": "email", "value": "a@example.com", "role": "x"}}, `unknown key "role"`},
		"a number":                {[]any{7}, "must be an email"},
		"an unquoted numeric sub": {[]any{map[string]any{"type": "sub", "value": 109876543210, "issuer": "https://idp"}}, "value must be a string, got int; quote it"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := readBudgetUsers(map[string]any{"users": c.users}); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestPlanBudgets_RefusesUsersOnTheMeshBudget(t *testing.T) {
	desired := budgetFile("Agent Mesh", map[string]any{"subjectType": "mesh", "amount": nil, "users": []any{"a@example.com"}})
	if _, err := planBudgets([]resolve.ResolvedResource{desired}, seededBudgets()); err == nil || !strings.Contains(err.Error(), "has no users") {
		t.Errorf("err = %v, want the mesh budget refused", err)
	}
}

func TestPlanBudgetAssignments(t *testing.T) {
	budgets := budgetsWithUsers(t,
		budgetWithUsers("Engineering", "stays@example.com", "new@example.com", "mover@example.com", "system:ci", "waiting@example.com"),
		budgetWithUsers("Operations"),
		budgetWithUsers("Platform", "fresh@example.com"),
	)
	actual := []wire.BudgetAssignmentResponse{
		concreteAssignment("a-stays", "e1", "Engineering", email("stays@example.com"), new("stays@example.com")),
		concreteAssignment("a-mover", "o1", "Operations", email("mover@example.com"), new("mover@example.com")),
		concreteAssignment("a-ci", "o1", "Operations", &apiv2.BudgetAssignmentIdentity{Type: "system", Value: "ci"}, nil),
		concreteAssignment("a-gone", "e1", "Engineering", email("gone@example.com"), new("gone@example.com")),
		pendingAssignment("a-waiting", "e1", "Engineering", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "waiting@example.com"}),
		concreteAssignment("a-elsewhere", defaultUserID, "Default User", email("other@example.com"), new("other@example.com")),
	}
	plan := mustPlanAssignments(t, budgets, actual)

	for label, c := range map[string]struct {
		ops  []budgetAssignmentOp
		want string
	}{
		"creates":   {plan.Creates, "new@example.com → Engineering, fresh@example.com → Platform"},
		"moves":     {plan.Moves, "mover@example.com → Engineering, system:ci → Engineering"},
		"removes":   {plan.Removes, "gone@example.com → Engineering"},
		"unchanged": {plan.Unchanged, "stays@example.com → Engineering, waiting@example.com → Engineering"},
	} {
		if got := assignmentOpNames(c.ops); got != c.want {
			t.Errorf("%s = [%s], want [%s]", label, got, c.want)
		}
	}
	if plan.Moves[0].FromBudget != "Operations" || plan.Moves[0].ID != "a-mover" {
		t.Errorf("move = %+v, want a-mover from Operations", plan.Moves[0])
	}
	if plan.Creates[1].BudgetID != "" {
		t.Errorf("a budget this apply creates has id %q, want none until apply", plan.Creates[1].BudgetID)
	}

	ops := (&PlanResult{BudgetAssignments: plan}).Ops()
	if s := Summarize(ops); s.Create != 2 || s.Update != 2 || s.Delete != 1 || s.Unchanged != 2 {
		t.Errorf("summary = %+v", s)
	}
	if s := Summarize(ops); s.Pending(false) != true {
		t.Errorf("pending without prune = false, want the creates to count")
	}
}

func TestPlanBudgetAssignments_MatchesAUIAssignmentByEmail(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Engineering", "pat@example.com"))
	plan := mustPlanAssignments(t, budgets, []wire.BudgetAssignmentResponse{
		signedInAssignment("a-pat", "e1", "Engineering", email("pat@example.com"), "pat@example.com", "u-pat"),
	})
	if len(plan.Unchanged) != 1 || len(plan.Creates)+len(plan.Removes) != 0 {
		t.Errorf("plan = %+v, want pat unchanged", plan)
	}
}

func TestPlanBudgetAssignments_MatchesAUIAssignmentBySub(t *testing.T) {
	actual := []wire.BudgetAssignmentResponse{
		signedInAssignment("a-pat", "e1", "Engineering", email("pat@example.com"), "pat@example.com", "u-pat"),
	}
	recordedByEmail := func(op budgetAssignmentOp) bool {
		return op.Recorded != nil && op.Recorded.Value == "pat@example.com" && op.Identity.Type == store.BudgetIdentitySub
	}

	same := mustPlanAssignments(t, budgetsWithUsers(t, budgetWithUsers("Engineering", subIdentity("u-pat"))), actual)
	if len(same.Rerecords) != 1 || same.Rerecords[0].ID != "a-pat" || !recordedByEmail(same.Rerecords[0]) ||
		len(same.Creates)+len(same.Moves)+len(same.Removes)+len(same.Unchanged) != 0 {
		t.Errorf("same budget: plan = %+v, want a-pat re-recorded from its email to the sub", same)
	}

	other := mustPlanAssignments(t, budgetsWithUsers(t,
		budgetWithUsers("Engineering"),
		budgetWithUsers("Operations", subIdentity("u-pat")),
	), actual)
	if len(other.Moves) != 1 || other.Moves[0].ID != "a-pat" || other.Moves[0].FromBudget != "Engineering" || !recordedByEmail(other.Moves[0]) ||
		len(other.Creates)+len(other.Removes)+len(other.Rerecords) != 0 {
		t.Errorf("another budget: plan = %+v, want a-pat moved from Engineering and re-recorded", other)
	}
}

func TestPlanBudgetAssignments_SharedEmailMatchesNoOne(t *testing.T) {
	jo := signedInAssignment("a-jo", "o1", "Operations",
		&apiv2.BudgetAssignmentIdentity{Type: "sub", Value: "u-jo", Issuer: new(testIssuer)}, "jo@example.com", "u-jo")
	jo.Subject.User.EmailShared = new(true)

	plan := mustPlanAssignments(t, budgetsWithUsers(t, budgetWithUsers("Engineering", "jo@example.com")), []wire.BudgetAssignmentResponse{jo})
	if got := assignmentOpNames(plan.Creates); got != "jo@example.com → Engineering" || len(plan.Moves)+len(plan.Unchanged) != 0 {
		t.Errorf("plan = %+v, want jo@example.com planned as a create", plan)
	}
}

func TestPlanBudgetAssignments_Refusals(t *testing.T) {
	pat := []wire.BudgetAssignmentResponse{
		signedInAssignment("a-pat", "e1", "Engineering", email("pat@example.com"), "pat@example.com", "u-pat"),
	}
	for name, c := range map[string]struct {
		budgets []resolve.ResolvedResource
		actual  []wire.BudgetAssignmentResponse
		want    string
	}{
		"one user on two budgets": {
			[]resolve.ResolvedResource{budgetWithUsers("Engineering", "a@example.com"), budgetWithUsers("Operations", "A@example.com")},
			nil, `also a user of budget "Engineering"`,
		},
		"an unknown system user": {
			[]resolve.ResolvedResource{budgetWithUsers("Engineering", "system:nobody")}, nil, `no service account "nobody"`,
		},
		"one user by sub and by email": {
			[]resolve.ResolvedResource{budgetWithUsers("Engineering", subIdentity("u-pat")), budgetWithUsers("Operations", "pat@example.com")},
			pat, "names the same user",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := planBudgetAssignments(budgetsWithUsers(t, c.budgets...), c.actual, map[string]bool{"ci": true})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestPlanBudgetAssignments_IgnoresAStalePendingRow(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Engineering", "sam@example.com"))
	plan, warnings, err := planBudgetAssignments(budgets, []wire.BudgetAssignmentResponse{
		pendingAssignment("a-stale", "o1", "Operations", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "sam@example.com"}),
		concreteAssignment("a-sam", "e1", "Engineering", email("sam@example.com"), new("sam@example.com")),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "a-sam" {
		t.Errorf("plan = %+v, want sam's assignment unchanged", plan)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "a-stale") {
		t.Errorf("warnings = %v, want one naming a-stale", warnings)
	}
}

func TestPlanBudgetAssignments_DoesNotRemoveAStalePendingRowOnAManagedBudget(t *testing.T) {
	budgets := budgetsWithUsers(t,
		budgetWithUsers("Engineering", "sam@example.com"),
		budgetWithUsers("Operations", "kim@example.com"),
	)
	plan, warnings, err := planBudgetAssignments(budgets, []wire.BudgetAssignmentResponse{
		pendingAssignment("a-stale", "o1", "Operations", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "sam@example.com"}),
		concreteAssignment("a-sam", "e1", "Engineering", email("sam@example.com"), new("sam@example.com")),
		concreteAssignment("a-kim", "o1", "Operations", email("kim@example.com"), new("kim@example.com")),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removes) != 0 {
		t.Errorf("removes = [%s], want none", assignmentOpNames(plan.Removes))
	}
	if len(plan.Unchanged) != 2 || len(plan.Creates)+len(plan.Moves) != 0 {
		t.Errorf("plan = %+v, want sam and kim unchanged", plan)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "a-stale") {
		t.Errorf("warnings = %v, want one naming a-stale", warnings)
	}
}

func TestPlanBudgetAssignments_RemovesAnOrphanedPendingRowOnAManagedBudget(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Operations", "kim@example.com"))
	plan, warnings, err := planBudgetAssignments(budgets, []wire.BudgetAssignmentResponse{
		pendingAssignment("a-orphan", "o1", "Operations", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "orphan@example.com"}),
		concreteAssignment("a-kim", "o1", "Operations", email("kim@example.com"), new("kim@example.com")),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := assignmentOpNames(plan.Removes); got != "orphan@example.com → Operations" || plan.Removes[0].ID != "a-orphan" {
		t.Errorf("removes = [%s], want a-orphan", got)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

func TestPlanBudgetAssignments_AnEmailNamesTheAssignmentItMade(t *testing.T) {
	actual := []wire.BudgetAssignmentResponse{
		signedInAssignment("a-alice", "e1", "Engineering", email("alice@x"), "alice2@x", "u-alice"),
		signedInAssignment("a-bob", "o1", "Operations", email("bob@x"), "alice@x", "u-bob"),
	}
	for _, order := range [][]wire.BudgetAssignmentResponse{actual, {actual[1], actual[0]}} {
		plan, warnings, err := planBudgetAssignments(budgetsWithUsers(t, budgetWithUsers("Engineering", "alice@x")), order, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "a-alice" || len(plan.Creates)+len(plan.Moves)+len(plan.Rerecords)+len(plan.Removes) != 0 {
			t.Errorf("alice@x: plan = %+v, want a-alice unchanged and nothing else", plan)
		}
		if len(warnings) != 0 {
			t.Errorf("alice@x: warnings = %v, want none", warnings)
		}

		plan, _, err = planBudgetAssignments(budgetsWithUsers(t, budgetWithUsers("Engineering", "alice2@x")), order, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Rerecords) != 1 || plan.Rerecords[0].ID != "a-alice" || plan.Rerecords[0].Recorded.Value != "alice@x" ||
			len(plan.Creates)+len(plan.Moves)+len(plan.Removes)+len(plan.Unchanged) != 0 {
			t.Errorf("alice2@x: plan = %+v, want a-alice re-recorded from alice@x and nothing else", plan)
		}
	}

	bob := mustPlanAssignments(t, budgetsWithUsers(t, budgetWithUsers("Operations", subIdentity("u-bob"))), actual)
	if len(bob.Rerecords) != 1 || bob.Rerecords[0].ID != "a-bob" || bob.Rerecords[0].Recorded.Value != "bob@x" {
		t.Errorf("u-bob: plan = %+v, want a-bob re-recorded from bob@x", bob)
	}
}

func TestPlanBudgetAssignments_AnEmailNowSharedNamesTheAssignmentItMade(t *testing.T) {
	a := signedInAssignment("a-x", "e1", "Engineering", email("x@co"), "x@co", "u-x")
	a.Subject.User.EmailShared = new(true)
	plan, warnings, err := planBudgetAssignments(budgetsWithUsers(t, budgetWithUsers("Engineering", "x@co")), []wire.BudgetAssignmentResponse{a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "a-x" || len(plan.Creates)+len(plan.Moves)+len(plan.Removes) != 0 {
		t.Errorf("plan = %+v, want a-x unchanged and nothing else", plan)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

func TestPlanBudgetAssignments_AnEmailTwoAssignmentsWereMadeByIsRefused(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Engineering", "x@co"))
	_, _, err := planBudgetAssignments(budgets, []wire.BudgetAssignmentResponse{
		signedInAssignment("a-1", "e1", "Engineering", email("x@co"), "one@co", "u-1"),
		signedInAssignment("a-2", "o1", "Operations", email("x@co"), "two@co", "u-2"),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "used to assign more than one user") {
		t.Errorf("err = %v, want one saying x@co was used to assign more than one user", err)
	}
}

func TestPlanBudgetAssignments_ADeletedBudgetItsUsersLeaveRevertsNone(t *testing.T) {
	stored := withBudget(withBudget(seededBudgets(),
		wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser}),
		wire.BudgetResponse{ID: "x1", Name: "Retired", SubjectType: store.BudgetSubjectUser, AssignedCount: 1, PendingCount: 1})
	budgets := mustPlanBudgets(t, []resolve.ResolvedResource{budgetWithUsers("Engineering", "a@example.com", "b@example.com")}, stored)
	wantOps(t, "deletes before", budgets.Deletes, "Retired")
	if got := budgets.Deletes[0].Warnings; len(got) != 1 || !strings.Contains(got[0], "2 assigned users") {
		t.Errorf("warnings before the moves = %v, want both assignees reverting", got)
	}

	mustPlanAssignments(t, budgets, []wire.BudgetAssignmentResponse{
		concreteAssignment("a-a", "x1", "Retired", email("a@example.com"), new("a@example.com")),
		pendingAssignment("a-b", "x1", "Retired", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "b@example.com"}),
	})
	wantOps(t, "kept after", budgets.Kept, "Default User")
	wantOps(t, "deletes after", budgets.Deletes, "Retired")
	if got := budgets.Deletes[0].Warnings; got != nil {
		t.Errorf("warnings after the moves = %v, want none, as nobody reverts", got)
	}
}

func TestPlanBudgets_CountsPendingAssigneesAmongThoseWhoRevert(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "x1", Name: "Waiting", SubjectType: store.BudgetSubjectUser, PendingCount: 2})
	plan := mustPlanBudgets(t, nil, stored)
	wantOps(t, "deletes", plan.Deletes, "Waiting")
	if got := plan.Deletes[0].Warnings; len(got) != 1 || !strings.Contains(got[0], "2 assigned users") {
		t.Errorf("warnings = %v", got)
	}
}

type fakeAssignmentPlatform struct {
	mu       sync.Mutex
	calls    []string
	fail     string
	notFound string
}

func (f *fakeAssignmentPlatform) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	call := strings.TrimSpace(r.Method + " " + r.URL.Path + " " + string(bytes.TrimSpace(body)))
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if f.fail != "" && strings.Contains(call, f.fail) {
		writeV2Error(w, http.StatusConflict, "conflict", nil)
		return
	}
	if f.notFound != "" && strings.Contains(call, f.notFound) {
		writeV2Error(w, http.StatusNotFound, "not found", nil)
		return
	}
	switch r.Method {
	case "POST":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(entplatform.DataResponse[wire.BudgetAssignmentResponse]{Data: wire.BudgetAssignmentResponse{ID: "made"}})
	case "PATCH":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"moved"}}`))
	case "DELETE":
		w.WriteHeader(http.StatusNoContent)
	}
}

func newFakeAssignmentPlatform(t *testing.T) (*fakeAssignmentPlatform, *Client) {
	f := &fakeAssignmentPlatform{}
	srv := httptest.NewServer(servedOnV2(f.ServeHTTP))
	t.Cleanup(srv.Close)
	return f, newClientAgainstServer(srv)
}

func TestApplyBudgetAssignments(t *testing.T) {
	budgets := budgetsWithUsers(t,
		budgetWithUsers("Engineering", "new@example.com", "mover@example.com", "renamed@example.com"),
		budgetWithUsers("Platform", "fresh@example.com"),
		budgetWithUsers("Lost", "orphan@example.com"),
	)
	actual := []wire.BudgetAssignmentResponse{
		concreteAssignment("a-mover", "o1", "Operations", email("mover@example.com"), new("mover@example.com")),
		concreteAssignment("a-renamed", "e1", "Engineering", email("old@example.com"), new("renamed@example.com")),
		concreteAssignment("a-gone", "e1", "Engineering", email("gone@example.com"), new("gone@example.com")),
	}
	plan := &PlanResult{Budgets: budgets, BudgetAssignments: mustPlanAssignments(t, budgets, actual)}
	budgetsOut := &budgetOutcome{Created: []budgetOpResult{{Op: budgetOp{Name: "Platform", ID: "p1"}}}}

	for _, prune := range []bool{false, true} {
		f, c := newFakeAssignmentPlatform(t)
		out := applyBudgetAssignments(context.Background(), c, plan, budgetsOut, ApplyOptions{Prune: prune})
		want := []string{
			`POST /api/v2/platform/budgetAssignments {"subjectType":"user","subjectId":null,"identity":{"type":"email","value":"new@example.com","issuer":null},"budgetId":"e1"}`,
			`POST /api/v2/platform/budgetAssignments {"subjectType":"user","subjectId":null,"identity":{"type":"email","value":"fresh@example.com","issuer":null},"budgetId":"p1"}`,
			`PATCH /api/v2/platform/budgetAssignments/a-mover {"budgetId":"e1"}`,
			`PATCH /api/v2/platform/budgetAssignments/a-renamed {"identity":{"type":"email","value":"renamed@example.com","issuer":null}}`,
		}
		if prune {
			want = append(want, "DELETE /api/v2/platform/budgetAssignments/a-gone")
		}
		if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
			t.Errorf("prune=%v calls:\n%s\nwant\n%s", prune, strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
		}
		if len(out.Created) != 2 || len(out.Moved) != 1 || len(out.Rerecorded) != 1 || len(out.Failures) != 0 {
			t.Errorf("prune=%v outcome = %+v", prune, out)
		}
		var lost, pruned bool
		for _, s := range out.Skipped {
			lost = lost || (s.Op.Budget == "Lost" && s.Reason == skipReasonBudgetNotCreated)
			pruned = pruned || (s.Verb == verbRemove && s.Reason == "")
		}
		if !lost || pruned == prune {
			t.Errorf("prune=%v skipped = %+v; want Lost skipped as not created, and the removal skipped only without prune", prune, out.Skipped)
		}
	}
}

func TestApplyBudgetAssignments_ReportsAFailureAndCarriesOn(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Engineering", "taken@example.com", "next@example.com"))
	plan := &PlanResult{Budgets: budgets, BudgetAssignments: mustPlanAssignments(t, budgets, nil)}
	f, c := newFakeAssignmentPlatform(t)
	f.fail = "taken@example.com"

	res := &ApplyResult{BudgetAssignments: applyBudgetAssignments(context.Background(), c, plan, nil, ApplyOptions{})}
	if len(res.BudgetAssignments.Failures) != 1 || len(res.BudgetAssignments.Created) != 1 || !anyFailures(res) {
		t.Errorf("outcome = %+v, want one failure, one create, and anyFailures", res.BudgetAssignments)
	}
}

func TestApplyBudgetAssignments_ARemovalFindingADeletedSystemUsersAssignmentGoneSucceeds(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Engineering"))
	plan := &PlanResult{
		Budgets: budgets,
		BudgetAssignments: mustPlanAssignments(t, budgets, []wire.BudgetAssignmentResponse{
			concreteAssignment("a-bot", "e1", "Engineering", &apiv2.BudgetAssignmentIdentity{Type: "system", Value: "bot"}, nil),
			pendingAssignment("p-x", "e1", "Engineering", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "x@co"}),
		}),
		SystemUsers: &systemUserPlan{Deletes: []systemUserOp{{Name: "bot"}}},
	}
	f, c := newFakeAssignmentPlatform(t)
	f.notFound = "/budgetAssignments/"
	out := applyBudgetAssignments(context.Background(), c, plan, nil, ApplyOptions{Prune: true})
	if len(out.Removed) != 1 || out.Removed[0].Op.ID != "a-bot" {
		t.Errorf("removed = %+v, want a-bot", out.Removed)
	}
	if len(out.Failures) != 1 || out.Failures[0].Op.ID != "p-x" || !strings.Contains(out.Failures[0].Err.Error(), "re-run the apply") {
		t.Errorf("failures = %+v, want p-x, saying to re-run", out.Failures)
	}
}

func TestApplyBudgetAssignments_SkipsASystemUserTheMiniIDPKeptFromBeingCreated(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Engineering", "system:ci"))
	plan := &PlanResult{
		Budgets:           budgets,
		BudgetAssignments: mustPlanAssignments(t, budgets, nil),
		SystemUsers:       &systemUserPlan{Creates: []systemUserOp{{Name: "ci"}}},
		RBACWritesBlocked: true,
	}
	f, c := newFakeAssignmentPlatform(t)
	out := applyBudgetAssignments(context.Background(), c, plan, nil, ApplyOptions{})
	if len(f.calls) != 0 || len(out.Skipped) != 1 || out.Skipped[0].Reason != skipReasonMiniIDP {
		t.Errorf("calls %v, skipped %+v; want the assignment skipped for the mini IDP", f.calls, out.Skipped)
	}
}

func TestRenderBudgetAssignmentPlanAndApply(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Engineering", "new@example.com", "mover@example.com"))
	plan := mustPlanAssignments(t, budgets, []wire.BudgetAssignmentResponse{
		concreteAssignment("a-mover", "o1", "Operations", email("mover@example.com"), new("mover@example.com")),
		concreteAssignment("a-gone", "e1", "Engineering", email("gone@example.com"), new("gone@example.com")),
	})
	var out bytes.Buffer
	RenderPlan(&out, &PlanResult{Budgets: budgets, BudgetAssignments: plan}, FormatOptions{NoColor: true})
	for _, want := range []string{"budgets (users)/", "+ new@example.com → Engineering", "assign", "~ mover@example.com → Engineering", "move from Operations",
		"- gone@example.com → Engineering", "remove (needs --prune)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan output:\n%s\nwant %q", out.String(), want)
		}
	}
	out.Reset()
	RenderPlan(&out, &PlanResult{Budgets: budgets, BudgetAssignments: plan}, FormatOptions{NoColor: true, Prune: true})
	if !strings.Contains(out.String(), "remove") || strings.Contains(out.String(), "needs --prune") {
		t.Errorf("plan output under --prune:\n%s\nwant the removal without the --prune note", out.String())
	}

	out.Reset()
	renderBudgetAssignmentApply(&out, &budgetAssignmentOutcome{
		Created: []budgetAssignmentOpResult{{Op: plan.Creates[0]}},
		Skipped: []budgetAssignmentOpResult{{Op: budgetAssignmentOp{Budget: "Lost", Identity: budgetIdentity{Value: "x@example.com"}}, Reason: skipReasonBudgetNotCreated}},
	}, FormatOptions{NoColor: true})
	for _, want := range []string{"Applied budgets (users):", "assigned", "skipped (budget_not_created)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("apply output:\n%s\nwant %q", out.String(), want)
		}
	}
}

func TestPlanBudgetAssignments_APendingRowOnAnotherUsersRecordedEmailIsLive(t *testing.T) {
	actual := []wire.BudgetAssignmentResponse{
		signedInAssignment("a-x", "e1", "Engineering", email("x@co"), "y@co", "u-x"),
		pendingAssignment("p-x", "o1", "Operations", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "x@co"}),
	}
	plan, warnings, err := planBudgetAssignments(budgetsWithUsers(t,
		budgetWithUsers("Engineering", subIdentity("u-x")),
		budgetWithUsers("Operations"),
	), actual, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Rerecords) != 1 || plan.Rerecords[0].ID != "a-x" {
		t.Errorf("re-records = [%s], want a-x", assignmentOpNames(plan.Rerecords))
	}
	if len(plan.Removes) != 1 || plan.Removes[0].ID != "p-x" {
		t.Errorf("removes = [%s], want p-x", assignmentOpNames(plan.Removes))
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}

	_, _, err = planBudgetAssignments(budgetsWithUsers(t, budgetWithUsers("Operations", "x@co")), actual, nil)
	if err == nil || !strings.Contains(err.Error(), "used to assign more than one user") {
		t.Errorf("naming x@co: err = %v, want it refused as used to assign more than one user", err)
	}
}

func TestPlanBudgetAssignments_LeavesAnUnmanagedBudgetsAssignees(t *testing.T) {
	budgets := budgetsWithUsers(t,
		budgetWithUsers("Engineering", "kim@example.com"),
		budgetFile("Operations", map[string]any{"subjectType": store.BudgetSubjectUser, "amount": nil}),
	)
	plan := mustPlanAssignments(t, budgets, []wire.BudgetAssignmentResponse{
		concreteAssignment("a-kim", "e1", "Engineering", email("kim@example.com"), new("kim@example.com")),
		concreteAssignment("a-ops", "o1", "Operations", email("ops@example.com"), new("ops@example.com")),
	})
	if len(plan.Removes) != 0 {
		t.Errorf("removes = [%s], want none", assignmentOpNames(plan.Removes))
	}
}

func TestPlanBudgetAssignments_AnEmailRecordedTwiceIsAmbiguousWhileOneUserHoldsIt(t *testing.T) {
	_, _, err := planBudgetAssignments(budgetsWithUsers(t, budgetWithUsers("Engineering", "x@co")), []wire.BudgetAssignmentResponse{
		signedInAssignment("a-holder", "e1", "Engineering", email("x@co"), "x@co", "u-1"),
		signedInAssignment("a-former", "o1", "Operations", email("x@co"), "y@co", "u-2"),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "used to assign more than one user") {
		t.Errorf("err = %v, want x@co refused as used to assign more than one user", err)
	}
}

func TestUniqueAssignments(t *testing.T) {
	a := concreteAssignment("a-x", "e1", "Engineering", email("x@co"), new("x@co"))
	b := concreteAssignment("a-y", "e1", "Engineering", email("y@co"), new("y@co"))
	got := uniqueAssignments([]wire.BudgetAssignmentResponse{a, b, a})
	if len(got) != 2 || got[0].ID != "a-x" || got[1].ID != "a-y" {
		t.Errorf("unique = %+v, want a-x then a-y", got)
	}
}

func TestApplyBudgetAssignments_AssignsToACreatedBudgetThatFailedToBecomeDefault(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Platform", "fresh@example.com"))
	plan := &PlanResult{Budgets: budgets, BudgetAssignments: mustPlanAssignments(t, budgets, nil)}
	budgetsOut := &budgetOutcome{Failures: []budgetOpResult{{Op: budgetOp{Name: "Platform", ID: "p1"}, Verb: verbSetDefault, Err: errors.New("409")}}}
	f, c := newFakeAssignmentPlatform(t)
	out := applyBudgetAssignments(context.Background(), c, plan, budgetsOut, ApplyOptions{})
	if len(out.Created) != 1 || len(f.calls) != 1 || !strings.Contains(f.calls[0], `"budgetId":"p1"`) {
		t.Errorf("calls %v, outcome %+v; want fresh@example.com assigned to p1", f.calls, out)
	}
}

func hiddenAssignment(budgetID, budgetName string) wire.BudgetAssignmentResponse {
	a := concreteAssignment("a-channel", budgetID, budgetName, &apiv2.BudgetAssignmentIdentity{Type: "system", Value: "channel"}, nil)
	a.Subject.ID = new(rbac.ChannelSystemUserID)
	a.Subject.User.IsSystemUser = true
	return a
}

func TestPlanBudgetAssignments_AHiddenUsersAssignmentIsMatchedButNeverRemoved(t *testing.T) {
	actual := []wire.BudgetAssignmentResponse{hiddenAssignment("o1", "Operations")}
	moved, _, err := planBudgetAssignments(budgetsWithUsers(t, budgetWithUsers("Engineering", "system:channel"), budgetWithUsers("Operations")), actual, map[string]bool{"channel": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(moved.Moves) != 1 || moved.Moves[0].ID != "a-channel" || len(moved.Creates)+len(moved.Removes) != 0 {
		t.Errorf("naming it elsewhere: plan = %+v, want a-channel moved", moved)
	}
	left := mustPlanAssignments(t, budgetsWithUsers(t, budgetWithUsers("Operations")), actual)
	if len(left.Removes) != 0 {
		t.Errorf("a managed budget not naming it: removes = [%s], want none", assignmentOpNames(left.Removes))
	}
}

func TestPlanBudgets_CountsHiddenAssignees(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "x1", Name: "Channel", SubjectType: store.BudgetSubjectUser, HiddenAssignedCount: 1})
	hiddenOnly := mustPlanBudgets(t, nil, stored)
	wantOps(t, "deletes", hiddenOnly.Deletes, "Channel")
	if got := hiddenOnly.Deletes[0].Warnings; len(got) != 1 || !strings.Contains(got[0], "1 assigned user") {
		t.Errorf("hidden-only warnings = %v", got)
	}

	stored = withBudget(withBudget(seededBudgets(),
		wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser}),
		wire.BudgetResponse{ID: "x1", Name: "Retired", SubjectType: store.BudgetSubjectUser, AssignedCount: 1, HiddenAssignedCount: 1})
	budgets := mustPlanBudgets(t, []resolve.ResolvedResource{budgetWithUsers("Engineering", "a@example.com")}, stored)
	mustPlanAssignments(t, budgets, []wire.BudgetAssignmentResponse{
		concreteAssignment("a-a", "x1", "Retired", email("a@example.com"), new("a@example.com")),
		hiddenAssignment("x1", "Retired"),
	})
	wantOps(t, "deletes after the listed user moves", budgets.Deletes, "Retired")
	if got := budgets.Deletes[0].Warnings; len(got) != 1 || !strings.Contains(got[0], "1 assigned user") {
		t.Errorf("warnings after the listed user moves = %v, want the hidden one still reverting", got)
	}
}

func TestPlanAllBudgetAssignments_SaysWhenSystemUsersCouldNotBeRead(t *testing.T) {
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == platform.APIV2Prefix+serviceAccountsResource.path || r.URL.Path == platform.APIV2Prefix+legacyServiceAccountsResource.path {
			writeV2Error(w, http.StatusNotFound, "not found", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":0,"totalPages":0}}}`))
	}))
	t.Cleanup(srv.Close)
	c := newClientAgainstServer(srv)

	for users, want := range map[string]string{
		"system:default": "",
		"system:ci":      "could not be read",
	} {
		out := &PlanResult{Budgets: budgetsWithUsers(t, budgetWithUsers("Engineering", users))}
		err := planAllBudgetAssignments(context.Background(), c, out)
		if want == "" && err != nil {
			t.Errorf("%s: %v, want a built-in planned", users, err)
		}
		if want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: err = %v, want one containing %q", users, err, want)
		}
	}
}

func TestPrunedSystemUserAssignments(t *testing.T) {
	budgets := budgetsWithUsers(t, budgetWithUsers("Engineering", "system:ci", "system:default", "a@example.com"))
	plan := &PlanResult{
		Budgets:           budgets,
		BudgetAssignments: mustPlanAssignments(t, budgets, nil),
		SystemUsers:       &systemUserPlan{Deletes: []systemUserOp{{Name: "ci"}, {Name: "unlisted"}}},
	}

	warnings := prunedSystemUserWarnings(plan)
	if len(warnings) != 1 || !strings.Contains(warnings[0], `budget "Engineering"`) || !strings.Contains(warnings[0], "system:ci") {
		t.Errorf("warnings = %v, want one naming Engineering and system:ci", warnings)
	}
	err := checkPrunedSystemUserAssignments(plan)
	if err == nil || !strings.Contains(err.Error(), "system:ci") || strings.Contains(err.Error(), "system:default") {
		t.Errorf("check = %v, want it to refuse system:ci alone", err)
	}

	plan.SystemUsers.Deletes = []systemUserOp{{Name: "unlisted"}}
	if got := prunedSystemUserWarnings(plan); len(got) != 0 || checkPrunedSystemUserAssignments(plan) != nil {
		t.Errorf("a delete no budget names: warnings %v, check %v; want neither", got, checkPrunedSystemUserAssignments(plan))
	}
}
