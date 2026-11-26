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
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type WorkflowDeploymentService struct {
	deploymentRepo  *store.WorkflowDeploymentRepo
	workflowRepo    *store.WorkflowRepo
	connectorRepo   *store.ConnectorRepo
	toolPackageRepo *store.ToolPackageRepo
	agentRepo       *store.AgentRepo
	resourceStore   ResourceStore
	controlClient   *control.Client
	awes            aweRollout
	namespace       string
	leader          *deployLeader
	logger          *slog.Logger
}

func NewWorkflowDeploymentService(
	deploymentRepo *store.WorkflowDeploymentRepo,
	workflowRepo *store.WorkflowRepo,
	connectorRepo *store.ConnectorRepo,
	toolPackageRepo *store.ToolPackageRepo,
	agentRepo *store.AgentRepo,
	resourceStore ResourceStore,
	controlClient *control.Client,
	namespace string,
	logger *slog.Logger,
) *WorkflowDeploymentService {
	return &WorkflowDeploymentService{
		deploymentRepo:  deploymentRepo,
		workflowRepo:    workflowRepo,
		connectorRepo:   connectorRepo,
		toolPackageRepo: toolPackageRepo,
		agentRepo:       agentRepo,
		resourceStore:   resourceStore,
		controlClient:   controlClient,
		namespace:       namespace,
		logger:          logger,
	}
}

func (s *WorkflowDeploymentService) Create(ctx context.Context, req CreateWorkflowDeploymentRequest, userID string) (*WorkflowDeploymentResponse, error) {
	return forwardOp(ctx, s.leader, opWorkflowDeploy, workflowDeployArgs{Req: req, UserID: userID}, func(ctx context.Context, deploymentID string) (*WorkflowDeploymentResponse, error) {
		return s.createHere(ctx, req, userID, deploymentID)
	}, s.GetByID, nil)
}

func (s *WorkflowDeploymentService) CreateWhenFree(ctx context.Context, req CreateWorkflowDeploymentRequest, userID string) (*WorkflowDeploymentResponse, error) {
	return forwardOp(ctx, s.leader, opWorkflowDeploy, workflowDeployArgs{Req: req, UserID: userID, Background: true}, func(ctx context.Context, deploymentID string) (*WorkflowDeploymentResponse, error) {
		return s.redeployHere(ctx, req, userID, deploymentID)
	}, s.GetByID, nil)
}

func (s *WorkflowDeploymentService) redeployHere(ctx context.Context, req CreateWorkflowDeploymentRequest, userID, deploymentID string) (*WorkflowDeploymentResponse, error) {
	status, err := s.workflowRepo.GetDeploymentStatus(ctx, req.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("get workflow deployment status: %w", err)
	}
	if err := validateDeploymentStateTransition(req.Action, status); err != nil {
		return nil, err
	}
	return s.createHere(ctx, req, userID, deploymentID)
}

func (s *WorkflowDeploymentService) createHere(ctx context.Context, req CreateWorkflowDeploymentRequest, userID, deploymentID string) (*WorkflowDeploymentResponse, error) {
	if req.WorkflowID == "" {
		return nil, &ValidationError{Message: "workflowId is required", Field: fieldWorkflowID}
	}

	workflow, err := s.workflowRepo.GetByID(ctx, req.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("get workflow: %w", err)
	}
	if workflow == nil {
		return nil, &NotFoundError{Resource: "workflow", ID: req.WorkflowID}
	}

	var deployOpts DeployOptions
	if req.TestMode {
		deployOpts = DeployOptions{TestMode: true, UserID: userID}
	}

	if req.Action != DeployActionUndeploy {
		if readyErr := s.validateToolsetsReadyForDeploy(ctx, workflow); readyErr != nil {
			return nil, onField(readyErr, fieldWorkflowID)
		}
		if filledErr := s.requireBoundConnectorsFilled(ctx, workflow.ID); filledErr != nil {
			return nil, filledErr
		}
	}

	deployment := &store.WorkflowDeployment{
		ID:         deploymentID,
		WorkflowID: req.WorkflowID,
		Action:     req.Action,
		CreatedBy:  userID,
	}

	var deployErr error
	var restored *rolledBackDeployment
	switch req.Action {
	case DeployActionDeploy, DeployActionUpdate:
		restored, deployErr = s.executeDeploy(ctx, deployment, workflow, deployOpts)
	case DeployActionUndeploy:
		_, deployErr = s.send(ctx, workflow, deployment, "")
	}

	return s.recordDeployment(ctx, req, workflow, deployment, restored, deployErr)
}

