// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestParseDataRetentionConfig_Defaults(t *testing.T) {
	cfg := ParseDataRetentionConfig(nil)

	if !cfg.Enabled {
		t.Error("Enabled should default to true")
	}
	if cfg.TaskRetentionDays != 90 {
		t.Errorf("TaskRetentionDays = %d, want 90", cfg.TaskRetentionDays)
	}
	if cfg.FeedbackRetentionDays != 90 {
		t.Errorf("FeedbackRetentionDays = %d, want 90", cfg.FeedbackRetentionDays)
	}
	if cfg.SSEEventRetentionDays != 7 {
		t.Errorf("SSEEventRetentionDays = %d, want 7", cfg.SSEEventRetentionDays)
	}
	if !cfg.CleanupSSEEvents {
		t.Error("CleanupSSEEvents should default to true")
	}
	if cfg.CleanupIntervalHours != 24 {
		t.Errorf("CleanupIntervalHours = %d, want 24", cfg.CleanupIntervalHours)
	}
	if cfg.BatchSize != 1000 {
		t.Errorf("BatchSize = %d, want 1000", cfg.BatchSize)
	}
	if cfg.SSEConsumedRetentionHours != 24 {
		t.Errorf("SSEConsumedRetentionHours = %d, want 24", cfg.SSEConsumedRetentionHours)
	}
	if cfg.SSECompletedTaskRetentionDays != 7 {
		t.Errorf("SSECompletedTaskRetentionDays = %d, want 7", cfg.SSECompletedTaskRetentionDays)
	}
	if cfg.TaskEventDeltaRetentionDays != 7 {
		t.Errorf("TaskEventDeltaRetentionDays = %d, want 7", cfg.TaskEventDeltaRetentionDays)
	}
	if cfg.SSEEventRetentionSeconds != 0 {
		t.Errorf("SSEEventRetentionSeconds should default to 0 (disabled), got %d", cfg.SSEEventRetentionSeconds)
	}
	if cfg.CleanupIntervalSeconds != 0 {
		t.Errorf("CleanupIntervalSeconds should default to 0 (disabled), got %d", cfg.CleanupIntervalSeconds)
	}
}

func TestParseDataRetentionConfig_SSEWindowsOverride(t *testing.T) {
	cfg := ParseDataRetentionConfig(map[string]any{
		"sse_consumed_retention_hours":         6,
		"sse_completed_task_retention_days":    3,
		"sse_consumed_retention_seconds":       2,
		"sse_completed_task_retention_seconds": 1,
		"task_event_delta_retention_days":      4,
		"task_event_delta_retention_seconds":   5,
	})

	if cfg.SSEConsumedRetentionHours != 6 {
		t.Errorf("SSEConsumedRetentionHours = %d, want 6", cfg.SSEConsumedRetentionHours)
	}
	if cfg.SSECompletedTaskRetentionDays != 3 {
		t.Errorf("SSECompletedTaskRetentionDays = %d, want 3", cfg.SSECompletedTaskRetentionDays)
	}
	if cfg.SSEConsumedRetentionSeconds != 2 {
		t.Errorf("SSEConsumedRetentionSeconds = %d, want 2", cfg.SSEConsumedRetentionSeconds)
	}
	if cfg.SSECompletedTaskRetentionSeconds != 1 {
		t.Errorf("SSECompletedTaskRetentionSeconds = %d, want 1", cfg.SSECompletedTaskRetentionSeconds)
	}
	if cfg.TaskEventDeltaRetentionDays != 4 {
		t.Errorf("TaskEventDeltaRetentionDays = %d, want 4", cfg.TaskEventDeltaRetentionDays)
	}
	if cfg.TaskEventDeltaRetentionSeconds != 5 {
		t.Errorf("TaskEventDeltaRetentionSeconds = %d, want 5", cfg.TaskEventDeltaRetentionSeconds)
	}
}

