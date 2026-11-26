// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harnessboot_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harnessboot"
)

func TestRBACIdpClaimsGroups_ScopesReflectGroupRole(t *testing.T) {
	rolesFile := filepath.Join(t.TempDir(), "roles.yaml")
	rolesYAML := `roles:
  analytics_reader:
    description: "Read analytics."
    scopes:
      - "analytics:_:read"
  base_user:
    description: "Baseline access, no analytics."
    scopes:
      - "chat:_:read"
`
	if err := os.WriteFile(rolesFile, []byte(rolesYAML), 0o600); err != nil {
		t.Fatalf("write roles file: %v", err)
	}

	idp, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []harness.FakeOIDCUser{
			{Sub: "alice@example.com", Email: "alice@example.com", Claims: map[string]any{"groups": []string{"analytics-team"}}},
			{Sub: "bob@example.com", Email: "bob@example.com", Claims: map[string]any{"groups": []string{"general"}}},
		},
	})
	if err != nil {
		t.Fatalf("NewMockOIDCProvider: %v", err)
	}
	t.Cleanup(func() { _ = idp.Close() })
	idps := map[string]*harness.MockOIDCProvider{"primary": idp}

	auth := &harness.GatewayAuth{
		FakeIDPs:             []harness.FakeIDPSpec{{Name: "primary", ClientID: "c", ClientSecret: "s"}},
		Catalog:              []harness.CatalogEntry{{Name: "primary", IDPRef: "primary"}},
		ExternalAuthProvider: "primary",
		SessionSecretKey:     "test-secret-key",
		AuthorizationService: map[string]any{
			"type":                           "default_rbac",
			"user_to_role_provider":          "idp_claims",
			"role_to_scope_definitions_path": rolesFile,
			"idp_claims_config": map[string]any{
				"oidc_provider": "primary",
				"claim_key":     "groups",
				"mappings": map[string]any{
					"analytics-team": []any{"analytics_reader"},
					"general":        []any{"base_user"},
				},
			},
		},
	}
	handle, err := harnessboot.BootGateway(t, &harness.Scenario{GatewayAuth: auth}, harness.SynthesizeCatalog(auth, idps))
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	ctx := t.Context()

	const queried = "analytics:_:read,chat:_:read"

	aliceCaps := userCapabilities(ctx, t, harness.AuthedClient(ctx, t, handle.BaseURL, idp, "alice@example.com"), handle.BaseURL, queried)
	if !aliceCaps["analytics:_:read"] {
		t.Errorf("alice (analytics-team) should resolve analytics:_:read; caps=%v", aliceCaps)
	}

	bobCaps := userCapabilities(ctx, t, harness.AuthedClient(ctx, t, handle.BaseURL, idp, "bob@example.com"), handle.BaseURL, queried)
	if bobCaps["analytics:_:read"] {
		t.Errorf("bob (general) must NOT resolve analytics:_:read; caps=%v", bobCaps)
	}
	if !bobCaps["chat:_:read"] {
		t.Errorf("bob's baseline role should resolve chat:_:read (a non-empty scope set, so the analytics denial is real); caps=%v", bobCaps)
	}
}

func userCapabilities(ctx context.Context, t *testing.T, client *http.Client, baseURL, scopesCSV string) map[string]bool {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/user/capabilities?scopes="+scopesCSV, nil)
	if err != nil {
		t.Fatalf("build capabilities request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET capabilities: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("capabilities status %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	return out.Capabilities
}
