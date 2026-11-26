// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func (s *Service) Apply(ctx context.Context, req ApplyBuildRequest, identity *auth.Subject) (*ApplyBuildResponse, error) {
	plans, reg, err := s.Plan(ctx, req, identity)
	if err != nil {
		return nil, err
	}
	if err := authorizePlan(identity, plans); err != nil {
		return nil, err
	}
	return s.applyRequest(ctx, req, identity, plans, reg, httputil.SurfaceAPI), nil
}

func (s *Service) applyRequest(ctx context.Context, req ApplyBuildRequest, identity *auth.Subject, plans []kindPlan, reg *xrefRegistry, submittedFrom string) *ApplyBuildResponse {
	dropped, incomplete := s.droppedFromPlan(ctx, req, identity, plans)
	resp := s.ApplyPlanned(ctx, plans, reg, req.DryRun, actorFrom(identity), submittedFrom)
	resp.Dropped, resp.DroppedCheckFailed = dropped, incomplete
	if !req.DryRun && !s.recordWritten(ctx, req.BuilderSessionID, actorFrom(identity), resp.Operations) {
		warnUnrecorded(resp.Operations)
	}
	return resp
}

func warnUnrecorded(operations []BuildOperationResult) {
	for i, op := range operations {
		if op.Status == statusSucceeded && isDroppableKind(op.Kind) && op.ResourceID != "" && op.Action != actionUnchanged {
			operations[i].Warnings = append(operations[i].Warnings, "the platform could not record that this Builder session wrote it, so a later plan that drops it will not be reported or offered for removal")
		}
	}
}

func (s *Service) ApplyPlanned(ctx context.Context, plans []kindPlan, reg *xrefRegistry, dryRun bool, actor, submittedFrom string) *ApplyBuildResponse {
	resp := &ApplyBuildResponse{DryRun: dryRun, Operations: []BuildOperationResult{}, Dropped: []DroppedResource{}}
	pre := s.prevalidate(ctx, plans, dryRun)
	checked := pre.rows
	if dryRun {
		resp.Operations = pre.withSaveWarnings(plans)
		resp.Summary = summarize(resp.Operations)
		return resp
	}
	if pre.refused {
		resp.Operations = refusedBeforeWriting(plans, pre)
		resp.Summary = summarize(resp.Operations)
		return resp
	}

	for _, kp := range plans {
		for _, row := range kp.operations(statusPlanned) {
			reg.put(xrefKindFor(kp.componentKind), row.Name, row.ResourceID)
			reg.put(xrefKindFor(kp.componentKind), row.ResolvedName, row.ResourceID)
		}
	}

	stopped := false
	for i, kp := range plans {
		for j, row := range checked[i] {
			if row.Action == actionUnchanged {
				row.Status = statusSucceeded
				resp.Operations = append(resp.Operations, row)
				continue
			}
			if stopped {
				row.Status = statusSkipped
				resp.Operations = append(resp.Operations, row)
				continue
			}

			id, warnings, execErr := s.execute(ctx, kp, row, reg, actor, submittedFrom, pre.reads[i][j])
			if execErr != nil {
				row.Status = statusFailed
				row.Error = s.operationError(execErr, row, false)
				stopped = true
			} else {
				row.Status = statusSucceeded
				row.ResourceID = id
				d, _ := desiredFor(kp, row)
				row.Warnings = withSaveWarnings(row.Warnings, warnings, plannedConnectorHIL(d))
				reg.put(xrefKindFor(kp.componentKind), row.Name, id)
				reg.put(xrefKindFor(kp.componentKind), row.ResolvedName, id)
				s.logger.Debug("builder apply operation applied",
					"resourceKind", row.Kind, "resourceName", row.Name, "action", row.Action)
			}
			resp.Operations = append(resp.Operations, row)
		}
	}

	resp.Summary = summarize(resp.Operations)
	return resp
}

func (s *Service) validateForExecute(create any) error {
	if agent, ok := create.(entplatform.CreateAgentRequest); ok {
		return entplatform.ValidateAgentRequest(agent, agent.SystemPrompt, s.instructionMinChars, s.instructionMaxChars)
	}
	return entplatform.ValidateRequest(create)
}

