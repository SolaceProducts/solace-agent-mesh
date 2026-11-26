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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (r *Reconciler) answerAWE(ctx context.Context, msg *broker.Message) {
	process := msg.UserProperties[discovery.ProcessIDProperty]
	if process == "" || !a2a.ValidAWEAgentsReplyTopic(r.namespace, msg.ReplyTo) {
		r.logger.Warn("AWE agents request dropped: it names no AWE process or its reply topic is not an AWE reply topic", "aweProcess", process, "replyTo", msg.ReplyTo)
		return
	}
	logger := r.logger.With("aweProcess", process)
	if msg.UserProperties[control.FirstAskProperty] == "true" {
		r.pushAllBindings(ctx)
	}
	readCtx, cancel := context.WithTimeout(ctx, control.AWEAgentsAnswerWait)
	agents, err := r.desiredAWEAgents(readCtx)
	cancel()
	if err != nil {
		logger.Warn("AWE agents not read; the AWE asks again", "error", err)
		return
	}
	answer := control.AgentsReply{Agents: agents}
	payload, err := json.Marshal(answer)
	if err != nil {
		logger.Error("AWE agents answer not encoded", "error", err)
		return
	}
	crossed, back := r.answerSize().record(len(payload))
	if back {
		logger.Info("AWE agents answer fits in one broker message again", "bytes", len(payload), "agents", len(agents))
	}
	if len(payload) > broker.MaxMessageSize {
		level := slog.LevelDebug
		if crossed {
			level = slog.LevelError
		}
		logger.Log(ctx, level, "AWE agents answer is larger than one broker message; AWEs get no answer and every deploy fails until agents are removed", "bytes", len(payload), "limit", broker.MaxMessageSize, "agents", len(agents))
		return
	}
	if err := r.brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: payload, CorrelationID: msg.CorrelationID}); err != nil {
		logger.Warn("AWE agents answer not sent; the AWE asks again", "error", err)
		return
	}
	logger.Debug("answered an AWE's request for its agents", "agents", len(answer.Agents))
}

func (r *Reconciler) answerSize() *aweAnswerSize {
	if r.deploys == nil {
		return &r.answerWithoutLeader
	}
	return &r.deploys.aweAnswer
}

