// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"testing"
)

func TestActivityBuiltinToolsResolve(t *testing.T) {
	opts := Options{}

	activityTools := []string{"report_activity", "list_failed_tasks", "explain_task", "summarize_feedback", "list_feedback", "explain_feedback", "transform_data_with_jmespath"}
	for _, name := range activityTools {
		if tl := LookupBuiltinTool(name, map[string]any{}, opts); tl == nil {
			t.Errorf("LookupBuiltinTool(%q) = nil, want a tool", name)
		} else if tl.Name() != name {
			t.Errorf("LookupBuiltinTool(%q).Name() = %q", name, tl.Name())
		}
	}

	if got := LookupBuiltinGroup("activity_reporting", opts); !slices.Equal(got, activityTools) {
		t.Errorf("activity_reporting group = %v, want %v", got, activityTools)
	}
}
