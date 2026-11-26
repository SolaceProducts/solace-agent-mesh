// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"testing"
)

func strPtr(s string) *string { return &s }

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func mustUnmarshal[T any](t *testing.T, data string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return v
}

func TestMessageJSON(t *testing.T) {
	msg := Message{
		Kind:      "message",
		MessageID: "msg-123",
		Role:      RoleAgent,
		Parts:     []Part{NewTextPart("Hello")},
		TaskID:    "task-456",
		ContextID: "ctx-789",
	}

	data := mustMarshal(t, msg)

	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kind", "messageId", "role", "parts", "taskId", "contextId"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing expected JSON key %q", key)
		}
	}
	if _, ok := raw["metadata"]; ok {
		t.Error("metadata should be omitted when nil")
	}

	got := mustUnmarshal[Message](t, data)
	if got.MessageID != "msg-123" || got.Role != RoleAgent || got.TaskID != "task-456" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.Parts) != 1 || got.Parts[0].Kind != "text" || got.Parts[0].Text != "Hello" {
		t.Errorf("parts mismatch: %+v", got.Parts)
	}
}

func TestMessageFromPythonJSON(t *testing.T) {
	pyJSON := `{
		"kind": "message",
		"messageId": "msg-abc",
		"role": "user",
		"parts": [
			{"kind": "text", "text": "What is the weather?"},
			{"kind": "data", "data": {"type": "agent_progress_update", "status_text": "thinking..."}}
		],
		"taskId": "task-001",
		"contextId": "ctx-001"
	}`

	msg := mustUnmarshal[Message](t, pyJSON)
	if msg.Kind != "message" || msg.MessageID != "msg-abc" || msg.Role != RoleUser {
		t.Errorf("header mismatch: %+v", msg)
	}
	if len(msg.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(msg.Parts))
	}
	if msg.Parts[0].Kind != "text" || msg.Parts[0].Text != "What is the weather?" {
		t.Errorf("text part mismatch: %+v", msg.Parts[0])
	}
	if msg.Parts[1].Kind != "data" || msg.Parts[1].Data == nil {
		t.Errorf("data part mismatch: %+v", msg.Parts[1])
	}
}

func TestPartTypes(t *testing.T) {
	tests := []struct {
		name string
		part Part
		key  string
	}{
		{"text", NewTextPart("hello"), "text"},
		{"data", NewDataPart(map[string]any{"type": "test"}), "data"},
		{"file", NewFilePart("s3://bucket/key", "report.pdf", "application/pdf"), "file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := mustMarshal(t, tt.part)
			var raw map[string]any
			if err := json.Unmarshal([]byte(data), &raw); err != nil {
				t.Fatal(err)
			}
			if raw["kind"] != tt.name {
				t.Errorf("kind = %v, want %v", raw["kind"], tt.name)
			}
			if _, ok := raw[tt.key]; !ok {
				t.Errorf("missing expected key %q in %s", tt.key, data)
			}
		})
	}
}

func TestTextPartEmptyString(t *testing.T) {
	part := NewTextPart("")
	data := mustMarshal(t, part)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["text"]; !ok {
		t.Error("text part with empty string must still include 'text' key")
	}
	if raw["text"] != "" {
		t.Errorf("text = %v, want empty string", raw["text"])
	}
	for _, key := range []string{"data", "file"} {
		if _, ok := raw[key]; ok {
			t.Errorf("text part should not include %q key", key)
		}
	}
}

func TestFilePartJSON(t *testing.T) {
	fp := NewFilePart("https://example.com/file.txt", "file.txt", "text/plain")
	data := mustMarshal(t, fp)

	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	file := raw["file"].(map[string]any)
	if file["uri"] != "https://example.com/file.txt" {
		t.Errorf("uri = %v", file["uri"])
	}
	if file["name"] != "file.txt" {
		t.Errorf("name = %v", file["name"])
	}
	if file["mimeType"] != "text/plain" {
		t.Errorf("mimeType = %v", file["mimeType"])
	}
}

