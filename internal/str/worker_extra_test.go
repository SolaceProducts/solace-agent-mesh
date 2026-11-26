// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

func TestIsInitRequestTopic(t *testing.T) {
	tests := []struct {
		topic string
		want  bool
	}{
		{"test-ns/a2a/v1/sam_remote_tool/init_request/my_tool", true},
		{"ns/a2a/v1/sam_remote_tool/init_request/echo", true},
		{"ns/a2a/v1/sam_remote_tool/invoke/echo", false},
		{"ns/a2a/v1/sam_remote_tool/init/echo", false},
		{"", false},
		{"/init_request/", true},
		{"init_request", false},
	}
	for _, tt := range tests {
		got := isInitRequestTopic(tt.topic)
		if got != tt.want {
			t.Errorf("isInitRequestTopic(%q) = %v, want %v", tt.topic, got, tt.want)
		}
	}
}

func TestDiscoveredToInitParams_Basic(t *testing.T) {
	w := &Worker{logger: slog.Default()}
	info := &DiscoveredTool{
		Description:  "A test tool",
		Parameters:   map[string]any{"message": map[string]any{"type": "string"}},
		Instructions: "Use this tool for testing",
	}

	result := w.discoveredToInitParams("test_tool", info)

	if result.ToolName != "test_tool" {
		t.Errorf("ToolName = %q, want %q", result.ToolName, "test_tool")
	}
	if result.Description != "A test tool" {
		t.Errorf("Description = %q, want %q", result.Description, "A test tool")
	}
	if result.Instructions != "Use this tool for testing" {
		t.Errorf("Instructions = %q, want %q", result.Instructions, "Use this tool for testing")
	}
	if result.Parameters == nil {
		t.Error("Parameters should not be nil")
	}
	if result.ArtifactParams != nil {
		t.Error("ArtifactParams should be nil when none provided")
	}
}

func TestDiscoveredToInitParams_WithArtifactParams(t *testing.T) {
	w := &Worker{logger: slog.Default()}
	info := &DiscoveredTool{
		Description: "Tool with artifacts",
		ArtifactParams: map[string]discoveredArtifactParam{
			"input_file": {IsList: false, IsOptional: true},
			"files":      {IsList: true, IsOptional: false},
		},
	}

	result := w.discoveredToInitParams("artifact_tool", info)

	if len(result.ArtifactParams) != 2 {
		t.Fatalf("ArtifactParams length = %d, want 2", len(result.ArtifactParams))
	}
	ap := result.ArtifactParams["input_file"]
	if ap.IsList {
		t.Error("input_file.IsList should be false")
	}
	if !ap.IsOptional {
		t.Error("input_file.IsOptional should be true")
	}
	ap2 := result.ArtifactParams["files"]
	if !ap2.IsList {
		t.Error("files.IsList should be true")
	}
	if ap2.IsOptional {
		t.Error("files.IsOptional should be false")
	}
}

func TestDiscoveredToInitParams_WithConfigSchema(t *testing.T) {
	w := &Worker{logger: slog.Default()}
	info := &DiscoveredTool{
		Description: "Configurable tool",
		ConfigSchema: []map[string]any{
			{"name": "api_key", "type": "string", "required": true},
		},
	}

	result := w.discoveredToInitParams("config_tool", info)
	if len(result.ConfigSchema) != 1 {
		t.Fatalf("ConfigSchema length = %d, want 1", len(result.ConfigSchema))
	}
}

func TestDiscoveredToInitParams_Empty(t *testing.T) {
	w := &Worker{logger: slog.Default()}
	info := &DiscoveredTool{}

	result := w.discoveredToInitParams("empty_tool", info)

	if result.ToolName != "empty_tool" {
		t.Errorf("ToolName = %q, want %q", result.ToolName, "empty_tool")
	}
	if result.ArtifactParams != nil {
		t.Error("ArtifactParams should be nil for empty tool")
	}
}

func TestPublishStatus_EmptyStatusTo(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	w.publishStatus("", "task-1", "processing...", "")
}

func TestPublishStatus_PublishesToTopic(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	statusTopic := "test-ns/a2a/v1/sam_remote_tool/status/test-agent/corr-1"

	q, err := brk.CreateQueue("status-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, statusTopic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishStatus(statusTopic, "task-42", "executing tool...", "trace-xyz")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected a status message")
	}
	msg.Ack()

	var req a2a.Request
	if unmarshalErr := json.Unmarshal(msg.Payload, &req); unmarshalErr != nil {
		t.Fatalf("unmarshal status: %v", unmarshalErr)
	}
	if req.Method != string(remotetool.MethodStatus) {
		t.Errorf("method = %q, want %q", req.Method, remotetool.MethodStatus)
	}

	paramsData, _ := json.Marshal(req.Params)
	var params remotetool.StatusParams
	if unmarshalErr := json.Unmarshal(paramsData, &params); unmarshalErr != nil {
		t.Fatalf("unmarshal status params: %v", unmarshalErr)
	}
	if params.TaskID != "task-42" {
		t.Errorf("TaskID = %q, want %q", params.TaskID, "task-42")
	}
	if params.StatusText != "executing tool..." {
		t.Errorf("StatusText = %q, want %q", params.StatusText, "executing tool...")
	}
	if params.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}
}

func TestPublishErrorWithID(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	replyTopic := "test-ns/reply/test-tool/12345"

	q, err := brk.CreateQueue("err-reply-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	msg := &broker.Message{
		Payload: []byte(`{"jsonrpc":"2.0","id":"req-1","method":"invoke"}`),
		UserProperties: map[string]string{
			remotetool.PropReplyTo: replyTopic,
		},
	}

	if err = w.publishErrorWithID(msg, "req-1", remotetool.ErrToolNotAvailable, "tool not found"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	resp, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected an error response message")
	}
	resp.Ack()

	var errResp struct {
		JSONRPC string                  `json:"jsonrpc"`
		ID      string                  `json:"id"`
		Error   *remotetool.InvokeError `json:"error"`
	}
	if unmarshalErr := json.Unmarshal(resp.Payload, &errResp); unmarshalErr != nil {
		t.Fatalf("unmarshal error response: %v", unmarshalErr)
	}
	if errResp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", errResp.JSONRPC, "2.0")
	}
	if errResp.ID != "req-1" {
		t.Errorf("id = %q, want %q", errResp.ID, "req-1")
	}
	if errResp.Error == nil {
		t.Fatal("expected error field")
	}
	if errResp.Error.Code != remotetool.ErrToolNotAvailable {
		t.Errorf("error code = %q, want %q", errResp.Error.Code, remotetool.ErrToolNotAvailable)
	}
	if errResp.Error.Message != "tool not found" {
		t.Errorf("error message = %q, want %q", errResp.Error.Message, "tool not found")
	}
}

func TestPublishErrorWithID_NoReplyTo(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	msg := &broker.Message{
		Payload:        []byte(`{"jsonrpc":"2.0","id":"req-1","method":"invoke"}`),
		UserProperties: map[string]string{},
	}

	if err := w.publishErrorWithID(msg, "req-1", remotetool.ErrToolError, "some error"); err != nil {
		t.Errorf("a call with no reply topic has nobody to answer and must count as answered, got %v", err)
	}
}

func TestPublishError_ExtractsIDFromPayload(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	replyTopic := "test-ns/reply/err-test/99"
	q, err := brk.CreateQueue("err-extract-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	msg := &broker.Message{
		Payload: []byte(`{"jsonrpc":"2.0","id":"extracted-req-id","method":"invoke","params":{}}`),
		UserProperties: map[string]string{
			remotetool.PropReplyTo: replyTopic,
		},
	}

	if err = w.publishError(msg, remotetool.ErrInvalidRequest, "bad params"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	resp, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected error response")
	}
	resp.Ack()

	var errResp struct {
		ID string `json:"id"`
	}
	if unmarshalErr := json.Unmarshal(resp.Payload, &errResp); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if errResp.ID != "extracted-req-id" {
		t.Errorf("id = %q, want %q", errResp.ID, "extracted-req-id")
	}
}

func TestPublishError_MalformedPayload(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	replyTopic := "test-ns/reply/err-malformed/1"
	q, err := brk.CreateQueue("err-malformed-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	msg := &broker.Message{
		Payload: []byte(`not-json`),
		UserProperties: map[string]string{
			remotetool.PropReplyTo: replyTopic,
		},
	}

	if err = w.publishError(msg, remotetool.ErrToolError, "something broke"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	resp, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected error response even with malformed payload")
	}
	resp.Ack()

	var errResp struct {
		ID    string                  `json:"id"`
		Error *remotetool.InvokeError `json:"error"`
	}
	if unmarshalErr := json.Unmarshal(resp.Payload, &errResp); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if errResp.ID != "" {
		t.Errorf("id = %q, want empty string", errResp.ID)
	}
	if errResp.Error == nil {
		t.Fatal("expected error field")
	}
}

func TestPublishToolRemoved(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	removedTopic := remotetool.RemovedTopic("test-ns", "old_tool")
	q, err := brk.CreateQueue("removed-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, removedTopic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishToolRemoved(t.Context(), "old_tool")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected removed notification")
	}
	msg.Ack()

	var req a2a.Request
	if unmarshalErr := json.Unmarshal(msg.Payload, &req); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if req.Method != string(remotetool.MethodRemoved) {
		t.Errorf("method = %q, want %q", req.Method, remotetool.MethodRemoved)
	}

	paramsData, _ := json.Marshal(req.Params)
	var params remotetool.RemovedParams
	if unmarshalErr := json.Unmarshal(paramsData, &params); unmarshalErr != nil {
		t.Fatalf("unmarshal params: %v", unmarshalErr)
	}
	if params.ToolName != "old_tool" {
		t.Errorf("ToolName = %q, want %q", params.ToolName, "old_tool")
	}
}

func TestPublishInitError(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	replyTopic := "test-ns/reply/init-err/1"
	q, err := brk.CreateQueue("init-err-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishInitError(t.Context(), replyTopic, "init-req-1", "schema resolution failed: timeout", "")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected init error response")
	}
	msg.Ack()

	var errResp struct {
		JSONRPC string                  `json:"jsonrpc"`
		ID      string                  `json:"id"`
		Error   *remotetool.InvokeError `json:"error"`
	}
	if unmarshalErr := json.Unmarshal(msg.Payload, &errResp); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if errResp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", errResp.JSONRPC, "2.0")
	}
	if errResp.ID != "init-req-1" {
		t.Errorf("id = %q, want %q", errResp.ID, "init-req-1")
	}
	if errResp.Error == nil {
		t.Fatal("expected error")
	}
	if errResp.Error.Code != remotetool.ErrToolError {
		t.Errorf("code = %q, want %q", errResp.Error.Code, remotetool.ErrToolError)
	}
}

func TestSyncSubscriptions_SubscribesManifestTools(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("syncSubscriptions: %v", err)
	}

	if len(w.subscribedTools) != 2 {
		t.Errorf("subscribedTools = %d, want 2", len(w.subscribedTools))
	}
	if _, ok := w.subscribedTools["echo_tool"]; !ok {
		t.Error("echo_tool not in subscribedTools")
	}
	if _, ok := w.subscribedTools["secret_tool"]; !ok {
		t.Error("secret_tool not in subscribedTools")
	}
	if !w.initRequestSubActive {
		t.Error("initRequestSubActive should be true")
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestSyncSubscriptions_IdempotentOnSecondCall(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("first syncSubscriptions: %v", err)
	}

	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("second syncSubscriptions: %v", err)
	}

	if len(w.subscribedTools) != 2 {
		t.Errorf("subscribedTools = %d after second call, want 2", len(w.subscribedTools))
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestPackageScanOnce_SubscribesNewlyAddedTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on Unix executable bit + shell stub")
	}

	brk := newTestBroker(t)
	packagesDir := t.TempDir()
	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:    "test-worker",
		Namespace:   "test-ns",
		ToolsetsDir: packagesDir,
		Sandbox:     SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("initial syncSubscriptions: %v", err)
	}
	t.Cleanup(func() { _ = brk.DeleteQueue(w.queue.Name()) })

	w.subsMu.Lock()
	gotSubs := len(w.subscribedTools)
	w.subsMu.Unlock()
	if gotSubs != 0 {
		t.Fatalf("startup subscribedTools count = %d, want 0", gotSubs)
	}

	pkgName := "echopkg"
	pkgDir := filepath.Join(packagesDir, pkgName)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
case "$1" in
  --schema) printf '%s' '{"tools":{"echo_tool":{"description":"test","parameters":{"type":"object","properties":{"text":{"type":"string"}}}}}}' ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(pkgDir, "echo_tool"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	pkgManifest := `version: 1
tools:
  echo_tool:
    runtime: native
    executable: ./echo_tool
    parameters:
      - name: text
        type: string
        required: true
`
	if err := os.WriteFile(filepath.Join(pkgDir, "manifest.yaml"), []byte(pkgManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	w.packageScanOnce(ctx)

	expected := pkgName + "__echo_tool"
	w.subsMu.Lock()
	_, ok := w.subscribedTools[expected]
	got := make([]string, 0, len(w.subscribedTools))
	for name := range w.subscribedTools {
		got = append(got, name)
	}
	w.subsMu.Unlock()
	if !ok {
		t.Fatalf("after packageScanOnce, %q not in subscribedTools = %v", expected, got)
	}
}

func TestPackageScanOnce_EmptyDirIsNoop(t *testing.T) {
	brk := newTestBroker(t)
	packagesDir := t.TempDir()
	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:    "test-worker",
		Namespace:   "test-ns",
		ToolsetsDir: packagesDir,
		Sandbox:     SandboxConfig{Mode: "direct"},
	})
	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("syncSubscriptions: %v", err)
	}
	t.Cleanup(func() { _ = brk.DeleteQueue(w.queue.Name()) })

	w.packageScanOnce(ctx)

	w.subsMu.Lock()
	gotSubs := len(w.subscribedTools)
	w.subsMu.Unlock()
	if gotSubs != 0 {
		t.Fatalf("subscribedTools count after empty-dir scan = %d, want 0", gotSubs)
	}
}