func (s *WorkflowDeploymentService) Rollback(ctx context.Context, req CreateWorkflowDeploymentRequest, priorID, userID string) (*WorkflowDeploymentResponse, error) {
	return forwardOp(ctx, s.leader, opWorkflowRollback, workflowRollbackArgs{Req: req, PriorID: priorID, UserID: userID}, func(ctx context.Context, deploymentID string) (*WorkflowDeploymentResponse, error) {
		return s.rollbackHere(ctx, req, priorID, userID, deploymentID)
	}, s.GetByID, nil)
}

func (s *WorkflowDeploymentService) rollbackHere(ctx context.Context, req CreateWorkflowDeploymentRequest, priorID, userID, deploymentID string) (*WorkflowDeploymentResponse, error) {
	workflow, err := s.workflowRepo.GetByID(ctx, req.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("get workflow: %w", err)
	}
	if workflow == nil {
		return nil, &NotFoundError{Resource: "workflow", ID: req.WorkflowID}
	}
	prior, err := s.deploymentRepo.GetByID(ctx, priorID)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	if prior == nil || prior.WorkflowID != req.WorkflowID {
		return nil, errNotARollbackTarget
	}
	if err := rollbackTargetError(prior.Status, prior.Action, prior.WorkflowConfigSnapshot); err != nil {
		return nil, err
	}

	deployment := &store.WorkflowDeployment{
		ID:                     deploymentID,
		WorkflowID:             req.WorkflowID,
		Action:                 req.Action,
		CreatedBy:              userID,
		WorkflowConfigSnapshot: prior.WorkflowConfigSnapshot,
	}
	restored, deployErr := s.send(ctx, workflow, deployment, *prior.WorkflowConfigSnapshot)
	return s.recordDeployment(ctx, req, workflow, deployment, restored, deployErr)
}

func (s *WorkflowDeploymentService) send(ctx context.Context, workflow *store.Workflow, deployment *store.WorkflowDeployment, yamlStr string) (*rolledBackDeployment, error) {
	if err := awaitLiveAWE(ctx, s.awes); err != nil {
		return nil, err
	}
	if err := s.leader.begin(ctx, store.IntentKindWorkflow, workflow.ID, deployment.ID, workflowIntent{Deployment: *deployment, YAML: yamlStr}); err != nil {
		return nil, err
	}
	return s.sendToAWE(ctx, workflow, deployment, yamlStr)
}

func (s *WorkflowDeploymentService) sendToAWE(ctx context.Context, workflow *store.Workflow, deployment *store.WorkflowDeployment, _ string) (*rolledBackDeployment, error) {
	var lastGood *store.WorkflowDeployment
	if deployment.Action != DeployActionUndeploy {
		lastGood = s.lastGoodDeploy(ctx, workflow)
	}
	instance := a2a.InstanceName(a2a.KindWorkflow, workflow.ID)
	err := rollOutOnAWEs(ctx, s.awes, instance, deployment.ID)
	if err == nil && deployment.Action == DeployActionUndeploy {
		err = removeUndeployedQueues(ctx, s.leader, s.awes, instance, deployment.ID)
	}
	if err == nil || ctx.Err() != nil || deployment.Action == DeployActionUndeploy {
		return nil, err
	}
	return s.rollback(ctx, workflow, lastGood), err
}

func (s *WorkflowDeploymentService) finishUndeployOfDeleted(ctx context.Context, d store.WorkflowDeployment) error {
	if d.Action != DeployActionUndeploy {
		return nil
	}
	instance := a2a.InstanceName(a2a.KindWorkflow, d.WorkflowID)
	if err := rollOutOnAWEs(ctx, s.awes, instance, d.ID); err != nil {
		return err
	}
	return removeUndeployedQueues(ctx, s.leader, s.awes, instance, d.ID)
}

