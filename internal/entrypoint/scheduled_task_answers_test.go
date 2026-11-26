// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func otherGWE(svc *ScheduledTaskService) *ScheduledTaskService {
	other := NewScheduledTaskService(svc.cfg, svc.repo, svc.sessRepo, svc.chatRepo, svc.sessKeyRepo, svc.sseRepo, slog.Default())
	other.core = svc.core
	return other
}

func sentRun(t *testing.T, svc *ScheduledTaskService, taskID string, now time.Time) *store.ScheduledTaskExecution {
	t.Helper()
	mustRunPasses(t, svc, now)
	for _, r := range taskRuns(t, svc, taskID) {
		if r.Status == "running" {
			return r
		}
	}
	t.Fatal("no running run")
	return nil
}

func TestSchedulerAnswers_RetryRules(t *testing.T) {
	tests := []struct {
		name         string
		state        a2a.TaskState
		maxRetries   int
		wantAttempts int
	}{
		{"canceled run is not retried", a2a.TaskStateCancelled, 2, 1},
		{"failed run is retried until the retries are used up", a2a.TaskStateFailed, 2, 3},
		{"rejected run is retried", a2a.TaskStateRejected, 1, 2},
		{"failed run with no retries configured", a2a.TaskStateFailed, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := passesSvc(t)
			other := otherGWE(svc)
			now := time.Now()
			task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) {
				st.MaxRetries = tc.maxRetries
				st.RetryDelaySeconds = 1
			})
			reply := "reply for " + tc.name

			for i := range tc.maxRetries + 2 {
				at := now.Add(time.Duration(2*i) * time.Second)
				mustRunPasses(t, svc, at)
				for _, r := range taskRuns(t, svc, task.ID) {
					if r.Status == "running" {
						other.handleAnswer(t.Context(), *r.A2ATaskID, finalTask(tc.state, reply), nil)
					}
				}
			}

			runs := taskRuns(t, svc, task.ID)
			if len(runs) != tc.wantAttempts {
				t.Fatalf("attempts = %d, want %d", len(runs), tc.wantAttempts)
			}
			for _, r := range runs {
				if r.Status != "failed" || r.ErrorMessage == nil || *r.ErrorMessage != reply {
					t.Errorf("run = {status %q, error %v}, want failed with %q", r.Status, r.ErrorMessage, reply)
				}
			}
			if got := reloadTask(t, svc, task.ID); got.ConsecutiveFailureCount != tc.wantAttempts {
				t.Errorf("consecutive_failure_count = %d, want %d", got.ConsecutiveFailureCount, tc.wantAttempts)
			}
		})
	}
}

func TestSchedulerAnswers_CompletedOnAnotherGWEOnce(t *testing.T) {
	svc := passesSvc(t)
	other := otherGWE(svc)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) { st.RunCount = 2 })
	run := sentRun(t, svc, task.ID, now)

	other.handleAnswer(t.Context(), *run.A2ATaskID, finalTask(a2a.TaskStateCompleted, "the answer"), nil)
	other.handleAnswer(t.Context(), *run.A2ATaskID, finalTask(a2a.TaskStateFailed, "late"), nil)
	svc.handleAnswer(t.Context(), *run.A2ATaskID, nil, &a2a.Error{Message: "duplicate"})

	got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "completed" || got.ResultSummary == nil || !contains(*got.ResultSummary, "the answer") || got.ErrorMessage != nil {
		t.Fatalf("run = %+v, want completed with the answer", got)
	}
	if st := reloadTask(t, svc, task.ID); st.RunCount != 3 || st.ConsecutiveFailureCount != 0 {
		t.Fatalf("counters = run %d failures %d, want 3 and 0", st.RunCount, st.ConsecutiveFailureCount)
	}
}

