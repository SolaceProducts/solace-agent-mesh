// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"strings"
	"testing"
)

func withRunStopShape(spec map[string]any, shape string) map[string]any {
	switch shape {
	case "fail_fast false":
		spec["fail_fast"] = false
	case "on_exit handler":
		spec["on_exit"] = map[string]any{"always": spec["nodes"].([]any)[0].(map[string]any)["id"]}
	case "when false":
		spec["nodes"].([]any)[0].(map[string]any)["when"] = "false"
	}
	return spec
}

func TestPlanWorkflows_AFailingToolNodeThatCannotStopARunIsAdvisedNotRefused(t *testing.T) {
	problems := map[string]func() map[string]any{
		"a tool name no source registers": func() map[string]any { return unknownToolSpec("get_weather") },
		"an argument the built-in does not declare": func() map[string]any {
			return toolNodeWorkflow([]any{map[string]any{"tool_type": "builtin", "tool_name": "web_request"}},
				map[string]any{
					"id": "fetch", "type": "tool", "tool_name": "web_request",
					"input": map[string]any{"url": "https://example.com", "output_filename": "x.jpg"},
				})
		},
	}
	for problem, build := range problems {
		for _, shape := range []string{"on_exit handler", "when false"} {
			t.Run(problem+", "+shape, func(t *testing.T) {
				plan, err := planOne(t, "wf", withRunStopShape(build(), shape))
				if err != nil {
					t.Fatalf("a node that cannot stop a run must not fail the plan: %v", err)
				}
				if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "does not refuse the workflow") {
					t.Fatalf("warnings = %v, want one saying the workflow is not refused for it", plan.Warnings)
				}
			})
		}
		t.Run(problem+", nothing keeps it from stopping a run", func(t *testing.T) {
			if _, err := planOne(t, "wf", build()); err == nil {
				t.Fatal("a node that can stop a run must fail the plan")
			}
		})
		t.Run(problem+", fail_fast false does not keep it from stopping a run", func(t *testing.T) {
			if _, err := planOne(t, "wf", withRunStopShape(build(), "fail_fast false")); err == nil {
				t.Fatal("a node that fail_fast false only skips, with its dependents, must still fail the plan")
			}
		})
	}
}
