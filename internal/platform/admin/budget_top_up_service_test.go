// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type topUpServiceFixture struct {
	fixture assignmentSvcFixture
	svc     *BudgetTopUpService
	logs    *bytes.Buffer
	clock   time.Time
}

func newTopUpService(t *testing.T) topUpServiceFixture {
	t.Helper()
	f := newAssignmentSvc(t)
	var logs bytes.Buffer
	svc := NewBudgetTopUpService(store.NewBudgetTopUpsRepo(f.db), nil, slog.New(slog.NewJSONHandler(&logs, nil)))
	clock := time.Date(2026, 10, 10, 14, 2, 11, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	s := topUpServiceFixture{fixture: f, svc: svc, logs: &logs, clock: clock}
	s.setAmount(t, store.SeededDefaultUserBudgetID, new(int64(250)))
	s.setAmount(t, store.SeededMeshBudgetID, new(int64(5000)))
	return s
}

func (s topUpServiceFixture) setAmount(t *testing.T, budgetID string, amount *int64) {
	t.Helper()
	if _, err := s.fixture.db.ExecContext(t.Context(), `UPDATE budgets SET amount = ?, warn_percentage = NULL WHERE id = ?`, amount, budgetID); err != nil {
		t.Fatalf("set budget %s amount: %v", budgetID, err)
	}
}

func (s topUpServiceFixture) auditLines(t *testing.T) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.logs.String()), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["resource"] == resourceBudgetTopUp {
			lines = append(lines, entry)
		}
	}
	return lines
}

func TestBudgetTopUpService_AuditsEachChangeWithoutNamingTheSubject(t *testing.T) {
	s := newTopUpService(t)
	raj := s.fixture.user(t, "raj", "jit", new("Raj Patel"), new("raj@acme.test"))

	granted, created, err := s.svc.Grant(t.Context(), CreateBudgetTopUpRequest{SubjectType: "user", SubjectID: raj, Amount: "50"}, "granter")
	if err != nil || !created {
		t.Fatalf("grant = %v, created %v", err, created)
	}
	if _, created, err := s.svc.Grant(t.Context(), CreateBudgetTopUpRequest{SubjectType: "user", SubjectID: raj, Amount: "80"}, "granter"); err != nil || created {
		t.Fatalf("regrant = %v, created %v; want a replacement", err, created)
	}
	if err := s.svc.CancelForSubject(t.Context(), store.BudgetSubjectUser, raj, "canceller"); err != nil {
		t.Fatal(err)
	}

	lines := s.auditLines(t)
	if len(lines) != 3 {
		t.Fatalf("audit lines = %v, want created, updated, deleted", lines)
	}
	for i, want := range []map[string]any{
		{"action": "created", "amount": "50.00", "subjectId": raj},
		{"action": "updated", "amount": "80.00", "previousAmount": "50.00"},
		{"action": "deleted", "amount": "80.00", "resourceId": granted.ID},
	} {
		for key, value := range want {
			if lines[i][key] != value {
				t.Errorf("audit line %d %s = %v, want %v", i, key, lines[i][key], value)
			}
		}
	}
	if strings.Contains(s.logs.String(), "Raj Patel") || strings.Contains(s.logs.String(), "raj@acme.test") {
		t.Errorf("an audit line names the subject: %s", s.logs.String())
	}
}

func TestBudgetTopUpService_AuditsEachRefusal(t *testing.T) {
	t.Run("an unlimited budget", func(t *testing.T) {
		s := newTopUpService(t)
		raj := s.fixture.user(t, "raj", "jit", nil, nil)
		s.setAmount(t, store.SeededDefaultUserBudgetID, nil)
		s.setAmount(t, store.SeededMeshBudgetID, nil)
		for _, req := range []CreateBudgetTopUpRequest{
			{SubjectType: "user", SubjectID: raj, Amount: "50"},
			{SubjectType: "mesh", Amount: "50"},
		} {
			if _, _, err := s.svc.Grant(t.Context(), req, "granter"); !errors.As(err, new(*ConflictError)) {
				t.Errorf("grant to %s = %v, want a conflict", req.SubjectType, err)
			}
		}
		lines := s.auditLines(t)
		if len(lines) != 2 {
			t.Fatalf("audit lines = %v, want a rejection for each refused grant", lines)
		}
		for _, line := range lines {
			if line["action"] != "rejected" || line["reason"] != "budget_unlimited" {
				t.Errorf("audit line = %v, want a budget_unlimited rejection", line)
			}
		}
	})

	t.Run("a grant overtaken by the next period", func(t *testing.T) {
		s := newTopUpService(t)
		raj := s.fixture.user(t, "raj", "jit", nil, nil)
		nextPeriodEnd := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		writtenAfterTheClock := s.clock.UnixMilli() + 1
		if _, err := s.fixture.db.ExecContext(t.Context(),
			`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
			 VALUES (?, 'user', ?, ?, ?, 'other', 'other', ?, ?)`,
			idgen.New(), raj, budgetAmountForTest(t, "70"), nextPeriodEnd, writtenAfterTheClock, writtenAfterTheClock); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.svc.Grant(t.Context(), CreateBudgetTopUpRequest{SubjectType: "user", SubjectID: raj, Amount: "80"}, "granter"); !errors.As(err, new(*ConflictError)) {
			t.Fatalf("grant = %v, want a conflict", err)
		}
		lines := s.auditLines(t)
		if len(lines) != 1 || lines[0]["action"] != "rejected" || lines[0]["reason"] != "period_changed" {
			t.Errorf("audit lines = %v, want one period_changed rejection", lines)
		}
	})
}

