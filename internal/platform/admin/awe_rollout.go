// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	agentRolloutTimeout = 60 * time.Second
	agentRolloutPoll    = 100 * time.Millisecond
	aweStatusSettle     = control.AWEStatusInterval + time.Second
)

var errNoAWEStatus = &runtimeError{errors.New("agent deployments are not available: no broker connection")}

var errNoLiveAWE = &UnavailableError{Message: "no agent executor is running yet; try the deployment again shortly"}

func awaitLiveAWE(ctx context.Context, awes aweRollout) error {
	if awes == nil {
		return nil
	}
	return awes.awaitLive(ctx)
}

type aweRollout interface {
	rollOut(ctx context.Context, instance, deploymentID string) error
	notify(ctx context.Context, instance string) error
	anyLive() bool
	awaitLive(ctx context.Context) error
	removeQueues(instance string) error
}

func rollOutOnAWEs(ctx context.Context, awes aweRollout, instance, deploymentID string) error {
	if awes == nil {
		return errNoAWEStatus
	}
	return awes.rollOut(ctx, instance, deploymentID)
}

func (s *DeploymentService) finishUndeployOfDeleted(ctx context.Context, d store.AgentDeployment, instance string) error {
	if d.Action != DeployActionUndeploy || instance == "" {
		return nil
	}
	if err := rollOutOnAWEs(ctx, s.awes, instance, d.ID); err != nil {
		return err
	}
	return removeUndeployedQueues(ctx, s.leader, s.awes, instance, d.ID)
}

func removeUndeployedQueues(ctx context.Context, leader *deployLeader, awes aweRollout, instance, deploymentID string) error {
	return leader.whileInFlight(ctx, deploymentID, func() error { return awes.removeQueues(instance) })
}

func notifyAWEs(ctx context.Context, awes aweRollout, instance string, logger *slog.Logger) {
	if awes == nil {
		return
	}
	if err := awes.notify(ctx, instance); err != nil {
		logger.Warn("agent change notice not sent; AWEs apply the change at their next check", "instance", instance, "error", err)
	}
}

func followAWEsForTerm(ctx context.Context, t *aweStatusTracker, logger *slog.Logger) {
	if err := t.follow(ctx); err != nil {
		logger.Error("AWE status not followed; agent deployments fail this term", "error", err)
	}
}

type aweStatusTracker struct {
	brk       broker.Broker
	namespace string
	now       func() time.Time
	wait      time.Duration
	answer    *aweAnswerSize

	mu        sync.Mutex
	awes      map[string]aweSeen
	listening time.Time
}

type aweSeen struct {
	lastSeen time.Time
	agents   map[string]control.AgentStatus
}

func newAWEStatusTracker(brk broker.Broker, namespace string) *aweStatusTracker {
	return &aweStatusTracker{brk: brk, namespace: namespace, now: time.Now, wait: agentRolloutTimeout, awes: map[string]aweSeen{}}
}

func (t *aweStatusTracker) follow(ctx context.Context) error {
	queue, err := t.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create AWE status subscription: %w", err)
	}
	if err := t.brk.Subscribe(queue, a2a.AWEStatusTopic(t.namespace)); err != nil {
		_ = t.brk.DeleteQueue(queue.Name())
		return fmt.Errorf("subscribe AWE status: %w", err)
	}
	t.mu.Lock()
	t.listening = t.now()
	t.awes = map[string]aweSeen{}
	t.mu.Unlock()
	go func() {
		defer func() { _ = t.brk.DeleteQueue(queue.Name()) }()
		for {
			msg, err := t.brk.Receive(ctx, queue)
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				return
			}
			if err != nil || msg == nil {
				continue
			}
			msg.Ack()
			var status control.AWEStatus
			if json.Unmarshal(msg.Payload, &status) != nil || status.ProcessID == "" {
				continue
			}
			t.observe(status)
		}
	}()
	return nil
}

func (t *aweStatusTracker) observe(status control.AWEStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if status.Leaving {
		delete(t.awes, status.ProcessID)
		return
	}
	agents := make(map[string]control.AgentStatus, len(status.Agents))
	for _, a := range status.Agents {
		agents[a.Name] = a
	}
	t.awes[status.ProcessID] = aweSeen{lastSeen: t.now(), agents: agents}
}

func (t *aweStatusTracker) outcome(name, deploymentID string) (done bool, failure string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if t.listening.IsZero() || now.Sub(t.listening) < aweStatusSettle {
		return false, ""
	}
	live, applied := 0, 0
	for _, awe := range t.awes {
		if now.Sub(awe.lastSeen) > control.AWEStatusExpiry {
			continue
		}
		live++
		a, ok := awe.agents[name]
		if !ok || a.DeploymentID != deploymentID {
			continue
		}
		if a.Error != "" {
			return false, a.Error
		}
		applied++
	}
	return live > 0 && applied == live, ""
}

