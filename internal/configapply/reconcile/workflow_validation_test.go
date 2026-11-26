// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func workflowWithNodes(nodes []map[string]any) map[string]any {
	rawNodes := make([]any, 0, len(nodes))
	for _, n := range nodes {
		rawNodes = append(rawNodes, n)
	}
	return map[string]any{
		"version":        "1.0.0",
		"nodes":          rawNodes,
		"output_mapping": map[string]any{"result": "{{step1.output.text}}"},
	}
}

func TestPlanWorkflows_ValidateAgentDangling(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("text-chain", workflowWithNodes([]map[string]any{
			{"id": "step1", "type": "agent", "agent_name": "uppercaser"},
			{"id": "step2", "type": "agent", "agent_name": "typo-here", "depends_on": []any{"step1"}},
		})),
	}

	rejecting := func(name string) error {
		if name == "uppercaser" {
			return nil
		}
		return fmt.Errorf("agent %q not found on platform and not declared in the desired manifest", name)
	}
	hooks := translate.WorkflowHooks{ValidateAgent: rejecting}

	_, err := planWorkflows(desired, nil, hooks)
	if err == nil {
		t.Fatal("expected dangling agent_name to fail plan, got nil")
	}
	for _, want := range []string{"text-chain", "step2", "typo-here", "type=agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q; got: %s", want, err.Error())
		}
	}
}

func TestPlanWorkflows_ValidateWorkflowDangling(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("orchestrator-workflow", workflowWithNodes([]map[string]any{
			{"id": "delegate", "type": "workflow", "workflow_name": "missing-sub"},
		})),
	}

	hooks := translate.WorkflowHooks{
		ValidateWorkflow: func(name string) error {
			return fmt.Errorf("workflow %q not found on platform and not declared in the desired manifest", name)
		},
	}

	_, err := planWorkflows(desired, nil, hooks)
	if err == nil {
		t.Fatal("expected dangling workflow_name to fail plan, got nil")
	}
	for _, want := range []string{"orchestrator-workflow", "delegate", "missing-sub", "type=workflow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q; got: %s", want, err.Error())
		}
	}
}

func TestPlanWorkflows_ValidateAgentAccepts(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("text-chain", workflowWithNodes([]map[string]any{
			{"id": "step1", "type": "agent", "agent_name": "uppercaser"},
			{"id": "step2", "type": "agent", "agent_name": "reverser", "depends_on": []any{"step1"}},
		})),
	}

	known := map[string]struct{}{"uppercaser": {}, "reverser": {}}
	hooks := translate.WorkflowHooks{
		ValidateAgent: func(name string) error {
			if _, ok := known[name]; ok {
				return nil
			}
			return fmt.Errorf("agent %q not found", name)
		},
	}

	plan, err := planWorkflows(desired, nil, hooks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "text-chain" {
		t.Errorf("expected one Create for text-chain, got %+v", plan.Creates)
	}
}

func TestPlanWorkflows_ValidateAcceptsBrokerInstanceName(t *testing.T) {
	const brokerName = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("text-chain", workflowWithNodes([]map[string]any{
			{"id": "step1", "type": "agent", "agent_name": brokerName},
		})),
	}

	accept := map[string]struct{}{
		"uppercaser": {},
		brokerName:   {},
	}
	hooks := translate.WorkflowHooks{
		ValidateAgent: func(name string) error {
			if _, ok := accept[name]; ok {
				return nil
			}
			return fmt.Errorf("agent %q not found", name)
		},
	}

	if _, err := planWorkflows(desired, nil, hooks); err != nil {
		t.Fatalf("broker-id agent_name must be accepted, got error: %v", err)
	}
}

func TestPlanWorkflows_ValidateRejectsNonStringAgentName(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("text-chain", workflowWithNodes([]map[string]any{
			{"id": "step1", "type": "agent", "agent_name": map[string]any{"from": "parent"}},
		})),
	}
	hooks := translate.WorkflowHooks{
		ValidateAgent: func(name string) error { return nil },
	}

	_, err := planWorkflows(desired, nil, hooks)
	if err == nil {
		t.Fatal("expected non-string agent_name to fail plan, got nil")
	}
	if !strings.Contains(err.Error(), "agent_name must be a string") {
		t.Errorf("error must surface the type mismatch; got: %s", err.Error())
	}
}

func TestPlanWorkflows_NoHooksSkipsValidation(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("text-chain", workflowWithNodes([]map[string]any{
			{"id": "step1", "type": "agent", "agent_name": "definitely-not-real"},
		})),
	}

	if _, err := planWorkflows(desired, nil, translate.WorkflowHooks{}); err != nil {
		t.Fatalf("zero hooks must skip validation, got error: %v", err)
	}
}

