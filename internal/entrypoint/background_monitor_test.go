// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

func TestBackgroundMonitorService_TimeoutsStaleTask(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)

	staleActivity := time.Now().Add(-5 * time.Second).UnixMilli()
	shortTimeout := int64(1000)
	err := taskRepo.Create(ctx, &store.Task{
		ID:                         tid("task-stale"),
		UserID:                     "user1",
		StartTime:                  staleActivity,
		ExecutionMode:              "background",
		BackgroundExecutionEnabled: true,
		MaxExecutionTimeMs:         &shortTimeout,
		LastActivityTime:           &staleActivity,
	})
	if err != nil {
		t.Fatalf("create stale task: %v", err)
	}

	fgActivity := staleActivity
	err = taskRepo.Create(ctx, &store.Task{
		ID:               tid("task-foreground"),
		UserID:           "user1",
		StartTime:        staleActivity,
		ExecutionMode:    "foreground",
		LastActivityTime: &fgActivity,
	})
	if err != nil {
		t.Fatalf("create foreground task: %v", err)
	}

	orphanActivity := time.Now().Add(-2 * time.Hour).UnixMilli()
	err = taskRepo.Create(ctx, &store.Task{
		ID:               tid("task-foreground-orphan"),
		UserID:           "user1",
		StartTime:        orphanActivity,
		ExecutionMode:    "foreground",
		LastActivityTime: &orphanActivity,
	})
	if err != nil {
		t.Fatalf("create orphaned foreground task: %v", err)
	}

	recentActivity := time.Now().UnixMilli()
	longTimeout := int64(3600000)
	err = taskRepo.Create(ctx, &store.Task{
		ID:                         tid("task-active"),
		UserID:                     "user1",
		StartTime:                  recentActivity,
		ExecutionMode:              "background",
		BackgroundExecutionEnabled: true,
		MaxExecutionTimeMs:         &longTimeout,
		LastActivityTime:           &recentActivity,
	})
	if err != nil {
		t.Fatalf("create active task: %v", err)
	}

	monitor := NewBackgroundMonitorService(BackgroundMonitorConfig{
		Enabled:          true,
		CheckIntervalSec: 60,
		DefaultTimeoutMs: 3600000,
	}, taskRepo, slog.Default())

	monitor.checkStaleTasks(ctx)

	staleTask, err := taskRepo.GetByID(ctx, tid("task-stale"))
	if err != nil {
		t.Fatalf("get stale task: %v", err)
	}
	if staleTask.Status == nil || *staleTask.Status != "timeout" {
		t.Errorf("expected stale task status 'timeout', got %v", staleTask.Status)
	}
	if staleTask.EndTime == nil {
		t.Error("expected stale task end_time to be set")
	}

	fgTask, err := taskRepo.GetByID(ctx, tid("task-foreground"))
	if err != nil {
		t.Fatalf("get foreground task: %v", err)
	}
	if fgTask.Status != nil {
		t.Errorf("expected foreground task status nil, got %v", *fgTask.Status)
	}
	if fgTask.EndTime != nil {
		t.Error("expected foreground task end_time to remain nil")
	}

	orphanTask, err := taskRepo.GetByID(ctx, tid("task-foreground-orphan"))
	if err != nil {
		t.Fatalf("get orphaned foreground task: %v", err)
	}
	if orphanTask.Status == nil || *orphanTask.Status != "timeout" {
		t.Errorf("expected orphaned foreground task status 'timeout', got %v", orphanTask.Status)
	}
	if orphanTask.EndTime == nil {
		t.Error("expected orphaned foreground task end_time to be set")
	}

	activeTask, err := taskRepo.GetByID(ctx, tid("task-active"))
	if err != nil {
		t.Fatalf("get active task: %v", err)
	}
	if activeTask.Status != nil {
		t.Errorf("expected active task status nil, got %v", *activeTask.Status)
	}
	if activeTask.EndTime != nil {
		t.Error("expected active task end_time to remain nil")
	}
}

