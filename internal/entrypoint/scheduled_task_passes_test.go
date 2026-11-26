// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func passesSvc(t *testing.T) *ScheduledTaskService {
	t.Helper()
	svc := setupIdentityTestCore(t).ScheduledTasks()
	if svc == nil {
		t.Fatal("ScheduledTasks() returned nil")
	}
	return svc
}

func dueTask(t *testing.T, svc *ScheduledTaskService, due time.Time, edit func(*store.ScheduledTask)) *store.ScheduledTask {
	t.Helper()
	dueMs := due.UnixMilli()
	task := makeIdentityTestTask(idgen.New(), "alice")
	task.ScheduleExpression = "0 0 1 1 *"
	task.Timezone = "UTC"
	task.Source = "ui"
	task.NextRunAt = &dueMs
	if edit != nil {
		edit(task)
	}
	insertTask(t, svc, task)
	return task
}

func taskRuns(t *testing.T, svc *ScheduledTaskService, taskID string) []*store.ScheduledTaskExecution {
	t.Helper()
	runs, err := svc.repo.FindExecutionsByTask(t.Context(), taskID, 100, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func reloadTask(t *testing.T, svc *ScheduledTaskService, id string) *store.ScheduledTask {
	t.Helper()
	task, err := svc.repo.GetByID(t.Context(), id)
	if err != nil || task == nil {
		t.Fatalf("reload task %s: %v, %v", id, task, err)
	}
	return task
}

func mustRunPasses(t *testing.T, svc *ScheduledTaskService, now time.Time) {
	t.Helper()
	if err := svc.runPasses(t.Context(), now); err != nil {
		t.Fatalf("runPasses: %v", err)
	}
}

func TestSchedulerPasses_TwoLeadersFireOnce(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	due := now.Add(-time.Second)
	task := dueTask(t, svc, due, func(st *store.ScheduledTask) {
		st.ScheduleType, st.ScheduleExpression = "interval", "300s"
	})

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := svc.firePass(t.Context(), now); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 || runs[0].Status != "pending" {
		t.Fatalf("runs = %d, want one pending run", len(runs))
	}
	got := reloadTask(t, svc, task.ID)
	if want := due.Add(300 * time.Second).UnixMilli(); got.NextRunAt == nil || *got.NextRunAt != want {
		t.Fatalf("next_run_at = %v, want %d", got.NextRunAt, want)
	}
}

func TestSchedulerPasses_MisfireGrace(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name        string
		late        time.Duration
		wantStatus  string
		wantMessage string
		wantLastRun bool
	}{
		{"two minutes late fires", 2 * time.Minute, "pending", "", true},
		{"twenty minutes late is skipped", 20 * time.Minute, "skipped", "missed by 20m0s, more than the misfire grace of 10m0s", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := passesSvc(t)
			task := dueTask(t, svc, now.Add(-tc.late), nil)
			if err := svc.firePass(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			runs := taskRuns(t, svc, task.ID)
			if len(runs) != 1 || runs[0].Status != tc.wantStatus {
				t.Fatalf("runs = %+v, want one %s run", runs, tc.wantStatus)
			}
			if tc.wantMessage != "" && (runs[0].ErrorMessage == nil || *runs[0].ErrorMessage != tc.wantMessage) {
				t.Fatalf("message = %v, want %q", runs[0].ErrorMessage, tc.wantMessage)
			}
			got := reloadTask(t, svc, task.ID)
			if got.NextRunAt == nil || *got.NextRunAt <= now.UnixMilli() {
				t.Fatalf("next_run_at = %v, want a time after now", got.NextRunAt)
			}
			if stamped := got.LastRunAt != nil; stamped != tc.wantLastRun {
				t.Fatalf("last_run_at = %v, want stamped %v", got.LastRunAt, tc.wantLastRun)
			}
		})
	}
}

func TestSchedulerPasses_SeveralMissedTimesFireOnce(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	due := now.Add(-5 * time.Minute)
	task := dueTask(t, svc, due, func(st *store.ScheduledTask) {
		st.ScheduleType, st.ScheduleExpression = "interval", "60s"
	})

	if err := svc.firePass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if got, want := reloadTask(t, svc, task.ID).NextRunAt, due.Add(6*time.Minute).UnixMilli(); got == nil || *got != want {
		t.Fatalf("next_run_at = %v, want %d on the interval's cadence", got, want)
	}
}

func TestSchedulerPasses_OlderOneTimeRowFiresOnce(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	at := now.Add(-time.Minute).UTC().Truncate(time.Second)
	task := makeIdentityTestTask(idgen.New(), "alice")
	task.ScheduleType, task.ScheduleExpression, task.Timezone, task.Source = "one_time", at.Format(time.RFC3339), "UTC", "ui"
	insertTask(t, svc, task)

	for range 2 {
		if err := svc.firePass(t.Context(), now); err != nil {
			t.Fatal(err)
		}
	}
	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	got := reloadTask(t, svc, task.ID)
	if got.NextRunAt != nil || got.LastRunAt == nil {
		t.Fatalf("after firing: next_run_at=%v last_run_at=%v, want NULL and set", got.NextRunAt, got.LastRunAt)
	}
}

func TestSchedulerPasses_SkipsWhileRunning(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	active := svc.pendingRun(task, now.Add(-time.Hour), 0, store.TriggerTypeScheduled, nil)
	active.Status = "running"
	if err := svc.repo.CreateExecution(t.Context(), active); err != nil {
		t.Fatal(err)
	}

	if err := svc.firePass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	var skipped *store.ScheduledTaskExecution
	for _, r := range taskRuns(t, svc, task.ID) {
		if r.ID != active.ID {
			skipped = r
		}
	}
	if skipped == nil || skipped.Status != "skipped" || skipped.FinalizedAt == nil || *skipped.FinalizedAt == 0 {
		t.Fatalf("skipped run = %+v, want skipped and already finalized", skipped)
	}
	if got := reloadTask(t, svc, task.ID); got.ConsecutiveFailureCount != 0 {
		t.Fatalf("consecutive_failure_count = %d, want 0", got.ConsecutiveFailureCount)
	}
}

func TestSchedulerPasses_AgentUnavailableCountsWithoutRetry(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) {
		st.TargetAgentName = "missing-agent"
		st.MaxRetries = 2
	})

	mustRunPasses(t, svc, now)

	runs := taskRuns(t, svc, task.ID)
	if len(runs) != 1 || runs[0].Status != "skipped" {
		t.Fatalf("runs = %+v, want one skipped run", runs)
	}
	if got := reloadTask(t, svc, task.ID); got.ConsecutiveFailureCount != 1 {
		t.Fatalf("consecutive_failure_count = %d, want 1", got.ConsecutiveFailureCount)
	}
}