func TestTaskJSON(t *testing.T) {
	task := Task{
		ID:        "task-001",
		ContextID: "ctx-001",
		Kind:      "task",
		Status: TaskStatus{
			State: TaskStateSubmitted,
		},
		History: []Message{
			{
				Kind:      "message",
				MessageID: "msg-1",
				Role:      RoleUser,
				Parts:     []Part{NewTextPart("Hello")},
			},
		},
	}

	data := mustMarshal(t, task)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}

	if raw["id"] != "task-001" {
		t.Errorf("id = %v", raw["id"])
	}
	if raw["kind"] != "task" {
		t.Errorf("kind = %v", raw["kind"])
	}
	status := raw["status"].(map[string]any)
	if status["state"] != "submitted" {
		t.Errorf("state = %v", status["state"])
	}

	got := mustUnmarshal[Task](t, data)
	if got.ID != "task-001" || got.Kind != "task" || got.Status.State != TaskStateSubmitted {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.History) != 1 {
		t.Errorf("history length = %d", len(got.History))
	}
}

func TestTaskFromPythonJSON(t *testing.T) {
	pyJSON := `{
		"kind": "task",
		"id": "task-abc",
		"contextId": "ctx-001",
		"status": {
			"state": "working",
			"message": {
				"kind": "message",
				"messageId": "msg-status",
				"role": "agent",
				"parts": [{"kind": "text", "text": "Processing..."}]
			}
		},
		"metadata": {"agent_name": "SearchAgent"}
	}`

	task := mustUnmarshal[Task](t, pyJSON)
	if task.ID != "task-abc" || task.Status.State != TaskStateWorking {
		t.Errorf("task mismatch: %+v", task)
	}
	if task.Status.Message == nil {
		t.Fatal("status message is nil")
	}
	if task.Status.Message.Parts[0].Text != "Processing..." {
		t.Errorf("status message text = %v", task.Status.Message.Parts[0].Text)
	}
	if task.Metadata["agent_name"] != "SearchAgent" {
		t.Errorf("metadata = %v", task.Metadata)
	}
}

func TestTaskStates(t *testing.T) {
	states := []TaskState{
		TaskStateSubmitted, TaskStateWorking, TaskStateCompleted,
		TaskStateFailed, TaskStateCancelled, TaskStateInputNeeded,
	}
	expected := []string{
		"submitted", "working", "completed", "failed", "canceled", "input-required",
	}
	for i, s := range states {
		if string(s) != expected[i] {
			t.Errorf("state %d = %q, want %q", i, s, expected[i])
		}
	}
}

func TestRequestJSON(t *testing.T) {
	req := NewRequest("task-001", "message/send", map[string]any{
		"message": map[string]any{"kind": "message"},
	})

	data := mustMarshal(t, req)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}

	if raw["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v", raw["jsonrpc"])
	}
	if raw["id"] != "task-001" {
		t.Errorf("id = %v", raw["id"])
	}
	if raw["method"] != "message/send" {
		t.Errorf("method = %v", raw["method"])
	}
}

func TestCancelTaskRequestWireFormat(t *testing.T) {
	req := NewCancelTaskRequest("task-001")

	if req.Method != "tasks/cancel" {
		t.Errorf("method = %q, want tasks/cancel", req.Method)
	}
	if req.ID != "task-001" {
		t.Errorf("id = %q, want task-001", req.ID)
	}

	data := mustMarshal(t, req)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v", raw["jsonrpc"])
	}
	if raw["method"] != "tasks/cancel" {
		t.Errorf("method = %v", raw["method"])
	}
	params, ok := raw["params"].(map[string]any)
	if !ok {
		t.Fatalf("params not an object: %T", raw["params"])
	}
	if params["id"] != "task-001" {
		t.Errorf("params.id = %v, want task-001", params["id"])
	}
}

func TestResponseJSON(t *testing.T) {
	resp := NewResponse("task-001", map[string]any{"status": "ok"})
	data := mustMarshal(t, resp)

	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v", raw["jsonrpc"])
	}
	if _, ok := raw["error"]; ok {
		t.Error("error should be omitted on success")
	}
}

func TestErrorResponseJSON(t *testing.T) {
	resp := NewErrorResponse("task-001", -32600, "Invalid Request")
	data := mustMarshal(t, resp)

	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["result"]; ok {
		t.Error("result should be omitted on error")
	}
	errObj := raw["error"].(map[string]any)
	if errObj["code"].(float64) != -32600 {
		t.Errorf("error code = %v", errObj["code"])
	}
	if errObj["message"] != "Invalid Request" {
		t.Errorf("error message = %v", errObj["message"])
	}
}

