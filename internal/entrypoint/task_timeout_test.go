// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type capturingAdapter struct {
	LoggingAdapter
	mu       sync.Mutex
	errors   []capturedError
	warnings []capturedWarning
}

type capturedError struct {
	taskID string
	err    *a2a.Error
}

type capturedWarning struct {
	taskID string
	event  *a2a.TaskStatusUpdateEvent
}

func (a *capturingAdapter) OnError(_ context.Context, taskID string, err *a2a.Error, _ []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errors = append(a.errors, capturedError{taskID: taskID, err: err})
}

func (a *capturingAdapter) OnStatusUpdate(_ context.Context, taskID string, event *a2a.TaskStatusUpdateEvent, _ []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.warnings = append(a.warnings, capturedWarning{taskID: taskID, event: event})
}

func (a *capturingAdapter) Errors() []capturedError {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]capturedError, len(a.errors))
	copy(out, a.errors)
	return out
}

func (a *capturingAdapter) Warnings() []capturedWarning {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]capturedWarning, len(a.warnings))
	copy(out, a.warnings)
	return out
}

func newCapturingCoreWithHard(t *testing.T, timeout, hardTimeout int) (*GatewayCore, *capturingAdapter) {
	t.Helper()
	adapter := &capturingAdapter{LoggingAdapter: *NewLoggingAdapter(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	core := &GatewayCore{
		adapter:    adapter,
		agents:     NewAgentRegistry(),
		tasks:      NewMemoryTaskRegistry(),
		taskTimers: newTaskTimerRegistry(),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg: &GatewayConfig{
			TaskTimeoutSeconds:     timeout,
			TaskHardTimeoutSeconds: hardTimeout,
		},
	}
	core.eventBindActive.Store(true)
	return core, adapter
}

func TestTaskTimeout_SoftWarningDoesNotCancel(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 3600)
	taskID := "task-idle"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(adapter.Warnings()) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	warns := adapter.Warnings()
	if len(warns) != 1 {
		t.Fatalf("expected 1 soft-warning OnStatusUpdate, got %d", len(warns))
	}
	got := warns[0]
	if got.taskID != taskID {
		t.Errorf("taskID = %q, want %q", got.taskID, taskID)
	}
	if got.event == nil || got.event.Final {
		t.Fatalf("warning event must be non-final, got %+v", got.event)
	}
	if got.event.Status.Message == nil || len(got.event.Status.Message.Parts) != 1 {
		t.Fatalf("warning event missing data part: %+v", got.event)
	}
	data, ok := got.event.Status.Message.Parts[0].Data.(map[string]any)
	if !ok || data["type"] != TaskTimeoutWarningType {
		t.Errorf("warning data part type = %v, want %q", data["type"], TaskTimeoutWarningType)
	}

	if len(adapter.Errors()) != 0 {
		t.Errorf("soft warning must not emit OnError, got %d", len(adapter.Errors()))
	}
	if !core.IsTaskRunning(taskID) {
		t.Error("soft warning must leave the task tracked (agent still owns it)")
	}
	core.cancelTaskTimeout(taskID)
	core.ForgetTask(taskID)
}

func TestTaskTimeout_HardBackstopIsTerminal(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	taskID := "task-hard"
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if len(adapter.Errors()) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(adapter.Warnings()) == 0 {
		t.Error("expected a soft warning before the hard backstop fired")
	}

	errs := adapter.Errors()
	if len(errs) != 1 {
		t.Fatalf("expected 1 OnError call, got %d", len(errs))
	}
	got := errs[0]
	if got.taskID != taskID {
		t.Errorf("taskID = %q, want %q", got.taskID, taskID)
	}
	if got.err == nil || got.err.Code != TaskTimeoutErrorCode {
		t.Errorf("error code = %v, want %d", got.err, TaskTimeoutErrorCode)
	}
	if !IsTaskTimeoutError(got.err) {
		t.Errorf("IsTaskTimeoutError should report true for timeout error")
	}
	if got.err.Message != "The request timed out. Please try again." {
		t.Errorf("message = %q, want friendly timeout message", got.err.Message)
	}
	data, ok := got.err.Data.(map[string]any)
	if !ok {
		t.Fatalf("error.Data not a map: %T", got.err.Data)
	}
	if data["taskStatus"] != "timeout" {
		t.Errorf("data.taskStatus = %v, want 'timeout'", data["taskStatus"])
	}
	if _, ok := data["timeout_seconds"].(int); !ok {
		t.Errorf("data.timeout_seconds should be int, got %T", data["timeout_seconds"])
	}

	if core.IsTaskRunning(taskID) {
		t.Error("task should be removed from registry after hard timeout")
	}
}

func TestTaskTimeout_NotArmedForUnregisteredTask(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	core.startTaskTimeout("delegated-child-subtask")

	core.taskTimers.mu.Lock()
	_, armed := core.taskTimers.timers["delegated-child-subtask"]
	core.taskTimers.mu.Unlock()
	if armed {
		t.Fatal("startTaskTimeout must not arm a timer for an unregistered task id")
	}

	time.Sleep(1500 * time.Millisecond)
	if len(adapter.Errors()) != 0 {
		t.Fatalf("expected no timeout error for an unregistered task, got %d", len(adapter.Errors()))
	}
}

func TestTaskTimeout_ResetOnActivity(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 3600)
	taskID := "task-active"
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	for range 5 {
		time.Sleep(300 * time.Millisecond)
		core.startTaskTimeout(taskID)
	}

	if len(adapter.Warnings()) > 0 {
		t.Errorf("expected no soft warning during active task, got %d", len(adapter.Warnings()))
	}
	if len(adapter.Errors()) > 0 {
		t.Errorf("expected no timeout during active task, got %d errors", len(adapter.Errors()))
	}

	core.cancelTaskTimeout(taskID)
	core.ForgetTask(taskID)
}

