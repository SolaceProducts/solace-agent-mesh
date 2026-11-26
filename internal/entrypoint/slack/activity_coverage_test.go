// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestActivity_WorkflowNodes(t *testing.T) {
	tl := newTestTimeline()
	start := taskUpdates(tl.applySignal("workflow_node_execution_start", map[string]any{
		"node_id": "n1", "node_name": "Fetch", "status_text": "fetching",
	}))
	if len(start) != 1 || start[0].ID != "n1" || start[0].Title != "Fetch" || start[0].Status != goslack.TaskCardStatusInProgress {
		t.Fatalf("unexpected workflow start card: %+v", start)
	}
	done := taskUpdates(tl.applySignal("workflow_node_execution_result", map[string]any{"node_id": "n1", "node_name": "Fetch"}))
	if done[0].ID != "n1" || done[0].Status != goslack.TaskCardStatusComplete {
		t.Errorf("unexpected workflow result card: %+v", done[0])
	}
}

func TestActivity_BuilderComponent(t *testing.T) {
	tl := newTestTimeline()
	cases := []struct {
		state string
		want  goslack.TaskCardStatus
	}{
		{"active", goslack.TaskCardStatusInProgress},
		{"completed", goslack.TaskCardStatusComplete},
		{"skipped", goslack.TaskCardStatusComplete},
		{"failed", goslack.TaskCardStatusError},
	}
	for _, tc := range cases {
		c := taskUpdates(tl.applySignal("builder_component_progress", map[string]any{
			"component_id": "skill:web", "component_name": "Web Search", "state": tc.state, "status_text": "working",
		}))
		if len(c) != 1 || c[0].ID != "skill:web" || c[0].Status != tc.want {
			t.Errorf("state %q → %+v, want status %q", tc.state, c[0], tc.want)
		}
	}
	if c := tl.applySignal("builder_component_progress", map[string]any{"state": "active"}); len(c) != 0 {
		t.Errorf("builder progress without component_id should yield no chunks")
	}
}

func TestActivity_ArtifactSavedAndFailed(t *testing.T) {
	tl := newTestTimeline()
	saved := taskUpdates(tl.applySignal("artifact_saved", map[string]any{"filename": "out.csv"}))
	if saved[0].ID != "artifact:out.csv" || saved[0].Status != goslack.TaskCardStatusComplete {
		t.Errorf("artifact_saved → %+v", saved[0])
	}
	failed := taskUpdates(tl.applySignal("artifact_creation_progress", map[string]any{"filename": "bad.bin", "status": "failed"}))
	if failed[0].Status != goslack.TaskCardStatusError {
		t.Errorf("failed artifact → status %q, want error", failed[0].Status)
	}
	if c := tl.applySignal("artifact_saved", map[string]any{}); len(c) != 0 {
		t.Error("artifact_saved without filename should yield no chunks")
	}
}

func TestActivity_PureHelpers(t *testing.T) {
	if humanizeTool("search_docs-v2") != "search docs v2" {
		t.Errorf("humanizeTool replace: %q", humanizeTool("search_docs-v2"))
	}
	if humanizeTool("") != "Tool" {
		t.Errorf("humanizeTool empty: %q", humanizeTool(""))
	}
	if nodeTitle(map[string]any{"node_name": "N"}) != "N" {
		t.Error("nodeTitle prefers node_name")
	}
	if nodeTitle(map[string]any{"node_id": "id1"}) != "id1" {
		t.Error("nodeTitle falls back to node_id")
	}
	if scalarPreview("s") != "s" || scalarPreview(nil) != "" {
		t.Error("scalarPreview string/nil")
	}
	if got := scalarPreview(map[string]any{"a": 1}); !strings.Contains(got, "\"a\"") {
		t.Errorf("scalarPreview map: %q", got)
	}
	if summarizeResult(nil) != "" || summarizeResult("x") != "x" {
		t.Error("summarizeResult nil/string")
	}
	if summarizeResult(map[string]any{"message": "m"}) != "m" {
		t.Error("summarizeResult map message")
	}
	if got := summarizeResult(map[string]any{"k": "v"}); got != "k=v" {
		t.Errorf("summarizeResult composite map: %q, want k=v", got)
	}
	if !isErrorResult(map[string]any{"isError": true}) ||
		!isErrorResult(map[string]any{"status": "error"}) ||
		!isErrorResult(map[string]any{"error": "x"}) {
		t.Error("isErrorResult positive cases")
	}
	if isErrorResult(map[string]any{"ok": true}) || isErrorResult("not a map") {
		t.Error("isErrorResult negative cases")
	}
	if truncateRunes("short", 600) != "short" {
		t.Error("truncateRunes no-op for short")
	}
}

func TestActivity_PlanTitleSingleLine(t *testing.T) {
	tl := newTestTimeline()
	long := "Saving artifact `sample_data.json`:\nSample JSON data with user records and metadata"
	plans := planUpdates(tl.applySignal("agent_progress_update", map[string]any{"status_text": long}))
	if len(plans) != 1 {
		t.Fatalf("want 1 plan_update chunk, got %d", len(plans))
	}
	got := plans[0].Title
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("plan title contains a newline (would bleed): %q", got)
	}
	if len([]rune(got)) > planTitleLimit {
		t.Errorf("plan title = %d runes, want <= %d: %q", len([]rune(got)), planTitleLimit, got)
	}
	if planTitle("") != "" {
		t.Error("empty status_text should yield empty title")
	}
}

func TestActivity_CardCapDropsAndLogs(t *testing.T) {
	tl := newTestTimeline()
	for i := 0; i < maxTimelineCards+3; i++ {
		tl.applySignal("workflow_node_execution_start", map[string]any{
			"node_id": "node-" + strings.Repeat("x", 1) + string(rune('a'+i%26)) + string(rune('0'+i/26)),
		})
	}
	if len(tl.cardTitles) != maxTimelineCards {
		t.Errorf("card count = %d, want capped at %d", len(tl.cardTitles), maxTimelineCards)
	}
}
