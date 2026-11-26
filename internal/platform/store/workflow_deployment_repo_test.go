// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type deployStep struct {
	status, action, snapshot string
	createdTime              int64
}

func TestWorkflowDeploymentRepo_ListLiveDeploys(t *testing.T) {
	db := TestDB(t)
	workflows := NewWorkflowRepo(db)
	repo := NewWorkflowDeploymentRepo(db)

	cases := []struct {
		name         string
		steps        []deployStep
		wantSnapshot string
	}{
		{name: "never-deployed"},
		{name: "updated", wantSnapshot: "v2", steps: []deployStep{
			{"success", "deploy", "v1", 1000}, {"success", "update", "v2", 2000},
		}},
		{name: "undeployed", steps: []deployStep{
			{"success", "deploy", "v1", 1000}, {"success", "undeploy", "", 2000},
		}},
		{name: "redeployed", wantSnapshot: "v2", steps: []deployStep{
			{"success", "deploy", "v1", 1000}, {"success", "undeploy", "", 2000}, {"success", "deploy", "v2", 3000},
		}},
		{name: "failed-redeploy-keeps-last-good", wantSnapshot: "v1", steps: []deployStep{
			{"success", "deploy", "v1", 1000}, {"failed", "update", "v2", 2000},
		}},
		{name: "undeploy-in-same-millisecond", steps: []deployStep{
			{"success", "deploy", "v1", 1000}, {"success", "undeploy", "", 1000},
		}},
		{name: "rejected-deploy-after-undeploy", steps: []deployStep{
			{"success", "deploy", "v1", 1000}, {"success", "undeploy", "", 2000}, {"failed", "deploy", "v2", 3000},
		}},
	}

	idByName := map[string]string{}
	for _, tc := range cases {
		wf := &Workflow{Name: tc.name, AppConfig: `{}`, CreatedBy: "u", UpdatedBy: "u"}
		if err := workflows.Create(t.Context(), wf, WorkflowBindings{}); err != nil {
			t.Fatalf("create workflow %s: %v", tc.name, err)
		}
		idByName[tc.name] = wf.ID
		for _, step := range tc.steps {
			row := &WorkflowDeployment{
				ID: idgen.New(), WorkflowID: wf.ID, Status: step.status, Action: step.action,
				CreatedBy: "u", CreatedTime: step.createdTime,
			}
			if step.snapshot != "" {
				row.WorkflowConfigSnapshot = &step.snapshot
			}
			if err := repo.Create(t.Context(), row); err != nil {
				t.Fatalf("create deployment for %s: %v", tc.name, err)
			}
		}
	}

	live, err := repo.ListLiveDeploys(t.Context())
	if err != nil {
		t.Fatalf("ListLiveDeploys: %v", err)
	}
	gotSnapshot := map[string]string{}
	for _, d := range live {
		if _, dup := gotSnapshot[d.WorkflowID]; dup {
			t.Errorf("workflow %s listed twice", d.WorkflowID)
		}
		if d.WorkflowConfigSnapshot == nil {
			t.Fatalf("live row %s has no snapshot", d.ID)
		}
		gotSnapshot[d.WorkflowID] = *d.WorkflowConfigSnapshot
	}
	for _, tc := range cases {
		got, listed := gotSnapshot[idByName[tc.name]]
		switch {
		case tc.wantSnapshot == "" && listed:
			t.Errorf("%s: listed live with snapshot %q, want it left out", tc.name, got)
		case tc.wantSnapshot != "" && got != tc.wantSnapshot:
			t.Errorf("%s: live snapshot %q (listed=%v), want %q", tc.name, got, listed, tc.wantSnapshot)
		}
	}
}
