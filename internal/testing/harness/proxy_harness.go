// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/proxy/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
	"gopkg.in/yaml.v3"
)

type ProxyTestHarness struct {
	scenario *TestScenario
	broker   broker.Broker
	gateway  Gateway
	artSvc   *artifact.ScopedService
	sessSvc  session.Store
	logger   *slog.Logger
	t        *testing.T

	proxyInstances []*proxy.ProxyInstance
	mockServer     *MockDownstreamServer
	reference      *ReferenceA2AServer
	mockOAuth      *MockOAuthServer

	recordedRequests []RecordedRequest
}

type RecordedRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Body    json.RawMessage
}

type MockDownstreamServer struct {
	Server *httptest.Server

	mu        sync.Mutex
	responses []json.RawMessage
	httpError *HTTPErrorConfig
	requests  []RecordedRequest
	streaming bool

	authConfig *DownstreamAgentAuth
	failedOnce bool

	agentCardFailRemaining int
	agentCardRequests      int
}

type HTTPErrorConfig struct {
	StatusCode int            `yaml:"status_code"`
	ErrorBody  map[string]any `yaml:"error_body"`
}

type DownstreamAgentAuth struct {
	Enabled        bool   `yaml:"enabled"`
	Type           string `yaml:"type"`
	ExpectedValue  string `yaml:"expected_value"`
	ShouldFailOnce bool   `yaml:"should_fail_once"`
}

type MockOAuthServer struct {
	Server *httptest.Server

	mu               sync.Mutex
	tokenResponses   []OAuthTokenResponse
	errorResponse    *OAuthErrorResponse
	currentIndex     int
	recordedRequests []RecordedRequest
}

type OAuthTokenResponse struct {
	AccessToken string `yaml:"access_token" json:"access_token"`
	ExpiresIn   int    `yaml:"expires_in" json:"expires_in"`
	TokenType   string `yaml:"token_type" json:"token_type"`
}

type OAuthErrorResponse struct {
	Error       string `yaml:"error" json:"error"`
	Description string `yaml:"error_description" json:"error_description"`
	StatusCode  int    `yaml:"status_code"`
}

type ProxyAuthConfig struct {
	AgentName             string              `yaml:"agent_name"`
	Authentication        *ProxyAuthSpec      `yaml:"authentication"`
	AgentCardHeaders      []map[string]string `yaml:"agent_card_headers"`
	TaskHeaders           []map[string]string `yaml:"task_headers"`
	UseAgentCardURL       *bool               `yaml:"use_agent_card_url"`
	UseAuthForAgentCard   *bool               `yaml:"use_auth_for_agent_card"`
	ConvertProgressUpdate *bool               `yaml:"convert_progress_updates"`
}

type ProxyAuthSpec struct {
	Type                      string `yaml:"type"`
	Token                     string `yaml:"token"`
	ClientID                  string `yaml:"client_id"`
	ClientSecret              string `yaml:"client_secret"`
	TokenURL                  string `yaml:"token_url"`
	TokenCacheDurationSeconds int    `yaml:"token_cache_duration_seconds"`

	CACertPathMode            string `yaml:"ca_cert_path_mode"`
	InsecureSkipVerify        bool   `yaml:"insecure_skip_verify"`
	MaxRetries                *int   `yaml:"max_retries"`
	TokenRefreshBufferSeconds int    `yaml:"token_refresh_buffer_seconds"`
}

type AuthAssertion struct {
	RequestIndex        int               `yaml:"request_index"`
	AuthorizationHeader map[string]string `yaml:"authorization_header"`
	APIKeyHeader        string            `yaml:"api_key_header"`
	CustomHeaders       map[string]string `yaml:"custom_headers"`
}

type OAuthTokenRequestAssertion struct {
	TokenURL            string            `yaml:"token_url"`
	CallCount           int               `yaml:"call_count"`
	RequestBodyContains map[string]string `yaml:"request_body_contains"`
}

