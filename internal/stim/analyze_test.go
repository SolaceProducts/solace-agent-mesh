// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package stim

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestParseAndAnalyze_Basic(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "abc12345-6789-0000-0000-000000000000"
  status: "completed"
  initial_request_text: "Hello world"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000005000
invocation_flow:
  - topic: "solace-agent-mesh/v1/agent/request/TestAgent/abc12345"
    direction: "request"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000000000
    payload:
      params:
        message:
          parts:
            - kind: "text"
              text: "Hello world"
  - topic: "solace-agent-mesh/v1/gateway/response/abc12345"
    direction: "response"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000005000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "Hello back!"
        metadata:
          token_usage:
            total_input_tokens: 100
            total_output_tokens: 50
            total_cached_input_tokens: 10
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.TaskID != "abc12345-6789-0000-0000-000000000000" {
		t.Errorf("got TaskID=%q, want abc12345-...", s.TaskID)
	}
	if s.Status != "completed" {
		t.Errorf("got Status=%q, want completed", s.Status)
	}
	if s.DurationMs != 5000 {
		t.Errorf("got DurationMs=%d, want 5000", s.DurationMs)
	}
	if s.Request != "Hello world" {
		t.Errorf("got Request=%q, want 'Hello world'", s.Request)
	}
	if s.FinalOutput != "Hello back!" {
		t.Errorf("got FinalOutput=%q, want 'Hello back!'", s.FinalOutput)
	}
	if s.TokenUsage == nil {
		t.Fatal("expected TokenUsage, got nil")
	}
	if s.TokenUsage.InputTokens != 100 {
		t.Errorf("got InputTokens=%d, want 100", s.TokenUsage.InputTokens)
	}
	if s.TokenUsage.OutputTokens != 50 {
		t.Errorf("got OutputTokens=%d, want 50", s.TokenUsage.OutputTokens)
	}
	if s.TokenUsage.CachedInputTokens != 10 {
		t.Errorf("got CachedInputTokens=%d, want 10", s.TokenUsage.CachedInputTokens)
	}
	if len(s.Steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(s.Steps))
	}
	if s.Steps[0].Type != "request" {
		t.Errorf("step 0 type=%q, want request", s.Steps[0].Type)
	}
	if s.Steps[1].Type != "response" {
		t.Errorf("step 1 type=%q, want response", s.Steps[1].Type)
	}
}

func TestParseAndAnalyze_ToolCalls(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "task1234"
  status: "completed"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000003000
invocation_flow:
  - topic: "solace-agent-mesh/v1/agent/request/Agent/task1234"
    direction: "request"
    task_id: "task1234"
    created_time: 1700000000000
    payload:
      params:
        message:
          parts:
            - kind: "text"
              text: "Do something"
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
                  tool_name: "get_time"
                  tool_args: {"timezone": "UTC"}
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
                  tool_name: "get_time"
                  result_data: "2024-01-01T00:00:00Z"
  - topic: "solace-agent-mesh/v1/gateway/response/task1234"
    direction: "response"
    task_id: "task1234"
    created_time: 1700000003000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "The time is now"
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.Steps) != 4 {
		t.Fatalf("got %d steps, want 4", len(s.Steps))
	}
	if s.Steps[1].Type != "tool_call" {
		t.Errorf("step 1 type=%q, want tool_call", s.Steps[1].Type)
	}
	if s.Steps[2].Type != "tool_result" {
		t.Errorf("step 2 type=%q, want tool_result", s.Steps[2].Type)
	}
}

