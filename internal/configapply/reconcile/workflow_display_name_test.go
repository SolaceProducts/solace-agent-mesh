// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
)

func TestPlanWorkflows_WarnsThatAnAuthoredDisplayNameIsIgnored(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutate   func(map[string]any)
		wantKeys []string
	}{
		{name: "no label", mutate: nil},
		{
			name:   "empty label, which the runtime treats as unset",
			mutate: func(m map[string]any) { m["display_name"] = "" },
		},
		{
			name:   "label equal to the resource name",
			mutate: func(m map[string]any) { m["display_name"] = "MuleCollectionWorkflow" },
		},
		{
			name:     "top-level label",
			mutate:   func(m map[string]any) { m["display_name"] = "Mule Collection Workflow" },
			wantKeys: []string{"display_name"},
		},
		{
			name: "nested camelCase label",
			mutate: func(m map[string]any) {
				m["workflow"].(map[string]any)["displayName"] = "Mule Collection Workflow"
			},
			wantKeys: []string{"workflow.displayName"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			desired := []resolve.ResolvedResource{resolvedWorkflow("MuleCollectionWorkflow", workflowSpecFixture(tt.mutate))}
			plan, err := planWorkflows(desired, nil, translate.WorkflowHooks{})
			if err != nil {
				t.Fatalf("planWorkflows: %v", err)
			}
			labelWarnings := slices.DeleteFunc(slices.Clone(plan.Warnings), func(w string) bool {
				return !strings.Contains(w, "is ignored: the platform labels the workflow's card")
			})
			if len(labelWarnings) != len(tt.wantKeys) {
				t.Fatalf("label warnings = %q, want one per %q", labelWarnings, tt.wantKeys)
			}
			for i, key := range tt.wantKeys {
				want := `workflow "MuleCollectionWorkflow" (workflows/MuleCollectionWorkflow.yaml): ` + key +
					` "Mule Collection Workflow" is ignored`
				if !strings.HasPrefix(labelWarnings[i], want) {
					t.Errorf("warning %d = %q, want prefix %q", i, labelWarnings[i], want)
				}
			}
		})
	}
}
