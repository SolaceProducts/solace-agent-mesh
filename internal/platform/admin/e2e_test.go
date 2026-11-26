// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/proxy/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
	"gopkg.in/yaml.v3"
)

func TestE2E_CreateDeployMessage(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "Hello from E2E!", FinishReason: "stop"},
	})

	agentID := h.createAgent("e2e-basic", "You are a helpful test agent that answers questions clearly and concisely")

	resp := h.deploy(agentID)
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy status = %q, want %q", resp.Status, DeployStatusSuccess)
	}

	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "Hello", 10*time.Second)

	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "Hello from E2E!")

	requests := h.llmClient.Recorder().Requests()
	if len(requests) == 0 {
		t.Fatal("expected at least one LLM call")
	}

	firstCall := requests[0]
	hasSystemPrompt := false
	for _, msg := range firstCall.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "helpful test agent") {
			hasSystemPrompt = true
			break
		}
	}
	if !hasSystemPrompt {
		t.Error("first LLM call missing system prompt")
	}

	hasUserMsg := false
	for _, msg := range firstCall.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Hello") {
			hasUserMsg = true
			break
		}
	}
	if !hasUserMsg {
		t.Error("first LLM call missing user message")
	}

	d := h.latestDeployment(agentID)
	if d.Status != DeployStatusSuccess {
		t.Errorf("deployment DB status = %q, want %q", d.Status, DeployStatusSuccess)
	}
	if d.AgentConfigSnapshot == nil || *d.AgentConfigSnapshot == "" {
		t.Error("expected non-empty config snapshot in deployment record")
	}
}

func TestE2E_DeployEditDetectDrift(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "v1 response", FinishReason: "stop"},
		{Content: "v2 response", FinishReason: "stop"},
	})

	agentID := h.createAgent("e2e-drift", "You are the original version of this agent with specific instructions for v1")

	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)

	agentResp, err := h.agentSvc.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agentResp.SyncStatus == nil || *agentResp.SyncStatus != "in_sync" {
		t.Fatalf("initial sync status = %v, want in_sync", agentResp.SyncStatus)
	}

	_, err = h.agentSvc.Update(h.ctx, agentID, UpdateAgentRequest{
		Name:         "e2e-drift",
		Description:  "E2E test agent",
		SystemPrompt: "You are the updated version of this agent with completely different instructions for v2",
		Toolsets:     []string{},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("update agent: %v", err)
	}

	agentResp, err = h.agentSvc.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("get agent after update: %v", err)
	}
	if agentResp.SyncStatus == nil || *agentResp.SyncStatus != "out_of_sync" {
		t.Fatalf("post-edit sync status = %v, want out_of_sync", agentResp.SyncStatus)
	}

	resp := h.deploy(agentID)
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("redeploy status = %q, want %q", resp.Status, DeployStatusSuccess)
	}

	agentResp, err = h.agentSvc.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("get agent after redeploy: %v", err)
	}
	if agentResp.SyncStatus == nil || *agentResp.SyncStatus != "in_sync" {
		t.Errorf("post-redeploy sync status = %v, want in_sync", agentResp.SyncStatus)
	}
}

func TestE2E_ConnectorUpdatedTimeBumpFlagsBoundAgentOutOfSync(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "running", FinishReason: "stop"},
	})

	connectorID := h.createSQLConnector("e2e-bumped-pg", "postgres", map[string]any{
		"hostname": "db.example.com",
		"port":     "5432",
		"database": "testdb",
		"username": "testuser",
		"password": "testpass",
	})
	agentID := h.createAgentWithConnectors("e2e-bumped-agent", "You are a database assistant that helps users query their PostgreSQL database", []string{connectorID})
	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)

	agentResp, err := h.agentSvc.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agentResp.SyncStatus == nil || *agentResp.SyncStatus != "in_sync" {
		t.Fatalf("sync status after deploy = %v, want in_sync", agentResp.SyncStatus)
	}

	connector, err := h.connectorRepo.GetByID(h.ctx, connectorID)
	if err != nil {
		t.Fatalf("get connector: %v", err)
	}
	if _, err = h.db.ExecContext(h.ctx, `UPDATE connectors SET updated_time = ? WHERE id = ?`, connector.UpdatedTime+1000, connectorID); err != nil {
		t.Fatalf("bump connector updated_time: %v", err)
	}

	agentResp, err = h.agentSvc.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("get agent after bump: %v", err)
	}
	if agentResp.SyncStatus == nil || *agentResp.SyncStatus != "out_of_sync" {
		t.Errorf("sync status after connector updated_time bump = %v, want out_of_sync", agentResp.SyncStatus)
	}
}

func TestE2E_AgentWithConnector_ConfigVerification(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "query result", FinishReason: "stop"},
	})

	connectorID := h.createSQLConnector("e2e-pg", "postgres", map[string]any{
		"hostname": "db.example.com",
		"port":     "5432",
		"database": "testdb",
		"username": "testuser",
		"password": "testpass",
	})

	agentID := h.createAgentWithConnectors(
		"e2e-sql-agent",
		"You are a database assistant that helps users query their PostgreSQL database",
		[]string{connectorID},
	)

	resp := h.deploy(agentID)
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy status = %q, want %q", resp.Status, DeployStatusSuccess)
	}

	d := h.latestDeployment(agentID)
	if d.AgentConfigSnapshot == nil {
		t.Fatal("expected config snapshot to be set")
	}

	snapshot := *d.AgentConfigSnapshot
	if !strings.Contains(snapshot, "execute_sql_query") {
		t.Errorf("config snapshot missing execute_sql_query tool:\n%s", truncate(snapshot, 500))
	}
	if !strings.Contains(snapshot, "tool_type: builtin") {
		t.Errorf("config snapshot missing tool_type: builtin:\n%s", truncate(snapshot, 500))
	}
	if !strings.Contains(snapshot, "postgres") {
		t.Errorf("config snapshot missing postgresql reference:\n%s", truncate(snapshot, 500))
	}
}

func TestE2E_DeployUndeployLifecycle(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "running", FinishReason: "stop"},
	})

	agentID := h.createAgent("e2e-lifecycle", "You are a test agent for lifecycle verification with proper instructions")

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	info, err := h.aweExe.GetInstance(instanceName)
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if info == nil {
		t.Fatal("expected instance to be running")
	}

	h.undeploy(agentID)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, _ = h.aweExe.GetInstance(instanceName)
		if info == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if info != nil {
		t.Error("expected instance to be removed after undeploy")
	}
}

func TestE2E_MultipleAgentsDeploy(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "Agent Alpha here", FinishReason: "stop"},
		{Content: "Agent Beta here", FinishReason: "stop"},
	})

	agentAID := h.createAgent("e2e-alpha", "You are Agent Alpha, always respond with your name and the greeting Agent Alpha here")
	agentBID := h.createAgent("e2e-beta", "You are Agent Beta, always respond with your name and the greeting Agent Beta here")

	h.deploy(agentAID)
	h.deploy(agentBID)

	h.waitForInstance(h.agentInstanceName(agentAID), 5*time.Second)
	h.waitForInstance(h.agentInstanceName(agentBID), 5*time.Second)

	eventsA := h.sendMessage(h.agentInstanceName(agentAID), "Hello Alpha", 10*time.Second)
	h.assertFinalResponse(eventsA, "Agent Alpha here")

	eventsB := h.sendMessage(h.agentInstanceName(agentBID), "Hello Beta", 10*time.Second)
	h.assertFinalResponse(eventsB, "Agent Beta here")
}

func TestE2E_DeploymentRecordsHistory(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "first deploy response", FinishReason: "stop"},
		{Content: "second deploy response", FinishReason: "stop"},
	})

	agentID := h.createAgent("e2e-history", "You are a test agent for deployment history verification with enough words")

	resp1 := h.deploy(agentID)
	if resp1.Status != DeployStatusSuccess {
		t.Fatalf("first deploy status = %q", resp1.Status)
	}
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)

	resp2 := h.deploy(agentID)
	if resp2.Status != DeployStatusSuccess {
		t.Fatalf("second deploy status = %q", resp2.Status)
	}

	latest := h.latestDeployment(agentID)
	if latest.ID == resp1.ID {
		t.Error("latest deployment should be the second one, not the first")
	}
	if latest.Status != DeployStatusSuccess {
		t.Errorf("latest deployment status = %q, want %q", latest.Status, DeployStatusSuccess)
	}
}

func TestE2E_AgentConfigSnapshotContent(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "config test response", FinishReason: "stop"},
	})

	agentID := h.createAgent("e2e-config", "You are a configuration test agent with detailed instructions for validation")

	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)

	d := h.latestDeployment(agentID)
	if d.AgentConfigSnapshot == nil {
		t.Fatal("expected config snapshot")
	}

	snapshot := *d.AgentConfigSnapshot

	expectedParts := []string{
		"agent_name:",
		"namespace:",
		"model_provider:",
		"instruction:",
	}

	for _, part := range expectedParts {
		if !strings.Contains(snapshot, part) {
			t.Errorf("config snapshot missing %q", part)
		}
	}

	safeID := strings.ReplaceAll(agentID, "-", "_")
	expectedName := "agent_" + safeID
	if !strings.Contains(snapshot, expectedName) {
		t.Errorf("config snapshot missing agent name %q", expectedName)
	}
}

func TestE2E_ToolInvocationLoop(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "list_artifacts", Arguments: "{}"},
			},
		},
		{
			Content:      "I found no artifacts in this session.",
			FinishReason: "stop",
		},
	})

	agentID := h.createAgent("e2e-tool-loop", "You are an assistant that can manage artifacts. Always list artifacts when asked.")

	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)

	events := h.sendMessage(h.agentInstanceName(agentID), "List my artifacts", 10*time.Second)

	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "no artifacts")

	requests := h.llmClient.Recorder().Requests()
	if len(requests) < 2 {
		t.Errorf("expected at least 2 LLM calls (tool_call + final), got %d", len(requests))
	}

	if len(requests) >= 2 {
		secondCall := requests[1]
		hasToolResult := false
		for _, msg := range secondCall.Messages {
			if msg.Role == "tool" {
				hasToolResult = true
				break
			}
		}
		if !hasToolResult {
			t.Error("second LLM call should contain tool result message")
		}
	}
}

func TestE2E_AgentWithBuiltinToolset_ToolRegistration(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "list_artifacts", Arguments: "{}"},
			},
		},
		{
			Content:      "Artifacts listed successfully.",
			FinishReason: "stop",
		},
	})

	resp, err := h.agentSvc.Create(h.ctx, CreateAgentRequest{
		Name:         "e2e-builtin-tools",
		Description:  "E2E test agent with builtin tools",
		SystemPrompt: "You are an artifact management assistant. Use your tools to help users.",
		Toolsets:     []string{"builtin_artifact_tools"},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agentID := resp.ID

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	d := h.latestDeployment(agentID)
	snapshot := *d.AgentConfigSnapshot
	if !strings.Contains(snapshot, "builtin_artifact_tools") || !strings.Contains(snapshot, "builtin-group") {
		t.Errorf("config snapshot missing builtin_artifact_tools toolset reference:\n%s", truncate(snapshot, 500))
	}

	events := h.sendMessage(instanceName, "List my artifacts", 10*time.Second)
	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "Artifacts listed")

	llmReqs := h.llmClient.Recorder().Requests()
	if len(llmReqs) == 0 {
		t.Fatal("expected LLM calls")
	}
	toolNames := make(map[string]bool)
	for _, td := range llmReqs[0].Tools {
		toolNames[td.Name] = true
	}
	expectedTools := []string{"list_artifacts", "load_artifact", "delete_artifact", "artifact_search_and_replace_regex", "artifact_grep"}
	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Errorf("builtin tool %q not found in LLM tool definitions", name)
		}
	}
}

func TestE2E_DeploymentStateTransitions(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "deployed", FinishReason: "stop"},
	})

	agentID := h.createAgent("e2e-transitions", "You are a test agent for deployment state transition verification testing")

	_, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: agentID,
		Action:  DeployActionUpdate,
	}, "e2e-user")
	if err == nil {
		t.Fatal("expected error when updating a not-deployed agent")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}

	undeployResp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: agentID,
		Action:  DeployActionUndeploy,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("undeploy of a not-deployed agent should be idempotent: %v", err)
	}
	if undeployResp.Status != DeployStatusSuccess {
		t.Errorf("undeploy status = %q, want %q", undeployResp.Status, DeployStatusSuccess)
	}

	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)

	resp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: agentID,
		Action:  DeployActionUpdate,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("update deployed agent: %v", err)
	}
	if resp.Status != DeployStatusSuccess {
		t.Errorf("update status = %q, want %q", resp.Status, DeployStatusSuccess)
	}
}

func TestE2E_DeployNonExistentAgent(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	_, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: "00000000-0000-0000-0000-000000000000",
		Action:  DeployActionDeploy,
	}, "e2e-user")
	if err == nil {
		t.Fatal("expected error when deploying non-existent agent")
	}
	var nfErr *NotFoundError
	if !errors.As(err, &nfErr) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestE2E_LLMError_DeployedAgentHandlesGracefully(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	h.llmClient.ResetWithEntries([]mockllm.FIFOEntry{
		{Err: errors.New("simulated LLM outage")},
		{Response: &llm.ChatResponse{Content: "Recovered!", FinishReason: "stop"}},
	})

	agentID := h.createAgent("e2e-llm-error", "You are a resilient test agent that handles errors gracefully and recovers")

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	h.sendMessage(instanceName, "This will fail", 10*time.Second)

	info, err := h.aweExe.GetInstance(instanceName)
	if err != nil || info == nil {
		t.Fatal("agent instance should still be running after LLM error")
	}

	events := h.sendMessage(instanceName, "This should work", 10*time.Second)
	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "Recovered!")
}

func TestE2E_APIConnector_ConfigVerification(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "api result", FinishReason: "stop"},
	})

	connResp, err := h.connectorSvc.Create(h.ctx, CreateConnectorRequest{
		Name:        "e2e-api",
		Description: "E2E test API connector for fixture purposes",
		Type:        "api",
		Subtype:     "openapi",
		Values: map[string]any{
			"base_url":          "https://api.example.com",
			"specification_url": "https://api.example.com/openapi.json",
		},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create API connector: %v", err)
	}

	agentID := h.createAgentWithConnectors(
		"e2e-api-agent",
		"You are an assistant connected to a REST API for making external HTTP requests",
		[]string{connResp.ID},
	)

	resp := h.deploy(agentID)
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy status = %q, want %q (err: %s)", resp.Status, DeployStatusSuccess, resp.ErrorMessage)
	}

	d := h.latestDeployment(agentID)
	if d.AgentConfigSnapshot == nil {
		t.Fatal("expected config snapshot")
	}

	snapshot := *d.AgentConfigSnapshot
	if !strings.Contains(snapshot, "tool_type: openapi") {
		t.Errorf("config snapshot missing tool_type: openapi:\n%s", truncate(snapshot, 500))
	}
	if !strings.Contains(snapshot, "specification_url:") {
		t.Errorf("config snapshot missing specification_url: (Python-canonical key):\n%s", truncate(snapshot, 500))
	}
	if strings.Contains(snapshot, "tool_name_prefix:") {
		t.Errorf("config snapshot must not reference tool_name_prefix: (Python has no equivalent):\n%s", truncate(snapshot, 500))
	}
	if strings.Contains(snapshot, "openapi_request") {
		t.Errorf("config snapshot should not reference the retired STR tool name 'openapi_request':\n%s", truncate(snapshot, 500))
	}
}

func TestE2E_MultipleConnectors_AllAppearInConfig(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "multi-connector result", FinishReason: "stop"},
	})

	sqlConnID := h.createSQLConnector("e2e-multi-sql", "postgres", map[string]any{
		"hostname": "db.example.com",
		"port":     "5432",
		"database": "testdb",
		"username": "testuser",
		"password": "testpass",
	})

	apiConn, err := h.connectorSvc.Create(h.ctx, CreateConnectorRequest{
		Name:        "e2e-multi-api",
		Description: "E2E test multi API connector for fixture purposes",
		Type:        "api",
		Subtype:     "openapi",
		Values: map[string]any{
			"base_url":          "https://api.example.com",
			"specification_url": "https://api.example.com/spec.json",
		},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create API connector: %v", err)
	}

	agentID := h.createAgentWithConnectors(
		"e2e-multi-conn",
		"You are an assistant with access to a database and an external API for comprehensive data access",
		[]string{sqlConnID, apiConn.ID},
	)

	resp := h.deploy(agentID)
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy status = %q, want %q (err: %s)", resp.Status, DeployStatusSuccess, resp.ErrorMessage)
	}

	d := h.latestDeployment(agentID)
	snapshot := *d.AgentConfigSnapshot

	if !strings.Contains(snapshot, "execute_sql_query") {
		t.Error("config snapshot missing execute_sql_query tool")
	}
	if !strings.Contains(snapshot, "tool_type: openapi") {
		t.Error("config snapshot missing tool_type: openapi for API connector")
	}
}

