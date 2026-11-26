// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type countingTaskRepo struct {
	*store.TaskRepo
	getByID        atomic.Int64
	create         atomic.Int64
	updateActivity atomic.Int64
	updateFinal    atomic.Int64
}

func (c *countingTaskRepo) GetByID(ctx context.Context, id string) (*store.Task, error) {
	c.getByID.Add(1)
	return c.TaskRepo.GetByID(ctx, id)
}

func (c *countingTaskRepo) Create(ctx context.Context, t *store.Task) error {
	c.create.Add(1)
	return c.TaskRepo.Create(ctx, t)
}

func (c *countingTaskRepo) UpdateLastActivityTime(ctx context.Context, id string) error {
	c.updateActivity.Add(1)
	return c.TaskRepo.UpdateLastActivityTime(ctx, id)
}

func (c *countingTaskRepo) UpdateFinalStatus(ctx context.Context, id string, u store.FinalStatusUpdate) error {
	c.updateFinal.Add(1)
	return c.TaskRepo.UpdateFinalStatus(ctx, id, u)
}

func startCountingLogger(t *testing.T, throttle time.Duration) (svc *TaskLoggerService, repo *countingTaskRepo, drain func()) {
	t.Helper()
	db := store.TestDB(t)
	repo = &countingTaskRepo{TaskRepo: store.NewTaskRepo(db)}
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{
		Enabled:           true,
		LogStatusUpdates:  true,
		LogArtifactEvents: true,
		QueueSize:         2000,
	}
	svc = NewTaskLoggerService(cfg, repo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
	svc.activityThrottle = throttle

	ctx, cancel := context.WithCancel(t.Context())
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	return svc, repo, func() {
		cancel()
		svc.Stop()
	}
}

func statusEvent(taskID string) TaskLogEvent {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"result":  map[string]any{"kind": "status-update", "taskId": taskID},
	})
	return TaskLogEvent{
		TaskID:    taskID,
		UserID:    "user-1",
		Topic:     "test-ns/a2a/v1/gateway/status/gw-test/x",
		Direction: "status",
		EventKind: "status-update",
		Payload:   payload,
	}
}

func requestEvent(taskID string) TaskLogEvent {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      taskID,
		"method":  "message/send",
		"params":  map[string]any{"message": map[string]any{"parts": []any{}}},
	})
	return TaskLogEvent{
		TaskID:             taskID,
		UserID:             "user-1",
		Topic:              "test-ns/a2a/v1/agent/request/my-agent",
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "hi",
		ExecutionMode:      "foreground",
	}
}

func TestTaskLogger_KnownTaskSkipsExistenceSelect(t *testing.T) {
	svc, repo, drain := startCountingLogger(t, 0)

	const events = 50
	task := tid("hot-task")
	for range events {
		svc.LogEvent(statusEvent(task))
	}
	drain()

	if got := repo.getByID.Load(); got != 1 {
		t.Errorf("GetByID called %d times, want 1 (one existence check, then cache hits)", got)
	}
	if got := repo.create.Load(); got != 1 {
		t.Errorf("Create called %d times, want 1 (single placeholder)", got)
	}
	if got := repo.updateActivity.Load(); got != events-1 {
		t.Errorf("UpdateLastActivityTime called %d times, want %d", got, events-1)
	}
}

func TestTaskLogger_ThrottlesActivityWrites(t *testing.T) {
	svc, repo, drain := startCountingLogger(t, time.Hour)

	const events = 50
	task := tid("throttled-task")
	for range events {
		svc.LogEvent(statusEvent(task))
	}
	drain()

	if got := repo.getByID.Load(); got != 1 {
		t.Errorf("GetByID called %d times, want 1", got)
	}
	if got := repo.updateActivity.Load(); got != 0 {
		t.Errorf("UpdateLastActivityTime called %d times, want 0 (all coalesced)", got)
	}

	stored, err := repo.TaskRepo.GetByID(context.Background(), task)
	if err != nil || stored == nil {
		t.Fatalf("task row missing: %v", err)
	}
	if stored.LastActivityTime == nil {
		t.Error("last_activity_time should be set by the placeholder create")
	}
}

func TestTaskLogger_RequestPrimesCacheForStatusEvents(t *testing.T) {
	svc, repo, drain := startCountingLogger(t, 0)

	task := tid("primed-task")
	svc.LogEvent(requestEvent(task))
	const statuses = 20
	for range statuses {
		svc.LogEvent(statusEvent(task))
	}
	drain()

	if got := repo.getByID.Load(); got != 1 {
		t.Errorf("GetByID called %d times, want 1 (request only; status events hit the cache)", got)
	}
	if got := repo.create.Load(); got != 1 {
		t.Errorf("Create called %d times, want 1", got)
	}
}

func TestTaskLogger_FirstSeenExistingRowSeedsCache(t *testing.T) {
	svc, repo, drain := startCountingLogger(t, 0)

	task := tid("preexisting-task")
	now := time.Now().UnixMilli()
	if err := repo.TaskRepo.Create(context.Background(), &store.Task{
		ID:               task,
		UserID:           "user-1",
		StartTime:        now,
		ExecutionMode:    "foreground",
		LastActivityTime: &now,
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	repo.create.Store(0)

	svc.LogEvent(statusEvent(task))
	svc.LogEvent(statusEvent(task))
	drain()

	if got := repo.getByID.Load(); got != 1 {
		t.Errorf("GetByID called %d times, want 1 (first event confirms existence; second hits cache)", got)
	}
	if got := repo.create.Load(); got != 0 {
		t.Errorf("Create called %d times, want 0 (row already existed)", got)
	}
	if got := repo.updateActivity.Load(); got != 2 {
		t.Errorf("UpdateLastActivityTime called %d times, want 2", got)
	}
}

func TestTaskLogger_EvictsCacheOnFinal(t *testing.T) {
	svc, _, drain := startCountingLogger(t, 0)

	task := tid("final-task")
	svc.LogEvent(statusEvent(task))
	final := statusEvent(task)
	final.Direction = "response"
	final.IsFinal = true
	final.FinalStatus = "completed"
	svc.LogEvent(final)
	drain()

	if _, present := svc.taskActivity[task]; present {
		t.Errorf("activity cache still holds %s after its final event", task)
	}
}

func TestTaskLogger_SweepEvictsAbandonedTasks(t *testing.T) {
	svc := &TaskLoggerService{
		taskActivity: map[string]int64{},
		logger:       slog.Default(),
	}
	now := time.Now().UnixMilli()
	svc.taskActivity["abandoned"] = now - staleTaskActivity.Milliseconds() - 1
	svc.taskActivity["live"] = now

	svc.lastActivitySweep = 0
	svc.sweepTaskActivity()

	if _, present := svc.taskActivity["abandoned"]; present {
		t.Error("abandoned task should have been swept")
	}
	if _, present := svc.taskActivity["live"]; !present {
		t.Error("live task should be retained")
	}

	svc.taskActivity["abandoned-2"] = now - staleTaskActivity.Milliseconds() - 1
	svc.sweepTaskActivity()
	if _, present := svc.taskActivity["abandoned-2"]; !present {
		t.Error("sweep should not re-scan before the interval elapses")
	}
}
