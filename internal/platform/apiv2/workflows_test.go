// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func workflowAppConfig(agentName string) map[string]any {
	return map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{start.output}}"},
			"nodes":          []any{map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": agentName}},
		},
	}
}

func createWorkflow(api *entrypointAPI, name string) Workflow {
	api.t.Helper()
	rec := api.call(http.MethodPost, "/workflows", map[string]any{
		"name":        name,
		"description": "Workflow the test creates.",
		"appConfig":   workflowAppConfig("test"),
	})
	return expectCreated[Workflow](api.t, rec, "workflows")
}

func deployWorkflow(api *entrypointAPI, workflowID string) *httptest.ResponseRecorder {
	api.t.Helper()
	return api.call(http.MethodPost, "/workflowDeployments", map[string]any{"workflowId": workflowID, "action": "deploy"})
}

func TestWorkflows_Lifecycle(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createWorkflow(api, "lifecycle-workflow")
	if created.Type != "workflow" || created.AppConfig["agent_type"] != "workflow" {
		t.Errorf("created = %+v, want type workflow with the app config as sent", created)
	}

	got := api.call(http.MethodGet, "/workflows/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	raw := rawData(t, got)
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	expectAbsent(t, raw, "createdAt", "updatedAt")

	listed := decodeList[Workflow](t, api.call(http.MethodGet, "/workflows", nil))
	if !slices.ContainsFunc(listed.Data, func(w Workflow) bool { return w.ID == created.ID }) {
		t.Errorf("list is missing the created workflow: %+v", listed.Data)
	}

	replaced := api.call(http.MethodPut, "/workflows/"+created.ID, map[string]any{
		"name":        "replaced-workflow",
		"description": "Workflow the test replaced.",
		"appConfig":   workflowAppConfig("other"),
	})
	expectStatus(t, replaced, http.StatusOK)
	w := decodeData[Workflow](t, replaced)
	nodes, _ := w.AppConfig["workflow"].(map[string]any)["nodes"].([]any)
	if w.Name != "replaced-workflow" || len(nodes) != 1 || nodes[0].(map[string]any)["agent_name"] != "other" {
		t.Errorf("after PUT = %+v, want the new name and app config", w)
	}

	expectStatus(t, api.call(http.MethodDelete, "/workflows/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/workflows/"+created.ID, nil), http.StatusNotFound)
}

func TestWorkflows_NameTakenIs409(t *testing.T) {
	api, _ := newAWEAPI(t)
	createWorkflow(api, "taken-workflow")

	rec := api.call(http.MethodPost, "/workflows", map[string]any{
		"name": "taken-workflow", "description": "Second workflow with that name.", "appConfig": workflowAppConfig("test"),
	})
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)

	other := createWorkflow(api, "renamed-workflow")
	rec = api.call(http.MethodPut, "/workflows/"+other.ID, map[string]any{
		"name": "taken-workflow", "description": other.Description, "appConfig": workflowAppConfig("test"),
	})
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)
}

func TestWorkflows_RespelledNameTakenIs409(t *testing.T) {
	api, _ := newAWEAPI(t)
	createWorkflow(api, "Taken Workflow")
	other := createWorkflow(api, "renamed-workflow")

	for _, respelling := range []string{"taken workflow", "TakenWorkflow", "TAKEN WORKFLOW"} {
		rec := api.call(http.MethodPost, "/workflows", map[string]any{
			"name": respelling, "description": "Second workflow with that name.", "appConfig": workflowAppConfig("test"),
		})
		expectStatus(t, rec, http.StatusConflict)
		decodeError(t, rec)

		rec = api.call(http.MethodPut, "/workflows/"+other.ID, map[string]any{
			"name": respelling, "description": other.Description, "appConfig": workflowAppConfig("test"),
		})
		expectStatus(t, rec, http.StatusConflict)
		decodeError(t, rec)
	}
}

