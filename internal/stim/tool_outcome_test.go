// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package stim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

const toolOutcomeTrace = `
invocation_details:
  task_id: "task1234"
  status: "completed"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000009000
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000001000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_invocation_start"
                  tool_name: "web_search_google"
                  function_call_id: "call-1"
                  tool_args: {"query": "first"}
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000002000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_result"
                  tool_name: "web_search_google"
                  function_call_id: "call-1"
                  result_data: {"formatted_results": "=== SEARCH RESULTS ==="}
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000003000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_invocation_start"
                  tool_name: "web_search_google"
                  function_call_id: "call-2"
                  tool_args: {"query": "second"}
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000004000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_result"
                  tool_name: "web_search_google"
                  function_call_id: "call-2"
                  result_data: {"status": "error", "message": "search backend returned 429"}
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000005000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_invocation_start"
                  tool_name: "get_time"
                  function_call_id: "call-3"
                  tool_args: {"timezone": "UTC"}
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000006000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_result"
                  tool_name: "get_time"
                  function_call_id: "call-3"
                  result_data: "2024-01-01T00:00:00Z"
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000007000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_result"
                  tool_name: "web_search_google"
                  function_call_id: "call-4"
                  result_data: {"status": "failed", "message": "quota exhausted"}
  - topic: "solace-agent-mesh/v1/gateway/response/task1234"
    direction: "response"
    task_id: "task1234"
    created_time: 1700000009000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "Done"
`

func TestAnalyze_ToolInvocationsCarryOutcome(t *testing.T) {
	s, err := ParseAndAnalyze([]byte(toolOutcomeTrace))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}

	if len(s.ToolInvocations) != 4 {
		t.Fatalf("got %d invocations, want 4 (three started, one result-only): %#v", len(s.ToolInvocations), s.ToolInvocations)
	}

	want := []ToolInvocation{
		{Name: "web_search_google", CallID: "call-1", Completed: true, Failed: false},
		{Name: "web_search_google", CallID: "call-2", Completed: true, Failed: true},
		{Name: "get_time", CallID: "call-3", Completed: true, Failed: false},
		{Name: "web_search_google", CallID: "call-4", Completed: true, Failed: true},
	}
	for i, w := range want {
		got := s.ToolInvocations[i]
		if got.Name != w.Name || got.CallID != w.CallID || got.Completed != w.Completed || got.Failed != w.Failed {
			t.Errorf("invocation %d = {name:%q call:%q completed:%v failed:%v}, want {name:%q call:%q completed:%v failed:%v}",
				i, got.Name, got.CallID, got.Completed, got.Failed, w.Name, w.CallID, w.Completed, w.Failed)
		}
	}
}

func TestAnalyze_ToolCallCountsKeepAttemptSemantics(t *testing.T) {
	s, err := ParseAndAnalyze([]byte(toolOutcomeTrace))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.Stats.TotalToolCalls != 3 {
		t.Errorf("TotalToolCalls = %d, want 3 (one per tool_call step)", s.Stats.TotalToolCalls)
	}
	if got := s.Stats.ToolCallCounts["web_search_google"]; got != 2 {
		t.Errorf("ToolCallCounts[web_search_google] = %d, want 2", got)
	}
	if got := s.Stats.ToolCallCounts["get_time"]; got != 1 {
		t.Errorf("ToolCallCounts[get_time] = %d, want 1", got)
	}

	var names []string
	for _, step := range s.Steps {
		if step.Type == "tool_call" {
			names = append(names, step.ToolName)
		}
	}
	if len(names) != 3 {
		t.Errorf("tool_call step names = %v, want 3 entries", names)
	}
}

func TestAnalyze_FailureCountsAreSeparatelyAvailable(t *testing.T) {
	s, err := ParseAndAnalyze([]byte(toolOutcomeTrace))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.Stats.TotalToolFailures != 2 {
		t.Errorf("TotalToolFailures = %d, want 2", s.Stats.TotalToolFailures)
	}
	if got := s.Stats.ToolFailureCounts["web_search_google"]; got != 2 {
		t.Errorf("ToolFailureCounts[web_search_google] = %d, want 2", got)
	}
	if _, present := s.Stats.ToolFailureCounts["get_time"]; present {
		t.Errorf("get_time succeeded and must not appear in ToolFailureCounts: %v", s.Stats.ToolFailureCounts)
	}

	var failedSteps int
	for _, step := range s.Steps {
		if step.Type == "tool_result" && step.ToolFailed {
			failedSteps++
		}
	}
	if failedSteps != 2 {
		t.Errorf("tool_result steps flagged failed = %d, want 2", failedSteps)
	}
}

