// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type memCredentialService struct {
	mu    sync.Mutex
	store map[string]string
}

func newMemCredentialService() *memCredentialService {
	return &memCredentialService{store: make(map[string]string)}
}

func (m *memCredentialService) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store[agentName+"|"+appName+"|"+userID+"|"+key], nil
}

func (m *memCredentialService) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[agentName+"|"+appName+"|"+userID+"|"+key] = value
	return nil
}

func (m *memCredentialService) Delete(_ context.Context, agentName, appName, userID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, agentName+"|"+appName+"|"+userID+"|"+key)
	return nil
}

func (m *memCredentialService) List(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

func newOAuthRefreshProxy(t *testing.T, tokenStatus int, tokenBody string) (*ProxyInstance, *memCredentialService) {
	t.Helper()
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenStatus)
		_, _ = w.Write([]byte(tokenBody))
	}))
	t.Cleanup(tokenSrv.Close)

	p := newBrokerProxy(t)
	p.cfg.TaskAuth = &AuthConfig{
		Type:             "oauth2_authorization_code",
		AuthorizationURL: tokenSrv.URL + "/authorize",
		TokenURL:         tokenSrv.URL + "/token",
		ClientID:         "cid",
		ClientSecret:     "csecret",
	}
	creds := newMemCredentialService()
	p.svc.(*mockServices).creds = creds

	raw, err := json.Marshal(toolauth.OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "still-valid-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	if err = creds.Set(t.Context(), p.cfg.Name, p.cfg.Name, "user-1", "a2a_proxy_"+p.cfg.Name, string(raw)); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	return p, creds
}

func TestHandleRequest_SignInProviderOutage_KeepsTokenAndFailsWithoutPrompt(t *testing.T) {
	p, creds := newOAuthRefreshProxy(t, http.StatusServiceUnavailable, `{"error":"server_error"}`)
	brk := p.svc.Broker()
	replyQ, err := brk.CreateQueue("outage-reply-q", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(replyQ, "reply/topic"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	reqBody := a2a.Request{
		Method: "message/send",
		ID:     "t1",
		Params: map[string]any{
			"message": map[string]any{
				"contextId": "sess-1",
				"parts":     []any{map[string]any{"kind": "text", "text": "hello"}},
			},
		},
	}
	data, _ := json.Marshal(reqBody)
	msg := broker.NewDeliveredMessage("m1", "topic", data, func() {}, nil)
	msg.UserProperties = map[string]string{"replyTo": "reply/topic", "userId": "user-1"}

	p.handleRequest(t.Context(), msg, &reqBody)

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, replyQ)
	if err != nil || received == nil {
		t.Fatalf("expected an error response, got %v", err)
	}
	var resp a2a.Response
	if err = json.Unmarshal(received.Payload, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error response, got a result")
	}
	if want := toolauth.SignInProviderUnavailableMessage(p.cfg.Name); resp.Error.Message != want {
		t.Errorf("error message = %q, want %q", resp.Error.Message, want)
	}
	if p.tasks.Get("t1") != nil {
		t.Error("the task must be removed, not left paused for a sign-in")
	}
	stored, _ := creds.Get(t.Context(), p.cfg.Name, p.cfg.Name, "user-1", "a2a_proxy_"+p.cfg.Name)
	if !strings.Contains(stored, "still-valid-rt") {
		t.Errorf("an outage at the sign-in provider must keep the stored token, got %q", stored)
	}
}

func TestGetOAuth2AuthCodeToken_RejectedRefresh_DeletesToken(t *testing.T) {
	p, creds := newOAuthRefreshProxy(t, http.StatusBadRequest, `{"error":"invalid_grant"}`)
	tc := &TaskContext{TaskID: "t1", UserID: "user-1"}

	_, err := p.getOAuth2AuthCodeToken(t.Context(), tc)
	if err == nil {
		t.Fatal("expected an error for a rejected refresh")
	}
	stored, _ := creds.Get(t.Context(), p.cfg.Name, p.cfg.Name, "user-1", "a2a_proxy_"+p.cfg.Name)
	if stored != "" {
		t.Errorf("a refresh token the provider rejected must be deleted, got %q", stored)
	}
}
