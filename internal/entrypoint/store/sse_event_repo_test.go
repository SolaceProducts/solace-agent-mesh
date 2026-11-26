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

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestSSEEventRepo_AppendAndGetByTask(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		err := repo.Append(ctx, &SSEEventBufferRow{
			TaskID:        "10000000-0000-0000-0000-000000000001",
			SessionID:     "00000000-0000-0000-0000-000000000001",
			UserID:        "u1",
			EventSequence: i,
			EventType:     "status_update",
			EventData:     `{"seq":` + string(rune('0'+i)) + `}`,
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	rows, err := repo.GetByTask(ctx, "10000000-0000-0000-0000-000000000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 events, got %d", len(rows))
	}
	if rows[0].EventSequence != 1 || rows[2].EventSequence != 3 {
		t.Errorf("sequences: %d, %d, %d", rows[0].EventSequence, rows[1].EventSequence, rows[2].EventSequence)
	}
}

func TestSSEEventRepo_GetByTaskWithFromSeq(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		_ = repo.Append(ctx, &SSEEventBufferRow{
			TaskID: "10000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
			EventSequence: i, EventType: "status_update", EventData: "{}",
		})
	}

	rows, err := repo.GetByTask(ctx, "10000000-0000-0000-0000-000000000001", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 events (seq 3,4,5), got %d", len(rows))
	}
	if rows[0].EventSequence != 3 {
		t.Errorf("first event seq=%d, want 3", rows[0].EventSequence)
	}
}

