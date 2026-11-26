// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func setupOutboxTestDB(t *testing.T) *OutboxRepo {
	t.Helper()
	db := TestDB(t)
	return NewOutboxRepo(db)
}

func TestOutboxRepo_CreateAndGetDispatchable(t *testing.T) {
	repo := setupOutboxTestDB(t)
	ctx := context.Background()

	e := &OutboxEvent{
		EventType:   "agent.deployed",
		AggregateID: "c0000000-0000-0000-0000-000000000001",
		Payload:     `{"agent_id":"c0000000-0000-0000-0000-000000000001","status":"deployed"}`,
	}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.ID == "" {
		t.Fatal("expected auto-generated ID")
	}
	if e.CreatedAt == 0 {
		t.Fatal("expected auto-generated CreatedAt")
	}

	events, err := repo.GetDispatchable(ctx, 10)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ID != e.ID {
		t.Errorf("ID = %q, want %q", events[0].ID, e.ID)
	}
	if events[0].EventType != "agent.deployed" {
		t.Errorf("EventType = %q", events[0].EventType)
	}
	if events[0].Published {
		t.Error("expected Published = false")
	}
}

func TestOutboxRepo_MarkPublished(t *testing.T) {
	repo := setupOutboxTestDB(t)
	ctx := context.Background()

	e1 := &OutboxEvent{EventType: "type1", AggregateID: "c0000000-0000-0000-0000-000000000002", Payload: "{}"}
	e2 := &OutboxEvent{EventType: "type2", AggregateID: "c0000000-0000-0000-0000-000000000003", Payload: "{}"}
	_ = repo.Create(ctx, e1)
	_ = repo.Create(ctx, e2)

	if err := repo.MarkPublished(ctx, []string{e1.ID}); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}

	events, _ := repo.GetDispatchable(ctx, 10)
	if len(events) != 1 {
		t.Fatalf("expected 1 unpublished, got %d", len(events))
	}
	if events[0].ID != e2.ID {
		t.Errorf("remaining event ID = %q, want %q", events[0].ID, e2.ID)
	}
}

func TestOutboxRepo_MarkPublished_Empty(t *testing.T) {
	repo := setupOutboxTestDB(t)
	if err := repo.MarkPublished(context.Background(), nil); err != nil {
		t.Fatalf("MarkPublished(nil): %v", err)
	}
}

func TestOutboxRepo_Cleanup(t *testing.T) {
	repo := setupOutboxTestDB(t)
	ctx := context.Background()

	old := &OutboxEvent{EventType: "old", AggregateID: "c0000000-0000-0000-0000-000000000004", Payload: "{}"}
	_ = repo.Create(ctx, old)
	_ = repo.MarkPublished(ctx, []string{old.ID})

	recent := &OutboxEvent{EventType: "recent", AggregateID: "c0000000-0000-0000-0000-000000000005", Payload: "{}"}
	_ = repo.Create(ctx, recent)
	_ = repo.MarkPublished(ctx, []string{recent.ID})

	deleted, err := repo.Cleanup(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	events, _ := repo.GetDispatchable(ctx, 10)
	if len(events) != 0 {
		t.Errorf("expected 0 events after cleanup, got %d", len(events))
	}
}

func TestOutboxRepo_GetDispatchable_OrderByCreatedAt(t *testing.T) {
	repo := setupOutboxTestDB(t)
	ctx := context.Background()

	e1 := &OutboxEvent{EventType: "first", AggregateID: "c0000000-0000-0000-0000-000000000004", Payload: "{}", CreatedAt: 100}
	e2 := &OutboxEvent{EventType: "second", AggregateID: "c0000000-0000-0000-0000-000000000005", Payload: "{}", CreatedAt: 200}
	e3 := &OutboxEvent{EventType: "third", AggregateID: "c0000000-0000-0000-0000-000000000006", Payload: "{}", CreatedAt: 50}
	_ = repo.Create(ctx, e1)
	_ = repo.Create(ctx, e2)
	_ = repo.Create(ctx, e3)

	events, _ := repo.GetDispatchable(ctx, 10)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].EventType != "third" || events[1].EventType != "first" || events[2].EventType != "second" {
		t.Errorf("order = [%s, %s, %s], want [third, first, second]",
			events[0].EventType, events[1].EventType, events[2].EventType)
	}
}

