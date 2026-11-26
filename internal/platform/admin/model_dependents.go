// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const errUndeployDependentsWrap = "undeploy dependents: %w"

type ModelDependentsService interface {
	GetDependents(ctx context.Context, alias string) ([]ModelDependentResponse, error)

	ListDependentsPage(ctx context.Context, alias string, offset, limit int, sortField string, desc bool) ([]ModelDependentResponse, int, error)

	UndeployDependents(ctx context.Context, alias string) ([]ModelDependentResponse, error)
}

type NoOpModelDependentsHandler struct{}

func (h *NoOpModelDependentsHandler) GetDependents(_ context.Context, _ string) ([]ModelDependentResponse, error) {
	return []ModelDependentResponse{}, nil
}

func (h *NoOpModelDependentsHandler) ListDependentsPage(_ context.Context, _ string, _, _ int, _ string, _ bool) ([]ModelDependentResponse, int, error) {
	return []ModelDependentResponse{}, 0, nil
}

func (h *NoOpModelDependentsHandler) UndeployDependents(_ context.Context, _ string) ([]ModelDependentResponse, error) {
	return nil, nil
}

type ModelDependentsHandler struct {
	agentRepo         *store.AgentRepo
	workflowRepo      *store.WorkflowRepo
	workflowRefs      workflowModelRefs
	modelConfigRepo   *store.ModelConfigRepo
	deploySvc         *DeploymentService
	workflowUndeploys workflowUndeployer
	logger            *slog.Logger
}

type workflowUndeployer interface {
	Create(ctx context.Context, req CreateWorkflowDeploymentRequest, userID string) (*WorkflowDeploymentResponse, error)
}

func (h *ModelDependentsHandler) SetWorkflowUndeployer(u workflowUndeployer) {
	h.workflowUndeploys = u
}

func NewModelDependentsHandler(
	agentRepo *store.AgentRepo,
	workflowRepo *store.WorkflowRepo,
	workflowDeploymentRepo *store.WorkflowDeploymentRepo,
	modelConfigRepo *store.ModelConfigRepo,
	deploySvc *DeploymentService,
	logger *slog.Logger,
) *ModelDependentsHandler {
	return &ModelDependentsHandler{
		agentRepo:       agentRepo,
		workflowRepo:    workflowRepo,
		workflowRefs:    workflowModelRefs{workflows: workflowRepo, deployments: workflowDeploymentRepo},
		modelConfigRepo: modelConfigRepo,
		deploySvc:       deploySvc,
		logger:          logger,
	}
}

func (h *ModelDependentsHandler) GetDependents(ctx context.Context, alias string) ([]ModelDependentResponse, error) {
	idToAlias, err := h.loadModelIDToAlias(ctx)
	if err != nil {
		h.logger.Warn("load model configurations for dependents lookup; falling back to raw compare",
			"error", err)
		idToAlias = nil
	}
	agents, err := h.getDependentsUsing(ctx, alias, idToAlias)
	if err != nil {
		return nil, err
	}
	workflows, err := h.workflowDependents(ctx, alias, idToAlias)
	if err != nil {
		return nil, err
	}
	agents, err = h.withMayStillRun(ctx, agents, h.latestAgentDeployments)
	if err != nil {
		return nil, err
	}
	workflows, err = h.withMayStillRun(ctx, workflows, h.latestWorkflowDeployments)
	if err != nil {
		return nil, err
	}
	return append(agents, workflows...), nil
}

func (h *ModelDependentsHandler) ListDependentsPage(ctx context.Context, alias string, offset, limit int, sortField string, desc bool) ([]ModelDependentResponse, int, error) {
	all, err := h.GetDependents(ctx, alias)
	if err != nil {
		return nil, 0, err
	}
	sortModelDependents(all, sortField, desc)
	start := min(offset, len(all))
	end := min(start+limit, len(all))
	return all[start:end], len(all), nil
}

func sortModelDependents(dependents []ModelDependentResponse, sortField string, desc bool) {
	byName := sortField == "name"
	slices.SortStableFunc(dependents, func(a, b ModelDependentResponse) int {
		if !byName {
			if c := cmp.Compare(kindRank(a), kindRank(b)); c != 0 {
				return orient(c, desc)
			}
		}
		return orient(cmp.Compare(a.Name, b.Name), desc)
	})
}

