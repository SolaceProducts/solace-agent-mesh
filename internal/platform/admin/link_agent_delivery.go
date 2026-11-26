// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/agentlink"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func entrypointIsServing(apply *control.EntrypointApply) bool {
	return apply != nil && !apply.Undeploy && apply.AppConfig != nil
}

func entrypointConfigUnreadable(apply *control.EntrypointApply) bool {
	return apply != nil && !apply.Undeploy && apply.AppConfig == nil
}

type droppedLinkAgentKey struct {
	agentID      string
	deploymentID string
}

const droppedLinkAgentReportEvery = time.Hour

type droppedLinkAgentReports struct {
	mu           sync.Mutex
	lastReported map[droppedLinkAgentKey]time.Time
}

func (r *droppedLinkAgentReports) due(agentID, deploymentID string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, at := range r.lastReported {
		if now.Sub(at) >= droppedLinkAgentReportEvery {
			delete(r.lastReported, key)
		}
	}
	key := droppedLinkAgentKey{agentID: agentID, deploymentID: deploymentID}
	if _, reported := r.lastReported[key]; reported {
		return false
	}
	if r.lastReported == nil {
		r.lastReported = map[droppedLinkAgentKey]time.Time{}
	}
	r.lastReported[key] = now
	return true
}

var droppedLinkAgents droppedLinkAgentReports

func hostsLinkAgents(gw *store.Gateway, apply *control.EntrypointApply) bool {
	return gw != nil && gw.Type == platpkg.AgentLinkGatewayType && entrypointIsServing(apply)
}

func deliveredLinkDeployment(recorded *store.AgentDeployment, inFlight *store.DeploymentIntent) *store.AgentDeployment {
	if inFlight != nil {
		var pending agentIntent
		if json.Unmarshal([]byte(inFlight.Payload), &pending) == nil {
			return &pending.Deployment
		}
	}
	return recorded
}

type linkDeliveryReads struct {
	inFlight func(ctx context.Context) (map[string]*store.DeploymentIntent, error)
	recorded func(ctx context.Context) (map[string]*store.AgentDeployment, error)
}

func linkDeliveryReadsFrom(db *store.DB) linkDeliveryReads {
	return linkDeliveryReads{
		inFlight: func(ctx context.Context) (map[string]*store.DeploymentIntent, error) {
			return store.NewDeploymentIntentRepo(db).Newest(ctx, store.IntentKindAgent)
		},
		recorded: store.NewLinkAgentRepo(db).DeliveredDeployments,
	}
}

func deliveredLinkAgentDeployments(ctx context.Context, db *store.DB, ids []string) (map[string]*store.AgentDeployment, error) {
	return linkDeliveryReadsFrom(db).delivered(ctx, ids)
}

func (reads linkDeliveryReads) delivered(ctx context.Context, ids []string) (map[string]*store.AgentDeployment, error) {
	inFlight, err := reads.inFlight(ctx)
	if err != nil {
		return nil, fmt.Errorf("load link agent deployments in flight: %w", err)
	}
	recorded, err := reads.recorded(ctx)
	if err != nil {
		return nil, err
	}
	delivered := make(map[string]*store.AgentDeployment, len(ids))
	for _, id := range ids {
		if deployment := deliveredLinkDeployment(recorded[id], inFlight[id]); deployment != nil {
			delivered[id] = deployment
		}
	}
	return delivered, nil
}

func deliveredLinkAgentDeployment(ctx context.Context, db *store.DB, agentID string) (*store.AgentDeployment, error) {
	delivered, err := deliveredLinkAgentDeployments(ctx, db, []string{agentID})
	if err != nil {
		return nil, err
	}
	return delivered[agentID], nil
}

