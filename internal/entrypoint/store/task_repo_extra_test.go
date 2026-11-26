// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestTaskRepo_ListActiveBackground(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	endTime := now + 1000

	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: now, ExecutionMode: "background"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000002", UserID: "u1", StartTime: now, ExecutionMode: "background", EndTime: &endTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000003", UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000004", UserID: "u2", StartTime: now, ExecutionMode: "background"}); err != nil {
		t.Fatal(err)
	}

	tasks, err := repo.ListActiveBackground(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 active background task, got %d", len(tasks))
	}
	if tasks[0].ID != "10000000-0000-0000-0000-000000000001" {
		t.Errorf("task ID = %s, want 10000000-0000-0000-0000-000000000001", tasks[0].ID)
	}
}

func TestTaskRepo_Search(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	base := int64(1000000)
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: base, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000002", UserID: "u1", StartTime: base + 100, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000003", UserID: "u1", StartTime: base + 200, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000004", UserID: "u2", StartTime: base + 300, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	tasks, total, err := repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, TaskSearchOpts{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(tasks) != 3 {
		t.Errorf("len = %d, want 3", len(tasks))
	}

	tasks, total, err = repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, TaskSearchOpts{StartDate: base + 50, EndDate: base + 150}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(tasks) != 1 || tasks[0].ID != "10000000-0000-0000-0000-000000000002" {
		t.Errorf("expected task 10000000-0000-0000-0000-000000000002")
	}

	tasks, total, err = repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, TaskSearchOpts{}, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (total should be unaffected by limit)", total)
	}
	if len(tasks) != 2 {
		t.Errorf("len = %d, want 2", len(tasks))
	}
}

func TestTaskRepo_Search_KindAndSort(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	taskA := "10000000-0000-0000-0000-00000000aaa1"
	taskB := "10000000-0000-0000-0000-00000000aaa2"
	compC := "10000000-0000-0000-0000-00000000aaa3"
	la1 := int64(300)
	la2 := int64(200)
	la3 := int64(150)
	if err := repo.Create(ctx, &Task{ID: taskA, UserID: "u1", StartTime: 100, ExecutionMode: "foreground", LastActivityTime: &la1, Kind: TaskKindNormal}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: taskB, UserID: "u1", StartTime: 200, ExecutionMode: "foreground", LastActivityTime: &la2, Kind: TaskKindNormal}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: compC, UserID: "u1", StartTime: 300, ExecutionMode: "foreground", LastActivityTime: &la3, Kind: TaskKindCompactionCost}); err != nil {
		t.Fatal(err)
	}

	tasks, total, err := repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, TaskSearchOpts{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(tasks) != 3 {
		t.Fatalf("default search: total=%d len=%d, want 3/3", total, len(tasks))
	}
	if tasks[0].ID != compC || tasks[1].ID != taskB || tasks[2].ID != taskA {
		t.Errorf("default order = [%s,%s,%s], want [%s,%s,%s]", tasks[0].ID, tasks[1].ID, tasks[2].ID, compC, taskB, taskA)
	}

	tasks, total, err = repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, TaskSearchOpts{Kind: TaskKindNormal}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(tasks) != 2 {
		t.Fatalf("kind=task: total=%d len=%d, want 2/2", total, len(tasks))
	}
	for _, ts := range tasks {
		if ts.Kind != TaskKindNormal {
			t.Errorf("got kind=%s, want only task", ts.Kind)
		}
	}

	tasks, _, err = repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, TaskSearchOpts{
		Kind:      TaskKindNormal,
		SortField: TaskSortLastActivity,
		SortDir:   TaskSortDesc,
	}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != taskA || tasks[1].ID != taskB {
		t.Errorf("lastActivity DESC order wrong: got [%s,%s]", tasks[0].ID, tasks[1].ID)
	}

	tasks, _, err = repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, TaskSearchOpts{
		Kind:      TaskKindNormal,
		SortField: TaskSortStartTime,
		SortDir:   TaskSortAsc,
	}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != taskA || tasks[1].ID != taskB {
		t.Errorf("startTime ASC order wrong: got [%s,%s]", tasks[0].ID, tasks[1].ID)
	}
}

