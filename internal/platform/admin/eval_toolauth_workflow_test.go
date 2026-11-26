// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func storeWorkflow(t *testing.T, repo *store.WorkflowRepo, name string, nodes ...map[string]any) *store.Workflow {
	t.Helper()
	return storeWorkflowConfig(t, repo, name, nil, nodes...)
}

func storeWorkflowConfig(t *testing.T, repo *store.WorkflowRepo, name string, extra map[string]any, nodes ...map[string]any) *store.Workflow {
	t.Helper()
	body := map[string]any{"nodes": nodes, "output_mapping": map[string]any{"result": "{{workflow.input}}"}}
	for k, v := range extra {
		body[k] = v
	}
	cfg, err := json.Marshal(map[string]any{"workflow": body})
	if err != nil {
		t.Fatal(err)
	}
	wf := &store.Workflow{Name: name, AppConfig: string(cfg), CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow %s: %v", name, err)
	}
	return wf
}

func wfAgentNode(id, agent string) map[string]any {
	return map[string]any{"id": id, "type": "agent", "agent_name": agent}
}

func wfNestedNode(id, workflowName string) map[string]any {
	return map[string]any{"id": id, "type": "workflow", "workflow_name": workflowName}
}

func TestWorkflowNodeAgents(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewWorkflowRepo(db)
	storeWorkflow(t, repo, "Research", wfAgentNode("fetch", "Researcher"), wfNestedNode("back", "Triage"))
	storeWorkflow(t, repo, "Triage",
		wfAgentNode("classify", "OrderClassifier"),
		map[string]any{"id": "lookup", "type": "tool", "tool_name": "load_artifact"},
		map[string]any{"id": "crm", "type": "tool", "connector": "crm_api"},
		wfAgentNode("summarize", "orderclassifier"),
		wfNestedNode("research", "Research"),
	)
	storeWorkflow(t, repo, "Partly YAML", wfAgentNode("first", "OrderClassifier"), wfNestedNode("elsewhere", "yaml_only_flow"))
	r := &evalWorkflowResolver{workflows: repo, cards: fakeCards{}}

	agents, complete, err := r.WorkflowNodeAgents(t.Context(), "Triage")

	if err != nil {
		t.Fatalf("list node agents: %v", err)
	}
	if !complete {
		t.Error("a workflow whose every nested workflow is stored, with a connector tool node, reads as incomplete")
	}
	want := []workflowNodeAgent{
		{AgentName: "OrderClassifier", NodeIDs: []string{"classify", "summarize"}},
		{AgentName: "Researcher", NodeIDs: []string{"research/fetch"}},
	}
	if fmt.Sprint(agents) != fmt.Sprint(want) {
		t.Errorf("agents = %+v, want %+v (tool nodes skipped, a nested workflow followed once even though it calls back)", agents, want)
	}

	if _, complete, err := r.WorkflowNodeAgents(t.Context(), "Partly YAML"); err != nil || complete {
		t.Errorf("a nested workflow with no stored config: complete = %v, err = %v; want incomplete", complete, err)
	}
	if agents, complete, err := r.WorkflowNodeAgents(t.Context(), "yaml_only_flow"); err != nil || complete || len(agents) != 0 {
		t.Errorf("a workflow deployed from YAML: %+v, complete = %v, err = %v; want nothing and incomplete", agents, complete, err)
	}
}

func deploySnapshot(t *testing.T, wf *store.Workflow, nodes ...map[string]any) *string {
	t.Helper()
	cfg := map[string]any{"nodes": nodes, "output_mapping": map[string]any{"result": "{{workflow.input}}"}, "max_call_depth": 3}
	out, err := marshalWorkflowAppsYAML(wf, wrapWorkflowAppConfig(wf, "", cfg))
	if err != nil {
		t.Fatal(err)
	}
	return &out
}

func rawSnapshot(yaml string) *string { return &yaml }

