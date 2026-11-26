// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"testing"
)

func TestSentinelBuiltinToolsResolve(t *testing.T) {
	opts := Options{}

	analyticsTools := []string{
		"query_task_health", "query_failed_tasks", "query_slow_tasks",
		"query_agent_performance", "query_token_usage",
		"query_scheduled_task_health", "query_feedback_summary",
		"query_tasks",
	}
	datadogTools := []string{"datadog_query_logs", "datadog_get_errors", "correlate_task_with_logs"}
	platformReadTools := []string{"platform_list_components", "platform_get_component_config"}

	all := append(append(append([]string{}, analyticsTools...), datadogTools...), platformReadTools...)
	for _, name := range all {
		if tl := LookupBuiltinTool(name, map[string]any{}, opts); tl == nil {
			t.Errorf("LookupBuiltinTool(%q) = nil, want a tool", name)
		} else if tl.Name() != name {
			t.Errorf("LookupBuiltinTool(%q).Name() = %q", name, tl.Name())
		}
	}

	if got := LookupBuiltinGroup("observability_analytics", opts); !slices.Equal(got, analyticsTools) {
		t.Errorf("observability_analytics group = %v, want %v", got, analyticsTools)
	}
	if got := LookupBuiltinGroup("observability_datadog", opts); !slices.Equal(got, datadogTools) {
		t.Errorf("observability_datadog group = %v, want %v", got, datadogTools)
	}
	if got := LookupBuiltinGroup("observability_platform_read", opts); !slices.Equal(got, platformReadTools) {
		t.Errorf("observability_platform_read group = %v, want %v", got, platformReadTools)
	}
}
