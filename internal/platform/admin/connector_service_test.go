// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestBuildMCPManifestEntries_PreservesSchemaUnderCanonicalKey(t *testing.T) {
	tools := []MCPDiscoveredTool{
		{
			Name:        "search",
			Description: "Search the catalogue.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"query"},
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer"},
				},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"hits"},
				"properties": map[string]any{
					"hits":  map[string]any{"type": "array"},
					"total": map[string]any{"type": "integer"},
				},
			},
		},
		{
			Name:        "ping",
			Description: "No-arg liveness check.",
			InputSchema: nil,
		},
	}

	entries := buildMCPManifestEntries(tools)
	if len(entries) != len(tools) {
		t.Fatalf("len(entries) = %d, want %d", len(entries), len(tools))
	}

	first := entries[0]
	if _, hasSchema := first["schema"]; hasSchema {
		t.Errorf("entries[0] has legacy %q key — must use canonical %q (issue #1327)", "schema", "inputSchema")
	}
	schema, ok := first["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("entries[0][%q] = %T, want map[string]any", "inputSchema", first["inputSchema"])
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "query" {
		t.Errorf("entries[0].inputSchema.required = %v, want [query]", schema["required"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("entries[0].inputSchema.properties = %T, want map[string]any", schema["properties"])
	}
	if _, found := props["query"]; !found {
		t.Errorf("entries[0].inputSchema.properties missing 'query': %v", props)
	}

	second := entries[1]
	if _, hasSchema := second["schema"]; hasSchema {
		t.Errorf("entries[1] has legacy %q key", "schema")
	}
	defSchema, ok := second["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("entries[1][%q] = %T, want map[string]any", "inputSchema", second["inputSchema"])
	}
	if defSchema["type"] != "object" {
		t.Errorf("entries[1].inputSchema.type = %v, want object", defSchema["type"])
	}

	outSchema, ok := first["outputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("entries[0][%q] = %T, want map[string]any", "outputSchema", first["outputSchema"])
	}
	outProps, ok := outSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("entries[0].outputSchema.properties = %T, want map[string]any", outSchema["properties"])
	}
	for _, field := range []string{"hits", "total"} {
		if _, found := outProps[field]; !found {
			t.Errorf("entries[0].outputSchema.properties missing %q: %v", field, outProps)
		}
	}

	if _, hasOut := second["outputSchema"]; hasOut {
		t.Errorf("entries[1] carries an %q key for a tool that declared none: %v", "outputSchema", second)
	}
}

func TestEnrichMCPManifestIfNeeded_EndToEnd(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var rq struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &rq)
		if len(rq.ID) == 0 || string(rq.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch rq.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake-mcp", "version": "1.0"},
			}
		case "tools/list":
			result = map[string]any{
				"tools": []map[string]any{{
					"name":        "search",
					"description": "Search the catalogue.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []any{"query"},
						"properties": map[string]any{
							"query": map[string]any{"type": "string"},
						},
					},
					"outputSchema": map[string]any{
						"type":     "object",
						"required": []any{"hits"},
						"properties": map[string]any{
							"hits":  map[string]any{"type": "array"},
							"total": map[string]any{"type": "integer"},
						},
					},
				}},
			}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		rb, _ := json.Marshal(result)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      rq.ID,
			"result":  json.RawMessage(rb),
		})
	}))
	t.Cleanup(srv.Close)

	cs := &ConnectorService{logger: discardLogger(), mcpSvc: NewMCPDiscoveryService(discardLogger(), nil)}
	values := map[string]any{
		"server_url":      srv.URL,
		"connection_type": "streamable-http",
	}

	out, ok, warnings := cs.enrichMCPManifestIfNeeded(context.Background(), "mcp", values)
	if !ok {
		t.Fatal("enrichMCPManifestIfNeeded returned ok=false, want true (discovery should have succeeded)")
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none when discovery succeeded", warnings)
	}

	raw, err := json.Marshal(out["manifest"])
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var manifest []map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(manifest) != 1 {
		t.Fatalf("len(manifest) = %d, want 1", len(manifest))
	}
	entry := manifest[0]
	if _, hasLegacy := entry["schema"]; hasLegacy {
		t.Errorf("manifest entry has legacy %q key — must use canonical %q (issue #1327)", "schema", "inputSchema")
	}
	schema, ok := entry["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("manifest entry[%q] = %T, want map[string]any", "inputSchema", entry["inputSchema"])
	}
	if required, _ := schema["required"].([]any); len(required) != 1 || required[0] != "query" {
		t.Errorf("manifest entry inputSchema.required = %v, want [query]", schema["required"])
	}
	props, _ := schema["properties"].(map[string]any)
	if _, found := props["query"]; !found {
		t.Errorf("manifest entry inputSchema.properties missing 'query': %v", props)
	}

	outSchema, ok := entry["outputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("manifest entry[%q] = %T, want map[string]any", "outputSchema", entry["outputSchema"])
	}
	if outSchema["type"] != "object" {
		t.Errorf("manifest entry outputSchema.type = %v, want object", outSchema["type"])
	}
	if required, _ := outSchema["required"].([]any); len(required) != 1 || required[0] != "hits" {
		t.Errorf("manifest entry outputSchema.required = %v, want [hits]", outSchema["required"])
	}
	outProps, _ := outSchema["properties"].(map[string]any)
	for _, field := range []string{"hits", "total"} {
		if _, found := outProps[field]; !found {
			t.Errorf("manifest entry outputSchema.properties missing %q: %v", field, outProps)
		}
	}
}

