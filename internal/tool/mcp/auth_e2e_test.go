// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func TestMCPOAuth2_E2E_PKCEFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	provider, err := harness.NewMockOAuthProvider(harness.MockOAuthProviderConfig{
		ClientID:           "pkce-fail-client",
		ClientSecret:       "pkce-fail-secret",
		TokenExpirySeconds: 3600,
	})
	if err != nil {
		t.Fatalf("start provider: %v", err)
	}
	defer provider.Close()

	mcpSrv, err := harness.NewMockMCPAuthServerWithProvider(provider)
	if err != nil {
		t.Fatalf("start mock MCP server: %v", err)
	}
	defer mcpSrv.Close()

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	brk := broker.NewDevBroker()
	if connErr := brk.Connect(ctx); connErr != nil {
		t.Fatalf("connect broker: %v", connErr)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	argsJSON := `{"query":"pkce fail test"}`
	llmResponses := []*llm.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "auth_protected_tool", Arguments: argsJSON}}},
		{Content: "Please authenticate.", FinishReason: "stop"},
	}
	llmClient := mockllm.NewFIFOClient(llmResponses)

	credSvc := newE2ECredentialService()
	svc := &e2eServices{
		brk: brk, llmMap: map[string]llm.Client{"": llmClient},
		sessions: session.NewMemoryStore(), credSvc: credSvc,
	}

	agentCfg := agent.AgentConfig{
		Name: "PKCEFailAgent", SystemPrompt: "You are a helpful assistant.",
		Model: "test-model", MaxTokens: 4096, MaxLLMCalls: 10,
		Namespace: "test-ns", SupportsStreaming: true,
	}
	ag := agent.New(agentCfg)
	if initErr := ag.Init(ctx, nil, svc); initErr != nil {
		t.Fatalf("agent init: %v", initErr)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":         "mcp",
			"connection_params": mcpSrv.ConnectionParams(),
			"auth": map[string]any{
				"type": "oauth2",
				"scheme": map[string]any{
					"authorization_url":          provider.AuthorizationURL(),
					"token_url":                  provider.TokenURL(),
					"scopes":                     []any{"read"},
					"token_endpoint_auth_method": "client_secret_post",
				},
				"credential":     map[string]any{"client_id": "pkce-fail-client", "client_secret": "pkce-fail-secret"},
				"credential_key": "pkce_fail_oauth",
			},
			"manifest": authProtectedToolManifest(),
		},
	}
	if _, regErr := awe.RegisterTools(ag, toolsCfg, svc); regErr != nil {
		t.Fatalf("register tools: %v", regErr)
	}
	if startErr := ag.Start(ctx); startErr != nil {
		t.Fatalf("agent start: %v", startErr)
	}
	defer func() { _ = ag.Stop(ctx) }()

	userID := "pkce-fail-user"
	authSignals := collectAuthSignals(ctx, t, brk, "test-ns")

	respQueue := publishTaskForAuthFlow(ctx, t, brk, "test-ns", "PKCEFailAgent", "pkcefail-task-1", "Use the auth tool", userID)

	if !waitFor(t, 2*time.Second, func() bool { return len(authSignals()) > 0 }) {
		t.Fatal("expected authentication_required signal")
	}
	authSignal := authSignals()[0]

	codeChallenge := extractQueryParam(t, authSignal.AuthURI, "code_challenge")
	code := provider.IssueAuthorizationCode(userID, codeChallenge,
		"http://localhost:8000/api/v1/auth/tool/callback", "pkce-fail-client")

	sendAuthResponseWithVerifier(ctx, t, brk, "test-ns", "PKCEFailAgent", authSignal, code, "totally-wrong-verifier")

	waitFor(t, 2*time.Second, func() bool {
		got, _ := credSvc.Get(ctx, "PKCEFailAgent", "PKCEFailAgent", userID, "pkce_fail_oauth")
		return got != ""
	})

	storedToken, _ := credSvc.Get(ctx, "PKCEFailAgent", "PKCEFailAgent", userID, "pkce_fail_oauth")
	if storedToken == "" {
		t.Error("token should be stored — agent must ignore tampered code_verifier in auth_response and use its own stored verifier")
	}
	if provider.AuthCodeGrantCount() != 1 {
		t.Errorf("expected exactly 1 successful auth code grant (stored verifier should match challenge), got %d", provider.AuthCodeGrantCount())
	}

	if _, recvErr := brk.Receive(ctx, respQueue); recvErr != nil {
		t.Fatalf("receive paused-task final response: %v", recvErr)
	}
}