type prevalidated struct {
	rows         [][]BuildOperationResult
	saveWarnings [][][]string
	reads        [][]connectorSpecRead
	refused      bool
}

type connectorSpecRead struct {
	create *entplatform.URLSpecRead
	update *entplatform.UpdateRead
}

func (p prevalidated) withSaveWarnings(plans []kindPlan) []BuildOperationResult {
	out := []BuildOperationResult{}
	for i, rows := range p.rows {
		for j, row := range rows {
			d, _ := desiredFor(plans[i], row)
			row.Warnings = withSaveWarnings(row.Warnings, p.saveWarnings[i][j], plannedConnectorHIL(d))
			out = append(out, row)
		}
	}
	return out
}

func (s *Service) prevalidate(ctx context.Context, plans []kindPlan, dryRun bool) prevalidated {
	claimed := claimedAgentNames{}
	planned := plannedConnectorsOf(plans)
	checks := s.deps.Workflows.NewPlannedChecks()
	pre := prevalidated{
		rows:         make([][]BuildOperationResult, len(plans)),
		saveWarnings: make([][][]string, len(plans)),
		reads:        make([][]connectorSpecRead, len(plans)),
	}
	for i, kp := range plans {
		rows := kp.operations(statusPlanned)
		pre.saveWarnings[i] = make([][]string, len(rows))
		pre.reads[i] = make([]connectorSpecRead, len(rows))
		for j, row := range rows {
			if row.Action == actionUnchanged {
				rows[j].Warnings = append(rows[j].Warnings, s.regradeUnchanged(ctx, kp, row, planned, checks)...)
				continue
			}
			saveWarnings, read, warnings, err := s.checkBeforeWrite(ctx, kp, row, claimed, planned, checks)
			pre.saveWarnings[i][j], pre.reads[i][j] = saveWarnings, read
			rows[j].Warnings = append(rows[j].Warnings, warnings...)
			if err != nil {
				rows[j].Status = statusFailed
				rows[j].Error = s.operationError(err, row, dryRun)
				pre.refused = true
			}
		}
		pre.rows[i] = rows
	}
	return pre
}

func (s *Service) checkBeforeWrite(ctx context.Context, kp kindPlan, row BuildOperationResult, claimed claimedAgentNames, planned plannedConnectors, checks *entplatform.PlannedConnectorChecks) (saveWarnings []string, read connectorSpecRead, warnings []string, err error) {
	d, err := desiredFor(kp, row)
	if err != nil {
		return nil, read, nil, err
	}
	err = s.rejectInvalid(d, row.Action)
	if err == nil {
		saveWarnings, read, err = s.rejectInvalidAgainstStored(ctx, d, row)
	}
	if err == nil {
		err = s.rejectUnconfiguredModel(ctx, d, row)
	}
	if err == nil {
		err = s.rejectNameConflict(ctx, d, row, claimed)
	}
	warnings, undeployableErr := s.rejectUndeployable(ctx, d, row, planned, checks)
	return saveWarnings, read, warnings, mergeValidationErrors(err, undeployableErr)
}

func refusedBeforeWriting(plans []kindPlan, pre prevalidated) []BuildOperationResult {
	out := pre.withSaveWarnings(plans)
	for i, row := range out {
		switch {
		case row.Status == statusFailed:
		case row.Action == actionUnchanged:
			out[i].Status = statusSucceeded
		default:
			out[i].Status = statusSkipped
		}
	}
	return out
}

func desiredFor(kp kindPlan, row BuildOperationResult) (desiredComponent, error) {
	d, ok := kp.desiredByName[row.ResolvedName]
	if !ok {
		return desiredComponent{}, fmt.Errorf("no converted config for %s %q", row.Kind, row.Name)
	}
	return d, nil
}