func TestEnrichMCPManifestIfNeeded_SendsCustomHeaders(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	var mu sync.Mutex
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if gotHeader == "" {
			gotHeader = r.Header.Get("DD-API-KEY")
		}
		mu.Unlock()
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cs := &ConnectorService{logger: discardLogger(), mcpSvc: NewMCPDiscoveryService(discardLogger(), nil)}
	values := map[string]any{
		"server_url":      srv.URL,
		"connection_type": "streamable-http",
		"auth_type":       "none",
		"custom_headers":  map[string]any{"DD-API-KEY": "secret-key"},
	}

	_, _, _ = cs.enrichMCPManifestIfNeeded(context.Background(), "mcp", values)

	mu.Lock()
	defer mu.Unlock()
	if gotHeader != "secret-key" {
		t.Fatalf("custom header not sent on save-time discovery: got %q, want %q", gotHeader, "secret-key")
	}
}

func TestConnectorService_Create_ProbeFailureBlocksSave(t *testing.T) {
	stub := &stubBedrockKBClient{err: errors.New("AccessDeniedException: not authorized to call GetKnowledgeBase")}
	installStubBedrockKBClient(t, stub, nil, nil)

	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)
	svc := NewConnectorService(repo, nil, "test-ns", discardLogger())

	req := CreateConnectorRequest{
		Name:        "bedrock-kb-conn",
		Description: "Bedrock KB connector for the probe-failure path",
		Type:        "knowledge_base",
		Subtype:     "bedrock",
		Values: map[string]any{
			"kb_id":                 "ABC123XYZ",
			"region":                "us-east-1",
			"auth_type":             "access_key",
			"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}
	_, err := svc.Create(t.Context(), req, "user-1")
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("Create returned %T (%v), want *ValidationError from the failed probe", err, err)
	}
	if verr.Message != bedrockKBProbeFailureMessage {
		t.Errorf("Create error message = %q, want the canonical probe-failure copy %q", verr.Message, bedrockKBProbeFailureMessage)
	}
	if stub.calls != 1 {
		t.Errorf("GetKnowledgeBase call count = %d, want 1 (the probe must have run)", stub.calls)
	}

	_, total, err := repo.ListPaginated(t.Context(), 1, 10, store.ConnectorFilter{}, store.ConnectorOrder{})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if total != 0 {
		t.Errorf("connector rows persisted = %d, want 0 (probe failure must block the save)", total)
	}
}