func TestTaskRepo_FindActiveChildren(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	endTime := now + 1000
	parentID := "10000000-0000-0000-0000-000000000010"

	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000010", UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	running := "running"
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000011", UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &parentID, Status: &running}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000012", UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &parentID}); err != nil {
		t.Fatal(err)
	}
	completed := "completed"
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000013", UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &parentID, EndTime: &endTime, Status: &completed}); err != nil {
		t.Fatal(err)
	}

	ids, err := repo.FindActiveChildren(ctx, "10000000-0000-0000-0000-000000000010")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 active children, got %d: %v", len(ids), ids)
	}
	idSet := map[string]bool{ids[0]: true, ids[1]: true}
	if !idSet["10000000-0000-0000-0000-000000000011"] || !idSet["10000000-0000-0000-0000-000000000012"] {
		t.Errorf("expected 10000000-0000-0000-0000-000000000011 and ...0012, got %v", ids)
	}
}

func TestTaskRepo_FindActiveChildren_None(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)

	ids, err := repo.FindActiveChildren(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 active children, got %d", len(ids))
	}
}

func TestTaskRepo_HasActiveBackgroundTask(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	sid := "00000000-0000-0000-0000-000000000010"

	has, err := repo.HasActiveBackgroundTask(ctx, "00000000-0000-0000-0000-000000000010")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("expected false with no tasks")
	}

	if createErr := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: now, ExecutionMode: "background", SessionID: &sid}); createErr != nil {
		t.Fatal(err)
	}

	has, err = repo.HasActiveBackgroundTask(ctx, "00000000-0000-0000-0000-000000000010")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("expected true with active background task")
	}

	endTime := now + 1000
	_ = repo.UpdateEndTime(ctx, "10000000-0000-0000-0000-000000000001", endTime)

	has, err = repo.HasActiveBackgroundTask(ctx, "00000000-0000-0000-0000-000000000010")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("expected false after ending background task")
	}
}

func TestTaskRepo_GetParentChain(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	p1 := "10000000-0000-0000-0000-000000000001"
	p2 := "10000000-0000-0000-0000-000000000002"

	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000002", UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &p1}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000003", UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &p2}); err != nil {
		t.Fatal(err)
	}

	chain, err := repo.GetParentChain(ctx, "10000000-0000-0000-0000-000000000003", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 {
		t.Fatalf("expected chain length 3, got %d: %v", len(chain), chain)
	}
	if chain[0] != "10000000-0000-0000-0000-000000000003" || chain[1] != "10000000-0000-0000-0000-000000000002" || chain[2] != "10000000-0000-0000-0000-000000000001" {
		t.Errorf("chain = %v, want [t3, t2, t1]", chain)
	}
}

func TestTaskRepo_GetParentChain_MaxDepth(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	p1 := "10000000-0000-0000-0000-000000000001"
	p2 := "10000000-0000-0000-0000-000000000002"

	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000002", UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &p1}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000003", UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &p2}); err != nil {
		t.Fatal(err)
	}

	chain, err := repo.GetParentChain(ctx, "10000000-0000-0000-0000-000000000003", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 {
		t.Fatalf("expected chain length 2, got %d: %v", len(chain), chain)
	}
	if chain[0] != "10000000-0000-0000-0000-000000000003" || chain[1] != "10000000-0000-0000-0000-000000000002" {
		t.Errorf("chain = %v, want [t3, t2]", chain)
	}
}