func TestOutboxRepo_GetDispatchable_Limit(t *testing.T) {
	repo := setupOutboxTestDB(t)
	ctx := context.Background()

	for range 5 {
		_ = repo.Create(ctx, &OutboxEvent{EventType: "type", AggregateID: "c0000000-0000-0000-0000-000000000007", Payload: "{}"})
	}

	events, _ := repo.GetDispatchable(ctx, 3)
	if len(events) != 3 {
		t.Errorf("expected 3 events with limit=3, got %d", len(events))
	}
}

func TestOutboxRepo_CreateInTx_RollbackDropsRow(t *testing.T) {
	repo := setupOutboxTestDB(t)
	ctx := context.Background()

	sentinel := errors.New("force rollback")
	err := repo.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := repo.CreateInTx(ctx, tx, &OutboxEvent{
			EventType:   "tool.uploaded",
			AggregateID: "c0000000-0000-0000-0000-000000000010",
			Payload:     "{}",
		}); err != nil {
			t.Fatalf("CreateInTx: %v", err)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx err = %v, want sentinel", err)
	}

	events, err := repo.GetDispatchable(ctx, 10)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d", len(events))
	}
}

func TestOutboxRepo_CreateInTx_CommitPersistsRow(t *testing.T) {
	repo := setupOutboxTestDB(t)
	ctx := context.Background()

	if err := repo.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return repo.CreateInTx(ctx, tx, &OutboxEvent{
			EventType:   "tool.uploaded",
			AggregateID: "c0000000-0000-0000-0000-000000000011",
			Payload:     "{}",
		})
	}); err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	events, _ := repo.GetDispatchable(ctx, 10)
	if len(events) != 1 {
		t.Fatalf("expected 1 row after commit, got %d", len(events))
	}
}

func TestOutboxRepo_MarkFailed_BackoffAndDeadLetter(t *testing.T) {
	repo := setupOutboxTestDB(t)
	ctx := context.Background()

	e := &OutboxEvent{EventType: "tool.uploaded", AggregateID: "c0000000-0000-0000-0000-000000000020", Payload: "{}"}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	future := time.Now().Add(time.Hour).UnixMilli()
	if err := repo.MarkFailed(ctx, e.ID, "first failure", 1, future, false); err != nil {
		t.Fatalf("MarkFailed (retry): %v", err)
	}
	dispatchable, _ := repo.GetDispatchable(ctx, 10)
	if len(dispatchable) != 0 {
		t.Fatalf("expected row to be hidden until next_attempt_at, got %d dispatchable", len(dispatchable))
	}

	past := time.Now().Add(-time.Hour).UnixMilli()
	if err := repo.MarkFailed(ctx, e.ID, "permanent failure", 11, past, true); err != nil {
		t.Fatalf("MarkFailed (dead): %v", err)
	}
	dispatchable, _ = repo.GetDispatchable(ctx, 10)
	if len(dispatchable) != 0 {
		t.Fatalf("expected dead row to stay hidden, got %d dispatchable", len(dispatchable))
	}
}

func TestOutboxRepo_HasDispatchable(t *testing.T) {
	repo := setupOutboxTestDB(t)
	ctx := t.Context()
	has := func() bool {
		t.Helper()
		ok, err := repo.HasDispatchable(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if has() {
		t.Fatal("an empty outbox reported a dispatchable event")
	}
	e := &OutboxEvent{EventType: "tool.uploaded", AggregateID: "c0000000-0000-0000-0000-000000000021", Payload: "{}"}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	if !has() {
		t.Fatal("a pending event was not reported")
	}
	if err := repo.MarkFailed(ctx, e.ID, "retry", 1, time.Now().Add(time.Hour).UnixMilli(), false); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Fatal("an event waiting out its backoff was reported")
	}
	if err := repo.MarkFailed(ctx, e.ID, "dead", 11, time.Now().Add(-time.Hour).UnixMilli(), true); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Fatal("a dead event was reported")
	}
	published := &OutboxEvent{EventType: "tool.uploaded", AggregateID: "c0000000-0000-0000-0000-000000000022", Payload: "{}"}
	if err := repo.Create(ctx, published); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkPublished(ctx, []string{published.ID}); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Fatal("a published event was reported")
	}
}
