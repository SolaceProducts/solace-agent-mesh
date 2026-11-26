// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestTriggerOpenAPIAuthFlow_NonDelegatablePrincipalFailsWithoutPrompt(t *testing.T) {
	cases := []struct {
		name     string
		userID   string
		identity *auth.Identity
	}{
		{"system user", rbac.DefaultSystemUserSub, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ot := newAuthCodeTool(t, true)
			signalled, beganFlow, gaveUpChecked := false, false, false
			toolCtx := &tool.Context{
				UserID:        c.userID,
				Identity:      c.identity,
				TraceID:       "trace-1",
				ToolCallID:    "call-1",
				SendSignal:    func(any) error { signalled = true; return nil },
				BeginAuthFlow: func(string, string, string) bool { beganFlow = true; return true },
				AuthGaveUp:    func(string, string, string) bool { gaveUpChecked = true; return false },
			}
			res, err := m.triggerOpenAPIAuthFlow(t.Context(), ot, toolCtx, map[string]any{"x": 1})
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

func TestTriggerOpenAPIAuthFlow_MachineAuthPrecedesNonDelegatableGuard(t *testing.T) {
	m, ot := newClientCredentialsTool(t)
	toolCtx := &tool.Context{
		UserID:     rbac.DefaultSystemUserSub,
		TraceID:    "trace-1",
		ToolCallID: "call-1",
		SendSignal: func(any) error { t.Error("machine auth must not prompt"); return nil },
	}
	res, err := m.triggerOpenAPIAuthFlow(t.Context(), ot, toolCtx, map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("machine auth failure should be a terminal error; got status=%q", res.Status)
	}
	if strings.Contains(res.Message, "DM me") {
		t.Errorf("machine-auth (shared-token) tool must NOT get the non-delegatable 'DM me' message; got %q", res.Message)
	}
	if !strings.Contains(res.Message, "exchange did not yield a usable token") {
		t.Errorf("expected the machine-auth exchange error; got %q", res.Message)
	}
}

func newClientCredentialsTool(t *testing.T) (*Manager, *OpenAPITool) {
	t.Helper()
	cfg := &AuthConfig{
		Type:          AuthTypeOAuth2ClientCredentials,
		CredentialKey: "sf-cred",
		TokenURL:      "https://auth.example.com/token",
		ClientID:      "client-1",
	}
	h, err := NewAuthHandlerForOpenAPI(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewAuthHandlerForOpenAPI: %v", err)
	}
	m := NewManager(slog.Default(), nil)
	return m, &OpenAPITool{name: "sf", authHandler: h, logger: slog.Default()}
}