func TestMCPOAuth2_E2E_CodeReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	provider, err := harness.NewMockOAuthProvider(harness.MockOAuthProviderConfig{
		ClientID:           "replay-client",
		ClientSecret:       "replay-secret",
		TokenExpirySeconds: 3600,
	})
	if err != nil {
		t.Fatalf("start provider: %v", err)
	}
	defer provider.Close()

	mcpSrv, err := harness.NewMockMCPAuthServerWithProvider(provider)
	if err != nil {
		t.Fatalf("start mock MCP server: %v", err)
	}
	defer mcpSrv.Close()

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	brk := broker.NewDevBroker()
	if connErr := brk.Connect(ctx); connErr != nil {
		t.Fatalf("connect broker: %v", connErr)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	argsJSON := `{"query":"replay test"}`
	llmResponses := []*llm.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "auth_protected_tool", Arguments: argsJSON}}},
		{Content: "Please authenticate.", FinishReason: "stop"},
	}
	llmClient := mockllm.NewFIFOClient(llmResponses)

	credSvc := newE2ECredentialService()
	svc := &e2eServices{
		brk: brk, llmMap: map[string]llm.Client{"": llmClient},
		sessions: session.NewMemoryStore(), credSvc: credSvc,
	}

	agentCfg := agent.AgentConfig{
		Name: "ReplayAgent", SystemPrompt: "You are a helpful assistant.",
		Model: "test-model", MaxTokens: 4096, MaxLLMCalls: 10,
		Namespace: "test-ns", SupportsStreaming: true,
	}
	ag := agent.New(agentCfg)
	if initErr := ag.Init(ctx, nil, svc); initErr != nil {
		t.Fatalf("agent init: %v", initErr)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":         "mcp",
			"connection_params": mcpSrv.ConnectionParams(),
			"auth": map[string]any{
				"type": "oauth2",
				"scheme": map[string]any{
					"authorization_url":          provider.AuthorizationURL(),
					"token_url":                  provider.TokenURL(),
					"scopes":                     []any{"read"},
					"token_endpoint_auth_method": "client_secret_post",
				},
				"credential":     map[string]any{"client_id": "replay-client", "client_secret": "replay-secret"},
				"credential_key": "replay_oauth",
			},
			"manifest": authProtectedToolManifest(),
		},
	}
	if _, regErr := awe.RegisterTools(ag, toolsCfg, svc); regErr != nil {
		t.Fatalf("register tools: %v", regErr)
	}
	if startErr := ag.Start(ctx); startErr != nil {
		t.Fatalf("agent start: %v", startErr)
	}
	defer func() { _ = ag.Stop(ctx) }()

	userID := "replay-user"
	authSignals := collectAuthSignals(ctx, t, brk, "test-ns")

	respQueue := publishTaskForAuthFlow(ctx, t, brk, "test-ns", "ReplayAgent", "replay-task-1", "Use the auth tool", userID)

	if !waitFor(t, 2*time.Second, func() bool { return len(authSignals()) > 0 }) {
		t.Fatal("expected authentication_required signal")
	}
	authSignal := authSignals()[0]

	codeChallenge := extractQueryParam(t, authSignal.AuthURI, "code_challenge")
	code := provider.IssueAuthorizationCode(userID, codeChallenge,
		"http://localhost:8000/api/v1/auth/tool/callback", "replay-client")

	sendAuthResponse(ctx, t, brk, "test-ns", "ReplayAgent", authSignal, code)

	if !waitFor(t, 2*time.Second, func() bool {
		tok, _ := credSvc.Get(ctx, "ReplayAgent", "ReplayAgent", userID, "replay_oauth")
		return tok != ""
	}) {
		t.Fatal("token should be stored after first auth_response")
	}

	sendAuthResponse(ctx, t, brk, "test-ns", "ReplayAgent", authSignal, code)

	time.Sleep(200 * time.Millisecond)

	if provider.AuthCodeGrantCount() != 1 {
		t.Errorf("expected 1 successful auth code grant, got %d", provider.AuthCodeGrantCount())
	}
	if provider.ErrorCount() != 0 {
		t.Errorf("expected 0 provider errors (dedup should intercept replay), got %d", provider.ErrorCount())
	}

	if _, recvErr := brk.Receive(ctx, respQueue); recvErr != nil {
		t.Fatalf("receive paused-task final response: %v", recvErr)
	}
}

