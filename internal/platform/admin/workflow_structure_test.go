// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func structureNodes(nodes ...map[string]any) []any {
	out := make([]any, len(nodes))
	for i, n := range nodes {
		out[i] = n
	}
	return out
}

func agentNode(id string, dependsOn ...string) map[string]any {
	n := map[string]any{"id": id, "type": "agent", "agent_name": "Summarizer"}
	if len(dependsOn) > 0 {
		deps := make([]any, len(dependsOn))
		for i, d := range dependsOn {
			deps[i] = d
		}
		n["depends_on"] = deps
	}
	return n
}

func TestValidateWorkflowRequest_StructureWithoutAName(t *testing.T) {
	cfg := map[string]any{"nodes": structureNodes(agentNode("a", "a"))}
	err := ValidateWorkflowRequest("", cfg)
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err = %#v, want a *ValidationError", err)
	}
	want := []string{`workflow: node "a" depends on itself`}
	if got := ve.Details["appConfig.nodes"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("details = %q, want %q", got, want)
	}
}

func TestValidateWorkflowRequest_Structure(t *testing.T) {
	flat := func(nodes ...map[string]any) map[string]any {
		return map[string]any{"agent_type": "workflow", "nodes": structureNodes(nodes...)}
	}
	nested := func(nodes ...map[string]any) map[string]any {
		return map[string]any{"agent_type": "workflow", "workflow": map[string]any{"nodes": structureNodes(nodes...)}}
	}

	accepted := map[string]map[string]any{
		"a chain":                          flat(agentNode("a"), agentNode("b", "a")),
		"a diamond":                        flat(agentNode("a"), agentNode("b", "a"), agentNode("c", "a"), agentNode("d", "b", "c")),
		"a nested diamond":                 nested(agentNode("a"), agentNode("b", "a"), agentNode("c", "a"), agentNode("d", "b", "c")),
		"a dependency on an unknown node":  flat(agentNode("a", "ghost")),
		"no nodes":                         flat(),
		"a node depending on two parents":  flat(agentNode("a"), agentNode("b"), agentNode("c", "a", "b")),
		"a long chain without a back edge": flat(agentNode("a"), agentNode("b", "a"), agentNode("c", "b"), agentNode("d", "c")),
	}
	for name, cfg := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			if err := ValidateWorkflowRequest("Flow", cfg); err != nil {
				t.Fatalf("a loadable workflow was refused: %v", err)
			}
		})
	}

	const flatField, nestedField = "appConfig.nodes", "appConfig.workflow.nodes"
	rejected := []struct {
		name        string
		cfg         map[string]any
		wantField   string
		wantDetails []string
	}{
		{
			name:        "a two-node cycle",
			cfg:         flat(agentNode("a", "b"), agentNode("b", "a")),
			wantField:   flatField,
			wantDetails: []string{`workflow "Flow": dependency cycle among nodes ["a", "b"]`},
		},
		{
			name:        "a three-node cycle behind a root",
			cfg:         flat(agentNode("root"), agentNode("a", "root", "c"), agentNode("b", "a"), agentNode("c", "b")),
			wantField:   flatField,
			wantDetails: []string{`workflow "Flow": dependency cycle among nodes ["a", "b", "c"]`},
		},
		{
			name:        "a self-dependency",
			cfg:         flat(agentNode("a", "a")),
			wantField:   flatField,
			wantDetails: []string{`workflow "Flow": node "a" depends on itself`},
		},
		{
			name:        "duplicate node ids",
			cfg:         flat(agentNode("a"), agentNode("a")),
			wantField:   flatField,
			wantDetails: []string{`workflow "Flow": node id "a" is defined 2 times; node ids must be unique`},
		},
		{
			name:        "the nested shape reports under its own path",
			cfg:         nested(agentNode("a", "b"), agentNode("b", "a")),
			wantField:   nestedField,
			wantDetails: []string{`workflow "Flow": dependency cycle among nodes ["a", "b"]`},
		},
		{
			name:      "every problem is collected, not just the first",
			cfg:       flat(agentNode("a"), agentNode("a"), agentNode("s", "s"), agentNode("x", "y"), agentNode("y", "x")),
			wantField: flatField,
			wantDetails: []string{
				`workflow "Flow": node id "a" is defined 2 times; node ids must be unique`,
				`workflow "Flow": node "s" depends on itself`,
				`workflow "Flow": dependency cycle among nodes ["x", "y"]`,
			},
		},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			err := ValidateWorkflowRequest("Flow", tc.cfg)
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("err = %#v, want a *ValidationError", err)
			}
			if got := ve.Details[tc.wantField]; !reflect.DeepEqual(got, tc.wantDetails) {
				t.Fatalf("details[%q] = %q, want %q (all details: %v)", tc.wantField, got, tc.wantDetails, ve.Details)
			}
			if ve.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", ve.Field, tc.wantField)
			}
			if !strings.HasPrefix(ve.Message, "workflow cannot be deployed as written: ") {
				t.Errorf("message = %q, want the deploy-time framing", ve.Message)
			}
			if len(ve.Details) != 1 {
				t.Errorf("details = %v, want one field", ve.Details)
			}
			for _, d := range tc.wantDetails {
				if !strings.Contains(ve.Message, d) {
					t.Errorf("message %q does not carry %q", ve.Message, d)
				}
			}
		})
	}
}