func TestPackageScanOnce_TriggerDrivesScan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on Unix executable bit + shell stub")
	}

	brk := newTestBroker(t)
	packagesDir := t.TempDir()
	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:    "test-worker",
		Namespace:   "test-ns",
		ToolsetsDir: packagesDir,
		Sandbox:     SandboxConfig{Mode: "direct"},
	})

	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("syncSubscriptions: %v", err)
	}
	t.Cleanup(func() { _ = brk.DeleteQueue(w.queue.Name()) })

	pkgName := "loopkg"
	pkgDir := filepath.Join(packagesDir, pkgName)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
case "$1" in
  --schema) printf '%s' '{"tools":{"loop_tool":{"description":"loop","parameters":{"type":"object","properties":{}}}}}' ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(pkgDir, "loop_tool"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	pkgManifest := `version: 1
tools:
  loop_tool:
    runtime: native
    executable: ./loop_tool
    parameters:
      - name: text
        type: string
`
	if err := os.WriteFile(filepath.Join(pkgDir, "manifest.yaml"), []byte(pkgManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	loopCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.reconcileRunLoop(loopCtx)
		close(done)
	}()

	w.signalReconcile(reconcileReasonPush)

	expected := pkgName + "__loop_tool"
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w.subsMu.Lock()
		_, ok := w.subscribedTools[expected]
		w.subsMu.Unlock()
		if ok {
			cancel()
			<-done
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("after 3s, %q not in subscribedTools", expected)
}

func TestWorker_ManifestPathsAndToolsetsDir_BothLoad(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on Unix executable bit")
	}

	brk := newTestBroker(t)

	builtinDir := t.TempDir()
	builtinStub := filepath.Join(builtinDir, "builtin_tool")
	if err := os.WriteFile(builtinStub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	builtinManifest := `version: 1
tools:
  builtin_tool:
    executable: ./builtin_tool
    parameters:
      type: object
      properties: {}
`
	builtinManifestPath := filepath.Join(builtinDir, "manifest.yaml")
	if err := os.WriteFile(builtinManifestPath, []byte(builtinManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	packagesDir := t.TempDir()
	pkgDir := filepath.Join(packagesDir, "uploaded_pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkgStub := `#!/bin/sh
case "$1" in
  --schema) printf '%s' '{"tools":{"uploaded_tool":{"description":"test","parameters":{"type":"object","properties":{}}}}}' ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(pkgDir, "uploaded_tool"), []byte(pkgStub), 0o755); err != nil {
		t.Fatal(err)
	}
	pkgManifest := `version: 1
tools:
  uploaded_tool:
    runtime: native
    executable: ./uploaded_tool
    parameters:
      - name: text
        type: string
`
	if err := os.WriteFile(filepath.Join(pkgDir, "manifest.yaml"), []byte(pkgManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{builtinManifestPath},
		ToolsetsDir:   packagesDir,
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("initial syncSubscriptions: %v", err)
	}
	t.Cleanup(func() { _ = brk.DeleteQueue(w.queue.Name()) })

	w.packageScanOnce(ctx)

	w.subsMu.Lock()
	subs := make(map[string]struct{}, len(w.subscribedTools))
	for name := range w.subscribedTools {
		subs[name] = struct{}{}
	}
	w.subsMu.Unlock()

	if _, ok := subs["builtin_tool"]; !ok {
		t.Errorf("builtin_tool from ManifestPaths not in subscribedTools; got %v", subs)
	}
	if _, ok := subs["uploaded_pkg__uploaded_tool"]; !ok {
		t.Errorf("uploaded_pkg__uploaded_tool from ToolsetsDir not in subscribedTools; got %v", subs)
	}
}

func TestUnsubscribeTool(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.syncSubscriptions(); err != nil {
		t.Fatal(err)
	}

	if _, ok := w.subscribedTools["echo_tool"]; !ok {
		t.Fatal("echo_tool should be subscribed before unsubscribe")
	}

	w.unsubscribeTool("echo_tool")

	if _, ok := w.subscribedTools["echo_tool"]; ok {
		t.Error("echo_tool should not be subscribed after unsubscribe")
	}

	w.unsubscribeTool("nonexistent_tool")

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestPublishToolInits(t *testing.T) {
	brk := newTestBroker(t)

	dir := t.TempDir()
	manifest := `version: 1
tools:
  echo_tool:
    executable: /bin/echo
    description: "Echoes back the input"
    parameters:
      message:
        type: string
    instructions: "Use echo_tool to echo messages"
  bare_tool:
    executable: /usr/bin/bare
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	echoInitTopic := remotetool.InitTopic("test-ns", "echo_tool")
	bareInitTopic := remotetool.InitTopic("test-ns", "bare_tool")

	echoQ, err := brk.CreateQueue("echo-init-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(echoQ, echoInitTopic); subErr != nil {
		t.Fatal(subErr)
	}

	bareQ, err := brk.CreateQueue("bare-init-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(bareQ, bareInitTopic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishToolInits(ctx)

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, echoQ)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected init message for echo_tool")
	}
	msg.Ack()

	bareCtx, bareCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer bareCancel()
	bareMsg, _ := brk.Receive(bareCtx, bareQ)
	if bareMsg != nil {
		bareMsg.Ack()
		t.Error("bare_tool should not get an init message (no parameters or instructions)")
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestPublishToolInits_AuthSurvives(t *testing.T) {
	brk := newTestBroker(t)

	dir := t.TempDir()
	manifest := `version: 1
tools:
  placeholder:
    executable: /usr/bin/placeholder
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	w.manifest.Register("authed_tool", &ManifestEntry{
		ToolName:    "authed_tool",
		Description: "tool with oauth2 auth",
		Parameters:  map[string]any{"type": "object"},
		Auth: &ToolDeclaredAuth{
			Type: "oauth2",
			Scheme: &ToolDeclaredAuthScheme{
				AuthorizationURL:        "https://auth.example.com/authorize",
				TokenURL:                "https://auth.example.com/token",
				Scopes:                  []string{"read", "write"},
				TokenEndpointAuthMethod: "client_secret_post",
			},
		},
	})

	initTopic := remotetool.InitTopic("test-ns", "authed_tool")
	q, err := brk.CreateQueue("authed-init-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, initTopic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishToolInits(ctx)

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, q)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected init message for authed_tool")
	}
	defer msg.Ack()

	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	paramsJSON, _ := json.Marshal(req.Params)
	var params remotetool.InitParams
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("decode init params: %v", err)
	}

	if params.ToolName != "authed_tool" {
		t.Fatalf("tool_name = %q", params.ToolName)
	}
	if params.Auth == nil {
		t.Fatal("init params Auth nil — wire didn't carry tool-declared auth")
	}
	if params.Auth.Type != "oauth2" {
		t.Errorf("auth.type = %q", params.Auth.Type)
	}
	if params.Auth.Scheme == nil {
		t.Fatal("auth.scheme nil")
	}
	if params.Auth.Scheme.AuthorizationURL != "https://auth.example.com/authorize" {
		t.Errorf("authorization_url = %q", params.Auth.Scheme.AuthorizationURL)
	}
	if params.Auth.Scheme.TokenURL != "https://auth.example.com/token" {
		t.Errorf("token_url = %q", params.Auth.Scheme.TokenURL)
	}
	if len(params.Auth.Scheme.Scopes) != 2 {
		t.Errorf("scopes = %v", params.Auth.Scheme.Scopes)
	}
	if params.Auth.Scheme.TokenEndpointAuthMethod != "client_secret_post" {
		t.Errorf("token_endpoint_auth_method = %q", params.Auth.Scheme.TokenEndpointAuthMethod)
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestPublishToolInits_RuntimeFieldsSurvive(t *testing.T) {
	prevOS, prevArch := runtimeGOOS, runtimeGOARCH
	runtimeGOOS = func() string { return "linux" }
	runtimeGOARCH = func() string { return "arm64" }
	t.Cleanup(func() {
		runtimeGOOS = prevOS
		runtimeGOARCH = prevArch
	})

	brk := newTestBroker(t)
	dir := t.TempDir()
	manifestSrc := `version: 1
tools:
  placeholder:
    executable: /usr/bin/placeholder
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifestSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	w.manifest.Register("runtime_tool", &ManifestEntry{
		ToolName:    "runtime_tool",
		Description: "exercises runtime fields",
		Parameters:  map[string]any{"type": "object"},
	})

	initTopic := remotetool.InitTopic("test-ns", "runtime_tool")
	q, err := brk.CreateQueue("runtime-init-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, initTopic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishToolInits(ctx)

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, q)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected init message")
	}
	defer msg.Ack()

	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	paramsJSON, _ := json.Marshal(req.Params)
	var params remotetool.InitParams
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("decode init params: %v", err)
	}
	if params.RuntimeOS != "linux" || params.RuntimeArch != "arm64" {
		t.Errorf("InitParams.Runtime{OS,Arch} = (%q, %q); want (linux, arm64)",
			params.RuntimeOS, params.RuntimeArch)
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestPublishToolInitsForNames_OnlyNamedTools(t *testing.T) {
	brk := newTestBroker(t)

	dir := t.TempDir()
	writeSchemaStubForManifestTest(t, filepath.Join(dir, "tool_a"), "tool_a", "Tool A")
	writeSchemaStubForManifestTest(t, filepath.Join(dir, "tool_b"), "tool_b", "Tool B")
	manifest := `version: 1
tools:
  tool_a:
    executable: ./tool_a
    description: "Tool A"
    parameters:
      x:
        type: string
  tool_b:
    executable: ./tool_b
    description: "Tool B"
    parameters:
      y:
        type: int
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	aQ, err := brk.CreateQueue("a-init-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(aQ, remotetool.InitTopic("test-ns", "tool_a")); subErr != nil {
		t.Fatal(subErr)
	}

	bQ, err := brk.CreateQueue("b-init-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(bQ, remotetool.InitTopic("test-ns", "tool_b")); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishToolInitsForNames(ctx, map[string]struct{}{"tool_a": {}})

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, aQ)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected init for tool_a")
	}
	msg.Ack()

	bCtx, bCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer bCancel()
	bMsg, _ := brk.Receive(bCtx, bQ)
	if bMsg != nil {
		bMsg.Ack()
		t.Error("tool_b should not have received an init message")
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestWorkerInit_CreatesQueueAndManifest(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "init-test-worker",
		Namespace:     "init-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if w.queue == nil {
		t.Fatal("queue should not be nil after Init")
	}
	expectedQueueName := "init-ns/q/str/init-test-worker"
	if w.queue.Name() != expectedQueueName {
		t.Errorf("queue name = %q, want %q", w.queue.Name(), expectedQueueName)
	}

	if w.manifest == nil {
		t.Fatal("manifest should not be nil")
	}
	tools := w.manifest.ToolNames()
	if len(tools) != 2 {
		t.Errorf("tools = %d, want 2", len(tools))
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestWorkerInit_BadManifest(t *testing.T) {
	brk := newTestBroker(t)

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "bad-manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: [invalid yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "fail-worker",
		Namespace:     "fail-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	err := w.Init(ctx, nil)
	if err == nil {
		t.Fatal("expected error for invalid manifest")
	}
}

func TestWorkerStartStop(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := w.Health(); err != nil {
		t.Errorf("Health: %v", err)
	}

	if err := w.Stop(ctx); err != nil {
		t.Errorf("Stop: %v", err)
	}

	w.subsMu.Lock()
	if len(w.subscribedTools) == 0 {
		t.Errorf("subscribedTools = 0 after Stop, want retained for durable-queue spool-through-restart")
	}
	w.subsMu.Unlock()
}

func TestHandleInitRequest_ToolNotInManifest(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	replyTopic := "test-ns/reply/init-request/1"
	replyQ, err := brk.CreateQueue("init-req-reply-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(replyQ, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	initReqParams := remotetool.InitRequestParams{
		ToolName: "nonexistent_tool",
	}
	req := a2a.NewRequest("init-req-1", remotetool.MethodInitRequest, initReqParams)
	payload, _ := json.Marshal(req)

	acked := false
	msg := broker.NewDeliveredMessage(
		"msg-1",
		"test-ns/a2a/v1/sam_remote_tool/init_request/nonexistent_tool",
		payload,
		func() { acked = true },
		nil,
	)
	msg.UserProperties = map[string]string{
		remotetool.PropReplyTo:  replyTopic,
		remotetool.PropClientID: "test-agent",
	}

	w.handleInitRequest(ctx, msg)

	if !acked {
		t.Error("message should be acked")
	}

	shortCtx, shortCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer shortCancel()
	resp, _ := brk.Receive(shortCtx, replyQ)
	if resp != nil {
		resp.Ack()
		t.Error("should not receive a response for tool not in manifest")
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestHandleInitRequest_StaticSchema(t *testing.T) {
	brk := newTestBroker(t)

	dir := t.TempDir()
	manifest := `version: 1
tools:
  echo_tool:
    executable: /bin/echo
    description: "Echoes input"
    parameters:
      message:
        type: string
    instructions: "Send a message to echo"
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	replyTopic := "test-ns/reply/init-request/2"
	replyQ, err := brk.CreateQueue("init-req-reply-q-2", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(replyQ, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	initReqParams := remotetool.InitRequestParams{
		ToolName: "echo_tool",
	}
	req := a2a.NewRequest("init-req-2", remotetool.MethodInitRequest, initReqParams)
	payload, _ := json.Marshal(req)

	acked := false
	msg := broker.NewDeliveredMessage(
		"msg-2",
		"test-ns/a2a/v1/sam_remote_tool/init_request/echo_tool",
		payload,
		func() { acked = true },
		nil,
	)
	msg.UserProperties = map[string]string{
		remotetool.PropReplyTo:  replyTopic,
		remotetool.PropClientID: "test-agent",
	}

	w.handleInitRequest(ctx, msg)

	if !acked {
		t.Error("message should be acked")
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := brk.Receive(recvCtx, replyQ)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected init response")
	}
	resp.Ack()

	var jsonRPCResp struct {
		ID     string                  `json:"id"`
		Result *remotetool.InitParams  `json:"result"`
		Error  *remotetool.InvokeError `json:"error"`
	}
	if unmarshalErr := json.Unmarshal(resp.Payload, &jsonRPCResp); unmarshalErr != nil {
		t.Fatalf("unmarshal response: %v", unmarshalErr)
	}
	if jsonRPCResp.Error != nil {
		t.Fatalf("unexpected error: %v", jsonRPCResp.Error)
	}
	if jsonRPCResp.Result == nil {
		t.Fatal("expected result")
	}
	if jsonRPCResp.Result.ToolName != "echo_tool" {
		t.Errorf("ToolName = %q, want %q", jsonRPCResp.Result.ToolName, "echo_tool")
	}
	if jsonRPCResp.Result.Description != "Echoes input" {
		t.Errorf("Description = %q, want %q", jsonRPCResp.Result.Description, "Echoes input")
	}
	if _, ok := jsonRPCResp.Result.Parameters["message"]; !ok {
		t.Errorf("Parameters is missing the manifest-declared \"message\" property, so the agent "+
			"would advertise this tool with no schema: %#v", jsonRPCResp.Result.Parameters)
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestHandleInitRequest_NoReplyTo(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	initReqParams := remotetool.InitRequestParams{
		ToolName: "echo_tool",
	}
	req := a2a.NewRequest("init-req-3", remotetool.MethodInitRequest, initReqParams)
	payload, _ := json.Marshal(req)

	acked := false
	msg := broker.NewDeliveredMessage(
		"msg-3",
		"test-ns/a2a/v1/sam_remote_tool/init_request/echo_tool",
		payload,
		func() { acked = true },
		nil,
	)
	msg.UserProperties = map[string]string{}

	w.handleInitRequest(ctx, msg)

	if !acked {
		t.Error("message should be acked even with no replyTo")
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestHandleInitRequest_MalformedPayload(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	acked := false
	msg := broker.NewDeliveredMessage(
		"msg-4",
		"test-ns/a2a/v1/sam_remote_tool/init_request/echo_tool",
		[]byte(`not-valid-json`),
		func() { acked = true },
		nil,
	)
	msg.UserProperties = map[string]string{remotetool.PropReplyTo: "some/reply"}

	w.handleInitRequest(ctx, msg)

	if !acked {
		t.Error("message should be acked even with malformed payload")
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestHandleInitRequest_ConnectorToolEmitsRequestedName(t *testing.T) {
	brk := newTestBroker(t)

	dir := t.TempDir()
	manifest := `version: 1
tools:
  slack:
    executable: /bin/echo
    description: "Slack connector"
    parameters:
      channel:
        type: string
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &testServices{brk: brk}
	w := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	replyTopic := "test-ns/reply/init-request/connector"
	replyQ, err := brk.CreateQueue("init-req-reply-connector", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(replyQ, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	dynamicName := "slack_send_message_abc12345"
	initReqParams := remotetool.InitRequestParams{
		ToolName:   dynamicName,
		ToolConfig: map[string]any{"_str_binary": "slack"},
	}
	req := a2a.NewRequest("init-req-connector", remotetool.MethodInitRequest, initReqParams)
	payload, _ := json.Marshal(req)

	acked := false
	msg := broker.NewDeliveredMessage(
		"msg-connector",
		"test-ns/a2a/v1/sam_remote_tool/init_request/"+dynamicName,
		payload,
		func() { acked = true },
		nil,
	)
	msg.UserProperties = map[string]string{
		remotetool.PropReplyTo:  replyTopic,
		remotetool.PropClientID: "test-agent",
	}

	w.handleInitRequest(ctx, msg)

	if !acked {
		t.Error("message should be acked")
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := brk.Receive(recvCtx, replyQ)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected init response")
	}
	resp.Ack()

	var jsonRPCResp struct {
		ID     string                  `json:"id"`
		Result *remotetool.InitParams  `json:"result"`
		Error  *remotetool.InvokeError `json:"error"`
	}
	if unmarshalErr := json.Unmarshal(resp.Payload, &jsonRPCResp); unmarshalErr != nil {
		t.Fatalf("unmarshal response: %v", unmarshalErr)
	}
	if jsonRPCResp.Error != nil {
		t.Fatalf("unexpected error: %v", jsonRPCResp.Error)
	}
	if jsonRPCResp.Result == nil {
		t.Fatal("expected result")
	}

	if jsonRPCResp.Result.ToolName != dynamicName {
		t.Errorf("ToolName = %q, want %q", jsonRPCResp.Result.ToolName, dynamicName)
	}
	if jsonRPCResp.Result.Description != "Slack connector" {
		t.Errorf("Description = %q, want %q", jsonRPCResp.Result.Description, "Slack connector")
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestResolveStaticToolSchema(t *testing.T) {
	w := &Worker{logger: slog.Default(), cfg: WorkerConfig{Sandbox: SandboxConfig{MaxTimeoutSeconds: 1800}}}

	entry := &ManifestEntry{
		ToolName:     "test_tool",
		Description:  "A test tool",
		Parameters:   map[string]any{"x": "string"},
		Instructions: "Use it wisely",
		ArtifactParams: map[string]discoveredArtifactParam{
			"file": {IsList: false, IsOptional: true},
		},
		TimeoutSeconds: 3600,
	}

	result, err := w.resolveStaticToolSchema(entry)
	if err != nil {
		t.Fatalf("resolveStaticToolSchema: %v", err)
	}
	if result.TimeoutSeconds != 1800 {
		t.Errorf("TimeoutSeconds = %d, want the 1800 ceiling the STR enforces over the manifest's 3600", result.TimeoutSeconds)
	}
	if result.ToolName != "test_tool" {
		t.Errorf("ToolName = %q, want %q", result.ToolName, "test_tool")
	}
	if result.Description != "A test tool" {
		t.Errorf("Description = %q", result.Description)
	}
	if result.Instructions != "Use it wisely" {
		t.Errorf("Instructions = %q", result.Instructions)
	}
	if len(result.ArtifactParams) != 1 {
		t.Errorf("ArtifactParams length = %d, want 1", len(result.ArtifactParams))
	}
}

func TestLookupDiscoveredTool_MultiToolPrefixedNames(t *testing.T) {
	discovered := map[string]DiscoveredTool{
		"search_users":              {Description: "search desc", Instructions: "search instr"},
		"enrich_authenticated_user": {Description: "enrich desc", Instructions: "enrich instr"},
		"get_org_chart":             {Description: "org_chart desc", Instructions: "org_chart instr"},
		"find_users":                {Description: "find_users desc", Instructions: "find_users instr"},
		"get_user_profile":          {Description: "profile desc", Instructions: "profile instr"},
		"get_user_profile_picture":  {Description: "picture desc", Instructions: "picture instr"},
		"get_time_off":              {Description: "time_off desc", Instructions: "time_off instr"},
	}
	for bare, want := range discovered {
		entry := &ManifestEntry{ToolName: "bamboohr_profile_provider__" + bare}
		got, ok, fallback := lookupDiscoveredTool(entry, discovered)
		if !ok {
			t.Errorf("%s: lookup failed", entry.ToolName)
			continue
		}
		if fallback != "" {
			t.Errorf("%s: unexpected fallback %q (multi-tool case must not hit the single-tool rename branch)", entry.ToolName, fallback)
		}
		if got.Description != want.Description {
			t.Errorf("%s: Description = %q, want %q (scramble — wrong tool's schema returned)", entry.ToolName, got.Description, want.Description)
		}
		if got.Instructions != want.Instructions {
			t.Errorf("%s: Instructions = %q, want %q (scramble — wrong tool's schema returned)", entry.ToolName, got.Instructions, want.Instructions)
		}
	}
}

func TestLookupDiscoveredTool_DirectHit(t *testing.T) {
	discovered := map[string]DiscoveredTool{
		"echo": {Description: "echoes input"},
	}
	entry := &ManifestEntry{ToolName: "echo"}
	got, ok, fallback := lookupDiscoveredTool(entry, discovered)
	if !ok || fallback != "" || got.Description != "echoes input" {
		t.Errorf("direct hit failed: ok=%v fallback=%q got=%+v", ok, fallback, got)
	}
}

func TestLookupDiscoveredTool_BareToolNameFallback(t *testing.T) {
	discovered := map[string]DiscoveredTool{
		"render_manim": {Description: "render"},
	}
	entry := &ManifestEntry{
		ToolName:     "manim__render_manim",
		BareToolName: "render_manim",
	}
	got, ok, fallback := lookupDiscoveredTool(entry, discovered)
	if !ok || fallback != "" || got.Description != "render" {
		t.Errorf("bare-name fallback failed: ok=%v fallback=%q got=%+v", ok, fallback, got)
	}
}

func TestLookupDiscoveredTool_PrefixStripFallback(t *testing.T) {
	discovered := map[string]DiscoveredTool{
		"render_manim": {Description: "render"},
	}
	entry := &ManifestEntry{ToolName: "manim__render_manim"}
	got, ok, fallback := lookupDiscoveredTool(entry, discovered)
	if !ok || fallback != "" || got.Description != "render" {
		t.Errorf("prefix-strip fallback failed: ok=%v fallback=%q got=%+v", ok, fallback, got)
	}
}

func TestLookupDiscoveredTool_SingleToolDynamicRename(t *testing.T) {
	discovered := map[string]DiscoveredTool{
		"runtime_renamed": {Description: "dynamic"},
	}
	entry := &ManifestEntry{ToolName: "configured_name"}
	got, ok, fallback := lookupDiscoveredTool(entry, discovered)
	if !ok || fallback != "runtime_renamed" || got.Description != "dynamic" {
		t.Errorf("single-tool rename failed: ok=%v fallback=%q got=%+v", ok, fallback, got)
	}
}

func TestLookupDiscoveredTool_MultiToolNoMatchIsAMiss(t *testing.T) {
	discovered := map[string]DiscoveredTool{
		"tool_a": {Description: "a"},
		"tool_b": {Description: "b"},
	}
	entry := &ManifestEntry{ToolName: "tool_c"}
	_, ok, _ := lookupDiscoveredTool(entry, discovered)
	if ok {
		t.Errorf("multi-tool no-match must return ok=false to avoid schema scramble")
	}
}

func TestBuildInitParamsFromDiscovered_MultiToolEmitsCorrectName(t *testing.T) {
	w := &Worker{logger: slog.Default()}
	discovered := map[string]DiscoveredTool{
		"search_users":  {Description: "search desc", Instructions: "search instr"},
		"get_org_chart": {Description: "chart desc", Instructions: "chart instr"},
	}
	entry := &ManifestEntry{
		ToolName:     "bamboohr_profile_provider__search_users",
		Capabilities: map[string]any{"profileProvider": true},
	}
	out, err := w.buildInitParamsFromDiscovered(entry, discovered)
	if err != nil {
		t.Fatalf("buildInitParamsFromDiscovered: %v", err)
	}
	if out.ToolName != "bamboohr_profile_provider__search_users" {
		t.Errorf("ToolName = %q, want prefixed manifest name", out.ToolName)
	}
	if out.Description != "search desc" || out.Instructions != "search instr" {
		t.Errorf("schema scramble: Description=%q Instructions=%q", out.Description, out.Instructions)
	}
	if v, _ := out.Capabilities["profileProvider"].(bool); !v {
		t.Errorf("Capabilities not propagated: %v", out.Capabilities)
	}
}

func TestBuildInitParamsFromDiscovered_SingleToolRenameEmitsDiscoveredName(t *testing.T) {
	w := &Worker{logger: slog.Default()}
	discovered := map[string]DiscoveredTool{
		"runtime_renamed": {Description: "dynamic", Instructions: "use it"},
	}
	entry := &ManifestEntry{ToolName: "configured_name"}
	out, err := w.buildInitParamsFromDiscovered(entry, discovered)
	if err != nil {
		t.Fatalf("buildInitParamsFromDiscovered: %v", err)
	}
	if out.ToolName != "runtime_renamed" {
		t.Errorf("ToolName = %q, want discovered (renamed) name %q", out.ToolName, "runtime_renamed")
	}
}

func TestBuildInitParamsFromDiscovered_MultiToolMissErrors(t *testing.T) {
	w := &Worker{logger: slog.Default()}
	discovered := map[string]DiscoveredTool{
		"tool_a": {Description: "a"},
		"tool_b": {Description: "b"},
	}
	entry := &ManifestEntry{ToolName: "tool_c"}
	out, err := w.buildInitParamsFromDiscovered(entry, discovered)
	if err == nil {
		t.Fatalf("expected error for multi-tool miss, got out=%+v", out)
	}
	if !strings.Contains(err.Error(), "tool_c") {
		t.Errorf("error should name the missing tool: %v", err)
	}
}

func TestNewWorker(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}

	cfg := WorkerConfig{
		WorkerID:  "my-worker",
		Namespace: "my-ns",
	}
	w := NewWorker(svc, cfg)

	if w.cfg.WorkerID != "my-worker" {
		t.Errorf("WorkerID = %q, want %q", w.cfg.WorkerID, "my-worker")
	}
	if w.cfg.Namespace != "my-ns" {
		t.Errorf("Namespace = %q, want %q", w.cfg.Namespace, "my-ns")
	}
	if w.subscribedTools == nil {
		t.Error("subscribedTools should be initialized")
	}
	if len(w.subscribedTools) != 0 {
		t.Error("subscribedTools should be empty initially")
	}
	if w.logger == nil {
		t.Error("logger should not be nil")
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()

	f := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(f) {
		t.Error("fileExists should return true for existing file")
	}

	if fileExists(filepath.Join(dir, "nope.txt")) {
		t.Error("fileExists should return false for missing file")
	}

	if fileExists(dir) {
		t.Error("fileExists should return false for a directory")
	}
}

func TestEnumerateToolManifests_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	paths := EnumerateToolManifests(dir, slog.Default())
	if len(paths) != 0 {
		t.Errorf("paths = %v, want empty for empty dir", paths)
	}
}

func TestEnumerateToolManifests_NonexistentDir(t *testing.T) {
	paths := EnumerateToolManifests("/nonexistent/tools", slog.Default())
	if len(paths) != 0 {
		t.Errorf("paths = %v, want empty for nonexistent dir", paths)
	}
}

func TestEnumerateToolManifests_PerToolManifest(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"alpha", "beta"} {
		toolDir := filepath.Join(dir, name)
		if err := os.Mkdir(toolDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(toolDir, "manifest.yaml"), []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Mkdir(filepath.Join(dir, "no_manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "loose.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := EnumerateToolManifests(dir, slog.Default())
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want 2 entries", paths)
	}
	for _, p := range paths {
		if filepath.Base(p) != "manifest.yaml" {
			t.Errorf("unexpected path basename: %s", p)
		}
		if !fileExists(p) {
			t.Errorf("returned path does not exist: %s", p)
		}
	}
}

func TestDispatchLoop_UnknownToolRetainsSubscription(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(ctx) })

	unknownTopic := remotetool.InvokeTopic("test-ns", "unknown_tool")
	if err := brk.Subscribe(w.queue, unknownTopic); err != nil {
		t.Fatal(err)
	}
	w.subsMu.Lock()
	w.subscribedTools["unknown_tool"] = struct{}{}
	w.subsMu.Unlock()

	resp, err := invokeToolOverBroker(t, ctx, brk, "unknown_tool", nil)
	if err != nil {
		t.Fatalf("no response to unknown-tool invoke: %v", err)
	}
	if code, _ := parseErrorResponse(t, resp); code != remotetool.ErrToolNotFound {
		t.Fatalf("want ErrToolNotFound, got %q", code)
	}

	w.subsMu.Lock()
	_, stillSubscribed := w.subscribedTools["unknown_tool"]
	w.subsMu.Unlock()
	if !stillSubscribed {
		t.Fatal("unknown_tool was unsubscribed by the dispatch path; a transient miss must not remove a subscription (BUG-2)")
	}
}

func TestIsVenvInfraExecutable(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"python", true},
		{"python3", true},
		{"pip", true},
		{"pip3", true},
		{"python3.11", true},
		{"pip3.11", true},
		{"activate", true},
		{"activate.csh", true},
		{"my_tool", false},
		{"piptool", false},
		{"deactivate", false},
	}
	for _, tt := range tests {
		got := isVenvInfraExecutable(tt.name)
		if got != tt.want {
			t.Errorf("isVenvInfraExecutable(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIsVenvInfraExecutable_WindowsExtensions(t *testing.T) {
	orig := runtimeGOOS
	runtimeGOOS = func() string { return "windows" }
	t.Cleanup(func() { runtimeGOOS = orig })

	tests := []struct {
		name string
		want bool
	}{
		{"python.exe", true},
		{"PYTHON.EXE", true},
		{"pip.exe", true},
		{"pip3.exe", true},
		{"pip3.10.exe", true},
		{"python3.11.exe", true},
		{"activate.bat", true},
		{"my_tool.exe", false},
		{"piptool.exe", false},
	}
	for _, tt := range tests {
		got := isVenvInfraExecutable(tt.name)
		if got != tt.want {
			t.Errorf("isVenvInfraExecutable(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIsELFBinary(t *testing.T) {
	dir := t.TempDir()

	elfPath := filepath.Join(dir, "elf_binary")
	elfData := []byte{0x7f, 'E', 'L', 'F', 0, 0, 0, 0}
	if err := os.WriteFile(elfPath, elfData, 0o755); err != nil {
		t.Fatal(err)
	}
	if !isNativeBinary(elfPath) {
		t.Error("should detect ELF binary")
	}

	scriptPath := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho hi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isNativeBinary(scriptPath) {
		t.Error("should not detect shell script as ELF")
	}

	shortPath := filepath.Join(dir, "short")
	if err := os.WriteFile(shortPath, []byte("AB"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isNativeBinary(shortPath) {
		t.Error("should not detect short file as ELF")
	}

	if isNativeBinary("/nonexistent/file") {
		t.Error("should not detect nonexistent file as ELF")
	}
}

func TestIsSkillTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"load_skill", true},
		{"list_skill_resources", true},
		{"read_skill_resource", true},
		{"grep_skill_resources", true},
		{"echo_tool", false},
		{"", false},
		{"load_skills", false},
	}
	for _, tt := range tests {
		got := isSkillTool(tt.name)
		if got != tt.want {
			t.Errorf("isSkillTool(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSkillNameFromArgs(t *testing.T) {
	tests := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"skill_name": "manim"}, "manim"},
		{map[string]any{"skill_name": ""}, ""},
		{map[string]any{}, ""},
		{map[string]any{"skill_name": 42}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		got := skillNameFromArgs(tt.args)
		if got != tt.want {
			t.Errorf("skillNameFromArgs(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestSkillOwnerFromToolName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"api-ref__search_docs", "api-ref"},
		{"manim__render_manim", "manim"},
		{"plain_tool", ""},
		{"__leading_sep", ""},
		{"", ""},
		{"no_sep_here", ""},
	}
	for _, tt := range tests {
		got := skillOwnerFromToolName(tt.name)
		if got != tt.want {
			t.Errorf("skillOwnerFromToolName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestManifestRegisterBuiltin(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManifest(manifestPath, slog.Default())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	entry := &ManifestEntry{
		ToolName:    "load_skill",
		Description: "Loads a skill",
	}
	m.RegisterBuiltin(entry)

	got := m.GetTool("load_skill")
	if got == nil {
		t.Fatal("load_skill should be registered")
	}
	if !got.IsBuiltin {
		t.Error("IsBuiltin should be true")
	}
}

func TestManifestRegister(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManifest(manifestPath, slog.Default())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	entry := &ManifestEntry{
		ToolName:   "render_manim",
		Executable: "/tools/manim/render",
	}
	m.Register("manim__render_manim", entry)

	got := m.GetTool("manim__render_manim")
	if got == nil {
		t.Fatal("manim__render_manim should be registered")
	}
	if got.ToolName != "render_manim" {
		t.Errorf("ToolName = %q, want %q", got.ToolName, "render_manim")
	}
}

func TestFindWheels(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"b_tool-1.0.whl", "a_sdk-2.0.whl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake-wheel"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dir.whl"), 0o755); err != nil {
		t.Fatal(err)
	}

	wheels := findWheels(dir)
	if len(wheels) != 2 {
		t.Fatalf("findWheels = %v, want 2 wheels", wheels)
	}
	if !strings.HasSuffix(wheels[0], "a_sdk-2.0.whl") {
		t.Errorf("first wheel = %q, expected a_sdk sorted first", wheels[0])
	}
}

func TestFindWheels_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	wheels := findWheels(dir)
	if len(wheels) != 0 {
		t.Errorf("findWheels on empty dir = %v", wheels)
	}
}

func TestFindWheels_NonexistentDir(t *testing.T) {
	wheels := findWheels("/nonexistent/dir")
	if wheels != nil {
		t.Errorf("findWheels on nonexistent dir = %v", wheels)
	}
}

func TestIsPackageDir_WithPackageZip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.zip"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isPackageDir(dir) {
		t.Error("should detect package.zip")
	}
}

func TestIsPackageDir_WithExtractedMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.zip.extracted"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isPackageDir(dir) {
		t.Error("should detect package.zip.extracted")
	}
}

func TestIsPackageDir_WithRuntimeManifest(t *testing.T) {
	dir := t.TempDir()
	manifest := `version: 1
tools:
  my_tool:
    runtime: python
    module: my_module
`
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isPackageDir(dir) {
		t.Error("should detect manifest with runtime: field")
	}
}

func TestIsPackageDir_RegularToolDir(t *testing.T) {
	dir := t.TempDir()
	manifest := `version: 1
tools:
  my_tool:
    executable: my_tool_bin
`
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if isPackageDir(dir) {
		t.Error("regular tool dir without runtime should not be a package dir")
	}
}

func TestIsPackageDir_Empty(t *testing.T) {
	dir := t.TempDir()
	if isPackageDir(dir) {
		t.Error("empty dir should not be a package dir")
	}
}

func TestReadPackageManifest_Valid(t *testing.T) {
	dir := t.TempDir()
	manifest := `version: 1
tools:
  my_tool:
    runtime: python
    module: my_module
    function: run_tool
    description: "A test tool"
    timeout_seconds: 60
`
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := readPackageManifest(path)
	if err != nil {
		t.Fatalf("readPackageManifest: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("version = %d, want 1", m.Version)
	}
	if len(m.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(m.Tools))
	}
	tool := m.Tools["my_tool"]
	if tool.Runtime != "python" {
		t.Errorf("runtime = %q", tool.Runtime)
	}
	if tool.Description != "A test tool" {
		t.Errorf("description = %q", tool.Description)
	}
}

func TestReadPackageManifest_Missing(t *testing.T) {
	_, err := readPackageManifest("/nonexistent/manifest.yaml")
	if err == nil {
		t.Error("expected error for missing manifest")
	}
}

func TestReadPackageManifest_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte("[invalid: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readPackageManifest(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestPackageHash_Deterministic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("version: 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	h1, err := packageHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := packageHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("hash should be deterministic: %q != %q", h1, h2)
	}
	if h1 == "" {
		t.Error("hash should not be empty")
	}
}

func TestPackageHash_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	h, err := packageHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h == "" {
		t.Error("hash should not be empty even for empty dir")
	}
}

func TestNewPackageProcessor(t *testing.T) {
	p := NewPackageProcessor("/tools", slog.Default())
	if p == nil {
		t.Fatal("should not be nil")
	}
	if p.toolsDir != "/tools" {
		t.Errorf("toolsDir = %q", p.toolsDir)
	}
	if p.cache == nil {
		t.Error("cache should be initialized")
	}
}

func TestProcessAll_NonexistentDir(t *testing.T) {
	p := NewPackageProcessor("/nonexistent/tools", slog.Default())
	changed, results, err := p.ProcessAll(t.Context())
	if err != nil {
		t.Fatalf("ProcessAll: %v", err)
	}
	if changed {
		t.Error("should not report changes for nonexistent dir")
	}
	if len(results) != 0 {
		t.Errorf("should have no results, got %d", len(results))
	}
}

func TestProcessAll_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	p := NewPackageProcessor(dir, slog.Default())
	changed, _, err := p.ProcessAll(t.Context())
	if err != nil {
		t.Fatalf("ProcessAll: %v", err)
	}
	if changed {
		t.Error("should not report changes for empty dir")
	}
}

func TestExecuteBuiltinSkillTool_NoRegistry(t *testing.T) {
	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: nil,
	}

	params := &remotetool.InvokeParams{
		ToolName: "load_skill",
		Args:     map[string]any{"skill_name": "test"},
	}
	_, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil {
		t.Fatal("expected error when registry is nil")
	}
	if invokeErr.Code != remotetool.ErrToolNotAvailable {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrToolNotAvailable)
	}
}

func TestExecuteBuiltinSkillTool_UnknownTool(t *testing.T) {
	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: NewSkillRegistry(t.TempDir()),
	}

	params := &remotetool.InvokeParams{
		ToolName: "unknown_skill_tool",
		Args:     map[string]any{},
	}
	_, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil {
		t.Fatal("expected error for unknown tool")
	}
	if invokeErr.Code != remotetool.ErrToolNotAvailable {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrToolNotAvailable)
	}
}

func TestExecuteBuiltinSkillTool_LoadSkill_MissingName(t *testing.T) {
	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: NewSkillRegistry(t.TempDir()),
	}

	params := &remotetool.InvokeParams{
		ToolName: "load_skill",
		Args:     map[string]any{},
	}
	_, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil {
		t.Fatal("expected error for missing skill_name")
	}
	if invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrInvalidRequest)
	}
}

func TestExecuteBuiltinSkillTool_LoadSkill_NotFound(t *testing.T) {
	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: NewSkillRegistry(t.TempDir()),
	}

	params := &remotetool.InvokeParams{
		ToolName: "load_skill",
		Args:     map[string]any{"skill_name": "nonexistent"},
	}
	_, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}

func TestExecuteBuiltinSkillTool_LoadSkill_Success(t *testing.T) {
	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["test_skill"] = &SkillEntry{
		Name:               "test_skill",
		InstructionContent: "Use this skill wisely",
		HasReferences:      true,
		HasAssets:          false,
		ResourceCount:      3,
		RequiredBuiltins:   []string{"web_request"},
	}
	reg.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "load_skill",
		Args:     map[string]any{"skill_name": "test_skill"},
	}
	result, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	tr := result.ToolResult
	if tr["instructions"] != "Use this skill wisely" {
		t.Errorf("instructions = %v", tr["instructions"])
	}
	if tr["has_references"] != true {
		t.Errorf("has_references = %v", tr["has_references"])
	}
	if tr["resource_count"] != 3 {
		t.Errorf("resource_count = %v", tr["resource_count"])
	}
	builtins, ok := tr["required_builtins"].([]string)
	if !ok || len(builtins) != 1 {
		t.Errorf("required_builtins = %v", tr["required_builtins"])
	}
}

func TestExecuteBuiltinSkillTool_LoadSkill_WithNameParam(t *testing.T) {
	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["fallback_skill"] = &SkillEntry{
		Name:               "fallback_skill",
		InstructionContent: "Fallback test",
	}
	reg.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "load_skill",
		Args:     map[string]any{"name": "fallback_skill"},
	}
	result, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestExecuteListSkillResources_MissingName(t *testing.T) {
	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: NewSkillRegistry(t.TempDir()),
	}

	params := &remotetool.InvokeParams{
		ToolName: "list_skill_resources",
		Args:     map[string]any{},
	}
	_, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil {
		t.Fatal("expected error for missing skill_name")
	}
}

func TestExecuteListSkillResources_WithFiles(t *testing.T) {
	skillDir := t.TempDir()
	refsDir := filepath.Join(skillDir, "references")
	assetsDir := filepath.Join(skillDir, "assets")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "api.md"), []byte("# API"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "logo.png"), []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["my_skill"] = &SkillEntry{
		Name:      "my_skill",
		LocalPath: skillDir,
	}
	reg.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "list_skill_resources",
		Args:     map[string]any{"skill_name": "my_skill"},
	}
	result, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr)
	}
	tr := result.ToolResult
	count, ok := tr["count"].(int)
	if !ok || count != 2 {
		t.Errorf("count = %v, want 2", tr["count"])
	}
}

