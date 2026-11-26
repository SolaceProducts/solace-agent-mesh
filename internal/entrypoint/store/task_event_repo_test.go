// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestTaskEventRepo_CreateAndGetByTaskID(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	_ = taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"})

	userID := "u1"
	e1 := &TaskEvent{
		ID:          "b0000000-0000-0000-0000-000000000001",
		TaskID:      "10000000-0000-0000-0000-000000000001",
		UserID:      &userID,
		CreatedTime: 1000,
		Topic:       "chat/response",
		Direction:   "outbound",
		Payload:     `{"text":"hello"}`,
	}
	e2 := &TaskEvent{
		ID:          "b0000000-0000-0000-0000-000000000002",
		TaskID:      "10000000-0000-0000-0000-000000000001",
		UserID:      &userID,
		CreatedTime: 2000,
		Topic:       "chat/response",
		Direction:   "outbound",
		Payload:     `{"text":"world"}`,
	}

	if err := repo.Create(ctx, e1); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, e2); err != nil {
		t.Fatal(err)
	}

	events, err := repo.GetByTaskID(ctx, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != "b0000000-0000-0000-0000-000000000001" {
		t.Errorf("first event ID = %s, want ev1", events[0].ID)
	}
	if events[1].Payload != `{"text":"world"}` {
		t.Errorf("second event payload = %s", events[1].Payload)
	}
}