func NewProxyTestHarness(t *testing.T, sc *TestScenario) *ProxyTestHarness {
	return &ProxyTestHarness{
		scenario: sc,
		logger:   slog.New(slog.NewTextHandler(&testWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		t:        t,
	}
}

func (h *ProxyTestHarness) Setup(ctx context.Context) error {
	h.broker = broker.NewDevBroker(broker.WithDevBrokerLogger(h.logger))
	if err := h.broker.Connect(ctx); err != nil {
		return fmt.Errorf("connect broker: %w", err)
	}

	h.sessSvc = session.NewMemoryStore()
	h.artSvc = &artifact.ScopedService{
		Inner:     memory.New(),
		Scope:     "namespace",
		Namespace: "test-ns",
	}

	proxyAuthCfg := h.parseProxyAuthConfig()
	downstreamAuth := h.parseDownstreamAgentAuth()
	httpError := h.parseHTTPError()
	downstreamResponses := h.parseDownstreamResponses()
	mockOAuthCfg := h.parseMockOAuthServer()

	if mockOAuthCfg != nil {
		h.mockOAuth = newMockOAuthServer(mockOAuthCfg)
	}

	isStreaming := false
	for _, resp := range downstreamResponses {
		var obj map[string]any
		if err := json.Unmarshal(resp, &obj); err == nil {
			if kind, _ := obj["kind"].(string); kind == "status-update" {
				isStreaming = true
				break
			}
		}
	}

	referenceCfg, err := h.parseReferenceA2A()
	if err != nil {
		return err
	}
	if referenceCfg != nil {
		h.reference, err = NewReferenceA2AServer(*referenceCfg)
		if err != nil {
			return fmt.Errorf("downstream_a2a_reference: %w", err)
		}
	} else {
		h.mockServer = NewMockDownstreamServer(downstreamResponses, httpError, downstreamAuth, isStreaming)
	}

	agentName := h.resolveAgentName()

	proxyCfgMap := h.buildProxyConfigMap(agentName, proxyAuthCfg)
	cfgMap := map[string]any{
		"kind":       "proxy",
		"name":       agentName,
		"app_config": proxyCfgMap,
	}

	svc := &proxyTestServices{
		brk:      h.broker,
		sessions: h.sessSvc,
		arts:     h.artSvc,
		logger:   h.logger,
	}

	inst, err := proxy.NewInstance(config.FromMap(cfgMap))
	if err != nil {
		return fmt.Errorf("create proxy instance: %w", err)
	}

	if err := inst.Init(ctx, nil, svc); err != nil {
		return fmt.Errorf("init proxy: %w", err)
	}
	if err := inst.Start(ctx); err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}
	h.proxyInstances = append(h.proxyInstances, inst)

	brokerGW := NewTestGateway("test-gw", "test-ns", h.broker, h.logger)
	if err := brokerGW.Setup(ctx); err != nil {
		return fmt.Errorf("gateway setup: %w", err)
	}
	h.gateway = brokerGW

	time.Sleep(100 * time.Millisecond)

	if len(h.scenario.SetupArtifacts) > 0 {
		if err := h.setupArtifacts(ctx); err != nil {
			return fmt.Errorf("setup artifacts: %w", err)
		}
	}

	if h.scenario.GatewayInput != nil {
		if h.scenario.GatewayInput.ExternalContext == nil {
			h.scenario.GatewayInput.ExternalContext = &ExtContext{}
		}
		if h.scenario.GatewayInput.ExternalContext.A2ASessionID == "" {
			sessionID := ""
			for _, exp := range h.scenario.ExpectedGatewayOutput {
				if exp.ContextID != "" && exp.ContextID != "*" {
					sessionID = exp.ContextID
					break
				}
			}
			if sessionID == "" {
				sessionID = "session_" + h.scenario.TestCaseID
			}
			h.scenario.GatewayInput.ExternalContext.A2ASessionID = sessionID
		}
	}

	return nil
}

func (h *ProxyTestHarness) Run(ctx context.Context) ([]CollectedEvent, error) {
	timeout := 10 * time.Second

	agentName := h.resolveAgentName()

	_, err := h.gateway.SendRequest(ctx, agentName, h.scenario.GatewayInput)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	events, err := h.gateway.CollectEvents(ctx, timeout)
	if err != nil && err != context.DeadlineExceeded {
		return events, fmt.Errorf("collect events: %w", err)
	}

	switch {
	case h.mockServer != nil:
		h.recordedRequests = h.mockServer.RecordedRequests()
	case h.reference != nil:
		h.recordedRequests = h.reference.RecordedRequests()
	}

	return events, nil
}

func (h *ProxyTestHarness) resolveAgentName() string {
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.TargetAgentName != "" {
		return h.scenario.GatewayInput.TargetAgentName
	}
	return "TestAgent_Proxied"
}

func (h *ProxyTestHarness) resolveScope() (agentName, userID, sessionID string) {
	agentName = h.resolveAgentName()
	userID = "test-user"
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.UserIdentity != "" {
		userID = h.scenario.GatewayInput.UserIdentity
	}
	sessionID = "session_" + h.scenario.TestCaseID
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.ExternalContext != nil &&
		h.scenario.GatewayInput.ExternalContext.A2ASessionID != "" {
		sessionID = h.scenario.GatewayInput.ExternalContext.A2ASessionID
	}
	return agentName, userID, sessionID
}

func (h *ProxyTestHarness) AssertEvents(t *testing.T, collected []CollectedEvent, expected []ExpectedEvent) {
	t.Helper()

	if len(expected) == 0 {
		return
	}

	var statusEvents []CollectedEvent
	var responseEvents []CollectedEvent
	for _, e := range collected {
		switch e.Type {
		case "status":
			statusEvents = append(statusEvents, e)
		case "response":
			responseEvents = append(responseEvents, e)
		}
	}

	statusIdx := 0
	for i, exp := range expected {
		switch exp.Type {
		case "status_update":
			if statusIdx >= len(statusEvents) {
				t.Errorf("expected_event[%d]: no status_update event at index %d (got %d status events)", i, statusIdx, len(statusEvents))
				statusIdx++
				continue
			}
			h.assertStatusUpdate(t, i, statusEvents[statusIdx], exp)
			statusIdx++

		case "final_response":
			found := false
			for _, c := range responseEvents {
				h.assertFinalResponse(t, i, c, exp)
				found = true
				break
			}
			if !found {
				t.Errorf("expected_event[%d]: no final_response event received (got %d events total)", i, len(collected))
			}

		case "error":
			found := false
			for _, c := range responseEvents {
				h.assertFinalResponse(t, i, c, exp)
				found = true
				break
			}
			if !found {
				t.Errorf("expected_event[%d]: no error event received (got %d events total)", i, len(collected))
			}
		}
	}
}

func (h *ProxyTestHarness) assertStatusUpdate(t *testing.T, idx int, collected CollectedEvent, expected ExpectedEvent) {
	t.Helper()
	prefix := fmt.Sprintf("expected_event[%d]", idx)

	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(collected.Payload, &envelope); err != nil {
		t.Errorf("%s: unmarshal status envelope: %v", prefix, err)
		return
	}
	if envelope.JSONRPC != "2.0" || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		t.Errorf("%s: status event is not a JSON-RPC response envelope: %s", prefix, collected.Payload)
		return
	}

	var su a2a.TaskStatusUpdateEvent
	if err := json.Unmarshal(envelope.Result, &su); err != nil {
		t.Errorf("%s: unmarshal status update: %v", prefix, err)
		return
	}
	if su.Kind == "" {
		t.Errorf("%s: status event result has no kind: %s", prefix, envelope.Result)
		return
	}

	if expected.FinalFlag != nil && su.Final != *expected.FinalFlag {
		t.Errorf("%s: final_flag: expected %v, got %v", prefix, *expected.FinalFlag, su.Final)
	}

	if len(expected.ContentParts) > 0 && su.Status.Message != nil {
		rawParts, err := json.Marshal(su.Status.Message.Parts)
		if err != nil {
			t.Errorf("%s: marshal parts: %v", prefix, err)
			return
		}
		h.assertMessageParts(t, prefix, rawParts, expected.ContentParts)
	}
}

