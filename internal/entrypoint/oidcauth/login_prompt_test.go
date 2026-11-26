// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func authorizeQuery(t *testing.T, q url.Values) url.Values {
	t.Helper()
	cfg := oauth2.Config{
		ClientID: "client-id",
		Endpoint: oauth2.Endpoint{AuthURL: "https://idp.example/authorize"},
	}
	u, err := url.Parse(cfg.AuthCodeURL("state-123", loginPromptOptions(q)...))
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	return u.Query()
}

func TestLoginPromptOptions_SelectAccountReachesTheIdP(t *testing.T) {
	got := authorizeQuery(t, url.Values{"prompt": []string{promptSelectAccount}})

	if got.Get("prompt") != promptSelectAccount {
		t.Errorf("prompt = %q, want %q", got.Get("prompt"), promptSelectAccount)
	}
	if got.Get("state") != "state-123" {
		t.Errorf("state = %q, want state-123 (options must not disturb the rest of the URL)", got.Get("state"))
	}
}

func TestLoginPromptOptions_RejectsEverythingElse(t *testing.T) {
	for _, prompt := range []string{"", "consent", "login", "none", "SELECT_ACCOUNT", "select_account extra", " select_account"} {
		t.Run("prompt="+prompt, func(t *testing.T) {
			q := url.Values{}
			if prompt != "" {
				q.Set("prompt", prompt)
			}

			if got := authorizeQuery(t, q).Get("prompt"); got != "" {
				t.Errorf("prompt = %q, want it dropped", got)
			}
		})
	}
}

func TestLoginPromptOptions_RepeatedPrompt(t *testing.T) {
	t.Run("first value rejected", func(t *testing.T) {
		q := url.Values{"prompt": {"login", promptSelectAccount}}

		if got := authorizeQuery(t, q).Get("prompt"); got != "" {
			t.Errorf("prompt = %q, want it dropped", got)
		}
	})

	t.Run("first value accepted", func(t *testing.T) {
		q := url.Values{"prompt": {promptSelectAccount, "login"}}

		got := authorizeQuery(t, q)["prompt"]
		if len(got) != 1 || got[0] != promptSelectAccount {
			t.Errorf("prompt = %q, want exactly one %q", got, promptSelectAccount)
		}
	})
}

func newProviderWithNativeOIDC(t *testing.T) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 srvIssuer(r),
			"authorization_endpoint": srvIssuer(r) + "/authorize",
			"token_endpoint":         srvIssuer(r) + "/token",
			"userinfo_endpoint":      srvIssuer(r) + "/userinfo",
			"jwks_uri":               srvIssuer(r) + "/jwks",
		})
	}))
	t.Cleanup(srv.Close)

	client, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:   srv.URL,
		ClientID: "client-id",
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}

	p := newProviderWithFakeOIDC(t, &fakeOIDCClient{})
	p.oidcClient = client
	return p
}

func loginRedirectQuery(t *testing.T, target string) url.Values {
	t.Helper()
	p := newProviderWithNativeOIDC(t)

	w := httptest.NewRecorder()
	p.handleLogin(w, httptest.NewRequest(http.MethodGet, target, nil))

	if w.Code != http.StatusFound {
		t.Fatalf("Code = %d, want 302; body = %s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	return loc.Query()
}

func TestHandleLogin_SelectAccountReachesTheRedirect(t *testing.T) {
	got := loginRedirectQuery(t, "/api/v1/auth/login?prompt="+promptSelectAccount)

	if got.Get("prompt") != promptSelectAccount {
		t.Errorf("prompt = %q, want %q", got.Get("prompt"), promptSelectAccount)
	}
}

func TestHandleLogin_NoPromptLeavesTheRedirectUntouched(t *testing.T) {
	got := loginRedirectQuery(t, "/api/v1/auth/login")

	if _, ok := got["prompt"]; ok {
		t.Errorf("prompt = %q, want no prompt parameter at all", got.Get("prompt"))
	}
}
