// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"gopkg.in/yaml.v3"
)

const errGetWorkflowWrap = "get workflow: %w"

type WorkflowService struct {
	workflowRepo    *store.WorkflowRepo
	deploymentRepo  *store.WorkflowDeploymentRepo
	connectorRepo   *store.ConnectorRepo
	toolPackageRepo *store.ToolPackageRepo
	agentRegistry   *platpkg.AgentRegistry
	namespace       string
	logger          *slog.Logger
	modelConfigs    *store.ModelConfigRepo
	resourceStore   ResourceStore
}

func (s *WorkflowService) SetModelConfigs(repo *store.ModelConfigRepo) { s.modelConfigs = repo }

func (s *WorkflowService) SetResourceStore(rs ResourceStore) { s.resourceStore = rs }

func (s *WorkflowService) loggerWith(args ...any) *slog.Logger {
	if s.logger == nil {
		return nil
	}
	return s.logger.With(args...)
}

func (s *WorkflowService) connectorInputCheck(resolve connectorResolver, logger *slog.Logger) *connectorInputCheck {
	return &connectorInputCheck{resolve: resolve, specs: s.resourceStore, logger: logger}
}

func (s *WorkflowService) rejectUnrunnableConnectorInputs(ctx context.Context, appConfig map[string]any, bindings []store.WorkflowConnectorBinding, logger *slog.Logger) error {
	if s.connectorRepo == nil || len(bindings) == 0 {
		return nil
	}
	err := s.connectorInputCheck(resolveThroughBindings(bindings, s.connectorRepo), logger).reject(ctx, appConfig)
	return onField(err, fieldAppConfig)
}

func (s *WorkflowService) CheckConnectorToolInputs(ctx context.Context, appConfig map[string]any) *ConnectorToolInputReport {
	if s.connectorRepo == nil {
		return newConnectorToolInputReport()
	}
	return s.connectorInputCheck(resolveByName(s.connectorRepo), s.logger).check(ctx, appConfig)
}

func NewWorkflowService(workflowRepo *store.WorkflowRepo, deploymentRepo *store.WorkflowDeploymentRepo, connectorRepo *store.ConnectorRepo, toolPackageRepo *store.ToolPackageRepo, agentRegistry *platpkg.AgentRegistry, namespace string, logger *slog.Logger) *WorkflowService {
	return &WorkflowService{
		workflowRepo:    workflowRepo,
		deploymentRepo:  deploymentRepo,
		connectorRepo:   connectorRepo,
		toolPackageRepo: toolPackageRepo,
		agentRegistry:   agentRegistry,
		namespace:       namespace,
		logger:          logger,
	}
}

func ValidateWorkflowRequest(name string, appConfig map[string]any) error {
	if err := validateWorkflowStructure(name, appConfig); err != nil {
		return err
	}
	if err := validateDecisionBlocks(appConfig); err != nil {
		return err
	}
	if err := validateToolNodeNames(name, appConfig); err != nil {
		return err
	}
	if _, err := validateWorkflowConnectorShape(appConfig); err != nil {
		return err
	}
	_, err := workflowToolsetNames(appConfig)
	return err
}

func (s *WorkflowService) Create(ctx context.Context, req CreateWorkflowRequest, userID string) (*WorkflowResponse, error) {
	if err := ValidateWorkflowRequest(req.Name, req.AppConfig); err != nil {
		return nil, onField(err, fieldAppConfig)
	}
	if nameErr := s.ensureNameAvailable(ctx, req.Name, ""); nameErr != nil {
		return nil, nameErr
	}
	if err := s.ValidateDecisionModel(ctx, req.AppConfig); err != nil {
		return nil, err
	}
	bindings, err := resolveWorkflowConnectorBindings(ctx, req.AppConfig, nil, s.connectorRepo)
	if err != nil {
		return nil, err
	}
	if inputsErr := s.rejectUnrunnableConnectorInputs(ctx, req.AppConfig, bindings, s.loggerWith("workflowName", req.Name)); inputsErr != nil {
		return nil, inputsErr
	}
	logToolNodesThatCannotStopRun(s.loggerWith("workflowName", req.Name), req.AppConfig)
	toolsets, err := resolveWorkflowToolsets(ctx, req.AppConfig, s.toolPackageRepo)
	if err != nil {
		return nil, onField(err, fieldAppConfig)
	}

	bodyJSON, err := json.Marshal(req.AppConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow body: %w", err)
	}

	workflow := &store.Workflow{
		Name:        req.Name,
		Description: strPtr(req.Description),
		AppConfig:   string(bodyJSON),
		CreatedBy:   userID,
		UpdatedBy:   userID,
	}

	if err := s.workflowRepo.Create(ctx, workflow, store.WorkflowBindings{Connectors: bindings, Toolsets: toolsets}); err != nil {
		if dbutil.IsUniqueViolationOn(err, "workflows", "name") {
			return nil, workflowNameConflict(req.Name)
		}
		return nil, fmt.Errorf("create workflow: %w", err)
	}

	return s.buildResponse(workflow, ""), nil
}

