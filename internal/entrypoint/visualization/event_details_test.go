// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package visualization

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildPayloadSummary_PreviewTruncationIsRuneSafe(t *testing.T) {
	evt := &RichStreamEvent{}
	evt.buildPayloadSummary(map[string]any{"params": strings.Repeat("a", 98) + "é"})
	if evt.PayloadSummary == nil || evt.PayloadSummary.ParamsPreview == nil {
		t.Fatal("expected a params preview")
	}
	if got := *evt.PayloadSummary.ParamsPreview; !utf8.ValidString(got) {
		t.Errorf("params preview contains invalid UTF-8: %q", got)
	}
}

func TestShouldFilterTopic(t *testing.T) {
	tests := []struct {
		name  string
		topic string
		want  bool
	}{
		{"discovery topic", "test-ns/a2a/v1/discovery/agents", true},
		{"trust topic", "test-ns/a2a/v1/trust/cards", true},
		{"normal request topic", "test-ns/a2a/v1/agent/request/MyAgent/task-1", false},
		{"normal status topic", "test-ns/a2a/v1/gateway/status/gw-1/task-1", false},
		{"skill init topic", "test-ns/a2a/v1/sam_skill/init/sam-tool-schema", true},
		{"skill non-init topic", "test-ns/a2a/v1/sam_skill/invoke/MySkill/task-1", true},
		{"sam_remote_tool request", "test-ns/a2a/v1/sam_remote_tool/agent/request/Foo/corr-1", true},
		{"sam_remote_tool response", "test-ns/a2a/v1/sam_remote_tool/agent/response/Foo/corr-1", true},
		{"agent name shares sam_skill prefix", "test-ns/a2a/v1/agent/request/sam_skill_helper/task-1", false},
		{"discovery without a2a prefix", "custom/discovery/agents", true},
		{"empty topic", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldFilterTopic(tt.topic); got != tt.want {
				t.Errorf("ShouldFilterTopic(%q) = %v, want %v", tt.topic, got, tt.want)
			}
		})
	}
}

func TestShouldFilterWorkingState(t *testing.T) {
	t.Run("working state with all text parts", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"status": map[string]any{
					"state": "working",
					"message": map[string]any{
						"parts": []any{
							map[string]any{"kind": "text", "text": "thinking..."},
						},
					},
				},
			},
		}
		if !ShouldFilterWorkingState(payload) {
			t.Error("expected all-text working state to be filtered")
		}
	})

	t.Run("working state with all in-progress data parts", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"status": map[string]any{
					"state": "working",
					"message": map[string]any{
						"parts": []any{
							map[string]any{"data": map[string]any{"status": "in-progress"}},
						},
					},
				},
			},
		}
		if !ShouldFilterWorkingState(payload) {
			t.Error("expected all-in-progress working state to be filtered")
		}
	})

	t.Run("working state with completed data parts", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"status": map[string]any{
					"state": "working",
					"message": map[string]any{
						"parts": []any{
							map[string]any{"data": map[string]any{"status": "completed"}},
						},
					},
				},
			},
		}
		if ShouldFilterWorkingState(payload) {
			t.Error("completed data parts should not be filtered")
		}
	})

	t.Run("non-working state", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"status": map[string]any{
					"state": "completed",
					"message": map[string]any{
						"parts": []any{
							map[string]any{"kind": "text", "text": "done"},
						},
					},
				},
			},
		}
		if ShouldFilterWorkingState(payload) {
			t.Error("non-working state should not be filtered")
		}
	})

	t.Run("missing result", func(t *testing.T) {
		if ShouldFilterWorkingState(map[string]any{"method": "foo"}) {
			t.Error("missing result should not be filtered")
		}
	})

	t.Run("missing status", func(t *testing.T) {
		payload := map[string]any{"result": map[string]any{"kind": "task"}}
		if ShouldFilterWorkingState(payload) {
			t.Error("missing status should not be filtered")
		}
	})

	t.Run("empty parts", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"status": map[string]any{
					"state":   "working",
					"message": map[string]any{"parts": []any{}},
				},
			},
		}
		if ShouldFilterWorkingState(payload) {
			t.Error("empty parts should not be filtered")
		}
	})
}

