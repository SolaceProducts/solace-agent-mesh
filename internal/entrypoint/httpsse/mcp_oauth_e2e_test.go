// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func TestMCPOAuth2_E2E_GatewayFullFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	accessToken := "gw-e2e-access-token-12345"
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("grant_type") != "authorization_code" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "unsupported_grant_type"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  accessToken,
			"refresh_token": "gw-e2e-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	mcpSrv, err := harness.NewMockMCPAuthServer(accessToken)
	if err != nil {
		t.Fatalf("start mock MCP server: %v", err)
	}
	defer mcpSrv.Close()

	brk := broker.NewDevBroker()
	if connectErr := brk.Connect(ctx); connectErr != nil {
		t.Fatalf("connect broker: %v", connectErr)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	credSvc := newGWE2ECredentialService()
	argsJSON := `{"query":"test data"}`
	llmResponses := []*llm.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: idgen.New(), Name: "auth_protected_tool", Arguments: argsJSON}}},
		{Content: "I need you to authenticate first.", FinishReason: "stop"},
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: idgen.New(), Name: "auth_protected_tool", Arguments: argsJSON}}},
		{Content: "Successfully retrieved: Authenticated result for query: test data", FinishReason: "stop"},
	}
	llmClient := mockllm.NewFIFOClient(llmResponses)

	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLLM("", llmClient),
		runtime.WithSessions(session.NewMemoryStore()),
		runtime.WithCredentialService(credSvc),
	)

	adapter := NewHTTPSSEAdapter()
	appConfig := map[string]any{
		"namespace":          "test-ns",
		"session_secret_key": "test-secret-key-1234567890",
		"gateway_id":         "e2e-gw",
		"fastapi_host":       "127.0.0.1",
		"fastapi_port":       0,
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)
	core.SetServices(rt.Services())

	if initErr := core.Init(ctx, nil); initErr != nil {
		t.Fatalf("gateway init: %v", initErr)
	}
	if startErr := core.Start(ctx); startErr != nil {
		t.Fatalf("gateway start: %v", startErr)
	}
	defer func() { _ = core.Stop(ctx) }()

	gwServer := httptest.NewServer(adapter.Handler())
	defer gwServer.Close()
	gwAddr := strings.TrimPrefix(gwServer.URL, "http://")
	t.Logf("Gateway listening on %s", gwAddr)

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", fmt.Sprintf("http://%s/api/v1/auth/tool/callback", gwAddr))

	agentCfg := agent.AgentConfig{
		Name:              "E2EGWAuthAgent",
		SystemPrompt:      "You are a helpful assistant.",
		Model:             "test-model",
		MaxTokens:         4096,
		MaxLLMCalls:       10,
		Namespace:         "test-ns",
		SupportsStreaming: true,
	}
	ag := agent.New(agentCfg)
	if agentInitErr := ag.Init(ctx, nil, rt.Services()); agentInitErr != nil {
		t.Fatalf("agent init: %v", agentInitErr)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type": "mcp",
			"connection_params": map[string]any{
				"type": "streamable-http",
				"url":  mcpSrv.MCPURL(),
			},
			"auth": map[string]any{
				"type": "oauth2",
				"scheme": map[string]any{
					"authorization_url":          "https://auth.example.com/authorize",
					"token_url":                  tokenSrv.URL + "/token",
					"scopes":                     []any{"read", "write"},
					"token_endpoint_auth_method": "client_secret_post",
				},
				"credential": map[string]any{
					"client_id":     "gw-e2e-client",
					"client_secret": "gw-e2e-secret",
				},
				"credential_key": "e2e_mcp_oauth",
			},
			"manifest": gwAuthProtectedToolManifest(),
		},
	}
	if _, regErr := awe.RegisterTools(ag, toolsCfg, rt.Services()); regErr != nil {
		t.Fatalf("register tools: %v", regErr)
	}
	if agentStartErr := ag.Start(ctx); agentStartErr != nil {
		t.Fatalf("agent start: %v", agentStartErr)
	}
	defer func() { _ = ag.Stop(ctx) }()

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "E2EGWAuthAgent",
		URL:  "solace:test",
	})

	t.Log("Step 1: Submitting task via HTTP POST")
	taskID := gwSubmitTask(t, gwAddr, "Search for test data using the auth tool", "E2EGWAuthAgent")
	t.Logf("Step 1: Task submitted, taskID=%s", taskID)

	t.Log("Step 2: Subscribing to SSE, looking for authentication_required signal")
	authURI := gwCollectAuthSignalFromSSE(ctx, t, gwAddr, taskID)
	t.Logf("Step 2: Got auth_uri: %s", authURI[:min(80, len(authURI))]+"...")

	parsedAuthURI, err := url.Parse(authURI)
	if err != nil {
		t.Fatalf("parse auth_uri: %v", err)
	}
	state := parsedAuthURI.Query().Get("state")
	if state == "" {
		t.Fatal("auth_uri missing state parameter")
	}
	t.Logf("Step 2: Extracted state=%s", state[:8]+"...")

	t.Log("Step 3: Simulating OAuth callback")
	gwHitOAuthCallback(t, gwAddr, "gw-e2e-mock-auth-code", state)
	t.Log("Step 3: OAuth callback returned success")

	t.Log("Step 4: Waiting for token storage")
	gwWaitForToken(ctx, t, credSvc, "E2EGWAuthAgent", "sam_dev_user", "e2e_mcp_oauth")
	gwWaitForFinalResponse(ctx, t, gwAddr, taskID)
	t.Log("Step 4: Token stored successfully")

	t.Log("Step 5: Submitting retry task via HTTP POST")
	taskID2 := gwSubmitTask(t, gwAddr, "Search for test data using the auth tool", "E2EGWAuthAgent")
	t.Logf("Step 5: Retry task submitted, taskID=%s", taskID2)

	gwWaitForFinalResponse(ctx, t, gwAddr, taskID2)
	t.Log("Step 5: Retry task completed")

	if mcpSrv.ToolCallCount() < 1 {
		t.Error("Step 6: MCP server should have received at least one tool call")
	}
	if !mcpSrv.HasValidAuthHeader() {
		t.Errorf("Step 6: MCP server last auth header = %q, expected valid Bearer token", mcpSrv.LastAuthHeader())
	}
	t.Logf("Step 6: MCP server tool calls: %d, last auth header valid: %v",
		mcpSrv.ToolCallCount(), mcpSrv.HasValidAuthHeader())

	t.Log("Gateway E2E OAuth2 flow completed successfully!")
}

