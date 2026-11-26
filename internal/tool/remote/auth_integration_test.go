// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type mockCredentialService struct {
	store map[string]string
}

func newMockCredentialService() *mockCredentialService {
	return &mockCredentialService{store: make(map[string]string)}
}

func (m *mockCredentialService) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	return m.store[agentName+"|"+appName+"|"+userID+"|"+key], nil
}

func (m *mockCredentialService) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	m.store[agentName+"|"+appName+"|"+userID+"|"+key] = value
	return nil
}

func (m *mockCredentialService) Delete(_ context.Context, agentName, appName, userID, key string) error {
	delete(m.store, agentName+"|"+appName+"|"+userID+"|"+key)
	return nil
}

func (m *mockCredentialService) List(_ context.Context, agentName, appName, userID string) ([]string, error) {
	prefix := agentName + "|" + appName + "|" + userID + "|"
	var keys []string
	for k := range m.store {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, strings.TrimPrefix(k, prefix))
		}
	}
	return keys, nil
}

func TestRemoteToolAuth_NoToken_TriggersAuthFlow(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	cfg := &ToolConfig{
		ToolName:       "auth_tool",
		Description:    "Auth-protected tool",
		TimeoutSeconds: 5,
		Auth: &toolauth.AuthConfig{
			Type:          "oauth2",
			CredentialKey: "remote_oauth",
			Scheme: &toolauth.OAuthScheme{
				AuthorizationURL: "https://auth.example.com/authorize",
				TokenURL:         "https://auth.example.com/token",
			},
			Credential: &toolauth.AuthCredential{ClientID: "test-client"},
		},
	}

	rt := NewTool(cfg, nil)
	credSvc := newMockCredentialService()

	var signalSent any
	toolCtx := &tool.Context{
		AgentName:         testAgent,
		UserID:            "user1",
		ClientID:          "client1",
		ParentTaskID:      "task-1",
		ToolCallID:        "call-1",
		CredentialService: credSvc,
		SendSignal: func(signal any) error {
			signalSent = signal
			return nil
		},
	}

	result, err := rt.Execute(context.Background(), map[string]any{"q": "test"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if result.Status != tool.StatusPending {
		t.Errorf("Status = %q, want %q", result.Status, tool.StatusPending)
	}
	if result.Message != "Pending user authorization." {
		t.Errorf("Message = %q", result.Message)
	}

	if signalSent == nil {
		t.Fatal("expected auth signal to be sent")
	}

	authData, ok := signalSent.(*a2a.AuthenticationRequiredData)
	if !ok {
		t.Fatalf("signal should be *a2a.AuthenticationRequiredData, got %T", signalSent)
	}
	if authData.CredentialKey != "remote_oauth" {
		t.Errorf("CredentialKey = %q", authData.CredentialKey)
	}
	if authData.TargetAgent != testAgent {
		t.Errorf("TargetAgent = %q", authData.TargetAgent)
	}
}

func TestRemoteToolAuth_TransientRefreshFailure_ReturnsProviderUnavailable(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer tokenSrv.Close()

	cfg := &ToolConfig{
		ToolName:       "outage_tool",
		Description:    "Auth-protected tool",
		TimeoutSeconds: 5,
		Auth: &toolauth.AuthConfig{
			Type:          "oauth2",
			CredentialKey: "remote_outage",
			Scheme: &toolauth.OAuthScheme{
				AuthorizationURL: tokenSrv.URL + "/authorize",
				TokenURL:         tokenSrv.URL + "/token",
			},
			Credential: &toolauth.AuthCredential{ClientID: "cid", ClientSecret: "csecret"},
		},
	}

	rt := NewTool(cfg, nil)
	credSvc := newMockCredentialService()
	tokenJSON, err := json.Marshal(&toolauth.OAuthToken{
		AccessToken:  "expired-access-token",
		RefreshToken: "still-valid-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	if err = credSvc.Set(t.Context(), testAgent, testAgent, "user1", "remote_outage", string(tokenJSON)); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	signalled := false
	beganFlow := false
	toolCtx := &tool.Context{
		AgentName:         testAgent,
		UserID:            "user1",
		ClientID:          "client1",
		ParentTaskID:      "task-1",
		ToolCallID:        "call-1",
		CredentialService: credSvc,
		SendSignal:        func(any) error { signalled = true; return nil },
		BeginAuthFlow:     func(string, string, string) bool { beganFlow = true; return true },
	}

	res, err := rt.Execute(t.Context(), map[string]any{"q": "test"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	want := toolauth.NewSignInProviderUnavailableResult(rt.Name(), nil)
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

	stored, _ := credSvc.Get(t.Context(), testAgent, testAgent, "user1", "remote_outage")
	var storedToken toolauth.OAuthToken
	if err = json.Unmarshal([]byte(stored), &storedToken); err != nil {
		t.Fatalf("credential was deleted or corrupted: %v", err)
	}
	if storedToken.RefreshToken != "still-valid-refresh-token" {
		t.Errorf("stored RefreshToken = %q, want the credential kept through the outage", storedToken.RefreshToken)
	}
}

func TestRemoteToolAuth_WithToken_PassesTokenToSTR(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "auth_echo",
		Description:    "Auth-protected echo",
		TimeoutSeconds: 5,
		Auth: &toolauth.AuthConfig{
			Type:          "oauth2",
			CredentialKey: "remote_oauth",
			Scheme: &toolauth.OAuthScheme{
				TokenURL: "https://auth.example.com/token",
			},
		},
	}
	rt := NewTool(cfg, exe)

	var receivedOAuthToken string
	simulateSTR(t, brk, "auth_echo", func(corrID, replyTo, _ string) {

		result := remotetool.InvokeResult{
			ToolResult: map[string]any{"message": "authenticated response"},
		}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	captureQ, _ := brk.CreateQueue("capture-auth-echo", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(captureQ, remotetool.InvokeTopic(testNamespace, "auth_echo"))

	credSvc := newMockCredentialService()
	token := &toolauth.OAuthToken{
		AccessToken: "remote-access-token-123",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
	}
	tokenJSON, _ := json.Marshal(token)
	_ = credSvc.Set(context.Background(), testAgent, testAgent, "user1", "remote_oauth", string(tokenJSON))

	toolCtx := &tool.Context{
		AgentName:         testAgent,
		UserID:            "user1",
		CredentialService: credSvc,
	}

	type execResult struct {
		result *tool.Result
		err    error
	}
	execCh := make(chan execResult, 1)
	go func() {
		r, e := rt.Execute(context.Background(), map[string]any{"q": "test"}, toolCtx)
		execCh <- execResult{r, e}
	}()

	captureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(captureCtx, captureQ)
	if err != nil {
		t.Fatalf("failed to capture invoke message: %v", err)
	}
	if msg != nil {
		msg.Ack()
		var req struct {
			Params remotetool.InvokeParams `json:"params"`
		}
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatalf("unmarshal invoke params: %v", err)
		}
		receivedOAuthToken = req.Params.ToolOAuthToken
		if len(req.Params.ToolAuthHeaders) != 0 {
			t.Errorf("oauth2 must not populate ToolAuthHeaders, got %v", req.Params.ToolAuthHeaders)
		}
	}

	er := <-execCh
	if er.err != nil {
		t.Fatalf("Execute error: %v", er.err)
	}
	if er.result.Status != tool.StatusSuccess {
		t.Errorf("Status = %q, want success", er.result.Status)
	}

	if receivedOAuthToken != "remote-access-token-123" {
		t.Errorf("ToolOAuthToken = %q, want %q", receivedOAuthToken, "remote-access-token-123")
	}
}

func captureInvokeParams(t *testing.T, brk *broker.DevBroker, toolName string, exec func()) remotetool.InvokeParams {
	t.Helper()
	captureQ, _ := brk.CreateQueue("capture-"+toolName, broker.QueueOptions{Named: true})
	_ = brk.Subscribe(captureQ, remotetool.InvokeTopic(testNamespace, toolName))

	done := make(chan struct{})
	go func() {
		exec()
		close(done)
	}()

	captureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(captureCtx, captureQ)
	if err != nil {
		t.Fatalf("capture invoke message for %s: %v", toolName, err)
	}
	msg.Ack()
	var req struct {
		Params remotetool.InvokeParams `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatalf("unmarshal invoke params: %v", err)
	}
	<-done
	return req.Params
}

func TestRemoteToolAuth_Basic_DeliversAuthorizationHeader(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "basic_tool",
		TimeoutSeconds: 5,
		Auth: &toolauth.AuthConfig{
			Type: "basic",
			Credential: &toolauth.AuthCredential{
				Username: "svc@example.com",
				Password: "api-token",
			},
		},
	}
	rt := NewTool(cfg, exe)

	simulateSTR(t, brk, "basic_tool", func(corrID, replyTo, _ string) {
		resp := a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: map[string]any{"message": "ok"}})
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	toolCtx := &tool.Context{AgentName: testAgent, UserID: "user1"}
	params := captureInvokeParams(t, brk, "basic_tool", func() {
		if _, err := rt.Execute(context.Background(), map[string]any{"q": "x"}, toolCtx); err != nil {
			t.Errorf("Execute error: %v", err)
		}
	})

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("svc@example.com:api-token"))
	if got := params.ToolAuthHeaders["Authorization"]; got != want {
		t.Errorf("ToolAuthHeaders[Authorization] = %q, want %q", got, want)
	}
	if params.ToolOAuthToken != "" {
		t.Errorf("basic auth must not set ToolOAuthToken, got %q", params.ToolOAuthToken)
	}
}

func TestRemoteToolAuth_Basic_MissingCredential_NonFatal(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "basic_no_cred",
		TimeoutSeconds: 5,
		Auth:           &toolauth.AuthConfig{Type: "basic"},
	}
	rt := NewTool(cfg, exe)

	simulateSTR(t, brk, "basic_no_cred", func(corrID, replyTo, _ string) {
		resp := a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: map[string]any{"message": "ok"}})
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	toolCtx := &tool.Context{AgentName: testAgent, UserID: "user1"}
	params := captureInvokeParams(t, brk, "basic_no_cred", func() {
		r, err := rt.Execute(context.Background(), map[string]any{"q": "x"}, toolCtx)
		if err != nil {
			t.Errorf("Execute error: %v", err)
		}
		if r != nil && r.Status != tool.StatusSuccess {
			t.Errorf("missing basic credential must not fail the call, got status %q", r.Status)
		}
	})

	if len(params.ToolAuthHeaders) != 0 {
		t.Errorf("missing credential must deliver no auth headers, got %v", params.ToolAuthHeaders)
	}
}

func TestRemoteToolAuth_Bearer_DeliversBothTokenAndHeader(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "bearer_tool",
		TimeoutSeconds: 5,
		Auth: &toolauth.AuthConfig{
			Type:       "bearer",
			Credential: &toolauth.AuthCredential{Token: "static-bearer-abc"},
		},
	}
	rt := NewTool(cfg, exe)

	simulateSTR(t, brk, "bearer_tool", func(corrID, replyTo, _ string) {
		resp := a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: map[string]any{"message": "ok"}})
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	toolCtx := &tool.Context{AgentName: testAgent, UserID: "user1"}
	params := captureInvokeParams(t, brk, "bearer_tool", func() {
		if _, err := rt.Execute(context.Background(), map[string]any{"q": "x"}, toolCtx); err != nil {
			t.Errorf("Execute error: %v", err)
		}
	})

	if params.ToolOAuthToken != "static-bearer-abc" {
		t.Errorf("bearer must keep delivering ToolOAuthToken, got %q", params.ToolOAuthToken)
	}
	if got := params.ToolAuthHeaders["Authorization"]; got != "Bearer static-bearer-abc" {
		t.Errorf("ToolAuthHeaders[Authorization] = %q, want %q", got, "Bearer static-bearer-abc")
	}
}

func TestRemoteToolAuth_STRReturnsAuthRequired_TriggersAuthFlow(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "str_auth_required",
		Description:    "Tool that returns AUTH_REQUIRED",
		TimeoutSeconds: 5,
		Auth: &toolauth.AuthConfig{
			Type:          "oauth2",
			CredentialKey: "str_auth_test",
			Scheme: &toolauth.OAuthScheme{
				AuthorizationURL: "https://auth.example.com/authorize",
				TokenURL:         "https://auth.example.com/token",
			},
			Credential: &toolauth.AuthCredential{ClientID: "cid"},
		},
	}
	rt := NewTool(cfg, exe)

	simulateSTR(t, brk, "str_auth_required", func(corrID, replyTo, _ string) {
		result := remotetool.InvokeResult{
			ToolResult: map[string]any{
				"status":     "error",
				"message":    "Authentication required",
				"error_code": "AUTH_REQUIRED",
			},
		}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	credSvc := newMockCredentialService()
	token := &toolauth.OAuthToken{
		AccessToken: "some-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
	}
	tokenJSON, _ := json.Marshal(token)
	_ = credSvc.Set(context.Background(), testAgent, testAgent, "user1", "str_auth_test", string(tokenJSON))

	var signalSent bool
	toolCtx := &tool.Context{
		AgentName:         testAgent,
		UserID:            "user1",
		ClientID:          "client1",
		ParentTaskID:      "task-1",
		ToolCallID:        "call-1",
		CredentialService: credSvc,
		SendSignal: func(signal any) error {
			signalSent = true
			return nil
		},
	}

	result, err := rt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if result.Message != "Pending user authorization." {
		t.Errorf("Message = %q, want %q", result.Message, "Pending user authorization.")
	}
	if !signalSent {
		t.Error("auth signal should be sent when STR returns AUTH_REQUIRED")
	}
}

func TestRemoteToolAuth_NoAuthConfig_NeverLoadsToken(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "no_auth_tool",
		TimeoutSeconds: 5,
	}
	rt := NewTool(cfg, exe)

	if rt.AuthHandler() != nil {
		t.Fatal("authHandler should be nil when no auth config")
	}

	captureQ, _ := brk.CreateQueue("capture-no-auth", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(captureQ, remotetool.InvokeTopic(testNamespace, "no_auth_tool"))

	simulateSTR(t, brk, "no_auth_tool", func(corrID, replyTo, _ string) {
		result := remotetool.InvokeResult{
			ToolResult: map[string]any{"message": "ok"},
		}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	credSvc := newMockCredentialService()
	token := &toolauth.OAuthToken{
		AccessToken: "should-not-be-delivered",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
	}
	tokenJSON, _ := json.Marshal(token)
	_ = credSvc.Set(context.Background(), testAgent, testAgent, "user1", "some_key", string(tokenJSON))

	toolCtx := &tool.Context{
		AgentName:         testAgent,
		UserID:            "user1",
		CredentialService: credSvc,
	}

	type execResult struct {
		result *tool.Result
		err    error
	}
	execCh := make(chan execResult, 1)
	go func() {
		r, e := rt.Execute(context.Background(), map[string]any{}, toolCtx)
		execCh <- execResult{r, e}
	}()

	captureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(captureCtx, captureQ)
	if err != nil {
		t.Fatalf("failed to capture invoke message: %v", err)
	}
	if msg != nil {
		msg.Ack()
		var req struct {
			Params remotetool.InvokeParams `json:"params"`
		}
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if req.Params.ToolOAuthToken != "" {
			t.Errorf("ToolOAuthToken should be empty for tool without auth config, got %q", req.Params.ToolOAuthToken)
		}
	}

	er := <-execCh
	if er.err != nil {
		t.Fatalf("Execute error: %v", er.err)
	}
}

func TestRemoteToolAuth_FullOAuthFlow(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		code := r.Form.Get("code")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "token-for-" + code,
			"refresh_token": "refresh-for-" + code,
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "full_oauth_tool",
		Description:    "Full OAuth test tool",
		TimeoutSeconds: 5,
		Auth: &toolauth.AuthConfig{
			Type:          "oauth2",
			CredentialKey: "full_oauth_key",
			Scheme: &toolauth.OAuthScheme{
				AuthorizationURL:        "https://auth.example.com/authorize",
				TokenURL:                tokenSrv.URL + "/token",
				Scopes:                  []string{"read", "write"},
				TokenEndpointAuthMethod: "client_secret_post",
			},
			Credential: &toolauth.AuthCredential{
				ClientID:     "test-client",
				ClientSecret: "test-secret",
			},
		},
	}
	rt := NewTool(cfg, exe)
	credSvc := newMockCredentialService()
	ctx := context.Background()

	t.Run("Step1_NoToken_AuthRequired", func(t *testing.T) {
		var authSignal *a2a.AuthenticationRequiredData
		toolCtx := &tool.Context{
			AgentName:         testAgent,
			UserID:            "user1",
			ClientID:          "client1",
			ParentTaskID:      "task-1",
			ToolCallID:        "call-1",
			CredentialService: credSvc,
			SendSignal: func(signal any) error {
				authSignal = signal.(*a2a.AuthenticationRequiredData)
				return nil
			},
		}

		result, err := rt.Execute(ctx, map[string]any{"q": "test"}, toolCtx)
		if err != nil {
			t.Fatalf("Execute() error: %v", err)
		}
		if result.Message != "Pending user authorization." {
			t.Errorf("Message = %q", result.Message)
		}
		if authSignal == nil {
			t.Fatal("expected auth signal")
		}
		if authSignal.CredentialKey != "full_oauth_key" {
			t.Errorf("CredentialKey = %q", authSignal.CredentialKey)
		}
		if authSignal.CodeVerifier == "" {
			t.Error("CodeVerifier should not be empty")
		}

		authResp := &a2a.AuthResponseData{
			Code:          "remote-auth-code",
			CodeVerifier:  authSignal.CodeVerifier,
			CredentialKey: authSignal.CredentialKey,
		}
		if err := rt.AuthHandler().HandleAuthResponse(ctx, authResp, toolCtx); err != nil {
			t.Fatalf("HandleAuthResponse error: %v", err)
		}

		stored, _ := credSvc.Get(ctx, testAgent, testAgent, "user1", "full_oauth_key")
		if stored == "" {
			t.Fatal("token should be stored")
		}

		var storedToken toolauth.OAuthToken
		_ = json.Unmarshal([]byte(stored), &storedToken)
		if storedToken.AccessToken != "token-for-remote-auth-code" {
			t.Errorf("stored token = %q", storedToken.AccessToken)
		}
	})

	t.Run("Step3_Retry_WithToken_Success", func(t *testing.T) {
		captureQ, _ := brk.CreateQueue("capture-full-oauth", broker.QueueOptions{Named: true})
		_ = brk.Subscribe(captureQ, remotetool.InvokeTopic(testNamespace, "full_oauth_tool"))

		simulateSTR(t, brk, "full_oauth_tool", func(corrID, replyTo, _ string) {
			result := remotetool.InvokeResult{
				ToolResult: map[string]any{"message": "authenticated result"},
			}
			resp := a2a.NewResponse(corrID, result)
			payload, _ := json.Marshal(resp)
			_ = brk.Publish(ctx, replyTo, &broker.Message{Payload: payload})
		})

		toolCtx := &tool.Context{
			AgentName:         testAgent,
			UserID:            "user1",
			CredentialService: credSvc,
		}

		type execResult struct {
			result *tool.Result
			err    error
		}
		execCh := make(chan execResult, 1)
		go func() {
			r, e := rt.Execute(ctx, map[string]any{"q": "test"}, toolCtx)
			execCh <- execResult{r, e}
		}()

		captureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(captureCtx, captureQ)
		if err != nil {
			t.Fatalf("failed to capture: %v", err)
		}
		if msg != nil {
			msg.Ack()
			var req struct {
				Params remotetool.InvokeParams `json:"params"`
			}
			_ = json.Unmarshal(msg.Payload, &req)
			if req.Params.ToolOAuthToken != "token-for-remote-auth-code" {
				t.Errorf("ToolOAuthToken = %q, want %q", req.Params.ToolOAuthToken, "token-for-remote-auth-code")
			}
		}

		er := <-execCh
		if er.err != nil {
			t.Fatalf("Execute error: %v", er.err)
		}
		if er.result.Status != tool.StatusSuccess {
			t.Errorf("Status = %q, want success", er.result.Status)
		}
	})
}

func TestRemoteToolAuth_MultiUserIsolation(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		code := r.Form.Get("code")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-for-" + code,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer tokenSrv.Close()

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	cfg := &ToolConfig{
		ToolName:       "multi_user_remote",
		TimeoutSeconds: 5,
		Auth: &toolauth.AuthConfig{
			Type:          "oauth2",
			CredentialKey: "multi_user_key",
			Scheme: &toolauth.OAuthScheme{
				AuthorizationURL:        "https://auth.example.com/authorize",
				TokenURL:                tokenSrv.URL + "/token",
				TokenEndpointAuthMethod: "client_secret_post",
			},
			Credential: &toolauth.AuthCredential{ClientID: "cid", ClientSecret: "cs"},
		},
	}
	rt := NewTool(cfg, nil)
	credSvc := newMockCredentialService()
	ctx := context.Background()

	toolCtxA := &tool.Context{
		AgentName: testAgent, UserID: "userA", ClientID: "c",
		CredentialService: credSvc,
	}
	_ = rt.AuthHandler().HandleAuthResponse(ctx, &a2a.AuthResponseData{
		Code: "codeA", CodeVerifier: "vA", CredentialKey: "multi_user_key",
	}, toolCtxA)

	toolCtxB := &tool.Context{
		AgentName: testAgent, UserID: "userB", ClientID: "c",
		CredentialService: credSvc,
	}
	_ = rt.AuthHandler().HandleAuthResponse(ctx, &a2a.AuthResponseData{
		Code: "codeB", CodeVerifier: "vB", CredentialKey: "multi_user_key",
	}, toolCtxB)

	tokenA, err := rt.AuthHandler().GetOAuthToken(ctx, toolCtxA)
	if err != nil {
		t.Fatalf("GetOAuthToken userA error: %v", err)
	}
	tokenB, err := rt.AuthHandler().GetOAuthToken(ctx, toolCtxB)
	if err != nil {
		t.Fatalf("GetOAuthToken userB error: %v", err)
	}

	if tokenA.AccessToken != "token-for-codeA" {
		t.Errorf("User A token = %q, want token-for-codeA", tokenA.AccessToken)
	}
	if tokenB.AccessToken != "token-for-codeB" {
		t.Errorf("User B token = %q, want token-for-codeB", tokenB.AccessToken)
	}
}

func TestRemoteToolAuth_AuthHandler_ReturnsNilWhenNoConfig(t *testing.T) {
	cfg := &ToolConfig{
		ToolName: "no_auth",
	}
	rt := NewTool(cfg, nil)

	if rt.AuthHandler() != nil {
		t.Error("AuthHandler should be nil when no auth config")
	}
}

func TestRemoteToolAuth_AuthHandler_ReturnsHandlerWhenConfigured(t *testing.T) {
	cfg := &ToolConfig{
		ToolName: "with_auth",
		Auth: &toolauth.AuthConfig{
			Type:          "oauth2",
			CredentialKey: "key",
		},
	}
	rt := NewTool(cfg, nil)

	handler := rt.AuthHandler()
	if handler == nil {
		t.Fatal("AuthHandler should not be nil when auth config is present")
	}
	if handler.CredentialKey() != "key" {
		t.Errorf("CredentialKey = %q", handler.CredentialKey())
	}
}

func TestParseToolConfig_AuthBlock(t *testing.T) {
	raw := map[string]any{
		"tool_name":        "my_tool",
		"tool_description": "My tool",
		"auth": map[string]any{
			"type": "oauth2",
			"scheme": map[string]any{
				"authorization_url": "https://auth.example.com/authorize",
				"token_url":         "https://auth.example.com/token",
				"scopes":            []any{"read", "write"},
			},
			"credential": map[string]any{
				"client_id":     "my-client",
				"client_secret": "my-secret",
			},
			"credential_key": "my_oauth_key",
		},
	}

	cfg, err := ParseToolConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolConfig error: %v", err)
	}

	if cfg.Auth == nil {
		t.Fatal("Auth should not be nil")
	}
	if cfg.Auth.Type != "oauth2" {
		t.Errorf("Type = %q", cfg.Auth.Type)
	}
	if cfg.Auth.CredentialKey != "my_oauth_key" {
		t.Errorf("CredentialKey = %q", cfg.Auth.CredentialKey)
	}
	if cfg.Auth.Scheme == nil {
		t.Fatal("Scheme should not be nil")
	}
	if cfg.Auth.Scheme.AuthorizationURL != "https://auth.example.com/authorize" {
		t.Errorf("AuthorizationURL = %q", cfg.Auth.Scheme.AuthorizationURL)
	}
	if cfg.Auth.Credential == nil {
		t.Fatal("Credential should not be nil")
	}
	if cfg.Auth.Credential.ClientID != "my-client" {
		t.Errorf("ClientID = %q", cfg.Auth.Credential.ClientID)
	}
}

func TestParseToolConfig_NoAuthBlock(t *testing.T) {
	raw := map[string]any{
		"tool_name":        "simple_tool",
		"tool_description": "Simple tool",
	}

	cfg, err := ParseToolConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolConfig error: %v", err)
	}

	if cfg.Auth != nil {
		t.Error("Auth should be nil when not configured")
	}
}

var _ = slog.Default
