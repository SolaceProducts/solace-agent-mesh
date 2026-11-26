// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
	"gopkg.in/yaml.v3"
)

const runtimeGracePeriod = 30 * time.Second

const (
	fieldAgentID           = "agentId"
	fieldWorkflowID        = "workflowId"
	fieldAction            = "action"
	fieldSystemPrompt      = "systemPrompt"
	fieldModelProvider     = "modelProvider"
	fieldConnectorIDs      = "connectorIds"
	fieldSkillIDs          = "skillIds"
	fieldSkillRefs         = "skillRefs"
	fieldToolsets          = "toolsets"
	fieldToolsetConfigs    = "toolsetConfigs"
	fieldAppConfig         = "appConfig"
	fieldAgentURL          = "agentUrl"
	fieldAgentCardURL      = "agentCardUrl"
	fieldRemoteAgentSkills = "skills"
	fieldAgentAuthConfig   = "agentAuthConfig"
)

func validateToolsets(ctx context.Context, toolsets []string, toolPackageRepo *store.ToolPackageRepo) error {
	for _, ts := range toolsets {
		if schema.IsValidBuiltinGroup(ts) {
			continue
		}
		if toolPackageRepo != nil {
			pkg, err := toolPackageRepo.GetByName(ctx, ts)
			if err != nil {
				return fmt.Errorf("look up tool package %q: %w", ts, err)
			}
			if pkg != nil && (pkg.DiscoveryStatus == "ready" || pkg.ZipResourceKey != nil) {
				continue
			}
		}
		valid := make([]string, 0, len(schema.ValidBuiltinGroups))
		for name := range schema.ValidBuiltinGroups {
			valid = append(valid, name)
		}
		slices.Sort(valid)
		return &ValidationError{Message: fmt.Sprintf("unknown toolset %q. valid built-ins: [%s] (or a ready custom tool package)", ts, strings.Join(valid, ", "))}
	}
	return nil
}

func validateToolsetsReady(ctx context.Context, toolsets []string, toolPackageRepo *store.ToolPackageRepo) error {
	if toolPackageRepo == nil {
		return nil
	}
	for _, ts := range toolsets {
		if schema.IsValidBuiltinGroup(ts) {
			continue
		}
		pkg, err := toolPackageRepo.GetByName(ctx, ts)
		if err != nil {
			return fmt.Errorf("look up tool package %q: %w", ts, err)
		}
		if pkg == nil {
			continue
		}
		if pkg.DiscoveryStatus == "ready" {
			continue
		}
		message := fmt.Sprintf("tool package %q is not ready (status: %q); wait for STR to finish discovery before deploying", ts, pkg.DiscoveryStatus)
		if pkg.DiscoveryStatus == "pending" {
			return errNotReady(message)
		}
		return &ValidationError{Message: message}
	}
	return nil
}

func validateSkillIDs(ctx context.Context, skillIDs []string, repo *store.SkillRepo) error {
	if repo == nil || len(skillIDs) == 0 {
		return nil
	}
	found, err := repo.GetByIDs(ctx, skillIDs)
	if err != nil {
		return fmt.Errorf("look up skill IDs: %w", err)
	}
	foundSet := make(map[string]struct{}, len(found))
	for _, sk := range found {
		foundSet[sk.ID] = struct{}{}
	}
	sorted := slices.Clone(skillIDs)
	slices.Sort(sorted)
	for _, id := range sorted {
		if _, ok := foundSet[id]; !ok {
			return &ValidationError{Message: fmt.Sprintf("unknown skill ID %q", id), Field: fieldSkillIDs}
		}
	}
	return nil
}

func ValidateAgentCreate(req CreateAgentRequest) error {
	agentType := req.Type
	if agentType == "" {
		agentType = AgentTypeStandard
	}
	if err := validateNoRepeats(req.InputModes, req.OutputModes, req.Connectors); err != nil {
		return err
	}
	return validateAgentRequestOnly(agentType, req.SystemPrompt, req.AdditionalConfigurations, req.SkillRefs)
}

func ValidateAgentUpdate(agentType string, req UpdateAgentRequest) error {
	if err := validateNoRepeats(req.InputModes, req.OutputModes, req.Connectors); err != nil {
		return err
	}
	return validateAgentRequestOnly(agentType, req.SystemPrompt, req.AdditionalConfigurations, req.SkillRefs)
}

func validateNoRepeats(inputModes, outputModes, connectors []string) error {
	fields := []struct {
		name   string
		values []string
	}{{"inputModes", inputModes}, {"outputModes", outputModes}, {"connectors", connectors}}
	details := map[string][]string{}
	var messages []string
	for _, f := range fields {
		seen := make(map[string]struct{}, len(f.values))
		for _, v := range f.values {
			if _, repeated := seen[v]; repeated {
				message := fmt.Sprintf("%s must not repeat %q", f.name, v)
				details[f.name] = []string{message}
				messages = append(messages, message)
				break
			}
			seen[v] = struct{}{}
		}
	}
	if len(details) == 0 {
		return nil
	}
	return &ValidationError{Message: strings.Join(messages, "; "), Details: details}
}

func validateAgentRequestOnly(agentType, systemPrompt string, additionalConfigurations json.RawMessage, skillRefs []string) error {
	if err := validateAgentBusinessRules(agentType, systemPrompt); err != nil {
		return err
	}
	if err := validateAdditionalConfigurations(additionalConfigurations); err != nil {
		return err
	}
	return validateSkillRefs(skillRefs)
}

func validateSkillRefs(refs []string) error {
	for _, ref := range refs {
		if strings.HasPrefix(ref, builtinSkillNamePrefix) && !builtinskills.HasName(ref) {
			return &ValidationError{Message: fmt.Sprintf("unknown built-in skill %q", ref), Field: fieldSkillRefs}
		}
	}
	return nil
}

func validateCrossToolsetTCKeyCollisions(ctx context.Context, agentID string, toolsets []string, repo *store.ToolPackageRepo) error {
	if repo == nil || len(toolsets) == 0 {
		return nil
	}

	agentSuffix := strings.ToUpper(strings.ReplaceAll(agentID, "-", ""))

	type keyOrigin struct {
		pkg, tool, configKey string
	}
	seen := make(map[string]keyOrigin)

	customNames := make([]string, 0, len(toolsets))
	for _, ts := range toolsets {
		if !schema.IsValidBuiltinGroup(ts) {
			customNames = append(customNames, ts)
		}
	}
	if len(customNames) == 0 {
		return nil
	}

	pkgs, err := repo.GetReadyPackagesByNames(ctx, customNames)
	if err != nil {
		return fmt.Errorf("look up tool packages for collision check: %w", err)
	}

	pkgIDs := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		pkgIDs = append(pkgIDs, p.ID)
	}

	filesByPkg, err := repo.GetFilesForPackages(ctx, pkgIDs)
	if err != nil {
		return fmt.Errorf("list files for packages in collision check: %w", err)
	}

	for _, pkg := range pkgs {
		for _, f := range filesByPkg[pkg.ID] {
			if f.ConfigSchema == nil || *f.ConfigSchema == "" {
				continue
			}
			var schemaFields []map[string]any
			if err := json.Unmarshal([]byte(*f.ConfigSchema), &schemaFields); err != nil {
				continue
			}
			for _, field := range schemaFields {
				isSecret, _ := field["secret"].(bool)
				if !isSecret {
					continue
				}
				configKey, _ := field["key"].(string)
				if configKey == "" {
					continue
				}

				toolUpper := strings.ToUpper(strings.ReplaceAll(pkg.Name+"__"+f.ToolName, "-", "_"))
				keyUpper := strings.ToUpper(strings.ReplaceAll(configKey, "-", "_"))
				envKey := fmt.Sprintf("TC_%s_%s_%s", toolUpper, keyUpper, agentSuffix)

				if prior, exists := seen[envKey]; exists {
					return &ValidationError{Message: fmt.Sprintf(
						"toolsets %q (tool %q, key %q) and %q (tool %q, key %q) produce the same environment variable %q — rename one toolset or tool to avoid the collision",
						prior.pkg, prior.tool, prior.configKey,
						pkg.Name, f.ToolName, configKey,
						envKey,
					), Field: fieldToolsets}
				}
				seen[envKey] = keyOrigin{pkg: pkg.Name, tool: f.ToolName, configKey: configKey}
			}
		}
	}
	return nil
}

