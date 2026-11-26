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
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestExecute_ScopeErrorRoutesToReauth(t *testing.T) {
	var toolErrText string

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
				ServerInfo:      mcpgo.Implementation{Name: "slack-mock", Version: "1.0.0"},
				Capabilities:    mcpgo.ServerCapabilities{},
			})
		case "tools/call":
			writeJSONRPC(w, req.ID, map[string]any{
				"isError": true,
				"content": []any{map[string]any{"type": "text", "text": toolErrText}},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	rawCfg := map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "streamable-http", "url": srv.URL},
		"auth": map[string]any{
			"type":       "bearer",
			"credential": map[string]any{"token": "tok"},
		},
		"manifest": []any{
			map[string]any{
				"name":        "slack_post",
				"description": "post a message",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
	cfg, err := ParseToolsetConfig(rawCfg)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err = mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil || len(tools) != 1 {
		t.Fatalf("DiscoverTools: err=%v n=%d", err, len(tools))
	}
	tl := tools[0]

	newCtx := func() *tool.Context {
		return &tool.Context{
			UserID:     "u1",
			AgentName:  "A",
			ToolCallID: "call-1",
			SendSignal: func(any) error { return nil },
		}
	}

	t.Run("scope error routes to re-auth", func(t *testing.T) {
		toolErrText = `{"error":"missing_scope"}`
		res, execErr := tl.Execute(t.Context(), map[string]any{}, newCtx())
		if execErr != nil {
			t.Fatalf("Execute: %v", execErr)
		}
		routed := toolauth.IsPendingAuthResult(res) ||
			(res.Status == "error" && strings.Contains(res.Message, "trigger auth flow"))
		if !routed {
			t.Errorf("scope error was not routed to re-auth: status=%q msg=%q", res.Status, res.Message)
		}
	})

	t.Run("ordinary error surfaces detail without re-auth", func(t *testing.T) {
		toolErrText = "channel_not_found"
		res, execErr := tl.Execute(t.Context(), map[string]any{}, newCtx())
		if execErr != nil {
			t.Fatalf("Execute: %v", execErr)
		}
		if toolauth.IsPendingAuthResult(res) {
			t.Fatal("ordinary error must NOT trigger re-auth")
		}
		if res.Status != "error" || !strings.Contains(res.Message, "channel_not_found") {
			t.Errorf("ordinary error detail not surfaced (extractErrorText): status=%q msg=%q", res.Status, res.Message)
		}
		if strings.Contains(res.Message, "trigger auth flow") {
			t.Error("ordinary error must not route to the auth flow")
		}
	})
}
