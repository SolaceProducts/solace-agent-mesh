// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func createStoredScheduleTask(t *testing.T, h *scheduledTaskHandler, scheduleType, expression string) string {
	t.Helper()
	userID := "sam_dev_user"
	taskID := idgen.New()
	if err := h.repo.Create(context.Background(), &store.ScheduledTask{
		ID: taskID, Name: "Stored", Namespace: "test-ns", UserID: &userID, CreatedBy: userID,
		ScheduleType: scheduleType, ScheduleExpression: expression, Timezone: "UTC",
		TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "chat",
		TimeoutSeconds: 3600,
	}); err != nil {
		t.Fatalf("repo.Create stored %s task %q: %v", scheduleType, expression, err)
	}
	return taskID
}

func uiEditPayload(scheduleType, expression, timezone string) map[string]any {
	return map[string]any{
		"name": "Renamed", "description": "d", "scheduleType": scheduleType, "scheduleExpression": expression,
		"timezone": timezone, "targetAgentName": "agent", "targetType": "agent",
		"taskMessage": []map[string]string{{"type": "text", "text": "hello"}},
		"enabled":     true, "timeoutSeconds": 3600,
	}
}

func TestUpdateScheduledTask_StoredScheduleIsJudgedByTheStoredRulesUntilItChanges(t *testing.T) {
	const embedded = "CRON_TZ=Europe/Berlin 0 9 * * *"
	const neverFires = "0 0 30 2 *"
	tests := []struct {
		name          string
		storedType    string
		stored        string
		body          map[string]any
		want          int
		wantPersisted string
	}{
		{"UI edit resending a stored embedded-timezone expression", "cron", embedded, uiEditPayload("cron", embedded, "UTC"), http.StatusOK, embedded},
		{"timezone-only change on a stored embedded-timezone row", "cron", embedded, map[string]any{"timezone": "America/New_York"}, http.StatusOK, embedded},
		{"new embedded-timezone expression", "cron", embedded, map[string]any{"scheduleExpression": "CRON_TZ=Asia/Tokyo 0 8 * * *"}, http.StatusBadRequest, embedded},
		{"UI edit resending a stored never-firing expression", "cron", neverFires, uiEditPayload("cron", neverFires, "UTC"), http.StatusOK, neverFires},
		{"new never-firing expression", "cron", "0 9 * * *", map[string]any{"scheduleExpression": neverFires}, http.StatusBadRequest, "0 9 * * *"},
		{"stored expression resent with a new schedule type", "cron", embedded, map[string]any{"scheduleType": "interval"}, http.StatusBadRequest, embedded},
		{"stored expression kept while the type changes to cron", "interval", embedded, map[string]any{"scheduleType": "cron"}, http.StatusBadRequest, embedded},
		{"UI edit resending a stored interval", "interval", "1h", uiEditPayload("interval", "1h", "UTC"), http.StatusOK, "1h"},
		{"UI edit resending a whitespace-padded stored expression", "cron", "  0 0 30 2 *  ", uiEditPayload("cron", "0 0 30 2 *", "UTC"), http.StatusOK, neverFires},
		{"timezone-only change on a whitespace-padded stored expression", "cron", "  0 0 30 2 *  ", map[string]any{"timezone": "America/New_York"}, http.StatusOK, neverFires},
		{"timezone-only change on a whitespace-padded stored interval", "interval", "  1h  ", map[string]any{"timezone": "America/New_York"}, http.StatusOK, "1h"},
		{"UI edit resending a stored one-time schedule", "one_time", "2099-01-01T09:00:00Z", uiEditPayload("one_time", "2099-01-01T09:00:00Z", "UTC"), http.StatusOK, "2099-01-01T09:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mux := setupScheduledTaskTest(t)
			taskID := createStoredScheduleTask(t, h, tt.storedType, tt.stored)

			rec := patchTask(t, h, mux, taskID, tt.body)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			got, err := h.repo.GetByID(context.Background(), taskID)
			if err != nil || got == nil {
				t.Fatalf("repo.GetByID(%s) = %v, %v", taskID, got, err)
			}
			if got.ScheduleExpression != tt.wantPersisted {
				t.Fatalf("stored expression = %q, want %q", got.ScheduleExpression, tt.wantPersisted)
			}
			if tt.want == http.StatusOK && tt.body["name"] != nil && got.Name != "Renamed" {
				t.Fatalf("stored name = %q, want the renamed value", got.Name)
			}
			wantTimezone := "UTC"
			if tz, ok := tt.body["timezone"].(string); ok && tt.want == http.StatusOK {
				wantTimezone = tz
			}
			if got.Timezone != wantTimezone || got.ScheduleType != tt.storedType {
				t.Fatalf("stored timezone, type = %q, %q, want %q, %q", got.Timezone, got.ScheduleType, wantTimezone, tt.storedType)
			}
		})
	}
}