func TestParseDataRetentionConfig_Override(t *testing.T) {
	cfg := ParseDataRetentionConfig(map[string]any{
		"enabled":                  false,
		"task_retention_days":      30,
		"feedback_retention_days":  60,
		"sse_event_retention_days": 7,
		"cleanup_sse_events":       false,
		"cleanup_interval_hours":   12,
		"batch_size":               500,
	})

	if cfg.Enabled {
		t.Error("Enabled should be false")
	}
	if cfg.TaskRetentionDays != 30 {
		t.Errorf("TaskRetentionDays = %d, want 30", cfg.TaskRetentionDays)
	}
	if cfg.FeedbackRetentionDays != 60 {
		t.Errorf("FeedbackRetentionDays = %d, want 60", cfg.FeedbackRetentionDays)
	}
	if cfg.SSEEventRetentionDays != 7 {
		t.Errorf("SSEEventRetentionDays = %d, want 7", cfg.SSEEventRetentionDays)
	}
	if cfg.CleanupSSEEvents {
		t.Error("CleanupSSEEvents should be false")
	}
	if cfg.CleanupIntervalHours != 12 {
		t.Errorf("CleanupIntervalHours = %d, want 12", cfg.CleanupIntervalHours)
	}
	if cfg.BatchSize != 500 {
		t.Errorf("BatchSize = %d, want 500", cfg.BatchSize)
	}
}

func TestParseDataRetentionConfig_SecondsOverrides(t *testing.T) {
	cfg := ParseDataRetentionConfig(map[string]any{
		"sse_event_retention_seconds": 2,
		"cleanup_interval_seconds":    1,
	})

	if cfg.SSEEventRetentionSeconds != 2 {
		t.Errorf("SSEEventRetentionSeconds = %d, want 2", cfg.SSEEventRetentionSeconds)
	}
	if cfg.CleanupIntervalSeconds != 1 {
		t.Errorf("CleanupIntervalSeconds = %d, want 1", cfg.CleanupIntervalSeconds)
	}
}

func TestParseDataRetentionConfig_EmptyMap(t *testing.T) {
	cfg := ParseDataRetentionConfig(map[string]any{})

	if !cfg.Enabled {
		t.Error("Enabled should default to true with empty map")
	}
	if cfg.TaskRetentionDays != 90 {
		t.Errorf("TaskRetentionDays = %d, want 90", cfg.TaskRetentionDays)
	}
}