func TestTaskStatusUpdateEventJSON(t *testing.T) {
	evt := TaskStatusUpdateEvent{
		TaskID:    "task-001",
		ContextID: "ctx-001",
		Kind:      "status-update",
		Status:    TaskStatus{State: TaskStateWorking},
		Final:     false,
	}

	data := mustMarshal(t, evt)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["kind"] != "status-update" {
		t.Errorf("kind = %v", raw["kind"])
	}
	if raw["taskId"] != "task-001" {
		t.Errorf("taskId = %v", raw["taskId"])
	}

	got := mustUnmarshal[TaskStatusUpdateEvent](t, data)
	if got.TaskID != "task-001" || got.Status.State != TaskStateWorking {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestTaskArtifactUpdateEventJSON(t *testing.T) {
	evt := TaskArtifactUpdateEvent{
		TaskID:    "task-001",
		ContextID: "ctx-001",
		Kind:      "artifact-update",
		Artifact: Artifact{
			ArtifactID: "art-1",
			Name:       "report.pdf",
			Parts:      []Part{NewFilePart("s3://bucket/report.pdf", "report.pdf", "application/pdf")},
		},
		LastChunk: true,
	}

	data := mustMarshal(t, evt)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["kind"] != "artifact-update" {
		t.Errorf("kind = %v", raw["kind"])
	}
	art := raw["artifact"].(map[string]any)
	if art["artifactId"] != "art-1" {
		t.Errorf("artifactId = %v", art["artifactId"])
	}
}

func TestAgentCardJSON(t *testing.T) {
	card := AgentCard{
		Name:            "SearchAgent",
		Description:     "Searches the web",
		URL:             "solace:ns/a2a/v1/agent/request/search-agent",
		Version:         "1.0.0",
		ProtocolVersion: "0.3.0",
		Capabilities: AgentCapabilities{
			Streaming: true,
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills: []AgentSkill{
			{
				ID:          "web-search",
				Name:        "Web Search",
				Description: "Search the web for information",
				Tags:        []string{"search", "web"},
			},
		},
		Provider: &AgentProvider{
			Organization: "Acme Corp",
		},
	}

	data := mustMarshal(t, card)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}

	if raw["protocolVersion"] != "0.3.0" {
		t.Errorf("protocolVersion = %v", raw["protocolVersion"])
	}
	caps := raw["capabilities"].(map[string]any)
	if caps["streaming"] != true {
		t.Errorf("streaming = %v", caps["streaming"])
	}

	got := mustUnmarshal[AgentCard](t, data)
	if got.Name != "SearchAgent" || got.Version != "1.0.0" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.Skills) != 1 || got.Skills[0].ID != "web-search" {
		t.Errorf("skills mismatch: %+v", got.Skills)
	}
}

func TestToolInvocationStartDataJSON(t *testing.T) {
	d := ToolInvocationStartData{
		Type:            "tool_invocation_start",
		ToolName:        "web_search",
		ToolArgs:        map[string]any{"query": "golang"},
		FunctionCallID:  "fc-1",
		ParallelGroupID: strPtr("pg-1"),
	}

	data := mustMarshal(t, d)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["type"] != "tool_invocation_start" {
		t.Errorf("type = %v", raw["type"])
	}
	if raw["tool_name"] != "web_search" {
		t.Errorf("tool_name = %v", raw["tool_name"])
	}
	if raw["parallel_group_id"] != "pg-1" {
		t.Errorf("parallel_group_id = %v", raw["parallel_group_id"])
	}
}

func TestStructuredInvocationRequestJSON(t *testing.T) {
	sir := StructuredInvocationRequest{
		Type:         "structured_invocation_request",
		WorkflowName: "data-pipeline",
		NodeID:       "node-1",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
	}

	data := mustMarshal(t, sir)
	got := mustUnmarshal[StructuredInvocationRequest](t, data)
	if got.WorkflowName != "data-pipeline" || got.NodeID != "node-1" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestStructuredInvocationResultJSON(t *testing.T) {
	sir := StructuredInvocationResult{
		Type:   "structured_invocation_result",
		Status: "success",
		OutputArtifactRef: &ArtifactRef{
			Name:    "output.json",
			Version: 1,
		},
	}

	data := mustMarshal(t, sir)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	ref := raw["output_artifact_ref"].(map[string]any)
	if ref["name"] != "output.json" {
		t.Errorf("name = %v", ref["name"])
	}
}

func TestUserPropertiesJSON(t *testing.T) {
	up := UserProperties{
		Timestamp:      1700000000000,
		ClientID:       "gw-1",
		ReplyTo:        "ns/a2a/v1/gateway/response/gw-1/task-1",
		A2AStatusTopic: "ns/a2a/v1/gateway/status/gw-1/task-1",
		CallDepth:      2,
	}

	data := mustMarshal(t, up)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["timestamp"].(float64) != 1700000000000 {
		t.Errorf("timestamp = %v", raw["timestamp"])
	}
	if raw["replyTo"] != "ns/a2a/v1/gateway/response/gw-1/task-1" {
		t.Errorf("replyTo = %v", raw["replyTo"])
	}
}

func TestEncodeUserProperties(t *testing.T) {
	up := &UserProperties{
		Timestamp:      1700000000000,
		ClientID:       "gw-1",
		ReplyTo:        "ns/a2a/v1/gateway/response/gw-1/task-1",
		A2AStatusTopic: "ns/a2a/v1/gateway/status/gw-1/task-1",
		A2AUserConfig:  `{"model":"gpt-4"}`,
		CallDepth:      3,
	}

	m := EncodeUserProperties(up)
	if m[PropTimestamp] != "1700000000000" {
		t.Errorf("timestamp = %q", m[PropTimestamp])
	}
	if m[PropClientID] != "gw-1" {
		t.Errorf("clientId = %q", m[PropClientID])
	}
	if m[PropReplyTo] != "ns/a2a/v1/gateway/response/gw-1/task-1" {
		t.Errorf("replyTo = %q", m[PropReplyTo])
	}
	if m[PropCallDepth] != "3" {
		t.Errorf("callDepth = %q", m[PropCallDepth])
	}
}

func TestDecodeUserProperties(t *testing.T) {
	m := map[string]string{
		PropTimestamp:      "1700000000000",
		PropClientID:       "gw-1",
		PropReplyTo:        "reply-topic",
		PropA2AStatusTopic: "status-topic",
		PropA2AUserConfig:  `{"model":"gpt-4"}`,
		PropCallDepth:      "2",
	}

	up := DecodeUserProperties(m)
	if up.Timestamp != 1700000000000 {
		t.Errorf("timestamp = %d", up.Timestamp)
	}
	if up.ClientID != "gw-1" {
		t.Errorf("clientId = %q", up.ClientID)
	}
	if up.ReplyTo != "reply-topic" {
		t.Errorf("replyTo = %q", up.ReplyTo)
	}
	if up.A2AUserConfig != `{"model":"gpt-4"}` {
		t.Errorf("a2aUserConfig = %q", up.A2AUserConfig)
	}
	if up.CallDepth != 2 {
		t.Errorf("callDepth = %d", up.CallDepth)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	original := &UserProperties{
		Timestamp:      1700000000000,
		ClientID:       "gw-1",
		ReplyTo:        "reply-topic",
		A2AStatusTopic: "status-topic",
		CallDepth:      5,
		TraceID:        "018f3a4b-7c9d-7e21-8b1f-2d4e5f6a7b8c",
	}

	m := EncodeUserProperties(original)
	decoded := DecodeUserProperties(m)

	if decoded.Timestamp != original.Timestamp ||
		decoded.ClientID != original.ClientID ||
		decoded.ReplyTo != original.ReplyTo ||
		decoded.A2AStatusTopic != original.A2AStatusTopic ||
		decoded.CallDepth != original.CallDepth ||
		decoded.TraceID != original.TraceID {
		t.Errorf("round-trip mismatch:\n  original: %+v\n  decoded:  %+v", original, decoded)
	}
}

func TestEncodeDecodeTraceID(t *testing.T) {
	const traceID = "018f3a4b-7c9d-7e21-8b1f-2d4e5f6a7b8c"
	up := &UserProperties{Timestamp: 1, TraceID: traceID}

	m := EncodeUserProperties(up)
	if m[PropTraceID] != traceID {
		t.Fatalf("traceId encode = %q, want %q", m[PropTraceID], traceID)
	}
	if got := DecodeUserProperties(m); got.TraceID != traceID {
		t.Fatalf("traceId decode = %q, want %q", got.TraceID, traceID)
	}

	bare := DecodeUserProperties(map[string]string{PropTimestamp: "1"})
	if bare.TraceID != "" {
		t.Fatalf("empty decode should leave TraceID blank, got %q", bare.TraceID)
	}
	bareEnc := EncodeUserProperties(&UserProperties{Timestamp: 1})
	if _, ok := bareEnc[PropTraceID]; ok {
		t.Fatalf("empty TraceID should be omitted from encode, got %q", bareEnc[PropTraceID])
	}
}

func TestEncodeDecodeA2AExtensions(t *testing.T) {
	const uri = "https://solace.com/sam/ext/session-control/v1"
	up := &UserProperties{
		Timestamp:     1,
		A2AExtensions: uri,
	}
	m := EncodeUserProperties(up)
	if m[PropA2AExtensions] != uri {
		t.Fatalf("a2aExtensions encode = %q, want %q", m[PropA2AExtensions], uri)
	}
	got := DecodeUserProperties(m)
	if got.A2AExtensions != uri {
		t.Fatalf("a2aExtensions decode = %q, want %q", got.A2AExtensions, uri)
	}
	bare := DecodeUserProperties(map[string]string{PropTimestamp: "1"})
	if bare.A2AExtensions != "" {
		t.Fatalf("empty decode should leave A2AExtensions blank, got %q", bare.A2AExtensions)
	}
}

func TestEncodeOmitsEmptyFields(t *testing.T) {
	up := &UserProperties{
		Timestamp: 1000,
	}
	m := EncodeUserProperties(up)

	if _, ok := m[PropClientID]; ok {
		t.Error("clientId should be omitted when empty")
	}
	if _, ok := m[PropCallDepth]; ok {
		t.Error("callDepth should be omitted when zero")
	}
	if _, ok := m[PropTimestamp]; !ok {
		t.Error("timestamp should always be present")
	}
}

func TestDecodeEmptyMap(t *testing.T) {
	up := DecodeUserProperties(map[string]string{})
	if up.Timestamp != 0 || up.ClientID != "" || up.CallDepth != 0 {
		t.Errorf("expected zero values, got %+v", up)
	}
}

func TestOmitEmptyFields(t *testing.T) {
	msg := Message{
		Kind:      "message",
		MessageID: "msg-1",
		Role:      RoleUser,
		Parts:     []Part{NewTextPart("hi")},
	}
	data := mustMarshal(t, msg)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"taskId", "contextId", "metadata"} {
		if _, ok := raw[key]; ok {
			t.Errorf("expected %q to be omitted, but it's present", key)
		}
	}

	task := Task{
		ID:   "t-1",
		Kind: "task",
		Status: TaskStatus{
			State: TaskStateSubmitted,
		},
	}
	data = mustMarshal(t, task)
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"history", "artifacts", "metadata"} {
		if _, ok := raw[key]; ok {
			t.Errorf("expected %q to be omitted, but it's present", key)
		}
	}
	if _, ok := raw["contextId"]; !ok {
		t.Error("contextId should always be present (even if empty)")
	}
}

func TestUserInputRequestData_RoundTrip(t *testing.T) {
	req := UserInputRequestData{
		Type:      "user_input_request",
		RequestID: "hil-abc123",
		ExpiresAt: "2026-02-28T16:45:00Z",
		Source:    "ask_user_question",
		Surface:   json.RawMessage(`{"surfaceId":"hil-abc123","components":[]}`),
	}

	data := mustMarshal(t, req)
	got := mustUnmarshal[UserInputRequestData](t, data)

	if got.Type != "user_input_request" {
		t.Errorf("type = %q, want user_input_request", got.Type)
	}
	if got.RequestID != "hil-abc123" {
		t.Errorf("request_id = %q, want hil-abc123", got.RequestID)
	}
	if got.ExpiresAt != "2026-02-28T16:45:00Z" {
		t.Errorf("expires_at = %q", got.ExpiresAt)
	}
	if got.Source != "ask_user_question" {
		t.Errorf("source = %q", got.Source)
	}
	if string(got.Surface) != `{"surfaceId":"hil-abc123","components":[]}` {
		t.Errorf("surface = %s", got.Surface)
	}
}

func TestUserInputResponseData_RoundTrip(t *testing.T) {
	resp := UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "hil-abc123",
		Status:    "answered",
		Data:      map[string]any{"answers": map[string]any{"q0": "vol-xyz"}},
	}

	data := mustMarshal(t, resp)
	got := mustUnmarshal[UserInputResponseData](t, data)

	if got.Type != "user_input_response" {
		t.Errorf("type = %q", got.Type)
	}
	if got.RequestID != "hil-abc123" {
		t.Errorf("request_id = %q", got.RequestID)
	}
	if got.Status != "answered" {
		t.Errorf("status = %q", got.Status)
	}
	answers, ok := got.Data["answers"].(map[string]any)
	if !ok {
		t.Fatal("data.answers is not a map")
	}
	if answers["q0"] != "vol-xyz" {
		t.Errorf("answers.q0 = %v", answers["q0"])
	}
}

