// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/maximhq/bifrost/core/schemas"
)

func newRPCErrorServer(t *testing.T, message string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "x", Version: "1"},
			})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32603, "message": message},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPTool_Execute_ServerErrorCarriesDispatchClass(t *testing.T) {
	cases := []struct {
		name    string
		message string
		class   string
	}{
		{"upstream 5xx", "Jira API request failed: status 503 Service Unavailable", taxonomy.DispatchClassBackend5xx},
		{"upstream 4xx", "RT archive rejected the query: status 403 Forbidden", taxonomy.DispatchClassBackend4xx},
		{"upstream auth", "Salesforce API returned status 401, auth required", taxonomy.DispatchClassAuthRequired},
		{"unreachable backend", "dial tcp 10.0.0.7:5432: connect: connection refused", taxonomy.DispatchClassInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRPCErrorServer(t, tc.message)
			cfg := &ToolsetConfig{
				ConnectionType: "streamable-http",
				URL:            srv.URL,
				ClientID:       "rpcerr",
				Manifest:       []ManifestTool{{Name: "ping", InputSchema: map[string]any{"type": "object"}}},
			}
			mgr := NewManager(slog.Default())
			defer mgr.Stop()
			if err := mgr.AddClient(cfg); err != nil {
				t.Fatalf("AddClient: %v", err)
			}
			ct := schemas.ChatTool{Function: &schemas.ChatToolFunction{Name: "ping"}}
			mt := newMCPTool("ping", "rpcerr-ping", &ct, mgr, cfg, nil, testLogger())

			res, err := mt.Execute(t.Context(), map[string]any{}, newMCPToolCtx(t))
			if err != nil {
				t.Fatalf("Execute returned an error instead of a failed result: %v", err)
			}
			if res.Status != tool.StatusError {
				t.Fatalf("status = %q, want error", res.Status)
			}
			if want := tool.DispatchErrorCode(tc.class); res.ErrorCode != want {
				t.Errorf("error code = %q, want %q", res.ErrorCode, want)
			}
		})
	}
}