func TestTaskRepo_GetParentChain_NoParent(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	chain, err := repo.GetParentChain(ctx, "10000000-0000-0000-0000-000000000001", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 1 || chain[0] != "10000000-0000-0000-0000-000000000001" {
		t.Errorf("chain = %v, want [10000000-0000-0000-0000-000000000001]", chain)
	}
}

func TestTaskRepo_GetChildTaskIDs(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	parentID := "10000000-0000-0000-0000-000000000010"

	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000010", UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000011", UserID: "u1", StartTime: now + 1, ExecutionMode: "foreground", ParentTaskID: &parentID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000012", UserID: "u1", StartTime: now + 2, ExecutionMode: "foreground", ParentTaskID: &parentID}); err != nil {
		t.Fatal(err)
	}

	ids, err := repo.GetChildTaskIDs(ctx, "10000000-0000-0000-0000-000000000010")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 children, got %d", len(ids))
	}
	if ids[0] != "10000000-0000-0000-0000-000000000011" || ids[1] != "10000000-0000-0000-0000-000000000012" {
		t.Errorf("children = %v, want [..0011, ..0012]", ids)
	}
}

func TestTaskRepo_GetChildTaskIDs_None(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	ids, err := repo.GetChildTaskIDs(ctx, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 children, got %d", len(ids))
	}
}

func TestTaskRepo_GetAllDescendantIDs(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	rootID := "10000000-0000-0000-0000-000000000020"
	c1ID := "10000000-0000-0000-0000-000000000021"

	if err := repo.Create(ctx, &Task{ID: rootID, UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: c1ID, UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &rootID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000022", UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &rootID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000023", UserID: "u1", StartTime: now, ExecutionMode: "foreground", ParentTaskID: &c1ID}); err != nil {
		t.Fatal(err)
	}

	ids, err := repo.GetAllDescendantIDs(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 descendants, got %d: %v", len(ids), ids)
	}
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	if !idSet["10000000-0000-0000-0000-000000000021"] || !idSet["10000000-0000-0000-0000-000000000022"] || !idSet["10000000-0000-0000-0000-000000000023"] {
		t.Errorf("expected c1, c2, gc1 — got %v", ids)
	}
}

func TestTaskRepo_GetAllDescendantIDs_NoDescendants(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	ids, err := repo.GetAllDescendantIDs(ctx, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 descendants, got %d", len(ids))
	}
}

func TestTaskRepo_DeleteOlderThan(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000031", UserID: "u1", StartTime: 1000, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000032", UserID: "u1", StartTime: 2000, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000033", UserID: "u1", StartTime: 5000, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	cutoff := time.UnixMilli(3000)
	deleted, err := repo.DeleteOlderThan(ctx, cutoff, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	got, err := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000033")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("expected new1 to survive")
	}

	got, _ = repo.GetByID(ctx, "10000000-0000-0000-0000-000000000031")
	if got != nil {
		t.Error("expected old1 to be deleted")
	}
}