func TestTaskEventRepo_GetByTaskID_Empty(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskEventRepo(db)

	events, err := repo.GetByTaskID(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestTaskEventRepo_GetByTaskIDs(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	_ = taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: now, ExecutionMode: "foreground"})
	_ = taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000002", UserID: "u1", StartTime: now, ExecutionMode: "foreground"})
	_ = taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000003", UserID: "u1", StartTime: now, ExecutionMode: "foreground"})

	_ = repo.Create(ctx, &TaskEvent{ID: "b0000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000001", CreatedTime: 1000, Topic: "a", Direction: "in", Payload: "{}"})
	_ = repo.Create(ctx, &TaskEvent{ID: "b0000000-0000-0000-0000-000000000002", TaskID: "10000000-0000-0000-0000-000000000002", CreatedTime: 2000, Topic: "b", Direction: "out", Payload: "{}"})
	_ = repo.Create(ctx, &TaskEvent{ID: "b0000000-0000-0000-0000-000000000003", TaskID: "10000000-0000-0000-0000-000000000003", CreatedTime: 3000, Topic: "c", Direction: "in", Payload: "{}"})

	events, err := repo.GetByTaskIDs(ctx, []string{"10000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000003"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != "b0000000-0000-0000-0000-000000000001" || events[1].ID != "b0000000-0000-0000-0000-000000000003" {
		t.Errorf("got IDs [%s, %s], want [ev1, ev3]", events[0].ID, events[1].ID)
	}
}

func TestTaskEventRepo_GetByTaskIDs_EmptySlice(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskEventRepo(db)

	events, err := repo.GetByTaskIDs(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if events != nil {
		t.Errorf("expected nil, got %v", events)
	}
}

func TestTaskEventRepo_CountByTaskIDs(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	t1 := "10000000-0000-0000-0000-000000000001"
	t2 := "10000000-0000-0000-0000-000000000002"
	t3 := "10000000-0000-0000-0000-000000000003"
	_ = taskRepo.Create(ctx, &Task{ID: t1, UserID: "u1", StartTime: now, ExecutionMode: "foreground"})
	_ = taskRepo.Create(ctx, &Task{ID: t2, UserID: "u1", StartTime: now, ExecutionMode: "foreground"})
	_ = taskRepo.Create(ctx, &Task{ID: t3, UserID: "u1", StartTime: now, ExecutionMode: "foreground"})

	_ = repo.Create(ctx, &TaskEvent{ID: "b0000000-0000-0000-0000-000000000001", TaskID: t1, CreatedTime: 1000, Topic: "a", Direction: "in", Payload: "{}"})
	_ = repo.Create(ctx, &TaskEvent{ID: "b0000000-0000-0000-0000-000000000002", TaskID: t1, CreatedTime: 1500, Topic: "a", Direction: "out", Payload: "{}"})
	_ = repo.Create(ctx, &TaskEvent{ID: "b0000000-0000-0000-0000-000000000003", TaskID: t2, CreatedTime: 2000, Topic: "b", Direction: "out", Payload: "{}"})

	counts, err := repo.CountByTaskIDs(ctx, []string{t1, t2, t3})
	if err != nil {
		t.Fatal(err)
	}
	if counts[t1] != 2 {
		t.Errorf("t1: got %d, want 2", counts[t1])
	}
	if counts[t2] != 1 {
		t.Errorf("t2: got %d, want 1", counts[t2])
	}
	if _, ok := counts[t3]; ok {
		t.Errorf("t3: expected absent, got %d", counts[t3])
	}
}

func TestTaskEventRepo_CountByTaskIDs_EmptySlice(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskEventRepo(db)

	counts, err := repo.CountByTaskIDs(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Errorf("expected empty map, got %v", counts)
	}
}

func TestTaskEventRepo_CreateWithNilUserID(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	_ = taskRepo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"})

	e := &TaskEvent{
		ID:          "b0000000-0000-0000-0000-000000000001",
		TaskID:      "10000000-0000-0000-0000-000000000001",
		UserID:      nil,
		CreatedTime: 1000,
		Topic:       "system/event",
		Direction:   "internal",
		Payload:     `{}`,
	}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatal(err)
	}

	events, err := repo.GetByTaskID(ctx, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].UserID != nil {
		t.Errorf("expected nil user_id, got %v", events[0].UserID)
	}
}

func TestTaskEventRepo_CreateBatch(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	taskID := "10000000-0000-0000-0000-000000000001"
	_ = taskRepo.Create(ctx, &Task{ID: taskID, UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"})

	if err := repo.CreateBatch(ctx, nil); err != nil {
		t.Fatalf("empty batch should be a no-op, got %v", err)
	}

	batch := []*TaskEvent{
		{ID: "b0000000-0000-0000-0000-000000000001", TaskID: taskID, CreatedTime: 1000, Topic: "t", Direction: "status", Payload: `{"i":1}`},
		{ID: "b0000000-0000-0000-0000-000000000002", TaskID: taskID, CreatedTime: 2000, Topic: "t", Direction: "status", Payload: `{"i":2}`},
		{ID: "b0000000-0000-0000-0000-000000000003", TaskID: taskID, CreatedTime: 3000, Topic: "t", Direction: "response", Payload: `{"i":3}`},
	}
	if err := repo.CreateBatch(ctx, batch); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	got, err := repo.GetByTaskID(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	for i, want := range []string{`{"i":1}`, `{"i":2}`, `{"i":3}`} {
		if got[i].Payload != want {
			t.Errorf("event %d payload = %s, want %s", i, got[i].Payload, want)
		}
	}
}

func TestTaskEventRepo_CreateBatch_FKViolationIsAtomic(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	batch := []*TaskEvent{
		{ID: "b0000000-0000-0000-0000-000000000001", TaskID: "20000000-0000-0000-0000-000000000099", CreatedTime: 1, Topic: "t", Direction: "status", Payload: "{}"},
	}
	if err := repo.CreateBatch(ctx, batch); err == nil {
		t.Skip("FK not enforced (PRAGMA foreign_keys off); atomicity guarantee not exercised")
	}
	got, err := repo.GetByTaskID(ctx, "20000000-0000-0000-0000-000000000099")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("FK-violating batch must insert nothing, got %d rows", len(got))
	}
}

func TestTaskEventRepo_CreateBatch_MultipleTasks(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	taskA := "10000000-0000-0000-0000-00000000000a"
	taskB := "10000000-0000-0000-0000-00000000000b"
	now := time.Now().UnixMilli()
	_ = taskRepo.Create(ctx, &Task{ID: taskA, UserID: "u1", StartTime: now, ExecutionMode: "foreground"})
	_ = taskRepo.Create(ctx, &Task{ID: taskB, UserID: "u1", StartTime: now, ExecutionMode: "foreground"})

	batch := []*TaskEvent{
		{ID: "b0000000-0000-0000-0000-0000000000a1", TaskID: taskA, CreatedTime: 1, Topic: "t", Direction: "status", Payload: `{"t":"a1"}`},
		{ID: "b0000000-0000-0000-0000-0000000000b1", TaskID: taskB, CreatedTime: 2, Topic: "t", Direction: "status", Payload: `{"t":"b1"}`},
		{ID: "b0000000-0000-0000-0000-0000000000a2", TaskID: taskA, CreatedTime: 3, Topic: "t", Direction: "response", Payload: `{"t":"a2"}`},
	}
	if err := repo.CreateBatch(ctx, batch); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	gotA, _ := repo.GetByTaskID(ctx, taskA)
	if len(gotA) != 2 || gotA[0].Payload != `{"t":"a1"}` || gotA[1].Payload != `{"t":"a2"}` {
		t.Fatalf("taskA rows wrong: %+v", gotA)
	}
	gotB, _ := repo.GetByTaskID(ctx, taskB)
	if len(gotB) != 1 || gotB[0].Payload != `{"t":"b1"}` {
		t.Fatalf("taskB rows wrong: %+v", gotB)
	}
}

func TestTaskEventRepo_DeleteDeltasOlderThan(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	const taskID = "10000000-0000-0000-0000-000000000001"
	now := time.Now().UnixMilli()
	if err := taskRepo.Create(ctx, &Task{ID: taskID, UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	agedDelta := &TaskEvent{ID: "b0000000-0000-0000-0000-000000000001", TaskID: taskID, CreatedTime: now - 10_000, Topic: "t", Direction: "status", Payload: "{}", IsDelta: true}
	agedStructural := &TaskEvent{ID: "b0000000-0000-0000-0000-000000000002", TaskID: taskID, CreatedTime: now - 10_000, Topic: "t", Direction: "status", Payload: "{}", IsDelta: false}
	freshDelta := &TaskEvent{ID: "b0000000-0000-0000-0000-000000000003", TaskID: taskID, CreatedTime: now, Topic: "t", Direction: "status", Payload: "{}", IsDelta: true}
	for _, e := range []*TaskEvent{agedDelta, agedStructural, freshDelta} {
		if err := repo.Create(ctx, e); err != nil {
			t.Fatalf("create %s: %v", e.ID, err)
		}
	}

	var storedDelta int
	if err := db.QueryRowContext(ctx, `SELECT is_delta FROM task_events WHERE id = ?`, agedDelta.ID).Scan(&storedDelta); err != nil {
		t.Fatalf("scan is_delta: %v", err)
	}
	if storedDelta != 1 {
		t.Errorf("agedDelta is_delta = %d, want 1", storedDelta)
	}

	cutoff := time.UnixMilli(now - 5_000)
	deleted, err := repo.DeleteDeltasOlderThan(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("DeleteDeltasOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only the aged delta)", deleted)
	}

	remaining, err := repo.GetByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("get remaining: %v", err)
	}
	got := make(map[string]bool, len(remaining))
	for _, e := range remaining {
		got[e.ID] = true
	}
	if got[agedDelta.ID] {
		t.Error("aged delta should have been deleted")
	}
	if !got[agedStructural.ID] {
		t.Error("aged structural row must survive (is_delta = 0)")
	}
	if !got[freshDelta.ID] {
		t.Error("fresh delta must survive (created after cutoff)")
	}
}

func TestTaskEventRepo_CreateBatch_PersistsIsDelta(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	const taskID = "10000000-0000-0000-0000-000000000001"
	now := time.Now().UnixMilli()
	if err := taskRepo.Create(ctx, &Task{ID: taskID, UserID: "u1", StartTime: now, ExecutionMode: "foreground"}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	delta := &TaskEvent{ID: "b0000000-0000-0000-0000-000000000001", TaskID: taskID, CreatedTime: now, Topic: "t", Direction: "status", Payload: "{}", IsDelta: true}
	structural := &TaskEvent{ID: "b0000000-0000-0000-0000-000000000002", TaskID: taskID, CreatedTime: now + 1, Topic: "t", Direction: "status", Payload: "{}", IsDelta: false}
	if err := repo.CreateBatch(ctx, []*TaskEvent{delta, structural}); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	assertIsDelta := func(id string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRowContext(ctx, `SELECT is_delta FROM task_events WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("scan %s: %v", id, err)
		}
		if got != want {
			t.Errorf("id %s is_delta = %d, want %d", id, got, want)
		}
	}
	assertIsDelta(delta.ID, 1)
	assertIsDelta(structural.ID, 0)
}

func TestTaskEventRepo_ForwardedPeerNames(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewTaskEventRepo(db)

	forwarded := func(taskID, forwarder, peer string) *TaskEvent {
		payload := `{"result":{"status":{"message":{"metadata":{"agent_name":"` + forwarder +
			`","forwarded_from_peer":"` + peer + `","peer_task_chain":["t-peer"]}}}}}`
		return &TaskEvent{ID: idgen.New(), TaskID: taskID, CreatedTime: 1, Topic: "t", Direction: "in", Payload: payload}
	}
	plain := func(taskID, agent string) *TaskEvent {
		return &TaskEvent{ID: idgen.New(), TaskID: taskID, CreatedTime: 1, Topic: "t", Direction: "in",
			Payload: `{"result":{"status":{"message":{"metadata":{"agent_name":"` + agent + `"}}}}}`}
	}

	inTree, outOfTree := idgen.New(), idgen.New()
	tasks := NewTaskRepo(db)
	for _, id := range []string{inTree, outOfTree} {
		if err := tasks.Create(ctx, &Task{ID: id, UserID: "u1", StartTime: 1, ExecutionMode: "foreground", Kind: TaskKindNormal}); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}
	for _, e := range []*TaskEvent{
		plain(inTree, "hr-bot"),
		forwarded(inTree, "hr-bot", "procurement-bot"),
		forwarded(inTree, "hr-bot", "procurement-bot"),
		forwarded(inTree, "hr-bot", "payroll-bot"),
		forwarded(outOfTree, "other-bot", "should-not-appear"),
	} {
		if err := repo.Create(ctx, e); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	got, err := repo.ForwardedPeerNames(ctx, []string{inTree})
	if err != nil {
		t.Fatalf("ForwardedPeerNames: %v", err)
	}
	sort.Strings(got)
	want := []string{"payroll-bot", "procurement-bot"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	empty, err := repo.ForwardedPeerNames(ctx, nil)
	if err != nil {
		t.Fatalf("ForwardedPeerNames(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty id set must yield no names, got %v", empty)
	}
}

func TestTaskEventRepo_ForwardedPeersByTaskReadsEveryChunk(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ids := make([]string, dbutil.InClauseCap+1)
	for i := range ids {
		ids[i] = idgen.New()
	}
	last := ids[len(ids)-1]
	if err := NewTaskRepo(db).Create(ctx, &Task{ID: last, UserID: "u1", StartTime: 1, ExecutionMode: "foreground", Kind: TaskKindNormal}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := NewTaskEventRepo(db).Create(ctx, &TaskEvent{ID: idgen.New(), TaskID: last, CreatedTime: 1, Topic: "t", Direction: "in",
		Payload: `{"result":{"status":{"message":{"metadata":{"forwarded_from_peer":"payroll-bot"}}}}}`}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	got, err := NewTaskEventRepo(db).ForwardedPeersByTask(ctx, ids)
	if err != nil {
		t.Fatalf("ForwardedPeersByTask: %v", err)
	}
	if !reflect.DeepEqual(got[last], []string{"payroll-bot"}) {
		t.Errorf("peers of the task in the second chunk = %v, want [payroll-bot]", got[last])
	}
}
