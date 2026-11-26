// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	bifrostMCP "github.com/maximhq/bifrost/core/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const unreachableMCPCommand = "/nonexistent/sam-test-mcp-server"

func mcpToolEntry(connectorID string) map[string]any {
	return map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": unreachableMCPCommand,
			"timeout": 2,
		},
		"source_connector_id": connectorID,
	}
}

func platformStatusOfStartedAgent(t *testing.T, toolsCfg []any) platform.RuntimeStatus {
	t.Helper()
	return platformStatusOf(t, startedAgent(t, toolsCfg))
}

func startedAgent(t *testing.T, toolsCfg []any) *agent.Agent {
	t.Helper()
	svc, _ := newTrackingServices(t)
	ag := agent.New(agent.AgentConfig{Name: "agent_tool_source_status", Namespace: "test-ns"})
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := ag.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := ag.Stop(context.Background()); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return ag
}

func platformStatusOf(t *testing.T, ag *agent.Agent) platform.RuntimeStatus {
	t.Helper()
	wire, err := json.Marshal(ag.BuildAgentCard())
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(wire, &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	registry := platform.NewAgentRegistry()
	registry.AddOrUpdate(card)
	return platform.RunningStatus(platform.RuntimeProblemsFromCard(registry.Get(card.Name)))
}

func TestStartedAgent_UnreachableMCPConnectorDegradesPlatformStatus(t *testing.T) {
	status := platformStatusOfStartedAgent(t, []any{mcpToolEntry("conn-dead")})

	if status.Status() != platform.RuntimeStatusDegraded {
		t.Fatalf("runtime status = %q, want %q: a deployed agent missing a connector's tools is not plainly running", status.Status(), platform.RuntimeStatusDegraded)
	}
	problems := status.Problems()
	if len(problems) != 1 || problems[0].ID != "toolSource:conn-dead" {
		t.Fatalf("problems = %+v, want one problem naming connector conn-dead", problems)
	}
}

const lateOpenAPISpec = `{
  "openapi": "3.0.0",
  "info": {"title": "t", "version": "1"},
  "servers": [{"url": "http://example.com"}],
  "paths": {"/users": {"get": {"operationId": "listUsers", "responses": {"200": {"description": "ok"}}}}}
}`

func TestStartedAgent_MCPConnectorThatComesUpLateRegistersAndClearsItsProblem(t *testing.T) {
	savedConnect := bifrostMCP.ConnectRetryConfig
	bifrostMCP.ConnectRetryConfig = bifrostMCP.RetryConfig{MaxRetries: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	t.Cleanup(func() { bifrostMCP.ConnectRetryConfig = savedConnect })

	mcpServer := server.NewMCPServer("runner", "1.0.0")
	mcpServer.AddTool(mcpgo.NewTool("job_status"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("Succeeded"), nil
	})
	handler := server.NewStreamableHTTPServer(mcpServer)
	var up atomic.Bool
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(runner.Close)

	ag := startedAgent(t, []any{
		map[string]any{"tool_type": "openapi", "specification": lateOpenAPISpec, "source_connector_id": "conn-api"},
		map[string]any{"tool_type": "builtin", "tool_name": "list_artifacts"},
		mcpToolEntry("conn-dead"),
		map[string]any{
			"tool_type":           "mcp",
			"connection_params":   map[string]any{"type": "streamable-http", "url": runner.URL},
			"source_connector_id": "conn-runner",
		},
	})
	if ag.Tools().GetTool("job_status") != nil {
		t.Fatal("job_status registered while its server was down")
	}
	up.Store(true)

	deadline := time.Now().Add(30 * time.Second)
	for ag.Tools().GetTool("job_status") == nil || len(platformStatusOf(t, ag).Problems()) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("the late MCP connector never joined the agent; problems = %+v", platformStatusOf(t, ag).Problems())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if problems := platformStatusOf(t, ag).Problems(); problems[0].ID != "toolSource:conn-dead" {
		t.Fatalf("problems = %+v, want only the connector that is still down", problems)
	}
	statusByConnector := map[string]any{}
	for _, ext := range ag.BuildAgentCard().Capabilities.Extensions {
		if ext.URI != agent.ExtensionURIToolRegistrations {
			continue
		}
		entries, _ := ext.Params["entries"].([]any)
		for _, e := range entries {
			entry, _ := e.(map[string]any)
			statusByConnector[entry["connector_id"].(string)] = entry["status"]
		}
	}
	want := map[string]any{"conn-api": "ready", "conn-dead": "failed", "conn-runner": "ready"}
	if !maps.Equal(statusByConnector, want) {
		t.Fatalf("card tool registrations = %v, want %v", statusByConnector, want)
	}
}