func TestUserInputResponseData_Cancelled(t *testing.T) {
	resp := UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "hil-def456",
		Status:    "cancelled",
	}

	data := mustMarshal(t, resp)
	got := mustUnmarshal[UserInputResponseData](t, data)

	if got.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
	if got.Data != nil {
		t.Errorf("data should be nil for cancelled, got %v", got.Data)
	}
}

func TestArtifactTagConstants(t *testing.T) {
	if ArtifactTagUserUploaded != "__user_uploaded" {
		t.Errorf("ArtifactTagUserUploaded = %q, want __user_uploaded", ArtifactTagUserUploaded)
	}
	if ArtifactTagWorking != "__working" {
		t.Errorf("ArtifactTagWorking = %q, want __working", ArtifactTagWorking)
	}
}

func TestArtifactTagsSerialization(t *testing.T) {
	art := Artifact{
		Name: "report.csv",
		Tags: []string{ArtifactTagWorking, "custom_tag"},
	}
	data := mustMarshal(t, art)
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	tags, ok := raw["tags"].([]any)
	if !ok {
		t.Fatalf("expected tags to be []any, got %T", raw["tags"])
	}
	if len(tags) != 2 || tags[0] != "__working" || tags[1] != "custom_tag" {
		t.Errorf("tags = %v, want [__working, custom_tag]", tags)
	}
}