func TestTaskRepo_DeleteOlderThan_Batching(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	for i := range 5 {
		if err := repo.Create(ctx, &Task{
			ID:            fmt.Sprintf("10000000-0000-0000-0000-00000000004%d", i),
			UserID:        "u1",
			StartTime:     int64(i * 100),
			ExecutionMode: "foreground",
		}); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := time.UnixMilli(1000)
	deleted, err := repo.DeleteOlderThan(ctx, cutoff, 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 5 {
		t.Errorf("deleted = %d, want 5", deleted)
	}
}

func TestTaskRepo_UpdateFinalStatus(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	endTime := now + 5000
	inputTokens := 100
	outputTokens := 200
	cachedTokens := 50
	details := `{"model":"gpt-4"}`

	err := repo.UpdateFinalStatus(ctx, "10000000-0000-0000-0000-000000000001", FinalStatusUpdate{
		Status: "completed", EndTime: endTime,
		InputTokens: &inputTokens, OutputTokens: &outputTokens, CachedInputTokens: &cachedTokens,
		TokenUsageDetails: &details,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == nil || *got.Status != "completed" {
		t.Errorf("status = %v, want completed", got.Status)
	}
	if got.EndTime == nil || *got.EndTime != endTime {
		t.Errorf("end_time = %v, want %d", got.EndTime, endTime)
	}
	if got.TotalInputTokens == nil || *got.TotalInputTokens != 100 {
		t.Errorf("input_tokens = %v, want 100", got.TotalInputTokens)
	}
	if got.TotalOutputTokens == nil || *got.TotalOutputTokens != 200 {
		t.Errorf("output_tokens = %v, want 200", got.TotalOutputTokens)
	}
	if got.TotalCachedInputTokens == nil || *got.TotalCachedInputTokens != 50 {
		t.Errorf("cached_tokens = %v, want 50", got.TotalCachedInputTokens)
	}
	if got.TokenUsageDetails == nil || *got.TokenUsageDetails != details {
		t.Errorf("token_usage_details = %v, want %s", got.TokenUsageDetails, details)
	}
}

func TestTaskRepo_UpdateFinalStatus_NilTokens(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	errMsg := "boom: agent exploded"
	err := repo.UpdateFinalStatus(ctx, "10000000-0000-0000-0000-000000000001", FinalStatusUpdate{
		Status: "failed", EndTime: now + 1000, ErrorMessage: &errMsg,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000001")
	if got.Status == nil || *got.Status != "failed" {
		t.Errorf("status = %v, want failed", got.Status)
	}
	if got.ErrorMessage == nil || *got.ErrorMessage != errMsg {
		t.Errorf("error_message = %v, want %q", got.ErrorMessage, errMsg)
	}
}

func TestTaskRepo_UpdateLastActivityTime(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	if err := repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	if err := repo.UpdateLastActivityTime(ctx, "10000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastActivityTime == nil {
		t.Fatal("expected last_activity_time to be set")
	}
	if *got.LastActivityTime < now {
		t.Errorf("last_activity_time = %d, expected >= %d", *got.LastActivityTime, now)
	}
}

func TestTaskRepo_FindStaleTasks(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	staleActivity := now - 10000
	recentActivity := now
	shortTimeout := int64(1000)
	longTimeout := int64(3600000)
	endTime := now

	if err := repo.Create(ctx, &Task{
		ID: "10000000-0000-0000-0000-000000000051", UserID: "u1", StartTime: staleActivity,
		ExecutionMode: "background", BackgroundExecutionEnabled: true,
		MaxExecutionTimeMs: &shortTimeout, LastActivityTime: &staleActivity,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{
		ID: "10000000-0000-0000-0000-000000000052", UserID: "u1", StartTime: recentActivity,
		ExecutionMode: "background", BackgroundExecutionEnabled: true,
		MaxExecutionTimeMs: &longTimeout, LastActivityTime: &recentActivity,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{
		ID: "10000000-0000-0000-0000-000000000053", UserID: "u1", StartTime: staleActivity,
		ExecutionMode: "background", BackgroundExecutionEnabled: true,
		MaxExecutionTimeMs: &shortTimeout, LastActivityTime: &staleActivity, EndTime: &endTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{
		ID: "10000000-0000-0000-0000-000000000054", UserID: "u1", StartTime: staleActivity,
		ExecutionMode: "foreground", LastActivityTime: &staleActivity,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{
		ID: "10000000-0000-0000-0000-000000000055", UserID: "u1", StartTime: staleActivity,
		ExecutionMode: "background", BackgroundExecutionEnabled: true,
		LastActivityTime: &staleActivity,
	}); err != nil {
		t.Fatal(err)
	}

	tasks, err := repo.FindStaleTasks(ctx, 3600000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 stale task, got %d", len(tasks))
	}
	if tasks[0].ID != "10000000-0000-0000-0000-000000000051" {
		t.Errorf("expected task 51, got %s", tasks[0].ID)
	}

	tasks, err = repo.FindStaleTasks(ctx, 1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 stale tasks, got %d", len(tasks))
	}
	idSet := map[string]bool{}
	for _, task := range tasks {
		idSet[task.ID] = true
	}
	if !idSet["10000000-0000-0000-0000-000000000051"] || !idSet["10000000-0000-0000-0000-000000000055"] || !idSet["10000000-0000-0000-0000-000000000054"] {
		t.Errorf("expected tasks 51, 55 and 54 (foreground orphan), got %v", tasks)
	}

	tasks, err = repo.FindStaleTasks(ctx, 1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected LIMIT 1 to return 1 task, got %d", len(tasks))
	}
}

func TestTaskRepo_TimeoutTaskOnlyFinalizesOpenRows(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	if err := repo.Create(ctx, &Task{
		ID: "10000000-0000-0000-0000-000000000061", UserID: "u1", StartTime: now,
		ExecutionMode: "background", BackgroundExecutionEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	if timedOut, err := repo.TimeoutTask(ctx, "10000000-0000-0000-0000-000000000061"); err != nil {
		t.Fatal(err)
	} else if !timedOut {
		t.Error("TimeoutTask returned timedOut=false, want true for an open row")
	}

	got, err := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000061")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == nil || *got.Status != "timeout" {
		t.Errorf("status = %v, want timeout", got.Status)
	}
	if got.EndTime == nil {
		t.Error("expected end_time to be set")
	}
	if got.LastActivityTime == nil || *got.LastActivityTime < now {
		t.Error("expected last_activity_time to be updated")
	}

	completedEnd := now + 1000
	completedStatus := "completed"
	if err = repo.Create(ctx, &Task{
		ID: "10000000-0000-0000-0000-000000000062", UserID: "u1", StartTime: now,
		ExecutionMode: "foreground", Status: &completedStatus, EndTime: &completedEnd,
	}); err != nil {
		t.Fatal(err)
	}
	if timedOut, timeoutErr := repo.TimeoutTask(ctx, "10000000-0000-0000-0000-000000000062"); timeoutErr != nil {
		t.Fatal(timeoutErr)
	} else if timedOut {
		t.Error("TimeoutTask returned timedOut=true on an already-finalized row, want false")
	}
	got2, err := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000062")
	if err != nil {
		t.Fatal(err)
	}
	if got2.Status == nil || *got2.Status != "completed" {
		t.Errorf("status = %v, want completed (TimeoutTask must not clobber a finalized row)", got2.Status)
	}
	if got2.EndTime == nil || *got2.EndTime != completedEnd {
		t.Errorf("end_time = %v, want %d (unchanged)", got2.EndTime, completedEnd)
	}
}

func TestTaskRepo_TimeoutTasksCountsOnlyRowsItMutated(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	openA := "10000000-0000-0000-0000-000000000071"
	openB := "10000000-0000-0000-0000-000000000072"
	for _, id := range []string{openA, openB} {
		if err := repo.Create(ctx, &Task{
			ID: id, UserID: "u1", StartTime: now, ExecutionMode: "foreground",
		}); err != nil {
			t.Fatal(err)
		}
	}

	completedEnd := now + 1000
	completedStatus := "completed"
	done := "10000000-0000-0000-0000-000000000073"
	if err := repo.Create(ctx, &Task{
		ID: done, UserID: "u1", StartTime: now, ExecutionMode: "foreground",
		Status: &completedStatus, EndTime: &completedEnd,
	}); err != nil {
		t.Fatal(err)
	}

	affected, err := repo.TimeoutTasks(ctx, []string{openA, openB, done})
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Errorf("affected = %d, want 2 (only the two open rows transition)", affected)
	}

	for _, id := range []string{openA, openB} {
		got, getErr := repo.GetByID(ctx, id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if got.Status == nil || *got.Status != "timeout" {
			t.Errorf("%s status = %v, want timeout", id, got.Status)
		}
		if got.EndTime == nil {
			t.Errorf("%s expected end_time to be set", id)
		}
	}

	gotDone, err := repo.GetByID(ctx, done)
	if err != nil {
		t.Fatal(err)
	}
	if gotDone.Status == nil || *gotDone.Status != "completed" {
		t.Errorf("finalized row status = %v, want completed (unchanged)", gotDone.Status)
	}
	if gotDone.EndTime == nil || *gotDone.EndTime != completedEnd {
		t.Errorf("finalized row end_time = %v, want %d (unchanged)", gotDone.EndTime, completedEnd)
	}

	if affected, err := repo.TimeoutTasks(ctx, nil); err != nil || affected != 0 {
		t.Errorf("TimeoutTasks(nil) = (%d, %v), want (0, nil)", affected, err)
	}
}
