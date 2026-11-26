// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestScheduleTaskTool_PublishesTrimmedTimezoneAndName(t *testing.T) {
	tests := []struct {
		name              string
		overrides         map[string]any
		wantTimezone      string
		wantName          string
		wantNameGenerated bool
	}{
		{"padded timezone", map[string]any{"timezone": " UTC "}, "UTC", "", true},
		{"padded name", map[string]any{"name": "  Nightly Report  "}, "", "Nightly Report", false},
		{"whitespace-only name is treated as omitted", map[string]any{"name": "   "}, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent a2a.ScheduleRequest
			tc := &tool.Context{ScheduleTask: func(_ context.Context, req a2a.ScheduleRequest) error { sent = req; return nil }}

			res, err := (&ScheduleTaskTool{}).Execute(context.Background(), scheduleArgs(tt.overrides), tc)

			if err != nil || res.Status == tool.StatusError {
				t.Fatalf("Execute = %+v, %v; want success", res, err)
			}
			if sent.Timezone != tt.wantTimezone {
				t.Errorf("published timezone = %q, want %q", sent.Timezone, tt.wantTimezone)
			}
			if tt.wantNameGenerated {
				if !strings.HasPrefix(sent.Name, "scheduled-") {
					t.Errorf("published name = %q, want a generated scheduled-<id> name", sent.Name)
				}
			} else if sent.Name != tt.wantName {
				t.Errorf("published name = %q, want %q", sent.Name, tt.wantName)
			}
		})
	}
}