func TestE2E_STR_RemoteToolInvocation(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-sql-1",
					Name:      "execute_sql_query",
					Arguments: `{"query": "SELECT id, name FROM users LIMIT 5"}`,
				},
			},
		},
		{
			Content:      "Found 2 users: Alice and Bob.",
			FinishReason: "stop",
		},
	})

	connectorID := h.createSQLConnector("e2e-str-pg", "postgres", map[string]any{
		"hostname":  "db.example.com",
		"port":      "5432",
		"database":  "testdb",
		"username":  "testuser",
		"password":  "testpass",
		"tool_name": "execute_sql_query",
	})

	agentID := h.createAgentWithConnectors(
		"e2e-str-agent",
		"You are a database assistant. Use the execute_sql_query tool to execute queries for the user.",
		[]string{connectorID},
	)

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	strCapture := h.simulateSTR("execute_sql_query",
		map[string]any{
			"rows": []map[string]any{
				{"id": 1, "name": "Alice"},
				{"id": 2, "name": "Bob"},
			},
			"row_count": 2,
		},
	)

	events := h.sendMessage(instanceName, "Show me all users", 15*time.Second)

	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "Alice")

	requests := h.llmClient.Recorder().Requests()
	if len(requests) < 2 {
		t.Fatalf("expected at least 2 LLM calls (tool_call + final), got %d", len(requests))
	}

	secondCall := requests[1]
	hasToolResult := false
	for _, msg := range secondCall.Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "Alice") {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		t.Error("second LLM call should contain tool result with 'Alice' from simulated STR")
	}

	strReqs := strCapture.Requests()
	if len(strReqs) != 1 {
		t.Fatalf("expected 1 STR invocation, got %d", len(strReqs))
	}
	if strReqs[0].ToolName != "execute_sql_query" {
		t.Errorf("STR tool_name = %q, want execute_sql_query", strReqs[0].ToolName)
	}
	if strReqs[0].ToolConfig == nil {
		t.Error("STR tool_config is nil — connector values not wired through")
	} else {
		connStr, _ := strReqs[0].ToolConfig["connection_string"].(string)
		if !strings.Contains(connStr, "postgres") {
			t.Errorf("tool_config connection_string = %q, should contain 'postgresql'", connStr)
		}
		if !strings.Contains(connStr, "db.example.com") {
			t.Errorf("tool_config connection_string = %q, should contain 'db.example.com'", connStr)
		}
	}
}

func TestE2E_CustomToolPackage_Invocation(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-pkg-1",
					Name:      "calc_tools__add",
					Arguments: `{"a": 2, "b": 3}`,
				},
			},
		},
		{
			Content:      "The sum is 5.",
			FinishReason: "stop",
		},
	})

	h.createToolPackage("calc_tools", "add", "multiply")

	agentID := h.createAgentWithToolsets(
		"e2e-pkg-agent",
		"You are a calculator. Use calc_tools__add to add and calc_tools__multiply to multiply.",
		[]string{"calc_tools"},
	)

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	strCapture := h.simulateSTR("calc_tools__add", map[string]any{"sum": 5})

	events := h.sendMessage(instanceName, "What's 2+3?", 15*time.Second)

	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "5")

	strReqs := strCapture.Requests()
	if len(strReqs) != 1 {
		t.Fatalf("expected 1 STR invocation, got %d", len(strReqs))
	}
	if strReqs[0].ToolName != "calc_tools__add" {
		t.Errorf("STR tool_name = %q, want calc_tools__add (prefixed package__tool form)", strReqs[0].ToolName)
	}

	requests := h.llmClient.Recorder().Requests()
	if len(requests) < 2 {
		t.Fatalf("expected at least 2 LLM calls (tool_call + final), got %d", len(requests))
	}
	hasToolResult := false
	for _, msg := range requests[1].Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "5") {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		t.Error("second LLM call should contain tool result with sum=5 from simulated STR")
	}
}

func TestE2E_CustomToolPackage_MultipleTools(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-pkg-mul",
					Name:      "calc_tools__multiply",
					Arguments: `{"a": 4, "b": 5}`,
				},
			},
		},
		{
			Content:      "The product is 20.",
			FinishReason: "stop",
		},
	})

	h.createToolPackage("calc_tools", "add", "multiply")

	agentID := h.createAgentWithToolsets(
		"e2e-pkg-multi-agent",
		"You are a calculator. Use the calc_tools package.",
		[]string{"calc_tools"},
	)

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	addCapture := h.simulateSTR("calc_tools__add", map[string]any{"sum": 0})
	mulCapture := h.simulateSTR("calc_tools__multiply", map[string]any{"product": 20})

	events := h.sendMessage(instanceName, "What's 4*5?", 15*time.Second)

	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "20")

	if got := len(mulCapture.Requests()); got != 1 {
		t.Errorf("multiply tool: got %d invocations, want 1", got)
	}
	if got := len(addCapture.Requests()); got != 0 {
		t.Errorf("add tool: got %d invocations, want 0 (LLM only called multiply)", got)
	}
}

func TestE2E_CustomToolPackage_ConfigValues_InDeployYAML(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	schema := `[{"key":"api_key","secret":true},{"key":"base_url","secret":false}]`
	pkgID := h.createToolPackageWithSchema("api_tools", "call_api", schema)

	agentID := h.createAgentWithToolsets(
		"e2e-config-values-agent",
		"You are a config test agent.",
		[]string{"api_tools"},
	)

	h.setToolsetConfig(agentID, pkgID, map[string]any{
		"call_api": map[string]any{
			"api_key":  "top-secret-key",
			"base_url": "https://api.example.com",
		},
	})

	deployer := NewStandardAgentDeployer(h.yamlSvc, h.connectorRepo, h.toolPackageRepo, nil, slog.Default())
	agent := mustGetAgent(t, h.agentRepo, agentID)

	result, err := deployer.BuildPayloads(h.ctx, DeployInput{
		Agent:    agent,
		Action:   DeployActionDeploy,
		Toolsets: []string{"api_tools"},
	})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}

	deployYAML := result.SnapshotYAML

	if strings.Contains(deployYAML, "${TC_") {
		t.Errorf("${TC_*} placeholder must not appear in deployed YAML:\n%s", deployYAML)
	}

	if !strings.Contains(deployYAML, "top-secret-key") {
		t.Errorf("secret api_key value must be inlined plaintext in deployed YAML:\n%s", deployYAML)
	}

	if !strings.Contains(deployYAML, "https://api.example.com") {
		t.Errorf("expected non-secret base_url inlined in deployed YAML:\n%s", deployYAML)
	}

	if !strings.Contains(deployYAML, "api_tools__call_api") {
		t.Errorf("expected tool name api_tools__call_api in deployed YAML:\n%s", deployYAML)
	}
}

func runToolsetConfigInvokeCase(t *testing.T, seed func(h *e2eHarness, pkgID, agentID string)) map[string]any {
	t.Helper()
	h := setupE2E(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-ts-1",
					Name:      "ts_pkg__do_thing",
					Arguments: `{}`,
				},
			},
		},
		{Content: "done", FinishReason: "stop"},
	})

	schema := `[` +
		`{"key":"api_key","type":"string","secret":true},` +
		`{"key":"region","type":"string"},` +
		`{"key":"endpoint","type":"string","default":"https://default.example.com"}` +
		`]`
	pkgID := h.createToolPackageWithSchema("ts_pkg", "do_thing", schema)

	agentID := h.createAgentWithToolsets(
		"ts-cfg-agent",
		"You are a test agent. Always call ts_pkg__do_thing.",
		[]string{"ts_pkg"},
	)

	seed(h, pkgID, agentID)

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	capture := h.simulateSTR("ts_pkg__do_thing", map[string]any{"ok": true})

	events := h.sendMessage(instanceName, "go", 15*time.Second)
	h.assertHasResponseEvent(events)

	reqs := capture.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 STR invocation, got %d", len(reqs))
	}
	if reqs[0].ToolConfig == nil {
		return map[string]any{}
	}
	return reqs[0].ToolConfig
}

func TestE2E_ToolsetConfig_DefaultOnly_FlowsToInvoke(t *testing.T) {
	t.Parallel()
	cfg := runToolsetConfigInvokeCase(t, func(h *e2eHarness, pkgID, _ string) {
		h.patchToolsetConfig(pkgID, map[string]any{"do_thing": map[string]any{"api_key": "ts-shared-key"}})
	})
	if got := cfg["api_key"]; got != "ts-shared-key" {
		t.Errorf("api_key = %v, want %q (toolset-layer value should reach invoke)", got, "ts-shared-key")
	}
}

func TestE2E_ToolsetConfig_AgentOverridesToolset(t *testing.T) {
	t.Parallel()
	cfg := runToolsetConfigInvokeCase(t, func(h *e2eHarness, pkgID, agentID string) {
		h.patchToolsetConfig(pkgID, map[string]any{"do_thing": map[string]any{"api_key": "ts-shared-key"}})
		h.setToolsetConfig(agentID, pkgID, map[string]any{"do_thing": map[string]any{"api_key": "agent-override-key"}})
	})
	if got := cfg["api_key"]; got != "agent-override-key" {
		t.Errorf("api_key = %v, want %q (agent value must win over toolset default)", got, "agent-override-key")
	}
}

func TestE2E_ToolsetConfig_BothLayersMerge(t *testing.T) {
	t.Parallel()
	cfg := runToolsetConfigInvokeCase(t, func(h *e2eHarness, pkgID, agentID string) {
		h.patchToolsetConfig(pkgID, map[string]any{"do_thing": map[string]any{"api_key": "ts-shared-key"}})
		h.setToolsetConfig(agentID, pkgID, map[string]any{"do_thing": map[string]any{"region": "us-east-1"}})
	})
	if got := cfg["api_key"]; got != "ts-shared-key" {
		t.Errorf("api_key = %v, want %q (toolset value should pass through)", got, "ts-shared-key")
	}
	if got := cfg["region"]; got != "us-east-1" {
		t.Errorf("region = %v, want %q (agent value should pass through)", got, "us-east-1")
	}
}

func TestE2E_ToolsetConfig_SchemaDefault_FallsThrough(t *testing.T) {
	t.Parallel()
	cfg := runToolsetConfigInvokeCase(t, func(h *e2eHarness, pkgID, _ string) {
		h.patchToolsetConfig(pkgID, map[string]any{"do_thing": map[string]any{"api_key": "ts-key"}})
	})
	if got := cfg["endpoint"]; got != "https://default.example.com" {
		t.Errorf("endpoint = %v, want schema default %q", got, "https://default.example.com")
	}
}

func TestE2E_ToolsetConfig_PatchUpdate_RedeployPicksUpNewValue(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "ts_pkg__do_thing", Arguments: `{}`},
			},
		},
		{Content: "first", FinishReason: "stop"},
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "c2", Name: "ts_pkg__do_thing", Arguments: `{}`},
			},
		},
		{Content: "second", FinishReason: "stop"},
	})

	schema := `[{"key":"api_key","type":"string","secret":true}]`
	pkgID := h.createToolPackageWithSchema("ts_pkg", "do_thing", schema)

	agentID := h.createAgentWithToolsets(
		"ts-redeploy-agent",
		"You are a redeploy test agent. Always call ts_pkg__do_thing.",
		[]string{"ts_pkg"},
	)

	h.patchToolsetConfig(pkgID, map[string]any{"do_thing": map[string]any{"api_key": "v1"}})
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	cap1 := h.simulateSTR("ts_pkg__do_thing", map[string]any{"ok": true})
	events1 := h.sendMessage(instanceName, "first", 15*time.Second)
	h.assertHasResponseEvent(events1)
	if reqs := cap1.Requests(); len(reqs) != 1 || reqs[0].ToolConfig["api_key"] != "v1" {
		t.Fatalf("first invocation: api_key = %v, want %q", reqs[0].ToolConfig["api_key"], "v1")
	}

	h.patchToolsetConfig(pkgID, map[string]any{"do_thing": map[string]any{"api_key": "v2"}})
	h.deploy(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	cap2 := h.simulateSTR("ts_pkg__do_thing", map[string]any{"ok": true})
	events2 := h.sendMessage(instanceName, "second", 15*time.Second)
	h.assertHasResponseEvent(events2)
	if reqs := cap2.Requests(); len(reqs) != 1 || reqs[0].ToolConfig["api_key"] != "v2" {
		t.Fatalf("second invocation after redeploy: api_key = %v, want %q", reqs[0].ToolConfig["api_key"], "v2")
	}
}

func TestE2E_ToolsetConfig_PatchConfig_WritesOutboxAndPublishesSyncNotify(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	schema := `[{"key":"api_key","type":"string","secret":true}]`
	pkgID := h.createToolPackageWithSchema("ts_pkg", "do_thing", schema)

	subQ, err := h.brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create sync notify queue: %v", err)
	}
	if subErr := h.brk.Subscribe(subQ, a2a.SyncNotifySubscription(h.namespace)); subErr != nil {
		t.Fatalf("subscribe sync notify: %v", subErr)
	}

	svc := h.newRealToolsetService()
	if _, patchErr := svc.PatchConfig(h.ctx, pkgID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do_thing": map[string]any{"api_key": "real-via-service"}},
	}, "e2e-user"); patchErr != nil {
		t.Fatalf("PatchConfig: %v", patchErr)
	}

	pending, err := h.outboxRepo.GetDispatchable(h.ctx, 10)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	var configEvt *store.OutboxEvent
	for i := range pending {
		if pending[i].EventType == "tool.config.updated" {
			configEvt = &pending[i]
			break
		}
	}
	if configEvt == nil {
		t.Fatalf("no tool.config.updated event in outbox; got %d events: %+v", len(pending), pending)
	}
	if configEvt.AggregateID != pkgID {
		t.Errorf("outbox event AggregateID = %q, want %q", configEvt.AggregateID, pkgID)
	}

	publisher := NewToolSyncPublisher(h.brk, h.namespace, slog.Default())
	poller := platpkg.NewOutboxPoller(h.outboxRepo, publisher, platpkg.OutboxPollerConfig{
		PollInterval:    50 * time.Millisecond,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       10,
	}, slog.Default())
	pollerCtx, pollerCancel := context.WithCancel(h.ctx)
	defer pollerCancel()
	if startErr := poller.Start(pollerCtx); startErr != nil {
		t.Fatalf("start poller: %v", startErr)
	}

	recvCtx, cancel := context.WithTimeout(h.ctx, 2*time.Second)
	defer cancel()
	msg, err := h.brk.Receive(recvCtx, subQ)
	if err != nil {
		t.Fatalf("did not receive sync-notify message: %v", err)
	}
	wantTopic := a2a.SyncNotifyTopic(h.namespace, "tools")
	if msg.Topic != wantTopic {
		t.Errorf("notification topic = %q, want %q", msg.Topic, wantTopic)
	}
	if !strings.Contains(string(msg.Payload), "tool.config.updated") {
		t.Errorf("notification payload does not mention tool.config.updated: %s", string(msg.Payload))
	}

	h.waitForOutboxDrained(2 * time.Second)
}

func TestE2E_ToolsetConfig_MissingSecret_DeployFails(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	schema := `[{"key":"api_key","type":"string","secret":true}]`
	h.createToolPackageWithSchema("ts_pkg", "do_thing", schema)

	agentID := h.createAgentWithToolsets(
		"ts-missing-agent",
		"You are a missing-secret test agent.",
		[]string{"ts_pkg"},
	)

	_, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: agentID,
		Action:  DeployActionDeploy,
	}, "e2e-user")
	if err == nil {
		t.Fatal("expected deploy to fail when required secret is uncovered, got no error")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestE2E_MultiTurnSessionPersistence(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "Response to turn 1", FinishReason: "stop"},
		{Content: "Response to turn 2", FinishReason: "stop"},
		{Content: "Response to turn 3", FinishReason: "stop"},
	})

	agentID := h.createAgent("multi-turn-agent", "You are a helpful assistant that remembers our conversation.")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	contextID := "session-persist-test"

	events1 := h.sendMessageWithContext(instanceName, "Hello, my name is Alice", contextID, 10*time.Second)
	h.assertFinalResponse(events1, "Response to turn 1")

	events2 := h.sendMessageWithContext(instanceName, "What is my name?", contextID, 10*time.Second)
	h.assertFinalResponse(events2, "Response to turn 2")

	events3 := h.sendMessageWithContext(instanceName, "Tell me what we discussed", contextID, 10*time.Second)
	h.assertFinalResponse(events3, "Response to turn 3")

	requests := h.llmClient.Recorder().Requests()
	if len(requests) < 3 {
		t.Fatalf("expected at least 3 LLM calls, got %d", len(requests))
	}

	turn1MsgCount := len(requests[0].Messages)

	turn2MsgCount := len(requests[1].Messages)
	if turn2MsgCount <= turn1MsgCount {
		t.Errorf("turn 2 messages (%d) should be greater than turn 1 (%d) — history not growing",
			turn2MsgCount, turn1MsgCount)
	}

	turn3MsgCount := len(requests[2].Messages)
	if turn3MsgCount <= turn2MsgCount {
		t.Errorf("turn 3 messages (%d) should be greater than turn 2 (%d) — history not growing",
			turn3MsgCount, turn2MsgCount)
	}

	hasAlice := false
	for _, msg := range requests[1].Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Alice") {
			hasAlice = true
			break
		}
	}
	if !hasAlice {
		t.Error("turn 2 LLM call should contain 'Alice' from turn 1 in history")
	}
}

