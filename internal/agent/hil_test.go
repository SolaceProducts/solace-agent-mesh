// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestHILRequestTracking(t *testing.T) {
	agent := New(AgentConfig{Name: "test-agent"})

	t.Run("register and deliver", func(t *testing.T) {
		ch := agent.registerHILRequest("req-1", hilTestOwner, "")

		resp := &a2a.UserInputResponseData{
			RequestID: "req-1",
			Status:    "answered",
			Data:      map[string]any{"answers": map[string]any{"q0": "yes"}},
		}

		ok := agent.deliverHILResponseAsOwner(resp)
		if !ok {
			t.Fatal("deliverHILResponse returned false for pending request")
		}

		select {
		case got := <-ch:
			if got.RequestID != "req-1" {
				t.Errorf("requestID = %q, want req-1", got.RequestID)
			}
			if got.Status != "answered" {
				t.Errorf("status = %q, want answered", got.Status)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for response on channel")
		}
	})

	t.Run("unregister prevents delivery", func(t *testing.T) {
		_ = agent.registerHILRequest("req-2", hilTestOwner, "")
		agent.unregisterHILRequest("req-2")

		ok := agent.deliverHILResponseAsOwner(&a2a.UserInputResponseData{
			RequestID: "req-2",
			Status:    "answered",
		})
		if ok {
			t.Error("deliverHILResponse returned true after unregister")
		}
	})

	t.Run("late response returns false", func(t *testing.T) {
		ok := agent.deliverHILResponseAsOwner(&a2a.UserInputResponseData{
			RequestID: "nonexistent",
			Status:    "answered",
		})
		if ok {
			t.Error("deliverHILResponse returned true for nonexistent request")
		}
	})

	t.Run("double deliver drops second", func(t *testing.T) {
		ch := agent.registerHILRequest("req-3", hilTestOwner, "")
		defer agent.unregisterHILRequest("req-3")

		resp1 := &a2a.UserInputResponseData{RequestID: "req-3", Status: "answered"}
		resp2 := &a2a.UserInputResponseData{RequestID: "req-3", Status: "cancelled"}

		ok1 := agent.deliverHILResponseAsOwner(resp1)
		_ = agent.deliverHILResponseAsOwner(resp2)

		if !ok1 {
			t.Error("first deliver should succeed")
		}

		got := <-ch
		if got.Status != "answered" {
			t.Errorf("got status %q, want answered (first response)", got.Status)
		}
	})
}

func TestExtractUserInputResponse(t *testing.T) {
	agent := New(AgentConfig{Name: "test-agent"})

	t.Run("extracts valid response", func(t *testing.T) {
		msg := a2a.Message{
			Parts: []a2a.Part{
				a2a.NewDataPart(map[string]any{
					"type":       "user_input_response",
					"request_id": "hil-abc",
					"status":     "answered",
					"data":       map[string]any{"answers": map[string]any{"q0": "yes"}},
				}),
			},
		}

		resp := agent.extractUserInputResponse(msg)
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if resp.RequestID != "hil-abc" {
			t.Errorf("requestID = %q, want hil-abc", resp.RequestID)
		}
		if resp.Status != "answered" {
			t.Errorf("status = %q, want answered", resp.Status)
		}
	})

	t.Run("ignores non-hil data parts", func(t *testing.T) {
		msg := a2a.Message{
			Parts: []a2a.Part{
				a2a.NewDataPart(map[string]any{
					"type": "some_other_signal",
				}),
				{Kind: "text", Text: "hello"},
			},
		}

		resp := agent.extractUserInputResponse(msg)
		if resp != nil {
			t.Error("expected nil for non-HIL message")
		}
	})
}