func TestWorkflowService_SaveRefusesAnUnloadableStructure(t *testing.T) {
	db := testOpenDB(t)
	svc := NewWorkflowService(store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), nil, nil, "ns", nil)
	body := func(nodes ...map[string]any) map[string]any {
		return map[string]any{"output_mapping": map[string]any{"result": "x"}, "nodes": structureNodes(nodes...)}
	}
	cyclic := body(agentNode("a", "b"), agentNode("b", "a"))
	valid := body(agentNode("a"), agentNode("b", "a"))
	const description = "a workflow used by the structure tests"
	const wantMessage = `workflow "flow": dependency cycle among nodes ["a", "b"]`
	requireRefused := func(t *testing.T, err error) {
		t.Helper()
		ve, ok := errors.AsType[*ValidationError](err)
		if !ok || !strings.Contains(ve.Message, wantMessage) {
			t.Fatalf("err = %v, want a ValidationError carrying %q", err, wantMessage)
		}
	}

	t.Run("create of a new workflow is refused and nothing is stored", func(t *testing.T) {
		_, err := svc.Create(t.Context(), CreateWorkflowRequest{Name: "flow", Description: description, AppConfig: cyclic}, "u")
		requireRefused(t, err)
		if got, _ := svc.workflowRepo.GetByName(t.Context(), "flow"); got != nil {
			t.Errorf("a refused workflow was stored: %+v", got)
		}
	})

	stored, err := svc.Create(t.Context(), CreateWorkflowRequest{Name: "flow", Description: description, AppConfig: valid}, "u")
	if err != nil {
		t.Fatalf("seed a valid workflow: %v", err)
	}
	requireUnchanged := func(t *testing.T) {
		t.Helper()
		got, err := svc.GetByID(t.Context(), stored.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if nodes, _ := got.AppConfig["nodes"].([]any); len(nodes) != 2 || !reflect.DeepEqual(nodes[1].(map[string]any)["depends_on"], []any{"a"}) {
			t.Errorf("the stored workflow changed: %v", got.AppConfig)
		}
	}

	t.Run("update to a cycle is refused and the stored workflow is untouched", func(t *testing.T) {
		_, err := svc.Update(t.Context(), stored.ID, UpdateWorkflowRequest{Name: "flow", Description: description, AppConfig: cyclic}, "u")
		requireRefused(t, err)
		requireUnchanged(t)
	})

	t.Run("create of a cycle under an existing name is refused and the stored workflow is untouched", func(t *testing.T) {
		_, err := svc.Create(t.Context(), CreateWorkflowRequest{Name: "flow", Description: description, AppConfig: cyclic}, "u")
		requireRefused(t, err)
		requireUnchanged(t)
	})
}