func gwSubmitTask(t *testing.T, gwAddr, text, agentName string) string {
	t.Helper()

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-" + fmt.Sprint(time.Now().UnixNano()),
		"params": map[string]any{
			"message": map[string]any{
				"kind": "message",
				"role": "user",
				"parts": []map[string]any{
					{"kind": "text", "text": text},
				},
				"metadata": map[string]any{"agent_name": agentName},
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/message:stream", gwAddr),
		"application/json",
		strings.NewReader(string(bodyBytes)),
	)
	if err != nil {
		t.Fatalf("POST message:stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST message:stream returned %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpcResp.Result.ID == "" {
		t.Fatal("response missing task ID")
	}
	return rpcResp.Result.ID
}

func gwCollectAuthSignalFromSSE(ctx context.Context, t *testing.T, gwAddr, taskID string) string {
	t.Helper()

	sseCtx, sseCancel := context.WithTimeout(ctx, 15*time.Second)
	defer sseCancel()

	req, _ := http.NewRequestWithContext(sseCtx, "GET",
		fmt.Sprintf("http://%s/api/v1/sse/subscribe/%s", gwAddr, taskID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE subscribe: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE subscribe returned %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	var eventType, eventData string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			eventData = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		} else if line == "" && eventData != "" {
			if eventType == "status_update" {
				if authURI := extractAuthURIFromSSEData(eventData); authURI != "" {
					return authURI
				}
			}
			eventType = ""
			eventData = ""
		}
	}

	t.Fatal("SSE stream ended without authentication_required signal")
	return ""
}

func extractAuthURIFromSSEData(data string) string {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(data), &envelope); err != nil || envelope.Result == nil {
		return ""
	}

	var event a2a.TaskStatusUpdateEvent
	if err := json.Unmarshal(envelope.Result, &event); err != nil {
		return ""
	}
	if event.Status.Message == nil {
		return ""
	}

	for _, part := range event.Status.Message.Parts {
		if part.Kind != "data" || part.Data == nil {
			continue
		}
		dataMap, ok := part.Data.(map[string]any)
		if !ok {
			continue
		}
		if dataMap["type"] != "authentication_required" {
			continue
		}
		if authURI, ok := dataMap["auth_uri"].(string); ok && authURI != "" {
			return authURI
		}
	}
	return ""
}

func gwHitOAuthCallback(t *testing.T, gwAddr, code, state string) {
	t.Helper()

	callbackURL := fmt.Sprintf("http://%s/api/v1/auth/tool/callback?code=%s&state=%s",
		gwAddr, url.QueryEscape(code), url.QueryEscape(state))

	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("OAuth callback GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OAuth callback returned %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "Authentication Successful") {
		t.Fatalf("OAuth callback body doesn't contain success message: %s", string(body))
	}
	if strings.Contains(strings.ToLower(string(body)), "retry") {
		t.Fatalf("OAuth callback page asks the user to retry, but the task resumes by itself: %s", string(body))
	}
}

func gwWaitForToken(ctx context.Context, t *testing.T, credSvc *gwE2ECredentialService, agentName, userID, key string) {
	t.Helper()

	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for token storage")
		case <-ctx.Done():
			t.Fatal("context cancelled waiting for token storage")
		case <-ticker.C:
			token, _ := credSvc.Get(ctx, agentName, agentName, userID, key)
			if token != "" {
				return
			}
		}
	}
}

