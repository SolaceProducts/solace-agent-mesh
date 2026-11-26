// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type modelsRecordingAWE struct {
	fakeAWE

	mu      sync.Mutex
	removed []string
}

func (a *modelsRecordingAWE) handle(method string, path []string, body json.RawMessage) (any, error) {
	if method == "delete" {
		a.mu.Lock()
		a.removed = append(a.removed, strings.Join(path, "/"))
		a.mu.Unlock()
	}
	return a.fakeAWE.handle(method, path, body)
}

func (a *modelsRecordingAWE) mark() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.removed)
}

func (a *modelsRecordingAWE) undeployedSince(mark int, workflowIDs ...string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var undeployed []string
	for _, id := range workflowIDs {
		instance := a2a.InstanceName(a2a.KindWorkflow, id)
		if slices.ContainsFunc(a.removed[mark:], func(p string) bool { return strings.HasSuffix(p, instance) }) {
			undeployed = append(undeployed, id)
		}
	}
	slices.Sort(undeployed)
	return undeployed
}

func newModelsRefsAPI(t *testing.T) (*entrypointAPI, *modelsRecordingAWE) {
	t.Helper()
	core, _ := newTestCore(t)
	awe := &modelsRecordingAWE{}
	startControlApp(t, core, "awe", awe.handle)
	return &entrypointAPI{apiClient: newAPIClient(t, core)}, awe
}

func modelsCreateDecision(api *entrypointAPI, alias string) Model {
	api.t.Helper()
	return expectCreated[Model](api.t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": alias, "provider": "typesafe", "modelName": "jev-1.13.0",
		"modelType": "decision", "authConfig": apiKeyAuth(),
	}), "models")
}

func modelsDecisionOn(modelRef string) map[string]any {
	return map[string]any{"decision": map[string]any{"model_alias": modelRef}}
}

func modelsSaveWorkflow(api *entrypointAPI, name string, appConfig map[string]any) Workflow {
	api.t.Helper()
	return expectCreated[Workflow](api.t, api.call(http.MethodPost, "/workflows", map[string]any{
		"name": name, "description": "Workflow the model test saves.", "appConfig": appConfig,
	}), "workflows")
}

func modelsRepointWorkflow(api *entrypointAPI, wf Workflow, appConfig map[string]any) {
	api.t.Helper()
	expectStatus(api.t, api.call(http.MethodPut, "/workflows/"+wf.ID, map[string]any{
		"name": wf.Name, "description": wf.Description, "appConfig": appConfig,
	}), http.StatusOK)
}

func modelsWorkflowAction(api *entrypointAPI, workflowID, action string) *httptest.ResponseRecorder {
	api.t.Helper()
	return api.call(http.MethodPost, "/workflowDeployments", map[string]any{"workflowId": workflowID, "action": action})
}

func modelsDeployWorkflow(api *entrypointAPI, workflowID string) {
	api.t.Helper()
	expectCreated[WorkflowDeployment](api.t, modelsWorkflowAction(api, workflowID, "deploy"), "workflowDeployments")
}

func modelsDependents(api *entrypointAPI, modelID string) map[string]ModelDependent {
	api.t.Helper()
	rec := api.call(http.MethodGet, "/models/"+modelID+"/dependents?pageSize=100", nil)
	expectStatus(api.t, rec, http.StatusOK)
	byKey := map[string]ModelDependent{}
	for _, d := range decodeList[ModelDependent](api.t, rec).Data {
		byKey[d.Key] = d
	}
	return byKey
}

func modelsLoggedCause(t *testing.T, logs *lockedBuffer, rec *httptest.ResponseRecorder) string {
	t.Helper()
	entry, logged := errorLogEntry(logs.String(), decodeError(t, rec).ErrorID)
	if !logged {
		t.Fatalf("no log line carries the errorId of %s", rec.Body.String())
	}
	return entry.Error
}

func modelsHideAgentsTable(t *testing.T, api *entrypointAPI) {
	t.Helper()
	if _, err := api.core.DB().ExecContext(t.Context(), `ALTER TABLE agents RENAME TO agents_unreachable`); err != nil {
		t.Fatalf("hide agents: %v", err)
	}
}