func TestConnectorService_Update_ProbeFailureBlocksSave(t *testing.T) {
	stub := &stubBedrockKBClient{}
	installStubBedrockKBClient(t, stub, nil, nil)

	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)
	svc := NewConnectorService(repo, nil, "test-ns", discardLogger())

	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "bedrock-kb-update",
		Description: "Bedrock KB connector seeded for the update probe-failure path",
		Type:        "knowledge_base",
		Subtype:     "bedrock",
		Values: map[string]any{
			"kb_id":                 "ORIGINALKB1",
			"region":                "us-east-1",
			"auth_type":             "access_key",
			"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create returned %v, want nil", err)
	}

	stub.err = errors.New("InvalidSignatureException: signature did not match")
	_, err = svc.Update(t.Context(), created.ID, UpdateConnectorRequest{
		Values: map[string]any{
			"kb_id":                 "CHANGEDKB99",
			"region":                "us-east-1",
			"auth_type":             "access_key",
			"aws_access_key_id":     "AKIAIOSFODNN7CHANGED",
			"aws_secret_access_key": "changedSecretAccessKeyChangedSecretKey00",
		},
	}, "user-1")
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("Update returned %T (%v), want *ValidationError from the failed probe", err, err)
	}
	if verr.Message != bedrockKBProbeFailureMessage {
		t.Errorf("Update error message = %q, want the canonical probe-failure copy %q", verr.Message, bedrockKBProbeFailureMessage)
	}

	row, err := repo.GetByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row == nil || row.Values == nil {
		t.Fatal("seeded connector vanished or lost its values")
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(*row.Values), &stored); err != nil {
		t.Fatalf("unmarshal stored values: %v", err)
	}
	if stored["kb_id"] != "ORIGINALKB1" {
		t.Errorf("stored kb_id = %v, want ORIGINALKB1 (probe failure must not persist the edit)", stored["kb_id"])
	}
}

func newRedeployServiceForTest(t *testing.T) (*ConnectorService, *store.DB, *store.AgentRepo, *store.OutboxRepo) {
	t.Helper()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	outboxRepo := store.NewOutboxRepo(db)
	svc := NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", discardLogger())
	svc.SetRedeployDeps(db, agentRepo, store.NewWorkflowRepo(db), outboxRepo)
	return svc, db, agentRepo, outboxRepo
}

func seedSQLConnectorBoundToAgent(t *testing.T, svc *ConnectorService, db *store.DB) (connectorID, agentID string) {
	t.Helper()
	conn, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "redeploy-sql",
		Description: "A connector seeded for the redeploy orchestration tests",
		Type:        "sql",
		Subtype:     "postgres",
		Values: map[string]any{
			"hostname": "localhost",
			"port":     "5432",
			"database": "testdb",
			"username": "testuser",
			"password": "testpass",
		},
	}, "u1")
	if err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	agentSvc := NewAgentService(store.NewAgentRepo(db), store.NewAgentDeploymentRepo(db), store.NewConnectorRepo(db), store.NewToolPackageRepo(db), nil, nil, discardLogger())
	agent, err := agentSvc.Create(t.Context(), CreateAgentRequest{
		Name:         "redeploy-agent",
		Description:  "Agent bound to the connector under test",
		SystemPrompt: "You help with database queries and answer user questions about data.",
		Toolsets:     []string{},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
		Connectors:   []string{conn.ID},
	}, "u1")
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return conn.ID, agent.ID
}