func (h *ProxyTestHarness) assertFinalResponse(t *testing.T, idx int, collected CollectedEvent, expected ExpectedEvent) {
	t.Helper()
	prefix := fmt.Sprintf("expected_event[%d]", idx)

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(collected.Payload, &envelope); err != nil {
		t.Errorf("%s: unmarshal response: %v", prefix, err)
		return
	}

	expectsError := expected.Error != nil || expected.Type == "error" || expected.ErrorCode != 0 || expected.ErrorMessageContains != ""
	if expectsError {
		if len(envelope.Error) == 0 || string(envelope.Error) == "null" {
			if len(envelope.Result) > 0 {
				var task struct {
					Status struct {
						State   string `json:"state"`
						Message struct {
							Parts []struct {
								Text string `json:"text"`
							} `json:"parts"`
						} `json:"message"`
					} `json:"status"`
				}
				if err := json.Unmarshal(envelope.Result, &task); err == nil && task.Status.State == "failed" {
					errMsg := ""
					if len(task.Status.Message.Parts) > 0 {
						errMsg = task.Status.Message.Parts[0].Text
					}
					expectedMsg := expected.ErrorMessageContains
					if expected.Error != nil && expected.Error.MessageContains != "" {
						expectedMsg = expected.Error.MessageContains
					}
					if expectedMsg != "" && !strings.Contains(errMsg, expectedMsg) {
						t.Errorf("%s: error_message: expected to contain %q, got %q", prefix, expectedMsg, errMsg)
					}
					return
				}
			}
			t.Errorf("%s: expected error but got success", prefix)
			return
		}
		var errObj struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(envelope.Error, &errObj); err != nil {
			t.Errorf("%s: unmarshal error: %v", prefix, err)
			return
		}
		expectedCode := expected.ErrorCode
		if expected.Error != nil && expected.Error.Code != 0 {
			expectedCode = expected.Error.Code
		}
		if expectedCode != 0 && errObj.Code != expectedCode {
			t.Errorf("%s: error_code: expected %d, got %d", prefix, expectedCode, errObj.Code)
		}
		expectedMsg := expected.ErrorMessageContains
		if expected.Error != nil && expected.Error.MessageContains != "" {
			expectedMsg = expected.Error.MessageContains
		}
		if expectedMsg != "" && !strings.Contains(errObj.Message, expectedMsg) {
			t.Errorf("%s: error_message: expected to contain %q, got %q", prefix, expectedMsg, errObj.Message)
		}
		return
	}

	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		t.Errorf("%s: unexpected error in response: %s", prefix, string(envelope.Error))
		return
	}

	var task struct {
		ID        string `json:"id"`
		ContextID string `json:"contextId"`
		Status    struct {
			State   string `json:"state"`
			Message struct {
				Kind      string          `json:"kind"`
				MessageID string          `json:"messageId"`
				Role      string          `json:"role"`
				Parts     json.RawMessage `json:"parts"`
			} `json:"message"`
		} `json:"status"`
	}
	if err := json.Unmarshal(envelope.Result, &task); err != nil {
		t.Errorf("%s: unmarshal task: %v", prefix, err)
		return
	}

	if expected.ID != "" && !MatchWildcard(task.ID, expected.ID) {
		t.Errorf("%s: id mismatch: expected %q, got %q", prefix, expected.ID, task.ID)
	}
	if expected.ContextID != "" && !MatchWildcard(task.ContextID, expected.ContextID) {
		t.Errorf("%s: contextId mismatch: expected %q, got %q", prefix, expected.ContextID, task.ContextID)
	}

	if expected.Status != nil {
		if expected.Status.State != "" && task.Status.State != expected.Status.State {
			t.Errorf("%s: status.state: expected %q, got %q", prefix, expected.Status.State, task.Status.State)
		}
		if expected.Status.Message != nil {
			h.assertMessageParts(t, prefix, task.Status.Message.Parts, expected.Status.Message.Parts)
		}
	}

	if expected.TaskState != "" && task.Status.State != expected.TaskState {
		t.Errorf("%s: task_state: expected %q, got %q", prefix, expected.TaskState, task.Status.State)
	}
	if len(expected.ContentParts) > 0 {
		h.assertMessageParts(t, prefix, task.Status.Message.Parts, expected.ContentParts)
	}
}

