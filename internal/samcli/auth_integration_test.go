// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

func TestSamAuthLogin_EndToEnd(t *testing.T) {
	rerouteHome(t)

	const samToken = "sam-end-to-end"
	platform := newFakeOAuthPlatform(t, samToken)

	restoreTransport := oauth.SetDefaultTransport(platform.Client().Transport)
	defer restoreTransport()

	stopBrowser := installInProcessBrowser(t, platform.Client())
	defer stopBrowser()

	cmd := newAuthLoginCmd()
	cmd.SetArgs([]string{
		"--target", "dev",
		"--url", platform.URL,
		"--timeout", "10s",
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	store, err := tokencache.NewDefault()
	if err != nil {
		t.Fatalf("NewDefault: %v", err)
	}
	cached, err := store.Load("dev")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cached == nil || cached.SAMAccessToken != samToken {
		t.Fatalf("cache = %+v; want SAMAccessToken=%q", cached, samToken)
	}

	r := tokencache.NewResolver(store, oauth.NewClient())
	tok, err := r.ResolveSAMToken(context.Background(), target.Target{
		Name: "dev", URL: platform.URL,
		Auth: target.AuthSpec{Type: target.AuthOAuth},
	})
	if err != nil {
		t.Fatalf("ResolveSAMToken: %v", err)
	}
	if tok != samToken {
		t.Fatalf("resolved token = %q; want %q", tok, samToken)
	}
}

func TestSamAuthLogout_EndToEnd(t *testing.T) {
	rerouteHome(t)

	logoutHit := 0
	platform := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/logout" {
			logoutHit++
			http.Error(w, "ok", http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer platform.Close()
	restoreTransport := oauth.SetDefaultTransport(platform.Client().Transport)
	defer restoreTransport()

	store, err := tokencache.NewDefault()
	if err != nil {
		t.Fatalf("NewDefault: %v", err)
	}
	if err := store.Save("dev", tokencache.Cached{
		PlatformURL:    platform.URL,
		SAMAccessToken: "tok",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newAuthLogoutCmd()
	cmd.SetArgs([]string{"--target", "dev", "--url", platform.URL})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if logoutHit != 1 {
		t.Errorf("/api/v1/auth/logout hit count = %d; want 1", logoutHit)
	}
	if got, _ := store.Load("dev"); got != nil {
		t.Errorf("cache survived logout: %+v", got)
	}
}

func rerouteHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("SAM_HOME", "")
}

func newFakeOAuthPlatform(t *testing.T, samToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/cli-config":
			_, _ = fmt.Fprintf(w, `{"issuer":"https://idp.example.com","client_id":"sam-cli","scopes":["openid","email"]}`)
		case "/api/v1/auth/login":
			q := r.URL.Query()
			finalURL := fmt.Sprintf("%s?access_token=ax&sam_access_token=%s&refresh_token=rt&expires_in=3600&state=%s",
				q.Get("redirect_uri"), samToken, q.Get("state"))
			http.Redirect(w, r, finalURL, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func installInProcessBrowser(t *testing.T, browserClient *http.Client) func() {
	t.Helper()
	restoreStderr := oauth.SetStderr(&bytes.Buffer{})

	client := *browserClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	oauth.OverrideBrowser(func(u string) error {
		go func() {
			req, err := http.NewRequest(http.MethodGet, u, nil)
			if err != nil {
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer func() { _ = resp.Body.Close() }()
			loc := resp.Header.Get("Location")
			if loc == "" {
				return
			}
			resp2, err := client.Get(loc)
			if err == nil {
				_ = resp2.Body.Close()
			}
		}()
		return nil
	})
	return func() {
		oauth.OverrideBrowser(nil)
		restoreStderr()
	}
}

func TestAuthFlags_ResolveTarget_FromManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(`
kind: manifest
name: sample
target:
  name: production
  url: https://platform.prod.example.com
  auth:
    type: oauth
resources: {}
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	f := &authFlags{manifestPath: manifestPath}
	tg, err := f.resolveTarget()
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if tg.URL != "https://platform.prod.example.com" {
		t.Errorf("URL = %q", tg.URL)
	}
	if tg.Name != "production" {
		t.Errorf("Name = %q", tg.Name)
	}
}

func TestAuthFlags_ResolveTarget_RequiresInput(t *testing.T) {
	if _, err := (&authFlags{}).resolveTarget(); err == nil {
		t.Fatal("expected error when neither --manifest nor --url is set")
	}
}

func TestAuthFlags_ResolveTarget_TargetOverridesManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(`
kind: manifest
name: sample
target:
  name: dev
  url: https://platform.dev.example.com
resources: {}
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	f := &authFlags{manifestPath: manifestPath, target: "override"}
	tg, err := f.resolveTarget()
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if tg.Name != "override" {
		t.Errorf("Name = %q; want override", tg.Name)
	}
	if !strings.HasPrefix(tg.URL, "https://") {
		t.Errorf("URL = %q; want manifest's URL preserved", tg.URL)
	}
}