func linkAgentsDeployed(ctx context.Context, db *store.DB, logger *slog.Logger) ([]deployedLinkAgent, error) {
	if logger == nil {
		logger = slog.Default()
	}
	ids, err := store.NewLinkAgentRepo(db).IDs(ctx)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	delivered, err := deliveredLinkAgentDeployments(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	var deployed []deployedLinkAgent
	for _, id := range ids {
		deployment := delivered[id]
		if deployment == nil || deployment.Action == DeployActionUndeploy {
			continue
		}
		if agent, ok := deployedLinkAgentOf(id, deployment, logger); ok {
			deployed = append(deployed, agent)
		}
	}
	return deployed, nil
}

func deployedLinkAgentOf(agentID string, deployment *store.AgentDeployment, logger *slog.Logger) (deployedLinkAgent, bool) {
	snapshot, ok := readLinkAgentSnapshot(deployment.AgentConfigSnapshot)
	if !ok {
		if droppedLinkAgents.due(agentID, deployment.ID, time.Now()) {
			logger.Error("deployed link agent's deployment holds no config; the agent is left out until it is deployed again",
				"linkAgentID", agentID, "deploymentID", deployment.ID)
		}
		return deployedLinkAgent{}, false
	}
	if _, err := agentlink.ParseConfig(map[string]any{agentLinkPlatformManagedKey: true, agentLinkAgentsKey: []any{snapshot.Agent}}); err != nil {
		if droppedLinkAgents.due(agentID, deployment.ID, time.Now()) {
			logger.Error("deployed link agent's config is refused by the agent link; the agent is left out until it is deployed again",
				"linkAgentID", agentID, "deploymentID", deployment.ID, "error", err)
		}
		return deployedLinkAgent{}, false
	}
	return deployedLinkAgent{agentID: agentID, deploymentID: deployment.ID, entrypointID: snapshot.EntrypointID, config: snapshot.Agent}, true
}

func onEntrypoint(deployed []deployedLinkAgent, entrypointID string) []deployedLinkAgent {
	var hosted []deployedLinkAgent
	for _, d := range deployed {
		if d.entrypointID == entrypointID {
			hosted = append(hosted, d)
		}
	}
	return hosted
}

func linkAgentsDeployedOn(ctx context.Context, db *store.DB, entrypointID string, logger *slog.Logger) ([]deployedLinkAgent, error) {
	deployed, err := linkAgentsDeployed(ctx, db, logger)
	if err != nil {
		return nil, err
	}
	return onEntrypoint(deployed, entrypointID), nil
}

func mergeLinkAgents(apply *control.EntrypointApply, deployed []deployedLinkAgent, logger *slog.Logger) {
	adapterConfig := entrypoint.AdapterConfig(apply.AppConfig)
	if adapterConfig == nil {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("deployed agent_link entrypoint has no adapter_config; its link agents are left out", "entrypointID", apply.Name)
		return
	}
	ordered := slices.Clone(deployed)
	slices.SortFunc(ordered, func(a, b deployedLinkAgent) int { return cmp.Compare(a.agentID, b.agentID) })
	agents := make([]any, len(ordered))
	for i, d := range ordered {
		agents[i] = d.config
	}
	adapterConfig[agentLinkPlatformManagedKey] = true
	adapterConfig[agentLinkAgentsKey] = agents
	apply.Revision = linkAgentsRevision(ordered)
}

func deployedLinkAgentNames(ctx context.Context, db *store.DB, entrypointID string, logger *slog.Logger) ([]string, error) {
	deployed, err := linkAgentsDeployedOn(ctx, db, entrypointID, logger)
	if err != nil || len(deployed) == 0 {
		return nil, err
	}
	agents, err := store.NewLinkAgentRepo(db).ListAll(ctx)
	if err != nil {
		return nil, err
	}
	current := make(map[string]string, len(agents))
	for _, a := range agents {
		current[a.ID] = a.Name
	}
	names := make([]string, len(deployed))
	for i, d := range deployed {
		names[i] = current[d.agentID]
	}
	slices.Sort(names)
	return names, nil
}

func refuseWhileLinkAgentsDeployed(ctx context.Context, db *store.DB, gw *store.Gateway, doing string, logger *slog.Logger) error {
	if db == nil || gw == nil || gw.Type != platpkg.AgentLinkGatewayType {
		return nil
	}
	names, err := deployedLinkAgentNames(ctx, db, gw.ID, logger)
	if err != nil {
		return fmt.Errorf("read the entrypoint's link agents: %w", err)
	}
	if len(names) == 0 {
		return nil
	}
	return &ValidationError{
		Message: fmt.Sprintf("cannot %s this entrypoint while link agents are deployed on it: %s; undeploy them first", doing, strings.Join(names, ", ")),
		Field:   fieldEntrypointID,
	}
}

func refuseWhileLinkAgentsAssigned(ctx context.Context, db *store.DB, gw *store.Gateway, logger *slog.Logger) error {
	if db == nil || gw == nil || gw.Type != platpkg.AgentLinkGatewayType {
		return nil
	}
	if err := refuseWhileLinkAgentsDeployed(ctx, db, gw, "delete", logger); err != nil {
		return err
	}
	assigned, err := store.NewLinkAgentRepo(db).ListByEntrypoint(ctx, gw.ID)
	if err != nil {
		return fmt.Errorf("read the entrypoint's link agents: %w", err)
	}
	if len(assigned) == 0 {
		return nil
	}
	names := make([]string, len(assigned))
	for i, a := range assigned {
		names[i] = a.Name
	}
	slices.Sort(names)
	return &ValidationError{
		Message: fmt.Sprintf("cannot delete this entrypoint while link agents connect through it: %s; delete them or move them to another entrypoint first", strings.Join(names, ", ")),
		Field:   fieldEntrypointID,
	}
}

type linkAgentRollout struct {
	db        *store.DB
	yamlSvc   *YAMLService
	gwes      *gweStatusTracker
	serialize func(ctx context.Context, entrypointID string) (release func(), err error)
	alone     sync.Mutex
	logger    *slog.Logger

	confirmWithin time.Duration
}

func (r *linkAgentRollout) run(ctx context.Context, deployment *store.AgentDeployment) error {
	if r == nil || r.gwes == nil {
		return &runtimeError{errors.New("link agent deployments are not available: no broker connection")}
	}
	undeploying := deployment.Action == DeployActionUndeploy
	entrypointID, incoming, err := r.target(ctx, deployment)
	if err != nil {
		return err
	}
	release, err := r.oneAtATime(ctx, entrypointID)
	if err != nil {
		return err
	}
	defer release()
	deployed, err := linkAgentsDeployed(ctx, r.db, r.logger)
	if err != nil {
		return fmt.Errorf("read the deployed link agents: %w", err)
	}
	hosting, err := DeployedEntrypoint(ctx, r.db, r.yamlSvc, r.logger, entrypointID)
	if err != nil {
		return fmt.Errorf("read the link agent's entrypoint: %w", err)
	}
	if entrypointConfigUnreadable(hosting) {
		return errLinkEntrypointUnreadable(entrypointID)
	}
	if !entrypointIsServing(hosting) {
		if !undeploying {
			return errLinkEntrypointNotDeployed(ctx, r.db, entrypointID)
		}
		return r.removeQueues(deployment.AgentID)
	}
	mergeLinkAgents(hosting, afterDeployment(onEntrypoint(deployed, entrypointID), deployment.AgentID, incoming), r.logger)
	hosting.Attempt = idgen.New()
	if err := r.publish(ctx, hosting); err != nil {
		return err
	}
	confirmCtx, cancelConfirm := r.confirmationContext(ctx)
	defer cancelConfirm()
	if err := awaitGWEs(confirmCtx, "the link agent deployment", func() (bool, string) {
		return r.gwes.attemptOutcome(hosting.Name, hosting.Revision, hosting.Attempt)
	}); err != nil {
		return err
	}
	if undeploying {
		return r.removeQueues(deployment.AgentID)
	}
	return nil
}

func (r *linkAgentRollout) confirmationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.confirmWithin <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, r.confirmWithin)
}

