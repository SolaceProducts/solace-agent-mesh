// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func (h *TestHarness) SetupScheduledTask(ctx context.Context, st *SetupScheduledTask) error {
	httpGW, ok := h.gateway.(*HTTPGateway)
	if !ok {
		return fmt.Errorf("setup_scheduled_task requires go-http gateway mode")
	}

	userID := h.effectiveUserID()

	if st.TargetType == "" {
		st.TargetType = "agent"
	}

	taskID, err := httpGW.CreateScheduledTask(ctx, st, userID)
	if err != nil {
		return fmt.Errorf("create scheduled task: %w", err)
	}
	h.scheduledTaskID = taskID
	h.logger.Info("created scheduled task", "taskID", taskID, "name", st.Name)

	if st.Trigger {
		execID, err := httpGW.TriggerScheduledTask(ctx, taskID, userID)
		if err != nil {
			return fmt.Errorf("trigger scheduled task: %w", err)
		}
		h.scheduledExecID = execID
		h.logger.Info("triggered scheduled task", "taskID", taskID, "executionID", execID)
	}

	return nil
}

func (h *TestHarness) AssertScheduledTaskOutput(t *testing.T) {
	t.Helper()

	expected := h.scenario.ExpectedScheduledTaskOutput
	if expected == nil {
		return
	}

	if h.scheduledTaskID == "" || h.scheduledExecID == "" {
		t.Error("expected_scheduled_task_output: no scheduled task/execution was set up (missing setup_scheduled_task with trigger: true?)")
		return
	}

	httpGW, ok := h.gateway.(*HTTPGateway)
	if !ok {
		t.Error("expected_scheduled_task_output requires go-http gateway mode")
		return
	}

	timeout := 30 * time.Second
	if expected.PollTimeoutSeconds > 0 {
		timeout = time.Duration(expected.PollTimeoutSeconds) * time.Second
	}

	userID := h.effectiveUserID()
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()

	exec, err := httpGW.PollExecution(ctx, h.scheduledTaskID, h.scheduledExecID, userID, timeout)
	if err != nil {
		t.Errorf("expected_scheduled_task_output: poll failed: %v", err)
		return
	}

	if expected.ExecutionStatus != "" && exec.Status != expected.ExecutionStatus {
		t.Errorf("expected_scheduled_task_output: status = %q, want %q", exec.Status, expected.ExecutionStatus)
	}

	if expected.ResultSummaryContains != "" {
		summaryText := extractResultSummaryText(exec.ResultSummary)
		if !strings.Contains(summaryText, expected.ResultSummaryContains) {
			t.Errorf("expected_scheduled_task_output: result_summary text = %q, want it to contain %q", summaryText, expected.ResultSummaryContains)
		}
	}

	if expected.ErrorMessage != nil {
		wantErr := *expected.ErrorMessage
		if wantErr == "" {
			if exec.ErrorMessage != nil && *exec.ErrorMessage != "" {
				t.Errorf("expected_scheduled_task_output: expected no error_message, got %q", *exec.ErrorMessage)
			}
		} else {
			if exec.ErrorMessage == nil {
				t.Errorf("expected_scheduled_task_output: expected error_message containing %q, got nil", wantErr)
			} else if !strings.Contains(*exec.ErrorMessage, wantErr) {
				t.Errorf("expected_scheduled_task_output: error_message = %q, want it to contain %q", *exec.ErrorMessage, wantErr)
			}
		}
	}

	if expected.ConsecutiveFailureCount != nil {
		if err := httpGW.PollConsecutiveFailureCount(context.Background(), h.scheduledTaskID, userID, *expected.ConsecutiveFailureCount, 10*time.Second); err != nil {
			t.Errorf("expected_scheduled_task_output: %v", err)
		}
	}
}

func extractResultSummaryText(summary any) string {
	if summary == nil {
		return ""
	}

	if m, ok := summary.(map[string]any); ok {
		if text, ok := m["text"].(string); ok {
			return text
		}
		data, _ := json.Marshal(m)
		return string(data)
	}

	if s, ok := summary.(string); ok {
		var m map[string]any
		if json.Unmarshal([]byte(s), &m) == nil {
			if text, ok := m["text"].(string); ok {
				return text
			}
		}
		return s
	}

	data, _ := json.Marshal(summary)
	return string(data)
}
