// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"testing"
)

func TestValidateComponentConfig_A2AAgent(t *testing.T) {
	tool := &ValidateComponentConfigTool{}

	valid := `{
		"name": "WeatherProxy",
		"agentUrl": "http://ec2-host:9000",
		"agentCardLocation": "well_known",
		"agentAuthConfig": { "type": "bearer", "token": "<<__SAM_REQUIRED__>>" }
	}`
	res, err := tool.Execute(context.Background(), map[string]any{
		"config_yaml": valid,
		"mime_type":   SAMA2AAgentConfigMIMEType,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, _ := res.Data["valid"].(bool); !v {
		t.Errorf("expected valid a2a config, got data=%v", res.Data)
	}
	if ct, _ := res.Data["component_type"].(string); ct != "a2a_agent" {
		t.Errorf("expected component_type a2a_agent, got %q", ct)
	}

	invalid := `{ "agentCardLocation": "well_known" }`
	res2, err := tool.Execute(context.Background(), map[string]any{
		"config_yaml": invalid,
		"mime_type":   SAMA2AAgentConfigMIMEType,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, _ := res2.Data["valid"].(bool); v {
		t.Errorf("expected invalid a2a config, got data=%v", res2.Data)
	}
	if _, ok := res2.Data["errors"]; !ok {
		t.Errorf("expected errors in result data, got %v", res2.Data)
	}
}