func TestSchedulerPasses_OwnerUnauthorizedIsNotRetried(t *testing.T) {
	core := setupIdentityTestCore(t)
	signer, _ := installFailingResolver(t, core, fmt.Errorf("%w: deactivated", ErrIdentityUnauthorized))
	svc := core.ScheduledTasks()
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) { st.MaxRetries = 3 })

	mustRunPasses(t, svc, now)
	mustRunPasses(t, svc, now.Add(2*time.Minute))

	if signer.callCount() != 0 {
		t.Errorf("signer.calls = %d, want 0", signer.callCount())
	}
	runs := taskRuns(t, svc, task.ID)
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].ErrorMessage == nil || *runs[0].ErrorMessage != ownerUnauthorizedMessage {
		t.Fatalf("runs = %+v, want one failed run, not retried", runs)
	}
	if got := reloadTask(t, svc, task.ID); got.ConsecutiveFailureCount != 1 {
		t.Fatalf("consecutive_failure_count = %d, want 1", got.ConsecutiveFailureCount)
	}
}

func TestSchedulerPasses_TransientIdentityFailureIsRetried(t *testing.T) {
	core := setupIdentityTestCore(t)
	signer, resolver := installFailingResolver(t, core, fmt.Errorf("authz unreachable"))
	svc := core.ScheduledTasks()
	now := time.Now()
	const maxRetries = 2
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) {
		st.MaxRetries = maxRetries
		st.RetryDelaySeconds = 1
	})

	for i := range maxRetries + 2 {
		mustRunPasses(t, svc, now.Add(time.Duration(2*i)*time.Second))
	}

	if signer.callCount() != 0 {
		t.Errorf("signer.calls = %d, want 0", signer.callCount())
	}
	if resolver.callCount() != maxRetries+1 {
		t.Errorf("resolver.calls = %d, want %d", resolver.callCount(), maxRetries+1)
	}
	runs := taskRuns(t, svc, task.ID)
	if len(runs) != maxRetries+1 {
		t.Fatalf("runs = %d, want %d", len(runs), maxRetries+1)
	}
	for _, r := range runs {
		if r.Status != "failed" || r.ErrorMessage == nil || *r.ErrorMessage != identityResolutionFailedMessage {
			t.Errorf("run = {status %q, error %v}, want failed with the fixed identity message", r.Status, r.ErrorMessage)
		}
		if r.ErrorMessage != nil && contains(*r.ErrorMessage, "authz unreachable") {
			t.Errorf("the stored error exposes the underlying authz error: %q", *r.ErrorMessage)
		}
	}
	if got := reloadTask(t, svc, task.ID); got.ConsecutiveFailureCount != maxRetries+1 {
		t.Fatalf("consecutive_failure_count = %d, want %d", got.ConsecutiveFailureCount, maxRetries+1)
	}
}

