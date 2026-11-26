// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func workflowSpecFixture(f func(map[string]any)) map[string]any {
	m := map[string]any{
		"version":                      "1.0.0",
		"default_node_timeout_seconds": 300,
		"workflow": map[string]any{
			"nodes": map[string]any{
				"generate_script": map[string]any{
					"agent_name": "scriptwriter",
					"next_node":  "render_video",
				},
			},
		},
	}
	if f != nil {
		f(m)
	}
	return m
}

func resolvedWorkflow(name string, spec map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:        "workflow",
		Name:        name,
		Description: "describes " + name,
		SourceFile:  "workflows/" + name + ".yaml",
		Spec:        spec,
	}
}

func actualWorkflow(id, name string, body map[string]any) wire.WorkflowResponse {
	return wire.WorkflowResponse{
		ID:          id,
		Name:        name,
		Description: "describes " + name,
		AppConfig:   body,
	}
}

func TestPlanWorkflows_CreateAndDelete(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedWorkflow("video_creation", workflowSpecFixture(nil))}
	actual := []wire.WorkflowResponse{{ID: "id-old", Name: "obsolete"}}

	plan, err := planWorkflows(desired, actual, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "video_creation" {
		t.Errorf("Creates = %+v", plan.Creates)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].ID != "id-old" {
		t.Errorf("Deletes = %+v", plan.Deletes)
	}
	if len(plan.Updates) != 0 || len(plan.Unchanged) != 0 {
		t.Errorf("expected no updates/unchanged, got updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
}

func TestPlanWorkflows_UnchangedWhenIdentical(t *testing.T) {
	body := workflowSpecFixture(nil)
	desired := []resolve.ResolvedResource{resolvedWorkflow("video_creation", body)}
	actual := []wire.WorkflowResponse{actualWorkflow("id-1", "video_creation", body)}

	plan, err := planWorkflows(desired, actual, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "id-1" {
		t.Errorf("expected single unchanged, got %+v", plan)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("expected no updates, got %+v", plan.Updates)
	}
}

func TestPlanWorkflows_BodyDriftSurfacesAsUpdate(t *testing.T) {
	desiredBody := workflowSpecFixture(func(m map[string]any) {
		m["default_node_timeout_seconds"] = 600
	})
	actualBody := workflowSpecFixture(nil)
	desired := []resolve.ResolvedResource{resolvedWorkflow("video_creation", desiredBody)}
	actual := []wire.WorkflowResponse{actualWorkflow("id-1", "video_creation", actualBody)}

	plan, err := planWorkflows(desired, actual, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-1" {
		t.Fatalf("expected single update, got %+v", plan)
	}
	got := plan.Updates[0].Update.AppConfig["default_node_timeout_seconds"]
	if got != 600 {
		t.Errorf("Update.AppConfig did not carry the new value: got %v want 600", got)
	}
}

func TestPlanWorkflows_HeaderDriftSurfacesAsUpdate(t *testing.T) {
	body := workflowSpecFixture(nil)
	desired := []resolve.ResolvedResource{resolvedWorkflow("video_creation", body)}
	actualResp := actualWorkflow("id-1", "video_creation", body)
	actualResp.Description = "drifted server-side"
	actual := []wire.WorkflowResponse{actualResp}

	plan, err := planWorkflows(desired, actual, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("expected single update, got %+v", plan)
	}
}

func TestPlanWorkflows_PreservesBodyOnCreate(t *testing.T) {
	body := workflowSpecFixture(nil)
	desired := []resolve.ResolvedResource{resolvedWorkflow("video_creation", body)}

	plan, err := planWorkflows(desired, nil, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected one create, got %+v", plan.Creates)
	}
	got := plan.Creates[0].Create.AppConfig
	if got["default_node_timeout_seconds"] != 300 {
		t.Errorf("Create.AppConfig lost a key: got %v", got)
	}
	if _, ok := got["workflow"].(map[string]any); !ok {
		t.Errorf("Create.AppConfig lost the inner workflow map: got %T", got["workflow"])
	}
}

func TestPlanWorkflows_DuplicateNameErrors(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("video_creation", workflowSpecFixture(nil)),
		resolvedWorkflow("video_creation", workflowSpecFixture(nil)),
	}
	if _, err := planWorkflows(desired, nil, translate.WorkflowHooks{}); err == nil {
		t.Errorf("expected error for duplicate workflow names")
	}
}

func TestPlanWorkflows_EmptyBody(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedWorkflow("empty_wf", map[string]any{})}
	plan, err := planWorkflows(desired, nil, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected 1 create, got %+v", plan.Creates)
	}
	if plan.Creates[0].Create.AppConfig == nil {
		t.Errorf("Create.AppConfig should be non-nil even when empty")
	}
	if len(plan.Creates[0].Create.AppConfig) != 0 {
		t.Errorf("Create.AppConfig should be empty, got %+v", plan.Creates[0].Create.AppConfig)
	}
}

func TestPlanWorkflows_BodyKeysShadowingHeadersDropped(t *testing.T) {
	spec := workflowSpecFixture(func(m map[string]any) {
		m["name"] = "shadow"
		m["description"] = "should be dropped from body"
	})
	desired := []resolve.ResolvedResource{resolvedWorkflow("video_creation", spec)}
	plan, err := planWorkflows(desired, nil, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	body := plan.Creates[0].Create.AppConfig
	for _, k := range []string{"name", "description"} {
		if _, leaked := body[k]; leaked {
			t.Errorf("AppConfig should not carry header key %q, got %v", k, body[k])
		}
	}
	if got := plan.Creates[0].Create.Name; got != "video_creation" {
		t.Errorf("typed Name should win, got %q", got)
	}
}

func TestPlanWorkflows_WrappedAppConfigSpecNotDoubleWrapped(t *testing.T) {
	innerBody := map[string]any{
		"default_node_timeout": "90s",
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "RSSummarizerAgent"},
		},
		"output_mapping": map[string]any{"result": "{{nodes.n1.output}}"},
	}
	spec := map[string]any{"appConfig": innerBody}
	desired := []resolve.ResolvedResource{resolvedWorkflow("github_release_notifier", spec)}

	plan, err := planWorkflows(desired, nil, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected one create, got %+v", plan.Creates)
	}
	got := plan.Creates[0].Create.AppConfig
	if _, doubleWrapped := got["appConfig"]; doubleWrapped {
		t.Fatalf("AppConfig is double-wrapped: top-level still carries an %q key. Got keys: %v", "appConfig", keysOf(got))
	}
	if _, ok := got["output_mapping"]; !ok {
		t.Errorf("Create.AppConfig missing output_mapping at top level; keys=%v", keysOf(got))
	}
	if _, ok := got["nodes"]; !ok {
		t.Errorf("Create.AppConfig missing nodes at top level; keys=%v", keysOf(got))
	}
}

