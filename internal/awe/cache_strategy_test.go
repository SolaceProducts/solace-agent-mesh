// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestExtractCacheStrategy(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{
			name: "map with 5m",
			raw:  map[string]any{"model": "claude", "cache_strategy": "5m"},
			want: "5m",
		},
		{
			name: "map with 1h",
			raw:  map[string]any{"model": "claude", "cache_strategy": "1h"},
			want: "1h",
		},
		{
			name: "map with none",
			raw:  map[string]any{"model": "claude", "cache_strategy": "none"},
			want: "none",
		},
		{
			name: "map without cache_strategy defaults to 5m",
			raw:  map[string]any{"model": "claude"},
			want: "5m",
		},
		{
			name: "string model defaults to 5m",
			raw:  "claude-sonnet",
			want: "5m",
		},
		{
			name: "invalid strategy defaults to 5m",
			raw:  map[string]any{"model": "claude", "cache_strategy": "bogus"},
			want: "5m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCacheStrategy(tt.raw)
			if got != tt.want {
				t.Errorf("extractCacheStrategy() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseAgentConfigCacheStrategy(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			name: "map model with cache_strategy",
			cfg: map[string]any{
				"agent_name": "test",
				"model": map[string]any{
					"model":          "anthropic/claude-sonnet-4-20250514",
					"cache_strategy": "1h",
				},
			},
			want: "1h",
		},
		{
			name: "string model defaults to 5m",
			cfg: map[string]any{
				"agent_name": "test",
				"model":      "claude-sonnet",
			},
			want: "5m",
		},
		{
			name: "no model at all defaults to empty",
			cfg: map[string]any{
				"agent_name": "test",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac, err := ParseAgentConfig(config.FromMap(tt.cfg))
			if err != nil {
				t.Fatal(err)
			}
			if ac.CacheStrategy != tt.want {
				t.Errorf("CacheStrategy = %q, want %q", ac.CacheStrategy, tt.want)
			}
		})
	}
}
