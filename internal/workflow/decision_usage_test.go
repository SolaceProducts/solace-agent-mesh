// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const decisionUsageReplyTopic = "test/reply"

func finalResponseTokenUsage(t *testing.T, brk *capturingBroker) map[string]any {
	t.Helper()
	for _, msg := range brk.snapshot() {
		if msg.Topic != decisionUsageReplyTopic {
			continue
		}
		var envelope struct {
			Result struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"result"`
		}
		if err := json.Unmarshal(msg.Payload, &envelope); err != nil {
			t.Fatalf("decode final response: %v", err)
		}
		usage, _ := envelope.Result.Metadata["token_usage"].(map[string]any)
		return usage
	}
	t.Fatalf("no final response on %s", decisionUsageReplyTopic)
	return nil
}

func newUsageDecisionRun(t *testing.T, doc string, jev *fakeJev) (*decisionRun, *capturingBroker) {
	t.Helper()
	run, brk := newPublishingDecisionRun(t, doc, jev)
	run.e.execCtx.UserProps.ReplyTo = decisionUsageReplyTopic
	return run, brk
}

func TestWorkflowReportsItsDecisionNodeTokenUsage(t *testing.T) {
	t.Run("a routed run reports the call on its final response", func(t *testing.T) {
		run, brk := newUsageDecisionRun(t, routedDecisionWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.9)})
		run.execute(t)

		usage := finalResponseTokenUsage(t, brk)
		if usage["total_input_tokens"] != float64(40) || usage["total_output_tokens"] != float64(0) {
			t.Fatalf("token_usage = %v, want the decision call's 40 input tokens", usage)
		}
		byModel, _ := usage["by_model"].(map[string]any)
		if _, ok := byModel["jev-1.13.0"]; !ok {
			t.Errorf("by_model = %v, want the served model jev-1.13.0", byModel)
		}
		bySource, _ := usage["by_source"].(map[string]any)
		if _, ok := bySource["decision"]; !ok {
			t.Errorf("by_source = %v, want a decision source", bySource)
		}
	})
	t.Run("every iteration of a map template counts", func(t *testing.T) {
		jev := &fakeJev{respond: func(map[string]any) string {
			return `{"model":"jev-1.13.0","answers":{"privileged":{"type":"noul","noul":0.1}},"usage":{"input_tokens":40,"output_tokens":0}}`
		}}
		run, brk := newUsageDecisionRun(t, decisionMapWorkflow, jev)
		run.e.state.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{
			"pages": []any{"quarterly numbers", "note from our attorney", "lunch menu"},
		}}
		run.execute(t)

		if usage := finalResponseTokenUsage(t, brk); usage["total_input_tokens"] != float64(3*40) {
			t.Fatalf("token_usage = %v, want 3 calls of 40 input tokens", usage)
		}
	})
	t.Run("every iteration of a loop body counts", func(t *testing.T) {
		doc := `
decision: {api_key: test-key, model: jev-1.13.0}
output_mapping: {r: "{{poll.output}}"}
nodes:
  - {id: poll, type: loop, node: check, condition: "{{check.output.answers.done.value}} == false", max_iterations: 5}
  - id: check
    type: decision
    input: {attempt: "{{_loop_iteration}}"}
    questions:
      done: {type: boolean, instructions: "The job has finished"}
`
		jev := &fakeJev{}
		jev.respond = func(map[string]any) string {
			p := "0.1"
			if jev.calls.Load() >= 2 {
				p = "0.9"
			}
			return `{"model":"jev-1.13.0","answers":{"done":{"type":"noul","noul":` + p + `}},"usage":{"input_tokens":40,"output_tokens":0}}`
		}
		run, brk := newUsageDecisionRun(t, doc, jev)
		run.execute(t)

		if usage := finalResponseTokenUsage(t, brk); usage["total_input_tokens"] != float64(2*40) {
			t.Fatalf("token_usage = %v, want 2 calls of 40 input tokens", usage)
		}
	})
	t.Run("a failed run still reports what it spent", func(t *testing.T) {
		doc := strings.Replace(routedDecisionWorkflow, "  - {id: billing, type: tool, tool_name: billing_tool, depends_on: [triage]}",
			"  - {id: billing, type: tool, tool_name: missing_tool, depends_on: [triage]}", 1)
		run, brk := newUsageDecisionRun(t, doc, &fakeJev{respond: departmentAnswer("billing", 0.9)})
		run.execute(t)

		if usage := finalResponseTokenUsage(t, brk); usage["total_input_tokens"] != float64(40) {
			t.Fatalf("token_usage = %v, want the decision call counted on the failure response", usage)
		}
	})
	t.Run("a provider failure spends nothing and reports nothing", func(t *testing.T) {
		run, brk := newUsageDecisionRun(t, routedDecisionWorkflow, &fakeJev{status: http.StatusServiceUnavailable})
		run.execute(t)

		if usage := finalResponseTokenUsage(t, brk); usage != nil {
			t.Fatalf("token_usage = %v, want none when no call succeeded", usage)
		}
	})
}
