// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type agentsDeleteGateDeployment struct {
	action string
	status string
}

type agentsDeleteGateKind struct {
	name         string
	collection   string
	historyTable string
	create       func(api *entrypointAPI, name string) string
	seed         func(api *entrypointAPI, id string, d agentsDeleteGateDeployment) error
}

func agentsDeleteGateSeedAgent(api *entrypointAPI, id string, d agentsDeleteGateDeployment) error {
	return store.NewAgentDeploymentRepo(api.core.DB()).Create(api.t.Context(),
		&store.AgentDeployment{AgentID: id, Action: d.action, Status: d.status, CreatedBy: "test-user"})
}

var agentsDeleteGateKinds = []agentsDeleteGateKind{
	{
		name:         "agent",
		collection:   "/agents/",
		historyTable: "agent_deployments",
		create:       func(api *entrypointAPI, name string) string { return createAgent(api, name).ID },
		seed:         agentsDeleteGateSeedAgent,
	},
	{
		name:         "remote agent",
		collection:   "/remoteAgents/",
		historyTable: "agent_deployments",
		create: func(api *entrypointAPI, name string) string {
			return createRemoteAgent(api, remoteAgentBody(name)).ID
		},
		seed: agentsDeleteGateSeedAgent,
	},
	{
		name:         "workflow",
		collection:   "/workflows/",
		historyTable: "workflow_deployments",
		create:       func(api *entrypointAPI, name string) string { return createWorkflow(api, name).ID },
		seed: func(api *entrypointAPI, id string, d agentsDeleteGateDeployment) error {
			return store.NewWorkflowDeploymentRepo(api.core.DB()).Create(api.t.Context(),
				&store.WorkflowDeployment{WorkflowID: id, Action: d.action, Status: d.status, CreatedBy: "test-user"})
		},
	},
}

func (k agentsDeleteGateKind) seedHistory(api *entrypointAPI, id string, history []agentsDeleteGateDeployment) {
	api.t.Helper()
	for _, d := range history {
		if err := k.seed(api, id, d); err != nil {
			api.t.Fatalf("seed %s deployment: %v", k.name, err)
		}
	}
}

func agentsDeleteGateRenameTable(api *entrypointAPI, from, to string) {
	api.t.Helper()
	if _, err := api.core.DB().ExecContext(api.t.Context(), "ALTER TABLE "+from+" RENAME TO "+to); err != nil {
		api.t.Fatalf("rename %s: %v", from, err)
	}
}

func TestDelete_WhenTheAWECannotStopInstances_ANotLiveOneIsStillDeleted(t *testing.T) {
	cases := []struct {
		name    string
		history []agentsDeleteGateDeployment
	}{
		{
			name: "successful undeploy skips undeploy",
			history: []agentsDeleteGateDeployment{
				{entplatform.DeployActionDeploy, entplatform.DeployStatusSuccess},
				{entplatform.DeployActionUndeploy, entplatform.DeployStatusSuccess},
			},
		},
		{name: "never deployed skips undeploy"},
	}
	shared, awe := newAWEAPI(t)
	awe.failRemoves.Store(true)
	for _, kind := range agentsDeleteGateKinds {
		for i, tc := range cases {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				api := shared.forRow(t)
				id := kind.create(api, fmt.Sprintf("%s gate %d", kind.name, i))
				kind.seedHistory(api, id, tc.history)

				expectStatus(t, api.call(http.MethodDelete, kind.collection+id, nil), http.StatusNoContent)
				expectStatus(t, api.call(http.MethodGet, kind.collection+id, nil), http.StatusNotFound)
			})
		}
	}
}

func TestRemoteAgents_DeleteUndeploysAPossiblyLiveProxyThenDeletesItAndItsMirror(t *testing.T) {
	success := agentsDeleteGateDeployment{entplatform.DeployActionDeploy, entplatform.DeployStatusSuccess}
	for _, tc := range []struct {
		name    string
		history []agentsDeleteGateDeployment
	}{
		{name: "deployed proxy", history: []agentsDeleteGateDeployment{success}},
		{name: "failed undeploy", history: []agentsDeleteGateDeployment{success, {entplatform.DeployActionUndeploy, entplatform.DeployStatusFailed}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, awe := newAWEAPI(t)
			id := createRemoteAgent(api, remoteAgentBody("remote-delete-routes")).ID
			agentsDeleteGateKinds[1].seedHistory(api, id, tc.history)

			expectStatus(t, api.call(http.MethodDelete, "/remoteAgents/"+id, nil), http.StatusNoContent)

			if got := awe.removes.Load(); got != 1 {
				t.Errorf("AWE instance removals = %d, want 1", got)
			}
			expectStatus(t, api.call(http.MethodGet, "/remoteAgents/"+id, nil), http.StatusNotFound)
			if mirror, _, _, _, _, err := store.NewAgentRepo(api.core.DB()).GetByID(t.Context(), id); err != nil || mirror != nil {
				t.Errorf("paired agents row = %v (err %v), want it deleted", mirror, err)
			}
		})
	}
}

