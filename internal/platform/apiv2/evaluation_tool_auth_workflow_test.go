// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func evaluationsWorkflowAgentNode(id, agentName string) map[string]any {
	return map[string]any{"id": id, "type": "agent", "agent_name": agentName}
}

func evaluationsWorkflowNestedNode(id, workflowName string) map[string]any {
	return map[string]any{"id": id, "type": "workflow", "workflow_name": workflowName}
}

func (m evaluationsToolAuthMesh) workflow(name string, nodes ...map[string]any) {
	m.api.t.Helper()
	cfg, err := json.Marshal(map[string]any{"workflow": map[string]any{
		"nodes": nodes, "output_mapping": map[string]any{"result": "{{workflow.input}}"},
	}})
	if err != nil {
		m.api.t.Fatal(err)
	}
	wf := &store.Workflow{Name: name, AppConfig: string(cfg), CreatedBy: "tester", UpdatedBy: "tester"}
	if err := store.NewWorkflowRepo(m.api.core.DB()).Create(m.api.t.Context(), wf, store.WorkflowBindings{}); err != nil {
		m.api.t.Fatalf("create workflow %q: %v", name, err)
	}
}

func (m evaluationsToolAuthMesh) workflowExperiment() (id, path string) {
	m.api.t.Helper()
	m.card("uuid-triage", "Triage")
	m.answer("uuid-triage", evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("crm")}})
	id, path = m.experiment("Triage", "")
	m.exec(`UPDATE eval_experiments SET target_type = $1 WHERE id = $2`, store.EvalTargetTypeWorkflow, id)
	return id, path
}

func (m evaluationsToolAuthMesh) orderClassifier() {
	m.managedAgent("OrderClassifier")
	m.card("uuid-classifier", "OrderClassifier")
	m.answer("uuid-classifier", evaluationsToolAuthAnswer{tools: []map[string]any{evaluationsToolAuthTool("gmail")}})
}

func TestExperimentToolAuth_AWorkflowsChecklistNamesEachAgentsNodes(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.orderClassifier()
	m.workflow("Review", evaluationsWorkflowAgentNode("review", "OrderClassifier"))
	m.workflow("Triage", evaluationsWorkflowAgentNode("classify", "OrderClassifier"), evaluationsWorkflowNestedNode("triage", "Review"))
	_, path := m.workflowExperiment()

	got := m.checklist(path)
	if len(got.Agents) != 2 {
		t.Fatalf("agents = %+v, want the workflow and its node agent", got.Agents)
	}
	workflow, node := got.Agents[0], got.Agents[1]
	if workflow.AgentName != "Triage" || !workflow.IsTarget || len(workflow.NodeIDs) != 0 || len(workflow.Tools) != 1 || workflow.Tools[0].CredentialKey != "crm" {
		t.Errorf("workflow = %+v, want the target with its own tool nodes' credential", workflow)
	}
	if node.AgentName != "OrderClassifier" || !slices.Equal(node.NodeIDs, []string{"classify", "triage/review"}) || len(node.Tools) != 1 {
		t.Errorf("node agent = %+v, want its nodes and its credential", node)
	}
}

