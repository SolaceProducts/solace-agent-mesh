// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artifactmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var _ tool.BuiltinTool = (*workflowAgentTool)(nil)

func TestWorkflowToolHonorsConfiguredTimeout(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	agent.cfg.WorkflowRequestTimeoutSeconds = 1
	agent.cfg.PeerRequestTimeoutSeconds = 8

	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

	wt := &workflowAgentTool{
		peerName:  "slow-wf",
		card:      a2a.AgentCard{Name: "slow-wf"},
		agent:     agent,
		namespace: agent.cfg.Namespace,
		inputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}

	start := time.Now()
	result, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{UserID: "user-1"})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "timed out") {
		t.Errorf("message = %q, should mention timeout", result.Message)
	}
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %v, configured WorkflowRequestTimeoutSeconds was 1s "+
			"(8s means the call site read PeerRequestTimeoutSeconds)", elapsed)
	}
}

func TestWorkflowToolName(t *testing.T) {
	tests := []struct {
		peerName string
		want     string
	}{
		{"my-workflow", "workflow_my_workflow"},
		{"simple", "workflow_simple"},
		{"multi-hyphen-name", "workflow_multi_hyphen_name"},
	}
	for _, tt := range tests {
		wt := &workflowAgentTool{peerName: tt.peerName}
		if got := wt.Name(); got != tt.want {
			t.Errorf("Name() for %q = %q, want %q", tt.peerName, got, tt.want)
		}
	}
}

func TestWorkflowToolDescription(t *testing.T) {
	wt := &workflowAgentTool{
		peerName: "data-pipeline",
		card:     a2a.AgentCard{Description: "Processes data in stages."},
	}
	desc := wt.Description()
	if !contains(desc, "data-pipeline") {
		t.Errorf("missing workflow name: %q", desc)
	}
	if !contains(desc, "Processes data in stages.") {
		t.Errorf("missing card description: %q", desc)
	}
}

func TestWorkflowToolDescriptionUsesDisplayName(t *testing.T) {
	wt := &workflowAgentTool{
		peerName: "wf_019e9445_40fe",
		card: a2a.AgentCard{
			Name:        "wf_019e9445_40fe",
			Description: "Processes data in stages.",
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{
					URI:    ExtensionURIDisplayName,
					Params: map[string]any{"display_name": "Weekly Report"},
				}},
			},
		},
	}
	desc := wt.Description()
	if !contains(desc, "Weekly Report") {
		t.Errorf("description should name the workflow by its display name: %q", desc)
	}
	if contains(desc, `"wf_019e9445_40fe" workflow`) {
		t.Errorf("description should not name the workflow by its internal slug: %q", desc)
	}
}

func TestWorkflowToolDescriptionNoCardDesc(t *testing.T) {
	wt := &workflowAgentTool{peerName: "simple"}
	desc := wt.Description()
	if !contains(desc, "simple") {
		t.Errorf("missing workflow name: %q", desc)
	}
}

func TestWorkflowToolParameters(t *testing.T) {
	wt := &workflowAgentTool{
		peerName: "test",
		inputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "number", "description": "Input value"},
				"name":  map[string]any{"type": "string", "description": "Input name"},
			},
		},
	}

	params := wt.Parameters()

	if params.Type != "object" {
		t.Errorf("type = %q, want object", params.Type)
	}

	if len(params.Properties) != 3 {
		t.Errorf("expected 3 properties, got %d", len(params.Properties))
	}
	if _, ok := params.Properties["input_artifact"]; !ok {
		t.Error("missing input_artifact property")
	}
	if _, ok := params.Properties["value"]; !ok {
		t.Error("missing value property from schema")
	}
	if _, ok := params.Properties["name"]; !ok {
		t.Error("missing name property from schema")
	}

	if len(params.Required) != 0 {
		t.Errorf("required = %v, want empty", params.Required)
	}
}

func TestWorkflowToolParametersNoSchema(t *testing.T) {
	wt := &workflowAgentTool{peerName: "test"}
	params := wt.Parameters()

	if _, ok := params.Properties["input_artifact"]; !ok {
		t.Error("missing input_artifact property")
	}
	if len(params.Properties) != 1 {
		t.Errorf("expected 1 property, got %d", len(params.Properties))
	}
}