func TestArtifactTagsOmittedWhenEmpty(t *testing.T) {
	art := Artifact{
		Name: "report.csv",
	}
	data := mustMarshal(t, art)
	var raw map[string]any
	_ = json.Unmarshal([]byte(data), &raw)
	if _, ok := raw["tags"]; ok {
		t.Error("tags should be omitted when nil/empty")
	}
}

func TestArtifactCreationProgressDataTagsSerialization(t *testing.T) {
	progress := ArtifactCreationProgressData{
		Type:     string(SignalArtifactCreationProgress),
		Filename: "data.json",
		Status:   "completed",
		Tags:     []string{ArtifactTagUserUploaded},
	}
	data := mustMarshal(t, progress)
	var raw map[string]any
	_ = json.Unmarshal([]byte(data), &raw)
	tags, ok := raw["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "__user_uploaded" {
		t.Errorf("ArtifactCreationProgressData tags = %v", raw["tags"])
	}
}

func TestArtifactSavedDataTagsSerialization(t *testing.T) {
	saved := ArtifactSavedData{
		Type:      string(SignalArtifactSaved),
		Filename:  "data.json",
		Version:   1,
		MIMEType:  "application/json",
		SizeBytes: 256,
		Tags:      []string{ArtifactTagWorking},
	}
	data := mustMarshal(t, saved)
	var raw map[string]any
	_ = json.Unmarshal([]byte(data), &raw)
	tags, ok := raw["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "__working" {
		t.Errorf("ArtifactSavedData tags = %v", raw["tags"])
	}
}

