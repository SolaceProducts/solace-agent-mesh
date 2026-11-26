// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type fakeCredentialService struct {
	mu    sync.Mutex
	store map[string]string
}

var _ auth.CredentialService = (*fakeCredentialService)(nil)

func newFakeCredentialService() *fakeCredentialService {
	return &fakeCredentialService{store: make(map[string]string)}
}

func credKey(agentName, appName, userID, key string) string {
	return strings.Join([]string{agentName, appName, userID, key}, "|")
}

func (f *fakeCredentialService) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.store[credKey(agentName, appName, userID, key)], nil
}

func (f *fakeCredentialService) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[credKey(agentName, appName, userID, key)] = value
	return nil
}

func (f *fakeCredentialService) Delete(_ context.Context, agentName, appName, userID, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.store, credKey(agentName, appName, userID, key))
	return nil
}

func (f *fakeCredentialService) List(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

type fakeSignalHandler struct {
	key         string
	displayName string
	stored      tool.StoredCredential
	lastBegin   *tool.Context
}

func (f *fakeSignalHandler) CredentialKey() string { return f.key }

func (f *fakeSignalHandler) HandleAuthResponse(context.Context, *a2a.AuthResponseData, *tool.Context) error {
	return nil
}

func (f *fakeSignalHandler) SupportsUserAuth() bool  { return true }
func (f *fakeSignalHandler) AuthType() string        { return "oauth2" }
func (f *fakeSignalHandler) AuthDisplayName() string { return f.displayName }

func (f *fakeSignalHandler) CheckStoredCredential(context.Context, *tool.Context) tool.StoredCredential {
	return f.stored
}

func (f *fakeSignalHandler) BuildAuthSignal(_ context.Context, toolCtx *tool.Context) (*a2a.AuthenticationRequiredData, error) {
	f.lastBegin = toolCtx
	return &a2a.AuthenticationRequiredData{
		Type:                      string(a2a.SignalAuthenticationRequired),
		TargetAgent:               toolCtx.AgentName,
		UserID:                    toolCtx.UserID,
		ClientID:                  toolCtx.ClientID,
		AuthURI:                   "https://auth.example.com/authorize",
		AuthRequestFunctionCallID: toolCtx.ToolCallID,
		CredentialKey:             f.key,
	}, nil
}

func newToolAuthTestAgent(t *testing.T) *Agent {
	t.Helper()
	agent, _, _ := newTestAgent()
	agent.svc.(*mockServices).credSvc = newFakeCredentialService()
	return agent
}

func TestListToolAuthStatuses(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	expiry := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	agent.RegisterAuthHandler(&fakeSignalHandler{
		key:         "b-connected",
		displayName: "Connected Tool",
		stored:      tool.StoredCredential{Connected: true, ExpiresAt: expiry, GrantRef: "grant-1"},
	})
	agent.RegisterAuthHandler(&fakeSignalHandler{
		key:         "c-disconnected",
		displayName: "Disconnected Tool",
	})
	agent.RegisterAuthHandler(&stubAuthHandler{key: "a-bare"})

	infos := agent.ListToolAuth(t.Context(), "user-1")
	if len(infos) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(infos))
	}

	bare, connected, disconnected := infos[0], infos[1], infos[2]

	if bare.CredentialKey != "a-bare" || bare.Interactive || bare.Status != "not_connected" {
		t.Errorf("bare handler entry = %+v", bare)
	}
	if !connected.Interactive || connected.Status != "connected" || connected.DisplayName != "Connected Tool" || connected.AuthType != "oauth2" {
		t.Errorf("connected entry = %+v", connected)
	}
	if !connected.ExpiresAt.Equal(expiry) {
		t.Errorf("expiresAt = %v, want %v", connected.ExpiresAt, expiry)
	}
	if connected.GrantRef != "grant-1" {
		t.Errorf("grantRef = %q, want grant-1", connected.GrantRef)
	}
	if !disconnected.Interactive || disconnected.Status != "not_connected" || !disconnected.ExpiresAt.IsZero() {
		t.Errorf("disconnected entry = %+v", disconnected)
	}
}

