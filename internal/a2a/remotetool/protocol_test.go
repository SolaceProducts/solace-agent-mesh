// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remotetool

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

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

func rawJSON(t *testing.T, data string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInvokeRequestJSON(t *testing.T) {
	params := InvokeParams{
		TaskID:   "corr-123",
		ToolName: "process_file",
		Args:     map[string]any{"input_file": "data.csv"},
		ToolConfig: map[string]any{
			"db_host": "localhost",
		},
		AppName:            "MyAgent",
		UserID:             "user-42",
		SessionID:          "session-abc",
		PreloadedArtifacts: map[string]any{},
		ArtifactReferences: map[string]ArtifactReference{
			"input_file": {Filename: "data.csv", Version: 0},
		},
		TimeoutSeconds: 30,
		SandboxProfile: "standard",
	}

	req := a2a.NewRequest("corr-123", MethodInvoke, params)
	data := mustMarshal(t, req)
	raw := rawJSON(t, data)

	if raw["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v", raw["jsonrpc"])
	}
	if raw["id"] != "corr-123" {
		t.Errorf("id = %v", raw["id"])
	}
	if raw["method"] != MethodInvoke {
		t.Errorf("method = %v", raw["method"])
	}

	p := raw["params"].(map[string]any)
	if p["task_id"] != "corr-123" {
		t.Errorf("task_id = %v", p["task_id"])
	}
	if p["tool_name"] != "process_file" {
		t.Errorf("tool_name = %v", p["tool_name"])
	}
	if p["app_name"] != "MyAgent" {
		t.Errorf("app_name = %v", p["app_name"])
	}
	if p["timeout_seconds"].(float64) != 30 {
		t.Errorf("timeout_seconds = %v", p["timeout_seconds"])
	}
	if p["sandbox_profile"] != "standard" {
		t.Errorf("sandbox_profile = %v", p["sandbox_profile"])
	}

	refs := p["artifact_references"].(map[string]any)
	ref := refs["input_file"].(map[string]any)
	if ref["filename"] != "data.csv" {
		t.Errorf("artifact filename = %v", ref["filename"])
	}
}

func TestInvokeRequestFromGoldenJSON(t *testing.T) {
	golden := `{
		"jsonrpc": "2.0",
		"id": "corr-abc",
		"method": "sam_remote_tool/invoke",
		"params": {
			"task_id": "corr-abc",
			"tool_name": "process_file",
			"args": {"input_file": "data.csv"},
			"tool_config": {},
			"app_name": "MyAgent",
			"user_id": "user-42",
			"session_id": "web-session-abc123",
			"preloaded_artifacts": {},
			"artifact_references": {
				"input_file": {"filename": "data.csv", "version": 0}
			},
			"timeout_seconds": 30,
			"sandbox_profile": "standard"
		}
	}`

	req := mustUnmarshal[a2a.Request](t, golden)
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q", req.JSONRPC)
	}
	if req.Method != MethodInvoke {
		t.Errorf("method = %q", req.Method)
	}

	paramsJSON, _ := json.Marshal(req.Params)
	params := mustUnmarshal[InvokeParams](t, string(paramsJSON))
	if params.TaskID != "corr-abc" {
		t.Errorf("task_id = %q", params.TaskID)
	}
	if params.ToolName != "process_file" {
		t.Errorf("tool_name = %q", params.ToolName)
	}
	if params.TimeoutSeconds != 30 {
		t.Errorf("timeout_seconds = %d", params.TimeoutSeconds)
	}
	ref, ok := params.ArtifactReferences["input_file"]
	if !ok {
		t.Fatal("missing artifact_references.input_file")
	}
	if ref.Filename != "data.csv" || ref.Version != 0 {
		t.Errorf("artifact ref = %+v", ref)
	}
}

