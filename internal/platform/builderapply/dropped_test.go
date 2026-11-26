// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	sessionA = "0197b2c4-0000-7000-8000-00000000000a"
	sessionB = "0197b2c4-0000-7000-8000-00000000000b"
)

func buildInSession(t *testing.T, svc *Service, session string, dryRun bool, components ...BuildComponentInput) *ApplyBuildResponse {
	t.Helper()
	return buildAs(t, svc, remover(), session, dryRun, components...)
}

func buildAs(t *testing.T, svc *Service, who *auth.Subject, session string, dryRun bool, components ...BuildComponentInput) *ApplyBuildResponse {
	t.Helper()
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{BuilderSessionID: session, DryRun: dryRun, Components: components}, who)
	if err != nil {
		t.Fatalf("build (dryRun=%v): %v", dryRun, err)
	}
	if resp.Summary.Failed != 0 || resp.Summary.Skipped != 0 {
		t.Fatalf("build (dryRun=%v) did not succeed: %+v", dryRun, resp.Operations)
	}
	return resp
}

func seedAgentDeployment(t *testing.T, svc *Service, agentID, action, status string) {
	t.Helper()
	if err := svc.deps.AgentDeployments.Create(t.Context(), &store.AgentDeployment{AgentID: agentID, Action: action, Status: status, CreatedBy: "seed"}); err != nil {
		t.Fatalf("seed agent deployment %s/%s: %v", action, status, err)
	}
}

func seedWorkflowDeployment(t *testing.T, svc *Service, workflowID, action, status string) {
	t.Helper()
	if err := svc.deps.WorkflowDeployments.Create(t.Context(), &store.WorkflowDeployment{WorkflowID: workflowID, Action: action, Status: status, CreatedBy: "seed"}); err != nil {
		t.Fatalf("seed workflow deployment %s/%s: %v", action, status, err)
	}
}

func droppedKindNames(resp *ApplyBuildResponse) []string {
	names := make([]string, 0, len(resp.Dropped))
	for _, d := range resp.Dropped {
		names = append(names, d.Kind+":"+d.Name)
	}
	return names
}

func TestDropped_ListsADeployedAgentThePlanDropsAndTouchesNothing(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("kept_agent"), agentComponent("dropped_agent"))
	dropped := agentNamed(t.Context(), t, svc, "dropped_agent")
	seedAgentDeployment(t, svc, dropped.ID, "deploy", "success")
	deploymentsBefore := countAgentDeployments(t, svc, dropped.ID)

	for _, dryRun := range []bool{true, false} {
		resp := buildInSession(t, svc, sessionA, dryRun, agentComponent("kept_agent"))
		want := DroppedResource{Kind: kindAgent, ID: dropped.ID, Name: "dropped_agent", CreatedBySession: true, Deployment: deploymentDeployed}
		if len(resp.Dropped) != 1 || resp.Dropped[0] != want {
			t.Fatalf("dryRun=%v: dropped = %+v, want [%+v]", dryRun, resp.Dropped, want)
		}
		if resp.DroppedCheckFailed {
			t.Errorf("dryRun=%v: droppedCheckFailed set for a list that could be read", dryRun)
		}
	}

	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"dropped_agent", "kept_agent"}) {
		t.Fatalf("agents = %v, want both still stored: a plan that drops a component removes nothing", got)
	}
	if after := countAgentDeployments(t, svc, dropped.ID); after != deploymentsBefore {
		t.Errorf("deployment records %d -> %d, the build must not undeploy what it drops", deploymentsBefore, after)
	}
}

func countAgentDeployments(t *testing.T, svc *Service, agentID string) int {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), svc.deps.DB, `SELECT COUNT(*) FROM agent_deployments WHERE agent_id = ?`, agentID)
	if err != nil {
		t.Fatalf("count deployments: %v", err)
	}
	return n
}

func TestDropped_ReportsEachDeploymentStateApart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		history [][2]string
		want    string
	}{
		{"never deployed", nil, deploymentNotDeployed},
		{"deployed", [][2]string{{"deploy", "success"}}, deploymentDeployed},
		{"updated", [][2]string{{"deploy", "success"}, {"update", "success"}}, deploymentDeployed},
		{"deploy failed", [][2]string{{"deploy", "failed"}}, deploymentDeployFailed},
		{"update failed", [][2]string{{"deploy", "success"}, {"update", "failed"}}, deploymentDeployFailed},
		{"undeployed", [][2]string{{"deploy", "success"}, {"undeploy", "success"}}, deploymentNotDeployed},
		{"undeploy failed", [][2]string{{"deploy", "success"}, {"undeploy", "failed"}}, deploymentUndeployFailed},
		{"redeployed after a failed undeploy", [][2]string{{"deploy", "success"}, {"undeploy", "failed"}, {"deploy", "success"}}, deploymentDeployed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			buildInSession(t, svc, sessionA, false, agentComponent("state_probe"), agentComponent("state_keeper"))
			probe := agentNamed(t.Context(), t, svc, "state_probe")
			for _, step := range tc.history {
				seedAgentDeployment(t, svc, probe.ID, step[0], step[1])
			}

			resp := buildInSession(t, svc, sessionA, true, agentComponent("state_keeper"))
			if len(resp.Dropped) != 1 || resp.Dropped[0].Deployment != tc.want {
				t.Fatalf("dropped = %+v, want one resource in state %q", resp.Dropped, tc.want)
			}
		})
	}
}

