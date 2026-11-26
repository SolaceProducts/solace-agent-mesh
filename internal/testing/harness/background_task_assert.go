// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func (h *TestHarness) AssertBackgroundTaskStatus(t *testing.T) {
	t.Helper()

	expected := h.scenario.ExpectedBackgroundTaskStatus
	if expected == nil {
		return
	}

	httpGW, ok := h.gateway.(*HTTPGateway)
	if !ok {
		t.Error("expected_background_task_status requires go-http gateway mode")
		return
	}

	taskID := h.lastTaskID()
	if taskID == "" {
		t.Error("expected_background_task_status: no task ID available (did the test send a request?)")
		return
	}

	timeout := 30 * time.Second
	if expected.PollTimeoutSeconds > 0 {
		timeout = time.Duration(expected.PollTimeoutSeconds) * time.Second
	}

	userID := h.effectiveUserID()
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()

	status, err := httpGW.PollTaskStatus(ctx, taskID, userID, timeout)
	if err != nil {
		t.Errorf("expected_background_task_status: poll failed: %v", err)
		return
	}

	if expected.IsBackground != nil && status.IsBackground != *expected.IsBackground {
		t.Errorf("expected_background_task_status: is_background = %v, want %v",
			status.IsBackground, *expected.IsBackground)
	}
	if expected.IsRunning != nil && status.IsRunning != *expected.IsRunning {
		t.Errorf("expected_background_task_status: is_running = %v, want %v",
			status.IsRunning, *expected.IsRunning)
	}
	if expected.Status != "" && status.Task.Status != expected.Status {
		t.Errorf("expected_background_task_status: status = %q, want %q",
			status.Task.Status, expected.Status)
	}
}

func (h *TestHarness) AssertBackgroundTaskTimeout(t *testing.T) {
	t.Helper()

	expected := h.scenario.ExpectedBackgroundTaskTimeout
	if expected == nil {
		return
	}

	httpGW, ok := h.gateway.(*HTTPGateway)
	if !ok {
		t.Error("expected_background_task_timeout requires go-http gateway mode")
		return
	}

	aged := h.lastTaskID()
	if aged == "" {
		t.Error("expected_background_task_timeout: no task ID available (did the test send a request?)")
		return
	}

	expectedStatus := expected.ExpectedStatus
	if expectedStatus == "" {
		expectedStatus = "timeout"
	}
	timeout := 15 * time.Second
	if expected.PollTimeoutSeconds > 0 {
		timeout = time.Duration(expected.PollTimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()

	if fresh := expected.FreshTask; fresh != nil {
		if fresh.ID == "" {
			t.Error("expected_background_task_timeout.fresh_task.id is required")
			return
		}
		userID := fresh.UserID
		if userID == "" {
			userID = h.effectiveUserID()
		}
		maxMs := fresh.MaxExecutionTimeMs
		if maxMs <= 0 {
			maxMs = 3_600_000
		}
		ageBy := max(fresh.AgeByMs, 0)
		activity := time.Now().UnixMilli() - ageBy
		if err := httpGW.InsertBackgroundTaskRow(ctx, &store.Task{
			ID:                         fresh.ID,
			UserID:                     userID,
			StartTime:                  activity,
			ExecutionMode:              "background",
			BackgroundExecutionEnabled: true,
			MaxExecutionTimeMs:         &maxMs,
			LastActivityTime:           &activity,
		}); err != nil {
			t.Errorf("expected_background_task_timeout: insert fresh task: %v", err)
			return
		}
	}

	var maxExecArg *int64
	if !expected.UseGatewayDefaultTimeout {
		maxExecArg = &expected.MaxExecutionTimeMs
	}
	if err := httpGW.AgeBackgroundTaskToStale(ctx, aged, maxExecArg, expected.AgeTaskByMs); err != nil {
		t.Errorf("expected_background_task_timeout: age task: %v", err)
		return
	}

	if row, err := httpGW.GetTaskRow(ctx, aged); err != nil {
		t.Errorf("expected_background_task_timeout: verify aged precondition: %v", err)
		return
	} else if row == nil {
		t.Errorf("expected_background_task_timeout: aged task %q missing", aged)
		return
	} else if row.Status != nil || row.EndTime != nil {
		t.Errorf("expected_background_task_timeout: aged task %q is not in running state (status=%v end_time=%v) — monitor result would be ambiguous",
			aged, row.Status, row.EndTime)
		return
	}

	if expected.ExpectNoChange {
		select {
		case <-ctx.Done():
			t.Errorf("expected_background_task_timeout: context cancelled during no-change wait: %v", ctx.Err())
			return
		case <-time.After(timeout):
		}
		task, err := httpGW.GetTaskRow(ctx, aged)
		if err != nil {
			t.Errorf("expected_background_task_timeout: query aged task (no-change): %v", err)
			return
		}
		if task == nil {
			t.Errorf("expected_background_task_timeout: aged task %q missing (no-change)", aged)
			return
		}
		if task.Status != nil {
			t.Errorf("expected_background_task_timeout: expect_no_change=true but task %q status=%q (monitor flipped it)",
				aged, *task.Status)
		}
		if task.EndTime != nil {
			t.Errorf("expected_background_task_timeout: expect_no_change=true but task %q has end_time set (monitor touched it)",
				aged)
		}
	} else {
		deadline := time.Now().Add(timeout)
		var lastStatus string
		for time.Now().Before(deadline) {
			task, err := httpGW.GetTaskRow(ctx, aged)
			if err != nil {
				t.Errorf("expected_background_task_timeout: query task: %v", err)
				return
			}
			if task != nil && task.Status != nil {
				lastStatus = *task.Status
				if lastStatus == expectedStatus {
					break
				}
			}
			select {
			case <-ctx.Done():
				t.Errorf("expected_background_task_timeout: context cancelled: %v", ctx.Err())
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		if lastStatus != expectedStatus {
			t.Errorf("expected_background_task_timeout: task %q status = %q, want %q (timed out after %s)",
				aged, lastStatus, expectedStatus, timeout)
			return
		}

		status, err := httpGW.GetTaskStatus(ctx, aged, h.effectiveUserID())
		if err != nil {
			t.Errorf("expected_background_task_timeout: GET status: %v", err)
			return
		}
		if status.IsRunning {
			t.Errorf("expected_background_task_timeout: status endpoint reports is_running=true, want false")
		}
		if status.Task.Status != expectedStatus {
			t.Errorf("expected_background_task_timeout: status endpoint reports status=%q, want %q",
				status.Task.Status, expectedStatus)
		}
	}

	if fresh := expected.FreshTask; fresh != nil {
		task, err := httpGW.GetTaskRow(ctx, fresh.ID)
		if err != nil {
			t.Errorf("expected_background_task_timeout: query fresh task: %v", err)
			return
		}
		if task == nil {
			t.Errorf("expected_background_task_timeout: fresh task %q missing", fresh.ID)
			return
		}
		if task.Status != nil && *task.Status == "timeout" {
			t.Errorf("expected_background_task_timeout: fresh task %q was incorrectly timed out", fresh.ID)
		}
		if task.EndTime != nil {
			t.Errorf("expected_background_task_timeout: fresh task %q has end_time set (monitor touched it)", fresh.ID)
		}
	}
}

func (h *TestHarness) lastTaskID() string {
	switch gw := h.gateway.(type) {
	case *HTTPGateway:
		return gw.lastTaskID()
	default:
		return ""
	}
}

func (g *HTTPGateway) lastTaskID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastSentID
}