func TestInvokeResultJSON(t *testing.T) {
	result := InvokeResult{
		ToolResult:      map[string]any{"status": "success", "word_count": float64(42)},
		ExecutionTimeMs: 150,
		TimedOut:        false,
		CreatedArtifacts: []CreatedArtifact{
			{Filename: "summary.txt", Version: 0, MIMEType: "text/plain", SizeBytes: 256},
		},
	}

	resp := a2a.NewResponse("corr-123", result)
	data := mustMarshal(t, resp)
	raw := rawJSON(t, data)

	if raw["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v", raw["jsonrpc"])
	}
	if raw["id"] != "corr-123" {
		t.Errorf("id = %v", raw["id"])
	}
	if _, ok := raw["error"]; ok {
		t.Error("error should be absent on success")
	}

	r := raw["result"].(map[string]any)
	if r["execution_time_ms"].(float64) != 150 {
		t.Errorf("execution_time_ms = %v", r["execution_time_ms"])
	}
	if r["timed_out"] != false {
		t.Errorf("timed_out = %v", r["timed_out"])
	}

	arts := r["created_artifacts"].([]any)
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}
	art := arts[0].(map[string]any)
	if art["filename"] != "summary.txt" {
		t.Errorf("filename = %v", art["filename"])
	}
	if art["mime_type"] != "text/plain" {
		t.Errorf("mime_type = %v", art["mime_type"])
	}
}

func TestInvokeResultFromGoldenJSON(t *testing.T) {
	golden := `{
		"jsonrpc": "2.0",
		"id": "corr-abc",
		"result": {
			"tool_result": {"status": "success", "word_count": 42},
			"execution_time_ms": 150,
			"timed_out": false,
			"created_artifacts": [
				{"filename": "summary.txt", "version": 0, "mime_type": "text/plain", "size_bytes": 256}
			]
		}
	}`

	resp := mustUnmarshal[a2a.Response](t, golden)
	if resp.ID != "corr-abc" {
		t.Errorf("id = %q", resp.ID)
	}
	if resp.Error != nil {
		t.Error("should not have error")
	}

	resultJSON, _ := json.Marshal(resp.Result)
	result := mustUnmarshal[InvokeResult](t, string(resultJSON))
	if result.ExecutionTimeMs != 150 {
		t.Errorf("execution_time_ms = %d", result.ExecutionTimeMs)
	}
	if result.TimedOut {
		t.Error("timed_out should be false")
	}
	if len(result.CreatedArtifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(result.CreatedArtifacts))
	}
	if result.CreatedArtifacts[0].Filename != "summary.txt" {
		t.Errorf("filename = %q", result.CreatedArtifacts[0].Filename)
	}
}

func TestInvokeErrorJSON(t *testing.T) {
	invokeErr := InvokeError{
		Code:    ErrExecutionError,
		Message: "Tool raised ValueError: invalid input format",
	}

	data := mustMarshal(t, invokeErr)
	raw := rawJSON(t, data)
	if raw["code"] != string(ErrExecutionError) {
		t.Errorf("code = %v", raw["code"])
	}
	if raw["message"] != "Tool raised ValueError: invalid input format" {
		t.Errorf("message = %v", raw["message"])
	}
}

func TestInvokeErrorFromGoldenJSON(t *testing.T) {
	golden := `{
		"jsonrpc": "2.0",
		"id": "corr-abc",
		"error": {
			"code": "EXECUTION_ERROR",
			"message": "Tool raised ValueError: invalid input format"
		}
	}`

	raw := rawJSON(t, golden)
	errObj := raw["error"].(map[string]any)
	if errObj["code"] != "EXECUTION_ERROR" {
		t.Errorf("code = %v", errObj["code"])
	}

	errJSON, _ := json.Marshal(errObj)
	invokeErr := mustUnmarshal[InvokeError](t, string(errJSON))
	if invokeErr.Code != ErrExecutionError {
		t.Errorf("code = %q", invokeErr.Code)
	}
	if invokeErr.Message != "Tool raised ValueError: invalid input format" {
		t.Errorf("message = %q", invokeErr.Message)
	}
}

func TestStatusNotificationJSON(t *testing.T) {
	params := StatusParams{
		TaskID:     "corr-123",
		StatusText: "Processing row 500 of 1000...",
		Timestamp:  "2025-02-09T12:34:56.789Z",
	}

	req := a2a.Request{
		JSONRPC: "2.0",
		Method:  MethodStatus,
		Params:  params,
	}
	data := mustMarshal(t, req)
	raw := rawJSON(t, data)

	if raw["method"] != MethodStatus {
		t.Errorf("method = %v", raw["method"])
	}

	p := raw["params"].(map[string]any)
	if p["task_id"] != "corr-123" {
		t.Errorf("task_id = %v", p["task_id"])
	}
	if p["status_text"] != "Processing row 500 of 1000..." {
		t.Errorf("status_text = %v", p["status_text"])
	}
	if p["timestamp"] != "2025-02-09T12:34:56.789Z" {
		t.Errorf("timestamp = %v", p["timestamp"])
	}
}

