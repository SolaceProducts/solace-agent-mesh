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
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type evaluationsToolAuthCall struct {
	instance, action, userID, credentialKey string
}

type evaluationsToolAuthAnswer struct {
	tools  []map[string]any
	begin  *a2a.AuthenticationRequiredData
	refuse map[string]error
}

type evaluationsToolAuthAWE struct {
	mu      sync.Mutex
	calls   []evaluationsToolAuthCall
	answers map[string]evaluationsToolAuthAnswer
}

func (a *evaluationsToolAuthAWE) handle(_ string, path []string, body json.RawMessage) (any, error) {
	if len(path) < 4 || path[0] != "instances" {
		return nil, errors.New("unexpected path")
	}
	var req struct {
		UserID        string `json:"userId"`
		CredentialKey string `json:"credentialKey"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	instance, action := path[1], path[len(path)-1]
	a.mu.Lock()
	a.calls = append(a.calls, evaluationsToolAuthCall{instance: instance, action: action, userID: req.UserID, credentialKey: req.CredentialKey})
	answer, answers := a.answers[instance]
	a.mu.Unlock()
	if !answers {
		return nil, fmt.Errorf("instance %q: %w", instance, control.ErrResourceNotFound)
	}
	if err := answer.refuse[action]; err != nil {
		return nil, err
	}
	switch action {
	case "query":
		if answer.tools == nil {
			return []any{}, nil
		}
		return answer.tools, nil
	case "begin":
		if answer.begin != nil {
			return answer.begin, nil
		}
		return evaluationsToolAuthBegin(instance, req.CredentialKey), nil
	}
	return map[string]string{"status": "ok"}, nil
}

func evaluationsToolAuthBegin(instance, credentialKey string) *a2a.AuthenticationRequiredData {
	return &a2a.AuthenticationRequiredData{
		Type:                      "authentication_required",
		TargetAgent:               instance,
		TargetAgentTaskID:         "preauth-" + credentialKey,
		ClientID:                  "platform-toolauth",
		AuthURI:                   "https://idp.example.com/authorize?state=state-" + credentialKey,
		AuthRequestFunctionCallID: "fc-" + credentialKey,
		CredentialKey:             credentialKey,
		CodeVerifier:              "verifier-" + credentialKey,
	}
}

func evaluationsToolAuthTool(credentialKey string) map[string]any {
	return map[string]any{"credentialKey": credentialKey, "interactive": true, "status": "not_connected"}
}

type evaluationsToolAuthRegistrations struct {
	broker.Broker
	mu       sync.Mutex
	messages []*broker.Message
	err      error
}

func (b *evaluationsToolAuthRegistrations) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if topic != a2a.ToolAuthRegisterTopic(evalNamespace) {
		return b.Broker.PublishGuaranteed(ctx, topic, msg)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	b.messages = append(b.messages, msg)
	return nil
}

func (b *evaluationsToolAuthRegistrations) published() []*broker.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*broker.Message(nil), b.messages...)
}

type evaluationsToolAuthMesh struct {
	experimentFixture
	awe           *evaluationsToolAuthAWE
	registrations *evaluationsToolAuthRegistrations
}

func newEvaluationsToolAuthMesh(t *testing.T) evaluationsToolAuthMesh {
	t.Helper()
	registrations := &evaluationsToolAuthRegistrations{}
	wrapTestBroker = func(b broker.Broker) broker.Broker {
		registrations.Broker = b
		return registrations
	}
	t.Cleanup(func() { wrapTestBroker = nil })
	m := evaluationsToolAuthMesh{
		experimentFixture: newExperimentFixture(t),
		awe:               &evaluationsToolAuthAWE{answers: map[string]evaluationsToolAuthAnswer{}},
		registrations:     registrations,
	}
	wrapTestBroker = nil
	startControlApp(t, m.api.core, "awe", m.awe.handle)
	return m
}

var evaluationsToolAuthExperiments atomic.Int64

func (m evaluationsToolAuthMesh) experiment(target, runAs string) (id, path string) {
	m.api.t.Helper()
	body := m.body(fmt.Sprintf("tool-auth-%d", evaluationsToolAuthExperiments.Add(1)), target)
	if runAs != "" {
		body["runAs"] = runAs
	}
	id = expectCreated[Experiment](m.api.t, m.api.call(http.MethodPost, experimentsPath, body), "evaluations/experiments").ID
	return id, experimentsPath + "/" + id + "/toolAuth"
}

func (m evaluationsToolAuthMesh) card(name, displayName string) {
	card := a2a.AgentCard{Name: name}
	if displayName != name {
		card.Capabilities.Extensions = []a2a.AgentExtension{{
			URI: platpkg.ExtensionURIDisplayName, Params: map[string]any{"display_name": displayName},
		}}
	}
	m.api.core.AgentRegistry().AddOrUpdate(card)
}

func (m evaluationsToolAuthMesh) managedAgent(name string, allowList ...string) string {
	m.api.t.Helper()
	agent := &store.Agent{Name: name, Type: "standard", CreatedBy: "tester", UpdatedBy: "tester"}
	if len(allowList) > 0 {
		raw, err := json.Marshal(map[string]any{"interAgentCommunication": map[string]any{"allowList": allowList}})
		if err != nil {
			m.api.t.Fatal(err)
		}
		agent.AdditionalConfigurations = raw
	}
	if err := store.NewAgentRepo(m.api.core.DB()).Create(m.api.t.Context(), agent, nil, nil, nil, nil, nil); err != nil {
		m.api.t.Fatalf("create agent %q: %v", name, err)
	}
	return entplatform.AgentInstanceName(agent)
}

func (m evaluationsToolAuthMesh) answer(instance string, answer evaluationsToolAuthAnswer) {
	m.awe.mu.Lock()
	defer m.awe.mu.Unlock()
	m.awe.answers[instance] = answer
}

func (m evaluationsToolAuthMesh) callsTo(action string) []evaluationsToolAuthCall {
	m.awe.mu.Lock()
	defer m.awe.mu.Unlock()
	var calls []evaluationsToolAuthCall
	for _, call := range m.awe.calls {
		if call.action == action {
			calls = append(calls, call)
		}
	}
	return calls
}

func (m evaluationsToolAuthMesh) checklist(path string) ExperimentToolAuth {
	m.api.t.Helper()
	rec := m.api.call(http.MethodGet, path, nil)
	expectStatus(m.api.t, rec, http.StatusOK)
	return decodeData[ExperimentToolAuth](m.api.t, rec)
}

func (m evaluationsToolAuthMesh) attribution(experimentID string) map[store.ToolAuthAttributionKey]store.ToolAuthAttribution {
	m.api.t.Helper()
	rows, err := store.ListToolAuthAttribution(m.api.t.Context(), m.api.core.DB(), experimentID)
	if err != nil {
		m.api.t.Fatalf("list attribution: %v", err)
	}
	return rows
}

func evaluationsToolAuthAgentNames(checklist ExperimentToolAuth) []string {
	names := make([]string, len(checklist.Agents))
	for i, agent := range checklist.Agents {
		names[i] = agent.AgentName
	}
	return names
}

func TestExperimentToolAuth_ControlCallsCarryTheExperimentsPrincipal(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	solver := m.managedAgent("Solver")
	m.answer(solver, evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("gmail")}})

	for runAs, principal := range map[string]string{"": rbac.EvalSystemUserSub, "team-bot": "system:team-bot"} {
		_, path := m.experiment("Solver", runAs)
		if got := m.checklist(path); got.RunAs != principal || len(got.Agents) != 1 || !got.Agents[0].Reachable {
			t.Errorf("runAs %q: checklist = %+v, want the reachable target alone, run as %s", runAs, got, principal)
		}
		expectStatus(t, m.api.call(http.MethodPut, path+"/begin", credentialBody("Solver", "gmail")), http.StatusOK)
		expectStatus(t, m.api.call(http.MethodPut, path+"/disconnect", credentialBody("Solver", "gmail")), http.StatusNoContent)
		for _, action := range []string{"query", "begin", "disconnect"} {
			calls := m.callsTo(action)
			last := calls[len(calls)-1]
			if last.instance != solver || last.userID != principal {
				t.Errorf("runAs %q: last %s call = %+v, want instance %s as %s", runAs, action, last, solver, principal)
			}
			if action != "query" && last.credentialKey != "gmail" {
				t.Errorf("runAs %q: %s credentialKey = %q, want gmail", runAs, action, last.credentialKey)
			}
		}
	}
}

func TestExperimentToolAuth_BeginRegistersTheOAuthState(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	solver := m.managedAgent("Solver")
	begin := evaluationsToolAuthBegin(solver, "gmail")
	begin.AuthURI = "https://idp.example.com/authorize?client_id=x&state=state-token-123"
	m.answer(solver, evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("gmail")}, begin: begin})
	experimentID, path := m.experiment("Solver", "team-bot")

	rec := m.api.call(http.MethodPut, path+"/begin", credentialBody("Solver", "gmail"))
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[ToolAuthAuthorization](t, rec); got.AuthURI != begin.AuthURI || got.CredentialKey != "gmail" {
		t.Errorf("begin = %+v, want the agent's authorization link for gmail", got)
	}

	published := m.registrations.published()
	if len(published) != 1 {
		t.Fatalf("registrations published = %d, want 1", len(published))
	}
	if published[0].TTL != 30*time.Minute {
		t.Errorf("registration TTL = %v, want 30m", published[0].TTL)
	}
	var registration a2a.ToolAuthStateRegistration
	if err := json.Unmarshal(published[0].Payload, &registration); err != nil {
		t.Fatal(err)
	}
	want := a2a.ToolAuthStateRegistration{
		State: "state-token-123", TargetAgent: solver, TaskID: "preauth-gmail", UserID: "system:team-bot",
		ClientID: "platform-toolauth", FunctionCallID: "fc-gmail", CredentialKey: "gmail", CodeVerifier: "verifier-gmail",
		TTLMs: (30 * time.Minute).Milliseconds(),
	}
	if registration != want {
		t.Errorf("registration = %+v, want %+v", registration, want)
	}
	if _, ok := m.attribution(experimentID)[store.ToolAuthAttributionKey{AgentName: "Solver", CredentialKey: "gmail"}]; !ok {
		t.Error("begin recorded no attribution for Solver/gmail")
	}
	if tool := m.checklist(path).Agents[0].Tools[0]; tool.LastAuthorizedBy == nil || *tool.LastAuthorizedBy == "" {
		t.Errorf("checklist tool = %+v, want who began the authorization", tool)
	}
}

func TestExperimentToolAuth_AgentControlErrors(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	cases := []struct {
		name string
		err  func(instance string) error
		want int
	}{
		{"an instance the executor does not know is 404", func(instance string) error {
			return fmt.Errorf("instance %q: %w", instance, control.ErrResourceNotFound)
		}, http.StatusNotFound},
		{"an instance still starting is 503", func(instance string) error {
			return fmt.Errorf("instance %q %s: %w", instance, control.InstanceNotRunning, control.ErrResourceNotFound)
		}, http.StatusServiceUnavailable},
		{"an executor without the endpoint is 502", func(instance string) error {
			return fmt.Errorf("unsupported: post instances/[%s toolAuth begin]", instance)
		}, http.StatusBadGateway},
		{"an instance that cannot manage tool auth is 422 on agentName", func(instance string) error {
			return fmt.Errorf("instance %q does not support tool auth management", instance)
		}, http.StatusUnprocessableEntity},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("Failing%d", i)
			instance := name + "Agent"
			m.card(instance, name)
			m.answer(instance, evaluationsToolAuthAnswer{refuse: map[string]error{"begin": tc.err(instance)}})
			_, path := m.experiment(name, "")

			rec := m.api.call(http.MethodPut, path+"/begin", credentialBody(name, "gmail"))
			expectStatus(t, rec, tc.want)
			if body := decodeError(t, rec); tc.want == http.StatusUnprocessableEntity && len(body.ValidationDetails["agentName"]) == 0 {
				t.Errorf("validationDetails = %v, want agentName", body.ValidationDetails)
			}
			if len(m.registrations.published()) != 0 {
				t.Error("a refused begin published a registration")
			}
		})
	}
}

func TestExperimentToolAuth_RefusedDisconnectKeepsTheAttribution(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.card("SolverAgent", "Solver")
	experimentID, path := m.experiment("Solver", "")
	m.answer("SolverAgent", evaluationsToolAuthAnswer{refuse: map[string]error{
		"disconnect": fmt.Errorf("credential key %q: %w", "gmail", control.ErrResourceNotFound),
	}})

	expectStatus(t, m.api.call(http.MethodPut, path+"/begin", credentialBody("Solver", "gmail")), http.StatusOK)
	rec := m.api.call(http.MethodPut, path+"/disconnect", credentialBody("Solver", "gmail"))
	expectStatus(t, rec, http.StatusNotFound)
	decodeError(t, rec)
	if _, ok := m.attribution(experimentID)[store.ToolAuthAttributionKey{AgentName: "Solver", CredentialKey: "gmail"}]; !ok {
		t.Error("a refused disconnect dropped the attribution")
	}
}

func TestExperimentToolAuth_ChecklistListsTheDelegationGraph(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.managedAgent("Solver", "Reporter")
	m.managedAgent("Reporter")
	m.card("uuid-solver", "Solver")
	m.card("uuid-reporter", "Reporter")
	m.answer("uuid-solver", evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("gmail")}})
	m.answer("uuid-reporter", evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("jira")}})
	_, path := m.experiment("Solver", "")

	got := m.checklist(path)
	if got.AllowListIsWildcard || len(got.Agents) != 2 {
		t.Fatalf("checklist = %+v, want a fully enumerated graph of the target and one peer", got)
	}
	target, peer := got.Agents[0], got.Agents[1]
	if target.AgentName != "Solver" || !target.IsTarget || !target.Reachable || len(target.Tools) != 1 || target.Tools[0].CredentialKey != "gmail" {
		t.Errorf("target = %+v, want reachable Solver with gmail", target)
	}
	if peer.AgentName != "Reporter" || peer.IsTarget || !peer.Reachable || len(peer.Tools) != 1 || peer.Tools[0].CredentialKey != "jira" {
		t.Errorf("peer = %+v, want reachable Reporter with jira", peer)
	}
}

func TestExperimentToolAuth_ACappedChecklistReportsTheWholeGraph(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	const peers, scanLimit = 70, 60
	m.managedAgent("Solver", "Peer*")
	m.card("SolverAgent", "Solver")
	for i := range peers {
		m.card(fmt.Sprintf("Peer%02d", i), fmt.Sprintf("Peer%02d", i))
	}
	_, path := m.experiment("Solver", "")

	got := m.checklist(path)
	if len(got.Agents) != scanLimit || got.TotalAgents != peers+1 || got.TotalRunningAgents != peers+1 || got.AgentScanLimit != scanLimit {
		t.Errorf("checklist lists %d agents of %d, %d running, scan limit %d; want %d of %d, %d running, limit %d",
			len(got.Agents), got.TotalAgents, got.TotalRunningAgents, got.AgentScanLimit, scanLimit, peers+1, peers+1, scanLimit)
	}
}

func TestExperimentToolAuth_AnEntrypointPrincipalIsReadOnly(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.card("SolverAgent", "Solver")
	m.answer("SolverAgent", evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("gmail")}})
	experimentID, path := m.experiment("Solver", "")
	m.exec(`UPDATE eval_experiments SET run_as = $1 WHERE id = $2`, rbac.DefaultSystemUserSub, experimentID)

	m.checklist(path)
	for _, action := range []string{"begin", "disconnect"} {
		rec := m.api.call(http.MethodPut, path+"/"+action, credentialBody("Solver", "gmail"))
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		details := strings.Join(decodeError(t, rec).ValidationDetails["runAs"], " ")
		if !strings.Contains(details, rbac.DefaultSystemUserSub) || !strings.Contains(details, "cannot be authorized or disconnected from here") {
			t.Errorf("%s runAs detail = %q, want it to name %s and this surface's remedy", action, details, rbac.DefaultSystemUserSub)
		}
	}
	if calls := append(m.callsTo("begin"), m.callsTo("disconnect")...); len(calls) != 0 {
		t.Errorf("the refused writes reached the agent: %+v", calls)
	}
}

func TestExperimentToolAuth_BeginFailsWhenTheStateCannotBeRegistered(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	cases := []struct {
		name          string
		publishFails  bool
		authURI       string
		targetAgent   string
		wantPublished bool
	}{
		{name: "the broker refuses the registration", publishFails: true, authURI: "https://idp.example.com/authorize?state=state-gmail"},
		{name: "the agent returns a script URI", authURI: "javascript:fetch('/steal')?state=state-gmail"},
		{name: "the agent names a target it cannot be called back on", authURI: "https://idp.example.com/authorize?state=state-gmail", targetAgent: "Solver Agent"},
		{name: "the agent returns a URI with no state", authURI: "https://idp.example.com/authorize?client_id=platform-toolauth"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("Unregistered%d", i)
			instance := name + "Agent"
			m.card(instance, name)
			begin := evaluationsToolAuthBegin(instance, "gmail")
			begin.AuthURI = tc.authURI
			if tc.targetAgent != "" {
				begin.TargetAgent = tc.targetAgent
			}
			m.answer(instance, evaluationsToolAuthAnswer{begin: begin})
			_, path := m.experiment(name, "")
			m.registrations.mu.Lock()
			m.registrations.err = nil
			if tc.publishFails {
				m.registrations.err = errors.New("broker down")
			}
			m.registrations.mu.Unlock()

			rec := m.api.call(http.MethodPut, path+"/begin", credentialBody(name, "gmail"))
			if rec.Code == http.StatusOK {
				t.Fatalf("begin = 200, want a failure: %s", rec.Body.String())
			}
			decodeError(t, rec)
			if strings.Contains(rec.Body.String(), "state-gmail") {
				t.Errorf("the response carries the unusable link: %s", rec.Body.String())
			}
			if got := len(m.registrations.published()); got != 0 {
				t.Errorf("registrations published = %d, want none", got)
			}
		})
	}
}

func TestExperimentToolAuth_AnUnmanagedAgentIsAddressedByItsCardName(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.card("SolverAgent", "Solver")
	m.answer("SolverAgent", evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("gmail")}})
	_, path := m.experiment("Solver", "")

	got := m.checklist(path)
	if len(got.Agents) != 1 || !got.Agents[0].Reachable || len(got.Agents[0].Tools) != 1 || got.Agents[0].Tools[0].CredentialKey != "gmail" {
		t.Errorf("agents = %+v, want Solver reachable with gmail", got.Agents)
	}
	if calls := m.callsTo("query"); len(calls) != 1 || calls[0].instance != "SolverAgent" {
		t.Errorf("query calls = %+v, want one to the card name SolverAgent", calls)
	}
}

func TestExperimentToolAuth_AnInstanceNamedTargetJoinsItsAttribution(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	solver := m.managedAgent("Solver", "Reporter")
	reporter := m.managedAgent("Reporter", "Solver")
	m.card(solver, "Solver")
	m.card(reporter, "Reporter")
	m.answer(solver, evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("gmail")}})
	m.answer(reporter, evaluationsToolAuthAnswer{})
	_, path := m.experiment(solver, "")

	expectStatus(t, m.api.call(http.MethodPut, path+"/begin", credentialBody(solver, "gmail")), http.StatusOK)

	got := m.checklist(path)
	if names := evaluationsToolAuthAgentNames(got); len(names) != 2 || names[0] != "Solver" || names[1] != "Reporter" || !got.Agents[0].IsTarget {
		t.Fatalf("agents = %v, want the target by its display name, then Reporter, once each", names)
	}
	if got.AllowListIsWildcard {
		t.Error("allowListIsWildcard = true, want the target's own allow list found under its instance name")
	}
	if tools := got.Agents[0].Tools; len(tools) != 1 || tools[0].LastAuthorizedBy == nil || *tools[0].LastAuthorizedBy == "" {
		t.Errorf("target tools = %+v, want gmail with who authorized it", tools)
	}
}

func TestExperimentToolAuth_ATargetThatIsNotRunningIsOneUnreachableRow(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	_, path := m.experiment("no-such-agent", "")

	got := m.checklist(path)
	if len(got.Agents) != 1 || got.Agents[0].AgentName != "no-such-agent" || !got.Agents[0].IsTarget || got.Agents[0].Reachable {
		t.Errorf("agents = %+v, want the target alone, unreachable", got.Agents)
	}
	if !got.AllowListIsWildcard {
		t.Error("allowListIsWildcard = false, want true: an agent with no row has no allow list to read")
	}
}

func TestExperimentToolAuth_AStoppedPeerIsUnreachable(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	solver := m.managedAgent("Solver", "Ghost")
	m.answer(solver, evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("gmail")}})
	_, path := m.experiment("Solver", "")

	got := m.checklist(path)
	if len(got.Agents) != 2 {
		t.Fatalf("agents = %+v, want the target and the stopped peer", got.Agents)
	}
	if target := got.Agents[0]; !target.Reachable || len(target.Tools) != 1 {
		t.Errorf("target = %+v, want it reachable with its credential", target)
	}
	if ghost := got.Agents[1]; ghost.AgentName != "Ghost" || ghost.Reachable || len(ghost.Tools) != 0 {
		t.Errorf("peer = %+v, want Ghost unreachable with no tools", ghost)
	}
}

func TestExperimentToolAuth_WritesAreLimitedToTheDelegationGraph(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.managedAgent("Solver", "Reporter")
	m.managedAgent("Reporter")
	for _, name := range []string{"Solver", "Reporter", "Stranger", "Open"} {
		m.card("uuid-"+strings.ToLower(name), name)
		m.answer("uuid-"+strings.ToLower(name), evaluationsToolAuthAnswer{})
	}
	_, enumerated := m.experiment("Solver", "")
	_, wildcard := m.experiment("Open", "")

	cases := []struct {
		name, path, agentName string
		allowed               bool
	}{
		{"the target agent", enumerated, "Solver", true},
		{"a discovered peer", enumerated, "Reporter", true},
		{"an agent outside the graph", enumerated, "Stranger", false},
		{"any running agent under a wildcard allow list", wildcard, "Stranger", true},
		{"an agent that is not running under a wildcard allow list", wildcard, "Nowhere", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			begin, disconnect := http.StatusUnprocessableEntity, http.StatusUnprocessableEntity
			if tc.allowed {
				begin, disconnect = http.StatusOK, http.StatusNoContent
			}
			calls, published := len(m.callsTo("begin"))+len(m.callsTo("disconnect")), len(m.registrations.published())
			expectStatus(t, m.api.call(http.MethodPut, tc.path+"/begin", credentialBody(tc.agentName, "gmail")), begin)
			expectStatus(t, m.api.call(http.MethodPut, tc.path+"/disconnect", credentialBody(tc.agentName, "gmail")), disconnect)
			if tc.allowed {
				return
			}
			if got := len(m.callsTo("begin")) + len(m.callsTo("disconnect")); got != calls {
				t.Errorf("the refused writes reached an agent %d times", got-calls)
			}
			if got := len(m.registrations.published()); got != published {
				t.Errorf("a refused begin published %d registrations", got-published)
			}
		})
	}
}

func TestExperimentToolAuth_BeginOnAPeerIsAddressedToThePeer(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.managedAgent("Solver", "Reporter")
	reporter := m.managedAgent("Reporter")
	m.answer(reporter, evaluationsToolAuthAnswer{})
	experimentID, path := m.experiment("Solver", "team-bot")

	expectStatus(t, m.api.call(http.MethodPut, path+"/begin", credentialBody("Reporter", "jira")), http.StatusOK)
	if calls := m.callsTo("begin"); len(calls) != 1 || calls[0].instance != reporter {
		t.Errorf("begin calls = %+v, want one to the peer's instance %s", calls, reporter)
	}
	published := m.registrations.published()
	if len(published) != 1 {
		t.Fatalf("registrations published = %d, want 1", len(published))
	}
	var registration a2a.ToolAuthStateRegistration
	if err := json.Unmarshal(published[0].Payload, &registration); err != nil {
		t.Fatal(err)
	}
	if registration.TargetAgent != reporter || registration.UserID != "system:team-bot" || registration.CredentialKey != "jira" {
		t.Errorf("registration = %+v, want it addressed to %s as system:team-bot", registration, reporter)
	}
	if _, ok := m.attribution(experimentID)[store.ToolAuthAttributionKey{AgentName: "Reporter", CredentialKey: "jira"}]; !ok {
		t.Error("no attribution row keyed by the peer")
	}
}

func TestExperimentToolAuth_DisconnectClearsOneAgentsAttribution(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.managedAgent("Solver", "Reporter")
	reporter := m.managedAgent("Reporter")
	m.answer(reporter, evaluationsToolAuthAnswer{})
	experimentID, path := m.experiment("Solver", "")
	for _, agentName := range []string{"Solver", "Reporter"} {
		if err := store.UpsertToolAuthAttribution(t.Context(), m.api.core.DB(), experimentID, agentName, "mcp_oauth2", "operator@test", 1700000000000); err != nil {
			t.Fatal(err)
		}
	}

	expectStatus(t, m.api.call(http.MethodPut, path+"/disconnect", credentialBody("Reporter", "mcp_oauth2")), http.StatusNoContent)
	rows := m.attribution(experimentID)
	if _, ok := rows[store.ToolAuthAttributionKey{AgentName: "Reporter", CredentialKey: "mcp_oauth2"}]; ok {
		t.Error("the disconnected peer's attribution survived")
	}
	if _, ok := rows[store.ToolAuthAttributionKey{AgentName: "Solver", CredentialKey: "mcp_oauth2"}]; !ok {
		t.Error("disconnecting the peer dropped the target's attribution for the same credential key")
	}
}

func TestExperimentToolAuth_PinningResolvesTheAgent(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.card("uuid-solver", "Solver")
	m.card("uuid-miro", "Miro Agent")
	m.managedAgent("Sealed")

	for _, agentName := range []string{"Miro Agent", "uuid-miro"} {
		t.Run("pinned as "+agentName, func(t *testing.T) {
			_, path := m.experiment("Solver", "")
			rec := m.api.call(http.MethodPost, path+"/agents", map[string]any{"agentName": agentName})
			expectStatus(t, rec, http.StatusCreated)
			pinned := decodeData[PinnedToolAuthAgent](t, rec)
			if pinned.AgentName != "Miro Agent" || pinned.AddedBy == "" {
				t.Fatalf("pinned = %+v, want the display name and who pinned it", pinned)
			}
			got := m.checklist(path)
			if len(got.Agents) != 2 || got.Agents[1].AgentName != "Miro Agent" || got.Agents[1].IsTarget ||
				got.Agents[1].AddedBy == nil || *got.Agents[1].AddedBy != pinned.AddedBy {
				t.Errorf("agents = %+v, want the target then the pinned agent with its adder", got.Agents)
			}
			expectStatus(t, m.api.call(http.MethodPost, path+"/agents", map[string]any{"agentName": agentName}), http.StatusConflict)
		})
	}

	_, path := m.experiment("Solver", "")
	expectStatus(t, m.api.call(http.MethodPost, path+"/agents", map[string]any{"agentName": "uuid-solver"}), http.StatusConflict)
	expectDetail(t, m.api.call(http.MethodPost, path+"/agents", map[string]any{"agentName": "Nowhere"}), http.StatusUnprocessableEntity, "agentName")
	_, sealed := m.experiment("Sealed", "")
	expectStatus(t, m.api.call(http.MethodPost, sealed+"/agents", map[string]any{"agentName": "Miro Agent"}), http.StatusUnprocessableEntity)
}

func TestExperimentToolAuth_UnpinningByRoutingNameLeavesTheCredential(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.card("uuid-solver", "Solver")
	m.card("uuid-miro", "Miro Agent")
	_, path := m.experiment("Solver", "")

	expectStatus(t, m.api.call(http.MethodPost, path+"/agents", map[string]any{"agentName": "uuid-miro"}), http.StatusCreated)
	expectStatus(t, m.api.call(http.MethodDelete, path+"/agents/uuid-miro", nil), http.StatusNoContent)
	if got := m.checklist(path); len(got.Agents) != 1 || !got.Agents[0].IsTarget {
		t.Errorf("agents after unpin = %+v, want the target alone", got.Agents)
	}
	expectStatus(t, m.api.call(http.MethodPost, path+"/agents", map[string]any{"agentName": "Miro Agent"}), http.StatusCreated)
	expectStatus(t, m.api.call(http.MethodDelete, path+"/agents/"+url.PathEscape("Miro Agent"), nil), http.StatusNoContent)
	if calls := m.callsTo("disconnect"); len(calls) != 0 {
		t.Errorf("unpinning reached the agent's disconnect: %+v", calls)
	}
}

func TestExperimentToolAuth_AnAmbiguousAgentNameIsRefused(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.managedAgent("Solver", "dataagent")
	m.card("uuid-solver", "Solver")
	m.card("agent-019e6c97", "Data Agent")
	m.card("dataagent", "dataagent")
	m.answer("dataagent", evaluationsToolAuthAnswer{})
	_, path := m.experiment("Solver", "")

	for _, action := range []string{"begin", "disconnect"} {
		rec := m.api.call(http.MethodPut, path+"/"+action, credentialBody("DATAAGENT", "gmail"))
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if details := strings.Join(decodeError(t, rec).ValidationDetails["agentName"], " "); !strings.Contains(details, "more than one running agent") {
			t.Errorf("%s agentName detail = %q, want it to name the ambiguity", action, details)
		}
	}
	expectStatus(t, m.api.call(http.MethodPut, path+"/begin", credentialBody("dataagent", "gmail")), http.StatusOK)
}