func TestSchedulerPasses_SendClaimsOnce(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	if err := svc.firePass(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := svc.sendPass(t.Context(), now); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	runs := taskRuns(t, svc, task.ID)
	if len(runs) != 1 || runs[0].Status != "running" || runs[0].SubmittedAt == nil || *runs[0].SubmittedAt != now.UnixMilli() {
		t.Fatalf("runs = %+v, want one running run claimed at now", runs)
	}
	if id := svc.runSessionID(t.Context(), runs[0].ID); id == "" {
		t.Fatal("the sent run has no session")
	}
}

func TestSchedulerPasses_TimeoutEndsOverdueRuns(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), func(st *store.ScheduledTask) { st.TimeoutSeconds = 60 })
	started := now.Add(-2 * time.Minute).UnixMilli()
	running := svc.pendingRun(task, now.Add(-2*time.Minute), 0, store.TriggerTypeScheduled, nil)
	running.Status, running.StartedAt, running.SubmittedAt = "running", &started, &started
	if err := svc.repo.CreateExecution(t.Context(), running); err != nil {
		t.Fatal(err)
	}
	var older int64
	olderRun := svc.pendingRun(task, now.Add(-2*time.Minute), 0, store.TriggerTypeScheduled, nil)
	olderRun.Status, olderRun.StartedAt, olderRun.SubmittedAt, olderRun.FinalizedAt = "running", &started, &older, &older
	if err := svc.repo.CreateExecution(t.Context(), olderRun); err != nil {
		t.Fatal(err)
	}

	if err := svc.timeoutPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	for _, r := range taskRuns(t, svc, task.ID) {
		if r.Status != "timeout" {
			t.Errorf("run %s status = %q, want timeout", r.ID, r.Status)
		}
	}
	if got := reloadTask(t, svc, task.ID); got.ConsecutiveFailureCount != 1 {
		t.Fatalf("consecutive_failure_count = %d, want 1 (the older-version run is not counted)", got.ConsecutiveFailureCount)
	}
}

func TestSchedulerPasses_LeaveOlderVersionAndManualRowsAlone(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), nil)
	var zero int64
	for _, status := range []string{"pending", "completed"} {
		run := svc.pendingRun(task, now.Add(-time.Second), 0, store.TriggerTypeManual, nil)
		run.Status, run.SubmittedAt, run.FinalizedAt = status, &zero, &zero
		if err := svc.repo.CreateExecution(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}

	mustRunPasses(t, svc, now)

	for _, r := range taskRuns(t, svc, task.ID) {
		if r.SubmittedAt == nil || *r.SubmittedAt != 0 || r.FinalizedAt == nil || *r.FinalizedAt != 0 {
			t.Errorf("run %s (%s) was touched: submitted_at=%v finalized_at=%v", r.ID, r.Status, r.SubmittedAt, r.FinalizedAt)
		}
	}
}

