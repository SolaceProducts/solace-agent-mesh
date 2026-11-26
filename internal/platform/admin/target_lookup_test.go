// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type deployRow struct{ action, status string }

func TestLookupTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		workflow  bool
		rows      []deployRow
		inFlight  string
		byDisplay bool
		want      control.TargetState
	}{
		{name: "never deployed", want: control.TargetNotDeployed},
		{name: "first deploy in flight", inFlight: DeployActionDeploy, want: control.TargetNotDeployed},
		{name: "deployed", rows: []deployRow{{DeployActionDeploy, "success"}}, want: control.TargetDeployed},
		{name: "deployed, by display name", rows: []deployRow{{DeployActionDeploy, "success"}}, byDisplay: true, want: control.TargetDeployed},
		{name: "update failed", rows: []deployRow{{DeployActionDeploy, "success"}, {"update", "failed"}}, want: control.TargetNotDeployed},
		{name: "undeployed", rows: []deployRow{{DeployActionDeploy, "success"}, {DeployActionUndeploy, "success"}}, want: control.TargetNotDeployed},
		{name: "undeploy failed may leave it running", rows: []deployRow{{DeployActionDeploy, "success"}, {DeployActionUndeploy, "failed"}}, want: control.TargetDeployed},
		{name: "undeploy in flight", rows: []deployRow{{DeployActionDeploy, "success"}}, inFlight: DeployActionUndeploy, want: control.TargetNotDeployed},
		{name: "update in flight", rows: []deployRow{{DeployActionDeploy, "success"}}, inFlight: "update", want: control.TargetDeployed},
		{name: "workflow deployed, by display name", workflow: true, rows: []deployRow{{DeployActionDeploy, "success"}}, byDisplay: true, want: control.TargetDeployed},
		{name: "workflow undeploy in flight", workflow: true, rows: []deployRow{{DeployActionDeploy, "success"}}, inFlight: DeployActionUndeploy, want: control.TargetNotDeployed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := testOpenDB(t)
			display := "Target " + idgen.New()
			kind, id := a2a.KindAgent, ""
			if tc.workflow {
				kind = a2a.KindWorkflow
				wf := &store.Workflow{Name: display, AppConfig: `{}`, CreatedBy: "u", UpdatedBy: "u"}
				if err := store.NewWorkflowRepo(db).Create(t.Context(), wf, store.WorkflowBindings{}); err != nil {
					t.Fatal(err)
				}
				id = wf.ID
			} else {
				agent := &store.Agent{Name: display, CreatedBy: "u", UpdatedBy: "u"}
				if err := store.NewAgentRepo(db).Create(t.Context(), agent, nil, nil, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
				id = agent.ID
			}
			for i, row := range tc.rows {
				recordDeployment(t, db, tc.workflow, id, row, int64(i+1))
			}
			if tc.inFlight != "" {
				putIntent(t, db, tc.workflow, id, tc.inFlight)
			}
			cardName := a2a.InstanceName(kind, id)
			name := cardName
			if tc.byDisplay {
				name = display
			}

			got, err := LookupTarget(t.Context(), db, name)
			if err != nil {
				t.Fatalf("LookupTarget(%q) error = %v", name, err)
			}
			if got.State != tc.want || got.CardName != cardName {
				t.Errorf("LookupTarget(%q) = %+v, want state %v with card name %q", name, got, tc.want, cardName)
			}
		})
	}
}

func TestLookupTarget_UnknownName(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	for _, name := range []string{"yaml-defined-agent", a2a.InstanceName(a2a.KindAgent, idgen.New())} {
		if got, err := LookupTarget(t.Context(), db, name); err != nil || got.State != control.TargetUnknown {
			t.Errorf("LookupTarget(%q) = %+v, %v; want unknown", name, got, err)
		}
	}
}

func recordDeployment(t *testing.T, db *store.DB, workflow bool, id string, row deployRow, at int64) {
	t.Helper()
	var err error
	if workflow {
		err = store.NewWorkflowDeploymentRepo(db).Create(t.Context(), &store.WorkflowDeployment{ID: idgen.New(), WorkflowID: id, Action: row.action, Status: row.status, CreatedBy: "u", CreatedTime: at})
	} else {
		err = store.NewAgentDeploymentRepo(db).Create(t.Context(), &store.AgentDeployment{ID: idgen.New(), AgentID: id, Action: row.action, Status: row.status, CreatedBy: "u", CreatedTime: at})
	}
	if err != nil {
		t.Fatal(err)
	}
}

func putIntent(t *testing.T, db *store.DB, workflow bool, id, action string) {
	t.Helper()
	deploymentID := idgen.New()
	kind := store.IntentKindAgent
	var payload any = agentIntent{Deployment: store.AgentDeployment{ID: deploymentID, AgentID: id, Action: action}}
	if workflow {
		kind = store.IntentKindWorkflow
		payload = workflowIntent{Deployment: store.WorkflowDeployment{ID: deploymentID, WorkflowID: id, Action: action}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeploymentIntentRepo(db).Put(t.Context(), &store.DeploymentIntent{ID: deploymentID, Kind: kind, ObjectID: id, Payload: string(body)}); err != nil {
		t.Fatal(err)
	}
}
