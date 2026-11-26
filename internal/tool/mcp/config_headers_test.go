// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import "testing"

func TestParseToolsetConfig_StdioAuthCredentialKeyDefaultIsPinned(t *testing.T) {
	cfg, err := ParseToolsetConfig(map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "stdio", "command": "/usr/bin/npx", "args": []any{"-y", "server-a"}},
		"auth":              map[string]any{"type": "oauth2"},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if want := "mcp_npx_oauth2"; cfg.AuthConfig.CredentialKey != want {
		t.Errorf("CredentialKey = %q, want %q", cfg.AuthConfig.CredentialKey, want)
	}
}

func TestParseToolsetConfig_LoadsAHeaderDeclaredUnderTwoSpellings(t *testing.T) {
	cases := map[string]map[string]any{
		"two spellings in headers": {
			"headers": map[string]any{"X-Api-Key": "a", "x-api-key": "b"},
		},
		"headers against basic auth": {
			"headers": map[string]any{"authorization": "Bearer t"},
			"auth":    map[string]any{"type": "basic", "username": "u", "password": "p"},
		},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			conn := map[string]any{"type": "streamable-http", "url": "https://mcp.example.com/mcp"}
			for k, v := range params {
				conn[k] = v
			}
			if _, err := ParseToolsetConfig(map[string]any{"tool_type": "mcp", "connection_params": conn}); err != nil {
				t.Fatalf("ParseToolsetConfig refused a config that loaded before: %v", err)
			}
		})
	}
}

func TestLowerCaseKeysKeepsTheLastSpellingInSortedOrderEveryTime(t *testing.T) {
	for range 100 {
		got := lowerCaseKeys(map[string]string{"X-A": "first", "x-a": "second", "X-B": "other"})
		if got["x-a"] != "second" || got["x-b"] != "other" || len(got) != 2 {
			t.Fatalf("lowerCaseKeys = %v, want x-a=second (the last spelling in sorted order) and x-b=other", got)
		}
	}
}