func endedRunWithSession(t *testing.T, svc *ScheduledTaskService, task *store.ScheduledTask, status string, now time.Time) (*store.ScheduledTaskExecution, string) {
	t.Helper()
	run := svc.pendingRun(task, now.Add(-time.Minute), 0, store.TriggerTypeScheduled, nil)
	started := now.Add(-time.Minute).UnixMilli()
	run.Status, run.StartedAt, run.SubmittedAt = status, &started, &started
	if err := svc.repo.CreateExecution(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	return run, svc.runSession(t.Context(), task, run.ID, svc.resolveUserID(task))
}

func storeEvent(t *testing.T, svc *ScheduledTaskService, taskID, sessionID string, seq int, eventType string, payload a2a.Response) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.sseRepo.Append(t.Context(), &store.SSEEventBufferRow{
		TaskID: taskID, SessionID: sessionID, UserID: "alice", EventSequence: seq,
		EventType: eventType, EventData: string(raw), CreatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerPasses_FinishSavesChatWithCitations(t *testing.T) {
	svc := passesSvc(t)
	notifier := &fakeUserNotifier{}
	svc.SetUserNotifier(notifier)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), nil)
	run, sessionID := endedRunWithSession(t, svc, task, "completed", now)
	a2aTaskID := *run.A2ATaskID

	storeEvent(t, svc, a2aTaskID, sessionID, 1, "status_update", a2a.Response{JSONRPC: "2.0", ID: a2aTaskID, Result: a2a.TaskStatusUpdateEvent{
		TaskID: a2aTaskID,
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{
			ragInfoUpdateDataPart(t, a2a.RAGInfoUpdateData{
				Type: string(a2a.SignalRAGInfoUpdate), Title: "Web Results", Query: "solace", SearchType: "web_search",
				Sources:    []a2a.RAGSource{{CitationID: "s1r0", Title: "Solace", SourceURL: "https://solace.com", URL: "https://solace.com"}},
				IsComplete: true, Timestamp: "2024-01-01T00:00:00Z",
			}),
		}}},
	}})
	storeEvent(t, svc, a2aTaskID, sessionID, 2, "final_response", a2a.Response{JSONRPC: "2.0", ID: a2aTaskID,
		Result: finalTask(a2a.TaskStateCompleted, "the answer")})

	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.repo.GetExecutionByID(t.Context(), run.ID); got.FinalizedAt != nil {
		t.Fatal("finished before the stored events were marked completed")
	}

	if err := svc.sseRepo.MarkCompleted(t.Context(), a2aTaskID); err != nil {
		t.Fatal(err)
	}
	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.repo.GetExecutionByID(t.Context(), run.ID); got.FinalizedAt == nil {
		t.Fatal("run not finalized")
	} else if got.ResultSummary == nil || !contains(*got.ResultSummary, "https://solace.com") {
		t.Fatalf("result_summary = %v, want the citation", got.ResultSummary)
	}
	chat, err := svc.chatRepo.GetByID(t.Context(), a2aTaskID)
	if err != nil || chat == nil {
		t.Fatalf("chat task = %v, %v", chat, err)
	}
	if !contains(chat.MessageBubbles, "the answer") || chat.TaskMetadata == nil || !contains(*chat.TaskMetadata, "https://solace.com") {
		t.Fatalf("chat = %s / %v, want the answer and its citation", chat.MessageBubbles, chat.TaskMetadata)
	}
	var sessionCreated int
	for _, c := range notifier.Calls() {
		if c.EventType == "session_created" {
			sessionCreated++
		}
	}
	if sessionCreated == 0 {
		t.Fatal("no session_created event")
	}

	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	var after int
	for _, c := range notifier.Calls() {
		if c.EventType == "session_created" {
			after++
		}
	}
	if after != sessionCreated {
		t.Fatalf("a finalized run was finished again: session_created %d then %d", sessionCreated, after)
	}
}

func TestSchedulerPasses_FinishWithoutCompletionMarkAfterDeadline(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), func(st *store.ScheduledTask) { st.TimeoutSeconds = 30 })
	run, _ := endedRunWithSession(t, svc, task, "completed", now)

	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.repo.GetExecutionByID(t.Context(), run.ID); got.FinalizedAt == nil {
		t.Fatal("a run past its timeout must finish without waiting for its stored events")
	}
}

func TestSchedulerPasses_ALaterUnreadableFinalRowKeepsTheReadableOne(t *testing.T) {
	tests := map[string]struct {
		status   string
		readable func(taskID string) a2a.Response
		want     string
	}{
		"a completed run keeps the answer it read": {
			"completed",
			func(taskID string) a2a.Response {
				return a2a.Response{JSONRPC: "2.0", ID: taskID, Result: finalTask(a2a.TaskStateCompleted, "the quarterly numbers are ready")}
			},
			"the quarterly numbers are ready",
		},
		"a failed run keeps the agent's error it read": {
			"failed",
			func(taskID string) a2a.Response {
				return a2a.Response{JSONRPC: "2.0", ID: taskID, Error: &a2a.Error{Code: -32000, Message: "agent blew up"}}
			},
			"agent blew up",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc := passesSvc(t)
			now := time.Now()
			task := dueTask(t, svc, now.Add(time.Hour), nil)
			run, sessionID := endedRunWithSession(t, svc, task, tc.status, now)
			a2aTaskID := *run.A2ATaskID
			storeEvent(t, svc, a2aTaskID, sessionID, 1, "final_response", tc.readable(a2aTaskID))
			if err := svc.sseRepo.Append(t.Context(), &store.SSEEventBufferRow{
				TaskID: a2aTaskID, SessionID: sessionID, UserID: "alice", EventSequence: 2,
				EventType: "final_response", EventData: `{"jsonrpc": "2.0", "result": {`, CreatedAt: time.Now().UnixMilli(),
			}); err != nil {
				t.Fatalf("append the corrupt final row: %v", err)
			}
			if err := svc.sseRepo.MarkCompleted(t.Context(), a2aTaskID); err != nil {
				t.Fatalf("MarkCompleted(%s): %v", a2aTaskID, err)
			}

			if err := svc.finishPass(t.Context(), now); err != nil {
				t.Fatalf("finishPass: %v", err)
			}

			chat, err := svc.chatRepo.GetByID(t.Context(), a2aTaskID)
			if err != nil || chat == nil || !contains(chat.MessageBubbles, tc.want) {
				t.Fatalf("saved chat = %v, %v, want the readable final's %q", chat, err, tc.want)
			}
		})
	}
}

