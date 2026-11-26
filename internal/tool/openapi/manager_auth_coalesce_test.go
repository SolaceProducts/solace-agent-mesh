// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func newAuthCodeTool(t *testing.T, full bool) (*Manager, *OpenAPITool) {
	t.Helper()
	cfg := &AuthConfig{Type: AuthTypeOAuth2AuthorizationCode, CredentialKey: "sf-cred"}
	if full {
		cfg.AuthorizationURL = "https://auth.example.com/authorize"
		cfg.TokenURL = "https://auth.example.com/token"
		cfg.ClientID = "client-1"
	}
	h, err := NewAuthHandlerForOpenAPI(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewAuthHandlerForOpenAPI: %v", err)
	}
	m := NewManager(slog.Default(), nil)
	return m, &OpenAPITool{name: "sf", authHandler: h, logger: slog.Default()}
}

func TestTriggerOpenAPIAuthFlow_GaveUpFailsFastWithoutPrompting(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	m, ot := newAuthCodeTool(t, true)

	signalled := false
	beganFlow := false
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-1",
		SendSignal:    func(any) error { signalled = true; return nil },
		BeginAuthFlow: func(string, string, string) bool { beganFlow = true; return true },
		AuthGaveUp:    func(string, string, string) bool { return true },
	}

	res, err := m.triggerOpenAPIAuthFlow(t.Context(), ot, toolCtx, map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if toolauth.IsPendingAuthResult(res) {
		t.Error("give-up must return a terminal result, not a pending-auth pause")
	}
	if res.Status != tool.StatusError {
		t.Errorf("Status = %q, want terminal error", res.Status)
	}
	if signalled {
		t.Error("give-up must NOT prompt again (no SendSignal)")
	}
	if beganFlow {
		t.Error("give-up must short-circuit BEFORE BeginAuthFlow (no marker claimed)")
	}
}

func TestTriggerOpenAPIAuthFlow_FollowerDoesNotPrompt(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	m, ot := newAuthCodeTool(t, true)

	signalled := false
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-2",
		SendSignal:    func(any) error { signalled = true; return nil },
		BeginAuthFlow: func(string, string, string) bool { return false },
	}

	res, err := m.triggerOpenAPIAuthFlow(t.Context(), ot, toolCtx, map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !toolauth.IsPendingAuthFollower(res) {
		t.Errorf("expected follower result; IsPendingAuthFollower=%v, IsPendingAuthResult=%v",
			toolauth.IsPendingAuthFollower(res), toolauth.IsPendingAuthResult(res))
	}
	if signalled {
		t.Error("follower must NOT call SendSignal (would produce a duplicate prompt)")
	}
}

func TestTriggerOpenAPIAuthFlow_LeaderReleasesMarkerOnError(t *testing.T) {
	m, ot := newAuthCodeTool(t, false)

	var endCalled bool
	var gotUser, gotTrace, gotCred string
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-1",
		SendSignal:    func(any) error { return nil },
		BeginAuthFlow: func(string, string, string) bool { return true },
		EndAuthFlow: func(u, tr, c string) {
			endCalled, gotUser, gotTrace, gotCred = true, u, tr, c
		},
	}

	res, _ := m.triggerOpenAPIAuthFlow(t.Context(), ot, toolCtx, map[string]any{})
	if toolauth.IsPendingAuthFollower(res) {
		t.Fatal("leader must not produce a follower result")
	}
	if !endCalled {
		t.Fatal("leader bailed on BuildAuthSignal error but EndAuthFlow was NOT called — marker leaks")
	}
	if gotUser != "u1" || gotTrace != "trace-1" || gotCred != "sf-cred" {
		t.Errorf("EndAuthFlow called with (%q,%q,%q); want (u1,trace-1,sf-cred)", gotUser, gotTrace, gotCred)
	}
}

func TestTriggerOpenAPIAuthFlow_FollowerNeverReleasesMarker(t *testing.T) {
	m, ot := newAuthCodeTool(t, false)

	var endCalled bool
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-3",
		SendSignal:    func(any) error { return nil },
		BeginAuthFlow: func(string, string, string) bool { return false },
		EndAuthFlow:   func(string, string, string) { endCalled = true },
	}

	res, err := m.triggerOpenAPIAuthFlow(t.Context(), ot, toolCtx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !toolauth.IsPendingAuthFollower(res) {
		t.Errorf("expected follower result; IsPendingAuthFollower=%v", toolauth.IsPendingAuthFollower(res))
	}
	if endCalled {
		t.Error("follower never held the marker and must NOT call EndAuthFlow")
	}
}

func TestTriggerOpenAPIAuthFlow_ToolsetExchangeNeverCoalesces(t *testing.T) {
	h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
		Type:          AuthTypeOAuth2ClientCredentials,
		CredentialKey: "sf-cred",
		TokenURL:      "https://auth.example.com/token",
		ClientID:      "client-1",
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewAuthHandlerForOpenAPI: %v", err)
	}
	m := NewManager(slog.Default(), nil)
	ot := &OpenAPITool{name: "sf", authHandler: h, logger: slog.Default()}

	beginCalled := false
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-1",
		BeginAuthFlow: func(string, string, string) bool { beginCalled = true; return true },
	}

	res, _ := m.triggerOpenAPIAuthFlow(t.Context(), ot, toolCtx, map[string]any{})
	if beginCalled {
		t.Error("client_credentials must NOT acquire the coalescing marker (no user prompt to coalesce)")
	}
	if toolauth.IsPendingAuthResult(res) {
		t.Error("client_credentials exchange failure must return a terminal error, not a pending-auth result")
	}
}

func TestTriggerOpenAPIAuthFlow_LeaderReleasesMarkerOnSendError(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	m, ot := newAuthCodeTool(t, true)

	var endCalled bool
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-1",
		SendSignal:    func(any) error { return errors.New("boom") },
		BeginAuthFlow: func(string, string, string) bool { return true },
		EndAuthFlow:   func(string, string, string) { endCalled = true },
	}

	res, err := m.triggerOpenAPIAuthFlow(t.Context(), ot, toolCtx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if toolauth.IsPendingAuthResult(res) {
		t.Error("send failure must NOT yield a pending-auth result — the prompt never reached the user")
	}
	if !endCalled {
		t.Error("send failure must release the in-flight marker so followers can re-lead")
	}
}

func TestTriggerOpenAPIAuthFlow_LeaderReleasesMarkerOnNilSendSignal(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	m, ot := newAuthCodeTool(t, true)

	var endCalled bool
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-1",
		SendSignal:    nil,
		BeginAuthFlow: func(string, string, string) bool { return true },
		EndAuthFlow:   func(string, string, string) { endCalled = true },
	}

	res, err := m.triggerOpenAPIAuthFlow(t.Context(), ot, toolCtx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if toolauth.IsPendingAuthResult(res) {
		t.Error("nil SendSignal must NOT yield a pending-auth result")
	}
	if !endCalled {
		t.Error("nil SendSignal must release the in-flight marker")
	}
}
