// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (a *recordingAWE) removedPaths() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.removals)
}

func TestUndeployDependents_AsksTheExecutorForEveryAgentThatMayStillRun(t *testing.T) {
	t.Parallel()
	const alias = "cascade-model"
	ctrl, awe := recordingControlClient(t)
	env := newAgentDeleteEnv(testOpenDB(t), ctrl)
	env.deploySvc.awes = awe
	handler := NewModelDependentsHandler(env.agentRepo, nil, nil, nil, env.deploySvc, slog.Default())

	cases := []struct {
		name        string
		history     []seededDeployment
		wantRemoved bool
	}{
		{"cleanly undeployed", []seededDeployment{{DeployActionDeploy, DeployStatusSuccess}, {DeployActionUndeploy, DeployStatusSuccess}}, false},
		{"never deployed", nil, false},
		{"failed deploy", []seededDeployment{{DeployActionDeploy, DeployStatusFailed}}, true},
		{"deploy still pending", []seededDeployment{{DeployActionDeploy, "pending"}}, true},
		{"failed undeploy", []seededDeployment{{DeployActionDeploy, DeployStatusSuccess}, {DeployActionUndeploy, DeployStatusFailed}}, true},
	}
	var wantRemoved []string
	for _, tc := range cases {
		created, err := env.agentSvc.Create(t.Context(), CreateAgentRequest{
			Name:          strings.ReplaceAll(tc.name, " ", "-"),
			Description:   "agent whose last deployment decides whether the cascade undeploys it",
			SystemPrompt:  deleteGateAgentPrompt,
			ModelProvider: []string{alias},
		}, "test-user")
		if err != nil {
			t.Fatalf("create %s: %v", tc.name, err)
		}
		id := created.ID
		env.seedHistory(t, id, tc.history)
		if tc.wantRemoved {
			wantRemoved = append(wantRemoved, "rollout "+AgentInstanceName(&store.Agent{ID: id}))
		}
	}

	failed, err := handler.UndeployDependents(t.Context(), alias)
	if err != nil || len(failed) != 0 {
		t.Fatalf("UndeployDependents = %v, %v, want every undeploy to succeed", failed, err)
	}

	got := awe.removedPaths()
	slices.Sort(got)
	slices.Sort(wantRemoved)
	if !slices.Equal(got, wantRemoved) {
		t.Errorf("AWE rollouts = %v, want exactly %v", got, wantRemoved)
	}
}
