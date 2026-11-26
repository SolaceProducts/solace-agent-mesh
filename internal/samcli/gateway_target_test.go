// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func TestResolveTargetURL_DesktopFromStateFile(t *testing.T) {
	rerouteHome(t)
	if err := settings.WriteDesktopInstance("http://127.0.0.1:54321/"); err != nil {
		t.Fatalf("WriteDesktopInstance: %v", err)
	}

	for _, name := range []string{"desktop", "Desktop", "DESKTOP"} {
		got, err := resolveTargetURL(name)
		if err != nil {
			t.Fatalf("resolveTargetURL(%q): %v", name, err)
		}
		if got != "http://127.0.0.1:54321" {
			t.Errorf("resolveTargetURL(%q) = %q; want trailing slash trimmed", name, got)
		}
	}
}

func TestResolveTargetURL_DesktopNotRunning(t *testing.T) {
	rerouteHome(t)

	_, err := resolveTargetURL("desktop")
	if err == nil {
		t.Fatal("expected an error when no desktop state file exists")
	}
	if !strings.Contains(err.Error(), "no running SAM desktop app") {
		t.Errorf("error = %q; want it to mention no running desktop", err)
	}
}

func TestResolveGatewayTarget_ExplicitURLWins(t *testing.T) {
	t.Setenv("SAM_WEBUI_URL", "https://env.example.com")

	got, err := resolveGatewayTargetURL(context.Background(),
		gatewayTargetRef{URL: "https://flag.example.com"}, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "https://flag.example.com" {
		t.Errorf("got %q; want flag URL to win over env", got)
	}
}

func TestResolveGatewayTarget_TargetFromCache(t *testing.T) {
	rerouteHome(t)
	store, err := tokencache.NewDefault()
	if err != nil {
		t.Fatalf("NewDefault: %v", err)
	}
	if saveErr := store.Save("dev", tokencache.Cached{
		PlatformURL:    "https://dev.example.com",
		SAMAccessToken: "tok",
		ExpiresAt:      time.Now().Add(time.Hour),
	}); saveErr != nil {
		t.Fatalf("save: %v", saveErr)
	}

	got, err := resolveGatewayTargetURL(context.Background(),
		gatewayTargetRef{Target: "dev"}, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "https://dev.example.com" {
		t.Errorf("got %q; want cached URL", got)
	}
}

func TestResolveGatewayTarget_TargetMissingErrors(t *testing.T) {
	rerouteHome(t)
	_, err := resolveGatewayTargetURL(context.Background(),
		gatewayTargetRef{Target: "nope"}, "")
	if err == nil {
		t.Fatal("expected error for missing target")
	}
	if !strings.Contains(err.Error(), "no cache entry") {
		t.Errorf("error = %v; want hint to run `sam auth login`", err)
	}
}

func TestResolveGatewayTarget_EnvFallback(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_WEBUI_URL", "platform.example.com")

	got, err := resolveGatewayTargetURL(context.Background(),
		gatewayTargetRef{}, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "https://platform.example.com" {
		t.Errorf("got %q; want normalised env URL", got)
	}
}

func TestResolveGatewayTarget_SingleCacheEntry(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_WEBUI_URL", "")

	store, _ := tokencache.NewDefault()
	if err := store.Save("only", tokencache.Cached{
		PlatformURL:    "https://only.example.com",
		SAMAccessToken: "tok",
		ExpiresAt:      time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := resolveGatewayTargetURL(context.Background(),
		gatewayTargetRef{}, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "https://only.example.com" {
		t.Errorf("got %q; want single-entry URL", got)
	}
}

func TestResolveGatewayTarget_MultipleCacheRequiresTarget(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_WEBUI_URL", "")

	store, _ := tokencache.NewDefault()
	for _, name := range []string{"dev", "prod"} {
		if err := store.Save(name, tokencache.Cached{
			PlatformURL:    "https://" + name + ".example.com",
			SAMAccessToken: "tok",
			ExpiresAt:      time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	_, err := resolveGatewayTargetURL(context.Background(), gatewayTargetRef{}, "")
	if err == nil {
		t.Fatal("expected error when multiple cached logins and no --target")
	}
	if !strings.Contains(err.Error(), "multiple cached targets") {
		t.Errorf("error = %v; want disambiguation message", err)
	}
}

func TestResolveGatewayTarget_DefaultFallback(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_WEBUI_URL", "")

	got, err := resolveGatewayTargetURL(context.Background(),
		gatewayTargetRef{}, defaultTaskSendURL)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != defaultTaskSendURL {
		t.Errorf("got %q; want fallback %q", got, defaultTaskSendURL)
	}
}

func TestResolveGatewayTarget_NoFallbackErrors(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_WEBUI_URL", "")

	_, err := resolveGatewayTargetURL(context.Background(), gatewayTargetRef{}, "")
	if err == nil {
		t.Fatal("expected error when nothing resolves and no fallback")
	}
}

func TestResolveGatewayBearer_PrecedenceExplicit(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_AUTH_TOKEN", "from-env")

	got := resolveGatewayBearer(context.Background(), "https://x", "explicit")
	if got != "explicit" {
		t.Errorf("got %q; want explicit to win", got)
	}
}

func TestResolveGatewayBearer_EnvAuthToken(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_AUTH_TOKEN", "from-auth-env")
	t.Setenv("SAM_PLATFORM_TOKEN", "from-platform-env")

	got := resolveGatewayBearer(context.Background(), "https://x", "")
	if got != "from-auth-env" {
		t.Errorf("got %q; want SAM_AUTH_TOKEN to win over SAM_PLATFORM_TOKEN", got)
	}
}

func TestResolveGatewayBearer_EnvPlatformTokenFallback(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_AUTH_TOKEN", "")
	t.Setenv("SAM_PLATFORM_TOKEN", "from-platform-env")

	got := resolveGatewayBearer(context.Background(), "https://x", "")
	if got != "from-platform-env" {
		t.Errorf("got %q; want SAM_PLATFORM_TOKEN fallback", got)
	}
}

func TestResolveGatewayBearer_EmptyWhenNoCache(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_AUTH_TOKEN", "")
	t.Setenv("SAM_PLATFORM_TOKEN", "")

	got := resolveGatewayBearer(context.Background(), "https://no-cache.example.com", "")
	if got != "" {
		t.Errorf("got %q; want empty (anonymous gateway)", got)
	}
}

func TestSendTask_UsesCachedURLAndToken(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_AUTH_TOKEN", "")
	t.Setenv("SAM_PLATFORM_TOKEN", "")
	t.Setenv("SAM_WEBUI_URL", "")

	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/agentCards" {
			gotAuth.Store(r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	store, err := tokencache.NewDefault()
	if err != nil {
		t.Fatalf("NewDefault: %v", err)
	}
	cacheName := target.CacheNameForURL(srv.URL)
	if saveErr := store.Save(cacheName, tokencache.Cached{
		PlatformURL:    srv.URL,
		SAMAccessToken: "cached-tok",
		ExpiresAt:      time.Now().Add(time.Hour),
	}); saveErr != nil {
		t.Fatalf("save: %v", saveErr)
	}

	err = sendTask("hello", sendTaskOpts{
		Agent:    "orchestrator",
		Quiet:    true,
		Insecure: true,
		Timeout:  5,
	})
	if err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("sendTask: want agent-not-found error, got %v", err)
	}
	if got, _ := gotAuth.Load().(string); got != "Bearer cached-tok" {
		t.Errorf("Authorization header = %q; want %q", got, "Bearer cached-tok")
	}
}

func TestSendTask_RefusesCachedTokenOverPlainHTTP(t *testing.T) {
	rerouteHome(t)
	t.Setenv("SAM_AUTH_TOKEN", "")
	t.Setenv("SAM_PLATFORM_TOKEN", "")
	t.Setenv("SAM_WEBUI_URL", "")

	store, _ := tokencache.NewDefault()
	cacheName := target.CacheNameForURL("http://insecure.example.com")
	if err := store.Save(cacheName, tokencache.Cached{
		PlatformURL:    "http://insecure.example.com",
		SAMAccessToken: "cached-tok",
		ExpiresAt:      time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	err := sendTask("hello", sendTaskOpts{Agent: "orchestrator", Quiet: true})
	if err == nil {
		t.Fatal("expected error refusing Bearer over http://")
	}
	if !strings.Contains(err.Error(), "bearer token over plain http") {
		t.Errorf("error = %v; want bearer-over-http rejection", err)
	}
}

func TestAssertSafeBearerOverHTTP(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		token    string
		insecure bool
		wantErr  bool
	}{
		{"https with token is fine", "https://x", "tok", false, false},
		{"http without token is fine", "http://x", "", false, false},
		{"http with token is rejected", "http://x", "tok", false, true},
		{"http with token allowed when insecure", "http://x", "tok", true, false},
		{"http with empty token is fine even without insecure", "http://x", "", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := assertSafeBearerOverHTTP(tc.url, tc.token, tc.insecure)
			if tc.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func resolveGatewayTargetURL(ctx context.Context, ref gatewayTargetRef, defaultFallback string) (string, error) {
	tg, err := resolveGatewayTarget(ctx, ref, defaultFallback)
	return tg.URL, err
}

func TestResolveManifestTarget(t *testing.T) {
	const (
		desktopURL  = "http://127.0.0.1:54321"
		stagingURL  = "https://staging.example.com"
		manifestURL = "https://manifest.example.com"
		overrideURL = "https://override.example.com"
	)
	bearer := manifest.AuthSpec{Type: string(target.AuthBearerToken), EnvVar: "SAM_STAGING_TOKEN"}

	cases := []struct {
		name        string
		block       manifest.TargetBlock
		urlOverride string
		desktop     bool
		wantURL     string
		wantErr     string
	}{
		{name: "url is used as-is, trailing slash trimmed", block: manifest.TargetBlock{URL: manifestURL + "/"}, wantURL: manifestURL},
		{name: "desktop name resolves to the running desktop", block: manifest.TargetBlock{Name: "desktop"}, desktop: true, wantURL: desktopURL},
		{name: "desktop name is case-insensitive", block: manifest.TargetBlock{Name: "Desktop"}, desktop: true, wantURL: desktopURL},
		{name: "named login resolves to its cached url", block: manifest.TargetBlock{Name: "staging"}, wantURL: stagingURL},
		{name: "url wins over name", block: manifest.TargetBlock{Name: "staging", URL: manifestURL}, wantURL: manifestURL},
		{name: "override wins over url", block: manifest.TargetBlock{URL: manifestURL}, urlOverride: overrideURL, wantURL: overrideURL},
		{name: "override skips name resolution", block: manifest.TargetBlock{Name: "desktop"}, urlOverride: overrideURL, wantURL: overrideURL},
		{name: "auth and name are carried through", block: manifest.TargetBlock{Name: "staging", URL: manifestURL, Auth: bearer}, wantURL: manifestURL},
		{name: "no url and no name errors", block: manifest.TargetBlock{}, wantErr: "target.url is empty"},
		{name: "desktop name without a running desktop errors", block: manifest.TargetBlock{Name: "desktop"}, wantErr: "no running SAM desktop app"},
		{name: "unknown login name errors", block: manifest.TargetBlock{Name: "nope"}, wantErr: "no cache entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rerouteHome(t)
			saveCachedLogin(t, "staging", stagingURL, "tok")
			if tc.desktop {
				if err := settings.WriteDesktopInstance(desktopURL + "/"); err != nil {
					t.Fatalf("WriteDesktopInstance: %v", err)
				}
			}

			tg, err := resolveManifestTarget("manifest.yaml", tc.block, tc.urlOverride)
			assertManifestErr(t, err, tc.wantErr)
			if tc.wantErr != "" {
				assertManifestScopedErr(t, err, "manifest.yaml")
				return
			}
			if tg.URL != tc.wantURL {
				t.Errorf("URL = %q; want %q", tg.URL, tc.wantURL)
			}
			if tg.Name != tc.block.Name {
				t.Errorf("Name = %q; want %q carried through", tg.Name, tc.block.Name)
			}
			wantAuth := target.AuthSpec{Type: target.AuthType(tc.block.Auth.Type), EnvVar: tc.block.Auth.EnvVar}
			if tg.Auth != wantAuth {
				t.Errorf("Auth = %+v; want %+v carried through", tg.Auth, wantAuth)
			}
		})
	}
}

func assertManifestScopedErr(t *testing.T, err error, manifestPath string) {
	t.Helper()
	if want := "--manifest " + `"` + manifestPath + `"`; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q; want it to start with %q", err, want)
	}
	if strings.Contains(err.Error(), "--target") {
		t.Errorf("error = %q; must not mention --target, which was not passed", err)
	}
}

func TestResolveGatewayTarget_ManifestLoadErrorIsManifestScoped(t *testing.T) {
	dir := t.TempDir()
	invalidYAML := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(invalidYAML, []byte("kind: manifest\ntarget:\n  url: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	cases := map[string]string{
		"invalid YAML": invalidYAML,
		"missing file": filepath.Join(dir, "missing.yaml"),
	}
	for name, manifestPath := range cases {
		t.Run(name, func(t *testing.T) {
			rerouteHome(t)
			_, err := resolveGatewayTarget(context.Background(), gatewayTargetRef{Manifest: manifestPath}, "")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			assertManifestScopedErr(t, err, manifestPath)
		})
	}
}

func TestResolveGatewayTarget_ManifestOverrideKeepsAuth(t *testing.T) {
	rerouteHome(t)
	mf := writeTargetManifest(t, "  url: https://manifest.example.com\n  auth:\n    type: bearer_token\n    envVar: SAM_STAGING_TOKEN\n")

	tg, err := resolveGatewayTarget(context.Background(),
		gatewayTargetRef{URL: "https://override.example.com", Manifest: mf}, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tg.URL != "https://override.example.com" {
		t.Errorf("URL = %q; want the --url override", tg.URL)
	}
	want := target.AuthSpec{Type: target.AuthBearerToken, EnvVar: "SAM_STAGING_TOKEN"}
	if tg.Auth != want {
		t.Errorf("Auth = %+v; want the manifest's %+v", tg.Auth, want)
	}
}

func TestResolveGatewayTarget_NoManifestHasNoAuth(t *testing.T) {
	rerouteHome(t)

	tg, err := resolveGatewayTarget(context.Background(),
		gatewayTargetRef{URL: "https://flag.example.com"}, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tg.Auth != (target.AuthSpec{}) {
		t.Errorf("Auth = %+v; want empty without a manifest", tg.Auth)
	}
}

func TestResolveGatewayCredential(t *testing.T) {
	const (
		url        = "https://staging.example.com"
		hostKeyed  = "https://host-keyed.example.com"
		stagingTok = "staging-cache-tok"
		hostTok    = "host-cache-tok"
	)
	bearer := target.AuthSpec{Type: target.AuthBearerToken, EnvVar: "SAM_STAGING_TOKEN"}
	oauthAuth := target.AuthSpec{Type: target.AuthOAuth}

	cases := []struct {
		name         string
		tg           target.Target
		env          map[string]string
		want         string
		wantRefresh  string
		fromManifest bool
		wantErr      string
	}{
		{name: "SAM_AUTH_TOKEN overrides manifest auth", tg: target.Target{URL: url, Auth: bearer},
			env: map[string]string{"SAM_AUTH_TOKEN": "auth-env", "SAM_STAGING_TOKEN": "staging-env"}, want: "auth-env"},
		{name: "SAM_PLATFORM_TOKEN overrides manifest auth", tg: target.Target{URL: url, Auth: bearer},
			env: map[string]string{"SAM_PLATFORM_TOKEN": "platform-env", "SAM_STAGING_TOKEN": "staging-env"}, want: "platform-env"},
		{name: "SAM_AUTH_TOKEN overrides a cached login and is never refreshed", tg: target.Target{Name: "staging", URL: url, Auth: oauthAuth},
			env: map[string]string{"SAM_AUTH_TOKEN": "auth-env"}, want: "auth-env"},
		{name: "bearer_token reads the named env var and is never refreshed", tg: target.Target{URL: url, Auth: bearer},
			env: map[string]string{"SAM_STAGING_TOKEN": "staging-env"}, want: "staging-env", fromManifest: true},
		{name: "bearer_token with the env var unset errors", tg: target.Target{URL: url, Auth: bearer},
			wantErr: "SAM_STAGING_TOKEN"},
		{name: "oauth reads and refreshes the cache entry keyed by target name", tg: target.Target{Name: "staging", URL: url, Auth: oauthAuth},
			want: stagingTok, wantRefresh: "staging", fromManifest: true},
		{name: "oauth without a login errors", tg: target.Target{Name: "nope", URL: url, Auth: oauthAuth},
			wantErr: "nope"},
		{name: "no auth uses and refreshes the login named by the target", tg: target.Target{Name: "staging", URL: url},
			want: stagingTok, wantRefresh: "staging", fromManifest: true},
		{name: "no auth matches the named login across a trailing slash and explicit default port",
			tg:   target.Target{Name: "staging", URL: "https://STAGING.example.com:443/"},
			want: stagingTok, wantRefresh: "staging", fromManifest: true},
		{name: "no auth falls back to the url-host login when the name has none", tg: target.Target{Name: "unknown", URL: hostKeyed},
			want: hostTok, wantRefresh: "host-keyed.example.com"},
		{name: "no auth skips a named login issued for another origin", tg: target.Target{Name: "staging", URL: hostKeyed},
			want: hostTok, wantRefresh: "host-keyed.example.com"},
		{name: "no auth skips a named login issued for another port", tg: target.Target{Name: "staging", URL: url + ":8443"},
			want: ""},
		{name: "no auth skips a named login issued for another origin and is otherwise anonymous",
			tg:   target.Target{Name: "staging", URL: "https://attacker.example.net"},
			want: ""},
		{name: "no auth and no name uses the url-host login", tg: target.Target{URL: hostKeyed},
			want: hostTok, wantRefresh: "host-keyed.example.com"},
		{name: "no auth and nothing cached is anonymous", tg: target.Target{URL: "https://no-cache.example.com"},
			want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rerouteHome(t)
			for _, v := range []string{"SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN", "SAM_STAGING_TOKEN"} {
				t.Setenv(v, tc.env[v])
			}
			saveCachedLogin(t, "staging", url, stagingTok)
			saveCachedLogin(t, target.CacheNameForURL(hostKeyed), hostKeyed, hostTok)

			cred, err := resolveGatewayCredential(context.Background(), tc.tg)
			assertManifestErr(t, err, tc.wantErr)
			if tc.wantErr != "" {
				return
			}
			if cred.Token != tc.want {
				t.Errorf("token = %q; want %q", cred.Token, tc.want)
			}
			switch {
			case tc.wantRefresh == "" && cred.Refresh != nil:
				t.Errorf("Refresh = %q; want none for a token that must be sent verbatim", cred.Refresh.CacheName())
			case tc.wantRefresh != "" && cred.Refresh == nil:
				t.Errorf("Refresh = nil; want the %q login", tc.wantRefresh)
			case tc.wantRefresh != "" && cred.Refresh.CacheName() != tc.wantRefresh:
				t.Errorf("Refresh = %q; want the %q login", cred.Refresh.CacheName(), tc.wantRefresh)
			}
			if cred.FromManifest != tc.fromManifest {
				t.Errorf("FromManifest = %v; want %v", cred.FromManifest, tc.fromManifest)
			}
		})
	}
}
