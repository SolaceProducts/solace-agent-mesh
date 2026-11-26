// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestExecute_RejectedTokenAnotherWriterReplaced(t *testing.T) {
	tests := []struct {
		name          string
		replaceStored bool
		rejectAll     bool
		wantCalls     int64
		wantPrompt    bool
		wantStored    string
	}{
		{name: "the call is retried once with the stored token and no sign-in is asked", replaceStored: true, wantCalls: 2, wantStored: "second-access"},
		{name: "a 401 for the stored token itself still asks the user to sign in and clears it", wantCalls: 1, wantPrompt: true},
		{name: "a replacement token that is also rejected is not retried again", rejectAll: true, wantCalls: 2, wantPrompt: true, wantStored: "access-3"},
	}
	rejections := []struct {
		name     string
		inResult bool
	}{
		{name: "HTTP 401"},
		{name: "auth error in the tool result", inResult: true},
	}
	for _, rejection := range rejections {
		for _, tt := range tests {
			t.Run(rejection.name+"/"+tt.name, func(t *testing.T) {
				const credKey = "replaced_oauth"
				credSvc := newStoringCredSvc()
				store := func(access string) {
					data, _ := json.Marshal(&toolauth.OAuthToken{AccessToken: access, RefreshToken: "rt-" + access, TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour).Unix()})
					if err := credSvc.Set(t.Context(), "A", "A", "u1", credKey, string(data)); err != nil {
						t.Fatalf("seed credential: %v", err)
					}
				}
				store("first-access")

				var calls atomic.Int64
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
					reject := func() {
						if !rejection.inResult {
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						writeJSONRPC(w, req.ID, map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "401 Unauthorized"}}})
					}
					if req.Method == "tools/call" {
						n := calls.Add(1)
						if tt.rejectAll {
							store(fmt.Sprintf("access-%d", n+1))
							reject()
							return
						}
						if r.Header.Get("Authorization") != "Bearer second-access" {
							if tt.replaceStored {
								store("second-access")
							}
							reject()
							return
						}
					}
					w.Header().Set("Content-Type", "application/json")
					switch req.Method {
					case "initialize":
						writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
							ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
							ServerInfo:      mcpgo.Implementation{Name: "items", Version: "1.0.0"},
						})
					case "tools/call":
						writeJSONRPC(w, req.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "ITEMS-OK"}}})
					default:
						writeJSONRPC(w, req.ID, map[string]any{})
					}
				}))
				defer srv.Close()

				cfg, err := ParseToolsetConfig(map[string]any{
					"tool_type":         "mcp",
					"connection_params": map[string]any{"type": "streamable-http", "url": srv.URL},
					"auth": map[string]any{
						"type":           "oauth2",
						"scheme":         map[string]any{"authorization_url": srv.URL + "/authorize", "token_url": srv.URL + "/token"},
						"credential":     map[string]any{"client_id": "cid", "client_secret": "secret"},
						"credential_key": credKey,
					},
					"manifest": []any{map[string]any{"name": "list_items", "description": "list", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}},
				})
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

				var prompted atomic.Bool
				res, err := tools[0].Execute(t.Context(), map[string]any{}, &tool.Context{
					UserID: "u1", AgentName: "A", ToolCallID: "call-1", CredentialService: credSvc,
					SendSignal: func(any) error { prompted.Store(true); return nil },
				})

				if err != nil {
					t.Fatalf("Execute: %v", err)
				}
				if got := calls.Load(); got != tt.wantCalls {
					t.Errorf("tools/call requests = %d, want %d", got, tt.wantCalls)
				}
				if prompted.Load() != tt.wantPrompt || toolauth.IsPendingAuthResult(res) != tt.wantPrompt {
					t.Errorf("sign-in asked = %v (result %q %q), want %v", prompted.Load(), res.Status, res.Message, tt.wantPrompt)
				}
				if got := storedAccess(t, credSvc, credKey); got != tt.wantStored {
					t.Errorf("stored access token = %q, want %q", got, tt.wantStored)
				}
			})
		}
	}
}

func storedAccess(t *testing.T, credSvc *storingCredSvc, credKey string) string {
	t.Helper()
	raw, _ := credSvc.Get(t.Context(), "A", "A", "u1", credKey)
	if raw == "" {
		return ""
	}
	var stored toolauth.OAuthToken
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("stored token %q does not decode: %v", raw, err)
	}
	return stored.AccessToken
}
