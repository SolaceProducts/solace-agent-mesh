// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type oauthEdit func(auth, scheme, cred map[string]any)

func withOAuth(edit oauthEdit) entryOption {
	return func(raw, _ map[string]any) {
		scheme := map[string]any{
			"authorization_url": "https://idp.example.com/authorize",
			"token_url":         "https://idp.example.com/token",
			"scopes":            []any{"read"},
		}
		cred := map[string]any{"client_id": "client-1", "client_secret": "client-secret-1"}
		auth := map[string]any{"type": "oauth2", "scheme": scheme, "credential": cred}
		if edit != nil {
			edit(auth, scheme, cred)
		}
		raw["auth"] = auth
		withManifest(manifestTool("get_weather", "w"))(raw, nil)
	}
}

func oauthEntry(t *testing.T, url string, f entryFilter, edit oauthEdit, extra ...entryOption) *ToolsetConfig {
	t.Helper()
	return sharedEntryConfig(t, url, f, append([]entryOption{withOAuth(edit)}, extra...)...)
}

func handlerOf(t *testing.T, mgr *Manager, cfg *ToolsetConfig) *AuthHandler {
	t.Helper()
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil || len(tools) == 0 {
		t.Fatalf("DiscoverTools = %v, %v; want the entry's tools", tools, err)
	}
	return tools[0].AuthHandler()
}

func TestOAuthEntriesDifferingOnlyInConnectionShareOneAuthHandler(t *testing.T) {
	changes := map[string]entryOption{
		"header":            withHeader("X-Tenant", "b"),
		"ssl verification":  withSSLVerifyOff(),
		"sse read timeout":  func(_, conn map[string]any) { conn["sse_read_timeout"] = 7 },
		"response byte cap": func(_, conn map[string]any) { conn["max_response_bytes"] = 4096 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			first := oauthEntry(t, manifestServerURL, entryFilter{prefix: "a_"}, nil)
			second := oauthEntry(t, manifestServerURL, entryFilter{prefix: "b_"}, nil, change)
			if first.ClientID == second.ClientID {
				t.Fatal("the entries have one client id, so they would not exercise two clients on one credential key")
			}
			if first.AuthConfig.CredentialKey != second.AuthConfig.CredentialKey {
				t.Fatalf("credential keys differ (%q, %q); the case needs one shared key", first.AuthConfig.CredentialKey, second.AuthConfig.CredentialKey)
			}
			registerEntries(t, mgr, []*ToolsetConfig{first, second})

			firstHandler, secondHandler := handlerOf(t, mgr, first), handlerOf(t, mgr, second)
			if firstHandler == nil || firstHandler != secondHandler {
				t.Errorf("handlers %p and %p; the entries share a credential key, so they must share one handler", firstHandler, secondHandler)
			}
			if _, direct := clientCounts(mgr); direct != 2 {
				t.Errorf("manager holds %d direct clients, want one per distinct connection", direct)
			}
		})
	}
}

func TestOAuthEntriesInOneCredentialSlotWithDifferentAuthAreRefused(t *testing.T) {
	const secret = "client-secret-OTHER-9c1d"
	changes := map[string]struct {
		edit      oauthEdit
		wantField string
	}{
		"scopes":                     {func(_, s, _ map[string]any) { s["scopes"] = []any{"read", "write"} }, "auth.scheme"},
		"audience":                   {func(_, s, _ map[string]any) { s["audience"] = "other" }, "auth.scheme"},
		"resource":                   {func(_, s, _ map[string]any) { s["resource"] = "other" }, "auth.scheme"},
		"token url":                  {func(_, s, _ map[string]any) { s["token_url"] = "https://idp.example.com/other" }, "auth.scheme"},
		"authorization url":          {func(_, s, _ map[string]any) { s["authorization_url"] = "https://idp.example.com/other" }, "auth.scheme"},
		"refresh url":                {func(_, s, _ map[string]any) { s["refresh_url"] = "https://idp.example.com/refresh" }, "auth.scheme"},
		"token endpoint auth method": {func(_, s, _ map[string]any) { s["token_endpoint_auth_method"] = "none" }, "auth.scheme"},
		"disable pkce":               {func(_, s, _ map[string]any) { s["disable_pkce"] = true }, "auth.disable_pkce"},
		"client id":                  {func(_, _, c map[string]any) { c["client_id"] = "client-2" }, "auth.credential"},
		"client secret":              {func(_, _, c map[string]any) { c["client_secret"] = secret }, "auth.credential"},
		"ca cert path":               {func(a, _, _ map[string]any) { a["ca_cert_path"] = "/etc/other-ca.pem" }, "auth.ca_cert_path"},
		"insecure skip verify":       {func(a, _, _ map[string]any) { a["insecure_skip_verify"] = true }, "auth.insecure_skip_verify"},
	}
	for name, tc := range changes {
		t.Run(name, func(t *testing.T) {
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			first := oauthEntry(t, manifestServerURL, entryFilter{prefix: "a_"}, nil)
			second := oauthEntry(t, manifestServerURL, entryFilter{prefix: "b_"}, tc.edit)
			if first.AuthConfig.CredentialKey != second.AuthConfig.CredentialKey {
				t.Fatal("the credential keys differ, so the case does not put both entries in one slot")
			}
			if err := mgr.AddClient(first); err != nil {
				t.Fatalf("first AddClient: %v", err)
			}

			err := mgr.AddClient(second)
			if !errors.Is(err, ErrConnectionConflict) {
				t.Fatalf("AddClient = %v, want ErrConnectionConflict", err)
			}
			for _, want := range []string{tc.wantField, "credential_key"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should mention %q", err, want)
				}
			}
			for _, leaked := range []string{secret, "client-2", "idp.example.com/other"} {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("error %q carries a configured value %q", err, leaked)
				}
			}

			mgr.mu.RLock()
			handlers, directClients := len(mgr.authHandlers), len(mgr.direct)
			mgr.mu.RUnlock()
			if handlers != 1 || directClients != 1 {
				t.Errorf("manager holds %d handlers and %d direct clients after the refusal, want 1 and 1", handlers, directClients)
			}
			if again := oauthEntry(t, manifestServerURL, entryFilter{prefix: "c_"}, nil); mgr.AddClient(again) != nil {
				t.Error("an entry identical to the first no longer joins after another entry was refused")
			}
		})
	}
}

