// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"errors"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func TestPlanOps_OutOfSyncAgentIsPendingRedeploy(t *testing.T) {
	outOfSync, inSync := syncStatusOutOfSync, "in_sync"
	plan := &PlanResult{Agents: &agentKindPlan{Kind: "agent", Unchanged: []agentOp{
		{Name: "stale", Actual: &wire.AgentResponse{SyncStatus: &outOfSync}},
		{Name: "fine", Actual: &wire.AgentResponse{SyncStatus: &inSync}},
	}}}

	ops := plan.Ops()
	want := []PlanOp{
		{Kind: "agent", Name: "stale", Action: ActionRedeploy},
		{Kind: "agent", Name: "fine", Action: ActionUnchanged},
	}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("Ops() = %+v, want %+v", ops, want)
	}
	if !Summarize(ops).Pending(false) {
		t.Error("an out-of-sync agent must count as a pending change")
	}
}

func TestPlanPending_IgnoresRBACWhenWritesBlocked(t *testing.T) {
	for _, kind := range []string{resolve.ServiceAccountKind, resolve.LegacyServiceAccountKind} {
		t.Run(kind, func(t *testing.T) {
			plan := &PlanResult{
				RBACWritesBlocked: true,
				RBACRoles:         &rbacRolePlan{Kind: "rbacRole", Creates: []rbacRoleOp{{Name: "support"}}},
				SystemUsers:       &systemUserPlan{Kind: kind, Creates: []systemUserOp{{Name: "bot"}}},
				DefaultRoles:      &defaultRolesPlan{Verb: defaultRolesVerbPut, RoleNames: []string{"support"}},
			}
			if plan.Pending(true, false) {
				t.Error("RBAC ops apply will skip must not count as pending")
			}
			if Summarize(plan.Ops()).Create != 2 {
				t.Error("blocked RBAC ops must still be listed in Ops")
			}

			plan.Models = &kindPlan{Kind: "model", Creates: []modelOp{{Name: "gpt"}}}
			if !plan.Pending(false, false) {
				t.Error("a non-RBAC change must still count as pending")
			}

			plan.Models, plan.RBACWritesBlocked = nil, false
			if !plan.Pending(false, false) {
				t.Error("RBAC ops must count as pending when writes are allowed")
			}
		})
	}
}

func TestPlanPending_IgnoresRedeployUnderNoDeploy(t *testing.T) {
	outOfSync := syncStatusOutOfSync
	plan := &PlanResult{Agents: &agentKindPlan{Kind: "agent", Unchanged: []agentOp{
		{Name: "stale", Actual: &wire.AgentResponse{SyncStatus: &outOfSync}},
	}}}
	if !plan.Pending(false, false) {
		t.Error("a redeploy must count as pending when deploys run")
	}
	if plan.Pending(false, true) {
		t.Error("a redeploy must not count as pending under --no-deploy")
	}
}

func TestApplyResults(t *testing.T) {
	pruneOnlyWebui := &webuiSettingsPlan{
		Verb:    webuiSettingsVerbPut,
		Desired: &manifest.WebuiSettingsSpec{AppName: sp("Acme")},
		Current: &wire.WebuiSettingsResponse{AppName: sp("Acme"), WelcomeMessage: sp("Hi")},
	}
	tests := []struct {
		name string
		res  *ApplyResult
		want []ApplyOpResult
	}{
		{name: "nothing attempted", res: &ApplyResult{}, want: []ApplyOpResult{}},
		{
			name: "creates, csv-only dataset edit and deploy failure",
			res: &ApplyResult{
				Models: &kindOutcome{Kind: "model", Created: []opResult{{Op: modelOp{Name: "gpt"}, Verb: "create"}}},
				Agents: &agentKindOutcome{
					Kind:        "agent",
					Created:     []agentOpResult{{Op: agentOp{Name: "helper"}, Verb: "create"}},
					Failures:    []agentOpResult{{Op: agentOp{Name: "helper"}, Verb: verbDeploy, Err: errors.New("boom")}},
					Deployments: []deployResult{{Kind: "agent", Name: "helper", Action: "deploy", Err: errors.New("boom")}},
				},
				Datasets: &datasetOutcome{Kind: "dataset", Examples: []datasetExamplesOutcome{
					{Name: "edited", Inserts: 1},
					{Name: "same", Unchanged: 3},
				}},
			},
			want: []ApplyOpResult{
				{Kind: "model", Name: "gpt", Status: StatusCreated},
				{Kind: "agent", Name: "helper", Status: StatusCreated},
				{Kind: "agent", Name: "helper", Status: StatusFailed, Action: "deploy", Error: "boom"},
				{Kind: "dataset", Name: "edited", Status: StatusUpdated},
			},
		},
		{
			name: "prune-only webuiSettings clear applied is a delete",
			res: &ApplyResult{
				Plan:            &PlanResult{WebuiSettings: pruneOnlyWebui, ProfileProvider: &profileProviderPlan{Verb: profileProviderVerbUnchanged}},
				WebuiSettings:   &webuiSettingsOutcome{Applied: &webuiSettingsOpResult{Verb: webuiSettingsVerbPut}},
				ProfileProvider: &profileProviderOutcome{Skipped: &profileProviderOpResult{Verb: profileProviderVerbUnchanged}},
			},
			want: []ApplyOpResult{{Kind: platformSection, Name: "webuiSettings", Status: StatusDeleted}},
		},
		{
			name: "prune-only webuiSettings clear without --prune is a skipped delete",
			res: &ApplyResult{
				Plan:          &PlanResult{WebuiSettings: pruneOnlyWebui},
				WebuiSettings: &webuiSettingsOutcome{Skipped: &webuiSettingsOpResult{Verb: webuiSettingsVerbPut}},
			},
			want: []ApplyOpResult{{Kind: platformSection, Name: "webuiSettings", Status: StatusSkipped, Action: ActionDelete}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.res.Results(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Results() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