func TestArtifactTagsRoundTrip(t *testing.T) {
	original := Artifact{
		Name: "test.txt",
		Tags: []string{ArtifactTagUserUploaded, ArtifactTagWorking},
	}
	data := mustMarshal(t, original)
	got := mustUnmarshal[Artifact](t, data)
	if len(got.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(got.Tags))
	}
	if got.Tags[0] != ArtifactTagUserUploaded || got.Tags[1] != ArtifactTagWorking {
		t.Errorf("tags round-trip mismatch: %v", got.Tags)
	}
}

func TestBuilderComponentProgressData_GetSignalType(t *testing.T) {
	t.Parallel()
	d := BuilderComponentProgressData{
		Type:          string(SignalBuilderComponentProgress),
		ComponentID:   "c-1",
		ComponentType: "agent",
		ComponentName: "Web Search",
		State:         "in_progress",
	}
	if got := d.GetSignalType(); got != string(SignalBuilderComponentProgress) {
		t.Errorf("GetSignalType() = %q, want %q", got, string(SignalBuilderComponentProgress))
	}
}

func TestIsEphemeralTestAgentName(t *testing.T) {
	t.Parallel()
	if !IsEphemeralTestAgentName(EphemeralTestAgentNamePrefix + "abc__user") {
		t.Error("expected ephemeral-prefixed name to be detected")
	}
	if IsEphemeralTestAgentName("RegularAgent") {
		t.Error("non-prefixed name must not be detected as ephemeral")
	}
	if IsEphemeralTestAgentName("") {
		t.Error("empty name must not be detected as ephemeral")
	}
}

