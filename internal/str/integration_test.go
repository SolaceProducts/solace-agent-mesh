// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

var (
	goTestToolPath string
	goTestToolOnce sync.Once
	goTestToolErr  error
)

func getGoTestTool(t *testing.T) string {
	t.Helper()
	goTestToolOnce.Do(func() {
		pkgDir, _ := os.Getwd()
		srcDir := filepath.Join(pkgDir, "testdata", "go-test-tool")

		tmpDir := os.TempDir()
		goTestToolPath = filepath.Join(tmpDir, "go-test-tool-integration")

		cmd := exec.Command("go", "build", "-o", goTestToolPath, ".")
		cmd.Dir = srcDir
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		goTestToolErr = cmd.Run()
	})
	if goTestToolErr != nil {
		t.Fatalf("failed to build Go test tool: %v", goTestToolErr)
	}
	return goTestToolPath
}

var (
	echoConfigToolPath string
	echoConfigToolOnce sync.Once
	echoConfigToolErr  error
)

func getEchoConfigTool(t *testing.T) string {
	t.Helper()
	echoConfigToolOnce.Do(func() {
		pkgDir, _ := os.Getwd()
		srcDir := filepath.Join(pkgDir, "testdata", "echo-config-tool")

		tmpDir := os.TempDir()
		echoConfigToolPath = filepath.Join(tmpDir, "echo-config-tool-integration")

		cmd := exec.Command("go", "build", "-o", echoConfigToolPath, ".")
		cmd.Dir = srcDir
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		echoConfigToolErr = cmd.Run()
	})
	if echoConfigToolErr != nil {
		t.Fatalf("failed to build echo-config-tool: %v", echoConfigToolErr)
	}
	return echoConfigToolPath
}

func newTestRunner(t *testing.T) *SandboxRunner {
	t.Helper()
	cfg := SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   30,
		MaxConcurrentExecutions: 2,
	}
	return NewSandboxRunner(cfg, slog.Default())
}

func newArtifactService() *artifact.ScopedService {
	return &artifact.ScopedService{
		Inner:     memory.New(),
		Scope:     "agent",
		Namespace: "test",
	}
}

func collectStatuses() (func(string), func() []string) {
	var mu sync.Mutex
	var statuses []string
	cb := func(s string) {
		mu.Lock()
		statuses = append(statuses, s)
		mu.Unlock()
	}
	get := func() []string {
		mu.Lock()
		defer mu.Unlock()
		result := make([]string, len(statuses))
		copy(result, statuses)
		return result
	}
	return cb, get
}

func TestGoTool_Echo(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)
	statusCb, getStatuses := collectStatuses()

	entry := &ManifestEntry{
		ToolName:   "echo",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-echo-1",
		ToolName:  "echo",
		Args:      map[string]any{"message": "Hello, World!"},
		UserID:    "user-123",
		SessionID: "sess-456",
		AppName:   "test-agent",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, statusCb)
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult == nil {
		t.Fatal("expected non-nil ToolResult")
	}
	if result.ToolResult["status"] != "success" {
		t.Errorf("status: got %v, want 'success'", result.ToolResult["status"])
	}
	if result.ToolResult["message"] != "Hello, World!" {
		t.Errorf("message: got %v, want 'Hello, World!'", result.ToolResult["message"])
	}

	data, ok := result.ToolResult["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not a map: %T", result.ToolResult["data"])
	}
	if data["user_id"] != "user-123" {
		t.Errorf("user_id: got %v", data["user_id"])
	}
	if data["session_id"] != "sess-456" {
		t.Errorf("session_id: got %v", data["session_id"])
	}
	if data["app_name"] != "test-agent" {
		t.Errorf("app_name: got %v", data["app_name"])
	}

	statuses := getStatuses()
	if len(statuses) < 2 {
		t.Errorf("expected at least 2 status updates, got %d: %v", len(statuses), statuses)
	}
}

func TestGoTool_EchoWithPrefix(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)

	entry := &ManifestEntry{
		ToolName:   "echo",
		Executable: toolBinary,
	}

	prefix := "URGENT"
	params := &remotetool.InvokeParams{
		TaskID:   "task-echo-prefix",
		ToolName: "echo",
		Args:     map[string]any{"message": "Hello", "prefix": prefix},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["message"] != "URGENT: Hello" {
		t.Errorf("message: got %v, want 'URGENT: Hello'", result.ToolResult["message"])
	}
}