func TestPlanWorkflows_ValidateConnectorDangling(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("mail-chain", workflowWithNodes([]map[string]any{
			{"id": "send", "type": "tool", "connector": "ghost-connector"},
		})),
	}
	hooks := translate.WorkflowHooks{
		ValidateConnector: func(name string) error {
			return fmt.Errorf("connector %q not found on platform and not declared in the desired manifest", name)
		},
	}

	_, err := planWorkflows(desired, nil, hooks)
	if err == nil {
		t.Fatal("expected dangling connector ref to fail plan, got nil")
	}
	for _, want := range []string{"mail-chain", "send", "ghost-connector"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q; got: %s", want, err.Error())
		}
	}
}

func TestPlanWorkflows_ValidateConnectorAccepts(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("mail-chain", workflowWithNodes([]map[string]any{
			{"id": "send", "type": "tool", "connector": "mailer", "tool": "send_email"},
		})),
	}
	hooks := translate.WorkflowHooks{
		ValidateConnector: func(name string) error {
			if name == "mailer" {
				return nil
			}
			return fmt.Errorf("connector %q not found", name)
		},
	}

	if _, err := planWorkflows(desired, nil, hooks); err != nil {
		t.Fatalf("valid connector ref must pass plan, got: %v", err)
	}
}

func TestPlanWorkflows_ConnectorRefsKeyRejected(t *testing.T) {
	spec := workflowWithNodes([]map[string]any{
		{"id": "send", "type": "tool", "connector": "mailer"},
	})
	spec["connector_refs"] = []any{"mailer"}
	desired := []resolve.ResolvedResource{resolvedWorkflow("mail-chain", spec)}
	hooks := translate.WorkflowHooks{
		ValidateConnector: func(string) error { return nil },
	}

	_, err := planWorkflows(desired, nil, hooks)
	if err == nil {
		t.Fatal("expected the retired connector_refs key to fail plan, got nil")
	}
	for _, want := range []string{"mail-chain", "connector_refs is no longer supported"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q; got: %s", want, err.Error())
		}
	}
}

func TestLazyConnectorValidator(t *testing.T) {
	var listCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+connectorsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		listCalls.Add(1)
		writeV2Page(w, []wire.ConnectorResponse{{ID: "11111111-1111-1111-1111-111111111111", Name: "live-conn", ConnectorType: "email", ConnectorSubtype: "smtp"}})
	})
	c := newV2PlatformClient(t, mux)

	validate := lazyConnectorValidator(t.Context(), c, []string{"desired-conn"})

	if err := validate("desired-conn"); err != nil {
		t.Fatalf("desired name must validate without a platform call: %v", err)
	}
	if listCalls.Load() != 0 {
		t.Fatalf("ListConnectors called %d times for a desired-only name, want 0", listCalls.Load())
	}

	if err := validate("live-conn"); err != nil {
		t.Fatalf("live name must validate: %v", err)
	}
	if err := validate("ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown name must fail with not-found, got %v", err)
	}
	if listCalls.Load() != 1 {
		t.Fatalf("ListConnectors called %d times, want exactly 1 (memoized)", listCalls.Load())
	}
}

func TestLazyConnectorValidator_FetchErrorSurfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+connectorsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusNotImplemented, "Connectors are not enabled", nil)
	})
	c := newV2PlatformClient(t, mux)

	validate := lazyConnectorValidator(t.Context(), c, nil)
	err := validate("any-conn")
	if err == nil || !strings.Contains(err.Error(), "list connectors for reference validation") {
		t.Fatalf("fetch failure must surface as a list error, got %v", err)
	}
}

func TestPlanWorkflows_NonStringNodeConnectorRejected(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("mail-chain", workflowWithNodes([]map[string]any{
			{"id": "send", "type": "tool", "connector": map[string]any{"from": "parent"}},
		})),
	}
	hooks := translate.WorkflowHooks{
		ValidateConnector: func(string) error { return nil },
	}

	_, err := planWorkflows(desired, nil, hooks)
	if err == nil || !strings.Contains(err.Error(), "connector must be a string") {
		t.Fatalf("expected non-string connector to fail plan, got %v", err)
	}
}

func TestPlanWorkflows_ValidateToolsetDangling(t *testing.T) {
	spec := workflowWithNodes([]map[string]any{
		{"id": "run", "type": "tool", "tool_name": "ghost-tools__run"},
	})
	spec["toolsets"] = []any{"ghost-tools"}
	desired := []resolve.ResolvedResource{resolvedWorkflow("tool-chain", spec)}
	hooks := translate.WorkflowHooks{
		ValidateToolset: func(name string) error {
			return fmt.Errorf("toolset %q not found on platform and not declared in the desired manifest", name)
		},
	}

	_, err := planWorkflows(desired, nil, hooks)
	if err == nil {
		t.Fatal("expected dangling toolset ref to fail plan, got nil")
	}
	for _, want := range []string{"tool-chain", "ghost-tools"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q; got: %s", want, err.Error())
		}
	}
}

