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
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const (
	entrypointRolloutTimeout = 60 * time.Second
	entrypointRolloutPoll    = 100 * time.Millisecond
	gweStatusSettle          = control.GatewayStatusInterval + time.Second
)

func followGWEsForTerm(ctx context.Context, t *gweStatusTracker, logger *slog.Logger) {
	if err := t.follow(ctx); err != nil {
		logger.Error("GWE status not followed; entrypoint deployments fail this term", "error", err)
	}
}

type gweStatusTracker struct {
	brk       broker.Broker
	namespace string
	now       func() time.Time

	mu        sync.Mutex
	gwes      map[string]gweSeen
	listening time.Time
}

type gweSeen struct {
	lastSeen    time.Time
	entrypoints map[string]control.EntrypointStatus
}

func newGWEStatusTracker(brk broker.Broker, namespace string) *gweStatusTracker {
	return &gweStatusTracker{brk: brk, namespace: namespace, now: time.Now, gwes: map[string]gweSeen{}}
}

func (t *gweStatusTracker) follow(ctx context.Context) error {
	queue, err := t.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create GWE status subscription: %w", err)
	}
	if err := t.brk.Subscribe(queue, a2a.GWEStatusTopic(t.namespace)); err != nil {
		_ = t.brk.DeleteQueue(queue.Name())
		return fmt.Errorf("subscribe GWE status: %w", err)
	}
	t.mu.Lock()
	t.listening = t.now()
	t.gwes = map[string]gweSeen{}
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
			var status control.GatewayStatus
			if json.Unmarshal(msg.Payload, &status) != nil || status.ProcessID == "" {
				continue
			}
			t.observe(status)
		}
	}()
	return nil
}

func (t *gweStatusTracker) observe(status control.GatewayStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if status.Leaving {
		delete(t.gwes, status.ProcessID)
		return
	}
	entrypoints := make(map[string]control.EntrypointStatus, len(status.Entrypoints))
	for _, e := range status.Entrypoints {
		entrypoints[e.Name] = e
	}
	t.gwes[status.ProcessID] = gweSeen{lastSeen: t.now(), entrypoints: entrypoints}
}

func (t *gweStatusTracker) outcome(name, deploymentID string) (done bool, failure string) {
	return t.appliedEverywhere(name, func(e control.EntrypointStatus) bool { return e.DeploymentID == deploymentID })
}

func (t *gweStatusTracker) attemptOutcome(name, revision, attempt string) (done bool, failure string) {
	return t.appliedEverywhere(name, func(e control.EntrypointStatus) bool {
		return e.Revision == revision && (e.Attempt == attempt || (e.Attempt == "" && e.Error == ""))
	})
}

func (t *gweStatusTracker) appliedEverywhere(name string, isExpected func(control.EntrypointStatus) bool) (done bool, failure string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if t.listening.IsZero() || now.Sub(t.listening) < gweStatusSettle {
		return false, ""
	}
	live, applied := 0, 0
	for _, gwe := range t.gwes {
		if now.Sub(gwe.lastSeen) > control.GatewayStatusExpiry {
			continue
		}
		live++
		e, ok := gwe.entrypoints[name]
		if !ok || !isExpected(e) {
			continue
		}
		if e.Error != "" {
			return false, e.Error
		}
		applied++
	}
	return live > 0 && applied == live, ""
}