func TestTaskTimeout_BothTiersResetOnActivity(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	taskID := "task-both-reset"
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	for range 5 {
		time.Sleep(500 * time.Millisecond)
		core.startTaskTimeout(taskID)
	}

	if len(adapter.Warnings()) != 0 {
		t.Errorf("soft timer should have been reset by activity, got %d warnings", len(adapter.Warnings()))
	}
	if len(adapter.Errors()) != 0 {
		t.Errorf("hard backstop should have been reset by activity, got %d errors", len(adapter.Errors()))
	}
	if !core.IsTaskRunning(taskID) {
		t.Error("task should still be running while active")
	}
	core.cancelTaskTimeout(taskID)
	core.ForgetTask(taskID)
}

func TestTaskTimeout_SoftWarningRearmsOnActivity(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 3600)
	taskID := "task-rearm"
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	waitFor := func(want int) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if len(adapter.Warnings()) >= want {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}

	waitFor(1)
	if len(adapter.Warnings()) != 1 {
		t.Fatalf("expected 1 warning after first idle stretch, got %d", len(adapter.Warnings()))
	}

	core.startTaskTimeout(taskID)
	waitFor(2)
	if len(adapter.Warnings()) != 2 {
		t.Fatalf("expected a second warning after activity re-armed the timer, got %d", len(adapter.Warnings()))
	}
	if len(adapter.Errors()) != 0 {
		t.Errorf("soft re-arm must not emit a terminal error, got %d", len(adapter.Errors()))
	}
	core.cancelTaskTimeout(taskID)
	core.ForgetTask(taskID)
}

func TestTaskTimeout_SoftOnlyNeverCancels(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 0)
	taskID := "task-soft-only"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(adapter.Warnings()) >= 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if len(adapter.Warnings()) < 1 {
		t.Fatalf("expected at least one soft warning, got %d", len(adapter.Warnings()))
	}
	if len(adapter.Errors()) != 0 {
		t.Errorf("hard tier disabled (hard=0) must never emit a terminal error, got %d", len(adapter.Errors()))
	}
	if !core.IsTaskRunning(taskID) {
		t.Error("task must stay running with the hard tier disabled")
	}
	core.cancelTaskTimeout(taskID)
	core.ForgetTask(taskID)
}

func TestTaskTimeout_SoftWarningFiresOncePerStretch(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 5)
	taskID := "task-once"
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	time.Sleep(2500 * time.Millisecond)

	if got := len(adapter.Warnings()); got != 1 {
		t.Fatalf("soft warning must fire exactly once per idle stretch, got %d", got)
	}
	if len(adapter.Errors()) != 0 {
		t.Errorf("hard backstop must not have fired yet, got %d errors", len(adapter.Errors()))
	}
	core.cancelTaskTimeout(taskID)
	core.ForgetTask(taskID)
}