func TestToolAuthInfoWireNames(t *testing.T) {
	raw, err := json.Marshal(ToolAuthInfo{CredentialKey: "k", Interactive: true, Status: "connected", GrantRef: "grant-1"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"credentialKey":"k","interactive":true,"status":"connected","grantRef":"grant-1"}`; string(raw) != want {
		t.Errorf("ToolAuthInfo JSON = %s, want %s", raw, want)
	}
}

func TestListToolAuthBearerHandlerNotInteractive(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	agent.RegisterAuthHandler(toolauth.NewAuthHandler(&toolauth.AuthConfig{
		Type:          "bearer",
		CredentialKey: "bearer-key",
	}, nil))

	infos := agent.ListToolAuth(t.Context(), "user-1")
	if len(infos) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(infos))
	}
	if infos[0].Interactive || infos[0].AuthType != "bearer" || infos[0].Status != "not_connected" {
		t.Errorf("bearer handler entry = %+v", infos[0])
	}
}

func TestListToolAuthRealHandlerStoredToken(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	creds := newFakeCredentialService()
	agent.svc.(*mockServices).credSvc = creds
	handler := toolauth.NewAuthHandler(&toolauth.AuthConfig{
		Type:          "oauth2",
		CredentialKey: "real-key",
		DisplayName:   "Real Tool",
	}, nil)
	agent.RegisterAuthHandler(handler)

	expiresAt := time.Now().Add(time.Hour).Unix()
	tokenJSON, err := json.Marshal(map[string]any{
		"access_token": "tok",
		"token_type":   "Bearer",
		"expires_at":   expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.Set(t.Context(), "test-agent", "test-agent", "user-1", "real-key", string(tokenJSON)); err != nil {
		t.Fatal(err)
	}

	infos := agent.ListToolAuth(t.Context(), "user-1")
	if len(infos) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(infos))
	}
	info := infos[0]
	if !info.Interactive || info.Status != "connected" || info.DisplayName != "Real Tool" {
		t.Errorf("real handler entry = %+v", info)
	}
	if want := time.Unix(expiresAt, 0).UTC(); !info.ExpiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", info.ExpiresAt, want)
	}

	infos = agent.ListToolAuth(t.Context(), "user-2")
	if infos[0].Status != "not_connected" {
		t.Errorf("user-2 status = %q, want not_connected", infos[0].Status)
	}
}

func TestListToolAuthProbeDoesNotMutateStoredCredential(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	creds := newFakeCredentialService()
	agent.svc.(*mockServices).credSvc = creds
	agent.RegisterAuthHandler(toolauth.NewAuthHandler(&toolauth.AuthConfig{
		Type:          "oauth2",
		CredentialKey: "expired-key",
		Scheme:        &toolauth.OAuthScheme{TokenURL: "https://idp.invalid/token"},
	}, nil))

	tokenJSON, err := json.Marshal(map[string]any{
		"access_token":  "stale",
		"refresh_token": "refresh",
		"token_type":    "Bearer",
		"expires_at":    time.Now().Add(-time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if setErr := creds.Set(t.Context(), "test-agent", "test-agent", "user-1", "expired-key", string(tokenJSON)); setErr != nil {
		t.Fatal(setErr)
	}

	infos := agent.ListToolAuth(t.Context(), "user-1")
	if len(infos) != 1 || infos[0].Status != toolAuthStatusConnected {
		t.Fatalf("infos = %+v, want one connected entry", infos)
	}

	after, err := creds.Get(t.Context(), "test-agent", "test-agent", "user-1", "expired-key")
	if err != nil {
		t.Fatal(err)
	}
	if after != string(tokenJSON) {
		t.Errorf("stored credential changed:\n got %s\nwant %s", after, tokenJSON)
	}
}

func TestBeginToolAuthReturnsSignal(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	handler := &fakeSignalHandler{key: "cred-1", displayName: "Tool"}
	agent.RegisterAuthHandler(handler)

	signal, err := agent.BeginToolAuth(t.Context(), "cred-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if signal.UserID != "user-1" || signal.CredentialKey != "cred-1" {
		t.Errorf("signal = %+v", signal)
	}

	got := handler.lastBegin
	if got.UserID != "user-1" || got.AgentName != "test-agent" || got.ClientID != "platform-toolauth" {
		t.Errorf("tool context = %+v", got)
	}
	if !strings.HasPrefix(got.ToolCallID, "preauth-") || got.ToolCallID != got.ParentTaskID {
		t.Errorf("ToolCallID = %q, ParentTaskID = %q", got.ToolCallID, got.ParentTaskID)
	}
	if got.TraceID == "" || got.CredentialService == nil {
		t.Errorf("TraceID = %q, CredentialService nil = %v", got.TraceID, got.CredentialService == nil)
	}

	firstCallID := got.ToolCallID
	if _, err := agent.BeginToolAuth(t.Context(), "cred-1", "user-1"); err != nil {
		t.Fatal(err)
	}
	if handler.lastBegin.ToolCallID == firstCallID {
		t.Error("consecutive begins must get distinct tool-call ids")
	}
}

func TestBeginToolAuthNonInteractiveHandler(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	agent.RegisterAuthHandler(&stubAuthHandler{key: "bare"})

	_, err := agent.BeginToolAuth(t.Context(), "bare", "user-1")
	if err == nil || !strings.Contains(err.Error(), "does not support interactive auth") {
		t.Errorf("err = %v", err)
	}
}

func TestBeginToolAuthRealBearerHandlerRejected(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	agent.RegisterAuthHandler(toolauth.NewAuthHandler(&toolauth.AuthConfig{
		Type:          "bearer",
		CredentialKey: "bearer-key",
		ResourceURL:   "https://example.invalid",
	}, nil))

	_, err := agent.BeginToolAuth(t.Context(), "bearer-key", "user-1")
	if err == nil || !strings.Contains(err.Error(), "does not support interactive auth") {
		t.Errorf("err = %v", err)
	}
}

func TestBeginToolAuthUnknownCredentialKey(t *testing.T) {
	agent := newToolAuthTestAgent(t)

	_, err := agent.BeginToolAuth(t.Context(), "ghost", "user-1")
	if !errors.Is(err, control.ErrResourceNotFound) {
		t.Errorf("err = %v, want ErrResourceNotFound", err)
	}
}

func TestToolAuthSystemSubPassthrough(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	handler := &fakeSignalHandler{key: "cred-1", displayName: "Tool"}
	agent.RegisterAuthHandler(handler)

	infos := agent.ListToolAuth(t.Context(), "system:eval")
	if len(infos) != 1 || !infos[0].Interactive {
		t.Fatalf("list for system sub = %+v", infos)
	}

	signal, err := agent.BeginToolAuth(t.Context(), "cred-1", "system:eval")
	if err != nil {
		t.Fatal(err)
	}
	if signal.UserID != "system:eval" {
		t.Errorf("signal UserID = %q, want system:eval", signal.UserID)
	}
}

func TestDisconnectToolAuthClearsStoredCredential(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	creds := newFakeCredentialService()
	agent.svc.(*mockServices).credSvc = creds
	agent.RegisterAuthHandler(toolauth.NewAuthHandler(&toolauth.AuthConfig{
		Type:          "oauth2",
		CredentialKey: "real-key",
		DisplayName:   "Real Tool",
	}, nil))

	tokenJSON, marshalErr := json.Marshal(map[string]any{
		"access_token": "tok",
		"token_type":   "Bearer",
		"expires_at":   time.Now().Add(time.Hour).Unix(),
	})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := creds.Set(t.Context(), "test-agent", "test-agent", "system:eval", "real-key", string(tokenJSON)); err != nil {
		t.Fatal(err)
	}
	if err := creds.Set(t.Context(), "test-agent", "test-agent", "system:other", "real-key", string(tokenJSON)); err != nil {
		t.Fatal(err)
	}
	if infos := agent.ListToolAuth(t.Context(), "system:eval"); infos[0].Status != "connected" {
		t.Fatalf("precondition: status = %q, want connected", infos[0].Status)
	}

	if err := agent.DisconnectToolAuth(t.Context(), "real-key", "system:eval"); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	if infos := agent.ListToolAuth(t.Context(), "system:eval"); infos[0].Status != "not_connected" {
		t.Errorf("status after disconnect = %q, want not_connected", infos[0].Status)
	}
	stored, getErr := creds.Get(t.Context(), "test-agent", "test-agent", "system:eval", "real-key")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored != "" {
		t.Error("stored credential survived the disconnect")
	}
	survivor, getErr := creds.Get(t.Context(), "test-agent", "test-agent", "system:other", "real-key")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if survivor == "" {
		t.Error("the disconnect wiped another principal's credential for the same agent and key")
	}
	if infos := agent.ListToolAuth(t.Context(), "system:other"); infos[0].Status != "connected" {
		t.Errorf("other principal status = %q, want connected", infos[0].Status)
	}
}

func TestDisconnectToolAuthUnknownCredentialKey(t *testing.T) {
	agent := newToolAuthTestAgent(t)

	err := agent.DisconnectToolAuth(t.Context(), "ghost", "user-1")
	if !errors.Is(err, control.ErrResourceNotFound) {
		t.Errorf("err = %v, want ErrResourceNotFound", err)
	}
}

func TestDisconnectToolAuthHandlerWithoutClear(t *testing.T) {
	agent := newToolAuthTestAgent(t)
	agent.RegisterAuthHandler(&stubAuthHandler{key: "bare"})

	err := agent.DisconnectToolAuth(t.Context(), "bare", "user-1")
	if err == nil || !strings.Contains(err.Error(), "does not support disconnect") {
		t.Errorf("err = %v", err)
	}
}
