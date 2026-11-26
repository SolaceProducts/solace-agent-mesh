// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
	"time"
)

func TestFeedbackRepo_DeleteOlderThan(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	sessRepo := NewSessionRepo(db)
	taskRepo := NewTaskRepo(db)
	ctx := context.Background()

	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", CreatedTime: time.Now().UnixMilli(), UpdatedTime: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground", SessionID: strPtr("00000000-0000-0000-0000-000000000001")}); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-48 * time.Hour)
	mustSaveFeedback(t, repo, &Feedback{ID: "40000000-0000-0000-0000-0000000000a1", SessionID: "00000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000001", UserID: "u1", Rating: "up", CreatedAt: old.UnixMilli(), UpdatedAt: old.UnixMilli()})
	mustSaveFeedback(t, repo, &Feedback{ID: "40000000-0000-0000-0000-0000000000a2", SessionID: "00000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000002", UserID: "u1", Rating: "down", CreatedAt: old.UnixMilli(), UpdatedAt: old.UnixMilli()})
	mustSaveFeedback(t, repo, &Feedback{ID: "40000000-0000-0000-0000-0000000000a3", SessionID: "00000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000003", UserID: "u1", Rating: "up", CreatedAt: old.UnixMilli(), UpdatedAt: old.UnixMilli()})
	mustSaveFeedback(t, repo, &Feedback{ID: "40000000-0000-0000-0000-0000000000a4", SessionID: "00000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000004", UserID: "u1", Rating: "up", CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli()})

	cutoff := time.Now().Add(-24 * time.Hour)
	deleted, err := repo.DeleteOlderThan(ctx, cutoff, 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}

	results, err := repo.Search(ctx, "u1", 0, 0, "", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("remaining = %d, want 1", len(results))
	}
}

func TestFeedbackRepo_DeleteOlderThan_None(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)

	deleted, err := repo.DeleteOlderThan(context.Background(), time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}

func TestProjectRepo_SoftDeleteSessionsByProject(t *testing.T) {
	db := newTestDB(t)
	projRepo := NewProjectRepo(db)
	sessRepo := NewSessionRepo(db)
	ctx := context.Background()

	if err := projRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Proj", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	pid := strPtr("30000000-0000-0000-0000-000000000001")
	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", ProjectID: pid, CreatedTime: time.Now().UnixMilli(), UpdatedTime: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "u1", ProjectID: pid, CreatedTime: time.Now().UnixMilli(), UpdatedTime: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}

	err := projRepo.SoftDeleteSessionsByProject(ctx, "30000000-0000-0000-0000-000000000001", "u1")
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := sessRepo.ListByProject(ctx, "30000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 non-deleted sessions, got %d", len(sessions))
	}
}

func TestSessionRepo_ListByProject(t *testing.T) {
	db := newTestDB(t)
	projRepo := NewProjectRepo(db)
	sessRepo := NewSessionRepo(db)
	ctx := context.Background()

	if err := projRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Proj", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	pid := strPtr("30000000-0000-0000-0000-000000000001")
	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", ProjectID: pid, CreatedTime: 100, UpdatedTime: 100}); err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "u1", ProjectID: pid, CreatedTime: 200, UpdatedTime: 200}); err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000003", UserID: "u1", CreatedTime: 300, UpdatedTime: 300}); err != nil {
		t.Fatal(err)
	}

	sessions, err := sessRepo.ListByProject(ctx, "30000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].ID != "00000000-0000-0000-0000-000000000001" || sessions[1].ID != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("unexpected order: %s, %s", sessions[0].ID, sessions[1].ID)
	}
}