func TestAnalyze_FailureCountsComeFromInvocationsNotSteps(t *testing.T) {
	s, err := ParseAndAnalyze([]byte(republishedDuplicateTrace))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}

	var failedSteps int
	for _, step := range s.Steps {
		if step.Type == "tool_result" && step.ToolFailed {
			failedSteps++
		}
	}
	if failedSteps != 2 {
		t.Fatalf("fixture no longer holds a republished duplicate: failing steps = %d, want 2", failedSteps)
	}

	if s.Stats.TotalToolFailures != 1 {
		t.Errorf("TotalToolFailures = %d, want 1: the agent republished one call's result, it did not fail twice",
			s.Stats.TotalToolFailures)
	}
	if got := s.Stats.ToolFailureCounts["web_search_google"]; got != 1 {
		t.Errorf("ToolFailureCounts[web_search_google] = %d, want 1", got)
	}
	if len(s.ToolInvocations) != 1 {
		t.Errorf("ToolInvocations = %d, want 1", len(s.ToolInvocations))
	}
}

const republishedDuplicateTrace = `
invocation_details:
  task_id: "task1234"
  status: "completed"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000009000
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000001000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_invocation_start"
                  tool_name: "web_search_google"
                  function_call_id: "call-1"
                  tool_args: {"query": "first"}
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000002000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_result"
                  tool_name: "web_search_google"
                  function_call_id: "call-1"
                  result_data: {"status": "error", "message": "search backend returned 429"}
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000003000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_result"
                  tool_name: "web_search_google"
                  function_call_id: "call-1"
                  result_data: {"status": "error", "message": "search backend returned 429"}
  - topic: "solace-agent-mesh/v1/gateway/response/task1234"
    direction: "response"
    task_id: "task1234"
    created_time: 1700000009000
    payload:
      result:
        kind: "task"
        status:
          state: "completed"
          message:
            parts:
              - kind: "text"
                text: "done"
`

func TestToolResultFailed(t *testing.T) {
	failing := []any{
		map[string]any{"status": "error", "message": "boom"},
		map[string]any{"status": "failed", "message": "fatal"},
		map[string]any{"status": "ERROR"},
		map[string]any{"error": "boom"},
		map[string]any{"error": true},
		map[string]any{"error": map[string]any{"code": "429"}},
		`{"status": "error"}`,
	}
	for _, rd := range failing {
		if !ToolResultFailed(rd) {
			t.Errorf("want failed for %#v", rd)
		}
	}

	succeeding := []any{
		map[string]any{"status": "success"},
		map[string]any{"status": "partial"},
		map[string]any{"formatted_results": "..."},
		map[string]any{"error": ""},
		map[string]any{"error": false},
		"2024-01-01T00:00:00Z",
		nil,
	}
	for _, rd := range succeeding {
		if ToolResultFailed(rd) {
			t.Errorf("want not-failed for %#v", rd)
		}
	}
}

func TestAnalyze_UnpairedInvocationStaysIncomplete(t *testing.T) {
	trace := `
invocation_details:
  task_id: "task1234"
  status: "completed"
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/status/task1234"
    direction: "status"
    task_id: "task1234"
    created_time: 1700000001000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_invocation_start"
                  tool_name: "web_request"
                  function_call_id: "call-1"
                  tool_args: {"url": "https://example.com"}
`
	s, err := ParseAndAnalyze([]byte(trace))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if len(s.ToolInvocations) != 1 {
		t.Fatalf("got %d invocations, want 1", len(s.ToolInvocations))
	}
	inv := s.ToolInvocations[0]
	if inv.Completed || inv.Failed {
		t.Errorf("invocation without a result = {completed:%v failed:%v}, want both false", inv.Completed, inv.Failed)
	}
	if s.Stats.TotalToolCalls != 1 {
		t.Errorf("TotalToolCalls = %d, want 1", s.Stats.TotalToolCalls)
	}
}

func TestRecordToolResult_DuplicateDoesNotDoubleCount(t *testing.T) {
	s := &Summary{}
	s.ToolInvocations = append(s.ToolInvocations, ToolInvocation{Name: "web_search", CallID: "call-1"})
	res := StatusDetail{Type: "tool_result", ToolName: "web_search", CallID: "call-1", ToolFailed: true}

	recordToolResult(s, res, "task-1")
	recordToolResult(s, res, "task-1")

	if len(s.ToolInvocations) != 1 {
		t.Fatalf("got %d invocations, want 1: the duplicate was appended", len(s.ToolInvocations))
	}
	stats := computeStats(nil, nil, s.ToolInvocations)
	if stats.TotalToolFailures != 1 {
		t.Errorf("TotalToolFailures = %d, want 1", stats.TotalToolFailures)
	}
	if got := stats.ToolFailureCounts["web_search"]; got != 1 {
		t.Errorf("ToolFailureCounts[web_search] = %d, want 1", got)
	}
}

