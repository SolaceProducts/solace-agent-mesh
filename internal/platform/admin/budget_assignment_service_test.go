// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type assignmentSvcFixture struct {
	svc     *BudgetAssignmentService
	db      *store.DB
	budgets *store.BudgetsRepo
	logs    *bytes.Buffer
}

func newAssignmentSvc(t *testing.T) assignmentSvcFixture {
	t.Helper()
	db := testOpenDB(t)
	var logs bytes.Buffer
	budgets := store.NewBudgetsRepo(db)
	svc := NewBudgetAssignmentService(store.NewBudgetAssignmentsRepo(db), budgets, store.NewUsersRepo(db), store.NewBudgetTopUpsRepo(db), nil,
		slog.New(slog.NewJSONHandler(&logs, nil)))
	return assignmentSvcFixture{svc: svc, db: db, budgets: budgets, logs: &logs}
}

func (f assignmentSvcFixture) userBudget(t *testing.T, name string) *store.Budget {
	t.Helper()
	b := &store.Budget{Name: name, SubjectType: store.BudgetSubjectUser, Amount: new(int64(100)), CreatedBy: "c", UpdatedBy: "c"}
	if err := f.budgets.Create(t.Context(), b); err != nil {
		t.Fatalf("create budget %q: %v", name, err)
	}
	return b
}

func (f assignmentSvcFixture) user(t *testing.T, sub, provisionedVia string, displayName, email *string) string {
	t.Helper()
	u := &store.User{
		Issuer: "https://idp.test", Sub: sub, ProvisionedVia: provisionedVia,
		DisplayName: displayName, Email: email, CreatedBy: "test", UpdatedBy: "test",
	}
	if provisionedVia == rbac.ProvisionedViaSystem {
		u.Issuer = rbac.SystemUserIssuer
	}
	if err := store.NewUsersRepo(f.db).UpsertUser(t.Context(), u, nil); err != nil {
		t.Fatalf("seed user %q: %v", sub, err)
	}
	return u.ID
}

func (f assignmentSvcFixture) mustAssign(t *testing.T, ref, budgetID string) *BudgetAssignmentResponse {
	t.Helper()
	resp, err := f.svc.Create(t.Context(), CreateBudgetAssignmentRequest{
		SubjectType: store.BudgetSubjectUser, SubjectID: ref, BudgetID: budgetID,
	}, "assigner")
	if err != nil {
		t.Fatalf("assign %s to %s: %v", ref, budgetID, err)
	}
	return resp
}

func (f assignmentSvcFixture) assignmentCount(t *testing.T) int64 {
	t.Helper()
	n, err := store.NewBudgetAssignmentsRepo(f.db).Count(t.Context(), store.BudgetAssignmentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func assignmentAudits(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range budgetAudits(t, logs) {
		if rec["resource"] == resourceBudgetAssignment {
			out = append(out, rec)
		}
	}
	return out
}

func wantAudit(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("audit %s = %v, want %v (line %v)", k, got[k], v, got)
		}
	}
}

func TestBudgetAssignmentService_Create(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	alice := f.user(t, "alice", "jit", new("Alice Smith"), new("alice@acme.test"))

	resp, err := f.svc.Create(t.Context(), CreateBudgetAssignmentRequest{
		SubjectType: store.BudgetSubjectUser, SubjectID: strings.ToUpper(alice), BudgetID: strings.ToUpper(eng.ID),
	}, "admin@acme.test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if *resp.Subject.ID != alice || resp.Budget.ID != eng.ID {
		t.Errorf("stored ref %s budget %s, want the canonical %s and %s", *resp.Subject.ID, resp.Budget.ID, alice, eng.ID)
	}
	if resp.Subject.DisplayName == nil || *resp.Subject.DisplayName != "Alice Smith" || resp.Budget.Name != "Engineering" {
		t.Errorf("joined names = %v, %q; want Alice Smith, Engineering", resp.Subject.DisplayName, resp.Budget.Name)
	}
	if resp.CreatedBy != "admin@acme.test" || resp.UpdatedBy != "admin@acme.test" || resp.ID == "" {
		t.Errorf("response = %+v", resp)
	}

	wantAudit(t, lastBudgetAudit(t, f.logs, auditActionCreated), map[string]any{
		"resource": resourceBudgetAssignment, "resourceId": resp.ID, "actor": "admin@acme.test",
		"subjectType": store.BudgetSubjectUser, "subjectId": alice, "budgetId": eng.ID,
	})
}

