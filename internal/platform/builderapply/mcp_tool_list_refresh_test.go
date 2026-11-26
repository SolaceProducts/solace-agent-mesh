// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const allowPrivateMCPEnvVar = "SAM_PLATFORM_ALLOW_PRIVATE_MCP"

type upgradableMCPServer struct {
	url           string
	upgraded      atomic.Bool
	down          atomic.Bool
	toolListCalls atomic.Int64
}

func newUpgradableMCPServer(t *testing.T) *upgradableMCPServer {
	t.Helper()
	s := &upgradableMCPServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.down.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var rq struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &rq)
		if len(rq.ID) == 0 || string(rq.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := map[string]any{}
		switch rq.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "upgradable-mcp", "version": "1"},
			}
		case "tools/list":
			s.toolListCalls.Add(1)
			tools := []map[string]any{{"name": "get_weather", "description": "Weather", "inputSchema": map[string]any{"type": "object"}}}
			if s.upgraded.Load() {
				tools = append(tools, map[string]any{"name": "get_forecast", "description": "Forecast", "inputSchema": map[string]any{"type": "object"}})
			}
			result = map[string]any{"tools": tools}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rq.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func mcpConnectorWithSelectedTools(name, serverURL string, selected ...string) BuildComponentInput {
	return connectorWith(name, func(b map[string]any) {
		b["type"], b["subtype"] = "mcp", "remote"
		values := map[string]any{"server_url": serverURL, "connection_type": "streamable-http", "auth_type": "none"}
		if len(selected) > 0 {
			values["selected_tools"] = selected[0]
		}
		b["values"] = values
	})
}

func storedManifestToolNames(t *testing.T, svc *Service, name string) []string {
	t.Helper()
	values, _ := connectorNamed(t, svc, name).Values.(map[string]any)
	manifest, _ := values["manifest"].([]any)
	names := make([]string, 0, len(manifest))
	for _, entry := range manifest {
		tool, _ := entry.(map[string]any)
		toolName, _ := tool["name"].(string)
		names = append(names, toolName)
	}
	slices.Sort(names)
	return names
}

func TestApply_MCPConnectorSelectsAToolTheServerGainedSinceItWasCreated(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	svc := newTestService(t)
	svc.deps.Connectors.SetMCPDiscoveryService(entplatform.NewMCPDiscoveryService(discardLogger(), nil))
	server := newUpgradableMCPServer(t)
	ctx := t.Context()
	if got := runBuild(ctx, t, svc, mcpConnectorWithSelectedTools("weather_mcp", server.url), false); got.refused {
		t.Fatalf("create refused: %+v", got)
	}
	if got, want := storedManifestToolNames(t, svc, "weather_mcp"), []string{"get_weather"}; !slices.Equal(got, want) {
		t.Fatalf("manifest after create = %v, want %v", got, want)
	}
	server.upgraded.Store(true)

	for _, dryRun := range []bool{true, false} {
		got := runBuild(ctx, t, svc, mcpConnectorWithSelectedTools("weather_mcp", server.url, "get_forecast"), dryRun)
		if got.refused {
			t.Errorf("dryRun=%v: refused selecting get_forecast, which the server lists now: %+v", dryRun, got)
		}
	}

	if got, want := storedManifestToolNames(t, svc, "weather_mcp"), []string{"get_forecast", "get_weather"}; !slices.Equal(got, want) {
		t.Errorf("stored manifest = %v, want the upgraded server's %v", got, want)
	}
	values, _ := connectorNamed(t, svc, "weather_mcp").Values.(map[string]any)
	if values["selected_tools"] != "get_forecast" {
		t.Errorf("selected_tools = %v, want get_forecast", values["selected_tools"])
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{
		mcpConnectorWithSelectedTools("weather_mcp", server.url, "get_forecast"),
	}}, testActor)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if len(resp.Operations) != 1 || resp.Operations[0].Action != actionUnchanged {
		t.Errorf("re-applying the same connector = %+v, want one unchanged operation, so nothing redeploys", resp.Operations)
	}
}

func mcpManifestOf(toolNames ...string) []any {
	manifest := make([]any, 0, len(toolNames))
	for _, name := range toolNames {
		manifest = append(manifest, map[string]any{"name": name, "description": name, "inputSchema": map[string]any{"type": "object"}})
	}
	return manifest
}

func mcpConnectorWithAuthoredManifest(name, serverURL string, toolNames ...string) BuildComponentInput {
	return connectorWith(name, func(b map[string]any) {
		b["type"], b["subtype"] = "mcp", "remote"
		b["values"] = map[string]any{
			"server_url": serverURL, "connection_type": "streamable-http", "auth_type": "none",
			"manifest": mcpManifestOf(toolNames...),
		}
	})
}

func applyOperations(ctx context.Context, t *testing.T, svc *Service, comp BuildComponentInput, dryRun bool) []BuildOperationResult {
	t.Helper()
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}, DryRun: dryRun}, testActor)
	if err != nil {
		t.Fatalf("dryRun=%v: apply: %v", dryRun, err)
	}
	if len(resp.Operations) != 1 {
		t.Fatalf("dryRun=%v: operations = %+v, want exactly one", dryRun, resp.Operations)
	}
	return resp.Operations
}

func newMCPService(t *testing.T) *Service {
	t.Helper()
	t.Setenv(allowPrivateMCPEnvVar, "true")
	svc := newTestService(t)
	svc.deps.Connectors.SetMCPDiscoveryService(entplatform.NewMCPDiscoveryService(discardLogger(), nil))
	return svc
}