func (h *ProxyTestHarness) assertMessageParts(t *testing.T, prefix string, rawParts json.RawMessage, expectedParts []PartMatch) {
	t.Helper()

	var parts []struct {
		Kind     string          `json:"kind"`
		Text     string          `json:"text"`
		Data     json.RawMessage `json:"data"`
		Filename string          `json:"filename"`
		MimeType string          `json:"mime_type"`
	}
	if err := json.Unmarshal(rawParts, &parts); err != nil {
		t.Errorf("%s: unmarshal parts: %v", prefix, err)
		return
	}

	for i, exp := range expectedParts {
		partPrefix := fmt.Sprintf("%s.parts[%d]", prefix, i)
		expectedKind := exp.Kind
		if expectedKind == "" {
			expectedKind = exp.Type
		}

		if i >= len(parts) {
			t.Errorf("%s: expected part at index %d, but only %d parts", partPrefix, i, len(parts))
			continue
		}
		actual := parts[i]

		if expectedKind != "" && actual.Kind != expectedKind {
			t.Errorf("%s: kind mismatch: expected %q, got %q", partPrefix, expectedKind, actual.Kind)
			continue
		}

		switch expectedKind {
		case "text", "":
			if err := MatchPartText(actual.Text, exp); err != nil {
				t.Errorf("%s: %v", partPrefix, err)
			}
		case "data":
			if exp.DataContains.Map != nil && len(actual.Data) > 0 {
				if err := MatchJSONContains(actual.Data, exp.DataContains.Map); err != nil {
					t.Errorf("%s: data_contains: %v", partPrefix, err)
				}
			}
		case "file":
			if exp.Filename != "" && actual.Filename != exp.Filename {
				t.Errorf("%s: filename mismatch: expected %q, got %q", partPrefix, exp.Filename, actual.Filename)
			}
			if exp.MimeType != "" && actual.MimeType != exp.MimeType {
				t.Errorf("%s: mime_type mismatch: expected %q, got %q", partPrefix, exp.MimeType, actual.MimeType)
			}
		}
	}
}

func (h *ProxyTestHarness) AssertDownstreamAuth(t *testing.T) {
	t.Helper()

	assertionsRaw, ok := h.scenario.Extra["assert_downstream_auth"]
	if !ok {
		return
	}

	var assertions []AuthAssertion
	if err := assertionsRaw.Decode(&assertions); err != nil {
		t.Errorf("decode assert_downstream_auth: %v", err)
		return
	}

	for i, assertion := range assertions {
		prefix := fmt.Sprintf("assert_downstream_auth[%d]", i)
		idx := assertion.RequestIndex

		if len(assertion.AuthorizationHeader) == 0 && assertion.APIKeyHeader == "" && len(assertion.CustomHeaders) == 0 {
			t.Errorf("%s: assertion declares no checks (authorization_header/api_key_header/custom_headers all empty) — likely a typo'd field", prefix)
			continue
		}

		taskRequests := h.getTaskRequests()

		if idx < 0 || idx >= len(taskRequests) {
			t.Errorf("%s: request_index %d out of range (%d task requests recorded)", prefix, idx, len(taskRequests))
			continue
		}

		req := taskRequests[idx]
		if exact, ok := assertion.AuthorizationHeader["exact"]; ok {
			actual := req.Headers.Get("Authorization")
			if actual != exact {
				t.Errorf("%s: authorization_header: expected %q, got %q", prefix, exact, actual)
			}
		}
		if assertion.APIKeyHeader != "" {
			actual := req.Headers.Get("X-Api-Key")
			if actual != assertion.APIKeyHeader {
				t.Errorf("%s: api_key_header: expected %q, got %q", prefix, assertion.APIKeyHeader, actual)
			}
		}
		for name, want := range assertion.CustomHeaders {
			actual := req.Headers.Get(name)
			if actual != want {
				t.Errorf("%s: custom_header %q: expected %q, got %q", prefix, name, want, actual)
			}
		}
	}
}

const unsignedReferenceJWKSURL = "https://keys.invalid/jwks.json"

func (h *ProxyTestHarness) AssertProxyCardSignature(t *testing.T) {
	t.Helper()
	signatureRaw, wantSignature := h.scenario.Extra["assert_proxy_card_signature"]
	refusedRaw, wantRefused := h.scenario.Extra["assert_proxy_card_refused"]
	if !wantSignature && !wantRefused {
		return
	}
	if len(h.proxyInstances) == 0 {
		t.Error("assert_proxy_card_signature / assert_proxy_card_refused: no proxy instance ran")
		return
	}
	if wantSignature {
		var want a2a.CardSignature
		if err := signatureRaw.Decode(&want); err != nil || !want.Known() {
			t.Errorf("assert_proxy_card_signature: %q is not a card signature outcome (%v)", want, err)
			return
		}
		for _, inst := range h.proxyInstances {
			assertPublishedCardSignature(t, inst, want)
		}
	}
	if wantRefused {
		var refused bool
		if err := refusedRaw.Decode(&refused); err != nil || !refused {
			t.Errorf("assert_proxy_card_refused must be true (%v)", err)
			return
		}
		for _, inst := range h.proxyInstances {
			assertCardRefused(t, inst)
		}
	}
}

func assertPublishedCardSignature(t *testing.T, inst *proxy.ProxyInstance, want a2a.CardSignature) {
	t.Helper()
	card := inst.CachedCard()
	if card == nil {
		t.Errorf("assert_proxy_card_signature: proxy published no card, want card_signature %q", want)
		return
	}
	rp, ok := a2a.RemoteProtocolFromCard(*card)
	if !ok {
		t.Errorf("assert_proxy_card_signature: the proxy's card has no sam/remote-protocol extension")
		return
	}
	if rp.CardSignature != want {
		t.Errorf("assert_proxy_card_signature: card_signature = %q, want %q", rp.CardSignature, want)
	}
}

func assertCardRefused(t *testing.T, inst *proxy.ProxyInstance) {
	t.Helper()
	if inst.CachedCard() != nil {
		t.Error("assert_proxy_card_refused: the proxy published a card")
	}
	if !slices.ContainsFunc(inst.Problems(), func(p health.Problem) bool { return p.Detail == string(health.DetailRemoteCardUntrusted) }) {
		t.Errorf("assert_proxy_card_refused: health = %+v, want the untrusted-card detail", inst.Problems())
	}
}

