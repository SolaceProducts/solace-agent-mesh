// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestParseModelConfig_GrantID(t *testing.T) {
	cfg, err := ParseModelConfig(map[string]any{"model": "openai/free", llm.GrantIDKey: " grant-1 "}, nil)
	if err != nil {
		t.Fatalf("ParseModelConfig: %v", err)
	}
	if cfg.GrantID != "grant-1" {
		t.Errorf("GrantID = %q", cfg.GrantID)
	}
	if _, passed := cfg.ProviderConfig[llm.GrantIDKey]; passed {
		t.Error("grant_id was passed through to the provider config")
	}
}

func TestParseModelConfig_GrantIDRefusals(t *testing.T) {
	cases := map[string]map[string]any{
		"empty":        {"model": "openai/free", llm.GrantIDKey: ""},
		"with api key": {"model": "openai/free", llm.GrantIDKey: "grant-1", "api_key": "k"},
		"with oauth2":  {"model": "openai/free", llm.GrantIDKey: "grant-1", "oauth_token_url": "https://id.example.com/token"},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseModelConfig(raw, nil); err == nil || !strings.Contains(err.Error(), llm.GrantIDKey) {
				t.Fatalf("err = %v, want a grant_id refusal", err)
			}
		})
	}
}