func TestParseAndAnalyze_SubTasks(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "parent01"
  status: "completed"
  total_tasks: 2
  start_time: 1700000000000
  end_time: 1700000010000
invocation_flow:
  - topic: "solace-agent-mesh/v1/agent/request/Orchestrator/parent01"
    direction: "request"
    task_id: "parent01"
    created_time: 1700000000000
    payload:
      params:
        message:
          parts:
            - kind: "text"
              text: "Do complex task"
  - topic: "solace-agent-mesh/v1/agent/request/Helper/child001"
    direction: "request"
    task_id: "child001"
    created_time: 1700000002000
    payload:
      params:
        message:
          parts:
            - kind: "text"
              text: "Sub task"
          metadata:
            parentTaskId: "parent01"
  - topic: "solace-agent-mesh/v1/gateway/response/parent01"
    direction: "response"
    task_id: "parent01"
    created_time: 1700000010000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "Done"
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.SubTasks) != 1 {
		t.Fatalf("got %d sub-tasks, want 1", len(s.SubTasks))
	}
	if s.SubTasks[0].Agent != "Helper" {
		t.Errorf("sub-task agent=%q, want Helper", s.SubTasks[0].Agent)
	}
	if s.SubTasks[0].ParentTaskID != "parent01" {
		t.Errorf("sub-task parent=%q, want parent01", s.SubTasks[0].ParentTaskID)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{500, "500ms"},
		{1500, "1.5s"},
		{65000, "1m5.0s"},
	}
	for _, tt := range tests {
		got := FormatDuration(tt.ms)
		if got != tt.want {
			t.Errorf("FormatDuration(%d)=%q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := Truncate("hello world", 5); got != "hello..." {
		t.Errorf("got %q", got)
	}
	if got := Truncate("line1\nline2", 20); got != "line1 line2" {
		t.Errorf("got %q", got)
	}
}

func TestTruncID(t *testing.T) {
	if got := TruncID("abcdefghijklmnop"); got != "abcdefgh" {
		t.Errorf("got %q", got)
	}
	if got := TruncID("short"); got != "short" {
		t.Errorf("got %q", got)
	}
}

func TestExtractErrorTokenUsage(t *testing.T) {
	payload := map[string]any{
		"error": map[string]any{
			"code":    -32603,
			"message": "Unable to complete request — conversation history too long.",
			"data": map[string]any{
				"token_usage": map[string]any{
					"total_input_tokens":        float64(1836947),
					"total_output_tokens":       float64(4103),
					"total_cached_input_tokens": float64(1830000),
					"total_cache_write_tokens":  float64(220000),
					"total_reasoning_tokens":    float64(0),
				},
			},
		},
	}
	tu := ExtractErrorTokenUsage(payload)
	if tu == nil {
		t.Fatal("ExtractErrorTokenUsage = nil, want usage")
	}
	if tu.InputTokens != 1836947 || tu.OutputTokens != 4103 || tu.CachedInputTokens != 1830000 {
		t.Errorf("usage = %+v, want in=1836947 out=4103 cached=1830000", tu)
	}
	if tu.CacheWriteTokens != 220000 {
		t.Errorf("CacheWriteTokens = %d, want 220000", tu.CacheWriteTokens)
	}

	if got := ExtractErrorTokenUsage(map[string]any{"error": map[string]any{"message": "boom"}}); got != nil {
		t.Errorf("usage for data-less error = %+v, want nil", got)
	}
}

func TestParseAndAnalyze_ErrorEventCarriesTokenUsage(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "abc12345-6789-0000-0000-000000000000"
  status: "failed"
  initial_request_text: "long research task"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000005000
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "error"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000004000
    payload:
      error:
        code: -32603
        message: "Unable to complete request — conversation history too long."
        data:
          token_usage:
            total_input_tokens: 1836947
            total_output_tokens: 4103
            total_cached_input_tokens: 1830000
            total_reasoning_tokens: 0
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil, want usage from error.data.token_usage")
	}
	if s.TokenUsage.InputTokens != 1836947 || s.TokenUsage.OutputTokens != 4103 || s.TokenUsage.CachedInputTokens != 1830000 {
		t.Errorf("TokenUsage = %+v, want in=1836947 out=4103 cached=1830000", s.TokenUsage)
	}
	found := false
	for _, st := range s.Steps {
		if st.Type == "error" {
			found = true
		}
	}
	if !found {
		t.Error("no error step recorded")
	}
}

func TestExtractTokenUsage_CarriesCacheWrites(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"metadata": map[string]any{
				"token_usage": map[string]any{
					"total_input_tokens":        float64(690000),
					"total_output_tokens":       float64(3800),
					"total_cached_input_tokens": float64(682000),
					"total_cache_write_tokens":  float64(84000),
					"total_reasoning_tokens":    float64(0),
				},
			},
		},
	}
	tu := ExtractTokenUsage(payload)
	if tu == nil {
		t.Fatal("ExtractTokenUsage = nil")
	}
	if tu.CacheWriteTokens != 84000 {
		t.Errorf("CacheWriteTokens = %d, want 84000", tu.CacheWriteTokens)
	}
	if tu.CachedInputTokens != 682000 {
		t.Errorf("CachedInputTokens = %d, want 682000", tu.CachedInputTokens)
	}
}

