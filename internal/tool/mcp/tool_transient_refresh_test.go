// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/maximhq/bifrost/core/schemas"
)

type storingCredSvc struct {
	mu    sync.Mutex
	store map[string]string
}

func newStoringCredSvc() *storingCredSvc {
	return &storingCredSvc{store: make(map[string]string)}
}

func (s *storingCredSvc) Get(_ context.Context, agent, app, user, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store[agent+"|"+app+"|"+user+"|"+key], nil
}

func (s *storingCredSvc) Set(_ context.Context, agent, app, user, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[agent+"|"+app+"|"+user+"|"+key] = value
	return nil
}

func (s *storingCredSvc) Delete(_ context.Context, agent, app, user, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, agent+"|"+app+"|"+user+"|"+key)
	return nil
}

func (s *storingCredSvc) List(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

func TestExecuteWithAuth_TransientRefreshFailure_ReturnsProviderUnavailableWithoutPrompt(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer tokenSrv.Close()

	const credKey = "mcp_outage"
	ah := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: credKey,
		Scheme: &OAuthScheme{
			AuthorizationURL: tokenSrv.URL + "/authorize",
			TokenURL:         tokenSrv.URL + "/token",
		},
		Credential: &MCPAuthCred{ClientID: "cid", ClientSecret: "csecret"},
	}, slog.Default())

	desc := "test tool"
	ct := &schemas.ChatTool{
		Type:     schemas.ChatToolTypeFunction,
		Function: &schemas.ChatToolFunction{Name: "test_tool", Description: &desc},
	}
	mt := newMCPTool("test__tool", "client-test_tool", ct, nil, &ToolsetConfig{}, ah, slog.Default())

	credSvc := newStoringCredSvc()
	tokenJSON, err := json.Marshal(&toolauth.OAuthToken{
		AccessToken:  "expired-access-token",
		RefreshToken: "still-valid-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	if err = credSvc.Set(t.Context(), "A", "A", "u1", credKey, string(tokenJSON)); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	signalled := false
	beganFlow := false
	toolCtx := &tool.Context{
		UserID:            "u1",
		AgentName:         "A",
		ToolCallID:        "call-1",
		CredentialService: credSvc,
		SendSignal:        func(any) error { signalled = true; return nil },
		BeginAuthFlow:     func(string, string, string) bool { beganFlow = true; return true },
	}

	res, err := mt.Execute(t.Context(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	want := toolauth.NewSignInProviderUnavailableResult(mt.name, nil)
	if res.Status != want.Status || res.Message != want.Message || res.ErrorCode != want.ErrorCode {
		t.Errorf("result = {%q, %q, %q}, want {%q, %q, %q}",
			res.Status, res.Message, res.ErrorCode, want.Status, want.Message, want.ErrorCode)
	}
	if toolauth.IsPendingAuthResult(res) {
		t.Error("an outage at the sign-in provider must not produce a pending-auth result")
	}
	if signalled || beganFlow {
		t.Errorf("no sign-in flow may start during an outage: signalled=%v beganFlow=%v", signalled, beganFlow)
	}

	stored, _ := credSvc.Get(t.Context(), "A", "A", "u1", credKey)
	var storedToken toolauth.OAuthToken
	if err = json.Unmarshal([]byte(stored), &storedToken); err != nil {
		t.Fatalf("credential was deleted or corrupted: %v", err)
	}
	if storedToken.RefreshToken != "still-valid-refresh-token" {
		t.Errorf("stored RefreshToken = %q, want the credential kept through the outage", storedToken.RefreshToken)
	}
}