const (
	AgentTypeStandard = store.AgentTypeStandard
	AgentTypeA2AProxy = store.AgentTypeA2AProxy
	AgentTypeLink     = store.AgentTypeLink
)

const errGetAgentWrap = "get agent: %w"

type AgentService struct {
	agentRepo       *store.AgentRepo
	deploymentRepo  *store.AgentDeploymentRepo
	connectorRepo   *store.ConnectorRepo
	toolPackageRepo *store.ToolPackageRepo
	skillRepo       *store.SkillRepo
	agentRegistry   *platpkg.AgentRegistry
	modelConfigSvc  *ModelConfigService
	logger          *slog.Logger
}

func NewAgentService(
	agentRepo *store.AgentRepo,
	deploymentRepo *store.AgentDeploymentRepo,
	connectorRepo *store.ConnectorRepo,
	toolPackageRepo *store.ToolPackageRepo,
	agentRegistry *platpkg.AgentRegistry,
	modelConfigSvc *ModelConfigService,
	logger *slog.Logger,
) *AgentService {
	return &AgentService{
		agentRepo:       agentRepo,
		deploymentRepo:  deploymentRepo,
		connectorRepo:   connectorRepo,
		toolPackageRepo: toolPackageRepo,
		agentRegistry:   agentRegistry,
		modelConfigSvc:  modelConfigSvc,
		logger:          logger,
	}
}

func (s *AgentService) WithSkillRepo(skillRepo *store.SkillRepo) {
	s.skillRepo = skillRepo
}

func (s *AgentService) CheckNameAvailable(ctx context.Context, name, excludeID string) error {
	return checkAgentNameFree(ctx, s.agentRepo, name, excludeID)
}

func checkAgentNameFree(ctx context.Context, repo *store.AgentRepo, name, excludeID string) error {
	existing, _, _, _, _, err := repo.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("check agent name: %w", err)
	}
	if existing != nil && existing.ID != excludeID {
		return NewAgentNameConflict(name)
	}
	return nil
}

func NewAgentNameConflict(name string) *ConflictError {
	return &ConflictError{Message: fmt.Sprintf("agent with name %q already exists", name)}
}

func nameConflictOr(err error, name, op string) error {
	if store.IsAgentNameViolation(err) {
		conflict := NewAgentNameConflict(name)
		conflict.LogMessage = fmt.Sprintf("%s: %v", op, err)
		return conflict
	}
	return fmt.Errorf("%s: %w", op, err)
}

func (s *AgentService) Create(ctx context.Context, req CreateAgentRequest, userID string) (*AgentResponse, error) {
	agentType := req.Type
	if agentType == "" {
		agentType = AgentTypeStandard
	}

	if err := ValidateAgentCreate(req); err != nil {
		return nil, err
	}

	modelProvider := firstModelProvider(req.ModelProvider)
	if err := s.validateModelProvider(ctx, modelProvider); err != nil {
		return nil, err
	}

	if err := s.CheckNameAvailable(ctx, req.Name, ""); err != nil {
		return nil, err
	}

	if len(req.Connectors) > 0 && s.connectorRepo != nil {
		if err := validateConnectorToolNames(ctx, req.Connectors, s.connectorRepo, req.Name, req.Toolsets); err != nil {
			return nil, err
		}
	}

	skillsJSON, err := json.Marshal(req.Skills)
	if err != nil {
		return nil, fmt.Errorf("marshal skills: %w", err)
	}
	skillsStr := string(skillsJSON)

	agent := &store.Agent{
		Name:                     req.Name,
		Type:                     agentType,
		Description:              strPtr(req.Description),
		SystemPrompt:             strPtr(req.SystemPrompt),
		Skills:                   &skillsStr,
		AdditionalConfigurations: normalizeAdditionalConfigurations(req.AdditionalConfigurations),
		CreatedBy:                userID,
		UpdatedBy:                userID,
	}
	if modelProvider != "" {
		agent.ModelProvider = &modelProvider
	}

	if len(req.SkillRefs) > 0 {
		refsJSON, refsErr := json.Marshal(req.SkillRefs)
		if refsErr != nil {
			return nil, fmt.Errorf("marshal skill refs: %w", refsErr)
		}
		refsStr := string(refsJSON)
		agent.SkillRefs = &refsStr
	}

	if err := validateSkillIDs(ctx, req.SkillIDs, s.skillRepo); err != nil {
		return nil, err
	}
	if err := validateToolsets(ctx, req.Toolsets, s.toolPackageRepo); err != nil {
		return nil, onField(err, fieldToolsets)
	}
	if err := validateCrossToolsetTCKeyCollisions(ctx, "", req.Toolsets, s.toolPackageRepo); err != nil {
		return nil, err
	}

	if err := s.canonicalizeAndValidateToolsetRefs(ctx, &req.Toolsets, &req.ToolsetConfigs, &req.ToolsetExcludedTools, req.AdditionalConfigurations); err != nil {
		return nil, onField(err, fieldToolsetConfigs)
	}

	excl, exclErr := encodeExclusions(req.ToolsetExcludedTools)
	if exclErr != nil {
		return nil, exclErr
	}

	var skillRows []store.AgentSkillRow
	if s.skillRepo != nil {
		var buildErr error
		skillRows, buildErr = buildNewAgentSkillRows(ctx, req.SkillIDs, req.SkillConfigs, s.skillRepo)
		if buildErr != nil {
			return nil, buildErr
		}
	}

	if createErr := s.agentRepo.Create(ctx, agent, req.Toolsets, req.InputModes, req.OutputModes, req.Connectors, excl); createErr != nil {
		return nil, nameConflictOr(createErr, req.Name, "create agent")
	}

	if s.skillRepo != nil {
		if err := s.skillRepo.SetAgentSkillsWithConfigs(ctx, agent.ID, skillRows); err != nil {
			return nil, fmt.Errorf("set agent skills: %w", err)
		}
	}

	if s.toolPackageRepo != nil {
		if err := applyToolsetConfigs(ctx, agent.ID, req.ToolsetConfigs, req.Toolsets, req.AdditionalConfigurations, s.toolPackageRepo); err != nil {
			return nil, err
		}
	}

	configs, configsErr := loadToolsetConfigBindings(ctx, agent.ID, s.toolPackageRepo)
	if configsErr != nil {
		return nil, configsErr
	}
	skillIDs := loadAgentSkillIDs(ctx, agent.ID, s.skillRepo)
	return s.buildResponse(ctx, agent, agentConfigInputs{toolsets: req.Toolsets, inputModes: req.InputModes, outputModes: req.OutputModes, connectorIDs: req.Connectors, exclusions: excl, toolsetConfigs: configs, skillIDs: skillIDs}, "", nil), nil
}

func (s *AgentService) StoredAdditionalConfigurations(ctx context.Context, id string) (json.RawMessage, error) {
	agent, _, _, _, _, err := s.agentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetAgentWrap, err)
	}
	if agent == nil {
		return nil, &NotFoundError{Resource: "agent", ID: id}
	}
	return agent.AdditionalConfigurations, nil
}