func (s *WorkflowDeploymentService) recordDeployment(ctx context.Context, req CreateWorkflowDeploymentRequest, workflow *store.Workflow, deployment *store.WorkflowDeployment, restored *rolledBackDeployment, deployErr error) (*WorkflowDeploymentResponse, error) {
	if errors.Is(deployErr, errOtherLeader) || errors.Is(deployErr, errNoLiveAWE) {
		return nil, deployErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if deployErr != nil {
		deployment.Status = DeployStatusFailed
		deployment.ErrorMessage, deployment.FailureReason, deployment.ErrorID = deployFailure(deployErr)
		s.logDeployFailure(deployment, deployErr)
	} else {
		deployment.Status = DeployStatusSuccess
		s.logger.Info("workflow deployment succeeded", "action", req.Action, "workflowID", req.WorkflowID)
	}

	if err := s.deploymentRepo.Create(ctx, deployment); err != nil {
		if outcome := s.leader.duplicateInsertOutcome(ctx, store.IntentKindWorkflow, deployment.ID, err); outcome != nil {
			return nil, outcome
		}
		return nil, fmt.Errorf("create deployment: %w", err)
	}
	s.leader.end(ctx, deployment.ID)
	notifyAWEs(ctx, s.awes, a2a.InstanceName(a2a.KindWorkflow, workflow.ID), s.logger)
	s.restore(ctx, workflow, restored)

	return s.buildResponse(deployment), nil
}

func (s *WorkflowDeploymentService) logDeployFailure(deployment *store.WorkflowDeployment, deployErr error) {
	fields := []any{"errorId", *deployment.ErrorID, "deploymentID", deployment.ID, "workflowID", deployment.WorkflowID,
		"action", deployment.Action, "reason", *deployment.FailureReason, "error", deployErr}
	if _, refused := errors.AsType[*ValidationError](deployErr); refused {
		s.logger.Warn("workflow deployment refused", fields...)
		return
	}
	s.logger.Error("workflow deployment failed", fields...)
}

const rollbackCreatedBy = "deploy-rollback"

func isNewerDeploymentRow(a, b *store.WorkflowDeployment) bool {
	return newerRow(a.CreatedTime, a.ID, b.CreatedTime, b.ID)
}

func isNewerAgentDeploymentRow(a, b *store.AgentDeployment) bool {
	return newerRow(a.CreatedTime, a.ID, b.CreatedTime, b.ID)
}

func newerRow(aTime int64, aID string, bTime int64, bID string) bool {
	if aTime != bTime {
		return aTime > bTime
	}
	return aID > bID
}

const rollbackRecordTimeout = 30 * time.Second

type rolledBackDeployment struct {
	runningSnapshot   string
	priorDeploymentID string
	deploymentID      string
}

func (s *WorkflowDeploymentService) restore(ctx context.Context, workflow *store.Workflow, restored *rolledBackDeployment) {
	if restored == nil {
		return
	}
	snapshot := restored.runningSnapshot
	row := &store.WorkflowDeployment{
		ID:                     restored.deploymentID,
		WorkflowID:             workflow.ID,
		Action:                 DeployActionDeploy,
		WorkflowConfigSnapshot: &snapshot,
		CreatedBy:              rollbackCreatedBy,
	}
	if err := s.leader.begin(ctx, store.IntentKindWorkflow, workflow.ID, row.ID, workflowIntent{Deployment: *row, YAML: snapshot}); err != nil {
		s.logger.Error("rollback failed; the workflow is not running", "workflowID", workflow.ID, "restoredDeploymentID", restored.priorDeploymentID, "error", err)
		return
	}
	rollErr := rollOutOnAWEs(ctx, s.awes, a2a.InstanceName(a2a.KindWorkflow, workflow.ID), row.ID)
	if rollErr != nil && ctx.Err() != nil {
		return
	}
	row.Status = DeployStatusSuccess
	if rollErr != nil {
		row.Status = DeployStatusFailed
		row.ErrorMessage, row.FailureReason, row.ErrorID = deployFailure(rollErr)
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackRecordTimeout)
	defer cancel()
	if err := s.deploymentRepo.Create(recordCtx, row); err != nil {
		if s.leader.duplicateInsertOutcome(recordCtx, store.IntentKindWorkflow, row.ID, err) != nil {
			return
		}
		s.logger.Error("rolled back a failed workflow deploy but could not record it; status will under-report",
			"workflowID", workflow.ID, "restoredDeploymentID", restored.priorDeploymentID, "error", err)
		return
	}
	s.leader.end(ctx, row.ID)
	notifyAWEs(ctx, s.awes, a2a.InstanceName(a2a.KindWorkflow, workflow.ID), s.logger)
	if rollErr != nil {
		s.logger.Error("rollback not confirmed by every AWE; AWEs keep applying the previous deployment",
			"workflowID", workflow.ID, "restoredDeploymentID", restored.priorDeploymentID, "error", rollErr)
		return
	}
	s.logger.Warn("workflow deploy failed; restored the previous deployment",
		"workflowID", workflow.ID, "restoredDeploymentID", restored.priorDeploymentID)
}

func (s *WorkflowDeploymentService) requireBoundConnectorsFilled(ctx context.Context, workflowID string) error {
	bindings, err := s.workflowRepo.GetConnectorBindings(ctx, workflowID)
	if err != nil {
		return fmt.Errorf("get workflow connector bindings: %w", err)
	}
	ids := make([]string, len(bindings))
	for i, b := range bindings {
		ids[i] = b.ConnectorID
	}
	return requireConnectorsFilled(ctx, s.connectorRepo, ids, unfilledWorkflowMessage)
}

func (s *WorkflowDeploymentService) validateToolsetsReadyForDeploy(ctx context.Context, workflow *store.Workflow) error {
	var appConfig map[string]any
	if err := json.Unmarshal([]byte(workflow.AppConfig), &appConfig); err != nil {
		return nil
	}
	names, err := workflowToolsetNames(appConfig)
	if err != nil || len(names) == 0 {
		return nil
	}
	return validateToolsetsReady(ctx, names, s.toolPackageRepo)
}

func (s *WorkflowDeploymentService) executeDeploy(ctx context.Context, deployment *store.WorkflowDeployment, workflow *store.Workflow, opts DeployOptions) (*rolledBackDeployment, error) {
	var appConfig map[string]any
	if err := json.Unmarshal([]byte(workflow.AppConfig), &appConfig); err != nil {
		return nil, fmt.Errorf("parse appConfig: %w", err)
	}

	wrapped := wrapWorkflowAppConfig(workflow, s.namespace, appConfig)

	if opts.TestMode && opts.UserID != "" {
		wrapped["required_scopes"] = []string{
			rbac.UserWorkflowInvokeScope(opts.UserID, workflow.Name),
		}
	}
	if sourceErr := schema.WorkflowToolSourceError(wrapped); sourceErr != nil {
		return nil, &ValidationError{Message: sourceErr.Error(), Field: fieldWorkflowID}
	}
	if hydrateErr := hydrateWorkflowToolsets(ctx, wrapped, s.toolPackageRepo, s.logger); hydrateErr != nil {
		return nil, fmt.Errorf("resolve toolset refs: %w", hydrateErr)
	}
	bindings, err := s.workflowRepo.GetConnectorBindings(ctx, workflow.ID)
	if err != nil {
		return nil, fmt.Errorf("get workflow connector bindings: %w", err)
	}
	if s.connectorRepo != nil && len(bindings) > 0 {
		inputCheck := &connectorInputCheck{
			resolve: resolveThroughBindings(bindings, s.connectorRepo),
			specs:   s.resourceStore,
			logger:  workflowLogger(s.logger, workflow.ID),
		}
		if inputsErr := inputCheck.reject(ctx, wrapped); inputsErr != nil {
			return nil, onField(inputsErr, fieldWorkflowID)
		}
	}
	if hydrateErr := hydrateWorkflowConnectors(ctx, wrapped, bindings, s.connectorRepo, s.resourceStore, workflowLogger(s.logger, workflow.ID)); hydrateErr != nil {
		return nil, fmt.Errorf("resolve connector refs: %w", hydrateErr)
	}
	if named := unknownToolNodeMessages(workflow.Name, wrapped); len(named) > 0 {
		return nil, &ValidationError{Message: "workflow cannot be deployed as written: " + strings.Join(named, "; "), Field: fieldWorkflowID}
	}
	logToolNodesThatCannotStopRun(workflowLogger(s.logger, workflow.ID), wrapped)
	if hydrateErr := hydrateWorkflowNodeTargets(ctx, wrapped, s.workflowRepo, s.logger); hydrateErr != nil {
		return nil, fmt.Errorf("resolve workflow node targets: %w", hydrateErr)
	}
	if hydrateErr := hydrateAgentNodeTargets(ctx, wrapped, agentResolverFor(s.agentRepo), s.logger); hydrateErr != nil {
		return nil, fmt.Errorf("resolve agent node targets: %w", hydrateErr)
	}
	yamlStr, err := marshalWorkflowAppsYAML(workflow, wrapped)
	if err != nil {
		return nil, fmt.Errorf("generate YAML: %w", err)
	}
	deployment.WorkflowConfigSnapshot = &yamlStr
	return s.send(ctx, workflow, deployment, yamlStr)
}

func (s *WorkflowDeploymentService) lastGoodDeploy(ctx context.Context, workflow *store.Workflow) *store.WorkflowDeployment {
	prior, err := s.deploymentRepo.GetLatestSuccessfulDeploy(ctx, workflow.ID)
	if err != nil {
		s.logger.Warn("could not load the last successful deploy; a failed deploy will not be rolled back",
			"workflowID", workflow.ID, "error", err)
		return nil
	}
	if prior == nil {
		return nil
	}
	if undeployed, err := s.undeployedSince(ctx, workflow, prior); err != nil {
		s.logger.Warn("could not check for an intervening undeploy; a failed deploy will not be rolled back",
			"workflowID", workflow.ID, "error", err)
		return nil
	} else if undeployed {
		return nil
	}
	if prior.WorkflowConfigSnapshot == nil || *prior.WorkflowConfigSnapshot == "" {
		s.logger.Warn("last successful deploy has no config snapshot; a failed deploy cannot be rolled back",
			"workflowID", workflow.ID, "priorDeploymentID", prior.ID)
		return nil
	}
	return prior
}

func (s *WorkflowDeploymentService) undeployedSince(ctx context.Context, workflow *store.Workflow, prior *store.WorkflowDeployment) (bool, error) {
	undeployed, err := s.deploymentRepo.GetLatestSuccessfulUndeploy(ctx, workflow.ID)
	if err != nil {
		return false, err
	}
	return undeployed != nil && isNewerDeploymentRow(undeployed, prior), nil
}

func (s *WorkflowDeploymentService) rollbackStillWarranted(ctx context.Context, workflow *store.Workflow, prior *store.WorkflowDeployment) (bool, string, error) {
	current, err := s.workflowRepo.GetByID(ctx, workflow.ID)
	if err != nil {
		return false, "could not confirm the workflow still exists", err
	}
	if current == nil {
		return false, "the workflow was deleted while the deploy was in flight", nil
	}
	undeployed, err := s.undeployedSince(ctx, workflow, prior)
	if err != nil {
		return false, "could not check for an intervening undeploy", err
	}
	if undeployed {
		return false, "the workflow was undeployed while the deploy was in flight", nil
	}
	latest, err := s.deploymentRepo.GetLatestSuccessfulDeploy(ctx, workflow.ID)
	if err != nil {
		return false, "could not check for an intervening deploy", err
	}
	if latest != nil && isNewerDeploymentRow(latest, prior) {
		return false, "a newer deploy succeeded while this one was in flight", nil
	}
	return true, "", nil
}

func (s *WorkflowDeploymentService) rollback(ctx context.Context, workflow *store.Workflow, prior *store.WorkflowDeployment) *rolledBackDeployment {
	if prior == nil || prior.WorkflowConfigSnapshot == nil {
		return nil
	}
	snapshot := *prior.WorkflowConfigSnapshot
	checkCtx, cancelCheck := context.WithTimeout(context.WithoutCancel(ctx), rollbackRecordTimeout)
	stillDeployable, why, checkErr := s.rollbackStillWarranted(checkCtx, workflow, prior)
	cancelCheck()
	if !stillDeployable {
		if checkErr != nil {
			s.logger.Warn("not rolling back", "workflowID", workflow.ID, "priorDeploymentID", prior.ID, "reason", why, "error", checkErr)
		} else {
			s.logger.Warn("not rolling back", "workflowID", workflow.ID, "priorDeploymentID", prior.ID, "reason", why)
		}
		return nil
	}

	if _, err := parseYAMLToControlPayloads([]byte(snapshot)); err != nil {
		s.logger.Error("cannot roll back: the last successful deploy no longer parses",
			"workflowID", workflow.ID, "error", err)
		return nil
	}
	return &rolledBackDeployment{runningSnapshot: snapshot, priorDeploymentID: prior.ID, deploymentID: idgen.New()}
}

func (s *WorkflowDeploymentService) GetByID(ctx context.Context, id string) (*WorkflowDeploymentResponse, error) {
	d, err := s.deploymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get workflow deployment: %w", err)
	}
	if d == nil {
		return nil, &NotFoundError{Resource: "workflow deployment", ID: id}
	}
	return s.buildResponse(d), nil
}