func (s *Service) rejectInvalid(d desiredComponent, action string) error {
	if err := s.validateForExecute(d.create); err != nil {
		return err
	}
	var writeErr error
	if action == actionCreate {
		writeErr = s.rejectInvalidCreate(d.create)
	} else {
		writeErr = rejectInvalidUpdate(d)
	}
	return mergeValidationErrors(writeErr, rejectInvalidBuiltinToolsetConfigs(d), rejectUndeclaredConnectorKeys(d))
}

func rejectInvalidBuiltinToolsetConfigs(d desiredComponent) error {
	create, isAgent := d.create.(entplatform.CreateAgentRequest)
	if !isAgent {
		return nil
	}
	return entplatform.ValidateBuiltinToolsetConfigBindings(create.ToolsetConfigs, create.AdditionalConfigurations)
}

func (s *Service) rejectHiddenConnectorTypeBecauseHandlerDoesNotRecheck(create entplatform.CreateConnectorRequest) error {
	if entplatform.ConnectorTypeHidden(s.features, create.Type) {
		return &entplatform.ValidationError{
			Message: create.Type + " connector is not enabled on this deployment",
			Field:   "type",
		}
	}
	return nil
}

func (s *Service) rejectInvalidCreate(desired any) error {
	switch create := desired.(type) {
	case entplatform.CreateConnectorRequest:
		if err := s.rejectHiddenConnectorTypeBecauseHandlerDoesNotRecheck(create); err != nil {
			return err
		}
		return entplatform.ValidateConnectorCreate(create)
	case entplatform.CreateRemoteAgentRequest:
		return entplatform.ValidateRemoteAgentCreate(defaultA2AAuthForCreate(create))
	case entplatform.CreateAgentRequest:
		if err := entplatform.ValidateAgentCreate(create); err != nil {
			return err
		}
		if len(create.AdditionalConfigurations) == 0 {
			return nil
		}
		_, err := checkExtraConfigBounds(create.AdditionalConfigurations, nil)
		return namedValidationError(create.Name, err)
	case entplatform.CreateWorkflowRequest:
		return entplatform.ValidateWorkflowRequest(create.Name, create.AppConfig)
	case entplatform.CreateGatewayRequest:
		return entplatform.ValidateGatewayCreate(create, s.features)
	}
	return nil
}

func rejectInvalidUpdate(d desiredComponent) error {
	switch create := d.create.(type) {
	case entplatform.CreateConnectorRequest:
		return entplatform.ValidateConnectorValuesShape(create.Values)
	case entplatform.CreateRemoteAgentRequest:
		if err := entplatform.ValidateRemoteAgentUpdate(entplatform.UpdateRemoteAgentRequest(create)); err != nil {
			return err
		}
		if err := entplatform.ValidateRemoteAgentCardSkills(create.AgentCardLocation, create.Skills != nil); err != nil {
			return err
		}
		return entplatform.ValidateRemoteAgentCardTrust(create.AgentCardLocation, create.RequireSignedCard != nil && *create.RequireSignedCard, create.TrustedCardJWKSURLs)
	case entplatform.CreateAgentRequest:
		return entplatform.ValidateAgentUpdate(entplatform.AgentTypeStandard, updateAgentRequestFrom(create, false))
	case entplatform.CreateGatewayRequest:
		return rejectInvalidGatewayUpdate(create)
	case entplatform.CreateWorkflowRequest:
		return entplatform.ValidateWorkflowRequest(create.Name, create.AppConfig)
	}
	return nil
}

func (s *Service) rejectInvalidAgainstStored(ctx context.Context, d desiredComponent, row BuildOperationResult) ([]string, connectorSpecRead, error) {
	var (
		warnings []string
		read     connectorSpecRead
		err      error
	)
	switch create := d.create.(type) {
	case entplatform.CreateConnectorRequest:
		warnings, read, err = s.validateConnectorAgainstState(ctx, create, row)
	case entplatform.CreateGatewayRequest:
		if row.Action != actionUpdate {
			return nil, read, nil
		}
		err = s.deps.Gateways.ValidateUpdate(ctx, row.ResourceID, updateGatewayRequestFrom(create))
	default:
		return nil, read, nil
	}
	if _, rejected := errors.AsType[*entplatform.ValidationError](err); rejected {
		return nil, connectorSpecRead{}, err
	}
	if err != nil {
		class, interruption := entplatform.ClassifyReadFailure(err)
		level := slog.LevelWarn
		if interruption {
			level = slog.LevelDebug
		}
		s.logger.Log(ctx, level, "builder preview could not read stored state to validate an update",
			"resourceKind", row.Kind, "resourceName", row.Name, "failureClass", class)
		return warnings, connectorSpecRead{}, nil
	}
	return warnings, read, nil
}

