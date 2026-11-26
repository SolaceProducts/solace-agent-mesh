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

func stdioOAuthEntry(t *testing.T, command string, authEdit func(auth map[string]any)) *ToolsetConfig {
	t.Helper()
	auth := map[string]any{"type": "oauth2"}
	if authEdit != nil {
		authEdit(auth)
	}
	cfg, err := ParseToolsetConfig(map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "stdio", "command": command},
		"auth":              auth,
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	return cfg
}

func TestAuthSlotFollowsWhatTheDefaultCredentialKeyFollows(t *testing.T) {
	http := func(url string, edit oauthEdit) *ToolsetConfig {
		return oauthEntry(t, url, entryFilter{}, edit)
	}
	explicitKey := func(key string) oauthEdit {
		return func(a, _, _ map[string]any) { a["credential_key"] = key }
	}
	cases := []struct {
		name     string
		a, b     *ToolsetConfig
		sameSlot bool
	}{
		{"trailing slash", http("https://h/mcp", nil), http("https://h/mcp/", nil), true},
		{"scheme", http("http://h/mcp", nil), http("https://h/mcp", nil), true},
		{"hyphen against slash", http("https://h/a-b", nil), http("https://h/a/b", nil), true},
		{"explicit key on spellings of one address", http("https://h/mcp", explicitKey("k")), http("https://h/mcp/", explicitKey("k")), true},
		{"stdio commands with one base name and an explicit key",
			stdioOAuthEntry(t, "/a/npx", func(a map[string]any) { a["credential_key"] = "k" }),
			stdioOAuthEntry(t, "/b/npx", func(a map[string]any) { a["credential_key"] = "k" }), true},
		{"explicit key shared across servers", http("https://h/mcp", explicitKey("k")), http("https://other/mcp", explicitKey("k")), false},
		{"explicit key shared across stdio commands",
			stdioOAuthEntry(t, "npx", func(a map[string]any) { a["credential_key"] = "k" }),
			stdioOAuthEntry(t, "uvx", func(a map[string]any) { a["credential_key"] = "k" }), false},
		{"one address, distinct explicit keys", http("https://h/mcp", explicitKey("k1")), http("https://h/mcp", explicitKey("k2")), false},
		{"addresses that differ only in a query credential", http("https://h/mcp?api_key=A", nil), http("https://h/mcp?api_key=B", nil), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slotA, okA := authSlotOf(tc.a)
			slotB, okB := authSlotOf(tc.b)
			if !okA || !okB {
				t.Fatal("an entry has no auth slot")
			}
			if same := slotA == slotB; same != tc.sameSlot {
				t.Errorf("same slot = %v, want %v (default keys %q and %q)", same, tc.sameSlot, tc.a.AuthConfig.CredentialKey, tc.b.AuthConfig.CredentialKey)
			}
			if sameKey := tc.a.SharedStateKey() == tc.b.SharedStateKey(); sameKey != tc.sameSlot {
				t.Errorf("same SharedStateKey = %v, want %v: the grouping must follow the slot", sameKey, tc.sameSlot)
			}
		})
	}
}

func TestOAuthEntriesOnSpellingsOfOneAddressAreRefused(t *testing.T) {
	for _, pair := range [][2]string{
		{"https://h/mcp", "https://h/mcp/"},
		{"http://h/mcp", "https://h/mcp"},
		{"https://h/a-b", "https://h/a/b"},
	} {
		t.Run(pair[0]+" and "+pair[1], func(t *testing.T) {
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			first := oauthEntry(t, pair[0], entryFilter{prefix: "a_"}, nil)
			second := oauthEntry(t, pair[1], entryFilter{prefix: "b_"}, nil)
			if first.AuthConfig.CredentialKey != second.AuthConfig.CredentialKey {
				t.Fatal("the entries default to different credential keys, so the case does not put them behind one key")
			}
			if err := mgr.AddClient(first); err != nil {
				t.Fatalf("first AddClient: %v", err)
			}
			err := mgr.AddClient(second)
			if !errors.Is(err, ErrConnectionConflict) {
				t.Fatalf("AddClient = %v, want ErrConnectionConflict", err)
			}
			for _, want := range []string{"auth.resource_url", "credential_key"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should mention %q", err, want)
				}
			}
			mgr.mu.RLock()
			handlers := len(mgr.authHandlers)
			mgr.mu.RUnlock()
			if handlers != 1 {
				t.Errorf("manager holds %d handlers, want one behind the one credential key", handlers)
			}
		})
	}
}
