// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type fakeCards map[string]*a2a.AgentCard

func (f fakeCards) Get(name string) *a2a.AgentCard { return f[name] }

func workflowCard(name string, inputSchema map[string]any) *a2a.AgentCard {
	extensions := []a2a.AgentExtension{{URI: a2a.ExtensionURIAgentType, Params: map[string]any{"type": "workflow"}}}
	if inputSchema != nil {
		extensions = append(extensions, a2a.AgentExtension{URI: a2a.ExtensionURISchemas, Params: map[string]any{"input_schema": inputSchema}})
	}
	return &a2a.AgentCard{Name: name, Capabilities: a2a.AgentCapabilities{Extensions: extensions}}
}

func deployedSchema() map[string]any {
	return map[string]any{"type": "object", "required": []any{"deployed"}}
}

func TestEvalWorkflowResolver(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewWorkflowRepo(db)
	ctx := t.Context()
	wf := &store.Workflow{Name: "Order Handler", AppConfig: `{"workflow":{"input_schema":{"type":"object","required":["stored"]}}}`, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	instance := a2a.InstanceName(a2a.KindWorkflow, wf.ID)

	cases := []struct {
		name         string
		cards        fakeCards
		lookup       string
		wantName     string
		wantInstance string
		wantRequired string
	}{
		{"by name, not deployed: the stored schema", fakeCards{}, "Order Handler", "Order Handler", instance, "stored"},
		{"by name with other spacing and case", fakeCards{}, "orderhandler", "Order Handler", instance, "stored"},
		{"by instance name", fakeCards{}, instance, "Order Handler", instance, "stored"},
		{"deployed: the card's schema wins", fakeCards{instance: workflowCard(instance, deployedSchema())}, "Order Handler", "Order Handler", instance, "deployed"},
		{"a workflow deployed from YAML only", fakeCards{"yaml_flow": workflowCard("yaml_flow", deployedSchema())}, "yaml_flow", "yaml_flow", "yaml_flow", "deployed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &evalWorkflowResolver{workflows: repo, cards: tc.cards}

			target, err := r.ResolveWorkflow(ctx, tc.lookup)

			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if target == nil {
				t.Fatal("resolved to nothing")
			}
			if target.Name != tc.wantName {
				t.Errorf("name = %q, want %q", target.Name, tc.wantName)
			}
			if target.InstanceName != tc.wantInstance {
				t.Errorf("instance = %q, want %q", target.InstanceName, tc.wantInstance)
			}
			required, _ := target.InputSchema["required"].([]any)
			if len(required) != 1 || required[0] != tc.wantRequired {
				t.Errorf("schema = %v, want the %s one", target.InputSchema, tc.wantRequired)
			}
		})
	}

	t.Run("an agent card is not a workflow", func(t *testing.T) {
		agent := &a2a.AgentCard{Name: "OrderDesk"}
		r := &evalWorkflowResolver{workflows: repo, cards: fakeCards{"OrderDesk": agent}}
		if target, err := r.ResolveWorkflow(ctx, "OrderDesk"); err != nil || target != nil {
			t.Errorf("resolve an agent = %+v, %v; want nothing", target, err)
		}
	})
	t.Run("an unknown name", func(t *testing.T) {
		r := &evalWorkflowResolver{workflows: repo, cards: fakeCards{}}
		if target, err := r.ResolveWorkflow(ctx, "nope"); err != nil || target != nil {
			t.Errorf("resolve = %+v, %v; want nothing", target, err)
		}
	})
}

func TestAppConfigInputSchema(t *testing.T) {
	cases := map[string]bool{
		`{"workflow":{"input_schema":{"type":"object"}}}`: true,
		`{"workflow":{"inputSchema":{"type":"object"}}}`:  true,
		`{"input_schema":{"type":"object"}}`:              true,
		`{"workflow":{"nodes":[]}}`:                       false,
		`not json`:                                        false,
	}
	for appConfig, want := range cases {
		if got := appConfigInputSchema(appConfig) != nil; got != want {
			t.Errorf("appConfigInputSchema(%s) found = %v, want %v", appConfig, got, want)
		}
	}
}

