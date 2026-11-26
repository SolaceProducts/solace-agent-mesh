// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func TestDesiredFor_PicksTheNewestRowThatReachedTheRuntime(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	agents := NewAgentRepo(db)
	deployments := NewAgentDeploymentRepo(db)
	gatewayDeployments := NewGatewayDeploymentRepo(db)
	snapshot := "name: a"
	runtimeError, rejected := "runtime_error", "rejected"
	type row struct {
		id, status, action string
		reason             *string
		snapshot           *string
		created            int64
	}
	cases := []struct {
		name string
		rows []row
		want string
	}{
		{"a runtime failure with a config counts", []row{
			{"00000000-0000-7000-8000-000000000001", "success", "deploy", nil, &snapshot, 1000},
			{"00000000-0000-7000-8000-000000000002", "failed", "update", &runtimeError, &snapshot, 2000},
		}, "00000000-0000-7000-8000-000000000002"},
		{"a runtime failure without a config is skipped", []row{
			{"00000000-0000-7000-8000-000000000003", "success", "deploy", nil, &snapshot, 1000},
			{"00000000-0000-7000-8000-000000000004", "failed", "update", &runtimeError, nil, 2000},
		}, "00000000-0000-7000-8000-000000000003"},
		{"a refused deploy is skipped", []row{
			{"00000000-0000-7000-8000-000000000005", "success", "deploy", nil, &snapshot, 1000},
			{"00000000-0000-7000-8000-000000000006", "failed", "update", &rejected, &snapshot, 2000},
		}, "00000000-0000-7000-8000-000000000005"},
		{"a runtime failure of an undeploy counts", []row{
			{"00000000-0000-7000-8000-000000000007", "success", "deploy", nil, &snapshot, 1000},
			{"00000000-0000-7000-8000-000000000008", "failed", "undeploy", &runtimeError, nil, 2000},
		}, "00000000-0000-7000-8000-000000000008"},
		{"an older row with a larger random id loses", []row{
			{"ffffffff-ffff-4fff-bfff-ffffffffffff", "success", "deploy", nil, &snapshot, 1000},
			{"00000000-0000-7000-8000-000000000009", "success", "update", nil, &snapshot, 2000},
		}, "00000000-0000-7000-8000-000000000009"},
		{"in a time tie the larger id wins", []row{
			{"00000000-0000-7000-8000-00000000000a", "success", "deploy", nil, &snapshot, 3000},
			{"00000000-0000-7000-8000-00000000000b", "success", "update", nil, &snapshot, 3000},
		}, "00000000-0000-7000-8000-00000000000b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			desc := "desired row test agent"
			agent := &Agent{Name: c.name, Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
			if err := agents.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			for _, r := range c.rows {
				d := &AgentDeployment{ID: r.id, AgentID: agent.ID, Status: r.status, Action: r.action,
					FailureReason: r.reason, AgentConfigSnapshot: r.snapshot, CreatedBy: "u", CreatedTime: r.created}
				if err := deployments.Create(ctx, d); err != nil {
					t.Fatal(err)
				}
			}
			got, err := deployments.DesiredFor(ctx, agent.ID)
			if err != nil || got == nil || got.ID != c.want {
				t.Fatalf("desired = %+v, %v, want %s", got, err, c.want)
			}

			gatewayID := seedGatewayForDeploys(ctx, t, db, c.name)
			for _, r := range c.rows {
				d := &GatewayDeployment{ID: r.id, GatewayID: gatewayID, Status: r.status, Action: r.action,
					FailureReason: r.reason, GatewayConfigSnapshot: r.snapshot, CreatedBy: "u", CreatedTime: r.created}
				if createErr := gatewayDeployments.Create(ctx, d); createErr != nil {
					t.Fatal(createErr)
				}
			}
			desired, err := gatewayDeployments.Desired(ctx)
			if err != nil || desired[gatewayID] == nil || desired[gatewayID].ID != c.want {
				t.Fatalf("desired entrypoint deployment = %+v, %v, want %s", desired[gatewayID], err, c.want)
			}
		})
	}
}