type workflowDeploymentRow struct {
	status, action, failureReason string
	snapshot                      *string
}

func TestWorkflowNodeAgentsFollowTheDeployedWorkflow(t *testing.T) {
	const stored, deployed, redeployed = "EditedAgent", "DeployedAgent", "RedeployedAgent"
	deploy := func(wf *store.Workflow, agent string) workflowDeploymentRow {
		return workflowDeploymentRow{status: DeployStatusSuccess, action: DeployActionDeploy, snapshot: deploySnapshot(t, wf, wfAgentNode("classify", agent))}
	}
	cases := []struct {
		name         string
		rows         func(wf *store.Workflow) []workflowDeploymentRow
		want         string
		unreadableAs string
	}{
		{name: "never deployed", rows: func(*store.Workflow) []workflowDeploymentRow { return nil }, want: stored},
		{name: "deployed", rows: func(wf *store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{deploy(wf, deployed)}
		}, want: deployed},
		{name: "undeployed", rows: func(wf *store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{deploy(wf, deployed), {status: DeployStatusSuccess, action: DeployActionUndeploy}}
		}, want: stored},
		{name: "deployed before snapshots were kept", rows: func(*store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{{status: DeployStatusSuccess, action: DeployActionDeploy}}
		}, want: stored},
		{name: "an empty snapshot", rows: func(*store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{{status: DeployStatusSuccess, action: DeployActionDeploy, snapshot: rawSnapshot("")}}
		}, want: stored},
		{name: "a newer update rejected before it ran", rows: func(wf *store.Workflow) []workflowDeploymentRow {
			rejected := deploy(wf, redeployed)
			rejected.status, rejected.action, rejected.failureReason = DeployStatusFailed, DeployActionUpdate, DeployFailureRejected
			return []workflowDeploymentRow{deploy(wf, deployed), rejected}
		}, want: deployed},
		{name: "a newer update that failed at runtime", rows: func(wf *store.Workflow) []workflowDeploymentRow {
			failed := deploy(wf, redeployed)
			failed.status, failed.action, failed.failureReason = DeployStatusFailed, DeployActionUpdate, DeployFailureRuntimeError
			return []workflowDeploymentRow{deploy(wf, deployed), failed}
		}, want: redeployed},
		{name: "a newer update", rows: func(wf *store.Workflow) []workflowDeploymentRow {
			update := deploy(wf, redeployed)
			update.action = DeployActionUpdate
			return []workflowDeploymentRow{deploy(wf, deployed), update}
		}, want: redeployed},
		{name: "an undeploy that failed at runtime", rows: func(wf *store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{deploy(wf, deployed), {status: DeployStatusFailed, action: DeployActionUndeploy, failureReason: DeployFailureRuntimeError}}
		}, want: stored},
		{name: "a snapshot with no apps", rows: func(*store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{{status: DeployStatusSuccess, action: DeployActionDeploy, snapshot: rawSnapshot("apps: []\n")}}
		}, unreadableAs: workflowConfigDeployed},
		{name: "a snapshot that is not YAML", rows: func(*store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{{status: DeployStatusSuccess, action: DeployActionDeploy, snapshot: rawSnapshot("apps: [secretvalue\n")}}
		}, unreadableAs: workflowConfigDeployed},
		{name: "a snapshot whose apps are not a list", rows: func(*store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{{status: DeployStatusSuccess, action: DeployActionDeploy, snapshot: rawSnapshot("apps: secretvalue\n")}}
		}, unreadableAs: workflowConfigDeployed},
		{name: "a snapshot with no app_config", rows: func(*store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{{status: DeployStatusSuccess, action: DeployActionDeploy, snapshot: rawSnapshot("apps:\n  - name: secretvalue\n")}}
		}, unreadableAs: workflowConfigDeployed},
		{name: "a snapshot that does not parse as a workflow", rows: func(wf *store.Workflow) []workflowDeploymentRow {
			return []workflowDeploymentRow{{status: DeployStatusSuccess, action: DeployActionDeploy,
				snapshot: deploySnapshot(t, wf, map[string]any{"id": "x", "type": "secretvalue"})}}
		}, unreadableAs: workflowConfigDeployed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := store.TestDB(t)
			repo := store.NewWorkflowRepo(db)
			deployments := store.NewWorkflowDeploymentRepo(db)
			wf := storeWorkflow(t, repo, "Triage", wfAgentNode("classify", stored))
			for i, row := range tc.rows(wf) {
				d := &store.WorkflowDeployment{WorkflowID: wf.ID, Status: row.status, Action: row.action, WorkflowConfigSnapshot: row.snapshot, CreatedBy: "u", CreatedTime: int64(1000 * (i + 1))}
				if row.failureReason != "" {
					d.FailureReason = &row.failureReason
				}
				if err := deployments.Create(t.Context(), d); err != nil {
					t.Fatalf("record deployment %d: %v", i, err)
				}
			}
			var logs bytes.Buffer
			r := &evalWorkflowResolver{workflows: repo, deployments: deployments, cards: fakeCards{}, logger: slog.New(slog.NewTextHandler(&logs, nil))}

			agents, complete, err := r.WorkflowNodeAgents(t.Context(), "Triage")

			if err != nil {
				t.Fatalf("list node agents: %v", err)
			}
			if tc.unreadableAs != "" {
				if complete || len(agents) != 0 {
					t.Errorf("agents = %+v, complete = %v; want none and incomplete", agents, complete)
				}
				if !strings.Contains(logs.String(), "workflowID="+wf.ID) || !strings.Contains(logs.String(), "configSource="+tc.unreadableAs) {
					t.Errorf("want one Warn naming the workflow and the %s config:\n%s", tc.unreadableAs, logs.String())
				}
				if strings.Contains(logs.String(), "secretvalue") {
					t.Errorf("the log quotes the config:\n%s", logs.String())
				}
				return
			}
			if !complete || len(agents) != 1 || agents[0].AgentName != tc.want {
				t.Errorf("agents = %+v, complete = %v; want %s", agents, complete, tc.want)
			}
			if logs.Len() != 0 {
				t.Errorf("a readable config logged:\n%s", logs.String())
			}
		})
	}
}