func TestSchedulerAnswers_InteractiveStateOnAnotherGWE(t *testing.T) {
	svc := passesSvc(t)
	other := otherGWE(svc)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := sentRun(t, svc, task.ID, now)

	other.handleInteractiveState(t.Context(), *run.A2ATaskID, &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking}})
	if got, _ := svc.repo.GetExecutionByID(t.Context(), run.ID); got.Status != "running" {
		t.Fatalf("a working update ended the run: %s", got.Status)
	}
	other.handleInteractiveState(t.Context(), *run.A2ATaskID, &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateInputNeeded}})

	got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.ErrorMessage == nil || *got.ErrorMessage != sanitizeError(interactiveStateErrorMessage) {
		t.Fatalf("run = {status %q, error %v}, want failed asking for input", got.Status, got.ErrorMessage)
	}
}

func TestSchedulerAnswers_DatabaseDownUntilStopKeepsTheAnswer(t *testing.T) {
	svc := passesSvc(t)
	closed, err := store.Open("sqlite:///"+t.TempDir()+"/closed.db", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := closed.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	broken := NewScheduledTaskService(svc.cfg, store.NewScheduledTaskRepo(closed), nil, nil, nil, nil, slog.Default())

	acked := false
	stopping, stop := context.WithTimeout(t.Context(), 2*taskStateRetryMin)
	defer stop()
	ctx, d := delivery.Attach(stopping, "msg-1", false, func() { acked = true })
	broken.handleAnswer(ctx, "00000000-0000-7000-8000-000000000001", finalTask(a2a.TaskStateCompleted, "x"), nil)
	d.Release()

	if !d.Kept() || acked {
		t.Fatalf("kept=%v acked=%v, want the answer kept unacknowledged", d.Kept(), acked)
	}
}

func TestSchedulerAnswers_DatabaseBackWritesTheAnswer(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := sentRun(t, svc, task.ID, now)
	db := svc.core.Store()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE scheduled_task_executions RENAME TO executions_away`); err != nil {
		t.Fatal(err)
	}
	restored := make(chan error, 1)
	time.AfterFunc(taskStateRetryMin/2, func() {
		_, err := db.ExecContext(t.Context(), `ALTER TABLE executions_away RENAME TO scheduled_task_executions`)
		restored <- err
	})

	acked := false
	ctx, d := delivery.Attach(t.Context(), "msg-1", false, func() { acked = true })
	otherGWE(svc).handleAnswer(ctx, *run.A2ATaskID, finalTask(a2a.TaskStateCompleted, "after the outage"), nil)
	d.Release()
	if err := <-restored; err != nil {
		t.Fatal(err)
	}

	if got, _ := svc.repo.GetExecutionByID(t.Context(), run.ID); got == nil || got.Status != "completed" {
		t.Fatalf("run = %+v, want completed once the database is back", got)
	}
	if d.Kept() || !acked {
		t.Fatalf("kept=%v acked=%v, want the answer acknowledged after the write", d.Kept(), acked)
	}
}

func TestSchedulerAnswers_CoreEndsTheRunBeforeAcknowledging(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := sentRun(t, svc, task.ID, now)
	a2aTaskID := *run.A2ATaskID
	payload, err := json.Marshal(a2a.Response{JSONRPC: "2.0", ID: a2aTaskID, Result: finalTask(a2a.TaskStateCompleted, "done")})
	if err != nil {
		t.Fatal(err)
	}

	var statusAtAck string
	ctx, d := delivery.Attach(t.Context(), "msg-1", false, func() {
		got, _ := svc.repo.GetExecutionByID(t.Context(), run.ID)
		statusAtAck = got.Status
	})
	svc.core.handleResponse(ctx, &broker.Message{
		Topic:   a2a.GatewayResponseTopic("test-ns", "test-gw", a2aTaskID),
		Payload: payload,
	})
	d.Release()

	if statusAtAck != "completed" {
		t.Fatalf("status when the answer was acknowledged = %q, want completed", statusAtAck)
	}
}

func TestSchedulerAnswers_ErrorAnswerIsRetried(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) {
		st.MaxRetries = 1
		st.RetryDelaySeconds = 1
	})
	run := sentRun(t, svc, task.ID, now)

	otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, nil, &a2a.Error{Code: -32000, Message: "llm unavailable"})

	runs := taskRuns(t, svc, task.ID)
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want the failed run and its retry", len(runs))
	}
	for _, r := range runs {
		switch r.ID {
		case run.ID:
			if r.Status != "failed" || r.ErrorMessage == nil || *r.ErrorMessage != "llm unavailable" {
				t.Errorf("answered run = {status %q, error %v}", r.Status, r.ErrorMessage)
			}
		default:
			if r.Status != "pending" || r.RetryCount != 1 {
				t.Errorf("retry = {status %q, retry %d}, want pending attempt 1", r.Status, r.RetryCount)
			}
		}
	}
}

func TestSchedulerAnswers_AnswerBeforeTheMark(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	if err := svc.firePass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	run := taskRuns(t, svc, task.ID)[0]
	a2aTaskID := *run.A2ATaskID
	sessionID := svc.runSession(t.Context(), task, run.ID, svc.resolveUserID(task))

	otherGWE(svc).handleAnswer(t.Context(), a2aTaskID, finalTask(a2a.TaskStateCompleted, "fast"), nil)
	marked, err := svc.repo.MarkSubmitted(t.Context(), run.ID, now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}

	got, _ := svc.repo.GetExecutionByID(t.Context(), run.ID)
	if marked || got.Status != "completed" || !submitted(got) {
		t.Fatalf("marked=%v run=%+v, want the answer to stand, the run recorded as submitted, and the late mark to do nothing", marked, got)
	}
	storeEvent(t, svc, a2aTaskID, sessionID, 1, "final_response", a2a.Response{JSONRPC: "2.0", ID: a2aTaskID, Result: finalTask(a2a.TaskStateCompleted, "fast")})
	if err := svc.sseRepo.MarkCompleted(t.Context(), a2aTaskID); err != nil {
		t.Fatal(err)
	}
	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if chat, err := svc.chatRepo.GetByID(t.Context(), a2aTaskID); err != nil || chat == nil || !contains(chat.MessageBubbles, "fast") {
		t.Fatalf("chat = %v, %v; want the answer saved", chat, err)
	}
}

func TestSchedulerAnswers_MarkBetweenReadAndWrite(t *testing.T) {
	svc := passesSvc(t)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	if err := svc.firePass(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	readPending := taskRuns(t, svc, task.ID)[0]
	if _, err := svc.repo.MarkSubmitted(t.Context(), readPending.ID, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	end, mayRetry := answerEnd(finalTask(a2a.TaskStateCompleted, "fast"), nil, now, false)
	if _, err := svc.endRunWith(t.Context(), now, readPending, task, end, mayRetry); err != nil {
		t.Fatal(err)
	}

	if got, _ := svc.repo.GetExecutionByID(t.Context(), readPending.ID); got.Status != "completed" {
		t.Fatalf("status = %q, want the answer written though the run was marked after it was read", got.Status)
	}
	if st := reloadTask(t, svc, task.ID); st.RunCount != 1 {
		t.Fatalf("run_count = %d, want 1", st.RunCount)
	}
}

func TestSchedulerAnswers_UnknownTaskIsIgnored(t *testing.T) {
	svc := passesSvc(t)
	acked := false
	ctx, d := delivery.Attach(t.Context(), "msg-1", false, func() { acked = true })
	svc.handleAnswer(ctx, "00000000-0000-7000-8000-0000000000ff", finalTask(a2a.TaskStateCompleted, "not a scheduled run"), nil)
	d.Release()

	if d.Kept() || !acked {
		t.Fatalf("kept=%v acked=%v, want an answer to a task that is not a scheduled run acknowledged at once", d.Kept(), acked)
	}
}
