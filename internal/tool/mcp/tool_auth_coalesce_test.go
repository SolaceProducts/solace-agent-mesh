// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestTriggerAuthFlow_NonDelegatablePrincipalFailsWithoutPrompt(t *testing.T) {
	cases := []struct {
		name     string
		userID   string
		identity *auth.Identity
	}{
		{"system user", rbac.DefaultSystemUserSub, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mt := newOAuth2MCPTool(t)
			signalled, beganFlow, gaveUpChecked := false, false, false
			toolCtx := &tool.Context{
				UserID:            c.userID,
				Identity:          c.identity,
				TraceID:           "trace-1",
				ToolCallID:        "call-1",
				CredentialService: &fakeCredSvc{},
				SendSignal:        func(any) error { signalled = true; return nil },
				BeginAuthFlow:     func(string, string, string) bool { beganFlow = true; return true },
				AuthGaveUp:        func(string, string, string) bool { gaveUpChecked = true; return false },
			}
			res, err := mt.triggerAuthFlow(t.Context(), map[string]any{"x": 1}, toolCtx, "")
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

func TestTriggerAuthFlow_GaveUpFailsFastWithoutPrompting(t *testing.T) {
	mt := newOAuth2MCPTool(t)

	signalled := false
	beganFlow := false
	var gotU, gotT, gotC string
	toolCtx := &tool.Context{
		UserID:            "u1",
		TraceID:           "trace-1",
		AgentName:         "A",
		ToolCallID:        "call-1",
		CredentialService: &fakeCredSvc{},
		SendSignal:        func(any) error { signalled = true; return nil },
		BeginAuthFlow:     func(string, string, string) bool { beganFlow = true; return true },
		AuthGaveUp:        func(u, tr, c string) bool { gotU, gotT, gotC = u, tr, c; return true },
	}

	res, err := mt.triggerAuthFlow(t.Context(), map[string]any{"x": 1}, toolCtx, "missing_scope: chat:write")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotU != "u1" || gotT != "trace-1" || gotC != mt.authHandler.CredentialKey() {
		t.Errorf("AuthGaveUp queried (%q,%q,%q), want (u1, trace-1, %q)", gotU, gotT, gotC, mt.authHandler.CredentialKey())
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
	if !strings.Contains(res.Message, "missing_scope: chat:write") {
		t.Errorf("give-up message should carry the underlying toolMessage; got %q", res.Message)
	}
}

var _ auth.CredentialService = (*fakeCredSvc)(nil)

type fakeCredSvc struct {
	setCalled bool
}

func (f *fakeCredSvc) Get(_ context.Context, _, _, _, _ string) (string, error) { return "", nil }
func (f *fakeCredSvc) Set(_ context.Context, _, _, _, _, _ string) error {
	f.setCalled = true
	return nil
}
func (f *fakeCredSvc) Delete(_ context.Context, _, _, _, _ string) error { return nil }
func (f *fakeCredSvc) List(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

func newOAuth2MCPTool(t *testing.T) *MCPTool {
	t.Helper()
	cfg := &MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "test-cred",
	}
	return newMCPToolWithAuth(t, cfg)
}

func newSignallingOAuth2MCPTool(t *testing.T) *MCPTool {
	t.Helper()
	return newMCPToolWithAuth(t, &MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "test-cred",
		Scheme: &OAuthScheme{
			AuthorizationURL: "https://idp.example.com/authorize",
			TokenURL:         "https://idp.example.com/token",
		},
		Credential: &MCPAuthCred{ClientID: "cid"},
	})
}

func newMCPToolWithAuth(t *testing.T, cfg *MCPAuthConfig) *MCPTool {
	t.Helper()
	ah := NewAuthHandler(cfg, slog.Default())

	desc := "test tool"
	ct := &schemas.ChatTool{
		Type: schemas.ChatToolTypeFunction,
		Function: &schemas.ChatToolFunction{
			Name:        "test_tool",
			Description: &desc,
		},
	}
	tcfg := &ToolsetConfig{}
	return newMCPTool("test__tool", "client-test_tool", ct, nil, tcfg, ah, slog.Default())
}

func TestTriggerAuthFlow_FollowerDoesNotPromptOrStore(t *testing.T) {
	mt := newOAuth2MCPTool(t)

	credSvc := &fakeCredSvc{}
	signalled := false

	toolCtx := &tool.Context{
		UserID:            "u1",
		AgentName:         "A",
		ToolCallID:        "call-1",
		CredentialService: credSvc,
		SendSignal:        func(any) error { signalled = true; return nil },
		BeginAuthFlow:     func(string, string, string) bool { return false },
	}

	res, err := mt.triggerAuthFlow(t.Context(), map[string]any{"x": 1}, toolCtx, "missing_scope: chat:write")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !toolauth.IsPendingAuthFollower(res) {
		t.Errorf("expected follower result; IsPendingAuthFollower=%v, IsPendingAuthResult=%v",
			toolauth.IsPendingAuthFollower(res), toolauth.IsPendingAuthResult(res))
	}
	if got := toolauth.PendingAuthToolMessage(res); got != "missing_scope: chat:write" {
		t.Errorf("PendingAuthToolMessage = %q, want the threaded reason", got)
	}
	if signalled {
		t.Error("follower must NOT call SendSignal")
	}
	if credSvc.setCalled {
		t.Error("follower must NOT write pending state via CredentialService.Set")
	}
}

func TestTriggerAuthFlow_LeaderPathReached(t *testing.T) {
	mt := newOAuth2MCPTool(t)

	toolCtx := &tool.Context{
		UserID:            "u1",
		AgentName:         "A",
		ToolCallID:        "call-1",
		CredentialService: &fakeCredSvc{},
		SendSignal:        func(any) error { return nil },
		BeginAuthFlow:     nil,
	}

	res, _ := mt.triggerAuthFlow(t.Context(), map[string]any{}, toolCtx, "")

	if res != nil && toolauth.IsPendingAuthFollower(res) {
		t.Error("nil BeginAuthFlow must NOT produce a follower result")
	}
}

func TestTriggerAuthFlow_LeaderReleasesMarkerOnError(t *testing.T) {
	mt := newOAuth2MCPTool(t)

	var endCalled bool
	var gotUser, gotCred string

	toolCtx := &tool.Context{
		UserID:            "u1",
		AgentName:         "A",
		ToolCallID:        "call-1",
		CredentialService: &fakeCredSvc{},
		SendSignal:        func(any) error { return nil },
		BeginAuthFlow:     func(string, string, string) bool { return true },
		EndAuthFlow: func(userID, _, credentialKey string) {
			endCalled = true
			gotUser = userID
			gotCred = credentialKey
		},
	}

	res, err := mt.triggerAuthFlow(t.Context(), map[string]any{}, toolCtx, "")
	if err == nil {
		t.Fatalf("expected leader error (BuildAuthSignal must fail on minimal config), got nil; res=%v", res)
	}
	if !endCalled {
		t.Fatal("leader bailed with an error but EndAuthFlow was NOT called — marker leaks (#1749)")
	}
	if gotUser != "u1" || gotCred != "test-cred" {
		t.Errorf("EndAuthFlow called with (%q,%q); want (u1,test-cred)", gotUser, gotCred)
	}
}

func TestTriggerAuthFlow_FollowerNeverReleasesMarker(t *testing.T) {
	mt := newOAuth2MCPTool(t)

	var endCalled bool

	toolCtx := &tool.Context{
		UserID:            "u1",
		AgentName:         "A",
		ToolCallID:        "call-2",
		CredentialService: &fakeCredSvc{},
		SendSignal:        func(any) error { return nil },
		BeginAuthFlow:     func(string, string, string) bool { return false },
		EndAuthFlow:       func(string, string, string) { endCalled = true },
	}

	res, err := mt.triggerAuthFlow(t.Context(), map[string]any{}, toolCtx, "")
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

func TestTriggerAuthFlow_LeaderUndeliveredPromptFailsCall(t *testing.T) {
	cases := []struct {
		name       string
		sendSignal func(any) error
		wantCode   string
	}{
		{
			name:     "no signal channel",
			wantCode: "DISPATCH_USER_INPUT_UNDELIVERABLE",
		},
		{
			name:       "signal channel refuses the prompt",
			sendSignal: func(any) error { return errors.New("broker unavailable") },
			wantCode:   "DISPATCH_USER_INPUT_UNDELIVERABLE",
		},
		{
			name:       "signal channel never accepts sign-in",
			sendSignal: func(any) error { return tool.UnavailableInput(errors.New("workflow node cannot prompt")) },
			wantCode:   "DISPATCH_USER_INPUT_UNAVAILABLE",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mt := newSignallingOAuth2MCPTool(t)

			var signalled, endCalled bool
			var gotUser, gotCred string
			toolCtx := &tool.Context{
				UserID:            "u1",
				TraceID:           "trace-1",
				AgentName:         "A",
				ToolCallID:        "call-1",
				CredentialService: &fakeCredSvc{},
				BeginAuthFlow:     func(string, string, string) bool { return true },
				EndAuthFlow: func(userID, _, credentialKey string) {
					endCalled = true
					gotUser = userID
					gotCred = credentialKey
				},
			}
			if c.sendSignal != nil {
				toolCtx.SendSignal = func(sig any) error {
					signalled = true
					return c.sendSignal(sig)
				}
			}

			res, err := mt.triggerAuthFlow(t.Context(), map[string]any{"x": 1}, toolCtx, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.sendSignal != nil && !signalled {
				t.Fatal("leader never reached SendSignal; BuildAuthSignal must succeed on this config")
			}
			if toolauth.IsPendingAuthResult(res) {
				t.Error("an undelivered prompt must not pause the call as pending auth")
			}
			if res.Status != tool.StatusError {
				t.Errorf("Status = %q, want %q", res.Status, tool.StatusError)
			}
			if res.ErrorCode != c.wantCode {
				t.Errorf("ErrorCode = %q, want %q", res.ErrorCode, c.wantCode)
			}
			if !endCalled {
				t.Fatal("leader failed the call but EndAuthFlow was NOT called; marker leaks (#1749)")
			}
			if gotUser != "u1" || gotCred != "test-cred" {
				t.Errorf("EndAuthFlow called with (%q,%q); want (u1,test-cred)", gotUser, gotCred)
			}
		})
	}
}
