// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"fmt"
	"testing"
)

const (
	lowerDeploymentID   = "01900000-0000-7000-8000-000000000001"
	greaterDeploymentID = "01900000-0000-7000-8000-000000000002"
)

func TestMayHaveLiveInstance(t *testing.T) {
	cases := []struct {
		action, status string
		want           bool
	}{
		{"deploy", "success", true},
		{"deploy", "failed", true},
		{"update", "success", true},
		{"update", "failed", true},
		{"undeploy", "success", false},
		{"undeploy", "failed", true},
	}
	for _, tc := range cases {
		if got := MayHaveLiveInstance(tc.status, tc.action); got != tc.want {
			t.Errorf("MayHaveLiveInstance(status=%q, action=%q) = %v, want %v", tc.status, tc.action, got, tc.want)
		}
	}
}

type deploymentRow struct {
	id, action, status string
	createdTime        int64
}

type deploymentSubject struct {
	name  string
	setup func(t *testing.T, db *DB) (seed func(deploymentRow), live func() (bool, error), display func() (string, error))
}

var deploymentSubjects = []deploymentSubject{
	{
		name: "agent",
		setup: func(t *testing.T, db *DB) (func(deploymentRow), func() (bool, error), func() (string, error)) {
			ctx := t.Context()
			repo := NewAgentRepo(db)
			agent := &Agent{Name: "liveness-agent", CreatedBy: "u", UpdatedBy: "u"}
			if err := repo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
				t.Fatalf("create agent: %v", err)
			}
			deployments := NewAgentDeploymentRepo(db)
			seed := func(r deploymentRow) {
				d := &AgentDeployment{ID: r.id, AgentID: agent.ID, Action: r.action, Status: r.status, CreatedBy: "u", CreatedTime: r.createdTime}
				if err := deployments.Create(ctx, d); err != nil {
					t.Fatalf("create agent deployment %s/%s: %v", r.action, r.status, err)
				}
			}
			live := func() (bool, error) { return repo.HasPossiblyLiveDeployment(ctx, agent.ID) }
			display := func() (string, error) { return repo.GetDeploymentStatus(ctx, agent.ID) }
			return seed, live, display
		},
	},
	{
		name: "workflow",
		setup: func(t *testing.T, db *DB) (func(deploymentRow), func() (bool, error), func() (string, error)) {
			ctx := t.Context()
			repo := NewWorkflowRepo(db)
			w := &Workflow{Name: "liveness-workflow", AppConfig: `{}`, CreatedBy: "u", UpdatedBy: "u"}
			if err := repo.Create(ctx, w, WorkflowBindings{}); err != nil {
				t.Fatalf("create workflow: %v", err)
			}
			deployments := NewWorkflowDeploymentRepo(db)
			seed := func(r deploymentRow) {
				d := &WorkflowDeployment{ID: r.id, WorkflowID: w.ID, Action: r.action, Status: r.status, CreatedBy: "u", CreatedTime: r.createdTime}
				if err := deployments.Create(ctx, d); err != nil {
					t.Fatalf("create workflow deployment %s/%s: %v", r.action, r.status, err)
				}
			}
			live := func() (bool, error) { return repo.HasPossiblyLiveDeployment(ctx, w.ID) }
			display := func() (string, error) { return repo.GetDeploymentStatus(ctx, w.ID) }
			return seed, live, display
		},
	},
}

func assertLatestRow(t *testing.T, winner deploymentRow, live func() (bool, error), display func() (string, error)) {
	t.Helper()
	gotLive, err := live()
	if err != nil {
		t.Fatalf("HasPossiblyLiveDeployment: %v", err)
	}
	if want := MayHaveLiveInstance(winner.status, winner.action); gotLive != want {
		t.Errorf("HasPossiblyLiveDeployment = %v, want %v (latest row %s/%s id=%s)", gotLive, want, winner.action, winner.status, winner.id)
	}
	gotDisplay, err := display()
	if err != nil {
		t.Fatalf("GetDeploymentStatus: %v", err)
	}
	if want := MapDeploymentDisplayStatus(winner.status, winner.action); gotDisplay != want {
		t.Errorf("GetDeploymentStatus = %q, want %q (latest row %s/%s id=%s)", gotDisplay, want, winner.action, winner.status, winner.id)
	}
}

func TestHasPossiblyLiveDeployment_NoRows(t *testing.T) {
	for _, subject := range deploymentSubjects {
		t.Run(subject.name, func(t *testing.T) {
			_, live, display := subject.setup(t, TestDB(t))
			got, err := live()
			if err != nil {
				t.Fatalf("HasPossiblyLiveDeployment: %v", err)
			}
			if got {
				t.Error("HasPossiblyLiveDeployment = true for a never-deployed resource, want false")
			}
			status, err := display()
			if err != nil {
				t.Fatalf("GetDeploymentStatus: %v", err)
			}
			if status != "not_deployed" {
				t.Errorf("GetDeploymentStatus = %q, want %q", status, "not_deployed")
			}
		})
	}
}

func TestHasPossiblyLiveDeployment_LatestRowDecides(t *testing.T) {
	states := []struct{ action, status string }{
		{"deploy", "success"},
		{"deploy", "failed"},
		{"update", "success"},
		{"update", "failed"},
		{"undeploy", "success"},
		{"undeploy", "failed"},
	}
	for _, subject := range deploymentSubjects {
		for _, state := range states {
			t.Run(fmt.Sprintf("%s/%s_%s", subject.name, state.action, state.status), func(t *testing.T) {
				seed, live, display := subject.setup(t, TestDB(t))
				earlier := deploymentRow{id: greaterDeploymentID, action: "undeploy", status: "success", createdTime: 1_000}
				if !MayHaveLiveInstance(state.status, state.action) {
					earlier.action = "deploy"
				}
				latest := deploymentRow{id: lowerDeploymentID, action: state.action, status: state.status, createdTime: 2_000}
				seed(earlier)
				seed(latest)
				assertLatestRow(t, latest, live, display)
			})
		}
	}
}

func TestHasPossiblyLiveDeployment_CreatedTimeTieBreak(t *testing.T) {
	const tiedTime = int64(1_000_000_000)
	undeployed := func(id string) deploymentRow {
		return deploymentRow{id: id, action: "undeploy", status: "success", createdTime: tiedTime}
	}
	deployed := func(id string) deploymentRow {
		return deploymentRow{id: id, action: "deploy", status: "success", createdTime: tiedTime}
	}
	cases := []struct {
		name           string
		lower, greater deploymentRow
		greaterFirst   bool
	}{
		{"greater_undeploy_inserted_last", deployed(lowerDeploymentID), undeployed(greaterDeploymentID), false},
		{"greater_undeploy_inserted_first", deployed(lowerDeploymentID), undeployed(greaterDeploymentID), true},
		{"greater_deploy_inserted_last", undeployed(lowerDeploymentID), deployed(greaterDeploymentID), false},
		{"greater_deploy_inserted_first", undeployed(lowerDeploymentID), deployed(greaterDeploymentID), true},
	}
	for _, subject := range deploymentSubjects {
		for _, tc := range cases {
			t.Run(subject.name+"/"+tc.name, func(t *testing.T) {
				seed, live, display := subject.setup(t, TestDB(t))
				if tc.greaterFirst {
					seed(tc.greater)
					seed(tc.lower)
				} else {
					seed(tc.lower)
					seed(tc.greater)
				}
				assertLatestRow(t, tc.greater, live, display)
			})
		}
	}
}