func TestMatchInvocation_CallIDMissDoesNotFallBackToName(t *testing.T) {
	invs := []ToolInvocation{{Name: "web_search", CallID: "parent-call-1"}}

	if got := matchInvocation(invs, "web_search", "subagent-call-9"); got != -1 {
		t.Errorf("matched index %d for an unknown call ID, want -1", got)
	}
	if got := matchInvocation(invs, "web_search", "parent-call-1"); got != 0 {
		t.Errorf("matched %d for the known call ID, want 0", got)
	}
	if got := matchInvocation(invs, "web_search", ""); got != 0 {
		t.Errorf("matched %d for a name-only result, want 0", got)
	}
}

func TestToolResultFailed_Shapes(t *testing.T) {
	for _, c := range []struct {
		name string
		data any
		want bool
	}{
		{"error status", map[string]any{"status": "error"}, true},
		{"failed status", map[string]any{"status": "failed"}, true},
		{"tool payload plus status", map[string]any{"error_type": "smtp_reject", "status": "error"}, true},

		{"delegated task failed", map[string]any{"status": map[string]any{"state": "failed"}}, true},
		{"delegated task canceled", map[string]any{"status": map[string]any{"state": "canceled"}}, true},
		{"delegated task completed", map[string]any{"status": map[string]any{"state": "completed"}}, false},

		{"success with error:none", map[string]any{"status": "success", "error": "none"}, false},
		{"success with error text", map[string]any{"status": "ok", "error": "no retries needed"}, false},

		{"error key only", map[string]any{"error": "boom"}, true},
		{"empty error key", map[string]any{"error": ""}, false},
		{"no outcome at all", map[string]any{"filename": "a.txt"}, false},
		{"bare string", "just text", false},
		{"nil", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ToolResultFailed(c.data); got != c.want {
				t.Errorf("ToolResultFailed(%#v) = %v, want %v", c.data, got, c.want)
			}
		})
	}
}

func TestDropBlockScalarContent_CoversAnyKeyAndFoldedStyle(t *testing.T) {
	for _, c := range []struct{ name, key, indicator string }{
		{"text", "text", "|4-"},
		{"message", "message", "|4-"},
		{"content", "content", "|4-"},
		{"python_code", "python_code", "|6-"},
		{"preview", "preview", "|4-"},
		{"code", "code", "|4-"},
		{"folded style", "content", ">4-"},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc := "invocation_flow:\n  - payload:\n      " + c.key + ": " + c.indicator +
				"\n          body line one\n          body line two\n  - other: kept\n"
			out, dropped := dropBlockScalarContent([]byte(doc))
			if dropped != 1 {
				t.Fatalf("dropped = %d, want 1: the %s scalar was passed through", dropped, c.key)
			}
			if strings.Contains(string(out), c.indicator) {
				t.Errorf("indicator %q survives, so the retry re-parses the same invalid YAML:\n%s", c.indicator, out)
			}
			if !strings.Contains(string(out), "other: kept") {
				t.Errorf("salvage dropped an unrelated key:\n%s", out)
			}
			var doc2 Document
			if err := yaml.Unmarshal(out, &doc2); err != nil {
				t.Errorf("salvaged output still does not parse: %v\n%s", err, out)
			}
		})
	}
}

func TestMatchInvocation_NameFallbackPrefersMostRecentOpenCall(t *testing.T) {
	invs := []ToolInvocation{
		{Name: "web_search", Result: "older"},
		{Name: "web_search", Result: "newer"},
	}
	if got := matchInvocation(invs, "web_search", ""); got != 1 {
		t.Errorf("matched index %d, want 1 (the most recent open call)", got)
	}

	invs[1].Completed = true
	if got := matchInvocation(invs, "web_search", ""); got != 0 {
		t.Errorf("matched index %d after closing the newer call, want 0", got)
	}
}

