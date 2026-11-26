// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type Reconciler struct {
	deploys                     *deployLeader
	answerWithoutLeader         aweAnswerSize
	agentRepo                   *store.AgentRepo
	agentDeploymentRepo         *store.AgentDeploymentRepo
	connectorRepo               *store.ConnectorRepo
	workflowRepo                *store.WorkflowRepo
	workflowDeploymentRepo      *store.WorkflowDeploymentRepo
	gatewayRepo                 *store.GatewayRepo
	gwDeploymentRepo            *store.GatewayDeploymentRepo
	skillRepo                   *store.SkillRepo
	toolPackageRepo             *store.ToolPackageRepo
	resourceStore               ResourceStore
	yamlSvc                     *YAMLService
	modelConfigSvc              *ModelConfigService
	deployers                   *DeployerRegistry
	deploySvc                   *DeploymentService
	bindingsPusher              *platpkg.BindingsPusher
	controlClient               *control.Client
	strReconcileHandler         *STRReconcileHandler
	brk                         broker.Broker
	namespace                   string
	logger                      *slog.Logger
	sentinelEnabled             bool
	activityMonitorEnabled      bool
	entrypointsStartedByGateway bool

	builtins sync.Mutex

	unbuildableReported sync.Map
}

func NewReconciler(
	agentRepo *store.AgentRepo,
	agentDeploymentRepo *store.AgentDeploymentRepo,
	connectorRepo *store.ConnectorRepo,
	workflowRepo *store.WorkflowRepo,
	gatewayRepo *store.GatewayRepo,
	gwDeploymentRepo *store.GatewayDeploymentRepo,
	yamlSvc *YAMLService,
	modelConfigSvc *ModelConfigService,
	controlClient *control.Client,
	brk broker.Broker,
	namespace string,
	logger *slog.Logger,
) *Reconciler {
	return &Reconciler{
		agentRepo:           agentRepo,
		agentDeploymentRepo: agentDeploymentRepo,
		connectorRepo:       connectorRepo,
		workflowRepo:        workflowRepo,
		gatewayRepo:         gatewayRepo,
		gwDeploymentRepo:    gwDeploymentRepo,
		yamlSvc:             yamlSvc,
		modelConfigSvc:      modelConfigSvc,
		bindingsPusher:      platpkg.NewBindingsPusher(controlClient, logger, platpkg.AWEBindingApp, platpkg.STRBindingApp),
		controlClient:       controlClient,
		brk:                 brk,
		namespace:           namespace,
		logger:              withLoggerName(logger, "reconciler"),
	}
}

func (r *Reconciler) SetDeployerRegistry(d *DeployerRegistry) {
	r.deployers = d
}

func (r *Reconciler) SetSTRReconcileHandler(h *STRReconcileHandler) {
	r.strReconcileHandler = h
}

func (r *Reconciler) SetDeploymentService(svc *DeploymentService) {
	r.deploySvc = svc
}

func (r *Reconciler) SetDeploys(deploys *deployLeader) {
	r.deploys = deploys
}

func (r *Reconciler) SetSentinelEnabled(enabled bool) {
	r.sentinelEnabled = enabled
}

func (r *Reconciler) SetResourceStore(rs ResourceStore) {
	r.resourceStore = rs
}

func (r *Reconciler) SetToolPackageRepo(repo *store.ToolPackageRepo) {
	r.toolPackageRepo = repo
}

func (r *Reconciler) SetWorkflowDeploymentRepo(repo *store.WorkflowDeploymentRepo) {
	r.workflowDeploymentRepo = repo
}

func (r *Reconciler) SetActivityMonitorEnabled(enabled bool) {
	r.activityMonitorEnabled = enabled
}

func (r *Reconciler) SetSkillRepo(repo *store.SkillRepo) {
	r.skillRepo = repo
}

func (r *Reconciler) pushAllBindings(ctx context.Context) {
	if r.modelConfigSvc == nil || r.bindingsPusher == nil {
		return
	}
	models, err := r.modelConfigSvc.ListAllRawDualKey(ctx)
	if err != nil {
		r.logger.Warn("push-all-bindings: failed to list model configs", "error", err)
		return
	}
	r.bindingsPusher.UpsertAll(ctx, models)
	r.logger.Info("pushed all model bindings", "count", len(models))
}

func (r *Reconciler) builtinFeatureDisabled(name string) bool {
	switch name {
	case builtinSentinelName:
		return !r.sentinelEnabled
	case BuiltinActivityMonitorName:
		return !r.activityMonitorEnabled
	default:
		return false
	}
}

var seededBuiltinAgentNames = []string{builtinOrchestratorName, builtinSentinelName, BuiltinActivityMonitorName}

