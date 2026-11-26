// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type testServices struct {
	budgetGate    *budget.Gate
	brk           broker.Broker
	authenticator auth.Authenticator
	toolAuth      auth.ToolAuthorizer
	auditLogger   *recordingAuditLogger
	artifacts     *artifact.ScopedService
	bindings      runtime.ModelBindings
	logger        *slog.Logger
}

func (s *testServices) Broker() broker.Broker              { return s.brk }
func (s *testServices) Config() config.Config              { return nil }
func (s *testServices) KVStore() runtime.KVStore           { return nil }
func (s *testServices) Cache() runtime.Cache               { return nil }
func (s *testServices) Metrics() runtime.Metrics           { return runtime.NoopMetrics{} }
func (s *testServices) Tracer() runtime.Tracer             { return runtime.NoopTracer{} }
func (s *testServices) LLM(_ string) llm.Client            { return nil }
func (s *testServices) Artifacts() *artifact.ScopedService { return s.artifacts }
func (s *testServices) Sessions() session.Store            { return nil }

func (s *testServices) Authenticator() auth.Authenticator {
	if s.authenticator != nil {
		return s.authenticator
	}
	return auth.NoopAuthenticator{}
}

func (s *testServices) ToolAuthorizer() auth.ToolAuthorizer {
	if s.toolAuth != nil {
		return s.toolAuth
	}
	return auth.NoopToolAuthorizer{}
}

func (s *testServices) AgentAuthorizer() auth.AgentAuthorizer {
	return auth.NoopAgentAuthorizer{}
}

func (s *testServices) AuditLogger() auth.AuditLogger {
	if s.auditLogger != nil {
		return s.auditLogger
	}
	return auth.NoopAuditLogger{}
}

func (s *testServices) CredentialService() auth.CredentialService {
	return auth.NoopCredentialService{}
}

func (s *testServices) ControlAuthorizer() auth.ControlAuthorizer {
	return auth.NoopControlAuthorizer{}
}
func (s *testServices) Volumes() volume.Service             { return nil }
func (s *testServices) BudgetGate() *budget.Gate            { return s.budgetGate }
func (s *testServices) HTTPClient() *http.Client            { return http.DefaultClient }
func (s *testServices) FeatureRegistry() *features.Registry { return nil }

func (s *testServices) UsageReporter() runtime.Component { return nil }
func (s *testServices) ModelResolver() runtime.ModelAliasResolver {
	return nil
}
func (s *testServices) Bindings() runtime.ModelBindings                { return s.bindings }
func (s *testServices) ConnectorSpecStore() runtime.ConnectorSpecStore { return nil }
func (s *testServices) Logger() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

type recordingAuditLogger struct {
	authEvents     []auth.AuthEvent
	toolAccess     []auth.ToolAccessEvent
	toolExecutions []auth.ToolExecutionEvent
}

func (r *recordingAuditLogger) LogAuth(_ context.Context, e auth.AuthEvent) {
	r.authEvents = append(r.authEvents, e)
}
func (r *recordingAuditLogger) LogToolAccess(_ context.Context, e auth.ToolAccessEvent) {
	r.toolAccess = append(r.toolAccess, e)
}
func (r *recordingAuditLogger) LogAgentAccess(_ context.Context, _ auth.AgentAccessEvent) {}
func (r *recordingAuditLogger) LogToolExecution(_ context.Context, e auth.ToolExecutionEvent) {
	r.toolExecutions = append(r.toolExecutions, e)
}
func (r *recordingAuditLogger) LogControlAccess(_ context.Context, _ auth.ControlAccessEvent)       {}
func (r *recordingAuditLogger) LogShareGrant(_ context.Context, _ auth.ShareGrantEvent)             {}
func (r *recordingAuditLogger) LogShareLevelChange(_ context.Context, _ auth.ShareLevelChangeEvent) {}
func (r *recordingAuditLogger) LogShareRevoke(_ context.Context, _ auth.ShareRevokeEvent)           {}
func (r *recordingAuditLogger) LogTemplateDeleted(_ context.Context, _ auth.TemplateDeletedEvent)   {}
func (r *recordingAuditLogger) LogActivationChange(_ context.Context, _ auth.ActivationChangeEvent) {}
func (r *recordingAuditLogger) LogConsentChange(_ context.Context, _ auth.ConsentChangeEvent)       {}