func TestBudgetAssignmentService_CreateAssignsAnyUser(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	ctx := t.Context()
	eng := f.userBudget(t, "Engineering")
	users := store.NewUsersRepo(f.db)

	builtin, _, err := users.GetByIssuerSub(ctx, rbac.SystemUserIssuer, rbac.DefaultSystemUserSub)
	if err != nil || builtin == nil {
		t.Fatalf("seeded system:default: %v, %v", builtin, err)
	}
	inactive := f.user(t, "gone", "jit", new("Gone"), nil)
	if _, err := f.db.ExecContext(ctx, `UPDATE users SET active = 0 WHERE id = ?`, inactive); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, rbac.EvalSystemUserID); err != nil {
		t.Fatal(err)
	}
	shadow := f.user(t, rbac.EvalSystemUserSub, rbac.ProvisionedViaSystem, new("Eval"), nil)
	if shadow == rbac.EvalSystemUserID {
		t.Fatal("the shadow row reused the seeded id; the test proves nothing")
	}

	for name, ref := range map[string]string{
		"scim user":           f.user(t, "scim-1", "scim", new("Sam Scim"), nil),
		"custom system user":  f.user(t, "system:ci-bot", rbac.ProvisionedViaSystem, new("CI bot"), nil),
		"seeded built-in":     builtin.ID,
		"inactive user":       inactive,
		"shadow built-in row": shadow,
	} {
		t.Run(name, func(t *testing.T) {
			if resp := f.mustAssign(t, ref, eng.ID); *resp.Subject.ID != ref {
				t.Errorf("stored %s, want %s", *resp.Subject.ID, ref)
			}
		})
	}
}

func TestBudgetAssignmentService_CreateRefusals(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	alice := f.user(t, "alice", "jit", new("Alice"), nil)

	cases := []struct {
		name string
		req  CreateBudgetAssignmentRequest
		want map[string][]string
	}{
		{"no subject type", CreateBudgetAssignmentRequest{SubjectID: alice, BudgetID: eng.ID},
			map[string][]string{"subjectType": {"is required"}}},
		{"mesh subject type", CreateBudgetAssignmentRequest{SubjectType: "mesh", SubjectID: alice, BudgetID: eng.ID},
			map[string][]string{"subjectType": {"the mesh budget applies to the whole deployment and has no assignments; must be: user"}}},
		{"agent subject type", CreateBudgetAssignmentRequest{SubjectType: "agent", SubjectID: alice, BudgetID: eng.ID},
			map[string][]string{"subjectType": {"agent assignments are not supported yet; must be: user"}}},
		{"unknown subject type", CreateBudgetAssignmentRequest{SubjectType: "team", SubjectID: alice, BudgetID: eng.ID},
			map[string][]string{"subjectType": {"must be: user"}}},
		{"no subject ref", CreateBudgetAssignmentRequest{SubjectType: "user", BudgetID: eng.ID},
			map[string][]string{"subjectId": {"is required, unless identity is given"}}},
		{"a system user's name", CreateBudgetAssignmentRequest{SubjectType: "user", SubjectID: "system:default", BudgetID: eng.ID},
			map[string][]string{"subjectId": {"must be a user id"}}},
		{"an email", CreateBudgetAssignmentRequest{SubjectType: "user", SubjectID: "alice@acme.test", BudgetID: eng.ID},
			map[string][]string{"subjectId": {"must be a user id"}}},
		{"no such user", CreateBudgetAssignmentRequest{SubjectType: "user", SubjectID: idgen.New(), BudgetID: eng.ID},
			map[string][]string{"subjectId": {"no user with this id"}}},
		{"no budget id", CreateBudgetAssignmentRequest{SubjectType: "user", SubjectID: alice},
			map[string][]string{"budgetId": {"is required"}}},
		{"no such budget", CreateBudgetAssignmentRequest{SubjectType: "user", SubjectID: alice, BudgetID: idgen.New()},
			map[string][]string{"budgetId": {"no budget with this id"}}},
		{"budget id not a UUID", CreateBudgetAssignmentRequest{SubjectType: "user", SubjectID: alice, BudgetID: "nope"},
			map[string][]string{"budgetId": {"no budget with this id"}}},
		{"the mesh budget", CreateBudgetAssignmentRequest{SubjectType: "user", SubjectID: alice, BudgetID: store.SeededMeshBudgetID},
			map[string][]string{"budgetId": {"is a mesh budget; a user can only be assigned a user budget"}}},
		{"every field wrong at once", CreateBudgetAssignmentRequest{SubjectID: "nope", BudgetID: idgen.New()},
			map[string][]string{
				"subjectType": {"is required"},
				"subjectId":   {"must be a user id"},
				"budgetId":    {"no budget with this id"},
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.svc.Create(t.Context(), c.req, "assigner")
			if got := validationDetails(t, err); !maps.EqualFunc(got, c.want, slices.Equal) {
				t.Errorf("details = %v, want %v", got, c.want)
			}
		})
	}
	if n := f.assignmentCount(t); n != 0 {
		t.Errorf("refused creates wrote %d assignments", n)
	}
}