func (r *Reconciler) buildDeployInput(ctx context.Context, agent *store.Agent) (DeployInput, error) {
	if agent.Type != AgentTypeStandard {
		return DeployInput{Agent: agent, Action: DeployActionDeploy}, nil
	}
	_, toolsets, inputModes, outputModes, connectorIDs, err := r.agentRepo.GetByID(ctx, agent.ID)
	if err != nil {
		return DeployInput{}, err
	}
	_, exclusions, err := r.agentRepo.GetToolsetsWithExclusions(ctx, agent.ID)
	if err != nil {
		return DeployInput{}, fmt.Errorf("load toolset exclusions for %s: %w", agent.ID, err)
	}
	skillIDs := loadAgentSkillIDs(ctx, agent.ID, r.skillRepo)
	return DeployInput{
		Agent:             agent,
		Action:            DeployActionDeploy,
		Toolsets:          toolsets,
		ToolsetExclusions: exclusions,
		InputModes:        inputModes,
		OutputModes:       outputModes,
		ConnectorIDs:      connectorIDs,
		SkillIDs:          skillIDs,
	}, nil
}

func (r *Reconciler) generateDeployableWorkflowYAML(ctx context.Context, wf *store.Workflow) (string, error) {
	var userConfig map[string]any
	if err := json.Unmarshal([]byte(wf.AppConfig), &userConfig); err != nil {
		return "", fmt.Errorf("parse stored appConfig: %w", err)
	}
	bindings, err := r.workflowRepo.GetConnectorBindings(ctx, wf.ID)
	if err != nil {
		return "", fmt.Errorf("get workflow connector bindings: %w", err)
	}
	wrapped := wrapWorkflowAppConfig(wf, r.namespace, userConfig)
	if err := hydrateWorkflowToolsets(ctx, wrapped, r.toolPackageRepo, r.logger); err != nil {
		return "", fmt.Errorf("resolve toolset refs: %w", err)
	}
	if err := hydrateWorkflowConnectors(ctx, wrapped, bindings, r.connectorRepo, r.resourceStore, workflowLogger(r.logger, wf.ID)); err != nil {
		return "", fmt.Errorf("resolve connector refs: %w", err)
	}
	if err := hydrateWorkflowNodeTargets(ctx, wrapped, r.workflowRepo, r.logger); err != nil {
		return "", fmt.Errorf("resolve workflow node targets: %w", err)
	}
	if err := hydrateAgentNodeTargets(ctx, wrapped, agentResolverFor(r.agentRepo), r.logger); err != nil {
		return "", fmt.Errorf("resolve agent node targets: %w", err)
	}
	return marshalWorkflowAppsYAML(wf, wrapped)
}

func (r *Reconciler) reconcileGateways(ctx context.Context) int {
	if r.gatewayRepo == nil || r.gwDeploymentRepo == nil {
		return 0
	}

	gateways, _, err := r.gatewayRepo.ListPaginated(ctx, 1, 1000, "")
	if err != nil {
		r.logger.Warn("failed to list gateways for reconciliation", "error", err)
		return 0
	}

	var count int
	for _, gw := range gateways {
		latest, latestErr := r.gwDeploymentRepo.GetLatestByGatewayID(ctx, gw.ID)
		if latestErr != nil || latest == nil || latest.Status != DeployStatusSuccess {
			continue
		}
		if latest.Action == DeployActionUndeploy {
			continue
		}

		yamlStr := ""
		if latest.GatewayConfigSnapshot != nil && *latest.GatewayConfigSnapshot != "" {
			yamlStr = *latest.GatewayConfigSnapshot
		} else {
			var yamlErr error
			yamlStr, yamlErr = r.yamlSvc.GenerateGatewayYAML(gw)
			if yamlErr != nil {
				r.logger.Error("failed to generate gateway YAML for reconciliation", "gateway", gw.Name, "error", yamlErr)
				continue
			}
		}

		if err := r.deployGatewayYAML(ctx, yamlStr); err != nil {
			r.logger.Error("failed to reconcile gateway", "gateway", gw.Name, "error", err)
		} else {
			r.logger.Debug("reconciled gateway", "gateway", gw.Name)
			count++
		}
	}
	return count
}

func (r *Reconciler) deployGatewayYAML(ctx context.Context, yamlStr string) error {
	payloads, err := schema.ParseAppsConfig([]byte(yamlStr))
	if err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}

	for _, app := range payloads {
		appConfig, ok := app["app_config"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := app["name"].(string)

		payload := map[string]any{
			"name":       name,
			"app_config": appConfig,
		}
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return fmt.Errorf("marshal payload: %w", marshalErr)
		}

		if _, err := r.controlClient.Custom(ctx, "gateway", "post", "instances", body); err != nil {
			if control.IsAlreadyExistsError(err) {
				r.logger.Debug("gateway instance already running, skipping", "name", name)
				continue
			}
			return fmt.Errorf("add gateway instance %q: %w", name, err)
		}
	}

	return nil
}
