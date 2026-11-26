// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"slices"
	"strings"
	"testing"
)

func TestAgentModelKeyErrors(t *testing.T) {
	cases := []struct {
		name      string
		app       map[string]any
		appConfig map[string]any
		wantPaths []string
	}{
		{name: "no model stated", app: map[string]any{"name": "a"}, appConfig: map[string]any{"agent_name": "a"}},
		{name: "model_provider inside app_config", appConfig: map[string]any{"model_provider": []any{"planning"}}},
		{name: "model_provider bare string inside app_config", appConfig: map[string]any{"model_provider": "planning"}},
		{name: "null model", appConfig: map[string]any{"model": nil}, app: map[string]any{"model": nil}},
		{name: "empty model", appConfig: map[string]any{"model": " "}, app: map[string]any{"model": ""}},
		{name: "platform default model", appConfig: map[string]any{"model": "${LLM_SERVICE_GENERAL_MODEL_NAME}"}},
		{name: "platform default in the object form", appConfig: map[string]any{"model": map[string]any{"model": "${LLM_SERVICE_GENERAL_MODEL_NAME}"}}},
		{name: "alias in app_config.model", appConfig: map[string]any{"model": "planning"}, wantPaths: []string{"apps[0].app_config/model"}},
		{name: "provider string in app_config.model", appConfig: map[string]any{"model": "openai/gpt-4o"}, wantPaths: []string{"apps[0].app_config/model"}},
		{name: "another env reference in app_config.model", appConfig: map[string]any{"model": "${LLM_SERVICE_PLANNING_MODEL_NAME}"}, wantPaths: []string{"apps[0].app_config/model"}},
		{name: "object form naming a model", appConfig: map[string]any{"model": map[string]any{"model": "openai/gpt-4o"}}, wantPaths: []string{"apps[0].app_config/model"}},
		{name: "object form without a model", appConfig: map[string]any{"model": map[string]any{"api_base": "https://x"}}, wantPaths: []string{"apps[0].app_config/model"}},
		{name: "a number", appConfig: map[string]any{"model": 4}, wantPaths: []string{"apps[0].app_config/model"}},
		{name: "platform default padded with blanks", appConfig: map[string]any{"model": "  ${LLM_SERVICE_GENERAL_MODEL_NAME}\n"}},
		{name: "empty object form", appConfig: map[string]any{"model": map[string]any{}}},
		{name: "platform default with an endpoint of its own", appConfig: map[string]any{"model": map[string]any{"model": "${LLM_SERVICE_GENERAL_MODEL_NAME}", "api_base": "https://other"}}, wantPaths: []string{"apps[0].app_config/model"}},
		{name: "empty model_provider list", appConfig: map[string]any{"model_provider": []any{}}},
		{name: "null model_provider", appConfig: map[string]any{"model_provider": nil}},
		{name: "two model_provider entries", appConfig: map[string]any{"model_provider": []any{"a", "b"}}, wantPaths: []string{"apps[0].app_config/model_provider"}},
		{name: "empty model_provider string", appConfig: map[string]any{"model_provider": " "}, wantPaths: []string{"apps[0].app_config/model_provider"}},
		{name: "empty model_provider entry", appConfig: map[string]any{"model_provider": []any{""}}, wantPaths: []string{"apps[0].app_config/model_provider"}},
		{name: "model_provider entry that is a mapping", appConfig: map[string]any{"model_provider": []any{map[string]any{"name": "a"}}}, wantPaths: []string{"apps[0].app_config/model_provider"}},
		{name: "model_provider number", appConfig: map[string]any{"model_provider": 3}, wantPaths: []string{"apps[0].app_config/model_provider"}},
		{name: "model_provider mapping", appConfig: map[string]any{"model_provider": map[string]any{"a": "b"}}, wantPaths: []string{"apps[0].app_config/model_provider"}},
		{name: "alias beside app_config", app: map[string]any{"model": "planning"}, wantPaths: []string{"apps[0].model"}},
		{name: "object form beside app_config", app: map[string]any{"model": map[string]any{"model": "x/y"}}, wantPaths: []string{"apps[0].model"}},
		{name: "model_provider beside app_config", app: map[string]any{"model_provider": []any{"planning"}}, wantPaths: []string{"apps[0].model_provider"}},
		{
			name:      "every misplacement reported together",
			app:       map[string]any{"model": "a", "model_provider": []any{"b"}},
			appConfig: map[string]any{"model": "c"},
			wantPaths: []string{"apps[0].model", "apps[0].model_provider", "apps[0].app_config/model"},
		},
		{
			name:      "model and a two-entry model_provider inside app_config",
			appConfig: map[string]any{"model": "c", "model_provider": []any{"a", "b"}},
			wantPaths: []string{"apps[0].app_config/model", "apps[0].app_config/model_provider"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AgentModelKeyErrors(tc.app, tc.appConfig, 0)
			var paths []string
			for _, e := range got {
				paths = append(paths, e.Path)
				if !strings.Contains(e.Message, "model_provider") {
					t.Errorf("%s: message %q does not name the key that works", e.Path, e.Message)
				}
			}
			if !slices.Equal(paths, tc.wantPaths) {
				t.Errorf("paths = %v, want %v", paths, tc.wantPaths)
			}
		})
	}
}

func TestAgentModelKeyErrors_PathCarriesTheAppIndex(t *testing.T) {
	got := AgentModelKeyErrors(map[string]any{"model": "x"}, nil, 2)
	if len(got) != 1 || got[0].Path != "apps[2].model" {
		t.Errorf("errors = %+v, want one at apps[2].model", got)
	}
}

func TestValidateConfig_AgentModelProviderIsAcceptedAndModelIsNot(t *testing.T) {
	const head = "apps:\n  - name: probe_agent\n    app_config:\n      agent_name: probe_agent\n      description: An agent used to probe the model key.\n"
	const tail = "      agent_card:\n        description: Probes the model key in tests.\n        skills:\n          - id: probe\n            name: Probe\n            description: Probes.\n"
	cases := []struct {
		name      string
		line      string
		wantValid bool
	}{
		{"model_provider list", "      model_provider: [planning]\n", true},
		{"model_provider empty list", "      model_provider: []\n", true},
		{"model_provider null", "      model_provider:\n", true},
		{"model_provider bare string", "      model_provider: planning\n", true},
		{"no model", "", true},
		{"model_provider with two entries", "      model_provider: [planning, general]\n", false},
		{"model_provider with an empty entry", "      model_provider: [\"\"]\n", false},
		{"model_provider as a mapping", "      model_provider: {a: b}\n", false},
		{"model", "      model: planning\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ValidateConfig([]byte(head + tc.line + tail))
			if err != nil {
				t.Fatalf("ValidateConfig: %v", err)
			}
			if result.Valid != tc.wantValid {
				t.Errorf("valid = %v, want %v; errors %+v", result.Valid, tc.wantValid, result.Errors)
			}
		})
	}
}