func TestBudgetAssignmentService_CreateOnTheDefault(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	alice := f.user(t, "alice", "jit", nil, nil)
	if resp := f.mustAssign(t, alice, store.SeededDefaultUserBudgetID); resp.Budget.ID != store.SeededDefaultUserBudgetID {
		t.Errorf("assigned to %s, want the default", resp.Budget.ID)
	}
}

func TestBudgetAssignmentService_CreateDuplicate(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	alice := f.user(t, "alice", "jit", nil, nil)
	first := f.mustAssign(t, alice, eng.ID)

	_, err := f.svc.Create(t.Context(), CreateBudgetAssignmentRequest{
		SubjectType: store.BudgetSubjectUser, SubjectID: strings.ToUpper(alice), BudgetID: ops.ID,
	}, "second")
	msg := conflictMessage(t, err)
	want := "This user already has a budget assignment."
	if msg != want {
		t.Errorf("conflict = %q\nwant       %q", msg, want)
	}
	wantAudit(t, lastBudgetAudit(t, f.logs, auditActionRejected), map[string]any{
		"resource": resourceBudgetAssignment, "resourceId": first.ID, "actor": "second",
		"reason": "subject_assigned", "subjectId": alice, "budgetId": ops.ID,
	})
	if got, _ := f.svc.Get(t.Context(), first.ID, false); got == nil || got.Budget.ID != eng.ID {
		t.Errorf("the refused create changed the assignment: %+v", got)
	}
	if n := f.assignmentCount(t); n != 1 {
		t.Errorf("assignments = %d, want 1", n)
	}
}

func TestBudgetAssignmentService_Get(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	a := f.mustAssign(t, f.user(t, "alice", "jit", nil, nil), eng.ID)

	got, err := f.svc.Get(t.Context(), strings.ToUpper(a.ID), false)
	if err != nil || !sameAssignment(got, a) {
		t.Errorf("get = %+v, %v; want %+v", got, err, a)
	}
	for _, id := range []string{idgen.New(), "nope"} {
		if _, err := f.svc.Get(t.Context(), id, false); !isNotFound(err) {
			t.Errorf("Get(%q) = %v, want a NotFoundError", id, err)
		}
	}
}

func sameAssignment(a, b *BudgetAssignmentResponse) bool {
	return reflect.DeepEqual(a, b)
}

