// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	toolAuthTargetAgent  = "support_agent"
	toolAuthAuthURI      = "https://idp.example.com/authorize?state=state-1"
	toolAuthStoreDownKey = "store_down"
)

type fakeToolAuthAWE struct {
	mu    sync.Mutex
	calls []string
}

func (a *fakeToolAuthAWE) handle(_ string, path []string, body json.RawMessage) (any, error) {
	if len(path) < 4 || path[0] != "instances" {
		return nil, errors.New("unexpected path")
	}
	instance, action := path[1], path[len(path)-1]
	a.mu.Lock()
	a.calls = append(a.calls, instance+"/"+action)
	a.mu.Unlock()
	switch action {
	case "query":
		return toolAuthCredentials(instance)
	case "begin":
		if err := requireHeldCredential(instance, body); err != nil {
			return nil, err
		}
		return map[string]any{
			"type": "authentication_required", "target_agent": instance, "credential_key": "google_drive",
			"auth_uri": toolAuthAuthURI, "gateway_task_id": nil,
		}, nil
	default:
		if err := requireHeldCredential(instance, body); err != nil {
			return nil, err
		}
		return map[string]string{"status": "ok"}, nil
	}
}

func requireHeldCredential(instance string, body json.RawMessage) error {
	var req struct {
		CredentialKey string `json:"credentialKey"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	if req.CredentialKey == toolAuthStoreDownKey {
		return errors.New("credential store unavailable at https://vault.internal/v1/secret?token=abc")
	}
	held, err := toolAuthCredentials(instance)
	if err != nil {
		return nil
	}
	for _, credential := range held.([]any) {
		if credential.(map[string]any)["credentialKey"] == req.CredentialKey {
			return nil
		}
	}
	return fmt.Errorf("credential key %q: %w", req.CredentialKey, control.ErrResourceNotFound)
}

func toolAuthCredentials(instance string) (any, error) {
	connected := map[string]any{
		"credentialKey": "google_drive", "displayName": "Google Drive", "authType": "oauth2",
		"interactive": true, "status": "connected", "expiresAt": "2030-01-02T03:04:05Z",
	}
	plain := map[string]any{"credentialKey": "jira", "interactive": false, "status": "not_connected"}
	switch {
	case instance == toolAuthTargetAgent:
		return []any{connected, plain}, nil
	case strings.HasPrefix(instance, "interactive_"):
		return []any{connected}, nil
	case strings.HasPrefix(instance, "plain_"):
		return []any{plain}, nil
	}
	return nil, errors.New("instance is not answering")
}

func (a *fakeToolAuthAWE) called(call string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Contains(a.calls, call)
}

type toolAuthFixture struct {
	experimentFixture
	awe          *fakeToolAuthAWE
	experimentID string
	path         string
}

func newToolAuthFixture(t *testing.T, meshAgents ...string) toolAuthFixture {
	t.Helper()
	f := toolAuthFixture{experimentFixture: newExperimentFixture(t), awe: &fakeToolAuthAWE{}}
	startControlApp(t, f.api.core, "awe", f.awe.handle)
	for _, name := range meshAgents {
		f.api.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: name})
	}
	f.experimentID = f.create("tool-auth-experiment", toolAuthTargetAgent).ID
	f.path = experimentsPath + "/" + f.experimentID + "/toolAuth"
	return f
}

func (f toolAuthFixture) checklist() map[string]any {
	f.api.t.Helper()
	rec := f.api.call(http.MethodGet, f.path, nil)
	expectStatus(f.api.t, rec, http.StatusOK)
	return rawData(f.api.t, rec)
}

func (f toolAuthFixture) agent(name string) map[string]any {
	f.api.t.Helper()
	for _, a := range f.checklist()["agents"].([]any) {
		if agent := a.(map[string]any); agent["agentName"] == name {
			return agent
		}
	}
	f.api.t.Fatalf("checklist has no agent %q", name)
	return nil
}

func credentialBody(agentName, credentialKey string) map[string]any {
	return map[string]any{"agentName": agentName, "credentialKey": credentialKey}
}

func TestExperimentToolAuth_Checklist(t *testing.T) {
	f := newToolAuthFixture(t, "mute_agent")
	expectStatus(t, f.api.call(http.MethodPost, f.path+"/agents", map[string]any{"agentName": "mute_agent"}), http.StatusCreated)

	rec := f.api.call(http.MethodGet, f.path, nil)
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	if raw["runAs"] != "system:eval" || raw["allowListIsWildcard"] != true || raw["totalAgents"] != 2.0 || raw["totalRunningAgents"] != 1.0 || raw["agentScanLimit"] != 60.0 {
		t.Errorf("checklist = %v, want the built-in system user, a wildcard graph of 2 agents, 1 running, scan limit 60", raw)
	}
	got := decodeData[ExperimentToolAuth](t, rec)
	if len(got.Agents) != 2 || got.Agents[0].AgentName != toolAuthTargetAgent || !got.Agents[0].IsTarget || !got.Agents[0].Reachable || len(got.Agents[0].Tools) != 2 {
		t.Fatalf("agents = %+v, want the reachable target with both credentials, then the added agent", got.Agents)
	}

	target := f.agent(toolAuthTargetAgent)
	expectNull(t, target, "addedBy")
	connected := target["tools"].([]any)[0].(map[string]any)
	expectISOTimes(t, connected, "expiresTime")
	expectNull(t, connected, "lastAuthorizedBy", "lastAuthorizedTime")
	expectAbsent(t, connected, "expiresAt", "lastAuthorizedAt")
	if connected["displayName"] != "Google Drive" || connected["authType"] != "oauth2" || connected["status"] != "connected" || connected["interactive"] != true {
		t.Errorf("connected credential = %v", connected)
	}
	notConnected := target["tools"].([]any)[1].(map[string]any)
	expectNull(t, notConnected, "displayName", "authType", "expiresTime", "lastAuthorizedBy", "lastAuthorizedTime")

	mute := f.agent("mute_agent")
	expectEmptyList(t, mute, "tools")
	if mute["reachable"] != false || mute["addedBy"] == nil {
		t.Errorf("added agent that does not answer = %v, want unreachable, with who added it", mute)
	}
}

func TestExperimentToolAuth_BeginAndDisconnect(t *testing.T) {
	f := newToolAuthFixture(t)

	rec := f.api.call(http.MethodPut, f.path+"/begin", credentialBody(toolAuthTargetAgent, "google_drive"))
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	expectISOTimes(t, raw, "expiresTime")
	expectAbsent(t, raw, "expiresAt")
	if begun := decodeData[ToolAuthAuthorization](t, rec); begun.AuthURI != toolAuthAuthURI || begun.CredentialKey != "google_drive" {
		t.Errorf("begun = %+v, want the agent's authorization link for google_drive", begun)
	}
	if !f.awe.called(toolAuthTargetAgent + "/begin") {
		t.Error("begin did not reach the agent")
	}
	credential := f.agent(toolAuthTargetAgent)["tools"].([]any)[0].(map[string]any)
	expectISOTimes(t, credential, "lastAuthorizedTime")
	if credential["lastAuthorizedBy"] == nil {
		t.Error("lastAuthorizedBy is null after begin, want who began it")
	}

	expectStatus(t, f.api.call(http.MethodPut, f.path+"/disconnect", credentialBody(toolAuthTargetAgent, "google_drive")), http.StatusNoContent)
	if !f.awe.called(toolAuthTargetAgent + "/disconnect") {
		t.Error("disconnect did not reach the agent")
	}
	credential = f.agent(toolAuthTargetAgent)["tools"].([]any)[0].(map[string]any)
	expectNull(t, credential, "lastAuthorizedBy", "lastAuthorizedTime")
}

func TestExperimentToolAuth_CredentialTheAgentDoesNotHoldIs404(t *testing.T) {
	f := newToolAuthFixture(t)

	for _, action := range []string{"begin", "disconnect"} {
		rec := f.api.call(http.MethodPut, f.path+"/"+action, credentialBody(toolAuthTargetAgent, "not_a_credential"))
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
		if !f.awe.called(toolAuthTargetAgent + "/" + action) {
			t.Errorf("%s did not reach the agent", action)
		}
	}
}

func TestExperimentToolAuth_AgentRefusalIs502(t *testing.T) {
	f := newToolAuthFixture(t)

	for _, action := range []string{"begin", "disconnect"} {
		rec := f.api.call(http.MethodPut, f.path+"/"+action, credentialBody(toolAuthTargetAgent, toolAuthStoreDownKey))
		expectStatus(t, rec, http.StatusBadGateway)
		decodeError(t, rec)
		for _, leaked := range []string{"vault.internal", "token=abc", "credential store"} {
			if strings.Contains(rec.Body.String(), leaked) {
				t.Errorf("%s body carries %q: %s", action, leaked, rec.Body.String())
			}
		}
	}
}

func TestExperimentToolAuth_NoAgentExecutorIs503(t *testing.T) {
	wrapTestBroker = func(b broker.Broker) broker.Broker { return unansweredRequestBroker{b} }
	t.Cleanup(func() { wrapTestBroker = nil })
	f := newExperimentFixture(t)
	path := experimentsPath + "/" + f.create("no-executor", toolAuthTargetAgent).ID + "/toolAuth"

	for action, failure := range map[string]string{"begin": "Unable to begin tool authorization", "disconnect": "Unable to disconnect tool authorization"} {
		rec := f.api.call(http.MethodPut, path+"/"+action, credentialBody(toolAuthTargetAgent, "google_drive"))
		expectStatus(t, rec, http.StatusServiceUnavailable)
		if body := decodeError(t, rec); body.Message != failure || len(body.ValidationDetails) != 0 {
			t.Errorf("%s = %+v, want %q alone", action, body, failure)
		}
		for _, leaked := range []string{"control request", "deadline", "not running"} {
			if strings.Contains(rec.Body.String(), leaked) {
				t.Errorf("%s body carries %q: %s", action, leaked, rec.Body.String())
			}
		}
	}
}

type unansweredRequestBroker struct{ broker.Broker }

const unansweredRequestWait = 100 * time.Millisecond

func (b unansweredRequestBroker) Request(ctx context.Context, topic string, msg *broker.Message, _ time.Duration) (*broker.Message, error) {
	return b.Broker.Request(ctx, topic, msg, unansweredRequestWait)
}

func TestExperimentToolAuth_Validation(t *testing.T) {
	f := newToolAuthFixture(t, "interactive_peer")

	for _, action := range []string{"begin", "disconnect"} {
		path := f.path + "/" + action
		for name, body := range map[string]string{
			"truncated JSON": `{"agentName":`,
			"unknown field":  `{"agentName":"a","credentialKey":"b","colour":"red"}`,
			"wrong type":     `{"agentName":7}`,
		} {
			if rec := f.api.call(http.MethodPut, path, body); rec.Code != http.StatusBadRequest {
				t.Errorf("%s with %s = %d, want 400", action, name, rec.Code)
			}
		}
		for name, body := range map[string]any{"empty body": map[string]any{}, "blank names": credentialBody("  ", " ")} {
			rec := f.api.call(http.MethodPut, path, body)
			expectStatus(t, rec, http.StatusUnprocessableEntity)
			details := decodeError(t, rec).ValidationDetails
			if len(details["agentName"]) == 0 || len(details["credentialKey"]) == 0 {
				t.Errorf("%s with %s: validationDetails = %v, want both agentName and credentialKey", action, name, details)
			}
		}
		expectDetail(t, f.api.call(http.MethodPut, path, credentialBody("stranger", "google_drive")), http.StatusUnprocessableEntity, "agentName")
	}

	for name, body := range map[string]string{"truncated JSON": `{"agentName":`, "unknown field": `{"agentName":"a","colour":"red"}`, "wrong type": `{"agentName":7}`} {
		if rec := f.api.call(http.MethodPost, f.path+"/agents", body); rec.Code != http.StatusBadRequest {
			t.Errorf("add agent with %s = %d, want 400", name, rec.Code)
		}
	}
	for _, body := range []map[string]any{{}, {"agentName": "  "}} {
		expectDetail(t, f.api.call(http.MethodPost, f.path+"/agents", body), http.StatusUnprocessableEntity, "agentName")
	}
	expectDetail(t, f.api.call(http.MethodPost, f.path+"/agents", map[string]any{"agentName": "stranger"}), http.StatusUnprocessableEntity, "agentName")
}

func TestExperimentToolAuth_UnknownIDs(t *testing.T) {
	f := newToolAuthFixture(t)
	for _, route := range []struct {
		method, suffix string
		body           any
	}{
		{http.MethodGet, "", nil},
		{http.MethodPut, "/begin", credentialBody(toolAuthTargetAgent, "google_drive")},
		{http.MethodPut, "/disconnect", credentialBody(toolAuthTargetAgent, "google_drive")},
		{http.MethodGet, "/candidateAgents", nil},
		{http.MethodPost, "/agents", map[string]any{"agentName": toolAuthTargetAgent}},
		{http.MethodDelete, "/agents/" + toolAuthTargetAgent, nil},
	} {
		bad := f.api.call(route.method, experimentsPath+"/not-a-uuid/toolAuth"+route.suffix, route.body)
		expectDetail(t, bad, http.StatusBadRequest, "experimentId")
		missing := f.api.call(route.method, experimentsPath+"/"+missingID+"/toolAuth"+route.suffix, route.body)
		expectStatus(t, missing, http.StatusNotFound)
		decodeError(t, missing)
	}
}

func TestExperimentToolAuth_CandidateAgents(t *testing.T) {
	f := newToolAuthFixture(t, "interactive_a", "interactive_b", "interactive_c", "plain_d", "mute_e")
	path := f.path + "/candidateAgents"

	all := decodeList[ToolAuthCandidateAgent](t, f.api.call(http.MethodGet, path, nil))
	names := make([]string, len(all.Data))
	for i, c := range all.Data {
		names[i] = c.AgentName
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"interactive_a", "interactive_b", "interactive_c"}) || all.Meta.Pagination.Count != 3 {
		t.Errorf("candidates = %v of %d, want only the agents with a credential a person can authorize", names, all.Meta.Pagination.Count)
	}

	first := decodeList[ToolAuthCandidateAgent](t, f.api.call(http.MethodGet, path+"?pageSize=2", nil))
	second := decodeList[ToolAuthCandidateAgent](t, f.api.call(http.MethodGet, path+"?pageSize=2&pageNumber=2", nil))
	if len(first.Data) != 2 || len(second.Data) != 1 || first.Meta.Pagination.TotalPages != 2 || first.Data[0] == second.Data[0] || first.Data[1] == second.Data[0] {
		t.Errorf("pages of 2 = %v then %v, pagination %+v, want 2 then the remaining 1", first.Data, second.Data, first.Meta.Pagination)
	}
	if beyond := decodeList[ToolAuthCandidateAgent](t, f.api.call(http.MethodGet, path+"?pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}
	expectDetail(t, f.api.call(http.MethodGet, path+"?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectStatus(t, f.api.call(http.MethodGet, path+"?pageNumber=ten", nil), http.StatusBadRequest)
}

func TestExperimentToolAuth_CandidateAgentsNoneIsAnEmptyList(t *testing.T) {
	f := newToolAuthFixture(t)
	rec := f.api.call(http.MethodGet, f.path+"/candidateAgents", nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeList[ToolAuthCandidateAgent](t, rec); len(got.Data) != 0 || got.Meta.Pagination.Count != 0 {
		t.Errorf("candidates with an empty mesh = %+v, want []", got)
	}
}

func TestExperimentToolAuth_AddAndRemoveAgent(t *testing.T) {
	f := newToolAuthFixture(t, "interactive_peer")

	rec := f.api.call(http.MethodPost, f.path+"/agents", map[string]any{"agentName": "interactive_peer"})
	expectStatus(t, rec, http.StatusCreated)
	expectLocation(t, rec, "/evaluations/experiments/"+f.experimentID+"/toolAuth/agents/interactive_peer")
	raw := rawData(t, rec)
	expectISOTimes(t, raw, "addedTime")
	expectAbsent(t, raw, "addedAt", "id")
	if added := decodeData[PinnedToolAuthAgent](t, rec); added.AgentName != "interactive_peer" || added.AddedBy == "" {
		t.Errorf("added = %+v, want the agent and who added it", added)
	}
	if peer := f.agent("interactive_peer"); peer["addedBy"] == nil || peer["reachable"] != true {
		t.Errorf("checklist entry = %v, want the added agent, reachable, with who added it", peer)
	}

	conflict := f.api.call(http.MethodPost, f.path+"/agents", map[string]any{"agentName": "interactive_peer"})
	expectStatus(t, conflict, http.StatusConflict)
	decodeError(t, conflict)
	expectStatus(t, f.api.call(http.MethodPost, f.path+"/agents", map[string]any{"agentName": toolAuthTargetAgent}), http.StatusUnprocessableEntity)

	expectStatus(t, f.api.call(http.MethodDelete, f.path+"/agents/interactive_peer", nil), http.StatusNoContent)
	missing := f.api.call(http.MethodDelete, f.path+"/agents/interactive_peer", nil)
	expectStatus(t, missing, http.StatusNotFound)
	decodeError(t, missing)
	for _, name := range []string{toolAuthTargetAgent, "never added", "no.such-agent"} {
		neverAdded := f.api.call(http.MethodDelete, f.path+"/agents/"+strings.ReplaceAll(name, " ", "%20"), nil)
		expectStatus(t, neverAdded, http.StatusNotFound)
		decodeError(t, neverAdded)
	}
	for _, a := range f.checklist()["agents"].([]any) {
		if a.(map[string]any)["agentName"] == "interactive_peer" {
			t.Error("the removed agent is still on the checklist")
		}
	}
}

func TestExperimentToolAuth_AddAgentNotRunningIs422(t *testing.T) {
	f := newToolAuthFixture(t)
	rec := f.api.call(http.MethodPost, f.path+"/agents", map[string]any{"agentName": "not_running"})
	expectDetail(t, rec, http.StatusUnprocessableEntity, "agentName")
	if got := decodeError(t, rec).ValidationDetails["agentName"][0]; got != "agentName must name a running agent" {
		t.Errorf("agentName detail = %q, want the service's sentence", got)
	}
	if strings.Contains(rec.Body.String(), "agent_name") {
		t.Errorf("body names a snake_case field: %s", rec.Body.String())
	}
}

func TestExperimentToolAuth_Scopes(t *testing.T) {
	f := newToolAuthFixture(t, "interactive_peer")
	credential := credentialBody(toolAuthTargetAgent, "google_drive")

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, f.path + "/begin", credential},
		{http.MethodPut, f.path + "/disconnect", credential},
		{http.MethodPost, f.path + "/agents", map[string]any{"agentName": "interactive_peer"}},
		{http.MethodDelete, f.path + "/agents/interactive_peer", nil},
	} {
		if rec := f.api.call(call.method, call.path, call.body, rbac.ScopeEvaluationRead); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a reader = %d, want 403", call.method, call.path, rec.Code)
		}
	}
	expectStatus(t, f.api.call(http.MethodGet, f.path, nil, rbac.ScopeEvaluationRead), http.StatusOK)
	expectStatus(t, f.api.call(http.MethodGet, f.path+"/candidateAgents", nil, rbac.ScopeEvaluationRead), http.StatusOK)
	for _, path := range []string{f.path, f.path + "/candidateAgents"} {
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeEvaluationInvoke), http.StatusForbidden)
	}

	expectStatus(t, f.api.call(http.MethodPut, f.path+"/begin", credential, rbac.ScopeEvaluationInvoke), http.StatusOK)
	expectStatus(t, f.api.call(http.MethodPut, f.path+"/disconnect", credential, rbac.ScopeEvaluationInvoke), http.StatusNoContent)
	for _, scope := range []string{rbac.ScopeEvaluationInvoke, rbac.ScopeEvaluationUpdate, rbac.ScopeEvaluationDelete} {
		expectStatus(t, f.api.call(http.MethodPost, f.path+"/agents", map[string]any{"agentName": "interactive_peer"}, scope), http.StatusForbidden)
	}
	expectStatus(t, f.api.call(http.MethodPost, f.path+"/agents", map[string]any{"agentName": "interactive_peer"}, rbac.ScopeEvaluationCreate), http.StatusCreated)
	for _, scope := range []string{rbac.ScopeEvaluationCreate, rbac.ScopeEvaluationUpdate} {
		expectStatus(t, f.api.call(http.MethodDelete, f.path+"/agents/interactive_peer", nil, scope), http.StatusForbidden)
	}
	expectStatus(t, f.api.call(http.MethodDelete, f.path+"/agents/interactive_peer", nil, rbac.ScopeEvaluationDelete), http.StatusNoContent)
}

func TestToolAuthAgentNodeIDsAreAListNeverNull(t *testing.T) {
	got := toExperimentToolAuth(entplatform.ToolAuthChecklist{Agents: []entplatform.ToolAuthAgent{
		{AgentName: "Triage", IsTarget: true},
		{AgentName: "OrderClassifier", NodeIDs: []string{"classify", "triage/review"}},
	}})

	raw, err := json.Marshal(got.Agents)
	if err != nil {
		t.Fatal(err)
	}
	var agents []map[string]any
	if err := json.Unmarshal(raw, &agents); err != nil {
		t.Fatal(err)
	}
	if ids, ok := agents[0]["nodeIds"].([]any); !ok || len(ids) != 0 {
		t.Errorf("an agent with no nodes: nodeIds = %v, want []", agents[0]["nodeIds"])
	}
	if ids := fmt.Sprint(agents[1]["nodeIds"]); ids != "[classify triage/review]" {
		t.Errorf("a node agent: nodeIds = %s, want its nodes", ids)
	}
}