func (s *AgentService) GetByID(ctx context.Context, id string) (*AgentResponse, error) {
	agent, toolsets, inputModes, outputModes, connectorIDs, err := s.agentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetAgentWrap, err)
	}
	if agent == nil {
		return nil, &NotFoundError{Resource: "agent", ID: id}
	}

	_, excl, exclErr := s.agentRepo.GetToolsetsWithExclusions(ctx, id)
	if exclErr != nil {
		return nil, fmt.Errorf("load toolset exclusions for %s: %w", id, exclErr)
	}
	deploymentStatus, _ := s.agentRepo.GetDeploymentStatus(ctx, id)
	var latestDeploy *store.AgentDeployment
	if s.deploymentRepo != nil {
		deploys, _ := s.deploymentRepo.GetLatestSuccessfulDeploys(ctx, []string{id})
		latestDeploy = deploys[id]
	}
	configs, configsErr := loadToolsetConfigBindings(ctx, id, s.toolPackageRepo)
	if configsErr != nil {
		return nil, configsErr
	}
	skillIDs := loadAgentSkillIDs(ctx, id, s.skillRepo)
	resp := s.buildResponse(ctx, agent, agentConfigInputs{toolsets: toolsets, inputModes: inputModes, outputModes: outputModes, connectorIDs: connectorIDs, exclusions: excl, toolsetConfigs: configs, skillIDs: skillIDs}, deploymentStatus, latestDeploy)
	s.backfillDeployedConnectorSnapshots(ctx, resp)
	skillConfigs, skillConfigErr := loadAgentSkillConfigBindings(ctx, id, s.skillRepo)
	if skillConfigErr != nil {
		return nil, skillConfigErr
	}
	resp.SkillConfigs = skillConfigs
	resp.SkillConfigFields = loadSkillConfigFields(ctx, id, s.skillRepo, s.logger)
	return resp, nil
}

func (s *AgentService) backfillDeployedConnectorSnapshots(ctx context.Context, resp *AgentResponse) {
	if resp == nil || s.connectorRepo == nil {
		return
	}
	deployed, ok := resp.Deployed.(*DeployedAgentInfo)
	if !ok || deployed == nil {
		return
	}
	if len(deployed.ConnectorSnapshots) == 0 && len(deployed.Connectors) > 0 {
		deployed.ConnectorSnapshots = resolveConnectorSnapshots(ctx, deployed.Connectors, s.connectorRepo)
	}
}

func (s *AgentService) ListPaginated(ctx context.Context, pageNumber, pageSize int, connectorID string) (*PaginatedResponse[AgentResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var agents []store.AgentWithRelations
	var total int
	var err error

	if connectorID != "" {
		agents, total, err = s.agentRepo.ListByConnectorID(ctx, connectorID, pageNumber, pageSize)
	} else {
		agents, total, err = s.agentRepo.ListPaginated(ctx, pageNumber, pageSize)
	}
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}

	agentIDs := make([]string, len(agents))
	for i, a := range agents {
		agentIDs[i] = a.Agent.ID
	}
	statuses, _ := s.agentRepo.GetDeploymentStatuses(ctx, agentIDs)
	var deploys map[string]*store.AgentDeployment
	if s.deploymentRepo != nil {
		deploys, _ = s.deploymentRepo.GetLatestSuccessfulDeploys(ctx, agentIDs)
	}

	configsByAgent := batchLoadToolsetConfigBindings(ctx, agentIDs, s.toolPackageRepo)

	var skillsByAgent map[string][]string
	if s.skillRepo != nil {
		if sm, sErr := s.skillRepo.GetSkillIDsForAgents(ctx, agentIDs); sErr != nil {
			s.logger.Warn("list agents: batch skill ID lookup failed", "error", sErr, "page", pageNumber, "connectorID", connectorID)
		} else {
			skillsByAgent = sm
		}
	}

	responses := make([]AgentResponse, len(agents))
	for i, a := range agents {
		status := statuses[a.Agent.ID]
		var latestDeploy *store.AgentDeployment
		if deploys != nil {
			latestDeploy = deploys[a.Agent.ID]
		}
		responses[i] = *s.buildResponse(ctx, a.Agent, agentConfigInputs{toolsets: a.Toolsets, inputModes: a.InputModes, outputModes: a.OutputModes, connectorIDs: a.ConnectorIDs, exclusions: a.ExcludedTools, toolsetConfigs: configsByAgent[a.Agent.ID], skillIDs: skillsByAgent[a.Agent.ID]}, status, latestDeploy)
	}
	s.backfillDeployedConnectorSnapshotsBatch(ctx, responses)

	return &PaginatedResponse[AgentResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *AgentService) backfillDeployedConnectorSnapshotsBatch(ctx context.Context, responses []AgentResponse) {
	if s.connectorRepo == nil {
		return
	}
	idSet := make(map[string]struct{})
	for i := range responses {
		d, ok := responses[i].Deployed.(*DeployedAgentInfo)
		if !ok || d == nil || len(d.ConnectorSnapshots) > 0 {
			continue
		}
		for _, id := range d.Connectors {
			idSet[id] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	conns, err := s.connectorRepo.GetByIDs(ctx, ids)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("list agents: connector snapshot backfill lookup failed", "error", err)
		}
		return
	}
	if len(conns) == 0 {
		return
	}
	byID := make(map[string]ConnectorSnapshot, len(conns))
	for _, c := range conns {
		cs := ConnectorSnapshot{ID: c.ID, Name: c.Name, Type: c.Type}
		if c.Subtype != nil {
			cs.Subtype = *c.Subtype
		}
		byID[c.ID] = cs
	}
	for i := range responses {
		d, ok := responses[i].Deployed.(*DeployedAgentInfo)
		if !ok || d == nil || len(d.ConnectorSnapshots) > 0 {
			continue
		}
		snaps := make([]ConnectorSnapshot, 0, len(d.Connectors))
		for _, id := range d.Connectors {
			if cs, found := byID[id]; found {
				snaps = append(snaps, cs)
			}
		}
		d.ConnectorSnapshots = snaps
	}
}

func (s *AgentService) Update(ctx context.Context, id string, req UpdateAgentRequest, userID string) (*AgentResponse, error) {
	existing, _, _, _, _, err := s.agentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetAgentWrap, err)
	}
	if existing == nil {
		return nil, &NotFoundError{Resource: "agent", ID: id}
	}
	if err := refuseRemoteAgentMirror(existing); err != nil {
		return nil, err
	}

	if err := ValidateAgentUpdate(existing.Type, req); err != nil {
		return nil, err
	}

	modelProvider := firstModelProvider(req.ModelProvider)
	if err := s.validateModelProvider(ctx, modelProvider); err != nil {
		return nil, err
	}

	if len(req.Connectors) > 0 && s.connectorRepo != nil {
		if err := validateConnectorToolNames(ctx, req.Connectors, s.connectorRepo, existing.Name, req.Toolsets); err != nil {
			return nil, err
		}
	}

	skillsJSON, marshalErr := json.Marshal(req.Skills)
	if marshalErr != nil {
		return nil, fmt.Errorf("marshal skills: %w", marshalErr)
	}
	skillsStr := string(skillsJSON)

	existing.Name = req.Name
	existing.Description = strPtr(req.Description)
	existing.SystemPrompt = strPtr(req.SystemPrompt)
	existing.Skills = &skillsStr
	existing.AdditionalConfigurations = normalizeAdditionalConfigurations(req.AdditionalConfigurations)
	existing.UpdatedBy = userID

	if modelProvider != "" {
		existing.ModelProvider = &modelProvider
	} else {
		existing.ModelProvider = nil
	}

	if len(req.SkillRefs) > 0 {
		refsJSON, refsErr := json.Marshal(req.SkillRefs)
		if refsErr != nil {
			return nil, fmt.Errorf("marshal skill refs: %w", refsErr)
		}
		refsStr := string(refsJSON)
		existing.SkillRefs = &refsStr
	} else {
		existing.SkillRefs = nil
	}

	if req.SkillIDs != nil {
		if err := validateSkillIDs(ctx, *req.SkillIDs, s.skillRepo); err != nil {
			return nil, err
		}
	}
	if err := validateToolsets(ctx, req.Toolsets, s.toolPackageRepo); err != nil {
		return nil, onField(err, fieldToolsets)
	}
	if err := validateCrossToolsetTCKeyCollisions(ctx, id, req.Toolsets, s.toolPackageRepo); err != nil {
		return nil, err
	}

	if err := s.canonicalizeAndValidateToolsetRefs(ctx, &req.Toolsets, &req.ToolsetConfigs, &req.ToolsetExcludedTools, req.AdditionalConfigurations); err != nil {
		return nil, onField(err, fieldToolsetConfigs)
	}

	excl, exclErr := encodeExclusions(req.ToolsetExcludedTools)
	if exclErr != nil {
		return nil, exclErr
	}

	writeSkills := s.skillRepo != nil && req.SkillIDs != nil
	var skillRows []store.AgentSkillRow
	if writeSkills {
		var buildErr error
		skillRows, buildErr = buildAgentSkillRows(ctx, id, *req.SkillIDs, req.SkillConfigs, s.skillRepo)
		if buildErr != nil {
			return nil, buildErr
		}
	}

	if updateErr := s.agentRepo.Update(ctx, existing, req.Toolsets, req.InputModes, req.OutputModes, req.Connectors, excl); updateErr != nil {
		return nil, nameConflictOr(updateErr, existing.Name, "update agent")
	}

	if writeSkills {
		if err := s.skillRepo.SetAgentSkillsWithConfigs(ctx, id, skillRows); err != nil {
			return nil, fmt.Errorf("set agent skills: %w", err)
		}
	}

	if s.toolPackageRepo != nil {
		if err := applyToolsetConfigs(ctx, id, req.ToolsetConfigs, req.Toolsets, req.AdditionalConfigurations, s.toolPackageRepo); err != nil {
			return nil, err
		}
	}

	deploymentStatus, _ := s.agentRepo.GetDeploymentStatus(ctx, id)
	var latestDeploy *store.AgentDeployment
	if s.deploymentRepo != nil {
		deploys, _ := s.deploymentRepo.GetLatestSuccessfulDeploys(ctx, []string{id})
		latestDeploy = deploys[id]
	}
	configs, configsErr := loadToolsetConfigBindings(ctx, id, s.toolPackageRepo)
	if configsErr != nil {
		return nil, configsErr
	}
	skillIDs := loadAgentSkillIDs(ctx, id, s.skillRepo)
	return s.buildResponse(ctx, existing, agentConfigInputs{toolsets: req.Toolsets, inputModes: req.InputModes, outputModes: req.OutputModes, connectorIDs: req.Connectors, exclusions: excl, toolsetConfigs: configs, skillIDs: skillIDs}, deploymentStatus, latestDeploy), nil
}

