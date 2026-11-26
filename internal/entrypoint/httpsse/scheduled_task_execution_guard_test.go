// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func createOwnedTask(t *testing.T, h *scheduledTaskHandler, name string) string {
	t.Helper()
	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: name, Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	return taskID
}

func TestTriggerScheduledTask_WhileRunningIsAConflictNotAServerError(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	taskID := createOwnedTask(t, h, "Trigger Twice")

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/trigger", nil))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first trigger = %d, want 202: %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/trigger", nil))
	if second.Code != http.StatusConflict {
		t.Fatalf("second trigger = %d, want 409: %s", second.Code, second.Body.String())
	}
}

func TestDeleteExecution_RefusesActiveRunsAndAllowsFinishedOnes(t *testing.T) {
	finalizedAt := int64(2000)
	tests := []struct {
		name        string
		status      string
		finalizedAt *int64
		wantCode    int
		wantGone    bool
	}{
		{name: "pending", status: "pending", wantCode: http.StatusConflict},
		{name: "running", status: "running", wantCode: http.StatusConflict},
		{name: "running with a finalized stamp", status: "running", finalizedAt: &finalizedAt, wantCode: http.StatusConflict},
		{name: "completed but still owed its finish pass", status: "completed", wantCode: http.StatusConflict},
		{name: "failed but still owed its finish pass", status: "failed", wantCode: http.StatusConflict},
		{name: "completed and finalized", status: "completed", finalizedAt: &finalizedAt, wantCode: http.StatusNoContent, wantGone: true},
		{name: "failed and finalized", status: "failed", finalizedAt: &finalizedAt, wantCode: http.StatusNoContent, wantGone: true},
		{name: "timeout and finalized", status: "timeout", finalizedAt: &finalizedAt, wantCode: http.StatusNoContent, wantGone: true},
		{name: "skipped and finalized", status: "skipped", finalizedAt: &finalizedAt, wantCode: http.StatusNoContent, wantGone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := setupScheduledTaskTest(t)
			taskID := createOwnedTask(t, h, "Has Runs")
			execID := idgen.New()
			if err := h.repo.CreateExecution(context.Background(), &store.ScheduledTaskExecution{
				ID: execID, ScheduledTaskID: taskID, Status: tt.status, ScheduledFor: 1000,
				TriggerType: store.TriggerTypeManual, FinalizedAt: tt.finalizedAt,
			}); err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest("DELETE", "/api/v1/scheduledTasks/executions/"+execID, nil)
			req.SetPathValue("id", execID)
			h.handleDeleteExecution(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}
			got, err := h.repo.GetExecutionByID(context.Background(), execID)
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != tt.wantGone {
				t.Fatalf("execution present = %v, want present = %v", got != nil, !tt.wantGone)
			}
		})
	}
}
