// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	peerDeckEmbed       = "«artifact_return:deck.pptx»"
	workflowHandoffName = "wf_invoice-flow_output_ab12cd34.json"
)

func delegatedReply(t *testing.T, state a2a.TaskState, metadata map[string]any, artifacts []any) *broker.Message {
	t.Helper()
	task := map[string]any{
		"kind": "task", "id": "task-delegated", "contextId": "ctx-delegated",
		"status": map[string]any{
			"state": string(state),
			"message": map[string]any{"kind": "message", "messageId": "msg-delegated", "role": "agent",
				"parts": []any{map[string]any{"kind": "text", "text": "Done."}}},
		},
		"metadata": metadata,
	}
	if artifacts != nil {
		task["artifacts"] = artifacts
	}
	payload, err := json.Marshal(a2a.Response{JSONRPC: "2.0", ID: "req-delegated", Result: task})
	if err != nil {
		t.Fatalf("marshal the delegated reply: %v", err)
	}
	return &broker.Message{Payload: payload}
}

func producedDeck() map[string]any {
	return map[string]any{
		"agent_name":         "analyst",
		"produced_artifacts": []any{map[string]any{"filename": "deck.pptx", "version": 2}},
	}
}

func deckPeerResult(t *testing.T) *tool.Result {
	t.Helper()
	result, err := parsePeerResponse("analyst", delegatedReply(t, a2a.TaskStateCompleted, producedDeck(), nil))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}
	return result
}

func legacyDeckPeerResult() *tool.Result {
	return &tool.Result{Status: "success", Message: "Built the deck.", Data: map[string]any{"artifacts": []string{"deck.pptx:2"}}}
}