func TestDataRetentionService_Cleanup_SSEEvents(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)

	sseRepo := store.NewSSEEventRepo(db)

	oldTask := "10000000-0000-0000-0000-000000000001"
	freshTask := "10000000-0000-0000-0000-000000000002"
	session := "00000000-0000-0000-0000-000000000001"

	for i := 1; i <= 2; i++ {
		if err := sseRepo.Append(ctx, &store.SSEEventBufferRow{
			TaskID: oldTask, SessionID: session, UserID: "u1",
			EventSequence: i, EventType: "status_update", EventData: "{}",
		}); err != nil {
			t.Fatalf("append aged: %v", err)
		}
	}
	if err := sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: freshTask, SessionID: session, UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: "{}",
	}); err != nil {
		t.Fatalf("append fresh: %v", err)
	}

	aged := time.Now().Add(-10 * 24 * time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET created_at = ? WHERE task_id = ?`,
		aged, oldTask); err != nil {
		t.Fatalf("age rows: %v", err)
	}

	cfg := DataRetentionConfig{
		Enabled:               true,
		TaskRetentionDays:     90,
		FeedbackRetentionDays: 90,
		SSEEventRetentionDays: 7,
		CleanupSSEEvents:      true,
		CleanupIntervalHours:  24,
		BatchSize:             100,
	}
	svc := NewDataRetentionService(cfg, store.NewTaskRepo(db), store.NewFeedbackRepo(db), sseRepo, store.NewTaskEventRepo(db), store.NewWhatsAppProcessedRepo(db), slog.Default())

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	remainingOld, _ := sseRepo.GetByTask(ctx, oldTask, 1)
	if len(remainingOld) != 0 {
		t.Errorf("aged rows not deleted; got %d remaining", len(remainingOld))
	}
	remainingFresh, _ := sseRepo.GetByTask(ctx, freshTask, 1)
	if len(remainingFresh) != 1 {
		t.Errorf("fresh row should survive; got %d remaining", len(remainingFresh))
	}
}

func TestDataRetentionService_Cleanup_SSELifecycle(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	sseRepo := store.NewSSEEventRepo(db)
	taskRepo := store.NewTaskRepo(db)

	const (
		consumedTask = "10000000-0000-0000-0000-000000000001"
		endedTask    = "10000000-0000-0000-0000-000000000002"
		runningTask  = "10000000-0000-0000-0000-000000000003"
	)
	const session = "00000000-0000-0000-0000-000000000001"
	now := time.Now().UnixMilli()
	endedAt := now - 10_000
	endedLongAgo := now - 2*60*60*1000

	mkTask := func(id string, endTime *int64) {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: "u1", StartTime: now - 20_000, ExecutionMode: "foreground", EndTime: endTime,
		}); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
	}
	appendRows := func(id string, n int) {
		for i := 1; i <= n; i++ {
			if err := sseRepo.Append(ctx, &store.SSEEventBufferRow{
				TaskID: id, SessionID: session, UserID: "u1",
				EventSequence: i, EventType: "status_update", EventData: "{}",
			}); err != nil {
				t.Fatalf("append %s/%d: %v", id, i, err)
			}
		}
	}

	mkTask(consumedTask, &endedAt)
	appendRows(consumedTask, 2)
	if err := sseRepo.MarkConsumed(ctx, consumedTask); err != nil {
		t.Fatal(err)
	}

	mkTask(endedTask, &endedLongAgo)
	appendRows(endedTask, 2)

	mkTask(runningTask, nil)
	appendRows(runningTask, 2)
	if err := sseRepo.MarkConsumed(ctx, runningTask); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE sse_task_consumed SET consumed_at = ?`, now-10_000); err != nil {
		t.Fatal(err)
	}

	cfg := DataRetentionConfig{
		Enabled:                          true,
		TaskRetentionDays:                90,
		FeedbackRetentionDays:            90,
		SSEEventRetentionDays:            30,
		CleanupSSEEvents:                 true,
		CleanupIntervalHours:             24,
		BatchSize:                        100,
		SSEConsumedRetentionSeconds:      1,
		SSECompletedTaskRetentionSeconds: 3600,
	}
	svc := NewDataRetentionService(cfg, taskRepo, store.NewFeedbackRepo(db), sseRepo, store.NewTaskEventRepo(db), store.NewWhatsAppProcessedRepo(db), slog.Default())

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	assertRows := func(id string, want int) {
		t.Helper()
		rows, _ := sseRepo.GetByTask(ctx, id, 1)
		if len(rows) != want {
			t.Errorf("task %s remaining rows = %d, want %d", id, len(rows), want)
		}
	}
	assertRows(consumedTask, 0)
	assertRows(endedTask, 0)
	assertRows(runningTask, 2)

	assertMarks := func(id string, want int) {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sse_task_consumed WHERE task_id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("task %s consumed marks = %d, want %d", id, n, want)
		}
	}
	assertMarks(consumedTask, 0)
	assertMarks(runningTask, 1)
}