func TestBudgetAssignmentService_List(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	ctx := t.Context()
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	alice := f.user(t, "alice", "jit", new("Alice Smith"), new("alice@acme.test"))
	bob := f.user(t, "bob", "jit", new("Bob Jones"), new("bjones@corp.test"))
	carol := f.user(t, "carol", "jit", new("Carol"), new("carol@acme.test"))
	f.mustAssign(t, alice, eng.ID)
	f.mustAssign(t, bob, eng.ID)
	f.mustAssign(t, carol, ops.ID)

	refs := func(p ListBudgetAssignmentsParams) ([]string, int) {
		t.Helper()
		if p.PageNumber == 0 {
			p.PageNumber, p.PageSize = 1, 20
		}
		resp, err := f.svc.List(ctx, p)
		if err != nil {
			t.Fatalf("list %+v: %v", p, err)
		}
		out := make([]string, len(resp.Data))
		for i, a := range resp.Data {
			out[i] = *a.Subject.ID
		}
		return out, resp.Meta.Pagination.Count
	}

	cases := []struct {
		name string
		p    ListBudgetAssignmentsParams
		want []string
	}{
		{"all", ListBudgetAssignmentsParams{}, []string{alice, bob, carol}},
		{"by budget", ListBudgetAssignmentsParams{BudgetID: eng.ID}, []string{alice, bob}},
		{"by subject type", ListBudgetAssignmentsParams{SubjectType: "user"}, []string{alice, bob, carol}},
		{"by subject, upper case", ListBudgetAssignmentsParams{SubjectID: strings.ToUpper(bob)}, []string{bob}},
		{"search a name", ListBudgetAssignmentsParams{Search: "SMITH"}, []string{alice}},
		{"search an email", ListBudgetAssignmentsParams{Search: "@corp"}, []string{bob}},
		{"search within a budget", ListBudgetAssignmentsParams{BudgetID: eng.ID, Search: "acme"}, []string{alice}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, count := refs(c.p)
			if !slices.Equal(got, c.want) || count != len(c.want) {
				t.Errorf("refs = %v (count %d), want %v", got, count, c.want)
			}
		})
	}

	t.Run("paged", func(t *testing.T) {
		got, count := refs(ListBudgetAssignmentsParams{PageNumber: 2, PageSize: 2})
		if !slices.Equal(got, []string{carol}) || count != 3 {
			t.Errorf("page 2 = %v (count %d), want [carol] of 3", got, count)
		}
	})

	for _, c := range []struct {
		name string
		p    ListBudgetAssignmentsParams
		want map[string][]string
	}{
		{"subject ref not an id", ListBudgetAssignmentsParams{SubjectID: "system:default"}, map[string][]string{"subjectId": {"must be a user id"}}},
		{"mesh subject type", ListBudgetAssignmentsParams{SubjectType: "mesh"},
			map[string][]string{"subjectType": {"the mesh budget applies to the whole deployment and has no assignments; must be: user"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.svc.List(ctx, ListBudgetAssignmentsParams{PageNumber: 1, PageSize: 20, SubjectID: c.p.SubjectID, SubjectType: c.p.SubjectType})
			if got := validationDetails(t, err); !maps.EqualFunc(got, c.want, slices.Equal) {
				t.Errorf("details = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBudgetAssignmentService_Update(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	ctx := t.Context()
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	alice := f.user(t, "alice", "jit", nil, nil)
	a := f.mustAssign(t, alice, eng.ID)

	moved, err := f.svc.Update(ctx, strings.ToUpper(a.ID), UpdateBudgetAssignmentRequest{BudgetID: strings.ToUpper(ops.ID)}, "mover")
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if moved.Budget.ID != ops.ID || moved.Budget.Name != "Operations" || moved.UpdatedBy != "mover" || *moved.Subject.ID != alice {
		t.Errorf("after move: %+v", moved)
	}
	wantAudit(t, lastBudgetAudit(t, f.logs, auditActionUpdated), map[string]any{
		"resource": resourceBudgetAssignment, "resourceId": a.ID, "actor": "mover",
		"subjectId": alice, "budgetId": ops.ID, "previousBudgetId": eng.ID,
	})

	t.Run("the same budget writes and audits nothing", func(t *testing.T) {
		before := len(assignmentAudits(t, f.logs))
		same, err := f.svc.Update(ctx, a.ID, UpdateBudgetAssignmentRequest{BudgetID: ops.ID}, "idle")
		if err != nil || same.UpdatedBy != "mover" || same.UpdatedTime != moved.UpdatedTime {
			t.Errorf("no-op move = %+v, %v; want it unchanged", same, err)
		}
		if after := len(assignmentAudits(t, f.logs)); after != before {
			t.Errorf("no-op move wrote %d audit lines", after-before)
		}
	})

	t.Run("onto the default", func(t *testing.T) {
		got, err := f.svc.Update(ctx, a.ID, UpdateBudgetAssignmentRequest{BudgetID: store.SeededDefaultUserBudgetID}, "mover")
		if err != nil || got.Budget.ID != store.SeededDefaultUserBudgetID {
			t.Errorf("move onto the default = %+v, %v", got, err)
		}
	})

	for _, c := range []struct {
		name, budgetID string
		want           map[string][]string
	}{
		{"no budget id", "", map[string][]string{"budgetId": {"is required, unless identity is given"}}},
		{"no such budget", idgen.New(), map[string][]string{"budgetId": {"no budget with this id"}}},
		{"the mesh budget", store.SeededMeshBudgetID, map[string][]string{"budgetId": {"is a mesh budget; a user can only be assigned a user budget"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.svc.Update(ctx, a.ID, UpdateBudgetAssignmentRequest{BudgetID: c.budgetID}, "mover")
			if got := validationDetails(t, err); !maps.EqualFunc(got, c.want, slices.Equal) {
				t.Errorf("details = %v, want %v", got, c.want)
			}
		})
	}

	for _, id := range []string{idgen.New(), "nope"} {
		if _, err := f.svc.Update(ctx, id, UpdateBudgetAssignmentRequest{BudgetID: eng.ID}, "mover"); !isNotFound(err) {
			t.Errorf("Update(%q) = %v, want a NotFoundError", id, err)
		}
	}
}

func TestBudgetAssignmentService_Delete(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	ctx := t.Context()
	eng := f.userBudget(t, "Engineering")
	alice := f.user(t, "alice", "jit", nil, nil)
	a := f.mustAssign(t, alice, eng.ID)
	kept := f.mustAssign(t, f.user(t, "bob", "jit", nil, nil), eng.ID)

	if err := f.svc.Delete(ctx, strings.ToUpper(a.ID), "remover"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	wantAudit(t, lastBudgetAudit(t, f.logs, auditActionDeleted), map[string]any{
		"resource": resourceBudgetAssignment, "resourceId": a.ID, "actor": "remover",
		"subjectId": alice, "budgetId": eng.ID,
	})
	if _, err := f.svc.Get(ctx, a.ID, false); !isNotFound(err) {
		t.Errorf("after delete, Get = %v; want a NotFoundError", err)
	}
	if _, err := f.svc.Get(ctx, kept.ID, false); err != nil {
		t.Errorf("delete removed another assignment: %v", err)
	}
	for _, id := range []string{a.ID, idgen.New(), "nope"} {
		if err := f.svc.Delete(ctx, id, "remover"); !isNotFound(err) {
			t.Errorf("Delete(%q) = %v, want a NotFoundError", id, err)
		}
	}
}

func TestBudgetAssignmentService_LogsOmitNames(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	ctx := t.Context()
	const canary = "canary-Ω"
	first := f.userBudget(t, canary+"-budget")
	second := f.userBudget(t, canary+"-other")
	ref := f.user(t, "canary", "jit", new(canary+"-user"), new(canary+"@acme.test"))

	a := f.mustAssign(t, ref, first.ID)
	_, dupErr := f.svc.Create(ctx, CreateBudgetAssignmentRequest{SubjectType: "user", SubjectID: ref, BudgetID: second.ID}, "x")
	if strings.Contains(conflictMessage(t, dupErr), canary) {
		t.Errorf("the duplicate's message names something: %v", dupErr)
	}
	if _, err := f.svc.Update(ctx, a.ID, UpdateBudgetAssignmentRequest{BudgetID: second.ID}, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.List(ctx, ListBudgetAssignmentsParams{PageNumber: 1, PageSize: 20, Search: canary}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, a.ID, "x"); err != nil {
		t.Fatal(err)
	}
	f.mustAssign(t, ref, first.ID)
	moved, err := f.svc.UpdateAssignments(ctx, second.ID, UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: []string{ref}}, "x")
	if err != nil || len(moved.Moved) != 1 || !strings.Contains(moved.Moved[0].FromBudget.Name, canary) {
		t.Fatalf("bulk move = %+v, %v", moved, err)
	}

	if strings.Contains(f.logs.String(), canary) {
		t.Errorf("a name reached the log:\n%s", f.logs.String())
	}
	if n := len(assignmentAudits(t, f.logs)); n != 6 {
		t.Errorf("audit lines = %d, want 6 (created, rejected, updated, deleted, created, moved)", n)
	}
}

func moveUnderOpenTx(t *testing.T, db *store.DB, assignmentID, budgetID string) (commit func()) {
	t.Helper()
	tx, err := db.BeginTx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(t.Context(), `UPDATE budget_assignments SET budget_id = ? WHERE id = ?`, budgetID, assignmentID); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBudgetAssignmentService_AuditsTheLockedBudget(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"move", "delete"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			f := newAssignmentSvc(t)
			if f.db.Dialect() != "postgres" {
				t.Skip("SQLite serialises writes on one connection")
			}
			eng := f.userBudget(t, "Engineering")
			ops := f.userBudget(t, "Operations")
			sales := f.userBudget(t, "Sales")
			a := f.mustAssign(t, f.user(t, "alice", "jit", nil, nil), eng.ID)
			commit := moveUnderOpenTx(t, f.db, a.ID, ops.ID)

			done := make(chan error, 1)
			go func() {
				if op == "move" {
					_, err := f.svc.Update(context.Background(), a.ID, UpdateBudgetAssignmentRequest{BudgetID: sales.ID}, "racer")
					done <- err
					return
				}
				done <- f.svc.Delete(context.Background(), a.ID, "racer")
			}()
			dbtest.WaitForLockWait(t, f.db, "SELECT budget_id FROM budget_assignments")
			commit()
			if err := <-done; err != nil {
				t.Fatalf("%s racing a move: %v", op, err)
			}

			if op == "move" {
				wantAudit(t, lastBudgetAudit(t, f.logs, auditActionUpdated), map[string]any{"previousBudgetId": ops.ID, "budgetId": sales.ID})
				return
			}
			wantAudit(t, lastBudgetAudit(t, f.logs, auditActionDeleted), map[string]any{"budgetId": ops.ID})
		})
	}
}

func (f assignmentSvcFixture) budgetOf(t *testing.T, ref string) string {
	t.Helper()
	resp, err := f.svc.List(t.Context(), ListBudgetAssignmentsParams{PageNumber: 1, PageSize: 20, SubjectID: ref})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) == 0 {
		return ""
	}
	return resp.Data[0].Budget.ID
}

func TestBudgetAssignmentService_UpdateAssignments(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	ctx := t.Context()
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	alice := f.user(t, "alice", "jit", nil, nil)
	bob := f.user(t, "bob", "jit", nil, nil)
	carol := f.user(t, "carol", "jit", nil, nil)
	dave := f.user(t, "dave", "jit", nil, nil)
	erin := f.user(t, "erin", "jit", nil, nil)
	bobOnOps := f.mustAssign(t, bob, ops.ID)
	carolOnEng := f.mustAssign(t, carol, eng.ID)
	daveOnEng := f.mustAssign(t, dave, eng.ID)
	erinOnOps := f.mustAssign(t, erin, ops.ID)
	nobody := idgen.New()
	before := len(assignmentAudits(t, f.logs))

	resp, err := f.svc.UpdateAssignments(ctx, eng.ID, UpdateBudgetAssignmentsRequest{
		SubjectType: store.BudgetSubjectUser,
		Add:         []string{strings.ToUpper(alice), bob, dave},
		Remove:      []string{carol, erin, nobody},
	}, "saver")
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	if len(resp.Added) != 1 || resp.Added[0].Subject.ID != alice || resp.Added[0].AssignmentID == "" {
		t.Errorf("added = %+v, want alice", resp.Added)
	}
	wantMoved := []BudgetAssignmentMovedEntry{{
		AssignmentID: bobOnOps.ID, Subject: BudgetAssignmentSubjectRef{ID: bob},
		FromBudget: BudgetAssignmentBudget{ID: ops.ID, Name: "Operations"},
	}}
	if !slices.Equal(resp.Moved, wantMoved) {
		t.Errorf("moved = %+v, want %+v", resp.Moved, wantMoved)
	}
	if !slices.Equal(resp.Removed, []BudgetAssignmentChangeEntry{{AssignmentID: carolOnEng.ID, Subject: BudgetAssignmentSubjectRef{ID: carol}}}) {
		t.Errorf("removed = %+v, want carol", resp.Removed)
	}
	type unchanged struct{ assignment, subject, reason string }
	var gotUnchanged []unchanged
	for _, u := range resp.Unchanged {
		id := ""
		if u.AssignmentID != nil {
			id = *u.AssignmentID
		}
		gotUnchanged = append(gotUnchanged, unchanged{id, u.Subject.ID, u.Reason})
	}
	wantUnchanged := []unchanged{
		{daveOnEng.ID, dave, "already_assigned"},
		{erinOnOps.ID, erin, "not_assigned"},
		{"", nobody, "not_assigned"},
	}
	if !slices.Equal(gotUnchanged, wantUnchanged) {
		t.Errorf("unchanged = %+v, want %+v", gotUnchanged, wantUnchanged)
	}

	for ref, want := range map[string]string{alice: eng.ID, bob: eng.ID, carol: "", dave: eng.ID, erin: ops.ID} {
		if got := f.budgetOf(t, ref); got != want {
			t.Errorf("after the save %s is on %q, want %q", ref, got, want)
		}
	}

	audits := assignmentAudits(t, f.logs)[before:]
	if len(audits) != 3 {
		t.Fatalf("audit lines = %d, want 3 (alice created, bob moved, carol deleted): %v", len(audits), audits)
	}
	changeSet := audits[0]["changeSetId"]
	if s, _ := changeSet.(string); s == "" {
		t.Fatalf("no changeSetId on %v", audits[0])
	}
	for i, want := range []map[string]any{
		{"action": auditActionCreated, "subjectId": alice, "budgetId": eng.ID, "actor": "saver"},
		{"action": auditActionUpdated, "subjectId": bob, "budgetId": eng.ID, "previousBudgetId": ops.ID, "resourceId": bobOnOps.ID},
		{"action": auditActionDeleted, "subjectId": carol, "budgetId": eng.ID, "resourceId": carolOnEng.ID},
	} {
		want["changeSetId"] = changeSet
		wantAudit(t, audits[i], want)
	}

	again, err := f.svc.UpdateAssignments(ctx, eng.ID, UpdateBudgetAssignmentsRequest{SubjectType: "user", Remove: []string{alice}}, "saver")
	if err != nil || len(again.Removed) != 1 {
		t.Fatalf("second save = %+v, %v", again, err)
	}
	if next := lastBudgetAudit(t, f.logs, auditActionDeleted)["changeSetId"]; next == changeSet {
		t.Error("two saves shared a changeSetId")
	}
}

func TestBudgetAssignmentService_UpdateAssignmentsRefusals(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	ctx := t.Context()
	eng := f.userBudget(t, "Engineering")
	alice := f.user(t, "alice", "jit", nil, nil)
	bob := f.user(t, "bob", "jit", nil, nil)
	kept := f.mustAssign(t, bob, eng.ID)
	ghost := idgen.New()
	save := func(budgetID string, req UpdateBudgetAssignmentsRequest) error {
		_, err := f.svc.UpdateAssignments(ctx, budgetID, req, "saver")
		return err
	}

	cases := []struct {
		name string
		req  UpdateBudgetAssignmentsRequest
		want map[string][]string
	}{
		{"no subject type", UpdateBudgetAssignmentsRequest{Add: []string{alice}},
			map[string][]string{"subjectType": {"is required"}}},
		{"mesh subject type", UpdateBudgetAssignmentsRequest{SubjectType: "mesh", Add: []string{alice}},
			map[string][]string{"subjectType": {"the mesh budget applies to the whole deployment and has no assignments; must be: user"}}},
		{"agent subject type", UpdateBudgetAssignmentsRequest{SubjectType: "agent", Add: []string{alice}},
			map[string][]string{"subjectType": {"agent assignments are not supported yet; must be: user"}}},
		{"ids that are not ids", UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: []string{alice, "system:default"}, Remove: []string{"bob@acme.test"}},
			map[string][]string{"add[1]": {"must be a user id"}, "remove[0]": {"must be a user id"}}},
		{"a repeat, by case", UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: []string{alice, strings.ToUpper(alice)}},
			map[string][]string{"add[1]": {"repeats add[0]"}}},
		{"a repeated remove", UpdateBudgetAssignmentsRequest{SubjectType: "user", Remove: []string{bob, bob}},
			map[string][]string{"remove[1]": {"repeats remove[0]"}}},
		{"in both lists", UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: []string{ghost, alice}, Remove: []string{alice}},
			map[string][]string{"remove[0]": {"is also in add[1]"}}},
		{"a user that does not exist", UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: []string{alice, ghost}, Remove: []string{bob}},
			map[string][]string{"add[1]": {"no user with this id"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validationDetails(t, save(eng.ID, c.req)); !maps.EqualFunc(got, c.want, slices.Equal) {
				t.Errorf("details = %v, want %v", got, c.want)
			}
		})
	}

	t.Run("the mesh budget", func(t *testing.T) {
		got := validationDetails(t, save(store.SeededMeshBudgetID, UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: []string{alice}}))
		want := map[string][]string{"subjectType": {"the budget is a mesh budget; a user can only be assigned a user budget"}}
		if !maps.EqualFunc(got, want, slices.Equal) {
			t.Errorf("details = %v, want %v", got, want)
		}
	})
	for name, budgetID := range map[string]string{"no such budget": idgen.New(), "budget id not a UUID": "nope"} {
		t.Run(name, func(t *testing.T) {
			if err := save(budgetID, UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: []string{alice}}); !isNotFound(err) {
				t.Errorf("save = %v, want a NotFoundError", err)
			}
		})
	}

	ids := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = idgen.New()
		}
		return out
	}
	t.Run("over the cap", func(t *testing.T) {
		err := save(eng.ID, UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: ids(600), Remove: ids(401)})
		v, ok := errors.AsType[*ValidationError](err)
		if !ok || v.Message != "A save can change at most 1000 assignments; this one has 1001." {
			t.Errorf("save of 1001 = %v", err)
		}
	})
	t.Run("at the cap", func(t *testing.T) {
		details := validationDetails(t, save(eng.ID, UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: ids(600), Remove: ids(400)}))
		if len(details) != 600 || len(details["add[599]"]) != 1 {
			t.Errorf("save of 1000 refused with %d details, want 600 unknown users", len(details))
		}
	})

	if got := f.budgetOf(t, alice); got != "" {
		t.Errorf("a refused save assigned alice to %s", got)
	}
	if got, _ := f.svc.Get(ctx, kept.ID, false); got == nil {
		t.Error("a refused save removed bob")
	}
	if n := len(assignmentAudits(t, f.logs)); n != 1 {
		t.Errorf("refused saves wrote %d audit lines beyond the setup's", n-1)
	}
}