func kindRank(d ModelDependentResponse) int {
	if d.Type == ComponentKindWorkflow {
		return 1
	}
	return 0
}

func orient(c int, desc bool) int {
	if desc {
		return -c
	}
	return c
}

func (h *ModelDependentsHandler) workflowDependents(ctx context.Context, alias string, idToAlias map[string]string) ([]ModelDependentResponse, error) {
	if !h.workflowRefs.enabled() {
		return nil, nil
	}
	refs := []string{alias}
	for id, a := range idToAlias {
		if a == alias {
			refs = append(refs, id)
		}
	}
	references, err := h.workflowRefs.find(ctx, refs...)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(references))
	for i, ref := range references {
		ids[i] = ref.workflow.ID
	}
	statuses, err := h.workflowRepo.GetDeploymentStatuses(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("get workflow deployment statuses: %w", err)
	}
	dependents := make([]ModelDependentResponse, len(references))
	for i, ref := range references {
		wf := ref.workflow
		status := cmp.Or(statuses[wf.ID], DisplayStatusNotDeployed)
		if ref.deployed {
			status = DisplayStatusDeployed
		}
		dependents[i] = ModelDependentResponse{
			ID:               wf.ID,
			Name:             wf.Name,
			Type:             ComponentKindWorkflow,
			DeploymentStatus: status,
		}
	}
	return dependents, nil
}

func (h *ModelDependentsHandler) UndeployDependents(ctx context.Context, alias string) ([]ModelDependentResponse, error) {
	idToAlias, err := h.loadModelIDToAlias(ctx)
	if err != nil {
		return nil, fmt.Errorf("undeploy dependents: load model configurations: %w", err)
	}
	workflows, err := h.workflowDependents(ctx, alias, idToAlias)
	if err != nil {
		return nil, fmt.Errorf(errUndeployDependentsWrap, err)
	}
	agents, err := h.getDependentsUsing(ctx, alias, idToAlias)
	if err != nil {
		return nil, fmt.Errorf(errUndeployDependentsWrap, err)
	}
	runningWorkflows, err := h.stillRunning(ctx, workflows, h.latestWorkflowDeployments)
	if err != nil {
		return nil, fmt.Errorf(errUndeployDependentsWrap, err)
	}
	runningAgents, err := h.stillRunning(ctx, agents, h.latestAgentDeployments)
	if err != nil {
		return nil, fmt.Errorf(errUndeployDependentsWrap, err)
	}
	failed := h.undeployWorkflows(ctx, alias, runningWorkflows)
	return append(failed, h.undeployAgents(ctx, alias, runningAgents)...), nil
}

func (h *ModelDependentsHandler) stillRunning(
	ctx context.Context,
	dependents []ModelDependentResponse,
	latest func(context.Context, []string) (map[string]store.DeploymentOutcome, error),
) ([]ModelDependentResponse, error) {
	marked, err := h.withMayStillRun(ctx, dependents, latest)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(marked, func(dep ModelDependentResponse) bool { return !dep.MayStillRun }), nil
}

func (h *ModelDependentsHandler) withMayStillRun(
	ctx context.Context,
	dependents []ModelDependentResponse,
	latest func(context.Context, []string) (map[string]store.DeploymentOutcome, error),
) ([]ModelDependentResponse, error) {
	if len(dependents) == 0 {
		return dependents, nil
	}
	ids := make([]string, len(dependents))
	for i, dep := range dependents {
		ids[i] = dep.ID
	}
	deployments, err := latest(ctx, ids)
	if err != nil {
		return nil, err
	}
	marked := make([]ModelDependentResponse, len(dependents))
	for i, dep := range dependents {
		outcome, everDeployed := deployments[dep.ID]
		dep.MayStillRun = everDeployed && store.MayHaveLiveInstance(outcome.Status, outcome.Action)
		marked[i] = dep
	}
	return marked, nil
}

func (h *ModelDependentsHandler) latestAgentDeployments(ctx context.Context, ids []string) (map[string]store.DeploymentOutcome, error) {
	return h.agentRepo.GetLatestDeployments(ctx, ids)
}

func (h *ModelDependentsHandler) latestWorkflowDeployments(ctx context.Context, ids []string) (map[string]store.DeploymentOutcome, error) {
	return h.workflowRepo.GetLatestDeployments(ctx, ids)
}

