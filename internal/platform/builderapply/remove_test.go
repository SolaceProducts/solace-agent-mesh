// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var removerScopes = append([]string{rbac.ScopeAgentBuilderDelete, rbac.ScopeWorkflowBuilderDelete}, allBuildScopes...)

func removal(session string, targets ...RemovalTarget) RemoveDroppedRequest {
	return RemoveDroppedRequest{BuilderSessionID: session, Resources: targets}
}

func agentTarget(id string) RemovalTarget    { return RemovalTarget{Kind: kindAgent, ID: id} }
func workflowTarget(id string) RemovalTarget { return RemovalTarget{Kind: kindWorkflow, ID: id} }

func removeAs(t *testing.T, svc *Service, who *auth.Subject, req RemoveDroppedRequest) *RemoveDroppedResponse {
	t.Helper()
	return svc.RemoveDropped(t.Context(), req, who)
}

func remover() *auth.Subject { return identityWith(removerScopes...) }

func resultFor(t *testing.T, resp *RemoveDroppedResponse, id string) RemovalResult {
	t.Helper()
	for _, r := range resp.Results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no result for %s in %+v", id, resp.Results)
	return RemovalResult{}
}

func withFailingUndeploy(t *testing.T, svc *Service) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	ctrl := control.NewClient(brk, "test-ns", control.WithTimeout(100*time.Millisecond))

	db := svc.deps.DB
	logger := discardLogger()
	connectorRepo := store.NewConnectorRepo(db)
	yamlSvc := entplatform.NewYAMLService("test-ns", logger)
	deployers := entplatform.NewDefaultDeployerRegistry(yamlSvc, connectorRepo, nil, nil, store.NewA2AAgentRepo(db), "test-ns", logger)
	svc.deps.AgentDeploy = entplatform.NewDeploymentService(store.NewDeploymentRepo(db), store.NewAgentRepo(db), connectorRepo, nil, yamlSvc, ctrl, deployers, logger)
	svc.deps.WorkflowDeploy = entplatform.NewWorkflowDeploymentService(store.NewWorkflowDeploymentRepo(db), store.NewWorkflowRepo(db), connectorRepo, nil, store.NewAgentRepo(db), nil, ctrl, "test-ns", logger)
}

func TestRemoveDropped_RemovesExactlyTheConfirmedResources(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("agent_to_keep"), agentComponent("agent_to_remove"), agentComponent("agent_also_dropped"))
	remove := agentNamed(t.Context(), t, svc, "agent_to_remove")

	resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(remove.ID)))
	if got := resultFor(t, resp, remove.ID); got.Status != removalRemoved || got.Name != "agent_to_remove" {
		t.Fatalf("result = %+v, want agent_to_remove removed", got)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"agent_also_dropped", "agent_to_keep"}) {
		t.Fatalf("agents = %v, want only the confirmed one gone: another dropped agent was not asked about and stays", got)
	}
}

func TestRemoveDropped_RemovesADroppedWorkflow(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("flow_target"), workflowComponent("order_flow", "order_flow", "flow_target"))
	wf := workflowNamed(t, svc, "order_flow")

	resp := removeAs(t, svc, remover(), removal(sessionA, workflowTarget(wf.ID)))
	if got := resultFor(t, resp, wf.ID); got.Status != removalRemoved {
		t.Fatalf("result = %+v, want removed", got)
	}
	if list, _ := svc.deps.listWorkflows(t.Context()); len(list) != 0 {
		t.Errorf("workflows = %+v, want none", list)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"flow_target"}) {
		t.Errorf("agents = %v, want the target agent untouched", got)
	}
}

