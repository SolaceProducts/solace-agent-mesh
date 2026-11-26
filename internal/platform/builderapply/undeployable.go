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
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

const decisionNodesOffWarning = "this workflow has decision nodes, but workflow decision nodes are turned off on this platform (feature workflow_decision_node), so a run fails when it reaches one"

const toolNodeChecksSkippedFormat = "tool-node checks were skipped: the workflow body did not parse (%v)"

const unchangedWorkflowWouldFailFormat = "this workflow was not changed by this build, but with the connector changes in it, it will fail when it is next deployed (a deployed workflow is redeployed with the connector): %s"

const connectorNodeChecksPermissiveHint = "; an operator can set SAM_FEATURE_BUILDER_CONNECTOR_NODE_CHECKS_PERMISSIVE=true to downgrade this to a warning"

const connectorInputsUncheckedFormat = "the inputs of tool nodes that call connector %q could not be checked: it has no stored specification"

type plannedConnectors map[string]entplatform.PlannedConnector

func plannedConnectorsOf(plans []kindPlan) plannedConnectors {
	out := plannedConnectors{}
	for _, kp := range plans {
		if kp.componentKind != kindConnector {
			continue
		}
		for _, row := range kp.operations(statusPlanned) {
			if row.Action == actionUnchanged {
				continue
			}
			d, err := desiredFor(kp, row)
			if err != nil {
				continue
			}
			create, isConnector := d.create.(entplatform.CreateConnectorRequest)
			if !isConnector {
				continue
			}
			values, _ := create.Values.(map[string]any)
			p := entplatform.PlannedConnector{Name: create.Name, Type: create.Type, Subtype: create.Subtype, Values: values}
			if row.Action == actionUpdate {
				p.ID = row.ResourceID
			}
			out[row.ResolvedName] = p
		}
	}
	return out
}

func (s *Service) rejectUndeployable(ctx context.Context, d desiredComponent, row BuildOperationResult, planned plannedConnectors, checks *entplatform.PlannedConnectorChecks) (warnings []string, err error) {
	create, isWorkflow := d.create.(entplatform.CreateWorkflowRequest)
	if !isWorkflow {
		return nil, nil
	}
	warnings = s.decisionNodeWarnings(create.AppConfig)
	workflowID := ""
	if row.Action == actionUpdate {
		workflowID = row.ResourceID
	}
	connectorWarnings, connectorErr := s.rejectUnrunnableConnectorNodes(ctx, workflowID, create, planned, checks)
	warnings = append(warnings, connectorWarnings...)
	toolWarnings, toolErr := s.rejectBadToolNodeArguments(create)
	warnings = append(warnings, toolWarnings...)
	return warnings, mergeValidationErrors(s.rejectUnusableDecisionModel(ctx, create, row), connectorErr, toolErr)
}

func (s *Service) regradeUnchanged(ctx context.Context, kp kindPlan, row BuildOperationResult, planned plannedConnectors, checks *entplatform.PlannedConnectorChecks) (warnings []string) {
	if kp.componentKind != kindWorkflow || len(planned) == 0 {
		return nil
	}
	d, err := desiredFor(kp, row)
	if err != nil {
		return nil
	}
	create, isWorkflow := d.create.(entplatform.CreateWorkflowRequest)
	if !isWorkflow || !checks.UsesPlannedConnector(ctx, row.ResourceID, create.AppConfig, planned) {
		return nil
	}
	writeRefusal, deployRefusal, warnings, inputRefusal := gradeConnectorNodes(ctx, row.ResourceID, create, planned, checks)
	for _, refusal := range []error{writeRefusal, deployRefusal, inputRefusal} {
		if ve, refused := errors.AsType[*entplatform.ValidationError](refusal); refused {
			warnings = append(warnings, fmt.Sprintf(unchangedWorkflowWouldFailFormat, ve.Message))
		}
	}
	return warnings
}