func (h *ModelDependentsHandler) undeployWorkflows(ctx context.Context, alias string, workflows []ModelDependentResponse) []ModelDependentResponse {
	if h.workflowUndeploys == nil {
		if len(workflows) > 0 {
			h.logger.Error("no workflow undeployer is wired, so running workflow dependents cannot be undeployed", "modelAlias", alias, "workflowCount", len(workflows))
		}
		return workflows
	}
	var failed []ModelDependentResponse
	for _, wf := range workflows {
		h.logger.Info("undeploying workflow dependent on deleted model",
			"workflowID", wf.ID, "workflowName", wf.Name, "modelAlias", alias)
		resp, err := h.workflowUndeploys.Create(ctx, CreateWorkflowDeploymentRequest{WorkflowID: wf.ID, Action: DeployActionUndeploy}, "system")
		if err != nil || resp == nil || resp.Status == DeployStatusFailed {
			h.logger.Error("failed to undeploy dependent workflow", undeployFailureFields(wf.ID, alias, resp, err)...)
			failed = append(failed, wf)
		}
	}
	return failed
}

func undeployFailureFields(workflowID, alias string, resp *WorkflowDeploymentResponse, err error) []any {
	fields := []any{"workflowID", workflowID, "modelAlias", alias}
	if err != nil {
		fields = append(fields, "error", err)
	}
	if resp != nil {
		fields = append(fields, "deployStatus", resp.Status, "deploymentErrorID", resp.ErrorID, "reason", resp.FailureReason)
	}
	return fields
}

func (h *ModelDependentsHandler) undeployAgents(ctx context.Context, alias string, agents []ModelDependentResponse) []ModelDependentResponse {
	var failed []ModelDependentResponse
	remaining := len(agents)
	for _, dep := range agents {
		h.logger.Info("undeploying agent dependent on deleted model",
			"agentID", dep.ID, "agentName", dep.Name, "modelAlias", alias)
		err := h.deploySvc.Undeploy(ctx, dep.ID, "system")
		remaining--
		if err != nil {
			h.logger.Error("failed to undeploy dependent agent",
				"agentID", dep.ID, "modelAlias", alias, "error", err, "remainingDependents", remaining)
			failed = append(failed, dep)
		}
	}
	return failed
}

func (h *ModelDependentsHandler) getDependentsUsing(ctx context.Context, alias string, idToAlias map[string]string) ([]ModelDependentResponse, error) {
	agents, err := h.agentRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}

	agentIDs := make([]string, 0, len(agents))
	for _, a := range agents {
		agentIDs = append(agentIDs, a.ID)
	}

	statuses, err := h.agentRepo.GetDeploymentStatuses(ctx, agentIDs)
	if err != nil {
		return nil, fmt.Errorf("get deployment statuses: %w", err)
	}

	var dependents []ModelDependentResponse
	for _, agent := range agents {
		if agent.Type == AgentTypeA2AProxy || agent.Type == AgentTypeLink {
			continue
		}

		provider := resolveModelProviderAlias(agent, idToAlias)
		if provider == alias {
			status := statuses[agent.ID]
			if status == "" {
				status = DisplayStatusNotDeployed
			}
			dependents = append(dependents, ModelDependentResponse{
				ID:               agent.ID,
				Name:             agent.Name,
				Type:             agent.Type,
				DeploymentStatus: status,
			})
		}
	}

	if dependents == nil {
		dependents = []ModelDependentResponse{}
	}
	return dependents, nil
}

func resolveModelProvider(agent *store.Agent) string {
	if agent.ModelProvider != nil && *agent.ModelProvider != "" {
		return *agent.ModelProvider
	}
	return "general"
}

func (h *ModelDependentsHandler) loadModelIDToAlias(ctx context.Context) (map[string]string, error) {
	if h.modelConfigRepo == nil {
		return nil, nil
	}
	mcs, err := h.modelConfigRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list model configurations: %w", err)
	}
	idToAlias := make(map[string]string, len(mcs))
	for _, mc := range mcs {
		if mc == nil || mc.ID == "" || mc.Alias == "" {
			continue
		}
		idToAlias[mc.ID] = mc.Alias
	}
	return idToAlias, nil
}

func resolveModelProviderAlias(agent *store.Agent, idToAlias map[string]string) string {
	raw := resolveModelProvider(agent)
	if idToAlias == nil {
		return raw
	}
	if alias, ok := idToAlias[raw]; ok {
		return alias
	}
	return raw
}
