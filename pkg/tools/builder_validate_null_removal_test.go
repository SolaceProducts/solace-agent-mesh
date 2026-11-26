// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestValidateComponentConfig_NullRemovesASetting(t *testing.T) {
	cases := []struct {
		name      string
		config    string
		wantValid bool
		wantPath  string
	}{
		{name: "null output_schema", config: agentYAML("", "output_schema: null"), wantValid: true},
		{name: "bare max_llm_calls_per_task", config: agentYAML("", "max_llm_calls_per_task:"), wantValid: true},
		{name: "null supports_streaming", config: agentYAML("", "supports_streaming: null"), wantValid: true},
		{name: "null artifact_handling_mode", config: agentYAML("", "artifact_handling_mode: null"), wantValid: true},
		{name: "null allow_list", config: agentYAML("", "inter_agent_communication:\n        allow_list: null"), wantValid: true},
		{name: "null per-tool hil", config: agentYAML("", "tools:\n        - tool_type: builtin\n          tool_name: web_request\n          hil: null"), wantValid: true},
		{name: "null require_approval inside hil is malformed", config: agentYAML("", "tools:\n        - tool_type: builtin\n          tool_name: web_request\n          hil: {require_approval: null}")},
		{name: "null show_args inside hil is malformed", config: agentYAML("", "tools:\n        - tool_type: builtin\n          tool_name: web_request\n          hil: {require_approval: true, show_args: null}")},
		{name: "null tool_config", config: agentYAML("", "tools:\n        - tool_type: builtin\n          tool_name: web_request\n          tool_config: null"), wantValid: true},
		{name: "null on a required key is missing", config: strings.Replace(agentYAML("", ""), "      description: Analyses sales data for the retail business.\n      instruction", "      description: null\n      instruction", 1), wantPath: "apps[0].app_config"},
		{name: "a wrong type is still refused", config: agentYAML("", "max_llm_calls_per_task: lots"), wantPath: "apps[0].app_config/max_llm_calls_per_task"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := (&ValidateComponentConfigTool{}).Execute(context.Background(), map[string]any{"config_yaml": tc.config}, nil)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if valid, _ := res.Data["valid"].(bool); valid != tc.wantValid {
				t.Fatalf("valid = %v, want %v; data %v", valid, tc.wantValid, res.Data)
			}
			if tc.wantPath == "" {
				return
			}
			errs, _ := res.Data["errors"].([]schema.ValidationError)
			for _, e := range errs {
				if e.Path == tc.wantPath {
					return
				}
			}
			t.Errorf("no error at %s; errors %+v", tc.wantPath, errs)
		})
	}
}