func (s *WorkflowService) ensureNameAvailable(ctx context.Context, name, excludeID string) error {
	taken, err := s.workflowRepo.NameTakenByOther(ctx, name, excludeID)
	if err != nil {
		return fmt.Errorf("check workflow name: %w", err)
	}
	if taken {
		return workflowNameConflict(name)
	}
	return nil
}

func workflowNameConflict(name string) *ConflictError {
	return &ConflictError{
		Message:    fmt.Sprintf("workflow with name %q already exists (names compare ignoring case and spaces)", name),
		LogMessage: "workflow name already taken",
	}
}

func (s *WorkflowService) GetByID(ctx context.Context, id string) (*WorkflowResponse, error) {
	workflow, err := s.workflowRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetWorkflowWrap, err)
	}
	if workflow == nil {
		return nil, &NotFoundError{Resource: "workflow", ID: id}
	}

	deploymentStatus, _ := s.workflowRepo.GetDeploymentStatus(ctx, id)
	return s.buildResponse(workflow, deploymentStatus), nil
}

func (s *WorkflowService) ListPaginated(ctx context.Context, pageNumber, pageSize int) (*PaginatedResponse[WorkflowResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	workflows, total, err := s.workflowRepo.ListPaginated(ctx, pageNumber, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}

	workflowIDs := make([]string, len(workflows))
	for i, w := range workflows {
		workflowIDs[i] = w.ID
	}
	statuses, _ := s.workflowRepo.GetDeploymentStatuses(ctx, workflowIDs)

	responses := make([]WorkflowResponse, len(workflows))
	for i, w := range workflows {
		status := statuses[w.ID]
		responses[i] = *s.buildResponse(&w, status)
	}

	return &PaginatedResponse[WorkflowResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *WorkflowService) Update(ctx context.Context, id string, req UpdateWorkflowRequest, userID string) (*WorkflowResponse, error) {
	existing, err := s.workflowRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetWorkflowWrap, err)
	}
	if existing == nil {
		return nil, &NotFoundError{Resource: "workflow", ID: id}
	}

	if validateErr := ValidateWorkflowRequest(req.Name, req.AppConfig); validateErr != nil {
		return nil, onField(validateErr, fieldAppConfig)
	}
	if store.NormalizeName(req.Name) != store.NormalizeName(existing.Name) {
		if nameErr := s.ensureNameAvailable(ctx, req.Name, existing.ID); nameErr != nil {
			return nil, nameErr
		}
	}

	currentBindings, err := s.workflowRepo.GetConnectorBindings(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get workflow connector bindings: %w", err)
	}
	if decisionErr := s.ValidateDecisionModel(ctx, req.AppConfig); decisionErr != nil {
		return nil, decisionErr
	}
	bindings, err := resolveWorkflowConnectorBindings(ctx, req.AppConfig, currentBindings, s.connectorRepo)
	if err != nil {
		return nil, err
	}
	if inputsErr := s.rejectUnrunnableConnectorInputs(ctx, req.AppConfig, bindings, workflowLogger(s.logger, id)); inputsErr != nil {
		return nil, inputsErr
	}
	logToolNodesThatCannotStopRun(workflowLogger(s.logger, id), req.AppConfig)
	toolsets, err := resolveWorkflowToolsets(ctx, req.AppConfig, s.toolPackageRepo)
	if err != nil {
		return nil, onField(err, fieldAppConfig)
	}

	bodyJSON, marshalErr := json.Marshal(req.AppConfig)
	if marshalErr != nil {
		return nil, fmt.Errorf("marshal workflow body: %w", marshalErr)
	}

	existing.Name = req.Name
	existing.Description = strPtr(req.Description)
	existing.AppConfig = string(bodyJSON)
	existing.UpdatedBy = userID

	if updateErr := s.workflowRepo.Update(ctx, existing, store.WorkflowBindings{Connectors: bindings, Toolsets: toolsets}); updateErr != nil {
		if dbutil.IsUniqueViolationOn(updateErr, "workflows", "name") {
			return nil, workflowNameConflict(req.Name)
		}
		return nil, fmt.Errorf("update workflow: %w", updateErr)
	}

	deploymentStatus, _ := s.workflowRepo.GetDeploymentStatus(ctx, id)
	return s.buildResponse(existing, deploymentStatus), nil
}

