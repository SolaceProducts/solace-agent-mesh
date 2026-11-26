// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"maps"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func definitionWith(extra map[string]any) map[string]any {
	definition := workflowWithNodes([]map[string]any{{"id": "step1", "type": "agent", "agent_name": "uppercaser"}})
	maps.Copy(definition, extra)
	return definition
}

func TestWorkflowSpecFor_WrapsADefinitionTheFlatShapeCannotHold(t *testing.T) {
	for name, tc := range map[string]struct {
		extra       map[string]any
		wantWrapped bool
	}{
		"its own description": {extra: map[string]any{"description": "A definition-level description."}, wantWrapped: true},
		"a name key":          {extra: map[string]any{"name": "inner-name"}, wantWrapped: true},
		"an appConfig key":    {extra: map[string]any{"appConfig": map[string]any{"nested": "value"}}, wantWrapped: true},
		"none of those":       {},
	} {
		t.Run(name, func(t *testing.T) {
			definition := definitionWith(tc.extra)

			spec := WorkflowSpecFor(definition)

			want := definition
			if tc.wantWrapped {
				want = map[string]any{"appConfig": definition}
			}
			if !reflect.DeepEqual(spec, want) {
				t.Errorf("spec = %v, want %v", spec, want)
			}
		})
	}
}

func TestWorkflowSpecFor_IsReadBackAsTheSameDefinition(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"its own description": {"description": "A definition-level description."},
		"a name key":          {"name": "inner-name"},
		"an appConfig key":    {"appConfig": map[string]any{"nested": "value"}},
		"none of those":       nil,
	} {
		t.Run(name, func(t *testing.T) {
			definition := definitionWith(extra)
			stored := []wire.WorkflowResponse{actualWorkflow("0190f5ec-0000-7000-8000-00000000c001", "text-chain", definition)}
			desired := []resolve.ResolvedResource{resolvedWorkflow("text-chain", WorkflowSpecFor(definition))}

			plan, err := planWorkflows(desired, stored, translate.WorkflowHooks{})

			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
				t.Errorf("unchanged=%d updates=%d, want the workflow unchanged", len(plan.Unchanged), len(plan.Updates))
			}
		})
	}
}

const (
	flatDescriptionNotAppliedWarning = `workflow "text-chain" (workflows/text-chain.yaml): spec.description is not applied: ` +
		`without spec.appConfig, description is the resource's own field, so the definition is stored without it. ` +
		`Put the definition under spec.appConfig to keep it`
	flatNameNotAppliedWarning = `workflow "text-chain" (workflows/text-chain.yaml): spec.name is not applied: ` +
		`without spec.appConfig, name is the resource's own field, so the definition is stored without it. ` +
		`Put the definition under spec.appConfig to keep it`
)

func TestPlanWorkflows_AFlatSpecThatSetsAHeaderKeyIsFlagged(t *testing.T) {
	wantDefinition := map[string]any{
		"version":        "1.0.0",
		"nodes":          []any{map[string]any{"id": "step1", "type": "agent", "agent_name": "uppercaser"}},
		"output_mapping": map[string]any{"result": "{{step1.output.text}}"},
	}
	for _, tc := range []struct {
		name         string
		headerKeys   map[string]any
		wantWarnings []string
	}{
		{
			name:         "a description alone is flagged once",
			headerKeys:   map[string]any{"description": "A definition-level description."},
			wantWarnings: []string{flatDescriptionNotAppliedWarning},
		},
		{
			name:         "a name alone is flagged once",
			headerKeys:   map[string]any{"name": "inner-name"},
			wantWarnings: []string{flatNameNotAppliedWarning},
		},
		{
			name:         "a description and a name are each flagged once",
			headerKeys:   map[string]any{"description": "A definition-level description.", "name": "inner-name"},
			wantWarnings: []string{flatDescriptionNotAppliedWarning, flatNameNotAppliedWarning},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flat := definitionWith(tc.headerKeys)

			plan, err := planWorkflows([]resolve.ResolvedResource{resolvedWorkflow("text-chain", flat)}, nil, translate.WorkflowHooks{})

			if err != nil {
				t.Fatalf("planWorkflows(flat spec with %v) error = %v, want nil", tc.headerKeys, err)
			}
			sortWarnings := cmpopts.SortSlices(func(a, b string) bool { return a < b })
			if diff := cmp.Diff(tc.wantWarnings, plan.Warnings, sortWarnings); diff != "" {
				t.Errorf("planWorkflows(flat spec with %v) warnings mismatch (-want +got):\n%s", tc.headerKeys, diff)
			}
			if len(plan.Creates) != 1 {
				t.Fatalf("planWorkflows(flat spec with %v) creates = %d, want 1", tc.headerKeys, len(plan.Creates))
			}
			if diff := cmp.Diff(wantDefinition, plan.Creates[0].Create.AppConfig); diff != "" {
				t.Errorf("planWorkflows(flat spec with %v) definition mismatch (-want +got):\n%s", tc.headerKeys, diff)
			}
		})
	}
}

func TestPlanWorkflows_AWrappedOrPlainFlatSpecIsNotFlagged(t *testing.T) {
	for name, spec := range map[string]map[string]any{
		"wrapped with its own description": {"appConfig": definitionWith(map[string]any{"description": "A definition-level description."})},
		"wrapped with a description beside the wrapper": {
			"appConfig":   definitionWith(nil),
			"description": "A description beside the wrapper.",
		},
		"flat with no header key": definitionWith(nil),
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := planWorkflows([]resolve.ResolvedResource{resolvedWorkflow("text-chain", spec)}, nil, translate.WorkflowHooks{})

			if err != nil {
				t.Fatalf("planWorkflows(%v) error = %v, want nil", spec, err)
			}
			if len(plan.Warnings) != 0 {
				t.Errorf("planWorkflows(%v) warnings = %v, want none", spec, plan.Warnings)
			}
		})
	}
}