func TestFprintSummary_TokenLine(t *testing.T) {
	s := Summary{
		TaskID: "abc12345-6789-0000-0000-000000000000",
		Status: "failed",
		TokenUsage: &TokenUsage{
			InputTokens:       690000,
			OutputTokens:      3800,
			CachedInputTokens: 682000,
			CacheWriteTokens:  84000,
			ReasoningTokens:   12,
		},
	}
	var buf bytes.Buffer
	fprintSummary(&buf, s, false)
	out := buf.String()

	want := "  Tokens:   in=690000 out=3800 cached=682000 write=84000 reasoning=12"
	if !strings.Contains(out, "\n"+want+"\n") {
		t.Errorf("token line wrong.\nwant: %q\ngot:\n%s", want, out)
	}
}

func TestFprintSummary_NoUsageOmitsTokenLine(t *testing.T) {
	var buf bytes.Buffer
	fprintSummary(&buf, Summary{TaskID: "t", Status: "completed"}, false)
	if strings.Contains(buf.String(), "Tokens:") {
		t.Errorf("token line printed with no usage recorded:\n%s", buf.String())
	}
}

func TestFprintSummary_ToolCallCountsAreSorted(t *testing.T) {
	s := Summary{
		TaskID: "t", Status: "completed",
		Stats: &Stats{
			TotalLLMCalls: 4, TotalToolCalls: 10,
			SumInputTokens: 100, SumOutputTokens: 20,
			ToolCallCounts: map[string]int{"zebra": 4, "mango": 3, "alpha": 1, "beta": 2},
			AutoContinues:  2,
		},
	}
	var buf bytes.Buffer
	fprintSummary(&buf, s, false)
	out := buf.String()

	want := "  Tool Calls: 10  [alpha(1) beta(2) mango(3) zebra(4)]"
	if !strings.Contains(out, want) {
		t.Errorf("tool-call line not sorted/formatted as expected.\nwant: %q\ngot:\n%s", want, out)
	}
	if !strings.Contains(out, "  LLM Calls:  4  (in=100 out=20)") {
		t.Errorf("LLM-calls line wrong:\n%s", out)
	}
	if !strings.Contains(out, "  Auto-continues: 2") {
		t.Errorf("auto-continues line missing:\n%s", out)
	}
}

func TestFprintSummary_ReportsFailuresAndDegradation(t *testing.T) {
	s := Summary{
		TaskID: "t", Status: "completed",
		Degraded:       true,
		DegradedReason: "strict parse failed; recovered by dropping the content of 3 block scalars",
		Stats: &Stats{
			TotalToolCalls:    4,
			ToolCallCounts:    map[string]int{"web_search_google": 3, "get_time": 1},
			TotalToolFailures: 2,
			ToolFailureCounts: map[string]int{"web_search_google": 2},
		},
	}
	var buf bytes.Buffer
	fprintSummary(&buf, s, false)
	out := buf.String()

	if !strings.Contains(out, "  DEGRADED: strict parse failed") {
		t.Errorf("a degraded summary must say so up front:\n%s", out)
	}
	if want := "  Tool Failures: 2  [web_search_google(2)]"; !strings.Contains(out, want) {
		t.Errorf("failure line missing.\nwant: %q\ngot:\n%s", want, out)
	}
	if want := "  Tool Calls: 4  [get_time(1) web_search_google(3)]"; !strings.Contains(out, want) {
		t.Errorf("attempt line changed.\nwant: %q\ngot:\n%s", want, out)
	}
}

func TestFprintSummary_FinalOutputOnlyWhenVerbose(t *testing.T) {
	s := Summary{TaskID: "t", Status: "completed", FinalOutput: "the-final-answer"}

	var verbose, quiet bytes.Buffer
	fprintSummary(&verbose, s, true)
	fprintSummary(&quiet, s, false)

	if !strings.Contains(verbose.String(), "the-final-answer") {
		t.Errorf("verbose run omitted the final output:\n%s", verbose.String())
	}
	if strings.Contains(quiet.String(), "the-final-answer") {
		t.Errorf("non-verbose run leaked the final output:\n%s", quiet.String())
	}
}

