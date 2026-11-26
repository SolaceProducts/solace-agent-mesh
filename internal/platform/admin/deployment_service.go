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

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/configbuilder"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const errGetDeploymentWrap = "get deployment: %w"

type DeploymentService struct {
	deploymentRepo   *store.DeploymentRepo
	agentRepo        *store.AgentRepo
	connectorRepo    *store.ConnectorRepo
	toolPackageRepo  *store.ToolPackageRepo
	skillRepo        *store.SkillRepo
	yamlSvc          *YAMLService
	controlClient    *control.Client
	deployers        *DeployerRegistry
	agentCards       *platpkg.AgentRegistry
	agentDeployments *store.AgentDeploymentRepo
	leader           *deployLeader
	awes             aweRollout
	links            *linkAgentRollout
	logger           *slog.Logger
}

func NewDeploymentService(
	deploymentRepo *store.DeploymentRepo,
	agentRepo *store.AgentRepo,
	connectorRepo *store.ConnectorRepo,
	toolPackageRepo *store.ToolPackageRepo,
	yamlSvc *YAMLService,
	controlClient *control.Client,
	deployers *DeployerRegistry,
	logger *slog.Logger,
) *DeploymentService {
	return &DeploymentService{
		deploymentRepo:  deploymentRepo,
		agentRepo:       agentRepo,
		connectorRepo:   connectorRepo,
		toolPackageRepo: toolPackageRepo,
		yamlSvc:         yamlSvc,
		controlClient:   controlClient,
		deployers:       deployers,
		logger:          logger,
	}
}

func (s *DeploymentService) WithSkipIfCurrent(cards *platpkg.AgentRegistry, deployments *store.AgentDeploymentRepo) {
	s.agentCards = cards
	s.agentDeployments = deployments
}

func (s *DeploymentService) WithSkillRepo(skillRepo *store.SkillRepo) {
	s.skillRepo = skillRepo
}

func (s *DeploymentService) Create(ctx context.Context, req CreateAgentDeploymentRequest, userID string) (*AgentDeploymentResponse, error) {
	return s.CreateWithOpts(ctx, req, userID, DeployOptions{})
}

func (s *DeploymentService) CreateWithOpts(ctx context.Context, req CreateAgentDeploymentRequest, userID string, extra DeployOptions) (*AgentDeploymentResponse, error) {
	return forwardOp(ctx, s.leader, opAgentDeploy, agentDeployArgs{Req: req, UserID: userID, Opts: extra}, func(ctx context.Context, deploymentID string) (*AgentDeploymentResponse, error) {
		return s.createHere(ctx, req, userID, extra, deploymentID)
	}, s.GetByID, nil)
}

