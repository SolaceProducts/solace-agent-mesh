// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const truncatedReportArgs = `{"leaflets":["lidl_leaflet.json","aldi_`

func registerCountingTool(t *testing.T, agent *Agent, name string) *atomic.Int32 {
	t.Helper()
	var dispatched atomic.Int32
	err := agent.Tools().Register(&testBuiltinTool{
		name:        name,
		description: "counts its dispatches",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			dispatched.Add(1)
			return tool.OK("done"), nil
		},
	})
	if err != nil {
		t.Fatalf("register %q: %v", name, err)
	}
	return &dispatched
}

var testUser = a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}

func runTask(t *testing.T, agent *Agent) {
	t.Helper()
	runTaskAs(t, agent, testUser)
}

func runTaskAs(t *testing.T, agent *Agent, user a2a.UserProperties) {
	t.Helper()
	resp, err := agent.HandleTask(t.Context(), "task-001", newUserMessage("Build the report"), user)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func toolResultContent(msgs []llm.ChatMessage, callID string) string {
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == callID {
			return m.Content
		}
	}
	return ""
}

func historyToolCall(msgs []llm.ChatMessage, callID string) (llm.ToolCall, bool) {
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.ID == callID {
				return tc, true
			}
		}
	}
	return llm.ToolCall{}, false
}

func TestHandleTaskTruncatedToolArgsAreNotDispatched(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "length",
			ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "build_report", Arguments: truncatedReportArgs}},
		},
		&llm.ChatResponse{Content: "I'll split the report into smaller calls.", FinishReason: "stop"},
	)
	dispatched := registerCountingTool(t, agent, "build_report")

	runTask(t, agent)

	if got := dispatched.Load(); got != 0 {
		t.Errorf("tool dispatched %d times, want 0 for cut-off arguments", got)
	}
	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls (rejected call, then retry), got %d", len(calls))
	}
	retry := calls[1].Messages
	if result := toolResultContent(retry, "call-1"); !strings.Contains(result, "output token limit") {
		t.Errorf("corrective result should name the output token limit, got %q", result)
	}
	recorded, ok := historyToolCall(retry, "call-1")
	if !ok {
		t.Fatal("rejected call missing from history")
	}
	if recorded.Arguments != "{}" {
		t.Errorf("history arguments = %q, want {}", recorded.Arguments)
	}
	assertPairedToolHistory(t, retry)
}

func TestHandleTaskTruncatedToolArgsAlongsideValidCall(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "length",
			ToolCalls: []llm.ToolCall{
				{ID: "call-ok", Name: "save_offers", Arguments: `{"retailer":"lidl"}`},
				{ID: "call-cut", Name: "build_report", Arguments: truncatedReportArgs},
			},
		},
		&llm.ChatResponse{Content: "Saved Lidl, retrying the report.", FinishReason: "stop"},
	)
	saved := registerCountingTool(t, agent, "save_offers")
	reported := registerCountingTool(t, agent, "build_report")

	runTask(t, agent)

	if got := saved.Load(); got != 1 {
		t.Errorf("valid call dispatched %d times, want 1", got)
	}
	if got := reported.Load(); got != 0 {
		t.Errorf("cut-off call dispatched %d times, want 0", got)
	}
	retry := mock.getCalls()[1].Messages
	if result := toolResultContent(retry, "call-ok"); !strings.Contains(result, `"message":"done"`) {
		t.Errorf("valid call result = %q, want its tool output", result)
	}
	if result := toolResultContent(retry, "call-cut"); !strings.Contains(result, "output token limit") {
		t.Errorf("cut-off call should get the corrective result, got %q", result)
	}
	if recorded, _ := historyToolCall(retry, "call-cut"); recorded.Arguments != "{}" {
		t.Errorf("cut-off call history arguments = %q, want {}", recorded.Arguments)
	}
	if recorded, _ := historyToolCall(retry, "call-ok"); recorded.Arguments != `{"retailer":"lidl"}` {
		t.Errorf("valid call history arguments = %q, want them unchanged", recorded.Arguments)
	}
	assertPairedToolHistory(t, retry)
}

