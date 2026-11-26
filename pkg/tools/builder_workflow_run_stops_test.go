// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const unknownToolWorkflow = `
apps:
  - name: TriageWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {result: "done"}
      %s
      tools:
        - tool_type: mcp
          tool_name: get_forecast
          connection_params: {type: streamable-http, url: "http://localhost:9999/mcp"}
      nodes:
        - {id: weather, type: tool, tool_name: get_weather%s}
`

func fullValidationFindings(t *testing.T, workflowYAML string) (valid bool, errs, warnings []string) {
	t.Helper()
	manifest := `
name: triage-project
components:
  - filename: triage.yaml
    type: workflow
    name: TriageWorkflow
    origin: session
`
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := t.Context()
	saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", manifest, "application/vnd.sam-build-manifest+yaml")
	saveTestArtifact(ctx, svc, toolCtx, "triage.yaml", workflowYAML, "application/x-yaml")
	result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest, "full_validation": true}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	valid, _ = result.Data["valid"].(bool)
	found, _ := result.Data["warnings"].([]schema.ValidationError)
	for _, w := range found {
		warnings = append(warnings, w.Message)
	}
	return valid, extractValidationErrors(result.Data), warnings
}

func TestFullValidation_AToolNodeThatCannotStopARunIsAWarning(t *testing.T) {
	for _, tc := range []struct {
		name     string
		topLevel string
		node     string
		refused  bool
	}{
		{"nothing keeps it from stopping a run", "", "", true},
		{"fail_fast is false, which does not keep the node from stopping a run", "fail_fast: false", "", true},
		{"the guard is the literal false", "", ", when: false", false},
		{"the guard reads data", "", `, when: "{{workflow.input.go}} == true"`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := strings.Replace(strings.Replace(unknownToolWorkflow, "%s", tc.topLevel, 1), "%s", tc.node, 1)
			valid, errs, warnings := fullValidationFindings(t, doc)
			joined := strings.Join(errs, "\n")
			if tc.refused {
				if valid || !strings.Contains(joined, `"get_weather"`) {
					t.Fatalf("valid = %v, errors = %s; want the unknown tool refused", valid, joined)
				}
				if len(warnings) != 0 {
					t.Errorf("warnings = %v, want a refused node reported as an error only", warnings)
				}
				return
			}
			if !valid {
				t.Fatalf("valid = false, errors = %s; want a node that cannot stop a run to leave the build valid", joined)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], `"get_weather"`) || !strings.Contains(warnings[0], "does not refuse the workflow") {
				t.Errorf("warnings = %v, want one naming the tool and saying the workflow is not refused", warnings)
			}
		})
	}
}
