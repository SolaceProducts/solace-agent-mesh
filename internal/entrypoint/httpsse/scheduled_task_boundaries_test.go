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

func patchTask(t *testing.T, h *scheduledTaskHandler, mux *http.ServeMux, taskID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, jsonBody(t, body)))
	return rec
}

func TestUpdateScheduledTask_NullIsRejectedNotTreatedAsAZeroValue(t *testing.T) {
	for _, field := range []string{"enabled", "maxRetries", "retryDelaySeconds", "timeoutSeconds"} {
		t.Run(field, func(t *testing.T) {
			h, mux := setupScheduledTaskTest(t)
			taskID := createOwnedTask(t, h, "Null "+field)

			rec := patchTask(t, h, mux, taskID, map[string]any{field: nil})

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			got, err := h.repo.GetByID(context.Background(), taskID)
			if err != nil || got == nil {
				t.Fatalf("GetByID: %v, %v", got, err)
			}
			if !got.Enabled || got.MaxRetries != 0 || got.TimeoutSeconds != 30 {
				t.Fatalf("a rejected null changed the task: %+v", got)
			}
		})
	}
}

func TestLimits_AcceptTheLastValidValueAndRejectTheFirstInvalidOne(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value int
		want  int
	}{
		{"maxRetries lower edge", "maxRetries", 0, http.StatusOK},
		{"maxRetries upper edge", "maxRetries", 10, http.StatusOK},
		{"maxRetries below", "maxRetries", -1, http.StatusBadRequest},
		{"maxRetries above", "maxRetries", 11, http.StatusBadRequest},
		{"retryDelaySeconds lower edge", "retryDelaySeconds", 0, http.StatusOK},
		{"retryDelaySeconds upper edge", "retryDelaySeconds", 86400, http.StatusOK},
		{"retryDelaySeconds below", "retryDelaySeconds", -1, http.StatusBadRequest},
		{"retryDelaySeconds above", "retryDelaySeconds", 86401, http.StatusBadRequest},
		{"timeoutSeconds lower edge", "timeoutSeconds", 1, http.StatusOK},
		{"timeoutSeconds upper edge", "timeoutSeconds", 604800, http.StatusOK},
		{"timeoutSeconds below", "timeoutSeconds", 0, http.StatusBadRequest},
		{"timeoutSeconds above", "timeoutSeconds", 604801, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run("PATCH "+tt.name, func(t *testing.T) {
			h, mux := setupScheduledTaskTest(t)
			taskID := createOwnedTask(t, h, "Limits")
			if rec := patchTask(t, h, mux, taskID, map[string]any{tt.field: tt.value}); rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
		t.Run("POST "+tt.name, func(t *testing.T) {
			_, mux := setupScheduledTaskTest(t)
			rec := httptest.NewRecorder()
			body := validCreateBody(map[string]any{tt.field: tt.value})
			mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body)))
			want := http.StatusCreated
			if tt.want != http.StatusOK {
				want = http.StatusBadRequest
			}
			if rec.Code != want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
			}
		})
	}
}

func TestUpdateScheduledTask_UIEditOfATaskWithAnOversizedStoredTimeoutStillSaves(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	userID := "sam_dev_user"
	taskID := idgen.New()
	const thirtyDays = 30 * 24 * 60 * 60
	if err := h.repo.Create(context.Background(), &store.ScheduledTask{
		ID: taskID, Name: "Agent Made", Namespace: "test-ns", UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *", Timezone: "UTC",
		TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "chat",
		TimeoutSeconds: thirtyDays,
	}); err != nil {
		t.Fatal(err)
	}

	uiPayload := map[string]any{
		"name": "Renamed", "description": "d", "scheduleType": "cron", "scheduleExpression": "0 10 * * *",
		"timezone": "UTC", "targetAgentName": "agent", "targetType": "agent",
		"taskMessage": []map[string]string{{"type": "text", "text": "hello"}},
		"enabled":     true, "timeoutSeconds": thirtyDays,
	}
	rec := patchTask(t, h, mux, taskID, uiPayload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if rec := patchTask(t, h, mux, taskID, map[string]any{"timeoutSeconds": thirtyDays + 1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("changing the timeout to another out-of-range value = %d, want 400", rec.Code)
	}
	if rec := patchTask(t, h, mux, taskID, map[string]any{"timeoutSeconds": thirtyDays}); rec.Code != http.StatusOK {
		t.Fatalf("PATCH carrying only the unchanged stored timeout = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateScheduledTask_NullTimeoutIsRejectedEvenWhenTheStoredTimeoutIsZero(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	userID := "sam_dev_user"
	taskID := idgen.New()
	if err := h.repo.Create(context.Background(), &store.ScheduledTask{
		ID: taskID, Name: "Zero Timeout", Namespace: "test-ns", UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *", Timezone: "UTC",
		TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}); err != nil {
		t.Fatal(err)
	}

	if rec := patchTask(t, h, mux, taskID, map[string]any{"timeoutSeconds": nil}); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestMessageAndMetadataShapes(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]any
		want      int
	}{
		{"data part with null data", map[string]any{"taskMessage": []map[string]any{{"kind": "data", "data": nil}}}, http.StatusBadRequest},
		{"image part", map[string]any{"taskMessage": []map[string]any{{"kind": "image", "text": "x"}}}, http.StatusBadRequest},
		{"file part", map[string]any{"taskMessage": []map[string]any{{"kind": "file", "text": "x"}}}, http.StatusBadRequest},
		{"text part by type", map[string]any{"taskMessage": []map[string]any{{"type": "text", "text": "x"}}}, http.StatusCreated},
		{"text part without kind or type", map[string]any{"taskMessage": []map[string]any{{"text": "run report"}}}, http.StatusCreated},
		{"blank text part without kind or type", map[string]any{"taskMessage": []map[string]any{{"text": "   "}}}, http.StatusBadRequest},
		{"metadata array", map[string]any{"taskMetadata": []int{1}}, http.StatusBadRequest},
		{"metadata string", map[string]any{"taskMetadata": "x"}, http.StatusBadRequest},
		{"metadata object", map[string]any{"taskMetadata": map[string]any{"k": "v"}}, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run("POST "+tt.name, func(t *testing.T) {
			_, mux := setupScheduledTaskTest(t)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, validCreateBody(tt.overrides))))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestUpdateScheduledTask_FieldTypeAndTimezoneCases(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"description as number", map[string]any{"description": 5}, http.StatusBadRequest},
		{"description cleared with null", map[string]any{"description": nil}, http.StatusOK},
		{"valid timezone", map[string]any{"timezone": "America/New_York"}, http.StatusOK},
		{"invalid timezone", map[string]any{"timezone": "Not/AZone"}, http.StatusBadRequest},
		{"scheduleType in-range", map[string]any{"scheduleType": "interval", "scheduleExpression": "5m"}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mux := setupScheduledTaskTest(t)
			taskID := createOwnedTask(t, h, "Types")
			if rec := patchTask(t, h, mux, taskID, tt.body); rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
