// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	olderSearchID   = "01a1e6d0-0000-7000-8000-00000000aaaa"
	newerSearchID   = "01a1e6d1-0000-7000-8000-00000000bbbb"
	researchAgentID = "01a1e6e0-0000-7000-8000-00000000cccc"
)

func googleResearchSettings() map[string]any {
	return map[string]any{
		"google_search_api_key": "g-key", "google_cse_id": "cse", "google_api_base": "https://g.example", "api_base": "https://g.example",
		"provider": "google", "max_iterations": 3,
	}
}

func webSearchConn(id, provider string, values map[string]any) ConnectorConfig {
	return ConnectorConfig{ID: id, Type: webSearchConnectorType, Subtype: provider, Values: values}
}

func researchConfig(cfg map[string]any) ToolEmission {
	return ToolEmission{BuiltinToolConfigs: map[string]map[string]map[string]any{researchToolset: {deepResearchTool: cfg}}}
}

func agentTools(t *testing.T, toolsets []string, connectors []ConnectorConfig, emission ToolEmission) []map[string]any {
	t.Helper()
	desc, prompt := "researches the web for the analysts", "You research questions on the web and cite your sources."
	agent := &store.Agent{ID: researchAgentID, Name: "research-agent", Description: &desc, SystemPrompt: &prompt, Type: "standard"}
	cfg, err := NewYAMLService("test-ns", slog.New(slog.DiscardHandler)).BuildAgentConfigFull(agent, toolsets, []string{"text"}, []string{"text"}, connectors, emission)
	if err != nil {
		t.Fatalf("BuildAgentConfigFull: %v", err)
	}
	tools, err := unwrapTools(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

func deepResearchEntry(t *testing.T, toolsets []string, connectors []ConnectorConfig, emission ToolEmission) map[string]any {
	t.Helper()
	for _, entry := range agentTools(t, toolsets, connectors, emission) {
		if entry["tool_name"] == deepResearchTool {
			return entry
		}
	}
	return nil
}

func TestDeepResearchSearchesThroughTheAgentsWebSearchConnector(t *testing.T) {
	t.Parallel()
	tavily := webSearchConn(olderSearchID, "tavily", map[string]any{"api_key": "tvly-key", "safe_search": "yes"})
	exa := webSearchConn(newerSearchID, "exa", map[string]any{"api_key": "exa-key", "api_base": "https://exa.mirror.example"})
	googleConfig := researchConfig(googleResearchSettings())
	tavilyConfig := map[string]any{"provider": "tavily", "api_key": "tvly-key", "api_base": "https://api.tavily.com", "safe_search": true}

	for _, tc := range []struct {
		name       string
		connectors []ConnectorConfig
		emission   ToolEmission
		want       map[string]any
	}{
		{"one connector and no config of its own", []ConnectorConfig{tavily}, ToolEmission{}, tavilyConfig},
		{"a connector over a Google config", []ConnectorConfig{tavily}, googleConfig,
			map[string]any{"provider": "tavily", "api_key": "tvly-key", "api_base": "https://api.tavily.com", "safe_search": true, "max_iterations": 3}},
		{"the oldest of two, bound newest first", []ConnectorConfig{exa, tavily}, ToolEmission{}, tavilyConfig},
		{"the oldest of two, bound oldest first", []ConnectorConfig{tavily, exa}, ToolEmission{}, tavilyConfig},
		{"safe search the author turned on stays on", []ConnectorConfig{exa}, researchConfig(map[string]any{"safe_search": "active"}),
			map[string]any{"provider": "exa", "api_key": "exa-key", "api_base": "https://exa.mirror.example", "safe_search": true}},
		{"safe search the author set unreadably stays on", []ConnectorConfig{exa}, researchConfig(map[string]any{"safe_search": "maybe"}),
			map[string]any{"provider": "exa", "api_key": "exa-key", "api_base": "https://exa.mirror.example", "safe_search": true}},
		{"safe search the author set to the connector's \"no\" stays on", []ConnectorConfig{exa}, researchConfig(map[string]any{"safe_search": "no"}),
			map[string]any{"provider": "exa", "api_key": "exa-key", "api_base": "https://exa.mirror.example", "safe_search": true}},
		{"safe search the author left empty stays on", []ConnectorConfig{exa}, researchConfig(map[string]any{"safe_search": ""}),
			map[string]any{"provider": "exa", "api_key": "exa-key", "api_base": "https://exa.mirror.example", "safe_search": true}},
		{"safe search the author turned off yields to the connector's", []ConnectorConfig{tavily}, researchConfig(map[string]any{"safe_search": false}), tavilyConfig},
		{"safe search off on both is left out, so an operator's tool_defaults can turn it on", []ConnectorConfig{exa}, researchConfig(map[string]any{"safe_search": false}),
			map[string]any{"provider": "exa", "api_key": "exa-key", "api_base": "https://exa.mirror.example"}},
		{"a connector whose provider this build does not know is passed over", []ConnectorConfig{webSearchConn("01a1e6c0-0000-7000-8000-000000000000", "bing", nil), exa}, ToolEmission{},
			map[string]any{"provider": "exa", "api_key": "exa-key", "api_base": "https://exa.mirror.example"}},
		{"a connector with no stored key names its environment variable", []ConnectorConfig{webSearchConn(olderSearchID, "tavily", nil)}, ToolEmission{},
			map[string]any{"provider": "tavily", "api_key": "${API_KEY_01A1E6D0_0000_7000_8000_00000000AAAA}", "api_base": "https://api.tavily.com"}},
		{"no Web Search connector keeps the Google config", []ConnectorConfig{{ID: olderSearchID, Type: "sql", Subtype: "tavily"}}, googleConfig,
			map[string]any{
				"google_search_api_key": "g-key", "google_cse_id": "cse", "google_api_base": "https://g.example", "api_base": "https://g.example",
				"provider": "google", "max_iterations": 3,
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry := deepResearchEntry(t, []string{researchToolset}, tc.connectors, tc.emission)
			if entry == nil {
				t.Fatal("the agent has no deep_research entry")
			}
			got, _ := entry["tool_config"].(map[string]any)
			if !maps.Equal(got, tc.want) {
				t.Fatalf("tool_config = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeepResearchGetsTheConnectorOnlyWhereTheAgentHasTheTool(t *testing.T) {
	t.Parallel()
	tavily := []ConnectorConfig{webSearchConn(olderSearchID, "tavily", map[string]any{"api_key": "tvly-key"})}

	t.Run("no research toolset", func(t *testing.T) {
		t.Parallel()
		tools := agentTools(t, []string{"builtin_time_tools"}, tavily, ToolEmission{})
		if !slices.ContainsFunc(tools, func(e map[string]any) bool {
			return e["tool_type"] == "builtin-group" && e["group_name"] == "builtin_time_tools"
		}) {
			t.Fatalf("tools = %v, want builtin_time_tools as one group entry", tools)
		}
		if slices.ContainsFunc(tools, func(e map[string]any) bool { return e["tool_name"] == deepResearchTool }) {
			t.Fatalf("tools = %v, want no deep_research", tools)
		}
	})
	t.Run("deep_research excluded", func(t *testing.T) {
		t.Parallel()
		excluded := ToolEmission{BuiltinExcludedTools: map[string][]string{researchToolset: {deepResearchTool}}}
		tools := agentTools(t, []string{researchToolset}, tavily, excluded)
		if slices.ContainsFunc(tools, func(e map[string]any) bool { return e["tool_name"] == deepResearchTool }) {
			t.Fatalf("tools = %v, want no deep_research", tools)
		}
		if !slices.ContainsFunc(tools, func(e map[string]any) bool { return e["tool_name"] == "web_search_google" && e["tool_config"] == nil }) {
			t.Fatalf("tools = %v, want web_search_google without the connector's config", tools)
		}
	})
}

func TestDeepResearchConnectorLeavesTheGivenConfigUnchanged(t *testing.T) {
	t.Parallel()
	stored := func() ToolEmission {
		return researchConfig(map[string]any{"google_search_api_key": "g-key", "google_cse_id": "cse"})
	}
	given := stored()

	deepResearchEntry(t, []string{researchToolset}, []ConnectorConfig{webSearchConn(olderSearchID, "tavily", map[string]any{"api_key": "tvly-key"})}, given)

	if !reflect.DeepEqual(given, stored()) {
		t.Fatalf("given emission = %v, want it unchanged", given)
	}
}

func TestDeepResearchConnectorLogsTheSettingsItReplaces(t *testing.T) {
	t.Parallel()
	const replacedMsg, unreadableMsg = "instead of its own search settings", "neither true nor false"
	const unreadableCanary, keptCanary = "SAFE-CANARY-7391x", 7391
	providerKeys := func() map[string]any {
		return map[string]any{
			"provider": "google", "api_key": "g-secret", "api_base": "https://g.example", "google_search_api_key": "g-secret",
			"google_cse_id": "cse-1", "google_api_base": "https://g.example", "max_iterations": keptCanary,
		}
	}
	values := []string{"g-secret", "cse-1", "g.example", "tvly-key", unreadableCanary, "7391"}
	for _, tc := range []struct {
		name       string
		emission   ToolEmission
		want       []string
		wantAbsent []string
	}{
		{"no settings of its own", ToolEmission{}, nil, []string{replacedMsg, unreadableMsg}},
		{"every provider key", researchConfig(providerKeys()), []string{
			"level=WARN", replacedMsg, `replacedKeys="[api_base api_key google_api_base google_cse_id google_search_api_key provider]"`,
			"toolset=" + researchToolset, "agentID=" + researchAgentID, "connectorID=" + olderSearchID,
		}, []string{unreadableMsg, "max_iterations"}},
		{"every provider key and an unreadable safe search", researchConfig(func() map[string]any {
			keys := providerKeys()
			keys["safe_search"] = []any{unreadableCanary}
			return keys
		}()), []string{
			`level=WARN msg="deep_research searches through`, `level=WARN msg="deep_research tool_config safe_search`, `valueType="[]interface {}"`,
		}, nil},
		{"only safe search, kept on", researchConfig(map[string]any{"safe_search": true}), nil, []string{replacedMsg, unreadableMsg}},
		{"an unreadable safe search", researchConfig(map[string]any{"safe_search": unreadableCanary}),
			[]string{"level=WARN", unreadableMsg, "valueType=string", "toolset=" + researchToolset}, []string{replacedMsg}},
		{"every provider key on an excluded deep_research", ToolEmission{
			BuiltinToolConfigs:   researchConfig(providerKeys()).BuiltinToolConfigs,
			BuiltinExcludedTools: map[string][]string{researchToolset: {deepResearchTool}},
		}, nil, []string{replacedMsg, unreadableMsg}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			buildResearchAgent(t, slog.New(slog.NewTextHandler(&logs, nil)), tc.emission)
			out := logs.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("logs = %q, want %q", out, want)
				}
			}
			for _, absent := range slices.Concat(tc.wantAbsent, values) {
				if strings.Contains(out, absent) {
					t.Errorf("logs = %q, want no %q", out, absent)
				}
			}
		})
	}
}

func TestDeployerLogsABoundConnectorItCannotRead(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	var logs bytes.Buffer
	deployer := NewStandardAgentDeployer(nil, repo, nil, nil, slog.New(slog.NewTextHandler(&logs, nil)))

	connectors, err := deployer.resolveConnectors(t.Context(), researchAgentID, []string{olderSearchID})
	if err != nil {
		t.Fatalf("resolveConnectors: %v", err)
	}
	if len(connectors) != 0 {
		t.Fatalf("connectors = %v, want none", connectors)
	}
	for _, want := range []string{"level=WARN", "could not read a bound connector", "agentID=" + researchAgentID, "connectorID=" + olderSearchID} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs = %q, want %q", logs.String(), want)
		}
	}
}

func TestDeepResearchConnectorWithoutALogger(t *testing.T) {
	t.Parallel()
	cfg := buildResearchAgent(t, nil, researchConfig(map[string]any{"google_search_api_key": "g-key", "google_cse_id": "cse"}))
	tools, err := unwrapTools(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range tools {
		if entry["tool_name"] == deepResearchTool {
			if got, _ := entry["tool_config"].(map[string]any); got["provider"] != "tavily" || got["google_search_api_key"] != nil {
				t.Fatalf("tool_config = %v, want the connector's provider and no Google key", got)
			}
			return
		}
	}
	t.Fatalf("tools = %v, want a deep_research entry", tools)
}

func buildResearchAgent(t *testing.T, logger *slog.Logger, emission ToolEmission) map[string]any {
	t.Helper()
	desc, prompt := "researches the web for the analysts", "You research questions on the web and cite your sources."
	agent := &store.Agent{ID: researchAgentID, Name: "research-agent", Description: &desc, SystemPrompt: &prompt, Type: "standard"}
	cfg, err := NewYAMLService("test-ns", logger).BuildAgentConfigFull(agent, []string{researchToolset}, []string{"text"}, []string{"text"},
		[]ConnectorConfig{webSearchConn(olderSearchID, "tavily", map[string]any{"api_key": "tvly-key"})}, emission)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
