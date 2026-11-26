// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const clusterGoogleSearchBase = "https://google.cluster.example"

func installAWEBaseToolDefaults(t *testing.T) {
	t.Helper()
	t.Setenv("GOOGLE_SEARCH_API_BASE", clusterGoogleSearchBase)
	path := filepath.Join(t.TempDir(), "awe_base.yaml")
	if err := os.WriteFile(path, settings.AWEClusterBaseConfigYAML(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := samapp.LoadFullConfig(t.Context(), &config.YAMLLoader{}, path)
	if err != nil {
		t.Fatalf("load awe_base.yaml: %v", err)
	}
	SetToolDefaults(samapp.ExtractToolDefaults(config.AsMap(cfg)["tool_defaults"]))
	t.Cleanup(func() { SetToolDefaults(nil) })
}

func TestAWEBaseToolDefaults_DeepResearchGoogleBaseNeverBecomesAnotherProvidersBase(t *testing.T) {
	installAWEBaseToolDefaults(t)

	for _, provider := range []string{"exa", "tavily"} {
		t.Run(provider, func(t *testing.T) {
			entry := applyToolDefaults("deep_research", map[string]any{
				"tool_config": map[string]any{"provider": provider, "api_key": "k"},
			})
			cfg := entry["tool_config"].(map[string]any)
			if cfg["google_api_base"] != clusterGoogleSearchBase {
				t.Errorf("google_api_base = %v, want the cluster default", cfg["google_api_base"])
			}
			if _, ok := cfg["api_base"]; ok {
				t.Fatalf("deep_research tool_config gained api_base %v from the cluster defaults", cfg["api_base"])
			}

			deepResearch := lookupBuiltinTool("deep_research", entry)
			toolCtx := &tool.Context{
				ParentTaskID: "t",
				InternalLLMCall: func(context.Context, string, string, float64) (string, error) {
					t.Error("deep_research called the LLM before resolving its search provider")
					return "", nil
				},
			}
			result, err := deepResearch.Execute(t.Context(), map[string]any{"research_question": "q"}, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			if want := provider + " has no API base"; !strings.Contains(result.Message, want) {
				t.Errorf("result = %q, want it to contain %q", result.Message, want)
			}
		})
	}
}

func TestAWEBaseToolDefaults_WebSearchGoogleKeepsItsAPIBaseDefault(t *testing.T) {
	installAWEBaseToolDefaults(t)

	entry := applyToolDefaults("web_search_google", map[string]any{"tool_config": map[string]any{}})
	if got := entry["tool_config"].(map[string]any)["api_base"]; got != clusterGoogleSearchBase {
		t.Errorf("web_search_google api_base = %v, want the cluster default", got)
	}
}