func TestStatusFromGoldenJSON(t *testing.T) {
	golden := `{
		"jsonrpc": "2.0",
		"method": "sam_remote_tool/status",
		"params": {
			"task_id": "corr-abc",
			"status_text": "Processing row 500 of 1000...",
			"timestamp": "2025-02-09T12:34:56.789Z"
		}
	}`

	req := mustUnmarshal[a2a.Request](t, golden)
	if req.Method != MethodStatus {
		t.Errorf("method = %q", req.Method)
	}
	if req.ID != "" {
		t.Errorf("id should be empty for notification, got %q", req.ID)
	}

	paramsJSON, _ := json.Marshal(req.Params)
	params := mustUnmarshal[StatusParams](t, string(paramsJSON))
	if params.TaskID != "corr-abc" {
		t.Errorf("task_id = %q", params.TaskID)
	}
	if params.StatusText != "Processing row 500 of 1000..." {
		t.Errorf("status_text = %q", params.StatusText)
	}
}

func TestErrorCodeRetryable(t *testing.T) {
	retryable := []ErrorCode{ErrToolNotAvailable}
	nonRetryable := []ErrorCode{
		ErrSandboxTimeout, ErrSandboxFailed, ErrToolNotFound,
		ErrImportError, ErrExecutionError, ErrToolError,
		ErrArtifactError, ErrInvalidRequest, ErrInternalError,
		ErrAuthenticationFailed,
	}

	for _, c := range retryable {
		if !c.Retryable() {
			t.Errorf("%q should be retryable", c)
		}
	}
	for _, c := range nonRetryable {
		if c.Retryable() {
			t.Errorf("%q should not be retryable", c)
		}
	}
}

func TestUserPropertiesEncode(t *testing.T) {
	up := &UserProperties{
		ReplyTo:   "ns/a2a/v1/sam_remote_tool/response/myagent/corr-123",
		StatusTo:  "ns/a2a/v1/sam_remote_tool/status/myagent/corr-123",
		ClientID:  "myagent",
		UserID:    "user-42",
		AuthToken: "jwt-token",
	}

	m := up.Encode()
	if m[PropReplyTo] != up.ReplyTo {
		t.Errorf("replyTo = %q", m[PropReplyTo])
	}
	if m[PropStatusTo] != up.StatusTo {
		t.Errorf("statusTo = %q", m[PropStatusTo])
	}
	if m[PropClientID] != "myagent" {
		t.Errorf("clientId = %q", m[PropClientID])
	}
	if m[PropUserID] != "user-42" {
		t.Errorf("userId = %q", m[PropUserID])
	}
	if m[PropAuthToken] != "jwt-token" {
		t.Errorf("authToken = %q", m[PropAuthToken])
	}
}

func TestUserPropertiesEncodeOmitsEmptyAuth(t *testing.T) {
	up := &UserProperties{
		ReplyTo:  "reply",
		StatusTo: "status",
		ClientID: "agent",
		UserID:   "user",
	}

	m := up.Encode()
	if _, ok := m[PropAuthToken]; ok {
		t.Error("authToken should be omitted when empty")
	}
}

func TestUserPropertiesDecode(t *testing.T) {
	m := map[string]string{
		PropReplyTo:   "reply-topic",
		PropStatusTo:  "status-topic",
		PropClientID:  "agent-1",
		PropUserID:    "user-1",
		PropAuthToken: "token-abc",
	}

	up := DecodeUserProperties(m)
	if up.ReplyTo != "reply-topic" {
		t.Errorf("replyTo = %q", up.ReplyTo)
	}
	if up.StatusTo != "status-topic" {
		t.Errorf("statusTo = %q", up.StatusTo)
	}
	if up.ClientID != "agent-1" {
		t.Errorf("clientId = %q", up.ClientID)
	}
	if up.UserID != "user-1" {
		t.Errorf("userId = %q", up.UserID)
	}
	if up.AuthToken != "token-abc" {
		t.Errorf("authToken = %q", up.AuthToken)
	}
}

func TestUserPropertiesRoundTrip(t *testing.T) {
	original := &UserProperties{
		ReplyTo:   "reply",
		StatusTo:  "status",
		ClientID:  "agent",
		UserID:    "user",
		AuthToken: "token",
	}

	m := original.Encode()
	decoded := DecodeUserProperties(m)

	if decoded != *original {
		t.Errorf("round-trip mismatch:\n  original: %+v\n  decoded:  %+v", original, decoded)
	}
}

