// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestMemoryStoreCreateAndGet(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	sess := &Session{
		AppName:   "app",
		UserID:    "user-1",
		SessionID: "sess-1",
		CreatedAt: time.Now(),
	}

	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, "app", "user-1", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("session not found")
	}
	if got.AppName != "app" || got.UserID != "user-1" || got.SessionID != "sess-1" {
		t.Errorf("session = %+v", got)
	}
}

func TestMemoryStoreCreateDuplicate(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	sess := &Session{AppName: "app", UserID: "u", SessionID: "s"}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, sess); err == nil {
		t.Error("expected error for duplicate create")
	}
}

func TestMemoryStoreGetNotFound(t *testing.T) {
	store := NewMemoryStore()
	got, err := store.Get(context.Background(), "app", "user", "sess")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for non-existent session")
	}
}

func TestMemoryStoreSave(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	sess := &Session{AppName: "app", UserID: "u", SessionID: "s", CreatedAt: time.Now()}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	loaded, _ := store.Get(ctx, "app", "u", "s")
	loaded.Metadata = map[string]any{"k": "v"}

	if err := store.Save(ctx, loaded); err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(ctx, "app", "u", "s")
	if got.Metadata["k"] != "v" {
		t.Errorf("metadata = %+v", got.Metadata)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
}

func TestMemoryStoreSaveOptimisticLock(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	sess := &Session{AppName: "app", UserID: "u", SessionID: "s", CreatedAt: time.Now()}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	reader1, _ := store.Get(ctx, "app", "u", "s")
	reader2, _ := store.Get(ctx, "app", "u", "s")

	reader1.Metadata = map[string]any{"who": "reader 1"}
	if err := store.Save(ctx, reader1); err != nil {
		t.Fatal(err)
	}

	reader2.Metadata = map[string]any{"who": "reader 2"}
	if err := store.Save(ctx, reader2); err == nil {
		t.Error("expected optimistic lock failure")
	}
}

func TestMemoryStoreSaveNotFound(t *testing.T) {
	store := NewMemoryStore()
	sess := &Session{AppName: "app", UserID: "u", SessionID: "nonexistent"}
	if err := store.Save(context.Background(), sess); err == nil {
		t.Error("expected error for saving non-existent session")
	}
}

func TestMemoryStoreAppendMessages(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessages(ctx, "app", "u", "s", []Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessages(ctx, "app", "u", "s", []Message{{Role: "user", Content: "c"}}); err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(ctx, "app", "u", "s")
	got.Metadata = map[string]any{"x": "y"}
	if err := store.Save(ctx, got); err != nil {
		t.Fatal(err)
	}

	reloaded, _ := store.Get(ctx, "app", "u", "s")
	want := []string{"a", "b", "c"}
	if len(reloaded.Messages) != len(want) {
		t.Fatalf("messages = %d, want %d", len(reloaded.Messages), len(want))
	}
	for i, w := range want {
		if reloaded.Messages[i].Content != w {
			t.Errorf("message[%d] = %q, want %q", i, reloaded.Messages[i].Content, w)
		}
	}
}

func TestMemoryStoreAppendMessagesMissingSession(t *testing.T) {
	store := NewMemoryStore()
	if err := store.AppendMessages(context.Background(), "app", "u", "nope", []Message{{Role: "user", Content: "x"}}); err == nil {
		t.Error("expected error appending to a non-existent session")
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	sess := &Session{AppName: "app", UserID: "u", SessionID: "s"}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, "app", "u", "s"); err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(ctx, "app", "u", "s")
	if got != nil {
		t.Error("session should be deleted")
	}
}

func TestMemoryStoreGetReturnsDeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	sess := &Session{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
		Messages:  []Message{{Role: "user", Content: "original"}},
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(ctx, "app", "u", "s")
	got.Messages[0].Content = "mutated"

	got2, _ := store.Get(ctx, "app", "u", "s")
	if got2.Messages[0].Content != "original" {
		t.Error("store returned a shallow copy — mutation leaked")
	}
}

func TestMemoryStoreSaveCheckpoint(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	cp := &TaskCheckpoint{
		TaskID:       "task-1",
		AppName:      "app",
		PendingCount: 2,
		State:        []byte(`{"step": 3}`),
	}

	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	claimed, err := store.AdoptCheckpoint(ctx, "task-1", "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatal(err)
	}
	if claimed != nil {
		t.Error("should not claim checkpoint with pending > 0")
	}
}

func TestMemoryStoreRecordToolResult(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	cp := &TaskCheckpoint{TaskID: "task-1", PendingCount: 2}
	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	pending, err := store.RecordToolResult(ctx, "task-1", "", &ToolCallResult{
		ToolCallID: "c1", ToolName: "tool_a", Content: "result 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Errorf("pending = %d, want 1", pending)
	}

	pending, err = store.RecordToolResult(ctx, "task-1", "", &ToolCallResult{
		ToolCallID: "c2", ToolName: "tool_b", Content: "result 2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Errorf("pending = %d, want 0", pending)
	}
}

func TestMemoryStoreRecordToolResultNotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.RecordToolResult(context.Background(), "nonexistent", "", &ToolCallResult{})
	if err == nil {
		t.Error("expected error for non-existent checkpoint")
	}
}

func TestMemoryStoreAdoptCheckpoint(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	cp := &TaskCheckpoint{TaskID: "task-1", PendingCount: 1}
	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	_, err := store.RecordToolResult(ctx, "task-1", "", &ToolCallResult{
		ToolCallID: "c1", Content: "done",
	})
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := store.AdoptCheckpoint(ctx, "task-1", "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("expected to claim checkpoint")
	}
	if len(claimed.ToolResults) != 1 {
		t.Errorf("tool results = %d, want 1", len(claimed.ToolResults))
	}

	claimed2, err := store.AdoptCheckpoint(ctx, "task-1", "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatal(err)
	}
	if claimed2 != nil {
		t.Error("expected nil for already-claimed checkpoint")
	}
}

func TestMemoryStoreClaimNotFound(t *testing.T) {
	store := NewMemoryStore()
	claimed, err := store.AdoptCheckpoint(context.Background(), "nonexistent", "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatal(err)
	}
	if claimed != nil {
		t.Error("expected nil for non-existent checkpoint")
	}
}

func TestMemoryStoreDeleteCheckpoint(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	cp := &TaskCheckpoint{TaskID: "task-1", PendingCount: 0}
	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteCheckpoint(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}

	claimed, _ := store.AdoptCheckpoint(ctx, "task-1", "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if claimed != nil {
		t.Error("checkpoint should be deleted")
	}
}

func TestMemoryStoreRecordToolResultConcurrent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	cp := &TaskCheckpoint{TaskID: "task-1", PendingCount: 10}
	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(10)
	results := make([]int, 10)

	for i := range 10 {
		go func(idx int) {
			defer wg.Done()
			pending, err := store.RecordToolResult(ctx, "task-1", "", &ToolCallResult{
				ToolCallID: "c" + string(rune('0'+idx)),
				Content:    "result",
			})
			if err != nil {
				t.Error(err)
				return
			}
			results[idx] = pending
		}(i)
	}

	wg.Wait()

	claimed, _ := store.AdoptCheckpoint(ctx, "task-1", "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if claimed == nil {
		t.Fatal("expected to claim checkpoint after all results recorded")
	}
	if claimed.PendingCount != 0 {
		t.Errorf("pending = %d, want 0", claimed.PendingCount)
	}
	if len(claimed.ToolResults) != 10 {
		t.Errorf("tool results = %d, want 10", len(claimed.ToolResults))
	}
}

func TestMemoryStoreSaveCheckpointReplace(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	cp1 := &TaskCheckpoint{TaskID: "task-1", PendingCount: 2, State: []byte("v1")}
	if err := store.SaveCheckpoint(ctx, cp1); err != nil {
		t.Fatal(err)
	}

	cp2 := &TaskCheckpoint{TaskID: "task-1", PendingCount: 3, State: []byte("v2")}
	if err := store.SaveCheckpoint(ctx, cp2); err != nil {
		t.Fatal(err)
	}

	for i := range 3 {
		if _, err := store.RecordToolResult(ctx, "task-1", "",
			&ToolCallResult{ToolCallID: fmt.Sprintf("tc-%d", i), Content: "r"}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	claimed, _ := store.AdoptCheckpoint(ctx, "task-1", "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if claimed == nil {
		t.Fatal("expected checkpoint")
	}
	if string(claimed.State) != "v2" {
		t.Errorf("state = %q, want v2", claimed.State)
	}
}

func TestMemoryStoreGetByContextAndTask(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if _, err := store.GetByContextAndTask(ctx, "app", "u", "ctx-1", ""); err == nil {
		t.Errorf("expected error for empty taskID")
	}

	tID := "task-aaa"
	run := &Session{
		AppName:   "app",
		UserID:    "u",
		SessionID: "sess-1",
		ContextID: "ctx-1",
		TaskID:    &tID,
	}
	if err := store.Create(ctx, run); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.GetByContextAndTask(ctx, "app", "u", "ctx-1", "task-aaa")
	if err != nil || got == nil || got.SessionID != "sess-1" {
		t.Errorf("GetByContextAndTask = (%+v, %v), want sess-1", got, err)
	}

	miss, err := store.GetByContextAndTask(ctx, "app", "u", "ctx-1", "task-bbb")
	if err != nil || miss != nil {
		t.Errorf("miss = (%+v, %v), want (nil, nil)", miss, err)
	}

	miss2, err := store.GetByContextAndTask(ctx, "app", "u", "ctx-2", "task-aaa")
	if err != nil || miss2 != nil {
		t.Errorf("wrong-context miss = (%+v, %v), want (nil, nil)", miss2, err)
	}
}

func TestMemoryStoreCreateRejectsDuplicateContextTask(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	tID := "task-a"
	if err := store.Create(ctx, &Session{
		AppName:   "app",
		UserID:    "u",
		SessionID: "sess-1",
		ContextID: "ctx-1",
		TaskID:    &tID,
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	tID2 := "task-a"
	if err := store.Create(ctx, &Session{
		AppName:   "app",
		UserID:    "u",
		SessionID: "sess-2",
		ContextID: "ctx-1",
		TaskID:    &tID2,
	}); !errors.Is(err, ErrSessionExists) {
		t.Errorf("duplicate (context, task) err = %v, want ErrSessionExists", err)
	}
}

func TestMemoryStoreConcurrentCreateSameKeyYieldsOneWinner(t *testing.T) {
	store := NewMemoryStore()
	assertOneConcurrentCreateWins(t, store)
}

func assertOneConcurrentCreateWins(t *testing.T, store Store) {
	t.Helper()
	const callers = 8
	sessID := idgen.New()
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			errs[i] = store.Create(t.Context(), &Session{
				AppName:   "app",
				UserID:    "user-1",
				SessionID: sessID,
				ContextID: sessID,
			})
		})
	}
	wg.Wait()

	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrSessionExists):
			t.Errorf("caller %d: err = %v, want ErrSessionExists", i, err)
		}
	}
	if wins != 1 {
		t.Errorf("successful creates = %d, want exactly 1", wins)
	}
}