func TestExtractAgentFromTopic_RealForms(t *testing.T) {
	for _, c := range []struct {
		name, topic, kind, want string
	}{
		{"request names the agent", "solace-agent-mesh/a2a/v1/agent/request/OrchestratorAgent", "request", "OrchestratorAgent"},
		{"request with a namespace containing slashes", "acme/prod/a2a/v1/agent/request/Researcher", "request", "Researcher"},
		{"status topics carry no agent", "solace-agent-mesh/a2a/v1/gateway/status/gw-1/task-9", "request", "unknown"},
		{"response is attributed generically", "solace-agent-mesh/a2a/v1/gateway/response/gw-1/task-9", "response", "agent"},
		{"empty topic", "", "request", "unknown"},
		{"truncated request topic names nothing", "solace-agent-mesh/a2a/v1/agent/request", "request", "unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractAgentFromTopic(c.topic, c.kind); got != c.want {
				t.Errorf("ExtractAgentFromTopic(%q, %q) = %q, want %q", c.topic, c.kind, got, c.want)
			}
		})
	}
}

func TestParseAndAnalyze_TokenUsageSumsAcrossDelegatedTasks(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "parent000-0000-0000-0000-000000000000"
  status: "completed"
  initial_request_text: "delegate this"
  total_tasks: 2
  start_time: 1700000000000
  end_time: 1700000009000
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/response/gw/parent000"
    direction: "response"
    task_id: "parent000-0000-0000-0000-000000000000"
    created_time: 1700000004000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "delegating"
        metadata:
          token_usage:
            total_input_tokens: 900
            total_output_tokens: 90
            total_cached_input_tokens: 400
            total_cache_write_tokens: 100
            total_reasoning_tokens: 9
  - topic: "solace-agent-mesh/v1/gateway/response/gw/child0001"
    direction: "response"
    task_id: "child0001-0000-0000-0000-000000000000"
    created_time: 1700000008000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "sub-task done"
        metadata:
          token_usage:
            total_input_tokens: 100
            total_output_tokens: 10
            total_cached_input_tokens: 40
            total_cache_write_tokens: 20
            total_reasoning_tokens: 1
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil, want the summed usage of both tasks")
	}
	want := TokenUsage{
		InputTokens:       1000,
		OutputTokens:      100,
		CachedInputTokens: 440,
		CacheWriteTokens:  120,
		ReasoningTokens:   10,
	}
	if *s.TokenUsage != want {
		t.Errorf("TokenUsage = %+v, want %+v", *s.TokenUsage, want)
	}
	if s.TokenUsage.InputTokens <= 900 {
		t.Errorf("InputTokens=%d does not exceed the largest single task's 900: "+
			"a task's usage is being dropped rather than added",
			s.TokenUsage.InputTokens)
	}
}

func TestParseAndAnalyze_TokenUsageKeepsOneTaskCumulative(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "abc12345-6789-0000-0000-000000000000"
  status: "completed"
  initial_request_text: "two turns, one task"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000009000
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "response"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000004000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "first"
        metadata:
          token_usage:
            total_input_tokens: 300
            total_output_tokens: 30
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "response"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000008000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "second"
        metadata:
          token_usage:
            total_input_tokens: 700
            total_output_tokens: 70
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil, want the task's cumulative usage")
	}
	if s.TokenUsage.InputTokens != 700 || s.TokenUsage.OutputTokens != 70 {
		t.Errorf("TokenUsage in=%d out=%d, want in=700 out=70 (the task's own "+
			"cumulative total, not the two reports added)",
			s.TokenUsage.InputTokens, s.TokenUsage.OutputTokens)
	}
}

func TestParseAndAnalyze_TokenUsageResponseThenErrorSameTaskIsNotSummed(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "abc12345-6789-0000-0000-000000000000"
  status: "failed"
  initial_request_text: "answers, then runs out of context"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000009000
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "response"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000004000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "interim answer"
        metadata:
          token_usage:
            total_input_tokens: 900000
            total_output_tokens: 2000
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "error"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000008000
    payload:
      error:
        code: -32603
        message: "Unable to complete request — conversation history too long."
        data:
          token_usage:
            total_input_tokens: 1800000
            total_output_tokens: 4000
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil, want the task's cumulative usage")
	}
	if s.TokenUsage.InputTokens != 1800000 || s.TokenUsage.OutputTokens != 4000 {
		t.Errorf("TokenUsage in=%d out=%d, want in=1800000 out=4000 (the abort's "+
			"cumulative total; adding the interim response would bill 2700000)",
			s.TokenUsage.InputTokens, s.TokenUsage.OutputTokens)
	}
}