func TestBudgetAssignmentService_UpdateAssignmentsEmpty(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	eng := f.userBudget(t, "Engineering")
	resp, err := f.svc.UpdateAssignments(t.Context(), eng.ID, UpdateBudgetAssignmentsRequest{SubjectType: "user"}, "saver")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(resp)
	if string(encoded) != `{"added":[],"moved":[],"removed":[],"unchanged":[]}` {
		t.Errorf("empty save = %s, want four empty lists", encoded)
	}
	if n := len(assignmentAudits(t, f.logs)); n != 0 {
		t.Errorf("an empty save wrote %d audit lines", n)
	}
}

func TestBudgetAssignmentService_UpdateAssignmentsOnTheDefault(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	ctx := t.Context()
	alice := f.user(t, "alice", "jit", nil, nil)
	pinned, err := f.svc.UpdateAssignments(ctx, store.SeededDefaultUserBudgetID, UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: []string{alice}}, "saver")
	if err != nil || len(pinned.Added) != 1 || f.budgetOf(t, alice) != store.SeededDefaultUserBudgetID {
		t.Fatalf("pin = %+v, %v", pinned, err)
	}
	unpinned, err := f.svc.UpdateAssignments(ctx, store.SeededDefaultUserBudgetID, UpdateBudgetAssignmentsRequest{SubjectType: "user", Remove: []string{alice}}, "saver")
	if err != nil || len(unpinned.Removed) != 1 || f.budgetOf(t, alice) != "" {
		t.Errorf("unpin = %+v, %v", unpinned, err)
	}
}