func (r *linkAgentRollout) oneAtATime(ctx context.Context, entrypointID string) (release func(), err error) {
	if r.serialize == nil {
		r.alone.Lock()
		return r.alone.Unlock, nil
	}
	return r.serialize(ctx, entrypointID)
}

func (r *linkAgentRollout) target(ctx context.Context, deployment *store.AgentDeployment) (entrypointID string, incoming *deployedLinkAgent, err error) {
	if deployment.Action != DeployActionUndeploy {
		snapshot, ok := readLinkAgentSnapshot(deployment.AgentConfigSnapshot)
		if !ok {
			return "", nil, fmt.Errorf("link agent deployment %s holds no config", deployment.ID)
		}
		return snapshot.EntrypointID, &deployedLinkAgent{
			agentID: deployment.AgentID, deploymentID: deployment.ID, entrypointID: snapshot.EntrypointID, config: snapshot.Agent,
		}, nil
	}
	repo := store.NewLinkAgentRepo(r.db)
	recorded, err := repo.DeliveredDeployments(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("read the link agent's deployment: %w", err)
	}
	if last := recorded[deployment.AgentID]; last != nil && last.Action != DeployActionUndeploy {
		if snapshot, ok := readLinkAgentSnapshot(last.AgentConfigSnapshot); ok {
			return snapshot.EntrypointID, nil, nil
		}
	}
	link, err := repo.GetByID(ctx, deployment.AgentID)
	if err != nil {
		return "", nil, fmt.Errorf("load link agent: %w", err)
	}
	if link == nil {
		return "", nil, &NotFoundError{Resource: resourceLinkAgent, ID: deployment.AgentID}
	}
	return link.EntrypointID, nil, nil
}

