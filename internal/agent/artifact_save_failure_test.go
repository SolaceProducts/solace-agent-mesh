// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type failingArtifactStore struct {
	artifact.Service
	saveErr error
}

func (f *failingArtifactStore) Save(_ context.Context, _ artifact.Locator, _ []byte, _ string) (int, error) {
	return 0, f.saveErr
}

func (f *failingArtifactStore) SaveVersion(_ context.Context, _ artifact.Locator, _ int, _ []byte, _ string) error {
	return f.saveErr
}

func (f *failingArtifactStore) SaveMetadata(_ context.Context, _ artifact.Locator, _ int, _ *artifact.VersionMetadata) error {
	return nil
}

func newFailingArtifactSvc(saveErr error) *artifact.ScopedService {
	inner := memoryArtifactStore()
	return &artifact.ScopedService{
		Inner:     &failingArtifactStore{Service: inner, saveErr: saveErr},
		Scope:     "namespace",
		Namespace: "test-ns",
	}
}

func newTestAgentWithFailingArtifactStore(t *testing.T, saveErr error, llmResps ...*llm.ChatResponse) (*Agent, *mockLLM) {
	t.Helper()
	mock := newMockLLM(llmResps...)
	store := newMemSessionStore()
	brk := newMockBroker()

	agent := New(AgentConfig{
		Name:         "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  5,
		Namespace:    "test-ns",
	})

	svc := newMockServices(mock, store)
	svc.brk = brk
	svc.arts = newFailingArtifactSvc(saveErr)
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	return agent, mock
}