type e2eCredentialService struct {
	mu    sync.Mutex
	store map[string]string
}

func newE2ECredentialService() *e2eCredentialService {
	return &e2eCredentialService{store: make(map[string]string)}
}

func (s *e2eCredentialService) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store[agentName+"|"+appName+"|"+userID+"|"+key], nil
}

func (s *e2eCredentialService) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[agentName+"|"+appName+"|"+userID+"|"+key] = value
	return nil
}

func (s *e2eCredentialService) Delete(_ context.Context, agentName, appName, userID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, agentName+"|"+appName+"|"+userID+"|"+key)
	return nil
}

func (s *e2eCredentialService) List(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

type e2eServices struct {
	brk      broker.Broker
	llmMap   map[string]llm.Client
	sessions session.Store
	credSvc  *e2eCredentialService
}

func (s *e2eServices) Broker() broker.Broker                     { return s.brk }
func (s *e2eServices) Config() config.Config                     { return nil }
func (s *e2eServices) KVStore() runtime.KVStore                  { return runtime.NewMapKVStore() }
func (s *e2eServices) Cache() runtime.Cache                      { return runtime.NewMapCache() }
func (s *e2eServices) Metrics() runtime.Metrics                  { return runtime.NoopMetrics{} }
func (s *e2eServices) Tracer() runtime.Tracer                    { return runtime.NoopTracer{} }
func (s *e2eServices) LLM(_ string) llm.Client                   { return s.llmMap[""] }
func (s *e2eServices) Artifacts() *artifact.ScopedService        { return nil }
func (s *e2eServices) Sessions() session.Store                   { return s.sessions }
func (s *e2eServices) Authenticator() auth.Authenticator         { return auth.NoopAuthenticator{} }
func (s *e2eServices) ToolAuthorizer() auth.ToolAuthorizer       { return auth.NoopToolAuthorizer{} }
func (s *e2eServices) AgentAuthorizer() auth.AgentAuthorizer     { return auth.NoopAgentAuthorizer{} }
func (s *e2eServices) AuditLogger() auth.AuditLogger             { return auth.NoopAuditLogger{} }
func (s *e2eServices) CredentialService() auth.CredentialService { return s.credSvc }
func (s *e2eServices) ControlAuthorizer() auth.ControlAuthorizer { return auth.NoopControlAuthorizer{} }
func (s *e2eServices) Volumes() volume.Service                   { return nil }
func (s *e2eServices) BudgetGate() *budget.Gate                  { return nil }
func (s *e2eServices) HTTPClient() *http.Client                  { return http.DefaultClient }
func (s *e2eServices) FeatureRegistry() *features.Registry       { return nil }

func (s *e2eServices) UsageReporter() runtime.Component               { return nil }
func (s *e2eServices) ModelResolver() runtime.ModelAliasResolver      { return nil }
func (s *e2eServices) Bindings() runtime.ModelBindings                { return nil }
func (s *e2eServices) ConnectorSpecStore() runtime.ConnectorSpecStore { return nil }
func (s *e2eServices) Logger() *slog.Logger                           { return slog.Default() }

func collectAuthSignals(ctx context.Context, t *testing.T, brk broker.Broker, namespace string) func() []*a2a.AuthenticationRequiredData {
	t.Helper()
	var signals []*a2a.AuthenticationRequiredData
	var mu sync.Mutex

	statusTopic := namespace + "/a2a/v1/e2e-test-status"
	q := &broker.SimpleQueue{QName: "auth-signal-q-" + fmt.Sprint(time.Now().UnixNano())}
	if subErr := brk.Subscribe(q, statusTopic); subErr != nil {
		t.Fatalf("subscribe status: %v", subErr)
	}

	go func() {
		for {
			msg, recvErr := brk.Receive(ctx, q)
			if recvErr != nil || msg == nil {
				return
			}
			var envelope struct {
				Result json.RawMessage `json:"result"`
			}
			if ue := json.Unmarshal(msg.Payload, &envelope); ue != nil || envelope.Result == nil {
				continue
			}
			var event a2a.TaskStatusUpdateEvent
			if ue := json.Unmarshal(envelope.Result, &event); ue != nil || event.Status.Message == nil {
				continue
			}
			for _, part := range event.Status.Message.Parts {
				if part.Kind != "data" || part.Data == nil {
					continue
				}
				dataMap, ok := part.Data.(map[string]any)
				if !ok || dataMap["type"] != "authentication_required" {
					continue
				}
				raw, _ := json.Marshal(dataMap)
				var authData a2a.AuthenticationRequiredData
				if ue := json.Unmarshal(raw, &authData); ue == nil {
					mu.Lock()
					signals = append(signals, &authData)
					mu.Unlock()
				}
			}
		}
	}()

	return func() []*a2a.AuthenticationRequiredData {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]*a2a.AuthenticationRequiredData, len(signals))
		copy(cp, signals)
		return cp
	}
}