func TestIsVisualizationExcluded(t *testing.T) {
	t.Run("visualization=false bool", func(t *testing.T) {
		payload := map[string]any{
			"params": map[string]any{
				"message": map[string]any{
					"metadata": map[string]any{"visualization": false},
				},
			},
		}
		if !IsVisualizationExcluded(payload) {
			t.Error("expected visualization=false to be excluded")
		}
	})

	t.Run("visualization=false string", func(t *testing.T) {
		payload := map[string]any{
			"params": map[string]any{
				"message": map[string]any{
					"metadata": map[string]any{"visualization": "false"},
				},
			},
		}
		if !IsVisualizationExcluded(payload) {
			t.Error("expected visualization='false' to be excluded")
		}
	})

	t.Run("visualization=true", func(t *testing.T) {
		payload := map[string]any{
			"params": map[string]any{
				"message": map[string]any{
					"metadata": map[string]any{"visualization": true},
				},
			},
		}
		if IsVisualizationExcluded(payload) {
			t.Error("visualization=true should not be excluded")
		}
	})

	t.Run("missing metadata", func(t *testing.T) {
		payload := map[string]any{
			"params": map[string]any{
				"message": map[string]any{},
			},
		}
		if IsVisualizationExcluded(payload) {
			t.Error("missing metadata should not be excluded")
		}
	})

	t.Run("empty payload", func(t *testing.T) {
		if IsVisualizationExcluded(map[string]any{}) {
			t.Error("empty payload should not be excluded")
		}
	})
}

func TestInferEventDetails_SystemEvent(t *testing.T) {
	payload := map[string]any{
		"event_type":       "agent_connected",
		"source_component": "my-agent",
	}
	evt := InferEventDetails("test-ns/a2a/v1/events/system", payload)

	if evt.Direction != "system_event" {
		t.Errorf("Direction = %q, want system_event", evt.Direction)
	}
	if evt.DebugType != "sam_event" {
		t.Errorf("DebugType = %q, want sam_event", evt.DebugType)
	}
	if evt.SourceEntity != "my-agent" {
		t.Errorf("SourceEntity = %q, want my-agent", evt.SourceEntity)
	}
	if evt.TargetEntity != "system" {
		t.Errorf("TargetEntity = %q, want system", evt.TargetEntity)
	}
	if evt.PayloadSummary == nil || evt.PayloadSummary.Method != "agent_connected" {
		t.Errorf("PayloadSummary.Method = %v, want agent_connected", evt.PayloadSummary)
	}
}

func TestInferEventDetails_Request(t *testing.T) {
	payload := map[string]any{
		"id":     "req-1",
		"method": "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"metadata": map[string]any{
					"agent_name": "TargetAgent",
				},
			},
		},
	}
	evt := InferEventDetails("test-ns/a2a/v1/agent/request/TargetAgent/task-1", payload)

	if evt.Direction != "request" {
		t.Errorf("Direction = %q, want request", evt.Direction)
	}
	if evt.DebugType != "message/send" {
		t.Errorf("DebugType = %q, want message/send", evt.DebugType)
	}
	if evt.TargetEntity != "TargetAgent" {
		t.Errorf("TargetEntity = %q, want TargetAgent", evt.TargetEntity)
	}
	if evt.TaskID != "req-1" {
		t.Errorf("TaskID = %v, want req-1", evt.TaskID)
	}
}

func TestInferEventDetails_ResponseTask(t *testing.T) {
	payload := map[string]any{
		"id": "resp-1",
		"result": map[string]any{
			"kind":   "task",
			"taskId": "task-123",
			"metadata": map[string]any{
				"agent_name": "MyAgent",
			},
		},
	}
	evt := InferEventDetails("test-ns/a2a/v1/agent/response/MyAgent/task-123", payload)

	if evt.Direction != "task" {
		t.Errorf("Direction = %q, want task", evt.Direction)
	}
	if evt.DebugType != "task_result" {
		t.Errorf("DebugType = %q, want task_result", evt.DebugType)
	}
	if evt.TaskID != "task-123" {
		t.Errorf("TaskID = %v, want task-123", evt.TaskID)
	}
	if evt.SourceEntity != "MyAgent" {
		t.Errorf("SourceEntity = %q, want MyAgent", evt.SourceEntity)
	}
}

