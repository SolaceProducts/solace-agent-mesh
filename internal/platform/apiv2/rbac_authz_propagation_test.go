// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type authzPropagationGWE struct {
	*entrypointAPI
	deps *entplatform.RouteDeps
}

func newAuthzPropagationGWEs(t *testing.T) (a, b *authzPropagationGWE) {
	t.Helper()
	shared := broker.NewDevBroker()
	if err := shared.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = shared.Disconnect(context.Background()) })
	wrapTestBroker = func(broker.Broker) broker.Broker { return shared }
	t.Cleanup(func() { wrapTestBroker = nil })
	dbURL := testTemplate.Clone(t)
	return startAuthzPropagationGWE(t, dbURL, "a"), startAuthzPropagationGWE(t, dbURL, "b")
}

func startAuthzPropagationGWE(t *testing.T, dbURL, name string) *authzPropagationGWE {
	t.Helper()
	core, deps := newTestCoreWithConfig(t, map[string]any{"database_url": dbURL})
	probe := &store.RBACRole{Name: "startup-probe-" + name, CreatedBy: "test", UpdatedBy: "test"}
	if err := store.NewRBACRepo(core.DB()).CreateRole(t.Context(), probe, nil, nil); err != nil {
		t.Fatalf("seed probe role: %v", err)
	}
	listening := false
	for _, svc := range deps.BackgroundServices {
		switch svc.(type) {
		case *platpkg.OutboxPoller, *entplatform.AuthzChangeListener:
			if err := svc.Start(t.Context()); err != nil {
				t.Fatalf("start %T: %v", svc, err)
			}
			t.Cleanup(func() { _ = svc.Stop(context.Background()) })
			_, isListener := svc.(*entplatform.AuthzChangeListener)
			listening = listening || isListener
		}
	}
	if !listening {
		t.Fatal("the platform wired no authz change listener")
	}
	authzPropagationWaitFor(t, "the startup rebuild", func() bool {
		_, err := deps.RBACComposer.ScopesForRoleIDs([]string{probe.ID})
		return err == nil
	})
	return &authzPropagationGWE{entrypointAPI: &entrypointAPI{apiClient: newAPIClient(t, core)}, deps: deps}
}

func authzPropagationWaitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func authzPropagationHasRole(t *testing.T, gwe *authzPropagationGWE, sub, userID, role string) bool {
	t.Helper()
	resolution, err := gwe.deps.AuthzLookupHandler.ResolveOwnerScopes(sub, userID)
	if err != nil {
		t.Fatalf("resolve owner scopes: %v", err)
	}
	return slices.Contains(resolution.Roles, role)
}

func TestRBACRoles_CreatedOnOneGWEReachesTheOtherGWEsRoleGraph(t *testing.T) {
	gweA, gweB := newAuthzPropagationGWEs(t)

	created := expectCreated[Role](t, gweA.call(http.MethodPost, "/rbac/roles", map[string]any{
		"name": "incident_responder", "scopes": []string{rbac.ScopeAgentBuilderRead},
	}), "rbac/roles")

	authzPropagationWaitFor(t, "the role created on GWE A to reach GWE B's role graph", func() bool {
		_, err := gweB.deps.RBACComposer.ScopesForRoleIDs([]string{created.ID})
		return err == nil
	})
}

func TestUsers_IdentityClaimsRevokeOnOneGWEClearsTheOtherGWEsCachedScopes(t *testing.T) {
	gweA, gweB := newAuthzPropagationGWEs(t)
	userID := gweA.seedUser(userSeed{sub: "carol", email: "carol@example.org"})
	authzPropagationHasRole(t, gweB, "carol", userID, "responder")

	repo := store.NewRBACRepo(gweA.core.DB())
	role := &store.RBACRole{Name: "responder", CreatedBy: "test", UpdatedBy: "test"}
	if err := repo.CreateRole(t.Context(), role, []string{rbac.ScopeAgentBuilderRead}, nil); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	if err := repo.AddUserRole(t.Context(), userID, role.ID, "test"); err != nil {
		t.Fatalf("grant role: %v", err)
	}
	if err := gweB.deps.RBACComposer.Rebuild(t.Context()); err != nil {
		t.Fatalf("rebuild GWE B's role graph: %v", err)
	}
	if authzPropagationHasRole(t, gweB, "carol", userID, "responder") {
		t.Fatal("GWE B answered from its role graph, not its cache; the test cannot see the cache clear")
	}

	expectStatus(t, gweA.call(http.MethodDelete, "/users/"+userID+"/identityClaims", nil), http.StatusNoContent)

	authzPropagationWaitFor(t, "the claim revoke on GWE A to clear GWE B's cached scopes", func() bool {
		return authzPropagationHasRole(t, gweB, "carol", userID, "responder")
	})
}
