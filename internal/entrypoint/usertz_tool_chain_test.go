// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestUserTimezoneBlobIsReadByGetCurrentTimeTool(t *testing.T) {
	for _, tz := range []string{"America/Toronto", "Asia/Tokyo", "Europe/Berlin"} {
		t.Run(tz, func(t *testing.T) {
			props := &a2a.UserProperties{}
			newTZCore("UTC").applyUserTimezoneToUserProps(props, tz)

			result, err := (&tools.GetCurrentDatetimeTool{}).Execute(
				t.Context(), nil, &tool.Context{A2AUserConfig: props.A2AUserConfig})
			if err != nil {
				t.Fatalf("get_current_time: %v", err)
			}
			if got := result.Data["timezone"]; got != tz {
				t.Errorf("tool reported timezone %v, want %v", got, tz)
			}
			if day, _ := result.Data["day_of_week"].(string); day == "" {
				t.Error("tool reported no day_of_week")
			}
		})
	}
}

func TestGetCurrentTimeToolDefaultsToUTCWithoutGatewayZone(t *testing.T) {
	props := &a2a.UserProperties{}
	newTZCore("").applyUserTimezoneToUserProps(props, "")

	result, err := (&tools.GetCurrentDatetimeTool{}).Execute(
		t.Context(), nil, &tool.Context{A2AUserConfig: props.A2AUserConfig})
	if err != nil {
		t.Fatalf("get_current_time: %v", err)
	}
	if got := result.Data["timezone"]; got != tool.DefaultUserTimezone {
		t.Errorf("tool reported timezone %v, want %v", got, tool.DefaultUserTimezone)
	}
}