func (s *WorkflowService) Delete(ctx context.Context, id string) error {
	if err := s.workflowRepo.Delete(ctx, id); err != nil {
		return &NotFoundError{Resource: "workflow", ID: id}
	}
	return nil
}

func (s *WorkflowService) DeleteWithUndeploy(ctx context.Context, id, userID string, deploySvc *WorkflowDeploymentService) error {
	workflow, err := s.workflowRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf(errGetWorkflowWrap, err)
	}
	if workflow == nil {
		return &NotFoundError{Resource: "workflow", ID: id}
	}
	if deploySvc != nil {
		mayBeLive, err := s.workflowRepo.HasPossiblyLiveDeployment(ctx, id)
		if err != nil {
			return err
		}
		if mayBeLive {
			if err := deploySvc.Undeploy(ctx, id, userID); err != nil {
				return undeployBlocksDelete(s.logger, "workflow undeploy before delete failed", "workflowID", id)
			}
		}
	}
	if err := s.workflowRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete workflow: %w", err)
	}
	return nil
}

func (s *WorkflowService) GetConfiguration(ctx context.Context, id string) (*WorkflowConfigurationResponse, error) {
	workflow, err := s.workflowRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetWorkflowWrap, err)
	}
	if workflow == nil {
		return nil, &NotFoundError{Resource: "workflow", ID: id}
	}

	yamlStr, err := generateWorkflowYAML(workflow, s.namespace)
	if err != nil {
		return nil, fmt.Errorf("generate YAML: %w", err)
	}

	return &WorkflowConfigurationResponse{ConfigurationFile: yamlStr}, nil
}

func generateWorkflowYAML(wf *store.Workflow, ns string) (string, error) {
	var userConfig map[string]any
	if err := json.Unmarshal([]byte(wf.AppConfig), &userConfig); err != nil {
		return "", fmt.Errorf("parse stored appConfig: %w", err)
	}
	return marshalWorkflowAppsYAML(wf, wrapWorkflowAppConfig(wf, ns, userConfig))
}

func wrapWorkflowAppConfig(wf *store.Workflow, ns string, userConfig map[string]any) map[string]any {
	namespace := cmp.Or(ns, "solace-agent-mesh")

	appConfig := userConfig
	if _, hasWorkflowKey := userConfig["workflow"]; !hasWorkflowKey {
		appConfig = map[string]any{
			"workflow": userConfig,
		}
	}
	appConfig["agent_type"] = "workflow"
	appConfig["namespace"] = namespace
	appConfig["display_name"] = wf.Name
	if wfCfg, ok := appConfig["workflow"].(map[string]any); ok {
		wfCfg["display_name"] = wf.Name
		if authored, _ := wfCfg["description"].(string); authored == "" {
			if stored := derefStr(wf.Description); stored != "" {
				wfCfg["description"] = stored
			}
		}
	}
	return appConfig
}

func marshalWorkflowAppsYAML(wf *store.Workflow, appConfig map[string]any) (string, error) {
	instanceName := a2a.InstanceName(a2a.KindWorkflow, wf.ID)

	doc := map[string]any{
		"apps": []map[string]any{{
			"name":       instanceName,
			"app_config": appConfig,
		}},
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal YAML: %w", err)
	}
	return string(out), nil
}

func (s *WorkflowService) buildResponse(w *store.Workflow, deploymentStatus string) *WorkflowResponse {
	resp := &WorkflowResponse{
		ID:               w.ID,
		Name:             w.Name,
		Description:      derefStr(w.Description),
		DeploymentStatus: deploymentStatus,
		CreatedBy:        w.CreatedBy,
		UpdatedBy:        w.UpdatedBy,
		CreatedTime:      Timestamp(w.CreatedTime),
		UpdatedTime:      Timestamp(w.UpdatedTime),
	}

	internalName := a2a.InstanceName(a2a.KindWorkflow, w.ID)
	if s.agentRegistry != nil {
		if card := s.agentRegistry.Get(internalName); card != nil {
			resp.RuntimeStatus = RunningStatus(s.agentRegistry.RuntimeProblems(internalName))
		} else if deploymentStatus == DisplayStatusDeployed {
			resp.RuntimeStatus = DisconnectedStatus()
		} else {
			resp.RuntimeStatus = StoppedStatus()
		}
	} else {
		resp.RuntimeStatus = StoppedStatus()
	}

	if w.AppConfig != "" {
		var body map[string]any
		if err := json.Unmarshal([]byte(w.AppConfig), &body); err == nil {
			resp.AppConfig = body
		}
	}
	if resp.AppConfig == nil {
		resp.AppConfig = map[string]any{}
	}

	return resp
}