func (s *DeploymentService) createHere(ctx context.Context, req CreateAgentDeploymentRequest, userID string, extra DeployOptions, deploymentID string) (*AgentDeploymentResponse, error) {
	if req.AgentID == "" {
		return nil, &ValidationError{Message: "agentId is required", Field: fieldAgentID}
	}

	agent, toolsets, inputModes, outputModes, connectorIDs, err := s.agentRepo.GetByID(ctx, req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	if agent == nil {
		return nil, &NotFoundError{Resource: "agent", ID: req.AgentID}
	}

	deployer, err := s.deployers.LookupOrError(agent.Type)
	if err != nil {
		return nil, err
	}

	currentStatus, _ := s.agentRepo.GetDeploymentStatus(ctx, req.AgentID)
	if err := validateDeploymentStateTransition(req.Action, currentStatus); err != nil {
		return nil, err
	}

	deployOpts := extra
	deployOpts.SkillNames = s.resolveAgentSkillNames(ctx, agent)
	deployOpts.DeploymentID = deploymentID

	_, toolsetExclusions, tsErr := s.agentRepo.GetToolsetsWithExclusions(ctx, req.AgentID)
	if tsErr != nil {
		return nil, fmt.Errorf("load toolset exclusions for %s: %w", req.AgentID, tsErr)
	}

	skillIDs := loadAgentSkillIDs(ctx, req.AgentID, s.skillRepo)

	input := DeployInput{
		Agent:             agent,
		Action:            req.Action,
		Toolsets:          toolsets,
		ToolsetExclusions: toolsetExclusions,
		InputModes:        inputModes,
		OutputModes:       outputModes,
		ConnectorIDs:      connectorIDs,
		SkillIDs:          skillIDs,
		Opts:              deployOpts,
	}

	if err := deployer.Validate(ctx, input); err != nil {
		return nil, onField(err, fieldAgentID)
	}

	if req.Action != DeployActionUndeploy {
		if err := validateToolsetsReady(ctx, toolsets, s.toolPackageRepo); err != nil {
			return nil, onField(err, fieldAgentID)
		}
	}

	if req.SkipIfCurrent && req.Action != DeployActionUndeploy {
		if current := s.currentDeployment(ctx, deployer, input); current != nil {
			s.logger.Info("deploy skipped: the running instance already carries this config",
				"agentID", req.AgentID, "deploymentID", current.ID, "kind", deployer.Kind())
			return buildAgentDeploymentResponse(current), nil
		}
	}

	deployment := &store.AgentDeployment{
		ID:        deploymentID,
		AgentID:   req.AgentID,
		Action:    req.Action,
		CreatedBy: userID,
	}

	if req.Action == DeployActionUndeploy {
		return s.rollout(ctx, deployment, deployer, agent, nil)
	}
	payloads, buildErr := s.prepareDeploy(ctx, deployment, deployer, input)
	if buildErr != nil {
		return s.recordDeployment(ctx, deployment, deployer.Kind(), buildErr)
	}
	return s.rollout(ctx, deployment, deployer, agent, payloads)
}

func (s *DeploymentService) Rollback(ctx context.Context, req CreateAgentDeploymentRequest, priorID, userID string) (*AgentDeploymentResponse, error) {
	return forwardOp(ctx, s.leader, opAgentRollback, agentRollbackArgs{Req: req, PriorID: priorID, UserID: userID}, func(ctx context.Context, deploymentID string) (*AgentDeploymentResponse, error) {
		return s.rollbackHere(ctx, req, priorID, userID, deploymentID)
	}, s.GetByID, nil)
}

func (s *DeploymentService) rollbackHere(ctx context.Context, req CreateAgentDeploymentRequest, priorID, userID, deploymentID string) (*AgentDeploymentResponse, error) {
	agent, _, _, _, _, err := s.agentRepo.GetByID(ctx, req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	if agent == nil {
		return nil, &NotFoundError{Resource: "agent", ID: req.AgentID}
	}
	deployer, err := s.deployers.LookupOrError(agent.Type)
	if err != nil {
		return nil, err
	}
	currentStatus, _ := s.agentRepo.GetDeploymentStatus(ctx, req.AgentID)
	err = validateDeploymentStateTransition(req.Action, currentStatus)
	if err != nil {
		return nil, err
	}

	prior, err := s.deploymentRepo.GetByID(ctx, priorID)
	if err != nil {
		return nil, fmt.Errorf(errGetDeploymentWrap, err)
	}
	if prior == nil || prior.AgentID != req.AgentID {
		return nil, errNotARollbackTarget
	}
	err = rollbackTargetError(prior.Status, prior.Action, prior.AgentConfigSnapshot)
	if err != nil {
		return nil, err
	}
	payloads, err := deployer.ReplayFromSnapshot(*prior.AgentConfigSnapshot)
	if errors.Is(err, ErrNoReplay) {
		return nil, &ValidationError{Field: "deploymentId", Message: noRollbackMessage(deployer)}
	}
	if err != nil {
		return nil, fmt.Errorf("replay deployment snapshot: %w", err)
	}

	deployment := &store.AgentDeployment{
		ID:                  deploymentID,
		AgentID:             req.AgentID,
		Action:              req.Action,
		CreatedBy:           userID,
		AgentConfigSnapshot: prior.AgentConfigSnapshot,
		AgentDataSnapshot:   prior.AgentDataSnapshot,
		ConfigHash:          prior.ConfigHash,
	}
	agentSurfaceHashesOf(prior).applyTo(deployment)
	return s.rollout(ctx, deployment, deployer, agent, payloads)
}

func noRollbackMessage(deployer Deployer) string {
	if isEntrypointHosted(deployer) {
		return "a link agent can't be rolled back"
	}
	return "a remote agent can't be rolled back"
}

var errNotARollbackTarget = &ValidationError{Field: "deploymentId", Message: "must be a deployment of the same agent or workflow"}

func rollbackTargetError(status, action string, snapshot *string) error {
	if status != DeployStatusSuccess || action == DeployActionUndeploy || snapshot == nil {
		return &ValidationError{Field: "deploymentId", Message: "must be a successful deploy or update"}
	}
	return nil
}

func (s *DeploymentService) recordDeployment(ctx context.Context, deployment *store.AgentDeployment, kind string, deployErr error) (*AgentDeploymentResponse, error) {
	if deployErr != nil {
		deployment.Status = DeployStatusFailed
		deployment.ErrorMessage, deployment.FailureReason, deployment.ErrorID = deployFailure(deployErr)
		s.logger.Error("deployment failed", "errorId", *deployment.ErrorID, "deploymentID", deployment.ID,
			"agentID", deployment.AgentID, "action", deployment.Action, "reason", *deployment.FailureReason, "error", deployErr)
	} else {
		deployment.Status = DeployStatusSuccess
		s.logger.Info("deployment succeeded", "action", deployment.Action, "agentID", deployment.AgentID, "kind", kind)
	}

	if err := s.deploymentRepo.Create(ctx, deployment); err != nil {
		if outcome := s.leader.duplicateInsertOutcome(ctx, store.IntentKindAgent, deployment.ID, err); outcome != nil {
			return nil, outcome
		}
		s.logger.Error("agent deployment row persist failed; control plane may have started an agent with an orphan deployment ID",
			"agentID", deployment.AgentID, "deploymentID", deployment.ID, "action", deployment.Action, "error", err)
		return nil, fmt.Errorf("create deployment: %w", err)
	}

	return buildAgentDeploymentResponse(deployment), nil
}

func (s *DeploymentService) currentDeployment(ctx context.Context, deployer Deployer, input DeployInput) *store.AgentDeployment {
	if s.agentCards == nil || s.agentDeployments == nil {
		return nil
	}
	latest, err := s.agentDeployments.GetLatestByAgentID(ctx, input.Agent.ID)
	if err != nil {
		s.logger.Warn("skip-if-current lookup failed; deploying", "agentID", input.Agent.ID, "error", err)
		return nil
	}
	if latest == nil || latest.Status != DeployStatusSuccess || latest.Action == DeployActionUndeploy || latest.AgentConfigSnapshot == nil {
		return nil
	}
	if _, replayErr := deployer.ReplayFromSnapshot(*latest.AgentConfigSnapshot); replayErr != nil {
		return nil
	}
	if !allInstancesRun(s.agentCards.Instances(deployer.InstanceName(input.Agent)), latest.ID) {
		return nil
	}
	input.Opts.DeploymentID = latest.ID
	input.Opts.SkillConfigsByName = nil
	rendered, err := deployer.BuildPayloads(ctx, input)
	if err != nil || rendered.SnapshotYAML != *latest.AgentConfigSnapshot {
		return nil
	}
	return latest
}

func allInstancesRun(instances []discovery.Instance, deploymentID string) bool {
	if len(instances) == 0 {
		return false
	}
	for _, inst := range instances {
		if platpkg.GetAgentDeploymentID(&inst.Card) != deploymentID || len(platpkg.RuntimeProblemsFromCard(&inst.Card)) > 0 {
			return false
		}
	}
	return true
}

func (s *DeploymentService) prepareDeploy(ctx context.Context, deployment *store.AgentDeployment, deployer Deployer, input DeployInput) ([]ControlPayload, error) {
	result, err := deployer.BuildPayloads(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("build deploy payloads: %w", err)
	}

	if result.SnapshotYAML != "" {
		snap := result.SnapshotYAML
		deployment.AgentConfigSnapshot = &snap
	}
	if result.ConfigHash != "" {
		h := result.ConfigHash
		deployment.ConfigHash = &h
	}
	if result.DataSnapshot != nil {
		if dataJSON, jsonErr := json.Marshal(result.DataSnapshot); jsonErr == nil {
			ds := string(dataJSON)
			deployment.AgentDataSnapshot = &ds
		}
	}
	result.SurfaceHashes.applyTo(deployment)
	return result.Payloads, nil
}

const instancesPathPrefix = "instances/"

func (s *DeploymentService) GetByID(ctx context.Context, id string) (*AgentDeploymentResponse, error) {
	d, err := s.deploymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetDeploymentWrap, err)
	}
	if d == nil {
		return nil, &NotFoundError{Resource: "deployment", ID: id}
	}
	return buildAgentDeploymentResponse(d), nil
}

