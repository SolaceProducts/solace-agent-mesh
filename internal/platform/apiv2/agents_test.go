// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newAWEAPI(t *testing.T) (*entrypointAPI, *fakeAWE) {
	t.Helper()
	core, _ := newTestCore(t)
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}
	awe := newFakeAWE(api)
	startControlApp(t, core, "awe", awe.handle)
	return api, awe
}

func newFakeAWE(api *entrypointAPI) *fakeAWE {
	api.t.Helper()
	awe := &fakeAWE{builtins: map[string]bool{}}
	for _, agent := range decodeList[Agent](api.t, api.call(http.MethodGet, "/agents?pageSize=100", nil)).Data {
		if agent.CreatedBy == "system" {
			awe.builtins[agent.InstanceName] = true
		}
	}
	return awe
}

type fakeAWE struct {
	fakeGateway
	mu       sync.Mutex
	lastAdd  string
	builtins map[string]bool
}

func (a *fakeAWE) handle(method string, path []string, body json.RawMessage) (any, error) {
	var added struct {
		Name string `json:"name"`
	}
	if method == "post" && (json.Unmarshal(body, &added) != nil || !a.builtins[added.Name]) {
		a.mu.Lock()
		a.lastAdd = string(body)
		a.mu.Unlock()
	}
	return a.fakeGateway.handle(method, path, body)
}

func (a *fakeAWE) lastAddBody() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastAdd
}

func createAgent(api *entrypointAPI, name string) Agent {
	api.t.Helper()
	rec := api.call(http.MethodPost, "/agents", map[string]any{
		"name":         name,
		"description":  "Agent the test creates.",
		"systemPrompt": "You help with tests.",
		"inputModes":   []string{"text"},
	})
	return expectCreated[Agent](api.t, rec, "agents")
}

func deployAgent(api *entrypointAPI, agentID string) *httptest.ResponseRecorder {
	api.t.Helper()
	return api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": agentID, "action": "deploy"})
}

func rawData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var raw struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode {data} from %s: %v", rec.Body.String(), err)
	}
	return raw.Data
}

func expectISOTimes(t *testing.T, data map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if s, _ := data[key].(string); !regexp.MustCompile(isoMillisUTC).MatchString(s) {
			t.Errorf("%s = %v, want ISO 8601 ms UTC", key, data[key])
		}
	}
}

func expectFailedDeployment[T any](api *entrypointAPI, rec *httptest.ResponseRecorder, message, collection string) T {
	api.t.Helper()
	return expectKeptDeployment[T](api, rec, http.StatusBadGateway, message, collection)
}

func expectKeptDeployment[T any](api *entrypointAPI, rec *httptest.ResponseRecorder, status int, message, collection string) T {
	api.t.Helper()
	expectStatus(api.t, rec, status)
	if body := decodeError(api.t, rec); body.Message != message {
		api.t.Errorf("message = %q, want %q", body.Message, message)
	}
	if strings.Contains(rec.Body.String(), deployFailText) {
		api.t.Errorf("the upstream error reached the response: %s", rec.Body.String())
	}
	location := rec.Header().Get("Location")
	id, ok := strings.CutPrefix(location, platpkg.APIV2Prefix+"/"+collection+"/")
	if !ok {
		api.t.Fatalf("Location = %q, want the deployment's v2 path", location)
	}
	kept := api.call(http.MethodGet, "/"+collection+"/"+id, nil)
	expectStatus(api.t, kept, http.StatusOK)
	if strings.Contains(kept.Body.String(), deployFailText) {
		api.t.Errorf("the upstream error reached the kept record: %s", kept.Body.String())
	}
	return decodeData[T](api.t, kept)
}

func expectCreated[T any](t *testing.T, rec *httptest.ResponseRecorder, collection string) T {
	t.Helper()
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[T](t, rec)
	expectLocation(t, rec, "/"+collection+"/"+reflect.ValueOf(created).FieldByName("ID").String())
	return created
}