func TestSchedulerPasses_FinishFailedRunChat(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), nil)

	answered, sessionID := endedRunWithSession(t, svc, task, "failed", now)
	storeEvent(t, svc, *answered.A2ATaskID, sessionID, 1, "final_response", a2a.Response{JSONRPC: "2.0", ID: *answered.A2ATaskID,
		Error: &a2a.Error{Code: -32000, Message: "agent blew up"}})
	beforeAgent, _ := endedRunWithSession(t, svc, task, "failed", now)

	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	chat, err := svc.chatRepo.GetByID(t.Context(), *answered.A2ATaskID)
	if err != nil || chat == nil || !contains(chat.MessageBubbles, "agent blew up") {
		t.Fatalf("answered failure chat = %v, %v; want the agent's error", chat, err)
	}
	if chat, _ := svc.chatRepo.GetByID(t.Context(), *beforeAgent.A2ATaskID); chat != nil {
		t.Fatalf("a run that failed before the agent took part got a chat: %s", chat.MessageBubbles)
	}
	for _, id := range []string{answered.ID, beforeAgent.ID} {
		if got, _ := svc.repo.GetExecutionByID(t.Context(), id); got.FinalizedAt == nil {
			t.Errorf("run %s not finalized", id)
		}
	}
}

func TestSchedulerPasses_AutoPauseThroughTheEndWrite(t *testing.T) {
	svc := passesSvc(t)
	svc.cfg.AutoPauseAfterConsecutiveFailures = 2
	notifier := &fakeUserNotifier{}
	svc.SetUserNotifier(notifier)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) {
		st.TargetAgentName = "missing-agent"
		st.ConsecutiveFailureCount = 1
	})

	mustRunPasses(t, svc, now)

	got := reloadTask(t, svc, task.ID)
	if got.Enabled || got.NextRunAt != nil || got.ConsecutiveFailureCount != 2 {
		t.Fatalf("task = enabled %v next %v failures %d, want paused at 2", got.Enabled, got.NextRunAt, got.ConsecutiveFailureCount)
	}
	var paused bool
	for _, c := range notifier.Calls() {
		paused = paused || c.EventType == "task_auto_paused"
	}
	if !paused {
		t.Fatal("no task_auto_paused event")
	}
}

func TestSchedulerPasses_InvalidStoredExpressionStopsFiring(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) { st.ScheduleExpression = "not a cron" })

	if err := svc.firePass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 || runs[0].Status != "skipped" || runs[0].ErrorMessage == nil || !contains(*runs[0].ErrorMessage, "invalid schedule") {
		t.Fatalf("runs = %+v, want one skipped run naming the invalid schedule", runs)
	}
	if got := reloadTask(t, svc, task.ID); got.NextRunAt != nil {
		t.Fatalf("next_run_at = %v, want NULL so it stops coming due", *got.NextRunAt)
	}
}

func TestSchedulerPasses_DatabaseDownReportsEveryPass(t *testing.T) {
	svc := passesSvc(t)
	closed, err := store.Open("sqlite:///"+t.TempDir()+"/closed.db", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := closed.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	svc.repo = store.NewScheduledTaskRepo(closed)

	err = svc.RunPasses(t.Context())
	for _, pass := range []string{"never-fired", "unsubmitted", "overdue", "unfinalized"} {
		if err == nil || !contains(err.Error(), pass) {
			t.Errorf("RunPasses error = %v, want one naming %q", err, pass)
		}
	}
}

func TestSchedulerPasses_LeaderJobRunsThePasses(t *testing.T) {
	svc := passesSvc(t)
	task := dueTask(t, svc, time.Now().Add(-time.Second), nil)

	if err := svc.core.runScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("runs = %+v, want one sent run", runs)
	}
	if err := (&GatewayCore{}).runScheduler(t.Context()); err != nil {
		t.Fatalf("a gateway without a scheduler: %v", err)
	}
}