func TestWorkflows_Paging(t *testing.T) {
	api, _ := newAWEAPI(t)
	for _, name := range []string{"workflow-one", "workflow-two", "workflow-three"} {
		createWorkflow(api, name)
	}

	defaults := decodeList[Workflow](t, api.call(http.MethodGet, "/workflows", nil))
	if p := defaults.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != 3 {
		t.Errorf("default pagination = %+v, want page 1 of size %d with count 3", p, defaultPageSize)
	}
	second := decodeList[Workflow](t, api.call(http.MethodGet, "/workflows?pageNumber=2&pageSize=2", nil))
	if len(second.Data) != 1 || second.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 2 of size 2 = %d items, pagination %+v", len(second.Data), second.Meta.Pagination)
	}
	beyond := decodeList[Workflow](t, api.call(http.MethodGet, "/workflows?pageNumber=9", nil))
	if len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}
	expectDetail(t, api.call(http.MethodGet, "/workflows?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectStatus(t, api.call(http.MethodGet, "/workflows?pageNumber=ten", nil), http.StatusBadRequest)
}

func TestWorkflows_Validation(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createWorkflow(api, "validation-workflow")

	every := api.call(http.MethodPost, "/workflows", `{"name":"x","description":"short"}`)
	expectStatus(t, every, http.StatusUnprocessableEntity)
	details := decodeError(t, every).ValidationDetails
	for _, field := range []string{"name", "description", "appConfig"} {
		if len(details[field]) == 0 {
			t.Errorf("want every field problem at once, %q missing from %v", field, details)
		}
	}
	expectDetail(t, api.call(http.MethodPost, "/workflows", `{"name":"extra-field","workflow":{}}`), http.StatusBadRequest, "workflow")
	expectDetail(t, api.call(http.MethodPost, "/workflows", `{"appConfig":[]}`), http.StatusBadRequest, "appConfig")
	expectStatus(t, api.call(http.MethodPost, "/workflows", `{"name":`), http.StatusBadRequest)
	expectDetail(t, api.call(http.MethodPut, "/workflows/"+created.ID, `{"name":"no-config","description":"Workflow without its config."}`),
		http.StatusUnprocessableEntity, "appConfig")

	for _, path := range []string{"/workflows/nope", "/workflows/nope/configuration"} {
		expectDetail(t, api.call(http.MethodGet, path, nil), http.StatusBadRequest, "id")
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		expectStatus(t, api.call(method, "/workflows/"+missingID, nil), http.StatusNotFound)
	}
	expectStatus(t, api.call(http.MethodPut, "/workflows/"+missingID, map[string]any{
		"name": "missing-workflow", "description": "Workflow that is not there.", "appConfig": workflowAppConfig("test"),
	}), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, "/workflows/"+missingID+"/configuration", nil), http.StatusNotFound)
}