func TestPlanWorkflows_WrappedAppConfig_ValidatesNodeRefs(t *testing.T) {
	spec := map[string]any{
		"appConfig": map[string]any{
			"nodes": []any{
				map[string]any{"id": "n1", "type": "agent", "agent_name": "missing-agent"},
			},
			"output_mapping": map[string]any{"result": "x"},
		},
	}
	desired := []resolve.ResolvedResource{resolvedWorkflow("wf", spec)}

	hooks := translate.WorkflowHooks{
		ValidateAgent: func(name string) error {
			return fmt.Errorf("agent %q not declared", name)
		},
	}
	if _, err := planWorkflows(desired, nil, hooks); err == nil {
		t.Fatal("expected dangling agent_name in wrapped-form spec to fail plan, got nil")
	}
}

func TestPlanWorkflows_WrappedAndFlatHashEquivalence(t *testing.T) {
	body := map[string]any{
		"default_node_timeout": "90s",
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "summariser"},
		},
		"output_mapping": map[string]any{"result": "{{nodes.n1.output}}"},
	}
	desired := []resolve.ResolvedResource{resolvedWorkflow("wf", map[string]any{"appConfig": body})}
	actual := []wire.WorkflowResponse{actualWorkflow("id-1", "wf", body)}

	plan, err := planWorkflows(desired, actual, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "id-1" {
		t.Errorf("expected the wrapped-spec / flat-actual pair to hash-equal; got plan = %+v", plan)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("expected zero updates, got %+v", plan.Updates)
	}
}

func TestPlanWorkflows_WrappedShapePrecedence(t *testing.T) {
	wrapped := map[string]any{
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "summariser"},
		},
		"output_mapping": map[string]any{"result": "{{nodes.n1.output}}"},
	}
	spec := map[string]any{
		"appConfig":            wrapped,
		"workflow_timeout":     "9m9s",
		"default_node_timeout": "1m1s",
	}
	desired := []resolve.ResolvedResource{resolvedWorkflow("wf", spec)}

	plan, err := planWorkflows(desired, nil, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected one create, got %+v", plan.Creates)
	}
	got := plan.Creates[0].Create.AppConfig
	for _, leakedKey := range []string{"workflow_timeout", "default_node_timeout", "appConfig"} {
		if _, leaked := got[leakedKey]; leaked && leakedKey == "appConfig" {
			t.Errorf("wrapped path double-wrapped: top-level still carries %q", leakedKey)
		}
	}
	if _, ok := got["output_mapping"]; !ok {
		t.Errorf("Create.AppConfig missing the wrapped output_mapping; keys=%v", keysOf(got))
	}
	if v, leaked := got["workflow_timeout"]; leaked {
		t.Errorf("sibling top-level workflow_timeout leaked into wrapped AppConfig: %v", v)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
