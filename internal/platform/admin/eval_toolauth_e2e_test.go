// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestE2E_ExperimentToolAuthControlRoundTrip(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	agentID := h.createAgent("e2e-toolauth", "You are a test agent for tool auth control plane round trip checks")
	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)

	logger := slog.New(slog.DiscardHandler)
	experimentRepo := store.NewEvalExperimentRepo(h.db)
	liveExp := &store.EvalExperiment{Name: "toolauth-e2e-live", TargetAgent: "e2e-toolauth", CreatedBy: "t", UpdatedBy: "t"}
	if err := experimentRepo.Create(h.ctx, liveExp, nil); err != nil {
		t.Fatalf("seed experiment: %v", err)
	}
	goneExp := &store.EvalExperiment{Name: "toolauth-e2e-gone", TargetAgent: "no-such-agent", CreatedBy: "t", UpdatedBy: "t"}
	if err := experimentRepo.Create(h.ctx, goneExp, nil); err != nil {
		t.Fatalf("seed dangling experiment: %v", err)
	}
	slugExp := &store.EvalExperiment{Name: "toolauth-e2e-slug", TargetAgent: h.agentInstanceName(agentID), CreatedBy: "t", UpdatedBy: "t"}
	if err := experimentRepo.Create(h.ctx, slugExp, nil); err != nil {
		t.Fatalf("seed slug-targeted experiment: %v", err)
	}

	svc := &EvalToolAuthService{h: &evalHandler{
		experiments: eval.NewExperimentService(experimentRepo, store.NewEvalDatasetRepo(h.db),
			store.NewEvalEvaluatorRepo(h.db), store.NewEvalRunRepo(h.db), logger),
		logger:          logger,
		toolAuthControl: h.controlClient,
		toolAuthBroker:  h.brk,
		agentResolver:   &agentNameResolverAdapter{repo: h.agentRepo},
		peerFilters:     &agentPeerFilterAdapter{repo: h.agentRepo, deployments: store.NewAgentDeploymentRepo(h.db), logger: logger},
		namespace:       h.namespace,
		db:              h.db,
	}}

	live, err := svc.Checklist(h.ctx, liveExp.ID)
	if err != nil {
		t.Fatalf("live checklist: %v", err)
	}
	if len(live.Agents) != 1 || live.Agents[0].AgentName != "e2e-toolauth" || !live.Agents[0].IsTarget || !live.Agents[0].Reachable || len(live.Agents[0].Tools) != 0 {
		t.Errorf("live checklist agents = %+v, want a reachable target with no tools", live.Agents)
	}

	var notFound *NotFoundError
	if _, err = svc.Begin(h.ctx, liveExp.ID, "e2e-toolauth", "no-such-credential", "tester"); !errors.As(err, &notFound) {
		t.Errorf("unknown-credential begin = %v, want *NotFoundError", err)
	}

	gone, err := svc.Checklist(h.ctx, goneExp.ID)
	if err != nil {
		t.Fatalf("dangling-agent checklist: %v", err)
	}
	if len(gone.Agents) != 1 || gone.Agents[0].AgentName != "no-such-agent" || !gone.Agents[0].IsTarget || gone.Agents[0].Reachable {
		t.Errorf("dangling-agent checklist agents = %+v, want the target reported unreachable", gone.Agents)
	}
	if !gone.AllowListIsWildcard {
		t.Error("dangling-agent checklist allowListIsWildcard = false, want true")
	}

	slug, err := svc.Checklist(h.ctx, slugExp.ID)
	if err != nil {
		t.Fatalf("slug-target checklist: %v", err)
	}
	if slug.AllowListIsWildcard {
		t.Error("slug-target checklist allowListIsWildcard = true, want the agent's own (empty) allow list")
	}

	var validation *ValidationError
	if _, err := svc.Begin(h.ctx, liveExp.ID, "no-such-agent", "no-such-credential", "tester"); !errors.As(err, &validation) || validation.Field != "agentName" {
		t.Errorf("off-graph begin = %v, want a *ValidationError on agentName", err)
	}
}