func (s *WorkflowDeploymentService) ListPaginated(ctx context.Context, pageNumber, pageSize int) (*PaginatedResponse[WorkflowDeploymentResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	deployments, total, err := s.deploymentRepo.ListPaginated(ctx, pageNumber, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list workflow deployments: %w", err)
	}

	responses := make([]WorkflowDeploymentResponse, len(deployments))
	for i, d := range deployments {
		responses[i] = *s.buildResponse(&d)
	}

	return &PaginatedResponse[WorkflowDeploymentResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *WorkflowDeploymentService) Undeploy(ctx context.Context, workflowID, userID string) error {
	_, err := forwardOp(ctx, s.leader, opWorkflowUndeploy, workflowUndeployArgs{WorkflowID: workflowID, UserID: userID}, func(ctx context.Context, deploymentID string) (*WorkflowDeploymentResponse, error) {
		return nil, s.undeployHere(ctx, workflowID, userID, deploymentID)
	}, s.GetByID, func(r *WorkflowDeploymentResponse) error {
		return recordedFailure(r.Status, r.FailureReason, r.ErrorMessage)
	})
	return err
}

func (s *WorkflowDeploymentService) undeployHere(ctx context.Context, workflowID, userID, deploymentID string) error {
	workflow, err := s.workflowRepo.GetByID(ctx, workflowID)
	if err != nil {
		return fmt.Errorf("look up workflow %s: %w", workflowID, err)
	}
	if workflow == nil {
		return fmt.Errorf("workflow not found: %s", workflowID)
	}

	deployment := &store.WorkflowDeployment{
		ID:         deploymentID,
		WorkflowID: workflowID,
		Action:     DeployActionUndeploy,
		CreatedBy:  userID,
	}

	if _, deployErr := s.send(ctx, workflow, deployment, ""); errors.Is(deployErr, errOtherLeader) || errors.Is(deployErr, errNoLiveAWE) || ctx.Err() != nil {
		return deployErr
	} else if deployErr != nil {
		deployment.Status = DeployStatusFailed
		deployment.ErrorMessage, deployment.FailureReason, deployment.ErrorID = deployFailure(deployErr)
		s.logDeployFailure(deployment, deployErr)
		if recErr := s.deploymentRepo.Create(ctx, deployment); recErr != nil {
			if outcome := s.leader.duplicateInsertOutcome(ctx, store.IntentKindWorkflow, deployment.ID, recErr); outcome != nil {
				return outcome
			}
			return errors.Join(deployErr, fmt.Errorf("record failed undeploy for %s: %w", workflowID, recErr))
		}
		s.leader.end(ctx, deployment.ID)
		return deployErr
	}
	deployment.Status = DeployStatusSuccess
	if recErr := s.deploymentRepo.Create(ctx, deployment); recErr != nil {
		if outcome := s.leader.duplicateInsertOutcome(ctx, store.IntentKindWorkflow, deployment.ID, recErr); outcome != nil {
			return outcome
		}
		return fmt.Errorf("record undeploy for %s: %w", workflowID, recErr)
	}
	s.leader.end(ctx, deployment.ID)
	notifyAWEs(ctx, s.awes, a2a.InstanceName(a2a.KindWorkflow, workflowID), s.logger)
	return nil
}

func (s *WorkflowDeploymentService) buildResponse(d *store.WorkflowDeployment) *WorkflowDeploymentResponse {
	return &WorkflowDeploymentResponse{
		ID:            d.ID,
		WorkflowID:    d.WorkflowID,
		Status:        d.Status,
		Action:        d.Action,
		ErrorMessage:  derefStr(d.ErrorMessage),
		FailureReason: derefStr(d.FailureReason),
		ErrorID:       derefStr(d.ErrorID),
		CreatedBy:     d.CreatedBy,
		UpdatedBy:     d.CreatedBy,
		CreatedTime:   Timestamp(d.CreatedTime),
		UpdatedTime:   Timestamp(d.UpdatedTime),
	}
}