func TestHandleTaskTruncatedToolArgsAlongsideContinuationCall(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "length",
			ToolCalls: []llm.ToolCall{
				{ID: "call-cut", Name: "build_report", Arguments: truncatedReportArgs},
				{ID: "cg-1", Name: "_continue_generation", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{Content: "Retrying the report in parts.", FinishReason: "stop"},
	)
	dispatched := registerCountingTool(t, agent, "build_report")

	runTask(t, agent)

	if got := dispatched.Load(); got != 0 {
		t.Errorf("cut-off call dispatched %d times, want 0", got)
	}
	retry := mock.getCalls()[1].Messages
	if result := toolResultContent(retry, "call-cut"); !strings.Contains(result, "output token limit") {
		t.Errorf("cut-off call should keep its corrective result next to a continuation call, got %q", result)
	}
	assertPairedToolHistory(t, retry)
}

func TestHandleTaskTruncatedToolArgsAlongsideFencedArtifact(t *testing.T) {
	artifactContent := BlockPrefixSaveArtifact + " filename=\"lidl.md\" description=\"Lidl offers\"\nMelitta 4.99\n" + BlockDelimiterClose + "\nSaved Lidl, building the report."
	agent, mock, store := newTestAgent(
		&llm.ChatResponse{
			Content:      artifactContent,
			FinishReason: "length",
			ToolCalls:    []llm.ToolCall{{ID: "call-cut", Name: "build_report", Arguments: truncatedReportArgs}},
		},
		&llm.ChatResponse{Content: "Retrying the report in parts.", FinishReason: "stop"},
	)
	svc := newMockServices(agent.svc.LLM(""), store)
	svc.brk = newMockBroker()
	svc.arts = &artifact.ScopedService{
		Inner:     memoryArtifactStore(),
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	agent.svc = svc
	dispatched := registerCountingTool(t, agent, "build_report")

	withStatusTopic := testUser
	withStatusTopic.A2AStatusTopic = "test-ns/a2a/v1/agent/status/test-agent"
	runTaskAs(t, agent, withStatusTopic)

	if got := dispatched.Load(); got != 0 {
		t.Errorf("cut-off call dispatched %d times, want 0", got)
	}
	retry := mock.getCalls()[1].Messages
	var sawNotify bool
	for _, m := range retry {
		if m.Role == "tool" && m.ToolName == "_notify_artifact_save" {
			sawNotify = true
		}
	}
	if !sawNotify {
		t.Error("artifact notification should still be injected")
	}
	if result := toolResultContent(retry, "call-cut"); !strings.Contains(result, "output token limit") {
		t.Errorf("cut-off call should get its corrective result alongside the artifact notification, got %q", result)
	}
	assertPairedToolHistory(t, retry)
}

func TestHandleTaskMalformedToolArgsWithoutTruncation(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "build_report", Arguments: `["not","an","object"]`}},
		},
		&llm.ChatResponse{Content: "Retrying with an object.", FinishReason: "stop"},
	)
	dispatched := registerCountingTool(t, agent, "build_report")

	runTask(t, agent)

	if got := dispatched.Load(); got != 0 {
		t.Errorf("tool dispatched %d times, want 0 for non-object arguments", got)
	}
	result := toolResultContent(mock.getCalls()[1].Messages, "call-1")
	if !strings.Contains(result, "not a valid JSON object") || strings.Contains(result, "output token limit") {
		t.Errorf("result should report malformed arguments without blaming the token limit, got %q", result)
	}
}

func TestHandleTaskNoArgumentToolCallsStillDispatch(t *testing.T) {
	for _, args := range []string{"", "null"} {
		t.Run(args, func(t *testing.T) {
			agent, _, _ := newTestAgent(
				&llm.ChatResponse{
					FinishReason: "tool_calls",
					ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "list_offers", Arguments: args}},
				},
				&llm.ChatResponse{Content: "No offers yet.", FinishReason: "stop"},
			)
			dispatched := registerCountingTool(t, agent, "list_offers")

			runTask(t, agent)

			if got := dispatched.Load(); got != 1 {
				t.Errorf("no-argument call dispatched %d times, want 1", got)
			}
		})
	}
}

func TestRejectMalformedToolArgsNamesTheOutputLimit(t *testing.T) {
	cases := []struct {
		finishReason  string
		wantTruncated bool
	}{
		{"length", true},
		{"max_tokens", true},
		{"model_context_window_exceeded", true},
		{"tool_calls", false},
		{"stop", false},
	}
	for _, tc := range cases {
		t.Run(tc.finishReason, func(t *testing.T) {
			calls := []llm.ToolCall{{ID: "call-1", Name: "build_report", Arguments: truncatedReportArgs}}
			valid, errs := rejectMalformedToolArgs(calls, tc.finishReason, testLogger())
			if len(valid) != 0 || len(errs) != 1 {
				t.Fatalf("got %d valid and %d errors, want the call rejected", len(valid), len(errs))
			}
			if got := strings.Contains(errs[0].Content, "output token limit"); got != tc.wantTruncated {
				t.Errorf("error names the output token limit = %v, want %v: %q", got, tc.wantTruncated, errs[0].Content)
			}
		})
	}
}

func TestClearMalformedToolArgsKeepsValidCallSharingAnID(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "dup", Name: "save_offers", Arguments: `{"retailer":"lidl"}`},
		{ID: "dup", Name: "build_report", Arguments: truncatedReportArgs},
	}

	cleared := clearMalformedToolArgs(calls)

	if cleared[0].Arguments != `{"retailer":"lidl"}` {
		t.Errorf("valid call arguments = %q, want them unchanged", cleared[0].Arguments)
	}
	if cleared[1].Arguments != "{}" {
		t.Errorf("cut-off call arguments = %q, want {}", cleared[1].Arguments)
	}
	if calls[1].Arguments != truncatedReportArgs {
		t.Error("clearMalformedToolArgs must not modify the caller's slice")
	}
}