type fakeAuthenticator struct {
	identity *auth.Identity
	err      error
}

func (a *fakeAuthenticator) Authenticate(_ context.Context, _ string, _ string) (*auth.Identity, error) {
	return a.identity, a.err
}

type denyToolAuthorizer struct {
	denied map[string]bool
	audit  auth.AuditLogger
}

func (d *denyToolAuthorizer) AuthorizeTool(ctx context.Context, identity *auth.Identity, toolName string, _ []string) error {
	denied := d.denied[toolName]
	if d.audit != nil {
		userID := ""
		if identity != nil {
			userID = identity.UserID
		}
		d.audit.LogToolAccess(ctx, auth.ToolAccessEvent{
			UserID:   userID,
			ToolName: toolName,
			Allowed:  !denied,
		})
	}
	if denied {
		return fmt.Errorf("access denied for tool %q", toolName)
	}
	return nil
}

func (d *denyToolAuthorizer) FilterTools(_ context.Context, _ *auth.Identity, tools []auth.ToolInfo) []auth.ToolInfo {
	var result []auth.ToolInfo
	for _, t := range tools {
		if !d.denied[t.Name] {
			result = append(result, t)
		}
	}
	return result
}

func newTestBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func writeTestManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"echo_tool", "secret_tool"} {
		stub := filepath.Join(dir, name)
		if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `version: 1
tools:
  echo_tool:
    executable: ./echo_tool
    parameters:
      type: object
      properties: {}
  secret_tool:
    executable: ./secret_tool
    parameters:
      type: object
      properties: {}
`
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestWorker(t *testing.T, svc runtime.Services, manifestPath string) *Worker {
	t.Helper()
	cfg := WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox: SandboxConfig{
			Mode: "direct",
		},
	}
	return NewWorker(svc, cfg)
}

