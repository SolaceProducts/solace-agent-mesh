// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestBuildStructuredResponse_EmitsTokenUsage(t *testing.T) {
	a, _, _ := newTestAgent()
	a.cfg.Name = "TestAgent"

	msg := a2a.Message{ContextID: "ctx-1"}
	result := &a2a.StructuredInvocationResult{
		Type:   "structured_invocation_result",
		Status: "success",
		OutputArtifactRef: &a2a.ArtifactRef{
			Name:    "wf_test_output_abc123.json",
			Version: 0,
		},
		RetryCount: 0,
	}
	usage := llm.Usage{
		PromptTokens:     1500,
		CompletionTokens: 250,
		CachedTokens:     400,
		TotalTokens:      1750,
	}

	resp, err := a.buildStructuredResponse("task-1", msg, result, taskUsageSnapshot{usage: usage}, nil)
	if err != nil {
		t.Fatalf("buildStructuredResponse: %v", err)
	}

	taskAny, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("response.Result is not a2a.Task: %T", resp.Result)
	}

	tu, ok := taskAny.Metadata["token_usage"].(map[string]any)
	if !ok {
		t.Fatalf("metadata.token_usage missing or wrong type — gateway tasklogger will store NULL tokens. Got metadata: %+v", taskAny.Metadata)
	}

	cases := map[string]int{
		"total_input_tokens":        1500,
		"total_output_tokens":       250,
		"total_cached_input_tokens": 400,
		"total_tokens":              1750,
	}
	for key, want := range cases {
		got, okInt := tu[key].(int)
		if !okInt {
			t.Errorf("token_usage[%q] missing or wrong type: %v (type %T)", key, tu[key], tu[key])
			continue
		}
		if got != want {
			t.Errorf("token_usage[%q] = %d, want %d", key, got, want)
		}
	}

	bySource, ok := tu["by_source"].(map[string]any)
	if !ok {
		t.Fatalf("token_usage.by_source missing")
	}
	agentSrc, ok := bySource["agent"].(map[string]any)
	if !ok {
		t.Fatalf("token_usage.by_source.agent missing")
	}
	if got, _ := agentSrc["input_tokens"].(int); got != 1500 {
		t.Errorf("by_source.agent.input_tokens = %d, want 1500", got)
	}

	if name, _ := taskAny.Metadata["agent_name"].(string); name != "TestAgent" {
		t.Errorf("metadata.agent_name = %q, want TestAgent (must still be set)", name)
	}
}

func TestBuildStructuredResponse_NoUsageOmitsTokenUsage(t *testing.T) {
	a, _, _ := newTestAgent()
	a.cfg.Name = "TestAgent"

	msg := a2a.Message{ContextID: "ctx-1"}
	result := &a2a.StructuredInvocationResult{Type: "structured_invocation_result", Status: "success"}

	resp, err := a.buildStructuredResponse("task-1", msg, result, taskUsageSnapshot{}, nil)
	if err != nil {
		t.Fatalf("buildStructuredResponse: %v", err)
	}

	taskAny, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("response.Result is not a2a.Task: %T", resp.Result)
	}
	if _, present := taskAny.Metadata["token_usage"]; present {
		t.Errorf("metadata.token_usage SHOULD be absent when usage is empty — gateway interprets present-but-zero as confirmed-zero. Got: %+v", taskAny.Metadata)
	}
}

func TestBuildStructuredResponse_MarshalsAsValidJSON(t *testing.T) {
	a, _, _ := newTestAgent()
	a.cfg.Name = "TestAgent"

	usage := llm.Usage{PromptTokens: 100, CompletionTokens: 20}
	resp, err := a.buildStructuredResponse("task-1", a2a.Message{ContextID: "c"},
		&a2a.StructuredInvocationResult{Type: "structured_invocation_result", Status: "success"}, taskUsageSnapshot{usage: usage}, nil)
	if err != nil {
		t.Fatalf("buildStructuredResponse: %v", err)
	}

	bytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var roundTrip map[string]any
	if err := json.Unmarshal(bytes, &roundTrip); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	resultMap := roundTrip["result"].(map[string]any)
	metaMap := resultMap["metadata"].(map[string]any)
	tu, ok := metaMap["token_usage"].(map[string]any)
	if !ok {
		t.Fatalf("token_usage missing after round-trip — gateway tasklogger would see no tokens")
	}
	if tu["total_input_tokens"].(float64) != 100 {
		t.Errorf("round-trip total_input_tokens = %v, want 100", tu["total_input_tokens"])
	}
}
