// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newDiscoverableMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
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
			result = map[string]any{"tools": []map[string]any{
				{"name": "search", "description": "Search.", "inputSchema": map[string]any{"type": "object"}},
				{"name": "fetch", "description": "Fetch.", "inputSchema": map[string]any{"type": "object"}},
			}}
		default:
			result = map[string]any{}
		}
		rb, _ := json.Marshal(result)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rq.ID, "result": json.RawMessage(rb)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mcpAllowListValues(serverURL string) map[string]any {
	return map[string]any{
		"server_url":      serverURL,
		"connection_type": "streamable-http",
		"auth_type":       "none",
		"allow_list":      "search",
	}
}

func createDiscoveredMCPConnector(t *testing.T, svc *ConnectorService, serverURL string) string {
	t.Helper()
	svc.SetMCPDiscoveryService(NewMCPDiscoveryService(discardLogger(), nil))
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:    "mcp-allow-list",
		Type:    "mcp",
		Subtype: "remote",
		Values:  mcpAllowListValues(serverURL),
	}, "user-1")
	if err != nil {
		t.Fatalf("Create returned %v, want nil", err)
	}
	return created.ID
}

func TestConnectorService_Update_MCPAllowListWithDiscoveredManifestAcceptsIdenticalValues(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	srv := newDiscoverableMCPServer(t)
	repo := store.NewConnectorRepo(testOpenDB(t))
	svc := NewConnectorService(repo, nil, "test-ns", discardLogger())
	id := createDiscoveredMCPConnector(t, svc, srv.URL)

	row, err := repo.GetByID(t.Context(), id)
	if err != nil || row == nil || row.Values == nil {
		t.Fatalf("GetByID = %v, %v", row, err)
	}
	if !strings.Contains(*row.Values, `"manifest"`) {
		t.Fatalf("create did not store a discovered manifest: %s", *row.Values)
	}

	req := UpdateConnectorRequest{Values: mcpAllowListValues(srv.URL)}
	if _, err := svc.PreflightUpdate(t.Context(), id, req); err != nil {
		t.Fatalf("PreflightUpdate with the values the connector was created with returned %v, want nil", err)
	}
	if _, err := svc.Update(t.Context(), id, req, "user-1"); err != nil {
		t.Fatalf("Update with the values the connector was created with returned %v, want nil", err)
	}
}

func TestConnectorService_Update_MCPRequestWithManifestAndAllowListIsRejected(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	srv := newDiscoverableMCPServer(t)
	repo := store.NewConnectorRepo(testOpenDB(t))
	svc := NewConnectorService(repo, nil, "test-ns", discardLogger())
	id := createDiscoveredMCPConnector(t, svc, srv.URL)

	values := mcpAllowListValues(srv.URL)
	values["manifest"] = []any{map[string]any{"name": "search"}}
	req := UpdateConnectorRequest{Values: values}

	for name, call := range map[string]func() error{
		"PreflightUpdate": func() error { _, err := svc.PreflightUpdate(t.Context(), id, req); return err },
		"Update":          func() error { _, err := svc.Update(t.Context(), id, req, "user-1"); return err },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "manifest is mutually exclusive") {
			t.Errorf("%s returned %v, want the manifest exclusivity error", name, err)
		}
	}
}

func TestConnectorService_Update_MCPStoredToolNameStillConflictsWithRequestedAllowList(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	srv := newDiscoverableMCPServer(t)
	repo := store.NewConnectorRepo(testOpenDB(t))
	svc := NewConnectorService(repo, nil, "test-ns", discardLogger())
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:    "mcp-tool-name",
		Type:    "mcp",
		Subtype: "remote",
		Values: map[string]any{
			"server_url":      srv.URL,
			"connection_type": "streamable-http",
			"auth_type":       "none",
			"tool_name":       "search",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("Create returned %v, want nil", err)
	}

	_, err = svc.Update(t.Context(), created.ID, UpdateConnectorRequest{Values: mcpAllowListValues(srv.URL)}, "user-1")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("Update returned %v, want the tool_name/allow_list exclusivity error", err)
	}
}