func expectFailure(t *testing.T, got *DeploymentFailure, kind, wantReason string) {
	t.Helper()
	want := fmt.Sprintf(deployFailureMessages[wantReason], kind)
	if got == nil || got.Reason != wantReason || got.Message != want || got.ErrorID == nil {
		t.Errorf("failure = %+v, want %s %q with its errorId", got, wantReason, want)
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	logs := &lockedBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func expectCauseLogged(t *testing.T, logs *lockedBuffer, rec *httptest.ResponseRecorder, failure *DeploymentFailure) {
	t.Helper()
	ids := []string{decodeError(t, rec).ErrorID}
	if failure != nil && failure.ErrorID != nil {
		ids = append(ids, *failure.ErrorID)
	}
	for _, id := range ids {
		if !loggedWithCause(logs.String(), id) {
			t.Errorf("no log line has errorId %s with the cause %q", id, deployFailText)
		}
	}
}

func loggedWithCause(logs, errorID string) bool {
	for line := range strings.Lines(logs) {
		var entry struct {
			ErrorID string `json:"errorId"`
			Error   string `json:"error"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.ErrorID == errorID && strings.Contains(entry.Error, deployFailText) {
			return true
		}
	}
	return false
}

func expectAbsent(t *testing.T, data map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := data[key]; ok {
			t.Errorf("the response carries %q, which v2 renames or drops", key)
		}
	}
}

func TestAgents_Lifecycle(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createAgent(api, "lifecycle-agent")
	if created.Type != "agent" || created.AgentType != "standard" {
		t.Errorf("type %q agentType %q, want agent and standard", created.Type, created.AgentType)
	}
	if created.Toolsets == nil || created.ConnectorIDs == nil || created.Skills == nil || created.ToolRegistrations == nil {
		t.Errorf("a list field is null: %+v", created)
	}

	got := api.call(http.MethodGet, "/agents/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	raw := rawData(t, got)
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	expectAbsent(t, raw, "connectors", "createdAt", "updatedAt", "expiresAt")
	for _, key := range []string{"connectorIds", "agentType"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("GET is missing %q", key)
		}
	}

	listed := decodeList[Agent](t, api.call(http.MethodGet, "/agents", nil))
	if !slices.ContainsFunc(listed.Data, func(a Agent) bool { return a.ID == created.ID }) {
		t.Errorf("list is missing the created agent: %+v", listed.Data)
	}

	promptless := api.call(http.MethodPut, "/agents/"+created.ID, map[string]any{
		"name": "replaced-agent", "description": "Agent the test replaced.",
	})
	expectDetail(t, promptless, http.StatusUnprocessableEntity, "systemPrompt")
	replaced := api.call(http.MethodPut, "/agents/"+created.ID, map[string]any{
		"name":         "replaced-agent",
		"description":  "Agent the test replaced.",
		"systemPrompt": "You help with other tests.",
	})
	expectStatus(t, replaced, http.StatusOK)
	if a := decodeData[Agent](t, replaced); a.Name != "replaced-agent" || len(a.InputModes) != 0 {
		t.Errorf("after PUT: name %q inputModes %v, want the new name and the omitted modes cleared", a.Name, a.InputModes)
	}

	expectStatus(t, api.call(http.MethodDelete, "/agents/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/agents/"+created.ID, nil), http.StatusNotFound)
}

func TestNeverDeployedResourcesSayNotDeployed(t *testing.T) {
	api, _ := newAWEAPI(t)
	agent := createAgent(api, "fresh-agent")
	workflow := createWorkflow(api, "fresh-workflow")
	remote := createRemoteAgent(api, remoteAgentBody("fresh-remote"))

	statuses := map[string]string{
		"agent create":        agent.DeploymentStatus,
		"workflow create":     workflow.DeploymentStatus,
		"remote agent create": remote.DeploymentStatus,
	}
	for _, a := range decodeList[Agent](t, api.call(http.MethodGet, "/agents", nil)).Data {
		if a.ID == agent.ID {
			statuses["agent list"] = a.DeploymentStatus
		}
	}
	for _, w := range decodeList[Workflow](t, api.call(http.MethodGet, "/workflows", nil)).Data {
		if w.ID == workflow.ID {
			statuses["workflow list"] = w.DeploymentStatus
		}
	}
	for _, r := range decodeList[RemoteAgent](t, api.call(http.MethodGet, "/remoteAgents", nil)).Data {
		if r.ID == remote.ID {
			statuses["remote agent list"] = r.DeploymentStatus
		}
	}
	if len(statuses) != 6 {
		t.Fatalf("a list is missing its resource: %v", statuses)
	}
	for where, status := range statuses {
		if status != "not_deployed" {
			t.Errorf("%s: deploymentStatus %q, want not_deployed", where, status)
		}
	}
}

func TestAgents_Paging(t *testing.T) {
	api, _ := newAWEAPI(t)
	seeded := decodeList[Agent](t, api.call(http.MethodGet, "/agents", nil)).Meta.Pagination.Count
	for _, name := range []string{"page-one", "page-two", "page-three"} {
		createAgent(api, name)
	}
	total := seeded + 3

	defaults := decodeList[Agent](t, api.call(http.MethodGet, "/agents", nil))
	if p := defaults.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != total || len(defaults.Data) != total {
		t.Errorf("default pagination = %+v with %d items, want page 1 of size %d holding all %d", p, len(defaults.Data), defaultPageSize, total)
	}
	last := decodeList[Agent](t, api.call(http.MethodGet, fmt.Sprintf("/agents?pageNumber=%d&pageSize=2", (total+1)/2), nil))
	if want := total - 2*((total-1)/2); len(last.Data) != want || last.Meta.Pagination.TotalPages != (total+1)/2 {
		t.Errorf("last page of size 2 = %d items, want %d; pagination %+v", len(last.Data), want, last.Meta.Pagination)
	}
	expectStatus(t, api.call(http.MethodGet, "/agents?pageNumber=ten", nil), http.StatusBadRequest)
	expectDetail(t, api.call(http.MethodGet, "/agents?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")

	filtered := decodeList[Agent](t, api.call(http.MethodGet, "/agents?connectorId="+missingID, nil))
	if len(filtered.Data) != 0 {
		t.Errorf("connectorId filter for an unused connector returned %d agents", len(filtered.Data))
	}
	expectDetail(t, api.call(http.MethodGet, "/agents?connectorId=nope", nil), http.StatusBadRequest, "connectorId")
}

func TestAgents_Validation(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createAgent(api, "validation-agent")
	path := "/agents/" + created.ID

	both := api.call(http.MethodPost, "/agents", `{"name":"x"}`)
	expectStatus(t, both, http.StatusUnprocessableEntity)
	if details := decodeError(t, both).ValidationDetails; len(details["name"]) == 0 || len(details["description"]) == 0 {
		t.Errorf("want every field problem at once, got %v", details)
	}
	expectDetail(t, api.call(http.MethodPost, "/agents", `{"name":"custom-agent","description":"An agent of another kind.","agentType":"custom"}`),
		http.StatusUnprocessableEntity, "agentType")
	expectDetail(t, api.call(http.MethodPost, "/agents", `{"name":"old-shape","type":"standard"}`), http.StatusBadRequest, "type")
	expectDetail(t, api.call(http.MethodPost, "/agents", `{"name":7}`), http.StatusBadRequest, "name")
	expectStatus(t, api.call(http.MethodPost, "/agents", `{"name":`), http.StatusBadRequest)

	expectDetail(t, api.call(http.MethodPut, path, `{"name":"fixed-kind","description":"Agent type is set at create.","agentType":"standard"}`),
		http.StatusBadRequest, "agentType")
	expectDetail(t, api.call(http.MethodPut, path, `{"name":"no-description"}`), http.StatusUnprocessableEntity, "description")

	expectDetail(t, api.call(http.MethodGet, "/agents/not-a-uuid", nil), http.StatusBadRequest, "id")
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		expectStatus(t, api.call(method, "/agents/"+missingID, nil), http.StatusNotFound)
	}
	expectStatus(t, api.call(http.MethodPut, "/agents/"+missingID, `{"name":"missing-one","description":"Agent that is not there."}`), http.StatusNotFound)
}

func TestAgents_Scopes(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createAgent(api, "scoped-agent")

	for _, tc := range []struct {
		method, path string
		body         any
		scope        string
	}{
		{http.MethodPost, "/agents", `{}`, rbac.ScopeAgentBuilderCreate},
		{http.MethodPut, "/agents/" + created.ID, `{}`, rbac.ScopeAgentBuilderUpdate},
		{http.MethodDelete, "/agents/" + created.ID, nil, rbac.ScopeAgentBuilderDelete},
		{http.MethodPut, "/agents/" + created.ID + "/secretsDiff", `{}`, rbac.ScopeAgentBuilderUpdate},
		{http.MethodPost, "/agentDeployments", `{}`, rbac.ScopeAgentBuilderDeploy},
		{http.MethodGet, "/agentDeployments", nil, rbac.ScopeDeploymentRead},
	} {
		rec := api.call(tc.method, tc.path, tc.body, rbac.ScopeAgentBuilderRead)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with only %s = %d, want 403 (needs %s)", tc.method, tc.path, rbac.ScopeAgentBuilderRead, rec.Code, tc.scope)
		}
	}
	expectStatus(t, api.call(http.MethodGet, "/agents", nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, "/agents", nil, rbac.ScopeAgentBuilderRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/agentPlatformDefaults", nil, rbac.ScopeAgentBuilderRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/agentDeployments", nil, rbac.ScopeDeploymentRead), http.StatusOK)
}

func seedSecretToolset(t *testing.T, api *entrypointAPI, name string) {
	t.Helper()
	repo := store.NewToolPackageRepo(api.core.DB())
	pkg := &store.ToolPackage{Name: name, DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), pkg); err != nil {
		t.Fatalf("create tool package: %v", err)
	}
	schema := `[{"key":"api_key","type":"string","secret":true,"required":true},{"key":"region","type":"string"}]`
	if err := repo.UpsertToolFile(t.Context(), &store.ToolPackageFile{PackageID: pkg.ID, ToolName: "fetch", ConfigSchema: &schema}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}
}

func TestAgents_ToolsetSecretsAreRedactedAndKept(t *testing.T) {
	const toolset, apiKey = "secret-toolset", "sk-agent-secret"
	api, _ := newAWEAPI(t)
	seedSecretToolset(t, api, toolset)
	config := func(key string) map[string]any {
		return map[string]any{
			"name":           "secret-agent",
			"description":    "Agent with a secret toolset setting.",
			"systemPrompt":   "You fetch things.",
			"toolsets":       []string{toolset},
			"toolsetConfigs": []map[string]any{{"toolsetName": toolset, "configValues": map[string]any{"fetch": map[string]any{"api_key": key, "region": "eu"}}}},
		}
	}

	rec := api.call(http.MethodPost, "/agents", config(apiKey))
	created := expectCreated[Agent](t, rec, "agents")
	if strings.Contains(rec.Body.String(), apiKey) {
		t.Errorf("create returned the secret: %s", rec.Body.String())
	}
	got := api.call(http.MethodGet, "/agents/"+created.ID, nil)
	if strings.Contains(got.Body.String(), apiKey) || !strings.Contains(got.Body.String(), "REDACTED") {
		t.Errorf("GET does not redact the secret: %s", got.Body.String())
	}

	expectStatus(t, api.call(http.MethodPut, "/agents/"+created.ID, config(redacted)), http.StatusOK)

	diffPath := "/agents/" + created.ID + "/secretsDiff"
	diff := api.call(http.MethodPut, diffPath, map[string]any{"values": map[string]any{"toolsetConfigs": config(apiKey)["toolsetConfigs"]}})
	expectStatus(t, diff, http.StatusOK)
	if match := decodeData[SecretsDiffResult](t, diff).Secrets[toolset+"/fetch.api_key"]; !match.Matches {
		t.Errorf("after a PUT with the redacted value, the stored secret no longer matches: %s", diff.Body.String())
	}
	if strings.Contains(diff.Body.String(), apiKey) {
		t.Error("secretsDiff returned the secret")
	}

	expectDetail(t, api.call(http.MethodPut, diffPath, `{}`), http.StatusUnprocessableEntity, "values")
	expectStatus(t, api.call(http.MethodPut, "/agents/"+missingID+"/secretsDiff", `{"values":{}}`), http.StatusNotFound)
}

func TestAgents_SkillConfigsSurviveReadThenPut(t *testing.T) {
	const apiKey = "sk-skill-secret"
	api, _ := newAWEAPI(t)
	schema := `[{"tool_name":"render","config_schema":[{"key":"api_key","type":"string","secret":true},{"key":"model_id","type":"string"}]}]`
	skill := &store.Skill{Name: "render_skill", Origin: "platform", DiscoveryStatus: "ready", BundledTools: &schema, CreatedBy: "u", UpdatedBy: "u"}
	if err := store.NewSkillRepo(api.core.DB()).Create(t.Context(), skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	rec := api.call(http.MethodPost, "/agents", map[string]any{
		"name":         "skill-agent",
		"description":  "Agent with a configured skill.",
		"systemPrompt": "You render things.",
		"skillIds":     []string{skill.ID},
		"skillConfigs": []map[string]any{{"skillId": skill.ID, "configValues": map[string]any{"render": map[string]any{"api_key": apiKey, "model_id": "m1"}}}},
	})
	id := expectCreated[Agent](t, rec, "agents").ID
	read := decodeData[Agent](t, api.call(http.MethodGet, "/agents/"+id, nil))
	if len(read.SkillConfigs) != 1 {
		t.Fatalf("GET skillConfigs = %+v, want the one created", read.SkillConfigs)
	}

	expectStatus(t, api.call(http.MethodPut, "/agents/"+read.ID, map[string]any{
		"name":         read.Name,
		"description":  read.Description,
		"systemPrompt": read.SystemPrompt,
		"skillIds":     read.SkillIDs,
		"skillConfigs": read.SkillConfigs,
	}), http.StatusOK)

	reread := decodeData[Agent](t, api.call(http.MethodGet, "/agents/"+read.ID, nil))
	if !slices.Equal(reread.SkillIDs, read.SkillIDs) || !reflect.DeepEqual(reread.SkillConfigs, read.SkillConfigs) {
		t.Errorf("after PUTting back what GET read: skillIds %v skillConfigs %+v, want %v %+v", reread.SkillIDs, reread.SkillConfigs, read.SkillIDs, read.SkillConfigs)
	}
	diff := api.call(http.MethodPut, "/agents/"+read.ID+"/secretsDiff", map[string]any{"values": map[string]any{
		"skillConfigs": []map[string]any{{"skillId": skill.ID, "configValues": map[string]any{"render": map[string]any{"api_key": apiKey}}}},
	}})
	expectStatus(t, diff, http.StatusOK)
	if match := decodeData[SecretsDiffResult](t, diff).Secrets[skill.Name+"/render.api_key"]; !match.Matches {
		t.Errorf("after PUTting back the redacted skill secret, the stored secret no longer matches: %s", diff.Body.String())
	}
}

func TestAgents_PutWithoutSkillConfigsKeepsThem(t *testing.T) {
	api, _ := newAWEAPI(t)
	schema := `[{"tool_name":"render","config_schema":[{"key":"api_key","type":"string","secret":true},{"key":"model_id","type":"string"}]}]`
	skill := &store.Skill{Name: "render_skill", Origin: "platform", DiscoveryStatus: "ready", BundledTools: &schema, CreatedBy: "u", UpdatedBy: "u"}
	if err := store.NewSkillRepo(api.core.DB()).Create(t.Context(), skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	agent := map[string]any{
		"name":         "skill-agent",
		"description":  "Agent with a configured skill.",
		"systemPrompt": "You render things.",
		"skillIds":     []string{skill.ID},
		"skillConfigs": []map[string]any{{"skillId": skill.ID, "configValues": map[string]any{"render": map[string]any{"api_key": "sk-skill-secret", "model_id": "m1"}}}},
	}
	id := expectCreated[Agent](t, api.call(http.MethodPost, "/agents", agent), "agents").ID
	stored := decodeData[Agent](t, api.call(http.MethodGet, "/agents/"+id, nil)).SkillConfigs

	delete(agent, "skillConfigs")
	expectStatus(t, api.call(http.MethodPut, "/agents/"+id, agent), http.StatusOK)
	if got := decodeData[Agent](t, api.call(http.MethodGet, "/agents/"+id, nil)).SkillConfigs; !reflect.DeepEqual(got, stored) {
		t.Errorf("after a PUT without skillConfigs: %+v, want the stored %+v", got, stored)
	}

	agent["skillConfigs"] = []any{}
	expectStatus(t, api.call(http.MethodPut, "/agents/"+id, agent), http.StatusOK)
	if got := decodeData[Agent](t, api.call(http.MethodGet, "/agents/"+id, nil)).SkillConfigs; len(got) != 0 {
		t.Errorf("after a PUT with skillConfigs []: %+v, want none", got)
	}
}

func TestAgentPlatformDefaults(t *testing.T) {
	api, _ := newAWEAPI(t)

	rec := api.call(http.MethodGet, "/agentPlatformDefaults", nil)
	expectStatus(t, rec, http.StatusOK)
	got := decodeData[AgentPlatformDefaults](t, rec)
	if got.SessionService.DefaultBehavior != "persistent" || got.Log.StdoutLogLevel != "info" || got.Log.LogFileLevel != "info" {
		t.Errorf("sessionService %+v log %+v, want lowercase enums", got.SessionService, got.Log)
	}
	if got.ValidationLimits.AgentInstructionMax == 0 {
		t.Errorf("validationLimits = %+v, want the operator's bounds", got.ValidationLimits)
	}
	for _, secret := range []string{"password", "apiKey", "api_key"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(secret)) {
			t.Errorf("platform defaults carry %q: %s", secret, rec.Body.String())
		}
	}
}

func TestAgentDeployments_Success(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createAgent(api, "deploy-agent")

	rec := deployAgent(api, created.ID)
	deployment := expectCreated[AgentDeployment](t, rec, "agentDeployments")
	if deployment.Type != "agentDeployment" || deployment.Status != entplatform.DeployStatusSuccess || deployment.AgentID != created.ID {
		t.Errorf("deployment = %+v", deployment)
	}
	expectISOTimes(t, rawData(t, rec), "createdTime", "updatedTime")

	expectStatus(t, api.call(http.MethodGet, "/agentDeployments/"+deployment.ID, nil), http.StatusOK)
	listed := decodeList[AgentDeployment](t, api.call(http.MethodGet, "/agentDeployments?agentId="+created.ID, nil))
	if len(listed.Data) != 1 || listed.Data[0].ID != deployment.ID {
		t.Errorf("deployments for the agent = %+v", listed.Data)
	}

	after := decodeData[Agent](t, api.call(http.MethodGet, "/agents/"+created.ID, nil))
	if after.Deployed == nil || after.Deployed.Name != created.Name {
		t.Fatalf("deployed = %+v after a successful deploy", after.Deployed)
	}
	if _, ok := rawData(t, api.call(http.MethodGet, "/agents/"+created.ID, nil))["deployed"].(map[string]any)["connectorSnapshots"]; ok {
		t.Error("deployed carries connectorSnapshots; deployment snapshots are never returned")
	}
	if after.Deployed.ConnectorIDs == nil {
		t.Errorf("a deployed list field is null: %+v", after.Deployed)
	}
}

func TestAgentDeployments_FailureIs502AndKeepsTheRecord(t *testing.T) {
	logs := captureLogs(t)
	api, awe := newAWEAPI(t)
	created := createAgent(api, "failing-agent")
	awe.failAdds.Store(true)

	rec := deployAgent(api, created.ID)
	d := expectFailedDeployment[AgentDeployment](api, rec, msgAgentDeployFailed, "agentDeployments")
	if d.Status != entplatform.DeployStatusFailed || d.AgentID != created.ID {
		t.Errorf("kept deployment = %+v, want failed", d)
	}
	expectFailure(t, d.Failure, "agent", entplatform.DeployFailureRuntimeError)
	expectCauseLogged(t, logs, rec, d.Failure)
}

func TestAgentDeployments_Validation(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createAgent(api, "validate-deploy")

	expectDetail(t, api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": created.ID, "action": "rollback"}),
		http.StatusUnprocessableEntity, "action")
	expectDetail(t, api.call(http.MethodPost, "/agentDeployments", map[string]any{"action": "deploy"}),
		http.StatusUnprocessableEntity, "agentId")
	expectDetail(t, api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": "nope", "action": "deploy"}),
		http.StatusUnprocessableEntity, "agentId")
	expectStatus(t, deployAgent(api, missingID), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodGet, "/agentDeployments?agentId=nope", nil), http.StatusBadRequest, "agentId")
	expectDetail(t, api.call(http.MethodGet, "/agentDeployments/nope", nil), http.StatusBadRequest, "id")
	expectStatus(t, api.call(http.MethodGet, "/agentDeployments/"+missingID, nil), http.StatusNotFound)
}

func TestAgentDeployments_ActionsRejectFieldsTheyDontTake(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createAgent(api, "stray-fields")
	deployed := decodeData[AgentDeployment](t, deployAgent(api, created.ID))
	expectActionsRejectFieldsTheyDontTake(t, api, "/agentDeployments", "agentId", created.ID, deployed.ID, "skipIfCurrent")
}

func expectActionsRejectFieldsTheyDontTake(t *testing.T, api *entrypointAPI, path, idField, id, deploymentID, option string) {
	t.Helper()
	deploy := map[string]any{"action": entplatform.DeployActionDeploy}
	update := map[string]any{"action": entplatform.DeployActionUpdate}
	undeploy := map[string]any{"action": entplatform.DeployActionUndeploy}
	rollback := map[string]any{"action": entplatform.DeployActionUpdate, "deploymentId": deploymentID}
	for _, action := range []map[string]any{deploy, update, undeploy, rollback} {
		for stray, value := range map[string]any{"config": map[string]any{}, "force": true, "version": 2} {
			expectDetail(t, api.call(http.MethodPost, path, with(action, idField, id, stray, value)), http.StatusBadRequest, stray)
		}
	}
	expectDetail(t, api.call(http.MethodPost, path, with(undeploy, idField, id, "deploymentId", deploymentID)), http.StatusUnprocessableEntity, "deploymentId")
	expectDetail(t, api.call(http.MethodPost, path, with(undeploy, idField, id, option, true)), http.StatusUnprocessableEntity, option)
	expectDetail(t, api.call(http.MethodPost, path, with(rollback, idField, id, option, true)), http.StatusUnprocessableEntity, option)
}

func with(body map[string]any, kv ...any) map[string]any {
	out := maps.Clone(body)
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func TestAgentDeployments_Rollback(t *testing.T) {
	api, awe := newAWEAPI(t)
	created := createAgent(api, "rollback-agent")
	first := decodeData[AgentDeployment](t, deployAgent(api, created.ID))
	expectStatus(t, api.call(http.MethodPut, "/agents/"+created.ID, map[string]any{
		"name": created.Name, "description": created.Description, "systemPrompt": "You help with newer tests.",
	}), http.StatusOK)
	expectCreated[AgentDeployment](t, deployAgent(api, created.ID), "agentDeployments")
	if !strings.Contains(awe.lastAddBody(), "newer tests") {
		t.Fatalf("the second deploy sent %s, want the newer prompt", awe.lastAddBody())
	}

	rollback := map[string]any{"agentId": created.ID, "action": "update", "deploymentId": first.ID}
	rec := api.call(http.MethodPost, "/agentDeployments", rollback)
	rolledBack := expectCreated[AgentDeployment](t, rec, "agentDeployments")
	if rolledBack.ID == first.ID || rolledBack.Status != entplatform.DeployStatusSuccess || rolledBack.Action != "update" {
		t.Errorf("rollback = %+v, want a new successful update", rolledBack)
	}
	if sent := awe.lastAddBody(); !strings.Contains(sent, created.SystemPrompt) || strings.Contains(sent, "newer tests") {
		t.Errorf("the rollback sent %s, want the first deployment's prompt %q", sent, created.SystemPrompt)
	}
	after := decodeData[Agent](t, api.call(http.MethodGet, "/agents/"+created.ID, nil))
	if after.Deployed == nil || after.Deployed.SystemPrompt != created.SystemPrompt {
		t.Errorf("deployed = %+v, want the first deployment's system prompt %q", after.Deployed, created.SystemPrompt)
	}
	if after.SystemPrompt != "You help with newer tests." || after.SyncStatus == nil || *after.SyncStatus != "out_of_sync" {
		t.Errorf("after rollback: systemPrompt %q syncStatus %v, want the current prompt kept and out_of_sync", after.SystemPrompt, after.SyncStatus)
	}

	awe.failAdds.Store(true)
	failed := expectFailedDeployment[AgentDeployment](api, api.call(http.MethodPost, "/agentDeployments", rollback), msgAgentDeployFailed, "agentDeployments")
	if failed.Status != entplatform.DeployStatusFailed || failed.ID == first.ID {
		t.Errorf("failed rollback = %+v, want a new failed record", failed)
	}
}

func TestAgentDeployments_RollbackRefusals(t *testing.T) {
	api, awe := newAWEAPI(t)
	created := createAgent(api, "rollback-refusals")
	other := decodeData[AgentDeployment](t, deployAgent(api, createAgent(api, "rollback-other").ID))
	deployed := decodeData[AgentDeployment](t, deployAgent(api, created.ID))
	undeploy := api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": created.ID, "action": "undeploy"})
	expectCreated[AgentDeployment](t, undeploy, "agentDeployments")
	awe.failAdds.Store(true)
	failed := expectFailedDeployment[AgentDeployment](api, deployAgent(api, created.ID), msgAgentDeployFailed, "agentDeployments")
	awe.failAdds.Store(false)

	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"another agent's deployment": {map[string]any{"deploymentId": other.ID}, "deploymentId"},
		"no such deployment":         {map[string]any{"deploymentId": missingID}, "deploymentId"},
		"a failed deployment":        {map[string]any{"deploymentId": failed.ID}, "deploymentId"},
		"an undeploy":                {map[string]any{"deploymentId": decodeData[AgentDeployment](t, undeploy).ID}, "deploymentId"},
		"not a UUID":                 {map[string]any{"deploymentId": "nope"}, "deploymentId"},
		"with undeploy":              {map[string]any{"deploymentId": deployed.ID, "action": "undeploy"}, "deploymentId"},
		"with skipIfCurrent":         {map[string]any{"deploymentId": deployed.ID, "skipIfCurrent": true}, "skipIfCurrent"},
	} {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{"agentId": created.ID, "action": "deploy"}
			maps.Copy(body, tc.body)
			expectDetail(t, api.call(http.MethodPost, "/agentDeployments", body), http.StatusUnprocessableEntity, tc.field)
		})
	}
	expectStatus(t, api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": missingID, "action": "deploy", "deploymentId": deployed.ID}),
		http.StatusNotFound)
}

func TestAgents_DeleteKeepsTheAgentWhenUndeployFails(t *testing.T) {
	api, awe := newAWEAPI(t)
	created := createAgent(api, "undeploy-fails")
	expectCreated[AgentDeployment](t, deployAgent(api, created.ID), "agentDeployments")
	awe.failRemoves.Store(true)

	expectStatus(t, api.call(http.MethodDelete, "/agents/"+created.ID, nil), http.StatusBadGateway)
	expectStatus(t, api.call(http.MethodGet, "/agents/"+created.ID, nil), http.StatusOK)

	awe.failRemoves.Store(false)
	before := awe.removes.Load()
	expectStatus(t, api.call(http.MethodDelete, "/agents/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/agents/"+created.ID, nil), http.StatusNotFound)
	if awe.removes.Load() == before {
		t.Error("the retried delete removed the record without undeploying the agent")
	}
}

func TestAgentDeployments_FailedUndeployIs502AndKeepsTheRecord(t *testing.T) {
	api, awe := newAWEAPI(t)
	created := createAgent(api, "undeploy-action-fails")
	expectCreated[AgentDeployment](t, deployAgent(api, created.ID), "agentDeployments")
	awe.failRemoves.Store(true)

	rec := api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": created.ID, "action": "undeploy"})
	d := expectFailedDeployment[AgentDeployment](api, rec, msgAgentDeployFailed, "agentDeployments")
	if d.Action != entplatform.DeployActionUndeploy || d.Status != entplatform.DeployStatusFailed {
		t.Errorf("kept deployment = %+v, want a failed undeploy", d)
	}
	if strings.Contains(rec.Body.String(), "instance failed to stop") {
		t.Errorf("the upstream error reached the response: %s", rec.Body.String())
	}
}

func TestAgents_RenameToATakenNameIs409(t *testing.T) {
	api, _ := newAWEAPI(t)
	createAgent(api, "taken-agent")
	other := createAgent(api, "renamed-agent")

	rec := api.call(http.MethodPut, "/agents/"+other.ID, map[string]any{
		"name": "taken-agent", "description": other.Description, "systemPrompt": other.SystemPrompt,
	})
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)
}

func TestAgentsAndWorkflows_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, agentsTag, agentDeploymentsTag, workflowsTag, workflowDeploymentsTag)
	want := []string{
		"DELETE /agents/{id}", "DELETE /workflows/{id}", "GET /agentDeployments", "GET /agentDeployments/{id}", "GET /agents",
		"GET /agents/{id}", "GET /workflowDeployments", "GET /workflowDeployments/{id}", "GET /workflows", "GET /workflows/{id}",
		"POST /agentDeployments", "POST /agents", "POST /workflowDeployments", "POST /workflows",
		"PUT /agents/{id}", "PUT /agents/{id}/secretsDiff", "PUT /workflows/{id}",
	}
	if !slices.Equal(got, want) {
		t.Errorf("API key routes = %v, want %v", got, want)
	}
}

func TestAccess_RBACFlagOffIs404(t *testing.T) {
	t.Setenv(features.EnvVarName("rbac"), "false")
	api, _ := newAWEAPI(t)
	agent := createAgent(api, "no-access-agent")
	workflow := createWorkflow(api, "no-access-workflow")
	for _, path := range []string{"/agents/" + agent.ID + "/access", "/workflows/" + workflow.ID + "/access"} {
		expectStatus(t, api.call(http.MethodGet, path, nil), http.StatusNotFound)
	}
}

func TestAgents_AccessTakesEitherScope(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createAgent(api, "access-agent")
	path := "/agents/" + created.ID + "/access"

	for _, scope := range []string{rbac.ScopeRBACRead, rbac.ScopeAgentBuilderRead} {
		rec := api.call(http.MethodGet, path, nil, scope)
		expectStatus(t, rec, http.StatusOK)
		decodeList[UserAccess](t, rec)
	}
	expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, "/agents/"+missingID+"/access", nil), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodGet, "/agents/nope/access", nil), http.StatusBadRequest, "id")
}

func TestAgents_EmptyFieldsAreNull(t *testing.T) {
	api, _ := newAWEAPI(t)
	rec := api.call(http.MethodPost, "/agents", map[string]any{
		"name":         "null-fields-agent",
		"description":  "Agent the test creates.",
		"systemPrompt": "You help with tests.",
		"skills":       []map[string]any{{"name": "Lookup", "description": "Looks things up."}},
	})
	id := expectCreated[Agent](t, rec, "agents").ID

	data := rawData(t, api.call(http.MethodGet, "/agents/"+id, nil))
	expectNull(t, data, "toolsetExcludedTools", "skillConfigFields", "additionalConfigurations", "sourceSessionId",
		"sourceComponentName", "sourceComponentKind", "syncStatus", "deployed")
	expectEmptyList(t, data, "warnings")
	skill := data["skills"].([]any)[0].(map[string]any)
	expectNull(t, skill, "id")
	expectEmptyList(t, skill, "tags", "examples", "inputModes", "outputModes")

	deployed := deployAgent(api, id)
	expectCreated[AgentDeployment](t, deployed, "agentDeployments")
	expectNull(t, rawData(t, deployed), "failure")
}

func TestAgents_ToolRegistrationErrorsAreSAMSentences(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createAgent(api, "tool-errors-agent")
	upstream := "dial tcp mcp.example.com:443: connection refused"
	entry := func(name, status, agentError string) map[string]any {
		return map[string]any{"tool_type": "openapi", "name": name, "status": status, "error": agentError, "tool_count": 1, "tool_names": []any{"get_order"}}
	}
	api.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: created.InstanceName,
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
			URI: platpkg.ExtensionURIToolRegistrations,
			Params: map[string]any{"entries": []any{
				entry("failed", "failed", upstream),
				entry("lost", "ready", "2 tools discovered, 1 registered: "+upstream),
				entry("clean", "ready", ""),
			}},
		}}},
	})

	rec := api.call(http.MethodGet, "/agents/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Errorf("body carries the agent's error text: %s", rec.Body.String())
	}
	got := map[string]*string{}
	for _, r := range decodeData[Agent](t, rec).ToolRegistrations {
		got[*r.Name] = r.Error
	}
	if e := got["failed"]; e == nil || *e != msgToolEntryFailed {
		t.Errorf("failed entry error = %v, want %q", e, msgToolEntryFailed)
	}
	if e := got["lost"]; e == nil || *e != msgToolEntryLostTools {
		t.Errorf("ready entry with losses error = %v, want %q", e, msgToolEntryLostTools)
	}
	if e, sent := got["clean"]; !sent || e != nil {
		t.Errorf("clean entry error = %v (sent %t), want null", e, sent)
	}
}

func expectNull(t *testing.T, data map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if v, sent := data[key]; !sent || v != nil {
			t.Errorf("%s = %v (sent %t), want null", key, v, sent)
		}
	}
}

func expectEmptyList(t *testing.T, data map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if list, ok := data[key].([]any); !ok || len(list) != 0 {
			t.Errorf("%s = %v, want []", key, data[key])
		}
	}
}

func TestDeployedAgent_CoversTheConfig(t *testing.T) {
	shown := map[string]string{
		"name": "name", "description": "description", "system_prompt": "systemPrompt", "skills": "skills",
		"skill_refs": "skillRefs", "additional_configurations": "additionalConfigurations",
	}
	leftOut := map[string]string{
		"id":                    "the agent's own id",
		"type":                  "fixed at create, so it never differs from the deployed one",
		"model_provider":        "the deploy's data snapshot does not record it, so there is no deployed value to show",
		"is_ephemeral":          "test-agent bookkeeping, not deployed config",
		"expires_at":            "test-agent bookkeeping, not deployed config",
		"source_session_id":     "test-agent bookkeeping, not deployed config",
		"source_component_name": "test-agent bookkeeping, not deployed config",
		"source_component_kind": "test-agent bookkeeping, not deployed config",
		"created_by":            "the agent's audit field; deployedBy records the deploy",
		"updated_by":            "the agent's audit field; deployedBy records the deploy",
		"created_time":          "the agent's audit field; deployedTime records the deploy",
		"updated_time":          "the agent's audit field; deployedTime records the deploy",
	}
	expectDeployedCoversConfig(t, reflect.TypeFor[store.Agent](), reflect.TypeFor[DeployedAgent](), shown, leftOut)
}

func expectDeployedCoversConfig(t *testing.T, config, deployed reflect.Type, shown, leftOut map[string]string) {
	t.Helper()
	summary := jsonNames(deployed)
	for _, f := range reflect.VisibleFields(config) {
		column := f.Tag.Get("db")
		if _, ok := leftOut[column]; ok {
			continue
		}
		if name, ok := shown[column]; !ok || !summary[name] {
			t.Errorf("%s.%s (%s) is neither in %s nor left out with a reason", config, f.Name, column, deployed.Name())
		}
	}
}

func TestAgents_UpdateKeepsAStoredValueBelowItsMinimum(t *testing.T) {
	api, _ := newAWEAPI(t)
	seeded, err := entplatform.NewAgentService(store.NewAgentRepo(api.core.DB()), nil, nil, nil, nil, nil, slog.Default()).
		Create(t.Context(), entplatform.CreateAgentRequest{
			Name:                     "stored-below-minimum",
			Description:              "Agent the test creates.",
			SystemPrompt:             "You help with tests.",
			AdditionalConfigurations: json.RawMessage(`{"maxLlmCallsPerTask":0}`),
		}, "u")
	if err != nil {
		t.Fatalf("seed an agent saved before the minimum: %v", err)
	}
	update := func(configurations string) *httptest.ResponseRecorder {
		return api.call(http.MethodPut, "/agents/"+seeded.ID, map[string]any{
			"name":                     "stored-below-minimum",
			"description":              "Only the description changed.",
			"systemPrompt":             "You help with tests.",
			"additionalConfigurations": json.RawMessage(configurations),
		})
	}

	kept := update(`{"maxLlmCallsPerTask":0}`)
	expectStatus(t, kept, http.StatusOK)
	if warnings := decodeData[Agent](t, kept).Warnings; !slices.ContainsFunc(warnings, func(w string) bool {
		return strings.Contains(w, "maxLlmCallsPerTask: value 0 is below minimum 1")
	}) {
		t.Errorf("an unchanged stored value should save with a warning, got %v", warnings)
	}
	expectDetail(t, update(`{"maxLlmCallsPerTask":-1}`), http.StatusUnprocessableEntity, "additionalConfigurations")
	expectDetail(t, update(`{"maxLlmCallsPerTask":0,"agentCardPublishing":{"intervalSeconds":-1}}`), http.StatusUnprocessableEntity, "additionalConfigurations")
}

func TestAgents_InstructionBoundsComeFromThePlatformConfig(t *testing.T) {
	core, _ := newTestCoreWithConfig(t, map[string]any{
		"agent_instruction_min_chars": 7,
		"agent_instruction_max_chars": 42,
	})
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}

	for _, tc := range []struct {
		name   string
		prompt string
		want   int
	}{
		{"one under the minimum", strings.Repeat("a", 6), http.StatusUnprocessableEntity},
		{"exactly the minimum", strings.Repeat("a", 7), http.StatusCreated},
		{"exactly the maximum", strings.Repeat("a", 42), http.StatusCreated},
		{"one over the maximum", strings.Repeat("a", 43), http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := agentConfigurationsBody("bounds-"+strings.ReplaceAll(tc.name, " ", "-"), map[string]any{})
			body["systemPrompt"] = tc.prompt
			expectStatus(t, api.call(http.MethodPost, "/agents", body), tc.want)
		})
	}
}