func gwWaitForFinalResponse(ctx context.Context, t *testing.T, gwAddr, taskID string) {
	t.Helper()

	sseCtx, sseCancel := context.WithTimeout(ctx, 15*time.Second)
	defer sseCancel()

	req, _ := http.NewRequestWithContext(sseCtx, "GET",
		fmt.Sprintf("http://%s/api/v1/sse/subscribe/%s", gwAddr, taskID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE subscribe for final response: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if line == "" && eventType == "final_response" {
			return
		}
	}

	t.Fatal("SSE stream ended without final_response event")
}

type gwE2ECredentialService struct {
	mu    sync.Mutex
	store map[string]string
}

func newGWE2ECredentialService() *gwE2ECredentialService {
	return &gwE2ECredentialService{store: make(map[string]string)}
}

func (s *gwE2ECredentialService) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store[agentName+"|"+appName+"|"+userID+"|"+key], nil
}

func (s *gwE2ECredentialService) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[agentName+"|"+appName+"|"+userID+"|"+key] = value
	return nil
}

func (s *gwE2ECredentialService) Delete(_ context.Context, agentName, appName, userID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, agentName+"|"+appName+"|"+userID+"|"+key)
	return nil
}

func (s *gwE2ECredentialService) List(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

var _ auth.CredentialService = (*gwE2ECredentialService)(nil)

type gwOAuthTestEnv struct {
	provider  *harness.MockOAuthProvider
	mcpSrv    *harness.MockMCPAuthServer
	brk       broker.Broker
	credSvc   *gwE2ECredentialService
	core      *entrypoint.GatewayCore
	gwAddr    string
	agentName string
	credKey   string
	clientID  string
}

func newGWOAuthTestEnv(ctx context.Context, t *testing.T, providerCfg harness.MockOAuthProviderConfig, llmResponses []*llm.ChatResponse) *gwOAuthTestEnv {
	t.Helper()

	if providerCfg.ClientID == "" {
		providerCfg.ClientID = "gw-oauth-client"
	}
	if providerCfg.ClientSecret == "" {
		providerCfg.ClientSecret = "gw-oauth-secret"
	}

	provider, err := harness.NewMockOAuthProvider(providerCfg)
	if err != nil {
		t.Fatalf("start OAuth provider: %v", err)
	}
	t.Cleanup(provider.Close)

	mcpSrv, err := harness.NewMockMCPAuthServerWithProvider(provider)
	if err != nil {
		t.Fatalf("start MCP server: %v", err)
	}
	t.Cleanup(mcpSrv.Close)

	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(ctx) })

	credSvc := newGWE2ECredentialService()
	llmClient := mockllm.NewFIFOClient(llmResponses)

	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLLM("", llmClient),
		runtime.WithSessions(session.NewMemoryStore()),
		runtime.WithCredentialService(credSvc),
	)

	adapter := NewHTTPSSEAdapter()
	appConfig := map[string]any{
		"namespace":          "test-ns",
		"session_secret_key": "test-secret-key-1234567890",
		"gateway_id":         "e2e-gw",
		"fastapi_host":       "127.0.0.1",
		"fastapi_port":       0,
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)
	core.SetServices(rt.Services())

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("gateway init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("gateway start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(ctx) })

	gwServer := httptest.NewServer(adapter.Handler())
	t.Cleanup(gwServer.Close)
	gwAddr := strings.TrimPrefix(gwServer.URL, "http://")
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", fmt.Sprintf("http://%s/api/v1/auth/tool/callback", gwAddr))

	agentName := "GWOAuthAgent"
	credKey := "gw_mcp_oauth"

	agentCfg := agent.AgentConfig{
		Name:              agentName,
		SystemPrompt:      "You are a helpful assistant.",
		Model:             "test-model",
		MaxTokens:         4096,
		MaxLLMCalls:       10,
		Namespace:         "test-ns",
		SupportsStreaming: true,
	}
	ag := agent.New(agentCfg)
	if err := ag.Init(ctx, nil, rt.Services()); err != nil {
		t.Fatalf("agent init: %v", err)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type": "mcp",
			"connection_params": map[string]any{
				"type": "streamable-http",
				"url":  mcpSrv.MCPURL(),
			},
			"auth": map[string]any{
				"type": "oauth2",
				"scheme": map[string]any{
					"authorization_url":          "https://auth.example.com/authorize",
					"token_url":                  provider.TokenURL(),
					"scopes":                     []any{"read", "write"},
					"token_endpoint_auth_method": "client_secret_post",
				},
				"credential": map[string]any{
					"client_id":     providerCfg.ClientID,
					"client_secret": providerCfg.ClientSecret,
				},
				"credential_key": credKey,
			},
			"manifest": gwAuthProtectedToolManifest(),
		},
	}
	if _, err := awe.RegisterTools(ag, toolsCfg, rt.Services()); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if err := ag.Start(ctx); err != nil {
		t.Fatalf("agent start: %v", err)
	}
	t.Cleanup(func() { _ = ag.Stop(ctx) })

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: agentName,
		URL:  "solace:test",
	})

	return &gwOAuthTestEnv{
		provider:  provider,
		mcpSrv:    mcpSrv,
		brk:       brk,
		credSvc:   credSvc,
		core:      core,
		gwAddr:    gwAddr,
		agentName: agentName,
		credKey:   credKey,
		clientID:  providerCfg.ClientID,
	}
}