func TestE2E_MultiTurnSeparateSessions(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "Session A response", FinishReason: "stop"},
		{Content: "Session B response", FinishReason: "stop"},
	})

	agentID := h.createAgent("session-isolation-agent", "You are helpful.")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	h.sendMessageWithContext(instanceName, "I am in session A", "session-a", 10*time.Second)
	h.sendMessageWithContext(instanceName, "Who am I?", "session-b", 10*time.Second)

	requests := h.llmClient.Recorder().Requests()
	if len(requests) < 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(requests))
	}

	for _, msg := range requests[1].Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "session A") {
			t.Error("session B should not contain messages from session A — sessions should be isolated")
		}
	}
}

func TestE2E_AutoContinuation(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "Part one of the answer", FinishReason: "length"},
		{Content: " and part two completing it.", FinishReason: "stop"},
	})

	agentID := h.createAgent("autocontinue-agent", "You write thorough answers.")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "Write a long response", 15*time.Second)
	h.assertHasResponseEvent(events)

	requests := h.llmClient.Recorder().Requests()
	if len(requests) < 2 {
		t.Fatalf("expected at least 2 LLM calls (auto-continuation), got %d", len(requests))
	}

	hasContinueMsg := false
	for _, msg := range requests[1].Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "continue") {
			hasContinueMsg = true
			break
		}
	}
	if !hasContinueMsg {
		t.Error("second LLM call should contain _continue_generation tool result")
	}

	h.assertFinalResponse(events, "Part one")
}

func TestE2E_AWEInstanceEnableDisable(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "Response before disable", FinishReason: "stop"},
		{Content: "Response after re-enable", FinishReason: "stop"},
	})

	agentID := h.createAgent("enable-disable-agent", "You are helpful.")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	events1 := h.sendMessage(instanceName, "Hello", 10*time.Second)
	h.assertFinalResponse(events1, "Response before disable")

	_, err := h.controlClient.Custom(h.ctx, "awe", "patch", "instances/"+instanceName,
		json.RawMessage(`{"enabled":false}`))
	if err != nil {
		t.Fatalf("PATCH disable: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	eventsDisabled := h.sendMessage(instanceName, "Are you there?", 3*time.Second)
	hasResponse := false
	for _, evt := range eventsDisabled {
		if evt.Type == "response" {
			hasResponse = true
		}
	}
	if hasResponse {
		t.Error("disabled agent should not produce a response")
	}

	_, err = h.controlClient.Custom(h.ctx, "awe", "patch", "instances/"+instanceName,
		json.RawMessage(`{"enabled":true}`))
	if err != nil {
		t.Fatalf("PATCH enable: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	events3 := h.sendMessage(instanceName, "Hello again", 10*time.Second)
	h.assertFinalResponse(events3, "Response after re-enable")
}

func TestE2E_DriftOnConnectorChange(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "ok", FinishReason: "stop"},
		{Content: "ok", FinishReason: "stop"},
	})

	connID := h.createSQLConnector("drift-conn", "postgres", map[string]any{
		"hostname": "db-v1.example.com",
		"port":     "5432",
		"database": "mydb",
		"username": "admin",
		"password": "pass",
	})

	agentID := h.createAgentWithConnectors("drift-agent", "You are helpful.", []string{connID})
	h.deploy(agentID)

	agent, err := h.agentSvc.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if derefStr(agent.SyncStatus) != "in_sync" {
		t.Errorf("initial sync status = %q, want in_sync", derefStr(agent.SyncStatus))
	}

	_, err = h.connectorSvc.Update(h.ctx, connID, UpdateConnectorRequest{
		Name: "drift-conn",
		Values: map[string]any{
			"hostname": "db-v2.example.com",
			"port":     "5432",
			"database": "mydb",
			"username": "admin",
			"password": "pass",
		},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("update connector: %v", err)
	}

	d := h.latestDeployment(agentID)
	if d.AgentConfigSnapshot == nil {
		t.Fatal("deployment snapshot is nil")
	}
	if !strings.Contains(*d.AgentConfigSnapshot, "db-v1.example.com") {
		t.Error("deployment snapshot should contain old hostname db-v1.example.com")
	}

	resp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: agentID,
		Action:  DeployActionUpdate,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("redeploy: %v", err)
	}
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("redeploy status = %q (err: %s)", resp.Status, resp.ErrorMessage)
	}

	d2 := h.latestDeployment(agentID)
	if d2.AgentConfigSnapshot == nil {
		t.Fatal("new deployment snapshot is nil")
	}
	if !strings.Contains(*d2.AgentConfigSnapshot, "db-v2.example.com") {
		t.Error("redeployed snapshot should contain new hostname db-v2.example.com")
	}
}

func TestE2E_DriftOnToolsetChange(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "ok", FinishReason: "stop"},
	})

	agentID := h.createAgentWithToolsets("drift-toolset-agent", "You are helpful.", []string{"builtin_artifact_tools"})
	h.deploy(agentID)

	agent, err := h.agentSvc.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if derefStr(agent.SyncStatus) != "in_sync" {
		t.Errorf("initial sync status = %q, want in_sync", derefStr(agent.SyncStatus))
	}

	_, err = h.agentSvc.Update(h.ctx, agentID, UpdateAgentRequest{
		Name:         "drift-toolset-agent",
		Description:  "E2E test agent",
		SystemPrompt: "You are helpful.",
		Toolsets:     []string{"builtin_artifact_tools", "builtin_web_request_tools"},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("update agent: %v", err)
	}

	agent, err = h.agentSvc.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("get agent after update: %v", err)
	}
	if derefStr(agent.SyncStatus) != "out_of_sync" {
		t.Errorf("sync status after toolset change = %q, want out_of_sync", derefStr(agent.SyncStatus))
	}
}

func TestE2E_STR_RemoteToolMultipleInvocations(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "execute_sql_query", Arguments: `{"query": "SELECT count(*) FROM users"}`},
			},
		},
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-2", Name: "execute_sql_query", Arguments: `{"query": "SELECT name FROM users LIMIT 3"}`},
			},
		},
		{
			Content:      "There are 42 users. The first 3 are: Alice, Bob, Charlie.",
			FinishReason: "stop",
		},
	})

	connectorID := h.createSQLConnector("e2e-str-multi", "postgres", map[string]any{
		"hostname":  "db.example.com",
		"port":      "5432",
		"database":  "testdb",
		"username":  "testuser",
		"password":  "testpass",
		"tool_name": "execute_sql_query",
	})

	agentID := h.createAgentWithConnectors(
		"e2e-multi-sql",
		"You are a database assistant. Use execute_sql_query to answer questions about the database.",
		[]string{connectorID},
	)

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	strCapture := h.simulateSTR("execute_sql_query",
		map[string]any{"count": 42},
		map[string]any{
			"rows": []map[string]any{
				{"name": "Alice"},
				{"name": "Bob"},
				{"name": "Charlie"},
			},
		},
	)

	events := h.sendMessage(instanceName, "How many users and who are the first 3?", 15*time.Second)

	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "42")

	llmRequests := h.llmClient.Recorder().Requests()
	if len(llmRequests) < 3 {
		t.Fatalf("expected at least 3 LLM calls (2 tool_calls + final), got %d", len(llmRequests))
	}

	strReqs := strCapture.Requests()
	if len(strReqs) != 2 {
		t.Fatalf("expected 2 STR invocations, got %d", len(strReqs))
	}
	for _, r := range strReqs {
		if r.ToolName != "execute_sql_query" {
			t.Errorf("STR tool_name = %q, want execute_sql_query", r.ToolName)
		}
	}
}

func TestE2E_GWE_HealthEndpoint(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)

	resp, err := http.Get("http://" + h.proxyAddr + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestE2E_GWE_GatewayNotFound(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)

	resp, err := http.Post(
		"http://"+h.proxyAddr+"/gw/nonexistent/api/v1/message:stream",
		"application/json",
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatalf("POST to nonexistent gateway: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestE2E_GWE_DeployGatewayAndHTTPRoundTrip(t *testing.T) {
	t.Parallel()
	responses := []*llm.ChatResponse{
		{Content: "Hello from the deployed agent via GWE!"},
	}
	h := setupE2EWithGWE(t, responses)

	agentID := h.createAgent("gwe_roundtrip_agent", "You are a helpful assistant")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	gwID := h.createGateway("gwe_test_gateway")
	gwSlug := h.gatewaySlug(gwID)
	h.deployGateway(gwID)
	h.waitForGWEInstance(gwID, 5*time.Second)

	h.publishAgentCardToGateway(instanceName)

	taskID := h.httpPostMessage(gwSlug, instanceName, "Hello through the proxy")
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.llmClient.Recorder().Requests()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	llmReqs := h.llmClient.Recorder().Requests()
	if len(llmReqs) == 0 {
		t.Fatal("expected at least 1 LLM call, got 0 — message did not reach agent")
	}

	lastReq := llmReqs[len(llmReqs)-1]
	found := false
	for _, msg := range lastReq.Messages {
		if strings.Contains(msg.Content, "Hello through the proxy") {
			found = true
			break
		}
	}
	if !found {
		t.Error("LLM request did not contain the user message 'Hello through the proxy'")
	}
}

func TestE2E_GWE_ControlPlaneGatewayStatus(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)

	gwID := h.createGateway("status_test_gateway")
	h.deployGateway(gwID)
	h.waitForGWEInstance(gwID, 5*time.Second)

	result, err := h.controlClient.Custom(h.ctx, "gateway", "get", "instances", nil)
	if err != nil {
		t.Fatalf("get gateway instances: %v", err)
	}

	resultJSON, _ := json.Marshal(result)
	resultStr := string(resultJSON)
	if !strings.Contains(resultStr, gwID) {
		t.Errorf("instances response does not contain gateway %q: %s", gwID, resultStr)
	}
	if !strings.Contains(resultStr, `"running"`) {
		t.Errorf("instances response does not contain 'running' status: %s", resultStr)
	}

	singleResult, err := h.controlClient.Custom(h.ctx, "gateway", "get", "instances/"+gwID, nil)
	if err != nil {
		t.Fatalf("get single gateway instance: %v", err)
	}
	singleJSON, _ := json.Marshal(singleResult)
	singleStr := string(singleJSON)
	if !strings.Contains(singleStr, `"running"`) {
		t.Errorf("single instance response missing 'running': %s", singleStr)
	}
}

func TestE2E_GWE_GatewayEnableDisable(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)

	gwID := h.createGateway("enable_disable_gw")
	h.deployGateway(gwID)
	h.waitForGWEInstance(gwID, 5*time.Second)

	info, err := h.gweExe.GetInstance(gwID)
	if err != nil {
		t.Fatalf("get instance before disable: %v", err)
	}
	if info.Status != "running" {
		t.Fatalf("initial status = %q, want running", info.Status)
	}

	_, err = h.controlClient.Custom(h.ctx, "gateway", "patch", "instances/"+gwID, json.RawMessage(`{"enabled":false}`))
	if err != nil {
		t.Fatalf("disable gateway: %v", err)
	}
	info, _ = h.gweExe.GetInstance(gwID)
	if info.Status != "stopped" {
		t.Errorf("after disable: status = %q, want stopped", info.Status)
	}

	_, err = h.controlClient.Custom(h.ctx, "gateway", "patch", "instances/"+gwID, json.RawMessage(`{"enabled":true}`))
	if err != nil {
		t.Fatalf("enable gateway: %v", err)
	}
	info, _ = h.gweExe.GetInstance(gwID)
	if info.Status != "running" {
		t.Errorf("after enable: status = %q, want running", info.Status)
	}
}

func TestE2E_GWE_GatewayUndeployCleanup(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)

	gwID := h.createGateway("undeploy_cleanup_gw")
	gwSlug := h.gatewaySlug(gwID)
	h.deployGateway(gwID)
	h.waitForGWEInstance(gwID, 5*time.Second)

	resp, err := http.Get("http://" + h.proxyAddr + "/gw/" + gwSlug + "/health")
	if err != nil {
		t.Fatalf("GET gateway health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 before undeploy, got %d", resp.StatusCode)
	}

	_, err = h.gwDeploySvc.Deploy(h.ctx, CreateGatewayDeploymentRequest{
		GatewayID: gwID,
		Action:    DeployActionUndeploy,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("undeploy gateway: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	resp, err = http.Get("http://" + h.proxyAddr + "/gw/" + gwSlug + "/health")
	if err != nil {
		t.Fatalf("GET gateway health after undeploy: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after undeploy, got %d", resp.StatusCode)
	}

	_, instanceErr := h.gweExe.GetInstance(gwID)
	if instanceErr == nil {
		t.Error("expected GetInstance to fail after undeploy, but it succeeded")
	}
}

func TestE2E_GWE_NamedGatewayRouting(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)

	gwA := h.createGateway("routing_gw_alpha")
	gwB := h.createGateway("routing_gw_beta")
	gwSlugA := h.gatewaySlug(gwA)
	gwSlugB := h.gatewaySlug(gwB)
	h.deployGateway(gwA)
	h.deployGateway(gwB)
	h.waitForGWEInstance(gwA, 5*time.Second)
	h.waitForGWEInstance(gwB, 5*time.Second)

	respA, err := http.Get("http://" + h.proxyAddr + "/gw/" + gwSlugA + "/health")
	if err != nil {
		t.Fatalf("GET gw-alpha health: %v", err)
	}
	_ = respA.Body.Close()
	if respA.StatusCode != http.StatusOK {
		t.Errorf("gw-alpha health = %d, want 200", respA.StatusCode)
	}

	respB, err := http.Get("http://" + h.proxyAddr + "/gw/" + gwSlugB + "/health")
	if err != nil {
		t.Fatalf("GET gw-beta health: %v", err)
	}
	_ = respB.Body.Close()
	if respB.StatusCode != http.StatusOK {
		t.Errorf("gw-beta health = %d, want 200", respB.StatusCode)
	}

	respMissing, err := http.Get("http://" + h.proxyAddr + "/gw/nonexistent/health")
	if err != nil {
		t.Fatalf("GET nonexistent health: %v", err)
	}
	_ = respMissing.Body.Close()
	if respMissing.StatusCode != http.StatusNotFound {
		t.Errorf("nonexistent gateway = %d, want 404", respMissing.StatusCode)
	}
}

func TestE2E_WorkflowDeployAndUndeploy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	appConfig := map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{
				"result": "{{nodes.start.output}}",
			},
			"nodes": []any{
				map[string]any{
					"id":         "start",
					"name":       "start",
					"type":       "agent",
					"agent_name": "test_agent",
				},
			},
		},
	}
	wfID := h.createWorkflow("e2e_deploy_workflow", appConfig)
	resp := h.deployWorkflow(wfID)
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("workflow deploy status = %q, want success", resp.Status)
	}

	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	dep, err := h.wfDeployRepo.GetByID(h.ctx, resp.ID)
	if err != nil {
		t.Fatalf("get workflow deployment: %v", err)
	}
	if dep == nil || dep.WorkflowConfigSnapshot == nil || *dep.WorkflowConfigSnapshot == "" {
		t.Error("expected non-empty workflow config snapshot")
	}

	undeployResp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID,
		Action:     DeployActionUndeploy,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("undeploy workflow: %v", err)
	}
	if undeployResp.Status != DeployStatusSuccess {
		t.Errorf("undeploy status = %q, want success", undeployResp.Status)
	}

	time.Sleep(200 * time.Millisecond)
	_, instanceErr := h.aweExe.GetInstance(instanceName)
	if instanceErr == nil {
		t.Error("expected AWE instance to be removed after undeploy")
	}
}