func TestRemoveDropped_RefusesWhatTheSessionDidNotCreate(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedAgent(t, svc, "preexisting_agent")
	edited := agentComponent("preexisting_agent")
	edited.Content = strings.Replace(edited.Content, "You are a test agent that answers questions.", "You are an agent the session only rewrote.", 1)
	buildInSession(t, svc, sessionA, false, edited, agentComponent("session_made"))
	buildInSession(t, svc, sessionB, false, agentComponent("other_session_made"))
	seedAgent(t, svc, "never_touched")

	otherCaller := auth.NewTestSubject(auth.SubjectFields{
		UserID: "u-1", Email: "another@example.com", Authenticated: true, Scopes: removerScopes,
	})
	cases := []struct {
		name string
		who  *auth.Subject
		req  func() RemoveDroppedRequest
	}{
		{"updated only by the session", remover(), func() RemoveDroppedRequest {
			return removal(sessionA, agentTarget(agentNamed(t.Context(), t, svc, "preexisting_agent").ID))
		}},
		{"created by another session", remover(), func() RemoveDroppedRequest {
			return removal(sessionA, agentTarget(agentNamed(t.Context(), t, svc, "other_session_made").ID))
		}},
		{"never written by the Builder", remover(), func() RemoveDroppedRequest {
			return removal(sessionA, agentTarget(agentNamed(t.Context(), t, svc, "never_touched").ID))
		}},
		{"created by another caller", otherCaller, func() RemoveDroppedRequest {
			return removal(sessionA, agentTarget(agentNamed(t.Context(), t, svc, "session_made").ID))
		}},
		{"offered as the wrong kind", remover(), func() RemoveDroppedRequest {
			return removal(sessionA, workflowTarget(agentNamed(t.Context(), t, svc, "session_made").ID))
		}},
	}
	before := storedAgentNames(t, svc)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req()
			resp := removeAs(t, svc, tc.who, req)
			if got := resp.Results[0]; got.Status != removalRefused || got.Error == nil || got.Error.ErrorID == "" {
				t.Fatalf("result = %+v, want refused with a reason and an errorId", got)
			}
			if got := storedAgentNames(t, svc); !slices.Equal(got, before) {
				t.Fatalf("agents = %v, want %v: a refused removal touches nothing", got, before)
			}
		})
	}
}

func TestRemoveDropped_NeedsTheKindsDeleteScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("scoped_agent"), workflowComponent("scoped_flow", "scoped_flow", "scoped_agent"))
	agent := agentNamed(t.Context(), t, svc, "scoped_agent")
	wf := workflowNamed(t, svc, "scoped_flow")

	onlyWorkflowDelete := identityWith(append([]string{rbac.ScopeWorkflowBuilderDelete}, allBuildScopes...)...)
	resp := removeAs(t, svc, onlyWorkflowDelete, removal(sessionA, agentTarget(agent.ID), workflowTarget(wf.ID)))
	if got := resultFor(t, resp, agent.ID); got.Status != removalRefused || !strings.Contains(got.Error.Message, rbac.ScopeAgentBuilderDelete) {
		t.Errorf("agent result = %+v, want refused naming %s", got, rbac.ScopeAgentBuilderDelete)
	}
	if got := resultFor(t, resp, wf.ID); got.Status != removalRemoved {
		t.Errorf("workflow result = %+v, want removed: the caller holds the workflow delete scope", got)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"scoped_agent"}) {
		t.Errorf("agents = %v, want the agent kept", got)
	}
}

func TestRemoveDropped_AResourceAlreadyGoneIsReportedNotFound(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("vanishing_agent"))
	agent := agentNamed(t.Context(), t, svc, "vanishing_agent")
	if err := svc.deps.Agents.Delete(t.Context(), agent.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(agent.ID)))
	if got := resultFor(t, resp, agent.ID); got.Status != removalNotFound || got.Error != nil {
		t.Fatalf("result = %+v, want not_found with no error: nothing is left to remove", got)
	}
}

func TestRemoveDropped_AFailedUndeployKeepsTheResourceAndARetryRunsAgain(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	withFailingUndeploy(t, svc)
	buildInSession(t, svc, sessionA, false, agentComponent("running_agent"))
	agent := agentNamed(t.Context(), t, svc, "running_agent")
	seedAgentDeployment(t, svc, agent.ID, "deploy", "success")

	for attempt := 1; attempt <= 2; attempt++ {
		resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(agent.ID)))
		got := resultFor(t, resp, agent.ID)
		if got.Status != removalFailed || got.Error == nil || got.Error.ErrorID == "" {
			t.Fatalf("attempt %d: result = %+v, want failed with an errorId", attempt, got)
		}
		if names := storedAgentNames(t, svc); !slices.Equal(names, []string{"running_agent"}) {
			t.Fatalf("attempt %d: agents = %v, want the agent kept: deleting after a failed undeploy orphans its instance", attempt, names)
		}
	}

	rows, _ := svc.deps.Ledger.ListForSession(t.Context(), testActor.Email(), sessionA)
	if len(rows) != 1 || !rows[0].CreatedBySession {
		t.Errorf("session record = %+v, want it kept after a failed removal", rows)
	}
}