func outboxPayloads(t *testing.T, repo *store.OutboxRepo, eventType string) []redeployPayload {
	t.Helper()
	events, err := repo.GetDispatchable(t.Context(), 100)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	var out []redeployPayload
	for _, e := range events {
		if e.EventType != eventType {
			continue
		}
		var p redeployPayload
		if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
			t.Fatalf("decode outbox payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

func TestUpdateWithRedeploy_WritesTheOutboxRowInTheSameTx(t *testing.T) {
	svc, db, _, outbox := newRedeployServiceForTest(t)
	connID, agentID := seedSQLConnectorBoundToAgent(t, svc, db)

	resp, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Description: "a new description for the connector"}, "u1", ConnectorUpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}
	if resp == nil || resp.Description != "a new description for the connector" {
		t.Fatalf("response = %+v, want the updated connector", resp)
	}
	events := outboxPayloads(t, outbox, "connector.updated")
	if len(events) != 1 {
		t.Fatalf("got %d connector.updated rows, want 1", len(events))
	}
	if !slices.Contains(events[0].AgentIDs, agentID) {
		t.Errorf("outbox agentIds = %v, want it to carry %s so the agent redeploys", events[0].AgentIDs, agentID)
	}
}

func TestUpdateWithRedeploy_RollsBackTheRowWhenTheOutboxWriteFails(t *testing.T) {
	svc, db, _, _ := newRedeployServiceForTest(t)
	connID, _ := seedSQLConnectorBoundToAgent(t, svc, db)
	before, err := svc.GetByID(t.Context(), connID)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}
	if _, err = db.ExecContext(t.Context(), `DROP TABLE outbox_events`); err != nil {
		t.Fatalf("drop outbox_events: %v", err)
	}

	if _, err = svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Description: "a new description for the connector"}, "u1", ConnectorUpdateOptions{}); err == nil {
		t.Fatal("want an error when the outbox write fails")
	}
	after, err := svc.GetByID(t.Context(), connID)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if after.Description != before.Description {
		t.Errorf("description = %q, want %q: the row must roll back with the outbox write", after.Description, before.Description)
	}
}

type versionedMCPServer struct {
	url         string
	bearerToken string
	mu          sync.Mutex
	version     string
}

var overlongMCPToolName = strings.Repeat("x", maxMCPToolNameLen+6)

var versionedMCPTools = map[string][]map[string]any{
	"v1": {
		{"name": "get_weather", "description": "Weather v1", "inputSchema": map[string]any{"type": "object"}},
	},
	"v2": {
		{"name": "get_weather", "description": "Weather v2", "inputSchema": map[string]any{"type": "object"}},
		{"name": "get_forecast", "description": "Forecast v2", "inputSchema": map[string]any{"type": "object"}},
	},
	"empty": {},
	"long": {
		{"name": "get_weather", "description": "Weather long", "inputSchema": map[string]any{"type": "object"}},
		{"name": overlongMCPToolName, "description": "Too long", "inputSchema": map[string]any{"type": "object"}},
	},
	"all-long": {
		{"name": overlongMCPToolName, "description": "Too long", "inputSchema": map[string]any{"type": "object"}},
	},
}

func newVersionedMCPServer(t *testing.T, bearerToken string) *versionedMCPServer {
	t.Helper()
	s := &versionedMCPServer{version: "v1", bearerToken: bearerToken}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		version := s.version
		s.mu.Unlock()
		if version == "down" {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		if s.bearerToken != "" && r.Header.Get("Authorization") != "Bearer "+s.bearerToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
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
				"serverInfo":      map[string]any{"name": "versioned-mcp", "version": version},
			}
		case "tools/list":
			result = map[string]any{"tools": versionedMCPTools[version]}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rq.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *versionedMCPServer) serve(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = version
}

func newMCPConnectorForUpdateTest(t *testing.T) (*ConnectorService, *store.DB, *versionedMCPServer, string) {
	t.Helper()
	return newMCPConnectorWithBearerForUpdateTest(t, "")
}

func newMCPConnectorWithBearerForUpdateTest(t *testing.T, bearerToken string) (*ConnectorService, *store.DB, *versionedMCPServer, string) {
	t.Helper()
	t.Setenv(allowPrivateMCPEnvVar, "true")
	svc, db, _, _ := newRedeployServiceForTest(t)
	svc.SetMCPDiscoveryService(NewMCPDiscoveryService(discardLogger(), nil))
	server := newVersionedMCPServer(t, bearerToken)
	var auth map[string]any
	if bearerToken != "" {
		auth = bearerAuthValues(bearerToken)
	}
	conn, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "versioned-mcp",
		Description: "An MCP server that is upgraded while the connector points at it",
		Type:        "mcp",
		Subtype:     "remote",
		Values:      mcpConnectorValues(server.url, auth),
	}, "u1")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if got := storedManifestTools(t, db, conn.ID); !slices.Equal(got, []string{"get_weather: Weather v1"}) {
		t.Fatalf("manifest after create = %v, want the v1 tool list", got)
	}
	return svc, db, server, conn.ID
}

