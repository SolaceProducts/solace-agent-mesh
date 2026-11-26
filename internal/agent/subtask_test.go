// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestSubTaskToolRegistered(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	st := agent.Tools().GetTool("sub_task")
	if st == nil {
		t.Fatal("sub_task tool not registered")
	}
	if st.Name() != "sub_task" {
		t.Errorf("name = %q", st.Name())
	}
	if !st.IsLongRunning() {
		t.Error("expected IsLongRunning = true")
	}
}

func TestSubTaskToolParameters(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	st := agent.Tools().GetTool("sub_task")
	params := st.Parameters()
	if params == nil {
		t.Fatal("expected parameters")
	}
	if _, ok := params.Properties["instruction"]; !ok {
		t.Error("missing instruction parameter")
	}
	if _, ok := params.Properties["type"]; !ok {
		t.Error("missing type parameter")
	}
	if _, ok := params.Properties["skills"]; !ok {
		t.Error("missing skills parameter")
	}
	if len(params.Required) != 2 {
		t.Errorf("expected 2 required params, got %d", len(params.Required))
	}
}

func TestSubTaskInvalidType(t *testing.T) {
	agent, _, _ := newTestAgent()
	st := &subTaskTool{agent: agent}
	toolCtx := &tool.Context{
		UserID:    "user-1",
		SessionID: "parent-session",
	}

	result, err := st.Execute(context.Background(), map[string]any{
		"instruction": "Do something",
		"type":        "invalid",
	}, toolCtx)

	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "must be 'fork' or 'fresh'") {
		t.Errorf("unexpected error message: %q", result.Message)
	}
}

func TestSubTaskMissingInstruction(t *testing.T) {
	agent, _, _ := newTestAgent()
	st := &subTaskTool{agent: agent}
	toolCtx := &tool.Context{
		UserID:    "user-1",
		SessionID: "parent-session",
	}

	result, err := st.Execute(context.Background(), map[string]any{
		"type": "fresh",
	}, toolCtx)

	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
}

func TestSubTaskParseResponseWithArtifacts(t *testing.T) {
	st := &subTaskTool{agent: nil}

	taskResult := a2a.Task{
		ID:   "subtask-123",
		Kind: "task",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					a2a.NewTextPart("Here is the result"),
				},
			},
		},
		Artifacts: []a2a.Artifact{
			{
				ArtifactID: "art-1",
				Name:       "output.txt",
			},
		},
	}

	resp := &a2a.Response{
		JSONRPC: "2.0",
		ID:      "subtask-123",
		Result:  taskResult,
	}
	respBytes, _ := json.Marshal(resp)

	result, err := st.parseResponse(&broker.Message{Payload: respBytes})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q", result.Status)
	}
	if !strings.Contains(result.Message, "Here is the result") {
		t.Errorf("message = %q", result.Message)
	}
	if result.Data == nil {
		t.Fatal("expected data")
	}
	if _, ok := result.Data["artifacts"]; !ok {
		t.Error("expected artifacts in data")
	}
}

func TestSubTaskParseResponseError(t *testing.T) {
	st := &subTaskTool{agent: nil}

	resp := &a2a.Response{
		JSONRPC: "2.0",
		ID:      "subtask-123",
		Error:   &a2a.Error{Code: -32600, Message: "depth exceeded"},
	}
	respBytes, _ := json.Marshal(resp)

	result, err := st.parseResponse(&broker.Message{Payload: respBytes})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "depth exceeded") {
		t.Errorf("expected error message in result, got %q", result.Message)
	}
}

func TestSubTaskParseResponseNil(t *testing.T) {
	st := &subTaskTool{agent: nil}

	result, err := st.parseResponse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
}

