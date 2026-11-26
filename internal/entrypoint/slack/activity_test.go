// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	goslack "github.com/slack-go/slack"
)

func newTestTimeline() *activityTimeline {
	return newActivityTimeline(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func taskUpdates(chunks []goslack.StreamChunk) []goslack.TaskUpdateChunk {
	var out []goslack.TaskUpdateChunk
	for _, c := range chunks {
		if tu, ok := c.(goslack.TaskUpdateChunk); ok {
			out = append(out, tu)
		}
	}
	return out
}

func planUpdates(chunks []goslack.StreamChunk) []goslack.PlanUpdateChunk {
	var out []goslack.PlanUpdateChunk
	for _, c := range chunks {
		if pu, ok := c.(goslack.PlanUpdateChunk); ok {
			out = append(out, pu)
		}
	}
	return out
}

func TestActivity_ToolCallLifecycle(t *testing.T) {
	tl := newTestTimeline()

	start := taskUpdates(tl.applySignal("tool_invocation_start", map[string]any{
		"tool_name":        "search_docs",
		"function_call_id": "fc1",
		"tool_args":        map[string]any{"query": "vacation policy"},
	}))
	if len(start) != 1 {
		t.Fatalf("expected 1 task_update, got %d", len(start))
	}
	if start[0].ID != "fc1" {
		t.Errorf("id = %q, want fc1", start[0].ID)
	}
	if start[0].Title != "search docs" {
		t.Errorf("title = %q, want %q", start[0].Title, "search docs")
	}
	if start[0].Status != goslack.TaskCardStatusInProgress {
		t.Errorf("status = %q, want in_progress", start[0].Status)
	}
	if start[0].Details != "" {
		t.Errorf("details should be empty (webui-parity minimal card), got %q", start[0].Details)
	}

	done := taskUpdates(tl.applySignal("tool_result", map[string]any{
		"tool_name":        "search_docs",
		"function_call_id": "fc1",
		"result_data":      map[string]any{"message": "5 hits"},
	}))
	if len(done) != 1 {
		t.Fatalf("expected 1 task_update on result, got %d", len(done))
	}
	if done[0].ID != "fc1" {
		t.Errorf("result id = %q, want fc1 (same card)", done[0].ID)
	}
	if done[0].Status != goslack.TaskCardStatusComplete {
		t.Errorf("status = %q, want complete", done[0].Status)
	}
	if done[0].Title != "search docs" {
		t.Errorf("title not carried over: %q", done[0].Title)
	}
	if done[0].Output != "" {
		t.Errorf("success output should be empty (webui-parity minimal card), got %q", done[0].Output)
	}
}

func TestActivity_ToolErrorResult(t *testing.T) {
	tl := newTestTimeline()
	tl.applySignal("tool_invocation_start", map[string]any{"tool_name": "x", "function_call_id": "fc1"})
	done := taskUpdates(tl.applySignal("tool_result", map[string]any{
		"function_call_id": "fc1",
		"result_data":      map[string]any{"status": "error", "message": "boom"},
	}))
	if done[0].Status != goslack.TaskCardStatusError {
		t.Errorf("status = %q, want error", done[0].Status)
	}
	if done[0].Output != "boom" {
		t.Errorf("error output = %q, want the failure surfaced", done[0].Output)
	}
}

func TestActivity_FailedStatusResult(t *testing.T) {
	tl := newTestTimeline()
	tl.applySignal("tool_invocation_start", map[string]any{"tool_name": "x", "function_call_id": "fc1"})
	done := taskUpdates(tl.applySignal("tool_result", map[string]any{
		"function_call_id": "fc1",
		"result_data":      map[string]any{"status": "failed", "message": "kaboom"},
	}))
	if done[0].Status != goslack.TaskCardStatusError {
		t.Errorf("status = %q, want error for a failed result", done[0].Status)
	}
	if done[0].Output != "kaboom" {
		t.Errorf("error output = %q, want the failure surfaced", done[0].Output)
	}
}

func TestActivity_FailureShapesTheCallSitesUsedToMiss(t *testing.T) {
	t.Run("result_data delivered as a JSON string", func(t *testing.T) {
		tl := newTestTimeline()
		tl.applySignal("tool_invocation_start", map[string]any{"tool_name": "x", "function_call_id": "fc1"})
		done := taskUpdates(tl.applySignal("tool_result", map[string]any{
			"function_call_id": "fc1",
			"result_data":      `{"status": "error", "message": "stringy boom"}`,
		}))
		if len(done) != 1 {
			t.Fatalf("expected 1 task_update, got %d", len(done))
		}
		if done[0].Status != goslack.TaskCardStatusError {
			t.Errorf("status = %q, want error for a JSON-string result_data", done[0].Status)
		}
		if done[0].Output != "stringy boom" {
			t.Errorf("output = %q, want the failure surfaced", done[0].Output)
		}
	})

	t.Run("outcome carried in result rather than result_data", func(t *testing.T) {
		tl := newTestTimeline()
		tl.applySignal("tool_invocation_start", map[string]any{"tool_name": "x", "function_call_id": "fc1"})
		done := taskUpdates(tl.applySignal("tool_result", map[string]any{
			"function_call_id": "fc1",
			"result":           map[string]any{"status": "error", "message": "fallback boom"},
		}))
		if len(done) != 1 {
			t.Fatalf("expected 1 task_update, got %d", len(done))
		}
		if done[0].Status != goslack.TaskCardStatusError {
			t.Errorf("status = %q, want error when the outcome is under `result`", done[0].Status)
		}
		if done[0].Output != "fallback boom" {
			t.Errorf("output = %q, want the failure surfaced", done[0].Output)
		}
	})

	t.Run("delegated sub-task shows its reason, not the Task scaffolding", func(t *testing.T) {
		tl := newTestTimeline()
		tl.applySignal("tool_invocation_start", map[string]any{"tool_name": "peer", "function_call_id": "fc1"})
		done := taskUpdates(tl.applySignal("tool_result", map[string]any{
			"function_call_id": "fc1",
			"result_data": map[string]any{
				"id": "task-9", "kind": "task", "contextId": "ctx-1",
				"artifacts": []any{}, "history": []any{},
				"status": map[string]any{
					"state": "failed",
					"message": map[string]any{
						"parts": []any{map[string]any{"kind": "text", "text": "the sub-agent could not reach the API"}},
					},
				},
			},
		}))
		if len(done) != 1 {
			t.Fatalf("expected 1 task_update, got %d", len(done))
		}
		if done[0].Status != goslack.TaskCardStatusError {
			t.Errorf("status = %q, want error for a failed sub-task", done[0].Status)
		}
		if done[0].Output != "the sub-agent could not reach the API" {
			t.Errorf("output = %q, want the sub-task's own reason", done[0].Output)
		}
		for _, leaked := range []string{"contextId", "artifacts", "history", "kind="} {
			if strings.Contains(done[0].Output, leaked) {
				t.Errorf("output leaked the Task scaffolding %q: %q", leaked, done[0].Output)
			}
		}
	})
}

func TestActivity_Delegation(t *testing.T) {
	tl := newTestTimeline()
	c := taskUpdates(tl.applySignal("tool_invocation_start", map[string]any{
		"tool_name":             "peer_billing",
		"function_call_id":      "fc9",
		"delegating_agent_name": "BillingAgent",
	}))
	if c[0].Title != "Delegating to BillingAgent" {
		t.Errorf("title = %q, want delegation title", c[0].Title)
	}
}

func TestActivity_ThinkingAccumulatesAndCloses(t *testing.T) {
	tl := newTestTimeline()
	a := taskUpdates(tl.applySignal("thinking_content", map[string]any{"content": "Step one. ", "is_complete": false}))
	b := taskUpdates(tl.applySignal("thinking_content", map[string]any{"content": "Step two.", "is_complete": false}))
	if a[0].ID != b[0].ID {
		t.Errorf("thinking chunks should share a card id: %q vs %q", a[0].ID, b[0].ID)
	}
	if !strings.Contains(b[0].Details, "Step one. Step two.") {
		t.Errorf("thinking did not accumulate: %q", b[0].Details)
	}
	if b[0].Status != goslack.TaskCardStatusInProgress {
		t.Errorf("status = %q, want in_progress while open", b[0].Status)
	}

	chunks := tl.applySignal("agent_progress_update", map[string]any{"status_text": "Now searching"})
	tus := taskUpdates(chunks)
	if len(tus) != 1 || tus[0].ID != a[0].ID || tus[0].Status != goslack.TaskCardStatusComplete {
		t.Errorf("expected thinking card closed to complete, got %+v", tus)
	}
	var sawPlan bool
	for _, c := range chunks {
		if _, ok := c.(goslack.PlanUpdateChunk); ok {
			sawPlan = true
		}
	}
	if !sawPlan {
		t.Error("expected a plan_update chunk for agent_progress_update")
	}
}

func TestActivity_ThinkingCompleteFlag(t *testing.T) {
	tl := newTestTimeline()
	c := taskUpdates(tl.applySignal("thinking_content", map[string]any{"content": "done thinking", "is_complete": true}))
	if c[0].Status != goslack.TaskCardStatusComplete {
		t.Errorf("status = %q, want complete on is_complete", c[0].Status)
	}
	c2 := taskUpdates(tl.applySignal("thinking_content", map[string]any{"content": "again", "is_complete": false}))
	if c2[0].ID == c[0].ID {
		t.Errorf("new thinking phase should use a new id, both were %q", c[0].ID)
	}
}

func TestActivity_Artifact(t *testing.T) {
	tl := newTestTimeline()
	c := taskUpdates(tl.applySignal("artifact_creation_progress", map[string]any{
		"filename": "report.pdf", "status": "completed",
	}))
	if c[0].ID != "artifact:report.pdf" || c[0].Status != goslack.TaskCardStatusComplete {
		t.Errorf("unexpected artifact card: %+v", c[0])
	}
	if c[0].Title != "Created report.pdf" {
		t.Errorf("title = %q", c[0].Title)
	}
}

func TestActivity_PlanTitle(t *testing.T) {
	tl := newTestTimeline()
	chunks := tl.applySignal("agent_progress_update", map[string]any{"status_text": "Working on it"})
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	pu, ok := chunks[0].(goslack.PlanUpdateChunk)
	if !ok || pu.Title != "Working on it" {
		t.Errorf("expected plan_update 'Working on it', got %+v", chunks[0])
	}
}

func TestActivity_ResultRedactsSecrets(t *testing.T) {
	details := summarizeResult(map[string]any{
		"query":         "vacation policy",
		"api_key":       "sk-supersecret",
		"authorization": "Bearer xyz",
		"PASSWORD":      "hunter2",
	})
	if strings.Contains(details, "sk-supersecret") || strings.Contains(details, "hunter2") || strings.Contains(details, "Bearer xyz") {
		t.Errorf("secret value leaked into result summary: %q", details)
	}
	if !strings.Contains(details, "api_key=<redacted>") {
		t.Errorf("expected api_key redacted, got %q", details)
	}
	if !strings.Contains(details, "vacation policy") {
		t.Errorf("non-secret field should be preserved, got %q", details)
	}
}

func TestActivity_StructuredResultIsNotAnError(t *testing.T) {
	success := map[string]any{
		"status":  "success",
		"message": "Query returned 28 rows",
		"content": `[{"region":"north"}]`,
	}
	if isErrorResult(success) {
		t.Errorf("structured success payload classified as an error: %v", success)
	}

	failure := map[string]any{"status": "error", "message": "boom"}
	if !isErrorResult(failure) {
		t.Errorf("structured error payload not classified as an error: %v", failure)
	}
}

func TestActivity_UnknownSignalIgnored(t *testing.T) {
	tl := newTestTimeline()
	if c := tl.applySignal("llm_invocation", map[string]any{"request": map[string]any{}}); len(c) != 0 {
		t.Errorf("unknown signal should yield no chunks, got %d", len(c))
	}
}

func TestActivity_CardCap(t *testing.T) {
	tl := newTestTimeline()
	for i := 0; i < maxTimelineCards+5; i++ {
		id := "fc" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		tl.applySignal("tool_invocation_start", map[string]any{"tool_name": "t", "function_call_id": id})
	}
	if len(tl.cardTitles) != maxTimelineCards {
		t.Errorf("card count = %d, want capped at %d", len(tl.cardTitles), maxTimelineCards)
	}
	c := taskUpdates(tl.applySignal("tool_result", map[string]any{"function_call_id": "fcA0", "result_data": "ok"}))
	if len(c) != 1 {
		t.Errorf("update to existing card should pass the cap, got %d chunks", len(c))
	}
}

func TestActivity_DetailsTruncatedRuneSafe(t *testing.T) {
	tl := newTestTimeline()
	long := strings.Repeat("✅", 1000)
	c := taskUpdates(tl.applySignal("thinking_content", map[string]any{"content": long, "is_complete": true}))
	if !utf8.ValidString(c[0].Details) {
		t.Error("truncated details is not valid UTF-8")
	}
	if len(c[0].Details) > cardDetailLimit {
		t.Errorf("details = %d bytes, want <= %d", len(c[0].Details), cardDetailLimit)
	}
}

func TestActivity_ChunksMarshalToWireTypes(t *testing.T) {
	tl := newTestTimeline()
	chunks := tl.applySignal("tool_invocation_start", map[string]any{
		"tool_name": "search", "function_call_id": "fc1", "tool_args": map[string]any{"q": "x"},
	})
	chunks = append(chunks, goslack.NewMarkdownTextChunk("hello"))

	b, err := json.Marshal(chunks)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"type":"task_update"`) {
		t.Errorf("missing task_update type in %s", s)
	}
	if !strings.Contains(s, `"type":"markdown_text"`) {
		t.Errorf("missing markdown_text type in %s", s)
	}
}

func TestActivity_FinalizeClosesThinking(t *testing.T) {
	tl := newTestTimeline()
	tl.applySignal("thinking_content", map[string]any{"content": "mid", "is_complete": false})
	fin := taskUpdates(tl.finalize())
	if len(fin) != 1 || fin[0].Status != goslack.TaskCardStatusComplete {
		t.Errorf("finalize should complete the open thinking card, got %+v", fin)
	}
	if extra := tl.finalize(); len(extra) != 0 {
		t.Errorf("second finalize should be a no-op, got %d", len(extra))
	}
}