func mcpConnectorValues(serverURL string, extra map[string]any) map[string]any {
	values := map[string]any{"server_url": serverURL, "connection_type": "streamable-http", "auth_type": "none"}
	maps.Copy(values, extra)
	return values
}

func bearerAuthValues(token string) map[string]any {
	return map[string]any{"auth_type": "http", "auth_http_scheme": "bearer", "auth_http_bearer_token": token}
}

func storedManifestTools(t *testing.T, db *store.DB, connectorID string) []string {
	t.Helper()
	row, err := store.NewConnectorRepo(db).GetByID(t.Context(), connectorID)
	if err != nil || row == nil || row.Values == nil {
		t.Fatalf("read connector %s: row=%v err=%v", connectorID, row, err)
	}
	var values struct {
		Manifest []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(*row.Values), &values); err != nil {
		t.Fatalf("decode stored values: %v", err)
	}
	tools := make([]string, 0, len(values.Manifest))
	for _, tool := range values.Manifest {
		tools = append(tools, tool.Name+": "+tool.Description)
	}
	slices.Sort(tools)
	return tools
}

func TestUpdateWithRedeploy_MCPManifestFollowsTheServer(t *testing.T) {
	tests := []struct {
		name          string
		serverVersion string
		extraValues   map[string]any
		wantTools     []string
		wantWarnings  []string
	}{
		{
			name:          "an update that sends no manifest stores the tools the server lists now",
			serverVersion: "v2",
			wantTools:     []string{"get_forecast: Forecast v2", "get_weather: Weather v2"},
		},
		{
			name:          "an update that sends a null manifest stores the tools the server lists now",
			serverVersion: "v2",
			extraValues:   map[string]any{"manifest": nil},
			wantTools:     []string{"get_forecast: Forecast v2", "get_weather: Weather v2"},
		},
		{
			name:          "an update that sends a manifest stores that manifest",
			serverVersion: "v2",
			extraValues: map[string]any{"manifest": []any{
				map[string]any{"name": "pinned_tool", "description": "Written by the operator", "inputSchema": map[string]any{"type": "object"}},
			}},
			wantTools: []string{"pinned_tool: Written by the operator"},
		},
		{
			name:          "an update that sends no manifest keeps the stored one when the server cannot list its tools",
			serverVersion: "down",
			wantTools:     []string{"get_weather: Weather v1"},
			wantWarnings:  []string{keptWhileTheServerIsDown},
		},
		{
			name:          "an update that sends no manifest keeps the stored one when the server lists no tools",
			serverVersion: "empty",
			wantTools:     []string{"get_weather: Weather v1"},
			wantWarnings:  []string{mcpToolListKeptWarning},
		},
		{
			name:          "an update that sends an empty manifest stores the tools the server lists now",
			serverVersion: "v2",
			extraValues:   map[string]any{"manifest": []any{}},
			wantTools:     []string{"get_forecast: Forecast v2", "get_weather: Weather v2"},
		},
		{
			name:          "an update that sends an empty manifest stores none and says so when the server cannot list its tools",
			serverVersion: "down",
			extraValues:   map[string]any{"manifest": []any{}},
			wantTools:     []string{},
			wantWarnings:  []string{mcpToolListClearedWarning},
		},
		{
			name:          "an update that sends an empty manifest stores none and says so when every listed name is over-long",
			serverVersion: "all-long",
			extraValues:   map[string]any{"manifest": []any{}},
			wantTools:     []string{},
			wantWarnings:  []string{mcpToolListClearedWarning, overlongToolNamesWarning([]string{overlongMCPToolName})},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, db, server, connID := newMCPConnectorForUpdateTest(t)
			server.serve(tc.serverVersion)

			req := UpdateConnectorRequest{Values: mcpConnectorValues(server.url, tc.extraValues)}
			resp, err := svc.UpdateWithRedeploy(t.Context(), connID, req, "u1", ConnectorUpdateOptions{})
			if err != nil {
				t.Fatalf("UpdateWithRedeploy: %v", err)
			}
			if got := storedManifestTools(t, db, connID); !slices.Equal(got, tc.wantTools) {
				t.Errorf("stored manifest = %v, want %v", got, tc.wantTools)
			}
			if !slices.Equal(resp.Warnings, tc.wantWarnings) {
				t.Errorf("warnings = %q, want %q", resp.Warnings, tc.wantWarnings)
			}
		})
	}
}