func refuseRemoteAgentMirror(agent *store.Agent) error {
	switch agent.Type {
	case AgentTypeA2AProxy:
		return &ConflictError{Message: "this agent mirrors a remote agent; change or delete it through the remote agents API"}
	case AgentTypeLink:
		return &ConflictError{Message: "this agent mirrors a link agent; change or delete it through the link agents API"}
	}
	return nil
}

func (s *AgentService) checkDeletable(ctx context.Context, id string) error {
	agent, _, _, _, _, err := s.agentRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf(errGetAgentWrap, err)
	}
	if agent == nil {
		return &NotFoundError{Resource: "agent", ID: id}
	}
	return refuseRemoteAgentMirror(agent)
}

func (s *AgentService) Delete(ctx context.Context, id string) error {
	if err := s.checkDeletable(ctx, id); err != nil {
		return err
	}
	if err := s.agentRepo.Delete(ctx, id); err != nil {
		return &NotFoundError{Resource: "agent", ID: id}
	}
	return nil
}

func (s *AgentService) DeleteWithUndeploy(ctx context.Context, id, userID string, deploySvc *DeploymentService) error {
	if err := s.checkDeletable(ctx, id); err != nil {
		return err
	}
	if deploySvc != nil {
		mayBeLive, err := s.agentRepo.HasPossiblyLiveDeployment(ctx, id)
		if err != nil {
			return err
		}
		if mayBeLive {
			if err := deploySvc.Undeploy(ctx, id, userID); err != nil {
				return undeployBlocksDelete(s.logger, "agent undeploy before delete failed", "agentID", id)
			}
		}
	}
	if err := s.agentRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	return nil
}

func undeployBlocksDelete(logger *slog.Logger, msg string, args ...any) error {
	if logger != nil {
		logger.Warn(msg, args...)
	}
	return &ExternalServiceError{Message: "undeploy failed; see server logs for details"}
}

type agentYAMLDoc struct {
	Apps []map[string]any `yaml:"apps"`
}

func ParseAgentYAML(data []byte) (CreateAgentRequest, error) {
	var doc agentYAMLDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return CreateAgentRequest{}, &ValidationError{Message: fmt.Sprintf("invalid YAML: %s", err.Error())}
	}

	var appCfg map[string]any
	for _, app := range doc.Apps {
		if cfg, ok := app["app_config"].(map[string]any); ok {
			appCfg = cfg
			break
		}
	}
	if appCfg == nil {
		return CreateAgentRequest{}, &ValidationError{Message: "agent YAML must contain an apps[].app_config block"}
	}

	displayName, _ := appCfg["display_name"].(string)
	agentName, _ := appCfg["agent_name"].(string)
	name := displayName
	if name == "" {
		name = agentName
	}
	if name == "" {
		return CreateAgentRequest{}, &ValidationError{Message: "agent YAML missing required field: display_name"}
	}

	description := ""
	if card, ok := appCfg["agent_card"].(map[string]any); ok {
		description, _ = card["description"].(string)
	}

	systemPrompt, _ := appCfg["instruction"].(string)

	var toolsets []string
	if tools, ok := appCfg["tools"].([]any); ok {
		seen := make(map[string]bool)
		for _, t := range tools {
			if tmap, ok := t.(map[string]any); ok {
				if ts, ok := tmap["toolset"].(string); ok && ts != "" && !seen[ts] {
					seen[ts] = true
					toolsets = append(toolsets, ts)
				}
			}
		}
	}

	var inputModes, outputModes []string
	var skills []SkillRequest
	if card, ok := appCfg["agent_card"].(map[string]any); ok {
		inputModes = toStringSlice(card["defaultInputModes"])
		outputModes = toStringSlice(card["defaultOutputModes"])
		skills = parseAgentCardSkills(card["skills"])
	}

	var modelProvider []string
	if mp := toStringSlice(appCfg["model_provider"]); len(mp) > 0 {
		modelProvider = mp
	}

	if systemPrompt == "" {
		return CreateAgentRequest{}, &ValidationError{Field: "instruction", Message: "instruction is required"}
	}

	return CreateAgentRequest{
		Name:          name,
		Description:   description,
		SystemPrompt:  systemPrompt,
		Type:          AgentTypeStandard,
		Skills:        skills,
		Toolsets:      toolsets,
		InputModes:    inputModes,
		OutputModes:   outputModes,
		ModelProvider: modelProvider,
	}, nil
}

