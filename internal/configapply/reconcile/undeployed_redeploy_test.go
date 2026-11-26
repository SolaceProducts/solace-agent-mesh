// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func renderedPlanRows(plan *PlanResult) map[string]string {
	var buf bytes.Buffer
	RenderPlan(&buf, plan, FormatOptions{NoColor: true})
	rows := map[string]string{}
	for line := range strings.Lines(buf.String()) {
		fields := strings.Fields(line)
		if len(fields) > 2 && (fields[0] == "!" || fields[0] == "=") {
			rows[fields[1]] = strings.Join(fields, " ")
		}
	}
	return rows
}

func unusedClient() *Client {
	return NewClient(target.Resolved{URL: "http://127.0.0.1:0", Token: "tok"})
}

func TestWorkflowNotDeployed_UnchangedConfigIsDeployed(t *testing.T) {
	body := workflowSpecFixture(nil)
	desired := []resolve.ResolvedResource{
		resolvedWorkflow("never_deployed", body),
		resolvedWorkflow("running", body),
		resolvedWorkflow("failed_once", body),
		resolvedWorkflow("status_unknown", body),
	}
	statuses := map[string]string{
		"never_deployed": entplatform.DisplayStatusNotDeployed,
		"running":        entplatform.DisplayStatusDeployed,
		"failed_once":    entplatform.DisplayStatusDeployFailed,
		"status_unknown": "",
	}
	var actual []wire.WorkflowResponse
	for name, status := range statuses {
		a := actualWorkflow("id-"+name, name, body)
		a.DeploymentStatus = status
		actual = append(actual, a)
	}

	plan, err := planWorkflows(desired, actual, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	if len(plan.Unchanged) != len(desired) {
		t.Fatalf("Unchanged = %+v, want all %d workflows unchanged", plan.Unchanged, len(desired))
	}
	actions := map[string]string{}
	for _, op := range (&PlanResult{Workflows: plan}).Ops() {
		actions[op.Name] = op.Action
	}
	wantActions := map[string]string{
		"never_deployed": ActionRedeploy,
		"running":        ActionUnchanged,
		"failed_once":    ActionRedeploy,
		"status_unknown": ActionUnchanged,
	}
	if !reflect.DeepEqual(actions, wantActions) {
		t.Errorf("plan actions = %v, want %v", actions, wantActions)
	}

	outcome := applyWorkflows(t.Context(), unusedClient(), plan, ApplyOptions{})
	d := &fakeDeployer{}
	runWorkflowDeployments(t.Context(), d, io.Discard, outcome)

	wantCalls := []deployCall{{ID: "id-failed_once", Action: "update"}, {ID: "id-never_deployed", Action: "deploy"}}
	sort.Slice(d.calls, func(i, j int) bool { return d.calls[i].ID < d.calls[j].ID })
	if !reflect.DeepEqual(d.calls, wantCalls) {
		t.Errorf("deploy calls = %+v, want %+v", d.calls, wantCalls)
	}
	if len(outcome.Deployments) != 2 || outcome.Deployments[0].Kind != "workflow" || outcome.Deployments[0].Err != nil {
		t.Errorf("Deployments = %+v, want two successful workflow deploys", outcome.Deployments)
	}
}

func TestWorkflowNotDeployed_DeployFailureIsMirrored(t *testing.T) {
	body := workflowSpecFixture(nil)
	desired := []resolve.ResolvedResource{resolvedWorkflow("never_deployed", body)}
	a := actualWorkflow("id-1", "never_deployed", body)
	a.DeploymentStatus = entplatform.DisplayStatusNotDeployed
	plan, err := planWorkflows(desired, []wire.WorkflowResponse{a}, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	outcome := applyWorkflows(t.Context(), unusedClient(), plan, ApplyOptions{})
	d := &fakeDeployer{failOnID: map[string]error{"id-1": io.ErrUnexpectedEOF}}
	runWorkflowDeployments(t.Context(), d, io.Discard, outcome)

	if len(outcome.Failures) != 1 || outcome.Failures[0].Op.Name != "never_deployed" || outcome.Failures[0].Verb != "deploy" {
		t.Errorf("Failures = %+v, want one mirrored deploy failure", outcome.Failures)
	}
}

func TestWorkflowNotDeployed_PlanRowSaysWillDeploy(t *testing.T) {
	body := workflowSpecFixture(nil)
	never := actualWorkflow("id-never", "never", body)
	never.DeploymentStatus = entplatform.DisplayStatusNotDeployed
	live := actualWorkflow("id-live", "live", body)
	live.DeploymentStatus = entplatform.DisplayStatusDeployed
	failed := actualWorkflow("id-failed", "failed", body)
	failed.DeploymentStatus = entplatform.DisplayStatusDeployFailed
	wp, err := planWorkflows(
		[]resolve.ResolvedResource{resolvedWorkflow("never", body), resolvedWorkflow("live", body), resolvedWorkflow("failed", body)},
		[]wire.WorkflowResponse{never, live, failed}, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("planWorkflows: %v", err)
	}
	rows := renderedPlanRows(&PlanResult{Workflows: wp})
	if want := "! never " + outOfSyncLabel; rows["never"] != want {
		t.Errorf("never row = %q, want %q", rows["never"], want)
	}
	if want := "! failed " + outOfSyncLabel + lastDeployFailedSuffix; rows["failed"] != want {
		t.Errorf("failed row = %q, want %q", rows["failed"], want)
	}
	if want := "= live " + noChangeLabel; rows["live"] != want {
		t.Errorf("live row = %q, want %q", rows["live"], want)
	}
}

func TestGatewayNotDeployed_UnchangedConfigIsDeployed(t *testing.T) {
	live := liveWebuiGateway("")
	live.DeploymentStatus = entplatform.DisplayStatusNotDeployed
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(nil))}

	plan, err := planGateways(t.Context(), nil, desired, []wire.EntrypointResponse{live}, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	if ops := (&PlanResult{Gateways: plan}).Ops(); len(ops) != 1 || ops[0].Action != ActionRedeploy {
		t.Fatalf("plan ops = %+v, want one redeploy", ops)
	}
	outcome := applyGatewayUpserts(t.Context(), unusedClient(), plan)
	d := &fakeDeployer{}
	runGatewayDeployments(t.Context(), d, io.Discard, outcome)

	wantCalls := []deployCall{{ID: "id-1", Action: "deploy"}}
	if !reflect.DeepEqual(d.calls, wantCalls) {
		t.Errorf("deploy calls = %+v, want %+v", d.calls, wantCalls)
	}
}

func TestAgentNotDeployed_UnchangedConfigIsDeployed(t *testing.T) {
	values := map[string]any{"region": "us-east-1"}
	desired := []resolve.ResolvedResource{agentResolvedWithToolsetConfig(values)}
	live := agentResponseWithToolsetConfig(values)
	live.ID = "id-x"
	live.DeploymentStatus = entplatform.DisplayStatusNotDeployed

	plan, err := planAgents(t.Context(), nil, desired, []wire.AgentResponse{live}, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("Unchanged = %+v, want the agent unchanged", plan.Unchanged)
	}
	outcome := applyAgents(t.Context(), unusedClient(), plan, ApplyOptions{})
	d := &fakeDeployer{}
	runAgentDeployments(t.Context(), d, io.Discard, outcome)

	wantCalls := []deployCall{{ID: "id-x", Action: "deploy"}}
	if !reflect.DeepEqual(d.calls, wantCalls) {
		t.Errorf("deploy calls = %+v, want %+v", d.calls, wantCalls)
	}
}

func TestRenderPlan_NotDeployedGatewayAndAgentSayWillDeploy(t *testing.T) {
	notDeployed := entplatform.DisplayStatusNotDeployed
	plan := &PlanResult{
		Agents: &agentKindPlan{Kind: "agent", Unchanged: []agentOp{
			{Name: "agent-never", Actual: &wire.AgentResponse{DeploymentStatus: notDeployed}},
		}},
		Gateways: &gatewayKindPlan{Kind: "entrypoint", Unchanged: []gatewayOp{
			{Name: "entry-never", Actual: &wire.EntrypointResponse{DeploymentStatus: notDeployed}},
		}},
	}
	rows := renderedPlanRows(plan)
	for _, name := range []string{"agent-never", "entry-never"} {
		if want := "! " + name + " " + outOfSyncLabel; rows[name] != want {
			t.Errorf("%s row = %q, want %q", name, rows[name], want)
		}
	}
}