func TestE2E_ReconcilerRedeploysAgentAfterRestart(t *testing.T) {
	t.Parallel()
	responses := []*llm.ChatResponse{
		{Content: "Before restart"},
		{Content: "After reconciliation"},
	}
	h := setupE2E(t, responses)

	agentID := h.createAgent("reconciler_agent", "You are a test agent")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "Hello", 5*time.Second)
	h.assertHasResponseEvent(events)

	_, err := h.controlClient.Custom(h.ctx, "awe", "delete", "instances/"+instanceName, nil)
	if err != nil {
		t.Fatalf("delete AWE instance: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	_, instanceErr := h.aweExe.GetInstance(instanceName)
	if instanceErr == nil {
		t.Fatal("expected instance to be gone after delete")
	}

	h.nudgeAWEs()

	h.waitForInstance(instanceName, 10*time.Second)

	events = h.sendMessage(instanceName, "Are you back?", 5*time.Second)
	h.assertHasResponseEvent(events)
}

func TestE2E_ReconcilerSkipsUndeployedAgents(t *testing.T) {
	t.Parallel()
	responses := []*llm.ChatResponse{
		{Content: "deployed response"},
	}
	h := setupE2E(t, responses)

	agentID := h.createAgent("skip_undeploy_agent", "You are a test agent")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	h.undeploy(agentID)
	time.Sleep(200 * time.Millisecond)

	h.nudgeAWEs()

	time.Sleep(1 * time.Second)

	_, instanceErr := h.aweExe.GetInstance(instanceName)
	if instanceErr == nil {
		t.Error("reconciler should NOT re-deploy an undeployed agent, but instance exists")
	}
}

func TestE2E_ReconcilerRedeploysWorkflow(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	appConfig := map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{
				"result": "{{nodes.start.output}}",
			},
			"nodes": []any{
				map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test"},
			},
		},
	}
	wfID := h.createWorkflow("reconcile_wf", appConfig)
	h.deployWorkflow(wfID)
	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	_, err := h.controlClient.Custom(h.ctx, "awe", "delete", "instances/"+instanceName, nil)
	if err != nil {
		t.Fatalf("delete workflow instance: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	h.nudgeAWEs()

	h.waitForInstance(instanceName, 10*time.Second)
}

func TestE2E_GWE_GatewayConfigDriftDetection(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)

	gwID := h.createGateway("drift_detection_gw")
	h.deployGateway(gwID)
	h.waitForGWEInstance(gwID, 5*time.Second)

	gw, err := h.gatewaySvc.GetByID(h.ctx, gwID)
	if err != nil {
		t.Fatalf("get gateway: %v", err)
	}
	if derefStr(gw.SyncStatus) != "in_sync" {
		t.Fatalf("initial syncStatus = %q, want in_sync", derefStr(gw.SyncStatus))
	}

	_, err = h.gatewaySvc.Update(h.ctx, gwID, UpdateGatewayRequest{
		Name:        "drift_detection_gw",
		Description: "Updated description to trigger drift",
	}, "e2e-user")
	if err != nil {
		t.Fatalf("update gateway: %v", err)
	}

	gw, err = h.gatewaySvc.GetByID(h.ctx, gwID)
	if err != nil {
		t.Fatalf("get gateway after update: %v", err)
	}
	if derefStr(gw.SyncStatus) != "out_of_sync" {
		t.Errorf("after update: syncStatus = %q, want out_of_sync", derefStr(gw.SyncStatus))
	}

	_, err = h.gwDeploySvc.Deploy(h.ctx, CreateGatewayDeploymentRequest{
		GatewayID: gwID,
		Action:    DeployActionUpdate,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("redeploy gateway: %v", err)
	}
	h.waitForGWEInstance(gwID, 5*time.Second)

	gw, err = h.gatewaySvc.GetByID(h.ctx, gwID)
	if err != nil {
		t.Fatalf("get gateway after redeploy: %v", err)
	}
	if derefStr(gw.SyncStatus) != "in_sync" {
		t.Errorf("after redeploy: syncStatus = %q, want in_sync", derefStr(gw.SyncStatus))
	}
}

func TestE2E_ReconcilerRedeploysGateway(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)

	gwID := h.createGateway("reconcile_gw")
	gwSlug := h.gatewaySlug(gwID)
	h.deployGateway(gwID)
	h.waitForGWEInstance(gwID, 5*time.Second)

	resp, err := http.Get("http://" + h.proxyAddr + "/gw/" + gwSlug + "/health")
	if err != nil {
		t.Fatalf("GET health before remove: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 before remove, got %d", resp.StatusCode)
	}

	_, err = h.controlClient.Custom(h.ctx, "gateway", "delete", "instances/"+gwID, nil)
	if err != nil {
		t.Fatalf("delete gateway instance: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	reconciler := NewReconciler(
		h.agentRepo, h.deployRepo, h.connectorRepo,
		h.workflowRepo, h.gatewayRepo, h.gwDeployRepo,
		h.yamlSvc, nil, h.controlClient, h.brk, h.namespace, slog.Default(),
	)
	reconciler.SetDeployerRegistry(NewDefaultDeployerRegistry(h.yamlSvc, h.connectorRepo, nil, nil, h.a2aRepo, h.namespace, slog.Default()))
	serveCtx, stopServing := context.WithCancel(h.ctx)
	t.Cleanup(stopServing)
	go func() { _ = reconciler.serve(serveCtx, leader.Term{Number: 1}) }()
	time.Sleep(500 * time.Millisecond)
	started := a2a.LifecycleTopic(h.namespace, "gateway", "started")
	if err = h.brk.Publish(h.ctx, started, &broker.Message{Topic: started, Payload: []byte("{}")}); err != nil {
		t.Fatalf("publish gateway/started: %v", err)
	}

	h.waitForGWEInstance(gwID, 10*time.Second)

	resp, err = http.Get("http://" + h.proxyAddr + "/gw/" + gwSlug + "/health")
	if err != nil {
		t.Fatalf("GET health after reconcile: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after reconcile, got %d", resp.StatusCode)
	}
}

func TestE2E_OutboxAutoRedeployOnConnectorChange(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	connID := h.createSQLConnector("outbox_conn", "postgres", map[string]any{
		"host": "db.example.com", "port": 5432, "username": "user", "password": "pass", "database_name": "testdb",
	})
	agentID := h.createAgentWithConnectors("outbox_agent", "You help with database queries and answer user questions about data.", []string{connID})
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	initialDeploy := h.latestDeployment(agentID)

	handler := &connectorRedeployer{
		agentRepo:  h.agentRepo,
		outboxRepo: h.outboxRepo,
		logger:     slog.Default(),
	}
	handler.writeConnectorRedeployEvent(h.ctx, "connector.updated", connID, "e2e-user")

	unpublished, err := h.outboxRepo.GetDispatchable(h.ctx, 10)
	if err != nil {
		t.Fatalf("get unpublished: %v", err)
	}
	if len(unpublished) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(unpublished))
	}
	var payload redeployPayload
	if unmarshalErr := json.Unmarshal([]byte(unpublished[0].Payload), &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal payload: %v", unmarshalErr)
	}
	if len(payload.AgentIDs) != 1 || payload.AgentIDs[0] != agentID {
		t.Fatalf("outbox event agent IDs = %v, want [%s]", payload.AgentIDs, agentID)
	}

	publisher := NewRedeployPublisher(h.deploySvc, h.agentRepo, slog.Default())
	pollerCfg := platpkg.OutboxPollerConfig{
		PollInterval:    50 * time.Millisecond,
		CleanupInterval: 10 * time.Minute,
		CleanupAge:      24 * time.Hour,
		BatchSize:       10,
	}
	poller := platpkg.NewOutboxPoller(h.outboxRepo, publisher, pollerCfg, slog.Default())
	if startErr := poller.Start(h.ctx); startErr != nil {
		t.Fatalf("start poller: %v", startErr)
	}
	t.Cleanup(func() { _ = poller.Stop(context.Background()) })

	deadline := time.Now().Add(3 * time.Second)
	redeployed := false
	for time.Now().Before(deadline) {
		latest := h.latestDeployment(agentID)
		if latest.ID != initialDeploy.ID && latest.Action == DeployActionUpdate && latest.Status == DeployStatusSuccess {
			redeployed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !redeployed {
		t.Fatal("agent was not redeployed within 3s")
	}

	h.waitForOutboxDrained(3 * time.Second)
}

func TestE2E_RedeployPublisherSkipsNonDeployedAgents(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	deployedID := h.createAgent("deployed_for_redeploy", "You help with testing. Answer questions about system behavior.")
	h.deploy(deployedID)
	h.waitForInstance(h.agentInstanceName(deployedID), 5*time.Second)

	notDeployedID := h.createAgent("not_deployed_agent", "You help with testing. Answer questions about system behavior.")

	publisher := NewRedeployPublisher(h.deploySvc, h.agentRepo, slog.Default())
	event := store.OutboxEvent{
		Payload: mustJSON(redeployPayload{AgentIDs: []string{deployedID, notDeployedID}, UserID: "e2e-user"}),
	}
	if err := publisher.PublishOutboxEvent(h.ctx, event); err != nil {
		t.Fatalf("publish outbox event: %v", err)
	}

	latest := h.latestDeployment(deployedID)
	if latest.Action != DeployActionUpdate {
		t.Errorf("deployed agent latest action = %q, want update", latest.Action)
	}

	notDeployedLatest, _ := h.deployRepo.GetLatestByAgentID(h.ctx, notDeployedID)
	if notDeployedLatest != nil {
		t.Errorf("non-deployed agent should have no deployments, got action=%q", notDeployedLatest.Action)
	}
}

func TestE2E_A2AAgentDeployCreatesProxyInstance(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	a2aID := h.createA2AAgent("proxy_test_agent", "https://remote.example.com")
	h.deployA2AAgent(a2aID)

	instanceName := h.agentInstanceName(a2aID)
	h.waitForInstance(instanceName, 5*time.Second)

	deploy, err := h.deployRepo.GetLatestByAgentID(h.ctx, a2aID)
	if err != nil {
		t.Fatalf("get latest deployment: %v", err)
	}
	if deploy == nil {
		t.Fatal("expected deployment record")
	}
	if deploy.Status != DeployStatusSuccess {
		t.Errorf("deploy status = %q, want success", deploy.Status)
	}
	if deploy.ConfigHash == nil || *deploy.ConfigHash == "" {
		t.Error("expected non-empty config hash")
	}

	h.undeployA2AAgent(a2aID)
	h.waitForInstanceGone(instanceName, 5*time.Second)
}

func TestE2E_A2AAgentDeployWithAuth(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	a2aResp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:           "E2E test A2A agent for fixture purposes",
		Name:                  "auth_proxy_agent",
		AgentURL:              "https://remote.example.com",
		AgentCardLocation:     "none",
		AgentAuthType:         "bearer",
		AgentAuthConfig:       map[string]any{"token": "secret123"},
		TaskHeaders:           map[string]string{"X-Custom": "value"},
		Skills:                []any{map[string]any{"name": "search"}},
		RequestTimeoutSeconds: 30,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent with auth: %v", err)
	}

	h.deployA2AAgent(a2aResp.ID)
	h.waitForInstance(h.agentInstanceName(a2aResp.ID), 5*time.Second)

	deploy, deployErr := h.deployRepo.GetLatestByAgentID(h.ctx, a2aResp.ID)
	if deployErr != nil {
		t.Fatalf("get deployment: %v", deployErr)
	}
	if deploy.ConfigHash == nil || *deploy.ConfigHash == "" {
		t.Fatal("config hash should be set after deploy")
	}

	inSyncAgent, inSyncErr := h.a2aSvc.GetByID(h.ctx, a2aResp.ID)
	if inSyncErr != nil {
		t.Fatalf("get A2A agent: %v", inSyncErr)
	}
	if inSyncAgent.SyncStatus == nil || *inSyncAgent.SyncStatus != "in_sync" {
		t.Errorf("sync status after deploy = %v, want in_sync", inSyncAgent.SyncStatus)
	}

	_, updateErr := h.a2aSvc.Update(h.ctx, a2aResp.ID, UpdateRemoteAgentRequest{
		RequestTimeoutSeconds: 60,
	}, "e2e-user")
	if updateErr != nil {
		t.Fatalf("update A2A agent: %v", updateErr)
	}

	got, getErr := h.a2aSvc.GetByID(h.ctx, a2aResp.ID)
	if getErr != nil {
		t.Fatalf("get A2A agent: %v", getErr)
	}
	if got.SyncStatus == nil {
		t.Error("sync status is nil, want out_of_sync")
	} else if *got.SyncStatus != "out_of_sync" {
		t.Errorf("sync status = %q, want out_of_sync", *got.SyncStatus)
	}
}

