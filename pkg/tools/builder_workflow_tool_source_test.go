// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"fmt"
	"strings"
	"testing"
)

func weatherWorkflowYAML(sourceDeclaration string) string {
	return fmt.Sprintf(`apps:
  - name: weather_flow
    app_config:
      agent_name: WeatherFlow
      agent_type: workflow
      description: "Workflow that reads the current weather through a tool node"
%s      nodes:
        - id: fetch_weather
          type: tool
          tool_name: WeatherToolset__get_current_weather
      output_mapping:
        weather: "{{fetch_weather.output}}"
`, sourceDeclaration)
}

func TestValidateComponentConfig_RefusesToolNodeWithoutToolSource(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(t.Context(), svc, toolCtx, manifestFilename, minimalManifestYAML, SAMBuildManifestMIMEType)

	undeclared := validateConfigIn(t, toolCtx, weatherWorkflowYAML(""), SAMWorkflowConfigMIMEType)
	data := undeclared.Data
	if valid, _ := data["valid"].(bool); valid {
		t.Fatalf("a tool node whose workflow declares no source must be invalid; got %v", data)
	}
	if errs := fmt.Sprint(data["errors"]); !strings.Contains(errs, "`toolsets: [WeatherToolset]`") {
		t.Errorf("errors should tell the Builder which declaration to add; got %s", errs)
	}

	declared := validateConfigIn(t, toolCtx, weatherWorkflowYAML("      toolsets: [WeatherToolset]\n"), SAMWorkflowConfigMIMEType)
	data = declared.Data
	if valid, _ := data["valid"].(bool); !valid {
		t.Fatalf("declaring the toolset must make the workflow valid; got %v", data)
	}
}