func TestBackgroundMonitorService_EmitsTerminalTimeoutMetricOncePerTask(t *testing.T) {
	rec := observability.NewRecordingMetrics()

	ctx := t.Context()
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)

	stale := time.Now().Add(-2 * time.Hour).UnixMilli()
	for _, id := range []string{tid("reap-a"), tid("reap-b")} {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: "u1", StartTime: stale,
			ExecutionMode: "foreground", LastActivityTime: &stale,
		}); err != nil {
			t.Fatalf("create stale task %s: %v", id, err)
		}
	}
	recent := time.Now().UnixMilli()
	if err := taskRepo.Create(ctx, &store.Task{
		ID: tid("reap-active"), UserID: "u1", StartTime: recent,
		ExecutionMode: "foreground", LastActivityTime: &recent,
	}); err != nil {
		t.Fatalf("create active task: %v", err)
	}

	monitor := NewBackgroundMonitorService(BackgroundMonitorConfig{
		Enabled: true, CheckIntervalSec: 60, DefaultTimeoutMs: 1000,
	}, taskRepo, slog.Default())
	monitor.metrics = rec

	monitor.checkStaleTasks(ctx)
	monitor.checkStaleTasks(ctx)

	if got := rec.CounterSum(MetricTaskTerminal, "status", "timeout"); got != 2 {
		t.Errorf("timeout counter = %d, want 2 (one per reaped task, no double count on re-sweep)", got)
	}
	if got := rec.CounterSum(MetricTaskTerminal); got != 2 {
		t.Errorf("total terminal counter = %d, want 2 (active task must not count)", got)
	}
}

func TestBackgroundMonitorService_DefaultTimeout(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)

	staleActivity := time.Now().Add(-5 * time.Second).UnixMilli()
	err := taskRepo.Create(ctx, &store.Task{
		ID:                         tid("task-no-timeout"),
		UserID:                     "user1",
		StartTime:                  staleActivity,
		ExecutionMode:              "background",
		BackgroundExecutionEnabled: true,
		LastActivityTime:           &staleActivity,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	monitor := NewBackgroundMonitorService(BackgroundMonitorConfig{
		Enabled:          true,
		CheckIntervalSec: 60,
		DefaultTimeoutMs: 1000,
	}, taskRepo, slog.Default())

	monitor.checkStaleTasks(ctx)

	task, err := taskRepo.GetByID(ctx, tid("task-no-timeout"))
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status == nil || *task.Status != "timeout" {
		t.Errorf("expected task status 'timeout', got %v", task.Status)
	}
}

func TestBackgroundMonitorService_StartStop(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)

	monitor := NewBackgroundMonitorService(BackgroundMonitorConfig{
		Enabled:          true,
		CheckIntervalSec: 1,
		DefaultTimeoutMs: 3600000,
	}, taskRepo, slog.Default())

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	monitor.Start(ctx, &wg)

	time.Sleep(100 * time.Millisecond)

	cancel()
	wg.Wait()
}

func TestParseBackgroundMonitorConfig(t *testing.T) {
	cfg := ParseBackgroundMonitorConfig(nil)
	if !cfg.Enabled {
		t.Error("expected enabled by default")
	}
	if cfg.CheckIntervalSec != 60 {
		t.Errorf("expected check interval 60, got %d", cfg.CheckIntervalSec)
	}
	if cfg.DefaultTimeoutMs != 3600000 {
		t.Errorf("expected default timeout 3600000, got %d", cfg.DefaultTimeoutMs)
	}

	cfg = ParseBackgroundMonitorConfig(map[string]any{
		"enabled":            false,
		"check_interval_sec": 30,
		"default_timeout_ms": 7200000,
	})
	if cfg.Enabled {
		t.Error("expected disabled")
	}
	if cfg.CheckIntervalSec != 30 {
		t.Errorf("expected check interval 30, got %d", cfg.CheckIntervalSec)
	}
	if cfg.DefaultTimeoutMs != 7200000 {
		t.Errorf("expected default timeout 7200000, got %d", cfg.DefaultTimeoutMs)
	}
}