func TestUserPropertiesA2AUserConfigRoundTrip(t *testing.T) {
	userConfig := `{"_enterprise_capabilities":["tool:*:*","agent:ResearchAgent:invoke"]}`
	original := &UserProperties{
		ReplyTo:       "reply",
		StatusTo:      "status",
		ClientID:      "agent",
		UserID:        "user",
		AuthToken:     "jwt-token",
		A2AUserConfig: userConfig,
	}

	m := original.Encode()

	if m[PropA2AUserConfig] != userConfig {
		t.Errorf("encoded a2aUserConfig = %q, want %q", m[PropA2AUserConfig], userConfig)
	}

	decoded := DecodeUserProperties(m)
	if decoded != *original {
		t.Errorf("round-trip mismatch:\n  original: %+v\n  decoded:  %+v", original, decoded)
	}
}

func TestUserPropertiesA2AUserConfigOmittedWhenEmpty(t *testing.T) {
	original := &UserProperties{
		ReplyTo:  "reply",
		StatusTo: "status",
		ClientID: "agent",
		UserID:   "user",
	}

	m := original.Encode()

	if v, exists := m[PropA2AUserConfig]; exists {
		t.Errorf("a2aUserConfig should not be in encoded map when empty, got %q", v)
	}
}

func TestArtifactReferenceJSON(t *testing.T) {
	ref := ArtifactReference{Filename: "data.csv", Version: 0}
	data := mustMarshal(t, ref)
	raw := rawJSON(t, data)

	if raw["filename"] != "data.csv" {
		t.Errorf("filename = %v", raw["filename"])
	}
	if raw["version"].(float64) != 0 {
		t.Errorf("version = %v", raw["version"])
	}

	got := mustUnmarshal[ArtifactReference](t, data)
	if got.Filename != "data.csv" || got.Version != 0 {
		t.Errorf("round-trip: %+v", got)
	}
}

func TestCreatedArtifactJSON(t *testing.T) {
	art := CreatedArtifact{
		Filename:  "summary.txt",
		Version:   0,
		MIMEType:  "text/plain",
		SizeBytes: 256,
	}

	data := mustMarshal(t, art)
	raw := rawJSON(t, data)

	if raw["filename"] != "summary.txt" {
		t.Errorf("filename = %v", raw["filename"])
	}
	if raw["mime_type"] != "text/plain" {
		t.Errorf("mime_type = %v", raw["mime_type"])
	}
	if raw["size_bytes"].(float64) != 256 {
		t.Errorf("size_bytes = %v", raw["size_bytes"])
	}
}

func TestInvokeParams_ParentTaskID(t *testing.T) {
	t.Parallel()

	t.Run("serializes and round-trips", func(t *testing.T) {
		t.Parallel()
		params := InvokeParams{
			TaskID:         "corr-1",
			ToolName:       "test_hil_tool",
			Args:           map[string]any{},
			ParentTaskID:   "parent-task-42",
			TimeoutSeconds: 10,
		}

		data := mustMarshal(t, params)
		raw := rawJSON(t, data)
		if raw["parent_task_id"] != "parent-task-42" {
			t.Errorf("parent_task_id = %v, want %q", raw["parent_task_id"], "parent-task-42")
		}

		got := mustUnmarshal[InvokeParams](t, data)
		if got.ParentTaskID != "parent-task-42" {
			t.Errorf("round-trip ParentTaskID = %q, want %q", got.ParentTaskID, "parent-task-42")
		}
	})

	t.Run("omitted when empty", func(t *testing.T) {
		t.Parallel()
		params := InvokeParams{
			TaskID:   "corr-2",
			ToolName: "some_tool",
			Args:     map[string]any{},
		}

		data := mustMarshal(t, params)
		raw := rawJSON(t, data)
		if _, exists := raw["parent_task_id"]; exists {
			t.Error("parent_task_id should be omitted when empty")
		}
	})
}

