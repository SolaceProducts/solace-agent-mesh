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

func validCreateBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"name":               "Hardening Task",
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "test-agent",
		"taskMessage":        []map[string]string{{"type": "text", "text": "hello"}},
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

func TestCreateScheduledTask_RejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]any
	}{
		{name: "source config", overrides: map[string]any{"source": "config"}},
		{name: "source arbitrary", overrides: map[string]any{"source": "anything-goes"}},
		{name: "empty message list", overrides: map[string]any{"taskMessage": []any{}}},
		{name: "empty message string", overrides: map[string]any{"taskMessage": ""}},
		{name: "blank text part", overrides: map[string]any{"taskMessage": []map[string]string{{"type": "text", "text": "   "}}}},
		{name: "object message", overrides: map[string]any{"taskMessage": map[string]any{}}},
		{name: "numeric message", overrides: map[string]any{"taskMessage": 7}},
		{name: "negative timeout", overrides: map[string]any{"timeoutSeconds": -5}},
		{name: "zero timeout", overrides: map[string]any{"timeoutSeconds": 0}},
		{name: "oversized timeout", overrides: map[string]any{"timeoutSeconds": 99999999999}},
		{name: "too many retries", overrides: map[string]any{"maxRetries": 1000000000}},
		{name: "negative retries", overrides: map[string]any{"maxRetries": -1}},
		{name: "oversized retry delay", overrides: map[string]any{"retryDelaySeconds": 99999999}},
		{name: "cron that never fires", overrides: map[string]any{"scheduleExpression": "0 0 30 2 *"}},
		{name: "embedded cron timezone", overrides: map[string]any{"scheduleExpression": "CRON_TZ=UTC 0 9 * * *"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mux := setupScheduledTaskTest(t)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, validCreateBody(tt.overrides))))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCreateScheduledTask_AcceptsChatSourceAndDataPart(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)
	body := validCreateBody(map[string]any{
		"source":      "chat",
		"taskMessage": []map[string]any{{"kind": "data", "data": map[string]any{"k": "v"}}},
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateScheduledTask_RejectsInvalidInputAndLeavesTaskUnchanged(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{name: "empty body", body: map[string]any{}},
		{name: "unknown field", body: map[string]any{"bogusField": 1}},
		{name: "read-only field", body: map[string]any{"userId": "bob"}},
		{name: "source is not updatable", body: map[string]any{"source": "config"}},
		{name: "enabled as string", body: map[string]any{"enabled": "false"}},
		{name: "name as number", body: map[string]any{"name": 5}},
		{name: "whitespace name", body: map[string]any{"name": "   "}},
		{name: "null message", body: map[string]any{"taskMessage": nil}},
		{name: "empty message list", body: map[string]any{"taskMessage": []any{}}},
		{name: "metadata as string", body: map[string]any{"taskMetadata": "x"}},
		{name: "negative timeout", body: map[string]any{"timeoutSeconds": -1}},
		{name: "oversized timeout", body: map[string]any{"timeoutSeconds": 99999999999}},
		{name: "too many retries", body: map[string]any{"maxRetries": 11}},
		{name: "expression as number", body: map[string]any{"scheduleExpression": 5}},
		{name: "never-firing cron", body: map[string]any{"scheduleExpression": "0 0 30 2 *"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mux := setupScheduledTaskTest(t)
			userID := "sam_dev_user"
			taskID := idgen.New()
			original := &store.ScheduledTask{
				ID: taskID, Name: "Original", Namespace: "test-ns",
				UserID: &userID, CreatedBy: userID,
				ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
				Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
				TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
				TimeoutSeconds: 3600,
			}
			if err := h.repo.Create(context.Background(), original); err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, jsonBody(t, tt.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}

			got, err := h.repo.GetByID(context.Background(), taskID)
			if err != nil || got == nil {
				t.Fatalf("GetByID: row=%v err=%v", got, err)
			}
			if got.Name != "Original" || got.TaskMessage != original.TaskMessage ||
				got.TimeoutSeconds != 3600 || got.Source != "ui" || !got.Enabled {
				t.Fatalf("rejected PATCH changed the stored task: %+v", got)
			}
		})
	}
}

func TestUpdateScheduledTask_ClearsMetadataWithNull(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	userID := "sam_dev_user"
	taskID := idgen.New()
	meta := `{"k":"v"}`
	task := &store.ScheduledTask{
		ID: taskID, Name: "Original", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, TaskMetadata: &meta, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, jsonBody(t, map[string]any{"taskMetadata": nil})))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got, err := h.repo.GetByID(context.Background(), taskID)
	if err != nil || got == nil {
		t.Fatalf("GetByID: row=%v err=%v", got, err)
	}
	if got.TaskMetadata != nil {
		t.Fatalf("TaskMetadata = %q, want nil", *got.TaskMetadata)
	}
}