func TestExecuteReadSkillResource_Success(t *testing.T) {
	skillDir := t.TempDir()
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "doc.md"), []byte("# Documentation\nLine 2\nLine 3"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["doc_skill"] = &SkillEntry{
		Name:      "doc_skill",
		LocalPath: skillDir,
	}
	reg.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "read_skill_resource",
		Args:     map[string]any{"skill_name": "doc_skill", "path": "references/doc.md"},
	}
	result, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr)
	}
	content, ok := result.ToolResult["content"].(string)
	if !ok || !strings.Contains(content, "Documentation") {
		t.Errorf("content = %v", result.ToolResult["content"])
	}
}

func TestExecuteReadSkillResource_PathTraversal(t *testing.T) {
	skillDir := t.TempDir()
	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["safe_skill"] = &SkillEntry{
		Name:      "safe_skill",
		LocalPath: skillDir,
	}
	reg.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "read_skill_resource",
		Args:     map[string]any{"skill_name": "safe_skill", "path": "../../etc/passwd"},
	}
	_, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil {
		t.Fatal("expected error for path traversal")
	}
	if invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrInvalidRequest)
	}
}

func TestExecuteReadSkillResource_MissingParams(t *testing.T) {
	skillDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "doc.md"), []byte("# Doc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["s"] = &SkillEntry{Name: "s", LocalPath: skillDir}
	reg.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "read_skill_resource",
		Args:     map[string]any{"skill_name": "s"},
	}
	_, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil {
		t.Fatal("expected error for missing path")
	}
	if invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrInvalidRequest)
	}
	for _, want := range []string{skillruntime.ResourceToolUsage(toolReadSkillResource), "references/doc.md"} {
		if !strings.Contains(invokeErr.Message, want) {
			t.Errorf("missing-path error does not contain %q: %s", want, invokeErr.Message)
		}
	}

	params.Args = map[string]any{"path": "references/doc.md"}
	_, invokeErr = w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil || !strings.Contains(invokeErr.Message, skillruntime.ResourceToolUsage(toolReadSkillResource)) {
		t.Errorf("missing skill_name error = %v, want the arguments read_skill_resource takes", invokeErr)
	}
}

