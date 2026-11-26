// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestExtractReasoningBudgetTokens(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want int
	}{
		{
			name: "map with thinking.budget_tokens int",
			raw: map[string]any{
				"model": "claude",
				"thinking": map[string]any{
					"budget_tokens": 10000,
				},
			},
			want: 10000,
		},
		{
			name: "map with thinking.budget_tokens float64",
			raw: map[string]any{
				"model": "claude",
				"thinking": map[string]any{
					"budget_tokens": float64(8192),
				},
			},
			want: 8192,
		},
		{
			name: "map without thinking key",
			raw:  map[string]any{"model": "claude"},
			want: 0,
		},
		{
			name: "thinking is not a map",
			raw: map[string]any{
				"model":    "claude",
				"thinking": "enabled",
			},
			want: 0,
		},
		{
			name: "thinking map without budget_tokens",
			raw: map[string]any{
				"model": "claude",
				"thinking": map[string]any{
					"other_field": true,
				},
			},
			want: 0,
		},
		{
			name: "string model (no map)",
			raw:  "claude-sonnet",
			want: 0,
		},
		{
			name: "nil input",
			raw:  nil,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractReasoningBudgetTokens(tt.raw)
			if got != tt.want {
				t.Errorf("extractReasoningBudgetTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseAgentConfigReasoningBudgetTokens(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want int
	}{
		{
			name: "model with thinking.budget_tokens",
			cfg: map[string]any{
				"agent_name": "test",
				"model": map[string]any{
					"model": "anthropic/claude-sonnet-4-20250514",
					"thinking": map[string]any{
						"budget_tokens": 10000,
					},
				},
			},
			want: 10000,
		},
		{
			name: "model without thinking config",
			cfg: map[string]any{
				"agent_name": "test",
				"model": map[string]any{
					"model": "openai/gpt-4",
				},
			},
			want: 0,
		},
		{
			name: "string model",
			cfg: map[string]any{
				"agent_name": "test",
				"model":      "claude-sonnet",
			},
			want: 0,
		},
		{
			name: "no model at all",
			cfg: map[string]any{
				"agent_name": "test",
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac, err := ParseAgentConfig(config.FromMap(tt.cfg))
			if err != nil {
				t.Fatal(err)
			}
			if ac.ReasoningBudgetTokens != tt.want {
				t.Errorf("ReasoningBudgetTokens = %d, want %d", ac.ReasoningBudgetTokens, tt.want)
			}
		})
	}
}