func TestModelDependents_ListsTheAgentsOnTheModel(t *testing.T) {
	api, _ := newModelsRefsAPI(t)
	generalID := modelIDByAlias(t, api, "general")
	expectStatus(t, api.call(http.MethodPatch, "/models/"+generalID, map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": "https://llm.example.com/v1", "authConfig": apiKeyAuth(),
	}), http.StatusOK)
	rec := api.call(http.MethodPost, "/agents", map[string]any{
		"name": "default-model-agent", "description": "Agent that names no model.",
		"systemPrompt": "You help with tests.", "inputModes": []string{"text"},
	})
	byDefault := expectCreated[Agent](t, rec, "agents")
	byID := createAgentOnModel(api, "id-model-agent", generalID)
	createModel(api, "elsewhere", apiKeyAuth())
	elsewhere := createAgentOnModel(api, "elsewhere-agent", "elsewhere")

	got := modelsDependents(api, generalID)

	for _, agent := range []Agent{byDefault, byID} {
		if got[agent.ID].DependentType != "agent" {
			t.Errorf("dependents = %+v, want %s listed as an agent", got, agent.Name)
		}
	}
	if _, listed := got[elsewhere.ID]; listed {
		t.Errorf("dependents = %+v, want no agent that names another model", got)
	}
}

func TestModelDependents_ListWorkflowsNamingTheModel(t *testing.T) {
	api, _ := newModelsRefsAPI(t)
	jev := modelsCreateDecision(api, "jev")
	modelsCreateDecision(api, "other")
	byAlias := modelsSaveWorkflow(api, "triage-by-alias", modelsDecisionOn("jev"))
	byID := modelsSaveWorkflow(api, "triage-by-id", map[string]any{
		"workflow": map[string]any{"decision": map[string]any{"modelAlias": jev.ID}},
	})
	modelsSaveWorkflow(api, "triage-other", modelsDecisionOn("other"))
	modelsDeployWorkflow(api, byAlias.ID)

	got := modelsDependents(api, jev.ID)

	want := map[string]ModelDependent{
		byAlias.ID: {Type: "modelDependent", Key: byAlias.ID, Name: "triage-by-alias", DependentType: "workflow", DeploymentStatus: entplatform.DisplayStatusDeployed, MayStillRun: true},
		byID.ID:    {Type: "modelDependent", Key: byID.ID, Name: "triage-by-id", DependentType: "workflow", DeploymentStatus: "not_deployed"},
	}
	if len(got) != len(want) {
		t.Fatalf("dependents = %+v, want the two workflows naming jev", got)
	}
	for key, dep := range want {
		if got[key] != dep {
			t.Errorf("dependent %+v, want %+v", got[key], dep)
		}
	}
}

func TestModels_DeleteUndeploysTheDeployedWorkflowsThatNameIt(t *testing.T) {
	api, awe := newModelsRefsAPI(t)
	jev := modelsCreateDecision(api, "jev")
	modelsCreateDecision(api, "other")
	deployed := modelsSaveWorkflow(api, "triage-by-alias", modelsDecisionOn("jev"))
	modelsDeployWorkflow(api, deployed.ID)
	savedOnly := modelsSaveWorkflow(api, "triage-by-id", modelsDecisionOn(jev.ID))
	unrelated := modelsSaveWorkflow(api, "unrelated", modelsDecisionOn("other"))
	modelsDeployWorkflow(api, unrelated.ID)
	mark := awe.mark()

	expectStatus(t, api.call(http.MethodDelete, "/models/"+jev.ID, nil), http.StatusNoContent)

	if got := awe.undeployedSince(mark, deployed.ID, savedOnly.ID, unrelated.ID); !slices.Equal(got, []string{deployed.ID}) {
		t.Errorf("undeployed workflows = %v, want only the deployed one naming the model (%s)", got, deployed.ID)
	}
	expectStatus(t, api.call(http.MethodGet, "/models/"+jev.ID, nil), http.StatusNotFound)
}

func TestModels_DeleteUndeploysAWorkflowWhoseRedeployWasRefused(t *testing.T) {
	api, awe := newModelsRefsAPI(t)
	jev := modelsCreateDecision(api, "jev")
	modelsCreateDecision(api, "jev2")
	refused := modelsSaveWorkflow(api, "redeploy-refused", modelsDecisionOn("jev"))
	modelsDeployWorkflow(api, refused.ID)
	modelsRepointWorkflow(api, refused, modelsDecisionOn("jev2"))
	awe.failAdds.Store(true)
	modelsWorkflowAction(api, refused.ID, "update")
	awe.failAdds.Store(false)

	if got := modelsDependents(api, jev.ID); len(got) != 1 || got[refused.ID].DeploymentStatus != entplatform.DisplayStatusDeployed {
		t.Fatalf("dependents = %+v, want %s listed as deployed", got, refused.ID)
	}
	mark := awe.mark()
	expectStatus(t, api.call(http.MethodDelete, "/models/"+jev.ID, nil), http.StatusNoContent)
	if got := awe.undeployedSince(mark, refused.ID); !slices.Equal(got, []string{refused.ID}) {
		t.Errorf("undeployed workflows = %v, want the one still running on the model (%s)", got, refused.ID)
	}
}