func invokeToolOverBroker(
	t *testing.T,
	ctx context.Context, //nolint:revive // t before ctx for test helper convention
	brk broker.Broker,
	toolName string,
	userProps map[string]string,
) (json.RawMessage, error) {
	t.Helper()

	replyTopic := fmt.Sprintf("test-ns/reply/%s/%d", toolName, time.Now().UnixNano())

	replyQ, err := brk.CreateQueue(replyTopic, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(replyQ, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	if userProps == nil {
		userProps = map[string]string{}
	}
	userProps[remotetool.PropReplyTo] = replyTopic
	userProps[remotetool.PropStatusTo] = ""
	userProps[remotetool.PropClientID] = "test-agent"
	userProps[remotetool.PropUserID] = "test-user"

	params := remotetool.InvokeParams{
		ToolName:  toolName,
		TaskID:    "task-123",
		SessionID: "sess-abc",
		Args:      map[string]any{"message": "hello"},
	}
	reqID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	jsonRPC := a2a.NewRequest(reqID, remotetool.MethodInvoke, params)
	payload, err := json.Marshal(jsonRPC)
	if err != nil {
		t.Fatal(err)
	}

	invokeTopic := remotetool.InvokeTopic("test-ns", toolName)
	if pubErr := brk.Publish(ctx, invokeTopic, &broker.Message{
		Payload:        payload,
		UserProperties: userProps,
		CorrelationID:  reqID,
	}); pubErr != nil {
		t.Fatal(pubErr)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := brk.Receive(recvCtx, replyQ)
	if err != nil {
		return nil, fmt.Errorf("no response received: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("nil response")
	}
	resp.Ack()

	return resp.Payload, nil
}

func parseErrorResponse(t *testing.T, payload json.RawMessage) (remotetool.ErrorCode, string) {
	t.Helper()
	var resp struct {
		Error *remotetool.InvokeError `json:"error"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response, got success")
	}
	return resp.Error.Code, resp.Error.Message
}

func isErrorResponse(payload json.RawMessage) bool {
	var resp struct {
		Error *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return false
	}
	return resp.Error != nil
}

func newConfigWorker(t *testing.T) *Worker {
	t.Helper()
	return NewWorker(&testServices{}, WorkerConfig{WorkerID: "cfg", Namespace: "test-ns"})
}

func TestResolveForwardedUserConfig_RehydratesFromUserProperties(t *testing.T) {
	w := newConfigWorker(t)
	blob := `{"user_profile":{"id":"u-1","work_email":"u-1@corp.com"}}`
	params := &remotetool.InvokeParams{TaskID: "t-1"}
	up := remotetool.UserProperties{A2AUserConfig: blob}

	w.resolveForwardedUserConfig(params, up, nil)

	if params.A2AUserConfig != blob {
		t.Errorf("blob not rehydrated from UserProperties: got %q", params.A2AUserConfig)
	}
}

func TestResolveForwardedUserConfig_ParamsTakePrecedence(t *testing.T) {
	w := newConfigWorker(t)
	fromParams := `{"user_profile":{"id":"p","work_email":"p@corp.com"}}`
	params := &remotetool.InvokeParams{TaskID: "t-1", A2AUserConfig: fromParams}
	up := remotetool.UserProperties{A2AUserConfig: `{"user_profile":{"id":"up"}}`}

	w.resolveForwardedUserConfig(params, up, nil)

	if params.A2AUserConfig != fromParams {
		t.Errorf("params blob should win; got %q", params.A2AUserConfig)
	}
}

func TestResolveForwardedUserConfig_PinsProfileToJWT(t *testing.T) {
	w := newConfigWorker(t)
	identity := &auth.Identity{UserID: "real@corp.com", Claims: map[string]any{"email": "real@corp.com"}}
	params := &remotetool.InvokeParams{
		TaskID:        "t-1",
		A2AUserConfig: `{"user_profile":{"id":"victim","work_email":"victim@corp.com","department":"R&D"}}`,
	}

	w.resolveForwardedUserConfig(params, remotetool.UserProperties{}, identity)

	var cfg struct {
		Profile map[string]any `json:"user_profile"`
	}
	if err := json.Unmarshal([]byte(params.A2AUserConfig), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Profile["id"] != "real@corp.com" || cfg.Profile["work_email"] != "real@corp.com" {
		t.Errorf("identity not pinned to JWT: %v", cfg.Profile)
	}
	if cfg.Profile["department"] != "R&D" {
		t.Errorf("enrichment field dropped: %v", cfg.Profile)
	}
}

func TestResolveForwardedUserConfig_DropsOversizeBlob(t *testing.T) {
	w := newConfigWorker(t)
	huge := `{"user_profile":{"id":"` + strings.Repeat("a", maxA2AUserConfigBytes) + `"}}`
	params := &remotetool.InvokeParams{TaskID: "t-1", A2AUserConfig: huge}

	w.resolveForwardedUserConfig(params, remotetool.UserProperties{}, nil)

	if params.A2AUserConfig != "" {
		t.Errorf("oversize blob should be dropped, got %d bytes", len(params.A2AUserConfig))
	}
}

func TestWorkerCommunityMode(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	payload, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", nil)
	if err != nil {
		t.Fatal(err)
	}

	if isErrorResponse(payload) {
		code, _ := parseErrorResponse(t, payload)
		if code == remotetool.ErrAuthenticationFailed {
			t.Error("no-auth mode should not produce auth errors")
		}
	}
}

func TestWorkerAuthenticationFailure(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk: brk,
		authenticator: &fakeAuthenticator{
			err: fmt.Errorf("invalid JWT signature"),
		},
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	userProps := map[string]string{
		remotetool.PropAuthToken: "bad-jwt-token",
	}
	payload, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", userProps)
	if err != nil {
		t.Fatal(err)
	}

	code, msg := parseErrorResponse(t, payload)
	if code != remotetool.ErrAuthenticationFailed {
		t.Errorf("error code = %q, want %q", code, remotetool.ErrAuthenticationFailed)
	}
	if msg == "" {
		t.Error("expected non-empty error message")
	}

	if len(auditLog.authEvents) != 1 {
		t.Fatalf("expected 1 auth event, got %d", len(auditLog.authEvents))
	}
	if auditLog.authEvents[0].Success {
		t.Error("auth event should be a failure")
	}
	if auditLog.authEvents[0].TaskID != "task-123" {
		t.Errorf("auth event task_id = %q, want %q", auditLog.authEvents[0].TaskID, "task-123")
	}
}

func TestWorkerToolAuthorizationDenied(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk: brk,
		authenticator: &fakeAuthenticator{
			identity: &auth.Identity{
				UserID:    "alice@example.com",
				Scopes:    []string{"tool:echo_tool:invoke"},
				AuthToken: "valid-jwt",
			},
		},
		toolAuth: &denyToolAuthorizer{
			denied: map[string]bool{"secret_tool": true},
			audit:  auditLog,
		},
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	userProps := map[string]string{
		remotetool.PropAuthToken: "valid-jwt",
	}
	payload, err := invokeToolOverBroker(t, ctx, brk, "secret_tool", userProps)
	if err != nil {
		t.Fatal(err)
	}

	code, _ := parseErrorResponse(t, payload)
	if code != remotetool.ErrAuthenticationFailed {
		t.Errorf("error code = %q, want %q", code, remotetool.ErrAuthenticationFailed)
	}

	if len(auditLog.authEvents) != 1 {
		t.Fatalf("expected 1 auth event, got %d", len(auditLog.authEvents))
	}
	if !auditLog.authEvents[0].Success {
		t.Error("auth should have succeeded")
	}

	if len(auditLog.toolAccess) != 1 {
		t.Fatalf("expected 1 tool access event, got %d", len(auditLog.toolAccess))
	}
	if auditLog.toolAccess[0].Allowed {
		t.Error("tool access should be denied")
	}
	if auditLog.toolAccess[0].ToolName != "secret_tool" {
		t.Errorf("tool access tool = %q, want %q", auditLog.toolAccess[0].ToolName, "secret_tool")
	}
	if auditLog.toolAccess[0].UserID != "alice@example.com" {
		t.Errorf("tool access user = %q, want %q", auditLog.toolAccess[0].UserID, "alice@example.com")
	}

	if len(auditLog.toolExecutions) != 0 {
		t.Errorf("expected 0 tool execution events, got %d", len(auditLog.toolExecutions))
	}
}

func TestWorkerToolAuthorizationAllowed(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk: brk,
		authenticator: &fakeAuthenticator{
			identity: &auth.Identity{
				UserID:    "alice@example.com",
				Scopes:    []string{"tool:echo_tool:invoke"},
				AuthToken: "valid-jwt",
			},
		},
		toolAuth: &denyToolAuthorizer{
			denied: map[string]bool{"secret_tool": true},
			audit:  auditLog,
		},
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	userProps := map[string]string{
		remotetool.PropAuthToken: "valid-jwt",
	}
	payload, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", userProps)
	if err != nil {
		t.Fatal(err)
	}

	if len(auditLog.authEvents) != 1 {
		t.Fatalf("expected 1 auth event, got %d", len(auditLog.authEvents))
	}
	if !auditLog.authEvents[0].Success {
		t.Error("auth should have succeeded")
	}

	if len(auditLog.toolAccess) != 1 {
		t.Fatalf("expected 1 tool access event, got %d", len(auditLog.toolAccess))
	}
	if !auditLog.toolAccess[0].Allowed {
		t.Error("tool access should be allowed")
	}

	if len(auditLog.toolExecutions) != 1 {
		t.Fatalf("expected 1 tool execution event, got %d", len(auditLog.toolExecutions))
	}
	execEvent := auditLog.toolExecutions[0]
	if execEvent.UserID != "alice@example.com" {
		t.Errorf("execution user = %q, want %q", execEvent.UserID, "alice@example.com")
	}
	if execEvent.ToolName != "echo_tool" {
		t.Errorf("execution tool = %q, want %q", execEvent.ToolName, "echo_tool")
	}
	if execEvent.AgentName != "test-agent" {
		t.Errorf("execution agent = %q, want %q", execEvent.AgentName, "test-agent")
	}
	if execEvent.DurationMs <= 0 {
		t.Errorf("execution duration should be positive, got %f", execEvent.DurationMs)
	}

	if isErrorResponse(payload) {
		code, _ := parseErrorResponse(t, payload)
		if code == remotetool.ErrAuthenticationFailed {
			t.Error("should not get auth error for authorized tool")
		}
	}
}

func TestWorkerScopesComeFromJWTNotBody(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	jwtScopes := []string{"tool:echo_tool:invoke", "tool:artifact:*"}

	var capturedIdentity *auth.Identity
	scopeCapturingAuth := &scopeCapturingAuthorizer{captured: &capturedIdentity}

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk: brk,
		authenticator: &fakeAuthenticator{
			identity: &auth.Identity{
				UserID:    "bob@example.com",
				AuthToken: "valid-jwt",
				Scopes:    jwtScopes,
			},
		},
		toolAuth:    scopeCapturingAuth,
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	userCfg := `{"_enterprise_capabilities": ["tool:echo_tool:invoke", "tool:artifact:*", "agent:Stripe:invoke"]}`
	userProps := map[string]string{
		remotetool.PropAuthToken:     "valid-jwt",
		remotetool.PropA2AUserConfig: userCfg,
	}
	_, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", userProps)
	if err != nil {
		t.Fatal(err)
	}

	if capturedIdentity == nil {
		t.Fatal("identity was not captured by authorizer")
	}
	if len(capturedIdentity.Scopes) != len(jwtScopes) {
		t.Fatalf("expected %d JWT scopes, got %d: %v (must NOT be inflated by unsigned body)",
			len(jwtScopes), len(capturedIdentity.Scopes), capturedIdentity.Scopes)
	}
	want := map[string]bool{}
	for _, s := range jwtScopes {
		want[s] = true
	}
	for _, s := range capturedIdentity.Scopes {
		if !want[s] {
			t.Errorf("unexpected scope %q (only JWT scopes %v should be present)", s, jwtScopes)
		}
	}
}

type scopeCapturingAuthorizer struct {
	captured **auth.Identity
}

func (s *scopeCapturingAuthorizer) AuthorizeTool(_ context.Context, identity *auth.Identity, _ string, _ []string) error {
	*s.captured = identity
	return nil
}

func (s *scopeCapturingAuthorizer) FilterTools(_ context.Context, _ *auth.Identity, tools []auth.ToolInfo) []auth.ToolInfo {
	return tools
}

func TestWorkerNoAuthTokenInEnterpriseRejectsAndAudits(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk: brk,
		authenticator: &fakeAuthenticator{
			err: fmt.Errorf("should not be called for empty-token path"),
		},
		toolAuth:    &denyToolAuthorizer{audit: auditLog},
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	payload, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", nil)
	if err != nil {
		t.Fatal(err)
	}

	if !isErrorResponse(payload) {
		t.Fatal("expected error response for empty-token request when auth is configured")
	}
	code, msg := parseErrorResponse(t, payload)
	if code != remotetool.ErrAuthenticationFailed {
		t.Errorf("error code = %q, want %q", code, remotetool.ErrAuthenticationFailed)
	}
	if !strings.Contains(strings.ToLower(msg), "missing auth token") {
		t.Errorf("error message %q does not mention missing token", msg)
	}

	if len(auditLog.authEvents) != 1 {
		t.Fatalf("expected 1 auth event, got %d", len(auditLog.authEvents))
	}
	ev := auditLog.authEvents[0]
	if ev.Success {
		t.Error("auth event should be a failure")
	}
	if ev.Reason != "missing auth token" {
		t.Errorf("auth reason = %q, want %q", ev.Reason, "missing auth token")
	}

	if len(auditLog.toolAccess) != 0 {
		t.Errorf("expected 0 tool access events, got %d", len(auditLog.toolAccess))
	}
}

func TestWorkerNoAuthTokenInCommunitySkipsEnterprise(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk:         brk,
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	payload, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", nil)
	if err != nil {
		t.Fatal(err)
	}

	if isErrorResponse(payload) {
		code, _ := parseErrorResponse(t, payload)
		if code == remotetool.ErrAuthenticationFailed {
			t.Error("no-auth mode (NoopAuthenticator) must not reject for missing auth token")
		}
	}
	if len(auditLog.authEvents) != 0 {
		t.Errorf("expected 0 auth events when auth not configured, got %d", len(auditLog.authEvents))
	}
}

func TestWorkerToolAccessEmittedOncePerInvocation(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk: brk,
		authenticator: &fakeAuthenticator{
			identity: &auth.Identity{
				UserID:    "alice@example.com",
				Scopes:    []string{"tool:*"},
				AuthToken: "valid-jwt",
			},
		},
		toolAuth:    &denyToolAuthorizer{audit: auditLog},
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	userProps := map[string]string{remotetool.PropAuthToken: "valid-jwt"}
	if _, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", userProps); err != nil {
		t.Fatal(err)
	}

	if got := len(auditLog.toolAccess); got != 1 {
		t.Errorf("expected exactly 1 LogToolAccess event per invocation, got %d", got)
	}
}

func TestWorkerSubscriptionAndDispatch(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	svc := &testServices{brk: brk}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	shortCtx, shortCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer shortCancel()
	payload, err := invokeToolOverBroker(t, shortCtx, brk, "nonexistent_tool", nil)
	if err == nil && payload != nil {
		if isErrorResponse(payload) {
			code, _ := parseErrorResponse(t, payload)
			if code != remotetool.ErrToolNotAvailable {
				t.Errorf("expected TOOL_NOT_AVAILABLE, got %q", code)
			}
		}
	}
}

func TestWorkerAuditLogToolExecution(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk: brk,
		authenticator: &fakeAuthenticator{
			identity: &auth.Identity{
				UserID:    "auditor@example.com",
				Scopes:    []string{"tool:*"},
				AuthToken: "audit-jwt",
			},
		},
		toolAuth:    &denyToolAuthorizer{audit: auditLog},
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	userProps := map[string]string{
		remotetool.PropAuthToken: "audit-jwt",
	}
	_, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", userProps)
	if err != nil {
		t.Fatal(err)
	}

	if len(auditLog.authEvents) != 1 {
		t.Fatalf("expected 1 auth event, got %d", len(auditLog.authEvents))
	}

	if len(auditLog.toolAccess) != 1 {
		t.Fatalf("expected 1 tool access event, got %d", len(auditLog.toolAccess))
	}
	if auditLog.toolAccess[0].UserID != "auditor@example.com" {
		t.Errorf("tool access user = %q", auditLog.toolAccess[0].UserID)
	}

	if len(auditLog.toolExecutions) != 1 {
		t.Fatalf("expected 1 tool execution event, got %d", len(auditLog.toolExecutions))
	}
	exec := auditLog.toolExecutions[0]
	if exec.UserID != "auditor@example.com" {
		t.Errorf("exec user = %q", exec.UserID)
	}
	if exec.AgentName != "test-agent" {
		t.Errorf("exec agent = %q", exec.AgentName)
	}
	if exec.ToolName != "echo_tool" {
		t.Errorf("exec tool = %q", exec.ToolName)
	}
	if exec.SessionID != "sess-abc" {
		t.Errorf("exec session = %q", exec.SessionID)
	}
	if exec.CredentialID != "" {
		t.Errorf("exec credential = %q for a person's task, want none", exec.CredentialID)
	}
}

func TestWorkerAuditLogToolExecution_NamesTheKey(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk: brk,
		authenticator: &fakeAuthenticator{
			identity: &auth.Identity{
				UserID:       "system:ci-deployer",
				AuthMethod:   "api_key",
				CredentialID: "key-1",
				Scopes:       []string{"tool:*"},
				AuthToken:    "audit-jwt",
			},
		},
		toolAuth:    &denyToolAuthorizer{audit: auditLog},
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := t.Context()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	if _, err := invokeToolOverBroker(t, ctx, brk, "echo_tool",
		map[string]string{remotetool.PropAuthToken: "audit-jwt"}); err != nil {
		t.Fatal(err)
	}
	if len(auditLog.toolExecutions) != 1 {
		t.Fatalf("expected 1 tool execution event, got %d", len(auditLog.toolExecutions))
	}
	if got := auditLog.toolExecutions[0].CredentialID; got != "key-1" {
		t.Errorf("exec credential = %q, want key-1", got)
	}
}
