// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"
)

func TestValidateBuildManifest_ConnectorBoundByManifestNeedsNoKeyInAgentConfig(t *testing.T) {
	const manifest = `
name: retail
components:
  - filename: retail_poslog.json
    type: connector
    name: retail-poslog
    origin: session
    connector_type: sql
  - filename: retail_pos_analyst.yaml
    type: agent
    name: Retail POS Analyst
    origin: session
    depends_on: [retail-poslog]
    connectors: [retail-poslog]
`
	agentConfig := agentYAML("", "")

	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()
	saveTestArtifact(ctx, svc, toolCtx, "retail_pos_analyst.yaml", agentConfig, "application/x-yaml")
	saveTestArtifact(ctx, svc, toolCtx, "retail_poslog.json", `{"name": "retail-poslog", "type": "sql", "subtype": "postgres", "values": {}}`, "application/json")

	componentCheck, err := (&ValidateComponentConfigTool{}).Execute(ctx, map[string]any{"config_yaml": agentConfig}, nil)
	if err != nil {
		t.Fatalf("component validation: %v", err)
	}
	if valid, _ := componentCheck.Data["valid"].(bool); !valid {
		t.Fatalf("component validation rejected the agent config: %v", componentCheck.Data["errors"])
	}

	result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest, "full_validation": true}, toolCtx)
	if err != nil {
		t.Fatalf("full validation: %v", err)
	}
	if valid, _ := result.Data["valid"].(bool); !valid {
		t.Errorf("full validation refused a connector the manifest binds: %s", strings.Join(extractValidationErrors(result.Data), " | "))
	}
}