func TestGoTool_EchoWithConfigPrefix(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)

	entry := &ManifestEntry{
		ToolName:   "echo",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:     "task-echo-config",
		ToolName:   "echo",
		Args:       map[string]any{"message": "Hello"},
		ToolConfig: map[string]any{"default_prefix": "INFO"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["message"] != "INFO: Hello" {
		t.Errorf("message: got %v, want 'INFO: Hello'", result.ToolResult["message"])
	}
}

func TestEchoConfigTool_SchemaIncludesConfigFields(t *testing.T) {
	toolBinary := getEchoConfigTool(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, toolBinary, "--schema")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("--schema failed: %v", err)
	}

	var schema struct {
		Tools map[string]struct {
			ConfigSchema []map[string]any `json:"config_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(output, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	tool, ok := schema.Tools["echo_config"]
	if !ok {
		t.Fatal("echo_config tool missing from --schema output")
	}
	if len(tool.ConfigSchema) != 3 {
		t.Fatalf("config_schema length = %d, want 3", len(tool.ConfigSchema))
	}

	byKey := map[string]map[string]any{}
	for _, f := range tool.ConfigSchema {
		byKey[f["key"].(string)] = f
	}

	apiKey, ok := byKey["api_key"]
	if !ok {
		t.Fatal("api_key field missing")
	}
	if apiKey["secret"] != true {
		t.Errorf("api_key.secret = %v, want true", apiKey["secret"])
	}
	if apiKey["required"] != true {
		t.Errorf("api_key.required = %v, want true", apiKey["required"])
	}
	if _, hasDefault := apiKey["default"]; hasDefault {
		t.Errorf("api_key should not declare a default; got %v", apiKey["default"])
	}

	endpoint, ok := byKey["endpoint"]
	if !ok {
		t.Fatal("endpoint field missing")
	}
	if endpoint["default"] != "https://api.example.com" {
		t.Errorf("endpoint.default = %v, want %q", endpoint["default"], "https://api.example.com")
	}
}

func TestEchoConfigTool_ToolConfigDeliveredToProcess(t *testing.T) {
	toolBinary := getEchoConfigTool(t)
	runner := newTestRunner(t)

	entry := &ManifestEntry{
		ToolName:   "echo_config",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:   "task-echo-config",
		ToolName: "echo_config",
		Args:     map[string]any{"text": "hello"},
		ToolConfig: map[string]any{
			"api_key":  "sk-test-1234",
			"region":   "us-east-1",
			"endpoint": "https://override.example.com",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	data, ok := result.ToolResult["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data map in result, got %T", result.ToolResult["data"])
	}
	cfg, ok := data["config_seen"].(map[string]any)
	if !ok {
		t.Fatalf("expected config_seen map, got %T", data["config_seen"])
	}

	if cfg["api_key"] != "sk-test-1234" {
		t.Errorf("api_key delivered to tool = %v, want %q", cfg["api_key"], "sk-test-1234")
	}
	if cfg["region"] != "us-east-1" {
		t.Errorf("region delivered to tool = %v, want %q", cfg["region"], "us-east-1")
	}
	if cfg["endpoint"] != "https://override.example.com" {
		t.Errorf("endpoint delivered to tool = %v, want %q", cfg["endpoint"], "https://override.example.com")
	}
}

func TestEchoConfigTool_EmptyToolConfig(t *testing.T) {
	toolBinary := getEchoConfigTool(t)
	runner := newTestRunner(t)

	entry := &ManifestEntry{
		ToolName:   "echo_config",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:   "task-echo-empty",
		ToolName: "echo_config",
		Args:     map[string]any{"text": "no config"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	data, _ := result.ToolResult["data"].(map[string]any)
	cfg, _ := data["config_seen"].(map[string]any)
	if len(cfg) != 0 {
		t.Errorf("expected empty config_seen, got %v", cfg)
	}
}

func TestGoTool_ProcessFile_InputArtifact(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)
	artifactSvc := newArtifactService()
	statusCb, getStatuses := collectStatuses()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	loc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "input.txt")
	_, err := artifactSvc.Inner.Save(ctx, loc, []byte("hello world"), "text/plain")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	entry := &ManifestEntry{
		ToolName:   "process_file",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-process-1",
		ToolName:  "process_file",
		Args:      map[string]any{},
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input": {Filename: "input.txt", Version: 0},
		},
	}

	result, invokeErr := runner.Execute(ctx, params, entry, artifactSvc, statusCb)
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "success" {
		t.Errorf("status: got %v", result.ToolResult["status"])
	}

	msg, _ := result.ToolResult["message"].(string)
	if !strings.Contains(msg, "Processed") {
		t.Errorf("message should mention 'Processed': got %q", msg)
	}

	data, ok := result.ToolResult["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not a map: %T", result.ToolResult["data"])
	}
	if data["input_filename"] != "input.txt" {
		t.Errorf("input_filename: got %v", data["input_filename"])
	}
	if data["input_mime_type"] != "text/plain" {
		t.Errorf("input_mime_type: got %v", data["input_mime_type"])
	}

	if len(result.CreatedArtifacts) == 0 {
		t.Fatal("expected at least 1 created artifact")
	}
	found := false
	for _, ca := range result.CreatedArtifacts {
		if ca.Filename == "processed_input.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected output artifact 'processed_input.txt', got %v", result.CreatedArtifacts)
	}

	outLoc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "processed_input.txt")
	outContent, outMIME, err := artifactSvc.Inner.Load(ctx, outLoc, nil)
	if err != nil {
		t.Fatalf("load output artifact: %v", err)
	}
	if string(outContent) != "HELLO WORLD" {
		t.Errorf("output content: got %q, want 'HELLO WORLD'", string(outContent))
	}
	if outMIME != "text/plain" {
		t.Errorf("output mime: got %q", outMIME)
	}

	statuses := getStatuses()
	if len(statuses) == 0 {
		t.Error("expected status updates")
	}
}

func TestGoTool_ProcessFile_WithTemplate(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)
	artifactSvc := newArtifactService()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	inputLoc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "data.txt")
	if _, err := artifactSvc.Inner.Save(ctx, inputLoc, []byte("important data"), "text/plain"); err != nil {
		t.Fatalf("save input: %v", err)
	}

	templateLoc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "template.txt")
	if _, err := artifactSvc.Inner.Save(ctx, templateLoc, []byte("BEGIN: {{content}} :END"), "text/plain"); err != nil {
		t.Fatalf("save template: %v", err)
	}

	entry := &ManifestEntry{
		ToolName:   "process_file",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-template-1",
		ToolName:  "process_file",
		Args:      map[string]any{},
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input":    {Filename: "data.txt", Version: 0},
			"template": {Filename: "template.txt", Version: 0},
		},
	}

	_, invokeErr := runner.Execute(ctx, params, entry, artifactSvc, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	outLoc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "processed_data.txt")
	outContent, _, err := artifactSvc.Inner.Load(ctx, outLoc, nil)
	if err != nil {
		t.Fatalf("load output: %v", err)
	}

	expected := "BEGIN: IMPORTANT DATA :END"
	if string(outContent) != expected {
		t.Errorf("output: got %q, want %q", string(outContent), expected)
	}
}

func TestGoTool_ErrorResult(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)

	entry := &ManifestEntry{
		ToolName:   "error_tool",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:   "task-error-1",
		ToolName: "error_tool",
		Args:     map[string]any{"message": "something went wrong"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "error" {
		t.Errorf("status: got %v, want 'error'", result.ToolResult["status"])
	}
	if result.ToolResult["message"] != "something went wrong" {
		t.Errorf("message: got %v", result.ToolResult["message"])
	}
	if result.ToolResult["error_code"] != "TEST_ERROR" {
		t.Errorf("error_code: got %v, want 'TEST_ERROR'", result.ToolResult["error_code"])
	}
}

func TestGoTool_SaveArtifact(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)
	artifactSvc := newArtifactService()

	entry := &ManifestEntry{
		ToolName:   "save_artifact",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-save-1",
		ToolName:  "save_artifact",
		Args:      map[string]any{"filename": "output.txt", "content": "saved content here"},
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, artifactSvc, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "success" {
		t.Errorf("status: got %v", result.ToolResult["status"])
	}

	if len(result.CreatedArtifacts) == 0 {
		t.Fatal("expected at least 1 created artifact from SaveArtifact")
	}
	found := false
	for _, ca := range result.CreatedArtifacts {
		if ca.Filename == "output.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected artifact 'output.txt', got %v", result.CreatedArtifacts)
	}

	outLoc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "output.txt")
	outContent, _, err := artifactSvc.Inner.Load(ctx, outLoc, nil)
	if err != nil {
		t.Fatalf("load output: %v", err)
	}
	if string(outContent) != "saved content here" {
		t.Errorf("content: got %q", string(outContent))
	}
}

func TestGoTool_UnknownTool(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)

	entry := &ManifestEntry{
		ToolName:   "nonexistent",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:   "task-unknown-1",
		ToolName: "nonexistent",
		Args:     map[string]any{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr == nil {
		t.Fatal("expected error for unknown tool")
	}
	if invokeErr.Code != remotetool.ErrToolError {
		t.Errorf("error code: got %v, want %v", invokeErr.Code, remotetool.ErrToolError)
	}
}

func TestGoTool_PrefixedDispatch(t *testing.T) {
	toolBinary := getGoTestTool(t)

	cases := []struct {
		name         string
		entry        *ManifestEntry
		wantErr      bool
		wantContains string
	}{
		{
			name: "STR recorded the bare dispatch name",
			entry: &ManifestEntry{
				ToolName:     "myskill__echo",
				BareToolName: "echo",
				Executable:   toolBinary,
			},
		},
		{
			name: "SDK strips the prefix when the STR never resolved one",
			entry: &ManifestEntry{
				ToolName:   "myskill__echo",
				Executable: toolBinary,
			},
		},
		{
			name: "bare half matches no registered tool",
			entry: &ManifestEntry{
				ToolName:   "myskill__no_such_tool",
				Executable: toolBinary,
			},
			wantErr:      true,
			wantContains: "registered:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := newTestRunner(t)
			params := &remotetool.InvokeParams{
				TaskID:   "task-prefixed-1",
				ToolName: tc.entry.ToolName,
				Args:     map[string]any{"message": "hello"},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			result, invokeErr := runner.Execute(ctx, params, tc.entry, nil, func(_ string) {})
			if tc.wantErr {
				if invokeErr == nil {
					t.Fatalf("expected an error for %q", tc.entry.ToolName)
				}
				if !strings.Contains(invokeErr.Message, tc.wantContains) {
					t.Errorf("error message %q does not name the registered tools", invokeErr.Message)
				}
				return
			}
			if invokeErr != nil {
				t.Fatalf("invoke failed for %q: %v", tc.entry.ToolName, invokeErr.Message)
			}
			if result == nil {
				t.Fatal("no result returned")
			}
		})
	}
}

func TestGoTool_SchemaDiscovery(t *testing.T) {
	toolBinary := getGoTestTool(t)

	discovered, err := DiscoverFromExecutable(toolBinary, "")
	if err != nil {
		t.Fatalf("DiscoverFromExecutable: %v", err)
	}

	expectedTools := []string{"echo", "process_file", "multi_artifact", "error_tool", "save_artifact", "summarize", "chat", "bulk_output", "authed_tool"}
	for _, name := range expectedTools {
		if _, ok := discovered[name]; !ok {
			t.Errorf("tool %q not discovered", name)
		}
	}

	if len(discovered) != len(expectedTools) {
		t.Errorf("discovered %d tools, want %d", len(discovered), len(expectedTools))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, toolBinary, "--schema")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("--schema failed: %v", err)
	}

	var schema struct {
		Tools map[string]struct {
			Description    string         `json:"description"`
			Parameters     map[string]any `json:"parameters"`
			ArtifactParams map[string]any `json:"artifact_params"`
			Instructions   string         `json:"instructions"`
			Auth           *struct {
				Type   string `json:"type"`
				Scheme *struct {
					AuthorizationURL string   `json:"authorization_url"`
					TokenURL         string   `json:"token_url"`
					Scopes           []string `json:"scopes"`
				} `json:"scheme"`
			} `json:"auth"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(output, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	echoSchema, ok := schema.Tools["echo"]
	if !ok {
		t.Fatal("echo tool not in schema")
	}
	if echoSchema.Instructions != "Use this tool to echo messages" {
		t.Errorf("echo instructions: got %q", echoSchema.Instructions)
	}

	pfSchema, ok := schema.Tools["process_file"]
	if !ok {
		t.Fatal("process_file not in schema")
	}
	if len(pfSchema.ArtifactParams) == 0 {
		t.Error("process_file should have artifact params")
	}
	inputAP, ok := pfSchema.ArtifactParams["input"]
	if !ok {
		t.Error("process_file should have 'input' artifact param")
	} else {
		ap := inputAP.(map[string]any)
		if ap["is_list"] != false {
			t.Error("input artifact should not be list")
		}
		if ap["is_optional"] != false {
			t.Error("input artifact should be required")
		}
	}

	templateAP, ok := pfSchema.ArtifactParams["template"]
	if !ok {
		t.Error("process_file should have 'template' artifact param")
	} else {
		ap := templateAP.(map[string]any)
		if ap["is_optional"] != true {
			t.Error("template artifact should be optional")
		}
	}

	maSchema, ok := schema.Tools["multi_artifact"]
	if !ok {
		t.Fatal("multi_artifact not in schema")
	}
	filesAP, ok := maSchema.ArtifactParams["files"]
	if !ok {
		t.Error("multi_artifact should have 'files' artifact param")
	} else {
		ap := filesAP.(map[string]any)
		if ap["is_list"] != true {
			t.Error("files artifact should be list")
		}
	}
}

func TestGoTool_SchemaAuthDiscovery(t *testing.T) {
	toolBinary := getGoTestTool(t)

	discovered, err := DiscoverFromExecutable(toolBinary, "")
	if err != nil {
		t.Fatalf("DiscoverFromExecutable: %v", err)
	}

	authedInfo, ok := discovered["authed_tool"]
	if !ok {
		t.Fatal("authed_tool not discovered")
	}
	if authedInfo.Auth == nil {
		t.Fatal("authed_tool should have Auth config")
	}
	if authedInfo.Auth.Type != "oauth2" {
		t.Errorf("auth type: got %q, want %q", authedInfo.Auth.Type, "oauth2")
	}
	if authedInfo.Auth.Scheme == nil {
		t.Fatal("auth scheme should not be nil")
	}
	if authedInfo.Auth.Scheme.AuthorizationURL != "https://auth.example.com/authorize" {
		t.Errorf("authorization_url: got %q", authedInfo.Auth.Scheme.AuthorizationURL)
	}
	if authedInfo.Auth.Scheme.TokenURL != "https://auth.example.com/token" {
		t.Errorf("token_url: got %q", authedInfo.Auth.Scheme.TokenURL)
	}
	if len(authedInfo.Auth.Scheme.Scopes) != 2 || authedInfo.Auth.Scheme.Scopes[0] != "read" || authedInfo.Auth.Scheme.Scopes[1] != "write" {
		t.Errorf("scopes: got %v", authedInfo.Auth.Scheme.Scopes)
	}

	echoInfo := discovered["echo"]
	if echoInfo.Auth != nil {
		t.Error("echo should not have auth config")
	}

	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.yaml")
	manifestContent := fmt.Sprintf("version: 1\ntools:\n  go_test_tool:\n    executable: %s\n", toolBinary)
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	manifest := NewManifest(manifestPath, slog.Default())
	if err := manifest.Load(t.Context()); err != nil {
		t.Fatalf("manifest load: %v", err)
	}

	authedEntry := manifest.GetTool("authed_tool")
	if authedEntry == nil {
		t.Fatal("authed_tool not in manifest after discovery")
	}
	if authedEntry.Auth == nil {
		t.Fatal("manifest entry for authed_tool should have Auth")
	}
	if authedEntry.Auth.Type != "oauth2" {
		t.Errorf("manifest auth type: got %q", authedEntry.Auth.Type)
	}
	if authedEntry.Auth.Scheme == nil || authedEntry.Auth.Scheme.TokenURL != "https://auth.example.com/token" {
		t.Errorf("manifest auth scheme: got %v", authedEntry.Auth.Scheme)
	}

	echoEntry := manifest.GetTool("echo")
	if echoEntry == nil {
		t.Fatal("echo not in manifest")
	}
	if echoEntry.Auth != nil {
		t.Error("echo manifest entry should not have auth")
	}
}

func TestPythonTool_SchemaAuthDiscovery(t *testing.T) {
	pythonBin := findPython(t)

	pkgDir, _ := os.Getwd()
	toolScript := filepath.Join(pkgDir, "testdata", "python-test-tool", "tool.py")

	tmpDir := t.TempDir()
	wrapperPath := filepath.Join(tmpDir, "py-tool-wrapper.sh")
	wrapperContent := fmt.Sprintf("#!/bin/sh\nexec %s %s \"$@\"\n", pythonBin, toolScript)
	if err := os.WriteFile(wrapperPath, []byte(wrapperContent), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	discovered, err := DiscoverFromExecutable(wrapperPath, "")
	if err != nil {
		t.Fatalf("DiscoverFromExecutable (Python): %v", err)
	}

	authedInfo, ok := discovered["authed_tool"]
	if !ok {
		t.Fatal("authed_tool not discovered from Python tool")
	}
	if authedInfo.Auth == nil {
		t.Fatal("Python authed_tool should have Auth config")
	}
	if authedInfo.Auth.Type != "oauth2" {
		t.Errorf("auth type: got %q, want %q", authedInfo.Auth.Type, "oauth2")
	}
	if authedInfo.Auth.Scheme == nil {
		t.Fatal("auth scheme should not be nil")
	}
	if authedInfo.Auth.Scheme.AuthorizationURL != "https://auth.example.com/authorize" {
		t.Errorf("authorization_url: got %q", authedInfo.Auth.Scheme.AuthorizationURL)
	}
	if authedInfo.Auth.Scheme.TokenURL != "https://auth.example.com/token" {
		t.Errorf("token_url: got %q", authedInfo.Auth.Scheme.TokenURL)
	}
	if len(authedInfo.Auth.Scheme.Scopes) != 2 {
		t.Errorf("scopes: got %v", authedInfo.Auth.Scheme.Scopes)
	}

	echoInfo := discovered["echo"]
	if echoInfo.Auth != nil {
		t.Error("Python echo should not have auth config")
	}

	manifestPath := filepath.Join(tmpDir, "manifest.yaml")
	manifestContent := fmt.Sprintf("version: 1\ntools:\n  py_test_tool:\n    executable: %s\n", wrapperPath)
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	manifest := NewManifest(manifestPath, slog.Default())
	if err := manifest.Load(t.Context()); err != nil {
		t.Fatalf("manifest load: %v", err)
	}

	authedEntry := manifest.GetTool("authed_tool")
	if authedEntry == nil {
		t.Fatal("authed_tool not in manifest from Python tool")
	}
	if authedEntry.Auth == nil {
		t.Fatal("Python manifest entry for authed_tool should have Auth")
	}
	if authedEntry.Auth.Type != "oauth2" {
		t.Errorf("manifest auth type: got %q", authedEntry.Auth.Type)
	}
}

func TestGoTool_LLMCallback(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)
	statusCb, getStatuses := collectStatuses()

	var capturedSystem, capturedUser, capturedAlias string
	var capturedTemp float64
	runner.llmCall = func(_ context.Context, modelAlias, _ string, _ *remotetool.InvokeParams, systemPrompt, userPrompt string, temperature float64) (string, error) {
		capturedAlias = modelAlias
		capturedSystem = systemPrompt
		capturedUser = userPrompt
		capturedTemp = temperature
		return "This is a mock LLM summary of the input text.", nil
	}

	entry := &ManifestEntry{
		ToolName:   "summarize",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:   "task-llm-1",
		ToolName: "summarize",
		Args:     map[string]any{"text": "The quick brown fox jumps over the lazy dog."},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, statusCb)
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "success" {
		t.Errorf("status: got %v, want 'success'", result.ToolResult["status"])
	}
	if result.ToolResult["message"] != "This is a mock LLM summary of the input text." {
		t.Errorf("message: got %v", result.ToolResult["message"])
	}

	if capturedSystem != "You are a summarizer. Be concise." {
		t.Errorf("system prompt: got %q", capturedSystem)
	}
	expectedUser := "Summarize: The quick brown fox jumps over the lazy dog."
	if capturedUser != expectedUser {
		t.Errorf("user prompt: got %q, want %q", capturedUser, expectedUser)
	}
	if capturedTemp != 0.3 {
		t.Errorf("temperature: got %v, want 0.3", capturedTemp)
	}
	if capturedAlias != "" {
		t.Errorf("modelAlias: got %q, want empty (no tool_config override)", capturedAlias)
	}

	data, ok := result.ToolResult["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not a map: %T", result.ToolResult["data"])
	}
	if data["original_length"] != float64(44) {
		t.Errorf("original_length: got %v, want 44", data["original_length"])
	}

	statuses := getStatuses()
	if len(statuses) == 0 {
		t.Error("expected at least 1 status update")
	}
	foundLLMStatus := false
	for _, s := range statuses {
		if strings.Contains(s, "LLM") {
			foundLLMStatus = true
		}
	}
	if !foundLLMStatus {
		t.Errorf("expected status about LLM call, got: %v", statuses)
	}
}

func TestGoTool_LLMCallback_ForwardsToolConfigModelAlias(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)
	statusCb, _ := collectStatuses()

	var capturedAlias string
	runner.llmCall = func(_ context.Context, modelAlias, _ string, _ *remotetool.InvokeParams, _, _ string, _ float64) (string, error) {
		capturedAlias = modelAlias
		return "mock summary", nil
	}

	entry := &ManifestEntry{
		ToolName:   "summarize",
		Executable: toolBinary,
	}
	params := minimalParams()
	params.ToolName = "summarize"
	params.Args = map[string]any{"text": "The quick brown fox jumps over the dog."}
	params.ToolConfig = map[string]any{ToolConfigModelAliasKey: "planning"}

	if _, invokeErr := runner.Execute(t.Context(), params, entry, nil, statusCb); invokeErr != nil {
		t.Fatalf("execute: %+v", invokeErr)
	}
	if capturedAlias != "planning" {
		t.Errorf("modelAlias reaching the LLM closure = %q, want planning from tool_config", capturedAlias)
	}
}

func TestGoTool_LLMCallback_RejectsWrongTypedModelAlias(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)
	statusCb, _ := collectStatuses()

	runner.llmCall = func(_ context.Context, _, _ string, _ *remotetool.InvokeParams, _, _ string, _ float64) (string, error) {
		t.Error("LLM callback should not be reached when the alias is malformed")
		return "", nil
	}

	entry := &ManifestEntry{ToolName: "summarize", Executable: toolBinary}
	params := minimalParams()
	params.ToolName = "summarize"
	params.Args = map[string]any{"text": "hello"}
	params.ToolConfig = map[string]any{ToolConfigModelAliasKey: 42}

	_, invokeErr := runner.Execute(t.Context(), params, entry, nil, statusCb)
	if invokeErr == nil {
		t.Fatal("expected an error for a non-string model alias")
	}
	if !strings.Contains(invokeErr.Message, ToolConfigModelAliasKey) {
		t.Errorf("error %q should name the offending key", invokeErr.Message)
	}
}