func TestWorkflows_Scopes(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createWorkflow(api, "scoped-workflow")

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/workflows", `{}`},
		{http.MethodPut, "/workflows/" + created.ID, `{}`},
		{http.MethodDelete, "/workflows/" + created.ID, nil},
		{http.MethodPost, "/workflowDeployments", `{}`},
		{http.MethodGet, "/workflowDeployments", nil},
	} {
		if rec := api.call(tc.method, tc.path, tc.body, rbac.ScopeWorkflowBuilderRead); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with only read = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
	expectStatus(t, api.call(http.MethodGet, "/workflows", nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, "/workflows/"+created.ID+"/configuration", nil, rbac.ScopeWorkflowBuilderRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/workflowDeployments", nil, rbac.ScopeDeploymentRead), http.StatusOK)
}

func TestWorkflows_Configuration(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createWorkflow(api, "configured-workflow")

	rec := api.call(http.MethodGet, "/workflows/"+created.ID+"/configuration", nil)
	expectStatus(t, rec, http.StatusOK)
	if file := decodeData[WorkflowConfiguration](t, rec).ConfigurationFile; !strings.Contains(file, "agent_name: test") {
		t.Errorf("configurationFile = %q, want the workflow's YAML", file)
	}
}

func TestWorkflowDeployments_Success(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createWorkflow(api, "deploy-workflow")

	rec := deployWorkflow(api, created.ID)
	deployment := expectCreated[WorkflowDeployment](t, rec, "workflowDeployments")
	if deployment.Type != "workflowDeployment" || deployment.Status != entplatform.DeployStatusSuccess || deployment.WorkflowID != created.ID {
		t.Errorf("deployment = %+v", deployment)
	}
	expectISOTimes(t, rawData(t, rec), "createdTime", "updatedTime")

	expectStatus(t, api.call(http.MethodGet, "/workflowDeployments/"+deployment.ID, nil), http.StatusOK)
	listed := decodeList[WorkflowDeployment](t, api.call(http.MethodGet, "/workflowDeployments", nil))
	if !slices.ContainsFunc(listed.Data, func(d WorkflowDeployment) bool { return d.ID == deployment.ID }) {
		t.Errorf("deployment list is missing %s: %+v", deployment.ID, listed.Data)
	}
	if after := decodeData[Workflow](t, api.call(http.MethodGet, "/workflows/"+created.ID, nil)); after.DeploymentStatus != "deployed" {
		t.Errorf("deploymentStatus = %q after a successful deploy", after.DeploymentStatus)
	}
}

func TestWorkflowDeployments_FailureIs502AndKeepsTheRecord(t *testing.T) {
	logs := captureLogs(t)
	api, awe := newAWEAPI(t)
	created := createWorkflow(api, "failing-workflow")
	awe.failAdds.Store(true)

	rec := deployWorkflow(api, created.ID)
	d := expectFailedDeployment[WorkflowDeployment](api, rec, msgWorkflowDeployFailed, "workflowDeployments")
	if d.Status != entplatform.DeployStatusFailed || d.WorkflowID != created.ID {
		t.Errorf("kept deployment = %+v, want failed", d)
	}
	expectFailure(t, d.Failure, "workflow", entplatform.DeployFailureRuntimeError)
	expectCauseLogged(t, logs, rec, d.Failure)
}

func TestWorkflowDeployments_RefusalIs422AndKeepsTheRecord(t *testing.T) {
	api, _ := newAWEAPI(t)
	appConfig := workflowAppConfig("test")
	appConfig["workflow"].(map[string]any)["nodes"] = []any{map[string]any{"id": "start", "type": "tool", "tool_name": "web_request"}}
	created := expectCreated[Workflow](t, api.call(http.MethodPost, "/workflows", map[string]any{
		"name": "refused-workflow", "description": "Workflow whose tool node has no source.", "appConfig": appConfig,
	}), "workflows")

	rec := deployWorkflow(api, created.ID)
	d := expectKeptDeployment[WorkflowDeployment](api, rec, http.StatusUnprocessableEntity, msgWorkflowDeployFailed, "workflowDeployments")
	if why := strings.Join(decodeError(t, rec).ValidationDetails["workflowId"], ""); !strings.Contains(why, "web_request") {
		t.Errorf("workflowId details = %q, want SAM's refusal naming the node's tool", why)
	}
	expectFailure(t, d.Failure, "workflow", entplatform.DeployFailureRejected)
}

func TestWorkflowDeployments_Validation(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createWorkflow(api, "validate-wf-deploy")

	expectDetail(t, api.call(http.MethodPost, "/workflowDeployments", map[string]any{"workflowId": created.ID, "action": "rollback"}),
		http.StatusUnprocessableEntity, "action")
	expectDetail(t, api.call(http.MethodPost, "/workflowDeployments", map[string]any{"workflowId": "nope", "action": "deploy"}),
		http.StatusUnprocessableEntity, "workflowId")
	expectDetail(t, api.call(http.MethodPost, "/workflowDeployments", `{"workflowId":"`+created.ID+`","action":"deploy","testMode":"yes"}`),
		http.StatusBadRequest, "testMode")
	expectStatus(t, deployWorkflow(api, missingID), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodGet, "/workflowDeployments/nope", nil), http.StatusBadRequest, "id")
	expectStatus(t, api.call(http.MethodGet, "/workflowDeployments/"+missingID, nil), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodGet, "/workflowDeployments?pageSize=0", nil), http.StatusUnprocessableEntity, "pageSize")
}

func TestWorkflowDeployments_ActionsRejectFieldsTheyDontTake(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createWorkflow(api, "stray-fields-workflow")
	deployed := decodeData[WorkflowDeployment](t, deployWorkflow(api, created.ID))
	expectActionsRejectFieldsTheyDontTake(t, api, "/workflowDeployments", "workflowId", created.ID, deployed.ID, "testMode")
}