func (s *Service) validateConnectorAgainstState(ctx context.Context, create entplatform.CreateConnectorRequest, row BuildOperationResult) ([]string, connectorSpecRead, error) {
	switch row.Action {
	case actionUpdate:
		warnings, read, err := s.deps.Connectors.ValidateUpdateReading(ctx, row.ResourceID, updateConnectorRequestFrom(create))
		return warnings, connectorSpecRead{update: &read}, err
	case actionCreate:
		warnings, read, err := s.deps.Connectors.ValidateCreateReading(ctx, create)
		return warnings, connectorSpecRead{create: read}, err
	}
	return nil, connectorSpecRead{}, nil
}

func (s *Service) rejectUnconfiguredModel(ctx context.Context, d desiredComponent, row BuildOperationResult) error {
	create, isAgent := d.create.(entplatform.CreateAgentRequest)
	if !isAgent {
		return nil
	}
	err := s.deps.Agents.ValidateModelProviders(ctx, create.ModelProvider)
	if _, rejected := errors.AsType[*entplatform.ValidationError](err); rejected {
		return err
	}
	if err != nil {
		s.logger.Warn("builder preview could not read model configurations to validate a model binding",
			"resourceKind", row.Kind, "resourceName", row.Name, "error", err)
	}
	return nil
}

type claimedAgentNames map[string]struct{}

func (s *Service) rejectNameConflict(ctx context.Context, d desiredComponent, row BuildOperationResult, claimed claimedAgentNames) error {
	var err error
	switch create := d.create.(type) {
	case entplatform.CreateRemoteAgentRequest:
		if row.Action == actionCreate {
			err = s.deps.A2AAgents.CheckNameAvailable(ctx, create.Name, "")
		} else {
			err = s.deps.A2AAgents.CheckUpdatedName(ctx, create.Name, row.ResourceID)
		}
		if _, conflicted := errors.AsType[*entplatform.ConflictError](err); !conflicted {
			claimed[normalizedResourceName(create.Name)] = struct{}{}
		}
	case entplatform.CreateAgentRequest:
		if _, taken := claimed[normalizedResourceName(create.Name)]; taken {
			return entplatform.NewAgentNameConflict(create.Name)
		}
		err = s.deps.Agents.CheckNameAvailable(ctx, create.Name, row.ResourceID)
	default:
		return nil
	}
	if _, conflicted := errors.AsType[*entplatform.ConflictError](err); conflicted {
		return err
	}
	if err != nil {
		s.logger.Warn("builder preview could not read stored state to check a name",
			"resourceKind", row.Kind, "resourceName", row.Name, "error", err)
	}
	return nil
}

func rejectInvalidGatewayUpdate(create entplatform.CreateGatewayRequest) error {
	values, isObject := create.Values.(map[string]any)
	if !isObject || create.Type != "event_mesh" {
		return nil
	}
	return entplatform.ValidateGatewayEventRules(values)
}

func xrefKindFor(componentKind string) string {
	if componentKind == kindConnector {
		return "connector"
	}
	return componentKind
}