func TestDropped_ListsADroppedWorkflowWithItsState(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("flow_target"), workflowComponent("order_flow", "order_flow", "flow_target"))
	wf := workflowNamed(t, svc, "order_flow")
	seedWorkflowDeployment(t, svc, wf.ID, "deploy", "success")
	seedWorkflowDeployment(t, svc, wf.ID, "undeploy", "failed")

	resp := buildInSession(t, svc, sessionA, true, agentComponent("flow_target"))
	want := DroppedResource{Kind: kindWorkflow, ID: wf.ID, Name: "order_flow", CreatedBySession: true, Deployment: deploymentUndeployFailed}
	if len(resp.Dropped) != 1 || resp.Dropped[0] != want {
		t.Fatalf("dropped = %+v, want [%+v]", resp.Dropped, want)
	}
}

func TestDropped_AResourceTheSessionOnlyUpdatedIsListedAsNotCreatedByIt(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedAgent(t, svc, "someone_elses_agent")
	edited := agentComponent("someone_elses_agent")
	edited.Content = strings.Replace(edited.Content, "You are a test agent that answers questions.", "You are an agent the Builder session rewrote.", 1)
	buildInSession(t, svc, sessionA, false, edited, agentComponent("session_agent"))
	other := agentNamed(t.Context(), t, svc, "someone_elses_agent")

	resp := buildInSession(t, svc, sessionA, true, agentComponent("session_agent"))
	want := DroppedResource{Kind: kindAgent, ID: other.ID, Name: "someone_elses_agent", CreatedBySession: false, Deployment: deploymentNotDeployed}
	if len(resp.Dropped) != 1 || resp.Dropped[0] != want {
		t.Fatalf("dropped = %+v, want [%+v]: an overwritten resource is reported but never as created by the session", resp.Dropped, want)
	}
}

func TestDropped_AResourceTheSessionNeverWroteIsNotListed(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedAgent(t, svc, "somebody_elses_agent")
	buildInSession(t, svc, sessionA, false, agentComponent("session_agent"))

	resp := buildInSession(t, svc, sessionA, true, agentComponent("session_agent"))
	if len(resp.Dropped) != 0 {
		t.Fatalf("dropped = %+v, want none: only what this session wrote can be dropped by it", resp.Dropped)
	}
}

func TestDropped_NothingWithoutASessionOrForAnotherCallerOrSession(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("scoped_a"), agentComponent("scoped_b"))

	otherCaller := auth.NewTestSubject(auth.SubjectFields{
		UserID: "u-1", Email: "another@example.com", Authenticated: true, Scopes: testActor.Scopes(),
	})
	for name, resp := range map[string]*ApplyBuildResponse{
		"no session id":  buildInSession(t, svc, "", true, agentComponent("scoped_b")),
		"other session":  buildInSession(t, svc, sessionB, true, agentComponent("scoped_b")),
		"other caller":   buildAs(t, svc, otherCaller, sessionA, true, agentComponent("scoped_b")),
		"all components": buildInSession(t, svc, sessionA, true, agentComponent("scoped_a"), agentComponent("scoped_b")),
	} {
		if len(resp.Dropped) != 0 {
			t.Errorf("%s: dropped = %+v, want none", name, resp.Dropped)
		}
	}
}

func TestDropped_ADryRunRecordsNothing(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedAgent(t, svc, "someone_elses_agent")
	edited := agentWithInstruction("someone_elses_agent", "You are an agent the session would rewrite.")

	for name, component := range map[string]BuildComponentInput{"a create": agentComponent("previewed_only"), "an update": edited} {
		resp := buildInSession(t, svc, sessionA, true, component)
		if op := resp.Operations[0]; op.Status != statusPlanned {
			t.Fatalf("%s: operation %+v, want planned", name, op)
		}
	}

	rows, err := svc.deps.Ledger.ListForSession(t.Context(), testActor.Email(), sessionA)
	if err != nil || len(rows) != 0 {
		t.Fatalf("session record after a dry run = %+v, %v; want empty", rows, err)
	}
}