func TestWorkflowToolIsLongRunning(t *testing.T) {
	wt := &workflowAgentTool{peerName: "test"}
	if !wt.IsLongRunning() {
		t.Error("workflow tool should be long-running")
	}
}

func TestAgentCardExtensions(t *testing.T) {
	card := a2a.AgentCard{
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI:    ExtensionURIAgentType,
					Params: map[string]any{"type": "workflow"},
				},
				{
					URI: ExtensionURISchemas,
					Params: map[string]any{
						"input_schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"value": map[string]any{"type": "number"},
							},
						},
					},
				},
			},
		},
	}

	agentType, inputSchema := agentCardExtensions(card)

	if agentType != "workflow" {
		t.Errorf("agentType = %q, want workflow", agentType)
	}
	if inputSchema == nil {
		t.Fatal("inputSchema should not be nil")
	}
	if _, ok := inputSchema["properties"]; !ok {
		t.Error("inputSchema missing properties")
	}
}

func TestAgentCardExtensionsDefaults(t *testing.T) {
	card := a2a.AgentCard{}

	agentType, inputSchema := agentCardExtensions(card)

	if agentType != "standard" {
		t.Errorf("agentType = %q, want standard", agentType)
	}
	if inputSchema != nil {
		t.Errorf("inputSchema = %v, want nil", inputSchema)
	}
}

func TestAgentCardExtensionsStandardType(t *testing.T) {
	card := a2a.AgentCard{
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI:    ExtensionURIAgentType,
					Params: map[string]any{"type": "standard"},
				},
			},
		},
	}

	agentType, inputSchema := agentCardExtensions(card)

	if agentType != "standard" {
		t.Errorf("agentType = %q, want standard", agentType)
	}
	if inputSchema != nil {
		t.Errorf("inputSchema = %v, want nil", inputSchema)
	}
}

func TestHandleDiscoveryWorkflowCard(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "self",
		Namespace: "ns",
	})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card := a2a.AgentCard{
		Name:        "my-workflow",
		Description: "A test workflow",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI:    ExtensionURIAgentType,
					Params: map[string]any{"type": "workflow"},
				},
				{
					URI: ExtensionURISchemas,
					Params: map[string]any{
						"input_schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"value": map[string]any{"type": "number"},
							},
						},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(card)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})

	if knownPeer(agent, "my-workflow") == nil {
		t.Error("my-workflow should be registered")
	}

	if agent.tools.GetTool("workflow_my_workflow") == nil {
		t.Error("workflow tool should be registered as workflow_my_workflow")
	}
	if agent.tools.GetTool("peer_my-workflow") != nil {
		t.Error("workflow should NOT be registered as peer tool")
	}
}

func TestHandleDiscoveryStandardCard(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "self",
		Namespace: "ns",
	})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card := a2a.AgentCard{
		Name:        "standard-agent",
		Description: "A normal agent",
	}
	data, _ := json.Marshal(card)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})

	if agent.tools.GetTool("peer_standard-agent") == nil {
		t.Error("standard agent should be registered as peer tool")
	}
	if agent.tools.GetTool("workflow_standard_agent") != nil {
		t.Error("standard agent should NOT be registered as workflow tool")
	}
}

func TestSanitizeForFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-workflow", "my-workflow"},
		{"hello world!", "helloworld"},
		{"a/b\\c.d", "abcd"},
		{"valid_name-123", "valid_name-123"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := sanitizeForFilename(tt.input); got != tt.want {
			t.Errorf("sanitizeForFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWorkflowToolExecute(t *testing.T) {
	var agent *Agent
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatal(err)
		}

		resultData := map[string]any{
			"type":   "structured_invocation_result",
			"status": "success",
		}
		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
				Message: &a2a.Message{
					Parts: []a2a.Part{a2a.NewDataPart(resultData)},
				},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{
		Name:      "caller",
		Namespace: "ns",
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	wt := &workflowAgentTool{
		peerName:  "target-wf",
		card:      a2a.AgentCard{Name: "target-wf"},
		agent:     agent,
		namespace: "ns",
		inputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "number"},
			},
		},
	}

	result, err := wt.Execute(context.Background(), map[string]any{
		"value": 42,
	}, &tool.Context{UserID: "user-1", SessionID: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q, want success", result.Status)
	}
}

