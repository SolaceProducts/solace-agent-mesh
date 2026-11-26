// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

func bundledSkillCount() int {
	return len(builtinskills.Names()) + len(design.Skills()) + len(schemaSkillNames())
}

const reconcileTestNamespace = "reconcile-ns"

func newReconcileBroker(t *testing.T) (broker.Broker, broker.Queue) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.SyncNotifySubscription(reconcileTestNamespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return brk, q
}

func drainNotifications(t *testing.T, brk broker.Broker, q broker.Queue, expected int, timeout time.Duration) []syncNotification {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got []syncNotification
	for time.Now().Before(deadline) && len(got) < expected {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		msg, err := brk.Receive(ctx, q)
		cancel()
		if err != nil || msg == nil {
			continue
		}
		var n syncNotification
		if jerr := json.Unmarshal(msg.Payload, &n); jerr != nil {
			t.Fatalf("unmarshal notification: %v", jerr)
		}
		got = append(got, n)
		msg.Ack()
	}
	return got
}

func TestSTRReconcileHandler_PublishesPerTool(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	brk, q := newReconcileBroker(t)
	toolPackageRepo := store.NewToolPackageRepo(db)

	makeReadyPackage(t.Context(), t, toolPackageRepo, "alpha-pkg")
	makeReadyPackage(t.Context(), t, toolPackageRepo, "beta-pkg")

	h := NewSTRReconcileHandler(brk, toolPackageRepo, nil, reconcileTestNamespace, slog.Default())
	h.Handle(t.Context())

	notes := drainNotifications(t, brk, q, 2, 3*time.Second)
	if len(notes) != 2 {
		t.Fatalf("expected 2 notifications, got %d: %+v", len(notes), notes)
	}

	names := map[string]bool{}
	for _, n := range notes {
		if n.Category != "tools" {
			t.Errorf("category = %q, want tools", n.Category)
		}
		if n.Reason != reasonToolExists {
			t.Errorf("reason = %q, want %q", n.Reason, reasonToolExists)
		}
		names[n.Name] = true
	}
	if !names["alpha-pkg"] || !names["beta-pkg"] {
		t.Errorf("expected both alpha-pkg and beta-pkg in notifications; got %v", names)
	}
}

func TestSTRReconcileHandler_PublishesPerSkill(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	brk, q := newReconcileBroker(t)
	skillRepo := store.NewSkillRepo(db)

	userSkills := []string{"skill-1", "skill-2"}
	for _, name := range userSkills {
		if err := skillRepo.UpsertByName(t.Context(), &store.Skill{
			Name:            name,
			DiscoveryStatus: "ready",
			CreatedBy:       "tester",
			UpdatedBy:       "tester",
		}); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}

	h := NewSTRReconcileHandler(brk, nil, skillRepo, reconcileTestNamespace, slog.Default())
	h.Handle(t.Context())

	wantTotal := bundledSkillCount() + len(userSkills)
	notes := drainNotifications(t, brk, q, wantTotal, 3*time.Second)
	if len(notes) != wantTotal {
		t.Fatalf("expected %d notifications (bundled + user), got %d", wantTotal, len(notes))
	}
	names := map[string]bool{}
	for _, n := range notes {
		if n.Category != "skills" {
			t.Errorf("category = %q, want skills", n.Category)
		}
		if n.Reason != reasonSkillExists {
			t.Errorf("reason = %q, want %q", n.Reason, reasonSkillExists)
		}
		names[n.Name] = true
	}
	for _, want := range userSkills {
		if !names[want] {
			t.Errorf("user skill %q missing from notifications", want)
		}
	}
}

func TestSTRReconcileHandler_NoUserResourcesStillPublishesBundled(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	brk, q := newReconcileBroker(t)

	h := NewSTRReconcileHandler(brk,
		store.NewToolPackageRepo(db),
		store.NewSkillRepo(db),
		reconcileTestNamespace,
		slog.Default(),
	)
	h.Handle(t.Context())

	want := bundledSkillCount()
	notes := drainNotifications(t, brk, q, want, 3*time.Second)
	if len(notes) != want {
		t.Fatalf("expected %d bundled-skill notifications with empty DB, got %d", want, len(notes))
	}
	names := map[string]bool{}
	for _, n := range notes {
		if n.Category != "skills" {
			t.Errorf("category = %q, want skills", n.Category)
		}
		if n.Reason != reasonSkillExists {
			t.Errorf("reason = %q, want %q", n.Reason, reasonSkillExists)
		}
		names[n.Name] = true
	}
	for _, want := range builtinskills.Names() {
		if !names[want] {
			t.Errorf("builtin skill %q missing from notifications", want)
		}
	}
	for _, sk := range design.Skills() {
		if !names[sk.Name] {
			t.Errorf("design skill %q missing from notifications", sk.Name)
		}
	}
	for _, want := range schemaSkillNames() {
		if !names[want] {
			t.Errorf("schema skill %q missing from notifications", want)
		}
	}
}

func TestSTRReconcileHandler_Pagination(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	brk, q := newReconcileBroker(t)
	toolPackageRepo := store.NewToolPackageRepo(db)

	const want = strReconcilePageSize + 5
	for i := range want {
		makeReadyPackage(t.Context(), t, toolPackageRepo, fmt.Sprintf("pkg-%03d", i))
	}

	h := NewSTRReconcileHandler(brk, toolPackageRepo, nil, reconcileTestNamespace, slog.Default())
	h.Handle(t.Context())

	notes := drainNotifications(t, brk, q, want, 5*time.Second)
	if len(notes) != want {
		t.Fatalf("expected %d notifications across two pages, got %d", want, len(notes))
	}
	seen := map[string]bool{}
	for _, n := range notes {
		seen[n.Name] = true
	}
	if len(seen) != want {
		t.Errorf("expected %d distinct names, got %d", want, len(seen))
	}
}

type failingPublishBroker struct {
	broker.Broker
	calls     int
	failAfter int
}

func (f *failingPublishBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	f.calls++
	if f.calls > f.failAfter {
		return errPublishGuaranteedRejected
	}
	return f.Broker.PublishGuaranteed(ctx, topic, msg)
}

var errPublishGuaranteedRejected = fmt.Errorf("synthetic publish failure for test")

func TestSTRReconcileHandler_PublishErrorsAreLoggedAndSkipped(t *testing.T) {
	t.Parallel()
	brk, _ := newReconcileBroker(t)
	wrapped := &failingPublishBroker{Broker: brk, failAfter: 1}

	h := NewSTRReconcileHandler(wrapped, nil, nil, reconcileTestNamespace, slog.Default())
	count := h.publishExistingSkills(t.Context())

	if count != 1 {
		t.Errorf("expected exactly 1 successful publish (failAfter=1), got %d", count)
	}
	if wrapped.calls != bundledSkillCount() {
		t.Errorf("expected handler to attempt all %d bundled skills despite failures, got %d attempts", bundledSkillCount(), wrapped.calls)
	}
}

func TestSTRReconcileHandler_NilBrokerIsSafe(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	h := NewSTRReconcileHandler(nil,
		store.NewToolPackageRepo(db),
		store.NewSkillRepo(db),
		reconcileTestNamespace,
		slog.Default(),
	)
	h.Handle(t.Context())
}
