// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) budgetAudits(t *testing.T, action string) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.buf.String()), "\n") {
		var rec map[string]any
		if line == "" || json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec["event"] == "budget_mutation" && rec["action"] == action {
			out = append(out, rec)
		}
	}
	return out
}

func (l *lockedLog) omits(t *testing.T, values ...string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	logged := strings.ToLower(l.buf.String())
	for _, v := range values {
		if strings.Contains(logged, strings.ToLower(v)) {
			t.Errorf("logs contain %q; an identity value must not reach a log", v)
		}
	}
}

type budgetBindFixture struct {
	assignments *store.BudgetAssignmentsRepo
	budgets     *store.BudgetsRepo
	users       *store.UsersRepo
	logs        *lockedLog
	changed     budgetsChangedRig
}

func startBudgetBindingHandler(t *testing.T, ns string) (budgetBindFixture, func(req userUpsertRequest)) {
	t.Helper()
	db := store.TestDB(t)
	f := budgetBindFixture{
		assignments: store.NewBudgetAssignmentsRepo(db), budgets: store.NewBudgetsRepo(db),
		users: store.NewUsersRepo(db), logs: &lockedLog{},
	}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) }) //nolint:contextcheck
	f.changed = listenForBudgetsChanged(t, brk, ns)
	h := NewUserUpsertHandler(f.users, brk, UserUpsertConfig{Namespace: ns}, slog.New(slog.NewJSONHandler(f.logs, nil)))
	h.SetBudgetBinder(f.assignments)
	if err := h.Start(t.Context()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)
	signIn := func(req userUpsertRequest) {
		t.Helper()
		if resp := upsertRoundTrip(t, brk, ns, "test-gw", req); resp.Error != nil {
			t.Fatalf("upsert %s: %+v", req.Sub, resp.Error)
		}
	}
	return f, signIn
}

func (f budgetBindFixture) budget(t *testing.T, name string) *store.Budget {
	t.Helper()
	b := &store.Budget{Name: name, SubjectType: store.BudgetSubjectUser, CreatedBy: "c", UpdatedBy: "c"}
	if err := f.budgets.Create(t.Context(), b); err != nil {
		t.Fatalf("create budget %q: %v", name, err)
	}
	return b
}

func (f budgetBindFixture) pend(t *testing.T, identityType, value, issuer, budgetID string) *store.BudgetPendingAssignment {
	t.Helper()
	p := &store.BudgetPendingAssignment{
		SubjectType: store.BudgetSubjectUser, IdentityType: identityType, IdentityValue: value, Issuer: issuer,
		BudgetID: budgetID, CreatedBy: "author", UpdatedBy: "author",
	}
	if err := f.assignments.CreatePending(t.Context(), p); err != nil {
		t.Fatalf("pend %q: %v", value, err)
	}
	return p
}

func (f budgetBindFixture) assignmentOf(t *testing.T, issuer, sub string) (userID, budgetID string) {
	t.Helper()
	u, _, err := f.users.GetByIssuerSub(t.Context(), issuer, sub)
	if err != nil || u == nil {
		t.Fatalf("user %s: %v, %v", sub, u, err)
	}
	as, err := f.assignments.List(t.Context(), store.BudgetAssignmentFilter{SubjectRef: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(as) == 0 {
		return u.ID, ""
	}
	return u.ID, as[0].BudgetID
}

func TestUserUpsertHandler_BindsAPendingBudgetAtFirstSignIn(t *testing.T) {
	f, signIn := startBudgetBindingHandler(t, "budget-bind")
	eng := f.budget(t, "Engineering")
	p := f.pend(t, store.BudgetIdentityEmail, "new@example.com", "", eng.ID)

	signIn(userUpsertRequest{Issuer: "iss", Sub: "sub-new", Email: "New@Example.com"})
	f.changed.expect(t, 1, "binding a pending budget")

	userID, budgetID := f.assignmentOf(t, "iss", "sub-new")
	if budgetID != eng.ID {
		t.Fatalf("assigned budget = %q, want %s", budgetID, eng.ID)
	}
	audits := f.logs.budgetAudits(t, auditActionMaterialized)
	if len(audits) != 1 || audits[0]["pendingId"] != p.ID || audits[0]["subjectId"] != userID ||
		audits[0]["budgetId"] != eng.ID || audits[0]["identityType"] != store.BudgetIdentityEmail {
		t.Errorf("materialized audits = %v", audits)
	}

	signIn(userUpsertRequest{Issuer: "iss", Sub: "sub-new", Email: "new@example.com"})
	f.changed.expect(t, 0, "a sign-in that binds nothing")
	if n := len(f.logs.budgetAudits(t, auditActionMaterialized)); n != 1 {
		t.Errorf("a later sign-in audited %d bindings, want still 1", n)
	}
	f.logs.omits(t, "new@example.com")
}

func TestUserUpsertHandler_AnExistingBudgetSurvivesSignIn(t *testing.T) {
	f, signIn := startBudgetBindingHandler(t, "budget-bind-existing")
	eng := f.budget(t, "Engineering")
	ops := f.budget(t, "Operations")

	signIn(userUpsertRequest{Issuer: "iss", Sub: "sub-old", Email: "old@example.com"})
	userID, _ := f.assignmentOf(t, "iss", "sub-old")
	if _, err := f.assignments.Create(t.Context(), &store.BudgetAssignment{
		SubjectType: store.BudgetSubjectUser, SubjectRef: userID, BudgetID: eng.ID, CreatedBy: "x", UpdatedBy: "x",
	}); err != nil {
		t.Fatal(err)
	}
	p := f.pend(t, store.BudgetIdentitySub, "sub-old", "iss", ops.ID)

	signIn(userUpsertRequest{Issuer: "iss", Sub: "sub-old", Email: "old@example.com"})

	if _, budgetID := f.assignmentOf(t, "iss", "sub-old"); budgetID != eng.ID {
		t.Errorf("assigned budget = %q, want the existing %s", budgetID, eng.ID)
	}
	audits := f.logs.budgetAudits(t, auditActionSuperseded)
	if len(audits) != 1 || audits[0]["resourceId"] != p.ID || audits[0]["subjectId"] != userID {
		t.Errorf("superseded audits = %v", audits)
	}
	f.logs.omits(t, "old@example.com", "sub-old")
}