func TestModels_DeleteUndeploysNothingWhenADependentsLookupFails(t *testing.T) {
	api, awe := newModelsRefsAPI(t)
	jev := modelsCreateDecision(api, "jev")
	running := modelsSaveWorkflow(api, "triage", modelsDecisionOn("jev"))
	modelsDeployWorkflow(api, running.ID)
	modelsHideAgentsTable(t, api)
	mark := awe.mark()

	expectStatus(t, api.call(http.MethodDelete, "/models/"+jev.ID, nil), http.StatusInternalServerError)

	if got := awe.undeployedSince(mark, running.ID); len(got) != 0 {
		t.Errorf("undeployed workflows = %v, want none after a failed lookup", got)
	}
	expectStatus(t, api.call(http.MethodGet, "/models/"+jev.ID, nil), http.StatusOK)
}

func TestModels_AliasRenameIsRefusedWhileAWorkflowNamesIt(t *testing.T) {
	logs := captureLogs(t)
	api, _ := newModelsRefsAPI(t)
	jev := modelsCreateDecision(api, "jev")
	modelsSaveWorkflow(api, "triage", modelsDecisionOn("jev"))

	rec := api.call(http.MethodPatch, "/models/"+jev.ID, map[string]any{"alias": "jev-renamed"})
	expectStatus(t, rec, http.StatusConflict)
	if cause := modelsLoggedCause(t, logs, rec); !strings.Contains(cause, `"triage"`) {
		t.Errorf("logged cause %q does not name the workflow", cause)
	}

	expectStatus(t, api.call(http.MethodPatch, "/models/"+jev.ID, map[string]any{
		"alias": "jev", "description": "unchanged alias",
	}), http.StatusOK)
}

func TestModels_DeleteUndeploysWorkflowsStillRunningOnIt(t *testing.T) {
	logs := captureLogs(t)
	api, awe := newModelsRefsAPI(t)
	jev := modelsCreateDecision(api, "jev")
	modelsCreateDecision(api, "jev2")
	moved := modelsSaveWorkflow(api, "moved-not-redeployed", modelsDecisionOn("jev"))
	modelsDeployWorkflow(api, moved.ID)
	modelsRepointWorkflow(api, moved, modelsDecisionOn("jev2"))
	both := modelsSaveWorkflow(api, "saved-and-deployed", modelsDecisionOn("jev"))
	modelsDeployWorkflow(api, both.ID)

	got := modelsDependents(api, jev.ID)
	if len(got) != 2 {
		t.Fatalf("dependents = %+v, want each workflow naming jev listed once", got)
	}
	for _, wf := range []Workflow{moved, both} {
		if dep := got[wf.ID]; dep.Name != wf.Name || dep.DeploymentStatus != entplatform.DisplayStatusDeployed {
			t.Errorf("dependent %+v, want %s listed as deployed", dep, wf.Name)
		}
	}

	rec := api.call(http.MethodPatch, "/models/"+jev.ID, map[string]any{"alias": "jev-renamed"})
	expectStatus(t, rec, http.StatusConflict)
	cause := modelsLoggedCause(t, logs, rec)
	for _, fragment := range []string{`"moved-not-redeployed" (deployed version)`, "redeploy", `"saved-and-deployed"`} {
		if !strings.Contains(cause, fragment) {
			t.Errorf("logged rename cause %q lacks %q", cause, fragment)
		}
	}
	if strings.Contains(cause, `"saved-and-deployed" (deployed version)`) {
		t.Errorf("logged rename cause %q calls a saved reference deployed-only", cause)
	}

	mark := awe.mark()
	expectStatus(t, api.call(http.MethodDelete, "/models/"+jev.ID, nil), http.StatusNoContent)
	want := slices.Sorted(slices.Values([]string{moved.ID, both.ID}))
	if got := awe.undeployedSince(mark, moved.ID, both.ID); !slices.Equal(got, want) {
		t.Errorf("undeployed workflows = %v, want both running on the model %v", got, want)
	}
}