func TestDataRetentionService_Cleanup_SSEBackstopKeepsRunningTaskHistory(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	sseRepo := store.NewSSEEventRepo(db)
	taskRepo := store.NewTaskRepo(db)

	const (
		runningTask = "10000000-0000-0000-0000-000000000001"
		idleTask    = "10000000-0000-0000-0000-000000000002"
		session     = "00000000-0000-0000-0000-000000000001"
	)
	now := time.Now()
	for _, id := range []string{runningTask, idleTask} {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: "u1", StartTime: now.Add(-10 * 24 * time.Hour).UnixMilli(), ExecutionMode: "foreground",
		}); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
		for seq := 1; seq <= 3; seq++ {
			if err := sseRepo.Append(ctx, &store.SSEEventBufferRow{
				TaskID: id, SessionID: session, UserID: "u1",
				EventSequence: seq, EventType: "status_update", EventData: "{}",
			}); err != nil {
				t.Fatalf("append %s/%d: %v", id, seq, err)
			}
		}
	}
	old := now.Add(-10 * 24 * time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET created_at = ? WHERE task_id = ? AND event_sequence <= 2`, old, runningTask); err != nil {
		t.Fatalf("age running task rows: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET created_at = ? WHERE task_id = ?`, old, idleTask); err != nil {
		t.Fatalf("age idle task rows: %v", err)
	}

	cfg := DataRetentionConfig{
		Enabled: true, TaskRetentionDays: 90, FeedbackRetentionDays: 90, SSEEventRetentionDays: 7,
		CleanupSSEEvents: true, CleanupIntervalHours: 24, BatchSize: 100,
	}
	svc := NewDataRetentionService(cfg, taskRepo, store.NewFeedbackRepo(db), sseRepo, store.NewTaskEventRepo(db), store.NewWhatsAppProcessedRepo(db), slog.Default())
	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if rows, _ := sseRepo.GetByTask(ctx, runningTask, 1); len(rows) != 3 {
		t.Errorf("running task kept %d of 3 rows, want all 3 (a replay would start with a gap)", len(rows))
	}
	if rows, _ := sseRepo.GetByTask(ctx, idleTask, 1); len(rows) != 0 {
		t.Errorf("idle task kept %d rows, want 0", len(rows))
	}
}

func TestDataRetentionService_Cleanup_TaskEventDeltas(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const taskID = "10000000-0000-0000-0000-000000000001"
	now := time.Now().UnixMilli()
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "u1", StartTime: now, ExecutionMode: "foreground",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	agedDelta := &store.TaskEvent{ID: "b0000000-0000-0000-0000-000000000001", TaskID: taskID, CreatedTime: now, Topic: "t", Direction: "status", Payload: "{}", IsDelta: true}
	agedStructural := &store.TaskEvent{ID: "b0000000-0000-0000-0000-000000000002", TaskID: taskID, CreatedTime: now, Topic: "t", Direction: "status", Payload: "{}", IsDelta: false}
	freshDelta := &store.TaskEvent{ID: "b0000000-0000-0000-0000-000000000003", TaskID: taskID, CreatedTime: now, Topic: "t", Direction: "status", Payload: "{}", IsDelta: true}
	for _, e := range []*store.TaskEvent{agedDelta, agedStructural, freshDelta} {
		if err := eventRepo.Create(ctx, e); err != nil {
			t.Fatalf("create event %s: %v", e.ID, err)
		}
	}

	aged := now - 10_000
	if _, err := db.ExecContext(ctx,
		`UPDATE task_events SET created_time = ? WHERE id IN (?, ?)`,
		aged, agedDelta.ID, agedStructural.ID); err != nil {
		t.Fatalf("age rows: %v", err)
	}

	cfg := DataRetentionConfig{
		Enabled:                        true,
		TaskRetentionDays:              90,
		FeedbackRetentionDays:          90,
		SSEEventRetentionDays:          30,
		CleanupSSEEvents:               true,
		CleanupIntervalHours:           24,
		BatchSize:                      100,
		TaskEventDeltaRetentionSeconds: 1,
	}
	svc := NewDataRetentionService(cfg, taskRepo, store.NewFeedbackRepo(db), store.NewSSEEventRepo(db), eventRepo, store.NewWhatsAppProcessedRepo(db), slog.Default())

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	remaining, err := eventRepo.GetByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("get remaining: %v", err)
	}
	got := make(map[string]bool, len(remaining))
	for _, e := range remaining {
		got[e.ID] = true
	}
	if got[agedDelta.ID] {
		t.Error("aged delta row should have been deleted")
	}
	if !got[agedStructural.ID] {
		t.Error("aged structural row should have survived the delta sweep")
	}
	if !got[freshDelta.ID] {
		t.Error("fresh delta row should have survived")
	}
}

