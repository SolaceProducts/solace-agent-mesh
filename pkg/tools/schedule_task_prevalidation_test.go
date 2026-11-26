// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func scheduleArgs(overrides map[string]any) map[string]any {
	args := map[string]any{"target_name": "a", "schedule_type": "cron", "schedule": "0 9 * * *", "message": "do it"}
	for k, v := range overrides {
		args[k] = v
	}
	return args
}

func TestScheduleTaskTool_RejectsWhatTheGatewayWouldRefuseBeforePublishing(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]any
	}{
		{"whitespace-only message", map[string]any{"message": "  \n\t "}},
		{"negative timeout", map[string]any{"timeout_seconds": float64(-1)}},
		{"timeout above seven days", map[string]any{"timeout_seconds": float64(604801)}},
		{"embedded cron timezone", map[string]any{"schedule": "CRON_TZ=Asia/Tokyo 0 9 * * *"}},
		{"cron that never fires", map[string]any{"schedule": "0 0 30 2 *"}},
		{"malformed cron", map[string]any{"schedule": "every morning"}},
		{"interval below the minimum", map[string]any{"schedule_type": "interval", "schedule": "10s"}},
		{"unknown timezone", map[string]any{"timezone": "Mars/Phobos"}},
		{"host-local timezone", map[string]any{"timezone": "Local"}},
		{"non-string timezone", map[string]any{"timezone": float64(5)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			published := 0
			tc := &tool.Context{ScheduleTask: func(context.Context, a2a.ScheduleRequest) error { published++; return nil }}

			res, err := (&ScheduleTaskTool{}).Execute(context.Background(), scheduleArgs(tt.overrides), tc)

			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if res.Status != tool.StatusError {
				t.Fatalf("status = %s, want an error result", res.Status)
			}
			if published != 0 {
				t.Fatalf("published %d schedule requests, want 0", published)
			}
		})
	}
}

func TestScheduleTaskTool_PublishesValidEdgeValues(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]any
	}{
		{"default timeout", nil},
		{"maximum timeout", map[string]any{"timeout_seconds": float64(604800)}},
		{"minimum interval", map[string]any{"schedule_type": "interval", "schedule": "60s"}},
		{"one-time in a timezone", map[string]any{"schedule_type": "one_time", "schedule": "2099-01-01T09:00:00", "timezone": "America/New_York"}},
		{"padded timezone", map[string]any{"timezone": " UTC "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			published := 0
			tc := &tool.Context{ScheduleTask: func(context.Context, a2a.ScheduleRequest) error { published++; return nil }}

			res, err := (&ScheduleTaskTool{}).Execute(context.Background(), scheduleArgs(tt.overrides), tc)

			if err != nil || res.Status == tool.StatusError {
				t.Fatalf("Execute = %+v, %v; want success", res, err)
			}
			if published != 1 {
				t.Fatalf("published %d schedule requests, want 1", published)
			}
		})
	}
}