func TestOAuthEntriesThatDoNotShareACredentialSlotAreNotRefused(t *testing.T) {
	scopesRead := func(_, s, _ map[string]any) { s["scopes"] = []any{"read", "write"} }
	cases := []struct {
		name         string
		first        oauthEdit
		second       oauthEdit
		secondURL    string
		wantHandlers int
	}{
		{"sign-in label only", nil, func(a, _, _ map[string]any) { a["display_name"] = "Other" }, manifestServerURL, 1},
		{"distinct credential keys", func(a, _, _ map[string]any) { a["credential_key"] = "key-a" },
			func(a, s, c map[string]any) { scopesRead(a, s, c); a["credential_key"] = "key-b" }, manifestServerURL, 2},
		{"one explicit key on different servers", func(a, _, _ map[string]any) { a["credential_key"] = "shared" },
			func(a, s, c map[string]any) { scopesRead(a, s, c); a["credential_key"] = "shared" }, "http://other.manifest.invalid/mcp", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			first := oauthEntry(t, manifestServerURL, entryFilter{prefix: "a_"}, tc.first)
			second := oauthEntry(t, tc.secondURL, entryFilter{prefix: "b_"}, tc.second)
			registerEntries(t, mgr, []*ToolsetConfig{first, second})

			mgr.mu.RLock()
			handlers := len(mgr.authHandlers)
			mgr.mu.RUnlock()
			if handlers != tc.wantHandlers {
				t.Errorf("manager holds %d handlers, want %d", handlers, tc.wantHandlers)
			}
		})
	}
}

func TestSharedStateKeyGroupsEntriesThatShareAClientOrACredentialSlot(t *testing.T) {
	plainA := sharedEntryConfig(t, "http://h/mcp", entryFilter{prefix: "a_"})
	plainAgain := sharedEntryConfig(t, "http://h/mcp", entryFilter{prefix: "b_"})
	plainOther := sharedEntryConfig(t, "http://h/mcp", entryFilter{}, withHeader("X-K", "v"))
	authA := oauthEntry(t, manifestServerURL, entryFilter{prefix: "a_"}, nil)
	authHeader := oauthEntry(t, manifestServerURL, entryFilter{prefix: "b_"}, nil, withHeader("X-K", "v"))
	authScopes := oauthEntry(t, manifestServerURL, entryFilter{prefix: "c_"}, func(_, s, _ map[string]any) { s["scopes"] = []any{"w"} })
	authOtherKey := oauthEntry(t, manifestServerURL, entryFilter{prefix: "d_"}, func(a, _, _ map[string]any) { a["credential_key"] = "k" })

	same := [][2]*ToolsetConfig{{plainA, plainAgain}, {authA, authHeader}, {authA, authScopes}}
	for _, pair := range same {
		if pair[0].SharedStateKey() != pair[1].SharedStateKey() {
			t.Errorf("entries %q and %q share a client or slot but have different keys", pair[0].ClientID, pair[1].ClientID)
		}
	}
	different := [][2]*ToolsetConfig{{plainA, plainOther}, {authA, authOtherKey}, {plainA, authA}}
	for _, pair := range different {
		if pair[0].SharedStateKey() == pair[1].SharedStateKey() {
			t.Errorf("entries %q and %q share nothing but have one key", pair[0].ClientID, pair[1].ClientID)
		}
	}
}
