// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"log/slog"
	"strings"
	"testing"
)

func TestRequiresManifestForAuth(t *testing.T) {
	t.Parallel()
	entry := func(connType string, withAuth, withManifest bool) map[string]any {
		raw := map[string]any{
			"tool_type":         "mcp",
			"connection_params": map[string]any{"type": connType, "url": "http://127.0.0.1:1/mcp"},
		}
		if withAuth {
			raw["auth"] = map[string]any{"type": "oauth2"}
		}
		if withManifest {
			raw["manifest"] = []any{map[string]any{"name": "a"}}
		}
		return raw
	}
	cases := []struct {
		name string
		raw  map[string]any
		want bool
	}{
		{"streamable-http with auth and no manifest", entry("streamable-http", true, false), true},
		{"streamable-http with auth and a manifest", entry("streamable-http", true, true), false},
		{"streamable-http without auth", entry("streamable-http", false, false), false},
		{"sse with auth and no manifest", entry("sse", true, false), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := ParseToolsetConfig(tc.raw)
			if err != nil {
				t.Fatalf("ParseToolsetConfig: %v", err)
			}
			if got := cfg.RequiresManifestForAuth(); got != tc.want {
				t.Fatalf("RequiresManifestForAuth = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAddClientRefusesWhatRequiresManifestForAuthReports(t *testing.T) {
	t.Parallel()
	cfg, err := ParseToolsetConfig(map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "streamable-http", "url": "http://127.0.0.1:1/mcp"},
		"auth":              map[string]any{"type": "oauth2"},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	err = mgr.AddClient(cfg)
	if err == nil || !strings.Contains(err.Error(), "requires a manifest") {
		t.Fatalf("AddClient should refuse an authenticated streamable-http toolset with no manifest, got %v", err)
	}
}
