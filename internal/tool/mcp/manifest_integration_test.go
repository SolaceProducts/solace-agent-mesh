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
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestManifest_EndToEnd(t *testing.T) {
	const expectedBearer = "Bearer integration-token"
	var (
		initializeN   atomic.Int64
		toolsCallN    atomic.Int64
		lastToolArgs  atomic.Pointer[map[string]any]
		lastAuthHdr   atomic.Pointer[string]
		rejectOnEmpty = true
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		lastAuthHdr.Store(&got)
		if rejectOnEmpty && got != expectedBearer {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch req.Method {
		case "initialize":
			initializeN.Add(1)
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "atlassian-mock", Version: "1.0.0"},
				Capabilities:    mcpgo.ServerCapabilities{},
			})
		case "tools/call":
			toolsCallN.Add(1)
			args := req.Params.Arguments
			lastToolArgs.Store(&args)
			writeJSONRPC(w, req.ID, map[string]any{
				"content": []any{
					map[string]any{
						"type": "text",
						"text": `{"accountId":"user-42","email":"user@example.com"}`,
					},
				},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	rawCfg := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  srv.URL,
		},
		"auth": map[string]any{
			"type": "bearer",
			"credential": map[string]any{
				"token": "integration-token",
			},
		},
		"manifest": []any{
			map[string]any{
				"name":        "atlassianUserInfo",
				"description": "Get current user info.",
				"inputSchema": map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": false,
					"$schema":              "http://json-schema.org/draft-07/schema#",
				},
			},
			map[string]any{
				"id":          "getJiraIssue",
				"description": "Get a Jira issue by ID or key.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cloudId":      map[string]any{"type": "string"},
						"issueIdOrKey": map[string]any{"type": "string"},
					},
					"required": []any{"cloudId", "issueIdOrKey"},
				},
			},
		},
	}
	cfg, err := ParseToolsetConfig(rawCfg)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if len(cfg.Manifest) != 2 {
		t.Fatalf("cfg.Manifest len = %d, want 2", len(cfg.Manifest))
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if addErr := mgr.AddClient(cfg); addErr != nil {
		t.Fatalf("AddClient: %v", addErr)
	}
	if n := initializeN.Load(); n != 0 {
		t.Fatalf("AddClient contacted the server (initializeN=%d); manifest path must defer", n)
	}

	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("DiscoverTools returned %d tools, want 2", len(tools))
	}

	var userInfoTool *MCPTool
	for _, tl := range tools {
		if tl.Name() == "atlassianUserInfo" {
			userInfoTool = tl
			break
		}
	}
	if userInfoTool == nil {
		t.Fatal("atlassianUserInfo tool not found in DiscoverTools output")
	}
	if got := userInfoTool.Parameters().Raw["$schema"]; got != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("atlassianUserInfo Parameters().Raw[$schema] = %v, want preserved", got)
	}

	toolCtx := &tool.Context{
		UserID:    "user-42",
		SessionID: "session-42",
		AgentName: "AtlassianAgent",
	}
	result, err := userInfoTool.Execute(t.Context(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("MCPTool.Execute: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}

	if got := lastAuthHdr.Load(); got == nil || *got != expectedBearer {
		t.Errorf("server saw Authorization = %v, want %q", got, expectedBearer)
	}

	if got := initializeN.Load(); got != 1 {
		t.Errorf("initialize count = %d, want 1", got)
	}
	if got := toolsCallN.Load(); got != 1 {
		t.Errorf("tools/call count = %d, want 1", got)
	}

	if result.Status == "error" {
		t.Errorf("result.Status = error, message = %q", result.Message)
	}

	payload, _ := json.Marshal(result)
	if !strings.Contains(string(payload), "user-42") {
		t.Errorf("serialized result does not contain mocked response marker:\n%s", string(payload))
	}

	_, err = userInfoTool.Execute(t.Context(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if got := initializeN.Load(); got != 1 {
		t.Errorf("after second call, initialize count = %d, want 1 (transport reused)", got)
	}
	if got := toolsCallN.Load(); got != 2 {
		t.Errorf("after second call, tools/call count = %d, want 2", got)
	}
}