func TestWorkflowToolExecuteRequestIDIsUUID(t *testing.T) {
	var agent *Agent
	var capturedID string
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatal(err)
		}
		capturedID = req.ID
		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State:   a2a.TaskStateCompleted,
				Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{"type": "structured_invocation_result", "status": "success"})}},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{Name: "caller", Namespace: "ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	wt := &workflowAgentTool{peerName: "target-wf", card: a2a.AgentCard{Name: "target-wf"}, agent: agent, namespace: "ns"}
	if _, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{UserID: "u", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}

	if capturedID == "" {
		t.Fatal("no workflow request was published")
	}
	if strings.HasPrefix(capturedID, "wf-req-") {
		t.Errorf("request ID is the legacy non-UUID form %q — task logger would drop the whole workflow run", capturedID)
	}
	if _, err := uuid.Parse(capturedID); err != nil {
		t.Errorf("request ID %q is not a valid UUID (task logger drops non-UUID taskIDs): %v", capturedID, err)
	}
}

func TestWorkflowToolExecuteWithTextFallback(t *testing.T) {
	var agent *Agent
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatal(err)
		}

		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
				Message: &a2a.Message{
					Parts: []a2a.Part{a2a.NewTextPart("workflow text result")},
				},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{Name: "caller", Namespace: "ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	wt := &workflowAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	result, err := wt.Execute(context.Background(), map[string]any{
		"value": 10,
	}, &tool.Context{UserID: "u", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if result.Message != "workflow text result" {
		t.Errorf("message = %q, want 'workflow text result'", result.Message)
	}
}

func TestWorkflowToolExecuteError(t *testing.T) {
	var agent *Agent
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		json.Unmarshal(msg.Payload, &req) //nolint:errcheck // test

		resultData := map[string]any{
			"type":          "structured_invocation_result",
			"status":        "error",
			"error_message": "workflow failed",
		}
		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
				Message: &a2a.Message{
					Parts: []a2a.Part{a2a.NewDataPart(resultData)},
				},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{Name: "caller", Namespace: "ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	wt := &workflowAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	result, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{UserID: "u", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !contains(result.Message, "workflow failed") {
		t.Errorf("message = %q, should contain 'workflow failed'", result.Message)
	}
}