func parseAgentCardSkills(v any) []SkillRequest {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]SkillRequest, 0, len(arr))
	for _, entry := range arr {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		id, _ := m["id"].(string)
		desc, _ := m["description"].(string)
		out = append(out, SkillRequest{
			ID:          id,
			Name:        name,
			Description: desc,
			Tags:        toStringSlice(m["tags"]),
			Examples:    toStringSlice(m["examples"]),
			InputModes:  toStringSlice(m["inputModes"]),
			OutputModes: toStringSlice(m["outputModes"]),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

type agentConfigInputs struct {
	toolsets       []string
	inputModes     []string
	outputModes    []string
	connectorIDs   []string
	exclusions     map[string]string
	toolsetConfigs []ToolsetConfigBinding
	skillIDs       []string
}

func (s *AgentService) buildResponse(ctx context.Context, a *store.Agent, cfg agentConfigInputs, deploymentStatus string, latestDeploy *store.AgentDeployment) *AgentResponse {
	toolsets, inputModes, outputModes := cfg.toolsets, cfg.inputModes, cfg.outputModes
	connectorIDs, exclusions, toolsetConfigs, skillIDs := cfg.connectorIDs, cfg.exclusions, cfg.toolsetConfigs, cfg.skillIDs
	brokerInstanceName := AgentInstanceName(a)

	resp := &AgentResponse{
		ID:                       a.ID,
		Name:                     a.Name,
		InstanceName:             brokerInstanceName,
		Type:                     a.Type,
		Description:              derefStr(a.Description),
		SystemPrompt:             derefStr(a.SystemPrompt),
		Toolsets:                 ensureSlice(toolsets),
		ToolsetConfigs:           toolsetConfigs,
		InputModes:               ensureSlice(inputModes),
		OutputModes:              ensureSlice(outputModes),
		Connectors:               ensureSlice(connectorIDs),
		ModelProvider:            modelProviderToSlice(a.ModelProvider),
		AdditionalConfigurations: cloneAdditionalConfigurations(a.AdditionalConfigurations),
		DeploymentStatus:         deploymentStatus,
		CreatedBy:                a.CreatedBy,
		UpdatedBy:                a.UpdatedBy,
		CreatedTime:              Timestamp(a.CreatedTime),
		UpdatedTime:              Timestamp(a.UpdatedTime),
	}

	if a.IsEphemeral {
		resp.IsEphemeral = true
		resp.SourceSessionID = derefStr(a.SourceSessionID)
		resp.SourceComponentName = derefStr(a.SourceComponentName)
		resp.SourceComponentKind = derefStr(a.SourceComponentKind)
		if a.ExpiresAt != nil {
			ts := Timestamp(*a.ExpiresAt)
			resp.ExpiresAt = &ts
		}
	}

	if decoded, _ := decodeExclusions(exclusions); len(decoded) > 0 {
		resp.ToolsetExcludedTools = decoded
	}

	if s.agentRegistry != nil {
		if card := s.agentRegistry.Get(brokerInstanceName); card != nil {
			resp.RuntimeStatus = RunningStatus(s.agentRegistry.RuntimeProblems(brokerInstanceName))
			resp.ToolRegistrations = toolRegistrationResponses(platpkg.AgentToolRegistrations(card))
		} else if deploymentStatus == DisplayStatusDeployed && latestDeploy != nil {
			if time.Since(time.UnixMilli(latestDeploy.UpdatedTime)) < runtimeGracePeriod {
				resp.RuntimeStatus = StartingStatus()
			} else {
				resp.RuntimeStatus = DisconnectedStatus()
			}
		} else {
			resp.RuntimeStatus = StoppedStatus()
		}
	} else {
		resp.RuntimeStatus = StoppedStatus()
	}

	isDeployed := deploymentStatus == DisplayStatusDeployed || deploymentStatus == DisplayStatusDeployFailed

	if isDeployed && latestDeploy != nil && latestDeploy.ConfigHash != nil {
		connectorFingerprints := connectorConfigFingerprints(ctx, s.connectorRepo, connectorIDs)
		currentHash := computeAgentConfigHash(a, toolsets, inputModes, outputModes, connectorFingerprints, skillIDs)
		if currentHash == *latestDeploy.ConfigHash {
			resp.SyncStatus = strPtr("in_sync")
		} else {
			resp.SyncStatus = strPtr("out_of_sync")
		}
	}

	if isDeployed && latestDeploy != nil {
		deployed, err := decodeDeployedSnapshot[DeployedAgentInfo](latestDeploy.AgentDataSnapshot)
		if err != nil && s.logger != nil {
			s.logger.Warn("failed to decode deployed snapshot", "agentID", a.ID, "error", err)
		}
		if deployed != nil {
			deployed.DeployedAt = Timestamp(latestDeploy.CreatedTime)
			deployed.DeployedBy = latestDeploy.CreatedBy
			resp.Deployed = deployed
		}
	}

	s.populateSkillFields(resp, a, skillIDs)

	return resp
}

func toolRegistrationResponses(regs []platpkg.ToolEntryRegistration) []ToolRegistrationResponse {
	if len(regs) == 0 {
		return nil
	}
	out := make([]ToolRegistrationResponse, len(regs))
	for i, reg := range regs {
		out[i] = ToolRegistrationResponse{
			ToolType:    reg.ToolType,
			ConnectorID: reg.ConnectorID,
			Name:        reg.Name,
			Status:      reg.Status,
			Error:       reg.Error,
			ToolCount:   reg.ToolCount,
			ToolNames:   reg.ToolNames,
		}
	}
	return out
}

func (s *AgentService) populateSkillFields(resp *AgentResponse, a *store.Agent, skillIDs []string) {
	if a.Skills != nil && *a.Skills != "" {
		var skills []SkillResponse
		if err := json.Unmarshal([]byte(*a.Skills), &skills); err == nil {
			resp.Skills = skills
		} else if s.logger != nil {
			s.logger.Warn("parse skills", "agentID", a.ID, "error", err)
		}
	}
	if resp.Skills == nil {
		resp.Skills = []SkillResponse{}
	}

	if a.SkillRefs != nil && *a.SkillRefs != "" {
		var refs []string
		if err := json.Unmarshal([]byte(*a.SkillRefs), &refs); err == nil {
			hasM2M := len(skillIDs) > 0
			kept := refs[:0]
			for _, ref := range refs {
				if keepSkillRef(ref, hasM2M) {
					kept = append(kept, ref)
				}
			}
			resp.SkillRefs = kept
		} else if s.logger != nil {
			s.logger.Warn("parse skill_refs", "agentID", a.ID, "error", err)
		}
	}
	if resp.SkillRefs == nil {
		resp.SkillRefs = []string{}
	}

	if skillIDs != nil {
		resp.SkillIDs = skillIDs
	} else {
		resp.SkillIDs = []string{}
	}
}

func applyToolsetConfigs(ctx context.Context, agentID string, bindings []ToolsetConfigBinding, boundToolsets []string, additionalConfig json.RawMessage, repo *store.ToolPackageRepo) error {
	if bindings == nil {
		return nil
	}
	if len(bindings) == 0 {
		if err := repo.SetToolsetConfigsForAgent(ctx, agentID, nil); err != nil {
			return err
		}
		return repo.SetBuiltinToolsetConfigsForAgent(ctx, agentID, nil)
	}

	if err := validateToolsetConfigBindings(ctx, bindings, boundToolsets, additionalConfig, repo); err != nil {
		return err
	}

	priorByPackage, err := priorToolsetConfigValuesByPackage(ctx, agentID, repo)
	if err != nil {
		return err
	}
	priorBuiltin, err := priorBuiltinToolsetConfigValues(ctx, agentID, repo)
	if err != nil {
		return err
	}

	pkgRows := make([]store.AgentToolsetConfig, 0, len(bindings))
	builtinRows := make([]store.AgentBuiltinToolsetConfig, 0, len(bindings))
	for _, b := range bindings {
		if _, isBuiltin := builtinToolsetTools[b.ToolsetName]; isBuiltin {
			merged, mergeErr := mergeBuiltinToolsetBindingSecrets(b.ToolsetName, b.ConfigValues, priorBuiltin)
			if mergeErr != nil {
				return mergeErr
			}
			configJSON, marshalErr := marshalConfigValues(b.ToolsetName, merged)
			if marshalErr != nil {
				return marshalErr
			}
			volumeBindingsJSON, marshalErr := marshalVolumeBindings(b.ToolsetName, b.VolumeBindings)
			if marshalErr != nil {
				return marshalErr
			}
			networkBindingsJSON, marshalErr := marshalNetworkBindings(b.ToolsetName, b.NetworkBindings)
			if marshalErr != nil {
				return marshalErr
			}
			builtinRows = append(builtinRows, store.AgentBuiltinToolsetConfig{
				AgentID:         agentID,
				ToolsetName:     b.ToolsetName,
				ConfigValues:    configJSON,
				VolumeBindings:  volumeBindingsJSON,
				NetworkBindings: networkBindingsJSON,
			})
			continue
		}

		pkg, err := repo.GetByName(ctx, b.ToolsetName)
		if err != nil {
			return fmt.Errorf("resolve toolset binding %q: %w", b.ToolsetName, err)
		}
		if pkg == nil {
			return fmt.Errorf("toolset %q vanished between validation and write", b.ToolsetName)
		}
		merged, mergeErr := mergeToolsetBindingSecrets(ctx, agentID, pkg.ID, b.ConfigValues, priorByPackage[pkg.ID], repo)
		if mergeErr != nil {
			return mergeErr
		}
		configJSON, marshalErr := marshalConfigValues(b.ToolsetName, merged)
		if marshalErr != nil {
			return marshalErr
		}
		pkgRows = append(pkgRows, store.AgentToolsetConfig{
			AgentID:      agentID,
			PackageID:    pkg.ID,
			ConfigValues: configJSON,
		})
	}

	if err := repo.SetToolsetConfigsForAgent(ctx, agentID, pkgRows); err != nil {
		return err
	}
	return repo.SetBuiltinToolsetConfigsForAgent(ctx, agentID, builtinRows)
}

func marshalVolumeBindings(toolsetName string, bindings map[string]string) (*string, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		return nil, fmt.Errorf("marshal volume bindings for %q: %w", toolsetName, err)
	}
	s := string(raw)
	return &s, nil
}

func validateBuiltinToolsetVolumeBindings(toolsetName string, bindings map[string]string, slotNames map[string]struct{}) error {
	if len(bindings) == 0 {
		return nil
	}
	if !builtinToolsetAcceptsVolumeBindings(toolsetName) {
		return &ValidationError{Message: fmt.Sprintf("toolset %q does not support volume bindings", toolsetName)}
	}
	params := builtinToolsetVolumeParams[toolsetName]

	details := map[string][]string{}
	for param, slot := range bindings {
		field := "volumeBindings." + param
		if _, ok := params[param]; !ok {
			details[field] = append(details[field], fmt.Sprintf("unknown volume param %q for toolset %q", param, toolsetName))
			continue
		}
		if slot == "" {
			details[field] = append(details[field], "volume slot name must not be empty")
			continue
		}
		if _, ok := slotNames[slot]; !ok {
			details[field] = append(details[field], fmt.Sprintf("references unknown volume slot %q; declare it under volumes in additionalConfigurations", slot))
		}
	}
	if len(details) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf("invalid volume bindings for toolset %q", toolsetName),
			Details: details,
		}
	}
	return nil
}

