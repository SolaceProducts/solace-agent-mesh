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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
	"gopkg.in/yaml.v3"
)

type agentCardLookup interface {
	Get(name string) *a2a.AgentCard
}

type evalWorkflowResolver struct {
	workflows   *store.WorkflowRepo
	deployments *store.WorkflowDeploymentRepo
	cards       agentCardLookup
	logger      *slog.Logger
	unparsed    sync.Map
}

func (r *evalWorkflowResolver) ResolveWorkflow(ctx context.Context, name string) (*eval.WorkflowTarget, error) {
	wf, err := r.workflowRow(ctx, name)
	if err != nil {
		return nil, err
	}
	if wf != nil {
		instance := a2a.InstanceName(a2a.KindWorkflow, wf.ID)
		schema := cardInputSchema(r.card(instance))
		if schema == nil {
			schema = appConfigInputSchema(wf.AppConfig)
		}
		return &eval.WorkflowTarget{Name: wf.Name, InstanceName: instance, InputSchema: schema}, nil
	}
	card := r.card(name)
	if card == nil || a2a.AgentTypeFromCard(*card) != string(a2a.KindWorkflow) {
		return nil, nil
	}
	return &eval.WorkflowTarget{Name: card.Name, InstanceName: card.Name, InputSchema: cardInputSchema(card)}, nil
}

func (r *evalWorkflowResolver) workflowRow(ctx context.Context, name string) (*store.Workflow, error) {
	if id, kind, ok := a2a.IDFromInstanceName(name); ok && kind == a2a.KindWorkflow {
		wf, err := r.workflows.GetByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("get workflow %s: %w", id, err)
		}
		return wf, nil
	}
	wf, err := r.workflows.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("get workflow %q: %w", name, err)
	}
	return wf, nil
}

func (r *evalWorkflowResolver) card(name string) *a2a.AgentCard {
	if r.cards == nil {
		return nil
	}
	return r.cards.Get(name)
}

func cardInputSchema(card *a2a.AgentCard) map[string]any {
	if card == nil {
		return nil
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != a2a.ExtensionURISchemas {
			continue
		}
		schema, _ := ext.Params["input_schema"].(map[string]any)
		return schema
	}
	return nil
}

func appConfigInputSchema(appConfig string) map[string]any {
	var cfg map[string]any
	if json.Unmarshal([]byte(appConfig), &cfg) != nil {
		return nil
	}
	if inner, ok := cfg["workflow"].(map[string]any); ok {
		cfg = inner
	}
	for _, key := range []string{"input_schema", "inputSchema"} {
		if schema, ok := cfg[key].(map[string]any); ok {
			return schema
		}
	}
	return nil
}

const msgEvalAuthorizationUnavailable = "Authorization state is unavailable on this instance"

type workflowNodeAgent struct {
	AgentName string
	NodeIDs   []string
}

func (r *evalWorkflowResolver) WorkflowNodeAgents(ctx context.Context, name string) (agents []workflowNodeAgent, complete bool, err error) {
	w := &workflowNodeAgentWalk{resolver: r, index: map[string]int{}, onPath: map[string]bool{}, complete: true}
	if err := w.walk(ctx, name, "", 0); err != nil {
		return nil, false, err
	}
	return w.agents, w.complete, nil
}

type workflowNodeAgentWalk struct {
	resolver *evalWorkflowResolver
	agents   []workflowNodeAgent
	index    map[string]int
	onPath   map[string]bool
	complete bool
}

