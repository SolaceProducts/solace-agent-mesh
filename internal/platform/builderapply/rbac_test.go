// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func planFor(t *testing.T, svc *Service, comps ...BuildComponentInput) []kindPlan {
	t.Helper()
	plans, _, err := svc.Plan(t.Context(), ApplyBuildRequest{Components: comps}, testActor)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return plans
}

func identityWith(scopes ...string) *auth.Subject {
	return auth.NewTestSubject(auth.SubjectFields{UserID: "u-1", Email: "t@example.com", Authenticated: true, Scopes: scopes})
}

func TestAuthorizePlan_UpdateNeedsUpdateScope(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind        string
		comp        func(string) BuildComponentInput
		name        string
		createScope string
		updateScope string
	}{
		{kindConnector, connectorComponent, "scoped-connector", rbac.ScopeConnectorCreate, rbac.ScopeConnectorUpdate},
		{kindAgent, agentComponent, "scoped_agent", rbac.ScopeAgentBuilderCreate, rbac.ScopeAgentBuilderUpdate},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()

			seed := tc.comp(tc.name)
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{seed}}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}

			edited := tc.comp(tc.name)
			edited.Content = editedContentFor(t, tc.kind, tc.name)
			plans := planFor(t, svc, edited)

			var updates int
			for _, kp := range plans {
				updates += len(kp.plan.Updates)
			}
			if updates == 0 {
				t.Fatalf("the fixture did not produce an update; the denial below would pass vacuously")
			}

			if err := authorizePlan(identityWith(tc.createScope), plans); err == nil {
				t.Errorf("a caller holding only %s was allowed to update an existing %s", tc.createScope, tc.kind)
			}
			if err := authorizePlan(identityWith(tc.createScope, tc.updateScope), plans); err != nil {
				t.Errorf("holding both scopes should authorize the update, got: %v", err)
			}
		})
	}
}

func editedContentFor(t *testing.T, kind, name string) string {
	t.Helper()
	switch kind {
	case kindConnector:
		return `{"name":"` + name + `","description":"A connector used in tests.","type":"sql","subtype":"postgres","values":{"hostname":"edited"}}`
	case kindAgent:
		return `
apps:
  - name: ` + name + `
    app_config:
      agent_name: ` + name + `
      instruction: An edited instruction so this plans as an update.
      tools:
        - tool_type: builtin-group
          group_name: artifact_management
      agent_card:
        description: "An agent created by the plan integration test."
`
	}
	t.Fatalf("no edited fixture for kind %q", kind)
	return ""
}

func TestAuthorizePlan_NamesEveryMissingScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	plans := planFor(t, svc, connectorComponent("c1"), agentComponent("a1"))

	err := authorizePlan(identityWith(), plans)
	if err == nil {
		t.Fatal("expected a denial with no scopes held")
	}
	for _, want := range []string{rbac.ScopeConnectorCreate, rbac.ScopeAgentBuilderCreate} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("denial %q should name %s", err.Error(), want)
		}
	}
}

func TestAuthorizePlan_UnchangedNeedsNoScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	comp := connectorComponent("steady-connector")

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	plans := planFor(t, svc, comp)

	for _, kp := range plans {
		if len(kp.plan.Creates) != 0 || len(kp.plan.Updates) != 0 {
			t.Fatalf("fixture is not all-unchanged: %+v", kp.plan)
		}
	}
	if err := authorizePlan(identityWith(), plans); err != nil {
		t.Errorf("an all-unchanged plan writes nothing and should need no scope, got: %v", err)
	}
}