func volumeSlotNamesFromConfig(raw json.RawMessage) map[string]struct{} {
	out := map[string]struct{}{}
	if len(raw) == 0 {
		return out
	}
	var cfg struct {
		Volumes []struct {
			Name string `json:"name"`
		} `json:"volumes"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return out
	}
	for _, v := range cfg.Volumes {
		if v.Name != "" {
			out[v.Name] = struct{}{}
		}
	}
	return out
}

func marshalNetworkBindings(toolsetName string, bindings map[string]string) (*string, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		return nil, fmt.Errorf("marshal network bindings for %q: %w", toolsetName, err)
	}
	s := string(raw)
	return &s, nil
}

func validateBuiltinToolsetNetworkBindings(toolsetName string, bindings map[string]string, policyNames map[string]struct{}) error {
	if len(bindings) == 0 {
		return nil
	}
	if !builtinToolsetAcceptsNetworkBindings(toolsetName) {
		return &ValidationError{Message: fmt.Sprintf("toolset %q does not support network bindings", toolsetName)}
	}
	params := builtinToolsetNetworkParamUnion(toolsetName)

	details := map[string][]string{}
	for param, policy := range bindings {
		field := "networkBindings." + param
		if _, ok := params[param]; !ok {
			details[field] = append(details[field], fmt.Sprintf("unknown network param %q for toolset %q", param, toolsetName))
			continue
		}
		if policy == "" {
			details[field] = append(details[field], "network policy name must not be empty")
			continue
		}
		if _, ok := policyNames[policy]; !ok {
			details[field] = append(details[field], fmt.Sprintf("references unknown network policy %q; declare it under networks in additionalConfigurations", policy))
		}
	}
	if len(details) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf("invalid network bindings for toolset %q", toolsetName),
			Details: details,
		}
	}
	return nil
}

func networkPolicyNamesFromConfig(raw json.RawMessage) map[string]struct{} {
	out := map[string]struct{}{}
	if len(raw) == 0 {
		return out
	}
	var cfg struct {
		Networks []struct {
			Name string `json:"name"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return out
	}
	for _, n := range cfg.Networks {
		if n.Name != "" {
			out[n.Name] = struct{}{}
		}
	}
	return out
}

func validateToolsetBindingValues(ctx context.Context, toolsetName, packageID string, values map[string]any, repo *store.ToolPackageRepo) error {
	if len(values) == 0 {
		return nil
	}
	files, err := repo.GetFilesForPackage(ctx, packageID)
	if err != nil {
		return fmt.Errorf("load files for toolset %q: %w", toolsetName, err)
	}
	schemaByTool := loadConfigSchemaByTool(files)
	details := map[string][]string{}
	for toolName, toolValsRaw := range values {
		if toolName == reservedAuthConfigKey {
			for subKey, msgs := range validateDeployerAuthBlock(toolValsRaw) {
				path := toolsetName + "." + subKey
				details[path] = append(details[path], msgs...)
			}
			continue
		}
		if toolName == reservedHILConfigKey {
			for subKey, msgs := range validateToolsetHIL(toolValsRaw, toolNamesFromFiles(files)) {
				path := toolsetName + "." + subKey
				details[path] = append(details[path], msgs...)
			}
			continue
		}
		if toolName == reservedRequiredScopesConfigKey {
			for subKey, msgs := range validateToolsetRequiredScopes(toolValsRaw, toolNamesFromFiles(files)) {
				path := toolsetName + "." + subKey
				details[path] = append(details[path], msgs...)
			}
			continue
		}
		toolSchema, knownTool := schemaByTool[toolName]
		if !knownTool {
			if len(schemaByTool) == 0 {
				continue
			}
			details[toolsetName+"."+toolName] = append(details[toolsetName+"."+toolName], "unknown tool name for this toolset")
			continue
		}
		toolVals, ok := toolValsRaw.(map[string]any)
		if !ok {
			details[toolsetName+"."+toolName] = append(details[toolsetName+"."+toolName], "config values must be an object")
			continue
		}
		for key, val := range toolVals {
			qualifiedPath := toolsetName + "." + toolName + "." + key
			entry, knownKey := toolSchema[key]
			if !knownKey {
				details[qualifiedPath] = append(details[qualifiedPath], "unknown configuration field for this tool")
				continue
			}
			if msg := validateConfigValueType(entry, val); msg != "" {
				details[qualifiedPath] = append(details[qualifiedPath], msg)
			}
		}
	}
	if len(details) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf("invalid configValues for toolset %q", toolsetName),
			Details: details,
		}
	}
	return nil
}

func ValidateBuiltinToolsetConfigBindings(bindings []ToolsetConfigBinding, additionalConfig json.RawMessage) error {
	slotNames := volumeSlotNamesFromConfig(additionalConfig)
	policyNames := networkPolicyNamesFromConfig(additionalConfig)
	for _, b := range bindings {
		if _, isBuiltin := builtinToolsetTools[b.ToolsetName]; !isBuiltin {
			continue
		}
		if err := builtinToolsetSecretFieldTypes(b); err != nil {
			return err
		}
		if err := validateBuiltinToolsetConfigKeys(b.ToolsetName, b.ConfigValues); err != nil {
			return err
		}
		if err := validateBuiltinToolsetVolumeBindings(b.ToolsetName, b.VolumeBindings, slotNames); err != nil {
			return err
		}
		if err := validateBuiltinToolsetNetworkBindings(b.ToolsetName, b.NetworkBindings, policyNames); err != nil {
			return err
		}
	}
	return nil
}