func TestInferEventDetails_StatusUpdate(t *testing.T) {
	payload := map[string]any{
		"id": "status-1",
		"result": map[string]any{
			"kind": "status-update",
			"status": map[string]any{
				"message": map[string]any{
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{
								"type":      "llm_invocation",
								"model":     "test-model",
								"tool_name": "get_time",
							},
						},
					},
					"metadata": map[string]any{
						"agent_name": "TestAgent",
					},
				},
			},
		},
	}
	evt := InferEventDetails("test-ns/a2a/v1/gateway/status/gw-1/task-1", payload)

	if evt.Direction != "status-update" {
		t.Errorf("Direction = %q, want status-update", evt.Direction)
	}
	if evt.DebugType != "llm_invocation" {
		t.Errorf("DebugType = %q, want llm_invocation", evt.DebugType)
	}
	if evt.SourceEntity != "TestAgent" {
		t.Errorf("SourceEntity = %q, want TestAgent", evt.SourceEntity)
	}
}

func TestInferEventDetails_StatusUpdate_StreamingText(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"kind": "status-update",
			"status": map[string]any{
				"message": map[string]any{
					"parts": []any{
						map[string]any{"kind": "text", "text": "streaming chunk"},
					},
				},
			},
		},
	}
	evt := InferEventDetails("test-ns/a2a/v1/status/gw-1/task-1", payload)

	if evt.DebugType != "streaming_text" {
		t.Errorf("DebugType = %q, want streaming_text", evt.DebugType)
	}
}

func TestInferEventDetails_ErrorResponse(t *testing.T) {
	payload := map[string]any{
		"id":    "err-1",
		"error": map[string]any{"code": -32600, "message": "invalid request"},
	}
	evt := InferEventDetails("test-ns/a2a/v1/agent/response/MyAgent/task-1", payload)

	if evt.Direction != "error_response" {
		t.Errorf("Direction = %q, want error_response", evt.Direction)
	}
	if evt.DebugType != "error" {
		t.Errorf("DebugType = %q, want error", evt.DebugType)
	}
}

func TestInferEventDetails_TopicFallback(t *testing.T) {
	tests := []struct {
		topic   string
		wantDir string
	}{
		{"test-ns/a2a/v1/agent/request/Agent/task-1", "request"},
		{"test-ns/a2a/v1/agent/response/Agent/task-1", "response"},
		{"test-ns/a2a/v1/gateway/status/gw-1/task-1", "status_update"},
		{"test-ns/a2a/v1/discovery/agents", "discovery"},
		{"test-ns/something/else", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.topic, func(t *testing.T) {
			evt := InferEventDetails(tt.topic, map[string]any{})
			if evt.Direction != tt.wantDir {
				t.Errorf("Direction = %q, want %q", evt.Direction, tt.wantDir)
			}
		})
	}
}

func TestInferEventDetails_StatusUpdate_AgentTopic(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"kind":     "status-update",
			"taskId":   "task-1",
			"metadata": map[string]any{"agent_name": "DataDoubler"},
			"status": map[string]any{
				"message": map[string]any{
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{"type": "llm_invocation", "model": "gpt-4o"},
						},
					},
				},
			},
		},
	}
	evt := InferEventDetails("ns/a2a/v1/agent/status/ParallelExecutionWorkflow/wf-task", payload)
	if evt.DebugType != "llm_invocation" {
		t.Errorf("DebugType = %q, want llm_invocation", evt.DebugType)
	}
	if evt.SourceEntity != "DataDoubler" {
		t.Errorf("SourceEntity = %q, want DataDoubler (from result.metadata)", evt.SourceEntity)
	}
}

func TestInferEventDetails_StatusUpdate_AgentTopicNoMetadata(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"kind": "status-update",
			"status": map[string]any{
				"message": map[string]any{
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{"type": "llm_invocation"},
						},
					},
				},
			},
		},
	}
	evt := InferEventDetails("ns/a2a/v1/agent/status/MyAgent/task-1", payload)
	if evt.SourceEntity != "MyAgent" {
		t.Errorf("SourceEntity = %q, want MyAgent (from topic)", evt.SourceEntity)
	}
}

