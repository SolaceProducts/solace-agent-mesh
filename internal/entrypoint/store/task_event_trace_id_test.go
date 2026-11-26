// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	traceTestParentTask = "10000000-0000-0000-0000-0000000000b1"
	traceTestChildTask  = "10000000-0000-0000-0000-0000000000b2"
	traceTestTraceID    = "01890000-0000-7000-8000-0000000000aa"
)

func seedTraceTestTasks(t *testing.T, db *DB) {
	t.Helper()
	repo := NewTaskRepo(db)
	for _, id := range []string{traceTestParentTask, traceTestChildTask} {
		if err := repo.Create(t.Context(), &Task{ID: id, UserID: "u1", StartTime: 1, ExecutionMode: "foreground"}); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
	}
}

func storedTraceID(t *testing.T, db *DB, eventID string) *string {
	t.Helper()
	type row struct {
		TraceID *string `db:"trace_id"`
	}
	out, err := dbutil.QueryRow[row](t.Context(), db, `SELECT trace_id FROM task_events WHERE id = ?`, eventID)
	if err != nil {
		t.Fatalf("read trace_id for %s: %v", eventID, err)
	}
	return out.TraceID
}

func TestB01_TaskEventTraceIDPersists(t *testing.T) {
	db := newTestDB(t)
	seedTraceTestTasks(t, db)
	repo := NewTaskEventRepo(db)
	ctx := t.Context()

	withTrace := func(id string) *TaskEvent {
		return &TaskEvent{ID: id, TaskID: traceTestParentTask, CreatedTime: 1, Topic: "t", Direction: "status", Payload: `{}`, TraceID: new(traceTestTraceID)}
	}
	withoutTrace := func(id string) *TaskEvent {
		return &TaskEvent{ID: id, TaskID: traceTestParentTask, CreatedTime: 2, Topic: "t", Direction: "status", Payload: `{}`}
	}

	if err := repo.Create(ctx, withTrace("b0000000-0000-0000-0000-0000000000c1")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, withoutTrace("b0000000-0000-0000-0000-0000000000c2")); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateBatch(ctx, []*TaskEvent{
		withTrace("b0000000-0000-0000-0000-0000000000c3"),
		withoutTrace("b0000000-0000-0000-0000-0000000000c4"),
	}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		eventID string
		want    *string
	}{
		{"B01_single_with_trace", "b0000000-0000-0000-0000-0000000000c1", new(traceTestTraceID)},
		{"B01_single_without_trace_is_null", "b0000000-0000-0000-0000-0000000000c2", nil},
		{"B01_batch_with_trace", "b0000000-0000-0000-0000-0000000000c3", new(traceTestTraceID)},
		{"B01_batch_without_trace_is_null", "b0000000-0000-0000-0000-0000000000c4", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := storedTraceID(t, db, tc.eventID); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("trace_id = %v, want %v", derefTrace(got), derefTrace(tc.want))
			}
		})
	}

	events, err := repo.GetByTaskID(ctx, traceTestParentTask)
	if err != nil {
		t.Fatal(err)
	}
	var readBack int
	for _, e := range events {
		if e.TraceID != nil && *e.TraceID == traceTestTraceID {
			readBack++
		}
	}
	if readBack != 2 {
		t.Fatalf("GetByTaskID returned %d events carrying the trace id, want 2", readBack)
	}
}