func registerPeerReturning(t *testing.T, agent *Agent, result func() *tool.Result) {
	t.Helper()
	err := agent.Tools().Register(&testBuiltinTool{
		name:   peerToolPrefix + "analyst",
		params: &tool.JSONSchema{Type: "object"},
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			return result(), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func workflowReply(t *testing.T, output string) *broker.Message {
	t.Helper()
	task := map[string]any{
		"kind": "task", "id": "task-wf", "contextId": "ctx-wf",
		"status": map[string]any{
			"state": string(a2a.TaskStateCompleted),
			"message": map[string]any{"kind": "message", "messageId": "msg-wf", "role": "agent", "parts": []any{
				map[string]any{"kind": "data", "data": map[string]any{
					"type": "structured_invocation_result", "status": "success",
					"output_artifact_ref": map[string]any{"name": output, "version": 0},
				}},
			}},
		},
	}
	payload, err := json.Marshal(a2a.Response{JSONRPC: "2.0", ID: "req-wf", Result: task})
	if err != nil {
		t.Fatalf("marshal the workflow reply: %v", err)
	}
	return &broker.Message{Payload: payload}
}

func proxiedAgentReply(t *testing.T, metadata map[string]any) *broker.Message {
	t.Helper()
	artifacts := []any{
		map[string]any{
			"artifactId": "art-1", "name": "Weekly Report",
			"parts": []any{map[string]any{"kind": "file", "file": map[string]any{"name": "report.md", "uri": "artifact://app/alice/s-1/report.md?version=1"}}},
		},
		map[string]any{"artifactId": "art-2", "parts": []any{}},
	}
	return delegatedReply(t, a2a.TaskStateCompleted, metadata, artifacts)
}

func TestAnswerNamingAPeerFileReturnsIt(t *testing.T) {
	tests := map[string]func(*testing.T) *tool.Result{
		"a peer that reports its file in produced_artifacts": deckPeerResult,
		"a peer that reports its file as a name:version string": func(*testing.T) *tool.Result {
			return legacyDeckPeerResult()
		},
	}
	for name, result := range tests {
		t.Run(name, func(t *testing.T) {
			agent, mock, _ := newTestAgent(
				&llm.ChatResponse{
					FinishReason: "tool_calls",
					ToolCalls:    []llm.ToolCall{{ID: "call-peer", Name: peerToolPrefix + "analyst", Arguments: `{}`}},
				},
				&llm.ChatResponse{Content: "The analyst built the deck, see deck.pptx.", FinishReason: "stop"},
			)
			registerPeerReturning(t, agent, func() *tool.Result { return result(t) })

			resp, err := agent.HandleTask(t.Context(), "task-peer-file", newUserMessage("Build the deck"),
				a2a.UserProperties{ClientID: "user-1", UserID: "user-1"})
			if err != nil {
				t.Fatal(err)
			}
			if calls := mock.getCalls(); len(calls) != 2 {
				t.Fatalf("expected 2 LLM calls, got %d", len(calls))
			}
			if !strings.Contains(peerResultSent(t, mock), "deck.pptx") {
				t.Fatal("fixture drift: the peer's tool result no longer carries its file")
			}
			body, _ := json.Marshal(resp)
			if !strings.Contains(string(body), peerDeckEmbed) {
				t.Errorf("the answer names deck.pptx but does not return it: %s", body)
			}
		})
	}
}

func peerResultSent(t *testing.T, mock *mockLLM) string {
	t.Helper()
	for _, m := range mock.getCalls()[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "call-peer" {
			return m.Content
		}
	}
	t.Fatal("the second LLM call carries no result for the peer call")
	return ""
}

func TestAnswerNamingAPeerFileReturnsItAfterResume(t *testing.T) {
	agent, mock, store := newTestAgent(&llm.ChatResponse{Content: "The analyst built the deck, see deck.pptx.", FinishReason: "stop"})
	agent.runCtx = t.Context()
	if err := store.Create(t.Context(), &session.Session{
		AppName: agent.cfg.Name, UserID: "u-resume", SessionID: "s-resume", ContextID: "s-resume",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.TaskID = "task-resume-peer-file"
	state.EffectiveSessionID = "s-resume"
	state.OriginalSessionID = "s-resume"
	state.UserProps.UserID = "u-resume"
	state.PausedAssistantMsg = assistantMsgWithOneToolCall("call-peer", peerToolPrefix+"analyst", `{}`)
	state.SyncResults[0] = deckPeerResult(t)
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "Build the deck"}, state.PausedAssistantMsg}
	agent.pausedTasks.Store(state)

	agent.resumeTaskExecution(state)

	if calls := mock.getCalls(); len(calls) != 1 {
		t.Fatalf("expected 1 LLM call after the resume, got %d", len(calls))
	}
	resp := <-state.DoneCh
	body, _ := json.Marshal(resp)
	if !strings.Contains(string(body), peerDeckEmbed) {
		t.Errorf("the answer names deck.pptx but does not return it: %s", body)
	}
}

func TestAnswerNamingADelegatedFileReturnsTheFileNotTheLabel(t *testing.T) {
	proxiedManifest := map[string]any{
		"produced_artifacts": []any{map[string]any{"filename": "report.md", "version": 1}},
	}
	tests := map[string]struct {
		delegation  string
		toolName    string
		state       a2a.TaskState
		metadata    map[string]any
		proxied     bool
		output      string
		answer      string
		wantFiles   []string
		wantReturns []string
	}{
		"a peer's completed task": {
			delegation: "peer", toolName: peerToolPrefix + "analyst", state: a2a.TaskStateCompleted, metadata: producedDeck(),
			answer: "The analyst built deck.pptx for you.", wantFiles: []string{"deck.pptx"}, wantReturns: []string{"deck.pptx"},
		},
		"a peer's failed task still returns the file it saved": {
			delegation: "peer", toolName: peerToolPrefix + "analyst", state: a2a.TaskStateFailed, metadata: producedDeck(),
			answer: "The analyst saved deck.pptx before it failed.", wantFiles: []string{"deck.pptx"}, wantReturns: []string{"deck.pptx"},
		},
		"a sub-task": {
			delegation: "sub-task", toolName: (&subTaskTool{}).Name(), state: a2a.TaskStateCompleted, metadata: producedDeck(),
			answer: "The sub-task wrote deck.pptx.", wantFiles: []string{"deck.pptx"}, wantReturns: []string{"deck.pptx"},
		},
		"a proxied agent's file is returned and its artifact label is not": {
			delegation: "peer", toolName: peerToolPrefix + "external", state: a2a.TaskStateCompleted, metadata: proxiedManifest, proxied: true,
			answer: "The Weekly Report is ready as report.md.", wantFiles: []string{"report.md"}, wantReturns: []string{"report.md"},
		},
		"a proxied agent's artifacts without a manifest return nothing": {
			delegation: "peer", toolName: peerToolPrefix + "external", state: a2a.TaskStateCompleted, metadata: map[string]any{}, proxied: true,
			answer: "The Weekly Report is ready as report.md.", wantFiles: nil, wantReturns: nil,
		},
		"a workflow's hand-off output is not returned": {
			delegation: "workflow", toolName: a2a.WorkflowToolName("invoice-flow"), output: workflowHandoffName,
			answer: "The summary is in " + workflowHandoffName + ".", wantFiles: nil, wantReturns: nil,
		},
		"a workflow's reply adopted through the generic peer parser returns no hand-off output": {
			delegation: "adopted-workflow", toolName: a2a.WorkflowToolName("invoice-flow"), output: workflowHandoffName,
			answer: "The summary is in " + workflowHandoffName + ".", wantFiles: nil, wantReturns: nil,
		},
		"a file the answer does not name": {
			delegation: "peer", toolName: peerToolPrefix + "analyst", state: a2a.TaskStateCompleted, metadata: producedDeck(),
			answer: "The analyst finished.", wantFiles: []string{"deck.pptx"}, wantReturns: nil,
		},
		"a peer that produced nothing": {
			delegation: "peer", toolName: peerToolPrefix + "analyst", state: a2a.TaskStateCompleted, metadata: map[string]any{"agent_name": "analyst"},
			answer: "The analyst built deck.pptx.", wantFiles: nil, wantReturns: nil,
		},
		"a tool that is not a delegation": {
			delegation: "peer", toolName: "lookup_customer", state: a2a.TaskStateCompleted, metadata: producedDeck(),
			answer: "The lookup built deck.pptx.", wantFiles: nil, wantReturns: nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var result *tool.Result
			var err error
			switch tc.delegation {
			case "peer":
				reply := delegatedReply(t, tc.state, tc.metadata, nil)
				if tc.proxied {
					reply = proxiedAgentReply(t, tc.metadata)
				}
				result, err = parsePeerResponse("analyst", reply)
			case "sub-task":
				result, err = (&subTaskTool{}).parseResponse(delegatedReply(t, tc.state, tc.metadata, nil))
			case "workflow":
				result, err = (&workflowAgentTool{peerName: "invoice-flow"}).parseResponse(t.Context(), workflowReply(t, tc.output), &tool.Context{})
			case "adopted-workflow":
				result, err = parsePeerResponse("invoice-flow", workflowReply(t, tc.output))
			}
			if err != nil {
				t.Fatalf("parse the %s's reply: %v", tc.delegation, err)
			}
			agent, _, _ := newTestAgent()
			state := newTestTaskState(agent)
			msg := agent.buildToolResultMessage(t.Context(), result, nil, nil, llm.ToolCall{ID: "call-1", Name: tc.toolName})

			state.recordReportedFiles(msg)

			if !slices.Equal(state.ReportedFiles, tc.wantFiles) {
				t.Fatalf("ReportedFiles = %v, want %v, from the tool result the model sees: %s", state.ReportedFiles, tc.wantFiles, msg.Content)
			}
			var wantReturns string
			for _, name := range tc.wantReturns {
				wantReturns += "\n\n«artifact_return:" + name + "»"
			}
			if got := namedFileReturns(tc.answer, producedFiles(state)); got != wantReturns {
				t.Errorf("namedFileReturns(%q) = %q, want %q", tc.answer, got, wantReturns)
			}
		})
	}
}

func TestReportedFiles_AreReadFromWhateverFieldsAreWellTyped(t *testing.T) {
	tests := map[string]struct {
		content string
		want    []string
	}{
		"a mistyped artifacts field does not drop the produced files": {
			content: `{"artifacts":"oops","produced_artifacts":[{"filename":"a.txt"}]}`,
			want:    []string{"a.txt"},
		},
		"a mistyped produced_artifacts field does not drop the legacy list": {
			content: `{"artifacts":["old.txt:1"],"produced_artifacts":{"filename":"a.txt"}}`,
			want:    []string{"old.txt"},
		},
		"an entry that is not an object is skipped and the others kept": {
			content: `{"produced_artifacts":[7,{"filename":"a.txt"},{"filename":3},{"filename":"b.txt"}]}`,
			want:    []string{"a.txt", "b.txt"},
		},
		"a file listed twice is recorded once": {
			content: `{"produced_artifacts":[{"filename":"a.txt"},{"filename":"a.txt"}],"artifacts":["a.txt:1"]}`,
			want:    []string{"a.txt"},
		},
		"the legacy list and the produced files are both read": {
			content: `{"artifacts":["old.txt:3"],"produced_artifacts":[{"filename":"a.txt"}]}`,
			want:    []string{"old.txt", "a.txt"},
		},
		"a workflow's output artifact is not read": {
			content: `{"status":"success","output_artifact":"out.json"}`,
			want:    nil,
		},
		"a workflow's reply parts are not read": {
			content: `{"reply_parts":[{"kind":"data","data":{"type":"structured_invocation_result","output_artifact_ref":{"name":"out.json"}}}]}`,
			want:    nil,
		},
		"a result that is not an object lists nothing": {
			content: `"done"`,
			want:    nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			state := newTestTaskState(agent)

			state.recordReportedFiles(llm.ChatMessage{Role: "tool", ToolName: peerToolPrefix + "analyst", Content: tc.content})

			if !slices.Equal(state.ReportedFiles, tc.want) {
				t.Fatalf("ReportedFiles = %v, want %v", state.ReportedFiles, tc.want)
			}
		})
	}
}