func (r *Reconciler) desiredAWEAgents(ctx context.Context) ([]control.AgentApply, error) {
	agents, err := r.agentRepo.GetAllWithEphemeral(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	agentIntents, err := r.newestIntents(ctx, store.IntentKindAgent)
	if err != nil {
		return nil, err
	}
	agentRows, err := r.agentDeploymentRepo.Desired(ctx)
	if err != nil {
		return nil, fmt.Errorf("read agent deployments: %w", err)
	}
	out := []control.AgentApply{}
	listed := map[string]bool{}
	for _, agent := range agents {
		listed[agent.ID] = true
		if r.builtinFeatureDisabled(agent.Name) {
			continue
		}
		if apply := r.desiredAgentApply(ctx, agent, agentIntents[agent.ID], agentRows[agent.ID]); apply != nil {
			out = append(out, *apply)
		}
	}
	for objectID, intent := range agentIntents {
		if listed[objectID] {
			continue
		}
		var pending agentIntent
		if json.Unmarshal([]byte(intent.Payload), &pending) == nil && pending.Instance != "" && pending.Deployment.Action == DeployActionUndeploy {
			out = append(out, *agentApply(pending.Instance, pending.Deployment.ID, pending.Deployment.Action, pending.Payloads))
		}
	}
	if r.workflowRepo == nil || r.workflowDeploymentRepo == nil {
		return out, nil
	}
	workflows, err := r.workflowRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	workflowIntents, err := r.newestIntents(ctx, store.IntentKindWorkflow)
	if err != nil {
		return nil, err
	}
	workflowRows, err := r.workflowDeploymentRepo.Desired(ctx)
	if err != nil {
		return nil, fmt.Errorf("read workflow deployments: %w", err)
	}
	listed = map[string]bool{}
	for i := range workflows {
		wf := &workflows[i]
		listed[wf.ID] = true
		if apply := r.desiredWorkflowApply(ctx, wf, workflowIntents[wf.ID], workflowRows[wf.ID]); apply != nil {
			out = append(out, *apply)
		}
	}
	for objectID, intent := range workflowIntents {
		var want workflowWant
		if listed[objectID] || !workflowIntentInto(intent, &want) || want.action != DeployActionUndeploy {
			continue
		}
		out = append(out, *agentApply(a2a.InstanceName(a2a.KindWorkflow, objectID), want.id, want.action, nil))
	}
	return out, nil
}

func (r *Reconciler) newestIntents(ctx context.Context, kind string) (map[string]*store.DeploymentIntent, error) {
	if r.deploys == nil || r.deploys.intents == nil {
		return map[string]*store.DeploymentIntent{}, nil
	}
	intents, err := r.deploys.intents.Newest(ctx, kind)
	if err != nil {
		return nil, fmt.Errorf("read %s deployments in flight: %w", kind, err)
	}
	return intents, nil
}

func (r *Reconciler) desiredAgentApply(ctx context.Context, agent *store.Agent, intent *store.DeploymentIntent, row *store.AgentDeployment) *control.AgentApply {
	if r.deployers == nil {
		return nil
	}
	deployer := r.deployers.Lookup(agent.Type)
	if deployer == nil || isEntrypointHosted(deployer) {
		return nil
	}
	instance := deployer.InstanceName(agent)
	if intent != nil {
		var pending agentIntent
		if json.Unmarshal([]byte(intent.Payload), &pending) == nil {
			return agentApply(instance, pending.Deployment.ID, pending.Deployment.Action, pending.Payloads)
		}
	}
	if row == nil {
		return nil
	}
	if row.Action == DeployActionUndeploy {
		return agentApply(instance, row.ID, row.Action, nil)
	}
	payloads, err := r.deployedAgentPayloads(ctx, agent, deployer, row)
	if err != nil {
		r.logUnbuildable(ctx, "deployed agent config cannot be built; AWEs keep what they run", row.ID, "agentID", agent.ID, "error", err)
		return &control.AgentApply{Name: instance, DeploymentID: row.ID}
	}
	return agentApply(instance, row.ID, row.Action, payloads)
}

func (r *Reconciler) deployedAgentPayloads(ctx context.Context, agent *store.Agent, deployer Deployer, row *store.AgentDeployment) ([]ControlPayload, error) {
	if stored := derefStr(row.DeployedPayloads); stored != "" {
		var payloads []ControlPayload
		if err := json.Unmarshal([]byte(stored), &payloads); err != nil {
			return nil, fmt.Errorf("decode deployed payloads: %w", err)
		}
		return payloads, nil
	}
	if snapshot := derefStr(row.AgentConfigSnapshot); snapshot != "" {
		replayed, err := deployer.ReplayFromSnapshot(snapshot)
		if err == nil {
			return replayed, nil
		}
		if !errors.Is(err, ErrNoReplay) {
			return nil, fmt.Errorf("replay deployment snapshot: %w", err)
		}
	}
	input, err := r.buildDeployInput(ctx, agent)
	if err != nil {
		return nil, fmt.Errorf("load agent: %w", err)
	}
	input.Opts.DeploymentID = row.ID
	result, err := deployer.BuildPayloads(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("rebuild payloads: %w", err)
	}
	return result.Payloads, nil
}

func (r *Reconciler) desiredWorkflowApply(ctx context.Context, wf *store.Workflow, intent *store.DeploymentIntent, row *store.WorkflowDeployment) *control.AgentApply {
	instance := a2a.InstanceName(a2a.KindWorkflow, wf.ID)
	var want workflowWant
	switch {
	case intent != nil && workflowIntentInto(intent, &want):
	case row != nil:
		want = workflowWant{id: row.ID, action: row.Action, yaml: derefStr(row.WorkflowConfigSnapshot)}
	default:
		return nil
	}
	if want.action == DeployActionUndeploy {
		return agentApply(instance, want.id, want.action, nil)
	}
	yamlStr := want.yaml
	if yamlStr == "" {
		var err error
		if yamlStr, err = r.generateDeployableWorkflowYAML(ctx, wf); err != nil {
			r.logUnbuildable(ctx, "deployed workflow config cannot be built; AWEs keep what they run", want.id, "workflowID", wf.ID, "error", err)
			return &control.AgentApply{Name: instance, DeploymentID: want.id}
		}
	}
	payloads, err := parseYAMLToControlPayloads([]byte(yamlStr))
	if err != nil {
		r.logUnbuildable(ctx, "deployed workflow config does not parse; AWEs keep what they run", want.id, "workflowID", wf.ID, "error", err)
		return &control.AgentApply{Name: instance, DeploymentID: want.id}
	}
	return agentApply(instance, want.id, want.action, payloads)
}

type workflowWant struct {
	id, action, yaml string
}

func workflowIntentInto(intent *store.DeploymentIntent, want *workflowWant) bool {
	var pending workflowIntent
	if json.Unmarshal([]byte(intent.Payload), &pending) != nil {
		return false
	}
	*want = workflowWant{id: pending.Deployment.ID, action: pending.Deployment.Action, yaml: pending.YAML}
	return true
}

func agentApply(instance, deploymentID, action string, payloads []ControlPayload) *control.AgentApply {
	if action == DeployActionUndeploy {
		return &control.AgentApply{Name: instance, DeploymentID: deploymentID, Undeploy: true}
	}
	apply := &control.AgentApply{Name: instance, DeploymentID: deploymentID}
	for _, payload := range payloads {
		if payload != nil {
			apply.Payloads = append(apply.Payloads, payload)
		}
	}
	return apply
}

func (r *Reconciler) logUnbuildable(ctx context.Context, msg, deploymentID string, args ...any) {
	level := slog.LevelDebug
	if _, reported := r.unbuildableReported.LoadOrStore(deploymentID, struct{}{}); !reported {
		level = slog.LevelWarn
	}
	r.logger.Log(ctx, level, msg, append(args, "deploymentID", deploymentID)...)
}
