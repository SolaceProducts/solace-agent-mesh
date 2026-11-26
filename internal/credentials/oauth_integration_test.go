// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package credentials

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func TestOAuth2WithEnterpriseSessionStore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	memStore := session.NewMemoryStore()
	cfg := identity.Config{Mode: "manual", Key: "test-key-1234567890abcdef"}
	km, err := identity.NewKeyManager(cfg, "test-agent", nil, nil)
	if err != nil {
		t.Fatalf("create key manager: %v", err)
	}
	credStore := NewSessionStore(memStore, km, nil)
	credSvc := NewAuthAdapter(credStore)

	provider, err := harness.NewMockOAuthProvider(harness.MockOAuthProviderConfig{
		ClientID:     "enterprise-client",
		ClientSecret: "enterprise-secret",
	})
	if err != nil {
		t.Fatalf("start OAuth provider: %v", err)
	}
	defer provider.Close()

	mcpSrv, err := harness.NewMockMCPAuthServerWithProvider(provider)
	if err != nil {
		t.Fatalf("start MCP server: %v", err)
	}
	defer mcpSrv.Close()

	brk := broker.NewDevBroker()
	if connErr := brk.Connect(ctx); connErr != nil {
		t.Fatalf("connect broker: %v", connErr)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	argsJSON := `{"query":"enterprise test"}`
	llmResponses := []*llm.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "auth_protected_tool", Arguments: argsJSON}}},
		{Content: "Please authenticate.", FinishReason: "stop"},
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c2", Name: "auth_protected_tool", Arguments: argsJSON}}},
		{Content: "Please authenticate.", FinishReason: "stop"},
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c3", Name: "auth_protected_tool", Arguments: argsJSON}}},
		{Content: "Success for user A.", FinishReason: "stop"},
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c4", Name: "auth_protected_tool", Arguments: argsJSON}}},
		{Content: "Success for user B.", FinishReason: "stop"},
	}
	llmClient := mockllm.NewFIFOClient(llmResponses)

	agentName := "EntOAuthAgent"
	credKey := "ent_mcp_oauth"
	namespace := "test-ns"
	statusTopic := namespace + "/a2a/v1/ent-oauth-test-status"

	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLLM("", llmClient),
		runtime.WithSessions(session.NewMemoryStore()),
		runtime.WithCredentialService(credSvc),
	)

	agentCfg := agent.AgentConfig{
		Name:         agentName,
		SystemPrompt: "You are a helpful assistant.",
		Model:        "test-model",
		MaxTokens:    4096,
		MaxLLMCalls:  10,
		Namespace:    namespace,
	}
	ag := agent.New(agentCfg)
	if initErr := ag.Init(ctx, nil, rt.Services()); initErr != nil {
		t.Fatalf("agent init: %v", initErr)
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
					"scopes":                     []any{"read"},
					"token_endpoint_auth_method": "client_secret_post",
				},
				"credential": map[string]any{
					"client_id":     "enterprise-client",
					"client_secret": "enterprise-secret",
				},
				"credential_key": credKey,
			},
			"manifest": []any{
				map[string]any{
					"name":        "auth_protected_tool",
					"description": "An auth-gated tool used by enterprise OAuth e2e tests.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	}
	if _, regErr := awe.RegisterTools(ag, toolsCfg, rt.Services()); regErr != nil {
		t.Fatalf("register tools: %v", regErr)
	}
	if startErr := ag.Start(ctx); startErr != nil {
		t.Fatalf("agent start: %v", startErr)
	}
	defer func() { _ = ag.Stop(ctx) }()

	authSignals := make(chan *a2a.AuthenticationRequiredData, 10)
	statusQueue := &broker.SimpleQueue{QName: "ent-oauth-status-q"}
	if subErr := brk.Subscribe(statusQueue, statusTopic); subErr != nil {
		t.Fatalf("subscribe status: %v", subErr)
	}

	go func() {
		for {
			msg, recvErr := brk.Receive(ctx, statusQueue)
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
					authSignals <- &authData
				}
			}
		}
	}()

	sendTask := func(userID, taskID string) (*a2a.AuthenticationRequiredData, *broker.SimpleQueue) {
		t.Helper()
		requestTopic := a2a.AgentRequestTopic(namespace, agentName)
		responseTopic := namespace + "/a2a/v1/ent-oauth-response/" + taskID
		q := &broker.SimpleQueue{QName: "ent-resp-q-" + taskID}
		if subErr := brk.Subscribe(q, responseTopic); subErr != nil {
			t.Fatalf("subscribe response: %v", subErr)
		}

		payload := a2a.Request{
			JSONRPC: "2.0", ID: taskID, Method: "message/send",
			Params: map[string]any{
				"message": a2a.Message{
					Role:  "user",
					Parts: []a2a.Part{a2a.NewTextPart("Search for data")},
				},
			},
		}
		payloadBytes, _ := json.Marshal(payload)
		if pubErr := brk.Publish(ctx, requestTopic, &broker.Message{
			Payload: payloadBytes,
			UserProperties: map[string]string{
				a2a.PropUserID:         userID,
				a2a.PropClientID:       "ent-test-client",
				a2a.PropReplyTo:        responseTopic,
				a2a.PropA2AStatusTopic: statusTopic,
			},
		}); pubErr != nil {
			t.Fatalf("publish task: %v", pubErr)
		}

		select {
		case signal := <-authSignals:
			return signal, q
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for auth signal")
			return nil, nil
		}
	}

	sendAuthResponse := func(userID string, signal *a2a.AuthenticationRequiredData, code string) {
		t.Helper()
		requestTopic := a2a.AgentRequestTopic(namespace, agentName)
		taskID := "auth-resp-" + userID
		responseTopic := namespace + "/a2a/v1/ent-oauth-response/" + taskID

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
					CodeVerifier:      signal.CodeVerifier,
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
				a2a.PropClientID:       "ent-test-client",
				a2a.PropReplyTo:        responseTopic,
				a2a.PropA2AStatusTopic: statusTopic,
			},
		}); pubErr != nil {
			t.Fatalf("publish auth response: %v", pubErr)
		}

		deadline := time.After(10 * time.Second)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-deadline:
				t.Fatal("timed out waiting for credential storage")
			case <-ticker.C:
				val, _ := credSvc.Get(ctx, agentName, agentName, userID, credKey)
				if val != "" {
					return
				}
			}
		}
	}

	sendRetry := func(userID, taskID string) {
		t.Helper()
		callsBefore := mcpSrv.ToolCallCount()
		requestTopic := a2a.AgentRequestTopic(namespace, agentName)
		responseTopic := namespace + "/a2a/v1/ent-oauth-response/" + taskID
		q := &broker.SimpleQueue{QName: "ent-retry-q-" + taskID}
		if subErr := brk.Subscribe(q, responseTopic); subErr != nil {
			t.Fatalf("subscribe retry response: %v", subErr)
		}

		payload := a2a.Request{
			JSONRPC: "2.0", ID: taskID, Method: "message/send",
			Params: map[string]any{
				"message": a2a.Message{
					Role:  "user",
					Parts: []a2a.Part{a2a.NewTextPart("Search for data")},
				},
			},
		}
		payloadBytes, _ := json.Marshal(payload)
		if pubErr := brk.Publish(ctx, requestTopic, &broker.Message{
			Payload: payloadBytes,
			UserProperties: map[string]string{
				a2a.PropUserID:         userID,
				a2a.PropClientID:       "ent-test-client",
				a2a.PropReplyTo:        responseTopic,
				a2a.PropA2AStatusTopic: statusTopic,
			},
		}); pubErr != nil {
			t.Fatalf("publish retry: %v", pubErr)
		}

		if _, recvErr := brk.Receive(ctx, q); recvErr != nil {
			t.Fatalf("receive retry response: %v", recvErr)
		}

		deadline := time.After(10 * time.Second)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-deadline:
				t.Fatalf("timed out waiting for MCP tool call for %s", userID)
			case <-ticker.C:
				if mcpSrv.ToolCallCount() > callsBefore {
					return
				}
			}
		}
	}

	t.Log("User A: sending task")
	signalA, respQueueA := sendTask("userA", "task-a-1")

	parsedA, _ := url.Parse(signalA.AuthURI)
	codeChallengeA := parsedA.Query().Get("code_challenge")
	redirectURI := parsedA.Query().Get("redirect_uri")

	codeA := provider.IssueAuthorizationCode("userA", codeChallengeA, redirectURI, "enterprise-client")
	t.Log("User A: completing auth flow")
	sendAuthResponse("userA", signalA, codeA)

	if _, recvErr := brk.Receive(ctx, respQueueA); recvErr != nil {
		t.Fatalf("drain userA paused-task response: %v", recvErr)
	}

	t.Log("User B: sending task")
	signalB, respQueueB := sendTask("userB", "task-b-1")

	parsedB, _ := url.Parse(signalB.AuthURI)
	codeChallengeB := parsedB.Query().Get("code_challenge")

	codeB := provider.IssueAuthorizationCode("userB", codeChallengeB, redirectURI, "enterprise-client")
	t.Log("User B: completing auth flow")
	sendAuthResponse("userB", signalB, codeB)

	if _, recvErr := brk.Receive(ctx, respQueueB); recvErr != nil {
		t.Fatalf("drain userB paused-task response: %v", recvErr)
	}

	tokenA, err := credSvc.Get(ctx, agentName, agentName, "userA", credKey)
	if err != nil || tokenA == "" {
		t.Fatal("userA token not stored")
	}
	tokenB, err := credSvc.Get(ctx, agentName, agentName, "userB", credKey)
	if err != nil || tokenB == "" {
		t.Fatal("userB token not stored")
	}
	if tokenA == tokenB {
		t.Error("userA and userB should have different stored tokens")
	}

	appName := secretAppName(agentName)
	sessionID := deriveSessionID(km.IdentityKey(), appName, "userA", credKey)
	sess, err := memStore.Get(ctx, appName, "userA", sessionID)
	if err != nil {
		t.Fatalf("get raw session: %v", err)
	}
	if sess == nil {
		t.Fatal("raw session not found")
	}
	secretVal, ok := sess.Metadata["secret_value"].(string)
	if !ok || secretVal == "" {
		t.Fatal("session metadata missing secret_value")
	}
	decoded, err := base64.StdEncoding.DecodeString(secretVal)
	if err != nil {
		t.Fatalf("secret_value is not valid base64: %v", err)
	}
	var discard any
	if json.Unmarshal(decoded, &discard) == nil {
		t.Error("secret_value appears to be plaintext JSON, expected encrypted ciphertext")
	}

	cfg2 := identity.Config{Mode: "manual", Key: "different-key-abcdef1234"}
	km2, err := identity.NewKeyManager(cfg2, "other-agent", nil, nil)
	if err != nil {
		t.Fatalf("create second key manager: %v", err)
	}
	if _, decErr := identity.Decrypt(km2.EncryptionKey(), decoded); decErr == nil {
		t.Error("identity.Decrypt accepted ciphertext under a different encryption key — AES-GCM key binding broken")
	}

	t.Log("User A: retry task")
	sendRetry("userA", "task-a-2")

	t.Log("User B: retry task")
	sendRetry("userB", "task-b-2")

	if c := mcpSrv.ToolCallCount(); c < 2 {
		t.Errorf("expected at least 2 MCP tool calls, got %d", c)
	}

	if c := provider.AuthCodeGrantCount(); c != 2 {
		t.Errorf("expected 2 auth_code grants, got %d", c)
	}

	if ec := provider.ErrorCount(); ec > 0 {
		t.Errorf("expected 0 provider errors, got %d", ec)
		for _, log := range provider.TokenRequestLogs() {
			if !log.Success {
				t.Logf("  failed request: grant_type=%s user=%s error=%s", log.GrantType, log.UserID, log.ErrorCode)
			}
		}
	}

	t.Log("Enterprise SessionStore OAuth integration test completed successfully")
}