func TestWorkflowNodeAgentsReadsEachNestedWorkflowAsItRuns(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewWorkflowRepo(db)
	deployments := store.NewWorkflowDeploymentRepo(db)
	parent := storeWorkflow(t, repo, "Triage", wfNestedNode("sub", "Review"))
	child := storeWorkflow(t, repo, "Review", wfAgentNode("c", "EditedReviewer"))
	stored := storeWorkflow(t, repo, "Archive", wfAgentNode("d", "Archiver"))
	for i, d := range []*store.WorkflowDeployment{
		{WorkflowID: parent.ID, Status: DeployStatusSuccess, Action: DeployActionDeploy,
			WorkflowConfigSnapshot: deploySnapshot(t, parent, wfNestedNode("sub", "Review"), wfNestedNode("old", "Archive"))},
		{WorkflowID: child.ID, Status: DeployStatusSuccess, Action: DeployActionDeploy,
			WorkflowConfigSnapshot: deploySnapshot(t, child, wfAgentNode("c", "Reviewer"))},
	} {
		d.CreatedBy, d.CreatedTime = "u", int64(1000*(i+1))
		if err := deployments.Create(t.Context(), d); err != nil {
			t.Fatal(err)
		}
	}
	r := &evalWorkflowResolver{workflows: repo, deployments: deployments, cards: fakeCards{}}

	agents, complete, err := r.WorkflowNodeAgents(t.Context(), "Triage")

	want := []workflowNodeAgent{{AgentName: "Reviewer", NodeIDs: []string{"sub/c"}}, {AgentName: "Archiver", NodeIDs: []string{"old/d"}}}
	if err != nil || !complete || fmt.Sprint(agents) != fmt.Sprint(want) {
		t.Errorf("agents = %+v, complete %v, err %v; want %+v (%s never deployed, so read as stored)", agents, complete, err, want, stored.Name)
	}
}