func TestBudgetTopUpService_CancelForSubject(t *testing.T) {
	s := newTopUpService(t)
	alice := s.fixture.user(t, "alice", "jit", new("Alice"), nil)
	if err := s.svc.CancelForSubject(t.Context(), store.BudgetSubjectUser, alice, "canceller"); err != nil {
		t.Fatalf("cancel with no top-up = %v, want nothing done", err)
	}
	granted, _, err := s.svc.Grant(t.Context(), CreateBudgetTopUpRequest{SubjectType: "user", SubjectID: alice, Amount: "50"}, "granter")
	if err != nil {
		t.Fatal(err)
	}
	if active, err := s.svc.Active(t.Context(), store.BudgetSubjectUser, alice); err != nil || active == nil || active.ID != granted.ID {
		t.Fatalf("active = %+v, %v; want %s", active, err, granted.ID)
	}

	if err := s.svc.CancelForSubject(t.Context(), store.BudgetSubjectUser, alice, "canceller"); err != nil {
		t.Fatal(err)
	}
	if active, err := s.svc.Active(t.Context(), store.BudgetSubjectUser, alice); err != nil || active != nil {
		t.Errorf("active after cancel = %+v, %v; want none", active, err)
	}
	lines := s.auditLines(t)
	if last := lines[len(lines)-1]; last["action"] != auditActionDeleted || last["resourceId"] != granted.ID || last["actor"] != "canceller" {
		t.Errorf("last audit line = %v, want the cancel of %s", last, granted.ID)
	}
}

func TestBudgetTopUpService_GrantForAMissingUserIsNotFound(t *testing.T) {
	s := newTopUpService(t)
	_, _, err := s.svc.Grant(t.Context(), CreateBudgetTopUpRequest{SubjectType: "user", SubjectID: idgen.New(), Amount: "5"}, "granter")
	if _, ok := errors.AsType[*NotFoundError](err); !ok {
		t.Errorf("err = %v, want not found, which /api/v2 answers 404 for a user deleted while the top-up is granted", err)
	}
}

func TestBudgetAssignmentService_GetSubject(t *testing.T) {
	f := newAssignmentSvc(t)
	team := f.userBudget(t, "Team")
	alice := f.user(t, "alice", "jit", new("Alice"), new("alice@example.com"))
	assignment := f.mustAssign(t, alice, team.ID)

	got, err := f.svc.GetSubject(t.Context(), alice)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != alice || got.SubjectType != store.BudgetSubjectUser || got.User == nil || got.Budget == nil ||
		got.Budget.ID != team.ID || got.Budget.AssignmentID == nil || *got.Budget.AssignmentID != assignment.ID {
		t.Errorf("alice = %+v", got)
	}

	mesh, err := f.svc.GetSubject(t.Context(), store.BudgetMeshSubjectRef)
	if err != nil || mesh.ID != store.BudgetMeshSubjectRef || mesh.SubjectType != store.BudgetSubjectMesh || mesh.User != nil ||
		mesh.Budget == nil || mesh.Budget.ID != store.SeededMeshBudgetID {
		t.Errorf("mesh = %+v, %v", mesh, err)
	}

	if hidden := rbac.UserListingHiddenIDs(); len(hidden) > 0 {
		if got, err := f.svc.GetSubject(t.Context(), hidden[0]); err != nil || got.ID != hidden[0] {
			t.Errorf("a hidden user = %+v, %v; want it found when named", got, err)
		}
	}
	var notFound *NotFoundError
	for _, id := range []string{"0190a5f0-0000-7000-8000-000000000000", "nobody"} {
		if _, err := f.svc.GetSubject(t.Context(), id); !errors.As(err, &notFound) {
			t.Errorf("subject %s = %v, want not found", id, err)
		}
	}
}

func TestBudgetConflicts_KeepV1Sentences(t *testing.T) {
	err := newBudgetConflict(BudgetConflictNameTaken, &ConflictError{Message: `A budget named "x" already exists.`, LogMessage: "taken"})
	conflict, ok := errors.AsType[*ConflictError](err)
	if !ok || conflict.Message != `A budget named "x" already exists.` || err.Error() != "taken" {
		t.Errorf("wrapped conflict = %v (%+v)", err, conflict)
	}
	if _, ok := BudgetConflictReasonOf(&ConflictError{Message: "other"}); ok {
		t.Errorf("an unwrapped conflict has a reason")
	}
}

func budgetAmountForTest(t *testing.T, decimal string) int64 {
	t.Helper()
	amount, err := budget.ParseAmount(decimal)
	if err != nil {
		t.Fatal(err)
	}
	return amount
}
