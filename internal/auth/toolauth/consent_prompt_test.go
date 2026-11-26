// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"log/slog"
	"maps"
	"net/url"
	"testing"
)

func TestBuildAuthorizationURL_WithoutConsentPrompt(t *testing.T) {
	scheme := &OAuthScheme{AuthorizationURL: "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize"}
	got, err := BuildAuthorizationURL(scheme, "cid", "st", "v", "http://localhost/cb", WithoutConsentPrompt())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Query().Has("prompt") {
		t.Errorf("prompt should be absent, got %q", u.Query().Get("prompt"))
	}
	if u.Query().Get("access_type") != "offline" {
		t.Errorf("access_type = %q, want offline", u.Query().Get("access_type"))
	}
}

func TestMergeAuthConfigs_DisableConsentPrompt(t *testing.T) {
	cases := []struct {
		name     string
		tool     bool
		deployer bool
		want     bool
	}{
		{"both false → false", false, false, false},
		{"tool true → true", true, false, true},
		{"deployer true → true", false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			merged := MergeAuthConfigs(
				&AuthConfig{Type: "oauth2", DisableConsentPrompt: c.tool},
				&AuthConfig{Type: "oauth2", DisableConsentPrompt: c.deployer},
			)
			if merged.DisableConsentPrompt != c.want {
				t.Errorf("DisableConsentPrompt: got %v, want %v", merged.DisableConsentPrompt, c.want)
			}
		})
	}
}

func TestBuildAuthSignal_ConsentPrompt(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")
	cases := []struct {
		name       string
		scheme     map[string]any
		wantPrompt string
	}{
		{"default sends prompt=consent", map[string]any{}, "consent"},
		{"disable_consent_prompt omits prompt", map[string]any{"disable_consent_prompt": true}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scheme := map[string]any{
				"authorization_url": "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize",
				"token_url":         "https://login.microsoftonline.com/tenant/oauth2/v2.0/token",
				"scopes":            []any{"https://analysis.windows.net/powerbi/api/Dataset.Read.All", "offline_access"},
			}
			maps.Copy(scheme, c.scheme)
			cfg := ParseAuthConfig(map[string]any{
				"type":       "oauth2",
				"scheme":     scheme,
				"credential": map[string]any{"client_id": "cid", "client_secret": "secret"},
			}, "powerbi")
			h := NewAuthHandler(cfg, slog.Default())

			signal, err := h.BuildAuthSignal(context.Background(), testToolCtx(newMockCredentialService()))
			if err != nil {
				t.Fatalf("BuildAuthSignal: %v", err)
			}
			u, err := url.Parse(signal.AuthURI)
			if err != nil {
				t.Fatalf("parse AuthURI: %v", err)
			}
			if got := u.Query().Get("prompt"); got != c.wantPrompt {
				t.Errorf("prompt = %q, want %q", got, c.wantPrompt)
			}
		})
	}
}