func TestWorkflowNodeAgentsReportsAnUnreadableDeploymentHistory(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewWorkflowRepo(db)
	storeWorkflow(t, repo, "Triage", wfAgentNode("classify", "OrderClassifier"))
	if _, err := db.ExecContext(t.Context(), `DROP TABLE workflow_deployments`); err != nil {
		t.Fatal(err)
	}
	r := &evalWorkflowResolver{workflows: repo, deployments: store.NewWorkflowDeploymentRepo(db), cards: fakeCards{}}

	if agents, _, err := r.WorkflowNodeAgents(t.Context(), "Triage"); err == nil || !strings.Contains(err.Error(), "read deployed workflow") {
		t.Errorf("agents = %+v, err = %v; want the deployment read's error, not the stored row", agents, err)
	}
}

func TestWorkflowNodeAgentsLogsAnUnreadableConfigOnceUntilItIsFixed(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewWorkflowRepo(db)
	const brokenConfig = `{"workflow": {"nodes": [{"id": "x", "type": "nope"}]}}`
	broken := &store.Workflow{Name: "Broken", AppConfig: brokenConfig, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), broken, store.WorkflowBindings{}); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	r := &evalWorkflowResolver{workflows: repo, cards: fakeCards{}, logger: slog.New(slog.NewTextHandler(&logs, nil))}
	read := func(wantComplete bool) {
		t.Helper()
		if _, complete, err := r.WorkflowNodeAgents(t.Context(), "Broken"); err != nil || complete != wantComplete {
			t.Errorf("complete = %v, err = %v; want complete = %v", complete, err, wantComplete)
		}
	}
	setConfig := func(cfg string) {
		t.Helper()
		broken.AppConfig = cfg
		if err := repo.Update(t.Context(), broken, store.WorkflowBindings{}); err != nil {
			t.Fatal(err)
		}
	}
	const warning = "workflow definition did not parse"

	read(false)
	read(false)
	if n := strings.Count(logs.String(), warning); n != 1 || !strings.Contains(logs.String(), "workflowID="+broken.ID) || !strings.Contains(logs.String(), "configSource=stored") {
		t.Errorf("logged %d time(s), want once with the workflow id and its source:\n%s", n, logs.String())
	}
	if strings.Contains(logs.String(), "nope") {
		t.Errorf("the log quotes the authored config:\n%s", logs.String())
	}
	other := storeWorkflow(t, repo, "Also broken", map[string]any{"id": "y", "type": "nope"})
	if _, complete, err := r.WorkflowNodeAgents(t.Context(), other.Name); err != nil || complete {
		t.Errorf("a second unreadable workflow: complete = %v, err = %v; want incomplete", complete, err)
	}
	if !strings.Contains(logs.String(), "workflowID="+other.ID) {
		t.Errorf("a second unreadable workflow was not logged:\n%s", logs.String())
	}
	logs.Reset()

	setConfig(`{"workflow": {"nodes": [{"id": "x", "type": "agent", "agent_name": "Checker"}], "output_mapping": {"result": "{{workflow.input}}"}}}`)
	read(true)
	setConfig(brokenConfig)
	read(false)
	if n := strings.Count(logs.String(), warning); n != 1 {
		t.Errorf("logged %d time(s), want once more after the fixed config broke again:\n%s", n, logs.String())
	}

	setConfig("")
	r.unparsed.Clear()
	logs.Reset()
	read(false)
	if !strings.Contains(logs.String(), warning) {
		t.Error("an empty stored config leaves the checklist incomplete without a log")
	}
}

