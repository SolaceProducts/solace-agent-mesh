// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

import "testing"

func TestAssembler_TextStreaming(t *testing.T) {
	a := NewMessageAssembler()

	data := map[string]any{
		"result": map[string]any{
			"kind":   "status-update",
			"taskId": "task-123",
			"status": map[string]any{
				"message": map[string]any{
					"parts": []any{
						map[string]any{"kind": "text", "text": "Hello "},
					},
				},
			},
		},
	}

	msg, newText := a.ProcessEvent("status_update", data)
	if newText != "Hello " {
		t.Fatalf("expected 'Hello ', got %q", newText)
	}
	if msg.TaskID != "task-123" {
		t.Fatalf("expected task-123, got %q", msg.TaskID)
	}
	if msg.IsComplete {
		t.Fatal("should not be complete yet")
	}

	data2 := map[string]any{
		"result": map[string]any{
			"kind":   "status-update",
			"taskId": "task-123",
			"status": map[string]any{
				"message": map[string]any{
					"parts": []any{
						map[string]any{"kind": "text", "text": "world!"},
					},
				},
			},
			"final": true,
		},
	}

	msg, newText = a.ProcessEvent("status_update", data2)
	if newText != "world!" {
		t.Fatalf("expected 'world!', got %q", newText)
	}
	if !msg.IsComplete {
		t.Fatal("should be complete")
	}
	if msg.Text() != "Hello world!" {
		t.Fatalf("expected 'Hello world!', got %q", msg.Text())
	}
}

func TestAssembler_RPCError(t *testing.T) {
	a := NewMessageAssembler()

	data := map[string]any{
		"error": map[string]any{
			"code":    float64(-32600),
			"message": "invalid request",
		},
	}

	msg, newText := a.ProcessEvent("error", data)
	if !msg.IsError {
		t.Fatal("should be error")
	}
	if !msg.IsComplete {
		t.Fatal("should be complete on error")
	}
	if newText != "\nError: invalid request" {
		t.Fatalf("unexpected error text: %q", newText)
	}
}

func TestAssembler_FinalTaskFailed(t *testing.T) {
	a := NewMessageAssembler()

	data := map[string]any{
		"result": map[string]any{
			"kind": "task",
			"id":   "task-456",
			"status": map[string]any{
				"state": "failed",
				"message": map[string]any{
					"parts": []any{
						map[string]any{"kind": "text", "text": "agent crashed"},
					},
				},
			},
		},
	}

	msg, newText := a.ProcessEvent("final_response", data)
	if !msg.IsComplete {
		t.Fatal("should be complete")
	}
	if !msg.IsError {
		t.Fatal("should be error")
	}
	if msg.ErrorMessage != "agent crashed" {
		t.Fatalf("unexpected error message: %q", msg.ErrorMessage)
	}
	if newText != "\nTask failed: agent crashed" {
		t.Fatalf("unexpected new text: %q", newText)
	}
}

func TestAssembler_AuthenticationRequired(t *testing.T) {
	a := NewMessageAssembler()

	data := map[string]any{
		"result": map[string]any{
			"kind":   "status-update",
			"taskId": "task-789",
			"status": map[string]any{
				"state": "input-needed",
				"message": map[string]any{
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{
								"type":                          "authentication_required",
								"auth_uri":                      "https://provider.example/authorize?state=abc123&code_challenge=xyz",
								"auth_request_function_call_id": "call_42",
								"credential_key":                "mcp_github",
								"target_agent":                  "orchestrator",
								"target_agent_task_id":          "task-789",
							},
						},
					},
				},
			},
		},
	}

	msg, newText := a.ProcessEvent("status_update", data)
	if newText != "" {
		t.Fatalf("expected no text output, got %q", newText)
	}
	if msg.AuthRequest == nil {
		t.Fatal("AuthRequest should be populated")
	}
	if msg.AuthRequest.AuthURI != "https://provider.example/authorize?state=abc123&code_challenge=xyz" {
		t.Fatalf("unexpected auth_uri: %q", msg.AuthRequest.AuthURI)
	}
	if msg.AuthRequest.FunctionCallID != "call_42" {
		t.Fatalf("unexpected function_call_id: %q", msg.AuthRequest.FunctionCallID)
	}
	if msg.AuthRequest.CredentialKey != "mcp_github" {
		t.Fatalf("unexpected credential_key: %q", msg.AuthRequest.CredentialKey)
	}
	if msg.AuthRequest.TargetAgent != "orchestrator" {
		t.Fatalf("unexpected target_agent: %q", msg.AuthRequest.TargetAgent)
	}
	if msg.IsComplete {
		t.Fatal("auth-required status update must not complete the task")
	}

	a.ClearAuthRequest()
	if a.Message().AuthRequest != nil {
		t.Fatal("AuthRequest should be cleared")
	}
}

