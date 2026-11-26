// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const budgetsChangedTestNamespace = "budgets-changed-test"

type budgetsChangedRig struct {
	brk    *broker.DevBroker
	queue  broker.Queue
	signal *BudgetsChangedSignal
}

func newBudgetsChangedRig(t *testing.T) budgetsChangedRig {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) }) //nolint:contextcheck
	return listenForBudgetsChanged(t, brk, budgetsChangedTestNamespace)
}

func listenForBudgetsChanged(t *testing.T, brk *broker.DevBroker, namespace string) budgetsChangedRig {
	t.Helper()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(q, a2a.BudgetsChangedTopic(namespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return budgetsChangedRig{brk: brk, queue: q,
		signal: NewBudgetsChangedSignal(brk, namespace, slog.New(slog.DiscardHandler))}
}

func (r budgetsChangedRig) signals(t *testing.T) int {
	t.Helper()
	n := 0
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		msg, err := r.brk.Receive(ctx, r.queue)
		cancel()
		if err != nil || msg == nil {
			return n
		}
		msg.Ack()
		n++
	}
}

func (r budgetsChangedRig) expect(t *testing.T, want int, what string) {
	t.Helper()
	if got := r.signals(t); got != want {
		t.Errorf("%s sent %d budgets changed signals, want %d", what, got, want)
	}
}

func TestBudgetsChangedSignal_IsNilWithoutABroker(t *testing.T) {
	signal := NewBudgetsChangedSignal(nil, budgetsChangedTestNamespace, nil)
	if signal != nil {
		t.Fatalf("signal = %+v, want nil", signal)
	}
	signal.send(t.Context())
}

func TestBudgetService_SignalsEachCommittedWriteAndNothingElse(t *testing.T) {
	rig := newBudgetsChangedRig(t)
	svc := NewBudgetService(store.NewBudgetsRepo(testOpenDB(t)), rig.signal, slog.New(slog.DiscardHandler))
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateBudgetRequest{Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: new("100"), AmountSet: true}, "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rig.expect(t, 1, "a create")

	if _, err = svc.Create(ctx, CreateBudgetRequest{Name: "x", SubjectType: store.BudgetSubjectUser, AmountSet: true}, "admin"); err == nil {
		t.Fatal("a create with a too-short name succeeded")
	}
	rig.expect(t, 0, "a refused create")

	if _, err = svc.Update(ctx, created.ID, UpdateBudgetRequest{Amount: new("200"), AmountSet: true}, "admin"); err != nil {
		t.Fatalf("update: %v", err)
	}
	rig.expect(t, 1, "an update")
	if _, err = svc.Update(ctx, created.ID, UpdateBudgetRequest{Amount: new("200"), AmountSet: true}, "admin"); err != nil {
		t.Fatalf("no-op update: %v", err)
	}
	rig.expect(t, 0, "an update that changes nothing")

	other, err := svc.Create(ctx, CreateBudgetRequest{Name: "Sales", SubjectType: store.BudgetSubjectUser, Amount: new("50"), AmountSet: true}, "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rig.signals(t)
	if _, err := svc.SetDefault(ctx, created.ID, "admin"); err != nil {
		t.Fatalf("set default: %v", err)
	}
	rig.expect(t, 1, "a promotion")
	if err := svc.Delete(ctx, created.ID, "admin"); err == nil {
		t.Fatal("deleting the tier's default succeeded")
	}
	rig.expect(t, 0, "a refused delete")
	if err := svc.Delete(ctx, other.ID, "admin"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	rig.expect(t, 1, "a delete")
}

func TestBudgetAssignmentService_SignalsEachCommittedWrite(t *testing.T) {
	rig := newBudgetsChangedRig(t)
	f := newAssignmentSvc(t)
	f.svc.changed = rig.signal
	ctx := t.Context()
	eng, sales := f.userBudget(t, "Engineering"), f.userBudget(t, "Sales")
	user := f.user(t, "alice", "oidc", nil, nil)

	a := f.mustAssign(t, user, eng.ID)
	rig.expect(t, 1, "an assignment")

	if _, err := f.svc.Update(ctx, a.ID, UpdateBudgetAssignmentRequest{BudgetID: sales.ID}, "admin"); err != nil {
		t.Fatalf("move: %v", err)
	}
	rig.expect(t, 1, "a move")
	if _, err := f.svc.Update(ctx, a.ID, UpdateBudgetAssignmentRequest{BudgetID: sales.ID}, "admin"); err != nil {
		t.Fatalf("no-op move: %v", err)
	}
	rig.expect(t, 0, "a move to the budget it is on")

	bob := f.user(t, "sub-bob", "jit", nil, new("bob@acme.test"))
	b := f.mustAssign(t, bob, eng.ID)
	rig.expect(t, 1, "bob's assignment")
	bySub := BudgetAssignmentIdentity{Type: "sub", Value: "sub-bob", Issuer: "https://idp.test"}
	if _, err := f.svc.Update(ctx, b.ID, UpdateBudgetAssignmentRequest{Identity: &bySub}, "admin"); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	rig.expect(t, 0, "a re-record, which caps the same user")
	byEmail := BudgetAssignmentIdentity{Type: "email", Value: "bob@acme.test"}
	if _, err := f.svc.Update(ctx, b.ID, UpdateBudgetAssignmentRequest{BudgetID: sales.ID, Identity: &byEmail}, "admin"); err != nil {
		t.Fatalf("move and re-record: %v", err)
	}
	rig.expect(t, 1, "a move and re-record")

	if err := f.svc.Delete(ctx, a.ID, "admin"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	rig.expect(t, 1, "a delete")

	if _, err := f.svc.UpdateAssignments(ctx, eng.ID, UpdateBudgetAssignmentsRequest{SubjectType: store.BudgetSubjectUser, Add: []string{user}}, "admin"); err != nil {
		t.Fatalf("save: %v", err)
	}
	rig.expect(t, 1, "a save that adds a subject")
	if _, err := f.svc.UpdateAssignments(ctx, eng.ID, UpdateBudgetAssignmentsRequest{SubjectType: store.BudgetSubjectUser}, "admin"); err != nil {
		t.Fatalf("empty save: %v", err)
	}
	rig.expect(t, 0, "an empty save")

	pending, err := f.svc.Create(ctx, CreateBudgetAssignmentRequest{
		SubjectType: store.BudgetSubjectUser, BudgetID: eng.ID,
		Identity: &BudgetAssignmentIdentity{Type: store.BudgetIdentityEmail, Value: "pat@example.com"},
	}, "admin")
	if err != nil {
		t.Fatalf("pending assignment: %v", err)
	}
	rig.expect(t, 1, "a pending assignment, which limits spend booked by the email")
	if _, err := f.svc.Update(ctx, pending.ID, UpdateBudgetAssignmentRequest{BudgetID: sales.ID}, "admin"); err != nil {
		t.Fatalf("move pending: %v", err)
	}
	rig.expect(t, 1, "a pending move")
	if err := f.svc.Delete(ctx, pending.ID, "admin"); err != nil {
		t.Fatalf("delete pending: %v", err)
	}
	rig.expect(t, 1, "a pending delete")
}

func TestBudgetTopUpService_SignalsAGrantAndACancel(t *testing.T) {
	rig := newBudgetsChangedRig(t)
	s := newTopUpService(t)
	s.svc.changed = rig.signal
	ctx := t.Context()

	if _, _, err := s.svc.Grant(ctx, CreateBudgetTopUpRequest{SubjectType: store.BudgetSubjectMesh, Amount: "500"}, "admin"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	rig.expect(t, 1, "a grant")
	if _, _, err := s.svc.Grant(ctx, CreateBudgetTopUpRequest{SubjectType: store.BudgetSubjectMesh, Amount: "700"}, "admin"); err != nil {
		t.Fatalf("regrant: %v", err)
	}
	rig.expect(t, 1, "a regrant")
	if _, _, err := s.svc.Grant(ctx, CreateBudgetTopUpRequest{SubjectType: store.BudgetSubjectMesh, Amount: "-1"}, "admin"); err == nil {
		t.Fatal("a negative grant succeeded")
	}
	rig.expect(t, 0, "a refused grant")

	if err := s.svc.CancelForSubject(ctx, store.BudgetSubjectMesh, store.BudgetMeshSubjectRef, "admin"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rig.expect(t, 1, "a cancel")
}
