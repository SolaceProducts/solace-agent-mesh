// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (f assignmentSvcFixture) assignIdentity(t *testing.T, identity BudgetAssignmentIdentity, budgetID string) (*BudgetAssignmentResponse, error) {
	t.Helper()
	return f.svc.Create(t.Context(), CreateBudgetAssignmentRequest{
		SubjectType: store.BudgetSubjectUser, Identity: &identity, BudgetID: budgetID,
	}, "author")
}

func TestBudgetAssignmentService_CreateByIdentity(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	alice := f.user(t, "sub-alice", "jit", nil, new("alice@acme.test"))
	bob := f.user(t, "sub-bob", "jit", nil, nil)

	cases := map[string]struct {
		identity     BudgetAssignmentIdentity
		wantState    string
		wantSubject  string
		wantIdentity BudgetAssignmentIdentity
	}{
		"an email names an existing user, whatever its case": {
			BudgetAssignmentIdentity{Type: "email", Value: " Alice@ACME.test "}, store.BudgetAssignmentConcrete, alice,
			BudgetAssignmentIdentity{Type: "email", Value: "alice@acme.test"},
		},
		"a sub names an existing user": {
			BudgetAssignmentIdentity{Type: "sub", Value: "sub-bob", Issuer: "https://idp.test"}, store.BudgetAssignmentConcrete, bob,
			BudgetAssignmentIdentity{Type: "sub", Value: "sub-bob", Issuer: "https://idp.test"},
		},
		"an unknown email is pending": {
			BudgetAssignmentIdentity{Type: "email", Value: "new@acme.test"}, store.BudgetAssignmentPending, "",
			BudgetAssignmentIdentity{Type: "email", Value: "new@acme.test"},
		},
		"an unknown sub is pending": {
			BudgetAssignmentIdentity{Type: "sub", Value: "u-8f21", Issuer: "https://idp.test"}, store.BudgetAssignmentPending, "",
			BudgetAssignmentIdentity{Type: "sub", Value: "u-8f21", Issuer: "https://idp.test"},
		},
		"a built-in system user hidden from pickers": {
			BudgetAssignmentIdentity{Type: "system", Value: "eval"}, store.BudgetAssignmentConcrete, "a38ff022-96f3-4a78-a2e5-8a71bfa882ab",
			BudgetAssignmentIdentity{Type: "system", Value: "eval"},
		},
		"a system user named with its prefix": {
			BudgetAssignmentIdentity{Type: "system", Value: "system:channel"}, store.BudgetAssignmentConcrete, "1f8ffb5d-fc1b-47b3-8578-559670c859ae",
			BudgetAssignmentIdentity{Type: "system", Value: "channel"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			created, err := f.assignIdentity(t, c.identity, eng.ID)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if created.Subject.Identity != nil || created.Subject.User != nil && created.Subject.User.Sub != nil {
				t.Errorf("create answered %+v; a write shows no more than an unexpanded read", created.Subject)
			}
			resp, err := f.svc.Get(t.Context(), created.ID, true)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if resp.State != c.wantState || resp.Subject.Identity == nil || *resp.Subject.Identity != c.wantIdentity {
				t.Errorf("created %+v identity %+v; want state %s, identity %+v", resp, resp.Subject.Identity, c.wantState, c.wantIdentity)
			}
			switch {
			case c.wantSubject == "" && resp.Subject.ID != nil:
				t.Errorf("pending subject id = %q, want null", *resp.Subject.ID)
			case c.wantSubject != "" && (resp.Subject.ID == nil || *resp.Subject.ID != c.wantSubject):
				t.Errorf("subject id = %v, want %s", resp.Subject.ID, c.wantSubject)
			}
			audit := lastBudgetAudit(t, f.logs, auditActionCreated)
			wantAudit(t, audit, map[string]any{"resourceId": resp.ID, "state": c.wantState, "identityType": c.wantIdentity.Type})
		})
	}

	b, err := f.svc.budgets.GetByID(t.Context(), eng.ID)
	if err != nil || b.AssignedCount != 4 || b.PendingCount != 2 {
		t.Errorf("counts = %+v, %v; want 4 assigned, 2 pending", b, err)
	}
}