func TestModels_DeletesOnceItsWorkflowsAreRedeployedOrUndeployed(t *testing.T) {
	api, awe := newModelsRefsAPI(t)
	jev := modelsCreateDecision(api, "jev")
	modelsCreateDecision(api, "jev2")
	var workflowIDs []string
	for _, action := range []string{"update", "undeploy"} {
		wf := modelsSaveWorkflow(api, action+"-off-jev", modelsDecisionOn("jev"))
		modelsDeployWorkflow(api, wf.ID)
		modelsRepointWorkflow(api, wf, modelsDecisionOn("jev2"))
		expectCreated[WorkflowDeployment](t, modelsWorkflowAction(api, wf.ID, action), "workflowDeployments")
		workflowIDs = append(workflowIDs, wf.ID)
	}

	if got := modelsDependents(api, jev.ID); len(got) != 0 {
		t.Fatalf("dependents = %+v, want none once no running or saved workflow names jev", got)
	}
	mark := awe.mark()
	expectStatus(t, api.call(http.MethodDelete, "/models/"+jev.ID, nil), http.StatusNoContent)
	if got := awe.undeployedSince(mark, workflowIDs...); len(got) != 0 {
		t.Errorf("undeployed workflows = %v, want none once no workflow runs on the model", got)
	}
}

func TestModels_DependentsMatchAnyUUIDSpellingOfTheModelID(t *testing.T) {
	api, awe := newModelsRefsAPI(t)
	jev := modelsCreateDecision(api, "jev")
	modelsCreateDecision(api, "jev2")
	braced := modelsSaveWorkflow(api, "braced", modelsDecisionOn("{"+strings.ToUpper(jev.ID)+"}"))
	deployed := modelsSaveWorkflow(api, "upper-deployed", modelsDecisionOn(strings.ToUpper(jev.ID)))
	modelsDeployWorkflow(api, deployed.ID)
	modelsRepointWorkflow(api, deployed, modelsDecisionOn("jev2"))

	got := modelsDependents(api, jev.ID)
	if _, listed := got[braced.ID]; !listed || len(got) != 2 {
		t.Fatalf("dependents = %+v, want both workflows naming the model id in a non-canonical spelling", got)
	}
	if _, listed := got[deployed.ID]; !listed {
		t.Fatalf("dependents = %+v, want %s, whose running deployment names the model id", got, deployed.ID)
	}

	mark := awe.mark()
	expectStatus(t, api.call(http.MethodDelete, "/models/"+jev.ID, nil), http.StatusNoContent)
	if got := awe.undeployedSince(mark, braced.ID, deployed.ID); !slices.Equal(got, []string{deployed.ID}) {
		t.Errorf("undeployed workflows = %v, want the one whose running deployment names the model id (%s)", got, deployed.ID)
	}
}

func TestModels_TypeGuardsHoldOnEveryRoute(t *testing.T) {
	api, _ := newModelsRefsAPI(t)
	modelsCreateDecision(api, "jev")
	chatty := createModel(api, "chatty", apiKeyAuth())

	t.Run("agent save refuses a decision model", func(t *testing.T) {
		rec := api.call(http.MethodPost, "/agents", map[string]any{
			"name": "router", "description": "routes incoming requests", "systemPrompt": "route things",
			"inputModes": []string{"text"}, "modelProvider": []string{"jev"},
		})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if !strings.Contains(rec.Body.String(), "is a decision model") {
			t.Errorf("POST agent: %s, want the decision-model refusal", rec.Body.String())
		}
	})

	t.Run("workflow save refuses a chat model alias", func(t *testing.T) {
		rec := api.call(http.MethodPost, "/workflows", map[string]any{
			"name": "triage-chat", "description": "routes incoming tickets", "appConfig": modelsDecisionOn("chatty"),
		})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if !strings.Contains(rec.Body.String(), "is a chat model") {
			t.Errorf("POST workflow: %s, want the chat-model refusal", rec.Body.String())
		}
	})

	t.Run("a chat model cannot move to the decision provider", func(t *testing.T) {
		expectDetailSaying(t, api.call(http.MethodPatch, "/models/"+chatty.ID, map[string]any{"provider": "typesafe"}),
			"provider", "serves decision models only")
	})
}

func TestModels_ReadFailuresLogTheCauseUnderTheErrorID(t *testing.T) {
	logs := captureLogs(t)
	api, _ := newModelAPI(t)
	created := createModel(api, "cause", apiKeyAuth())
	if err := api.core.DB().Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	for _, path := range []string{"/models", "/modelStatus", "/models/" + created.ID, "/models/" + created.ID + "/dependents"} {
		t.Run(path, func(t *testing.T) {
			rec := api.call(http.MethodGet, path, nil)
			expectStatus(t, rec, http.StatusInternalServerError)
			if strings.Contains(rec.Body.String(), "database is closed") {
				t.Errorf("the store's error reached the wire: %s", rec.Body.String())
			}
			entry, logged := errorLogEntry(logs.String(), decodeError(t, rec).ErrorID)
			if !logged || !strings.Contains(entry.Error, "database is closed") {
				t.Errorf("log entry for the errorId = %+v (found %v), want the store's error", entry, logged)
			}
		})
	}
}
