// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestDeployerAuthFlowsToAuthorizationURL(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	sdkAuthJSON := marshalJSON(t, remotetool.AuthDeclaration{
		Type: "oauth2",
		Scheme: &remotetool.AuthDeclarationScheme{
			AuthorizationURL: "https://auth.atlassian.com/authorize",
			TokenURL:         "https://auth.atlassian.com/oauth/token",
			Scopes:           []string{"read:jira-work", "offline_access"},
		},
	})
	f := store.ToolPackageFile{ToolName: "atlassian_rest_request", Auth: &sdkAuthJSON}

	configValues := map[string]any{
		"base_url": "https://api.atlassian.com",
		"auth": map[string]any{
			"credential": map[string]any{"client_id": "test-cid-from-vault"},
			"scheme":     map[string]any{"audience": "api.atlassian.com"},
		},
	}

	emitted := buildToolAuthBlock(f, deployerAuthFromConfig(configValues))
	if emitted == nil {
		t.Fatal("buildToolAuthBlock returned nil for a valid SDK + deployer auth pair")
	}

	yamlBytes, err := yaml.Marshal(emitted)
	if err != nil {
		t.Fatalf("marshal emitted auth block to YAML: %v", err)
	}
	var roundTripped map[string]any
	if err = yaml.Unmarshal(yamlBytes, &roundTripped); err != nil {
		t.Fatalf("unmarshal emitted auth YAML: %v", err)
	}

	ac := toolauth.ParseAuthConfig(roundTripped, "atlassian_rest_request")
	if ac == nil {
		t.Fatal("ParseAuthConfig returned nil — emitted block is not in the shape it understands")
	}
	if ac.Credential == nil || ac.Credential.ClientID != "test-cid-from-vault" {
		t.Fatalf("client_id did not survive the round-trip: %+v", ac.Credential)
	}
	if ac.Scheme == nil || ac.Scheme.Audience != "api.atlassian.com" {
		t.Fatalf("audience did not survive the round-trip: %+v", ac.Scheme)
	}

	handler := toolauth.NewAuthHandler(ac, slog.Default())
	toolCtx := &tool.Context{
		AgentName:    "JiraConfluence",
		ParentTaskID: "task-xyz",
		UserID:       "user1",
		ClientID:     "gateway-client",
		ToolCallID:   "call-1",
	}
	signal, err := handler.BuildAuthSignal(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("BuildAuthSignal: %v", err)
	}
	if signal == nil || signal.AuthURI == "" {
		t.Fatal("BuildAuthSignal returned no AuthURI")
	}

	parsed, err := url.Parse(signal.AuthURI)
	if err != nil {
		t.Fatalf("parse AuthURI: %v", err)
	}
	if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != "https://auth.atlassian.com/authorize" {
		t.Errorf("authorization endpoint = %q, want SDK-declared https://auth.atlassian.com/authorize", got)
	}
	q := parsed.Query()
	if got := q.Get("client_id"); got != "test-cid-from-vault" {
		t.Errorf("client_id query param = %q, want deployer-supplied test-cid-from-vault", got)
	}
	if got := q.Get("audience"); got != "api.atlassian.com" {
		t.Errorf("audience query param = %q, want deployer-supplied api.atlassian.com — without this Atlassian's 3LO rejects the authorize request", got)
	}
	if got := q.Get("response_type"); got != "code" {
		t.Errorf("response_type = %q, want code", got)
	}
	scope := q.Get("scope")
	if !strings.Contains(scope, "read:jira-work") || !strings.Contains(scope, "offline_access") {
		t.Errorf("scope = %q, want SDK-declared Atlassian scopes", scope)
	}
	if q.Get("state") == "" {
		t.Error("state query param is empty — CSRF state must be present")
	}
	if q.Get("code_challenge") == "" {
		t.Error("code_challenge query param is empty — PKCE challenge must be present for a public client flow")
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if got := q.Get("redirect_uri"); got != "http://localhost:8000/api/v1/auth/tool/callback" {
		t.Errorf("redirect_uri = %q, want the OAUTH_TOOL_REDIRECT_URI env override", got)
	}
}
