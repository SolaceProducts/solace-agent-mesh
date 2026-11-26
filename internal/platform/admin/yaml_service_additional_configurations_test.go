// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func extractAppConfigForMerge(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	apps, ok := cfg["apps"].([]map[string]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("expected non-empty apps slice, got: %T %v", cfg["apps"], cfg["apps"])
	}
	appCfg, ok := apps[0]["app_config"].(map[string]any)
	if !ok {
		t.Fatalf("expected app_config map, got: %T", apps[0]["app_config"])
	}
	return appCfg
}

func newTestAgentForYAMLMerge(extra []byte) *store.Agent {
	prompt := "You are a helpful assistant. Answer questions thoughtfully and politely; this is long enough to satisfy the platform's prompt-length validator."
	return &store.Agent{
		ID:                       "550e8400-e29b-41d4-a716-446655440000",
		Name:                     "merge-test",
		Description:              strPtr("YAML merge fixture"),
		SystemPrompt:             &prompt,
		Type:                     "standard",
		AdditionalConfigurations: extra,
		CreatedBy:                "tester",
		UpdatedBy:                "tester",
	}
}

func TestBuildAgentConfig_EmptyAdditionalConfigurationsIsNoOp(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge(nil)

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)
	if pub, ok := app["agent_card_publishing"].(map[string]any); !ok || pub["interval_seconds"] != 5 {
		t.Errorf("expected default interval_seconds=5, got: %v", app["agent_card_publishing"])
	}
}

func TestBuildAgentConfig_AdditionalConfigurationsAddsAndOverridesAuthoredKeys(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge([]byte(`{
		"supportsStreaming": true,
		"agentCardPublishing": {"intervalSeconds": 30},
		"artifactHandlingMode": "embed"
	}`))

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	if got, ok := app["supports_streaming"].(bool); !ok || !got {
		t.Errorf("expected supports_streaming=true, got: %v (%T)", app["supports_streaming"], app["supports_streaming"])
	}
	if got, ok := app["artifact_handling_mode"].(string); !ok || got != "embed" {
		t.Errorf("expected artifact_handling_mode=\"embed\", got: %v", app["artifact_handling_mode"])
	}
	pub, ok := app["agent_card_publishing"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_card_publishing map, got: %T", app["agent_card_publishing"])
	}
	if got, _ := pub["interval_seconds"].(float64); got != 30 {
		t.Errorf("expected authored interval_seconds=30, got: %v (%T)", pub["interval_seconds"], pub["interval_seconds"])
	}
	if _, present := app["supportsStreaming"]; present {
		t.Errorf("camelCase 'supportsStreaming' must not appear in emitted YAML — translator must emit only the snake_case runtime key")
	}
	if _, present := pub["intervalSeconds"]; present {
		t.Errorf("camelCase 'intervalSeconds' must not appear in emitted YAML — nested translation didn't fire")
	}
}

func TestBuildAgentConfig_AdditionalConfigurationsDeepMergePreservesSiblingKeys(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge([]byte(`{
		"agentDiscovery": {"healthCheckIntervalSeconds": 90}
	}`))

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	disc, ok := app["agent_discovery"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_discovery map, got: %T", app["agent_discovery"])
	}
	if got, _ := disc["enabled"].(bool); !got {
		t.Errorf("expected agent_discovery.enabled=true preserved, got: %v", disc["enabled"])
	}
	if got, _ := disc["health_check_interval_seconds"].(float64); got != 90 {
		t.Errorf("expected agent_discovery.health_check_interval_seconds=90, got: %v", disc["health_check_interval_seconds"])
	}
}

func TestBuildAgentConfig_AdditionalConfigurationsAuthoredNullOverwrites(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge([]byte(`{"agentCardPublishing":null}`))

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	got, present := app["agent_card_publishing"]
	if !present {
		t.Fatalf("expected agent_card_publishing key to remain (with nil value), got missing")
	}
	if got != nil {
		t.Errorf("expected authored null to overwrite default, got: %#v", got)
	}
}

func TestBuildAgentConfig_LegacyNilColumnIsHarmless(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge(nil)
	a.AdditionalConfigurations = nil

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	if pub, _ := app["agent_card_publishing"].(map[string]any); pub == nil || pub["interval_seconds"] != 5 {
		t.Errorf("expected default interval_seconds=5, got: %v", app["agent_card_publishing"])
	}
	if app["agent_name"] == nil {
		t.Errorf("expected computed agent_name to remain set")
	}
}