const (
	expiredBlobTask = "10000000-0000-0000-0000-0000000000e1"
	liveBlobTask    = "10000000-0000-0000-0000-0000000000e2"
)

func newBlobRetentionFixture(t *testing.T, db *store.DB) (*DataRetentionService, *store.TaskRepo, *store.TaskEventRepo) {
	t.Helper()
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	now := time.Now().UnixMilli()
	old := now - 100*24*60*60*1000
	for _, tk := range []*store.Task{
		{ID: expiredBlobTask, UserID: "u1", StartTime: old, ExecutionMode: "foreground"},
		{ID: liveBlobTask, UserID: "u1", StartTime: now, ExecutionMode: "foreground"},
	} {
		if err := taskRepo.Create(ctx, tk); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}
	blobs := []*store.TaskEventBlob{
		{Hash: "sha256:expired-only", Kind: "system_instruction", Content: "a", ByteLen: 1, FirstSeen: old, LastSeen: old},
		{Hash: "sha256:still-live", Kind: "tools", Content: "b", ByteLen: 1, FirstSeen: old, LastSeen: old},
		{Hash: "sha256:fresh-orphan", Kind: "tools", Content: "c", ByteLen: 1, FirstSeen: now, LastSeen: now},
	}
	if err := eventRepo.Blobs().UpsertBatch(ctx, blobs); err != nil {
		t.Fatalf("upsert blobs: %v", err)
	}
	expiredRef, liveRef := "sha256:expired-only", "sha256:still-live"
	for _, e := range []*store.TaskEvent{
		{ID: "b0000000-0000-0000-0000-0000000000e1", TaskID: expiredBlobTask, CreatedTime: old, Topic: "t", Direction: "status", Payload: "{}", ConfigPromptBlob: &expiredRef},
		{ID: "b0000000-0000-0000-0000-0000000000e2", TaskID: liveBlobTask, CreatedTime: now, Topic: "t", Direction: "status", Payload: "{}", ConfigToolsBlob: &liveRef},
	} {
		if err := eventRepo.Create(ctx, e); err != nil {
			t.Fatalf("create event %s: %v", e.ID, err)
		}
	}

	cfg := DataRetentionConfig{
		Enabled: true, TaskRetentionDays: 90, FeedbackRetentionDays: 90, SSEEventRetentionDays: 30,
		CleanupIntervalHours: 24, BatchSize: 100, TaskEventDeltaRetentionDays: 7,
	}
	svc := NewDataRetentionService(cfg, taskRepo, store.NewFeedbackRepo(db), store.NewSSEEventRepo(db), eventRepo, store.NewWhatsAppProcessedRepo(db), slog.Default())
	return svc, taskRepo, eventRepo
}

func TestDataRetentionService_Cleanup_TaskEventBlobs(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	svc, taskRepo, eventRepo := newBlobRetentionFixture(t, db)
	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("build the blob reference indexes: %v", err)
	}

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if got, err := taskRepo.GetByID(ctx, expiredBlobTask); err != nil || got != nil {
		t.Fatalf("expired task should be gone: task=%v err=%v", got, err)
	}
	left, err := eventRepo.Blobs().GetContentByHashes(ctx, []string{"sha256:expired-only", "sha256:still-live", "sha256:fresh-orphan"})
	if err != nil {
		t.Fatalf("get blobs: %v", err)
	}
	if _, ok := left["sha256:expired-only"]; ok {
		t.Error("blob referenced only by the expired task should have been swept")
	}
	if _, ok := left["sha256:still-live"]; !ok {
		t.Error("blob referenced by a live task should have survived")
	}
	if _, ok := left["sha256:fresh-orphan"]; !ok {
		t.Error("unreferenced blob seen inside the guard should have survived")
	}
}