func TestUpdateConnectorWithRedeploy_StoresTheToolsTheMCPServerListsNow(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("v2")

	_, err := UpdateConnectorWithRedeploy(t.Context(), db, svc, ConnectorRedeployUpdate{
		ID:      connID,
		Request: UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)},
		UserID:  "u1",
	}, discardLogger())
	if err != nil {
		t.Fatalf("UpdateConnectorWithRedeploy: %v", err)
	}
	want := []string{"get_forecast: Forecast v2", "get_weather: Weather v2"}
	if got := storedManifestTools(t, db, connID); !slices.Equal(got, want) {
		t.Errorf("stored manifest = %v, want %v", got, want)
	}
}

func TestUpdateWithRedeploy_RefusesASelectedToolTheMCPServerNoLongerLists(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("v2")
	if _, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{
		Values: mcpConnectorValues(server.url, map[string]any{"selected_tools": "get_forecast"}),
	}, "u1", ConnectorUpdateOptions{}); err != nil {
		t.Fatalf("select get_forecast on v2: %v", err)
	}
	server.serve("v1")

	_, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{
		Values: mcpConnectorValues(server.url, map[string]any{"selected_tools": "get_forecast"}),
	}, "u1", ConnectorUpdateOptions{})
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("UpdateWithRedeploy error = %T (%v), want *ValidationError", err, err)
	}
	if !strings.Contains(verr.Message, "get_forecast") || !strings.Contains(verr.Message, "get_weather") {
		t.Errorf("message = %q, want it to name the missing get_forecast and the available get_weather", verr.Message)
	}
	want := []string{"get_forecast: Forecast v2", "get_weather: Weather v2"}
	if got := storedManifestTools(t, db, connID); !slices.Equal(got, want) {
		t.Errorf("stored manifest = %v, want the refused update to leave %v", got, want)
	}
}

func TestDeleteWithRedeploy_WritesTheOutboxRowAndRemovesTheConnector(t *testing.T) {
	svc, db, _, outbox := newRedeployServiceForTest(t)
	connID, agentID := seedSQLConnectorBoundToAgent(t, svc, db)

	if err := svc.DeleteWithRedeploy(t.Context(), connID, "u1"); err != nil {
		t.Fatalf("DeleteWithRedeploy: %v", err)
	}
	if _, err := svc.GetByID(t.Context(), connID); err == nil {
		t.Error("connector still exists after delete")
	}
	events := outboxPayloads(t, outbox, "connector.deleted")
	if len(events) != 1 || !slices.Contains(events[0].AgentIDs, agentID) {
		t.Errorf("connector.deleted rows = %+v, want one carrying %s", events, agentID)
	}
}

func TestDeleteWithRedeploy_KeepsTheConnectorWhenTheOutboxWriteFails(t *testing.T) {
	svc, db, _, _ := newRedeployServiceForTest(t)
	connID, _ := seedSQLConnectorBoundToAgent(t, svc, db)
	if _, err := db.ExecContext(t.Context(), `DROP TABLE outbox_events`); err != nil {
		t.Fatalf("drop outbox_events: %v", err)
	}

	if err := svc.DeleteWithRedeploy(t.Context(), connID, "u1"); err == nil {
		t.Fatal("want an error when the outbox write fails")
	}
	if _, err := svc.GetByID(t.Context(), connID); err != nil {
		t.Errorf("connector must survive a failed outbox write: %v", err)
	}
}