func TestWorkflowToolExecuteMessageSizeExceeded(t *testing.T) {
	agent := New(AgentConfig{
		Name:                "caller",
		Namespace:           "ns",
		MaxMessageSizeBytes: 50,
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	wt := &workflowAgentTool{
		peerName:  "target-wf",
		card:      a2a.AgentCard{Name: "target-wf"},
		agent:     agent,
		namespace: "ns",
	}

	result, err := wt.Execute(context.Background(), map[string]any{
		"value": "This is a value that will produce a message larger than 50 bytes when serialized",
	}, &tool.Context{UserID: "user-1", SessionID: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "exceeds maximum limit") {
		t.Errorf("message = %q, should mention size limit", result.Message)
	}
}

func TestPrepareInputArtifactMode(t *testing.T) {
	wt := &workflowAgentTool{peerName: "test"}

	inputJSON, artifactURI, err := wt.prepareInput(context.Background(), map[string]any{
		"input_artifact": "my_data.json",
	}, &tool.Context{})

	if err != nil {
		t.Fatal(err)
	}
	if inputJSON != nil {
		t.Error("inputJSON should be nil in artifact mode")
	}
	if artifactURI != "artifact://my_data.json" {
		t.Errorf("artifactURI = %q, want artifact://my_data.json", artifactURI)
	}
}

func TestPrepareInputParameterMode(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	wt := &workflowAgentTool{peerName: "test", agent: agent}

	inputJSON, artifactURI, err := wt.prepareInput(context.Background(), map[string]any{
		"value": 42,
		"name":  "test",
	}, &tool.Context{})

	if err != nil {
		t.Fatal(err)
	}
	if artifactURI != "" {
		t.Errorf("artifactURI = %q, want empty", artifactURI)
	}
	if inputJSON == nil {
		t.Fatal("inputJSON should not be nil")
	}

	var parsed map[string]any
	if err := json.Unmarshal(inputJSON, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["value"] != float64(42) {
		t.Errorf("value = %v", parsed["value"])
	}
	if parsed["name"] != "test" {
		t.Errorf("name = %v", parsed["name"])
	}
}

func TestPrepareInputFallsBackToInlineBytesOnSaveError(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	wt := &workflowAgentTool{peerName: "target-wf", agent: agent}

	inputJSON, artifactURI, err := wt.prepareInput(context.Background(), map[string]any{
		"value": 42,
	}, &tool.Context{
		AgentName:       "test",
		UserID:          "user-1",
		SessionID:       "sess-1",
		ArtifactService: newFailingArtifactSvc(errors.New("store unavailable")),
	})

	if err != nil {
		t.Fatalf("expected no error (graceful fallback), got %v", err)
	}
	if artifactURI != "" {
		t.Errorf("artifactURI = %q, want empty on save failure", artifactURI)
	}
	if inputJSON == nil {
		t.Fatal("inputJSON should not be nil on save failure fallback")
	}

	var parsed map[string]any
	if err := json.Unmarshal(inputJSON, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["value"] != float64(42) {
		t.Errorf("value = %v", parsed["value"])
	}
}

func TestPrepareInputSaveFailureLogsCorrelationFields(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	var logBuf bytes.Buffer
	agent.logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	wt := &workflowAgentTool{peerName: "target-wf", agent: agent}

	_, _, err := wt.prepareInput(context.Background(), map[string]any{
		"value": 42,
	}, &tool.Context{
		AgentName:       "test",
		UserID:          "user-1",
		SessionID:       "sess-1",
		TraceID:         "trace-xyz",
		ParentTaskID:    "task-abc",
		ArtifactService: newFailingArtifactSvc(errors.New("store unavailable")),
	})
	if err != nil {
		t.Fatalf("expected no error (graceful fallback), got %v", err)
	}

	logged := logBuf.String()
	for _, want := range []string{"trace-xyz", "task-abc", "wi_target-wf_"} {
		if !strings.Contains(logged, want) {
			t.Errorf("save-failure Warn log missing correlation field %q, got: %s", want, logged)
		}
	}
}

func TestPrepareInputParameterModeTagsHandoffArtifactWorking(t *testing.T) {
	agent := New(AgentConfig{Name: "caller"})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	wt := &workflowAgentTool{
		peerName: "target-wf",
		agent:    agent,
		card: a2a.AgentCard{
			Name: "target-wf",
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{
					URI:    ExtensionURIDisplayName,
					Params: map[string]any{"display_name": "Nightly Rollup"},
				}},
			},
		},
	}
	scoped := &artifact.ScopedService{Inner: artifactmem.New(), Scope: "agent"}

	_, artifactURI, err := wt.prepareInput(context.Background(), map[string]any{
		"value": 42,
	}, &tool.Context{
		AgentName:       "caller",
		UserID:          "user-1",
		SessionID:       "sess-1",
		ArtifactService: scoped,
	})
	if err != nil {
		t.Fatal(err)
	}

	const prefix = "artifact://"
	if !strings.HasPrefix(artifactURI, prefix) {
		t.Fatalf("artifactURI = %q, want %s<filename>:<version>", artifactURI, prefix)
	}
	rest := strings.TrimPrefix(artifactURI, prefix)
	filename := rest[:strings.LastIndex(rest, ":")]
	if !strings.HasPrefix(filename, "wi_") {
		t.Errorf("filename = %q, want wi_ prefix", filename)
	}

	meta := artifact.LoadCompanionMetadata(context.Background(), scoped, "caller", "user-1", "sess-1", filename, nil)
	if meta == nil {
		t.Fatal("expected non-nil metadata for saved input artifact")
	}
	rawTags, ok := meta["tags"].([]any)
	if !ok || len(rawTags) != 1 || rawTags[0] != a2a.ArtifactTagWorking {
		t.Errorf("tags = %v, want [%s]", meta["tags"], a2a.ArtifactTagWorking)
	}
	desc, _ := meta["description"].(string)
	if !strings.Contains(desc, "Nightly Rollup") {
		t.Errorf("description = %q, want the display name in it", desc)
	}
	if strings.Contains(desc, "target-wf") {
		t.Errorf("description = %q, should not name the workflow by its routable peerName", desc)
	}
}

func TestPrepareInputExcludesInputArtifactKey(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	wt := &workflowAgentTool{peerName: "test", agent: agent}

	inputJSON, _, err := wt.prepareInput(context.Background(), map[string]any{
		"input_artifact": "",
		"value":          1,
	}, &tool.Context{})

	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	json.Unmarshal(inputJSON, &parsed) //nolint:errcheck // test
	if _, ok := parsed["input_artifact"]; ok {
		t.Error("input_artifact key should be excluded from parameter JSON")
	}
}

func TestBuildA2AMessageInlineBytes(t *testing.T) {
	wt := &workflowAgentTool{
		peerName: "test-wf",
		agent:    &Agent{cfg: AgentConfig{Name: "caller"}},
		inputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}

	inputJSON := []byte(`{"value":42}`)
	msg := wt.buildA2AMessage(inputJSON, "", "ctx-123", "parent-task-1", "")

	if msg.Role != a2a.RoleUser {
		t.Errorf("role = %q", msg.Role)
	}
	if msg.ContextID != "ctx-123" {
		t.Errorf("contextID = %q", msg.ContextID)
	}
	if len(msg.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(msg.Parts))
	}

	if msg.Metadata["sessionBehavior"] != "RUN_BASED" {
		t.Error("missing sessionBehavior metadata")
	}
	if msg.Metadata["parentTaskId"] != "parent-task-1" {
		t.Errorf("parentTaskId = %v, want %q", msg.Metadata["parentTaskId"], "parent-task-1")
	}
}

func TestBuildA2AMessageArtifactURI(t *testing.T) {
	wt := &workflowAgentTool{
		peerName: "test-wf",
		agent:    &Agent{cfg: AgentConfig{Name: "caller"}},
		inputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}

	msg := wt.buildA2AMessage(nil, "artifact://data.json", "ctx-456", "parent-task-2", "")

	if len(msg.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(msg.Parts))
	}
}

func TestBuildPeerInfoWorkflowFlag(t *testing.T) {
	agent := New(AgentConfig{Name: "self", Namespace: "ns"})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	agent.InjectPeerCard(a2a.AgentCard{
		Name: "standard-peer",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    ExtensionURIDisplayName,
				Params: map[string]any{"display_name": "Standard Peer"},
			}},
		},
	})

	agent.InjectPeerCard(a2a.AgentCard{
		Name: "my-workflow",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI:    ExtensionURIAgentType,
					Params: map[string]any{"type": "workflow"},
				},
			},
		},
	})

	peers := agent.buildPeerInfo(context.Background(), nil)
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}

	var foundStandard, foundWorkflow bool
	for _, p := range peers {
		switch p.Name {
		case "standard-peer":
			if p.IsWorkflow {
				t.Error("standard-peer should not be a workflow")
			}
			if p.DisplayName != "Standard Peer" {
				t.Errorf("expected DisplayName propagated from card, got %q", p.DisplayName)
			}
			foundStandard = true
		case "my-workflow":
			if !p.IsWorkflow {
				t.Error("my-workflow should be a workflow")
			}
			foundWorkflow = true
		}
	}
	if !foundStandard || !foundWorkflow {
		t.Errorf("missing peers: standard=%v, workflow=%v", foundStandard, foundWorkflow)
	}
}

