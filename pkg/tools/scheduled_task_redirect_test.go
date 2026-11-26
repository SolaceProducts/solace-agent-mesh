// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func webCtx() *tool.Context {
	return &tool.Context{
		GatewayCapabilities: map[string]bool{"web_ui_redirect": true},
	}
}

func TestScheduledTaskRedirectTool_StaticMetadata(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	if tl.Name() != "scheduled_task_redirect" {
		t.Errorf("Name() = %q, want scheduled_task_redirect", tl.Name())
	}
	if !strings.Contains(strings.ToLower(tl.Description()), "scheduled tasks") {
		t.Errorf("Description should mention scheduled tasks; got %q", tl.Description())
	}
}

func TestScheduledTaskRedirectTool_Parameters(t *testing.T) {
	t.Parallel()
	schema := NewScheduledTaskRedirectTool().Parameters()
	if schema == nil {
		t.Fatal("Parameters() returned nil")
	}
	for _, want := range []string{"context", "mode", "task_id", "artifacts"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Errorf("schema missing %q property", want)
		}
	}
	if len(schema.Required) != 2 {
		t.Fatalf("Required = %v, want [context mode]", schema.Required)
	}
	gotRequired := map[string]bool{}
	for _, r := range schema.Required {
		gotRequired[r] = true
	}
	if !gotRequired["context"] || !gotRequired["mode"] {
		t.Errorf("Required = %v, want context and mode", schema.Required)
	}
}

func TestScheduledTaskRedirectTool_Execute_RejectsMissingContext(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	res, err := tl.Execute(t.Context(), map[string]any{"mode": "create"}, webCtx())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "error" {
		t.Errorf("Status = %q, want error", res.Status)
	}
}

func TestScheduledTaskRedirectTool_Execute_RejectsBadMode(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	for _, m := range []string{"", "nope", "Create"} {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx", "mode": m}, webCtx())
		if err != nil {
			t.Fatalf("Execute(mode=%q): %v", m, err)
		}
		if res.Status != "error" {
			t.Errorf("mode=%q: Status = %q, want error", m, res.Status)
		}
	}
}

func TestScheduledTaskRedirectTool_Execute_RequiresTaskIDForEditDelete(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	for _, m := range []string{"edit", "delete"} {
		res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx", "mode": m}, webCtx())
		if err != nil {
			t.Fatalf("Execute(mode=%q): %v", m, err)
		}
		if res.Status != "error" {
			t.Errorf("mode=%q with no task_id: Status = %q, want error", m, res.Status)
		}
	}
}

func TestScheduledTaskRedirectTool_Execute_CreateMode(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	res, err := tl.Execute(t.Context(), map[string]any{
		"context": "Send a market summary every Monday at 9am",
		"mode":    "create",
	}, webCtx())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status == "error" {
		t.Fatalf("unexpected error result: %+v", res)
	}
	data := res.Data
	if data["type"] != "redirect" {
		t.Errorf("type = %v, want redirect", data["type"])
	}
	if data["route"] != "/scheduled-tasks" {
		t.Errorf("route = %v, want /scheduled-tasks", data["route"])
	}
	if data["label"] != "Create in Scheduled Tasks" {
		t.Errorf("label = %v, want 'Create in Scheduled Tasks'", data["label"])
	}
	urlParams, _ := data["url_params"].(map[string]any)
	if urlParams["mode"] != "create" {
		t.Errorf("url_params.mode = %v, want create", urlParams["mode"])
	}
	if _, ok := urlParams["taskId"]; ok {
		t.Error("url_params.taskId should be absent for mode=create")
	}
}