func TestAnalyze_FailureUnderResultKeyIsClassified(t *testing.T) {
	trace := `
invocation_details:
  task_id: "t-1"
  status: "completed"
invocation_flow:
  - topic: "ns/a2a/v1/gateway/status/gw/t-1"
    direction: "status"
    task_id: "t-1"
    created_time: 1700000001000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            parts:
              - kind: "data"
                data:
                  type: "tool_result"
                  tool_name: "flaky_tool"
                  function_call_id: "call-1"
                  result:
                    status: "error"
                    message: "429 from upstream"
`
	s, err := ParseAndAnalyze([]byte(trace))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.Stats.TotalToolFailures != 1 {
		t.Errorf("TotalToolFailures = %d, want 1: a failure under `result` was not classified", s.Stats.TotalToolFailures)
	}
	if len(s.ToolInvocations) != 1 || !s.ToolInvocations[0].Failed {
		t.Errorf("invocation not marked failed: %+v", s.ToolInvocations)
	}
}

func TestToolResultFailed_AllDelegatedStates(t *testing.T) {
	for _, state := range []string{"failed", "error", "rejected", "canceled", "cancelled"} {
		if !ToolResultFailed(map[string]any{"status": map[string]any{"state": state}}) {
			t.Errorf("delegated state %q not treated as a failure", state)
		}
	}
	for _, state := range []string{"completed", "working", "submitted", ""} {
		if ToolResultFailed(map[string]any{"status": map[string]any{"state": state}}) {
			t.Errorf("delegated state %q treated as a failure", state)
		}
	}
}

func TestToolResultFailed_DelegatedStatusIsDecisive(t *testing.T) {
	if ToolResultFailed(map[string]any{
		"status": map[string]any{"state": "completed"},
		"error":  "no retries were needed",
	}) {
		t.Error("a completed delegated task with a descriptive error field was called a failure")
	}
	if !ToolResultFailed(map[string]any{
		"status": map[string]any{"state": "failed"},
		"error":  "",
	}) {
		t.Error("a failed delegated task was not called a failure")
	}
}

func TestParseAndAnalyze_RejectsTruncatedMidLine(t *testing.T) {
	full := []byte(toolOutcomeTrace)
	if _, err := ParseAndAnalyze(full); err != nil {
		t.Fatalf("the complete fixture must parse: %v", err)
	}

	rejected, parseable := 0, 0
	for cut := 200; cut < len(full); cut += 25 {
		if full[cut-1] == '\n' {
			continue
		}
		var probe Document
		if yaml.Unmarshal(full[:cut], &probe) != nil {
			continue
		}
		parseable++
		if _, err := ParseAndAnalyze(full[:cut]); err != nil {
			rejected++
		}
	}
	if parseable == 0 {
		t.Fatal("no parseable mid-line truncation in the fixture; the test proves nothing")
	}
	if rejected != parseable {
		t.Errorf("rejected %d of %d parseable mid-line truncations; each reports a plausible summary with a failure silently missing",
			rejected, parseable)
	}
}

func TestParseAndAnalyze_AcceptsEveryCompleteTrace(t *testing.T) {
	for name, src := range map[string]string{
		"toolOutcomeTrace":          toolOutcomeTrace,
		"republishedDuplicateTrace": republishedDuplicateTrace,
		"terse, payload-less": `
invocation_details:
  task_id: "t1"
  status: "completed"
invocation_flow:
  - topic: "solace-agent-mesh/a2a/v1/agent/request/Orchestrator"
    direction: "request"
    task_id: "t1"
    created_time: 1700000001000
`,
	} {
		if _, err := ParseAndAnalyze([]byte(src)); err != nil {
			t.Errorf("%s: a complete trace was rejected: %v", name, err)
		}
	}

	if !sourcetree.IsInternal(t) {
		return
	}
	dir := "../../cmd/stim2perf/testdata"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the .stim corpus: %v", err)
	}
	seen := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".stim" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		seen++
		if _, err := ParseAndAnalyze(b); err != nil {
			t.Errorf("%s: a shipped trace was rejected: %v", e.Name(), err)
		}
	}
	if seen == 0 {
		t.Error("no .stim files found; the corpus check proves nothing")
	}
}

func TestParseAndAnalyze_LineBoundaryTruncationIsNotCaught(t *testing.T) {
	full := toolOutcomeTrace
	ref, err := ParseAndAnalyze([]byte(full))
	if err != nil {
		t.Fatalf("the complete fixture must parse: %v", err)
	}

	lines := strings.SplitAfter(full, "\n")
	found := false
	for n := 10; n < len(lines); n++ {
		cut := strings.Join(lines[:n], "")
		s, err := ParseAndAnalyze([]byte(cut))
		if err != nil || s == nil {
			continue
		}
		if len(s.Steps) < len(ref.Steps) {
			found = true
			if s.Degraded {
				t.Errorf("a line-boundary truncation set Degraded; the documented residual no longer holds")
			}
			break
		}
	}
	if !found {
		t.Error("no line-boundary truncation was silently accepted; checkDocumentShape's stated residual is now wrong")
	}
}