func (t *aweStatusTracker) removeQueues(instance string) error {
	var errs []error
	for _, kind := range []string{"a2a", "proxy"} {
		if err := t.brk.DeprovisionQueue(a2a.QueueName(t.namespace, kind, instance)); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return &runtimeError{fmt.Errorf("remove the queue of %s: %w", instance, err)}
	}
	return nil
}

func (t *aweStatusTracker) awaitLive(ctx context.Context) error {
	if t.anyLive() {
		return nil
	}
	wait, cancel := context.WithTimeout(ctx, t.wait)
	defer cancel()
	ticker := time.NewTicker(agentRolloutPoll)
	defer ticker.Stop()
	for {
		select {
		case <-wait.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errNoLiveAWE
		case <-ticker.C:
			if t.anyLive() {
				return nil
			}
		}
	}
}

func (t *aweStatusTracker) anyLive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.listening.IsZero() {
		return false
	}
	now := t.now()
	for _, awe := range t.awes {
		if now.Sub(awe.lastSeen) <= control.AWEStatusExpiry {
			return true
		}
	}
	return false
}

func (t *aweStatusTracker) rollOut(ctx context.Context, instance, deploymentID string) error {
	body, err := json.Marshal(control.AgentsChanged{Name: instance})
	if err != nil {
		return fmt.Errorf("encode agent change: %w", err)
	}
	if err := t.brk.Publish(ctx, a2a.AWEAgentsChangedTopic(t.namespace), &broker.Message{Payload: body}); err != nil {
		return &runtimeError{fmt.Errorf("publish agent change: %w", err)}
	}
	waitCtx, cancel := context.WithTimeout(ctx, t.wait)
	defer cancel()
	ticker := time.NewTicker(agentRolloutPoll)
	defer ticker.Stop()
	for {
		done, failure := t.outcome(instance, deploymentID)
		if failure != "" {
			return &runtimeError{fmt.Errorf("agent failed to start: %s", failure)}
		}
		if done {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if n := t.answer.tooLarge(); n > 0 {
				return &runtimeError{fmt.Errorf("the agent deployment was not confirmed by every AWE within %s: the list of deployed agents is %d bytes, over the broker's %d byte message limit, so AWEs receive no list", t.wait, n, broker.MaxMessageSize)}
			}
			return &runtimeError{fmt.Errorf("the agent deployment was not confirmed by every AWE within %s", t.wait)}
		case <-ticker.C:
		}
	}
}

func (t *aweStatusTracker) notify(ctx context.Context, instance string) error {
	body, err := json.Marshal(control.AgentsChanged{Name: instance})
	if err != nil {
		return err
	}
	return t.brk.Publish(ctx, a2a.AWEAgentsChangedTopic(t.namespace), &broker.Message{Payload: body})
}

func (s *DeploymentService) rollout(ctx context.Context, deployment *store.AgentDeployment, deployer Deployer, agent *store.Agent, payloads []ControlPayload) (*AgentDeploymentResponse, error) {
	instance := ""
	if !isEntrypointHosted(deployer) {
		if err := awaitLiveAWE(ctx, s.awes); err != nil {
			return nil, err
		}
		instance = deployer.InstanceName(agent)
	}
	if err := s.leader.begin(ctx, store.IntentKindAgent, deployment.AgentID, deployment.ID, agentIntent{Deployment: *deployment, Payloads: payloads, Instance: instance}); err != nil {
		return nil, err
	}
	return s.sendAndRecord(ctx, deployment, deployer, agent, payloads)
}

func (s *DeploymentService) sendAndRecord(ctx context.Context, deployment *store.AgentDeployment, deployer Deployer, agent *store.Agent, payloads []ControlPayload) (*AgentDeploymentResponse, error) {
	if len(payloads) > 0 {
		if encoded, err := json.Marshal(payloads); err == nil {
			stored := string(encoded)
			deployment.DeployedPayloads = &stored
		}
	}
	deployErr := s.deliver(ctx, deployment, deployer, agent)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	resp, err := s.recordDeployment(ctx, deployment, deployer.Kind(), deployErr)
	if err == nil {
		s.leader.end(ctx, deployment.ID)
		if isEntrypointHosted(deployer) {
			if deployErr != nil {
				s.links.settle(ctx, deployment)
			}
		} else {
			notifyAWEs(ctx, s.awes, deployer.InstanceName(agent), s.logger)
		}
	}
	return resp, err
}

func (s *DeploymentService) deliver(ctx context.Context, deployment *store.AgentDeployment, deployer Deployer, agent *store.Agent) error {
	if isEntrypointHosted(deployer) {
		return s.links.run(ctx, deployment)
	}
	instance := deployer.InstanceName(agent)
	err := rollOutOnAWEs(ctx, s.awes, instance, deployment.ID)
	if err == nil && deployment.Action == DeployActionUndeploy {
		err = removeUndeployedQueues(ctx, s.leader, s.awes, instance, deployment.ID)
	}
	return err
}