func TestDataRetentionService_Cleanup_TaskEventBlobsWaitForReferenceIndexes(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("SQLite builds the reference indexes in its migrations")
	}
	svc, _, eventRepo := newBlobRetentionFixture(t, db)

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	left, err := eventRepo.Blobs().GetContentByHashes(ctx, []string{"sha256:expired-only"})
	if err != nil {
		t.Fatalf("get blobs: %v", err)
	}
	if _, ok := left["sha256:expired-only"]; !ok {
		t.Error("the sweep ran before its reference indexes were built; each probe would scan task_events")
	}
}

func TestDataRetentionService_Cleanup_SSEEvents_Disabled(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	sseRepo := store.NewSSEEventRepo(db)

	oldTask := "10000000-0000-0000-0000-000000000001"
	if err := sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: oldTask, SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-60 * 24 * time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET created_at = ? WHERE task_id = ?`, aged, oldTask); err != nil {
		t.Fatal(err)
	}

	cfg := DataRetentionConfig{
		Enabled:               true,
		TaskRetentionDays:     90,
		FeedbackRetentionDays: 90,
		SSEEventRetentionDays: 30,
		CleanupSSEEvents:      false,
		CleanupIntervalHours:  24,
		BatchSize:             100,
	}
	svc := NewDataRetentionService(cfg, store.NewTaskRepo(db), store.NewFeedbackRepo(db), sseRepo, store.NewTaskEventRepo(db), store.NewWhatsAppProcessedRepo(db), slog.Default())

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	remaining, _ := sseRepo.GetByTask(ctx, oldTask, 1)
	if len(remaining) != 1 {
		t.Errorf("sse cleanup disabled: row should survive; got %d remaining", len(remaining))
	}
}

func TestDataRetentionService_Cleanup_WhatsAppDedupClaims(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	repo := store.NewWhatsAppProcessedRepo(db)

	for _, row := range []struct {
		wamid  string
		ageDay int
	}{
		{"wamid.PAST-HORIZON", 9},
		{"wamid.INSIDE-HORIZON", 6},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO whatsapp_processed_messages (gateway_id, wamid, state, claimed_at, processed_at)
			 VALUES (?, ?, ?, ?, ?)`,
			"gw-wa", row.wamid, store.WhatsAppMessageDone,
			time.Now().Add(-time.Duration(row.ageDay)*24*time.Hour).UnixMilli(),
			time.Now().Add(-time.Duration(row.ageDay)*24*time.Hour).UnixMilli(),
		); err != nil {
			t.Fatalf("seed %s: %v", row.wamid, err)
		}
	}

	cfg := DataRetentionConfig{
		Enabled:               true,
		TaskRetentionDays:     90,
		FeedbackRetentionDays: 90,
		CleanupSSEEvents:      false,
		CleanupIntervalHours:  24,
		BatchSize:             100,
	}
	svc := NewDataRetentionService(cfg, store.NewTaskRepo(db), store.NewFeedbackRepo(db),
		store.NewSSEEventRepo(db), store.NewTaskEventRepo(db), repo, slog.Default())

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if state, err := repo.State(ctx, "gw-wa", "wamid.PAST-HORIZON"); err != nil || state != "" {
		t.Errorf("claim past Meta's redelivery horizon = %q (err %v), want it pruned", state, err)
	}
	if state, err := repo.State(ctx, "gw-wa", "wamid.INSIDE-HORIZON"); err != nil || state != store.WhatsAppMessageDone {
		t.Errorf("claim inside Meta's redelivery horizon = %q (err %v), want it kept", state, err)
	}
}

func TestWhatsAppDedupRetentionClearsMetaHorizon(t *testing.T) {
	const metaRedeliveryHorizon = 7 * 24 * time.Hour
	if WhatsAppClaimRetention <= metaRedeliveryHorizon {
		t.Fatalf("WhatsAppClaimRetention = %v, must exceed Meta's %v redelivery horizon",
			WhatsAppClaimRetention, metaRedeliveryHorizon)
	}
}