func (s *Service) execute(ctx context.Context, kp kindPlan, row BuildOperationResult, reg *xrefRegistry, actor, submittedFrom string, read connectorSpecRead) (string, []string, error) {
	d, err := desiredFor(kp, row)
	if err != nil {
		return "", nil, err
	}

	switch kp.componentKind {
	case kindConnector:
		create := d.create.(entplatform.CreateConnectorRequest)
		if row.Action == actionCreate {
			resp, err := s.deps.Connectors.CreateWithSpecRead(ctx, create, actor, read.create)
			if err != nil {
				return "", nil, err
			}
			s.analytics.ConnectorCreated(ctx, resp.Type)
			return resp.ID, resp.Warnings, nil
		}
		resp, err := entplatform.UpdateConnectorWithRedeploy(ctx, s.deps.DB, s.deps.Connectors, entplatform.ConnectorRedeployUpdate{
			ID:      row.ResourceID,
			Request: updateConnectorRequestFrom(create),
			UserID:  actor,
			Read:    read.update,
		}, s.logger)
		if err != nil {
			return "", nil, err
		}
		return row.ResourceID, resp.Warnings, nil

	case kindA2AAgent:
		create := d.create.(entplatform.CreateRemoteAgentRequest)
		if row.Action == actionCreate {
			resp, err := s.deps.A2AAgents.Create(ctx, defaultA2AAuthForCreate(create), actor)
			if err != nil {
				return "", nil, err
			}
			s.analytics.RemoteAgentCreated(ctx, submittedFrom)
			return resp.ID, nil, nil
		}
		_, err := s.deps.A2AAgents.Update(ctx, row.ResourceID, entplatform.UpdateRemoteAgentRequest(create), actor)
		return row.ResourceID, nil, err

	case kindAgent:
		create := d.create.(entplatform.CreateAgentRequest)
		if err := resolveAgentSentinels(&create, reg); err != nil {
			return "", nil, err
		}
		if row.Action == actionCreate {
			resp, err := s.deps.Agents.Create(ctx, create, actor)
			if err != nil {
				return "", nil, err
			}
			s.analytics.AgentCreated(ctx, len(resp.Toolsets), submittedFrom)
			return resp.ID, nil, nil
		}
		_, err := s.deps.Agents.Update(ctx, row.ResourceID, updateAgentRequestFrom(create, d.authoredSkillRefs()), actor)
		return row.ResourceID, nil, err

	case kindWorkflow:
		create := d.create.(entplatform.CreateWorkflowRequest)
		if row.Action == actionCreate {
			resp, err := s.deps.Workflows.Create(ctx, create, actor)
			if err != nil {
				return "", nil, err
			}
			s.analytics.WorkflowCreated(ctx)
			return resp.ID, nil, nil
		}
		_, err := s.deps.Workflows.Update(ctx, row.ResourceID, entplatform.UpdateWorkflowRequest(create), actor)
		return row.ResourceID, nil, err

	case kindGateway:
		create := d.create.(entplatform.CreateGatewayRequest)
		if row.Action == actionCreate {
			resp, err := s.deps.Gateways.Create(ctx, create, actor)
			if err != nil {
				return "", nil, err
			}
			s.analytics.EntrypointCreated(ctx, resp.Type)
			return resp.ID, nil, nil
		}
		_, err := s.deps.Gateways.Update(ctx, row.ResourceID, updateGatewayRequestFrom(create), actor)
		return row.ResourceID, nil, err
	}
	return "", nil, fmt.Errorf("no executor for component kind %q", kp.componentKind)
}

func updateAgentRequestFrom(create entplatform.CreateAgentRequest, authoredSkillRefs bool) entplatform.UpdateAgentRequest {
	var skillIDs *[]string
	if authoredSkillRefs {
		refs := create.SkillIDs
		skillIDs = &refs
	}
	return entplatform.UpdateAgentRequest{
		Name:                     create.Name,
		Description:              create.Description,
		SystemPrompt:             create.SystemPrompt,
		Skills:                   create.Skills,
		SkillRefs:                create.SkillRefs,
		SkillIDs:                 skillIDs,
		Toolsets:                 create.Toolsets,
		ToolsetExcludedTools:     create.ToolsetExcludedTools,
		ToolsetConfigs:           create.ToolsetConfigs,
		SkillConfigs:             create.SkillConfigs,
		Connectors:               create.Connectors,
		InputModes:               create.InputModes,
		OutputModes:              create.OutputModes,
		ModelProvider:            create.ModelProvider,
		AdditionalConfigurations: create.AdditionalConfigurations,
	}
}