func TestParseAndAnalyze_TokenUsagePartialLaterReport(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "abc12345-6789-0000-0000-000000000000"
  status: "completed"
  initial_request_text: "two reports, the second partial"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000009000
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "response"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000004000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "first"
        metadata:
          token_usage:
            total_input_tokens: 300
            total_output_tokens: 30
            total_cache_write_tokens: 120
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "response"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000008000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "second"
        metadata:
          token_usage:
            total_input_tokens: 700
            total_output_tokens: 70
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil")
	}
	if s.TokenUsage.InputTokens != 700 || s.TokenUsage.OutputTokens != 70 {
		t.Errorf("in=%d out=%d, want in=700 out=70",
			s.TokenUsage.InputTokens, s.TokenUsage.OutputTokens)
	}
	if s.TokenUsage.CacheWriteTokens != 120 {
		t.Errorf("CacheWriteTokens = %d, want 120 held from the first report; a "+
			"whole-report overwrite drops it to 0", s.TokenUsage.CacheWriteTokens)
	}
}

func TestParseAndAnalyze_NoUsageLeavesTokenUsageNil(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "abc12345-6789-0000-0000-000000000000"
  status: "completed"
  initial_request_text: "no usage anywhere"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000002000
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "response"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000001000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "done"
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage != nil {
		t.Errorf("TokenUsage = %+v, want nil when no event reported usage", s.TokenUsage)
	}
}

func TestParseAndAnalyze_UnkeyedUsageDoesNotShrinkAKeyedTask(t *testing.T) {
	yaml := `
invocation_details:
  task_id: "abc12345-6789-0000-0000-000000000000"
  status: "completed"
  initial_request_text: "one keyed report, one trimmed of its id"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000009000
invocation_flow:
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "response"
    task_id: "abc12345-6789-0000-0000-000000000000"
    created_time: 1700000004000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "keyed"
        metadata:
          token_usage:
            total_input_tokens: 1000
            total_output_tokens: 100
  - topic: "solace-agent-mesh/v1/gateway/response/gw/abc12345"
    direction: "response"
    created_time: 1700000008000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "no task id"
        metadata:
          token_usage:
            total_input_tokens: 500
            total_output_tokens: 50
`
	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil")
	}
	if s.TokenUsage.InputTokens != 1500 {
		t.Errorf("InputTokens = %d, want 1500; folding the unkeyed report into the "+
			"root task's bucket maxes it away and reports 1000",
			s.TokenUsage.InputTokens)
	}
}

func subTaskCarrier(eventTaskID, subTaskID string, in, out int) string {
	return fmt.Sprintf(`
  - topic: "solace-agent-mesh/a2a/v1/gateway/status/bench/x"
    direction: "status"
    task_id: %q
    created_time: 1700000002000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            kind: "message"
            parts:
              - kind: "data"
                data:
                  type: "tool_result"
                  tool_name: "sub_task"
                  function_call_id: "toolu_x"
                  result_data:
                    id: %q
                    kind: "task"
                    metadata:
                      agent_name: "GaiaAgent"
                      token_usage:
                        total_input_tokens: %d
                        total_output_tokens: %d`, eventTaskID, subTaskID, in, out)
}

func rootResponse(taskID string, in, out int) string {
	return fmt.Sprintf(`
  - topic: "solace-agent-mesh/a2a/v1/gateway/response/bench/x"
    direction: "response"
    task_id: %q
    created_time: 1700000009000
    payload:
      result:
        status:
          message:
            parts:
              - kind: "text"
                text: "done"
        metadata:
          token_usage:
            total_input_tokens: %d
            total_output_tokens: %d`, taskID, in, out)
}

const (
	probeRoot    = "11111111-1111-1111-1111-111111111111"
	probeParent  = "22222222-2222-2222-2222-222222222222"
	probeSubA    = "33333333-3333-3333-3333-333333333333"
	probeSibling = "44444444-4444-4444-4444-444444444444"
)

func probeDoc(rootTaskID, flow string) string {
	return fmt.Sprintf(`
invocation_details:
  task_id: %q
  status: "completed"
  initial_request_text: "probe"
  total_tasks: 1
  start_time: 1700000000000
  end_time: 1700000009000
invocation_flow:%s
`, rootTaskID, flow)
}