func TestBuildAgentConfig_DefensiveStripIgnoresComputedKeyOverride(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge([]byte(`{"agent_name":"hax","tools":[{"x":1}]}`))

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	if got, _ := app["agent_name"].(string); got == "hax" || got == "" {
		t.Errorf("expected platform-computed agent_name to win, got: %q", got)
	}
	tools, _ := app["tools"].([]map[string]any)
	if len(tools) != 0 {
		t.Errorf("expected platform-computed empty tools, got: %v", app["tools"])
	}
}

func TestBuildAgentConfig_TranslatesCamelToSnakeAtAllRegisteredDepths(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge([]byte(`{
		"autoSummarization": {"enabled": true, "compactionPercentage": 0.4},
		"interAgentCommunication": {"allowList": ["a","b"], "requestTimeoutSeconds": 15},
		"agentCardPublishing": {"intervalSeconds": 30},
		"agentDiscovery": {"healthCheckTtlSeconds": 300}
	}`))

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	if v, _ := app["auto_summarization"].(map[string]any); v == nil ||
		v["enabled"] != true || v["compaction_percentage"].(float64) != 0.4 {
		t.Errorf("autoSummarization translation incorrect: %v", app["auto_summarization"])
	}
	if v, _ := app["inter_agent_communication"].(map[string]any); v == nil ||
		v["request_timeout_seconds"].(float64) != 15 {
		t.Errorf("interAgentCommunication translation incorrect: %v", app["inter_agent_communication"])
	}
	if v, _ := app["agent_card_publishing"].(map[string]any); v == nil ||
		v["interval_seconds"].(float64) != 30 {
		t.Errorf("agentCardPublishing translation incorrect: %v", app["agent_card_publishing"])
	}
	if v, _ := app["agent_discovery"].(map[string]any); v == nil ||
		v["health_check_ttl_seconds"].(float64) != 300 {
		t.Errorf("agentDiscovery translation incorrect: %v", app["agent_discovery"])
	}
}

func TestBuildAgentConfig_AgentCardChildrenStayCamelCase(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge([]byte(`{
		"agentCard": {
			"documentationUrl": "https://example.test/docs",
			"defaultInputModes": ["text"],
			"defaultOutputModes": ["text", "image"],
			"welcome": {"message": "hi", "suggestions": ["a","b"]}
		}
	}`))

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	card, ok := app["agent_card"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_card block (snake_case block name), got: %T", app["agent_card"])
	}
	if got, _ := card["documentationUrl"].(string); got != "https://example.test/docs" {
		t.Errorf("expected camelCase documentationUrl preserved inside agent_card, got: %v", card["documentationUrl"])
	}
	if _, present := card["documentation_url"]; present {
		t.Errorf("snake_case 'documentation_url' must not appear inside agent_card — children are identity")
	}
	welcome, _ := card["welcome"].(map[string]any)
	if welcome == nil {
		t.Fatalf("expected welcome sub-block, got: %v", card["welcome"])
	}
	if msg, _ := welcome["message"].(string); msg != "hi" {
		t.Errorf("expected agent_card.welcome.message='hi' (identity), got: %v", welcome["message"])
	}
}

func TestBuildAgentConfig_OpaqueListPassthroughIsTheCasingBoundary(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge([]byte(`{
		"volumes": [
			{"tool_type":"builtin", "tool_name":"x", "tool_config":{"gemini_api_key":"redacted"}}
		]
	}`))

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	vols, ok := app["volumes"].([]any)
	if !ok || len(vols) != 1 {
		t.Fatalf("expected volumes list with 1 element, got: %T %v", app["volumes"], app["volumes"])
	}
	first, ok := vols[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first volume to be a map, got: %T", vols[0])
	}
	for _, key := range []string{"tool_type", "tool_name", "tool_config"} {
		if _, present := first[key]; !present {
			t.Errorf("opaque list element lost snake_case key %q during translation: %v", key, first)
		}
	}
	tc, _ := first["tool_config"].(map[string]any)
	if tc == nil || tc["gemini_api_key"] != "redacted" {
		t.Errorf("expected opaque inner snake_case 'gemini_api_key' preserved, got: %v", first["tool_config"])
	}
}