func TestConnectorGetSecretsDiff_MatchesTheStoredSecret(t *testing.T) {
	svc := newConnectorServiceForTest(t)
	id := createStoredEventMeshConnector(t, svc)

	diff, err := svc.GetSecretsDiff(t.Context(), id, map[string]any{"broker_password": storedConnectorBrokerPassword})
	if err != nil {
		t.Fatalf("GetSecretsDiff: %v", err)
	}
	if !diff["broker_password"].Matches {
		t.Error("an identical secret must report matches=true")
	}

	diff, err = svc.GetSecretsDiff(t.Context(), id, map[string]any{"broker_password": "something-else"})
	if err != nil {
		t.Fatalf("GetSecretsDiff: %v", err)
	}
	if diff["broker_password"].Matches {
		t.Error("a changed secret must report matches=false")
	}
}

func TestConnectorGetSecretsDiff_UnknownConnectorIsNotFound(t *testing.T) {
	svc := newConnectorServiceForTest(t)

	_, err := svc.GetSecretsDiff(t.Context(), "00000000-0000-4000-8000-000000000000", map[string]any{})

	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
}

func TestConnectorService_ListPaginated_ExcludedTypesKeepPaginationMetaCoherent(t *testing.T) {
	repo := store.NewConnectorRepo(testOpenDB(t))
	svc := NewConnectorService(repo, nil, "test-ns", discardLogger())
	for i, typ := range []string{"api", "api", "api", "event_mesh", "event_mesh", "event_mesh", "event_mesh"} {
		c := &store.Connector{Name: fmt.Sprintf("c-%d", i), Type: typ, CreatedBy: "u", UpdatedBy: "u"}
		if err := repo.Create(t.Context(), c); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	resp, err := svc.ListPaginated(t.Context(), 1, 3, store.ConnectorFilter{ExcludeTypes: []string{"event_mesh"}}, store.ConnectorOrder{})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	p := resp.Meta.Pagination
	if len(resp.Data) != 3 || p.Count != 3 || p.TotalPages != 1 {
		t.Errorf("rows=%d count=%d totalPages=%d, want 3/3/1: hidden rows must not inflate the page count", len(resp.Data), p.Count, p.TotalPages)
	}
}

func TestConnectorGetSecretsDiff_ComparesEverySettingOfAnUnregisteredType(t *testing.T) {
	svc := newConnectorServiceForTest(t)
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "plugin-connector",
		Description: "connector of a type no definition registers",
		Type:        "plugin_only",
		Subtype:     "custom",
		Values:      map[string]any{"token": "stored-token", "channel_id": 42, "region": "eu"},
	}, "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ConnectorSecretFieldsKnown("plugin_only", "custom") {
		t.Fatal("the fixture type must be unregistered")
	}

	for name, tc := range map[string]struct {
		candidate map[string]any
		changed   string
	}{
		"the stored settings":        {candidate: map[string]any{"token": "stored-token", "channel_id": json.Number("42"), "region": "eu"}},
		"a redacted or null setting": {candidate: map[string]any{"token": RedactedSentinel, "region": nil}},
		"a rotated token":            {candidate: map[string]any{"token": "rotated-token"}, changed: "token"},
		"a changed number":           {candidate: map[string]any{"channel_id": 43}, changed: "channel_id"},
		"a setting not stored":       {candidate: map[string]any{"extra": "x"}, changed: "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			diff, err := svc.GetSecretsDiff(t.Context(), created.ID, tc.candidate)
			if err != nil {
				t.Fatalf("GetSecretsDiff: %v", err)
			}
			if len(diff) != len(tc.candidate) {
				t.Errorf("diff = %+v, want one result per setting the candidate sets", diff)
			}
			for field, d := range diff {
				if want := field != tc.changed; d.Matches != want {
					t.Errorf("%s matches = %v, want %v", field, d.Matches, want)
				}
			}
		})
	}
}