func TestSetForwardedPeerChain(t *testing.T) {
	chainOf := func(m map[string]any) []any {
		c, _ := m["peer_task_chain"].([]any)
		return c
	}
	eqChain := func(got []any, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	t.Run("first hop seeds leaf and chain", func(t *testing.T) {
		newMeta := map[string]any{}
		SetForwardedPeerChain(nil, newMeta, "task-C", "task-B")
		if newMeta["original_peer_event_taskId"] != "task-C" {
			t.Errorf("leaf = %v, want task-C", newMeta["original_peer_event_taskId"])
		}
		if !eqChain(chainOf(newMeta), "task-C") {
			t.Errorf("chain = %v, want [task-C]", chainOf(newMeta))
		}
	})

	t.Run("re-forward preserves leaf and extends chain", func(t *testing.T) {
		incoming := map[string]any{
			"original_peer_event_taskId": "task-C",
			"peer_task_chain":            []any{"task-C"},
		}
		newMeta := map[string]any{}
		SetForwardedPeerChain(incoming, newMeta, "task-B", "task-A")
		if newMeta["original_peer_event_taskId"] != "task-C" {
			t.Errorf("leaf = %v, want task-C (deepest preserved)", newMeta["original_peer_event_taskId"])
		}
		if !eqChain(chainOf(newMeta), "task-C", "task-B") {
			t.Errorf("chain = %v, want [task-C task-B]", chainOf(newMeta))
		}
	})

	t.Run("self-forward sets nothing", func(t *testing.T) {
		newMeta := map[string]any{}
		SetForwardedPeerChain(nil, newMeta, "task-A", "task-A")
		if _, ok := newMeta["original_peer_event_taskId"]; ok {
			t.Errorf("leaf set on self-forward: %v", newMeta["original_peer_event_taskId"])
		}
		if _, ok := newMeta["peer_task_chain"]; ok {
			t.Errorf("chain set on self-forward: %v", newMeta["peer_task_chain"])
		}
	})

	t.Run("malformed incoming chain is ignored, not propagated", func(t *testing.T) {
		incoming := map[string]any{"peer_task_chain": "not-a-slice"}
		newMeta := map[string]any{}
		SetForwardedPeerChain(incoming, newMeta, "task-B", "task-A")
		if !eqChain(chainOf(newMeta), "task-B") {
			t.Errorf("chain = %v, want [task-B] (bad incoming chain dropped)", chainOf(newMeta))
		}
	})

	t.Run("empty origTaskID with incoming chain keeps chain only", func(t *testing.T) {
		incoming := map[string]any{"peer_task_chain": []any{"task-C", "task-B"}}
		newMeta := map[string]any{}
		SetForwardedPeerChain(incoming, newMeta, "", "task-A")
		if !eqChain(chainOf(newMeta), "task-C", "task-B") {
			t.Errorf("chain = %v, want [task-C task-B] (no empty hop appended)", chainOf(newMeta))
		}
	})
}

func TestAuthenticationRequiredData_ToolDisplayNameOmitempty(t *testing.T) {
	decode := func(v AuthenticationRequiredData) map[string]any {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	if _, present := decode(AuthenticationRequiredData{Type: "authentication_required"})["tool_display_name"]; present {
		t.Error("tool_display_name should be omitted when empty (Python parity)")
	}

	m := decode(AuthenticationRequiredData{Type: "authentication_required", ToolDisplayName: "Atlassian"})
	if m["tool_display_name"] != "Atlassian" {
		t.Errorf("tool_display_name = %v, want Atlassian", m["tool_display_name"])
	}
}

func TestAuthenticationRequiredFromStatus(t *testing.T) {
	authEvent := func(uri string) *TaskStatusUpdateEvent {
		return &TaskStatusUpdateEvent{
			Kind: "status-update",
			Status: TaskStatus{Message: &Message{
				Role: "agent",
				Parts: []Part{NewDataPart(map[string]any{
					"type":         "authentication_required",
					"target_agent": "agent-x",
					"auth_uri":     uri,
				})},
			}},
		}
	}

	t.Run("present", func(t *testing.T) {
		got, ok := AuthenticationRequiredFromStatus(authEvent("https://idp.example/authorize?state=s1"))
		if !ok || got.AuthURI != "https://idp.example/authorize?state=s1" || got.TargetAgent != "agent-x" {
			t.Errorf("got (%+v, %v)", got, ok)
		}
	})
	t.Run("plain text status has none", func(t *testing.T) {
		ev := &TaskStatusUpdateEvent{Kind: "status-update", Status: TaskStatus{Message: &Message{Parts: []Part{NewTextPart("thinking")}}}}
		if _, ok := AuthenticationRequiredFromStatus(ev); ok {
			t.Error("expected no signal for a plain text status update")
		}
	})
	t.Run("nil event / nil message", func(t *testing.T) {
		if _, ok := AuthenticationRequiredFromStatus(nil); ok {
			t.Error("nil event must yield no signal")
		}
		if _, ok := AuthenticationRequiredFromStatus(&TaskStatusUpdateEvent{Kind: "status-update"}); ok {
			t.Error("nil status message must yield no signal")
		}
	})
}

func TestAuthenticationResultFromStatus(t *testing.T) {
	resultEvent := func(success bool) *TaskStatusUpdateEvent {
		return &TaskStatusUpdateEvent{
			Kind: "status-update",
			Status: TaskStatus{Message: &Message{
				Role: "agent",
				Parts: []Part{NewDataPart(map[string]any{
					"type":                 "authentication_result",
					"target_agent":         "agent-x",
					"target_agent_task_id": "task-1",
					"success":              success,
				})},
			}},
		}
	}

	t.Run("success present", func(t *testing.T) {
		got, ok := AuthenticationResultFromStatus(resultEvent(true))
		if !ok || !got.Success || got.TargetAgent != "agent-x" || got.TargetAgentTaskID != "task-1" {
			t.Errorf("got (%+v, %v)", got, ok)
		}
	})
	t.Run("failure present", func(t *testing.T) {
		got, ok := AuthenticationResultFromStatus(resultEvent(false))
		if !ok || got.Success {
			t.Errorf("got (%+v, %v), want ok with Success=false", got, ok)
		}
	})
	t.Run("plain text / authentication_required is not a result", func(t *testing.T) {
		ev := &TaskStatusUpdateEvent{Kind: "status-update", Status: TaskStatus{Message: &Message{Parts: []Part{NewTextPart("thinking")}}}}
		if _, ok := AuthenticationResultFromStatus(ev); ok {
			t.Error("plain text status update must yield no result signal")
		}
		req := &TaskStatusUpdateEvent{Kind: "status-update", Status: TaskStatus{Message: &Message{Parts: []Part{NewDataPart(map[string]any{"type": "authentication_required"})}}}}
		if _, ok := AuthenticationResultFromStatus(req); ok {
			t.Error("authentication_required must not parse as a result")
		}
	})
	t.Run("nil event / nil message", func(t *testing.T) {
		if _, ok := AuthenticationResultFromStatus(nil); ok {
			t.Error("nil event must yield no signal")
		}
		if _, ok := AuthenticationResultFromStatus(&TaskStatusUpdateEvent{Kind: "status-update"}); ok {
			t.Error("nil status message must yield no signal")
		}
	})
}

func TestUserPropertiesIsPeerCall(t *testing.T) {
	if (UserProperties{}).IsPeerCall() {
		t.Error("empty DelegatingAgentName must be a direct call, not a peer call")
	}
	if !(UserProperties{DelegatingAgentName: "parent_wf"}).IsPeerCall() {
		t.Error("non-empty DelegatingAgentName must be a peer call")
	}
}