func afterDeployment(hosted []deployedLinkAgent, agentID string, incoming *deployedLinkAgent) []deployedLinkAgent {
	after := slices.DeleteFunc(slices.Clone(hosted), func(d deployedLinkAgent) bool { return d.agentID == agentID })
	if incoming == nil {
		return after
	}
	return append(after, *incoming)
}

func (r *linkAgentRollout) settle(ctx context.Context, deployment *store.AgentDeployment) {
	if r == nil || r.gwes == nil {
		return
	}
	entrypointID, _, err := r.target(ctx, deployment)
	if err != nil {
		return
	}
	release, err := r.oneAtATime(ctx, entrypointID)
	if err != nil {
		return
	}
	defer release()
	hosting, err := DeployedEntrypoint(ctx, r.db, r.yamlSvc, r.logger, entrypointID)
	if err != nil || !entrypointIsServing(hosting) {
		return
	}
	if err := r.publish(ctx, hosting); err != nil {
		r.logger.Warn("link agent's entrypoint not told of a failed deployment; an Executor that reads the Platform's tables catches up at its next check, any other at the entrypoint's next change",
			"linkAgentID", deployment.AgentID, "deploymentID", deployment.ID, "error", err)
	}
}

func (r *linkAgentRollout) publish(ctx context.Context, hosting *control.EntrypointApply) error {
	body, err := json.Marshal(hosting)
	if err != nil {
		return fmt.Errorf("encode link agent deployment: %w", err)
	}
	if err := r.gwes.brk.Publish(ctx, a2a.EntrypointApplyTopic(r.gwes.namespace), &broker.Message{Payload: body}); err != nil {
		return &runtimeError{fmt.Errorf("publish link agent deployment: %w", err)}
	}
	return nil
}

func (r *linkAgentRollout) removeQueues(agentID string) error {
	var errs []error
	for _, queue := range a2a.LinkAgentQueueNames(r.gwes.namespace, linkAgentMeshName(agentID)) {
		if err := r.gwes.brk.DeprovisionQueue(queue); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return &runtimeError{fmt.Errorf("remove the link agent's queues: %w", err)}
	}
	return nil
}