func TestApply_MCPConnectorAuthoredManifestEditIsAnUpdate(t *testing.T) {
	svc := newMCPService(t)
	server := newUpgradableMCPServer(t)
	ctx := t.Context()
	if got := runBuild(ctx, t, svc, mcpConnectorWithAuthoredManifest("authored_mcp", server.url, "tool_a"), false); got.refused {
		t.Fatalf("create refused: %+v", got)
	}
	if got, want := storedManifestToolNames(t, svc, "authored_mcp"), []string{"tool_a"}; !slices.Equal(got, want) {
		t.Fatalf("manifest after create = %v, want %v", got, want)
	}

	for _, dryRun := range []bool{true, false} {
		ops := applyOperations(ctx, t, svc, mcpConnectorWithAuthoredManifest("authored_mcp", server.url, "tool_b"), dryRun)
		if ops[0].Action != actionUpdate {
			t.Errorf("dryRun=%v: action = %q, want %q for an edited authored manifest: %+v", dryRun, ops[0].Action, actionUpdate, ops[0])
		}
	}
	if got, want := storedManifestToolNames(t, svc, "authored_mcp"), []string{"tool_b"}; !slices.Equal(got, want) {
		t.Errorf("stored manifest = %v, want the authored %v", got, want)
	}
}

func TestApply_MCPConnectorWithoutValuesPlansUnchangedAndKeepsItsManifest(t *testing.T) {
	cases := map[string]func(map[string]any){
		"values omitted": func(b map[string]any) { delete(b, "values") },
		"values null":    func(b map[string]any) { b["values"] = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc := newMCPService(t)
			server := newUpgradableMCPServer(t)
			ctx := t.Context()
			if got := runBuild(ctx, t, svc, mcpConnectorWithSelectedTools("bare_mcp", server.url), false); got.refused {
				t.Fatalf("create refused: %+v", got)
			}
			bare := connectorWith("bare_mcp", func(b map[string]any) {
				b["type"], b["subtype"] = "mcp", "remote"
				mutate(b)
			})
			for _, dryRun := range []bool{true, false} {
				ops := applyOperations(ctx, t, svc, bare, dryRun)
				if ops[0].Action != actionUnchanged {
					t.Errorf("dryRun=%v: action = %q, want %q: %+v", dryRun, ops[0].Action, actionUnchanged, ops[0])
				}
			}
			if got, want := storedManifestToolNames(t, svc, "bare_mcp"), []string{"get_weather"}; !slices.Equal(got, want) {
				t.Errorf("stored manifest = %v, want %v", got, want)
			}
		})
	}
}

func TestApply_MCPConnectorUpdateListsTheServersToolsOncePerRun(t *testing.T) {
	svc := newMCPService(t)
	server := newUpgradableMCPServer(t)
	ctx := t.Context()
	if got := runBuild(ctx, t, svc, mcpConnectorWithSelectedTools("counted_mcp", server.url), false); got.refused {
		t.Fatalf("create refused: %+v", got)
	}
	server.upgraded.Store(true)

	for _, dryRun := range []bool{true, false} {
		server.toolListCalls.Store(0)
		ops := applyOperations(ctx, t, svc, mcpConnectorWithSelectedTools("counted_mcp", server.url, "get_forecast"), dryRun)
		if ops[0].Action != actionUpdate {
			t.Errorf("dryRun=%v: action = %q, want %q: %+v", dryRun, ops[0].Action, actionUpdate, ops[0])
		}
		if got := server.toolListCalls.Load(); got != 1 {
			t.Errorf("dryRun=%v: tools/list calls = %d, want exactly 1", dryRun, got)
		}
	}
}

func TestApply_MCPConnectorUpdateWarnsAndKeepsItsManifestWhenTheServerIsDown(t *testing.T) {
	const warningText = "keeps the tool list it already had"
	svc := newMCPService(t)
	server := newUpgradableMCPServer(t)
	ctx := t.Context()
	if got := runBuild(ctx, t, svc, mcpConnectorWithSelectedTools("down_mcp", server.url), false); got.refused {
		t.Fatalf("create refused: %+v", got)
	}
	server.down.Store(true)
	redescribed := connectorWith("down_mcp", func(b map[string]any) {
		b["type"], b["subtype"] = "mcp", "remote"
		b["description"] = "A changed description."
		b["values"] = map[string]any{"server_url": server.url, "connection_type": "streamable-http", "auth_type": "none"}
	})

	for _, dryRun := range []bool{true, false} {
		ops := applyOperations(ctx, t, svc, redescribed, dryRun)
		if ops[0].Action != actionUpdate {
			t.Errorf("dryRun=%v: action = %q, want %q: %+v", dryRun, ops[0].Action, actionUpdate, ops[0])
		}
		matching := 0
		for _, w := range ops[0].Warnings {
			if strings.Contains(w, warningText) {
				matching++
			}
		}
		if matching != 1 {
			t.Errorf("dryRun=%v: warnings = %q, want exactly one containing %q", dryRun, ops[0].Warnings, warningText)
		}
	}
	if got, want := storedManifestToolNames(t, svc, "down_mcp"), []string{"get_weather"}; !slices.Equal(got, want) {
		t.Errorf("stored manifest = %v, want the unchanged %v", got, want)
	}
}

func TestWithStoredMCPManifest_NilValuesTakeTheStoredManifest(t *testing.T) {
	stored := map[string]any{"manifest": mcpManifestOf("get_weather")}
	got := withStoredMCPManifest(entplatform.CreateConnectorRequest{Type: "mcp", Subtype: "remote"}, stored)
	values, _ := got.Values.(map[string]any)
	if manifest, _ := values["manifest"].([]any); len(manifest) != 1 {
		t.Errorf("planned values = %+v, want the stored manifest", got.Values)
	}
}