func TestGoTool_LLMCallback_Error(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)

	runner.llmCall = func(_ context.Context, _, _ string, _ *remotetool.InvokeParams, _, _ string, _ float64) (string, error) {
		return "", fmt.Errorf("model quota exceeded")
	}

	entry := &ManifestEntry{
		ToolName:   "summarize",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:   "task-llm-err",
		ToolName: "summarize",
		Args:     map[string]any{"text": "some text"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "error" {
		t.Errorf("status: got %v, want 'error'", result.ToolResult["status"])
	}
	msg, _ := result.ToolResult["message"].(string)
	if !strings.Contains(msg, "LLM call failed") {
		t.Errorf("message should mention LLM failure: got %q", msg)
	}
	if result.ToolResult["error_code"] != "LLM_ERROR" {
		t.Errorf("error_code: got %v, want 'LLM_ERROR'", result.ToolResult["error_code"])
	}
}

func TestGoTool_LLMCallback_NoLLMService(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newTestRunner(t)

	entry := &ManifestEntry{
		ToolName:   "summarize",
		Executable: toolBinary,
	}

	params := &remotetool.InvokeParams{
		TaskID:   "task-llm-none",
		ToolName: "summarize",
		Args:     map[string]any{"text": "some text"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "error" {
		t.Errorf("status: got %v, want 'error'", result.ToolResult["status"])
	}
	msg, _ := result.ToolResult["message"].(string)
	if !strings.Contains(msg, "LLM call") || !strings.Contains(msg, "failed") {
		t.Errorf("message should mention LLM unavailability: got %q", msg)
	}
}

func TestPythonTool_Echo(t *testing.T) {
	pythonBin := findPython(t)
	runner := newTestRunner(t)
	statusCb, getStatuses := collectStatuses()

	pkgDir, _ := os.Getwd()
	toolScript := filepath.Join(pkgDir, "testdata", "python-test-tool", "tool.py")

	entry := &ManifestEntry{
		ToolName: "echo",
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-py-echo-1",
		ToolName:  "echo",
		Args:      map[string]any{"message": "Hello from Python!"},
		UserID:    "py-user",
		SessionID: "py-sess",
		AppName:   "py-agent",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := executePythonTool(ctx, runner, params, entry, nil, statusCb, toolScript, pythonBin)
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "success" {
		t.Errorf("status: got %v", result.ToolResult["status"])
	}
	if result.ToolResult["message"] != "Hello from Python!" {
		t.Errorf("message: got %v, want 'Hello from Python!'", result.ToolResult["message"])
	}

	data, ok := result.ToolResult["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not a map: %T", result.ToolResult["data"])
	}
	if data["user_id"] != "py-user" {
		t.Errorf("user_id: got %v", data["user_id"])
	}

	statuses := getStatuses()
	if len(statuses) < 1 {
		t.Errorf("expected status updates, got %d", len(statuses))
	}
}

func TestPythonTool_ProcessFile(t *testing.T) {
	pythonBin := findPython(t)
	runner := newTestRunner(t)
	artifactSvc := newArtifactService()

	pkgDir, _ := os.Getwd()
	toolScript := filepath.Join(pkgDir, "testdata", "python-test-tool", "tool.py")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	loc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "sample.txt")
	if _, err := artifactSvc.Inner.Save(ctx, loc, []byte("test content"), "text/plain"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	entry := &ManifestEntry{
		ToolName: "process_file",
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-py-process-1",
		ToolName:  "process_file",
		Args:      map[string]any{},
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input": {Filename: "sample.txt", Version: 0},
		},
	}

	result, invokeErr := executePythonTool(ctx, runner, params, entry, artifactSvc, func(_ string) {}, toolScript, pythonBin)
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "success" {
		t.Errorf("status: got %v", result.ToolResult["status"])
	}

	if len(result.CreatedArtifacts) == 0 {
		t.Fatal("expected at least 1 created artifact")
	}

	outLoc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "processed_sample.txt")
	outContent, _, err := artifactSvc.Inner.Load(ctx, outLoc, nil)
	if err != nil {
		t.Fatalf("load output: %v", err)
	}
	if string(outContent) != "TEST CONTENT" {
		t.Errorf("output: got %q, want 'TEST CONTENT'", string(outContent))
	}
}

func TestGoVsPython_SameResult(t *testing.T) {
	pythonBin := findPython(t)
	toolBinary := getGoTestTool(t)

	pkgDir, _ := os.Getwd()
	toolScript := filepath.Join(pkgDir, "testdata", "python-test-tool", "tool.py")

	runner := newTestRunner(t)
	artifactSvc := newArtifactService()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "compat.txt")
	if _, err := artifactSvc.Inner.Save(ctx, loc, []byte("compatibility test"), "text/plain"); err != nil {
		t.Fatalf("save: %v", err)
	}

	goEntry := &ManifestEntry{
		ToolName:   "process_file",
		Executable: toolBinary,
	}
	goParams := &remotetool.InvokeParams{
		TaskID:    "task-compat-go",
		ToolName:  "process_file",
		Args:      map[string]any{},
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input": {Filename: "compat.txt", Version: 0},
		},
	}

	goResult, goErr := runner.Execute(ctx, goParams, goEntry, artifactSvc, func(_ string) {})
	if goErr != nil {
		t.Fatalf("Go tool failed: %s: %s", goErr.Code, goErr.Message)
	}

	if _, err := artifactSvc.Inner.Save(ctx, loc, []byte("compatibility test"), "text/plain"); err != nil {
		t.Fatalf("re-save: %v", err)
	}

	pyEntry := &ManifestEntry{
		ToolName: "process_file",
	}
	pyParams := &remotetool.InvokeParams{
		TaskID:    "task-compat-py",
		ToolName:  "process_file",
		Args:      map[string]any{},
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input": {Filename: "compat.txt", Version: 0},
		},
	}

	pyResult, pyErr := executePythonTool(ctx, runner, pyParams, pyEntry, artifactSvc, func(_ string) {}, toolScript, pythonBin)
	if pyErr != nil {
		t.Fatalf("Python tool failed: %s: %s", pyErr.Code, pyErr.Message)
	}

	if goResult.ToolResult["status"] != pyResult.ToolResult["status"] {
		t.Errorf("status mismatch: Go=%v, Python=%v",
			goResult.ToolResult["status"], pyResult.ToolResult["status"])
	}

	if len(goResult.CreatedArtifacts) == 0 {
		t.Error("Go tool should produce artifacts")
	}
	if len(pyResult.CreatedArtifacts) == 0 {
		t.Error("Python tool should produce artifacts")
	}

	goOut := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "processed_compat.txt")
	goContent, _, err := artifactSvc.Inner.Load(ctx, goOut, nil)
	if err != nil {
		t.Fatalf("load Go output: %v", err)
	}
	if string(goContent) != "COMPATIBILITY TEST" {
		t.Errorf("Go output: got %q", string(goContent))
	}

}