func (h *ProxyTestHarness) AssertOAuthTokenRequests(t *testing.T) {
	t.Helper()

	assertionsRaw, ok := h.scenario.Extra["assert_oauth_token_requests"]
	if !ok {
		return
	}

	var assertions []OAuthTokenRequestAssertion
	if err := assertionsRaw.Decode(&assertions); err != nil {
		t.Errorf("decode assert_oauth_token_requests: %v", err)
		return
	}

	if h.mockOAuth == nil {
		t.Error("assert_oauth_token_requests specified but no mock_oauth_server configured")
		return
	}

	recorded := h.mockOAuth.RecordedRequests()
	for i, assertion := range assertions {
		prefix := fmt.Sprintf("assert_oauth_token_requests[%d]", i)
		if assertion.CallCount == 0 && len(assertion.RequestBodyContains) == 0 {
			t.Errorf("%s: assertion declares no checks (call_count/request_body_contains both empty) — likely a typo'd field", prefix)
			continue
		}
		if assertion.CallCount > 0 && len(recorded) != assertion.CallCount {
			t.Errorf("%s: expected %d token requests, got %d", prefix, assertion.CallCount, len(recorded))
		}
		if assertion.RequestBodyContains != nil && len(recorded) > 0 {
			bodyStr := string(recorded[0].Body)
			for key, val := range assertion.RequestBodyContains {
				expected := key + "=" + val
				if !strings.Contains(bodyStr, expected) {
					t.Errorf("%s: request body missing %s=%s; got %s", prefix, key, val, bodyStr)
				}
			}
		}
	}
}

func (h *ProxyTestHarness) getTaskRequests() []RecordedRequest {
	var result []RecordedRequest
	for _, r := range h.recordedRequests {
		if r.Method == "POST" {
			result = append(result, r)
		}
	}
	return result
}

type DownstreamRequestBodyAssertion struct {
	RequestIndex int      `yaml:"request_index"`
	BodyContains []string `yaml:"body_contains"`
	BodyNotMatch []string `yaml:"body_does_not_contain"`
}

func (h *ProxyTestHarness) AssertDownstreamRequestBody(t *testing.T) {
	t.Helper()

	raw, ok := h.scenario.Extra["assert_downstream_request_body_contains"]
	if !ok {
		return
	}
	var assertions []DownstreamRequestBodyAssertion
	if err := raw.Decode(&assertions); err != nil {
		t.Errorf("decode assert_downstream_request_body_contains: %v", err)
		return
	}

	taskRequests := h.getTaskRequests()
	for i, a := range assertions {
		prefix := fmt.Sprintf("assert_downstream_request_body_contains[%d]", i)
		if len(a.BodyContains) == 0 && len(a.BodyNotMatch) == 0 {
			t.Errorf("%s: assertion declares no checks (body_contains/body_does_not_contain both empty) — likely a typo'd field", prefix)
			continue
		}
		if a.RequestIndex < 0 || a.RequestIndex >= len(taskRequests) {
			t.Errorf("%s: request_index %d out of range (%d task requests recorded)", prefix, a.RequestIndex, len(taskRequests))
			continue
		}
		body := string(taskRequests[a.RequestIndex].Body)
		for _, want := range a.BodyContains {
			if !strings.Contains(body, want) {
				t.Errorf("%s: request body missing %q (body length=%d)", prefix, want, len(body))
			}
		}
		for _, unwanted := range a.BodyNotMatch {
			if strings.Contains(body, unwanted) {
				t.Errorf("%s: request body unexpectedly contains %q", prefix, unwanted)
			}
		}
	}
}

func (h *ProxyTestHarness) AssertArtifactState(t *testing.T, expected []ExpectedEvent) {
	t.Helper()
	if h.artSvc == nil {
		return
	}

	agentName, userID, sessionID := h.resolveScope()
	for _, exp := range expected {
		assertArtifactList(t, h.artSvc, agentName, userID, sessionID, "assert_artifact_state", exp.AssertArtifactState)
	}
}

func (h *ProxyTestHarness) Cleanup() {
	for _, inst := range h.proxyInstances {
		_ = inst.Stop(context.Background())
	}
	if h.gateway != nil {
		h.gateway.Cleanup()
	}
	if h.mockServer != nil {
		h.mockServer.Close()
	}
	if h.reference != nil {
		h.reference.Close()
	}
	if h.mockOAuth != nil {
		h.mockOAuth.Close()
	}
	if h.broker != nil {
		_ = h.broker.Disconnect(context.Background())
	}
}

func (h *ProxyTestHarness) parseProxyAuthConfig() *ProxyAuthConfig {
	raw, ok := h.scenario.Extra["proxy_auth_config"]
	if !ok {
		return nil
	}
	var cfg ProxyAuthConfig
	if err := raw.Decode(&cfg); err != nil {
		h.t.Logf("WARN: decode proxy_auth_config: %v", err)
		return nil
	}
	return &cfg
}

func (h *ProxyTestHarness) parseDownstreamAgentAuth() *DownstreamAgentAuth {
	raw, ok := h.scenario.Extra["downstream_agent_auth"]
	if !ok {
		return nil
	}
	var auth DownstreamAgentAuth
	if err := raw.Decode(&auth); err != nil {
		return nil
	}
	return &auth
}

func (h *ProxyTestHarness) parseHTTPError() *HTTPErrorConfig {
	raw, ok := h.scenario.Extra["downstream_http_error"]
	if !ok {
		return nil
	}
	var cfg HTTPErrorConfig
	if err := raw.Decode(&cfg); err != nil {
		return nil
	}
	return &cfg
}