func TestWorkflowNodeAgentsFollowsNestedWorkflowsAsFarAsTheyRun(t *testing.T) {
	chain := func(t *testing.T, maxCallDepth int) []workflowNodeAgent {
		repo := store.NewWorkflowRepo(store.TestDB(t))
		for i := range 7 {
			nodes := []map[string]any{wfAgentNode("a", fmt.Sprintf("Agent%d", i))}
			if i < 6 {
				nodes = append(nodes, wfNestedNode("next", fmt.Sprintf("wf%d", i+1)))
			}
			storeWorkflowConfig(t, repo, fmt.Sprintf("wf%d", i), map[string]any{"max_call_depth": maxCallDepth}, nodes...)
		}
		agents, complete, err := (&evalWorkflowResolver{workflows: repo, cards: fakeCards{}}).WorkflowNodeAgents(t.Context(), "wf0")
		if err != nil || !complete {
			t.Fatalf("complete = %v, err = %v", complete, err)
		}
		return agents
	}
	names := func(agents []workflowNodeAgent) string {
		var out []string
		for _, a := range agents {
			out = append(out, a.AgentName)
		}
		return strings.Join(out, ",")
	}

	t.Run("max_call_depth 3", func(t *testing.T) {
		if got := names(chain(t, 3)); got != "Agent0,Agent1,Agent2,Agent3" {
			t.Errorf("agents = %s, want the four workflows that run", got)
		}
	})
	t.Run("max_call_depth 0, unlimited", func(t *testing.T) {
		if got := names(chain(t, 0)); got != "Agent0,Agent1,Agent2,Agent3,Agent4,Agent5,Agent6" {
			t.Errorf("agents = %s, want every workflow", got)
		}
	})
}

func TestWorkflowNodeAgentsListsANodeReachedTwice(t *testing.T) {
	repo := store.NewWorkflowRepo(store.TestDB(t))
	storeWorkflow(t, repo, "Shared", wfAgentNode("x", "Checker"))
	storeWorkflow(t, repo, "Top", wfNestedNode("a", "Shared"), wfNestedNode("b", "Shared"))

	agents, complete, err := (&evalWorkflowResolver{workflows: repo, cards: fakeCards{}}).WorkflowNodeAgents(t.Context(), "Top")

	if err != nil || !complete || len(agents) != 1 || !slices.Equal(agents[0].NodeIDs, []string{"a/x", "b/x"}) {
		t.Errorf("agents = %+v, complete %v, err %v; want Checker at both a/x and b/x", agents, complete, err)
	}
}

type fakeWorkflowNodes struct {
	agents   []workflowNodeAgent
	complete bool
	err      error
	asked    []string
}

func (f *fakeWorkflowNodes) WorkflowNodeAgents(_ context.Context, name string) ([]workflowNodeAgent, bool, error) {
	f.asked = append(f.asked, name)
	return f.agents, f.complete, f.err
}