func TestExecuteGrepSkillResources_Success(t *testing.T) {
	skillDir := t.TempDir()
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "api.md"), []byte("# API Reference\nfunc Create() error\nfunc Delete() error"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["grep_skill"] = &SkillEntry{
		Name:      "grep_skill",
		LocalPath: skillDir,
	}
	reg.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "grep_skill_resources",
		Args:     map[string]any{"skill_name": "grep_skill", "pattern": "func.*error"},
	}
	result, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr)
	}
	count, ok := result.ToolResult["match_count"].(int)
	if !ok || count != 2 {
		t.Errorf("match_count = %v, want 2", result.ToolResult["match_count"])
	}
}

func TestExecuteGrepSkillResources_InvalidRegex(t *testing.T) {
	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["s"] = &SkillEntry{Name: "s", LocalPath: t.TempDir()}
	reg.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "grep_skill_resources",
		Args:     map[string]any{"skill_name": "s", "pattern": "[invalid"},
	}
	_, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestExecuteGrepSkillResources_MissingPattern(t *testing.T) {
	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["s"] = &SkillEntry{Name: "s", LocalPath: t.TempDir()}
	reg.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "grep_skill_resources",
		Args:     map[string]any{"skill_name": "s"},
	}
	_, invokeErr := w.executeBuiltinSkillTool(t.Context(), params, nil)
	if invokeErr == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestParseWorkerConfig_Defaults(t *testing.T) {
	cfg := config.FromMap(map[string]any{})
	wc, err := ParseWorkerConfig(cfg)
	if err != nil {
		t.Fatalf("ParseWorkerConfig: %v", err)
	}
	if wc.WorkerID != config.DefaultWorkerID {
		t.Errorf("WorkerID = %q, want %q", wc.WorkerID, config.DefaultWorkerID)
	}
	if wc.DefaultTimeout != 300 {
		t.Errorf("DefaultTimeout = %d, want 300", wc.DefaultTimeout)
	}
}

