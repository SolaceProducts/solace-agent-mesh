// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func rbacScopeCatalogAPI(t *testing.T, resolver entplatform.ResourceResolver) *entrypointAPI {
	t.Helper()
	core, deps := newTestCoreWithConfig(t, nil)
	deps.RBACScopeResolvers[rbac.ScopeAgentInvokeAny] = resolver
	return &entrypointAPI{apiClient: newAPIClient(t, core)}
}

func rbacScopesByName(t *testing.T, rec *httptest.ResponseRecorder) map[string]ScopeCatalogEntry {
	t.Helper()
	expectStatus(t, rec, http.StatusOK)
	out := map[string]ScopeCatalogEntry{}
	for _, e := range decodeList[ScopeCatalogEntry](t, rec).Data {
		out[e.Scope] = e
	}
	return out
}

func TestScopes_CatalogListsDescribedTemplatesAndNoToolScopes(t *testing.T) {
	api := newRBACAPI(t)
	rec := api.call(http.MethodGet, "/rbac/scopes?pageSize=100", nil)
	expectStatus(t, rec, http.StatusOK)
	page := decodeList[ScopeCatalogEntry](t, rec)
	if page.Meta.Pagination.Count != len(page.Data) {
		t.Errorf("count %d, want %d: the catalog fits one page of 100", page.Meta.Pagination.Count, len(page.Data))
	}

	byScope := map[string]ScopeCatalogEntry{}
	for _, e := range page.Data {
		if strings.HasPrefix(e.Scope, "tool:") {
			t.Errorf("tool scope %q is in the catalog", e.Scope)
		}
		if e.Name == "" || e.Description == nil || *e.Description == "" {
			t.Errorf("entry %q lacks a name or description: %+v", e.Scope, e)
		}
		if e.ResourceName != nil {
			t.Errorf("entry %q names a resource without expand=resourceScopes", e.Scope)
		}
		byScope[e.Scope] = e
	}
	for _, want := range []string{rbac.ScopeAgentInvokeAny, rbac.ScopeWorkflowInvokeAny, rbac.ScopeEvaluationCreate} {
		if _, ok := byScope[want]; !ok {
			t.Errorf("catalog lacks %q", want)
		}
	}
}

func TestScopes_ExpandMaterializesOnlyResolvedTemplates(t *testing.T) {
	api := rbacScopeCatalogAPI(t, func(context.Context) ([]entplatform.ResourceInstance, error) {
		return []entplatform.ResourceInstance{
			{CanonicalName: "agent_019f", DisplayName: "HR Agent"},
			{CanonicalName: "agent_02aa", DisplayName: "Orchestrator"},
		}, nil
	})

	for scope, e := range rbacScopesByName(t, api.call(http.MethodGet, "/rbac/scopes?pageSize=100&category=agent", nil)) {
		if e.ResourceName != nil {
			t.Errorf("unexpanded catalog holds the instance scope %q", scope)
		}
	}

	expanded := rbacScopesByName(t, api.call(http.MethodGet, "/rbac/scopes?pageSize=100&expand=resourceScopes&search=invoke", nil))
	for scope, e := range expanded {
		if category, _, _ := strings.Cut(scope, ":"); e.ResourceName != nil && category != "agent" {
			t.Errorf("instance scope %q materialized with no resolver behind it", scope)
		}
	}
	hr, ok := expanded["agent:agent_019f:invoke"]
	if !ok {
		t.Fatalf("expanded catalog lacks agent:agent_019f:invoke: %v", expanded)
	}
	if hr.Name != "Invoke HR Agent" || hr.ResourceName == nil || *hr.ResourceName != "HR Agent" {
		t.Errorf("instance entry = %+v, want name 'Invoke HR Agent' and resourceName 'HR Agent'", hr)
	}
	for _, template := range []string{rbac.ScopeAgentInvokeAny, rbac.ScopeWorkflowInvokeAny} {
		if _, ok := expanded[template]; !ok {
			t.Errorf("expanded catalog lost the template %q", template)
		}
	}
}

func TestScopes_FeedbackScopesKeyOnThePlatformAgentID(t *testing.T) {
	api, _ := newAWEAPI(t)
	agent := createAgent(api, "HR Agent")

	expanded := rbacScopesByName(t, api.call(http.MethodGet, "/rbac/scopes?pageSize=100&expand=resourceScopes&search=feedback", nil))
	read := rbac.AgentReadFeedbackScope(agent.ID)
	e, ok := expanded[read]
	if !ok {
		t.Fatalf("expanded catalog lacks %q: %v", read, expanded)
	}
	if e.Name != "Read feedback of HR Agent" || e.ResourceName == nil || *e.ResourceName != "HR Agent" {
		t.Errorf("%q = %+v, want name 'Read feedback of HR Agent' and resourceName 'HR Agent'", read, e)
	}
	for scope, entry := range expanded {
		if entry.ResourceName != nil && *entry.ResourceName == "HR Agent" && strings.HasSuffix(scope, ":read_feedback") && scope != read {
			t.Errorf("feedback scope %q is not keyed on the platform id %s", scope, agent.ID)
		}
	}
	if _, ok := expanded[rbac.ScopeAgentReadFeedbackAny]; !ok {
		t.Errorf("expanded catalog lost the template %q", rbac.ScopeAgentReadFeedbackAny)
	}
}

func TestScopes_ExpandFailsWhenAResolverFails(t *testing.T) {
	api := rbacScopeCatalogAPI(t, func(context.Context) ([]entplatform.ResourceInstance, error) {
		return nil, errors.New("agent store unavailable")
	})
	rec := api.call(http.MethodGet, "/rbac/scopes?expand=resourceScopes", nil)
	expectStatus(t, rec, http.StatusInternalServerError)
	if strings.Contains(rec.Body.String(), "agent store unavailable") {
		t.Errorf("500 body leaks the cause: %s", rec.Body.String())
	}
}