type fakePrincipalResolver struct {
	scopes     map[string][]string
	rebuilt    map[string][]string
	err        error
	rebuildErr error
	rebuilds   int
}

func (f *fakePrincipalResolver) Lookup(userID, _ string, _ map[string]any) (rbac.Resolution, error) {
	if f.rebuilds > 0 && f.rebuildErr == nil {
		return rbac.Resolution{Scopes: f.rebuilt[userID]}, f.err
	}
	return rbac.Resolution{Scopes: f.scopes[userID]}, f.err
}

func (f *fakePrincipalResolver) Rebuild(context.Context) error {
	f.rebuilds++
	return f.rebuildErr
}

func TestEvalWorkflowInvokeChecker(t *testing.T) {
	const instance = "workflow_0f9a1b2c3d4e5f60718293a4b5c6d7e8"
	grants := map[string][]string{
		"system:eval":       {"workflow:*:invoke"},
		"system:agent-only": {"agent:*:invoke"},
		"system:one":        {rbac.WorkflowInvokeScopeForInstance(instance)},
	}
	checker := &evalWorkflowInvokeChecker{resolver: &fakePrincipalResolver{scopes: grants, rebuilt: grants}}
	cases := map[string]bool{"system:eval": true, "system:agent-only": false, "system:one": true, "system:nobody": false}
	for principal, want := range cases {
		got, err := checker.CanInvokeWorkflow(t.Context(), principal, instance)
		if err != nil || got != want {
			t.Errorf("%s: allowed = %v (%v), want %v", principal, got, err, want)
		}
	}

	if newEvalWorkflowInvokeChecker(nil, nil) != nil {
		t.Error("no composer must mean no checker, not a checker around a nil composer")
	}
}

func TestEvalWorkflowInvokeChecker_RefreshesBeforeRefusing(t *testing.T) {
	const instance = "workflow_0f9a1b2c3d4e5f60718293a4b5c6d7e8"
	granted := map[string][]string{"system:orders-bot": {"workflow:*:invoke"}}

	t.Run("a grant this replica sees only after a rebuild is honoured", func(t *testing.T) {
		resolver := &fakePrincipalResolver{rebuilt: granted}
		checker := &evalWorkflowInvokeChecker{resolver: resolver}

		allowed, err := checker.CanInvokeWorkflow(t.Context(), "system:orders-bot", instance)

		if err != nil || !allowed {
			t.Errorf("allowed = %v (%v), want the rebuilt grant honoured", allowed, err)
		}
		if resolver.rebuilds != 1 {
			t.Errorf("rebuilds = %d, want 1", resolver.rebuilds)
		}
	})

	t.Run("a grant already visible needs no rebuild", func(t *testing.T) {
		resolver := &fakePrincipalResolver{scopes: granted}
		checker := &evalWorkflowInvokeChecker{resolver: resolver}

		if allowed, err := checker.CanInvokeWorkflow(t.Context(), "system:orders-bot", instance); err != nil || !allowed {
			t.Errorf("allowed = %v (%v), want true", allowed, err)
		}
		if resolver.rebuilds != 0 {
			t.Errorf("rebuilds = %d, want none", resolver.rebuilds)
		}
	})

	t.Run("a failed rebuild refuses against the stale snapshot", func(t *testing.T) {
		resolver := &fakePrincipalResolver{rebuilt: granted, rebuildErr: errors.New("database down")}
		checker := &evalWorkflowInvokeChecker{resolver: resolver}

		if allowed, err := checker.CanInvokeWorkflow(t.Context(), "system:orders-bot", instance); err != nil || allowed {
			t.Errorf("allowed = %v (%v), want a refusal, not an error", allowed, err)
		}
	})

	t.Run("a lookup failure is unavailable, not a refusal", func(t *testing.T) {
		checker := &evalWorkflowInvokeChecker{resolver: &fakePrincipalResolver{err: errors.New("rbac composer: not initialized")}}

		_, err := checker.CanInvokeWorkflow(t.Context(), "system:eval", instance)

		if _, ok := errors.AsType[*UnavailableError](err); !ok {
			t.Errorf("error = %v, want an UnavailableError", err)
		}
	})
}