func TestParseWorkerConfig_AllFields(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"worker_id":               "my-worker",
		"namespace":               "prod-ns",
		"manifest_paths":          []any{"/tools/manifest.yaml"},
		"default_timeout_seconds": 60,
		"skills_dir":              "/skills",
		"toolsets_dir":            "/synced-toolsets",
		"sandbox": map[string]any{
			"mode":                    "bwrap",
			"bwrap_bin":               "/usr/bin/bwrap",
			"prlimit_bin":             "/usr/bin/prlimit",
			"work_base_dir":           "/tmp/sandbox",
			"default_profile":         "standard",
			"default_timeout_seconds": 120,
			"fresh_proc":              "never",
		},
	})

	wc, err := ParseWorkerConfig(cfg)
	if err != nil {
		t.Fatalf("ParseWorkerConfig: %v", err)
	}
	if wc.WorkerID != "my-worker" {
		t.Errorf("WorkerID = %q", wc.WorkerID)
	}
	if wc.Namespace != "prod-ns" {
		t.Errorf("Namespace = %q", wc.Namespace)
	}
	if len(wc.ManifestPaths) != 1 || wc.ManifestPaths[0] != "/tools/manifest.yaml" {
		t.Errorf("ManifestPaths = %v", wc.ManifestPaths)
	}
	if wc.DefaultTimeout != 60 {
		t.Errorf("DefaultTimeout = %d", wc.DefaultTimeout)
	}
	if wc.SkillsDir != "/skills" {
		t.Errorf("SkillsDir = %q", wc.SkillsDir)
	}
	if wc.ToolsetsDir != "/synced-toolsets" {
		t.Errorf("ToolsetsDir = %q", wc.ToolsetsDir)
	}

	if wc.Sandbox.Mode != "bwrap" {
		t.Errorf("Sandbox.Mode = %q", wc.Sandbox.Mode)
	}
	if wc.Sandbox.BwrapBin != "/usr/bin/bwrap" {
		t.Errorf("Sandbox.BwrapBin = %q", wc.Sandbox.BwrapBin)
	}
	if wc.Sandbox.FreshProc != "never" {
		t.Errorf("Sandbox.FreshProc = %q", wc.Sandbox.FreshProc)
	}
}

func TestParseWorkerConfig_FreshProcInvalid(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"sandbox": map[string]any{
			"mode":       "bwrap",
			"fresh_proc": "bogus",
		},
	})
	if _, err := ParseWorkerConfig(cfg); err == nil {
		t.Fatal("expected error for invalid sandbox.fresh_proc value")
	}
}

func TestSkillRegistry_AllBundledToolEntries(t *testing.T) {
	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["skill_a"] = &SkillEntry{
		Name: "skill_a",
		BundledToolManifest: map[string]*ManifestEntry{
			"render": {ToolName: "render", Executable: "/tools/render"},
		},
	}
	reg.entries["skill_b"] = &SkillEntry{
		Name:                "skill_b",
		BundledToolManifest: nil,
	}
	reg.mu.Unlock()

	entries := reg.AllBundledToolEntries()
	if len(entries) != 1 {
		t.Fatalf("AllBundledToolEntries = %d, want 1", len(entries))
	}
	if _, ok := entries["skill_a__render"]; !ok {
		t.Errorf("expected key 'skill_a__render', got keys: %v", entries)
	}
}

