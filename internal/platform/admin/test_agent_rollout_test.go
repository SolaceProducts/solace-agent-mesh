// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestE2E_ABuilderTestAgentRunsOnTheAWEsAndStaysRunning(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	h.createAgent("component_under_test", "prompt")
	svc := NewTestAgentService(h.agentRepo, h.workflowRepo, store.NewSkillRepo(h.db), h.deploySvc, h.agentSvc, time.Hour, h.deploySvc.logger)

	created, err := svc.CreateForSession(h.ctx, "session-1", "component_under_test", ComponentKindAgent, "e2e-user")
	if err != nil {
		t.Fatalf("create the test agent: %v", err)
	}
	latest, err := h.deployRepo.GetLatestByAgentID(h.ctx, created.ID)
	if err != nil || latest == nil {
		t.Fatalf("read the test agent's deployment: %v, %v", latest, err)
	}
	if latest.Status != DeployStatusSuccess {
		t.Fatalf("test agent deployment status %q, want %q: the leader must list a test agent in its answer to the AWEs", latest.Status, DeployStatusSuccess)
	}
	agent, _, _, _, _, err := h.agentRepo.GetByID(h.ctx, created.ID)
	if err != nil || agent == nil {
		t.Fatalf("read the test agent: %v", err)
	}
	instance := AgentInstanceName(agent)
	h.waitForInstance(instance, 5*time.Second)

	h.deploy(h.createAgent("deployed_after", "prompt"))
	if info, err := h.aweExe.GetInstance(instance); err != nil || info.Status != awe.StatusRunning {
		t.Fatalf("after the AWEs applied a later answer the test agent is %v (%v), want running", info, err)
	}
}