func TestExperimentToolAuth_AWorkflowsWritesAreLimitedToItsGraph(t *testing.T) {
	cases := []struct {
		name      string
		nodes     []map[string]any
		agentName string
		allowed   bool
	}{
		{"the workflow itself", nil, "Triage", true},
		{"a node agent", nil, "OrderClassifier", true},
		{"an agent outside a fully listed workflow", nil, "Stranger", false},
		{"a running agent when the workflow's nodes cannot all be listed", []map[string]any{evaluationsWorkflowNestedNode("elsewhere", "yaml_only_flow")}, "Stranger", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newEvaluationsToolAuthMesh(t)
			m.orderClassifier()
			m.card("uuid-stranger", "Stranger")
			m.answer("uuid-stranger", evaluationsToolAuthAnswer{})
			m.workflow("Triage", append([]map[string]any{evaluationsWorkflowAgentNode("classify", "OrderClassifier")}, tc.nodes...)...)
			_, path := m.workflowExperiment()

			begin, disconnect := http.StatusUnprocessableEntity, http.StatusUnprocessableEntity
			if tc.allowed {
				begin, disconnect = http.StatusOK, http.StatusNoContent
			}
			expectStatus(t, m.api.call(http.MethodPut, path+"/begin", credentialBody(tc.agentName, "gmail")), begin)
			expectStatus(t, m.api.call(http.MethodPut, path+"/disconnect", credentialBody(tc.agentName, "gmail")), disconnect)
			if tc.allowed {
				return
			}
			if calls := append(m.callsTo("begin"), m.callsTo("disconnect")...); len(calls) != 0 {
				t.Errorf("the refused writes reached an agent: %+v", calls)
			}
			if published := m.registrations.published(); len(published) != 0 {
				t.Errorf("a refused begin published %d registrations", len(published))
			}
		})
	}
}

func TestExperimentToolAuth_PinsOnAWorkflowExperiment(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.orderClassifier()
	m.card("uuid-miro", "Miro Agent")
	m.workflow("Triage", evaluationsWorkflowAgentNode("classify", "OrderClassifier"), evaluationsWorkflowNestedNode("elsewhere", "yaml_only_flow"))
	_, path := m.workflowExperiment()

	expectStatus(t, m.api.call(http.MethodPost, path+"/agents", map[string]any{"agentName": "Miro Agent"}), http.StatusCreated)
	for _, name := range []string{"Triage", "OrderClassifier"} {
		expectStatus(t, m.api.call(http.MethodPost, path+"/agents", map[string]any{"agentName": name}), http.StatusConflict)
	}
	var rows []string
	for _, a := range m.checklist(path).Agents {
		rows = append(rows, fmt.Sprintf("%s target=%v nodes=%v added=%v", a.AgentName, a.IsTarget, a.NodeIDs, a.AddedBy != nil))
	}
	want := []string{"Triage target=true nodes=[] added=false", "OrderClassifier target=false nodes=[classify] added=false", "Miro Agent target=false nodes=[] added=true"}
	if !slices.Equal(rows, want) {
		t.Errorf("checklist = %v\nwant        %v", rows, want)
	}

	expectStatus(t, m.api.call(http.MethodDelete, path+"/agents/"+url.PathEscape("Miro Agent"), nil), http.StatusNoContent)
	if after := m.checklist(path); len(after.Agents) != 2 {
		t.Errorf("checklist after unpin = %+v, want the workflow and its node agent", after.Agents)
	}
}

func TestExperimentToolAuth_AWorkflowNodeNamedByItsBrokerNameIsListedByItsDisplayName(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.orderClassifier()
	m.workflow("Triage", evaluationsWorkflowAgentNode("classify", "uuid-classifier"))
	_, path := m.workflowExperiment()

	got := m.checklist(path)
	if len(got.Agents) != 2 || got.Agents[1].AgentName != "OrderClassifier" || !slices.Equal(got.Agents[1].NodeIDs, []string{"classify"}) {
		t.Errorf("agents = %+v, want the node agent under its display name", got.Agents)
	}
}

func TestExperimentToolAuth_AWorkflowWhoseNodesCannotBeReadFailsTheChecklist(t *testing.T) {
	m := newEvaluationsToolAuthMesh(t)
	m.workflow("Triage", evaluationsWorkflowAgentNode("classify", "OrderClassifier"))
	_, path := m.workflowExperiment()
	m.exec(`DROP TABLE workflow_deployments`)

	rec := m.api.call(http.MethodGet, path, nil)
	if rec.Code < http.StatusInternalServerError {
		t.Fatalf("status = %d, want a server error rather than a checklist missing its node agents: %s", rec.Code, rec.Body.String())
	}
	decodeError(t, rec)
}