func (s *GatewayDeploymentService) rollOutEntrypoint(ctx context.Context, deployment *store.GatewayDeployment, wireID string, payloads []ControlPayload) error {
	if s.gwes == nil {
		return &runtimeError{errors.New("entrypoint deployments are not available: no broker connection")}
	}
	apply := entrypointApply(deployment.GatewayID, deployment.ID, deployment.Action, payloads)
	if apply == nil {
		return fmt.Errorf("entrypoint deployment %s has no config", deployment.ID)
	}
	if err := s.mergeLinkAgents(ctx, deployment.GatewayID, apply); err != nil {
		return err
	}
	body, err := json.Marshal(apply)
	if err != nil {
		return fmt.Errorf("encode entrypoint deployment: %w", err)
	}
	if err := s.gwes.brk.Publish(ctx, a2a.EntrypointApplyTopic(s.gwes.namespace), &broker.Message{Payload: body}); err != nil {
		return &runtimeError{fmt.Errorf("publish entrypoint deployment: %w", err)}
	}
	if err := awaitGWEs(ctx, "the entrypoint deployment", func() (bool, string) {
		return s.gwes.outcome(deployment.GatewayID, deployment.ID)
	}); err != nil {
		return err
	}
	if deployment.Action == DeployActionUndeploy {
		var errs []error
		for _, queue := range a2a.EntrypointQueueNames(s.gwes.namespace, wireID) {
			if err := s.gwes.brk.DeprovisionQueue(queue); err != nil {
				errs = append(errs, err)
			}
		}
		s.removeExternalQueues(ctx, deployment.GatewayID)
		if err := errors.Join(errs...); err != nil {
			return &runtimeError{fmt.Errorf("remove the entrypoint's queues: %w", err)}
		}
	}
	return nil
}

func awaitGWEs(ctx context.Context, what string, outcome func() (done bool, failure string)) error {
	waitCtx, cancel := context.WithTimeout(ctx, entrypointRolloutTimeout)
	defer cancel()
	ticker := time.NewTicker(entrypointRolloutPoll)
	defer ticker.Stop()
	for {
		done, failure := outcome()
		if failure != "" {
			return &runtimeError{fmt.Errorf("entrypoint failed to start: %s", failure)}
		}
		if done {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return &runtimeError{fmt.Errorf("%s was not confirmed within %s", what, entrypointRolloutTimeout)}
		case <-ticker.C:
		}
	}
}

func (s *GatewayDeploymentService) mergeLinkAgents(ctx context.Context, gatewayID string, apply *control.EntrypointApply) error {
	if s.gatewayRepo == nil || !entrypointIsServing(apply) {
		return nil
	}
	gw, err := s.gatewayRepo.GetByID(ctx, gatewayID)
	if err != nil {
		return fmt.Errorf("get gateway: %w", err)
	}
	if !hostsLinkAgents(gw, apply) {
		return nil
	}
	deployed, err := linkAgentsDeployedOn(ctx, s.gatewayRepo.DB(), gatewayID, s.logger)
	if err != nil {
		return fmt.Errorf("read the entrypoint's link agents: %w", err)
	}
	mergeLinkAgents(apply, deployed, s.logger)
	return nil
}

func (s *GatewayDeploymentService) removeExternalQueues(ctx context.Context, gatewayID string) {
	if s.deploymentRepo == nil {
		return
	}
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	deploys, err := s.deploymentRepo.GetLatestSuccessfulDeploys(ctx, []string{gatewayID})
	if err != nil {
		logger.Warn("undeployed entrypoint's queues outside SAM not removed: its last deploy could not be read", "entrypointID", gatewayID, "error", err)
		return
	}
	last := deploys[gatewayID]
	if last == nil || derefStr(last.GatewayConfigSnapshot) == "" {
		return
	}
	apps, err := schema.ParseAppsConfig([]byte(*last.GatewayConfigSnapshot))
	if err != nil {
		logger.Warn("undeployed entrypoint's queues outside SAM not removed: its last config does not parse", "entrypointID", gatewayID, "error", err)
		return
	}
	for _, app := range apps {
		appConfig, _ := app["app_config"].(map[string]any)
		if appConfig == nil {
			continue
		}
		if err := entrypoint.RemoveExternalQueues(ctx, appConfig); err != nil {
			logger.Warn("undeployed entrypoint left queues on a broker outside SAM", "entrypointID", gatewayID, "error", err)
		}
	}
}