func TestBudgetAssignmentService_CreateByIdentityRefusals(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	f.user(t, "sub-jo-1", "jit", nil, new("jo@acme.test"))
	f.user(t, "sub-jo-2", "jit", nil, new("jo@acme.test"))
	assigned := f.user(t, "sub-kim", "jit", nil, new("kim@acme.test"))
	f.mustAssign(t, assigned, eng.ID)
	if _, err := f.assignIdentity(t, BudgetAssignmentIdentity{Type: "email", Value: "pending@acme.test"}, eng.ID); err != nil {
		t.Fatal(err)
	}

	invalid := map[string]struct {
		identity BudgetAssignmentIdentity
		field    string
	}{
		"an email shared by two users":   {BudgetAssignmentIdentity{Type: "email", Value: "jo@acme.test"}, "identity.value"},
		"a sub without an issuer":        {BudgetAssignmentIdentity{Type: "sub", Value: "u-1"}, "identity.issuer"},
		"an issuer on an email":          {BudgetAssignmentIdentity{Type: "email", Value: "x@acme.test", Issuer: "https://idp.test"}, "identity.issuer"},
		"a legacy identity":              {BudgetAssignmentIdentity{Type: "legacy", Value: "x@acme.test"}, "identity.type"},
		"an empty value":                 {BudgetAssignmentIdentity{Type: "email"}, "identity.value"},
		"a system user written as a sub": {BudgetAssignmentIdentity{Type: "sub", Value: "system:default", Issuer: "sam:system"}, "identity.type"},
		"an unknown system user":         {BudgetAssignmentIdentity{Type: "system", Value: "nobody"}, "identity.value"},
		"a bare system prefix":           {BudgetAssignmentIdentity{Type: "system", Value: "system:"}, "identity.value"},
	}
	for name, c := range invalid {
		t.Run(name, func(t *testing.T) {
			_, err := f.assignIdentity(t, c.identity, ops.ID)
			if details := validationDetails(t, err); len(details[c.field]) == 0 {
				t.Errorf("details = %v, want a failure on %s", details, c.field)
			}
		})
	}

	t.Run("a system value without a type reports the type once", func(t *testing.T) {
		_, err := f.assignIdentity(t, BudgetAssignmentIdentity{Value: "system:x"}, ops.ID)
		if got := validationDetails(t, err)["identity.type"]; len(got) != 1 {
			t.Errorf("identity.type = %v, want exactly one message", got)
		}
	})

	conflicts := map[string]BudgetAssignmentIdentity{
		"a user already assigned":     {Type: "email", Value: "kim@acme.test"},
		"an identity already pending": {Type: "email", Value: "pending@acme.test"},
	}
	for name, identity := range conflicts {
		t.Run(name, func(t *testing.T) {
			if _, err := f.assignIdentity(t, identity, ops.ID); !errors.As(err, new(*ConflictError)) {
				t.Errorf("create = %v, want a ConflictError", err)
			}
		})
	}

	t.Run("both a subject id and an identity", func(t *testing.T) {
		_, err := f.svc.Create(t.Context(), CreateBudgetAssignmentRequest{
			SubjectType: store.BudgetSubjectUser, SubjectID: assigned,
			Identity: &BudgetAssignmentIdentity{Type: "email", Value: "x@acme.test"}, BudgetID: ops.ID,
		}, "author")
		if details := validationDetails(t, err); len(details["identity"]) == 0 {
			t.Errorf("details = %v, want a failure on identity", details)
		}
	})
}