func TestDeliverHILResponseToPausedTask_Answered(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "test_hil_tool", alwaysOKTool())
	state := pausedStateWithHILEntry(t, agent, "task-hil-1", "call-h1", "hil-req-abc")

	ok := agent.deliverHILResponseToPausedTaskAsOwner(&a2a.UserInputResponseData{
		RequestID: "hil-req-abc",
		Status:    "answered",
		Data:      map[string]any{"decision": "approved", "approver": "alice"},
	})
	if !ok {
		t.Fatal("expected true for matching HIL request")
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.PendingCalls["call-h1"] != nil {
		t.Error("pending call should be removed after delivery")
	}
	r := state.SyncResults[0]
	if r == nil {
		t.Fatal("SyncResults[0] is nil")
	}
	if r.Data["status"] != "approved" {
		t.Errorf("result status = %v, want approved", r.Data["status"])
	}
	if resumeCalls.Load() != 1 {
		t.Errorf("resume hook called %d times, want 1", resumeCalls.Load())
	}
}

func TestDeliverHILResponseToPausedTask_NotFound(t *testing.T) {
	t.Parallel()
	agent := newTestAgentWithStubTool(t, "test_hil_tool", alwaysOKTool())

	ok := agent.deliverHILResponseToPausedTaskAsOwner(&a2a.UserInputResponseData{
		RequestID: "nonexistent",
		Status:    "answered",
	})
	if ok {
		t.Error("expected false for nonexistent HIL request")
	}
}

func TestDeliverHILResponseToPausedTask_AlreadyClaimed(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "test_hil_tool", alwaysOKTool())
	state := pausedStateWithHILEntry(t, agent, "task-hil-dup", "call-hd", "hil-req-dup")

	ok1 := agent.deliverHILResponseToPausedTaskAsOwner(&a2a.UserInputResponseData{
		RequestID: "hil-req-dup",
		Status:    "answered",
		Data:      map[string]any{"decision": "approved"},
	})
	if !ok1 {
		t.Fatal("first delivery should succeed")
	}

	_ = state
	ok2 := agent.deliverHILResponseToPausedTaskAsOwner(&a2a.UserInputResponseData{
		RequestID: "hil-req-dup",
		Status:    "answered",
		Data:      map[string]any{"decision": "denied"},
	})
	if ok2 {
		t.Error("second delivery should fail — pending call already claimed")
	}
}

func TestDeliverHILResponse_FallsBackToPausedTask(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "test_hil_tool", alwaysOKTool())
	_ = pausedStateWithHILEntry(t, agent, "task-fb", "call-fb", "hil-fb-req")

	ok := agent.deliverHILResponseAsOwner(&a2a.UserInputResponseData{
		RequestID: "hil-fb-req",
		Status:    "answered",
		Data:      map[string]any{"decision": "approved"},
	})
	if !ok {
		t.Fatal("deliverHILResponse should succeed via paused task fallback")
	}
	if resumeCalls.Load() != 1 {
		t.Errorf("resume hook called %d times, want 1", resumeCalls.Load())
	}
}

func TestGenerateApprovalSurface(t *testing.T) {
	schema := &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"path":      map[string]any{"type": "string", "description": "The file path to delete."},
			"recursive": map[string]any{"type": "boolean", "description": "Whether to delete recursively."},
		},
		Required: []string{"path"},
	}
	surface, err := generateApprovalSurface(
		"hil-approval-1",
		"delete_file",
		"code-worker",
		map[string]any{"path": "/tmp/test.txt", "recursive": true},
		schema,
		"This will delete files.",
		true,
		30*time.Minute,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed["surfaceId"] != "hil-approval-1" {
		t.Errorf("surfaceId = %v, want hil-approval-1", parsed["surfaceId"])
	}

	components := parsed["components"].([]any)

	var hasApprove, hasDeny bool
	for _, c := range components {
		cm := c.(map[string]any)
		if cm["id"] == "approve-btn" {
			hasApprove = true
		}
		if cm["id"] == "deny-btn" {
			hasDeny = true
		}
	}
	if !hasApprove {
		t.Error("missing approve button")
	}
	if !hasDeny {
		t.Error("missing deny button")
	}

	var hasArgsCard, hasPathField, hasRecursiveField bool
	for _, c := range components {
		cm := c.(map[string]any)
		switch cm["id"] {
		case "args-card":
			hasArgsCard = true
		case "arg-path":
			hasPathField = true
		case "arg-recursive":
			hasRecursiveField = true
		}
	}
	if !hasArgsCard {
		t.Error("missing args card (showArgs=true)")
	}
	if !hasPathField {
		t.Error("missing arg-path field")
	}
	if !hasRecursiveField {
		t.Error("missing arg-recursive field")
	}
}