func (h *ProxyTestHarness) parseDownstreamResponses() []json.RawMessage {
	raw, ok := h.scenario.Extra["downstream_a2a_agent_responses"]
	if !ok {
		return nil
	}

	var nodes []yaml.Node
	if err := raw.Decode(&nodes); err != nil {
		h.t.Logf("WARN: decode downstream_a2a_agent_responses: %v", err)
		return nil
	}

	var responses []json.RawMessage
	for _, node := range nodes {
		var generic any
		if err := node.Decode(&generic); err != nil {
			continue
		}
		data, err := json.Marshal(generic)
		if err != nil {
			continue
		}
		responses = append(responses, data)
	}
	return responses
}

func (h *ProxyTestHarness) parseReferenceA2A() (*ReferenceA2AConfig, error) {
	raw, ok := h.scenario.Extra["downstream_a2a_reference"]
	if !ok {
		return nil, nil
	}
	var cfg ReferenceA2AConfig
	if err := raw.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode downstream_a2a_reference: %w", err)
	}
	if len(cfg.Interfaces) == 0 {
		return nil, fmt.Errorf("downstream_a2a_reference: interfaces must list at least one interface")
	}
	return &cfg, nil
}

func (h *ProxyTestHarness) parseMockOAuthServer() *MockOAuthServerConfig {
	raw, ok := h.scenario.Extra["mock_oauth_server"]
	if !ok {
		return nil
	}
	var cfg MockOAuthServerConfig
	if err := raw.Decode(&cfg); err != nil {
		return nil
	}
	return &cfg
}

type MockOAuthServerConfig struct {
	TokenURL         string               `yaml:"token_url"`
	AccessToken      string               `yaml:"access_token"`
	ExpiresIn        int                  `yaml:"expires_in"`
	ResponseSequence []OAuthTokenResponse `yaml:"response_sequence"`
	Error            *OAuthErrorResponse  `yaml:"error"`
	StatusCode       int                  `yaml:"status_code"`
	TLS              bool                 `yaml:"tls"`
}

func storeAuthWithoutAuthenticatingCardFetches(m map[string]any, authMap map[string]any, useAuthForCard bool) {
	if useAuthForCard {
		m["authentication"] = authMap
	} else {
		m["task_authentication"] = authMap
	}
}

func (h *ProxyTestHarness) buildProxyConfigMap(agentName string, authCfg *ProxyAuthConfig) map[string]any {
	m := map[string]any{
		"name":                          agentName,
		"namespace":                     "test-ns",
		"agent_card_path":               "/.well-known/agent-card.json",
		"ssl_verify":                    false,
		"discovery_interval_seconds":    0,
		"task_state_ttl_minutes":        60,
		"task_cleanup_interval_minutes": 0,
	}
	if h.reference != nil {
		m["url"] = h.reference.Server.URL
		m["use_agent_card_url"] = true
	} else {
		m["url"] = h.mockServer.Server.URL
		m["use_agent_card_url"] = false
	}
	if raw, ok := h.scenario.Extra["proxy_protocol_version"]; ok {
		var pin string
		if err := raw.Decode(&pin); err != nil {
			h.t.Fatalf("decode proxy_protocol_version: %v", err)
		}
		m["protocol_version"] = pin
	}
	if h.reference != nil && h.reference.cfg.Signed {
		m["trusted_card_jwks_urls"] = []any{h.reference.JWKSURL()}
	}
	if raw, ok := h.scenario.Extra["proxy_require_signed_card"]; ok {
		var require bool
		if err := raw.Decode(&require); err != nil {
			h.t.Fatalf("decode proxy_require_signed_card: %v", err)
		}
		m["require_signed_card"] = require
		if _, trusted := m["trusted_card_jwks_urls"]; !trusted {
			m["trusted_card_jwks_urls"] = []any{unsignedReferenceJWKSURL}
		}
	}

	if authCfg != nil {
		if authCfg.UseAgentCardURL != nil {
			m["use_agent_card_url"] = *authCfg.UseAgentCardURL
		}
		if authCfg.ConvertProgressUpdate != nil {
			m["convert_progress_updates"] = *authCfg.ConvertProgressUpdate
		}
		if authCfg.Authentication != nil {
			auth := authCfg.Authentication
			authMap := map[string]any{
				"type": auth.Type,
			}
			if auth.Token != "" {
				authMap["token"] = auth.Token
			}
			if auth.ClientID != "" {
				authMap["client_id"] = auth.ClientID
			}
			if auth.ClientSecret != "" {
				authMap["client_secret"] = auth.ClientSecret
			}
			if auth.TokenURL != "" {
				if h.mockOAuth != nil {
					authMap["token_url"] = h.mockOAuth.Server.URL + "/token"
					if !strings.HasPrefix(h.mockOAuth.Server.URL, "https://") {
						authMap["allow_insecure_token_url"] = true
					}
				} else {
					authMap["token_url"] = auth.TokenURL
				}
			}
			if auth.TokenCacheDurationSeconds > 0 {
				authMap["token_cache_duration_seconds"] = auth.TokenCacheDurationSeconds
			}
			if auth.CACertPathMode == "test-server" && h.mockOAuth != nil {
				if caPEM := h.mockOAuth.CACertPEM(); len(caPEM) > 0 {
					caFile := filepath.Join(h.t.TempDir(), "oauth-ca.pem")
					if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
						h.t.Fatalf("write mock OAuth CA cert: %v", err)
					}
					authMap["ca_cert_path"] = caFile
				}
			}
			if auth.InsecureSkipVerify {
				authMap["insecure_skip_verify"] = true
			}
			if auth.MaxRetries != nil {
				authMap["max_retries"] = *auth.MaxRetries
			}
			if auth.TokenRefreshBufferSeconds > 0 {
				authMap["token_refresh_buffer_seconds"] = auth.TokenRefreshBufferSeconds
			}
			useAuthForCard := authCfg.UseAuthForAgentCard != nil && *authCfg.UseAuthForAgentCard
			storeAuthWithoutAuthenticatingCardFetches(m, authMap, useAuthForCard)
		}
		if len(authCfg.AgentCardHeaders) > 0 {
			var yamlShapedHeaders []any
			for _, hdr := range authCfg.AgentCardHeaders {
				yamlShapedHeaders = append(yamlShapedHeaders, map[string]any{"name": hdr["name"], "value": hdr["value"]})
			}
			m["agent_card_headers"] = yamlShapedHeaders
		}
		if len(authCfg.TaskHeaders) > 0 {
			var headers []any
			for _, hdr := range authCfg.TaskHeaders {
				headers = append(headers, map[string]any{"name": hdr["name"], "value": hdr["value"]})
			}
			m["task_headers"] = headers
		}
	}

	if agentName == "TestAgent_Proxied_NoConvert" {
		m["convert_progress_updates"] = false
	}

	return m
}