func TestBudgetAssignmentService_UpdateAssignmentsLosesARaceIsARetryableConflict(t *testing.T) {
	t.Parallel()
	f := newAssignmentSvc(t)
	if f.db.Dialect() != "postgres" {
		t.Skip("SQLite serialises writes on one connection")
	}
	eng := f.userBudget(t, "Engineering")
	ops := f.userBudget(t, "Operations")
	alice := f.user(t, "alice", "jit", nil, nil)
	bob := f.user(t, "bob", "jit", nil, nil)

	tx, err := f.db.BeginTx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(t.Context(),
		`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'user', ?, ?, 'x', 'x', 1, 1)`, idgen.New(), alice, ops.ID); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := f.svc.UpdateAssignments(context.Background(), eng.ID,
			UpdateBudgetAssignmentsRequest{SubjectType: "user", Add: []string{bob, alice}}, "racer")
		done <- err
	}()
	dbtest.WaitForLockWait(t, f.db, "INSERT INTO budget_assignments")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	msg := conflictMessage(t, <-done)
	if msg != "These assignments changed while the save was in progress. Reload them and save again." {
		t.Errorf("conflict = %q", msg)
	}
	if got := f.budgetOf(t, bob); got != "" {
		t.Errorf("the losing save kept bob on %s", got)
	}
}