func TestPromptGroupRepo_SetProductionPrompt(t *testing.T) {
	db := newTestDB(t)
	pgRepo := NewPromptGroupRepo(db)
	pRepo := NewPromptRepo(db)
	ctx := context.Background()

	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group", UserID: "u1"}); err != nil {
		t.Fatal("create prompt group:", err)
	}
	if err := pRepo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000001", PromptText: "hello", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 1}); err != nil {
		t.Fatal("create prompt:", err)
	}

	err := pgRepo.SetProductionPrompt(ctx, "50000000-0000-0000-0000-000000000001", "60000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}

	g, err := pgRepo.GetByID(ctx, "50000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		t.Fatal("expected prompt group, got nil")
	}
	if g.ProductionPromptID == nil || *g.ProductionPromptID != "60000000-0000-0000-0000-000000000001" {
		t.Errorf("production_prompt_id = %v, want 60000000-0000-0000-0000-000000000001", g.ProductionPromptID)
	}
}

func TestPromptGroupRepo_ListAllByUser(t *testing.T) {
	db := newTestDB(t)
	pgRepo := NewPromptGroupRepo(db)
	ctx := context.Background()

	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Mine", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000002", Name: "Shared", UserID: "u2"}); err != nil {
		t.Fatal(err)
	}
	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000003", Name: "Other", UserID: "u3"}); err != nil {
		t.Fatal(err)
	}

	groups, err := pgRepo.ListAllByUser(ctx, "u1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].ID != "50000000-0000-0000-0000-000000000001" {
		t.Errorf("expected [50000000-0000-0000-0000-000000000001], got %v", groupIDs(groups))
	}

	groups, err = pgRepo.ListAllByUser(ctx, "u1", []string{"50000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(groups))
	}
}

func TestPromptRepo_CountPrompts(t *testing.T) {
	db := newTestDB(t)
	pgRepo := NewPromptGroupRepo(db)
	pRepo := NewPromptRepo(db)
	ctx := context.Background()

	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := pRepo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000001", PromptText: "a", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := pRepo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000002", PromptText: "b", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 2}); err != nil {
		t.Fatal(err)
	}

	count, err := pRepo.CountPrompts(ctx, "50000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestPromptRepo_GetLatestExcluding(t *testing.T) {
	db := newTestDB(t)
	pgRepo := NewPromptGroupRepo(db)
	pRepo := NewPromptRepo(db)
	ctx := context.Background()

	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := pRepo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000001", PromptText: "first", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 1, CreatedAt: 100, UpdatedAt: 100}); err != nil {
		t.Fatal(err)
	}
	if err := pRepo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000002", PromptText: "second", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 2, CreatedAt: 200, UpdatedAt: 200}); err != nil {
		t.Fatal(err)
	}

	p, err := pRepo.GetLatestExcluding(ctx, "50000000-0000-0000-0000-000000000001", "60000000-0000-0000-0000-000000000002")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.ID != "60000000-0000-0000-0000-000000000001" {
		t.Errorf("expected 60000000-0000-0000-0000-000000000001, got %v", p)
	}

	p, err = pRepo.GetLatestExcluding(ctx, "50000000-0000-0000-0000-000000000001", "60000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.ID != "60000000-0000-0000-0000-000000000002" {
		t.Errorf("expected 60000000-0000-0000-0000-000000000002, got %v", p)
	}
}

func TestPromptRepo_ListCommandsByUser(t *testing.T) {
	db := newTestDB(t)
	pgRepo := NewPromptGroupRepo(db)
	pRepo := NewPromptRepo(db)
	ctx := context.Background()

	cmd1 := strPtr("/hello")
	cmd2 := strPtr("/world")
	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "G1", UserID: "u1", Command: cmd1}); err != nil {
		t.Fatal(err)
	}
	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000002", Name: "G2", UserID: "u1", Command: cmd2}); err != nil {
		t.Fatal(err)
	}
	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000003", Name: "G3", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000004", Name: "G4", UserID: "u2", Command: strPtr("/other")}); err != nil {
		t.Fatal(err)
	}

	cmds, err := pRepo.ListCommandsByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 {
		t.Errorf("expected 2 commands, got %d: %v", len(cmds), cmds)
	}
}