func TestInferEventDetails_Request_StructuredInvocation(t *testing.T) {
	payload := map[string]any{
		"id":     "wf_task-validate",
		"method": "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"metadata": map[string]any{
					"agent_name":    "DataDoubler",
					"workflow_name": "ParallelExecutionWorkflow",
				},
				"parts": []any{
					map[string]any{
						"kind": "data",
						"data": map[string]any{
							"type":          "structured_invocation_request",
							"workflow_name": "ParallelExecutionWorkflow",
							"node_id":       "validate",
						},
					},
					map[string]any{"kind": "text", "text": "Validate the input."},
				},
			},
		},
	}
	evt := InferEventDetails("ns/a2a/v1/agent/request/DataDoubler", payload)
	if evt.DebugType != "structured_invocation_request" {
		t.Errorf("DebugType = %q, want structured_invocation_request", evt.DebugType)
	}
	if evt.Direction != "request" {
		t.Errorf("Direction = %q, want request", evt.Direction)
	}
	if evt.TargetEntity != "DataDoubler" {
		t.Errorf("TargetEntity = %q, want DataDoubler", evt.TargetEntity)
	}
}

func TestInferEventDetails_Request_PlainText(t *testing.T) {
	payload := map[string]any{
		"id":     "req-1",
		"method": "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"parts": []any{
					map[string]any{"kind": "text", "text": "hello agent"},
				},
			},
		},
	}
	evt := InferEventDetails("ns/a2a/v1/agent/request/Agent", payload)
	if evt.DebugType != "message/send" {
		t.Errorf("DebugType = %q, want message/send (no data parts)", evt.DebugType)
	}
}

func TestInferEventDetails_StatusUpdate_WorkflowGatewayTopic(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"kind": "status-update",
			"status": map[string]any{
				"message": map[string]any{
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{
								"type":          "workflow_execution_start",
								"workflow_name": "ParallelExecutionWorkflow",
								"execution_id":  "exec-1",
							},
						},
					},
				},
			},
		},
	}
	evt := InferEventDetails("ns/a2a/v1/gateway/status/embedded-gw/task-1", payload)
	if evt.DebugType != "workflow_execution_start" {
		t.Errorf("DebugType = %q, want workflow_execution_start", evt.DebugType)
	}
	if evt.SourceEntity != "ParallelExecutionWorkflow" {
		t.Errorf("SourceEntity = %q, want ParallelExecutionWorkflow (from data part)", evt.SourceEntity)
	}
}

func TestInferEventDetails_Task_TopicFallback(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"kind":   "task",
			"taskId": "task-1",
		},
	}
	evt := InferEventDetails("ns/a2a/v1/agent/response/MyAgent/task-1", payload)
	if evt.SourceEntity != "MyAgent" {
		t.Errorf("SourceEntity = %q, want MyAgent (from topic)", evt.SourceEntity)
	}
	if evt.DebugType != "task_result" {
		t.Errorf("DebugType = %q, want task_result", evt.DebugType)
	}
}

func TestAgentFromTopic(t *testing.T) {
	tests := []struct {
		topic string
		want  string
	}{
		{"ns/a2a/v1/agent/status/MyAgent/task-1", "MyAgent"},
		{"ns/a2a/v1/agent/response/MyAgent/task-1", "MyAgent"},
		{"ns/a2a/v1/gateway/status/gw-1/task-1", ""},
		{"ns/a2a/v1/agent/request/MyAgent/task-1", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := agentFromTopic(tt.topic); got != tt.want {
			t.Errorf("agentFromTopic(%q) = %q, want %q", tt.topic, got, tt.want)
		}
	}
}

func TestFirstDataPartType_NoDataParts(t *testing.T) {
	parts := []any{
		map[string]any{"kind": "text", "text": "hi"},
		map[string]any{"kind": "file", "file": map[string]any{"name": "x.json"}},
	}
	if _, ok := firstDataPartType(parts); ok {
		t.Error("expected no data part type for text/file-only parts")
	}
}

func TestInferEventDetails_ArtifactUpdate(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"kind": "artifact-update",
			"artifact": map[string]any{
				"metadata": map[string]any{
					"agent_name": "ArtifactAgent",
				},
			},
		},
	}
	evt := InferEventDetails("test-ns/a2a/v1/status/gw-1/task-1", payload)
	if evt.SourceEntity != "ArtifactAgent" {
		t.Errorf("SourceEntity = %q, want ArtifactAgent", evt.SourceEntity)
	}
}