func TestDropped_AComponentTheApplyWritesOntoIsNeverListed(t *testing.T) {
	t.Parallel()
	cases := map[string]func(storedID string) []BuildComponentInput{
		"renamed by id": func(id string) []BuildComponentInput {
			return []BuildComponentInput{withResourceID(agentComponent("refund_handler"), id), agentComponent("untouched_agent")}
		},
		"case only": func(string) []BuildComponentInput {
			return []BuildComponentInput{agentComponent("Refund_Orchestrator"), agentComponent("untouched_agent")}
		},
		"unchanged": func(string) []BuildComponentInput {
			return []BuildComponentInput{agentComponent("refund_orchestrator"), agentComponent("untouched_agent")}
		},
	}
	for name, components := range cases {
		for _, dryRun := range []bool{true, false} {
			t.Run(name+map[bool]string{true: "/dry run", false: "/apply"}[dryRun], func(t *testing.T) {
				t.Parallel()
				svc := newTestService(t)
				buildInSession(t, svc, sessionA, false, agentComponent("refund_orchestrator"), agentComponent("untouched_agent"))
				stored := agentNamed(t.Context(), t, svc, "refund_orchestrator")

				resp := buildInSession(t, svc, sessionA, dryRun, components(stored.ID)...)
				if len(resp.Dropped) != 0 {
					t.Errorf("dropped = %v, want none: the agent is the one the plan writes onto", droppedKindNames(resp))
				}
			})
		}
	}
}

func TestDropped_ARenameThatLeavesAnotherAgentBehindListsOnlyThatOne(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("first_agent"), agentComponent("second_agent"))
	first := agentNamed(t.Context(), t, svc, "first_agent")

	resp := buildInSession(t, svc, sessionA, true, withResourceID(agentComponent("first_agent_renamed"), first.ID))
	if got := droppedKindNames(resp); !slices.Equal(got, []string{"agent:second_agent"}) {
		t.Fatalf("dropped = %v, want only second_agent: first_agent is paired by id", got)
	}
}

func TestDropped_ADryRunAndTheApplyReportTheSameList(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("agent_one"), agentComponent("agent_two"), agentComponent("agent_three"))

	preview := buildInSession(t, svc, sessionA, true, agentComponent("agent_two"))
	applied := buildInSession(t, svc, sessionA, false, agentComponent("agent_two"))
	if !slices.Equal(preview.Dropped, applied.Dropped) || len(applied.Dropped) != 2 {
		t.Fatalf("preview %+v and apply %+v must name the same two dropped agents", preview.Dropped, applied.Dropped)
	}
}

func TestDropped_AResourceDeletedElsewhereIsForgottenNotListed(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("kept_here"), agentComponent("deleted_elsewhere"))
	gone := agentNamed(t.Context(), t, svc, "deleted_elsewhere")
	if err := svc.deps.Agents.Delete(t.Context(), gone.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	holdsGone := func() bool {
		rows, _ := svc.deps.Ledger.ListForSession(t.Context(), testActor.Email(), sessionA)
		return slices.ContainsFunc(rows, func(row store.BuilderSessionResource) bool { return row.ResourceID == gone.ID })
	}

	preview := buildInSession(t, svc, sessionA, true, agentComponent("kept_here"))
	if len(preview.Dropped) != 0 {
		t.Fatalf("dropped = %+v, want none: the resource no longer exists", preview.Dropped)
	}
	if !holdsGone() {
		t.Fatal("a dry run forgot a resource: it writes nothing")
	}

	applied := buildInSession(t, svc, sessionA, false, agentComponent("kept_here"))
	if len(applied.Dropped) != 0 {
		t.Fatalf("dropped = %+v, want none", applied.Dropped)
	}
	if holdsGone() {
		t.Error("session record still holds the deleted resource after an apply")
	}
}

func TestDropped_ACheckThatCannotRunSaysSo(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("check_kept"), agentComponent("check_dropped"))

	noAgentRead := make([]string, 0, len(testActor.Scopes()))
	for _, scope := range testActor.Scopes() {
		if scope != rbac.ScopeAgentBuilderRead {
			noAgentRead = append(noAgentRead, scope)
		}
	}
	caller := identityWith(noAgentRead...)
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{BuilderSessionID: sessionA, DryRun: true, Components: []BuildComponentInput{connectorComponent("check_connector")}}, caller)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !resp.DroppedCheckFailed || len(resp.Dropped) != 0 {
		t.Fatalf("dropped = %+v, droppedCheckFailed = %v; want an unreadable check reported as failed, not as nothing dropped", resp.Dropped, resp.DroppedCheckFailed)
	}
}

func TestDropped_WireShapeIsAnEmptyListNotNull(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	body := `{"dryRun":true,"components":[{"kind":"agent","name":"wire_agent","content":` + jsonString(agentComponent("wire_agent").Content) + `}]}`

	rec := postBuild(t, svc, body, allBuildScopes)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := string(raw["dropped"]); got != "[]" {
		t.Errorf("dropped = %s, want []", got)
	}
	if _, present := raw["droppedCheckFailed"]; present {
		t.Errorf("droppedCheckFailed sent when the check ran")
	}
}

func TestDropped_ARequestWithABadSessionIDIsRejected(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	body := `{"builderSessionId":"not-a-uuid","components":[{"kind":"agent","name":"wire_agent","content":` + jsonString(agentComponent("wire_agent").Content) + `}]}`
	if rec := postBuild(t, svc, body, allBuildScopes); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
