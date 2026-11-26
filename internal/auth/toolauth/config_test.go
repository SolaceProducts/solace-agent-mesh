// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"strings"
	"testing"
)

func TestDefaultOAuthCredentialKey(t *testing.T) {
	base := func() *AuthConfig {
		return &AuthConfig{
			Type: "oauth2",
			Scheme: &OAuthScheme{
				AuthorizationURL: "https://idp.example.com/auth",
				TokenURL:         "https://idp.example.com/token",
				Audience:         "api://app",
				Scopes:           []string{"openid", "profile", "email"},
			},
			Credential: &AuthCredential{ClientID: "client-a"},
		}
	}

	t.Run("nil config yields empty key", func(t *testing.T) {
		if got := DefaultOAuthCredentialKey(nil); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("deterministic and oauth2-prefixed", func(t *testing.T) {
		k := DefaultOAuthCredentialKey(base())
		if k != DefaultOAuthCredentialKey(base()) {
			t.Error("not deterministic for equal input")
		}
		if !strings.HasPrefix(k, "oauth2_") {
			t.Errorf("key %q missing oauth2_ prefix", k)
		}
	})

	t.Run("scope order does not affect the key", func(t *testing.T) {
		reordered := base()
		reordered.Scheme.Scopes = []string{"email", "openid", "profile"}
		if DefaultOAuthCredentialKey(base()) != DefaultOAuthCredentialKey(reordered) {
			t.Error("scope reordering changed the key; the sort guarantee is broken")
		}
	})

	t.Run("length-prefixing prevents scope run-together collisions", func(t *testing.T) {
		ab := base()
		ab.Scheme.Scopes = []string{"a", "bc"}
		abc := base()
		abc.Scheme.Scopes = []string{"ab", "c"}
		if DefaultOAuthCredentialKey(ab) == DefaultOAuthCredentialKey(abc) {
			t.Error(`["a","bc"] and ["ab","c"] collided; fields are not length-prefixed`)
		}
	})

	t.Run("identity differences yield distinct keys", func(t *testing.T) {
		k := DefaultOAuthCredentialKey(base())
		cases := map[string]func(*AuthConfig){
			"token_url": func(c *AuthConfig) { c.Scheme.TokenURL = "https://other/token" },
			"audience":  func(c *AuthConfig) { c.Scheme.Audience = "api://other" },
			"resource":  func(c *AuthConfig) { c.Scheme.Resource = "https://mcp.other/mcp" },
			"scopes":    func(c *AuthConfig) { c.Scheme.Scopes = []string{"openid"} },
			"client_id": func(c *AuthConfig) { c.Credential.ClientID = "client-b" },
		}
		for name, mutate := range cases {
			c := base()
			mutate(c)
			if DefaultOAuthCredentialKey(c) == k {
				t.Errorf("differing %s did not change the key", name)
			}
		}
	})

	t.Run("empty type still produces a stable oauth2-prefixed key", func(t *testing.T) {
		k := DefaultOAuthCredentialKey(&AuthConfig{})
		if !strings.HasPrefix(k, "oauth2_") {
			t.Errorf("key %q missing oauth2_ fallback prefix", k)
		}
	})
}

func TestMergeAuthConfigs_BothNil(t *testing.T) {
	got := MergeAuthConfigs(nil, nil)
	if got != nil {
		t.Fatal("expected nil when both inputs are nil")
	}
}

func TestMergeAuthConfigs_ToolDeclaredOnly(t *testing.T) {
	tool := &AuthConfig{
		Type:          "oauth2",
		CredentialKey: "tool_key",
		Scheme: &OAuthScheme{
			AuthorizationURL: "https://tool.example.com/authorize",
			TokenURL:         "https://tool.example.com/token",
			Scopes:           []string{"read"},
		},
	}
	got := MergeAuthConfigs(tool, nil)
	if got != tool {
		t.Fatal("expected tool config returned when deployer is nil")
	}
}

func TestMergeAuthConfigs_DeployerDeclaredOnly(t *testing.T) {
	deployer := &AuthConfig{
		Type:          "oauth2",
		CredentialKey: "deployer_key",
		Credential: &AuthCredential{
			ClientID:     "deploy-id",
			ClientSecret: "deploy-secret",
		},
	}
	got := MergeAuthConfigs(nil, deployer)
	if got != deployer {
		t.Fatal("expected deployer config returned when tool is nil")
	}
}

func TestMergeAuthConfigs_DeployerWinsTypeAndCredentialKey(t *testing.T) {
	tool := &AuthConfig{
		Type:          "oauth2",
		CredentialKey: "tool_key",
	}
	deployer := &AuthConfig{
		Type:          "bearer",
		CredentialKey: "deployer_key",
	}
	got := MergeAuthConfigs(tool, deployer)
	if got.Type != "bearer" {
		t.Errorf("Type: got %q, want %q", got.Type, "bearer")
	}
	if got.CredentialKey != "deployer_key" {
		t.Errorf("CredentialKey: got %q, want %q", got.CredentialKey, "deployer_key")
	}
}

func TestMergeAuthConfigs_ToolFallbackForEmptyDeployerFields(t *testing.T) {
	tool := &AuthConfig{
		Type:          "oauth2",
		CredentialKey: "tool_key",
	}
	deployer := &AuthConfig{}
	got := MergeAuthConfigs(tool, deployer)
	if got.Type != "oauth2" {
		t.Errorf("Type: got %q, want %q (tool fallback)", got.Type, "oauth2")
	}
	if got.CredentialKey != "tool_key" {
		t.Errorf("CredentialKey: got %q, want %q (tool fallback)", got.CredentialKey, "tool_key")
	}
}

func TestMergeAuthConfigs_DeployerCredentialWins(t *testing.T) {
	tool := &AuthConfig{
		Type: "oauth2",
		Credential: &AuthCredential{
			ClientID:     "tool-id",
			ClientSecret: "tool-secret",
		},
	}
	deployer := &AuthConfig{
		Type: "oauth2",
		Credential: &AuthCredential{
			ClientID:     "deploy-id",
			ClientSecret: "deploy-secret",
		},
	}
	got := MergeAuthConfigs(tool, deployer)
	if got.Credential.ClientID != "deploy-id" {
		t.Errorf("ClientID: got %q, want %q", got.Credential.ClientID, "deploy-id")
	}
	if got.Credential.ClientSecret != "deploy-secret" {
		t.Errorf("ClientSecret: got %q, want %q", got.Credential.ClientSecret, "deploy-secret")
	}
}

func TestMergeAuthConfigs_ToolCredentialFallback(t *testing.T) {
	tool := &AuthConfig{
		Type: "oauth2",
		Credential: &AuthCredential{
			ClientID: "tool-id",
		},
	}
	deployer := &AuthConfig{
		Type: "oauth2",
	}
	got := MergeAuthConfigs(tool, deployer)
	if got.Credential == nil || got.Credential.ClientID != "tool-id" {
		t.Error("expected tool credential when deployer has none")
	}
}

func TestMergeAuthConfigs_SchemeFieldByFieldMerge(t *testing.T) {
	tool := &AuthConfig{
		Type: "oauth2",
		Scheme: &OAuthScheme{
			AuthorizationURL:        "https://tool.example.com/authorize",
			TokenURL:                "https://tool.example.com/token",
			Scopes:                  []string{"read", "write"},
			TokenEndpointAuthMethod: "client_secret_post",
		},
	}
	deployer := &AuthConfig{
		Type: "oauth2",
		Scheme: &OAuthScheme{
			TokenURL: "https://deployer.example.com/token",
			Scopes:   []string{"admin"},
		},
	}
	got := MergeAuthConfigs(tool, deployer)

	if got.Scheme.AuthorizationURL != "https://tool.example.com/authorize" {
		t.Errorf("AuthorizationURL: got %q, want tool's URL (deployer empty)", got.Scheme.AuthorizationURL)
	}
	if got.Scheme.TokenURL != "https://deployer.example.com/token" {
		t.Errorf("TokenURL: got %q, want deployer's URL", got.Scheme.TokenURL)
	}
	if len(got.Scheme.Scopes) != 1 || got.Scheme.Scopes[0] != "admin" {
		t.Errorf("Scopes: got %v, want [admin]", got.Scheme.Scopes)
	}
	if got.Scheme.TokenEndpointAuthMethod != "client_secret_post" {
		t.Errorf("TokenEndpointAuthMethod: got %q, want tool's method (deployer empty)", got.Scheme.TokenEndpointAuthMethod)
	}
}

func TestMergeAuthConfigs_DeployerSchemeOnlyNoToolScheme(t *testing.T) {
	tool := &AuthConfig{Type: "oauth2"}
	deployer := &AuthConfig{
		Type: "oauth2",
		Scheme: &OAuthScheme{
			AuthorizationURL: "https://deployer.example.com/authorize",
			TokenURL:         "https://deployer.example.com/token",
		},
	}
	got := MergeAuthConfigs(tool, deployer)
	if got.Scheme != deployer.Scheme {
		t.Error("expected deployer scheme used directly when tool has no scheme")
	}
}

func TestMergeAuthConfigs_ToolSchemeOnlyNoDeployerScheme(t *testing.T) {
	tool := &AuthConfig{
		Type: "oauth2",
		Scheme: &OAuthScheme{
			AuthorizationURL: "https://tool.example.com/authorize",
			TokenURL:         "https://tool.example.com/token",
		},
	}
	deployer := &AuthConfig{Type: "oauth2"}
	got := MergeAuthConfigs(tool, deployer)
	if got.Scheme != tool.Scheme {
		t.Error("expected tool scheme used directly when deployer has no scheme")
	}
}

func TestParseAuthConfig(t *testing.T) {
	t.Run("empty type returns nil", func(t *testing.T) {
		got := ParseAuthConfig(map[string]any{"credential_key": "key"}, "default")
		if got != nil {
			t.Errorf("expected nil for empty type, got %+v", got)
		}
	})

	t.Run("oauth2 TLS knobs", func(t *testing.T) {
		raw := map[string]any{
			"type":                 "oauth2",
			"ca_cert_path":         "/etc/pki/idp-ca.pem",
			"insecure_skip_verify": true,
			"scheme": map[string]any{
				"authorization_url": "https://idp.example.com/authorize",
				"token_url":         "https://idp.example.com/token",
			},
		}
		got := ParseAuthConfig(raw, "my_tool")
		if got == nil {
			t.Fatal("expected non-nil config")
		}
		if got.CACertPath != "/etc/pki/idp-ca.pem" {
			t.Errorf("CACertPath = %q, want /etc/pki/idp-ca.pem", got.CACertPath)
		}
		if !got.InsecureSkipVerify {
			t.Error("InsecureSkipVerify = false, want true")
		}
	})

	t.Run("basic with credential", func(t *testing.T) {
		raw := map[string]any{
			"type": "basic",
			"credential": map[string]any{
				"username": "admin",
				"password": "secret",
			},
		}
		got := ParseAuthConfig(raw, "my_tool")
		if got == nil {
			t.Fatal("expected non-nil config")
		}
		if got.Type != "basic" {
			t.Errorf("Type = %q", got.Type)
		}
		if got.CredentialKey != "my_tool_basic" {
			t.Errorf("CredentialKey = %q, want %q", got.CredentialKey, "my_tool_basic")
		}
		if got.Credential == nil || got.Credential.Username != "admin" {
			t.Errorf("Credential.Username = %v", got.Credential)
		}
		if got.Credential.Password != "secret" {
			t.Errorf("Credential.Password = %q", got.Credential.Password)
		}
	})

	t.Run("bearer with token and custom key", func(t *testing.T) {
		raw := map[string]any{
			"type":           "bearer",
			"credential_key": "custom_key",
			"credential": map[string]any{
				"token": "my-token",
			},
		}
		got := ParseAuthConfig(raw, "default")
		if got == nil {
			t.Fatal("expected non-nil config")
		}
		if got.CredentialKey != "custom_key" {
			t.Errorf("CredentialKey = %q", got.CredentialKey)
		}
		if got.Credential == nil || got.Credential.Token != "my-token" {
			t.Errorf("Credential.Token = %v", got.Credential)
		}
	})

	t.Run("oauth2 with full scheme", func(t *testing.T) {
		raw := map[string]any{
			"type": "oauth2",
			"scheme": map[string]any{
				"authorization_url":          "https://auth.example.com/authorize",
				"token_url":                  "https://auth.example.com/token",
				"scopes":                     []any{"read", "write"},
				"token_endpoint_auth_method": "client_secret_basic",
			},
			"credential": map[string]any{
				"client_id":     "cid",
				"client_secret": "csecret",
			},
		}
		got := ParseAuthConfig(raw, "my_tool")
		if got == nil {
			t.Fatal("expected non-nil config")
		}
		if got.Scheme == nil {
			t.Fatal("expected non-nil Scheme")
		}
		if got.Scheme.AuthorizationURL != "https://auth.example.com/authorize" {
			t.Errorf("AuthorizationURL = %q", got.Scheme.AuthorizationURL)
		}
		if got.Scheme.TokenURL != "https://auth.example.com/token" {
			t.Errorf("TokenURL = %q", got.Scheme.TokenURL)
		}
		if len(got.Scheme.Scopes) != 2 || got.Scheme.Scopes[0] != "read" {
			t.Errorf("Scopes = %v", got.Scheme.Scopes)
		}
		if got.Scheme.TokenEndpointAuthMethod != "client_secret_basic" {
			t.Errorf("TokenEndpointAuthMethod = %q", got.Scheme.TokenEndpointAuthMethod)
		}
	})

	t.Run("oauth2 scheme leaves token_endpoint_auth_method empty when unset", func(t *testing.T) {
		raw := map[string]any{
			"type": "oauth2",
			"scheme": map[string]any{
				"authorization_url": "https://auth.example.com/authorize",
				"token_url":         "https://auth.example.com/token",
			},
		}
		got := ParseAuthConfig(raw, "my_tool")
		if got.Scheme.TokenEndpointAuthMethod != "" {
			t.Errorf("TokenEndpointAuthMethod = %q, want \"\" (no parse-time default)", got.Scheme.TokenEndpointAuthMethod)
		}
	})

	t.Run("non-oauth2 ignores scheme block", func(t *testing.T) {
		raw := map[string]any{
			"type": "basic",
			"scheme": map[string]any{
				"authorization_url": "https://auth.example.com/authorize",
			},
		}
		got := ParseAuthConfig(raw, "my_tool")
		if got.Scheme != nil {
			t.Errorf("Scheme should be nil for basic type, got %+v", got.Scheme)
		}
	})

	t.Run("no credential block", func(t *testing.T) {
		raw := map[string]any{"type": "oauth2"}
		got := ParseAuthConfig(raw, "my_tool")
		if got.Credential != nil {
			t.Errorf("expected nil Credential, got %+v", got.Credential)
		}
	})
}

func TestParseStringSlice(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"nil", nil, nil},
		{"non-list string", "not a list", nil},
		{"non-list int", 42, nil},
		{"strings", []any{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"mixed types skips non-strings", []any{"a", 42, "b"}, []string{"a", "b"}},
		{"empty list", []any{}, nil},
		{"all non-strings", []any{1, 2, 3}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseStringSlice(tt.v)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseStringSlice() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseAuthConfig_ResourceURLZeroValue(t *testing.T) {
	raw := map[string]any{
		"type": "oauth2",
		"credential": map[string]any{
			"client_id":     "cid",
			"client_secret": "csecret",
		},
	}
	got := ParseAuthConfig(raw, "my_tool")
	if got == nil {
		t.Fatal("expected non-nil config")
	}
	if got.ResourceURL != "" {
		t.Errorf("ResourceURL: got %q, want \"\" (zero value, never parsed from YAML)", got.ResourceURL)
	}
}

func TestParseAuthConfig_DisablePKCE(t *testing.T) {
	t.Run("true flips AuthConfig.DisablePKCE on", func(t *testing.T) {
		raw := map[string]any{
			"type": "oauth2",
			"scheme": map[string]any{
				"audience":     "api.atlassian.com",
				"disable_pkce": true,
			},
		}
		got := ParseAuthConfig(raw, "atlassian_rest_request")
		if got == nil {
			t.Fatal("expected non-nil config")
		}
		if !got.DisablePKCE {
			t.Errorf("DisablePKCE: got false, want true")
		}
	})

	t.Run("false / absent leaves DisablePKCE as zero", func(t *testing.T) {
		raw := map[string]any{
			"type": "oauth2",
			"scheme": map[string]any{
				"audience":     "api.example",
				"disable_pkce": false,
			},
		}
		got := ParseAuthConfig(raw, "tool")
		if got == nil {
			t.Fatal("expected non-nil config")
		}
		if got.DisablePKCE {
			t.Errorf("DisablePKCE: got true, want false (deployer set explicit false)")
		}
	})

	t.Run("absent scheme leaves DisablePKCE as zero", func(t *testing.T) {
		raw := map[string]any{"type": "oauth2"}
		got := ParseAuthConfig(raw, "tool")
		if got == nil {
			t.Fatal("expected non-nil config")
		}
		if got.DisablePKCE {
			t.Errorf("DisablePKCE: got true, want false (no scheme block)")
		}
	})
}

func TestMergeAuthConfigs_DisablePKCE(t *testing.T) {
	cases := []struct {
		name     string
		tool     bool
		deployer bool
		want     bool
	}{
		{"both false → false", false, false, false},
		{"tool true → true", true, false, true},
		{"deployer true → true", false, true, true},
		{"both true → true", true, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			merged := MergeAuthConfigs(
				&AuthConfig{Type: "oauth2", DisablePKCE: c.tool},
				&AuthConfig{Type: "oauth2", DisablePKCE: c.deployer},
			)
			if merged.DisablePKCE != c.want {
				t.Errorf("DisablePKCE: got %v, want %v", merged.DisablePKCE, c.want)
			}
		})
	}
}

func TestMergeAuthConfigs_InsecureSkipVerify(t *testing.T) {
	cases := []struct {
		name     string
		tool     bool
		deployer bool
		want     bool
	}{
		{"both false → false", false, false, false},
		{"tool true → true", true, false, true},
		{"deployer true → true", false, true, true},
		{"both true → true", true, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			merged := MergeAuthConfigs(
				&AuthConfig{Type: "oauth2", InsecureSkipVerify: c.tool},
				&AuthConfig{Type: "oauth2", InsecureSkipVerify: c.deployer},
			)
			if merged.InsecureSkipVerify != c.want {
				t.Errorf("InsecureSkipVerify: got %v, want %v", merged.InsecureSkipVerify, c.want)
			}
		})
	}
}

func TestMergeAuthConfigs_DeployerCACertPathWins(t *testing.T) {
	got := MergeAuthConfigs(
		&AuthConfig{Type: "oauth2", CACertPath: "/tool/ca.pem"},
		&AuthConfig{Type: "oauth2", CACertPath: "/deployer/ca.pem"},
	)
	if got.CACertPath != "/deployer/ca.pem" {
		t.Errorf("CACertPath: got %q, want deployer's", got.CACertPath)
	}
}

func TestMergeAuthConfigs_ToolCACertPathFallback(t *testing.T) {
	got := MergeAuthConfigs(
		&AuthConfig{Type: "oauth2", CACertPath: "/tool/ca.pem"},
		&AuthConfig{Type: "oauth2"},
	)
	if got.CACertPath != "/tool/ca.pem" {
		t.Errorf("CACertPath: got %q, want tool's (deployer empty)", got.CACertPath)
	}
}

func TestMergeAuthConfigs_DeployerResourceURLWins(t *testing.T) {
	tool := &AuthConfig{
		Type:        "oauth2",
		ResourceURL: "https://tool.example.com/mcp",
	}
	deployer := &AuthConfig{
		Type:        "oauth2",
		ResourceURL: "https://deployer.example.com/mcp",
	}
	got := MergeAuthConfigs(tool, deployer)
	if got.ResourceURL != "https://deployer.example.com/mcp" {
		t.Errorf("ResourceURL: got %q, want deployer's URL", got.ResourceURL)
	}
}

func TestMergeAuthConfigs_ToolResourceURLFallback(t *testing.T) {
	tool := &AuthConfig{
		Type:        "oauth2",
		ResourceURL: "https://tool.example.com/mcp",
	}
	deployer := &AuthConfig{
		Type: "oauth2",
	}
	got := MergeAuthConfigs(tool, deployer)
	if got.ResourceURL != "https://tool.example.com/mcp" {
		t.Errorf("ResourceURL: got %q, want tool's URL (deployer empty)", got.ResourceURL)
	}
}

func TestParseAuthConfig_DisplayName(t *testing.T) {
	ac := ParseAuthConfig(map[string]any{
		"type":         "oauth2",
		"display_name": "Atlassian",
	}, "key")
	if ac == nil {
		t.Fatal("ParseAuthConfig returned nil")
	}
	if ac.DisplayName != "Atlassian" {
		t.Errorf("DisplayName: got %q, want \"Atlassian\" (YAML key display_name)", ac.DisplayName)
	}
}

func TestParseAuthConfig_DisplayNameTrimmed(t *testing.T) {
	ac := ParseAuthConfig(map[string]any{
		"type":         "oauth2",
		"display_name": "  ",
	}, "key")
	if ac == nil {
		t.Fatal("ParseAuthConfig returned nil")
	}
	if ac.DisplayName != "" {
		t.Errorf("DisplayName: got %q, want empty (whitespace-only trimmed)", ac.DisplayName)
	}
}

func TestMergeAuthConfigs_DeployerDisplayNameWins(t *testing.T) {
	got := MergeAuthConfigs(
		&AuthConfig{Type: "oauth2", DisplayName: "Tool Name"},
		&AuthConfig{Type: "oauth2", DisplayName: "Deployer Name"},
	)
	if got.DisplayName != "Deployer Name" {
		t.Errorf("DisplayName: got %q, want deployer's", got.DisplayName)
	}
}

func TestMergeAuthConfigs_ToolDisplayNameFallback(t *testing.T) {
	got := MergeAuthConfigs(
		&AuthConfig{Type: "oauth2", DisplayName: "Tool Name"},
		&AuthConfig{Type: "oauth2"},
	)
	if got.DisplayName != "Tool Name" {
		t.Errorf("DisplayName: got %q, want tool's (deployer empty)", got.DisplayName)
	}
}

func TestMergeAuthConfigs_FullProductionScenario(t *testing.T) {
	tool := &AuthConfig{
		Type:          "oauth2",
		CredentialKey: "my_tool_oauth2",
		Scheme: &OAuthScheme{
			AuthorizationURL:        "https://tool.example.com/authorize",
			TokenURL:                "https://tool.example.com/token",
			Scopes:                  []string{"read", "write"},
			TokenEndpointAuthMethod: "client_secret_post",
		},
	}
	deployer := &AuthConfig{
		Type:          "oauth2",
		CredentialKey: "my_tool_oauth2",
		Credential: &AuthCredential{
			ClientID:     "prod-client-id",
			ClientSecret: "prod-client-secret",
		},
		Scheme: &OAuthScheme{
			TokenEndpointAuthMethod: "client_secret_basic",
		},
	}

	got := MergeAuthConfigs(tool, deployer)

	if got.Type != "oauth2" {
		t.Errorf("Type: got %q, want %q", got.Type, "oauth2")
	}
	if got.CredentialKey != "my_tool_oauth2" {
		t.Errorf("CredentialKey: got %q, want %q", got.CredentialKey, "my_tool_oauth2")
	}
	if got.Credential.ClientID != "prod-client-id" {
		t.Errorf("ClientID: got %q, want %q", got.Credential.ClientID, "prod-client-id")
	}
	if got.Scheme.AuthorizationURL != "https://tool.example.com/authorize" {
		t.Errorf("AuthorizationURL: got %q, want tool's URL", got.Scheme.AuthorizationURL)
	}
	if got.Scheme.TokenURL != "https://tool.example.com/token" {
		t.Errorf("TokenURL: got %q, want tool's URL", got.Scheme.TokenURL)
	}
	if len(got.Scheme.Scopes) != 2 {
		t.Errorf("Scopes: got %v, want [read, write]", got.Scheme.Scopes)
	}
	if got.Scheme.TokenEndpointAuthMethod != "client_secret_basic" {
		t.Errorf("TokenEndpointAuthMethod: got %q, want %q", got.Scheme.TokenEndpointAuthMethod, "client_secret_basic")
	}
}

func TestParseAuthConfig_Resource(t *testing.T) {
	ac := ParseAuthConfig(map[string]any{
		"type": "oauth2",
		"scheme": map[string]any{
			"authorization_url": "https://idp.example.com/auth",
			"token_url":         "https://idp.example.com/token",
			"resource":          "https://mcp.example.com/mcp",
		},
	}, "key")
	if ac == nil || ac.Scheme == nil {
		t.Fatal("expected parsed oauth2 scheme")
	}
	if ac.Scheme.Resource != "https://mcp.example.com/mcp" {
		t.Errorf("Scheme.Resource = %q, want https://mcp.example.com/mcp", ac.Scheme.Resource)
	}
}

func TestMergeAuthConfigs_ResourceMerge(t *testing.T) {
	t.Run("tool fallback when deployer empty", func(t *testing.T) {
		tool := &AuthConfig{Type: "oauth2", Scheme: &OAuthScheme{Resource: "https://tool.example.com/mcp"}}
		deployer := &AuthConfig{Type: "oauth2", Scheme: &OAuthScheme{TokenURL: "https://d/token"}}
		got := MergeAuthConfigs(tool, deployer)
		if got.Scheme.Resource != "https://tool.example.com/mcp" {
			t.Errorf("Resource = %q, want the tool fallback", got.Scheme.Resource)
		}
	})
	t.Run("deployer override wins", func(t *testing.T) {
		tool := &AuthConfig{Type: "oauth2", Scheme: &OAuthScheme{Resource: "https://tool.example.com/mcp"}}
		deployer := &AuthConfig{Type: "oauth2", Scheme: &OAuthScheme{Resource: "https://deployer.example.com/mcp"}}
		got := MergeAuthConfigs(tool, deployer)
		if got.Scheme.Resource != "https://deployer.example.com/mcp" {
			t.Errorf("Resource = %q, want the deployer override", got.Scheme.Resource)
		}
	})
}