func TestUserProperties_A2AStatusTopicRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("round-trip", func(t *testing.T) {
		t.Parallel()
		original := &UserProperties{
			ReplyTo:        "reply",
			StatusTo:       "status",
			ClientID:       "agent",
			UserID:         "user",
			A2AStatusTopic: "ns/a2a/v1/status/agent/task-1",
		}

		m := original.Encode()
		if m[PropA2AStatusTopic] != "ns/a2a/v1/status/agent/task-1" {
			t.Errorf("encoded a2aStatusTopic = %q", m[PropA2AStatusTopic])
		}

		decoded := DecodeUserProperties(m)
		if decoded.A2AStatusTopic != original.A2AStatusTopic {
			t.Errorf("decoded A2AStatusTopic = %q, want %q", decoded.A2AStatusTopic, original.A2AStatusTopic)
		}
	})

	t.Run("omitted when empty", func(t *testing.T) {
		t.Parallel()
		up := &UserProperties{
			ReplyTo:  "reply",
			StatusTo: "status",
			ClientID: "agent",
			UserID:   "user",
		}
		m := up.Encode()
		if _, exists := m[PropA2AStatusTopic]; exists {
			t.Error("a2aStatusTopic should not be in encoded map when empty")
		}
	})
}

func TestUserProperties_ContextIDRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("round-trip", func(t *testing.T) {
		t.Parallel()
		original := &UserProperties{
			ReplyTo:   "reply",
			StatusTo:  "status",
			ClientID:  "agent",
			UserID:    "user",
			ContextID: "session-xyz-123",
		}

		m := original.Encode()
		if m[PropContextID] != "session-xyz-123" {
			t.Errorf("encoded contextId = %q", m[PropContextID])
		}

		decoded := DecodeUserProperties(m)
		if decoded.ContextID != original.ContextID {
			t.Errorf("decoded ContextID = %q, want %q", decoded.ContextID, original.ContextID)
		}
	})

	t.Run("omitted when empty", func(t *testing.T) {
		t.Parallel()
		up := &UserProperties{
			ReplyTo:  "reply",
			StatusTo: "status",
			ClientID: "agent",
			UserID:   "user",
		}
		m := up.Encode()
		if _, exists := m[PropContextID]; exists {
			t.Error("contextId should not be in encoded map when empty")
		}
	})
}

func TestInitParams_AuthRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("oauth2 with full scheme", func(t *testing.T) {
		t.Parallel()
		params := InitParams{
			ToolName:    "calendar_lookup",
			Description: "Look up events",
			Parameters:  map[string]any{"type": "object"},
			Auth: &AuthDeclaration{
				Type: "oauth2",
				Scheme: &AuthDeclarationScheme{
					AuthorizationURL:        "https://auth.example.com/authorize",
					TokenURL:                "https://auth.example.com/token",
					Scopes:                  []string{"calendar.read", "calendar.write"},
					TokenEndpointAuthMethod: "client_secret_post",
				},
			},
		}

		data := mustMarshal(t, params)
		raw := rawJSON(t, data)
		authRaw, ok := raw["auth"].(map[string]any)
		if !ok {
			t.Fatalf("auth field missing or wrong type: %T", raw["auth"])
		}
		if authRaw["type"] != "oauth2" {
			t.Errorf("auth.type = %v", authRaw["type"])
		}
		schemeRaw := authRaw["scheme"].(map[string]any)
		if schemeRaw["authorization_url"] != "https://auth.example.com/authorize" {
			t.Errorf("authorization_url = %v", schemeRaw["authorization_url"])
		}
		if schemeRaw["token_url"] != "https://auth.example.com/token" {
			t.Errorf("token_url = %v", schemeRaw["token_url"])
		}
		if schemeRaw["token_endpoint_auth_method"] != "client_secret_post" {
			t.Errorf("token_endpoint_auth_method = %v", schemeRaw["token_endpoint_auth_method"])
		}

		got := mustUnmarshal[InitParams](t, data)
		if got.Auth == nil || got.Auth.Scheme == nil {
			t.Fatal("decoded auth/scheme nil")
		}
		if got.Auth.Type != params.Auth.Type {
			t.Errorf("Type round-trip: %q != %q", got.Auth.Type, params.Auth.Type)
		}
		if got.Auth.Scheme.AuthorizationURL != params.Auth.Scheme.AuthorizationURL ||
			got.Auth.Scheme.TokenURL != params.Auth.Scheme.TokenURL ||
			got.Auth.Scheme.TokenEndpointAuthMethod != params.Auth.Scheme.TokenEndpointAuthMethod {
			t.Errorf("scheme round-trip mismatch: %+v", got.Auth.Scheme)
		}
		if len(got.Auth.Scheme.Scopes) != 2 || got.Auth.Scheme.Scopes[0] != "calendar.read" {
			t.Errorf("scopes round-trip: %v", got.Auth.Scheme.Scopes)
		}
	})

	t.Run("auth omitted when nil", func(t *testing.T) {
		t.Parallel()
		params := InitParams{ToolName: "no_auth_tool"}
		data := mustMarshal(t, params)
		raw := rawJSON(t, data)
		if _, exists := raw["auth"]; exists {
			t.Error("auth should be omitted when nil")
		}
	})
}