func TestRemoveDropped_WithoutADeployServiceAResourceThatMayBeRunningIsKept(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("maybe_running"), agentComponent("never_deployed"))
	running := agentNamed(t.Context(), t, svc, "maybe_running")
	idle := agentNamed(t.Context(), t, svc, "never_deployed")
	seedAgentDeployment(t, svc, running.ID, "deploy", "success")

	resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(running.ID), agentTarget(idle.ID)))
	if got := resultFor(t, resp, running.ID); got.Status != removalFailed {
		t.Errorf("running result = %+v, want failed", got)
	}
	if got := resultFor(t, resp, idle.ID); got.Status != removalRemoved {
		t.Errorf("idle result = %+v, want removed: it has no instance to orphan", got)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"maybe_running"}) {
		t.Errorf("agents = %v, want only the possibly running agent kept", got)
	}
}

func TestRemoveDropped_AFailedUndeployOfAWorkflowKeepsIt(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	withFailingUndeploy(t, svc)
	buildInSession(t, svc, sessionA, false, agentComponent("flow_target"), workflowComponent("live_flow", "live_flow", "flow_target"))
	wf := workflowNamed(t, svc, "live_flow")
	seedWorkflowDeployment(t, svc, wf.ID, "deploy", "success")

	resp := removeAs(t, svc, remover(), removal(sessionA, workflowTarget(wf.ID)))
	if got := resultFor(t, resp, wf.ID); got.Status != removalFailed {
		t.Fatalf("result = %+v, want failed", got)
	}
	if list, _ := svc.deps.listWorkflows(t.Context()); len(list) != 1 {
		t.Errorf("workflows = %d, want the workflow kept", len(list))
	}
}

func TestRemoveDropped_ARepeatedIDIsRemovedOnce(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("only_once"))
	agent := agentNamed(t.Context(), t, svc, "only_once")

	resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(agent.ID), agentTarget(strings.ToUpper(agent.ID))))
	if len(resp.Results) != 1 || resp.Results[0].Status != removalRemoved {
		t.Fatalf("results = %+v, want one removed result", resp.Results)
	}
}

func TestRemoveDropped_ARemovedResourceIsForgotten(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("forgotten_agent"))
	agent := agentNamed(t.Context(), t, svc, "forgotten_agent")
	removeAs(t, svc, remover(), removal(sessionA, agentTarget(agent.ID)))

	rows, _ := svc.deps.Ledger.ListForSession(t.Context(), testActor.Email(), sessionA)
	if len(rows) != 0 {
		t.Errorf("session record = %+v, want it empty after the removal", rows)
	}
}

func postRemoval(t *testing.T, svc *Service, body string, scopes []string) *httptest.ResponseRecorder {
	t.Helper()
	h := &handler{svc: svc, logger: discardLogger()}
	req := httptest.NewRequest(http.MethodPost, RemovalsPath, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "u-1", Email: testActor.Email(), Authenticated: true, Scopes: scopes,
	})))
	rec := httptest.NewRecorder()
	h.handleRemove(rec, req)
	return rec
}

func TestHandleRemove_WireShapeAndValidation(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("wire_agent"))
	agent := agentNamed(t.Context(), t, svc, "wire_agent")

	cases := []struct {
		name string
		body string
		want int
	}{
		{"unknown field", `{"builderSessionId":"` + sessionA + `","resources":[{"kind":"agent","id":"` + agent.ID + `"}],"force":true}`, http.StatusBadRequest},
		{"no session", `{"resources":[{"kind":"agent","id":"` + agent.ID + `"}]}`, http.StatusUnprocessableEntity},
		{"bad id", `{"builderSessionId":"` + sessionA + `","resources":[{"kind":"agent","id":"not-a-uuid"}]}`, http.StatusUnprocessableEntity},
		{"a kind it cannot remove", `{"builderSessionId":"` + sessionA + `","resources":[{"kind":"connector","id":"` + agent.ID + `"}]}`, http.StatusUnprocessableEntity},
		{"nothing to remove", `{"builderSessionId":"` + sessionA + `","resources":[]}`, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := postRemoval(t, svc, tc.body, removerScopes); rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"wire_agent"}) {
				t.Fatalf("agents = %v, a rejected request removes nothing", got)
			}
		})
	}

	rec := postRemoval(t, svc, `{"builderSessionId":"`+sessionA+`","resources":[{"kind":"agent","id":"`+agent.ID+`"}]}`, removerScopes)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []struct {
			Kind, ID, Name, Status string
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Results) != 1 || got.Results[0].Status != "removed" || got.Results[0].Name != "wire_agent" {
		t.Fatalf("body = %s (%v)", rec.Body.String(), err)
	}
}