func TestSSEEventRepo_GetMaxSequence(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	maxSeq, err := repo.GetMaxSequence(ctx, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if maxSeq != 0 {
		t.Errorf("expected 0, got %d", maxSeq)
	}

	for i := 1; i <= 5; i++ {
		_ = repo.Append(ctx, &SSEEventBufferRow{
			TaskID: "10000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
			EventSequence: i, EventType: "status_update", EventData: "{}",
		})
	}

	maxSeq, err = repo.GetMaxSequence(ctx, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if maxSeq != 5 {
		t.Errorf("expected 5, got %d", maxSeq)
	}
}

func TestSSEEventRepo_MarkConsumed(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	_ = repo.Append(ctx, &SSEEventBufferRow{
		TaskID: "10000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: "{}",
	})

	if err := repo.MarkConsumed(ctx, "10000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}

	rows, _ := repo.GetByTask(ctx, "10000000-0000-0000-0000-000000000001", 1)
	if len(rows) != 1 {
		t.Fatal("expected 1 row")
	}
	if !rows[0].Consumed {
		t.Error("expected consumed=true")
	}
	if rows[0].ConsumedAt == nil {
		t.Error("expected consumed_at to be set")
	}
}

func TestSSEEventRepo_DeleteByTask(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	_ = repo.Append(ctx, &SSEEventBufferRow{
		TaskID: "10000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: "{}",
	})

	if err := repo.DeleteByTask(ctx, "10000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}

	rows, _ := repo.GetByTask(ctx, "10000000-0000-0000-0000-000000000001", 1)
	if len(rows) != 0 {
		t.Errorf("expected 0 rows after delete, got %d", len(rows))
	}
}

func TestSSEEventRepo_MarkCompletedAndIsCompleted(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()
	const task = "10000000-0000-0000-0000-000000000001"

	if done, err := repo.IsCompleted(ctx, task); err != nil || done {
		t.Fatalf("IsCompleted(unknown) = %v, %v; want false, nil", done, err)
	}

	for i := 1; i <= 3; i++ {
		_ = repo.Append(ctx, &SSEEventBufferRow{
			TaskID: task, SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
			EventSequence: i, EventType: "status_update", EventData: "{}",
		})
	}

	if done, _ := repo.IsCompleted(ctx, task); done {
		t.Fatal("IsCompleted before MarkCompleted = true, want false")
	}

	if err := repo.MarkCompleted(ctx, task); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	if done, err := repo.IsCompleted(ctx, task); err != nil || !done {
		t.Fatalf("IsCompleted after MarkCompleted = %v, %v; want true, nil", done, err)
	}

	if err := repo.MarkCompleted(ctx, "20000000-0000-0000-0000-000000000002"); err != nil {
		t.Fatalf("MarkCompleted(empty task): %v", err)
	}
}

func TestSSEEventRepo_MarkCompletedBatch(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()
	const (
		sess      = "00000000-0000-0000-0000-000000000001"
		twoRows   = "10000000-0000-0000-0000-000000000001"
		threeRows = "10000000-0000-0000-0000-000000000002"
		untouched = "10000000-0000-0000-0000-000000000003"
		noRows    = "10000000-0000-0000-0000-000000000004"
	)
	for task, n := range map[string]int{twoRows: 2, threeRows: 3, untouched: 1} {
		for seq := 1; seq <= n; seq++ {
			if err := repo.Append(ctx, &SSEEventBufferRow{
				TaskID: task, SessionID: sess, UserID: "u1",
				EventSequence: seq, EventType: "status_update", EventData: "{}",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := repo.MarkCompletedBatch(ctx, []string{twoRows, threeRows, noRows}); err != nil {
		t.Fatalf("MarkCompletedBatch: %v", err)
	}

	states, err := repo.SequenceStates(ctx, []string{twoRows, threeRows, untouched})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]TaskLogState{
		twoRows:   {MaxSeq: 2, Completed: true},
		threeRows: {MaxSeq: 3, Completed: true},
		untouched: {MaxSeq: 1, Completed: false},
	}
	for task, w := range want {
		if got := states[task]; got.MaxSeq != w.MaxSeq || got.Completed != w.Completed {
			t.Errorf("task %s state = %+v, want %+v", task, got, w)
		}
	}
	if err := repo.MarkCompletedBatch(ctx, nil); err != nil {
		t.Fatalf("MarkCompletedBatch(nil): %v", err)
	}
	for task, n := range map[string]int64{twoRows: 1, threeRows: 1, untouched: 0} {
		got, err := dbutil.QueryVal[int64](ctx, db,
			`SELECT COUNT(*) FROM sse_event_buffer WHERE task_id = ? AND completed = 1`, task)
		if err != nil {
			t.Fatal(err)
		}
		if got != n {
			t.Errorf("task %s has %d completed rows, want %d: only the newest row is the marker", task, got, n)
		}
	}
}

func TestSSEEventRepo_MarkCompletedBatch_MoreTasksThanOneStatementHolds(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()
	ids := make([]string, dbutil.InClauseCap+2)
	rows := make([]*SSEEventBufferRow, len(ids))
	for i := range ids {
		ids[i] = fmt.Sprintf("20000000-0000-0000-0000-%012d", i)
		rows[i] = &SSEEventBufferRow{
			TaskID: ids[i], SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
			EventSequence: 1, EventType: "final_response", EventData: "{}",
		}
	}
	for start := 0; start < len(rows); start += 100 {
		if _, err := repo.AppendBatch(ctx, rows[start:min(start+100, len(rows))]); err != nil {
			t.Fatal(err)
		}
	}

	if err := repo.MarkCompletedBatch(ctx, ids); err != nil {
		t.Fatalf("MarkCompletedBatch: %v", err)
	}

	got, err := dbutil.QueryVal[int64](ctx, db, `SELECT COUNT(*) FROM sse_event_buffer WHERE completed = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(len(ids)) {
		t.Fatalf("%d tasks marked completed, want all %d", got, len(ids))
	}
}

func TestSSEEventRepo_GetUnconsumedBySession(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	_ = repo.Append(ctx, &SSEEventBufferRow{
		TaskID: "10000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: `{"a":1}`,
	})
	_ = repo.Append(ctx, &SSEEventBufferRow{
		TaskID: "10000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 2, EventType: "final_response", EventData: `{"a":2}`,
	})
	_ = repo.Append(ctx, &SSEEventBufferRow{
		TaskID: "10000000-0000-0000-0000-000000000002", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: `{"b":1}`,
	})

	_ = repo.MarkConsumed(ctx, "10000000-0000-0000-0000-000000000002")

	result, err := repo.GetUnconsumedBySession(ctx, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 task with unconsumed events, got %d", len(result))
	}
	if events, ok := result["10000000-0000-0000-0000-000000000001"]; !ok {
		t.Error("expected events for task 10000000-0000-0000-0000-000000000001")
	} else if len(events) != 2 {
		t.Errorf("expected 2 unconsumed events for t1, got %d", len(events))
	}
}

func TestSSEEventRepo_GetUnconsumedBySession_Empty(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	result, err := repo.GetUnconsumedBySession(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d tasks", len(result))
	}
}

func TestSSEEventRepo_DeleteOlderThan(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		if err := repo.Append(ctx, &SSEEventBufferRow{
			TaskID: "10000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
			EventSequence: i, EventType: "status_update", EventData: "{}",
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := repo.Append(ctx, &SSEEventBufferRow{
		TaskID: "10000000-0000-0000-0000-000000000002", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: "{}",
	}); err != nil {
		t.Fatal(err)
	}

	aged := time.Now().Add(-48 * time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET created_at = ? WHERE task_id = ?`,
		aged, "10000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("age rows: %v", err)
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	deleted, err := repo.DeleteOlderThan(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}

	rows, _ := repo.GetByTask(ctx, "10000000-0000-0000-0000-000000000002", 1)
	if len(rows) != 1 {
		t.Errorf("fresh rows remaining = %d, want 1", len(rows))
	}
	rows, _ = repo.GetByTask(ctx, "10000000-0000-0000-0000-000000000001", 1)
	if len(rows) != 0 {
		t.Errorf("aged rows remaining = %d, want 0", len(rows))
	}
}

func TestSSEEventRepo_DeleteOlderThan_Batching(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		if err := repo.Append(ctx, &SSEEventBufferRow{
			TaskID: "10000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
			EventSequence: i, EventType: "status_update", EventData: "{}",
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	aged := time.Now().Add(-48 * time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET created_at = ?`, aged); err != nil {
		t.Fatalf("age rows: %v", err)
	}

	deleted, err := repo.DeleteOlderThan(ctx, time.Now().Add(-24*time.Hour), 2)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 5 {
		t.Errorf("deleted = %d, want 5", deleted)
	}
}

func TestSSEEventRepo_DeleteOlderThan_KeepsTaskWithRowsInsideWindow(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()
	const (
		straddling = "10000000-0000-0000-0000-000000000001"
		allOld     = "10000000-0000-0000-0000-000000000002"
	)
	for _, id := range []string{straddling, allOld} {
		for seq := 1; seq <= 3; seq++ {
			if err := repo.Append(ctx, &SSEEventBufferRow{
				TaskID: id, SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
				EventSequence: seq, EventType: "status_update", EventData: "{}",
			}); err != nil {
				t.Fatalf("append: %v", err)
			}
		}
	}
	aged := time.Now().Add(-48 * time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET created_at = ? WHERE task_id = ? OR event_sequence <= 2`, aged, allOld); err != nil {
		t.Fatalf("age rows: %v", err)
	}

	deleted, err := repo.DeleteOlderThan(ctx, time.Now().Add(-24*time.Hour), 2)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want only the 3 rows of the task with nothing inside the window", deleted)
	}
	if rows, _ := repo.GetByTask(ctx, straddling, 1); len(rows) != 3 {
		t.Errorf("straddling task kept %d rows, want 3", len(rows))
	}
}

func TestSSEEventRepo_DeleteOlderThan_None(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()

	deleted, err := repo.DeleteOlderThan(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}

	_ = repo.Append(ctx, &SSEEventBufferRow{
		TaskID: "10000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: "{}",
	})
	deleted, err = repo.DeleteOlderThan(ctx, time.Now().Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 0 {
		t.Errorf("fresh row should not be deleted; got deleted=%d", deleted)
	}
}

func TestSSEEventRepo_DeleteConsumedTasksBefore(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	taskRepo := NewTaskRepo(db)
	ctx := context.Background()

	const (
		consumedOld   = "10000000-0000-0000-0000-000000000001"
		partial       = "10000000-0000-0000-0000-000000000002"
		running       = "10000000-0000-0000-0000-000000000003"
		consumedFresh = "10000000-0000-0000-0000-000000000004"
		neverConsumed = "10000000-0000-0000-0000-000000000005"
	)
	const session = "00000000-0000-0000-0000-000000000001"
	now := time.Now().UnixMilli()
	ended := now - 60_000

	mkTask := func(id string, endTime *int64) {
		if err := taskRepo.Create(ctx, &Task{
			ID: id, UserID: "u1", StartTime: now, ExecutionMode: "foreground", EndTime: endTime,
		}); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
	}
	appendRows := func(id string, n int) {
		for i := 1; i <= n; i++ {
			if err := repo.Append(ctx, &SSEEventBufferRow{
				TaskID: id, SessionID: session, UserID: "u1",
				EventSequence: i, EventType: "status_update", EventData: "{}",
			}); err != nil {
				t.Fatalf("append %s/%d: %v", id, i, err)
			}
		}
	}

	mkTask(consumedOld, &ended)
	appendRows(consumedOld, 3)
	if err := repo.MarkConsumed(ctx, consumedOld); err != nil {
		t.Fatal(err)
	}

	mkTask(partial, &ended)
	appendRows(partial, 2)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sse_task_consumed (task_id, consumed_through_seq, consumed_at) VALUES (?, 1, ?)`,
		partial, now); err != nil {
		t.Fatal(err)
	}

	mkTask(running, nil)
	appendRows(running, 2)
	if err := repo.MarkConsumed(ctx, running); err != nil {
		t.Fatal(err)
	}

	mkTask(consumedFresh, &ended)
	appendRows(consumedFresh, 1)
	if err := repo.MarkConsumed(ctx, consumedFresh); err != nil {
		t.Fatal(err)
	}

	mkTask(neverConsumed, &ended)
	appendRows(neverConsumed, 2)

	aged := now - int64(25*time.Hour/time.Millisecond)
	for _, id := range []string{consumedOld, partial, running} {
		if _, err := db.ExecContext(ctx,
			`UPDATE sse_task_consumed SET consumed_at = ? WHERE task_id = ?`, aged, id); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := now - int64(24*time.Hour/time.Millisecond)
	deleted, err := repo.DeleteConsumedTasksBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteConsumedTasksBefore: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3 (consumedOld's rows only)", deleted)
	}

	assertRows := func(id string, want int) {
		t.Helper()
		rows, _ := repo.GetByTask(ctx, id, 1)
		if len(rows) != want {
			t.Errorf("task %s remaining rows = %d, want %d", id, len(rows), want)
		}
	}
	assertRows(consumedOld, 0)
	assertRows(partial, 2)
	assertRows(running, 2)
	assertRows(consumedFresh, 1)
	assertRows(neverConsumed, 2)
}

func TestSSEEventRepo_DeleteForTasksEndedBefore(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	taskRepo := NewTaskRepo(db)
	ctx := context.Background()

	const (
		endedOld   = "10000000-0000-0000-0000-000000000001"
		endedFresh = "10000000-0000-0000-0000-000000000002"
		running    = "10000000-0000-0000-0000-000000000003"
	)
	const session = "00000000-0000-0000-0000-000000000001"
	now := time.Now().UnixMilli()
	oldEnd := now - int64(10*24*time.Hour/time.Millisecond)
	freshEnd := now - 1000

	mkTask := func(id string, endTime *int64) {
		if err := taskRepo.Create(ctx, &Task{
			ID: id, UserID: "u1", StartTime: now, ExecutionMode: "foreground", EndTime: endTime,
		}); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
	}
	appendRows := func(id string, n int) {
		for i := 1; i <= n; i++ {
			if err := repo.Append(ctx, &SSEEventBufferRow{
				TaskID: id, SessionID: session, UserID: "u1",
				EventSequence: i, EventType: "status_update", EventData: "{}",
			}); err != nil {
				t.Fatalf("append %s/%d: %v", id, i, err)
			}
		}
	}

	mkTask(endedOld, &oldEnd)
	appendRows(endedOld, 3)
	mkTask(endedFresh, &freshEnd)
	appendRows(endedFresh, 2)
	mkTask(running, nil)
	appendRows(running, 2)

	cutoff := now - int64(7*24*time.Hour/time.Millisecond)
	deleted, err := repo.DeleteForTasksEndedBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteForTasksEndedBefore: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3 (endedOld's rows only)", deleted)
	}

	if rows, _ := repo.GetByTask(ctx, endedOld, 1); len(rows) != 0 {
		t.Errorf("endedOld remaining = %d, want 0", len(rows))
	}
	if rows, _ := repo.GetByTask(ctx, endedFresh, 1); len(rows) != 2 {
		t.Errorf("endedFresh remaining = %d, want 2", len(rows))
	}
	if rows, _ := repo.GetByTask(ctx, running, 1); len(rows) != 2 {
		t.Errorf("running remaining = %d, want 2", len(rows))
	}
}

func TestSSEEventRepo_IdempotentOnDuplicatePK(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()
	const task = "10000000-0000-0000-0000-0000000000aa"
	row := func(seq int, data string) *SSEEventBufferRow {
		return &SSEEventBufferRow{TaskID: task, SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1", EventSequence: seq, EventType: "status_update", EventData: data}
	}

	if err := repo.Append(ctx, row(1, `{"v":"orig"}`)); err != nil {
		t.Fatalf("initial append: %v", err)
	}
	if err := repo.Append(ctx, row(1, `{"v":"dup"}`)); err != nil {
		t.Fatalf("duplicate Append should be a no-op, got: %v", err)
	}

	inserted, err := repo.AppendBatch(ctx, []*SSEEventBufferRow{row(1, `{"v":"dup2"}`), row(2, `{"v":"two"}`), row(3, `{"v":"three"}`)})
	if err != nil {
		t.Fatalf("AppendBatch with a duplicate row should skip it, got: %v", err)
	}
	if inserted != 2 {
		t.Errorf("AppendBatch inserted = %d, want 2 (seq 1 skipped as a duplicate, seq 2+3 inserted)", inserted)
	}

	rows, err := repo.GetByTask(ctx, task, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 distinct rows (1,2,3), got %d", len(rows))
	}
	if rows[0].EventSequence != 1 || rows[0].EventData != `{"v":"orig"}` {
		t.Errorf("seq-1 row should keep its original data, got seq=%d data=%s", rows[0].EventSequence, rows[0].EventData)
	}
}

func TestSSEEventRepo_SequenceStates(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()
	running, finished, unknown := "10000000-0000-0000-0000-0000000000a1", "10000000-0000-0000-0000-0000000000a2", "10000000-0000-0000-0000-0000000000a3"
	for task, n := range map[string]int{running: 3, finished: 2} {
		for seq := 1; seq <= n; seq++ {
			if err := repo.Append(ctx, &SSEEventBufferRow{TaskID: task, SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1", EventSequence: seq, EventType: "status_update", EventData: `{}`}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := repo.MarkCompleted(ctx, finished); err != nil {
		t.Fatal(err)
	}

	got, err := repo.SequenceStates(ctx, []string{running, finished, unknown})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]TaskLogState{running: {MaxSeq: 3}, finished: {MaxSeq: 2, Completed: true}}
	if len(got) != len(want) || got[running] != want[running] || got[finished] != want[finished] {
		t.Fatalf("states = %+v, want %+v (no entry for a task with no row)", got, want)
	}
	if empty, err := repo.SequenceStates(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no tasks: %v, %v", empty, err)
	}
}

func TestSSEEventRepo_SequenceStatesOnAClosedDatabase(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	_ = db.Close()
	if _, err := repo.SequenceStates(context.Background(), []string{"10000000-0000-0000-0000-0000000000b1"}); err == nil {
		t.Fatal("SequenceStates on a closed database returned no error")
	}
}

func TestSSEEventRepo_SequenceStatesReadsTheHighestRow(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := context.Background()
	appendRow := func(task string, seq int) {
		t.Helper()
		if err := repo.Append(ctx, &SSEEventBufferRow{TaskID: task, SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1", EventSequence: seq, EventType: "status_update", EventData: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	late := "10000000-0000-0000-0000-0000000000c1"
	appendRow(late, 1)
	appendRow(late, 2)
	if err := repo.MarkCompleted(ctx, late); err != nil {
		t.Fatal(err)
	}
	appendRow(late, 3)

	ids := []string{late}
	for i := range dbutil.InClauseCap + 1 {
		id := fmt.Sprintf("20000000-0000-0000-0000-%012d", i)
		ids = append(ids, id)
		if i%250 == 0 {
			appendRow(id, 1)
		}
	}
	got, err := repo.SequenceStates(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if got[late] != (TaskLogState{MaxSeq: 3}) {
		t.Fatalf("state = %+v, want the highest row, which is not marked", got[late])
	}
	if len(got) != 6 {
		t.Fatalf("states for %d tasks, want 6", len(got))
	}
}

func TestSSEEventRepo_Owner(t *testing.T) {
	const (
		unattributedTask = "10000000-0000-0000-0000-000000000002"
		mixedTask        = "10000000-0000-0000-0000-000000000003"
		sessionID        = "00000000-0000-0000-0000-000000000001"
	)
	repo := NewSSEEventRepo(newTestDB(t))
	ctx := t.Context()

	rows := []*SSEEventBufferRow{
		{TaskID: unattributedTask, SessionID: sessionID, UserID: "", EventSequence: 1, EventType: "status_update", EventData: "{}"},
		{TaskID: mixedTask, SessionID: sessionID, UserID: "", EventSequence: 1, EventType: "status_update", EventData: "{}"},
		{TaskID: mixedTask, SessionID: sessionID, UserID: "u1", EventSequence: 2, EventType: "status_update", EventData: "{}"},
	}
	if _, err := repo.AppendBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct{ taskID, want string }{
		"no rows":                {"10000000-0000-0000-0000-000000000004", ""},
		"only unattributed":      {unattributedTask, ""},
		"mixed names attributed": {mixedTask, "u1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := repo.Owner(ctx, tc.taskID)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("Owner = %q, want %q", got, tc.want)
			}
		})
	}
}
