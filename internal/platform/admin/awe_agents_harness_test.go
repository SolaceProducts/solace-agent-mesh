// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type platformAgentsForTest struct {
	db        *store.DB
	brk       broker.Broker
	namespace string
	yamlSvc   *YAMLService
	deployers *DeployerRegistry
	exe       *awe.Executor
	agents    *DeploymentService
	workflows *WorkflowDeploymentService
	rollout   func(live *aweStatusTracker) aweRollout
}

type e2eOption func(*platformAgentsForTest)

func withAWERollout(rollout func(live *aweStatusTracker) aweRollout) e2eOption {
	return func(p *platformAgentsForTest) { p.rollout = rollout }
}

func followPlatformAgentsForTest(t *testing.T, p platformAgentsForTest) *aweStatusTracker {
	t.Helper()
	logger := slog.Default()
	ctx := t.Context()

	deploys := newDeployLeader(nil, store.NewDeploymentIntentRepo(p.db), logger)
	rec := NewReconciler(store.NewAgentRepo(p.db), store.NewAgentDeploymentRepo(p.db), store.NewConnectorRepo(p.db),
		store.NewWorkflowRepo(p.db), store.NewGatewayRepo(p.db), store.NewGatewayDeploymentRepo(p.db),
		p.yamlSvc, nil, nil, p.brk, p.namespace, logger)
	rec.SetDeployerRegistry(p.deployers)
	rec.SetWorkflowDeploymentRepo(store.NewWorkflowDeploymentRepo(p.db))
	rec.SetDeploys(deploys)
	rec.SetEntrypointsStartedByGateway(true)

	awes := newAWEStatusTracker(p.brk, p.namespace)
	if err := awes.follow(ctx); err != nil {
		t.Fatalf("follow AWE status: %v", err)
	}
	awes.listening = awes.now().Add(-aweStatusSettle)
	var rollout aweRollout = awes
	if p.rollout != nil {
		rollout = p.rollout(awes)
	}
	if p.agents != nil {
		p.agents.leader = deploys
		p.agents.awes = rollout
	}
	if p.workflows != nil {
		p.workflows.leader = deploys
		p.workflows.awes = rollout
	}
	rec.SetDeploymentService(p.agents)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = rec.LeaderJob().Run(ctx, leader.Term{Number: 1})
	}()
	t.Cleanup(func() { <-served })
	waitForLeaderAnswers(ctx, t, p.brk, p.namespace)
	if p.exe != nil {
		p.exe.FollowPlatformAgents(ctx, p.namespace)
	}
	return awes
}

func waitForLeaderAnswers(ctx context.Context, t *testing.T, brk broker.Broker, namespace string) {
	t.Helper()
	topic := a2a.AWEAgentsRequestTopic(namespace)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := brk.Request(ctx, topic, &broker.Message{ReplyTo: a2a.AWEAgentsReplyTopic(namespace, "harness"), UserProperties: map[string]string{discovery.ProcessIDProperty: "harness"}}, 200*time.Millisecond)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the leader never answered an AWE's ask: %v", err)
		}
	}
}

func (h *e2eHarness) nudgeAWEs() {
	h.t.Helper()
	if err := h.brk.Publish(h.ctx, a2a.AWEAgentsChangedTopic(h.namespace), &broker.Message{Payload: []byte(`{"name":""}`)}); err != nil {
		h.t.Fatalf("send the agent change notice: %v", err)
	}
}

func (h *e2eHarness) useHarnessAWEs(svc *WorkflowDeploymentService) {
	svc.awes = h.wfDeploySvc.awes
	svc.leader = h.wfDeploySvc.leader
}

type stallingRollout struct {
	aweRollout

	mu      sync.Mutex
	stalls  int
	stall   time.Duration
	started chan struct{}
}

func (s *stallingRollout) rollOut(ctx context.Context, instance, deploymentID string) error {
	s.mu.Lock()
	stalled := s.stalls > 0
	if stalled {
		s.stalls--
		if s.started != nil {
			close(s.started)
			s.started = nil
		}
	}
	d := s.stall
	s.mu.Unlock()
	if stalled {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.aweRollout.rollOut(ctx, instance, deploymentID)
}

func (h *e2eHarness) stallNextRollouts(n int, d time.Duration) <-chan struct{} {
	h.t.Helper()
	stalling, ok := h.deploySvc.awes.(*stallingRollout)
	if !ok {
		stalling = &stallingRollout{aweRollout: h.deploySvc.awes}
		h.deploySvc.awes = stalling
		h.wfDeploySvc.awes = stalling
	}
	stalling.mu.Lock()
	defer stalling.mu.Unlock()
	stalling.stalls, stalling.stall = n, d
	stalling.started = make(chan struct{})
	return stalling.started
}

func (h *e2eHarness) waitForAWEsToRun(instance, deploymentID string) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if done, failure := h.awes.outcome(instance, deploymentID); done {
			return
		} else if failure != "" {
			h.t.Fatalf("AWE could not run %s as %s: %s", instance, deploymentID, failure)
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("AWEs do not run %s as %s", instance, deploymentID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