func updateConnectorRequestFrom(create entplatform.CreateConnectorRequest) entplatform.UpdateConnectorRequest {
	values, isObject := create.Values.(map[string]any)
	if !isObject {
		return entplatform.UpdateConnectorRequest{
			Name:        create.Name,
			Description: create.Description,
			Values:      create.Values,
		}
	}
	return entplatform.UpdateConnectorRequest{
		Name:        create.Name,
		Description: create.Description,
		Values:      stripPlaceholderSecrets(entplatform.ConnectorSecretFieldNames(create.Type, create.Subtype), values),
	}
}

func updateGatewayRequestFrom(create entplatform.CreateGatewayRequest) entplatform.UpdateGatewayRequest {
	values, isObject := create.Values.(map[string]any)
	if !isObject {
		return entplatform.UpdateGatewayRequest{
			Name:        create.Name,
			Description: create.Description,
			Values:      create.Values,
		}
	}
	return entplatform.UpdateGatewayRequest{
		Name:        create.Name,
		Description: create.Description,
		Values:      stripPlaceholderSecrets(entplatform.GatewaySecretFieldNames(create.Type), values),
	}
}

func (s *Service) operationError(err error, row BuildOperationResult, dryRun bool) *OperationError {
	out := &OperationError{ErrorID: idgen.New()}

	if ve, ok := errors.AsType[*entplatform.ValidationError](err); ok {
		out.Message, out.ValidationDetails = ve.Message, ve.Details
		if ve.Message == entplatform.InvalidAgentSkillConfigMessage {
			out.Message = ve.Message + ": these values were already stored on this agent and are preserved, not written, by this save. " +
				"Changing which skills are attached resubmits them, and the skill's current configuration no longer accepts them. " +
				"Correct or clear them on the agent's page, then save again"
		}
	} else {
		out.Message = clientSafeMessage(err)
	}
	out.HTTPStatus = clientStatus(err)

	s.logger.Warn("builder apply operation failed",
		"resourceKind", row.Kind,
		"resourceName", row.Name,
		"action", row.Action,
		"dryRun", dryRun,
		"errorId", out.ErrorID,
		"error", err,
		"cause", errors.Unwrap(err))
	return out
}

func clientStatus(err error) int {
	if _, ok := errors.AsType[*entplatform.ValidationError](err); ok {
		return http.StatusUnprocessableEntity
	}
	if _, ok := errors.AsType[*entplatform.NotFoundError](err); ok {
		return http.StatusNotFound
	}
	if _, ok := errors.AsType[*entplatform.ConflictError](err); ok {
		return http.StatusConflict
	}
	if _, ok := errors.AsType[*entplatform.ForbiddenError](err); ok {
		return http.StatusForbidden
	}
	if _, ok := errors.AsType[*entplatform.ExternalServiceError](err); ok {
		return http.StatusBadGateway
	}
	if _, ok := errors.AsType[*entplatform.UnavailableError](err); ok {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

func clientSafeMessage(err error) string {
	if e, ok := errors.AsType[*entplatform.NotFoundError](err); ok {
		return e.Error()
	}
	if e, ok := errors.AsType[*entplatform.ConflictError](err); ok {
		return e.Message
	}
	if e, ok := errors.AsType[*entplatform.ForbiddenError](err); ok {
		return e.Message
	}
	if e, ok := errors.AsType[*entplatform.ExternalServiceError](err); ok {
		return e.Message
	}
	if e, ok := errors.AsType[*entplatform.UnavailableError](err); ok {
		return e.Message
	}
	return "internal error"
}

func summarize(ops []BuildOperationResult) BuildApplySummary {
	var s BuildApplySummary
	for _, op := range ops {
		switch op.Status {
		case statusFailed:
			s.Failed++
			continue
		case statusSkipped:
			s.Skipped++
			continue
		}
		switch op.Action {
		case actionCreate:
			s.Created++
		case actionUpdate:
			s.Updated++
		case actionUnchanged:
			s.Unchanged++
		}
	}
	return s
}