func TestInferEventDetails_ResponseWithTaskID_Variants(t *testing.T) {
	t.Run("task_id snake_case", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"kind":    "task",
				"task_id": "task-snake",
			},
		}
		evt := InferEventDetails("topic", payload)
		if evt.TaskID != "task-snake" {
			t.Errorf("TaskID = %v, want task-snake", evt.TaskID)
		}
	})

	t.Run("id fallback", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"kind": "task",
				"id":   "task-id",
			},
		}
		evt := InferEventDetails("topic", payload)
		if evt.TaskID != "task-id" {
			t.Errorf("TaskID = %v, want task-id", evt.TaskID)
		}
	})

	t.Run("non-map result fallback to topic", func(t *testing.T) {
		payload := map[string]any{
			"result": "just a string",
		}
		evt := InferEventDetails("test-ns/a2a/v1/agent/response/Agent/task-1", payload)
		if evt.Direction != "response" {
			t.Errorf("Direction = %q, want response", evt.Direction)
		}
	})
}

func TestBuildPayloadSummary(t *testing.T) {
	t.Run("with method and params", func(t *testing.T) {
		payload := map[string]any{
			"method": "message/send",
			"params": map[string]any{"text": "hello"},
		}
		evt := InferEventDetails("topic", payload)
		if evt.PayloadSummary == nil {
			t.Fatal("expected PayloadSummary")
		}
		if evt.PayloadSummary.Method != "message/send" {
			t.Errorf("Method = %q", evt.PayloadSummary.Method)
		}
		if evt.PayloadSummary.ParamsPreview == nil {
			t.Fatal("expected ParamsPreview")
		}
		if !strings.Contains(*evt.PayloadSummary.ParamsPreview, "hello") {
			t.Errorf("ParamsPreview = %q, want to contain 'hello'", *evt.PayloadSummary.ParamsPreview)
		}
	})

	t.Run("no method defaults to N/A", func(t *testing.T) {
		evt := InferEventDetails("topic", map[string]any{})
		if evt.PayloadSummary == nil {
			t.Fatal("expected PayloadSummary")
		}
		if evt.PayloadSummary.Method != "N/A" {
			t.Errorf("Method = %q, want N/A", evt.PayloadSummary.Method)
		}
	})

	t.Run("long preview truncated", func(t *testing.T) {
		longText := strings.Repeat("x", 200)
		payload := map[string]any{
			"method": "test",
			"params": map[string]any{"data": longText},
		}
		evt := InferEventDetails("topic", payload)
		if evt.PayloadSummary.ParamsPreview == nil {
			t.Fatal("expected ParamsPreview")
		}
		if len(*evt.PayloadSummary.ParamsPreview) > 110 {
			t.Errorf("preview length = %d, expected truncated to ~103", len(*evt.PayloadSummary.ParamsPreview))
		}
		if !strings.HasSuffix(*evt.PayloadSummary.ParamsPreview, "...") {
			t.Error("expected truncated preview to end with '...'")
		}
	})

	t.Run("error response preview", func(t *testing.T) {
		payload := map[string]any{
			"error": map[string]any{"code": -32600, "message": "bad"},
		}
		evt := InferEventDetails("topic", payload)
		if evt.PayloadSummary.ParamsPreview == nil {
			t.Fatal("expected ParamsPreview for error")
		}
		if !strings.Contains(*evt.PayloadSummary.ParamsPreview, "bad") {
			t.Errorf("ParamsPreview = %q, want to contain 'bad'", *evt.PayloadSummary.ParamsPreview)
		}
	})
}