func validateBuiltinToolsetConfigKeys(toolsetName string, configValues map[string]any) error {
	if len(configValues) == 0 {
		return nil
	}
	available, ok := builtinToolsetTools[toolsetName]
	if !ok {
		return nil
	}
	allowed := make(map[string]struct{}, len(available))
	for _, n := range available {
		allowed[n] = struct{}{}
	}
	var messages []string
	details := map[string][]string{}
	for _, key := range slices.Sorted(maps.Keys(configValues)) {
		val := configValues[key]
		if key == reservedHILConfigKey {
			if hilDetails := validateToolsetHIL(val, allowed); len(hilDetails) > 0 {
				messages = append(messages, fmt.Sprintf("invalid approval settings (hil) for toolset %q", toolsetName))
				for path, msgs := range hilDetails {
					details[toolsetName+"."+path] = msgs
				}
			}
			continue
		}
		if _, ok := allowed[key]; !ok {
			messages = append(messages, fmt.Sprintf(
				"configValues[%q] is not a tool in toolset %q. Available tools: %s.",
				key, toolsetName, strings.Join(available, ", "),
			))
			details[toolsetName+"."+key] = append(details[toolsetName+"."+key], "not a tool in this toolset")
			continue
		}
		if _, isObject := val.(map[string]any); val != nil && !isObject {
			messages = append(messages, fmt.Sprintf("configValues[%q] for toolset %q must be an object or null.", key, toolsetName))
			details[toolsetName+"."+key] = append(details[toolsetName+"."+key], "config values must be an object")
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return &ValidationError{Message: strings.Join(messages, " "), Details: details}
}

func mergeToolsetBindingSecrets(ctx context.Context, agentID, packageID string, incoming map[string]any, prior map[string]any, repo *store.ToolPackageRepo) (map[string]any, error) {
	if len(incoming) == 0 {
		return incoming, nil
	}
	files, err := repo.GetFilesForPackage(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf("load tool files for secret merge: %w", err)
	}
	schemaByTool := loadConfigSchemaByTool(files)

	out := make(map[string]any, len(incoming))
	for toolName, toolValsRaw := range incoming {
		if toolName == reservedAuthConfigKey {
			out[toolName] = mergeDeployerAuthBlock(toolValsRaw, prior[reservedAuthConfigKey])
			continue
		}
		toolVals, ok := toolValsRaw.(map[string]any)
		if !ok {
			out[toolName] = toolValsRaw
			continue
		}
		priorVals, _ := prior[toolName].(map[string]any)
		merged, err := mergeToolSecretFields(toolVals, priorVals, toolsetSecretFields(schemaByTool[toolName]))
		if err != nil {
			return nil, err
		}
		out[toolName] = merged
	}
	return out, nil
}

func mergeBuiltinToolsetBindingSecrets(toolsetName string, incoming map[string]any, prior map[string]map[string]any) (map[string]any, error) {
	if len(incoming) == 0 {
		return incoming, nil
	}
	out := make(map[string]any, len(incoming))
	for toolName, toolValsRaw := range incoming {
		toolVals, ok := toolValsRaw.(map[string]any)
		if !ok {
			out[toolName] = toolValsRaw
			continue
		}
		merged, err := mergeToolSecretFields(toolVals, priorBuiltinToolValues(prior, toolsetName, toolName), builtinToolSecretFields(toolName))
		if err != nil {
			return nil, err
		}
		out[toolName] = merged
	}
	return out, nil
}

func mergeToolSecretFields(toolVals, priorVals map[string]any, secretFields map[string]secretFieldInfo) (map[string]any, error) {
	if len(secretFields) == 0 {
		return toolVals, nil
	}
	working := make(map[string]any, len(toolVals))
	maps.Copy(working, toolVals)
	if _, err := mergeSecretFields(working, priorVals, secretFields); err != nil {
		return nil, err
	}
	dropUnresolvedSentinels(working, secretFields)
	return working, nil
}

func priorBuiltinToolValues(prior map[string]map[string]any, toolsetName, toolName string) map[string]any {
	if vals, ok := prior[toolsetName][toolName].(map[string]any); ok {
		return vals
	}
	for _, other := range slices.Sorted(maps.Keys(prior)) {
		if vals, ok := prior[other][toolName].(map[string]any); ok {
			return vals
		}
	}
	return nil
}

func priorBuiltinToolsetConfigValues(ctx context.Context, agentID string, repo *store.ToolPackageRepo) (map[string]map[string]any, error) {
	rows, err := repo.GetBuiltinToolsetConfigsForAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("load stored builtin toolset config values: %w", err)
	}
	out := make(map[string]map[string]any, len(rows))
	for _, r := range rows {
		if vals := parseConfigValues(r.ConfigValues); vals != nil {
			out[r.ToolsetName] = vals
		}
	}
	return out, nil
}

func parseConfigValues(raw *string) map[string]any {
	if raw == nil || *raw == "" {
		return nil
	}
	var vals map[string]any
	if json.Unmarshal([]byte(*raw), &vals) != nil {
		return nil
	}
	return vals
}

func redactBuiltinToolsetSecrets(vals map[string]any) map[string]any {
	result := make(map[string]any, len(vals))
	for toolName, toolValsRaw := range vals {
		toolVals, ok := toolValsRaw.(map[string]any)
		if !ok {
			result[toolName] = toolValsRaw
			continue
		}
		result[toolName] = redactSecretFields(toolVals, builtinToolSecretFields(toolName))
	}
	return result
}

func builtinToolsetConfigBinding(r store.AgentBuiltinToolsetConfig) ToolsetConfigBinding {
	b := ToolsetConfigBinding{ToolsetName: r.ToolsetName}
	if vals := parseConfigValues(r.ConfigValues); vals != nil {
		b.ConfigValues = redactBuiltinToolsetSecrets(vals)
	}
	b.VolumeBindings = unmarshalStringBindings(r.VolumeBindings)
	b.NetworkBindings = unmarshalStringBindings(r.NetworkBindings)
	return b
}

func marshalConfigValues(toolsetName string, values map[string]any) (*string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal config values for %q: %w", toolsetName, err)
	}
	s := string(raw)
	return &s, nil
}

func dropUnresolvedSentinels(values map[string]any, secretFields map[string]secretFieldInfo) {
	for key := range secretFields {
		if values[key] == redactedSentinel {
			delete(values, key)
		}
	}
}

func toolsetSecretFields(schema map[string]configSchemaField) map[string]secretFieldInfo {
	out := map[string]secretFieldInfo{}
	for key, field := range schema {
		if field.Secret {
			out[key] = secretFieldInfo{Required: field.isRequired()}
		}
	}
	return out
}

func priorToolsetConfigValuesByPackage(ctx context.Context, agentID string, repo *store.ToolPackageRepo) (map[string]map[string]any, error) {
	rows, err := repo.GetToolsetConfigsForAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("load stored toolset config values: %w", err)
	}
	out := make(map[string]map[string]any, len(rows))
	for _, r := range rows {
		if r.ConfigValues == nil || *r.ConfigValues == "" {
			continue
		}
		var vals map[string]any
		if json.Unmarshal([]byte(*r.ConfigValues), &vals) == nil {
			out[r.PackageID] = vals
		}
	}
	return out, nil
}

func loadToolsetConfigBindings(ctx context.Context, agentID string, repo *store.ToolPackageRepo) ([]ToolsetConfigBinding, error) {
	if repo == nil || agentID == "" {
		return nil, nil
	}

	var result []ToolsetConfigBinding

	pkgRows, err := repo.GetToolsetConfigsForAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("load toolset configs for %s: %w", agentID, err)
	}
	if len(pkgRows) > 0 {
		pkgIDs := make([]string, 0, len(pkgRows))
		for _, r := range pkgRows {
			pkgIDs = append(pkgIDs, r.PackageID)
		}
		nameByID, lookupErr := repo.GetPackageNamesByIDs(ctx, pkgIDs)
		if lookupErr != nil {
			return nil, fmt.Errorf("load toolset package names for %s: %w", agentID, lookupErr)
		}
		filesMap, _ := repo.GetFilesForPackages(ctx, pkgIDs)
		for _, r := range pkgRows {
			name, ok := nameByID[r.PackageID]
			if !ok {
				continue
			}
			b := ToolsetConfigBinding{ToolsetName: name}
			if r.ConfigValues != nil && *r.ConfigValues != "" {
				var vals map[string]any
				if json.Unmarshal([]byte(*r.ConfigValues), &vals) == nil {
					b.ConfigValues = redactNestedSecrets(vals, filesMap[r.PackageID])
				}
			}
			result = append(result, b)
		}
	}

	builtinRows, err := repo.GetBuiltinToolsetConfigsForAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("load builtin toolset configs for %s: %w", agentID, err)
	}
	for _, r := range builtinRows {
		result = append(result, builtinToolsetConfigBinding(r))
	}

	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func unmarshalStringBindings(raw *string) map[string]string {
	if raw == nil || *raw == "" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(*raw), &m) != nil || len(m) == 0 {
		return nil
	}
	return m
}