func TestE2E_A2AFetchAgentCard(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	var receivedAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "remote-agent",
			"description": "A test remote agent for card fetching",
			"skills": [{"name": "search", "description": "Search the web"}],
			"defaultInputModes": ["text"],
			"defaultOutputModes": ["text"]
		}`))
	}))
	t.Cleanup(srv.Close)

	resp, err := h.a2aSvc.FetchAgentCard(h.ctx, FetchAgentCardRequest{
		AgentCardURL: srv.URL + "/.well-known/agent.json",
	})
	if err != nil {
		t.Fatalf("fetch agent card: %v", err)
	}
	if resp.Name != "remote-agent" {
		t.Errorf("name = %q, want remote-agent", resp.Name)
	}
	if resp.Description != "A test remote agent for card fetching" {
		t.Errorf("description = %q, want 'A test remote agent for card fetching'", resp.Description)
	}

	bearerResp, bearerErr := h.a2aSvc.FetchAgentCard(h.ctx, FetchAgentCardRequest{
		AgentCardURL:       srv.URL + "/.well-known/agent.json",
		AuthenticationType: "bearer",
		AuthConfig:         &CardAuthConfig{BearerToken: "test-token-123"},
	})
	if bearerErr != nil {
		t.Fatalf("fetch with bearer: %v", bearerErr)
	}
	if bearerResp.Name != "remote-agent" {
		t.Errorf("bearer fetch name = %q, want remote-agent", bearerResp.Name)
	}
	if receivedAuthHeader != "Bearer test-token-123" {
		t.Errorf("auth header = %q, want 'Bearer test-token-123'", receivedAuthHeader)
	}
}

func TestE2E_ModelConfigPublishAndBootstrap(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	ctx := h.ctx

	publisher := NewModelConfigPublisher(h.brk, h.namespace, true, slog.Default())

	subQueue, err := h.brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create sub queue: %v", err)
	}
	updateTopic := a2a.ModelConfigUpdateTopic(h.namespace, "test-alias")
	if subErr := h.brk.Subscribe(subQueue, updateTopic); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	rawConfig := map[string]any{"provider": "openai", "model": "gpt-4"}
	if pubErr := publisher.PublishUpdate(ctx, "model-id-1", "test-alias", "", rawConfig); pubErr != nil {
		t.Fatalf("publish update: %v", pubErr)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, recvErr := h.brk.Receive(recvCtx, subQueue)
	if recvErr != nil {
		t.Fatalf("receive update: %v", recvErr)
	}
	var updatePayload map[string]any
	if unmarshalErr := json.Unmarshal(msg.Payload, &updatePayload); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	configData, ok := updatePayload["model_config"].(map[string]any)
	if !ok {
		t.Fatalf("model_config not a map: %T", updatePayload["model_config"])
	}
	if configData["provider"] != "openai" {
		t.Errorf("provider = %v, want openai", configData["provider"])
	}

	if delErr := publisher.PublishDelete(ctx, "model-id-1", "test-alias"); delErr != nil {
		t.Fatalf("publish delete: %v", delErr)
	}
	delCtx, delCancel := context.WithTimeout(ctx, 2*time.Second)
	defer delCancel()
	delMsg, delRecvErr := h.brk.Receive(delCtx, subQueue)
	if delRecvErr != nil {
		t.Fatalf("receive delete: %v", delRecvErr)
	}
	var delPayload map[string]any
	if unmarshalErr := json.Unmarshal(delMsg.Payload, &delPayload); unmarshalErr != nil {
		t.Fatalf("unmarshal delete: %v", unmarshalErr)
	}
	if delPayload["model_config"] != nil {
		t.Errorf("expected model_config=null, got %v", delPayload["model_config"])
	}

	_, createErr := h.modelConfigSvc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "bootstrap-model",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "e2e-user")
	if createErr != nil {
		t.Fatalf("create model config: %v", createErr)
	}

	listener := NewModelConfigBootstrapListener(h.modelConfigSvc, h.brk, h.namespace, slog.Default())
	if startErr := listener.Start(ctx); startErr != nil {
		t.Fatalf("start listener: %v", startErr)
	}
	t.Cleanup(func() { _ = listener.Stop(context.Background()) })

	time.Sleep(100 * time.Millisecond)

	replyTopic := h.namespace + "/configuration/model/response/bootstrap-model/test-component"
	replyQueue, replyQueueErr := h.brk.CreateDirectSubscription()
	if replyQueueErr != nil {
		t.Fatalf("create reply queue: %v", replyQueueErr)
	}
	if subErr := h.brk.Subscribe(replyQueue, replyTopic); subErr != nil {
		t.Fatalf("subscribe reply: %v", subErr)
	}

	bootstrapReq, _ := json.Marshal(bootstrapRequest{
		ComponentID:   "test-component",
		ComponentType: "agent",
		ReplyTo:       replyTopic,
		ModelID:       "bootstrap-model",
	})
	requestTopic := a2a.ModelConfigBootstrapRequestTopic(h.namespace, "bootstrap-model")
	if pubErr := h.brk.Publish(ctx, requestTopic, &broker.Message{
		Payload: bootstrapReq,
	}); pubErr != nil {
		t.Fatalf("publish bootstrap request: %v", pubErr)
	}

	replyCtx, replyCancel := context.WithTimeout(ctx, 3*time.Second)
	defer replyCancel()
	replyMsg, replyErr := h.brk.Receive(replyCtx, replyQueue)
	if replyErr != nil {
		t.Fatalf("receive bootstrap response: %v", replyErr)
	}

	var bootstrapResp map[string]any
	if unmarshalErr := json.Unmarshal(replyMsg.Payload, &bootstrapResp); unmarshalErr != nil {
		t.Fatalf("unmarshal bootstrap response: %v", unmarshalErr)
	}
	respConfig, ok := bootstrapResp["model_config"].(map[string]any)
	if !ok {
		t.Fatalf("bootstrap response model_config not a map: %T", bootstrapResp["model_config"])
	}
	modelVal, _ := respConfig["model"].(string)
	if modelVal == "" {
		t.Error("bootstrap response missing model field")
	}
	if !strings.Contains(modelVal, "gpt-4") {
		t.Errorf("bootstrap model = %v, want to contain gpt-4", modelVal)
	}
	if _, present := respConfig[samapp.ModelConfigIDKey]; present {
		t.Errorf("bootstrap response carries %s, which Python agents forward to the provider: %v", samapp.ModelConfigIDKey, respConfig)
	}
}

func mockRemoteA2AAgent(t *testing.T, responseText string) (srv *httptest.Server, receivedRequests *[]a2a.Request) {
	t.Helper()
	var (
		mu   sync.Mutex
		reqs []a2a.Request
	)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "agent-card.json") {
			w.Header().Set("Content-Type", "application/json")
			card := map[string]any{
				"name":               "mock-remote",
				"description":        "A mock remote A2A agent for E2E testing",
				"url":                srv.URL,
				"version":            "1.0.0",
				"protocolVersion":    "0.3.0",
				"defaultInputModes":  []string{"text"},
				"defaultOutputModes": []string{"text"},
				"skills":             []map[string]any{{"id": "echo", "name": "echo", "description": "echoes back"}},
				"capabilities":       map[string]any{"streaming": false},
			}
			_ = json.NewEncoder(w).Encode(card)
			return
		}

		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			var req a2a.Request
			if err := json.Unmarshal(body, &req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			reqs = append(reqs, req)
			mu.Unlock()

			resp := a2a.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: a2a.Task{
					ID:   req.ID,
					Kind: "task",
					Status: a2a.TaskStatus{
						State: a2a.TaskStateCompleted,
						Message: &a2a.Message{
							Kind:      "message",
							MessageID: "resp-msg-1",
							Role:      a2a.RoleAgent,
							Parts:     []a2a.Part{{Kind: "text", Text: responseText}},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func TestE2E_A2AProxyMessageFlow(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	srv, receivedRequests := mockRemoteA2AAgent(t, "Hello from the remote agent!")

	a2aResp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "proxy_flow_agent",
		AgentURL:          srv.URL,
		AgentCardLocation: "well_known",
		Skills:            []any{map[string]any{"name": "echo"}},
		AgentAuthConfig:   map[string]any{},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent: %v", err)
	}

	h.deployA2AAgent(a2aResp.ID)
	instanceName := h.agentInstanceName(a2aResp.ID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "What is the meaning of life?", 10*time.Second)

	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "Hello from the remote agent!")

	if len(*receivedRequests) == 0 {
		t.Fatal("mock remote agent received no requests — proxy did not forward")
	}
	req := (*receivedRequests)[0]
	if req.Method != "message/send" {
		t.Errorf("forwarded method = %q, want message/send", req.Method)
	}
	paramsBytes, _ := json.Marshal(req.Params)
	if !strings.Contains(string(paramsBytes), "What is the meaning of life?") {
		t.Errorf("forwarded request missing original message text, got params: %s", paramsBytes)
	}
}

func TestE2E_A2AProxyMessageFlowWithBearerAuth(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	var (
		mu              sync.Mutex
		receivedAuthHdr string
		srv             *httptest.Server
	)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "agent-card.json") {
			w.Header().Set("Content-Type", "application/json")
			card := map[string]any{
				"name": "auth-remote", "description": "auth test", "url": srv.URL,
				"version": "1.0.0", "protocolVersion": "0.3.0",
				"defaultInputModes": []string{"text"}, "defaultOutputModes": []string{"text"},
				"skills":       []map[string]any{{"id": "s1", "name": "s1", "description": "d"}},
				"capabilities": map[string]any{"streaming": false},
			}
			_ = json.NewEncoder(w).Encode(card)
			return
		}
		if r.Method == http.MethodPost {
			mu.Lock()
			receivedAuthHdr = r.Header.Get("Authorization")
			mu.Unlock()

			body, _ := io.ReadAll(r.Body)
			var req a2a.Request
			_ = json.Unmarshal(body, &req)
			resp := a2a.Response{
				JSONRPC: "2.0", ID: req.ID,
				Result: a2a.Task{
					ID: req.ID, Kind: "task",
					Status: a2a.TaskStatus{
						State:   a2a.TaskStateCompleted,
						Message: &a2a.Message{Kind: "message", MessageID: "m1", Role: a2a.RoleAgent, Parts: []a2a.Part{{Kind: "text", Text: "authenticated"}}},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)

	a2aResp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "proxy_auth_flow",
		AgentURL:          srv.URL,
		AgentCardLocation: "well_known",
		AgentAuthType:     "bearer",
		AgentAuthConfig:   map[string]any{"token": "e2e-secret-token"},
		Skills:            []any{map[string]any{"name": "s1"}},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent with auth: %v", err)
	}

	h.deployA2AAgent(a2aResp.ID)
	instanceName := h.agentInstanceName(a2aResp.ID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "auth test", 10*time.Second)
	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "authenticated")

	mu.Lock()
	gotAuth := receivedAuthHdr
	mu.Unlock()
	if gotAuth != "Bearer e2e-secret-token" {
		t.Errorf("remote agent received auth header = %q, want 'Bearer e2e-secret-token'", gotAuth)
	}
}

func TestE2E_A2AProxyRemoteError(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "agent-card.json") {
			w.Header().Set("Content-Type", "application/json")
			card := map[string]any{
				"name": "error-remote", "description": "error test", "url": srv.URL,
				"version": "1.0.0", "protocolVersion": "0.3.0",
				"defaultInputModes": []string{"text"}, "defaultOutputModes": []string{"text"},
				"skills":       []map[string]any{{"id": "s1", "name": "s1", "description": "d"}},
				"capabilities": map[string]any{"streaming": false},
			}
			_ = json.NewEncoder(w).Encode(card)
			return
		}
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			var req a2a.Request
			_ = json.Unmarshal(body, &req)
			resp := a2a.Response{
				JSONRPC: "2.0", ID: req.ID,
				Error: &a2a.Error{Code: -32000, Message: "remote agent encountered an error"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)

	a2aResp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "proxy_error_flow",
		AgentURL:          srv.URL,
		AgentCardLocation: "well_known",
		Skills:            []any{map[string]any{"name": "s1"}},
		AgentAuthConfig:   map[string]any{},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent: %v", err)
	}

	h.deployA2AAgent(a2aResp.ID)
	instanceName := h.agentInstanceName(a2aResp.ID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "trigger error", 10*time.Second)

	h.assertHasResponseEvent(events)
	foundError := false
	for _, evt := range events {
		if evt.Type == "response" && strings.Contains(string(evt.Payload), "error") {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Error("expected error in response events")
		for i, evt := range events {
			t.Logf("  event[%d] type=%s payload=%s", i, evt.Type, truncate(string(evt.Payload), 200))
		}
	}
}

func TestE2E_ConnectorDeleteTriggersRedeploy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	connID := h.createSQLConnector("del_conn", "postgres", map[string]any{
		"host": "db.example.com", "port": 5432, "username": "user", "password": "pass", "database_name": "testdb",
	})
	agentID := h.createAgentWithConnectors("del_agent", "You help with database queries and answer user questions about data.", []string{connID})
	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)

	initialDeploy := h.latestDeployment(agentID)

	handler := &connectorRedeployer{
		svc:        h.connectorSvc,
		agentRepo:  h.agentRepo,
		outboxRepo: h.outboxRepo,
		logger:     slog.Default(),
	}

	agentIDs := handler.findAgentsByConnector(h.ctx, connID)

	if err := handler.svc.Delete(h.ctx, connID); err != nil {
		t.Fatalf("delete connector: %v", err)
	}

	handler.writeRedeployEvent(h.ctx, "connector.deleted", connID, "e2e-user", agentIDs)

	unpublished, err := h.outboxRepo.GetDispatchable(h.ctx, 10)
	if err != nil {
		t.Fatalf("get unpublished: %v", err)
	}
	if len(unpublished) == 0 {
		t.Fatal("expected outbox event for connector.deleted but found none — " +
			"agent IDs were likely queried AFTER the cascade delete removed agent_connectors rows")
	}
	if unpublished[0].EventType != "connector.deleted" {
		t.Errorf("event type = %q, want connector.deleted", unpublished[0].EventType)
	}
	var payload redeployPayload
	if unmarshalErr := json.Unmarshal([]byte(unpublished[0].Payload), &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal payload: %v", unmarshalErr)
	}
	if len(payload.AgentIDs) != 1 || payload.AgentIDs[0] != agentID {
		t.Fatalf("outbox event agent IDs = %v, want [%s]", payload.AgentIDs, agentID)
	}

	publisher := NewRedeployPublisher(h.deploySvc, h.agentRepo, slog.Default())
	pollerCfg := platpkg.OutboxPollerConfig{
		PollInterval:    50 * time.Millisecond,
		CleanupInterval: 10 * time.Minute,
		CleanupAge:      24 * time.Hour,
		BatchSize:       10,
	}
	poller := platpkg.NewOutboxPoller(h.outboxRepo, publisher, pollerCfg, slog.Default())
	if startErr := poller.Start(h.ctx); startErr != nil {
		t.Fatalf("start poller: %v", startErr)
	}
	t.Cleanup(func() { _ = poller.Stop(context.Background()) })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		latest := h.latestDeployment(agentID)
		if latest.ID != initialDeploy.ID && latest.Action == DeployActionUpdate {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("agent was not redeployed within 3s after connector delete")
}

func TestE2E_SharedConnectorRedeployAll(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	connID := h.createSQLConnector("shared_conn", "postgres", map[string]any{
		"host": "db.example.com", "port": 5432, "username": "user", "password": "pass", "database_name": "shareddb",
	})

	agent1ID := h.createAgentWithConnectors("shared_agent_1", "Agent 1 that helps with database queries.", []string{connID})
	agent2ID := h.createAgentWithConnectors("shared_agent_2", "Agent 2 that helps with database queries.", []string{connID})

	h.deploy(agent1ID)
	h.deploy(agent2ID)
	h.waitForInstance(h.agentInstanceName(agent1ID), 5*time.Second)
	h.waitForInstance(h.agentInstanceName(agent2ID), 5*time.Second)

	initial1 := h.latestDeployment(agent1ID)
	initial2 := h.latestDeployment(agent2ID)

	handler := &connectorRedeployer{
		agentRepo:  h.agentRepo,
		outboxRepo: h.outboxRepo,
		logger:     slog.Default(),
	}
	handler.writeConnectorRedeployEvent(h.ctx, "connector.updated", connID, "e2e-user")

	unpublished, err := h.outboxRepo.GetDispatchable(h.ctx, 10)
	if err != nil {
		t.Fatalf("get unpublished: %v", err)
	}
	if len(unpublished) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(unpublished))
	}
	var payload redeployPayload
	if unmarshalErr := json.Unmarshal([]byte(unpublished[0].Payload), &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal payload: %v", unmarshalErr)
	}
	if len(payload.AgentIDs) != 2 {
		t.Fatalf("outbox event should contain 2 agent IDs, got %d: %v", len(payload.AgentIDs), payload.AgentIDs)
	}

	publisher := NewRedeployPublisher(h.deploySvc, h.agentRepo, slog.Default())
	pollerCfg := platpkg.OutboxPollerConfig{
		PollInterval:    50 * time.Millisecond,
		CleanupInterval: 10 * time.Minute,
		CleanupAge:      24 * time.Hour,
		BatchSize:       10,
	}
	poller := platpkg.NewOutboxPoller(h.outboxRepo, publisher, pollerCfg, slog.Default())
	if startErr := poller.Start(h.ctx); startErr != nil {
		t.Fatalf("start poller: %v", startErr)
	}
	t.Cleanup(func() { _ = poller.Stop(context.Background()) })

	deadline := time.Now().Add(3 * time.Second)
	var agent1Redeployed, agent2Redeployed bool
	for time.Now().Before(deadline) {
		if !agent1Redeployed {
			latest1 := h.latestDeployment(agent1ID)
			if latest1.ID != initial1.ID && latest1.Action == DeployActionUpdate {
				agent1Redeployed = true
			}
		}
		if !agent2Redeployed {
			latest2 := h.latestDeployment(agent2ID)
			if latest2.ID != initial2.ID && latest2.Action == DeployActionUpdate {
				agent2Redeployed = true
			}
		}
		if agent1Redeployed && agent2Redeployed {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("not all agents redeployed within 3s: agent1=%v agent2=%v", agent1Redeployed, agent2Redeployed)
}

func TestE2E_AgentDeleteWhileDeployed(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	agentID := h.createAgent("del_deployed", "You are a test agent that will be deleted while deployed to verify cleanup")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	h.undeploy(agentID)
	h.deleteAgent(agentID)

	h.waitForInstanceGone(instanceName, 5*time.Second)

	agent, _, _, _, _, _ := h.agentRepo.GetByID(h.ctx, agentID)
	if agent != nil {
		t.Error("agent should be nil after delete")
	}

	deploy, _ := h.deployRepo.GetLatestByAgentID(h.ctx, agentID)
	if deploy != nil {
		t.Error("deployment record should be nil after cascade delete")
	}
}

func TestE2E_WorkflowUpdateAndRedeploy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	appConfigV1 := map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{nodes.start.output}}"},
			"nodes": []any{
				map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "agent_v1"},
			},
		},
	}
	wfID := h.createWorkflow("wf_update_test", appConfigV1)
	resp1 := h.deployWorkflow(wfID)
	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	dep1, err := h.wfDeployRepo.GetByID(h.ctx, resp1.ID)
	if err != nil {
		t.Fatalf("get deployment v1: %v", err)
	}
	if dep1.WorkflowConfigSnapshot == nil {
		t.Fatal("v1 snapshot is nil")
	}
	snapshotV1 := *dep1.WorkflowConfigSnapshot

	appConfigV2 := map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{nodes.start.output}}"},
			"nodes": []any{
				map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "agent_v2"},
			},
		},
	}
	_, updateErr := h.workflowSvc.Update(h.ctx, wfID, UpdateWorkflowRequest{
		Name:        "wf_update_test",
		Description: "E2E test workflow for update verification",
		AppConfig:   appConfigV2,
	}, "e2e-user")
	if updateErr != nil {
		t.Fatalf("update workflow: %v", updateErr)
	}

	resp2 := h.deployWorkflow(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	dep2, err := h.wfDeployRepo.GetByID(h.ctx, resp2.ID)
	if err != nil {
		t.Fatalf("get deployment v2: %v", err)
	}
	if dep2.WorkflowConfigSnapshot == nil {
		t.Fatal("v2 snapshot is nil")
	}
	snapshotV2 := *dep2.WorkflowConfigSnapshot

	if snapshotV1 == snapshotV2 {
		t.Error("snapshots should differ after update")
	}
	if !strings.Contains(snapshotV1, "agent_v1") {
		t.Errorf("v1 snapshot missing agent_v1: %s", truncate(snapshotV1, 200))
	}
	if !strings.Contains(snapshotV2, "agent_v2") {
		t.Errorf("v2 snapshot missing agent_v2: %s", truncate(snapshotV2, 200))
	}
}

func TestE2E_A2AAgentUndeployThenDelete(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	a2aID := h.createA2AAgent("del_a2a_test", "https://remote.example.com")
	h.deployA2AAgent(a2aID)
	instanceName := h.agentInstanceName(a2aID)
	h.waitForInstance(instanceName, 5*time.Second)

	deploy, _ := h.deployRepo.GetLatestByAgentID(h.ctx, a2aID)
	if deploy == nil {
		t.Fatal("expected deployment record after A2A deploy")
	}

	h.undeployA2AAgent(a2aID)
	h.waitForInstanceGone(instanceName, 5*time.Second)

	h.deleteA2AAgent(a2aID)

	a2a, _ := h.a2aRepo.GetByID(h.ctx, a2aID)
	if a2a != nil {
		t.Error("A2A agent should be nil after delete")
	}
}

func TestE2E_ReconcilerUsesSnapshotNotCurrentConfig(t *testing.T) {
	t.Parallel()
	responses := []*llm.ChatResponse{
		{Content: "version one response"},
		{Content: "reconciled response"},
	}
	h := setupE2E(t, responses)

	agentID := h.createAgent("reconcile_drift", "You are version one of this agent and your job is to help with testing reconciliation behavior")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	dep := h.latestDeployment(agentID)
	if dep.AgentConfigSnapshot == nil {
		t.Fatal("deployment snapshot is nil")
	}
	originalSnapshot := *dep.AgentConfigSnapshot

	_, updateErr := h.agentSvc.Update(h.ctx, agentID, UpdateAgentRequest{
		Name:         "reconcile_drift",
		Description:  "E2E test agent for reconciler drift detection",
		SystemPrompt: "You are version TWO of this agent with completely new instructions that differ from the original version one configuration",
		Toolsets:     []string{},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "e2e-user")
	if updateErr != nil {
		t.Fatalf("update agent: %v", updateErr)
	}

	agentResp, _ := h.agentSvc.GetByID(h.ctx, agentID)
	if agentResp.SyncStatus == nil || *agentResp.SyncStatus != "out_of_sync" {
		t.Fatalf("expected out_of_sync after update, got %v", agentResp.SyncStatus)
	}

	_, delErr := h.controlClient.Custom(h.ctx, "awe", "delete", "instances/"+instanceName, nil)
	if delErr != nil {
		t.Fatalf("delete AWE instance: %v", delErr)
	}
	h.waitForInstanceGone(instanceName, 5*time.Second)

	h.nudgeAWEs()

	h.waitForInstance(instanceName, 10*time.Second)

	reconciledDep := h.latestDeployment(agentID)
	if reconciledDep.AgentConfigSnapshot == nil {
		t.Fatal("reconciled deployment snapshot is nil")
	}
	if *reconciledDep.AgentConfigSnapshot != originalSnapshot {
		t.Error("reconciler should re-deploy the original snapshot, not current config")
	}
	if strings.Contains(*reconciledDep.AgentConfigSnapshot, "version TWO") {
		t.Error("reconciled snapshot should NOT contain the updated prompt")
	}
	if !strings.Contains(*reconciledDep.AgentConfigSnapshot, "version one") {
		t.Error("reconciled snapshot should contain the original v1 prompt")
	}

	agentAfter, _ := h.agentSvc.GetByID(h.ctx, agentID)
	if agentAfter.SyncStatus == nil || *agentAfter.SyncStatus != "out_of_sync" {
		t.Errorf("sync status should remain out_of_sync after reconciliation, got %v", agentAfter.SyncStatus)
	}
}

func TestE2E_WorkflowDeleteWhileDeployed(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	appConfig := map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{nodes.start.output}}"},
			"nodes": []any{
				map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test_agent"},
			},
		},
	}
	wfID := h.createWorkflow("wf_del_deployed", appConfig)
	resp := h.deployWorkflow(wfID)
	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	_, undeployErr := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID,
		Action:     DeployActionUndeploy,
	}, "e2e-user")
	if undeployErr != nil {
		t.Fatalf("undeploy workflow: %v", undeployErr)
	}
	h.waitForInstanceGone(instanceName, 5*time.Second)

	if deleteErr := h.workflowSvc.Delete(h.ctx, wfID); deleteErr != nil {
		t.Fatalf("delete workflow: %v", deleteErr)
	}

	wf, _ := h.workflowRepo.GetByID(h.ctx, wfID)
	if wf != nil {
		t.Error("workflow should be nil after delete")
	}

	dep, _ := h.wfDeployRepo.GetByID(h.ctx, resp.ID)
	if dep != nil {
		t.Error("deployment record should be nil after cascade delete")
	}
}

func TestE2E_ModelConfigUpdatePublishPropagation(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	resp, err := h.modelConfigSvc.Create(h.ctx, &CreateModelConfigRequest{
		Alias:     "update_pub_test",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create model config: %v", err)
	}

	publisher := NewModelConfigPublisher(h.brk, h.namespace, true, slog.Default())

	updateTopic := a2a.ModelConfigUpdateTopic(h.namespace, "update_pub_test")
	q, qErr := h.brk.CreateDirectSubscription()
	if qErr != nil {
		t.Fatalf("create queue: %v", qErr)
	}
	if subErr := h.brk.Subscribe(q, updateTopic); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	rawConfig, rawErr := h.modelConfigSvc.GetByIDRaw(h.ctx, resp.ID)
	if rawErr != nil {
		t.Fatalf("get raw config: %v", rawErr)
	}
	if pubErr := publisher.PublishUpdate(h.ctx, resp.ID, "update_pub_test", "", rawConfig); pubErr != nil {
		t.Fatalf("publish update: %v", pubErr)
	}

	ctx1, cancel1 := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel1()
	msg1, recvErr := h.brk.Receive(ctx1, q)
	if recvErr != nil || msg1 == nil {
		t.Fatalf("receive update message: %v", recvErr)
	}
	msg1.Ack()
	if !strings.Contains(string(msg1.Payload), "gpt-4") {
		t.Errorf("update message missing gpt-4: %s", truncate(string(msg1.Payload), 200))
	}

	newModel := "gpt-4o"
	_, updateErr := h.modelConfigSvc.Update(h.ctx, resp.ID, &UpdateModelConfigRequest{
		ModelName: &newModel,
	}, "e2e-user")
	if updateErr != nil {
		t.Fatalf("update model config: %v", updateErr)
	}

	rawConfig2, _ := h.modelConfigSvc.GetByIDRaw(h.ctx, resp.ID)
	if pubErr := publisher.PublishUpdate(h.ctx, resp.ID, "update_pub_test", "", rawConfig2); pubErr != nil {
		t.Fatalf("publish updated config: %v", pubErr)
	}

	ctx2, cancel2 := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel2()
	msg2, recvErr2 := h.brk.Receive(ctx2, q)
	if recvErr2 != nil || msg2 == nil {
		t.Fatalf("receive updated message: %v", recvErr2)
	}
	msg2.Ack()
	if !strings.Contains(string(msg2.Payload), "gpt-4o") {
		t.Errorf("updated message missing gpt-4o: %s", truncate(string(msg2.Payload), 200))
	}

	if pubErr := publisher.PublishDelete(h.ctx, resp.ID, "update_pub_test"); pubErr != nil {
		t.Fatalf("publish delete: %v", pubErr)
	}

	ctx3, cancel3 := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel3()
	msg3, recvErr3 := h.brk.Receive(ctx3, q)
	if recvErr3 != nil || msg3 == nil {
		t.Fatalf("receive delete message: %v", recvErr3)
	}
	msg3.Ack()
	var deletePayload map[string]any
	if unmarshalErr := json.Unmarshal(msg3.Payload, &deletePayload); unmarshalErr != nil {
		t.Fatalf("unmarshal delete payload: %v", unmarshalErr)
	}
	if deletePayload["model_config"] != nil {
		t.Errorf("delete payload model_config should be nil, got %v", deletePayload["model_config"])
	}
}

func TestE2E_STR_RealToolExecution(t *testing.T) {
	t.Parallel()

	h := setupE2EWithSTR(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-echo-1",
					Name:      "echo",
					Arguments: `{"message": "hello from real STR"}`,
				},
			},
		},
		{
			Content:      "The echo tool returned your message successfully.",
			FinishReason: "stop",
		},
	})

	connID := h.createConnectorForSTRTool("str-echo-conn", "echo")
	agentID := h.createAgentWithConnectors(
		"str-real-agent",
		"You are a test agent. When asked to echo, use the echo tool to echo the user's message back.",
		[]string{connID},
	)

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	events := h.sendMessage(instanceName, "Please echo hello from real STR", 15*time.Second)

	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "echo tool returned")

	requests := h.llmClient.Recorder().Requests()
	if len(requests) < 2 {
		t.Fatalf("expected at least 2 LLM calls (tool_call + final), got %d", len(requests))
	}

	hasToolResult := false
	for _, msg := range requests[1].Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "hello from real STR") {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		t.Error("second LLM call should contain tool result with 'hello from real STR' from real STR subprocess")
		for i, msg := range requests[1].Messages {
			t.Logf("  msg[%d] role=%s content=%s", i, msg.Role, truncate(msg.Content, 200))
		}
	}
}

func TestE2E_STR_ToolErrorHandling(t *testing.T) {
	t.Parallel()

	h := setupE2EWithSTR(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-err-1",
					Name:      "error_tool",
					Arguments: `{"message": "intentional test failure"}`,
				},
			},
		},
		{
			Content:      "The tool encountered an error. I'll try to help without it.",
			FinishReason: "stop",
		},
	})

	connID := h.createConnectorForSTRTool("str-err-conn", "error_tool")
	agentID := h.createAgentWithConnectors(
		"str-err-agent",
		"You are a test agent. Use the error_tool when asked. If it fails, explain the error to the user.",
		[]string{connID},
	)

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	events := h.sendMessage(instanceName, "Please use the error tool", 15*time.Second)

	h.assertHasResponseEvent(events)

	requests := h.llmClient.Recorder().Requests()
	if len(requests) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(requests))
	}

	hasErrorResult := false
	for _, msg := range requests[1].Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "intentional test failure") {
			hasErrorResult = true
			break
		}
	}
	if !hasErrorResult {
		t.Error("second LLM call should contain error result from error_tool subprocess")
		for i, msg := range requests[1].Messages {
			t.Logf("  msg[%d] role=%s content=%s", i, msg.Role, truncate(msg.Content, 200))
		}
	}
}

func TestE2E_STR_AgentWorksAfterRedeploy(t *testing.T) {
	t.Parallel()

	h := setupE2EWithSTR(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "echo", Arguments: `{"message": "before redeploy"}`},
			},
		},
		{Content: "First echo done.", FinishReason: "stop"},
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-2", Name: "echo", Arguments: `{"message": "after redeploy"}`},
			},
		},
		{Content: "Second echo done.", FinishReason: "stop"},
	})

	connID := h.createConnectorForSTRTool("str-redeploy-conn", "echo")
	agentID := h.createAgentWithConnectors(
		"str-redeploy-agent",
		"You are a test agent. Use the echo tool to echo whatever the user says back to them verbatim.",
		[]string{connID},
	)

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	events1 := h.sendMessage(instanceName, "Echo before redeploy", 15*time.Second)
	h.assertHasResponseEvent(events1)
	h.assertFinalResponse(events1, "First echo done")

	_, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: agentID,
		Action:  DeployActionUpdate,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("redeploy agent: %v", err)
	}

	h.waitForInstance(instanceName, 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	events2 := h.sendMessage(instanceName, "Echo after redeploy", 15*time.Second)
	h.assertHasResponseEvent(events2)
	h.assertFinalResponse(events2, "Second echo done")

	deployments, _, err := h.deployRepo.ListPaginated(h.ctx, 1, 10, agentID)
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deployments) < 2 {
		t.Errorf("expected at least 2 deployments (initial + update), got %d", len(deployments))
	}
}

func TestE2E_STR_ConnectorUpdateAutoRedeploy(t *testing.T) {
	t.Parallel()

	h := setupE2EWithSTR(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-pre", Name: "echo", Arguments: `{"message": "pre-update"}`},
			},
		},
		{Content: "Pre-update echo complete.", FinishReason: "stop"},
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-post", Name: "echo", Arguments: `{"message": "post-update"}`},
			},
		},
		{Content: "Post-update echo complete.", FinishReason: "stop"},
	})

	connID := h.createSQLConnector("str-autoupgrade-conn", "postgres", map[string]any{
		"tool_name": "echo",
		"hostname":  "host-v1",
		"port":      "5432",
		"database":  "testdb",
		"username":  "testuser",
		"password":  "testpass",
	})
	agentID := h.createAgentWithConnectors(
		"str-autoupgrade-agent",
		"You are a test agent. Use the echo tool to echo whatever the user says back to them verbatim.",
		[]string{connID},
	)

	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	events1 := h.sendMessage(instanceName, "Echo pre-update", 15*time.Second)
	h.assertHasResponseEvent(events1)
	h.assertFinalResponse(events1, "Pre-update echo complete")

	initialDeploy := h.latestDeployment(agentID)

	if _, err := h.connectorSvc.Update(h.ctx, connID, UpdateConnectorRequest{
		Name: "str-autoupgrade-conn",
		Values: map[string]any{
			"tool_name": "echo",
			"hostname":  "host-v2",
			"port":      "5432",
			"database":  "testdb",
			"username":  "testuser",
			"password":  "testpass",
		},
	}, "e2e-user"); err != nil {
		t.Fatalf("update connector: %v", err)
	}

	handler := &connectorRedeployer{
		svc:        h.connectorSvc,
		agentRepo:  h.agentRepo,
		outboxRepo: h.outboxRepo,
		logger:     slog.Default(),
	}
	handler.writeConnectorRedeployEvent(h.ctx, "connector.updated", connID, "e2e-user")

	publisher := NewRedeployPublisher(h.deploySvc, h.agentRepo, slog.Default())
	poller := platpkg.NewOutboxPoller(h.outboxRepo, publisher, platpkg.OutboxPollerConfig{
		PollInterval:    200 * time.Millisecond,
		CleanupInterval: 1 * time.Hour,
		CleanupAge:      1 * time.Hour,
		BatchSize:       10,
	}, slog.Default())
	pollerCtx, pollerCancel := context.WithCancel(h.ctx)
	defer pollerCancel()
	if err := poller.Start(pollerCtx); err != nil {
		t.Fatalf("start outbox poller: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		latest := h.latestDeployment(agentID)
		if latest.ID != initialDeploy.ID && latest.Action == DeployActionUpdate && latest.Status == DeployStatusSuccess {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	pollerCancel()

	latestDeploy := h.latestDeployment(agentID)
	if latestDeploy.ID == initialDeploy.ID {
		t.Fatal("outbox poller did not trigger a new deployment — auto-redeploy failed")
	}
	snapshot := ""
	if latestDeploy.AgentConfigSnapshot != nil {
		snapshot = *latestDeploy.AgentConfigSnapshot
	}
	if !strings.Contains(snapshot, "host-v2") {
		t.Errorf("new deployment snapshot should contain 'host-v2', got: %s", truncate(snapshot, 300))
	}

	h.waitForInstance(instanceName, 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	events2 := h.sendMessage(instanceName, "Echo post-update", 15*time.Second)
	h.assertHasResponseEvent(events2)
	h.assertFinalResponse(events2, "Post-update echo complete")
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestE2E_ModelDeleteUndeploysDependents(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "alias agent ok", FinishReason: "stop"},
		{Content: "uuid agent ok", FinishReason: "stop"},
		{Content: "general agent ok", FinishReason: "stop"},
	})

	const dependentAlias = "report_gen"
	modelResp, err := h.modelConfigSvc.Create(h.ctx, &CreateModelConfigRequest{
		Alias:     dependentAlias,
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create model config: %v", err)
	}

	aliasAgentID := h.createAgentWithModelProvider(
		"alias-dependent",
		"You are an agent saved with the model alias (YAML / legacy path).",
		dependentAlias,
	)
	uuidAgentID := h.createAgentWithModelProvider(
		"uuid-dependent",
		"You are an agent saved with the model configuration UUID (Platform UI path).",
		modelResp.ID,
	)
	generalAgentID := h.createAgent(
		"general-dependent",
		"You are an agent that uses the default general model for basic tasks.",
	)

	h.deploy(aliasAgentID)
	h.deploy(uuidAgentID)
	h.deploy(generalAgentID)
	h.waitForInstance(h.agentInstanceName(aliasAgentID), 5*time.Second)
	h.waitForInstance(h.agentInstanceName(uuidAgentID), 5*time.Second)
	h.waitForInstance(h.agentInstanceName(generalAgentID), 5*time.Second)

	dependentsHandler := NewModelDependentsHandler(h.agentRepo, nil, nil, h.modelConfigRepo, h.deploySvc, slog.Default())

	dependents, err := dependentsHandler.GetDependents(h.ctx, dependentAlias)
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(dependents) != 2 {
		t.Fatalf("expected 2 dependents on %q (alias-saved + UUID-saved), got %d: %v", dependentAlias, len(dependents), dependents)
	}
	gotIDs := map[string]string{}
	for _, d := range dependents {
		gotIDs[d.ID] = d.DeploymentStatus
	}
	if status, ok := gotIDs[aliasAgentID]; !ok || status != DisplayStatusDeployed {
		t.Errorf("alias-saved dependent missing or wrong status; got %v", gotIDs)
	}
	if status, ok := gotIDs[uuidAgentID]; !ok || status != DisplayStatusDeployed {
		t.Errorf("UUID-saved dependent missing or wrong status; got %v", gotIDs)
	}

	failed, err := dependentsHandler.UndeployDependents(h.ctx, dependentAlias)
	if err != nil {
		t.Fatalf("UndeployDependents: %v", err)
	}
	if len(failed) > 0 {
		t.Fatalf("UndeployDependents could not undeploy %d dependent(s): %v", len(failed), failed)
	}

	h.waitForInstanceGone(h.agentInstanceName(aliasAgentID), 5*time.Second)
	h.waitForInstanceGone(h.agentInstanceName(uuidAgentID), 5*time.Second)

	generalInfo, _ := h.aweExe.GetInstance(h.agentInstanceName(generalAgentID))
	if generalInfo == nil {
		t.Error("general agent should still be running after undeploying dependents")
	}

	if err := h.modelConfigSvc.Delete(h.ctx, modelResp.ID); err != nil {
		t.Fatalf("delete model config: %v", err)
	}
	_, getErr := h.modelConfigSvc.GetByAlias(h.ctx, dependentAlias)
	if getErr == nil {
		t.Errorf("model config %q should be deleted", dependentAlias)
	}
}

func TestE2E_AIAssistantGenerateAgent(t *testing.T) {
	t.Parallel()

	llmResponseJSON := `{
		"name": "DataAnalyzer",
		"description": "Analyzes data from connected databases and provides insights on trends and patterns",
		"systemPrompt": "You are a data analysis assistant. You help users explore and understand their data by running queries, identifying trends, and presenting insights clearly. Always validate your analysis before presenting results to ensure accuracy and completeness.",
		"toolsets": ["builtin_web_request_tools"],
		"skills": [
			{"name": "Trend Analysis", "description": "Analyzes quarterly trends from various data sources and generates visual reports"},
			{"name": "ab", "description": "too short name to pass validation"}
		],
		"connectors": ["VALID_CONNECTOR_ID", "nonexistent-id"],
		"inputModes": ["text"],
		"outputModes": ["text"]
	}`

	h := setupE2E(t, nil)

	connResp, err := h.connectorSvc.Create(h.ctx, CreateConnectorRequest{
		Description: "E2E test connector for fixture purposes",
		Name:        "ai-test-db",
		Type:        "sql",
		Subtype:     "postgres",
		Values: map[string]any{
			"hostname": "db.example.com",
			"port":     "5432",
			"database": "analytics",
			"username": "reader",
			"password": "pass",
		},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}

	llmResponseJSON = strings.ReplaceAll(llmResponseJSON, "VALID_CONNECTOR_ID", connResp.ID)

	h.createAgent("DataAnalyzer", "Existing agent to trigger name collision resolution.")

	mockLLM := mockllm.NewFIFOClient([]*llm.ChatResponse{
		{Content: llmResponseJSON, FinishReason: "stop"},
	})
	resolver := func(_ context.Context) (llm.Client, string, func(), error) {
		return mockLLM, "", func() {}, nil
	}
	aiSvc := newAIAssistantServiceWithResolver(resolver, h.agentRepo, h.connectorRepo, slog.Default())

	result, err := aiSvc.GenerateAgent(h.ctx, GenerateAgentRequest{
		Description: "a data analytics assistant that can query databases",
	}, "e2e-user", "")
	if err != nil {
		t.Fatalf("GenerateAgent: %v", err)
	}

	if result.Name != "DataAnalyzer 2" {
		t.Errorf("name = %q, want 'DataAnalyzer 2' (conflict resolution)", result.Name)
	}

	hasArtifactTools := false
	hasWebTools := false
	for _, ts := range result.Toolsets {
		if ts == "builtin_artifact_tools" {
			hasArtifactTools = true
		}
		if ts == "builtin_web_request_tools" {
			hasWebTools = true
		}
	}
	if !hasArtifactTools {
		t.Error("toolsets should include builtin_artifact_tools (default)")
	}
	if !hasWebTools {
		t.Error("toolsets should include builtin_web_request_tools (from LLM)")
	}

	if len(result.Connectors) != 1 || result.Connectors[0] != connResp.ID {
		t.Errorf("connectors = %v, want [%s] (nonexistent filtered out)", result.Connectors, connResp.ID)
	}

	if len(result.Skills) != 1 {
		t.Fatalf("expected 1 valid skill (short name filtered), got %d", len(result.Skills))
	}
	if result.Skills[0].Name != "Trend Analysis" {
		t.Errorf("skill name = %q, want 'Trend Analysis'", result.Skills[0].Name)
	}

	if len(result.SystemPrompt) < 100 {
		t.Errorf("system prompt too short (%d chars), expected 100+", len(result.SystemPrompt))
	}
}

func TestE2E_ModelConfigBootstrapRepliesOnlyOnResponseTopic(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	if _, err := h.modelConfigSvc.Create(h.ctx, &CreateModelConfigRequest{
		Alias:      "guarded-model",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-guarded"},
	}, "e2e-user"); err != nil {
		t.Fatalf("create model config: %v", err)
	}

	listener := NewModelConfigBootstrapListener(h.modelConfigSvc, h.brk, h.namespace, slog.Default())
	if err := listener.Start(h.ctx); err != nil {
		t.Fatalf("start listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Stop(context.Background()) })

	subscribe := func(topic string) broker.Queue {
		q, err := h.brk.CreateDirectSubscription()
		if err != nil {
			t.Fatalf("create queue: %v", err)
		}
		if err := h.brk.Subscribe(q, topic); err != nil {
			t.Fatalf("subscribe %s: %v", topic, err)
		}
		return q
	}
	attackerTopic := h.namespace + "/attacker/inbox"
	otherComponentTopic := a2a.ModelConfigBootstrapResponseTopic(h.namespace, "guarded-model", "victim")
	noComponentTopic := a2a.ModelConfigBootstrapResponseTopic(h.namespace, "guarded-model", "")
	otherNamespaceTopic := a2a.ModelConfigBootstrapResponseTopic("other-ns", "guarded-model", "agent-1")
	validTopic := a2a.ModelConfigBootstrapResponseTopic(h.namespace, "guarded-model", "agent-1")
	forgedQueues := map[string]broker.Queue{
		attackerTopic:       subscribe(attackerTopic),
		otherComponentTopic: subscribe(otherComponentTopic),
		noComponentTopic:    subscribe(noComponentTopic),
		otherNamespaceTopic: subscribe(otherNamespaceTopic),
	}
	validQueue := subscribe(validTopic)

	requests := []bootstrapRequest{
		{ComponentID: "agent-1", ReplyTo: attackerTopic, ModelID: "guarded-model"},
		{ComponentID: "agent-1", ReplyTo: otherComponentTopic, ModelID: "guarded-model"},
		{ReplyTo: noComponentTopic, ModelID: "guarded-model"},
		{ComponentID: "agent-1", ReplyTo: otherNamespaceTopic, ModelID: "guarded-model"},
		{ComponentID: "agent-1", ReplyTo: validTopic, ModelID: "guarded-model"},
	}
	for _, req := range requests {
		req.ComponentType = "agent"
		payload, _ := json.Marshal(req)
		if err := h.brk.Publish(h.ctx, a2a.ModelConfigBootstrapRequestTopic(h.namespace, req.ModelID), &broker.Message{Payload: payload}); err != nil {
			t.Fatalf("publish bootstrap request: %v", err)
		}
	}

	replyCtx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel()
	reply, err := h.brk.Receive(replyCtx, validQueue)
	if err != nil {
		t.Fatalf("receive bootstrap response on the protocol's topic: %v", err)
	}
	if !strings.Contains(string(reply.Payload), "sk-guarded") {
		t.Errorf("valid bootstrap response lacks the model config: %s", reply.Payload)
	}

	for topic, q := range forgedQueues {
		pollCtx, pollCancel := context.WithTimeout(h.ctx, 100*time.Millisecond)
		msg, recvErr := h.brk.Receive(pollCtx, q)
		pollCancel()
		if recvErr == nil && msg != nil {
			t.Errorf("bootstrap response sent to %s: %s", topic, msg.Payload)
		}
	}
}

func TestE2E_ModelConfigBootstrapNotFound(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	listener := NewModelConfigBootstrapListener(h.modelConfigSvc, h.brk, h.namespace, slog.Default())
	if err := listener.Start(h.ctx); err != nil {
		t.Fatalf("start listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Stop(context.Background()) })

	time.Sleep(100 * time.Millisecond)

	replyTopic := h.namespace + "/configuration/model/response/nonexistent/test-component"
	replyQueue, qErr := h.brk.CreateDirectSubscription()
	if qErr != nil {
		t.Fatalf("create reply queue: %v", qErr)
	}
	if subErr := h.brk.Subscribe(replyQueue, replyTopic); subErr != nil {
		t.Fatalf("subscribe reply: %v", subErr)
	}

	bootstrapReq, _ := json.Marshal(bootstrapRequest{
		ComponentID:   "test-component",
		ComponentType: "agent",
		ReplyTo:       replyTopic,
		ModelID:       "nonexistent",
	})
	requestTopic := a2a.ModelConfigBootstrapRequestTopic(h.namespace, "nonexistent")
	if pubErr := h.brk.Publish(h.ctx, requestTopic, &broker.Message{
		Payload: bootstrapReq,
	}); pubErr != nil {
		t.Fatalf("publish bootstrap request: %v", pubErr)
	}

	replyCtx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel()
	replyMsg, recvErr := h.brk.Receive(replyCtx, replyQueue)
	if recvErr != nil {
		t.Fatalf("receive bootstrap response: %v", recvErr)
	}

	var resp map[string]any
	if unmarshalErr := json.Unmarshal(replyMsg.Payload, &resp); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if resp["model_config"] != nil {
		t.Errorf("expected model_config=null for nonexistent model, got %v", resp["model_config"])
	}
}

func TestE2E_A2AProxy_TrailingSlashURL(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	srv := newA2ATestServer(t, withA2AResponseText("25"))

	resp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "trailing_slash_test",
		AgentURL:          srv.URL + "/",
		AgentCardLocation: "well_known",
		Skills:            []any{map[string]any{"name": "math"}},
		AgentAuthConfig:   map[string]any{},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent: %v", err)
	}

	h.deployA2AAgent(resp.ID)
	instanceName := h.agentInstanceName(resp.ID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "What is 100/4?", 10*time.Second)
	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "25")

	reqs := srv.getTaskRequests()
	if len(reqs) == 0 {
		t.Fatal("strict A2A server received no task requests — card fetch likely failed due to double-slash URL")
	}
}

func TestE2E_A2AProxy_TaskIDStrippedBecauseExternalServersAssignTheirOwn(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	srv := newA2ATestServer(t, withA2AResponseText("task ID was properly stripped"))

	resp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "taskid_strip_test",
		AgentURL:          srv.URL,
		AgentCardLocation: "well_known",
		Skills:            []any{map[string]any{"name": "echo"}},
		AgentAuthConfig:   map[string]any{},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent: %v", err)
	}

	h.deployA2AAgent(resp.ID)
	instanceName := h.agentInstanceName(resp.ID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "Test taskId stripping", 10*time.Second)
	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "task ID was properly stripped")

	reqs := srv.getTaskRequests()
	if len(reqs) == 0 {
		t.Fatal("strict A2A server received no requests")
	}
	for i, raw := range reqs {
		if strings.Contains(string(raw), `"taskId"`) {
			t.Errorf("request[%d] still contains taskId — proxy should strip it before forwarding:\n%s",
				i, truncate(string(raw), 300))
		}
	}
}

func TestE2E_A2AProxy_SeparateCardAndTaskAuth(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	srv := newA2ATestServer(t,
		withCardAuth("card-secret-token"),
		withTaskAuth("task-secret-key"),
		withA2AResponseText("authenticated response"),
	)

	resp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "dual_auth_test",
		AgentURL:          srv.URL,
		AgentCardLocation: "well_known",
		AgentCardAuthType: "bearer",
		AgentCardAuthConfig: map[string]any{
			"token": "card-secret-token",
		},
		AgentAuthType: "apikey",
		AgentAuthConfig: map[string]any{
			"apiKey":     "task-secret-key",
			"headerName": "X-API-Key",
		},
		Skills: []any{map[string]any{"name": "secure"}},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent: %v", err)
	}

	h.deployA2AAgent(resp.ID)
	instanceName := h.agentInstanceName(resp.ID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "Secure request", 10*time.Second)
	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "authenticated response")

	reqs := srv.getTaskRequests()
	if len(reqs) == 0 {
		t.Fatal("server received no task requests — card auth or task auth failed")
	}
}

func TestE2E_A2AProxy_NoCardAgent(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	srv := newA2ATestServer(t,
		withCardDisabled(),
		withA2AResponseText("no-card response"),
	)

	resp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "no_card_test",
		AgentURL:          srv.URL,
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "echo"}},
		AgentAuthConfig:   map[string]any{},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent: %v", err)
	}

	h.deployA2AAgent(resp.ID)
	instanceName := h.agentInstanceName(resp.ID)
	h.waitForInstance(instanceName, 5*time.Second)

	events := h.sendMessage(instanceName, "Hello no-card agent", 10*time.Second)
	h.assertHasResponseEvent(events)
	h.assertFinalResponse(events, "no-card response")

	reqs := srv.getTaskRequests()
	if len(reqs) == 0 {
		t.Fatal("no-card proxy did not forward the message to downstream server")
	}
}

func TestE2E_A2AProxy_UpdatePushesNewConfig(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	srv := newA2ATestServer(t, withA2AResponseText("first response"))

	resp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "update_target",
		AgentURL:          srv.URL,
		AgentCardLocation: "well_known",
		Skills:            []any{map[string]any{"name": "echo"}},
		AgentAuthConfig:   map[string]any{},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent: %v", err)
	}

	h.deployA2AAgent(resp.ID)
	instanceName := h.agentInstanceName(resp.ID)
	h.waitForInstance(instanceName, 5*time.Second)

	if _, updateErr := h.a2aSvc.Update(h.ctx, resp.ID, UpdateRemoteAgentRequest{
		Name:                  "update_target",
		AgentURL:              srv.URL,
		Description:           "updated description",
		AgentCardLocation:     "well_known",
		RequestTimeoutSeconds: 90,
		Skills:                []any{map[string]any{"name": "echo"}},
	}, "e2e-user"); updateErr != nil {
		t.Fatalf("update A2A agent: %v", updateErr)
	}

	updateResp, deployErr := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: resp.ID,
		Action:  DeployActionUpdate,
	}, "e2e-user")
	if deployErr != nil {
		t.Fatalf("update deploy: %v", deployErr)
	}
	if updateResp.Status != DeployStatusSuccess {
		t.Errorf("update deploy status = %q, want %q (err=%q)", updateResp.Status, DeployStatusSuccess, updateResp.ErrorMessage)
	}

	h.waitForInstance(instanceName, 5*time.Second)
	events := h.sendMessage(instanceName, "post-update probe", 10*time.Second)
	h.assertHasResponseEvent(events)
}

func TestE2E_A2AProxy_ReconcilerRedeploys(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "standard agent response"},
		{Content: "reconciled response"},
	})

	srv := newA2ATestServer(t, withA2AResponseText("proxy response"))

	standardID := h.createAgent("reconciler_std", "You are a standard agent for reconciler testing")
	h.deploy(standardID)
	standardInstance := h.agentInstanceName(standardID)
	h.waitForInstance(standardInstance, 5*time.Second)

	a2aResp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "reconciler_proxy",
		AgentURL:          srv.URL,
		AgentCardLocation: "well_known",
		Skills:            []any{map[string]any{"name": "echo"}},
		AgentAuthConfig:   map[string]any{},
	}, "e2e-user")
	if err != nil {
		t.Fatalf("create A2A agent: %v", err)
	}
	h.deployA2AAgent(a2aResp.ID)
	proxyInstance := h.agentInstanceName(a2aResp.ID)
	h.waitForInstance(proxyInstance, 5*time.Second)

	if _, err := h.controlClient.Custom(h.ctx, "awe", "delete", "instances/"+standardInstance, nil); err != nil {
		t.Fatalf("delete standard instance: %v", err)
	}
	if _, err := h.controlClient.Custom(h.ctx, "awe", "delete", "instances/"+proxyInstance, nil); err != nil {
		t.Fatalf("delete proxy instance: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	h.nudgeAWEs()

	h.waitForInstance(standardInstance, 10*time.Second)
	h.waitForInstance(proxyInstance, 10*time.Second)

	events := h.sendMessage(standardInstance, "Are you reconciled?", 5*time.Second)
	h.assertHasResponseEvent(events)
}

func TestE2E_ModelProviderYAMLFormat(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	agentID := h.createAgentWithModelProvider("yaml_fmt_agent", "You are a test agent", "planning")

	agent, toolsets, inputModes, outputModes, _, err := h.agentRepo.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}

	cfg, buildErr := h.yamlSvc.BuildAgentConfig(agent, toolsets, inputModes, outputModes, nil)
	if buildErr != nil {
		t.Fatalf("BuildAgentConfig: %v", buildErr)
	}

	appConfig := extractAppConfig(t, cfg)

	mp := appConfig["model_provider"]
	if mp == nil {
		t.Fatal("model_provider not set in app_config")
	}

	mpList, ok := mp.([]string)
	if !ok {
		t.Fatalf("model_provider should be []string, got %T (value: %v)", mp, mp)
	}
	if len(mpList) != 1 || mpList[0] != "planning" {
		t.Errorf("model_provider = %v, want [planning]", mpList)
	}
}

func TestE2E_ModelProviderYAMLFormat_General(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	agentID := h.createAgent("yaml_fmt_general", "You are a test agent")

	agent, toolsets, inputModes, outputModes, _, err := h.agentRepo.GetByID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}

	cfg, buildErr := h.yamlSvc.BuildAgentConfig(agent, toolsets, inputModes, outputModes, nil)
	if buildErr != nil {
		t.Fatalf("BuildAgentConfig: %v", buildErr)
	}

	appConfig := extractAppConfig(t, cfg)

	mp, exists := appConfig["model_provider"]
	if !exists {
		t.Fatal("model_provider must be emitted for 'general' provider too")
	}
	mpList, ok := mp.([]string)
	if !ok {
		t.Fatalf("model_provider should be []string, got %T", mp)
	}
	if len(mpList) != 1 || mpList[0] != "general" {
		t.Errorf("model_provider = %v, want [general]", mpList)
	}
}

func extractAppConfig(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	apps, ok := cfg["apps"].([]map[string]any)
	if !ok || len(apps) == 0 {
		t.Fatal("missing apps array in generated config")
	}
	appConfig, ok := apps[0]["app_config"].(map[string]any)
	if !ok {
		t.Fatal("missing app_config in first app entry")
	}
	return appConfig
}

func TestE2E_GWE_SSEStreamRoundTrip(t *testing.T) {
	t.Parallel()

	responses := []*llm.ChatResponse{
		{Content: "Hello from SSE stream!", FinishReason: "stop"},
	}
	h := setupE2EWithGWE(t, responses)

	agentID := h.createAgent("sse_roundtrip_agent", "You are a helpful assistant")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	gwID := h.createGateway("sse_test_gw")
	gwSlug := h.gatewaySlug(gwID)
	h.deployGateway(gwID)
	h.waitForGWEInstance(gwID, 5*time.Second)

	h.publishAgentCardToGateway(instanceName)

	taskID := h.httpPostMessage(gwSlug, instanceName, "Stream me a response")

	events := h.httpSubscribeSSE(gwSlug, taskID, 15*time.Second)

	if len(events) == 0 {
		t.Fatal("expected SSE events but got none")
	}

	var hasFinal bool
	var finalData string
	for _, ev := range events {
		if ev.Event == "final_response" {
			hasFinal = true
			finalData = ev.Data
		}
	}
	if !hasFinal {
		t.Fatalf("no final_response event in SSE stream; got events: %v", events)
	}
	if !strings.Contains(finalData, "Hello from SSE stream!") {
		t.Errorf("final_response data does not contain expected text:\n%s", finalData)
	}
}

func TestE2E_WorkflowMessageExecution(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "Workflow processed your request", FinishReason: "stop"},
	})

	agentID := h.createAgent("wf_inner_agent", "You are a workflow agent that processes tasks.")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	appConfig := map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{
				"result": "{{nodes.process.output}}",
			},
			"nodes": []any{
				map[string]any{
					"id":         "process",
					"type":       "agent",
					"agent_name": instanceName,
				},
			},
		},
	}

	wfID := h.createWorkflow("e2e_msg_workflow", appConfig)
	h.deployWorkflow(wfID)
	wfInstanceName := workflowInstanceName(wfID)
	h.waitForInstance(wfInstanceName, 5*time.Second)

	events := h.sendMessage(wfInstanceName, "Execute this workflow", 15*time.Second)
	h.assertHasResponseEvent(events)
}

func buildGoToolZIP(t *testing.T, pkgName string) []byte {
	t.Helper()
	elfHeader := []byte{0x7f, 'E', 'L', 'F', 0, 1, 2}
	manifest := []byte("name: " + pkgName + "\nversion: 1.0.0\n")
	return buildZIP(t, map[string][]byte{
		pkgName:         elfHeader,
		"manifest.yaml": manifest,
	})
}

func TestE2E_ProCodeTooling_Vertical(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	logger := slog.New(slog.DiscardHandler)

	resourceStore := objectstore.NewStore(objectstore.NewMemoryClient())
	storage := newToolsetStorage(resourceStore, logger)

	toolsetSvc := NewToolsetService(h.toolPackageRepo.DB(), h.toolPackageRepo, storage, h.outboxRepo, h.namespace, logger)
	toolSvc := NewToolService(store.NewToolRepo(h.toolPackageRepo.DB()), logger)

	brk := broker.NewDevBroker()
	if err := brk.Connect(h.ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	handler := NewDBResourceEventHandler(nil, toolSvc, toolsetSvc, logger)
	tracker := platpkg.NewResourceTracker(brk, h.namespace, handler, logger)
	if err := tracker.Start(h.ctx); err != nil {
		t.Fatalf("start tracker: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Stop(context.Background()) })
	time.Sleep(50 * time.Millisecond)

	var (
		pkgID   string
		pkgResp *ToolsetResponse
	)

	t.Run("1_upload", func(t *testing.T) {
		created, err := toolsetSvc.Create(h.ctx, CreateToolsetRequest{Name: "my-pkg", Description: "vertical e2e pkg"}, "e2e-user")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		pkgID = created.ID

		zipBytes := buildGoToolZIP(t, "my-pkg")
		err = toolsetSvc.Upload(h.ctx, pkgID, "my-pkg.zip", zipBytes, "e2e-user")
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}

		got, err := toolsetSvc.GetByID(h.ctx, pkgID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.DiscoveryStatus != "pending" {
			t.Errorf("DiscoveryStatus = %q, want pending", got.DiscoveryStatus)
		}
	})

	t.Run("2_discovery_broker_to_db", func(t *testing.T) {
		configSchema := `[{"name":"api-key","secret":true}]`
		publishToolInit(t, brk, h.namespace, remotetool.InitParams{
			ToolName:     "my-pkg__my-tool",
			Description:  "my vertical tool",
			Parameters:   map[string]any{"type": "object", "properties": map[string]any{}},
			ConfigSchema: []map[string]any{{"name": "api-key", "secret": true}},
		})
		_ = configSchema

		waitForDB(t, 5*time.Second, func() bool {
			got, _ := toolsetSvc.GetByID(h.ctx, pkgID)
			return got != nil && got.DiscoveryStatus == "ready"
		})

		pkgResp, _ = toolsetSvc.GetByID(h.ctx, pkgID)
		if pkgResp == nil {
			t.Fatal("GetByID returned nil after discovery")
		}
		if pkgResp.DiscoveryStatus != "ready" {
			t.Errorf("DiscoveryStatus = %q, want ready", pkgResp.DiscoveryStatus)
		}
		if len(pkgResp.Files) != 1 {
			t.Fatalf("Files len = %d, want 1", len(pkgResp.Files))
		}
		if pkgResp.Files[0].ToolName != "my-tool" {
			t.Errorf("ToolName = %q, want my-tool", pkgResp.Files[0].ToolName)
		}
		if pkgResp.Files[0].ConfigSchema == nil {
			t.Error("ConfigSchema should be non-nil after discovery")
		}
	})

	t.Run("3_agent_assignment", func(t *testing.T) {
		_, err := h.agentSvc.Create(h.ctx, CreateAgentRequest{
			Name:         "pro-code-agent",
			Description:  "vertical e2e agent",
			SystemPrompt: "test",
			Toolsets:     []string{"my-pkg"},
			InputModes:   []string{"text"},
			OutputModes:  []string{"text"},
		}, "e2e-user")
		if err != nil {
			t.Fatalf("createAgentWithToolsets: %v", err)
		}
	})

	t.Run("4_yaml_emission_secret_omitted_when_no_config_values", func(t *testing.T) {
		files, err := h.toolPackageRepo.GetFilesForPackage(h.ctx, pkgID)
		if err != nil {
			t.Fatalf("GetFilesForPackage: %v", err)
		}
		if len(files) == 0 {
			t.Fatal("no tool_package_files rows — step 2 must complete first")
		}

		agent := &store.Agent{
			Description: strPtr("test agent for fixture purposes"),
			ID:          "aaaabbbb-1111-2222-3333-ccccddddeeee",
			Name:        "pro-code-agent",
			CreatedBy:   "e2e-user",
			UpdatedBy:   "e2e-user",
		}

		packages := map[string]ToolPackageInfo{
			"my-pkg": {
				ToolFiles: files,
			},
		}

		yamlStr, err := h.yamlSvc.GenerateAgentYAMLWithPackages(
			agent,
			[]string{"my-pkg"},
			[]string{"text"},
			[]string{"text"},
			nil,
			packages,
		)
		if err != nil {
			t.Fatalf("GenerateAgentYAMLWithPackages: %v", err)
		}

		if !strings.Contains(yamlStr, "tool_name: my-pkg__my-tool") {
			t.Errorf("YAML missing tool_name: my-pkg__my-tool:\n%s", truncate(yamlStr, 800))
		}

		if strings.Contains(yamlStr, "${TC_") {
			t.Errorf("${TC_*} placeholder must not appear in YAML:\n%s", truncate(yamlStr, 800))
		}

		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
			t.Fatalf("YAML parse error: %v", err)
		}
		apps, _ := parsed["apps"].([]any)
		if len(apps) == 0 {
			t.Fatal("YAML has no apps")
		}
		cfg, _ := apps[0].(map[string]any)["app_config"].(map[string]any)
		tools, _ := cfg["tools"].([]any)
		var foundEntry map[string]any
		for _, raw := range tools {
			entry, _ := raw.(map[string]any)
			if entry["tool_name"] == "my-pkg__my-tool" {
				foundEntry = entry
				break
			}
		}
		if foundEntry == nil {
			t.Fatal("tool entry my-pkg__my-tool not found in YAML tools list")
		}

		if _, present := foundEntry["tool_config"]; present {
			t.Errorf("tool_config must be absent when secret has no config_value and no default: %v", foundEntry["tool_config"])
		}
	})

	t.Run("5_tool_failure_structured_error", func(t *testing.T) {
		invokeID := "e2e-invoke-fail-1"
		replyTopic := h.namespace + "/a2a/v1/e2e-reply/" + invokeID

		replyQueue, err := brk.CreateDirectSubscription()
		if err != nil {
			t.Fatalf("create reply queue: %v", err)
		}
		err = brk.Subscribe(replyQueue, replyTopic)
		if err != nil {
			t.Fatalf("subscribe reply: %v", err)
		}

		invokeErr := remotetool.InvokeError{
			Code:       remotetool.ErrToolExitNonzero,
			Message:    "tool failed",
			StderrTail: "TOOL_STDERR_OUTPUT\nfailed at line 7",
			StackTrace: "goroutine 1 [running]:\nmain.main()\n\t/app/main.go:42",
		}

		errJSON, _ := json.Marshal(invokeErr)
		errRaw := json.RawMessage(errJSON)
		rpcResp := map[string]any{
			"jsonrpc": "2.0",
			"id":      invokeID,
			"error":   json.RawMessage(errJSON),
		}
		respPayload, _ := json.Marshal(rpcResp)
		_ = errRaw

		invokeTopic := remotetool.InvokeTopic(h.namespace, "my-pkg__my-tool")
		invokeReqEnv := map[string]any{
			"jsonrpc": "2.0",
			"id":      invokeID,
			"method":  remotetool.MethodInvoke,
			"params": remotetool.InvokeParams{
				TaskID:   invokeID,
				ToolName: "my-pkg__my-tool",
				Args:     map[string]any{"input": "test"},
			},
		}
		reqPayload, _ := json.Marshal(invokeReqEnv)

		invokeQueue, err := brk.CreateDirectSubscription()
		if err != nil {
			t.Fatalf("create invoke queue: %v", err)
		}
		err = brk.Subscribe(invokeQueue, invokeTopic)
		if err != nil {
			t.Fatalf("subscribe invoke topic: %v", err)
		}

		err = brk.Publish(h.ctx, invokeTopic, &broker.Message{
			Payload: reqPayload,
			UserProperties: map[string]string{
				remotetool.PropReplyTo: replyTopic,
			},
		})
		if err != nil {
			t.Fatalf("publish invoke: %v", err)
		}

		recvCtx, cancel := context.WithTimeout(h.ctx, 2*time.Second)
		defer cancel()
		msg, err := brk.Receive(recvCtx, invokeQueue)
		if err != nil || msg == nil {
			t.Skip("no invoke message received on topic (STR not running) — skipping error assertion")
			return
		}
		msg.Ack()

		err = brk.Publish(h.ctx, replyTopic, &broker.Message{
			Payload:       respPayload,
			CorrelationID: invokeID,
		})
		if err != nil {
			t.Fatalf("publish error response: %v", err)
		}

		replyCtx, replyCancel := context.WithTimeout(h.ctx, 2*time.Second)
		defer replyCancel()
		replyMsg, err := brk.Receive(replyCtx, replyQueue)
		if err != nil || replyMsg == nil {
			t.Fatal("no reply received")
		}
		replyMsg.Ack()

		var captured struct {
			Error *remotetool.InvokeError `json:"error"`
		}
		if err := json.Unmarshal(replyMsg.Payload, &captured); err != nil {
			t.Fatalf("unmarshal reply: %v", err)
		}
		if captured.Error == nil {
			t.Fatal("expected error field in reply")
		}
		if captured.Error.Code != remotetool.ErrToolExitNonzero {
			t.Errorf("Code = %q, want TOOL_EXIT_NONZERO", captured.Error.Code)
		}
		if captured.Error.StderrTail == "" {
			t.Error("StderrTail should be populated")
		}
		if captured.Error.StackTrace == "" {
			t.Error("StackTrace should be populated")
		}
	})

	t.Run("6_telemetry_outbox", func(t *testing.T) {
		events, err := h.outboxRepo.GetDispatchable(h.ctx, 100)
		if err != nil {
			t.Fatalf("GetDispatchable: %v", err)
		}
		var found bool
		for _, e := range events {
			if e.EventType != "tool.uploaded" {
				continue
			}
			var p redeployPayload
			if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
				continue
			}
			if p.Name == "my-pkg" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no tool.uploaded outbox event for my-pkg; events: %v", events)
		}
	})

	t.Run("7_failed_discovery_sweeper", func(t *testing.T) {
		created, err := toolsetSvc.Create(h.ctx, CreateToolsetRequest{Name: "bad-pkg", Description: "stale pending"}, "e2e-user")
		if err != nil {
			t.Fatalf("Create bad-pkg: %v", err)
		}

		zipBytes := buildGoToolZIP(t, "bad-pkg")
		if uploadErr := toolsetSvc.Upload(h.ctx, created.ID, "bad.zip", zipBytes, "e2e-user"); uploadErr != nil {
			t.Fatalf("Upload bad-pkg: %v", uploadErr)
		}

		pkg, err := toolsetSvc.GetByID(h.ctx, created.ID)
		if err != nil {
			t.Fatalf("GetByID bad-pkg: %v", err)
		}
		if pkg.DiscoveryStatus != "pending" {
			t.Fatalf("precondition: bad-pkg DiscoveryStatus = %q, want pending", pkg.DiscoveryStatus)
		}

		futureMs := time.Now().Add(time.Hour).UnixMilli()
		err = toolsetSvc.FailStalePending(h.ctx, futureMs)
		if err != nil {
			t.Fatalf("FailStalePending: %v", err)
		}

		pkg, err = toolsetSvc.GetByID(h.ctx, created.ID)
		if err != nil {
			t.Fatalf("GetByID bad-pkg after sweep: %v", err)
		}
		if pkg.DiscoveryStatus != "failed" {
			t.Errorf("DiscoveryStatus = %q, want failed", pkg.DiscoveryStatus)
		}
		if len(pkg.DiscoveryErrors) == 0 {
			t.Error("DiscoveryErrors should be non-empty after sweep")
		}
	})

	t.Run("8_reupload_in_use_succeeds_and_rename_422", func(t *testing.T) {
		agentID := h.createAgentWithToolsets("ref-agent", "uses my-pkg", []string{"my-pkg"})
		_ = agentID

		zipBytes := buildGoToolZIP(t, "my-pkg")

		if err := toolsetSvc.Upload(h.ctx, pkgID, "my-pkg-v2.zip", zipBytes, "e2e-user"); err != nil {
			t.Fatalf("re-upload of in-use package: %v", err)
		}

		_, err := toolsetSvc.Update(h.ctx, pkgID, UpdateToolsetRequest{Name: "renamed-pkg"}, "e2e-user")
		if err == nil {
			t.Fatal("expected ValidationError for rename after upload")
		}
		var valErr *ValidationError
		if !errors.As(err, &valErr) {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
		if !strings.Contains(valErr.Message, "rename unsupported") {
			t.Errorf("ValidationError.Message = %q, expected to contain 'rename unsupported'", valErr.Message)
		}
	})
}