func (env *gwOAuthTestEnv) gwDoAuthFlow(ctx context.Context, t *testing.T, userID string) {
	t.Helper()

	taskID := gwSubmitTaskAs(t, env.gwAddr, "Search for test data", env.agentName, userID)
	authURI := gwCollectAuthSignalFromSSE(ctx, t, env.gwAddr, taskID)

	parsed, err := url.Parse(authURI)
	if err != nil {
		t.Fatalf("parse auth_uri: %v", err)
	}
	state := parsed.Query().Get("state")
	codeChallenge := parsed.Query().Get("code_challenge")
	redirectURI := parsed.Query().Get("redirect_uri")

	code := env.provider.IssueAuthorizationCode(userID, codeChallenge, redirectURI, env.clientID)
	gwHitOAuthCallback(t, env.gwAddr, code, state)
	gwWaitForToken(ctx, t, env.credSvc, env.agentName, userID, env.credKey)
	gwWaitForFinalResponse(ctx, t, env.gwAddr, taskID)
}

func gwSubmitTaskAs(t *testing.T, gwAddr, text, agentName, userID string) string {
	t.Helper()

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-" + fmt.Sprint(time.Now().UnixNano()),
		"params": map[string]any{
			"message": map[string]any{
				"kind": "message",
				"role": "user",
				"parts": []map[string]any{
					{"kind": "text", "text": text},
				},
				"metadata": map[string]any{"agent_name": agentName},
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest("POST",
		fmt.Sprintf("http://%s/api/v1/message:stream", gwAddr),
		strings.NewReader(string(bodyBytes)))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("X-SAM-User-ID", userID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST message:stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST message:stream returned %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpcResp.Result.ID == "" {
		t.Fatal("response missing task ID")
	}
	return rpcResp.Result.ID
}

func gwAuthProtectedToolManifest() []any {
	return []any{
		map[string]any{
			"name":        "auth_protected_tool",
			"description": "An auth-gated tool used by OAuth gateway e2e tests.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
			},
		},
	}
}

func gwMakeLLMResponses(authTasks, successTasks int) []*llm.ChatResponse {
	argsJSON := `{"query":"test data"}`
	var responses []*llm.ChatResponse

	for i := range authTasks {
		responses = append(responses,
			&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("call_auth_%d", i), Name: "auth_protected_tool", Arguments: argsJSON}}},
			&llm.ChatResponse{Content: "I need you to authenticate first.", FinishReason: "stop"},
		)
	}
	for i := range successTasks {
		responses = append(responses,
			&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("call_ok_%d", i), Name: "auth_protected_tool", Arguments: argsJSON}}},
			&llm.ChatResponse{Content: "Successfully retrieved the data.", FinishReason: "stop"},
		)
	}
	return responses
}

