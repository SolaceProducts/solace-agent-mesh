// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"
)

func (f assignmentSvcFixture) recordedIdentity(t *testing.T, id string) BudgetAssignmentIdentity {
	t.Helper()
	got, err := f.svc.Get(t.Context(), id, true)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if got.Subject.Identity == nil {
		t.Fatalf("assignment %s reports no identity", id)
	}
	return *got.Subject.Identity
}

func (f assignmentSvcFixture) rerecord(t *testing.T, id string, req UpdateBudgetAssignmentRequest) (*BudgetAssignmentResponse, error) {
	t.Helper()
	return f.svc.Update(t.Context(), id, req, "apply")
}

func TestBudgetAssignmentService_UpdateRerecordsTheIdentity(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	alice := f.user(t, "sub-alice", "jit", nil, new("alice@acme.test"))
	a := f.mustAssign(t, alice, eng.ID)
	bySub := BudgetAssignmentIdentity{Type: "sub", Value: "sub-alice", Issuer: "https://idp.test"}

	got, err := f.rerecord(t, strings.ToUpper(a.ID), UpdateBudgetAssignmentRequest{Identity: &bySub})
	if err != nil || got.Budget.ID != eng.ID || *got.Subject.ID != alice {
		t.Fatalf("re-record = %+v, %v; want alice's assignment, still on Engineering", got, err)
	}
	if id := f.recordedIdentity(t, a.ID); id != bySub {
		t.Errorf("recorded identity = %+v, want %+v", id, bySub)
	}
	wantAudit(t, lastBudgetAudit(t, f.logs, auditActionUpdated), map[string]any{
		"resourceId": a.ID, "identityType": "sub", "previousIdentityType": "email", "previousBudgetId": nil,
	})

	audits := len(assignmentAudits(t, f.logs))
	if _, err = f.rerecord(t, a.ID, UpdateBudgetAssignmentRequest{Identity: &BudgetAssignmentIdentity{Type: "sub", Value: " sub-alice ", Issuer: "https://idp.test"}}); err != nil {
		t.Fatalf("re-record the same identity: %v", err)
	}
	if n := len(assignmentAudits(t, f.logs)); n != audits {
		t.Errorf("re-recording the identity already recorded audited %d lines, want none", n-audits)
	}

	byEmail := BudgetAssignmentIdentity{Type: "email", Value: "Alice@ACME.test"}
	got, err = f.rerecord(t, a.ID, UpdateBudgetAssignmentRequest{BudgetID: ops.ID, Identity: &byEmail})
	if err != nil || got.Budget.ID != ops.ID {
		t.Fatalf("move and re-record = %+v, %v; want alice on Operations", got, err)
	}
	if id := f.recordedIdentity(t, a.ID); id != (BudgetAssignmentIdentity{Type: "email", Value: "alice@acme.test"}) {
		t.Errorf("recorded identity = %+v, want the normalised email", id)
	}
	wantAudit(t, lastBudgetAudit(t, f.logs, auditActionUpdated), map[string]any{
		"resourceId": a.ID, "previousBudgetId": eng.ID, "previousIdentityType": "sub",
	})
}

func TestBudgetAssignmentService_UpdateRefusesAnIdentityThatDoesNotNameTheUser(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	alice := f.user(t, "sub-alice", "jit", nil, new("alice@acme.test"))
	f.user(t, "sub-bob", "jit", nil, new("bob@acme.test"))
	f.user(t, "sub-jo1", "jit", nil, new("jo@acme.test"))
	f.user(t, "sub-jo2", "jit", nil, new("jo@acme.test"))
	a := f.mustAssign(t, alice, eng.ID)
	pending, err := f.assignIdentity(t, BudgetAssignmentIdentity{Type: "email", Value: "later@acme.test"}, eng.ID)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		id    string
		req   UpdateBudgetAssignmentRequest
		field string
		want  string
	}{
		{"another user's email", a.ID, UpdateBudgetAssignmentRequest{Identity: emailIdentity("bob@acme.test")}, "identity.value", "names another user"},
		{"another user's sub", a.ID, UpdateBudgetAssignmentRequest{Identity: &BudgetAssignmentIdentity{Type: "sub", Value: "sub-bob", Issuer: "https://idp.test"}}, "identity.value", "names another user"},
		{"alice's sub under another issuer", a.ID, UpdateBudgetAssignmentRequest{Identity: &BudgetAssignmentIdentity{Type: "sub", Value: "sub-alice", Issuer: "https://other.test"}}, "identity.value", "no user has this identity"},
		{"an email no user has", a.ID, UpdateBudgetAssignmentRequest{Identity: emailIdentity("nobody@acme.test")}, "identity.value", "no user has this identity"},
		{"a shared email", a.ID, UpdateBudgetAssignmentRequest{Identity: emailIdentity("jo@acme.test")}, "identity.value", "Matches multiple users"},
		{"a shared email, with how to resolve it", a.ID, UpdateBudgetAssignmentRequest{Identity: emailIdentity("jo@acme.test")}, "identity", "assign by sub or resolve the duplicate"},
		{"a move with another user's email", a.ID, UpdateBudgetAssignmentRequest{BudgetID: ops.ID, Identity: emailIdentity("bob@acme.test")}, "identity.value", "names another user"},
		{"a malformed identity", a.ID, UpdateBudgetAssignmentRequest{Identity: &BudgetAssignmentIdentity{Type: "sub", Value: "sub-alice"}}, "identity.issuer", "is required"},
		{"a pending assignment", pending.ID, UpdateBudgetAssignmentRequest{Identity: emailIdentity("alice@acme.test")}, "identity", "pending assignment"},
		{"neither a budget nor an identity", a.ID, UpdateBudgetAssignmentRequest{}, "budgetId", "unless identity is given"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.rerecord(t, c.id, c.req)
			if details := validationDetails(t, err); !strings.Contains(strings.Join(details[c.field], "; "), c.want) {
				t.Errorf("details = %v, want %s to say %q", details, c.field, c.want)
			}
		})
	}
	if id := f.recordedIdentity(t, a.ID); id != *emailIdentity("alice@acme.test") {
		t.Errorf("a refused re-record wrote %+v", id)
	}
	if got, err := f.svc.Get(t.Context(), a.ID, false); err != nil || got.Budget.ID != eng.ID {
		t.Errorf("after a move refused for its identity, assignment = %+v, %v; want it still on Engineering", got, err)
	}
	if id := f.recordedIdentity(t, pending.ID); id != *emailIdentity("later@acme.test") {
		t.Errorf("a refused re-record of a pending assignment wrote %+v", id)
	}
}

func emailIdentity(v string) *BudgetAssignmentIdentity {
	return &BudgetAssignmentIdentity{Type: "email", Value: v}
}