func TestTaskTimeout_CanceledOnForget(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	taskID := "task-cancel"
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	core.ForgetTask(taskID)

	time.Sleep(2500 * time.Millisecond)
	if len(adapter.Errors()) != 0 {
		t.Errorf("expected no OnError after ForgetTask, got %d", len(adapter.Errors()))
	}
	if len(adapter.Warnings()) != 0 {
		t.Errorf("expected no warning after ForgetTask, got %d", len(adapter.Warnings()))
	}
}

func TestTaskTimeout_DisabledWhenZero(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 0, 1)
	taskID := "task-disabled"
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	time.Sleep(1500 * time.Millisecond)
	if len(adapter.Errors()) != 0 {
		t.Errorf("expected no timeouts when disabled, got %d", len(adapter.Errors()))
	}
	if len(adapter.Warnings()) != 0 {
		t.Errorf("expected no warnings when disabled, got %d", len(adapter.Warnings()))
	}
	core.ForgetTask(taskID)
}

func TestTaskTimeout_FinalizesTaskRow(t *testing.T) {
	core, _ := newCapturingCoreWithHard(t, 1, 2)
	tl, taskRepo := attachTimeoutTaskLogger(t, core)
	defer tl.Stop()

	taskID := tid("timeout-finalized")
	seedForegroundTask(t, taskRepo, taskID)
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	task := waitForFinalizedTask(t, taskRepo, taskID)
	if task.Status == nil || *task.Status != "failed" {
		t.Errorf("status = %q, want 'failed'", stringValue(task.Status))
	}
	if task.EndTime == nil {
		t.Error("end_time should be set after timeout")
	}
}

func TestB01_HardTimeoutFinalEventCarriesTheTaskTraceID(t *testing.T) {
	core, _ := newCapturingCoreWithHard(t, 1, 2)
	tl, taskRepo := attachTimeoutTaskLogger(t, core)
	defer tl.Stop()

	taskID := tid("timeout-traced")
	seedForegroundTask(t, taskRepo, taskID)
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{TraceID: b01TraceID})
	core.startTaskTimeout(taskID)
	waitForFinalizedTask(t, taskRepo, taskID)
	tl.Stop()

	events, err := tl.eventRepo.GetByTaskID(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("the timeout wrote no task event")
	}
	final := events[len(events)-1]
	if final.TraceID == nil || *final.TraceID != b01TraceID {
		t.Fatalf("timeout event trace_id = %v, want %q", final.TraceID, b01TraceID)
	}
}