func TestUserProperties_AllNewFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	original := &UserProperties{
		ReplyTo:        "reply",
		StatusTo:       "status",
		ClientID:       "agent",
		UserID:         "user",
		AuthToken:      "jwt",
		A2AUserConfig:  `{"scopes":["tool:*:*"]}`,
		A2AStatusTopic: "ns/a2a/v1/status/agent/task-1",
		ContextID:      "session-abc",
		TraceID:        "018f3a4b-7c9d-7e21-8b1f-2d4e5f6a7b8c",
	}

	m := original.Encode()
	decoded := DecodeUserProperties(m)

	if decoded != *original {
		t.Errorf("round-trip mismatch:\n  original: %+v\n  decoded:  %+v", *original, decoded)
	}
}

func TestUserProperties_TraceIDRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("round-trip", func(t *testing.T) {
		t.Parallel()
		const traceID = "018f3a4b-7c9d-7e21-8b1f-2d4e5f6a7b8c"
		original := &UserProperties{
			ReplyTo:  "reply",
			StatusTo: "status",
			ClientID: "agent",
			UserID:   "user",
			TraceID:  traceID,
		}

		m := original.Encode()
		if m[PropTraceID] != traceID {
			t.Errorf("encoded traceId = %q, want %q", m[PropTraceID], traceID)
		}

		decoded := DecodeUserProperties(m)
		if decoded.TraceID != traceID {
			t.Errorf("decoded TraceID = %q, want %q", decoded.TraceID, traceID)
		}
	})

	t.Run("omitted when empty", func(t *testing.T) {
		t.Parallel()
		up := &UserProperties{
			ReplyTo:  "reply",
			StatusTo: "status",
			ClientID: "agent",
			UserID:   "user",
		}
		m := up.Encode()
		if _, exists := m[PropTraceID]; exists {
			t.Error("traceId should not be in encoded map when empty")
		}
	})
}

func TestInvokeSpendWireKeys(t *testing.T) {
	const golden = `{"user_id":"u-row","user_sub":"alice","agent_id":"a-row","agent_name":"researcher",` +
		`"workflow_id":"w-row","workflow_name":"flow","workflow_node_id":"node-1","root_kind":"agent",` +
		`"root_id":"r-row","root_name":"root","root_node_id":"node-0","entrypoint_id":"e-row",` +
		`"entrypoint_name":"webui-gw","channel":"webui","trigger_kind":"chat","trigger_id":"trig-1",` +
		`"task_id":"agent-task","parent_task_id":"orchestrator-task","trace_id":"trace-1","session_id":"sess-1"}`
	full := SpendAttribution{
		UserID: "u-row", UserSub: "alice", AgentID: "a-row", AgentName: "researcher",
		WorkflowID: "w-row", WorkflowName: "flow", WorkflowNodeID: "node-1", RootKind: "agent",
		RootID: "r-row", RootName: "root", RootNodeID: "node-0", EntrypointID: "e-row",
		EntrypointName: "webui-gw", Channel: "webui", TriggerKind: "chat", TriggerID: "trig-1",
		TaskID: "agent-task", ParentTaskID: "orchestrator-task", TraceID: "trace-1", SessionID: "sess-1",
	}

	var params struct {
		Spend json.RawMessage `json:"spend"`
	}
	if err := json.Unmarshal([]byte(mustMarshal(t, InvokeParams{ToolName: "t", Spend: &full})), &params); err != nil {
		t.Fatal(err)
	}
	if string(params.Spend) != golden {
		t.Errorf("spend wire =\n%s\nwant\n%s", params.Spend, golden)
	}
	if got := mustUnmarshal[SpendAttribution](t, golden); got != full {
		t.Errorf("decoded golden = %+v, want %+v", got, full)
	}

	if got := mustMarshal(t, SpendAttribution{UserSub: "alice"}); got != `{"user_sub":"alice"}` {
		t.Errorf("sparse spend = %s, want only its set keys", got)
	}
	if _, present := rawJSON(t, mustMarshal(t, InvokeParams{ToolName: "t"}))["spend"]; present {
		t.Error("an invoke with no attribution carries a spend key")
	}
}