func findPython(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found, skipping Python tool tests")
	}
	return path
}

func executePythonTool(
	ctx context.Context,
	runner *SandboxRunner,
	params *remotetool.InvokeParams,
	entry *ManifestEntry,
	artifactSvc *artifact.ScopedService,
	statusCb func(string),
	scriptPath, pythonBin string,
) (*remotetool.InvokeResult, *remotetool.InvokeError) {
	tmpDir, err := os.MkdirTemp("", "py-wrapper-*")
	if err != nil {
		return nil, &remotetool.InvokeError{
			Code:    remotetool.ErrInternalError,
			Message: fmt.Sprintf("create temp dir: %v", err),
		}
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")
	wrapperContent := fmt.Sprintf("#!/bin/sh\nexec %s %s \"$@\"\n", pythonBin, scriptPath)
	if err := os.WriteFile(wrapperPath, []byte(wrapperContent), 0o755); err != nil {
		return nil, &remotetool.InvokeError{
			Code:    remotetool.ErrInternalError,
			Message: fmt.Sprintf("write wrapper: %v", err),
		}
	}

	wrapperEntry := &ManifestEntry{
		ToolName:       entry.ToolName,
		Executable:     wrapperPath,
		TimeoutSeconds: entry.TimeoutSeconds,
	}

	return runner.Execute(ctx, params, wrapperEntry, artifactSvc, statusCb)
}

func newSpillRunner(t *testing.T, maxBytes int) *SandboxRunner {
	t.Helper()
	cfg := SandboxConfig{
		Mode:                        "direct",
		WorkBaseDir:                 t.TempDir(),
		DefaultTimeoutSeconds:       30,
		MaxConcurrentExecutions:     2,
		ToolOutputLLMReturnMaxBytes: maxBytes,
	}
	return NewSandboxRunner(cfg, slog.Default())
}

func TestSpill_BulkOutputViaData(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newSpillRunner(t, 1024)
	svc := newArtifactService()

	entry := &ManifestEntry{ToolName: "bulk_output", Executable: toolBinary}
	params := &remotetool.InvokeParams{
		TaskID:    "task-spill-data",
		ToolName:  "bulk_output",
		Args:      map[string]any{"size_bytes": 50000, "mode": "data"},
		UserID:    "user-spill",
		SessionID: "sess-spill",
		AppName:   "spill-agent",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, svc, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}
	if _, has := result.ToolResult["data"]; has {
		t.Errorf("data key should have been removed after spill")
	}
	objs, ok := result.ToolResult["data_objects"].([]any)
	if !ok || len(objs) != 1 {
		t.Fatalf("expected 1 data_objects entry, got %v", result.ToolResult["data_objects"])
	}
	entryMap := objs[0].(map[string]any)
	ref, ok := entryMap["artifact_ref"].(map[string]any)
	if !ok {
		t.Fatalf("missing artifact_ref on spilled entry: %v", entryMap)
	}
	filename, _ := ref["filename"].(string)
	if !strings.HasPrefix(filename, "bulk_output_result_") || !strings.HasSuffix(filename, ".json") {
		t.Errorf("unexpected spilled filename %q", filename)
	}
	if got, _ := entryMap["mime_type"].(string); got != "application/json" {
		t.Errorf("spilled data-map mime_type = %q, want application/json", got)
	}
	meta, _ := entryMap["metadata"].(map[string]any)
	if got, _ := meta["spilled_by_str"].(bool); !got {
		t.Errorf("spilled_by_str not set in metadata")
	}
	msg, _ := result.ToolResult["message"].(string)
	if !strings.Contains(msg, "Max result size exceeded") {
		t.Errorf("message missing spill notice: %q", msg)
	}

	stored, _, err := svc.Inner.Load(ctx, artifact.Locator{
		AppName: "spill-agent", UserID: "user-spill", SessionID: "sess-spill", Filename: filename,
	}, nil)
	if err != nil {
		t.Fatalf("load spilled artifact: %v", err)
	}
	if len(stored) < 50000 {
		t.Errorf("stored payload too small: %d bytes", len(stored))
	}
	if !strings.Contains(string(stored), `"payload"`) {
		t.Errorf("stored payload missing original key: %s", string(stored[:200]))
	}
}

func TestSpill_BulkOutputViaDataObjects(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newSpillRunner(t, 1024)
	svc := newArtifactService()

	entry := &ManifestEntry{ToolName: "bulk_output", Executable: toolBinary}
	params := &remotetool.InvokeParams{
		TaskID:    "task-spill-do",
		ToolName:  "bulk_output",
		Args:      map[string]any{"size_bytes": 50000, "mode": "data_objects"},
		UserID:    "user-spill",
		SessionID: "sess-spill",
		AppName:   "spill-agent",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, svc, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}
	objs, ok := result.ToolResult["data_objects"].([]any)
	if !ok || len(objs) != 1 {
		t.Fatalf("expected 1 data_objects entry, got %v", result.ToolResult["data_objects"])
	}
	entryMap := objs[0].(map[string]any)
	if entryMap["content"] != nil {
		t.Errorf("content not cleared after spill: %v", entryMap["content"])
	}
	if entryMap["disposition"] != "artifact" {
		t.Errorf("disposition = %v, want artifact", entryMap["disposition"])
	}
	ref, ok := entryMap["artifact_ref"].(map[string]any)
	if !ok {
		t.Fatalf("missing artifact_ref: %v", entryMap)
	}
	if got, _ := ref["filename"].(string); got != "bulk.txt" {
		t.Errorf("expected declared filename preserved, got %q", got)
	}

	stored, _, err := svc.Inner.Load(ctx, artifact.Locator{
		AppName: "spill-agent", UserID: "user-spill", SessionID: "sess-spill", Filename: "bulk.txt",
	}, nil)
	if err != nil {
		t.Fatalf("load spilled artifact: %v", err)
	}
	if len(stored) != 50000 {
		t.Errorf("stored bytes = %d, want 50000", len(stored))
	}
}

func TestSpill_UnderCap_PassesThrough(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newSpillRunner(t, 65536)
	svc := newArtifactService()

	entry := &ManifestEntry{ToolName: "bulk_output", Executable: toolBinary}
	params := &remotetool.InvokeParams{
		TaskID:    "task-spill-pass",
		ToolName:  "bulk_output",
		Args:      map[string]any{"size_bytes": 1024, "mode": "data"},
		UserID:    "user-spill",
		SessionID: "sess-spill",
		AppName:   "spill-agent",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, svc, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}
	if _, has := result.ToolResult["data"]; !has {
		t.Errorf("data key removed even though under cap")
	}
	if objs, _ := result.ToolResult["data_objects"].([]any); len(objs) != 0 {
		t.Errorf("data_objects populated despite small payload: %v", objs)
	}
}

func TestSpill_DefaultCap_50KBDataObjectPassesThrough(t *testing.T) {
	toolBinary := getGoTestTool(t)
	runner := newSpillRunner(t, 102400)
	svc := newArtifactService()

	entry := &ManifestEntry{ToolName: "bulk_output", Executable: toolBinary}
	params := &remotetool.InvokeParams{
		TaskID:    "task-spill-50k",
		ToolName:  "bulk_output",
		Args:      map[string]any{"size_bytes": 50000, "mode": "data_objects"},
		UserID:    "user-spill",
		SessionID: "sess-spill",
		AppName:   "spill-agent",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, svc, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}
	objs, ok := result.ToolResult["data_objects"].([]any)
	if !ok || len(objs) != 1 {
		t.Fatalf("expected 1 data_objects entry, got %v", result.ToolResult["data_objects"])
	}
	entryMap := objs[0].(map[string]any)
	if _, spilled := entryMap["artifact_ref"]; spilled {
		t.Errorf("50 KB object must not spill at the 100 KB cap: %v", entryMap)
	}
	content, _ := entryMap["content"].(string)
	if len(content) != 50000 {
		t.Errorf("content not preserved for inline: got %d bytes, want 50000", len(content))
	}
	if entryMap["disposition"] != "inline" {
		t.Errorf("disposition mutated to %v; STR must pass it through unchanged", entryMap["disposition"])
	}
}