func TestDataRetentionService_Cleanup_WhatsAppOrphanedPrompts(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	prompts := store.NewWhatsAppPendingPromptRepo(db)

	seed := func(sender string, expiresAt time.Time) {
		t.Helper()
		now := time.Now().UnixMilli()
		err := db.WithTx(ctx, func(tx *dbutil.Tx) error {
			_, upsertErr := prompts.Upsert(ctx, tx, &store.WhatsAppPendingPrompt{
				GatewayID: "gw-decommissioned", SenderID: sender,
				TaskID: "task-" + sender, RequestID: "req-" + sender,
				SurfaceJSON: `{"surfaceId":"s"}`, ExpiresAt: expiresAt.UnixMilli(),
				CreatedAt: now, UpdatedAt: now,
			})
			return upsertErr
		})
		if err != nil {
			t.Fatalf("seed %s: %v", sender, err)
		}
	}
	seed("orphan", time.Now().Add(-90*24*time.Hour))
	seed("recent", time.Now().Add(time.Hour))

	cfg := DataRetentionConfig{
		Enabled: true, TaskRetentionDays: 90, FeedbackRetentionDays: 90,
		CleanupSSEEvents: false, CleanupIntervalHours: 24, BatchSize: 100,
		WhatsAppOutboundRetentionDays: 30,
	}
	svc := NewDataRetentionService(cfg, store.NewTaskRepo(db), store.NewFeedbackRepo(db),
		store.NewSSEEventRepo(db), store.NewTaskEventRepo(db), nil, slog.Default()).
		WithWhatsAppOutbound(store.NewWhatsAppOutboundRepo(db), store.NewWhatsAppConversationRepo(db), prompts)

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if row, err := prompts.Get(ctx, "gw-decommissioned", "orphan"); err != nil || row != nil {
		t.Errorf("orphaned prompt = %v (err %v), want it pruned: no live gateway will ever claim it", row, err)
	}
	if row, err := prompts.Get(ctx, "gw-decommissioned", "recent"); err != nil || row == nil {
		t.Errorf("a live prompt = %v (err %v), want it kept", row, err)
	}
}