func gwWaitForFinalResponseErr(ctx context.Context, gwAddr, taskID string) error {
	sseCtx, sseCancel := context.WithTimeout(ctx, 30*time.Second)
	defer sseCancel()

	req, _ := http.NewRequestWithContext(sseCtx, "GET",
		fmt.Sprintf("http://%s/api/v1/sse/subscribe/%s", gwAddr, taskID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("SSE subscribe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE subscribe returned %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if line == "" && eventType == "final_response" {
			return nil
		}
	}
	return fmt.Errorf("SSE stream ended without final_response event")
}

func TestMCPOAuth2_E2E_GatewayMultiUserIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	env := newGWOAuthTestEnv(ctx, t, harness.MockOAuthProviderConfig{}, gwMakeLLMResponses(2, 2))

	t.Log("User A: starting auth flow")
	env.gwDoAuthFlow(ctx, t, "userA")

	t.Log("User B: starting auth flow")
	env.gwDoAuthFlow(ctx, t, "userB")

	tokenA, _ := env.credSvc.Get(ctx, env.agentName, env.agentName, "userA", env.credKey)
	tokenB, _ := env.credSvc.Get(ctx, env.agentName, env.agentName, "userB", env.credKey)
	if tokenA == "" {
		t.Fatal("userA token not stored")
	}
	if tokenB == "" {
		t.Fatal("userB token not stored")
	}
	if tokenA == tokenB {
		t.Error("userA and userB should have different stored tokens")
	}

	t.Log("User A: submitting retry task")
	taskA2 := gwSubmitTaskAs(t, env.gwAddr, "Retry search", env.agentName, "userA")
	gwWaitForFinalResponse(ctx, t, env.gwAddr, taskA2)

	t.Log("User B: submitting retry task")
	taskB2 := gwSubmitTaskAs(t, env.gwAddr, "Retry search", env.agentName, "userB")
	gwWaitForFinalResponse(ctx, t, env.gwAddr, taskB2)

	if c := env.provider.AuthCodeGrantCount(); c != 2 {
		t.Errorf("expected 2 auth_code grants, got %d", c)
	}

	providerTokenA := env.provider.AccessTokenForUser("userA")
	providerTokenB := env.provider.AccessTokenForUser("userB")
	if providerTokenA == "" || providerTokenB == "" {
		t.Error("provider should have tokens for both users")
	}
	if providerTokenA == providerTokenB {
		t.Error("provider should have issued different tokens for userA and userB")
	}

	authHeaders := env.mcpSrv.AuthHeaders()
	if len(authHeaders) < 2 {
		t.Errorf("expected at least 2 auth headers on MCP server, got %d", len(authHeaders))
	}

	if ec := env.provider.ErrorCount(); ec > 0 {
		t.Errorf("expected 0 provider errors, got %d", ec)
		for _, log := range env.provider.TokenRequestLogs() {
			if !log.Success {
				t.Logf("  failed request: grant_type=%s user=%s error=%s", log.GrantType, log.UserID, log.ErrorCode)
			}
		}
	}
}

func TestMCPOAuth2_E2E_ConcurrentMultiUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	numUsers := 5
	env := newGWOAuthTestEnv(ctx, t, harness.MockOAuthProviderConfig{}, gwMakeLLMResponses(numUsers, numUsers))

	for i := range numUsers {
		userID := fmt.Sprintf("concurrent-user-%d", i)
		t.Logf("Auth flow for %s", userID)
		env.gwDoAuthFlow(ctx, t, userID)
	}

	taskIDs := make([]string, numUsers)
	for i := range numUsers {
		userID := fmt.Sprintf("concurrent-user-%d", i)
		taskIDs[i] = gwSubmitTaskAs(t, env.gwAddr, "Retry search", env.agentName, userID)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, numUsers)
	for i := range numUsers {
		wg.Go(func() {
			if err := gwWaitForFinalResponseErr(ctx, env.gwAddr, taskIDs[i]); err != nil {
				errCh <- fmt.Errorf("concurrent-user-%d: %w", i, err)
			}
		})
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	if c := env.provider.AuthCodeGrantCount(); c != numUsers {
		t.Errorf("expected %d auth_code grants, got %d", numUsers, c)
	}

	tokens := make(map[string]bool)
	for i := range numUsers {
		userID := fmt.Sprintf("concurrent-user-%d", i)
		token, _ := env.credSvc.Get(ctx, env.agentName, env.agentName, userID, env.credKey)
		if token == "" {
			t.Errorf("user %s has no stored token", userID)
			continue
		}
		tokens[token] = true
	}
	if len(tokens) != numUsers {
		t.Errorf("expected %d distinct tokens, got %d", numUsers, len(tokens))
	}

	if ec := env.provider.ErrorCount(); ec > 0 {
		t.Errorf("expected 0 provider errors, got %d", ec)
	}
}