func TestBudgetAssignmentService_PendingLifecycle(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	created, err := f.assignIdentity(t, BudgetAssignmentIdentity{Type: "email", Value: "new@acme.test"}, eng.ID)
	if err != nil {
		t.Fatal(err)
	}

	moved, err := f.svc.Update(t.Context(), strings.ToUpper(created.ID), UpdateBudgetAssignmentRequest{BudgetID: ops.ID}, "mover")
	if err != nil || moved.State != store.BudgetAssignmentPending || moved.Budget.ID != ops.ID {
		t.Fatalf("move = %+v, %v; want the pending assignment on Operations", moved, err)
	}
	wantAudit(t, lastBudgetAudit(t, f.logs, auditActionUpdated), map[string]any{
		"resourceId": created.ID, "state": store.BudgetAssignmentPending, "previousBudgetId": eng.ID,
	})

	page, err := f.svc.List(t.Context(), ListBudgetAssignmentsParams{PageNumber: 1, PageSize: 10, State: store.BudgetAssignmentPending})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != created.ID {
		t.Errorf("pending list = %+v, %v", page, err)
	}

	if err := f.svc.Delete(t.Context(), created.ID, "remover"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.svc.Get(t.Context(), created.ID, false); !isNotFound(err) {
		t.Errorf("get after delete = %v, want a NotFoundError", err)
	}
}

func TestBudgetAssignmentService_AssignmentSupersedesAPendingOne(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	pending, err := f.assignIdentity(t, BudgetAssignmentIdentity{Type: "email", Value: "late@acme.test"}, ops.ID)
	if err != nil {
		t.Fatal(err)
	}
	user := f.user(t, "sub-late", "jit", nil, new("late@acme.test"))

	created := f.mustAssign(t, user, eng.ID)
	wantAudit(t, lastBudgetAudit(t, f.logs, auditActionSuperseded), map[string]any{
		"resourceId": pending.ID, "assignmentId": created.ID, "state": store.BudgetAssignmentPending,
	})
	if _, err := f.svc.Get(t.Context(), pending.ID, false); !isNotFound(err) {
		t.Errorf("pending assignment survived: %v", err)
	}
}

func TestBudgetAssignmentService_AuditOmitsIdentityValues(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	const secretish = "canary-person@acme.test"
	created, err := f.assignIdentity(t, BudgetAssignmentIdentity{Type: "email", Value: secretish}, eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.assignIdentity(t, BudgetAssignmentIdentity{Type: "email", Value: secretish}, eng.ID); err == nil {
		t.Fatal("duplicate pending create succeeded")
	}
	if err := f.svc.Delete(t.Context(), created.ID, "remover"); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(f.logs.Bytes(), []byte("canary-person")) {
		t.Errorf("an identity value reached the log:\n%s", f.logs.String())
	}
}

func TestAssignmentIdentityOf(t *testing.T) {
	email := "jo@acme.test"
	cases := map[string]struct {
		a    store.BudgetAssignment
		want *BudgetAssignmentIdentity
	}{
		"provenance wins": {
			store.BudgetAssignment{IdentityType: "sub", IdentityValue: "u-1", IdentityIssuer: "iss", SubjectEmail: &email},
			&BudgetAssignmentIdentity{Type: "sub", Value: "u-1", Issuer: "iss"},
		},
		"a system user by name": {
			store.BudgetAssignment{SubjectIsSystemUser: true, SubjectSub: "system:ci"},
			&BudgetAssignmentIdentity{Type: "system", Value: "ci"},
		},
		"a user by email": {
			store.BudgetAssignment{SubjectEmail: &email, SubjectSub: "u-2", SubjectIssuer: "iss"},
			&BudgetAssignmentIdentity{Type: "email", Value: email},
		},
		"a shared email falls back to the sub": {
			store.BudgetAssignment{SubjectEmail: &email, SubjectEmailShared: true, SubjectSub: "u-2", SubjectIssuer: "iss"},
			&BudgetAssignmentIdentity{Type: "sub", Value: "u-2", Issuer: "iss"},
		},
		"a user row that is gone": {store.BudgetAssignment{}, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := assignmentIdentityOf(&c.a); !reflect.DeepEqual(got, c.want) {
				t.Errorf("identity = %+v, want %+v", got, c.want)
			}
		})
	}
}