func TestDataRetentionService_Cleanup_WhatsAppOutboundRunsWithRetentionDisabled(t *testing.T) {
	db := store.TestDB(t)
	ctx := context.Background()
	outbound := store.NewWhatsAppOutboundRepo(db)

	old := time.Now().Add(-90 * 24 * time.Hour).UnixMilli()
	settled := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: "gw", Recipient: "15551234567",
		Kind: "text", Payload: `{"text":"long gone"}`,
		State: store.WhatsAppOutboundFailed, CreatedAt: old, UpdatedAt: old,
	}
	unsettled := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: "gw", Recipient: "15559999999",
		Kind: "text", Payload: `{"text":"orphaned"}`,
		State: store.WhatsAppOutboundQueued, CreatedAt: old, UpdatedAt: old,
	}
	for _, row := range []*store.WhatsAppOutbound{settled, unsettled} {
		if err := outbound.Enqueue(ctx, nil, row); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	cfg := ParseDataRetentionConfig(map[string]any{"enabled": false})
	svc := NewDataRetentionService(cfg, store.NewTaskRepo(db), store.NewFeedbackRepo(db),
		store.NewSSEEventRepo(db), store.NewTaskEventRepo(db), store.NewWhatsAppProcessedRepo(db),
		slog.Default()).WithWhatsAppOutbound(outbound, store.NewWhatsAppConversationRepo(db), store.NewWhatsAppPendingPromptRepo(db))

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	for _, row := range []*store.WhatsAppOutbound{settled, unsettled} {
		got, err := outbound.Get(ctx, row.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got != nil {
			t.Errorf("row %s (%s) survived the sweep; retention being off must not keep this data", row.ID, row.State)
		}
	}
}

func TestDataRetentionService_Cleanup_DisabledRetentionKeepsTheDataItGoverns(t *testing.T) {
	db := store.TestDB(t)
	ctx := context.Background()
	taskRepo := store.NewTaskRepo(db)
	feedbackRepo := store.NewFeedbackRepo(db)
	outbound := store.NewWhatsAppOutboundRepo(db)

	const taskID = "20000000-0000-0000-0000-000000000001"
	const sessionID = "40000000-0000-0000-0000-000000000001"
	ancient := time.Now().Add(-365 * 24 * time.Hour).UnixMilli()
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "u1", StartTime: ancient, ExecutionMode: "foreground",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	store.SeedFeedback(t, db, &store.Feedback{
		ID: "30000000-0000-0000-0000-000000000001", TaskID: taskID, UserID: "u1",
		SessionID: sessionID, Rating: "up", SubjectID: "agent-1", SubjectType: "agent",
		EntrypointType: "webui", CreatedAt: ancient, UpdatedAt: ancient,
	})
	sseRepo := store.NewSSEEventRepo(db)
	if err := sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskID, SessionID: sessionID, UserID: "u1", EventSequence: 1,
		EventType: "status", EventData: "{}", CreatedAt: ancient,
	}); err != nil {
		t.Fatalf("append sse event: %v", err)
	}
	waRow := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: "gw", Recipient: "15551234567",
		Kind: "text", Payload: `{"text":"gone"}`,
		State: store.WhatsAppOutboundSent, CreatedAt: ancient, UpdatedAt: ancient,
	}
	if err := outbound.Enqueue(ctx, nil, waRow); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	cfg := ParseDataRetentionConfig(map[string]any{"enabled": false})
	svc := NewDataRetentionService(cfg, taskRepo, feedbackRepo,
		sseRepo, store.NewTaskEventRepo(db), store.NewWhatsAppProcessedRepo(db),
		slog.Default()).WithWhatsAppOutbound(outbound, store.NewWhatsAppConversationRepo(db), store.NewWhatsAppPendingPromptRepo(db))

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if got, err := outbound.Get(ctx, waRow.ID); err != nil {
		t.Fatalf("Get outbound: %v", err)
	} else if got != nil {
		t.Fatal("the whatsapp sweep did not run, so this test proves nothing about the switch")
	}
	if task, err := taskRepo.GetByID(ctx, taskID); err != nil {
		t.Fatalf("GetByID: %v", err)
	} else if task == nil {
		t.Error("a year-old task was deleted with retention disabled; the switch is what says not to")
	}
	if items, err := feedbackRepo.Search(ctx, "u1", 0, 0, taskID, "", "", 10, 0); err != nil {
		t.Fatalf("Search: %v", err)
	} else if len(items) == 0 {
		t.Error("year-old feedback was deleted with retention disabled")
	}
	if events, err := sseRepo.GetByTask(ctx, taskID, 0); err != nil {
		t.Fatalf("GetByTask: %v", err)
	} else if len(events) == 0 {
		t.Error("year-old SSE buffer rows were deleted with retention disabled")
	}
}

func TestDataRetentionService_Cleanup_DedupSweepIgnoresTheRetentionSwitch(t *testing.T) {
	db := store.TestDB(t)
	ctx := context.Background()
	dedup := store.NewWhatsAppProcessedRepo(db)

	stale := time.Now().Add(-2 * WhatsAppClaimRetention)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO whatsapp_processed_messages (gateway_id, wamid, claimed_at, state, attempts)
		 VALUES (?, ?, ?, ?, ?)`,
		"gw", "wamid.ANCIENT", stale.UnixMilli(), "completed", 1); err != nil {
		t.Fatalf("seed dedup claim: %v", err)
	}

	cfg := ParseDataRetentionConfig(map[string]any{"enabled": false})
	svc := NewDataRetentionService(cfg, store.NewTaskRepo(db), store.NewFeedbackRepo(db),
		store.NewSSEEventRepo(db), store.NewTaskEventRepo(db), dedup,
		slog.Default())
	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	var remaining int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM whatsapp_processed_messages WHERE wamid = ?`, "wamid.ANCIENT").Scan(&remaining); err != nil {
		t.Fatalf("count dedup claims: %v", err)
	}
	if remaining != 0 {
		t.Error("a dedup claim past Meta's redelivery horizon survived; that sweep does not honour the switch")
	}
}
