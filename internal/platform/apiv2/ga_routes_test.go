// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"

	"github.com/stretchr/testify/assert"
)

var gaRoutes = []string{
	"DELETE /agents/{id}",
	"DELETE /apiKeys/{id}",
	"DELETE /budgetAssignments/{id}",
	"DELETE /budgets/{id}",
	"DELETE /connectors/{id}",
	"DELETE /entrypoints/{id}",
	"DELETE /evaluations/datasets/{id}",
	"DELETE /evaluations/evaluators/{id}",
	"DELETE /evaluations/examples/{id}",
	"DELETE /evaluations/experiments/{id}",
	"DELETE /linkAgents/{id}",
	"DELETE /models/{id}",
	"DELETE /profileProvider",
	"DELETE /rbac/claimMappings/{id}",
	"DELETE /rbac/grants/{id}",
	"DELETE /rbac/roles/{id}",
	"DELETE /rbac/serviceAccounts/{id}",
	"DELETE /rbac/systemUsers/{id}",
	"DELETE /skills/{id}",
	"DELETE /toolsets/{id}",
	"DELETE /workflows/{id}",
	"GET /agentDeployments",
	"GET /agentDeployments/{id}",
	"GET /agents",
	"GET /agents/{id}",
	"GET /apiKeys",
	"GET /apiKeys/{id}",
	"GET /budgetAssignments",
	"GET /budgetAssignments/{id}",
	"GET /budgets",
	"GET /budgets/{id}",
	"GET /connectors",
	"GET /connectors/{id}",
	"GET /connectors/{id}/resource",
	"GET /entrypointDeployments",
	"GET /entrypointDeployments/{id}",
	"GET /entrypoints",
	"GET /entrypoints/{id}",
	"GET /evaluations/datasets",
	"GET /evaluations/datasets/{datasetId}/examples",
	"GET /evaluations/datasets/{id}",
	"GET /evaluations/evaluators",
	"GET /evaluations/evaluators/{id}",
	"GET /evaluations/examples/{id}",
	"GET /evaluations/experiments",
	"GET /evaluations/experiments/{experimentId}/operations/{id}",
	"GET /evaluations/experiments/{experimentId}/runs",
	"GET /evaluations/experiments/{id}",
	"GET /evaluations/runGroups/{id}",
	"GET /evaluations/runResults/{runResultId}/executionData",
	"GET /evaluations/runResults/{runResultId}/taskEvents",
	"GET /evaluations/runs/{id}",
	"GET /evaluations/runs/{runId}/results",
	"GET /health",
	"GET /linkAgents",
	"GET /linkAgents/{id}",
	"GET /models",
	"GET /models/{id}",
	"GET /models/{id}/dependents",
	"GET /profileProvider",
	"GET /rbac/claimMappings",
	"GET /rbac/claimMappings/{id}",
	"GET /rbac/defaultRoles",
	"GET /rbac/grants",
	"GET /rbac/grants/{id}",
	"GET /rbac/roles",
	"GET /rbac/roles/{id}",
	"GET /rbac/serviceAccounts",
	"GET /rbac/serviceAccounts/{id}",
	"GET /rbac/systemUsers",
	"GET /rbac/systemUsers/{id}",
	"GET /serverCapabilities",
	"GET /skills",
	"GET /skills/{id}",
	"GET /skills/{id}/bundle",
	"GET /skills/{id}/config",
	"GET /toolsetBuildTarget",
	"GET /toolsetUploads/{id}",
	"GET /toolsets",
	"GET /toolsets/{id}",
	"GET /toolsets/{id}/bundle",
	"GET /toolsets/{id}/config",
	"GET /version",
	"GET /webuiSettings",
	"GET /workflowDeployments",
	"GET /workflowDeployments/{id}",
	"GET /workflows",
	"GET /workflows/{id}",
	"PATCH /budgetAssignments/{id}",
	"PATCH /budgets/{id}",
	"PATCH /connectors/{id}",
	"PATCH /entrypoints/{id}",
	"PATCH /evaluations/datasets/{id}",
	"PATCH /evaluations/evaluators/{id}",
	"PATCH /evaluations/examples/{id}",
	"PATCH /evaluations/experiments/{id}",
	"PATCH /models/{id}",
	"PATCH /rbac/grants/{id}",
	"PATCH /skills/{id}",
	"PATCH /toolsets/{id}",
	"PATCH /toolsets/{id}/config",
	"POST /agentDeployments",
	"POST /agents",
	"POST /apiKeys",
	"POST /budgetAssignments",
	"POST /budgets",
	"POST /connectors",
	"POST /entrypointDeployments",
	"POST /entrypoints",
	"POST /evaluations/datasets",
	"POST /evaluations/datasets/{datasetId}/examples",
	"POST /evaluations/evaluators",
	"POST /evaluations/experiments",
	"POST /linkAgents",
	"POST /models",
	"POST /rbac/claimMappings",
	"POST /rbac/grants",
	"POST /rbac/roles",
	"POST /rbac/serviceAccounts",
	"POST /rbac/systemUsers",
	"POST /skills",
	"POST /toolsetUploads",
	"POST /toolsets",
	"POST /workflowDeployments",
	"POST /workflows",
	"PUT /agents/{id}",
	"PUT /agents/{id}/secretsDiff",
	"PUT /apiKeys/{id}",
	"PUT /budgets/{id}/makeDefault",
	"PUT /connectors/{id}/resource",
	"PUT /connectors/{id}/secretsDiff",
	"PUT /entrypoints/{id}/secretsDiff",
	"PUT /evaluations/experiments/{experimentId}/trigger",
	"PUT /evaluations/runs/{id}/cancel",
	"PUT /linkAgents/{id}",
	"PUT /linkAgents/{id}/secretsDiff",
	"PUT /models/{id}/secretsDiff",
	"PUT /profileProvider",
	"PUT /rbac/claimMappings/{id}",
	"PUT /rbac/defaultRoles",
	"PUT /rbac/roles/{id}",
	"PUT /rbac/serviceAccounts/{id}",
	"PUT /rbac/systemUsers/{id}",
	"PUT /skills/{id}/bundle",
	"PUT /toolsetUploads/{id}/complete",
	"PUT /toolsetUploads/{toolsetUploadId}/chunks/{id}",
	"PUT /toolsets/{id}/bundle",
	"PUT /toolsets/{id}/secretsDiff",
	"PUT /webuiSettings",
	"PUT /workflows/{id}",
}