func TestB01_TaskEventTraceIDSharedAcrossDelegatedTasks(t *testing.T) {
	db := newTestDB(t)
	seedTraceTestTasks(t, db)
	repo := NewTaskEventRepo(db)
	ctx := t.Context()

	if err := repo.CreateBatch(ctx, []*TaskEvent{
		{ID: "b0000000-0000-0000-0000-0000000000d1", TaskID: traceTestParentTask, CreatedTime: 1, Topic: "t", Direction: "request", Payload: `{}`, TraceID: new(traceTestTraceID)},
		{ID: "b0000000-0000-0000-0000-0000000000d2", TaskID: traceTestChildTask, CreatedTime: 2, Topic: "t", Direction: "request", Payload: `{}`, TraceID: new(traceTestTraceID)},
	}); err != nil {
		t.Fatal(err)
	}

	events, err := repo.GetByTaskIDs(ctx, []string{traceTestParentTask, traceTestChildTask})
	if err != nil {
		t.Fatal(err)
	}
	taskIDs := map[string]bool{}
	for _, e := range events {
		if e.TraceID == nil || *e.TraceID != traceTestTraceID {
			t.Fatalf("event %s trace_id = %v, want %s", e.ID, derefTrace(e.TraceID), traceTestTraceID)
		}
		taskIDs[e.TaskID] = true
	}
	if len(taskIDs) != 2 {
		t.Fatalf("parent and child sharing a trace id collapsed to %d task ids, want 2", len(taskIDs))
	}
}

func TestB01_TaskEventSingleAndBatchInsertParity(t *testing.T) {
	t.Run("B01_params_match_columns", func(t *testing.T) {
		insertCols := len(strings.Split(taskEventInsertColumns, ","))
		for _, dialect := range []string{"sqlite", "postgres"} {
			if got := strings.Count(taskEventValuesTuple(dialect), "?"); got != taskEventParamsPerRow {
				t.Errorf("%s tuple binds %d params, want %d", dialect, got, taskEventParamsPerRow)
			}
			if got := strings.Count(taskEventValuesTuple(dialect), ",") + 1; got != insertCols {
				t.Errorf("%s tuple has %d positions, insert column list has %d", dialect, got, insertCols)
			}
		}
		if got := len(taskEventArgs(&TaskEvent{})); got != taskEventParamsPerRow {
			t.Errorf("taskEventArgs yields %d values, want %d", got, taskEventParamsPerRow)
		}
	})

	t.Run("B01_single_and_batch_rows_identical", func(t *testing.T) {
		db := newTestDB(t)
		seedTraceTestTasks(t, db)
		repo := NewTaskEventRepo(db)
		full := func(id string) *TaskEvent {
			return &TaskEvent{
				ID: id, TaskID: traceTestParentTask, UserID: new("u1"), CreatedTime: 7, Topic: "ns/a2a/v1/x",
				Direction: "status", Payload: `{"k":"v"}`, IsDelta: true, SignalType: new("tool_result"),
				AgentName: new("agent_a"), ToolName: new("tool_t"), EventSeq: new(int64(3)), PublisherID: new("pub"),
				ConfigPromptBlob: new("sha256:p"), ConfigToolsBlob: new("sha256:t"), ForwardedFrom: new("peer"),
				TraceID: new(traceTestTraceID),
			}
		}
		const singleID, batchID = "b0000000-0000-0000-0000-0000000000e1", "b0000000-0000-0000-0000-0000000000e2"
		if err := repo.Create(t.Context(), full(singleID)); err != nil {
			t.Fatal(err)
		}
		if err := repo.CreateBatch(t.Context(), []*TaskEvent{full(batchID)}); err != nil {
			t.Fatal(err)
		}
		single := wholeTaskEventRow(t, db, singleID)
		batch := wholeTaskEventRow(t, db, batchID)
		for col, v := range single {
			if col == "id" {
				continue
			}
			if !reflect.DeepEqual(v, batch[col]) {
				t.Errorf("column %s: single=%v batch=%v", col, v, batch[col])
			}
		}
		if _, ok := single["trace_id"]; !ok {
			t.Error("trace_id missing from the single-insert row")
		}
	})
}

func wholeTaskEventRow(t *testing.T, db *DB, eventID string) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT * FROM task_events WHERE id = ?`, eventID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatalf("event %s not found", eventID)
	}
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(cols))
	for i, c := range cols {
		if c == "commit_xid" || !vals[i].Valid {
			continue
		}
		out[c] = vals[i].String
	}
	return out
}

func derefTrace(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", *s)
}
