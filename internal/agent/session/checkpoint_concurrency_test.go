// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestAdoptCheckpointIsExclusiveUnderConcurrency(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	taskID, sessID := idgen.New(), idgen.New()
	seedSession(t, store, "app", "user", sessID)
	if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
		TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, PendingCount: 0,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	const replicas = 16
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for range replicas {
		wg.Go(func() {
			<-start
			cp, err := store.AdoptCheckpoint(ctx, taskID, "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if cp != nil {
				wins.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Errorf("%d replicas claimed the checkpoint, want exactly 1", got)
	}
}

func TestRecordToolResultDrainsExactlyOnceUnderConcurrency(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	taskID, sessID := idgen.New(), idgen.New()
	seedSession(t, store, "app", "user", sessID)

	const calls = 12
	if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
		TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, PendingCount: calls,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	var zeroSightings atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range calls {
		wg.Go(func() {
			<-start
			remaining, err := store.RecordToolResult(ctx, taskID, "", &ToolCallResult{
				ToolCallID: idgen.New(),
				ToolName:   "peer_agent",
				Content:    "done",
			})
			if err != nil {
				t.Errorf("record %d: %v", i, err)
				return
			}
			if remaining == 0 {
				zeroSightings.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()

	if got := zeroSightings.Load(); got != 1 {
		t.Errorf("%d writers saw pendingCount reach 0, want exactly 1", got)
	}

	cp, err := store.AdoptCheckpoint(ctx, taskID, "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if cp == nil {
		t.Fatal("checkpoint must be claimable once every result is recorded")
	}
	if len(cp.ToolResults) != calls {
		t.Errorf("recorded %d results, want %d", len(cp.ToolResults), calls)
	}
}

func TestRegisterPendingCallsIsIdempotentUnderConcurrency(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	taskID, sessID, corrID := idgen.New(), idgen.New(), idgen.New()
	seedSession(t, store, "app", "user", sessID)
	if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
		TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			<-start
			if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{
				{CorrID: corrID, TaskID: taskID, AgentName: "app", Kind: PendingKindPeer},
			}); err != nil {
				t.Errorf("register: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()

	got, err := store.LookupPendingCall(ctx, "app", corrID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil || got.TaskID != taskID {
		t.Errorf("got %+v, want a single row for task %q", got, taskID)
	}
}