func TestSSEEventRepo_FindSequenceAfterTimestamp(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	sessRepo := NewSessionRepo(db)
	taskRepo := NewTaskRepo(db)
	ctx := context.Background()

	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", CreatedTime: 1, UpdatedTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: 1, ExecutionMode: "foreground", SessionID: strPtr("00000000-0000-0000-0000-000000000001")}); err != nil {
		t.Fatal(err)
	}

	for _, r := range []struct {
		seq       int
		createdAt int64
	}{
		{1, 100},
		{2, 200},
		{3, 300},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO sse_event_buffer (task_id, session_id, user_id, event_sequence, event_type, event_data, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"10000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000001", "u1", r.seq, "data", "{}", r.createdAt)
		if err != nil {
			t.Fatal(err)
		}
	}

	seq, err := repo.FindSequenceAfterTimestamp(ctx, "10000000-0000-0000-0000-000000000001", 150)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Errorf("seq = %d, want 2", seq)
	}

	seq, err = repo.FindSequenceAfterTimestamp(ctx, "10000000-0000-0000-0000-000000000001", 999)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Errorf("seq = %d, want 0", seq)
	}
}

func TestTaskRepo_GetByIDs(t *testing.T) {
	db := newTestDB(t)
	sessRepo := NewSessionRepo(db)
	taskRepo := NewTaskRepo(db)
	ctx := context.Background()

	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", CreatedTime: 1, UpdatedTime: 1}); err != nil {
		t.Fatal(err)
	}

	sid := strPtr("00000000-0000-0000-0000-000000000001")
	if err := taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: 1, ExecutionMode: "foreground", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	if err := taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000002", UserID: "u1", StartTime: 2, ExecutionMode: "foreground", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	if err := taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000003", UserID: "u1", StartTime: 3, ExecutionMode: "foreground", SessionID: sid}); err != nil {
		t.Fatal(err)
	}

	tasks, err := taskRepo.GetByIDs(ctx, []string{
		"10000000-0000-0000-0000-000000000001",
		"10000000-0000-0000-0000-000000000003",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	tasks, err = taskRepo.GetByIDs(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tasks != nil {
		t.Errorf("expected nil for empty input, got %d", len(tasks))
	}

	tasks, err = taskRepo.GetByIDs(ctx, []string{"ffffffff-ffff-ffff-ffff-ffffffffffff"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks for missing ID, got %d", len(tasks))
	}
}

func TestTaskRepo_HasActiveBackgroundTaskBatch(t *testing.T) {
	db := newTestDB(t)
	sessRepo := NewSessionRepo(db)
	taskRepo := NewTaskRepo(db)
	ctx := context.Background()

	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", CreatedTime: 1, UpdatedTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "u1", CreatedTime: 2, UpdatedTime: 2}); err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000003", UserID: "u1", CreatedTime: 3, UpdatedTime: 3}); err != nil {
		t.Fatal(err)
	}

	sid1 := strPtr("00000000-0000-0000-0000-000000000001")
	sid2 := strPtr("00000000-0000-0000-0000-000000000002")
	sid3 := strPtr("00000000-0000-0000-0000-000000000003")

	if err := taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: 1, ExecutionMode: "background", SessionID: sid1}); err != nil {
		t.Fatal(err)
	}
	endTime := int64(100)
	if err := taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000002", UserID: "u1", StartTime: 2, EndTime: &endTime, ExecutionMode: "background", SessionID: sid2}); err != nil {
		t.Fatal(err)
	}
	_ = sid3

	result, err := taskRepo.HasActiveBackgroundTaskBatch(ctx, []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result["00000000-0000-0000-0000-000000000001"] {
		t.Error("session 1 should have active background task")
	}
	if result["00000000-0000-0000-0000-000000000002"] {
		t.Error("session 2 should NOT have active background task (completed)")
	}
	if result["00000000-0000-0000-0000-000000000003"] {
		t.Error("session 3 should NOT have active background task (no tasks)")
	}

	result, err = taskRepo.HasActiveBackgroundTaskBatch(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

func TestChatTaskRepo_ListBySessionIDs(t *testing.T) {
	db := newTestDB(t)
	sessRepo := NewSessionRepo(db)
	ctRepo := NewChatTaskRepo(db)
	ctx := context.Background()

	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", CreatedTime: 1, UpdatedTime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "u1", CreatedTime: 2, UpdatedTime: 2}); err != nil {
		t.Fatal(err)
	}

	if err := ctRepo.Create(ctx, &ChatTask{ID: "20000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1", UserMessage: strPtr("a")}); err != nil {
		t.Fatal(err)
	}
	if err := ctRepo.Create(ctx, &ChatTask{ID: "20000000-0000-0000-0000-000000000002", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1", UserMessage: strPtr("b")}); err != nil {
		t.Fatal(err)
	}
	if err := ctRepo.Create(ctx, &ChatTask{ID: "20000000-0000-0000-0000-000000000003", SessionID: "00000000-0000-0000-0000-000000000002", UserID: "u1", UserMessage: strPtr("c")}); err != nil {
		t.Fatal(err)
	}

	result, err := ctRepo.ListBySessionIDs(ctx, []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result["00000000-0000-0000-0000-000000000001"]) != 2 {
		t.Errorf("session 1: expected 2 chat tasks, got %d", len(result["00000000-0000-0000-0000-000000000001"]))
	}
	if len(result["00000000-0000-0000-0000-000000000002"]) != 1 {
		t.Errorf("session 2: expected 1 chat task, got %d", len(result["00000000-0000-0000-0000-000000000002"]))
	}

	result, err = ctRepo.ListBySessionIDs(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

func TestProjectRepo_GetByIDs(t *testing.T) {
	db := newTestDB(t)
	projRepo := NewProjectRepo(db)
	ctx := context.Background()

	if err := projRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Active1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := projRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000002", Name: "Active2", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := projRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000003", Name: "Deleted", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := projRepo.SoftDelete(ctx, "30000000-0000-0000-0000-000000000003", "u1"); err != nil {
		t.Fatal(err)
	}

	projects, err := projRepo.GetByIDs(ctx, []string{
		"30000000-0000-0000-0000-000000000001",
		"30000000-0000-0000-0000-000000000002",
		"30000000-0000-0000-0000-000000000003",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 non-deleted projects, got %d", len(projects))
	}

	projects, err = projRepo.GetByIDs(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if projects != nil {
		t.Errorf("expected nil for empty input, got %d", len(projects))
	}
}

func TestPromptRepo_GetByIDs(t *testing.T) {
	db := newTestDB(t)
	pgRepo := NewPromptGroupRepo(db)
	pRepo := NewPromptRepo(db)
	ctx := context.Background()

	if err := pgRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := pRepo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000001", PromptText: "a", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := pRepo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000002", PromptText: "b", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 2}); err != nil {
		t.Fatal(err)
	}
	if err := pRepo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000003", PromptText: "c", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 3}); err != nil {
		t.Fatal(err)
	}

	prompts, err := pRepo.GetByIDs(ctx, []string{
		"60000000-0000-0000-0000-000000000001",
		"60000000-0000-0000-0000-000000000003",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(prompts))
	}

	prompts, err = pRepo.GetByIDs(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prompts != nil {
		t.Errorf("expected nil for empty input, got %d", len(prompts))
	}

	prompts, err = pRepo.GetByIDs(ctx, []string{"ffffffff-ffff-ffff-ffff-ffffffffffff"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 0 {
		t.Errorf("expected 0 prompts for missing ID, got %d", len(prompts))
	}
}

func strPtr(s string) *string { return &s }

func groupIDs(groups []*PromptGroup) []string {
	ids := make([]string, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	return ids
}