func TestWorkflowPeerToolExecute_NilIdentity_EnterpriseRBAC_Denies(t *testing.T) {
	agent := newEnterpriseAgentHarness(t)

	wt := &workflowAgentTool{
		peerName:  "target-workflow",
		agent:     agent,
		namespace: "ns",
	}

	result, err := wt.Execute(context.Background(), map[string]any{
		"task_description": "do X",
	}, &tool.Context{Identity: nil, SessionID: "s"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want %q", result.Status, tool.StatusError)
	}
	if !strings.Contains(result.Message, "workflow invocation denied") {
		t.Errorf("message = %q, want contains 'workflow invocation denied'", result.Message)
	}
	if !strings.Contains(result.Message, "no identity provided") {
		t.Errorf("message = %q, want contains 'no identity provided'", result.Message)
	}
}

func TestWorkflowPeerToolExecute_DeployedWorkflow_RequiresWorkflowScope(t *testing.T) {
	const wfCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const dashedUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"

	t.Run("agent wildcard denied", func(t *testing.T) {
		agent := newEnterpriseAgentHarness(t)
		wt := &workflowAgentTool{peerName: wfCard, agent: agent, namespace: "ns"}
		result, err := wt.Execute(context.Background(), map[string]any{
			"task_description": "do X",
		}, &tool.Context{
			Identity:  &auth.Identity{UserID: "u", Scopes: []string{"agent:*:invoke"}},
			SessionID: "s",
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if result.Status != tool.StatusError || !strings.Contains(result.Message, "workflow invocation denied") {
			t.Fatalf("status=%q message=%q, want error with 'workflow invocation denied'", result.Status, result.Message)
		}
	})

	t.Run("workflow scope authorized", func(t *testing.T) {
		agent := newEnterpriseAgentHarness(t)
		brk := newMockBroker()
		agent.svc.(*mockServices).brk = brk
		agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

		wt := &workflowAgentTool{
			peerName:    wfCard,
			card:        a2a.AgentCard{Name: wfCard},
			agent:       agent,
			namespace:   agent.cfg.Namespace,
			inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}
		result, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{
			Identity:    &auth.Identity{UserID: "u", Scopes: []string{"workflow:" + dashedUUID + ":invoke"}},
			UserID:      "u",
			SessionID:   "s",
			PauseResume: true,
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if result.Status != tool.StatusPending {
			t.Fatalf("status = %q, want pending (authorization passed)", result.Status)
		}
		if subTaskID, _ := result.Data["sub_task_id"].(string); subTaskID != "" {
			agent.peerRouter.Cancel(subTaskID)
		}
	})
}

func TestWorkflowToolExecutePauseResumeReturnsPending(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

	wt := &workflowAgentTool{
		peerName:  "target-wf",
		card:      a2a.AgentCard{Name: "target-wf"},
		agent:     agent,
		namespace: agent.cfg.Namespace,
		inputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}

	result, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{
		UserID:      "user-1",
		SessionID:   "sess-1",
		PauseResume: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusPending {
		t.Errorf("status = %q, want pending", result.Status)
	}
	if result.OnDelivery == nil {
		t.Fatal("expected Pending result with OnDelivery relay")
	}
	subTaskID, _ := result.Data["sub_task_id"].(string)
	if subTaskID == "" {
		t.Errorf("Data[sub_task_id] = %v, want non-empty correlation key", result.Data["sub_task_id"])
	}
	if kind, _ := result.Data["pending_kind"].(string); kind != "workflow" {
		t.Errorf("Data[pending_kind] = %q, want \"workflow\"", kind)
	}
	agent.peerRouter.Cancel(subTaskID)
}

func TestWorkflowToolExecuteRegistersWithForwardArtifactProgress(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

	wt := &workflowAgentTool{
		peerName:  "target-wf",
		card:      a2a.AgentCard{Name: "target-wf"},
		agent:     agent,
		namespace: agent.cfg.Namespace,
		inputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}

	result, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{
		UserID:      "user-1",
		SessionID:   "sess-1",
		PauseResume: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	subTaskID, _ := result.Data["sub_task_id"].(string)
	if subTaskID == "" {
		t.Fatalf("Data[sub_task_id] = %v, want non-empty correlation key", result.Data["sub_task_id"])
	}
	defer agent.peerRouter.Cancel(subTaskID)

	agent.peerRouter.mu.Lock()
	pending := agent.peerRouter.pending[subTaskID]
	agent.peerRouter.mu.Unlock()
	if pending == nil {
		t.Fatal("expected a pending registration for the workflow sub-task")
	}
	if !pending.forwardArtifactProgress {
		t.Error("workflow peer tool must register with WithForwardArtifactProgress so " +
			"rewriteStatusEvent forwards the workflow's real content artifacts to the orchestrator")
	}
}

func TestClassifyPendingKindWorkflow(t *testing.T) {
	r := tool.Pending("waiting", tool.WithData(map[string]any{
		"sub_task_id":   "wf-req-x",
		"workflow_name": "target-wf",
		"pending_kind":  "workflow",
	}))
	if got := classifyPendingKind(r); got != PendingCallWorkflow {
		t.Errorf("classifyPendingKind = %v, want PendingCallWorkflow", got)
	}
}

type ctxHonoringArtifactStore struct {
	artifact.Service
}

func (s ctxHonoringArtifactStore) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return s.Service.Load(ctx, loc, version)
}

func TestWorkflowToolExecutePauseResumeLoadsOutputArtifactAfterCtxCancel(t *testing.T) {
	var agent *Agent
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Error(err)
			return
		}
		resultData := map[string]any{
			"type":                "structured_invocation_result",
			"status":              "success",
			"output_artifact_ref": map[string]any{"name": "wf_out.json"},
		}
		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
				Message: &a2a.Message{
					Parts: []a2a.Part{a2a.NewDataPart(resultData)},
				},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{Name: "caller", Namespace: "ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	store := ctxHonoringArtifactStore{Service: artifactmem.New()}
	scoped := &artifact.ScopedService{Inner: store, Scope: "agent"}
	loc := scoped.Resolve("target-wf", "user-1", "sess-1", "wf_out.json")
	if _, err := store.Save(context.Background(), loc, []byte(`{"answer":42}`), "application/json"); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	wt := &workflowAgentTool{
		peerName:  "target-wf",
		card:      a2a.AgentCard{Name: "target-wf"},
		agent:     agent,
		namespace: "ns",
	}

	ctx, cancel := context.WithCancel(context.Background())
	result, err := wt.Execute(ctx, map[string]any{}, &tool.Context{
		UserID:          "user-1",
		SessionID:       "sess-1",
		ArtifactService: scoped,
		PauseResume:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OnDelivery == nil {
		t.Fatal("expected Pending result with OnDelivery relay")
	}
	cancel()

	done := make(chan *tool.Result, 1)
	result.OnDelivery(func(r *tool.Result) { done <- r })

	select {
	case final := <-done:
		if final.Status != tool.StatusSuccess {
			t.Fatalf("delivered status = %q, want success", final.Status)
		}
		out, ok := final.Data["output"].(map[string]any)
		if !ok {
			t.Fatalf("Data[output] missing or not an object: %#v — artifact content dropped (relay forwarded a cancelled ctx)", final.Data)
		}
		if out["answer"] != float64(42) {
			t.Errorf("output = %v, want answer=42", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OnDelivery never delivered the workflow response")
	}
}

func TestWorkflowToolExecutePauseResumeDeliversSuccess(t *testing.T) {
	var agent *Agent
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Error(err)
			return
		}
		resultData := map[string]any{
			"type":   "structured_invocation_result",
			"status": "success",
		}
		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
				Message: &a2a.Message{
					Parts: []a2a.Part{a2a.NewDataPart(resultData)},
				},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{Name: "caller", Namespace: "ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	wt := &workflowAgentTool{
		peerName:  "target-wf",
		card:      a2a.AgentCard{Name: "target-wf"},
		agent:     agent,
		namespace: "ns",
	}

	result, err := wt.Execute(context.Background(), map[string]any{"value": 42}, &tool.Context{
		UserID:      "user-1",
		SessionID:   "sess-1",
		PauseResume: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OnDelivery == nil {
		t.Fatal("expected Pending result with OnDelivery relay")
	}

	done := make(chan *tool.Result, 1)
	result.OnDelivery(func(r *tool.Result) { done <- r })

	select {
	case final := <-done:
		if final.Status != tool.StatusSuccess {
			t.Errorf("delivered status = %q, want success", final.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OnDelivery never delivered the workflow response")
	}
}

func TestWorkflowToolExecutePauseResumeDeliversCancel(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

	wt := &workflowAgentTool{
		peerName:  "target-wf",
		card:      a2a.AgentCard{Name: "target-wf"},
		agent:     agent,
		namespace: agent.cfg.Namespace,
		inputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}

	result, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{
		UserID:      "user-1",
		SessionID:   "sess-1",
		PauseResume: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OnDelivery == nil {
		t.Fatal("expected Pending result with OnDelivery relay")
	}

	subTaskID, _ := result.Data["sub_task_id"].(string)
	if subTaskID == "" {
		t.Fatal("missing sub_task_id correlation key")
	}

	done := make(chan *tool.Result, 1)
	result.OnDelivery(func(r *tool.Result) { done <- r })

	agent.peerRouter.CancelWithPropagation(subTaskID, agent.cfg.Namespace)

	select {
	case final := <-done:
		if final.Status != tool.StatusError {
			t.Errorf("delivered status = %q, want error", final.Status)
		}
		if !strings.Contains(final.Message, "cancelled") && !strings.Contains(final.Message, "timed out") {
			t.Errorf("message = %q, should mention cancelled/timed out", final.Message)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OnDelivery never delivered the cancellation")
	}
}

func waitForPausedTask(t *testing.T, agent *Agent, taskID string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if agent.pausedTasks.Load(taskID) != nil {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("task %q never reached paused state", taskID)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func submitTaskMessage(t *testing.T, agent *Agent, ns, name, taskID, ctxID string) {
	t.Helper()
	req := a2a.Request{
		JSONRPC: "2.0",
		Method:  "message/send",
		ID:      taskID,
		Params: map[string]any{
			"message": a2a.Message{
				Kind:      "message",
				MessageID: "m-" + taskID,
				Role:      a2a.RoleUser,
				Parts:     []a2a.Part{a2a.NewTextPart("go")},
				ContextID: ctxID,
			},
		},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	agent.routeMessage(t.Context(), &broker.Message{
		Topic:          a2a.AgentRequestTopic(ns, name),
		Payload:        payload,
		UserProperties: map[string]string{"replyTo": "reply/" + taskID, "userId": "u1"},
	})
}

func TestWorkflowPendingResumesThroughDispatchWiring(t *testing.T) {
	const ns, name = "ns", "resume-agent"

	mock := newMockLLM(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "workflow_self", Arguments: "{}"}}},
		&llm.ChatResponse{Content: "all done", FinishReason: "stop"},
	)

	agent := New(AgentConfig{Name: name, Namespace: ns, MaxLLMCalls: 5})
	svc := newMockServices(mock, newMemSessionStore())
	svc.brk = newMockBroker()
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	agent.logger = discardLogger()
	agent.runCtx = t.Context()

	deliverCh := make(chan func(*tool.Result), 1)
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "workflow_self",
		description: "pausing delegation tool",
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			r := tool.Pending("waiting for workflow", tool.WithData(map[string]any{
				"sub_task_id":  "st",
				"pending_kind": "workflow",
			}))
			r.OnDelivery = func(deliver func(*tool.Result)) { deliverCh <- deliver }
			return r, nil
		},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	submitTaskMessage(t, agent, ns, name, "taskR", "ctxR")
	waitForPausedTask(t, agent, "taskR")

	select {
	case deliver := <-deliverCh:
		deliver(tool.OK("workflow finished"))
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher never invoked the tool's OnDelivery")
	}

	deadline := time.After(5 * time.Second)
	for {
		if agent.pausedTasks.Load("taskR") == nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("task did not resume to completion after delivery")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestWorkflowToolExecute_DeniedWithoutInvokeScopeEvenWithAgentDelegateWildcard(t *testing.T) {
	agent := newEnterpriseAgentHarness(t)
	wt := &workflowAgentTool{
		peerName:    "flow_wf",
		card:        a2a.AgentCard{Name: "flow_wf"},
		agent:       agent,
		namespace:   agent.cfg.Namespace,
		inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}

	id := &auth.Identity{UserID: "u", Scopes: []string{"agent:*:delegate"}}
	result, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{UserID: "u", Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error (denied before publish)", result.Status)
	}
	if !strings.Contains(result.Message, "workflow invocation denied") {
		t.Fatalf("message = %q, want it to mention 'workflow invocation denied'", result.Message)
	}
}

func TestWorkflowToolExecute_AllowedWithInvokeScope(t *testing.T) {
	agent := newEnterpriseAgentHarness(t)
	agent.cfg.WorkflowRequestTimeoutSeconds = 1
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

	wt := &workflowAgentTool{
		peerName:    "flow_wf",
		card:        a2a.AgentCard{Name: "flow_wf"},
		agent:       agent,
		namespace:   agent.cfg.Namespace,
		inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}

	id := &auth.Identity{UserID: "u", Scopes: []string{"workflow:flow_wf:invoke"}}
	result, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{UserID: "u", Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error (timeout)", result.Status)
	}
	if strings.Contains(result.Message, "invocation denied") {
		t.Fatalf("authz should have passed with the invoke scope; got a denial: %q", result.Message)
	}
	if !strings.Contains(result.Message, "timed out") {
		t.Fatalf("message = %q, want a timeout (proves it got past authz to publish)", result.Message)
	}
}