func TestEntryExecutableAvailable(t *testing.T) {
	dir := t.TempDir()

	execPath := filepath.Join(dir, "runnable")
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write exec: %v", err)
	}
	nonExecPath := filepath.Join(dir, "plain")
	if err := os.WriteFile(nonExecPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write plain: %v", err)
	}
	pyPath := filepath.Join(dir, "tool.py")
	if err := os.WriteFile(pyPath, []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatalf("write py: %v", err)
	}

	tests := []struct {
		name  string
		entry *ManifestEntry
		want  bool
	}{
		{"empty executable", &ManifestEntry{}, false},
		{"missing path", &ManifestEntry{Executable: filepath.Join(dir, "nope")}, false},
		{"directory", &ManifestEntry{Executable: dir}, false},
		{"non-executable regular file", &ManifestEntry{Executable: nonExecPath}, false},
		{"executable regular file", &ManifestEntry{Executable: execPath}, true},
		{"non-executable python script", &ManifestEntry{Executable: pyPath}, false},
		{"python script missing", &ManifestEntry{Executable: filepath.Join(dir, "missing.py")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := entryExecutableAvailable(tc.entry); got != tc.want {
				t.Errorf("entryExecutableAvailable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRegisterBundledSkillTools_SkipsUnresolvedExecutable(t *testing.T) {
	dir := t.TempDir()
	goodDir := filepath.Join(dir, "good")
	if err := os.MkdirAll(goodDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	goodExec := filepath.Join(goodDir, "good")
	if err := os.WriteFile(goodExec, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write exec: %v", err)
	}

	reg := NewSkillRegistry(dir)
	reg.mu.Lock()
	reg.entries["good"] = &SkillEntry{
		Name: "good",
		BundledToolManifest: map[string]*ManifestEntry{
			"tool": {ToolName: "tool", Executable: goodExec, ToolDir: goodDir, Parameters: map[string]any{}},
		},
	}
	reg.entries["missing"] = &SkillEntry{
		Name: "missing",
		BundledToolManifest: map[string]*ManifestEntry{
			"tool": {ToolName: "tool", Executable: "/does/not/exist", ToolDir: "/does/not/exist", Parameters: map[string]any{}},
		},
	}
	reg.mu.Unlock()

	w := &Worker{
		cfg:           WorkerConfig{SkillsDir: dir, Namespace: "ns"},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		manifest:      NewManifest("", slog.New(slog.NewTextHandler(io.Discard, nil))),
		skillRegistry: reg,
	}

	w.registerBundledSkillTools(t.Context())

	if got := w.manifest.GetTool("good__tool"); got == nil {
		t.Errorf("expected good__tool to be registered")
	}
	if got := w.manifest.GetTool("missing__tool"); got != nil {
		t.Errorf("expected missing__tool to NOT be registered (executable unresolved), got %+v", got)
	}
}

func TestSkillRegistry_SkillNames(t *testing.T) {
	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["a"] = &SkillEntry{Name: "a"}
	reg.entries["b"] = &SkillEntry{Name: "b"}
	reg.mu.Unlock()

	names := reg.SkillNames()
	if len(names) != 2 {
		t.Errorf("SkillNames = %d, want 2", len(names))
	}
}

func TestSkillRegistry_Skills(t *testing.T) {
	reg := NewSkillRegistry(t.TempDir())
	reg.mu.Lock()
	reg.entries["a"] = &SkillEntry{Name: "a"}
	reg.mu.Unlock()

	skills := reg.Skills()
	if len(skills) != 1 {
		t.Fatalf("Skills = %d, want 1", len(skills))
	}
	if skills[0].Name != "a" {
		t.Errorf("skill name = %q", skills[0].Name)
	}
}

func TestSetupToolDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := SetupToolDir(t.Context(), nil, dir, slog.Default()); err != nil {
		t.Fatalf("SetupToolDir: %v", err)
	}
}

func TestSetupToolDirWithOptions_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := SetupToolDirWithOptions(t.Context(), nil, dir, true, slog.Default()); err != nil {
		t.Fatalf("SetupToolDirWithOptions: %v", err)
	}
}

func createTestZip(t *testing.T, dir, zipName string, files map[string]string) string {
	t.Helper()
	zipPath := filepath.Join(dir, zipName)
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

func TestExtractZipFile_Basic(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	zipPath := createTestZip(t, src, "test.zip", map[string]string{
		"hello.txt":        "hello world",
		"subdir/nested.py": "print('hi')",
	})

	if err := extractZipFile(zipPath, dest); err != nil {
		t.Fatalf("extractZipFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("hello.txt content = %q, want %q", string(data), "hello world")
	}

	data, err = os.ReadFile(filepath.Join(dest, "subdir", "nested.py"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "print('hi')" {
		t.Errorf("nested.py content = %q", string(data))
	}
}

func TestExtractZipFile_ZipSlipRejected(t *testing.T) {
	src := t.TempDir()
	zipPath := filepath.Join(src, "evil.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	fw, err := w.Create("../../../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("evil"))
	_ = w.Close()
	_ = f.Close()

	dest := t.TempDir()
	err = extractZipFile(zipPath, dest)
	if err == nil {
		t.Fatal("expected error for zip slip attack, got nil")
	}
	if !strings.Contains(err.Error(), "illegal file path") {
		t.Errorf("error = %v, want 'illegal file path'", err)
	}
}

func TestExtractZipFile_InvalidZip(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.zip")
	if err := os.WriteFile(badPath, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := extractZipFile(badPath, dir)
	if err == nil {
		t.Fatal("expected error for invalid zip, got nil")
	}
}

func TestExtractZipFile_PreservesExecutableMode(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	zipPath := filepath.Join(src, "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	hdr := &zip.FileHeader{Name: "tools/test_harness/test_harness", Method: zip.Deflate}
	hdr.SetMode(0o755)
	fw, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, werr := fw.Write([]byte("#!/bin/sh\necho hi\n")); werr != nil {
		t.Fatal(werr)
	}
	if zerr := zw.Close(); zerr != nil {
		t.Fatal(zerr)
	}
	if cerr := f.Close(); cerr != nil {
		t.Fatal(cerr)
	}

	target := filepath.Join(dest, "tools", "test_harness", "test_harness")
	if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
		t.Fatal(mkErr)
	}
	if wErr := os.WriteFile(target, []byte("stale"), 0o644); wErr != nil {
		t.Fatal(wErr)
	}

	if extErr := extractZipFile(zipPath, dest); extErr != nil {
		t.Fatalf("extractZipFile: %v", extErr)
	}

	st, stErr := os.Stat(target)
	if stErr != nil {
		t.Fatalf("stat extracted file: %v", stErr)
	}
	if perm := st.Mode().Perm(); perm != 0o755 {
		t.Errorf("extracted file mode = %o, want 0o755 — extractZipFile didn't chmod over an existing target", perm)
	}
}

func TestExtractZips_ExtractsAndRenames(t *testing.T) {
	dir := t.TempDir()
	createTestZip(t, dir, "tools.zip", map[string]string{
		"tool.py": "# tool",
	})

	if err := extractZips(dir, true, slog.Default()); err != nil {
		t.Fatalf("extractZips: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "tools.zip")); !os.IsNotExist(err) {
		t.Error("original zip should be renamed away")
	}
	if _, err := os.Stat(filepath.Join(dir, "tools.zip.extracted")); err != nil {
		t.Error("expected .zip.extracted marker file")
	}

	data, err := os.ReadFile(filepath.Join(dir, "tool.py"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# tool" {
		t.Errorf("tool.py content = %q", string(data))
	}
}

func TestExtractZips_SkipsAlreadyExtracted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tools.zip.extracted"), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractZips(dir, true, slog.Default()); err != nil {
		t.Fatalf("extractZips: %v", err)
	}
}

func TestExtractZips_NoZips(t *testing.T) {
	dir := t.TempDir()
	if err := extractZips(dir, true, slog.Default()); err != nil {
		t.Fatalf("extractZips with no zips: %v", err)
	}
}

func TestDescribeBinaryFormat(t *testing.T) {
	pe := func(machine uint16) []byte {
		b := make([]byte, 0x46)
		b[0], b[1] = 0x4d, 0x5a
		b[0x3c] = 0x40
		b[0x40], b[0x41], b[0x42], b[0x43] = 'P', 'E', 0, 0
		b[0x44] = byte(machine)
		b[0x45] = byte(machine >> 8)
		return b
	}
	peBadSig := make([]byte, 0x46)
	peBadSig[0], peBadSig[1] = 0x4d, 0x5a
	peBadSig[0x3c] = 0x40

	cases := []struct {
		name   string
		header []byte
		want   string
	}{
		{"linux elf x86_64", append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0x3e, 0}, 0), "Linux ELF (x86_64)"},
		{"linux elf aarch64", append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0xb7, 0}, 0), "Linux ELF (aarch64)"},
		{"linux elf riscv64", append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0xf3, 0}, 0), "Linux ELF (riscv64)"},
		{"macos macho big-endian 64", []byte{0xfe, 0xed, 0xfa, 0xcf, 0, 0, 0, 0}, "macOS Mach-O"},
		{"macos macho little-endian 64", []byte{0xcf, 0xfa, 0xed, 0xfe, 0, 0, 0, 0}, "macOS Mach-O"},
		{"windows pe x86_64", pe(0x8664), "Windows PE (x86_64)"},
		{"windows pe aarch64", pe(0xaa64), "Windows PE (aarch64)"},
		{"windows pe i386", pe(0x014c), "Windows PE (i386)"},
		{"windows pe mz only (truncated)", []byte{0x4d, 0x5a, 0x00, 0x00}, "Windows PE (unknown-arch)"},
		{"windows pe bad signature at e_lfanew", peBadSig, "Windows PE (unknown-arch)"},
		{"random bytes", []byte{0x00, 0x01, 0x02, 0x03}, ""},
		{"too short", []byte{0x7f, 'E'}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bin")
			if err := os.WriteFile(path, tc.header, 0o755); err != nil {
				t.Fatal(err)
			}
			got := describeBinaryFormat(path)
			if got != tc.want {
				t.Errorf("describeBinaryFormat = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExplainExecFormatError(t *testing.T) {
	defer func() {
		runtimeGOOS = func() string { return runtime.GOOS }
		runtimeGOARCH = func() string { return runtime.GOARCH }
	}()
	runtimeGOOS = func() string { return "darwin" }
	runtimeGOARCH = func() string { return "arm64" }

	dir := t.TempDir()
	elfPath := filepath.Join(dir, "linux_bin")
	header := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0xb7, 0}, 0)
	if err := os.WriteFile(elfPath, header, 0o755); err != nil {
		t.Fatal(err)
	}

	wrapped := explainExecFormatError(elfPath, fmt.Errorf("fork/exec %s: exec format error", elfPath))
	msg := wrapped.Error()
	for _, want := range []string{"exec format error", "Linux ELF (aarch64)", "darwin/arm64", "GOOS=darwin GOARCH=arm64"} {
		if !strings.Contains(msg, want) {
			t.Errorf("wrapped error missing %q\ngot: %s", want, msg)
		}
	}

	if got := explainExecFormatError(elfPath, fmt.Errorf("permission denied")); got.Error() != "permission denied" {
		t.Errorf("non-format error should pass through unchanged; got %q", got)
	}
	if got := explainExecFormatError(elfPath, nil); got != nil {
		t.Errorf("nil error should pass through; got %v", got)
	}
}

func TestExtractZips_WipesStaleStateBeforeReextraction(t *testing.T) {
	dir := t.TempDir()

	staleOnlyFiles := []string{
		"annotated_types-0.7.0-py3-none-any.whl",
		"sam_calc_tool-0.3.0-py3-none-any.whl",
		"tool.py",
	}
	for _, name := range append(staleOnlyFiles, "manifest.yaml") {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".zip.extracted-from-v1"), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	createTestZip(t, dir, "package.zip", map[string]string{
		"manifest.yaml":           "name: calc\nversion: 2\n",
		"python/calc/__init__.py": "x=1",
	})

	if err := extractZips(dir, true, slog.Default()); err != nil {
		t.Fatalf("extractZips: %v", err)
	}

	for _, name := range staleOnlyFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("stale file %s should have been wiped before extraction", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".venv")); err == nil {
		t.Error(".venv/ from prior version should have been wiped")
	}
	if _, err := os.Stat(filepath.Join(dir, "package.zip.extracted")); err != nil {
		t.Error("expected new package.zip.extracted marker")
	}
	if _, err := os.Stat(filepath.Join(dir, "python", "calc", "__init__.py")); err != nil {
		t.Errorf("expected new python/calc/__init__.py: %v", err)
	}
	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifestData) != "name: calc\nversion: 2\n" {
		t.Errorf("manifest.yaml = %q, want new content from package.zip", manifestData)
	}
}

func TestRunSetupScript_NoScript(t *testing.T) {
	dir := t.TempDir()
	if err := runSetupScript(t.Context(), nil, dir, true, slog.Default()); err != nil {
		t.Fatalf("runSetupScript with no script: %v", err)
	}
}

func TestRunSetupScript_RunsAndMarks(t *testing.T) {
	dir := t.TempDir()
	outputFile := filepath.Join(dir, "output.txt")
	script := fmt.Sprintf("#!/bin/bash\necho done > %s\n", outputFile)
	if err := os.WriteFile(filepath.Join(dir, "setup.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := runSetupScript(t.Context(), nil, dir, true, slog.Default()); err != nil {
		t.Fatalf("runSetupScript: %v", err)
	}

	if _, err := os.Stat(outputFile); err != nil {
		t.Fatal("setup.sh did not run: output file missing")
	}

	markerPath := filepath.Join(dir, ".sam-setup-done")
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatal("marker file missing")
	}

	_ = os.Remove(outputFile)
	if err := runSetupScript(t.Context(), nil, dir, true, slog.Default()); err != nil {
		t.Fatalf("runSetupScript (cached): %v", err)
	}
	if _, err := os.Stat(outputFile); !os.IsNotExist(err) {
		t.Error("setup.sh should not have run again (marker matches)")
	}
}

func TestParseManifestEntry_VolumeParams(t *testing.T) {
	cfg := map[string]any{
		"executable": "my_tool",
		"volume_params": []any{
			map[string]any{
				"name":        "data",
				"description": "data volume",
				"mode":        "readwrite",
				"mount_path":  "/data",
			},
		},
	}
	entry, err := parseManifestEntry("test_tool", cfg)
	if err != nil {
		t.Fatalf("parseManifestEntry: %v", err)
	}
	if len(entry.VolumeParams) != 1 {
		t.Fatalf("expected 1 volume param, got %d", len(entry.VolumeParams))
	}
	vp := entry.VolumeParams[0]
	if vp.Name != "data" || vp.Mode != "readwrite" || vp.MountPath != "/data" {
		t.Errorf("volume param = %+v", vp)
	}
}

func TestParseManifestEntry_VolumeParams_MissingName(t *testing.T) {
	cfg := map[string]any{
		"volume_params": []any{
			map[string]any{
				"mode":       "readonly",
				"mount_path": "/data",
			},
		},
	}
	_, err := parseManifestEntry("test_tool", cfg)
	if err == nil || !strings.Contains(err.Error(), "missing required") {
		t.Errorf("expected error about missing required, got %v", err)
	}
}

func TestParseManifestEntry_VolumeParams_InvalidMode(t *testing.T) {
	cfg := map[string]any{
		"volume_params": []any{
			map[string]any{
				"name":       "vol",
				"mode":       "invalid",
				"mount_path": "/mnt",
			},
		},
	}
	_, err := parseManifestEntry("test_tool", cfg)
	if err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("expected error about invalid mode, got %v", err)
	}
}

func TestParseManifestEntry_NetworkParams(t *testing.T) {
	cfg := map[string]any{
		"executable": "my_tool",
		"network_params": []any{
			map[string]any{
				"name":        "outbound",
				"description": "HTTPS to repository hosts",
			},
			map[string]any{
				"name": "telemetry",
				"mode": "egress",
			},
		},
	}
	entry, err := parseManifestEntry("test_tool", cfg)
	if err != nil {
		t.Fatalf("parseManifestEntry: %v", err)
	}
	if len(entry.NetworkParams) != 2 {
		t.Fatalf("expected 2 network params, got %d", len(entry.NetworkParams))
	}
	if entry.NetworkParams[0].Name != "outbound" || entry.NetworkParams[0].Mode != "egress" {
		t.Errorf("network param 0 = %+v", entry.NetworkParams[0])
	}
	if entry.NetworkParams[1].Name != "telemetry" || entry.NetworkParams[1].Mode != "egress" {
		t.Errorf("network param 1 = %+v", entry.NetworkParams[1])
	}
}

func TestParseManifestEntry_NetworkParams_MissingName(t *testing.T) {
	cfg := map[string]any{
		"network_params": []any{
			map[string]any{"description": "no name"},
		},
	}
	_, err := parseManifestEntry("test_tool", cfg)
	if err == nil || !strings.Contains(err.Error(), "missing required 'name'") {
		t.Errorf("expected missing-name error, got %v", err)
	}
}

func TestParseManifestEntry_NetworkParams_UnknownMode(t *testing.T) {
	cfg := map[string]any{
		"network_params": []any{
			map[string]any{
				"name": "outbound",
				"mode": "bidirectional",
			},
		},
	}
	_, err := parseManifestEntry("test_tool", cfg)
	if err == nil || !strings.Contains(err.Error(), "unsupported mode") {
		t.Errorf("expected unsupported-mode error, got %v", err)
	}
}

func TestParseManifestEntry_ResourceLimits(t *testing.T) {
	cfg := map[string]any{
		"executable": "tool",
		"resource_limits": map[string]any{
			"max_cpu_seconds":   30,
			"max_file_size_mb":  100,
			"max_open_files":    256,
			"max_processes":     50,
			"max_stack_size_mb": 8,
		},
	}
	entry, err := parseManifestEntry("rl_tool", cfg)
	if err != nil {
		t.Fatalf("parseManifestEntry: %v", err)
	}
	if entry.ResourceLimits == nil {
		t.Fatal("expected non-nil ResourceLimits")
	}
	if entry.ResourceLimits.MaxCPUSeconds != 30 {
		t.Errorf("MaxCPUSeconds = %d, want 30", entry.ResourceLimits.MaxCPUSeconds)
	}
	if entry.ResourceLimits.MaxProcesses != 50 {
		t.Errorf("MaxProcesses = %d, want 50", entry.ResourceLimits.MaxProcesses)
	}
}

func TestParseManifestEntry_ArtifactParams(t *testing.T) {
	cfg := map[string]any{
		"executable": "tool",
		"artifact_params": map[string]any{
			"file_input": map[string]any{
				"is_list":     true,
				"is_optional": false,
			},
		},
		"instructions": "Use this tool wisely",
	}
	entry, err := parseManifestEntry("ap_tool", cfg)
	if err != nil {
		t.Fatalf("parseManifestEntry: %v", err)
	}
	if entry.ArtifactParams == nil || len(entry.ArtifactParams) != 1 {
		t.Fatalf("expected 1 artifact param, got %v", entry.ArtifactParams)
	}
	ap := entry.ArtifactParams["file_input"]
	if !ap.IsList || ap.IsOptional {
		t.Errorf("artifact param = %+v", ap)
	}
	if entry.Instructions != "Use this tool wisely" {
		t.Errorf("Instructions = %q", entry.Instructions)
	}
}

func TestParseManifestEntry_SchemaRefreshSeconds(t *testing.T) {
	cfg := map[string]any{"executable": "tool"}
	entry, err := parseManifestEntry("t", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if entry.SchemaRefreshSeconds != 300 {
		t.Errorf("default SchemaRefreshSeconds = %d, want 300", entry.SchemaRefreshSeconds)
	}

	cfg["schema_refresh_seconds"] = 60
	entry, err = parseManifestEntry("t", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if entry.SchemaRefreshSeconds != 60 {
		t.Errorf("SchemaRefreshSeconds = %d, want 60", entry.SchemaRefreshSeconds)
	}

	cfg["schema_refresh_seconds"] = 0
	entry, err = parseManifestEntry("t", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if entry.SchemaRefreshSeconds != 0 {
		t.Errorf("SchemaRefreshSeconds = %d, want 0 (explicit zero must disable refresh)", entry.SchemaRefreshSeconds)
	}
}

func TestSchemaRefreshDisabledByYAMLZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	yaml := `version: 1
tools:
  refresh_off:
    executable: a
    schema_refresh_seconds: 0
  refresh_on:
    executable: b
    schema_refresh_seconds: 30
  refresh_default:
    executable: c
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManifest(path, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := m.ToolsWithSchemaRefresh()
	if _, present := got["refresh_off"]; present {
		t.Errorf("refresh_off should be excluded from ToolsWithSchemaRefresh, got %v", got)
	}
	if got["refresh_on"] != 30 {
		t.Errorf("refresh_on = %d, want 30", got["refresh_on"])
	}
	if got["refresh_default"] != 300 {
		t.Errorf("refresh_default = %d, want 300", got["refresh_default"])
	}
}

func TestParseResourceLimits_AllZeros(t *testing.T) {
	rl := parseResourceLimits(map[string]any{})
	if rl != nil {
		t.Errorf("expected nil for empty resource limits, got %+v", rl)
	}
}

func TestParseSkillToolManifest_Basic(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := `version: 1
expose_globally: true
tools:
  echo_tool:
    executable: echo_bin
    tool_dir: ./echo_dir
    description: "An echo tool"
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, exposeGlobally, err := parseSkillToolManifest(nil, manifestPath, toolsDir, "")
	if err != nil {
		t.Fatalf("parseSkillToolManifest: %v", err)
	}
	if !exposeGlobally {
		t.Error("expected expose_globally = true")
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries["echo_tool"]
	if entry == nil {
		t.Fatal("expected echo_tool entry")
	}
	if entry.Description != "An echo tool" {
		t.Errorf("Description = %q", entry.Description)
	}
	if !strings.HasPrefix(entry.Executable, toolsDir) {
		t.Errorf("Executable = %q, should be under %s", entry.Executable, toolsDir)
	}
	if !strings.HasPrefix(entry.ToolDir, toolsDir) {
		t.Errorf("ToolDir = %q, should be under %s", entry.ToolDir, toolsDir)
	}
}

func TestParseSkillToolManifest_SchemaDiscovery(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	schemaJSON := `{
		"tools": {
			"render_manim": {
				"description": "Render Manim animations",
				"parameters": {"type":"object","properties":{"script":{"type":"string"}}},
				"config_schema": [
					{"key":"api_key","type":"string","required":true,"secret":true},
					{"key":"region","type":"string","default":"us-east-1"}
				]
			}
		}
	}`
	binPath := filepath.Join(toolsDir, "render_manim")
	script := "#!/bin/sh\nif [ \"$1\" = \"--schema\" ]; then\n  cat <<'EOF'\n" + schemaJSON + "\nEOF\nfi\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := `version: 1
tools:
  render_manim:
    executable: render_manim
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, _, err := parseSkillToolManifest(nil, manifestPath, toolsDir, "")
	if err != nil {
		t.Fatalf("parseSkillToolManifest: %v", err)
	}
	entry := entries["render_manim"]
	if entry == nil {
		t.Fatal("expected render_manim entry")
	}
	if len(entry.ConfigSchema) != 2 {
		t.Fatalf("ConfigSchema len = %d, want 2 — schema discovery did not populate the field", len(entry.ConfigSchema))
	}
	first := entry.ConfigSchema[0]
	if first["key"] != "api_key" {
		t.Errorf("ConfigSchema[0].key = %v, want api_key", first["key"])
	}
	if first["secret"] != true {
		t.Errorf("ConfigSchema[0].secret = %v, want true", first["secret"])
	}
	if entry.Description != "Render Manim animations" {
		t.Errorf("Description = %q, expected schema to fill empty manifest description", entry.Description)
	}
}

func TestParseSkillToolManifest_ToolDirSubdir(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	binDir := filepath.Join(toolsDir, "test_harness")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(binDir, "test_harness")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := `version: 1
tools:
  generate_test_plan:
    runtime: go
    executable: test_harness
    tool_dir: test_harness
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, _, err := parseSkillToolManifest(nil, manifestPath, toolsDir, "")
	if err != nil {
		t.Fatalf("parseSkillToolManifest: %v", err)
	}
	entry := entries["generate_test_plan"]
	if entry == nil {
		t.Fatal("expected generate_test_plan entry")
	}
	if entry.Executable != binPath {
		t.Errorf("Executable = %q, want %q (must point inside tool_dir, not at the subdir itself)", entry.Executable, binPath)
	}
	if entry.ToolDir != binDir {
		t.Errorf("ToolDir = %q, want %q", entry.ToolDir, binDir)
	}
}

func TestParseSkillToolManifest_EmptyTools(t *testing.T) {
	dir := t.TempDir()
	manifest := "version: 1\nexpose_globally: false\n"
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, exposeGlobally, err := parseSkillToolManifest(nil, path, dir, "")
	if err != nil {
		t.Fatalf("parseSkillToolManifest: %v", err)
	}
	if exposeGlobally {
		t.Error("expected expose_globally = false")
	}
	if entries != nil {
		t.Errorf("expected nil entries, got %v", entries)
	}
}

func TestParseSkillToolManifest_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte("[invalid yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := parseSkillToolManifest(nil, path, dir, "")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseSkillToolManifest_FileNotFound(t *testing.T) {
	_, _, err := parseSkillToolManifest(nil, "/nonexistent/manifest.yaml", "/tmp", "")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEnrichSkillToolParams_SkillManagementTool(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my_skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{SkillsDir: dir},
	}

	params := &remotetool.InvokeParams{
		ToolName: "load_skill",
		Args:     map[string]any{"skill_name": "my_skill"},
	}
	entry := &ManifestEntry{}

	w.enrichSkillToolParams(params, entry)

	if params.ToolConfig == nil {
		t.Fatal("expected ToolConfig to be set")
	}
	if params.ToolConfig["skills_dir"] != dir {
		t.Errorf("skills_dir = %v, want %s", params.ToolConfig["skills_dir"], dir)
	}
	if len(entry.ExtraMounts) != 1 {
		t.Fatalf("expected 1 ExtraMount, got %d", len(entry.ExtraMounts))
	}
	if entry.ExtraMounts[0].HostPath != skillDir {
		t.Errorf("ExtraMount.HostPath = %s, want %s", entry.ExtraMounts[0].HostPath, skillDir)
	}
	if entry.ExtraMounts[0].MountPath != "/skill" {
		t.Errorf("ExtraMount.MountPath = %s, want /skill", entry.ExtraMounts[0].MountPath)
	}
	if !entry.ExtraMounts[0].ReadOnly {
		t.Error("ExtraMount should be read-only")
	}
}

func TestEnrichSkillToolParams_BundledTool(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "api_ref")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{SkillsDir: dir},
	}

	params := &remotetool.InvokeParams{
		ToolName: "api_ref__search_docs",
		Args:     map[string]any{},
	}
	entry := &ManifestEntry{}

	w.enrichSkillToolParams(params, entry)

	if len(entry.ExtraMounts) != 1 {
		t.Fatalf("expected 1 ExtraMount, got %d", len(entry.ExtraMounts))
	}
	if entry.ExtraMounts[0].HostPath != skillDir {
		t.Errorf("ExtraMount.HostPath = %s, want %s", entry.ExtraMounts[0].HostPath, skillDir)
	}
}

func TestEnrichSkillToolParams_NoSkillsDir(t *testing.T) {
	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{SkillsDir: ""},
	}
	params := &remotetool.InvokeParams{ToolName: "load_skill"}
	entry := &ManifestEntry{}
	w.enrichSkillToolParams(params, entry)

	if params.ToolConfig != nil {
		t.Error("expected nil ToolConfig when SkillsDir is empty")
	}
	if len(entry.ExtraMounts) != 0 {
		t.Error("expected no ExtraMounts when SkillsDir is empty")
	}
}

func TestEnrichSkillToolParams_RegularTool(t *testing.T) {
	dir := t.TempDir()
	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{SkillsDir: dir},
	}
	params := &remotetool.InvokeParams{
		ToolName: "regular_tool",
		Args:     map[string]any{},
	}
	entry := &ManifestEntry{}
	w.enrichSkillToolParams(params, entry)

	if params.ToolConfig != nil {
		t.Error("expected nil ToolConfig for regular tool")
	}
	if len(entry.ExtraMounts) != 0 {
		t.Error("expected no ExtraMounts for regular tool")
	}
}

func TestEnrichSkillToolParams_SkillDirNotExist(t *testing.T) {
	dir := t.TempDir()
	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{SkillsDir: dir},
	}
	params := &remotetool.InvokeParams{
		ToolName: "load_skill",
		Args:     map[string]any{"skill_name": "nonexistent"},
	}
	entry := &ManifestEntry{}
	w.enrichSkillToolParams(params, entry)

	if params.ToolConfig == nil || params.ToolConfig["skills_dir"] != dir {
		t.Error("expected skills_dir in ToolConfig")
	}
	if len(entry.ExtraMounts) != 0 {
		t.Error("expected no ExtraMounts when skill dir doesn't exist")
	}
}

func TestPublishSkillInit(t *testing.T) {
	b := newTestBroker(t)
	ns := "test-ns"
	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{Namespace: ns},
		svc:    &testServices{brk: b},
	}

	ctx := t.Context()
	skill := &SkillEntry{
		Name:        "my_skill",
		Description: "A test skill",
		Tags:        []string{"tag1"},
	}

	topic := remotetool.SkillInitTopic(ns, "my_skill")
	q, err := b.CreateQueue("skill-init-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := b.Subscribe(q, topic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishSkillInit(ctx, skill)

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := b.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	if req.Method != remotetool.MethodSkillInit {
		t.Errorf("method = %s, want %s", req.Method, remotetool.MethodSkillInit)
	}
}

func TestPublishSkillInit_WithBundledTools(t *testing.T) {
	b := newTestBroker(t)
	ns := "test-ns"
	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{Namespace: ns},
		svc:    &testServices{brk: b},
	}

	ctx := t.Context()
	skill := &SkillEntry{
		Name:        "manim",
		Description: "Manim rendering",
		BundledToolManifest: map[string]*ManifestEntry{
			"render_manim": {
				ToolName:    "render_manim",
				Description: "Render a Manim scene",
				Parameters:  map[string]any{"type": "object"},
				ArtifactParams: map[string]discoveredArtifactParam{
					"output": {IsList: false, IsOptional: false},
				},
			},
		},
	}

	topic := remotetool.SkillInitTopic(ns, "manim")
	q, err := b.CreateQueue("bundled-init-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := b.Subscribe(q, topic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishSkillInit(ctx, skill)

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := b.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	paramsData, _ := json.Marshal(req.Params)
	if !strings.Contains(string(paramsData), "manim__render_manim") {
		t.Errorf("expected bundled tool manim__render_manim in params: %s", string(paramsData))
	}
}

func TestPublishSkillInit_UnregisteredBundledToolFlagsDiscoveryFailed(t *testing.T) {
	b := newTestBroker(t)
	ns := "test-ns"
	logger := slog.Default()
	w := &Worker{
		logger:   logger,
		cfg:      WorkerConfig{Namespace: ns},
		svc:      &testServices{brk: b},
		manifest: NewManifestFromSources(nil, logger),
	}
	w.manifest.Register("brokenskill__goodtool", &ManifestEntry{ToolName: "brokenskill__goodtool"})

	skill := &SkillEntry{
		Name: "brokenskill",
		BundledToolManifest: map[string]*ManifestEntry{
			"goodtool": {ToolName: "goodtool", Description: "runs fine"},
			"badtool":  {ToolName: "badtool", Description: "unrunnable"},
		},
	}

	topic := remotetool.SkillInitTopic(ns, "brokenskill")
	q, err := b.CreateQueue("brokenskill-init-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := b.Subscribe(q, topic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishSkillInit(t.Context(), skill)

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := b.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	paramsData, _ := json.Marshal(req.Params)
	var params remotetool.SkillInitParams
	if err := json.Unmarshal(paramsData, &params); err != nil {
		t.Fatal(err)
	}

	if !params.DiscoveryFailed {
		t.Error("DiscoveryFailed = false, want true (a bundled tool was not registered)")
	}
	names := make([]string, 0, len(params.BundledTools))
	for _, bt := range params.BundledTools {
		names = append(names, bt.ToolName)
	}
	if len(names) != 1 || names[0] != "brokenskill__goodtool" {
		t.Errorf("BundledTools = %v, want only [brokenskill__goodtool] (badtool must be excluded)", names)
	}
}

func TestPublishSkillInit_ExposeGlobally_UnregisteredToolFlagsDiscoveryFailed(t *testing.T) {
	b := newTestBroker(t)
	ns := "test-ns"
	logger := slog.Default()
	w := &Worker{
		logger:   logger,
		cfg:      WorkerConfig{Namespace: ns},
		svc:      &testServices{brk: b},
		manifest: NewManifestFromSources(nil, logger),
	}
	w.manifest.Register("gskill__ggood", &ManifestEntry{ToolName: "gskill__ggood"})
	w.manifest.Register("ggood", &ManifestEntry{ToolName: "ggood"})

	skill := &SkillEntry{
		Name:                "gskill",
		ExposeToolsGlobally: true,
		BundledToolManifest: map[string]*ManifestEntry{
			"ggood": {ToolName: "ggood", Description: "runs fine"},
			"gbad":  {ToolName: "gbad", Description: "unrunnable"},
		},
	}

	topic := remotetool.SkillInitTopic(ns, "gskill")
	q, err := b.CreateQueue("gskill-init-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := b.Subscribe(q, topic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishSkillInit(t.Context(), skill)

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := b.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	paramsData, _ := json.Marshal(req.Params)
	var params remotetool.SkillInitParams
	if err := json.Unmarshal(paramsData, &params); err != nil {
		t.Fatal(err)
	}

	if !params.DiscoveryFailed {
		t.Error("DiscoveryFailed = false, want true (a globally-exposed bundled tool was not registered)")
	}
	names := make([]string, 0, len(params.BundledTools))
	for _, bt := range params.BundledTools {
		names = append(names, bt.ToolName)
	}
	if len(names) != 1 || names[0] != "gskill__ggood" {
		t.Errorf("BundledTools = %v, want only [gskill__ggood]", names)
	}
}

func TestPublishSkillInit_AllToolsRegistered_NoDiscoveryFailed(t *testing.T) {
	b := newTestBroker(t)
	ns := "test-ns"
	logger := slog.Default()
	w := &Worker{
		logger:   logger,
		cfg:      WorkerConfig{Namespace: ns},
		svc:      &testServices{brk: b},
		manifest: NewManifestFromSources(nil, logger),
	}
	w.manifest.Register("okskill__a", &ManifestEntry{ToolName: "okskill__a"})
	w.manifest.Register("okskill__b", &ManifestEntry{ToolName: "okskill__b"})

	skill := &SkillEntry{
		Name: "okskill",
		BundledToolManifest: map[string]*ManifestEntry{
			"a": {ToolName: "a", Description: "tool a"},
			"b": {ToolName: "b", Description: "tool b"},
		},
	}

	topic := remotetool.SkillInitTopic(ns, "okskill")
	q, err := b.CreateQueue("okskill-init-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := b.Subscribe(q, topic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishSkillInit(t.Context(), skill)

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := b.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	paramsData, _ := json.Marshal(req.Params)
	var params remotetool.SkillInitParams
	if err := json.Unmarshal(paramsData, &params); err != nil {
		t.Fatal(err)
	}

	if params.DiscoveryFailed {
		t.Error("DiscoveryFailed = true, want false (all bundled tools registered)")
	}
	if len(params.BundledTools) != 2 {
		t.Errorf("len(BundledTools) = %d, want 2", len(params.BundledTools))
	}
}

func TestPublishSkillInit_BundledToolsByteStable(t *testing.T) {
	b := newTestBroker(t)
	ns := "test-ns"
	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{Namespace: ns},
		svc:    &testServices{brk: b},
	}

	ctx := t.Context()
	skill := &SkillEntry{
		Name:        "dates",
		Description: "Dates skill (test).",
		BundledToolManifest: map[string]*ManifestEntry{
			"weekday_of": {
				ToolName:    "weekday_of",
				Description: "weekday",
				Parameters:  map[string]any{"type": "object"},
			},
			"date_math": {
				ToolName:    "date_math",
				Description: "date math",
				Parameters:  map[string]any{"type": "object"},
			},
			"parse_date": {
				ToolName:    "parse_date",
				Description: "parse",
				Parameters:  map[string]any{"type": "object"},
			},
			"render_calendar": {
				ToolName:    "render_calendar",
				Description: "render",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	}

	topic := remotetool.SkillInitTopic(ns, "dates")
	q, err := b.CreateQueue("byte-stable-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := b.Subscribe(q, topic); subErr != nil {
		t.Fatal(subErr)
	}

	const N = 20
	var firstPayload []byte
	mismatchAt := -1
	for i := range N {
		w.publishSkillInit(ctx, skill)
		recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		msg, recvErr := b.Receive(recvCtx, q)
		cancel()
		if recvErr != nil {
			t.Fatalf("receive #%d: %v", i, recvErr)
		}
		var req a2a.Request
		if jErr := json.Unmarshal(msg.Payload, &req); jErr != nil {
			t.Fatalf("unmarshal #%d: %v", i, jErr)
		}
		paramsBytes, mErr := json.Marshal(req.Params)
		if mErr != nil {
			t.Fatalf("marshal #%d: %v", i, mErr)
		}
		if firstPayload == nil {
			firstPayload = paramsBytes
			continue
		}
		if string(paramsBytes) != string(firstPayload) && mismatchAt < 0 {
			mismatchAt = i
		}
	}
	if mismatchAt >= 0 {
		t.Errorf("publishSkillInit emits non-deterministic BundledTools order: "+
			"payload #%d differs from #0 (Go map-iteration randomness; see "+
			"the chronic redeploy loop of 2026-05-28). "+
			"Fix: sort initParams.BundledTools by ToolName in publishSkillInit.",
			mismatchAt)
	}
}

func TestPublishSkillRemoved(t *testing.T) {
	b := newTestBroker(t)
	ns := "test-ns"
	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{Namespace: ns},
		svc:    &testServices{brk: b},
	}

	ctx := t.Context()
	topic := remotetool.SkillRemovedTopic(ns, "old_skill")
	q, err := b.CreateQueue("skill-removed-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := b.Subscribe(q, topic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishSkillRemoved(ctx, "old_skill")

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := b.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	if req.Method != remotetool.MethodSkillRemoved {
		t.Errorf("method = %s, want %s", req.Method, remotetool.MethodSkillRemoved)
	}
}

func TestPublishSkillInitsForNames(t *testing.T) {
	b := newTestBroker(t)
	ns := "test-ns"

	registry := NewSkillRegistry(t.TempDir())
	registry.mu.Lock()
	registry.entries["my_skill"] = &SkillEntry{
		Name:        "my_skill",
		Description: "test",
	}
	registry.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		cfg:           WorkerConfig{Namespace: ns},
		svc:           &testServices{brk: b},
		skillRegistry: registry,
	}

	ctx := t.Context()
	topic := remotetool.SkillInitTopic(ns, "my_skill")
	q, err := b.CreateQueue("skill-inits-names-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := b.Subscribe(q, topic); subErr != nil {
		t.Fatal(subErr)
	}

	w.publishSkillInitsForNames(ctx, map[string]struct{}{"my_skill": {}, "nonexistent": {}})

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := b.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	if req.Method != remotetool.MethodSkillInit {
		t.Errorf("method = %s, want %s", req.Method, remotetool.MethodSkillInit)
	}
}

func TestPublishSkillInitsForNames_NilRegistry(t *testing.T) {
	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: nil,
	}
	w.publishSkillInitsForNames(t.Context(), map[string]struct{}{"x": {}})
}

func TestRepublishSkillInits_NilRegistry(t *testing.T) {
	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: nil,
	}
	w.RepublishSkillInits(t.Context())
}

func TestInitSkillRegistry_EmptySkillsDir(t *testing.T) {
	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{SkillsDir: ""},
	}
	w.initSkillRegistry()
	if w.skillRegistry != nil {
		t.Error("expected nil registry when SkillsDir is empty")
	}
}

func TestInitSkillRegistry_WithDir(t *testing.T) {
	dir := t.TempDir()
	w := &Worker{
		logger: slog.Default(),
		cfg:    WorkerConfig{SkillsDir: dir},
	}
	w.initSkillRegistry()
	if w.skillRegistry == nil {
		t.Error("expected non-nil registry when SkillsDir is set")
	}
}

func TestManifestHasChanged_FileRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")

	if err := os.WriteFile(path, []byte("version: 1\ntools: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManifest(path, slog.Default())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.HasChanged() {
		t.Error("HasChanged should be false immediately after Load")
	}

	_ = os.Remove(path)
	if !m.HasChanged() {
		t.Error("HasChanged should be true after file removal")
	}
}

func TestManifestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte("[invalid yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManifest(path, slog.Default())
	if err := m.Load(t.Context()); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestManifestLoad_BadVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	content := "version: 99\ntools:\n  t:\n    executable: x\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManifest(path, slog.Default())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.GetTool("t") == nil {
		t.Error("expected tool t to be loaded despite version warning")
	}
}

func TestResolveExecutable_RootBinary(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "my_tool")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveExecutable(dir, "my_tool")
	if err != nil {
		t.Fatalf("resolveExecutable: %v", err)
	}
	if resolved != binPath {
		t.Errorf("resolved = %s, want %s", resolved, binPath)
	}
}

func TestResolveExecutable_LayerBin(t *testing.T) {
	dir := t.TempDir()
	layerBin := filepath.Join(dir, "python", "bin")
	if err := os.MkdirAll(layerBin, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(layerBin, "py_tool")
	if err := os.WriteFile(binPath, []byte("#!/usr/bin/env python\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveExecutable(dir, "py_tool")
	if err != nil {
		t.Fatalf("resolveExecutable: %v", err)
	}
	if resolved != binPath {
		t.Errorf("resolved = %s, want %s", resolved, binPath)
	}
}

func TestResolveExecutable_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveExecutable(dir, "missing_tool")
	if err == nil {
		t.Fatal("expected error for missing executable")
	}
}

func TestBoolFromAny(t *testing.T) {
	if !boolFromAny(true) {
		t.Error("expected true")
	}
	if boolFromAny(false) {
		t.Error("expected false")
	}
	if boolFromAny("true") {
		t.Error("expected false for string")
	}
	if boolFromAny(nil) {
		t.Error("expected false for nil")
	}
	if boolFromAny(1) {
		t.Error("expected false for int")
	}
}

func TestComputeFingerprint_DifferentEntries(t *testing.T) {
	e1 := &ManifestEntry{Description: "tool A", Instructions: "do X"}
	e2 := &ManifestEntry{Description: "tool B", Instructions: "do Y"}
	e3 := &ManifestEntry{Description: "tool A", Instructions: "do X"}

	fp1 := computeFingerprint(e1)
	fp2 := computeFingerprint(e2)
	fp3 := computeFingerprint(e3)

	if fp1 == fp2 {
		t.Error("different entries should have different fingerprints")
	}
	if fp1 != fp3 {
		t.Error("identical entries should have same fingerprints")
	}
}

func TestComputeFingerprint_WithParams(t *testing.T) {
	e1 := &ManifestEntry{
		Parameters:     map[string]any{"type": "object"},
		ArtifactParams: map[string]discoveredArtifactParam{"f": {IsList: true}},
	}
	e2 := &ManifestEntry{
		Parameters:     map[string]any{"type": "string"},
		ArtifactParams: map[string]discoveredArtifactParam{"f": {IsList: true}},
	}

	if computeFingerprint(e1) == computeFingerprint(e2) {
		t.Error("entries with different parameters should have different fingerprints")
	}
}

func TestCountFiles(t *testing.T) {
	dir := t.TempDir()
	if n := countFiles(dir); n != 0 {
		t.Errorf("expected 0 files, got %d", n)
	}

	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0o600)

	if n := countFiles(dir); n != 2 {
		t.Errorf("expected 2 files, got %d", n)
	}
}

func TestManifestEntry_ExecName(t *testing.T) {
	e := &ManifestEntry{Executable: "/usr/bin/my_tool", ToolName: "fallback"}
	if e.execName() != "my_tool" {
		t.Errorf("execName = %s, want my_tool", e.execName())
	}
	e2 := &ManifestEntry{Executable: "", ToolName: "fallback"}
	if e2.execName() != "fallback" {
		t.Errorf("execName = %s, want fallback", e2.execName())
	}
}

func TestRegisterBuiltinSkillTools_NilRegistry(t *testing.T) {
	w := &Worker{
		logger:        slog.Default(),
		skillRegistry: nil,
	}
	w.registerBuiltinSkillTools()
}

func TestRegisterBuiltinSkillTools_WithRegistry(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManifest(manifestPath, slog.Default())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	registry := NewSkillRegistry(dir)
	registry.mu.Lock()
	registry.entries["test_skill"] = &SkillEntry{Name: "test_skill"}
	registry.mu.Unlock()

	w := &Worker{
		logger:        slog.Default(),
		manifest:      m,
		skillRegistry: registry,
	}
	w.registerBuiltinSkillTools()

	if m.GetTool("load_skill") == nil {
		t.Error("expected load_skill to be registered")
	}
	if m.GetTool("list_skill_resources") == nil {
		t.Error("expected list_skill_resources to be registered")
	}
	if m.GetTool("read_skill_resource") == nil {
		t.Error("expected read_skill_resource to be registered")
	}
	if m.GetTool("grep_skill_resources") == nil {
		t.Error("expected grep_skill_resources to be registered")
	}
	for _, spec := range skillruntime.ResourceTools() {
		if got := m.GetTool(spec.Name); got == nil || got.Description != spec.Description {
			t.Errorf("%s is not registered with its shared description: %+v", spec.Name, got)
		}
	}

	entry := m.GetTool("load_skill")
	if !entry.IsBuiltin {
		t.Error("load_skill should be marked as builtin")
	}
}