func (w *workflowNodeAgentWalk) walk(ctx context.Context, name, prefix string, depth int) error {
	wf, err := w.resolver.workflowRow(ctx, name)
	if err != nil {
		return err
	}
	if wf == nil {
		w.complete = false
		return nil
	}
	cfg, err := w.resolver.runConfig(ctx, wf)
	if err != nil {
		return err
	}
	if cfg == nil {
		w.complete = false
		return nil
	}
	if w.onPath[wf.ID] || (cfg.MaxCallDepth > 0 && depth > cfg.MaxCallDepth) {
		return nil
	}
	w.onPath[wf.ID] = true
	defer delete(w.onPath, wf.ID)
	for _, node := range cfg.Nodes {
		nodeID := prefix + node.ID
		switch node.Type {
		case "agent":
			w.addNode(node.AgentName, nodeID)
		case "workflow":
			if err := w.walk(ctx, node.WorkflowName, nodeID+"/", depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *workflowNodeAgentWalk) addNode(agentName, nodeID string) {
	key := store.NormalizeName(agentName)
	if i, seen := w.index[key]; seen {
		w.agents[i].NodeIDs = append(w.agents[i].NodeIDs, nodeID)
		return
	}
	w.index[key] = len(w.agents)
	w.agents = append(w.agents, workflowNodeAgent{AgentName: agentName, NodeIDs: []string{nodeID}})
}

const (
	workflowConfigDeployed = "deployed"
	workflowConfigStored   = "stored"
)

func (r *evalWorkflowResolver) runConfig(ctx context.Context, wf *store.Workflow) (*workflow.WorkflowConfig, error) {
	raw, source, err := r.rawRunConfig(ctx, wf)
	if err != nil {
		return nil, err
	}
	if inner, ok := raw["workflow"].(map[string]any); ok {
		raw = inner
	}
	var cfg *workflow.WorkflowConfig
	if raw != nil {
		cfg, err = workflow.ParseWorkflowConfig(wf.Name, "", workflow.WithConnectorNodePlaceholders(raw))
	}
	key := wf.ID + "/" + source
	if cfg == nil || err != nil {
		r.warnUnparsed(key, wf.ID, source)
		return nil, nil
	}
	r.unparsed.Delete(key)
	return cfg, nil
}

func (r *evalWorkflowResolver) rawRunConfig(ctx context.Context, wf *store.Workflow) (raw map[string]any, source string, err error) {
	snapshot, err := r.deployedSnapshot(ctx, wf.ID)
	if err != nil {
		return nil, "", err
	}
	if snapshot != "" {
		var doc deploymentSnapshot
		if yaml.Unmarshal([]byte(snapshot), &doc) != nil || len(doc.Apps) == 0 {
			return nil, workflowConfigDeployed, nil
		}
		return doc.Apps[0].AppConfig, workflowConfigDeployed, nil
	}
	if json.Unmarshal([]byte(wf.AppConfig), &raw) != nil {
		return nil, workflowConfigStored, nil
	}
	return raw, workflowConfigStored, nil
}

func (r *evalWorkflowResolver) deployedSnapshot(ctx context.Context, workflowID string) (string, error) {
	if r.deployments == nil {
		return "", nil
	}
	deployed, err := r.deployments.DesiredFor(ctx, workflowID)
	if err != nil {
		return "", fmt.Errorf("read deployed workflow %s: %w", workflowID, err)
	}
	if deployed == nil || deployed.Action == DeployActionUndeploy {
		return "", nil
	}
	return derefStr(deployed.WorkflowConfigSnapshot), nil
}

func (r *evalWorkflowResolver) warnUnparsed(key, workflowID, source string) {
	if r.logger == nil {
		return
	}
	if _, logged := r.unparsed.LoadOrStore(key, true); !logged {
		r.logger.Warn("eval: workflow definition did not parse; its node agents are left off the tool-auth checklist", "workflowID", workflowID, "configSource", source)
	}
}

type evalWorkflowInvokeChecker struct {
	resolver evalPrincipalResolver
	logger   *slog.Logger
}

func newEvalWorkflowInvokeChecker(composer *rbac.Composer, logger *slog.Logger) eval.WorkflowInvokeChecker {
	if composer == nil {
		return nil
	}
	return &evalWorkflowInvokeChecker{resolver: composer, logger: logger}
}

func (c *evalWorkflowInvokeChecker) CanInvokeWorkflow(ctx context.Context, principal, instanceName string) (bool, error) {
	scope := rbac.WorkflowInvokeScopeForInstance(instanceName)
	res, err := c.resolver.Lookup(principal, evalLookupGatewayID, nil)
	if err != nil {
		return false, c.unavailable(principal, err)
	}
	if auth.HasScope(res.Scopes, scope) {
		return true, nil
	}
	res, err = rebuildAndLookup(ctx, c.resolver, principal)
	if errors.Is(err, errEvalSnapshotRefresh) {
		logWarn(c.logger, "eval workflow invoke check: authorization snapshot refresh failed; resolving against the stale snapshot",
			"userID", principal, "error", err)
		return false, nil
	}
	if err != nil {
		return false, c.unavailable(principal, err)
	}
	return auth.HasScope(res.Scopes, scope), nil
}

func (c *evalWorkflowInvokeChecker) unavailable(principal string, err error) error {
	logWarn(c.logger, "eval workflow invoke check: authorization lookup failed", "userID", principal, "error", err)
	return &UnavailableError{Message: msgEvalAuthorizationUnavailable, Cause: err}
}