func artifactStatusFromResponse(t *testing.T, resp *a2a.Response) string {
	t.Helper()
	task, ok := resp.Result.(a2a.Task)
	if !ok {
		return ""
	}
	if task.Status.Message == nil {
		return ""
	}
	for _, p := range task.Status.Message.Parts {
		if p.Kind != "data" {
			continue
		}
		data, err := json.Marshal(p.Data)
		if err != nil {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if toolName, _ := p.Metadata["tool_name"].(string); toolName != "_notify_artifact_save" {
			continue
		}
		if s, ok := m["status"].(string); ok {
			return s
		}
	}
	return ""
}

func TestArtifactSaveFailure_SurfacedInResponse(t *testing.T) {
	storageErr := errors.New("connection refused: S3 unavailable")

	artifactBlock := BlockPrefixSaveArtifact + " filename=\"report.md\" description=\"A report\"\n# Report\nContent.\n" + BlockDelimiterClose
	followUpContent := "I tried to save the report but the storage is unavailable."

	agent, _ := newTestAgentWithFailingArtifactStore(t, storageErr,
		&llm.ChatResponse{Content: artifactBlock, FinishReason: "stop"},
		&llm.ChatResponse{Content: followUpContent, FinishReason: "stop"},
	)

	resp, err := agent.HandleTask(
		t.Context(),
		"task-artfail-001",
		newUserMessage("Create a report"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatalf("HandleTask returned unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected A2A error: %+v", resp.Error)
	}

	status := artifactStatusFromResponse(t, resp)
	if status != "error" {
		t.Errorf("artifact data-part status = %q, want %q — save failure not surfaced", status, "error")
	}

	task := resp.Result.(a2a.Task)
	if pa, exists := task.Metadata["produced_artifacts"]; exists {
		t.Errorf("produced_artifacts should be absent for all-failed saves, got: %v", pa)
	}
}

func TestArtifactSaveFailure_ErrorInToolResult(t *testing.T) {
	storageErr := errors.New("no such host: s3.example.com")

	artifactBlock := BlockPrefixSaveArtifact + " filename=\"data.csv\" description=\"Data\"\ncol1,col2\n1,2\n" + BlockDelimiterClose
	followUp := "The storage backend is unreachable."

	agent, mock := newTestAgentWithFailingArtifactStore(t, storageErr,
		&llm.ChatResponse{Content: artifactBlock, FinishReason: "stop"},
		&llm.ChatResponse{Content: followUp, FinishReason: "stop"},
	)

	resp, err := agent.HandleTask(
		t.Context(),
		"task-artfail-002",
		newUserMessage("Export data"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("A2A error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(calls))
	}

	secondCallMsgs := calls[1].Messages
	var foundErrorResult bool
	for _, m := range secondCallMsgs {
		if m.Role != "tool" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(m.Content), &payload); err != nil {
			continue
		}
		if payload["status"] == "error" {
			foundErrorResult = true
			errMsg, _ := payload["error"].(string)
			if !strings.Contains(errMsg, "no such host") {
				t.Errorf("tool result error = %q, want store error text", errMsg)
			}
		}
	}
	if !foundErrorResult {
		t.Error("second LLM call should contain a _notify_artifact_save tool result with status=error")
	}
}

func TestArtifactSaveSuccess_StillWorks(t *testing.T) {
	artifactBlock := BlockPrefixSaveArtifact + " filename=\"note.txt\" description=\"A note\"\nHello!\n" + BlockDelimiterClose
	followUp := "Your note has been saved."

	mock := newMockLLM(
		&llm.ChatResponse{Content: artifactBlock, FinishReason: "stop"},
		&llm.ChatResponse{Content: followUp, FinishReason: "stop"},
	)
	store := newMemSessionStore()
	brk := newMockBroker()
	memStore := memoryArtifactStore()

	agent := New(AgentConfig{
		Name:         "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  5,
		Namespace:    "test-ns",
	})

	svc := newMockServices(mock, store)
	svc.brk = brk
	svc.arts = &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	resp, err := agent.HandleTask(
		t.Context(),
		"task-artok-001",
		newUserMessage("Save a note"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("A2A error: %+v", resp.Error)
	}

	status := artifactStatusFromResponse(t, resp)
	if status != "success" {
		t.Errorf("artifact data-part status = %q, want %q", status, "success")
	}

	task := resp.Result.(a2a.Task)
	pa, ok := task.Metadata["produced_artifacts"]
	if !ok {
		t.Fatal("produced_artifacts missing for successful save")
	}
	list, ok := pa.([]map[string]any)
	if !ok {
		t.Fatalf("produced_artifacts has unexpected type %T", pa)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 produced artifact, got %d", len(list))
	}
	if list[0]["filename"] != "note.txt" {
		t.Errorf("produced artifact filename = %q, want note.txt", list[0]["filename"])
	}
}

func TestPreviewArtifactNotifications_ErrorStatusPreserved(t *testing.T) {
	agent, _, _ := newTestAgent()

	artifacts := []artifactCreationRecord{
		{Filename: "ok.txt", Version: 1, CallID: "call-1", Status: "success"},
		{Filename: "bad.txt", Version: 0, CallID: "call-2", Status: "error", ErrorMsg: "backend down"},
	}

	calls := agent.previewArtifactNotifications(artifacts)

	if len(calls) != 2 {
		t.Fatalf("expected 2 preview calls, got %d", len(calls))
	}

	if calls[0].Args["status"] != "success" {
		t.Errorf("call[0] status = %v, want success", calls[0].Args["status"])
	}
	if calls[1].Args["status"] != "error" {
		t.Errorf("call[1] status = %v, want error — error status must not be masked as success", calls[1].Args["status"])
	}
}

func TestProducedArtifactsListsToolSavedFiles(t *testing.T) {
	mock := newMockLLM(
		&llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "make_pdf", Arguments: `{}`}}, FinishReason: "tool_calls"},
		&llm.ChatResponse{Content: "The report is ready.", FinishReason: "stop"},
	)
	agent := New(AgentConfig{Name: "test-agent", SystemPrompt: "You make reports.", Model: "test-model", MaxTokens: 1000, MaxLLMCalls: 5, Namespace: "test-ns"})
	svc := newMockServices(mock, newMemSessionStore())
	svc.brk = newMockBroker()
	svc.arts = &artifact.ScopedService{Inner: memoryArtifactStore(), Scope: "namespace", Namespace: "test-ns"}
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := agent.tools.Register(&testBuiltinTool{
		name:   "make_pdf",
		params: &tool.JSONSchema{Type: "object"},
		execFn: func(ctx context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
			if _, err := toolCtx.Facade().SaveArtifact(ctx, "report.pdf", []byte("%PDF-1.7"), "application/pdf"); err != nil {
				return nil, err
			}
			return tool.OK("saved report.pdf"), nil
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, err := agent.HandleTask(t.Context(), "task-toolsave-001", newUserMessage("Make the report"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1", A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("A2A error: %+v", resp.Error)
	}

	list, _ := resp.Result.(a2a.Task).Metadata["produced_artifacts"].([]map[string]any)
	if len(list) != 1 || list[0]["filename"] != "report.pdf" {
		t.Errorf("produced_artifacts = %v, want report.pdf", list)
	}
}
