// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

const builtinDeployCheck = time.Second

func (r *Reconciler) SetEntrypointsStartedByGateway(v bool) {
	r.entrypointsStartedByGateway = v
}

func (r *Reconciler) LeaderJob() leader.Job {
	return leader.Job{
		Name:         leader.JobPlatformReconciler,
		OverdueAfter: leader.NeverOverdue,
		Run:          r.serve,
	}
}

func (r *Reconciler) serve(ctx context.Context, _ leader.Term) error {
	if r.brk == nil {
		return nil
	}
	queue, err := r.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create reconciler subscription: %w", err)
	}
	defer func() { _ = r.brk.DeleteQueue(queue.Name()) }()
	topics := []string{
		a2a.AWEAgentsRequestTopic(r.namespace),
		a2a.LifecycleTopic(r.namespace, "str", "started"),
	}
	if !r.entrypointsStartedByGateway {
		topics = append(topics, a2a.LifecycleTopic(r.namespace, "gateway", "started"))
	}
	for _, topic := range topics {
		if err := r.brk.Subscribe(queue, topic); err != nil {
			return fmt.Errorf("subscribe %s: %w", topic, err)
		}
	}

	go r.deployBuiltinsWhenAWEReports(ctx)
	for {
		msg, err := r.brk.Receive(ctx, queue)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, broker.ErrQueueNotFound) {
			return fmt.Errorf("receive reconciler requests: %w", err)
		}
		if err != nil || msg == nil {
			continue
		}
		msg.Ack()
		switch msg.Topic {
		case a2a.AWEAgentsRequestTopic(r.namespace):
			go r.answerAWE(ctx, msg)
		case a2a.LifecycleTopic(r.namespace, "str", "started"):
			go r.pushAllBindings(ctx)
			if r.strReconcileHandler != nil {
				go r.strReconcileHandler.Handle(ctx)
			}
		case a2a.LifecycleTopic(r.namespace, "gateway", "started"):
			go r.reconcileGateways(ctx)
		}
	}
}

func (r *Reconciler) deployBuiltinsWhenAWEReports(ctx context.Context) {
	if r.deploySvc == nil || r.deploySvc.awes == nil {
		return
	}
	ticker := time.NewTicker(builtinDeployCheck)
	defer ticker.Stop()
	for !r.deploySvc.awes.anyLive() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
	r.deployBuiltinsOnce(ctx)
}

func (r *Reconciler) deployBuiltinsOnce(ctx context.Context) {
	if r.deploySvc == nil {
		return
	}
	r.builtins.Lock()
	defer r.builtins.Unlock()
	for _, name := range seededBuiltinAgentNames {
		if r.builtinFeatureDisabled(name) {
			continue
		}
		agent, toolsets, inputModes, outputModes, connectorIDs, err := r.agentRepo.GetByName(ctx, name)
		if err != nil || agent == nil {
			continue
		}
		latest, err := r.agentDeploymentRepo.GetLatestSuccessfulDeploys(ctx, []string{agent.ID})
		if err != nil {
			r.logger.Warn("built-in agent deploy check failed", "name", name, "error", err)
			continue
		}
		action, reason := DeployActionDeploy, "first deploy"
		if dep := latest[agent.ID]; dep != nil {
			if agent.UpdatedBy != builtinAgentActor || dep.ConfigHash == nil {
				continue
			}
			current := computeAgentConfigHash(agent, toolsets, inputModes, outputModes,
				connectorConfigFingerprints(ctx, r.connectorRepo, connectorIDs), loadAgentSkillIDs(ctx, agent.ID, r.skillRepo))
			if current == *dep.ConfigHash {
				continue
			}
			action, reason = DeployActionUpdate, "release config"
		}
		resp, err := r.deploySvc.Create(ctx, CreateAgentDeploymentRequest{AgentID: agent.ID, Action: action}, builtinAgentActor)
		if err != nil || resp == nil || resp.Status != DeployStatusSuccess {
			r.logger.Warn("built-in agent deploy did not succeed; it is tried again at the next leader change",
				"name", name, "reason", reason, "error", err)
			continue
		}
		r.logger.Info("deployed built-in agent", "name", name, "reason", reason)
	}
}