func TestPlanWorkflows_ValidateToolsetAccepts(t *testing.T) {
	spec := workflowWithNodes([]map[string]any{
		{"id": "run", "type": "tool", "tool_name": "echo-tools__run"},
	})
	spec["toolsets"] = []any{"echo-tools"}
	desired := []resolve.ResolvedResource{resolvedWorkflow("tool-chain", spec)}
	hooks := translate.WorkflowHooks{
		ValidateToolset: func(name string) error {
			if name == "echo-tools" {
				return nil
			}
			return fmt.Errorf("toolset %q not found", name)
		},
	}

	if _, err := planWorkflows(desired, nil, hooks); err != nil {
		t.Fatalf("valid toolset ref must pass plan, got: %v", err)
	}
}

func TestLazyToolsetValidator_BuiltinsAndDesired(t *testing.T) {
	validate := lazyToolsetValidator(t.Context(), nil, []string{"my-desired-tools"})
	if err := validate("my-desired-tools"); err != nil {
		t.Errorf("desired toolset should validate without a fetch: %v", err)
	}
	if err := validate("builtin_artifact_tools"); err != nil {
		t.Errorf("built-in toolset should validate without a fetch: %v", err)
	}
	if err := validate("hil_tools"); err != nil {
		t.Errorf("built-in hil_tools should validate without a fetch: %v", err)
	}
}

func TestPlanWorkflows_ToolsetDuplicateRejected(t *testing.T) {
	spec := workflowWithNodes([]map[string]any{
		{"id": "run", "type": "tool", "tool_name": "echo-tools__run"},
	})
	spec["toolsets"] = []any{"echo-tools", "echo-tools"}
	desired := []resolve.ResolvedResource{resolvedWorkflow("tool-chain", spec)}
	hooks := translate.WorkflowHooks{ValidateToolset: func(string) error { return nil }}

	_, err := planWorkflows(desired, nil, hooks)
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate toolset name must fail plan like it fails apply, got: %v", err)
	}
}

func TestPlanWorkflows_ToolsetShapeErrors(t *testing.T) {
	hooks := translate.WorkflowHooks{ValidateToolset: func(string) error { return nil }}

	t.Run("not a list", func(t *testing.T) {
		spec := workflowWithNodes(nil)
		spec["toolsets"] = "echo-tools"
		_, err := planWorkflows([]resolve.ResolvedResource{resolvedWorkflow("bad-shape", spec)}, nil, hooks)
		if err == nil || !strings.Contains(err.Error(), "must be a list") {
			t.Fatalf("err = %v, want must-be-a-list error", err)
		}
	})

	t.Run("empty name", func(t *testing.T) {
		spec := workflowWithNodes(nil)
		spec["toolsets"] = []any{""}
		_, err := planWorkflows([]resolve.ResolvedResource{resolvedWorkflow("bad-name", spec)}, nil, hooks)
		if err == nil || !strings.Contains(err.Error(), "non-empty toolset name") {
			t.Fatalf("err = %v, want non-empty-name error", err)
		}
	})

	t.Run("nested in workflow body", func(t *testing.T) {
		spec := map[string]any{
			"workflow": map[string]any{
				"version":        "1.0.0",
				"nodes":          []any{},
				"output_mapping": map[string]any{"result": "x"},
				"toolsets":       []any{"nested-tools"},
			},
		}
		failing := translate.WorkflowHooks{ValidateToolset: func(name string) error {
			return fmt.Errorf("toolset %q not found", name)
		}}
		_, err := planWorkflows([]resolve.ResolvedResource{resolvedWorkflow("nested", spec)}, nil, failing)
		if err == nil || !strings.Contains(err.Error(), `toolset "nested-tools" not found`) {
			t.Fatalf("err = %v, want nested-body toolset validation error", err)
		}
	})
}

func TestLazyToolsetValidator_LiveFetch(t *testing.T) {
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ToolsetResponse{{Name: "live-tools"}})
	}))
	defer srv.Close()

	validate := lazyToolsetValidator(t.Context(), newClientAgainstServer(srv), nil)
	if err := validate("live-tools"); err != nil {
		t.Errorf("live toolset should validate via fetch: %v", err)
	}
	if err := validate("ghost-tools"); err == nil || !strings.Contains(err.Error(), "not found on platform") {
		t.Errorf("err = %v, want not-found error", err)
	}

	broken := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusInternalServerError, "boom", nil)
	}))
	defer broken.Close()
	validateBroken := lazyToolsetValidator(t.Context(), newClientAgainstServer(broken), nil)
	if err := validateBroken("anything"); err == nil || !strings.Contains(err.Error(), "list toolsets") {
		t.Errorf("err = %v, want fetch error", err)
	}
}