func TestExtractEventUserID(t *testing.T) {
	t.Run("request metadata", func(t *testing.T) {
		payload := map[string]any{
			"params": map[string]any{
				"message": map[string]any{
					"metadata": map[string]any{"user_id": "alice@example.com"},
				},
			},
		}
		if got := ExtractEventUserID(nil, payload); got != "alice@example.com" {
			t.Errorf("got %q, want alice@example.com", got)
		}
	})

	t.Run("response metadata", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"metadata": map[string]any{"user_id": "bob@example.com"},
			},
		}
		if got := ExtractEventUserID(nil, payload); got != "bob@example.com" {
			t.Errorf("got %q, want bob@example.com", got)
		}
	})

	t.Run("status update metadata (Python-parity fallback)", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"status": map[string]any{
					"message": map[string]any{
						"metadata": map[string]any{"user_id": "carol@example.com"},
					},
				},
			},
		}
		if got := ExtractEventUserID(nil, payload); got != "carol@example.com" {
			t.Errorf("got %q, want carol@example.com", got)
		}
	})

	t.Run("broker user properties", func(t *testing.T) {
		props := map[string]string{"userId": "dave@example.com"}
		if got := ExtractEventUserID(props, map[string]any{}); got != "dave@example.com" {
			t.Errorf("got %q, want dave@example.com", got)
		}
	})

	t.Run("missing", func(t *testing.T) {
		if got := ExtractEventUserID(nil, map[string]any{}); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("empty user_id on request falls through to UserProperties", func(t *testing.T) {
		payload := map[string]any{
			"params": map[string]any{
				"message": map[string]any{
					"metadata": map[string]any{"user_id": ""},
				},
			},
		}
		props := map[string]string{"userId": "eve@example.com"}
		if got := ExtractEventUserID(props, payload); got != "eve@example.com" {
			t.Errorf("got %q, want eve@example.com (UserProperties wins)", got)
		}
	})

	t.Run("empty user_id on response falls through to UserProperties", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"metadata": map[string]any{"user_id": ""},
			},
		}
		props := map[string]string{"userId": "frank@example.com"}
		if got := ExtractEventUserID(props, payload); got != "frank@example.com" {
			t.Errorf("got %q, want frank@example.com (UserProperties wins)", got)
		}
	})

	t.Run("empty user_id on status message falls through to UserProperties", func(t *testing.T) {
		payload := map[string]any{
			"result": map[string]any{
				"status": map[string]any{
					"message": map[string]any{
						"metadata": map[string]any{"user_id": ""},
					},
				},
			},
		}
		props := map[string]string{"userId": "grace@example.com"}
		if got := ExtractEventUserID(props, payload); got != "grace@example.com" {
			t.Errorf("got %q, want grace@example.com (UserProperties wins)", got)
		}
	})

	t.Run("request metadata used when UserProperties absent", func(t *testing.T) {
		payload := map[string]any{
			"params": map[string]any{
				"message": map[string]any{
					"metadata": map[string]any{"user_id": "alice"},
				},
			},
			"result": map[string]any{
				"metadata": map[string]any{"user_id": "bob"},
			},
		}
		if got := ExtractEventUserID(nil, payload); got != "alice" {
			t.Errorf("got %q, want alice (request branch first when UserProperties absent)", got)
		}
	})

	t.Run("UserProperties wins over client-supplied payload metadata", func(t *testing.T) {
		payload := map[string]any{
			"params": map[string]any{
				"message": map[string]any{
					"metadata": map[string]any{"user_id": "victim@example.com"},
				},
			},
		}
		props := map[string]string{"userId": "attacker@example.com"}
		if got := ExtractEventUserID(props, payload); got != "attacker@example.com" {
			t.Errorf("got %q, want attacker@example.com (UserProperties must win over untrusted client metadata)", got)
		}
	})
}

func TestGetNestedString(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep-value",
			},
		},
		"flat": "top-level",
	}

	if got := getNestedString(m, "a", "b", "c"); got != "deep-value" {
		t.Errorf("got %q, want deep-value", got)
	}
	if got := getNestedString(m, "flat"); got != "top-level" {
		t.Errorf("got %q, want top-level", got)
	}
	if got := getNestedString(m, "a", "missing", "c"); got != "" {
		t.Errorf("got %q, want empty for missing key", got)
	}
	if got := getNestedString(m, "a", "b", "c", "extra"); got != "" {
		t.Errorf("got %q, want empty for non-map intermediate", got)
	}
}

func TestGetNestedParts(t *testing.T) {
	m := map[string]any{
		"status": map[string]any{
			"message": map[string]any{
				"parts": []any{"a", "b"},
			},
		},
	}

	parts := getNestedParts(m, "status", "message", "parts")
	if len(parts) != 2 {
		t.Errorf("got %d parts, want 2", len(parts))
	}

	if got := getNestedParts(m, "missing", "path"); got != nil {
		t.Errorf("got %v, want nil for missing path", got)
	}
}
