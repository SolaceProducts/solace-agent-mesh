// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package stim

import (
	"strings"
	"testing"
)

const malformedTrace = `invocation_details:
    status: completed
    task_id: "task1234"
    total_tasks: 1
    start_time: 1700000000000
    end_time: 1700000003000
invocation_flow:
    - created_time: 1700000001000
      direction: status
      payload:
        result:
            kind: status-update
            status:
                message:
                    parts:
                        - kind: text
                          text: |4-
                                indented first line
                            second line
      task_id: "task1234"
      topic: solace-agent-mesh/v1/gateway/status/task1234
    - created_time: 1700000003000
      direction: response
      payload:
        result:
            status:
                message:
                    parts:
                        - kind: text
                          text: The answer is 42
      task_id: "task1234"
      topic: solace-agent-mesh/v1/gateway/response/task1234
`

func TestParseAndAnalyze_UnparseableTraceIsLoud(t *testing.T) {
	s, err := ParseAndAnalyze([]byte(malformedTrace))
	if err == nil {
		t.Fatalf("want an error for a malformed trace, got summary with tokens=%v output=%q", s.TokenUsage, s.FinalOutput)
	}
	if s != nil {
		t.Errorf("want no Summary alongside the error, got %#v", s)
	}
	if !strings.Contains(err.Error(), "parsing STIM YAML") {
		t.Errorf("error should attribute the failure to STIM parsing: %v", err)
	}

	salvaged, salvageErr := ParseAndAnalyzeSalvaged([]byte(malformedTrace))
	if salvageErr != nil {
		t.Fatalf("ParseAndAnalyzeSalvaged: %v", salvageErr)
	}
	if salvaged.TokenUsage != nil || len(salvaged.LLMCalls) != 0 {
		t.Fatalf("fixture no longer demonstrates the zero-token record: usage=%#v llmCalls=%d", salvaged.TokenUsage, len(salvaged.LLMCalls))
	}
	if !salvaged.Degraded {
		t.Error("the only summary this trace can produce must be marked Degraded")
	}
}

func TestParseAndAnalyzeSalvaged_FlagsDegradation(t *testing.T) {
	s, err := ParseAndAnalyzeSalvaged([]byte(malformedTrace))
	if err != nil {
		t.Fatalf("salvage should recover the event skeleton: %v", err)
	}
	if !s.Degraded {
		t.Error("salvaged summary must be marked Degraded")
	}
	if !strings.Contains(s.DegradedReason, "block scalars") {
		t.Errorf("DegradedReason should name what was dropped: %q", s.DegradedReason)
	}
	if s.TaskID != "task1234" {
		t.Errorf("TaskID = %q, want task1234", s.TaskID)
	}
	if s.FinalOutput != "The answer is 42" {
		t.Errorf("FinalOutput = %q, want the response text that did parse", s.FinalOutput)
	}
}

func TestParseAndAnalyzeSalvaged_LeavesGoodTracesAlone(t *testing.T) {
	wellFormed := `invocation_details:
    status: completed
    task_id: "task1234"
invocation_flow:
    - created_time: 1700000003000
      direction: response
      task_id: "task1234"
      topic: solace-agent-mesh/v1/gateway/response/task1234
      payload:
        result:
            status:
                message:
                    parts:
                        - kind: text
                          text: "The answer is 42"
`
	s, err := ParseAndAnalyzeSalvaged([]byte(wellFormed))
	if err != nil {
		t.Fatalf("ParseAndAnalyzeSalvaged: %v", err)
	}
	if s.Degraded {
		t.Errorf("a parseable trace must not be marked degraded: %q", s.DegradedReason)
	}
}

func TestMarshalledTraceParsesBack(t *testing.T) {
	doc := map[string]any{
		"invocation_details": map[string]any{"task_id": "task1234", "status": "completed", "total_tasks": 1},
		"invocation_flow": []map[string]any{{
			"topic":        "solace-agent-mesh/v1/gateway/response/task1234",
			"direction":    "response",
			"task_id":      "task1234",
			"created_time": 1700000003000,
			"payload": map[string]any{"result": map[string]any{"status": map[string]any{"message": map[string]any{
				"parts": []any{map[string]any{"kind": "text", "text": "    indented first line\nsecond line"}},
			}}}},
		}},
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s, err := ParseAndAnalyze(data)
	if err != nil {
		t.Fatalf("a trace written by Marshal must parse strictly: %v\n%s", err, data)
	}
	if s.FinalOutput != "    indented first line\nsecond line" {
		t.Errorf("FinalOutput = %q, want the text as written", s.FinalOutput)
	}
}

func TestParseAndAnalyze_RejectsDocumentsThatAreNotTraces(t *testing.T) {
	for _, c := range []struct{ name, doc string }{
		{"empty", ""},
		{"whitespace only", "   \n\t\n  \n"},
		{"comment only", "# nothing here\n"},
		{"valid yaml, wrong shape", "some_other_key: 1\n"},
		{"truncated before invocation_flow", "invocation_details:\n  task_id: t-1\n  status: completed\n"},
		{"truncated before invocation_details", "invocation_flow:\n  - a: 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := ParseAndAnalyze([]byte(c.doc))
			if err == nil {
				t.Fatalf("no error; got Summary %+v — a zero summary reads as a task that did nothing", s)
			}
			if s != nil {
				t.Errorf("Summary returned alongside the error: %+v", s)
			}
		})
	}
}

func TestParseAndAnalyze_AcceptsMinimalTrace(t *testing.T) {
	doc := "invocation_details:\n  task_id: t-1\ninvocation_flow:\n  - direction: request\n"
	s, err := ParseAndAnalyze([]byte(doc))
	if err != nil {
		t.Fatalf("rejected a minimal but valid trace: %v", err)
	}
	if s.TaskID != "t-1" {
		t.Errorf("TaskID = %q, want t-1", s.TaskID)
	}
}
