// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/credentials"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	oauthE2ENamespace = "oauth-e2e"
	oauthE2EUser      = "alice"
)

type oauthSystem struct {
	name         string
	clientID     string
	clientSecret string
	method       string
	ignoresPKCE  bool
	operation    string
	toolName     string
	provider     *harness.MockOAuthProvider
	api          *httptest.Server

	mu         sync.Mutex
	seenTokens []string
}

func newOAuthSystem(t *testing.T, name, operation, toolName string) *oauthSystem {
	t.Helper()
	return newOAuthSystemAuthenticating(t, name, operation, toolName, "client_secret_basic")
}

func newOAuthSystemAuthenticating(t *testing.T, name, operation, toolName, method string) *oauthSystem {
	t.Helper()
	sys := &oauthSystem{name: name, clientID: strings.ToLower(strings.Fields(name)[0]) + "-client", operation: operation, toolName: toolName, method: method}
	if method != "none" {
		sys.clientSecret = sys.clientID + "-secret"
	}
	provider, err := harness.NewMockOAuthProvider(harness.MockOAuthProviderConfig{
		ClientID:     sys.clientID,
		ClientSecret: sys.clientSecret,
	})
	if err != nil {
		t.Fatalf("start %s OAuth provider: %v", name, err)
	}
	t.Cleanup(provider.Close)
	sys.provider = provider

	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"openapi": "3.0.0",
			"info":    map[string]any{"title": name, "version": "1"},
			"servers": []any{map[string]any{"url": sys.api.URL}},
			"paths": map[string]any{
				"/records": map[string]any{
					"get": map[string]any{
						"operationId": operation,
						"responses":   map[string]any{"200": map[string]any{"description": "ok"}},
					},
				},
			},
		})
	})
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		sys.mu.Lock()
		sys.seenTokens = append(sys.seenTokens, token)
		sys.mu.Unlock()
		if _, valid := sys.provider.ValidateAccessToken(token); !valid {
			http.Error(w, `{"error":"invalid_token"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"records":[]}`))
	})
	sys.api = httptest.NewServer(mux)
	t.Cleanup(sys.api.Close)
	return sys
}

func (s *oauthSystem) connector(id string) entplatform.ConnectorConfig {
	values := map[string]any{
		"specification_url":                     s.api.URL + "/openapi.json",
		"base_url":                              s.api.URL,
		"auth_type":                             "oauth",
		"auth_oauth_authorization_url":          s.provider.AuthorizationURL(),
		"auth_oauth_token_url":                  s.provider.TokenURL(),
		"auth_oauth_client_id":                  s.clientID,
		"auth_oauth_scopes":                     "read",
		"auth_oauth_token_endpoint_auth_method": s.method,
	}
	if s.clientSecret != "" {
		values["auth_oauth_client_secret"] = s.clientSecret
	}
	return entplatform.ConnectorConfig{ID: id, Name: s.name, Type: "api", Subtype: "openapi", Values: values}
}

func (s *oauthSystem) lastSeenToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seenTokens) == 0 {
		return ""
	}
	return s.seenTokens[len(s.seenTokens)-1]
}

type oauthAgent struct {
	name        string
	agent       *agent.Agent
	broker      broker.Broker
	credentials auth.CredentialService
}

