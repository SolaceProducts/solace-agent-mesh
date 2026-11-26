// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestTriggerAuthFlow_GaveUpFailsFastWithoutPrompting(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	tl := newRemoteTool(fullOAuth2Handler())

	signalled := false
	beganFlow := false
	var gotU, gotT, gotC string
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-1",
		SendSignal:    func(any) error { signalled = true; return nil },
		BeginAuthFlow: func(string, string, string) bool { beganFlow = true; return true },
		AuthGaveUp:    func(u, tr, c string) bool { gotU, gotT, gotC = u, tr, c; return true },
	}

	res, err := tl.triggerAuthFlow(t.Context(), toolCtx, map[string]any{"x": 1}, "missing_scope: read")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wantC := tl.authHandler.Load().CredentialKey(); gotU != "u1" || gotT != "trace-1" || gotC != wantC {
		t.Errorf("AuthGaveUp queried (%q,%q,%q), want (u1, trace-1, %q)", gotU, gotT, gotC, wantC)
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
	if !strings.Contains(res.Message, "missing_scope: read") {
		t.Errorf("give-up message should carry the underlying toolMessage; got %q", res.Message)
	}
}

func TestTriggerAuthFlow_NonDelegatablePrincipalFailsWithoutPrompt(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	cases := []struct {
		name     string
		userID   string
		identity *auth.Identity
	}{
		{"system user", rbac.DefaultSystemUserSub, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tl := newRemoteTool(fullOAuth2Handler())
			signalled, beganFlow, gaveUpChecked := false, false, false
			toolCtx := &tool.Context{
				AgentName:     "A",
				UserID:        c.userID,
				Identity:      c.identity,
				TraceID:       "trace-1",
				ToolCallID:    "call-1",
				SendSignal:    func(any) error { signalled = true; return nil },
				BeginAuthFlow: func(string, string, string) bool { beganFlow = true; return true },
				AuthGaveUp:    func(string, string, string) bool { gaveUpChecked = true; return false },
			}
			res, err := tl.triggerAuthFlow(t.Context(), toolCtx, map[string]any{"x": 1}, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if toolauth.IsPendingAuthResult(res) || res.Status != tool.StatusError {
				t.Errorf("want terminal error, got status=%q pending=%v", res.Status, toolauth.IsPendingAuthResult(res))
			}
			if signalled {
				t.Error("non-delegatable principal must NOT emit an auth prompt (no SendSignal)")
			}
			if beganFlow {
				t.Error("guard must short-circuit BEFORE BeginAuthFlow")
			}
			if gaveUpChecked {
				t.Error("guard must run BEFORE the give-up check")
			}
			if !strings.Contains(res.Message, "DM me") {
				t.Errorf("message should tell the user to DM directly; got %q", res.Message)
			}
		})
	}
}

func fullOAuth2Handler() *toolauth.AuthHandler {
	return toolauth.NewAuthHandler(&toolauth.AuthConfig{
		Type:          "oauth2",
		CredentialKey: "sf-cred",
		Scheme: &toolauth.OAuthScheme{
			AuthorizationURL: "https://auth.example.com/authorize",
			TokenURL:         "https://auth.example.com/token",
		},
		Credential: &toolauth.AuthCredential{ClientID: "client-1"},
	}, slog.Default())
}

func minimalOAuth2Handler() *toolauth.AuthHandler {
	return toolauth.NewAuthHandler(&toolauth.AuthConfig{
		Type:          "oauth2",
		CredentialKey: "sf-cred",
	}, slog.Default())
}

func newRemoteTool(h *toolauth.AuthHandler) *Tool {
	tl := &Tool{cfg: &ToolConfig{ToolName: "sf"}, logger: slog.Default()}
	tl.authHandler.Store(h)
	return tl
}

func TestTriggerAuthFlow_FollowerDoesNotPrompt(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	tl := newRemoteTool(fullOAuth2Handler())

	signalled := false
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-2",
		SendSignal:    func(any) error { signalled = true; return nil },
		BeginAuthFlow: func(string, string, string) bool { return false },
	}

	res, err := tl.triggerAuthFlow(t.Context(), toolCtx, map[string]any{"x": 1}, "")
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

func TestTriggerAuthFlow_LeaderReleasesMarkerOnError(t *testing.T) {
	tl := newRemoteTool(minimalOAuth2Handler())

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

	res, _ := tl.triggerAuthFlow(t.Context(), toolCtx, map[string]any{}, "")
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

func TestTriggerAuthFlow_LeaderReleasesMarkerOnNilSendSignal(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	tl := newRemoteTool(fullOAuth2Handler())

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

	res, err := tl.triggerAuthFlow(t.Context(), toolCtx, map[string]any{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if toolauth.IsPendingAuthResult(res) {
		t.Error("nil SendSignal must NOT yield a pending-auth result")
	}
	if !endCalled {
		t.Error("nil SendSignal must release the in-flight marker so followers can re-lead")
	}
}

func TestTriggerAuthFlow_FollowerNeverReleasesMarker(t *testing.T) {
	tl := newRemoteTool(minimalOAuth2Handler())

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

	res, err := tl.triggerAuthFlow(t.Context(), toolCtx, map[string]any{}, "")
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

func TestTriggerAuthFlow_CarriesToolMessageOntoPendingResult(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	tl := newRemoteTool(fullOAuth2Handler())
	toolCtx := &tool.Context{
		AgentName:     "A",
		UserID:        "u1",
		TraceID:       "trace-1",
		ToolCallID:    "call-1",
		SendSignal:    func(any) error { return nil },
		BeginAuthFlow: func(string, string, string) bool { return true },
		EndAuthFlow:   func(string, string, string) {},
	}

	const reason = "atlassian_rest_request: 401 from Atlassian — scope does not match"
	res, err := tl.triggerAuthFlow(t.Context(), toolCtx, map[string]any{}, reason)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !toolauth.IsPendingAuthResult(res) {
		t.Fatalf("expected pending-auth result, got %+v", res)
	}
	if got := toolauth.PendingAuthToolMessage(res); got != reason {
		t.Errorf("PendingAuthToolMessage = %q, want %q", got, reason)
	}
}
