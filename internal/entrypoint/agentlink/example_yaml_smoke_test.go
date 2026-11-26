// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"os"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

var expandVarsPattern = regexp.MustCompile(`\$\{([^}:\s]+)(?:\s*,\s*((?:[^$}]|\$[^{}]|\$\{[^}]*\}|\$)*))?\}`)

func expandVars(text string) string {
	return expandVarsPattern.ReplaceAllStringFunc(text, func(match string) string {
		groups := expandVarsPattern.FindStringSubmatch(match)
		if val, ok := os.LookupEnv(groups[1]); ok {
			return val
		}
		return expandVars(groups[2])
	})
}

func TestTheExampleConfigParses(t *testing.T) {
	sourcetree.RequireInternal(t)
	t.Setenv("CLAIMS_ASSISTANT_TOKEN_SHA256", digest("a"))
	t.Setenv("CLAIMS_FOUNDRY_TOKEN_SHA256", digest("b"))
	t.Setenv("AZURE_TENANT_ID", "tenant")
	t.Setenv("AZURE_CLIENT_ID", "client")
	t.Setenv("AZURE_CLIENT_SECRET", "secret")
	raw, err := os.ReadFile("../../../examples/entrypoints/agent_link_entrypoint_example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		Apps []struct {
			AppConfig struct {
				GatewayAdapter struct {
					Type          string         `yaml:"type"`
					AdapterConfig map[string]any `yaml:"adapter_config"`
				} `yaml:"gateway_adapter"`
			} `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if uerr := yaml.Unmarshal([]byte(expandVars(string(raw))), &top); uerr != nil {
		t.Fatal(uerr)
	}
	ga := top.Apps[0].AppConfig.GatewayAdapter
	if ga.Type != adapterType {
		t.Fatalf("type %q", ga.Type)
	}
	cfg, err := ParseConfig(ga.AdapterConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 2 || cfg.Agents["claims-foundry"].Wake == nil {
		t.Fatalf("agents %+v", cfg.Agents)
	}
}