func deployOAuthAgent(ctx context.Context, t *testing.T, connectors []entplatform.ConnectorConfig) *oauthAgent {
	t.Helper()
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	rendered, err := entplatform.NewYAMLService(oauthE2ENamespace, slog.Default()).GenerateAgentYAML(
		&store.Agent{ID: "7f0c2d6e-3a41-4f9b-9d0e-2b7c5e8a1f10", Name: "Records assistant", Type: "standard"},
		nil, nil, nil, connectors)
	if err != nil {
		t.Fatalf("generate agent YAML: %v", err)
	}
	var doc struct {
		Apps []struct {
			AppConfig struct {
				AgentName string `yaml:"agent_name"`
				Tools     []any  `yaml:"tools"`
			} `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil || len(doc.Apps) != 1 {
		t.Fatalf("parse generated agent YAML: %v (apps=%d)", err, len(doc.Apps))
	}
	appCfg := doc.Apps[0].AppConfig

	credSvc := sqliteCredentialService(t, appCfg.AgentName)
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLLM("", mockllm.NewFIFOClient(nil)),
		runtime.WithSessions(session.NewMemoryStore()),
		runtime.WithCredentialService(credSvc),
	)
	ag := agent.New(agent.AgentConfig{
		Name:        appCfg.AgentName,
		Model:       "test-model",
		MaxTokens:   1024,
		MaxLLMCalls: 2,
		Namespace:   oauthE2ENamespace,
	})
	if err := ag.Init(ctx, nil, rt.Services()); err != nil {
		t.Fatalf("agent init: %v", err)
	}
	if _, err := awe.RegisterTools(ag, appCfg.Tools, rt.Services()); err != nil {
		t.Fatalf("register tools from the generated YAML: %v", err)
	}
	if err := ag.Start(ctx); err != nil {
		t.Fatalf("agent start: %v", err)
	}
	t.Cleanup(func() { _ = ag.Stop(context.Background()) })
	return &oauthAgent{name: appCfg.AgentName, agent: ag, broker: brk, credentials: credSvc}
}

func sqliteCredentialService(t *testing.T, agentName string) auth.CredentialService {
	t.Helper()
	db, err := dbutil.Open("sqlite:///"+filepath.Join(t.TempDir(), "credentials.db"), slog.Default())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sessions := session.NewSQLStore(db)
	if migrateErr := sessions.Migrate(context.Background()); migrateErr != nil {
		t.Fatalf("migrate session store: %v", migrateErr)
	}
	km, err := identity.NewKeyManager(identity.Config{Mode: "manual", Key: "oauth-e2e-key-0123456789abcdef"}, agentName, nil, nil)
	if err != nil {
		t.Fatalf("key manager: %v", err)
	}
	return credentials.NewAuthAdapter(credentials.NewSessionStore(sessions, km, nil))
}

func (a *oauthAgent) connect(ctx context.Context, t *testing.T, credentialKey string, systems ...*oauthSystem) *url.URL {
	t.Helper()
	signal, err := a.agent.BeginToolAuth(ctx, credentialKey, oauthE2EUser)
	if err != nil {
		t.Fatalf("begin tool auth for %s: %v", credentialKey, err)
	}
	authURI, err := url.Parse(signal.AuthURI)
	if err != nil {
		t.Fatalf("parse auth URI: %v", err)
	}
	endpoint := authURI.Scheme + "://" + authURI.Host + authURI.Path
	idx := slices.IndexFunc(systems, func(s *oauthSystem) bool { return s.provider.AuthorizationURL() == endpoint })
	if idx < 0 {
		t.Fatalf("sign-in link %s points at no known authorization server", endpoint)
	}
	query := authURI.Query()
	challenge := query.Get("code_challenge")
	if systems[idx].ignoresPKCE {
		challenge = ""
	}
	code := systems[idx].provider.IssueAuthorizationCode(oauthE2EUser, challenge, query.Get("redirect_uri"), query.Get("client_id"))

	taskID := "auth-response-" + credentialKey
	payload, _ := json.Marshal(a2a.Request{
		JSONRPC: "2.0", ID: taskID, Method: "message/send",
		Params: map[string]any{"message": a2a.Message{
			Role: "user",
			Parts: []a2a.Part{a2a.NewDataPart(a2a.AuthResponseData{
				Type:              "auth_response",
				TargetAgent:       signal.TargetAgent,
				TargetAgentTaskID: signal.TargetAgentTaskID,
				UserID:            oauthE2EUser,
				ClientID:          signal.ClientID,
				FunctionCallID:    signal.AuthRequestFunctionCallID,
				Code:              code,
				State:             query.Get("state"),
				CredentialKey:     signal.CredentialKey,
				CodeVerifier:      signal.CodeVerifier,
				IsCredentialSave:  true,
			})},
		}},
	})
	if err := a.broker.Publish(ctx, a2a.AgentRequestTopic(oauthE2ENamespace, a.name), &broker.Message{
		Payload: payload,
		UserProperties: map[string]string{
			a2a.PropUserID:   oauthE2EUser,
			a2a.PropClientID: "oauth-e2e-gateway",
			a2a.PropReplyTo:  oauthE2ENamespace + "/a2a/v1/oauth-e2e-response/" + taskID,
		},
	}); err != nil {
		t.Fatalf("publish auth_response: %v", err)
	}
	a.waitConnected(ctx, t, credentialKey)
	return authURI
}

func (a *oauthAgent) waitConnected(ctx context.Context, t *testing.T, credentialKey string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, info := range a.agent.ListToolAuth(ctx, oauthE2EUser) {
			if info.CredentialKey == credentialKey && info.Status == "connected" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("credential %s never reported connected", credentialKey)
}

func (a *oauthAgent) call(ctx context.Context, t *testing.T, sys *oauthSystem) *tool.Result {
	t.Helper()
	tl, ok := a.agent.Tools().GetTool(sys.toolName).(tool.BuiltinTool)
	if !ok {
		t.Fatalf("tool %q for %s is not registered as an in-process tool", sys.toolName, sys.name)
	}
	res, err := tl.Execute(ctx, map[string]any{}, &tool.Context{
		AgentName:         a.name,
		UserID:            oauthE2EUser,
		ParentTaskID:      "task-" + sys.toolName,
		ToolCallID:        "call-" + sys.toolName,
		CredentialService: a.credentials,
		SendSignal:        func(any) error { return nil },
	})
	if err != nil {
		t.Fatalf("execute %s: %v", sys.toolName, err)
	}
	return res
}

func TestOpenAPIConnectors_TwoOAuthSystemsOnOneAgentKeepSeparateCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	alpha := newOAuthSystem(t, "Alpha CRM", "listAlphaRecords", "list_alpha_records")
	beta := newOAuthSystem(t, "Beta ERP", "listBetaRecords", "list_beta_records")
	alphaID := "11111111-1111-4111-8111-111111111111"
	betaID := "22222222-2222-4222-8222-222222222222"
	deployed := deployOAuthAgent(ctx, t, []entplatform.ConnectorConfig{alpha.connector(alphaID), beta.connector(betaID)})

	listed := deployed.agent.ListToolAuth(ctx, oauthE2EUser)
	names := make([]string, 0, len(listed))
	for _, info := range listed {
		names = append(names, info.DisplayName)
		if !info.Interactive || info.Status != "not_connected" {
			t.Errorf("connection %+v, want interactive and not_connected before sign-in", info)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{alpha.name, beta.name}) {
		t.Errorf("listed connections are named %q, want one per connector: %q and %q", names, alpha.name, beta.name)
	}

	for _, info := range listed {
		deployed.connect(ctx, t, info.CredentialKey, alpha, beta)
	}

	for _, sys := range []*oauthSystem{alpha, beta} {
		res := deployed.call(ctx, t, sys)
		token := sys.lastSeenToken()
		if _, issuedHere := sys.provider.ValidateAccessToken(token); !issuedHere {
			other := alpha
			if sys == alpha {
				other = beta
			}
			if _, issuedThere := other.provider.ValidateAccessToken(token); issuedThere {
				t.Errorf("%s API received a token %s's authorization server issued", sys.name, other.name)
				continue
			}
			t.Errorf("%s API received no token its authorization server issued (result %s: %s)", sys.name, res.Status, res.Message)
			continue
		}
		if res.Status != tool.StatusSuccess {
			t.Errorf("%s call status = %s (%s), want success", sys.name, res.Status, res.Message)
		}
	}
}

func TestOpenAPIConnectors_CredentialKeyFollowsTheConnectorID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	alpha := newOAuthSystem(t, "Alpha CRM", "listAlphaRecords", "list_alpha_records")
	connectorID := "33333333-3333-4333-8333-333333333333"
	first := deployOAuthAgent(ctx, t, []entplatform.ConnectorConfig{alpha.connector(connectorID)})
	renamed := alpha.connector(connectorID)
	renamed.Name = "Alpha CRM (renamed)"
	second := deployOAuthAgent(ctx, t, []entplatform.ConnectorConfig{renamed})

	before, after := first.agent.ListToolAuth(ctx, oauthE2EUser), second.agent.ListToolAuth(ctx, oauthE2EUser)
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("listings = %+v / %+v, want one connection each", before, after)
	}
	if before[0].CredentialKey != after[0].CredentialKey {
		t.Errorf("credential key changed on rename: %q -> %q", before[0].CredentialKey, after[0].CredentialKey)
	}
	if want := "openapi_connector_" + connectorID; before[0].CredentialKey != want {
		t.Errorf("credential key = %q, want %q", before[0].CredentialKey, want)
	}
	if after[0].DisplayName != renamed.Name {
		t.Errorf("display name = %q, want %q", after[0].DisplayName, renamed.Name)
	}
}

func TestOpenAPIConnectors_ATokenUnderTheSharedKeyIsNotReused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	alpha := newOAuthSystem(t, "Alpha CRM", "listAlphaRecords", "list_alpha_records")
	deployed := deployOAuthAgent(ctx, t, []entplatform.ConnectorConfig{alpha.connector("44444444-4444-4444-8444-444444444444")})
	legacy, _ := json.Marshal(map[string]any{
		"access_token": "token-from-the-shared-slot",
		"token_type":   "Bearer",
		"expires_at":   time.Now().Add(time.Hour).Unix(),
	})
	if err := deployed.credentials.Set(ctx, deployed.name, deployed.name, oauthE2EUser, "openapi_oauth2_authorization_code", string(legacy)); err != nil {
		t.Fatalf("seed shared-slot token: %v", err)
	}

	listed := deployed.agent.ListToolAuth(ctx, oauthE2EUser)
	if len(listed) != 1 || listed[0].Status != "not_connected" {
		t.Fatalf("listing = %+v, want the connector's own connection, not connected", listed)
	}
	res := deployed.call(ctx, t, alpha)
	if res.Status != tool.StatusPending {
		t.Errorf("call status = %s (%s), want a pause for sign-in", res.Status, res.Message)
	}
	if seen := alpha.lastSeenToken(); seen != "" {
		t.Errorf("Alpha CRM API was called with %q, want no call before the user signs in", seen)
	}
}

func TestOpenAPIConnectors_SignInSendsPKCE(t *testing.T) {
	for _, method := range []string{"client_secret_basic", "client_secret_post", "none"} {
		t.Run(method, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			sys := newOAuthSystemAuthenticating(t, "Alpha CRM", "listAlphaRecords", "list_alpha_records", method)
			deployed := deployOAuthAgent(ctx, t, []entplatform.ConnectorConfig{sys.connector("55555555-5555-4555-8555-555555555555")})

			listed := deployed.agent.ListToolAuth(ctx, oauthE2EUser)
			if len(listed) != 1 {
				t.Fatalf("listing = %+v, want one connection", listed)
			}
			query := deployed.connect(ctx, t, listed[0].CredentialKey, sys).Query()
			if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
				t.Errorf("sign-in link query = %v, want code_challenge with code_challenge_method=S256", query)
			}
			if res := deployed.call(ctx, t, sys); res.Status != tool.StatusSuccess {
				t.Errorf("call after sign-in = %s (%s), want success", res.Status, res.Message)
			}
		})
	}
}

func TestOpenAPIConnectors_AServerThatIgnoresPKCEStillSignsIn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sys := newOAuthSystem(t, "Alpha CRM", "listAlphaRecords", "list_alpha_records")
	sys.ignoresPKCE = true
	deployed := deployOAuthAgent(ctx, t, []entplatform.ConnectorConfig{sys.connector("66666666-6666-4666-8666-666666666666")})

	listed := deployed.agent.ListToolAuth(ctx, oauthE2EUser)
	if len(listed) != 1 {
		t.Fatalf("listing = %+v, want one connection", listed)
	}
	deployed.connect(ctx, t, listed[0].CredentialKey, sys)
	if res := deployed.call(ctx, t, sys); res.Status != tool.StatusSuccess {
		t.Errorf("call after sign-in = %s (%s), want success", res.Status, res.Message)
	}
}

func TestOpenAPIConnectors_PKCEDisabledSendsNoChallenge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sys := newOAuthSystem(t, "Alpha CRM", "listAlphaRecords", "list_alpha_records")
	conn := sys.connector("77777777-7777-4777-8777-777777777777")
	conn.Values["auth_oauth_pkce"] = "disabled"
	deployed := deployOAuthAgent(ctx, t, []entplatform.ConnectorConfig{conn})

	listed := deployed.agent.ListToolAuth(ctx, oauthE2EUser)
	if len(listed) != 1 {
		t.Fatalf("listing = %+v, want one connection", listed)
	}
	query := deployed.connect(ctx, t, listed[0].CredentialKey, sys).Query()
	if challenge := query.Get("code_challenge"); challenge != "" {
		t.Errorf("code_challenge = %q, want none with PKCE disabled", challenge)
	}
	if res := deployed.call(ctx, t, sys); res.Status != tool.StatusSuccess {
		t.Errorf("call after sign-in = %s (%s), want success", res.Status, res.Message)
	}
}