func TestBuildAgentConfig_UnknownKeysCopiedVerbatim(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge([]byte(`{
		"someExperimentalKey": "lift-me",
		"deeply": {"nested": {"value": 7}}
	}`))

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	if got, _ := app["someExperimentalKey"].(string); got != "lift-me" {
		t.Errorf("expected unknown 'someExperimentalKey' passed verbatim, got: %v", app["someExperimentalKey"])
	}
	d, _ := app["deeply"].(map[string]any)
	if d == nil {
		t.Fatalf("expected unknown nested 'deeply' passed verbatim, got: %v", app["deeply"])
	}
	n, _ := d["nested"].(map[string]any)
	if n == nil || n["value"].(float64) != 7 {
		t.Errorf("expected nested unknown sub-tree preserved, got: %v", d)
	}
}

func TestBuildAgentConfig_WelcomeSuggestionsEndToEnd(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge([]byte(`{
		"agentCard": {
			"welcome": {
				"message": "What would you like to explore today?",
				"suggestions": [
					{"label": "Summarise", "prompt": "Summarise the doc.", "autoSend": true},
					{"label": "Reply",     "prompt": "Draft a reply."}
				]
			}
		}
	}`))

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)

	card, ok := app["agent_card"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_card map, got: %T", app["agent_card"])
	}
	welcome, ok := card["welcome"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_card.welcome map, got: %T", card["welcome"])
	}
	if msg, _ := welcome["message"].(string); msg != "What would you like to explore today?" {
		t.Errorf("expected welcome.message identity, got: %v", welcome["message"])
	}
	sugs, ok := welcome["suggestions"].([]any)
	if !ok {
		t.Fatalf("expected suggestions []any, got: %T", welcome["suggestions"])
	}
	if len(sugs) != 2 {
		t.Fatalf("expected 2 suggestions, got %d", len(sugs))
	}
	first, ok := sugs[0].(map[string]any)
	if !ok {
		t.Fatalf("expected suggestions[0] map, got: %T", sugs[0])
	}
	if got, _ := first["auto_send"].(bool); !got {
		t.Errorf("expected suggestions[0].auto_send=true in emitted YAML, got: %v", first["auto_send"])
	}
	if _, present := first["autoSend"]; present {
		t.Errorf("camelCase 'autoSend' must not appear in emitted runtime YAML — translator did not flip the inner key")
	}

	delete(app, "model_provider")
	parsed, err := awe.ParseAgentConfig(config.FromMap(app))
	if err != nil {
		t.Fatalf("ParseAgentConfig: %v", err)
	}
	if parsed.Welcome == nil {
		t.Fatalf("expected parsed WelcomeConfig, got nil")
	}
	if parsed.Welcome.Message != "What would you like to explore today?" {
		t.Errorf("expected Welcome.Message round-trip, got: %q", parsed.Welcome.Message)
	}
	if len(parsed.Welcome.Suggestions) != 2 {
		t.Fatalf("expected 2 parsed suggestions, got %d", len(parsed.Welcome.Suggestions))
	}
	sug0 := parsed.Welcome.Suggestions[0]
	if sug0.Label != "Summarise" || sug0.Prompt != "Summarise the doc." || !sug0.AutoSend {
		t.Errorf("suggestions[0] mis-parsed: %+v", sug0)
	}
	sug1 := parsed.Welcome.Suggestions[1]
	if sug1.Label != "Reply" || sug1.Prompt != "Draft a reply." || sug1.AutoSend {
		t.Errorf("suggestions[1] mis-parsed: %+v", sug1)
	}
}

func TestBuildAgentConfig_PeerAllowListOverridesDefault(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	a := newTestAgentForYAMLMerge(nil)

	cfg, err := svc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, nil,
		DeployOptions{PeerAllowList: []string{"workflow_abc", "agent_xyz"}})
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	app := extractAppConfigForMerge(t, cfg)
	iac, ok := app["inter_agent_communication"].(map[string]any)
	if !ok {
		t.Fatalf("expected inter_agent_communication map, got: %T", app["inter_agent_communication"])
	}
	allow, ok := iac["allow_list"].([]string)
	if !ok {
		t.Fatalf("expected allow_list []string, got: %T", iac["allow_list"])
	}
	if len(allow) != 2 || allow[0] != "workflow_abc" || allow[1] != "agent_xyz" {
		t.Errorf("allow_list = %v, want [workflow_abc agent_xyz]", allow)
	}
}