func (h *ProxyTestHarness) setupArtifacts(ctx context.Context) error {
	agentName, userID, sessionID := h.resolveScope()

	for _, sa := range h.scenario.SetupArtifacts {
		appName := agentName
		if sa.AppName != "" {
			appName = sa.AppName
		}

		loc := h.artSvc.Resolve(appName, userID, sessionID, sa.Filename)
		content := []byte(sa.Content)

		version, err := h.artSvc.Inner.Save(ctx, loc, content, sa.MimeType)
		if err != nil {
			return fmt.Errorf("store artifact %q: %w", sa.Filename, err)
		}
		if sa.Description != "" {
			meta := &artifact.VersionMetadata{
				MIMEType:    sa.MimeType,
				SizeBytes:   int64(len(content)),
				Description: sa.Description,
			}
			if err := h.artSvc.Inner.SaveMetadata(ctx, loc, version, meta); err != nil {
				return fmt.Errorf("save artifact metadata %q: %w", sa.Filename, err)
			}
		}
	}
	return nil
}

func NewMockDownstreamServer(responses []json.RawMessage, httpError *HTTPErrorConfig, auth *DownstreamAgentAuth, streaming bool) *MockDownstreamServer {
	ms := &MockDownstreamServer{
		responses:  responses,
		httpError:  httpError,
		authConfig: auth,
		streaming:  streaming,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent-card.json", ms.handleAgentCard)
	mux.HandleFunc("/", ms.handleRequest)

	ms.Server = httptest.NewServer(mux)
	return ms
}

func (ms *MockDownstreamServer) SetAgentCardFailCount(n int) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.agentCardFailRemaining = n
}

func (ms *MockDownstreamServer) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	ms.mu.Lock()
	ms.requests = append(ms.requests, RecordedRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header.Clone(),
	})
	ms.agentCardRequests++
	shouldFail := ms.agentCardRequests > 1 && ms.agentCardFailRemaining > 0
	if shouldFail {
		ms.agentCardFailRemaining--
	}
	ms.mu.Unlock()

	if shouldFail {
		http.Error(w, "simulated agent-card fetch failure", http.StatusServiceUnavailable)
		return
	}

	card := a2a.AgentCard{
		Name:               "TestDownstreamAgent",
		Description:        "A test downstream agent",
		URL:                ms.Server.URL,
		Version:            "1.0.0",
		ProtocolVersion:    "0.3.0",
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             []a2a.AgentSkill{},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(card)
}

func (ms *MockDownstreamServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		ms.handleAgentCard(w, r)
		return
	}

	body, _ := io.ReadAll(r.Body)
	defer func() { _ = r.Body.Close() }()

	ms.mu.Lock()
	ms.requests = append(ms.requests, RecordedRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header.Clone(),
		Body:    body,
	})
	ms.mu.Unlock()

	if ms.authConfig != nil && ms.authConfig.Enabled {
		ms.mu.Lock()
		shouldFail := ms.authConfig.ShouldFailOnce && !ms.failedOnce
		if shouldFail {
			ms.failedOnce = true
		}
		ms.mu.Unlock()

		if shouldFail {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		authHeader := r.Header.Get("Authorization")
		switch ms.authConfig.Type {
		case "bearer":
			expected := "Bearer " + ms.authConfig.ExpectedValue
			if authHeader != expected {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
		case "apikey":
			apiKey := r.Header.Get("X-Api-Key")
			if apiKey != ms.authConfig.ExpectedValue {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
		}
	}

	if ms.httpError != nil {
		w.WriteHeader(ms.httpError.StatusCode)
		if ms.httpError.ErrorBody != nil {
			_ = json.NewEncoder(w).Encode(ms.httpError.ErrorBody)
		}
		return
	}

	ms.mu.Lock()
	responses := ms.responses
	ms.mu.Unlock()

	if len(responses) == 0 {
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
				return
			}
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var req a2a.Request
	_ = json.Unmarshal(body, &req)

	useSSE := ms.streaming || strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	if !useSSE {
		for _, resp := range responses {
			var obj map[string]any
			if err := json.Unmarshal(resp, &obj); err == nil {
				if kind, _ := obj["kind"].(string); kind == "status-update" {
					useSSE = true
					break
				}
			}
		}
	}

	if useSSE {
		ms.handleStreamingResponse(w, r, req, responses)
	} else {
		ms.handleNonStreamingResponse(w, req, responses)
	}
}

func (ms *MockDownstreamServer) handleNonStreamingResponse(w http.ResponseWriter, req a2a.Request, responses []json.RawMessage) {
	if len(responses) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var taskResp json.RawMessage
	for _, resp := range responses {
		var obj map[string]any
		if err := json.Unmarshal(resp, &obj); err == nil {
			if kind, _ := obj["kind"].(string); kind == "task" {
				taskResp = resp
				break
			}
		}
	}
	if taskResp == nil {
		taskResp = responses[0]
	}

	rpcResp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  taskResp,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResp)
}

func (ms *MockDownstreamServer) handleStreamingResponse(w http.ResponseWriter, r *http.Request, req a2a.Request, responses []json.RawMessage) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for _, resp := range responses {
		rpcResp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  resp,
		}
		data, _ := json.Marshal(rpcResp)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
	}
}