func TestTaskTimeout_AgentCancelledReportFillsTokenUsage(t *testing.T) {
	core, _ := newCapturingCoreWithHard(t, 1, 2)
	tl, taskRepo := attachTimeoutTaskLogger(t, core)
	defer tl.Stop()

	taskID := tid("timeout-then-cancelled-report")
	seedForegroundTask(t, taskRepo, taskID)
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	timedOut := waitForFinalizedTask(t, taskRepo, taskID)
	if timedOut.Status == nil || *timedOut.Status != "failed" || timedOut.TotalInputTokens != nil {
		t.Fatalf("after hard timeout: status = %q, input tokens = %s; want 'failed' with no usage",
			stringValue(timedOut.Status), intValue(timedOut.TotalInputTokens))
	}

	payload, err := json.Marshal(a2a.NewResponse(taskID, &a2a.Task{
		Kind:   "task",
		ID:     taskID,
		Status: a2a.TaskStatus{State: a2a.TaskStateCancelled},
		Metadata: map[string]any{"token_usage": map[string]any{
			"total_input_tokens":        5000,
			"total_output_tokens":       300,
			"total_cached_input_tokens": 4200,
			"last_input_tokens":         900,
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	topic := "test-ns/a2a/v1/gateway/response/gw-test/" + taskID
	tl.handleResponseMessage(&broker.Message{Topic: topic, Payload: payload}, topic, a2a.UserProperties{UserID: "u1"})

	tl.Stop()

	task, err := taskRepo.GetByID(t.Context(), taskID)
	if err != nil || task == nil {
		t.Fatalf("task missing: %v", err)
	}
	assertTaskTokens(t, task, "failed", 5000, 300, 4200)
}

func attachTimeoutTaskLogger(t *testing.T, core *GatewayCore) (*TaskLoggerService, *store.TaskRepo) {
	t.Helper()
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{
		Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true,
		LogFileParts: true, MaxFilePartSizeBytes: 102400, QueueSize: 64,
	}
	tl := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := tl.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	core.taskLogger.Store(tl)
	return tl, taskRepo
}

func seedForegroundTask(t *testing.T, taskRepo *store.TaskRepo, taskID string) {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := taskRepo.Create(t.Context(), &store.Task{
		ID:               taskID,
		UserID:           "u1",
		StartTime:        now,
		ExecutionMode:    "foreground",
		LastActivityTime: &now,
	}); err != nil {
		t.Fatal(err)
	}
}

func waitForFinalizedTask(t *testing.T, taskRepo *store.TaskRepo, taskID string) *store.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := taskRepo.GetByID(t.Context(), taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task != nil && task.Status != nil && *task.Status != "" && task.EndTime != nil {
			return task
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("task row never finalized")
	return nil
}

func TestTaskTimerRegistry_StopAll(t *testing.T) {
	reg := newTaskTimerRegistry()
	var primaryFired, backstopFired atomicBool
	reg.startOrReset("t1",
		100*time.Millisecond, func(context.Context) { primaryFired.set(true) },
		120*time.Millisecond, func(context.Context) { backstopFired.set(true) })
	reg.stopAll()

	time.Sleep(300 * time.Millisecond)
	if primaryFired.get() {
		t.Error("primary timer should not have fired after stopAll")
	}
	if backstopFired.get() {
		t.Error("backstop timer should not have fired after stopAll")
	}
}

func TestTaskTimerRegistry_SweeperWindsDown(t *testing.T) {
	r := newTaskTimerRegistry()
	defer r.stopAll()

	r.startOrReset("two-tier", 20*time.Millisecond, func(context.Context) {}, 40*time.Millisecond, func(context.Context) {})
	r.startOrReset("soft-only", 20*time.Millisecond, func(context.Context) {}, 0, nil)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n, running := len(r.timers), r.running
		r.mu.Unlock()
		if n == 0 && !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	n, running := len(r.timers), r.running
	r.mu.Unlock()
	t.Fatalf("sweeper did not wind down: timers=%d running=%v", n, running)
}

type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (b *atomicBool) set(v bool) { b.mu.Lock(); b.v = v; b.mu.Unlock() }
func (b *atomicBool) get() bool  { b.mu.Lock(); defer b.mu.Unlock(); return b.v }

func TestTaskTimeout_HardBackstopSkipsATaskThatAlreadyEnded(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	core.onTaskHardTimeout(t.Context(), "task-ended-elsewhere", 2)
	if len(adapter.Errors()) != 0 {
		t.Fatalf("a task no longer running must not be reported as timed out: %+v", adapter.Errors())
	}
}

func TestTaskTimeout_SoftWarningSkipsATaskThatAlreadyEnded(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	core.onTaskIdleWarning(t.Context(), "task-ended-elsewhere", 1)
	if len(adapter.Warnings()) != 0 {
		t.Fatalf("a task no longer running must not get a still-working notice: %+v", adapter.Warnings())
	}
}

func TestTaskTimeout_SoftWarningReadsTheRowNotTheLocalCopy(t *testing.T) {
	submitting, other := twoDBRegistries(t)
	id := idgen.New()
	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	other.Remove(id)
	if submitting.Get(id) == nil {
		t.Fatal("the local copy must survive the other instance's Remove, or this test proves nothing")
	}

	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	core.tasks = submitting
	core.onTaskIdleWarning(t.Context(), id, 1)
	if len(adapter.Warnings()) != 0 {
		t.Fatalf("a task ended on another instance must not get a still-working notice: %+v", adapter.Warnings())
	}
}

func TestTaskTimeout_NotArmedForATaskHeldByAnotherInstance(t *testing.T) {
	holding, other := twoDBRegistries(t)
	id := idgen.New()
	if err := holding.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	if other.Get(id) == nil {
		t.Fatal("the row must be visible to the other instance, or this test proves nothing")
	}

	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	core.tasks = other
	core.startTaskTimeout(id)

	core.taskTimers.mu.Lock()
	_, armed := core.taskTimers.timers[id]
	core.taskTimers.mu.Unlock()
	if armed {
		t.Fatal("startTaskTimeout must not arm a timer for a task another instance holds")
	}
	if len(adapter.Errors()) != 0 {
		t.Fatalf("expected no timeout error, got %d", len(adapter.Errors()))
	}
}
