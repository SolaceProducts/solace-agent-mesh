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

type storedLimits struct {
	name                                    string
	maxRetries, retryDelaySeconds, timeoutS int
}

func TestUpdateScheduledTask_AnUnchangedStoredLimitOutsideTheRangeIsTolerated(t *testing.T) {
	legacy := storedLimits{name: "Legacy Limits", maxRetries: 20, retryDelaySeconds: 172800, timeoutS: 2592000}
	tests := []struct {
		name string
		body map[string]any
		want int
		then storedLimits
	}{
		{"retries resent unchanged", map[string]any{"name": "Renamed", "maxRetries": 20}, http.StatusOK,
			storedLimits{"Renamed", 20, 172800, 2592000}},
		{"retry delay resent unchanged", map[string]any{"name": "Renamed", "retryDelaySeconds": 172800}, http.StatusOK,
			storedLimits{"Renamed", 20, 172800, 2592000}},
		{"all three limits resent unchanged", map[string]any{"name": "Renamed", "maxRetries": 20, "retryDelaySeconds": 172800, "timeoutSeconds": 2592000}, http.StatusOK,
			storedLimits{"Renamed", 20, 172800, 2592000}},
		{"retries changed to another out-of-range value", map[string]any{"maxRetries": 21}, http.StatusBadRequest, legacy},
		{"retry delay changed to another out-of-range value", map[string]any{"retryDelaySeconds": 172801}, http.StatusBadRequest, legacy},
		{"an unchanged limit beside a changed out-of-range one", map[string]any{"maxRetries": 20, "retryDelaySeconds": 172801}, http.StatusBadRequest, legacy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mux := setupScheduledTaskTest(t)
			userID := "sam_dev_user"
			taskID := idgen.New()
			if err := h.repo.Create(context.Background(), &store.ScheduledTask{
				ID: taskID, Name: legacy.name, Namespace: "test-ns", UserID: &userID, CreatedBy: userID,
				ScheduleType: "cron", ScheduleExpression: "0 9 * * *", Timezone: "UTC",
				TargetAgentName: "agent", TargetType: "agent",
				TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "chat",
				MaxRetries: legacy.maxRetries, RetryDelaySeconds: legacy.retryDelaySeconds, TimeoutSeconds: legacy.timeoutS,
			}); err != nil {
				t.Fatalf("repo.Create legacy-limits task: %v", err)
			}

			rec := patchTask(t, h, mux, taskID, tt.body)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			got, err := h.repo.GetByID(context.Background(), taskID)
			if err != nil || got == nil {
				t.Fatalf("repo.GetByID(%s) = %v, %v", taskID, got, err)
			}
			stored := storedLimits{got.Name, got.MaxRetries, got.RetryDelaySeconds, got.TimeoutSeconds}
			if stored != tt.then {
				t.Fatalf("stored = %+v, want %+v", stored, tt.then)
			}
		})
	}
}