func TestScheduledTaskRedirectTool_Execute_EditMode(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	res, err := tl.Execute(t.Context(), map[string]any{
		"context": "Change the weekly report to run on Tuesdays",
		"mode":    "edit",
		"task_id": "task-abc",
	}, webCtx())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status == "error" {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if res.Data["label"] != "Review changes in Scheduled Tasks" {
		t.Errorf("label = %v, want 'Review changes in Scheduled Tasks'", res.Data["label"])
	}
	urlParams, _ := res.Data["url_params"].(map[string]any)
	if urlParams["mode"] != "edit" || urlParams["taskId"] != "task-abc" {
		t.Errorf("url_params = %v, want mode=edit taskId=task-abc", urlParams)
	}
}

func TestScheduledTaskRedirectTool_Execute_DeleteMode(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	res, err := tl.Execute(t.Context(), map[string]any{
		"context": "Delete the Monday market summary task",
		"mode":    "delete",
		"task_id": "task-xyz",
	}, webCtx())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status == "error" {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if res.Data["label"] != "Confirm in Scheduled Tasks" {
		t.Errorf("label = %v, want 'Confirm in Scheduled Tasks'", res.Data["label"])
	}
	urlParams, _ := res.Data["url_params"].(map[string]any)
	if urlParams["mode"] != "delete" || urlParams["taskId"] != "task-xyz" {
		t.Errorf("url_params = %v, want mode=delete taskId=task-xyz", urlParams)
	}
}

func TestScheduledTaskRedirectTool_Execute_ViewMode(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	res, err := tl.Execute(t.Context(), map[string]any{
		"context": "show your scheduled tasks",
		"mode":    "view",
	}, webCtx())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status == "error" {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if res.Data["label"] != "Open Scheduled Tasks" {
		t.Errorf("label = %v, want 'Open Scheduled Tasks'", res.Data["label"])
	}
}

func TestScheduledTaskRedirectTool_Execute_PropagatesArtifacts(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	res, err := tl.Execute(t.Context(), map[string]any{
		"context":   "create with these files",
		"mode":      "create",
		"artifacts": []any{"prompt.md", "schedule.csv", 42},
	}, &tool.Context{SessionID: "sess-1", GatewayCapabilities: map[string]bool{"web_ui_redirect": true}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	arts, ok := res.Data["artifacts"].([]string)
	if !ok {
		t.Fatalf("artifacts not []string: %T", res.Data["artifacts"])
	}
	if len(arts) != 2 || arts[0] != "prompt.md" || arts[1] != "schedule.csv" {
		t.Errorf("artifacts = %v, want [prompt.md schedule.csv]", arts)
	}
	if res.Data["source_session_id"] != "sess-1" {
		t.Errorf("source_session_id = %v, want sess-1", res.Data["source_session_id"])
	}
}

func TestScheduledTaskRedirectTool_Execute_NonWebGatewayFallback(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	noWebCtx := &tool.Context{GatewayCapabilities: map[string]bool{}}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"create", map[string]any{"context": "ctx", "mode": "create"}},
		{"edit", map[string]any{"context": "ctx", "mode": "edit", "task_id": "t1"}},
		{"delete", map[string]any{"context": "ctx", "mode": "delete", "task_id": "t1"}},
		{"view", map[string]any{"context": "ctx", "mode": "view"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tl.Execute(t.Context(), tc.args, noWebCtx)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if res.Status == "error" {
				t.Fatalf("unexpected error result: %+v", res)
			}
			if res.Data != nil {
				if _, ok := res.Data["type"]; ok {
					t.Errorf("non-web gateway should not produce a redirect data part; got %v", res.Data)
				}
			}
			body := strings.ToLower(res.Message)
			if !strings.Contains(body, "web ui") || !strings.Contains(body, "scheduled tasks") {
				t.Errorf("fallback message should point to the web UI; got %q", res.Message)
			}
		})
	}
}

func TestScheduledTaskRedirectTool_Execute_NilToolContextFallsBack(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	res, err := tl.Execute(t.Context(), map[string]any{"context": "ctx", "mode": "create"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status == "error" {
		t.Fatalf("unexpected error: %+v", res)
	}
	if res.Data != nil {
		if _, ok := res.Data["type"]; ok {
			t.Errorf("nil toolCtx should not produce a redirect data part; got %v", res.Data)
		}
	}
}

func TestScheduledTaskRedirectTool_Execute_OmitsArtifactsWhenNoSession(t *testing.T) {
	t.Parallel()
	tl := NewScheduledTaskRedirectTool()
	res, err := tl.Execute(t.Context(), map[string]any{
		"context":   "ctx",
		"mode":      "create",
		"artifacts": []any{"a.txt"},
	}, webCtx())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, ok := res.Data["artifacts"]; ok {
		t.Error("artifacts should not be set when SessionID is empty")
	}
	if _, ok := res.Data["source_session_id"]; ok {
		t.Error("source_session_id should not be set when SessionID is empty")
	}
}
