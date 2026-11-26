// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPreviewSchedule_RefusesWhatCreateRefuses(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"host-local timezone", map[string]any{"scheduleType": "cron", "scheduleExpression": "0 9 * * *", "timezone": "Local"}, http.StatusBadRequest},
		{"cron that never fires", map[string]any{"scheduleType": "cron", "scheduleExpression": "0 0 30 2 *", "timezone": "UTC"}, http.StatusBadRequest},
		{"valid cron in a named zone", map[string]any{"scheduleType": "cron", "scheduleExpression": "0 9 * * *", "timezone": "Europe/Berlin"}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mux := setupScheduledTaskTest(t)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks/preview", jsonBody(t, tt.body)))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