func TestPublishedRoutesAreTheGARoutes(t *testing.T) {
	var published []string
	for _, rt := range specRoutes() {
		if rt.Published {
			published = append(published, rt.Method+" "+rt.Path)
		}
	}
	slices.Sort(published)

	want := slices.Clone(gaRoutes)
	slices.Sort(want)

	assert.Equal(t, want, published, "a route is published without being on the GA list, or on the list without being published")
}

func TestPublishedRoutesAreOpenToAPIKeys(t *testing.T) {
	for _, rt := range specRoutes() {
		op := rt.Method + " " + rt.Path
		switch {
		case rt.Published && rt.Gate != GatePublic && !rt.AllowAPIKey:
			t.Errorf("%s is published but closed to API keys", op)
		case rt.Gate == GatePublic && rt.AllowAPIKey:
			t.Errorf("%s is GatePublic, so it resolves no key and AllowAPIKey does nothing", op)
		case rt.AllowAPIKey && !rt.Published && !openToKeysButUnpublished[op]:
			t.Errorf("%s is open to API keys but unpublished, so a key still gets 403", op)
		}
	}
}

var openToKeysButUnpublished = map[string]bool{
	"GET /users/{userId}/apiKeys": true,
}

var pathParameter = regexp.MustCompile(`\{[^}]+\}`)

const keyAllowlistRefusal = "API keys can only call the Platform configuration API"

func TestPublishedKeyRoutesRefuseAKeyWithoutTheRightScope(t *testing.T) {
	core, _ := newTestCore(t)
	keys := map[string]*auth.Subject{
		"no scope":        holdingKey(),
		"unrelated scope": holdingKey("unrelated:_:read"),
	}
	checked := 0
	for _, rt := range specRoutes() {
		if !rt.AllowAPIKey || !rt.Published {
			continue
		}
		checked++
		for name, key := range keys {
			t.Run(rt.Method+" "+rt.Path+" with "+name, func(t *testing.T) {
				target := platpkg.APIV2Prefix + pathParameter.ReplaceAllString(rt.Path, "0192f1e4-7a3b-7c2d-9e8f-123456789abc")
				req := httptest.NewRequest(rt.Method, target, nil)
				req = req.WithContext(auth.ContextWithSubject(req.Context(), key))
				rec := serveBare(t, core, req)

				if strings.Contains(rec.Body.String(), keyAllowlistRefusal) {
					t.Fatalf("refused by the key allowlist, want the route's own gate: %s", rec.Body.String())
				}
				if rt.Gate == GateSignedIn {
					if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
						t.Errorf("status = %d, want the signed-in route to serve a key", rec.Code)
					}
					return
				}
				if rec.Code != http.StatusForbidden {
					t.Errorf("status = %d, want 403 from the route's own gate; body=%s", rec.Code, rec.Body.String())
				}
			})
		}
	}
	if checked == 0 {
		t.Fatal("no published route is open to API keys, so the check proved nothing")
	}
}