func TestRemoteAgents_DeleteOfAStandardAgentIDIsNotFoundAndKeepsItsInstance(t *testing.T) {
	api, awe := newAWEAPI(t)
	id := createAgent(api, "standard-not-remote").ID
	agentsDeleteGateKinds[0].seedHistory(api, id, []agentsDeleteGateDeployment{{entplatform.DeployActionDeploy, entplatform.DeployStatusSuccess}})
	deployments := store.NewAgentDeploymentRepo(api.core.DB())
	seeded, err := deployments.GetLatestByAgentID(t.Context(), id)
	if err != nil || seeded == nil {
		t.Fatalf("latest deployment after seed: %v, %v", seeded, err)
	}
	awe.failRemoves.Store(true)

	expectStatus(t, api.call(http.MethodDelete, "/remoteAgents/"+id, nil), http.StatusNotFound)

	if got := awe.removes.Load(); got != 0 {
		t.Errorf("AWE instance removals = %d, want none", got)
	}
	expectStatus(t, api.call(http.MethodGet, "/agents/"+id, nil), http.StatusOK)
	latest, err := deployments.GetLatestByAgentID(t.Context(), id)
	if err != nil || latest == nil || latest.ID != seeded.ID {
		t.Errorf("latest deployment = %+v (err %v), want the seeded row %s untouched", latest, err, seeded.ID)
	}
}

func TestDelete_FailsClosedWhenTheDeploymentHistoryCannotBeRead(t *testing.T) {
	for _, kind := range agentsDeleteGateKinds {
		t.Run(kind.name, func(t *testing.T) {
			api, awe := newAWEAPI(t)
			id := kind.create(api, "delete-gate")
			agentsDeleteGateRenameTable(api, kind.historyTable, kind.historyTable+"_unavailable")

			rec := api.call(http.MethodDelete, kind.collection+id, nil)

			expectStatus(t, rec, http.StatusInternalServerError)
			decodeError(t, rec)
			if got := awe.removes.Load(); got != 0 {
				t.Errorf("AWE instance removals = %d, want none", got)
			}
			agentsDeleteGateRenameTable(api, kind.historyTable+"_unavailable", kind.historyTable)
			expectStatus(t, api.call(http.MethodGet, kind.collection+id, nil), http.StatusOK)
		})
	}
}

func TestRemoteAgents_DeleteWithoutADeploymentService(t *testing.T) {
	core, deps := newTestCore(t)
	awe := &fakeAWE{}
	startControlApp(t, core, "awe", awe.handle)
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}
	deleteWithoutBroker := remoteAgentDeleteWithoutBroker(t, deps, core)

	mayBeLive := createRemoteAgent(api, remoteAgentBody("remote-no-broker-live"))
	awe.failAdds.Store(true)
	expectFailedDeployment[AgentDeployment](api, deployAgent(api, mayBeLive.ID), msgAgentDeployFailed, "agentDeployments")
	expectStatus(t, deleteWithoutBroker(mayBeLive.ID), http.StatusBadGateway)
	expectStatus(t, api.call(http.MethodGet, "/remoteAgents/"+mayBeLive.ID, nil), http.StatusOK)

	neverDeployed := createRemoteAgent(api, remoteAgentBody("remote-no-broker-idle"))
	expectStatus(t, deleteWithoutBroker(neverDeployed.ID), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/remoteAgents/"+neverDeployed.ID, nil), http.StatusNotFound)
}

func remoteAgentDeleteWithoutBroker(t *testing.T, deps *entplatform.RouteDeps, core *platpkg.PlatformCore) func(id string) *httptest.ResponseRecorder {
	t.Helper()
	routes := remoteAgentRoutes(&entplatform.RouteDeps{A2AAgentSvc: deps.A2AAgentSvc}, core, nil)
	for _, route := range routes {
		if route.Method != http.MethodDelete || route.Path != remoteAgentPath {
			continue
		}
		return func(id string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodDelete, "/remoteAgents/"+id, nil)
			req.SetPathValue("id", id)
			rec := httptest.NewRecorder()
			route.Handler.ServeHTTP(rec, req)
			return rec
		}
	}
	t.Fatal("no remote agent delete route")
	return nil
}