func TestWorkflowDeployments_Rollback(t *testing.T) {
	api, awe := newAWEAPI(t)
	created := createWorkflow(api, "rollback-workflow")
	first := decodeData[WorkflowDeployment](t, deployWorkflow(api, created.ID))
	expectStatus(t, api.call(http.MethodPut, "/workflows/"+created.ID, map[string]any{
		"name": created.Name, "description": created.Description, "appConfig": workflowAppConfig("newer-agent"),
	}), http.StatusOK)
	second := decodeData[WorkflowDeployment](t, deployWorkflow(api, created.ID))
	if !strings.Contains(awe.lastAddBody(), "newer-agent") {
		t.Fatalf("the second deploy sent %s, want the newer config", awe.lastAddBody())
	}

	rollback := map[string]any{"workflowId": created.ID, "action": "update", "deploymentId": first.ID}
	rec := api.call(http.MethodPost, "/workflowDeployments", rollback)
	rolledBack := expectCreated[WorkflowDeployment](t, rec, "workflowDeployments")
	if rolledBack.ID == first.ID || rolledBack.Status != entplatform.DeployStatusSuccess || rolledBack.Action != "update" {
		t.Errorf("rollback = %+v, want a new successful update", rolledBack)
	}
	snapshot := func(id string) string {
		d, err := store.NewWorkflowDeploymentRepo(api.core.DB()).GetByID(t.Context(), id)
		if err != nil || d == nil || d.WorkflowConfigSnapshot == nil {
			t.Fatalf("stored deployment %s = %+v, %v", id, d, err)
		}
		return *d.WorkflowConfigSnapshot
	}
	if got := snapshot(rolledBack.ID); got != snapshot(first.ID) || got == snapshot(second.ID) {
		t.Errorf("the rollback ran with %q, want the first deployment's config", got)
	}
	if sent := awe.lastAddBody(); sent == "" || strings.Contains(sent, "newer-agent") {
		t.Errorf("the rollback sent %s, want the first deployment's config", sent)
	}

	other := decodeData[WorkflowDeployment](t, deployWorkflow(api, createWorkflow(api, "rollback-other").ID))
	refuse := func(body map[string]any, field string) {
		t.Helper()
		req := map[string]any{"workflowId": created.ID, "action": "deploy"}
		maps.Copy(req, body)
		expectDetail(t, api.call(http.MethodPost, "/workflowDeployments", req), http.StatusUnprocessableEntity, field)
	}
	refuse(map[string]any{"deploymentId": other.ID}, "deploymentId")
	refuse(map[string]any{"deploymentId": missingID}, "deploymentId")
	refuse(map[string]any{"deploymentId": first.ID, "action": "undeploy"}, "deploymentId")
	refuse(map[string]any{"deploymentId": first.ID, "testMode": true}, "testMode")

	awe.failAdds.Store(true)
	failed := expectFailedDeployment[WorkflowDeployment](api, api.call(http.MethodPost, "/workflowDeployments", rollback), msgWorkflowDeployFailed, "workflowDeployments")
	if failed.Status != entplatform.DeployStatusFailed {
		t.Errorf("failed rollback = %+v, want a failed record", failed)
	}
	refuse(map[string]any{"deploymentId": failed.ID}, "deploymentId")
}

func TestWorkflows_DeleteKeepsTheWorkflowWhenUndeployFails(t *testing.T) {
	api, awe := newAWEAPI(t)
	created := createWorkflow(api, "wf-undeploy-fails")
	expectCreated[WorkflowDeployment](t, deployWorkflow(api, created.ID), "workflowDeployments")
	awe.failRemoves.Store(true)

	rec := api.call(http.MethodDelete, "/workflows/"+created.ID, nil)
	expectStatus(t, rec, http.StatusBadGateway)
	if strings.Contains(rec.Body.String(), "instance failed to stop") {
		t.Errorf("the upstream error reached the response: %s", rec.Body.String())
	}
	expectStatus(t, api.call(http.MethodGet, "/workflows/"+created.ID, nil), http.StatusOK)

	awe.failRemoves.Store(false)
	before := awe.removes.Load()
	expectStatus(t, api.call(http.MethodDelete, "/workflows/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/workflows/"+created.ID, nil), http.StatusNotFound)
	if awe.removes.Load() == before {
		t.Error("the retried delete removed the record without undeploying the workflow")
	}
}

func TestWorkflows_AccessTakesEitherScope(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createWorkflow(api, "access-workflow")
	path := fmt.Sprintf("/workflows/%s/access", created.ID)

	for _, scope := range []string{rbac.ScopeRBACRead, rbac.ScopeWorkflowBuilderRead} {
		rec := api.call(http.MethodGet, path, nil, scope)
		expectStatus(t, rec, http.StatusOK)
		decodeList[UserAccess](t, rec)
	}
	expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, "/workflows/"+missingID+"/access", nil), http.StatusNotFound)
}
