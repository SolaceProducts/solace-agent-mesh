// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const uncoveredTriageWorkflow = `
apps:
  - name: TriageWorkflow
    app_config:
      agent_type: workflow
      decision: {model_alias: ticket-router}
      output_mapping: {department: "{{triage.output.answers.department.choice}}"}
      nodes:
        - id: triage
          type: decision
          input: {ticket: "{{workflow.input.ticket}}"}
          questions:
            department: {type: choice, instructions: "Which team owns the ticket?", criteria: {billing: x, technical: y}}
          route_on: department
          routes: {billing: billing_desk}
        - {id: billing_desk, type: agent, agent_name: Billing, depends_on: [triage]}
`

func fullValidationErrors(t *testing.T, workflowYAML string) (valid bool, errs []string) {
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
	return valid, extractValidationErrors(result.Data)
}

func TestFullValidationRunsTheEnginesRealLoadCheck(t *testing.T) {
	if workflowLoadCheck == nil {
		t.Fatal("no workflow load check registered: the engine is not linked into this test binary")
	}
	valid, errs := fullValidationErrors(t, uncoveredTriageWorkflow)
	if valid {
		t.Fatal("valid = true for a decision route the AWE refuses to load")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "no route for option(s) technical") || strings.Contains(joined, `workflow "TriageWorkflow":`) {
		t.Errorf("errors = %s, want the uncovered option reported once, without the parser's own workflow prefix", joined)
	}
}

func TestFullValidationReportsAMissingDependencyOnce(t *testing.T) {
	routed := strings.Replace(uncoveredTriageWorkflow, "routes: {billing: billing_desk}", "routes: {billing: billing_desk, technical: billing_desk}", 1)
	doc := strings.Replace(routed, "depends_on: [triage]}", "depends_on: [triage, nope]}", 1)
	_, errs := fullValidationErrors(t, doc)
	var naming []string
	for _, e := range errs {
		if strings.Contains(e, "nope") {
			naming = append(naming, e)
		}
		if strings.Contains(e, "would fail to load") {
			t.Errorf("load finding %q repeats the schema check's depends_on error", e)
		}
	}
	if len(naming) != 1 {
		t.Errorf("errors naming the missing node = %v, want exactly one", naming)
	}
}

func TestFullValidationReportsAMissingDependencyUnderTheDependenciesAlias(t *testing.T) {
	routed := strings.Replace(uncoveredTriageWorkflow, "routes: {billing: billing_desk}", "routes: {billing: billing_desk, technical: billing_desk}", 1)
	doc := strings.Replace(routed, "depends_on: [triage]}", "dependencies: [triage, ghost]}", 1)
	valid, errs := fullValidationErrors(t, doc)
	if valid {
		t.Fatal("valid = true for a dependency on a node that does not exist")
	}
	var naming []string
	for _, e := range errs {
		if strings.Contains(e, "ghost") {
			naming = append(naming, e)
		}
		if strings.Contains(e, "would fail to load") {
			t.Errorf("load finding %q: the schema check should have reported the aliased dependency itself", e)
		}
	}
	if len(naming) != 1 {
		t.Errorf("errors naming the missing node = %v, want exactly one", naming)
	}
}

func TestFullValidationReadsDependencySpellingsInTheEnginesOrder(t *testing.T) {
	routed := strings.Replace(uncoveredTriageWorkflow, "routes: {billing: billing_desk}", "routes: {billing: billing_desk, technical: billing_desk}", 1)

	bothSet := strings.Replace(routed, "depends_on: [triage]}", "depends_on: [triage], dependencies: [triage, ghost]}", 1)
	if _, errs := fullValidationErrors(t, bothSet); strings.Contains(strings.Join(errs, "\n"), "ghost") {
		t.Errorf("errors = %v; with both keys set the engine reads depends_on, so the ignored alias must not be checked", errs)
	}

	nullDependsOn := strings.Replace(routed, "depends_on: [triage]}", "depends_on: null, dependencies: [triage, ghost]}", 1)
	_, errs := fullValidationErrors(t, nullDependsOn)
	var naming []string
	for _, e := range errs {
		if strings.Contains(e, "ghost") {
			naming = append(naming, e)
		}
		if strings.Contains(e, "would fail to load") {
			t.Errorf("load finding %q: the schema check should have read dependencies past a null depends_on", e)
		}
	}
	if len(naming) != 1 {
		t.Errorf("errors naming the missing node = %v, want exactly one", naming)
	}
}

func TestFullValidationReportsAMissingDependencyTheSchemaCheckDidNotSee(t *testing.T) {
	previous := workflowLoadCheck
	t.Cleanup(func() { workflowLoadCheck = previous })
	RegisterWorkflowLoadCheck(func(string, map[string]any) error {
		return fmt.Errorf(`node "billing_desk" depends on non-existent node "ghost": %w`, ErrWorkflowMissingDependency)
	})
	routed := strings.Replace(uncoveredTriageWorkflow, "routes: {billing: billing_desk}", "routes: {billing: billing_desk, technical: billing_desk}", 1)

	valid, errs := fullValidationErrors(t, routed)
	joined := strings.Join(errs, "\n")
	if valid || !strings.Contains(joined, "would fail to load") || !strings.Contains(joined, "ghost") {
		t.Errorf("valid = %v, errors = %s; want the load check's missing dependency reported", valid, joined)
	}
}

func TestFullValidationKeepsADistinctLoadErrorBesideAMissingDependency(t *testing.T) {
	doc := strings.Replace(uncoveredTriageWorkflow, "depends_on: [triage]}", "depends_on: [triage, nope]}", 1)
	_, errs := fullValidationErrors(t, doc)
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "'nope'") || !strings.Contains(joined, "no route for option(s) technical") {
		t.Errorf("errors = %s, want both the missing dependency and the uncovered option", joined)
	}
}

func TestFullValidationReportsTheLoadFailureBesideAnUnrelatedSchemaError(t *testing.T) {
	doc := strings.Replace(uncoveredTriageWorkflow, "agent_name: Billing,", `agent_name: Billing, input: {note: "{{ghost.output.text}}"},`, 1)
	_, errs := fullValidationErrors(t, doc)
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "ghost") || !strings.Contains(joined, "no route for option(s) technical") {
		t.Errorf("errors = %s, want both the schema error and the load failure", joined)
	}
}

func TestFullValidationFailsAWorkflowTheEngineWouldNotLoad(t *testing.T) {
	previous := workflowLoadCheck
	t.Cleanup(func() { workflowLoadCheck = previous })

	var checked []string
	RegisterWorkflowLoadCheck(func(name string, appConfig map[string]any) error {
		checked = append(checked, name)
		if _, ok := appConfig["workflow"]; !ok {
			return errors.New("missing workflow body")
		}
		return errors.New(`decision node "triage" has no route for option(s) technical and the node has no default`)
	})

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
	saveTestArtifact(ctx, svc, toolCtx, "triage.yaml", "apps:\n  - name: TriageWorkflow\n    app_config:\n      name: TriageWorkflow\n      workflow:\n        nodes: []\n", "application/x-yaml")

	result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest, "full_validation": true}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checked) != 1 || checked[0] != "TriageWorkflow" {
		t.Fatalf("load check ran for %v, want once for TriageWorkflow", checked)
	}
	if valid, _ := result.Data["valid"].(bool); valid {
		t.Fatal("valid = true, want the workflow the engine would refuse to fail validation")
	}
	errs := strings.Join(extractValidationErrors(result.Data), "\n")
	if !strings.Contains(errs, "would fail to load when deployed") || !strings.Contains(errs, "no route for option(s) technical") {
		t.Errorf("errors = %s, want the load failure reported", errs)
	}
}