func TestToolAuthGraphForAWorkflowExperimentListsTheWorkflowThenItsNodeAgents(t *testing.T) {
	nodes := &fakeWorkflowNodes{agents: []workflowNodeAgent{{AgentName: "OrderClassifier", NodeIDs: []string{"classify"}}}, complete: true}
	filters := &fakePeerFilters{managed: map[string]toolAuthPeerFilter{"OrderClassifier": denyAll()}}
	h := &evalHandler{
		db:            store.TestDB(t),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		workflowNodes: nodes,
		peerFilters:   filters,
	}
	exp := &store.EvalExperiment{ID: "01a15b00-0000-7000-8000-000000000001", TargetType: store.EvalTargetTypeWorkflow, TargetAgent: "Triage"}

	graph, err := h.toolAuthGraph(t.Context(), exp)

	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if !slices.Equal(nodes.asked, []string{"Triage"}) {
		t.Errorf("node agents read for %v, want the experiment's workflow", nodes.asked)
	}
	if len(graph.Agents) != 2 || graph.Agents[0].AgentName != "Triage" || !graph.Agents[0].IsTarget ||
		graph.Agents[1].AgentName != "OrderClassifier" || graph.Agents[1].IsTarget || !slices.Equal(graph.Agents[1].NodeIDs, []string{"classify"}) {
		t.Errorf("agents = %+v, want the workflow as target, then its node agent carrying its node", graph.Agents)
	}
	if graph.AllowListIsWildcard {
		t.Error("a fully listed workflow offers the picker")
	}

	agentExp := &store.EvalExperiment{ID: exp.ID, TargetType: store.EvalTargetTypeAgent, TargetAgent: "OrderClassifier"}
	agentGraph, err := h.toolAuthGraph(t.Context(), agentExp)
	if err != nil || len(agentGraph.Agents) != 1 || !agentGraph.Agents[0].IsTarget || len(nodes.asked) != 1 {
		t.Errorf("an agent experiment: %+v, err %v, node agents read %d times; want the agent as target and no workflow lookup", agentGraph.Agents, err, len(nodes.asked))
	}

	h.workflowNodes = nil
	unwired, err := h.toolAuthGraph(t.Context(), exp)
	if err != nil || len(unwired.Agents) != 1 || !unwired.AllowListIsWildcard {
		t.Errorf("no node-agent source: %+v, wildcard %v, err %v; want the workflow alone and the picker offered", unwired.Agents, unwired.AllowListIsWildcard, err)
	}
}

func TestDiscoverToolAuthGraphFromWorkflowNodeAgents(t *testing.T) {
	seeds := toolAuthGraphSeeds{target: "Triage", agents: []toolAuthGraphNode{
		{AgentName: "OrderClassifier", NodeIDs: []string{"classify"}},
		{AgentName: "Researcher", NodeIDs: []string{"research/fetch"}},
	}}
	filters := &fakePeerFilters{managed: map[string]toolAuthPeerFilter{
		"OrderClassifier": allow("CRM Lookup"),
		"Researcher":      denyAll(),
		"CRM Lookup":      denyAll(),
	}}
	pinned := []toolAuthGraphNode{{AgentName: "Researcher", AddedBy: "alice@example.com"}}

	graph, err := discoverToolAuthGraphFrom(context.Background(), seeds, pinned, filters, fakeMesh{})

	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	var names []string
	for _, node := range graph.Agents {
		names = append(names, node.AgentName)
	}
	if !slices.Equal(names, []string{"Triage", "OrderClassifier", "Researcher", "CRM Lookup"}) {
		t.Errorf("agents = %v, want the workflow, its node agents, then what they delegate to", names)
	}
	if !graph.Agents[0].IsTarget || graph.Agents[1].IsTarget {
		t.Errorf("targets = %v / %v, want only the workflow", graph.Agents[0].IsTarget, graph.Agents[1].IsTarget)
	}
	if researcher := graph.Agents[2]; !slices.Equal(researcher.NodeIDs, []string{"research/fetch"}) || researcher.AddedBy != "alice@example.com" {
		t.Errorf("a pinned node agent = %+v, want its node kept and who pinned it shown", researcher)
	}
	if len(graph.Agents[3].NodeIDs) != 0 {
		t.Errorf("a delegate carries node ids %v", graph.Agents[3].NodeIDs)
	}
	if graph.AllowListIsWildcard {
		t.Error("a fully listed workflow graph reads as incomplete")
	}
	if slices.Contains(filters.asked, "Triage") {
		t.Error("the workflow's own allow list was read; a workflow has none")
	}

	seeds.incomplete = true
	incomplete, err := discoverToolAuthGraphFrom(context.Background(), seeds, nil, filters, fakeMesh{})
	if err != nil || !incomplete.AllowListIsWildcard {
		t.Errorf("unlistable seeds: wildcard = %v, err = %v; want the picker offered", incomplete.AllowListIsWildcard, err)
	}
	if !incomplete.AdmitsUnenumerated(toolAuthMeshAgent{RoutingName: "agent_x", DisplayName: "Anything"}) {
		t.Error("with seeds that could not be listed, a running agent is refused")
	}
}
