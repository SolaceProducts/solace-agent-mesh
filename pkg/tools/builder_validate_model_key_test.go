// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestValidateComponentConfig_AgentModelKeys(t *testing.T) {
	cases := []struct {
		name      string
		appLine   string
		cfgLine   string
		wantValid bool
		wantPath  string
	}{
		{name: "model_provider where it is read", cfgLine: "model_provider: [planning]", wantValid: true},
		{name: "no model stated", wantValid: true},
		{name: "model_provider beside app_config", appLine: "model_provider: [planning]", wantPath: "apps[0].model_provider"},
		{name: "alias beside app_config", appLine: "model: planning", wantPath: "apps[0].model"},
		{name: "provider string beside app_config", appLine: "model: anthropic/claude-sonnet-5", wantPath: "apps[0].model"},
		{name: "platform default beside app_config", appLine: "model: ${LLM_SERVICE_GENERAL_MODEL_NAME}", wantValid: true},
		{name: "model in app_config", cfgLine: "model: planning", wantPath: "apps[0].app_config/model"},
		{name: "two aliases", cfgLine: "model_provider: [fast, planning]", wantPath: "apps[0].app_config/model_provider"},
		{name: "an empty alias", cfgLine: "model_provider: [\"\"]", wantPath: "apps[0].app_config/model_provider"},
		{name: "model_provider emptied to return to the default", cfgLine: "model_provider: []", wantValid: true},
		{name: "null model_provider", cfgLine: "model_provider:", wantValid: true},
		{name: "null model", appLine: "model:", wantValid: true},
		{name: "empty model beside app_config", appLine: "model: \"\"", wantValid: true},
		{name: "object form beside app_config", appLine: "model: {model: openai/gpt-4o}", wantPath: "apps[0].model"},
		{name: "a number beside app_config", appLine: "model: 4", wantPath: "apps[0].model"},
		{name: "right key and a wrong one beside it", appLine: "model: planning", cfgLine: "model_provider: [general]", wantPath: "apps[0].model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := (&ValidateComponentConfigTool{}).Execute(context.Background(), map[string]any{"config_yaml": agentYAML(tc.appLine, tc.cfgLine)}, nil)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			valid, _ := res.Data["valid"].(bool)
			if valid != tc.wantValid {
				t.Fatalf("valid = %v, want %v; data %v", valid, tc.wantValid, res.Data)
			}
			if tc.wantPath == "" {
				return
			}
			errs, _ := res.Data["errors"].([]schema.ValidationError)
			found := false
			for _, e := range errs {
				found = found || e.Path == tc.wantPath && strings.Contains(e.Message, "model_provider")
			}
			if !found {
				t.Errorf("no error at %s naming model_provider; errors %+v", tc.wantPath, errs)
			}
		})
	}
}

func TestValidateComponentConfig_ModelKeysOnAWorkflowAreNotAnAgentsConcern(t *testing.T) {
	config := "apps:\n  - name: Flow\n    model: planning\n    app_config:\n      agent_type: workflow\n      agent_name: Flow\n"
	result := &schema.ValidationResult{Valid: true}
	refuseUnsavedModelKeys(result, []byte(config))
	if !result.Valid || len(result.Errors) != 0 {
		t.Errorf("a workflow was refused for a model key: %+v", result.Errors)
	}
}

func TestValidateComponentConfig_ModelKeysAreCheckedOnEveryApp(t *testing.T) {
	config := agentYAML("", "") + "  - name: Second\n    model: planning\n    app_config:\n      agent_name: Second\n      description: Another agent used in tests.\n      instruction: do it\n"
	result := &schema.ValidationResult{Valid: true}
	refuseUnsavedModelKeys(result, []byte(config))
	if result.Valid || len(result.Errors) != 1 || result.Errors[0].Path != "apps[1].model" {
		t.Errorf("errors = %+v, want exactly one at apps[1].model", result.Errors)
	}
}

func agentYAML(appLine, cfgLine string) string {
	var b strings.Builder
	b.WriteString("apps:\n  - name: Sales\n")
	if appLine != "" {
		fmt.Fprintf(&b, "    %s\n", appLine)
	}
	b.WriteString("    app_config:\n      agent_name: Sales\n      description: Analyses sales data for the retail business.\n      instruction: do it\n")
	if cfgLine != "" {
		fmt.Fprintf(&b, "      %s\n", cfgLine)
	}
	b.WriteString("      agent_card:\n        description: Analyses sales data for the retail business.\n        skills:\n          - id: sales\n            name: Sales\n            description: Analyses sales data.\n")
	return b.String()
}