func TestSubTaskPassesParentSessionID(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

	st := &subTaskTool{agent: agent}
	toolCtx := &tool.Context{
		UserID:    "user-1",
		SessionID: "parent-session-abc",
		AgentName: "test-agent",
	}

	_, err := st.Execute(context.Background(), map[string]any{
		"instruction": "Check artifacts",
		"type":        "fresh",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	_ = mock

	brk.mu.Lock()
	published := brk.published
	brk.mu.Unlock()

	if len(published) == 0 {
		t.Fatal("expected at least 1 broker publish for sub-task")
	}

	var a2aReq struct {
		Params struct {
			Message struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(published[0].payload, &a2aReq); err != nil {
		t.Fatalf("unmarshal published message: %v", err)
	}

	parentSID, ok := a2aReq.Params.Message.Metadata["parentSessionID"].(string)
	if !ok || parentSID != "parent-session-abc" {
		t.Errorf("expected parentSessionID=parent-session-abc, got %v", a2aReq.Params.Message.Metadata["parentSessionID"])
	}
}

func TestSubTaskPromptIncludesDepth(t *testing.T) {
	cases := []struct {
		name         string
		callDepth    int
		maxCallDepth int
		wantDepth    string
		wantMax      bool
		maxStr       string
	}{
		{name: "first_level", callDepth: 0, maxCallDepth: 6, wantDepth: "sub-task depth 1", wantMax: true, maxStr: "maximum of 6"},
		{name: "deep", callDepth: 4, maxCallDepth: 6, wantDepth: "sub-task depth 5", wantMax: true, maxStr: "maximum of 6"},
		{name: "unlimited", callDepth: 2, maxCallDepth: 0, wantDepth: "sub-task depth 3", wantMax: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			agent.cfg.MaxCallDepth = tc.maxCallDepth

			brk := newMockBroker()
			agent.svc.(*mockServices).brk = brk
			agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

			st := &subTaskTool{agent: agent}
			toolCtx := &tool.Context{
				UserID:    "user-1",
				SessionID: "parent-session",
				AgentName: "test-agent",
				CallDepth: tc.callDepth,
			}

			if _, err := st.Execute(context.Background(), map[string]any{
				"instruction": "do the thing",
				"type":        "fresh",
			}, toolCtx); err != nil {
				t.Fatal(err)
			}

			brk.mu.Lock()
			published := brk.published
			brk.mu.Unlock()
			if len(published) == 0 {
				t.Fatal("expected a broker publish for sub-task")
			}

			var a2aReq struct {
				Params struct {
					Message struct {
						Parts []struct {
							Kind string `json:"kind"`
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"message"`
				} `json:"params"`
			}
			if err := json.Unmarshal(published[0].payload, &a2aReq); err != nil {
				t.Fatalf("unmarshal published message: %v", err)
			}

			var text string
			for _, p := range a2aReq.Params.Message.Parts {
				if p.Kind == "text" {
					text += p.Text
				}
			}

			if !strings.Contains(text, tc.wantDepth) {
				t.Errorf("sub-task prompt missing %q; got:\n%s", tc.wantDepth, text)
			}
			if tc.wantMax {
				if !strings.Contains(text, tc.maxStr) {
					t.Errorf("sub-task prompt missing %q; got:\n%s", tc.maxStr, text)
				}
			} else if strings.Contains(text, "maximum of") {
				t.Errorf("sub-task prompt should not mention a maximum when unlimited; got:\n%s", text)
			}
		})
	}
}

func TestSubTaskPromptUsesDisplayName(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	agent.cfg.DisplayName = "Data Analyst"

	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

	st := &subTaskTool{agent: agent}
	toolCtx := &tool.Context{
		UserID:    "user-1",
		SessionID: "parent-session",
		AgentName: "test-agent",
	}

	if _, err := st.Execute(context.Background(), map[string]any{
		"instruction": "do the thing",
		"type":        "fresh",
	}, toolCtx); err != nil {
		t.Fatal(err)
	}

	brk.mu.Lock()
	published := brk.published
	brk.mu.Unlock()
	if len(published) == 0 {
		t.Fatal("expected a broker publish for sub-task")
	}

	var a2aReq struct {
		Params struct {
			Message struct {
				Parts []struct {
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(published[0].payload, &a2aReq); err != nil {
		t.Fatalf("unmarshal published message: %v", err)
	}

	var text string
	for _, p := range a2aReq.Params.Message.Parts {
		if p.Kind == "text" {
			text += p.Text
		}
	}

	if !strings.Contains(text, "sub-task of agent 'Data Analyst'") {
		t.Errorf("sub-task prompt should name the caller by display name; got:\n%s", text)
	}
	if strings.Contains(text, "sub-task of agent 'test-agent'") {
		t.Errorf("sub-task prompt should not name the caller by its slug; got:\n%s", text)
	}
}

func TestSubTaskParentSessionIDNested(t *testing.T) {

	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	st := &subTaskTool{agent: agent}
	_ = st.Name()

	parentSessionID := "root-session"

	msg := a2a.Message{
		Metadata: map[string]any{
			"sessionBehavior": "RUN_BASED",
			"parentSessionID": parentSessionID,
		},
	}

	psid, ok := msg.Metadata["parentSessionID"].(string)
	if !ok || psid != "root-session" {
		t.Errorf("parentSessionID = %q, want %q", psid, "root-session")
	}

}

func TestMergeSkills(t *testing.T) {
	tests := []struct {
		name      string
		parent    []string
		requested []string
		want      []string
	}{
		{
			name:      "nil parent, no requested",
			parent:    nil,
			requested: nil,
			want:      nil,
		},
		{
			name:      "nil parent, with requested",
			parent:    nil,
			requested: []string{"skill-a"},
			want:      []string{"skill-a"},
		},
		{
			name:      "parent with skills, no requested",
			parent:    []string{"skill-a", "skill-b"},
			requested: nil,
			want:      []string{"skill-a", "skill-b"},
		},
		{
			name:      "merge parent and requested, no duplicates",
			parent:    []string{"skill-a"},
			requested: []string{"skill-b"},
			want:      []string{"skill-a", "skill-b"},
		},
		{
			name:      "deduplicate: requested skill already in parent",
			parent:    []string{"skill-a", "skill-b"},
			requested: []string{"skill-a", "skill-c"},
			want:      []string{"skill-a", "skill-b", "skill-c"},
		},
		{
			name:      "empty parent, with requested",
			parent:    []string{},
			requested: []string{"skill-a"},
			want:      []string{"skill-a"},
		},
		{
			name:      "empty strings ignored",
			parent:    []string{"skill-a", ""},
			requested: []string{"", "skill-b"},
			want:      []string{"skill-a", "skill-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeSkills(tt.parent, tt.requested)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParentLoadedSkills(t *testing.T) {
	if got := parentLoadedSkills(nil); got != nil {
		t.Errorf("nil toolCtx: got %v, want nil", got)
	}

	if got := parentLoadedSkills(&tool.Context{}); got != nil {
		t.Errorf("nil SkillState: got %v, want nil", got)
	}
}

func TestSubTaskResultDataFormat(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "completed work"})

	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

	st := &subTaskTool{agent: agent}
	toolCtx := &tool.Context{
		UserID:    "user-1",
		SessionID: "parent-session",
		AgentName: "test-agent",
	}

	result, err := st.Execute(context.Background(), map[string]any{
		"instruction": "Do work",
		"type":        "fresh",
	}, toolCtx)

	if err != nil {
		t.Fatal(err)
	}

	if result.Data != nil {
		dataBytes, marshalErr := json.Marshal(result.Data)
		if marshalErr != nil {
			t.Fatalf("failed to marshal result data: %v", marshalErr)
		}
		if len(dataBytes) == 0 {
			t.Error("expected non-empty data JSON")
		}
	}
}

func TestSubTaskHonorsConfiguredTimeout(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	agent.cfg.SubTaskTimeoutSeconds = 1
	agent.cfg.PeerRequestTimeoutSeconds = 8

	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

	st := &subTaskTool{agent: agent}
	toolCtx := &tool.Context{
		UserID:    "user-1",
		SessionID: "parent-session",
		AgentName: "test-agent",
	}

	result, err := st.Execute(context.Background(), map[string]any{
		"instruction": "never answered",
		"type":        "fresh",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.OnDelivery == nil {
		t.Fatal("expected Pending result with OnDelivery")
	}

	done := make(chan *tool.Result, 1)
	start := time.Now()
	result.OnDelivery(func(r *tool.Result) { done <- r })

	select {
	case final := <-done:
		elapsed := time.Since(start)
		if final.Status != tool.StatusError {
			t.Errorf("status = %q, want error", final.Status)
		}
		if !strings.Contains(final.Message, "timed out") {
			t.Errorf("message = %q, should mention timeout", final.Message)
		}
		if elapsed > 5*time.Second {
			t.Errorf("elapsed = %v, configured SubTaskTimeoutSeconds was 1s "+
				"(8s means the call site read PeerRequestTimeoutSeconds)", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("OnDelivery never fired — timeout regressed to legacy default")
	}
}

func TestCoreTaskInstructionStripsWrapper(t *testing.T) {
	wrapped := "You are running as a sub-task of agent 'X'. You are at sub-task depth 2.\n\n" +
		"YOU are the one who must do this work.\n\n" +
		"Task:\n\nCreate a professional PowerPoint summarizing the weekly report."
	got := coreTaskInstruction(wrapped)
	want := "Create a professional PowerPoint summarizing the weekly report."
	if got != want {
		t.Errorf("coreTaskInstruction = %q, want %q", got, want)
	}

	plain := "Do the thing and report back."
	if got := coreTaskInstruction(plain); got != plain {
		t.Errorf("coreTaskInstruction(plain) = %q, want unchanged", got)
	}
}

func TestInstructionsNearlyIdentical(t *testing.T) {
	spec := "summarizing the SAM weekly operational analysis. Overall health degraded, 1650 total " +
		"tasks, 1428 completed at 86.5 percent, 84 failed, dominant failure Northwind Freight sales " +
		"call prep on June 7, token spike to 147 million input tokens, recommend investigating the " +
		"Salesforce connector auth issue and reviewing the disabled scheduled tasks. Use the navy " +
		"header bar, Solace green accents, amber for degraded and red for failures, Calibri font."
	parent := "Create a professional PowerPoint (.pptx) file using python-pptx " + spec
	reDelegation := "Write and execute Python code to create a PowerPoint presentation (.pptx) " + spec
	if !instructionsNearlyIdentical(parent, reDelegation) {
		t.Error("expected near-verbatim re-delegation to be flagged")
	}

	subPart := "Validate that the file weekly_ops_report.pptx opens without error."
	if instructionsNearlyIdentical(parent, subPart) {
		t.Error("genuine smaller sub-part should NOT be flagged")
	}

	if instructionsNearlyIdentical("do it", "do it") {
		t.Error("short instructions must not be flagged")
	}
}

func TestSubTaskRefusesNearDuplicateInstruction(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	st := &subTaskTool{agent: agent}

	spec := "Build a self-contained HTML report of the weekly operational analysis. Overall health " +
		"degraded, 1650 total tasks, 1428 completed, 84 failed, dominant failure the Northwind Freight " +
		"sales call prep cluster on June 7, token spike to 147 million input tokens, recommend " +
		"investigating the Salesforce connector auth issue. Navy header bar, Solace green accents."

	toolCtx := &tool.Context{
		UserID:              "user-1",
		SessionID:           "parent-session",
		AgentName:           "test-agent",
		IncomingInstruction: "You are running as a sub-task. Task:\n\n" + spec,
	}

	result, err := st.Execute(context.Background(), map[string]any{
		"instruction": spec,
		"type":        "fresh",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error (near-duplicate must be refused before publish)", result.Status)
	}
	if result.OnDelivery != nil {
		t.Error("refused sub-task must not register an OnDelivery / publish to the broker")
	}
	if !strings.Contains(result.Message, "same as the task") {
		t.Errorf("message = %q, should explain the re-delegation refusal", result.Message)
	}
}

func TestSubTaskRefusesOverDepthBeforePublish(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	agent.cfg.MaxCallDepth = 3
	st := &subTaskTool{agent: agent}

	toolCtx := &tool.Context{
		UserID:    "user-1",
		SessionID: "parent-session",
		AgentName: "test-agent",
		CallDepth: 3,
	}
	result, err := st.Execute(context.Background(), map[string]any{
		"instruction": "Fetch the latest Q2 sales figures and summarize the top three regions.",
		"type":        "fresh",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error (over-depth must be refused before publish)", result.Status)
	}
	if result.OnDelivery != nil {
		t.Error("over-depth sub-task must not register an OnDelivery / publish to the broker")
	}
	if !strings.Contains(result.Message, "depth") {
		t.Errorf("message = %q, should explain the depth refusal", result.Message)
	}
}

func TestSubTaskRefusesOverFanoutBeforePublish(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	agent.cfg.MaxCallDepth = 0
	st := &subTaskTool{agent: agent}

	counter := new(atomic.Int64)
	counter.Store(2)

	toolCtx := &tool.Context{
		UserID:           "user-1",
		SessionID:        "parent-session",
		AgentName:        "test-agent",
		MaxSubTaskFanout: 2,
		SubTaskCount:     counter,
	}
	result, err := st.Execute(context.Background(), map[string]any{
		"instruction": "Render the appendix charts as PNGs and attach them to the report.",
		"type":        "fresh",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error (fan-out limit must be refused before publish)", result.Status)
	}
	if result.OnDelivery != nil {
		t.Error("over-fanout sub-task must not register an OnDelivery / publish to the broker")
	}
	if !strings.Contains(result.Message, "fan-out limit") {
		t.Errorf("message = %q, should explain the fan-out refusal", result.Message)
	}
	if got := counter.Load(); got != 3 {
		t.Errorf("counter = %d, want 3 (reservation is counted even on refusal)", got)
	}
}

func TestSubTaskContextMessage(t *testing.T) {
	msg := subTaskContextMessage("Data Analyst", "You are at sub-task depth 1.", "do the thing")

	if !strings.Contains(msg, "sub-task of agent 'Data Analyst'") {
		t.Errorf("caller display name not in preamble: %q", msg)
	}
	if !strings.Contains(msg, "Task:\n\ndo the thing") {
		t.Errorf("preamble should carry the Task marker and instruction: %q", msg)
	}
}