func TestFailureChatText(t *testing.T) {
	interactive := sanitizeError(interactiveStateErrorMessage)
	other := "identity resolution failed: down"
	tests := []struct {
		name  string
		final storedFinal
		row   *string
		want  string
	}{
		{"agent error", storedFinal{rpcErr: &a2a.Error{Message: "boom"}}, &other, "boom"},
		{"agent ended the task failed", storedFinal{task: finalTask(a2a.TaskStateFailed, "tool failed")}, &other, "tool failed"},
		{"asked for input", storedFinal{}, &interactive, interactive},
		{"failed before the agent took part", storedFinal{}, &other, ""},
		{"no message", storedFinal{}, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := failureChatText(tc.final, &store.ScheduledTaskExecution{ErrorMessage: tc.row}); got != tc.want {
				t.Fatalf("failureChatText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSchedulerPasses_NoSendStartsAfterStop(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	if err := svc.firePass(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_ = svc.sendPass(ctx, now)

	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 || runs[0].Status != "pending" || runs[0].SubmittedAt != nil {
		t.Fatalf("runs = %+v, want the run left pending for the next leader", runs)
	}
}

func TestSchedulerPasses_RefusedPublishIsSentAgain(t *testing.T) {
	svc := passesSvc(t)
	brk := svc.core.svc.Broker()
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)

	if err := brk.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	mustRunPasses(t, svc, now)
	runs := taskRuns(t, svc, task.ID)
	if len(runs) != 1 || runs[0].Status != "pending" || runs[0].SubmittedAt != nil {
		t.Fatalf("runs = %+v, want the run still pending after a refused publish", runs)
	}
	session := svc.runSessionID(t.Context(), runs[0].ID)

	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	mustRunPasses(t, svc, now.Add(5*time.Second))
	runs = taskRuns(t, svc, task.ID)
	if len(runs) != 1 || runs[0].Status != "running" || runs[0].SubmittedAt == nil {
		t.Fatalf("runs = %+v, want the run sent on the next pass", runs)
	}
	if got := svc.runSessionID(t.Context(), runs[0].ID); got != session || got == "" {
		t.Fatalf("session = %q, want the first send's %q", got, session)
	}
}

type entrypointAdapter struct{ GatewayAdapter }

func TestOnlyASchedulerHostBuildsAScheduler(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	appConfig := map[string]any{
		"namespace":         "test-ns",
		"gateway_id":        "test-entrypoint",
		"session_service":   map[string]any{"type": "sql", "database_url": store.TestSQLiteDBURL(t)},
		"scheduler_service": map[string]any{"enabled": true},
	}
	core := NewGatewayCore(entrypointAdapter{&recordingAdapter{}}, appConfig)
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	if core.ScheduledTasks() != nil {
		t.Fatal("an entrypoint built a scheduler")
	}
}

func TestSchedulerPasses_UndiscoveredAgentWaitsOutTheWarmup(t *testing.T) {
	svc := passesSvc(t)
	svc.core.startedAtNanos.Store(time.Now().UnixNano())
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) { st.TargetAgentName = "late-agent" })

	mustRunPasses(t, svc, now)
	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 || runs[0].Status != "pending" {
		t.Fatalf("runs = %+v, want the run left pending while agent cards arrive", runs)
	}

	svc.core.targetDiscoveryWarmup = 0
	mustRunPasses(t, svc, now.Add(5*time.Second))
	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 || runs[0].Status != "skipped" {
		t.Fatalf("runs = %+v, want the run skipped once the warm-up is over", runs)
	}
}

func TestSchedulerPasses_OlderVersionRunDoesNotHoldTheScheduleBack(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	var zero int64
	stuck := svc.pendingRun(task, now.Add(-10*time.Minute), 0, store.TriggerTypeScheduled, nil)
	stuck.Status, stuck.SubmittedAt, stuck.FinalizedAt = "running", &zero, &zero
	if err := svc.repo.CreateExecution(t.Context(), stuck); err != nil {
		t.Fatal(err)
	}

	if err := svc.firePass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	for _, r := range taskRuns(t, svc, task.ID) {
		if r.ID != stuck.ID && r.Status != "pending" {
			t.Fatalf("new run = %s (%v), want pending despite the older version's running row", r.Status, r.ErrorMessage)
		}
	}
	if n := len(taskRuns(t, svc, task.ID)); n != 2 {
		t.Fatalf("runs = %d, want the stuck row and one new run", n)
	}
}

func TestSchedulerPasses_DelegationDeniedEndsTheRun(t *testing.T) {
	core := setupIdentityTestCore(t)
	_, resolver := installFailingResolver(t, core, nil)
	resolver.ident = auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"agent:other-agent:invoke"}})
	svc := core.ScheduledTasks()
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) { st.MaxRetries = 3 })

	mustRunPasses(t, svc, now)
	mustRunPasses(t, svc, now.Add(5*time.Second))

	runs := taskRuns(t, svc, task.ID)
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].ErrorMessage == nil || *runs[0].ErrorMessage != ownerDeniedTargetMessage {
		t.Fatalf("runs = %+v, want one failed run, not retried and not left pending", runs)
	}
}