func redactNestedSecrets(vals map[string]any, files []store.ToolPackageFile) map[string]any {
	schemaByTool := loadConfigSchemaByTool(files)
	result := make(map[string]any, len(vals))
	for toolName, toolValsRaw := range vals {
		if toolName == reservedAuthConfigKey {
			if sanitized := sanitizeDeployerAuthValue(toolValsRaw); sanitized != nil {
				result[toolName] = sanitized
			}
			continue
		}
		toolVals, ok := toolValsRaw.(map[string]any)
		if !ok {
			result[toolName] = toolValsRaw
			continue
		}
		schema, hasTool := schemaByTool[toolName]
		if !hasTool {
			result[toolName] = toolVals
			continue
		}
		secrets := make(map[string]secretFieldInfo)
		for key, entry := range schema {
			if entry.Secret {
				secrets[key] = secretFieldInfo{Required: entry.isRequired()}
			}
		}
		result[toolName] = redactSecretFields(toolVals, secrets)
	}
	return result
}

func batchLoadToolsetConfigBindings(ctx context.Context, agentIDs []string, repo *store.ToolPackageRepo) map[string][]ToolsetConfigBinding {
	if repo == nil || len(agentIDs) == 0 {
		return nil
	}

	result := make(map[string][]ToolsetConfigBinding, len(agentIDs))

	pkgRowsByAgent, err := repo.GetToolsetConfigsForAgents(ctx, agentIDs)
	if err == nil && len(pkgRowsByAgent) > 0 {
		allPkgIDs := make(map[string]struct{})
		for _, rows := range pkgRowsByAgent {
			for _, r := range rows {
				allPkgIDs[r.PackageID] = struct{}{}
			}
		}
		pkgIDSlice := make([]string, 0, len(allPkgIDs))
		for id := range allPkgIDs {
			pkgIDSlice = append(pkgIDSlice, id)
		}
		nameByID, lookupErr := repo.GetPackageNamesByIDs(ctx, pkgIDSlice)
		filesMap, _ := repo.GetFilesForPackages(ctx, pkgIDSlice)
		if lookupErr == nil {
			for agentID, rows := range pkgRowsByAgent {
				for _, r := range rows {
					name, ok := nameByID[r.PackageID]
					if !ok {
						continue
					}
					b := ToolsetConfigBinding{ToolsetName: name}
					if r.ConfigValues != nil && *r.ConfigValues != "" {
						var vals map[string]any
						if json.Unmarshal([]byte(*r.ConfigValues), &vals) == nil {
							b.ConfigValues = redactNestedSecrets(vals, filesMap[r.PackageID])
						}
					}
					result[agentID] = append(result[agentID], b)
				}
			}
		}
	}

	builtinRowsByAgent, err := repo.GetBuiltinToolsetConfigsForAgents(ctx, agentIDs)
	if err == nil {
		for agentID, rows := range builtinRowsByAgent {
			for _, r := range rows {
				result[agentID] = append(result[agentID], builtinToolsetConfigBinding(r))
			}
		}
	}

	return result
}

func loadAgentSkillIDs(ctx context.Context, agentID string, repo *store.SkillRepo) []string {
	if repo == nil || agentID == "" {
		return []string{}
	}
	ids, err := repo.GetSkillIDsForAgent(ctx, agentID)
	if err != nil || len(ids) == 0 {
		return []string{}
	}
	return ids
}

func connectorConfigFingerprints(ctx context.Context, repo *store.ConnectorRepo, connectorIDs []string) []string {
	fingerprints := make([]string, 0, len(connectorIDs))
	if repo == nil || len(connectorIDs) == 0 {
		return fingerprints
	}
	versions := make(map[string]int64, len(connectorIDs))
	if conns, err := repo.GetByIDs(ctx, connectorIDs); err == nil {
		for _, c := range conns {
			versions[c.ID] = c.UpdatedTime
		}
	}
	for _, id := range connectorIDs {
		fingerprints = append(fingerprints, fmt.Sprintf("%s:%d", id, versions[id]))
	}
	return fingerprints
}

func computeAgentConfigHash(a *store.Agent, toolsets, inputModes, outputModes, connectorFingerprints, skillIDs []string) string {
	sorted := func(s []string) []string {
		c := make([]string, len(s))
		copy(c, s)
		slices.Sort(c)
		return c
	}

	addCfg := canonicalizeAdditionalConfigurationsForHash(a.AdditionalConfigurations)

	data := map[string]any{
		"name":                     a.Name,
		"description":              derefStr(a.Description),
		"systemPrompt":             derefStr(a.SystemPrompt),
		"skills":                   derefStr(a.Skills),
		"skillRefs":                derefStr(a.SkillRefs),
		"skillIds":                 sorted(skillIDs),
		"additionalConfigurations": addCfg,
		"toolsets":                 sorted(toolsets),
		"inputModes":               sorted(inputModes),
		"outputModes":              sorted(outputModes),
		"connectors":               sorted(connectorFingerprints),
	}
	b, _ := json.Marshal(data)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *AgentService) ValidateModelProviders(ctx context.Context, providers []string) error {
	return s.validateModelProvider(ctx, firstModelProvider(providers))
}

func (s *AgentService) validateModelProvider(ctx context.Context, modelProvider string) error {
	if modelProvider == "" || s.modelConfigSvc == nil {
		return nil
	}
	mc, err := s.modelConfigSvc.repo.GetByAliasOrID(ctx, modelProvider)
	if err != nil {
		return fmt.Errorf("validate model provider: %w", err)
	}
	if !isConfigured(mc) {
		return &ValidationError{Message: fmt.Sprintf(
			"Model configuration '%s' is not configured. Configure it in the Models UI before assigning it to an agent.",
			modelProvider,
		), Field: fieldModelProvider}
	}
	if mc.ModelType == store.ModelTypeDecision {
		return &ValidationError{Message: fmt.Sprintf(
			"Model configuration '%s' is a decision model, which workflow decision nodes use; an agent needs a chat model.",
			modelProvider,
		), Field: fieldModelProvider}
	}
	return nil
}

func validateAgentBusinessRules(agentType, systemPrompt string) error {
	if agentType == AgentTypeStandard || agentType == "" {
		if systemPrompt == "" {
			return &ValidationError{Message: "system prompt is required for standard agents", Field: fieldSystemPrompt}
		}
	}
	return nil
}

func encodeExclusions(in map[string][]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for ts, tools := range in {
		if len(tools) == 0 {
			continue
		}
		b, err := json.Marshal(tools)
		if err != nil {
			return nil, fmt.Errorf("marshal excluded tools for %q: %w", ts, err)
		}
		out[ts] = string(b)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func decodeExclusions(in map[string]string) (map[string][]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string][]string, len(in))
	for ts, jsonStr := range in {
		var tools []string
		if err := json.Unmarshal([]byte(jsonStr), &tools); err != nil {
			return out, fmt.Errorf("decode excluded tools for toolset %q: %w", ts, err)
		}
		if len(tools) > 0 {
			out[ts] = tools
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

type ValidationError = platpkg.ValidationError

type NotFoundError = platpkg.NotFoundError

type ConflictError = platpkg.ConflictError

type ForbiddenError = platpkg.ForbiddenError

type ExternalServiceError = platpkg.ExternalServiceError

type UnavailableError = platpkg.UnavailableError

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strPtrOrEmpty(s string) *string {
	return &s
}

func strPtrOrDefault(s, def string) *string {
	if s == "" {
		return &def
	}
	return &s
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ensureSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func firstModelProvider(providers []string) string {
	if len(providers) > 0 {
		return providers[0]
	}
	return ""
}

func modelProviderToSlice(s *string) []string {
	if s == nil || *s == "" {
		return []string{}
	}
	return []string{*s}
}
