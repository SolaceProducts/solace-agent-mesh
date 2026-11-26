// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func sdkWireAuth() *remotetool.AuthDeclaration {
	return &remotetool.AuthDeclaration{
		Type: "oauth2",
		Scheme: &remotetool.AuthDeclarationScheme{
			AuthorizationURL:        "https://localhost:9786/realms/sam/protocol/openid-connect/auth",
			TokenURL:                "https://localhost:9786/realms/sam/protocol/openid-connect/token",
			Scopes:                  []string{"openid", "profile", "email"},
			TokenEndpointAuthMethod: "client_secret_post",
		},
	}
}

func deployerAuthMap(withSharedKey bool) map[string]any {
	m := map[string]any{
		"type": "oauth2",
		"credential": map[string]any{
			"client_id":     "test-client",
			"client_secret": "test-secret",
		},
	}
	if withSharedKey {
		m["credential_key"] = "oauth_probe_shared"
	}
	return m
}

func resolvedKeyFor(t *testing.T, toolName string, withSharedKey bool) string {
	t.Helper()
	cfg, err := ParseToolConfig(map[string]any{
		"tool_type": "builtin",
		"tool_name": toolName,
		"auth":      deployerAuthMap(withSharedKey),
	})
	if err != nil {
		t.Fatalf("ParseToolConfig(%s): %v", toolName, err)
	}
	rt := NewTool(cfg, nil)
	rt.initMu.Lock()
	rt.applyInitAuthLocked(sdkWireAuth())
	rt.initMu.Unlock()
	ah := rt.AuthHandler()
	if ah == nil {
		t.Fatalf("%s: no auth handler after init", toolName)
	}
	return ah.CredentialKey()
}

func TestSDKDeclaredAuth_SharedIdentityCoalesces(t *testing.T) {
	alpha := resolvedKeyFor(t, "probe_alpha", false)
	beta := resolvedKeyFor(t, "probe_beta", false)
	gamma := resolvedKeyFor(t, "probe_gamma", false)

	if alpha != beta || beta != gamma {
		t.Fatalf("expected one shared credential key for the toolset; got alpha=%q beta=%q gamma=%q", alpha, beta, gamma)
	}
	if alpha == "probe_alpha_oauth2" {
		t.Errorf("credential key is still the per-tool name default %q", alpha)
	}
}

func TestSDKDeclaredAuth_DifferentIdentityIsolated(t *testing.T) {
	shared := resolvedKeyFor(t, "probe_alpha", false)

	cfg, err := ParseToolConfig(map[string]any{
		"tool_type": "builtin",
		"tool_name": "probe_delta",
		"auth":      deployerAuthMap(false),
	})
	if err != nil {
		t.Fatalf("ParseToolConfig: %v", err)
	}
	rt := NewTool(cfg, nil)
	wire := sdkWireAuth()
	wire.Scheme.Scopes = []string{"openid", "salesforce.full"}
	rt.initMu.Lock()
	rt.applyInitAuthLocked(wire)
	rt.initMu.Unlock()

	if got := rt.AuthHandler().CredentialKey(); got == shared {
		t.Fatalf("tool with different OAuth scopes shares key %q with the base identity; should be isolated", got)
	}
}

func TestSDKDeclaredAuth_ExplicitDeployerKeyWins(t *testing.T) {
	alpha := resolvedKeyFor(t, "probe_alpha", true)
	beta := resolvedKeyFor(t, "probe_beta", true)

	if alpha != "oauth_probe_shared" || beta != "oauth_probe_shared" {
		t.Fatalf("expected explicit deployer key %q for both tools; got alpha=%q beta=%q", "oauth_probe_shared", alpha, beta)
	}
}