func TestSchedulerPasses_RetiredKeysWarnAndStart(t *testing.T) {
	capture := newLogCapture()
	originalDefault := slog.Default()
	slog.SetDefault(capture.logger)
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{
			"enabled":                         true,
			"max_concurrent_executions":       5,
			"stale_execution_timeout_seconds": 7200,
			"stale_cleanup_interval_seconds":  600,
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	if core.ScheduledTasks() == nil {
		t.Fatal("scheduler must still start when retired keys are set")
	}
	logs := capture.text()
	for _, want := range []string{"scheduler_service.max_concurrent_executions is ignored", "stale_execution_timeout_seconds", "stale_cleanup_interval_seconds"} {
		if !strings.Contains(logs, want) {
			t.Errorf("no warning naming %q; logs:\n%s", want, logs)
		}
	}
}

func TestSchedulerPasses_BusySchedulerStillFires(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	busy := dueTask(t, svc, now.Add(time.Hour), func(st *store.ScheduledTask) { st.Name = "busy" })
	for range 50 {
		started := now.Add(-time.Minute).UnixMilli()
		run := svc.pendingRun(busy, now.Add(-time.Minute), 0, store.TriggerTypeManual, nil)
		run.Status, run.StartedAt, run.SubmittedAt = "running", &started, &started
		if err := svc.repo.CreateExecution(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	due := dueTask(t, svc, now.Add(-time.Second), nil)
	manual := dueTask(t, svc, now.Add(time.Hour), func(st *store.ScheduledTask) { st.Name = "manual" })

	mustRunPasses(t, svc, now)
	if runs := taskRuns(t, svc, due.ID); len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("due fire with 50 other runs in flight = %+v, want one run sent", runs)
	}
	if _, err := svc.TriggerTaskNow(t.Context(), manual.ID, "alice"); err != nil {
		t.Fatalf("manual run with 50 other runs in flight: %v", err)
	}
}

func TestSchedulerPasses_FireWhileTaskRunningIsSkipped(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	started := now.Add(-time.Minute).UnixMilli()
	inFlight := svc.pendingRun(task, now.Add(-time.Minute), 0, store.TriggerTypeScheduled, nil)
	inFlight.Status, inFlight.StartedAt, inFlight.SubmittedAt = "running", &started, &started
	if err := svc.repo.CreateExecution(t.Context(), inFlight); err != nil {
		t.Fatal(err)
	}

	if err := svc.firePass(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	var skipped []*store.ScheduledTaskExecution
	for _, r := range taskRuns(t, svc, task.ID) {
		if r.ID != inFlight.ID {
			skipped = append(skipped, r)
		}
	}
	if len(skipped) != 1 || skipped[0].Status != "skipped" || skipped[0].ErrorMessage == nil || *skipped[0].ErrorMessage != "previous execution still running" {
		t.Fatalf("runs besides the one in flight = %+v, want one skipped with %q", skipped, "previous execution still running")
	}
}

func TestSchedulerPasses_PendingPastItsTimeoutIsNotSent(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), func(st *store.ScheduledTask) { st.TimeoutSeconds = 3600 })
	late := svc.pendingRun(task, now.Add(-2*time.Hour), 1, store.TriggerTypeScheduled, nil)
	if err := svc.repo.CreateExecution(t.Context(), late); err != nil {
		t.Fatal(err)
	}

	mustRunPasses(t, svc, now)

	got, _ := svc.repo.GetExecutionByID(t.Context(), late.ID)
	if got.Status != "timeout" || got.SubmittedAt != nil || got.ErrorMessage == nil || *got.ErrorMessage != neverSentMessage {
		t.Fatalf("run = {status %q, submitted %v, error %v}, want timeout, never sent", got.Status, got.SubmittedAt, got.ErrorMessage)
	}
}

func TestSchedulerPasses_PausedScheduleSkipsItsRetry(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), func(st *store.ScheduledTask) { st.Enabled = false })
	retry := svc.pendingRun(task, now.Add(-time.Second), 1, store.TriggerTypeScheduled, nil)
	if err := svc.repo.CreateExecution(t.Context(), retry); err != nil {
		t.Fatal(err)
	}

	mustRunPasses(t, svc, now)

	got, _ := svc.repo.GetExecutionByID(t.Context(), retry.ID)
	if got.Status != "skipped" || got.ErrorMessage == nil || *got.ErrorMessage != schedulePausedMessage {
		t.Fatalf("retry of a paused schedule = {status %q, error %v}, want skipped as paused", got.Status, got.ErrorMessage)
	}
	if _, err := svc.TriggerTaskNow(t.Context(), task.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	mustRunPasses(t, svc, now.Add(time.Second))
	for _, r := range taskRuns(t, svc, task.ID) {
		if r.TriggerType == store.TriggerTypeManual && r.Status != "running" {
			t.Fatalf("manual run on a paused schedule = %q, want sent", r.Status)
		}
	}
}