func (s *DeploymentService) ListPaginated(ctx context.Context, pageNumber, pageSize int) (*PaginatedResponse[AgentDeploymentResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	deployments, total, err := s.deploymentRepo.ListPaginated(ctx, pageNumber, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}

	responses := make([]AgentDeploymentResponse, len(deployments))
	for i, d := range deployments {
		responses[i] = *buildAgentDeploymentResponse(&d)
	}

	return &PaginatedResponse[AgentDeploymentResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *DeploymentService) Undeploy(ctx context.Context, agentID, userID string) error {
	_, err := forwardOp(ctx, s.leader, opAgentUndeploy, agentUndeployArgs{AgentID: agentID, UserID: userID}, func(ctx context.Context, deploymentID string) (*AgentDeploymentResponse, error) {
		return nil, s.undeployHere(ctx, agentID, userID, deploymentID)
	}, s.GetByID, func(r *AgentDeploymentResponse) error { return undeployOutcome(agentID, r.Status, r.ErrorMessage) })
	return err
}

func undeployOutcome(agentID, status, message string) error {
	if status != DeployStatusSuccess {
		return &runtimeError{fmt.Errorf("undeploy %s: %s", agentID, message)}
	}
	return nil
}

func (s *DeploymentService) undeployHere(ctx context.Context, agentID, userID, deploymentID string) error {
	agent, _, _, _, _, err := s.agentRepo.GetByID(ctx, agentID)
	if err != nil {
		return fmt.Errorf("look up agent %s: %w", agentID, err)
	}
	if agent == nil {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	deployer, lookupErr := s.deployers.LookupOrError(agent.Type)
	if lookupErr != nil {
		return lookupErr
	}
	if err := deployer.Validate(ctx, DeployInput{Agent: agent, Action: DeployActionUndeploy}); err != nil {
		return onField(err, fieldAgentID)
	}

	deployment := &store.AgentDeployment{
		ID:        deploymentID,
		AgentID:   agentID,
		Action:    DeployActionUndeploy,
		CreatedBy: userID,
	}

	if _, recErr := s.rollout(ctx, deployment, deployer, agent, nil); recErr != nil {
		return fmt.Errorf("record undeploy for %s: %w", agentID, recErr)
	}
	return undeployOutcome(agentID, deployment.Status, derefStr(deployment.ErrorMessage))
}

func (s *DeploymentService) resolveAgentSkillNames(ctx context.Context, agent *store.Agent) []string {
	var names []string
	seen := make(map[string]struct{})
	add := func(n string) {
		if n == "" {
			return
		}
		if _, dup := seen[n]; dup {
			return
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}

	hasM2M := false
	if s.skillRepo != nil {
		ids, err := s.skillRepo.GetSkillIDsForAgent(ctx, agent.ID)
		if err != nil {
			s.logger.Warn("resolveAgentSkillNames: get skill IDs", "agentID", agent.ID, "error", err)
		}
		if len(ids) > 0 {
			skills, lookupErr := s.skillRepo.GetByIDs(ctx, ids)
			if lookupErr != nil {
				s.logger.Warn("resolveAgentSkillNames: get skills by IDs", "agentID", agent.ID, "error", lookupErr)
			} else {
				hasM2M = true
				for _, sk := range skills {
					add(sk.Name)
				}
			}
		}
	}

	if agent.SkillRefs == nil || *agent.SkillRefs == "" {
		return names
	}
	var refs []string
	if err := json.Unmarshal([]byte(*agent.SkillRefs), &refs); err != nil {
		s.logger.Warn("resolveAgentSkillNames: parse skill_refs", "agentID", agent.ID, "error", err)
		return names
	}
	for _, ref := range refs {
		if !keepSkillRef(ref, hasM2M) {
			continue
		}
		add(ref)
	}
	return names
}

func validateDeploymentStateTransition(action, displayStatus string) error {
	if action == DeployActionUpdate && displayStatus != DisplayStatusDeployed && displayStatus != DisplayStatusDeployFailed {
		return &ValidationError{Message: fmt.Sprintf("cannot update: current status is %q (must be deployed or deploy_failed)", displayStatus), Field: fieldAction}
	}
	return nil
}

func parseYAMLToControlPayloads(yamlBytes []byte) ([]map[string]any, error) {
	return configbuilder.ParseYAMLToControlPayloads(yamlBytes)
}

type AgentDeploymentService struct {
	deploymentRepo *store.AgentDeploymentRepo
	logger         *slog.Logger
}

func NewAgentDeploymentService(
	deploymentRepo *store.AgentDeploymentRepo,
	logger *slog.Logger,
) *AgentDeploymentService {
	return &AgentDeploymentService{
		deploymentRepo: deploymentRepo,
		logger:         logger,
	}
}

func (s *AgentDeploymentService) GetByID(ctx context.Context, id string) (*AgentDeploymentResponse, error) {
	d, err := s.deploymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetDeploymentWrap, err)
	}
	if d == nil {
		return nil, &NotFoundError{Resource: "agent deployment", ID: id}
	}
	return buildAgentDeploymentResponse(d), nil
}

func (s *AgentDeploymentService) ListPaginated(ctx context.Context, pageNumber, pageSize int, agentID string) (*PaginatedResponse[AgentDeploymentResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	deployments, total, err := s.deploymentRepo.ListPaginated(ctx, pageNumber, pageSize, agentID)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}

	responses := make([]AgentDeploymentResponse, len(deployments))
	for i, d := range deployments {
		responses[i] = *buildAgentDeploymentResponse(d)
	}

	return &PaginatedResponse[AgentDeploymentResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func buildAgentDeploymentResponse(d *store.AgentDeployment) *AgentDeploymentResponse {
	return &AgentDeploymentResponse{
		ID:            d.ID,
		AgentID:       d.AgentID,
		Status:        d.Status,
		Action:        d.Action,
		ErrorMessage:  derefStr(d.ErrorMessage),
		FailureReason: derefStr(d.FailureReason),
		ErrorID:       derefStr(d.ErrorID),
		CreatedBy:     d.CreatedBy,
		CreatedTime:   Timestamp(d.CreatedTime),
		UpdatedTime:   Timestamp(d.UpdatedTime),
	}
}

type GatewayDeploymentService struct {
	deploymentRepo *store.GatewayDeploymentRepo
	gatewayRepo    *store.GatewayRepo
	yamlSvc        *YAMLService
	controlClient  *control.Client
	leader         *deployLeader
	gwes           *gweStatusTracker
	logger         *slog.Logger
}

func NewGatewayDeploymentService(
	deploymentRepo *store.GatewayDeploymentRepo,
	gatewayRepo *store.GatewayRepo,
	yamlSvc *YAMLService,
	controlClient *control.Client,
	logger *slog.Logger,
) *GatewayDeploymentService {
	return &GatewayDeploymentService{
		deploymentRepo: deploymentRepo,
		gatewayRepo:    gatewayRepo,
		yamlSvc:        yamlSvc,
		controlClient:  controlClient,
		logger:         logger,
	}
}

func (s *GatewayDeploymentService) HasControlPlane() bool { return s.controlClient != nil }

func (s *GatewayDeploymentService) Deploy(ctx context.Context, req CreateGatewayDeploymentRequest, userID string) (*GatewayDeploymentResponse, error) {
	return forwardOp(ctx, s.leader, opEntrypointDeploy, entrypointDeployArgs{Req: req, UserID: userID}, func(ctx context.Context, deploymentID string) (*GatewayDeploymentResponse, error) {
		return s.deployHere(ctx, req, userID, deploymentID)
	}, s.GetByID, func(r *GatewayDeploymentResponse) error {
		return recordedFailure(r.Status, r.FailureReason, r.ErrorMessage)
	})
}

func (s *GatewayDeploymentService) deployHere(ctx context.Context, req CreateGatewayDeploymentRequest, userID, deploymentID string) (*GatewayDeploymentResponse, error) {
	if s.controlClient == nil {
		return nil, &ValidationError{Message: "deployment service not available (no broker connection)"}
	}

	gw, err := s.gatewayRepo.GetByID(ctx, req.GatewayID)
	if err != nil {
		return nil, fmt.Errorf("get gateway: %w", err)
	}
	if gw == nil {
		return nil, &NotFoundError{Resource: "entrypoint", ID: req.GatewayID}
	}

	snapshot, _ := json.Marshal(gw)
	snapshotStr := string(snapshot)
	deployment := &store.GatewayDeployment{
		ID:                    deploymentID,
		GatewayID:             req.GatewayID,
		Action:                req.Action,
		GatewayConfigSnapshot: &snapshotStr,
		CreatedBy:             userID,
	}

	var payloads []ControlPayload
	var deployErr error
	if deployment.Action != DeployActionUndeploy {
		if verr := validateGatewayReadyForDeploy(gw); verr != nil {
			return nil, verr
		}
		payloads, deployErr = s.prepareGatewayDeploy(deployment, gw)
	} else if refusal := refuseWhileLinkAgentsDeployed(ctx, s.gatewayRepo.DB(), gw, "undeploy", s.logger); refusal != nil {
		return nil, refusal
	}
	if deployErr == nil {
		if deployErr = s.leader.begin(ctx, store.IntentKindEntrypoint, deployment.GatewayID, deployment.ID, entrypointIntent{Deployment: *deployment, Payloads: payloads}); deployErr == nil {
			deployErr = s.rollOutEntrypoint(ctx, deployment, gatewayWireID(gw), payloads)
		}
	}
	if errors.Is(deployErr, errOtherLeader) {
		return nil, deployErr
	}

	if err := s.recordDeployment(ctx, deployment, deployErr); err != nil {
		return nil, err
	}

	if deployErr != nil {
		resp, err := s.GetByID(ctx, deployment.ID)
		if err != nil {
			s.logger.Warn("entrypoint deployment record lookup failed", "deploymentID", deployment.ID, "error", err)
		}
		return resp, deployErr
	}
	return s.GetByID(ctx, deployment.ID)
}

func (s *GatewayDeploymentService) recordDeployment(ctx context.Context, deployment *store.GatewayDeployment, deployErr error) error {
	deployment.Status = DeployStatusSuccess
	if deployErr != nil {
		errMsg, reason, errID := deployErr.Error(), deployFailureReason(deployErr), idgen.New()
		deployment.Status = DeployStatusFailed
		deployment.ErrorMessage, deployment.FailureReason, deployment.ErrorID = &errMsg, &reason, &errID
	}
	if createErr := s.deploymentRepo.Create(ctx, deployment); createErr != nil {
		if outcome := s.leader.duplicateInsertOutcome(ctx, store.IntentKindEntrypoint, deployment.ID, createErr); outcome != nil {
			return outcome
		}
		if deployErr != nil {
			return fmt.Errorf("create deployment: %w (deploy failed: %v)", createErr, deployErr)
		}
		return fmt.Errorf("create deployment: %w", createErr)
	}
	s.leader.end(ctx, deployment.ID)
	if deployErr != nil {
		level := slog.LevelError
		if *deployment.FailureReason == DeployFailureRejected {
			level = slog.LevelWarn
		}
		s.logger.Log(ctx, level, "entrypoint deployment failed", "errorId", *deployment.ErrorID, "deploymentID", deployment.ID,
			"entrypointID", deployment.GatewayID, "action", deployment.Action, "reason", *deployment.FailureReason, "error", deployErr)
	}
	return nil
}

type runtimeError struct{ err error }

func (e *runtimeError) Error() string { return e.err.Error() }
func (e *runtimeError) Unwrap() error { return e.err }

func deployFailure(deployErr error) (errMsg, reason, errorID *string) {
	msg, r, id := deployErr.Error(), deployFailureReason(deployErr), idgen.New()
	return &msg, &r, &id
}

func deployFailureReason(err error) string {
	if _, ok := errors.AsType[*ValidationError](err); ok {
		return DeployFailureRejected
	}
	if _, ok := errors.AsType[*runtimeError](err); ok {
		return DeployFailureRuntimeError
	}
	return DeployFailureInvalidConfig
}

func configNeedsSolaceBroker(config map[string]any) bool {
	appConfig := firstAppConfig(config)
	if appConfig["gateway_type"] != "event_mesh" {
		return false
	}
	emBroker, _ := appConfig["event_mesh_broker_config"].(map[string]any)
	return !samapp.IsDevMode(emBroker)
}

func firstAppConfig(config map[string]any) map[string]any {
	var first any
	switch apps := config["apps"].(type) {
	case []map[string]any:
		if len(apps) > 0 {
			first = apps[0]
		}
	case []any:
		if len(apps) > 0 {
			first = apps[0]
		}
	}
	app, _ := first.(map[string]any)
	appConfig, _ := app["app_config"].(map[string]any)
	return appConfig
}

func validateGatewayReadyForDeploy(gw *store.Gateway) error {
	if gw.Values == nil || *gw.Values == "" {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(*gw.Values), &parsed); err != nil {
		return nil
	}
	if gw.Type == "event_mesh" {
		parsed = withoutDevBrokerPlaceholders(parsed)
	}
	return validateGatewayValuesForDeploy(parsed)
}

func (s *GatewayDeploymentService) prepareGatewayDeploy(deployment *store.GatewayDeployment, gw *store.Gateway) ([]ControlPayload, error) {
	config, err := s.yamlSvc.BuildGatewayConfig(gw)
	if err != nil {
		return nil, fmt.Errorf("build entrypoint config: %w", err)
	}

	promptTransformHash, err := computeGatewayPromptTransformHash(config)
	if err != nil {
		return nil, fmt.Errorf("compute gateway prompt transform hash: %w", err)
	}
	deployment.PromptTransformHash = &promptTransformHash

	if !broker.SolaceSupported && configNeedsSolaceBroker(config) {
		return nil, &ValidationError{Message: "event mesh entrypoints require a Solace-enabled build; this server was built without Solace support and can only run an event mesh entrypoint in dev mode", Field: fieldEntrypointID}
	}

	yamlStr, err := configbuilder.MarshalYAML(config)
	if err != nil {
		return nil, fmt.Errorf("marshal gateway config: %w", err)
	}
	deployment.GatewayConfigSnapshot = &yamlStr

	configHash := computeGatewayConfigHash(gw)
	deployment.ConfigHash = &configHash

	dataSnapshot := DeployedGatewayInfo{
		Name:        gw.Name,
		Description: derefStr(gw.Description),
		Type:        gw.Type,
	}
	if gw.Values != nil {
		var parsed map[string]any
		if json.Unmarshal([]byte(*gw.Values), &parsed) == nil {
			secretFields := getSecretRegistry().GatewaySecretFields(gw.Type)
			dataSnapshot.Values = redactSecretFields(parsed, secretFields)
		}
	}
	if dataJSON, jsonErr := json.Marshal(dataSnapshot); jsonErr == nil {
		ds := string(dataJSON)
		deployment.GatewayDataSnapshot = &ds
	}

	payloads, err := configbuilder.ToControlPayloads(config)
	if err != nil {
		return nil, fmt.Errorf("extract control payloads: %w", err)
	}
	return payloads, nil
}

func (s *GatewayDeploymentService) GetByID(ctx context.Context, id string) (*GatewayDeploymentResponse, error) {
	d, err := s.deploymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetDeploymentWrap, err)
	}
	if d == nil {
		return nil, &NotFoundError{Resource: "entrypoint deployment", ID: id}
	}
	return buildGatewayDeploymentResponse(d), nil
}

func (s *GatewayDeploymentService) ListPaginated(ctx context.Context, pageNumber, pageSize int, gatewayID string) (*PaginatedResponse[GatewayDeploymentResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	deployments, total, err := s.deploymentRepo.ListPaginated(ctx, pageNumber, pageSize, gatewayID)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}

	responses := make([]GatewayDeploymentResponse, len(deployments))
	for i, d := range deployments {
		responses[i] = *buildGatewayDeploymentResponse(d)
	}

	return &PaginatedResponse[GatewayDeploymentResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func buildGatewayDeploymentResponse(d *store.GatewayDeployment) *GatewayDeploymentResponse {
	return &GatewayDeploymentResponse{
		ID:            d.ID,
		GatewayID:     d.GatewayID,
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