func hilEvent(taskID, agentName, where string) map[string]any {
	meta := map[string]any{"agent_name": agentName}
	message := map[string]any{
		"parts": []any{
			map[string]any{
				"kind": "data",
				"data": map[string]any{
					"type":       "user_input_request",
					"request_id": "hil-req-1",
					"source":     "tool_approval",
				},
			},
		},
	}
	result := map[string]any{
		"kind":   "status-update",
		"taskId": taskID,
		"status": map[string]any{"state": "input-required", "message": message},
	}
	switch where {
	case "event":
		result["metadata"] = meta
	case "message":
		message["metadata"] = meta
	}
	return map[string]any{"result": result}
}

func TestAssembler_HILRequest_AgentName(t *testing.T) {
	tests := []struct {
		name      string
		where     string
		wantAgent string
	}{
		{"direct agent (event metadata)", "event", "DirectAgent"},
		{"peer-forwarded (inner message metadata)", "message", "Orchestrator"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewMessageAssembler()
			msg, _ := a.ProcessEvent("status_update", hilEvent("task-1", tc.wantAgent, tc.where))
			if msg.HILRequest == nil {
				t.Fatal("HILRequest should be populated")
			}
			if msg.HILRequest.AgentName != tc.wantAgent {
				t.Errorf("HILRequest.AgentName = %q, want %q", msg.HILRequest.AgentName, tc.wantAgent)
			}
			if msg.HILRequest.TaskID != "task-1" {
				t.Errorf("HILRequest.TaskID = %q, want task-1", msg.HILRequest.TaskID)
			}
			if msg.HILRequest.RequestID != "hil-req-1" {
				t.Errorf("HILRequest.RequestID = %q, want hil-req-1", msg.HILRequest.RequestID)
			}
		})
	}
}

func TestResolveAgentName(t *testing.T) {
	cards := []AgentCard{
		{Name: "orchestrator"},
		{Name: "data_analyst"},
		{Name: "Web_Search_Agent"},
		{
			Name: "agent_019e1876_5f14_7f40_b463_b564c754128c",
			Capabilities: AgentCapabilities{
				Extensions: []AgentExtension{{
					URI:    displayNameExtensionURI,
					Params: map[string]any{"display_name": "Jira and Confluence"},
				}},
			},
		},
		{
			Name: "agent_019e38ed_f9e1_7728_8173_50508620e643",
			Capabilities: AgentCapabilities{
				Extensions: []AgentExtension{{
					URI:    displayNameExtensionURI,
					Params: map[string]any{"display_name": "Support"},
				}},
			},
		},
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"orchestrator", "orchestrator"},
		{"Orchestrator", "orchestrator"},
		{"web_search", "Web_Search_Agent"},
		{"nonexistent", ""},
		{"Jira and Confluence", "agent_019e1876_5f14_7f40_b463_b564c754128c"},
		{"jira and confluence", "agent_019e1876_5f14_7f40_b463_b564c754128c"},
		{"Support", "agent_019e38ed_f9e1_7728_8173_50508620e643"},
		{"support", "agent_019e38ed_f9e1_7728_8173_50508620e643"},
		{"agent_019e1876_5f14_7f40_b463_b564c754128c", "agent_019e1876_5f14_7f40_b463_b564c754128c"},
	}

	for _, tt := range tests {
		got := ResolveAgentName(cards, tt.input)
		if got != tt.expected {
			t.Errorf("ResolveAgentName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestAgentCardDisplayName(t *testing.T) {
	tests := []struct {
		name string
		card AgentCard
		want string
	}{
		{
			name: "extension present",
			card: AgentCard{
				Name: "agent_x",
				Capabilities: AgentCapabilities{
					Extensions: []AgentExtension{{
						URI:    displayNameExtensionURI,
						Params: map[string]any{"display_name": "Friendly"},
					}},
				},
			},
			want: "Friendly",
		},
		{
			name: "no extension falls back to Name",
			card: AgentCard{Name: "Orchestrator"},
			want: "Orchestrator",
		},
		{
			name: "empty display_name param ignored",
			card: AgentCard{
				Name: "agent_y",
				Capabilities: AgentCapabilities{
					Extensions: []AgentExtension{{
						URI:    displayNameExtensionURI,
						Params: map[string]any{"display_name": ""},
					}},
				},
			},
			want: "agent_y",
		},
		{
			name: "unrelated extension ignored",
			card: AgentCard{
				Name: "agent_z",
				Capabilities: AgentCapabilities{
					Extensions: []AgentExtension{{URI: "https://example.com/other"}},
				},
			},
			want: "agent_z",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.card.DisplayName(); got != tc.want {
				t.Errorf("DisplayName() = %q; want %q", got, tc.want)
			}
		})
	}
}