func TestGenerateApprovalSurface_HiddenArgs(t *testing.T) {
	surface, err := generateApprovalSurface(
		"hil-approval-2",
		"delete_file",
		"code-worker",
		map[string]any{"path": "/tmp/test.txt"},
		nil,
		"",
		false,
		45*time.Minute,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	components := parsed["components"].([]any)
	for _, c := range components {
		cm := c.(map[string]any)
		if cm["id"] == "args-card" {
			t.Error("args card should not be present when showArgs=false")
		}
	}
}

func TestAgentDisplayName(t *testing.T) {
	tests := []struct {
		name string
		cfg  AgentConfig
		want string
	}{
		{
			name: "DisplayName wins when set",
			cfg:  AgentConfig{Name: "agent_019e1876_5f14", DisplayName: "Jira and Confluence"},
			want: "Jira and Confluence",
		},
		{
			name: "falls back to Name when DisplayName empty",
			cfg:  AgentConfig{Name: "agent_019e1876_5f14"},
			want: "agent_019e1876_5f14",
		},
		{
			name: "empty DisplayName is treated as unset",
			cfg:  AgentConfig{Name: "slug", DisplayName: ""},
			want: "slug",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentDisplayName(&tc.cfg); got != tc.want {
				t.Errorf("agentDisplayName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGenerateApprovalSurface_DescriptionAsTooltip(t *testing.T) {
	schema := &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"projectKey": map[string]any{"type": "string", "description": "A project key in Jira is a unique identifier."},
			"summary":    map[string]any{"type": "string"},
		},
	}
	surface, err := generateApprovalSurface(
		"hil-approval-tt",
		"createJiraIssue",
		"code-worker",
		map[string]any{"projectKey": "DATAGO", "summary": "Add Gmail"},
		schema,
		"",
		true,
		30*time.Minute,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	components := parsed["components"].([]any)

	var projectLabel, summaryLabel, projectValue, summaryValue map[string]any
	for _, c := range components {
		cm := c.(map[string]any)
		switch cm["id"] {
		case "arg-projectKey-label":
			projectLabel = cm
		case "arg-summary-label":
			summaryLabel = cm
		case "arg-projectKey-value":
			projectValue = cm
		case "arg-summary-value":
			summaryValue = cm
		}
		if id, _ := cm["id"].(string); strings.HasSuffix(id, "-desc") {
			t.Errorf("unexpected legacy description component %q — descriptions should ride along the label as tooltip", id)
		}
	}
	if projectValue == nil {
		t.Fatal("missing arg-projectKey-value")
	}
	if ml, _ := projectValue["multiline"].(bool); !ml {
		t.Errorf("arg-projectKey-value missing multiline: true (got %v)", projectValue["multiline"])
	}
	if summaryValue == nil {
		t.Fatal("missing arg-summary-value")
	}
	if ml, _ := summaryValue["multiline"].(bool); !ml {
		t.Errorf("arg-summary-value missing multiline: true (got %v)", summaryValue["multiline"])
	}
	if projectLabel == nil {
		t.Fatal("missing arg-projectKey-label")
	}
	if tt, _ := projectLabel["tooltip"].(string); tt != "A project key in Jira is a unique identifier." {
		t.Errorf("projectKey tooltip = %q, want schema description", tt)
	}
	if summaryLabel == nil {
		t.Fatal("missing arg-summary-label")
	}
	if _, has := summaryLabel["tooltip"]; has {
		t.Errorf("summary label should have no tooltip (no schema description) — got %v", summaryLabel["tooltip"])
	}
}

func TestGenerateApprovalSurface_MessageTemplating(t *testing.T) {
	tests := []struct {
		name    string
		message string
		args    map[string]any
		want    string
	}{
		{
			name:    "substitutes named args",
			message: "Create a {{.issueTypeName}} in project {{.projectKey}}: '{{.summary}}'?",
			args:    map[string]any{"issueTypeName": "Story", "projectKey": "DATAGO", "summary": "Add Gmail"},
			want:    "Create a Story in project DATAGO: 'Add Gmail'?",
		},
		{
			name:    "missing key renders empty",
			message: "Hi {{.who}}!",
			args:    map[string]any{},
			want:    "Hi !",
		},
		{
			name:    "no template tokens passes through",
			message: "Confirm?",
			args:    map[string]any{"foo": "bar"},
			want:    "Confirm?",
		},
		{
			name:    "malformed template falls back to raw",
			message: "Hi {{.who",
			args:    map[string]any{"who": "alice"},
			want:    "Hi {{.who",
		},
		{
			name:    "oversized output is truncated with ellipsis",
			message: "{{.payload}}",
			args:    map[string]any{"payload": strings.Repeat("a", 5000)},
			want:    strings.Repeat("a", 1024-len("…")) + "…",
		},
		{
			name:    "oversized multibyte output truncates on rune boundary",
			message: "{{.payload}}",
			args:    map[string]any{"payload": strings.Repeat("あ", 400)},
			want:    "",
		},
		{
			name:    "execute-time error falls back to raw",
			message: "Hi {{.who.Name}}",
			args:    map[string]any{"who": "alice"},
			want:    "Hi {{.who.Name}}",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			surface, err := generateApprovalSurface(
				"hil-approval-tmpl",
				"some_tool",
				"agent",
				tc.args,
				nil,
				tc.message,
				false,
				30*time.Minute,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var parsed map[string]any
			if err := json.Unmarshal(surface, &parsed); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			components := parsed["components"].([]any)
			var msgText string
			for _, c := range components {
				cm := c.(map[string]any)
				if cm["id"] == "message" {
					if lit, ok := cm["text"].(map[string]any); ok {
						msgText, _ = lit["literalString"].(string)
					}
				}
			}
			if tc.name == "oversized multibyte output truncates on rune boundary" {
				if len(msgText) > 1024 {
					t.Errorf("truncated length = %d, want ≤ 1024", len(msgText))
				}
				if !utf8.ValidString(msgText) {
					t.Errorf("truncated output is not valid UTF-8: %q", msgText)
				}
				if !strings.HasSuffix(msgText, "…") {
					t.Errorf("truncated output should end with ellipsis, got %q", msgText[len(msgText)-6:])
				}
				return
			}
			if msgText != tc.want {
				t.Errorf("rendered message = %q, want %q", msgText, tc.want)
			}
		})
	}
}

func TestGenerateApprovalSurface_ContentTypeHint(t *testing.T) {
	surface, err := generateApprovalSurface(
		"hil-approval-ct",
		"create_confluence_page",
		"jira-confluence",
		map[string]any{
			"body":     "<h1>Release Notes</h1><p>Shipped.</p>",
			"spaceKey": "ENG",
			"metadata": map[string]any{"labels": []any{"release"}},
		},
		nil,
		"",
		true,
		30*time.Minute,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	contentTypeByID := map[string]string{}
	textByID := map[string]string{}
	for _, c := range parsed["components"].([]any) {
		cm := c.(map[string]any)
		id, _ := cm["id"].(string)
		if ct, ok := cm["contentType"].(string); ok {
			contentTypeByID[id] = ct
		}
		if lit, ok := cm["text"].(map[string]any); ok {
			textByID[id], _ = lit["literalString"].(string)
		}
	}

	want := map[string]string{
		"arg-body-value":     "html",
		"arg-spaceKey-value": "text",
		"arg-metadata-value": "json",
	}
	for id, wantCT := range want {
		if got := contentTypeByID[id]; got != wantCT {
			t.Errorf("contentType for %s = %q, want %q", id, got, wantCT)
		}
	}

	if got := textByID["arg-body-value"]; got != "<h1>Release Notes</h1><p>Shipped.</p>" {
		t.Errorf("arg-body-value literalString changed: %q", got)
	}
	if got := textByID["arg-spaceKey-value"]; got != "ENG" {
		t.Errorf("arg-spaceKey-value literalString changed: %q", got)
	}
}