func publishTaskForAuthFlow(ctx context.Context, t *testing.T, brk broker.Broker, namespace, agentName, taskID, text, userID string) *broker.SimpleQueue {
	t.Helper()
	requestTopic := a2a.AgentRequestTopic(namespace, agentName)
	responseTopic := namespace + "/a2a/v1/e2e-test-response/" + taskID
	q := &broker.SimpleQueue{QName: "resp-q-" + taskID}
	if subErr := brk.Subscribe(q, responseTopic); subErr != nil {
		t.Fatalf("subscribe response: %v", subErr)
	}

	payload := a2a.Request{
		JSONRPC: "2.0", ID: taskID, Method: "message/send",
		Params: map[string]any{
			"message": a2a.Message{
				Role:  "user",
				Parts: []a2a.Part{a2a.NewTextPart(text)},
			},
		},
	}
	payloadBytes, _ := json.Marshal(payload)
	if pubErr := brk.Publish(ctx, requestTopic, &broker.Message{
		Payload: payloadBytes,
		UserProperties: map[string]string{
			a2a.PropUserID:         userID,
			a2a.PropClientID:       "e2e-test-client",
			a2a.PropReplyTo:        responseTopic,
			a2a.PropA2AStatusTopic: namespace + "/a2a/v1/e2e-test-status",
		},
	}); pubErr != nil {
		t.Fatalf("publish task: %v", pubErr)
	}
	return q
}

func sendAuthResponse(ctx context.Context, t *testing.T, brk broker.Broker, namespace, agentName string, signal *a2a.AuthenticationRequiredData, code string) {
	t.Helper()
	sendAuthResponseWithVerifier(ctx, t, brk, namespace, agentName, signal, code, signal.CodeVerifier)
}

func sendAuthResponseWithVerifier(ctx context.Context, t *testing.T, brk broker.Broker, namespace, agentName string, signal *a2a.AuthenticationRequiredData, code, verifier string) {
	t.Helper()
	requestTopic := a2a.AgentRequestTopic(namespace, agentName)
	taskID := "auth-resp-" + fmt.Sprint(time.Now().UnixNano())
	responseTopic := namespace + "/a2a/v1/e2e-test-response/" + taskID

	authRespMsg := a2a.Message{
		Role: "user",
		Parts: []a2a.Part{
			a2a.NewDataPart(a2a.AuthResponseData{
				Type:              "auth_response",
				TargetAgent:       signal.TargetAgent,
				TargetAgentTaskID: signal.TargetAgentTaskID,
				UserID:            signal.UserID,
				ClientID:          signal.ClientID,
				FunctionCallID:    signal.AuthRequestFunctionCallID,
				Code:              code,
				CredentialKey:     signal.CredentialKey,
				CodeVerifier:      verifier,
				IsCredentialSave:  true,
			}),
		},
	}
	payload := a2a.Request{
		JSONRPC: "2.0", ID: taskID, Method: "message/send",
		Params: map[string]any{"message": authRespMsg},
	}
	payloadBytes, _ := json.Marshal(payload)
	if pubErr := brk.Publish(ctx, requestTopic, &broker.Message{
		Payload: payloadBytes,
		UserProperties: map[string]string{
			a2a.PropUserID:         signal.UserID,
			a2a.PropClientID:       "e2e-test-client",
			a2a.PropReplyTo:        responseTopic,
			a2a.PropA2AStatusTopic: namespace + "/a2a/v1/e2e-test-status",
		},
	}); pubErr != nil {
		t.Fatalf("publish auth response: %v", pubErr)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func extractQueryParam(t *testing.T, rawURL, param string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	return u.Query().Get(param)
}

func authProtectedToolManifest() []any {
	return []any{
		map[string]any{
			"name":        "auth_protected_tool",
			"description": "An auth-gated tool used by OAuth e2e tests.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
			},
		},
	}
}
