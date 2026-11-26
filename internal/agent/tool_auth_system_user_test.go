// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/authwrap"
)

const (
	systemUserPrincipal = "system:nightly-sync"
	otherSystemUser     = "system:weekly-report"
	systemUserCredKey   = "google_drive"
)

func connectForSystemUser(t *testing.T, agent *Agent, principal, code string) {
	t.Helper()
	signal, err := agent.BeginToolAuth(t.Context(), systemUserCredKey, principal)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if signal.UserID != principal || signal.AuthURI == "" {
		t.Fatalf("begin signal for %q with auth URI %t", signal.UserID, signal.AuthURI != "")
	}
	state, err := a2a.StateFromAuthURI(signal.AuthURI)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := agent.handleAuthResponse(t.Context(), "callback-task", &a2a.AuthResponseData{
		Type:              "auth_response",
		TargetAgent:       signal.TargetAgent,
		TargetAgentTaskID: signal.TargetAgentTaskID,
		UserID:            principal,
		ClientID:          signal.ClientID,
		FunctionCallID:    signal.AuthRequestFunctionCallID,
		Code:              code,
		CredentialKey:     signal.CredentialKey,
		CodeVerifier:      signal.CodeVerifier,
		State:             state,
		IsCredentialSave:  true,
	}, a2a.UserProperties{UserID: principal})
	if err != nil || resp == nil || resp.Error != nil {
		t.Fatalf("auth response: err %v, resp %+v", err, resp)
	}
}

func TestSystemUserCredential_UsedByItsTasksAndNoneElse(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-for-" + r.Form.Get("code"), "refresh_token": "refresh",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(tokenSrv.Close)
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	agent := newToolAuthTestAgent(t)
	handler := toolauth.NewAuthHandler(&toolauth.AuthConfig{
		Type:          "oauth2",
		CredentialKey: systemUserCredKey,
		DisplayName:   "Google Drive",
		Scheme: &toolauth.OAuthScheme{
			AuthorizationURL:        "https://idp.example.com/authorize",
			TokenURL:                tokenSrv.URL + "/token",
			TokenEndpointAuthMethod: "client_secret_post",
		},
		Credential: &toolauth.AuthCredential{ClientID: "sam", ClientSecret: "secret"},
	}, nil)
	agent.RegisterAuthHandler(handler)
	connectForSystemUser(t, agent, systemUserPrincipal, "alice-consent")

	var used *toolauth.OAuthToken
	wrapped := authwrap.Wrap(&systemUserProbeTool{seen: &used}, handler, nil)
	call := func(principal, agentName string) (*tool.Result, bool) {
		prompted := false
		used = nil
		res, err := wrapped.Execute(context.Background(), map[string]any{}, &tool.Context{
			UserID:            principal,
			AgentName:         agentName,
			ToolCallID:        "call-1",
			TraceID:           "trace-1",
			CredentialService: agent.svc.CredentialService(),
			SendSignal:        func(any) error { prompted = true; return nil },
			BeginAuthFlow:     func(string, string, string) bool { prompted = true; return true },
		})
		if err != nil {
			t.Fatalf("execute as %s: %v", principal, err)
		}
		return res, prompted
	}

	res, prompted := call(systemUserPrincipal, agent.cfg.Name)
	if res.Status != tool.StatusSuccess || used == nil || used.AccessToken != "token-for-alice-consent" {
		t.Errorf("as %s: status %q, token %+v, want the connected token", systemUserPrincipal, res.Status, used)
	}
	if prompted {
		t.Error("a task with a connected credential was prompted to sign in")
	}

	for _, c := range []struct{ principal, agentName string }{
		{otherSystemUser, agent.cfg.Name},
		{systemUserPrincipal, "another-agent"},
	} {
		res, prompted := call(c.principal, c.agentName)
		if used != nil {
			t.Errorf("%s on %s used a credential connected for %s on %s", c.principal, c.agentName, systemUserPrincipal, agent.cfg.Name)
		}
		if prompted || toolauth.IsPendingAuthResult(res) || res.Status != tool.StatusError {
			t.Errorf("%s on %s: status %q prompted %t, want the terminal no-delegation result", c.principal, c.agentName, res.Status, prompted)
		}
		if !strings.Contains(res.Message, toolauth.NoDelegationMessage(wrapped.Name())) {
			t.Errorf("%s on %s message %q", c.principal, c.agentName, res.Message)
		}
	}
}

type systemUserProbeTool struct {
	seen **toolauth.OAuthToken
}

func (p *systemUserProbeTool) Name() string                 { return "drive_search" }
func (p *systemUserProbeTool) Description() string          { return "search drive" }
func (p *systemUserProbeTool) Parameters() *tool.JSONSchema { return nil }
func (p *systemUserProbeTool) Instructions() string         { return "" }
func (p *systemUserProbeTool) IsLongRunning() bool          { return false }
func (p *systemUserProbeTool) RawStringArgs() []string      { return nil }
func (p *systemUserProbeTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return nil
}
func (p *systemUserProbeTool) ContextParam() string { return "" }

func (p *systemUserProbeTool) Execute(_ context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if token, ok := toolCtx.OAuthToken.(*toolauth.OAuthToken); ok {
		*p.seen = token
	}
	return tool.OK("found"), nil
}
