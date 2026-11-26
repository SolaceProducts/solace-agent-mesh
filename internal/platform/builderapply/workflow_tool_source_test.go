// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"fmt"
	"strings"
	"testing"
)

func timeWorkflowArtifact(sourceDeclaration string) string {
	return fmt.Sprintf(`
apps:
  - name: time_flow
    app_config:
      agent_name: time_flow
      agent_type: workflow
      description: A workflow whose only step is a tool node.
      output_mapping:
        time: "{{stamp.output}}"
%s      nodes:
        - id: stamp
          type: tool
          tool_name: get_current_time
`, sourceDeclaration)
}

func TestDesiredWorkflow_RefusesToolNodeWithoutToolSource(t *testing.T) {
	t.Parallel()
	_, err := desiredWorkflow("time_flow", timeWorkflowArtifact(""))
	if err == nil {
		t.Fatal("a tool node whose workflow declares no source fails every run; the save must refuse it")
	}
	for _, part := range []string{`"stamp"`, "get_current_time", "tool_name: get_current_time}"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q is missing %q", err, part)
		}
	}

	declared := "      tools:\n        - tool_type: builtin\n          tool_name: get_current_time\n"
	if _, err := desiredWorkflow("time_flow", timeWorkflowArtifact(declared)); err != nil {
		t.Fatalf("a declared tool must save: %v", err)
	}
}
