// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func usersAccess(t *testing.T, rec *httptest.ResponseRecorder) (people, serviceAccounts []string) {
	t.Helper()
	expectStatus(t, rec, http.StatusOK)
	page := decodeList[UserAccess](t, rec)
	if page.Meta.Pagination.Count != len(page.Data) {
		t.Errorf("count %d over %d rows", page.Meta.Pagination.Count, len(page.Data))
	}
	for _, u := range page.Data {
		if u.IsServiceAccount != u.IsSystemUser {
			t.Errorf("user %s: isServiceAccount %v differs from isSystemUser %v", u.UserID, u.IsServiceAccount, u.IsSystemUser)
		}
		if u.IsServiceAccount {
			serviceAccounts = append(serviceAccounts, u.UserID)
		} else {
			people = append(people, u.UserID)
		}
	}
	return people, serviceAccounts
}

func TestAccess_ListsWhoCanInvokeEachKind(t *testing.T) {
	api, _ := newAWEAPI(t)
	agent := createAgent(api, "accessagent")
	workflow := createWorkflow(api, "accessworkflow")
	roleFor := func(name, scope string) string {
		t.Helper()
		rec := api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": name, "scopes": []string{scope}})
		expectStatus(t, rec, http.StatusCreated)
		return decodeData[Role](t, rec).ID
	}
	agentRole := roleFor("agent-invoker", rbac.AgentInvokeScope(agent.ID))
	workflowRole := roleFor("workflow-invoker", rbac.WorkflowInvokeScope(workflow.ID))
	alice := api.seedUser(userSeed{sub: "s1", displayName: "Alice Ant", email: "alice@example.com"})
	bob := api.seedUser(userSeed{sub: "s2", displayName: "Bob Bee", email: "bob@example.com"})
	expectStatus(t, api.replaceUserRoleGrants(alice, agentRole), http.StatusOK)
	expectStatus(t, api.replaceUserRoleGrants(bob, workflowRole), http.StatusOK)

	rec := api.call(http.MethodPost, "/rbac/serviceAccounts", map[string]any{"name": "access-bot", "roleIds": []string{agentRole}})
	expectStatus(t, rec, http.StatusCreated)
	bot := decodeData[ServiceAccount](t, rec).ID

	agentPath := "/agents/" + agent.ID + "/access"
	people, serviceAccounts := usersAccess(t, api.call(http.MethodGet, agentPath+"?pageSize=20", nil))
	if !slices.Equal(people, []string{alice}) {
		t.Errorf("agent access people = %v, want only alice", people)
	}
	if !slices.Contains(serviceAccounts, bot) {
		t.Errorf("agent access service accounts = %v, want access-bot %s flagged isServiceAccount", serviceAccounts, bot)
	}
	if people, _ := usersAccess(t, api.call(http.MethodGet, "/workflows/"+workflow.ID+"/access?pageSize=20", nil)); !slices.Equal(people, []string{bob}) {
		t.Errorf("workflow access people = %v, want only bob: an agent grant does not reach a workflow", people)
	}

	rec = api.call(http.MethodGet, agentPath+"?search=zzz", nil)
	expectStatus(t, rec, http.StatusOK)
	if page := decodeList[UserAccess](t, rec); len(page.Data) != 0 || page.Meta.Pagination.Count != 0 {
		t.Errorf("search=zzz = %d rows of %d, want an empty page", len(page.Data), page.Meta.Pagination.Count)
	}
	body := api.call(http.MethodGet, agentPath, nil).Body.String()
	for _, field := range []string{`"roles"`, `"issuer"`, `"groups"`} {
		if strings.Contains(body, field) {
			t.Errorf("access body carries the directory field %s: %s", field, body)
		}
	}
}

func TestAccess_ScopeGateMatchesTheComponentKind(t *testing.T) {
	api, _ := newAWEAPI(t)
	agentPath := "/agents/" + createAgent(api, "gate-agent").ID + "/access"
	workflowPath := "/workflows/" + createWorkflow(api, "gate-workflow").ID + "/access"
	for _, c := range []struct{ path, scope string }{
		{agentPath, rbac.ScopeAgentBuilderUpdate},
		{agentPath, rbac.ScopeWorkflowBuilderRead},
		{workflowPath, rbac.ScopeWorkflowBuilderDelete},
	} {
		expectStatus(t, api.call(http.MethodGet, c.path, nil, c.scope), http.StatusForbidden)
	}
	expectStatus(t, api.anonymous().call(http.MethodGet, agentPath, nil), http.StatusUnauthorized)
}

func TestAccess_UnwiredResolverIs500WithoutTheCause(t *testing.T) {
	core, deps := newTestCoreWithConfig(t, nil)
	deps.UsersSvc.SetInvokeScopeResolvers(nil, nil)
	api := newAPIClient(t, core)

	rec := api.call(http.MethodGet, "/agents/"+missingID+"/access", nil)
	expectStatus(t, rec, http.StatusInternalServerError)
	if decodeError(t, rec).ErrorID == "" {
		t.Error("errorId is empty")
	}
	if strings.Contains(rec.Body.String(), "resolver not configured") {
		t.Errorf("500 body leaks the cause: %s", rec.Body.String())
	}
}