func (ms *MockDownstreamServer) RecordedRequests() []RecordedRequest {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	result := make([]RecordedRequest, len(ms.requests))
	copy(result, ms.requests)
	return result
}

func (ms *MockDownstreamServer) Close() {
	if ms.Server != nil {
		ms.Server.Close()
	}
}

func newMockOAuthServer(cfg *MockOAuthServerConfig) *MockOAuthServer {
	ms := &MockOAuthServer{}

	if cfg.Error != nil {
		ms.errorResponse = cfg.Error
		if ms.errorResponse.StatusCode == 0 {
			ms.errorResponse.StatusCode = cfg.StatusCode
			if ms.errorResponse.StatusCode == 0 {
				ms.errorResponse.StatusCode = 401
			}
		}
	} else if len(cfg.ResponseSequence) > 0 {
		ms.tokenResponses = cfg.ResponseSequence
	} else {
		ms.tokenResponses = []OAuthTokenResponse{{
			AccessToken: cfg.AccessToken,
			ExpiresIn:   cfg.ExpiresIn,
			TokenType:   "Bearer",
		}}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/token", ms.handleTokenRequest)
	if cfg.TLS {
		ms.Server = httptest.NewTLSServer(mux)
	} else {
		ms.Server = httptest.NewServer(mux)
	}

	return ms
}

func (ms *MockOAuthServer) CACertPEM() []byte {
	if ms == nil || ms.Server == nil {
		return nil
	}
	cert := ms.Server.Certificate()
	if cert == nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

func (ms *MockOAuthServer) handleTokenRequest(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	defer func() { _ = r.Body.Close() }()

	ms.mu.Lock()
	ms.recordedRequests = append(ms.recordedRequests, RecordedRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header.Clone(),
		Body:    body,
	})

	if ms.errorResponse != nil {
		ms.mu.Unlock()
		w.WriteHeader(ms.errorResponse.StatusCode)
		_ = json.NewEncoder(w).Encode(ms.errorResponse)
		return
	}

	idx := ms.currentIndex
	if idx >= len(ms.tokenResponses) {
		idx = len(ms.tokenResponses) - 1
	}
	resp := ms.tokenResponses[idx]
	ms.currentIndex++
	ms.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (ms *MockOAuthServer) RecordedRequests() []RecordedRequest {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	result := make([]RecordedRequest, len(ms.recordedRequests))
	copy(result, ms.recordedRequests)
	return result
}

func (ms *MockOAuthServer) Close() {
	if ms.Server != nil {
		ms.Server.Close()
	}
}

type proxyTestServices struct {
	brk      broker.Broker
	sessions session.Store
	arts     *artifact.ScopedService
	logger   *slog.Logger
}

func (s *proxyTestServices) Broker() broker.Broker                 { return s.brk }
func (s *proxyTestServices) Config() config.Config                 { return nil }
func (s *proxyTestServices) KVStore() runtime.KVStore              { return nil }
func (s *proxyTestServices) Cache() runtime.Cache                  { return nil }
func (s *proxyTestServices) Metrics() runtime.Metrics              { return runtime.NoopMetrics{} }
func (s *proxyTestServices) Tracer() runtime.Tracer                { return runtime.NoopTracer{} }
func (s *proxyTestServices) Artifacts() *artifact.ScopedService    { return s.arts }
func (s *proxyTestServices) Sessions() session.Store               { return s.sessions }
func (s *proxyTestServices) LLM(_ string) llm.Client               { return nil }
func (s *proxyTestServices) Authenticator() auth.Authenticator     { return auth.NoopAuthenticator{} }
func (s *proxyTestServices) ToolAuthorizer() auth.ToolAuthorizer   { return auth.NoopToolAuthorizer{} }
func (s *proxyTestServices) AgentAuthorizer() auth.AgentAuthorizer { return auth.NoopAgentAuthorizer{} }
func (s *proxyTestServices) AuditLogger() auth.AuditLogger         { return auth.NoopAuditLogger{} }
func (s *proxyTestServices) ControlAuthorizer() auth.ControlAuthorizer {
	return auth.NoopControlAuthorizer{}
}
func (s *proxyTestServices) CredentialService() auth.CredentialService {
	return auth.NoopCredentialService{}
}
func (s *proxyTestServices) Volumes() volume.Service             { return nil }
func (s *proxyTestServices) BudgetGate() *budget.Gate            { return nil }
func (s *proxyTestServices) HTTPClient() *http.Client            { return http.DefaultClient }
func (s *proxyTestServices) FeatureRegistry() *features.Registry { return nil }

func (s *proxyTestServices) UsageReporter() runtime.Component { return nil }
func (s *proxyTestServices) ModelResolver() runtime.ModelAliasResolver {
	return nil
}
func (s *proxyTestServices) Bindings() runtime.ModelBindings                { return nil }
func (s *proxyTestServices) ConnectorSpecStore() runtime.ConnectorSpecStore { return nil }
func (s *proxyTestServices) Logger() *slog.Logger                           { return s.logger }