func TestSchedulerPasses_StoredEventsReadErrorRetriesTheFinish(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), nil)
	run, _ := endedRunWithSession(t, svc, task, "failed", now)
	db := svc.core.Store()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE sse_event_buffer RENAME TO sse_away`); err != nil {
		t.Fatal(err)
	}

	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.repo.GetExecutionByID(t.Context(), run.ID); got.FinalizedAt != nil {
		t.Fatal("finished although its stored events could not be read")
	}

	if _, err := db.ExecContext(t.Context(), `ALTER TABLE sse_away RENAME TO sse_event_buffer`); err != nil {
		t.Fatal(err)
	}
	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.repo.GetExecutionByID(t.Context(), run.ID); got.FinalizedAt == nil {
		t.Fatal("not finished once the stored events could be read")
	}
}

func TestSchedulerPasses_CompletionMarkWaitIsShort(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), nil)
	recent, _ := endedRunWithSession(t, svc, task, "completed", now)
	older, _ := endedRunWithSession(t, svc, task, "completed", now)
	for id, ended := range map[string]time.Time{recent.ID: now.Add(-5 * time.Second), older.ID: now.Add(-completionMarkWait - time.Second)} {
		if err := svc.repo.UpdateExecution(t.Context(), id, map[string]any{"completed_at": ended.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.repo.GetExecutionByID(t.Context(), recent.ID); got.FinalizedAt != nil {
		t.Fatal("a run that ended 5 s ago finished without waiting for its completion mark")
	}
	if got, _ := svc.repo.GetExecutionByID(t.Context(), older.ID); got.FinalizedAt == nil {
		t.Fatal("a run that ended past the wait is still waiting for its completion mark")
	}
}

func TestFailureChatText_InputRequestWinsOverTheCancel(t *testing.T) {
	message := sanitizeError(interactiveStateErrorMessage)
	run := &store.ScheduledTaskExecution{ErrorMessage: &message}
	if got := failureChatText(storedFinal{task: finalTask(a2a.TaskStateCancelled, "Task was cancelled.")}, run); got != message {
		t.Fatalf("chat text = %q, want the input-request message", got)
	}
}

func TestSchedulerPasses_FailedManualRunIsNotRetried(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(time.Hour), func(st *store.ScheduledTask) {
		st.MaxRetries = 2
		st.RetryDelaySeconds = 1
	})
	if _, err := svc.TriggerTaskNow(t.Context(), task.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	mustRunPasses(t, svc, now)
	run := taskRuns(t, svc, task.ID)[0]

	otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, nil, &a2a.Error{Code: -32000, Message: "agent failed"})
	mustRunPasses(t, svc, now.Add(5*time.Second))

	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 || runs[0].Status != "failed" {
		t.Fatalf("runs = %+v, want the failed manual run and no retry", runs)
	}
}

func TestSchedulerPasses_RecordedAgainstOwnerSignedAsCreator(t *testing.T) {
	core := setupIdentityTestCore(t)
	signer := &recordingSigner{}
	core.SetTaskSigner(signer)
	resolver := &stubIdentityResolver{ident: auth.NewTestSubject(auth.SubjectFields{
		UserID: "alice", Scopes: []string{"agent:test-agent:invoke"}, Authenticated: true,
	})}
	core.SetIdentityResolver(resolver)
	brk := core.Services().Broker()
	q, err := brk.CreateQueue("owner-attribution-agent", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "test-agent")); err != nil {
		t.Fatal(err)
	}

	svc := core.ScheduledTasks()
	now := time.Now()
	owner := "bob"
	dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) { st.UserID = &owner })
	mustRunPasses(t, svc, now)

	if got := resolver.askedFor(); got != "alice" {
		t.Errorf("resolved the identity of %q, want the creator alice", got)
	}
	if id := signer.firstIdentity(); id == nil || id.UserID() != "alice" {
		t.Errorf("signed as %v, want the creator alice", id)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil || msg == nil {
		t.Fatalf("the agent received nothing: %v", err)
	}
	msg.Ack()
	if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != owner {
		t.Errorf("published user id = %q, want the owner %q", got, owner)
	}
}