func (s *Service) rejectBadToolNodeArguments(create entplatform.CreateWorkflowRequest) (warnings []string, err error) {
	opts := workflow.StaticCheckOptions{
		Permissive: s.features != nil && s.features.IsEnabled(features.KeyWorkflowToolValidationPermissive),
	}
	problems, parseErr := workflow.ToolNodeArgumentProblems(create.Name, create.AppConfig, opts)
	if errors.Is(parseErr, workflow.ErrEscalationChoiceRemoved) {
		return nil, &entplatform.ValidationError{Message: parseErr.Error(), Field: "appConfig"}
	}
	if parseErr != nil {
		return []string{fmt.Sprintf(toolNodeChecksSkippedFormat, parseErr)}, nil
	}
	return splitProblems(nil, problems,
		func(p workflow.StaticProblem) string { return p.Message },
		func(p workflow.StaticProblem) bool { return p.Fatal })
}

func (s *Service) rejectUnrunnableConnectorNodes(ctx context.Context, workflowID string, create entplatform.CreateWorkflowRequest, planned plannedConnectors, checks *entplatform.PlannedConnectorChecks) (warnings []string, err error) {
	writeRefusal, deployRefusal, warnings, inputRefusal := gradeConnectorNodes(ctx, workflowID, create, planned, checks)
	if ve, refused := errors.AsType[*entplatform.ValidationError](deployRefusal); refused {
		if s.connectorNodeChecksPermissive() {
			warnings = append(warnings, ve.Message)
			deployRefusal = nil
		} else {
			deployRefusal = &entplatform.ValidationError{Message: ve.Message + connectorNodeChecksPermissiveHint, Field: ve.Field, Details: ve.Details}
		}
	}
	return warnings, mergeValidationErrors(writeRefusal, deployRefusal, inputRefusal)
}

func gradeConnectorNodes(ctx context.Context, workflowID string, create entplatform.CreateWorkflowRequest, planned plannedConnectors, checks *entplatform.PlannedConnectorChecks) (writeRefusal, deployRefusal error, warnings []string, inputRefusal error) {
	writeRefusal, deployRefusal = checks.RejectUndeployableConnectorNodes(ctx, workflowID, create.AppConfig, planned)
	report := checks.CheckToolInputs(ctx, workflowID, create.AppConfig, planned)
	for _, u := range report.Unchecked {
		if entplatform.InputsNotCheckable(u) {
			warnings = append(warnings, fmt.Sprintf(connectorInputsUncheckedFormat, u.Connector))
		}
	}
	warnings, inputRefusal = splitProblems(warnings, report.Problems,
		func(p entplatform.ConnectorToolInputProblem) string { return p.Message },
		func(p entplatform.ConnectorToolInputProblem) bool { return p.Fatal })
	return writeRefusal, deployRefusal, warnings, inputRefusal
}

func (s *Service) connectorNodeChecksPermissive() bool {
	return s.features != nil && s.features.IsEnabled(features.KeyBuilderConnectorNodeChecksPermissive)
}

func splitProblems[P any](warnings []string, problems []P, message func(P) string, fatal func(P) bool) ([]string, error) {
	var refusals []string
	for _, p := range problems {
		if fatal(p) {
			refusals = append(refusals, message(p))
		} else {
			warnings = append(warnings, message(p))
		}
	}
	if len(refusals) == 0 {
		return warnings, nil
	}
	return nil, &entplatform.ValidationError{Message: strings.Join(refusals, "; "), Field: "appConfig"}
}

func (s *Service) decisionNodeWarnings(appConfig map[string]any) []string {
	if s.features == nil || s.features.IsEnabled(features.KeyWorkflowDecisionNode) || !entplatform.WorkflowUsesDecisionNodes(appConfig) {
		return nil
	}
	return []string{decisionNodesOffWarning}
}

func (s *Service) rejectUnusableDecisionModel(ctx context.Context, create entplatform.CreateWorkflowRequest, row BuildOperationResult) error {
	err := s.deps.Workflows.ValidateDecisionModel(ctx, create.AppConfig)
	if _, rejected := errors.AsType[*entplatform.ValidationError](err); rejected {
		return err
	}
	if err != nil {
		class, interruption := entplatform.ClassifyReadFailure(err)
		level := slog.LevelWarn
		if interruption {
			level = slog.LevelDebug
		}
		s.logger.Log(ctx, level, "builder could not read model configurations to validate a decision model",
			"resourceKind", row.Kind, "resourceName", row.Name, "failureClass", class)
	}
	return nil
}