func TestAnalyzeCountsSubTaskSpendFromToolResults(t *testing.T) {
	yaml := probeDoc(probeRoot,
		subTaskCarrier(probeRoot, probeSubA, 86029, 1641)+
			subTaskCarrier(probeRoot, probeSibling, 377704, 2292)+
			rootResponse(probeRoot, 24233, 855))

	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TaskID != probeRoot {
		t.Fatalf("TaskID = %q, want the root task from invocation_details", s.TaskID)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil")
	}
	if want := 24233 + 86029 + 377704; s.TokenUsage.InputTokens != want {
		t.Errorf("InputTokens = %d, want %d (the root plus both sub-tasks)",
			s.TokenUsage.InputTokens, want)
	}
	if want := 855 + 1641 + 2292; s.TokenUsage.OutputTokens != want {
		t.Errorf("OutputTokens = %d, want %d", s.TokenUsage.OutputTokens, want)
	}
}

func TestAnalyzeIgnoresSiblingsInASubTaskScopedExport(t *testing.T) {
	yaml := probeDoc(probeSubA,
		subTaskCarrier(probeParent, probeSibling, 377704, 2292)+
			rootResponse(probeSubA, 86029, 1641))

	s, err := ParseAndAnalyze([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil")
	}
	if s.TokenUsage.InputTokens != 86029 {
		t.Errorf("InputTokens = %d, want 86029 (the exported task alone); the sibling's "+
			"377704 belongs to a task outside this export", s.TokenUsage.InputTokens)
	}
}

func TestAnalyzeIgnoresTheLLMInvocationHistoryEcho(t *testing.T) {
	echo := fmt.Sprintf(`
  - topic: "solace-agent-mesh/a2a/v1/gateway/status/bench/x"
    direction: "status"
    task_id: %q
    created_time: 1700000001000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            kind: "message"
            parts:
              - kind: "data"
                data:
                  type: "llm_invocation"
                  request:
                    contents:
                      - parts:
                          - function_response:
                              response:
                                id: %q
                                kind: "task"
                                metadata:
                                  token_usage:
                                    total_input_tokens: 377704
                                    total_output_tokens: 2292`, probeRoot, probeSibling)

	s, err := ParseAndAnalyze([]byte(probeDoc(probeRoot, echo+rootResponse(probeRoot, 5000, 100))))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil")
	}
	if s.TokenUsage.InputTokens != 5000 {
		t.Errorf("InputTokens = %d, want 5000 (this turn alone); the echoed 377704 was "+
			"spent by a prior turn's delegation", s.TokenUsage.InputTokens)
	}
}

func TestAnalyzeIgnoresResultDataOutsideAToolResult(t *testing.T) {
	carrier := fmt.Sprintf(`
  - topic: "solace-agent-mesh/a2a/v1/gateway/status/bench/x"
    direction: "status"
    task_id: %q
    created_time: 1700000001000
    payload:
      result:
        kind: "status-update"
        status:
          message:
            kind: "message"
            parts:
              - kind: "data"
                data:
                  type: "agent_progress_update"
                  result_data:
                    id: %q
                    kind: "task"
                    metadata:
                      token_usage:
                        total_input_tokens: 377704`, probeRoot, probeSibling)

	s, err := ParseAndAnalyze([]byte(probeDoc(probeRoot, carrier+rootResponse(probeRoot, 5000, 100))))
	if err != nil {
		t.Fatalf("ParseAndAnalyze: %v", err)
	}
	if s.TokenUsage == nil {
		t.Fatal("TokenUsage = nil")
	}
	if s.TokenUsage.InputTokens != 5000 {
		t.Errorf("InputTokens = %d, want 5000; only a tool_result reports a completed delegation",
			s.TokenUsage.InputTokens)
	}
}

func TestToolResultSummary_LeadsWithStatusAndMessage(t *testing.T) {
	rows := `[{"region":"north","total_orders":1204}]` + strings.Repeat(" ", 500)

	got := toolResultSummary(map[string]any{
		"status":  "error",
		"message": "Query failed: relation does not exist",
		"content": rows,
	})

	if !strings.HasPrefix(got, "error: Query failed") {
		t.Errorf("got %q, want it to lead with the status and message", Truncate(got, 80))
	}
	if len(Truncate(got, 200)) < len("error: Query failed") {
		t.Errorf("summary lost to truncation: %q", Truncate(got, 200))
	}
}

func TestToolResultSummary_FallsBackToJSON(t *testing.T) {
	got := toolResultSummary(map[string]any{"rows": 3})
	if got != `{"rows":3}` {
		t.Errorf("got %q, want the marshalled payload when there is no summary", got)
	}
	if toolResultSummary(nil) != "" {
		t.Errorf("nil payload should render empty, got %q", toolResultSummary(nil))
	}
}
