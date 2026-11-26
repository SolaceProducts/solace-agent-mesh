// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package platform

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const resourceReportTimeout = 2 * time.Second

type ResourceReporter struct {
	analytics *productanalytics.Service
	deploys   DeployHistory
	logger    *slog.Logger
	slots     chan struct{}
}

type DeployHistory interface {
	IsFirstAgentDeploy(ctx context.Context, deploymentID string) (bool, error)
	IsFirstWorkflowDeploy(ctx context.Context, deploymentID string) (bool, error)
}

func NewResourceReporter(analytics *productanalytics.Service, deploys DeployHistory, logger *slog.Logger) *ResourceReporter {
	if analytics == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ResourceReporter{analytics: analytics, deploys: deploys, logger: logger, slots: make(chan struct{}, resourceReportConcurrency)}
}

func (r *ResourceReporter) AgentCreated(ctx context.Context, toolsetCount int, submittedFrom string) {
	r.report(ctx, productanalytics.EventAgentCreated,
		agentCreatedProperties(productanalytics.AgentTypeUserCreated, toolsetCount, submittedFrom))
}

func (r *ResourceReporter) RemoteAgentCreated(ctx context.Context, submittedFrom string) {
	r.report(ctx, productanalytics.EventAgentCreated,
		agentCreatedProperties(productanalytics.AgentTypeRemote, 0, submittedFrom))
}

func agentCreatedProperties(agentType string, toolsetCount int, submittedFrom string) map[string]any {
	props := approvedOnly(productanalytics.PropSubmittedFrom, submittedFrom, httputil.SubmitterSurfaces)
	props[productanalytics.PropToolsetCount] = toolsetCount
	props[productanalytics.PropAgentType] = agentType
	return props
}

func (r *ResourceReporter) AgentDeployed(ctx context.Context, deploymentID string) {
	r.reportFirstDeploy(ctx, productanalytics.EventAgentDeployed, deploymentID, r.agentDeployIsFirst)
}

func (r *ResourceReporter) WorkflowCreated(ctx context.Context) {
	r.report(ctx, productanalytics.EventWorkflowCreated, map[string]any{})
}

func (r *ResourceReporter) WorkflowDeployed(ctx context.Context, deploymentID string) {
	r.reportFirstDeploy(ctx, productanalytics.EventWorkflowDeployed, deploymentID, r.workflowDeployIsFirst)
}

func (r *ResourceReporter) EvalCreated(ctx context.Context, targetType string) {
	r.report(ctx, productanalytics.EventEvalCreated, approvedOnly(productanalytics.PropTargetType, targetType, productanalytics.EvalTargetTypes))
}

func (r *ResourceReporter) EvalRun(ctx context.Context, outcome, errorCategory, targetType string, shape *EvalRunShape, decision *EvalDecisionFacts) {
	r.report(ctx, productanalytics.EventEvalRun, evalRunProperties(outcome, errorCategory, targetType, shape, decision))
}

func evalRunProperties(outcome, errorCategory, targetType string, shape *EvalRunShape, decision *EvalDecisionFacts) map[string]any {
	props := approvedOnly(productanalytics.PropOutcome, outcome, productanalytics.EvalRunOutcomes)
	if targetType != "" {
		maps.Copy(props, approvedOnly(productanalytics.PropTargetType, targetType, productanalytics.EvalTargetTypes))
	}
	if errorCategory != "" {
		maps.Copy(props, approvedOnly(productanalytics.PropErrorCategory, errorCategory, taxonomy.ErrorCategories))
	}
	maps.Copy(props, evalShapeProperties(targetType, shape))
	maps.Copy(props, evalDecisionProperties(decision))
	return props
}

func evalShapeProperties(targetType string, shape *EvalRunShape) map[string]any {
	if shape == nil {
		return map[string]any{}
	}
	trials := taxonomy.EvalTrialsSingle
	if shape.MultiTrial {
		trials = taxonomy.EvalTrialsMultiple
	}
	props := map[string]any{
		productanalytics.PropEvaluatorTypes:   approvedSet(shape.EvaluatorTypes, productanalytics.EvalRunEvaluatorTypes),
		productanalytics.PropTrialsPerExample: trials,
	}
	if targetType == taxonomy.EvalTargetTypeWorkflow && !shape.TrialFailureKindsUnknown {
		props[productanalytics.PropWorkflowTrialFailureKinds] = approvedSet(shape.TrialFailureKinds, productanalytics.EvalWorkflowTrialFailureKinds)
	}
	return props
}

func approvedSet(values, vocabulary []string) []string {
	kept := make([]string, 0, len(values))
	for _, v := range values {
		if slices.Contains(vocabulary, v) && !slices.Contains(kept, v) {
			kept = append(kept, v)
		}
	}
	slices.Sort(kept)
	return kept
}

func evalDecisionProperties(decision *EvalDecisionFacts) map[string]any {
	if decision == nil {
		return map[string]any{}
	}
	props := map[string]any{productanalytics.PropDecisionEvaluatorCount: decision.Evaluators}
	if decision.Evaluators == 0 {
		return props
	}
	props[productanalytics.PropDecisionCasesDecided] = decision.Decided
	props[productanalytics.PropDecisionCasesEscalated] = decision.Escalated
	props[productanalytics.PropDecisionCasesErrored] = decision.Errored
	for _, reason := range taxonomy.EscalationReasons {
		props[productanalytics.DecisionEscalationReasonFields[reason]] = decision.EscalatedBy[reason]
	}
	return props
}

func (r *ResourceReporter) EvaluatorCreated(ctx context.Context, evaluatorType, questionType, escalation, submittedFrom string) {
	r.report(ctx, productanalytics.EventEvaluatorCreated, evaluatorCreatedProperties(evaluatorType, questionType, escalation, submittedFrom))
}

func evaluatorCreatedProperties(evaluatorType, questionType, escalation, submittedFrom string) map[string]any {
	props := approvedOnly(productanalytics.PropEvaluatorType, evaluatorType, productanalytics.EvaluatorTypes)
	if evaluatorType == taxonomy.EvaluatorTypeDecision {
		maps.Copy(props, approvedOnly(productanalytics.PropQuestionType, questionType, productanalytics.DecisionQuestionTypes))
		maps.Copy(props, approvedOnly(productanalytics.PropEscalation, escalation, productanalytics.EscalationModes))
	}
	maps.Copy(props, approvedOnly(productanalytics.PropSubmittedFrom, submittedFrom, httputil.SubmitterSurfaces))
	return props
}

func (r *ResourceReporter) agentDeployIsFirst(ctx context.Context, deploymentID string) (bool, error) {
	return r.deploys.IsFirstAgentDeploy(ctx, deploymentID)
}

func (r *ResourceReporter) workflowDeployIsFirst(ctx context.Context, deploymentID string) (bool, error) {
	return r.deploys.IsFirstWorkflowDeploy(ctx, deploymentID)
}

func (r *ResourceReporter) AgentDeleted(ctx context.Context) {
	r.report(ctx, productanalytics.EventAgentDeleted, agentDeletedProperties(productanalytics.AgentTypeUserCreated))
}

func (r *ResourceReporter) RemoteAgentDeleted(ctx context.Context) {
	r.report(ctx, productanalytics.EventAgentDeleted, agentDeletedProperties(productanalytics.AgentTypeRemote))
}

func agentDeletedProperties(agentType string) map[string]any {
	return map[string]any{productanalytics.PropAgentType: agentType}
}

func (r *ResourceReporter) ConnectorCreated(ctx context.Context, connectorType string) {
	r.report(ctx, productanalytics.EventConnectorCreated,
		approvedOnly(productanalytics.PropCategory, connectorType, taxonomy.ConnectorCategories))
}

func (r *ResourceReporter) ConnectorDeleted(ctx context.Context) {
	r.report(ctx, productanalytics.EventConnectorDeleted, map[string]any{})
}

func (r *ResourceReporter) EntrypointCreated(ctx context.Context, entrypointType string) {
	r.report(ctx, productanalytics.EventGatewayCreated,
		approvedOnly(productanalytics.PropGatewayType, entrypointType, taxonomy.GatewayTypes))
}

func (r *ResourceReporter) EntrypointDeleted(ctx context.Context) {
	r.report(ctx, productanalytics.EventGatewayDeleted, map[string]any{})
}

func (r *ResourceReporter) ToolsetCreated(ctx context.Context) {
	r.report(ctx, productanalytics.EventToolsetCreated, map[string]any{})
}

func (r *ResourceReporter) ModelConfigured(ctx context.Context, provider, modelName, apiBase, modelKind, submittedFrom string) {
	r.report(ctx, productanalytics.EventModelConfigured, modelConfiguredProperties(provider, modelName, apiBase, modelKind, submittedFrom))
}

func modelConfiguredProperties(provider, modelName, apiBase, modelKind, submittedFrom string) map[string]any {
	runProvider := providerTheRunReports(provider, modelName, modelKind)
	props := approvedOnly(productanalytics.PropProvider, runProvider, taxonomy.ProviderLabels)
	props[productanalytics.PropModel] = modelfamily.Of(modelName)
	props[productanalytics.PropModelType] = taxonomy.ModelServedFrom(runProvider, apiBase)
	maps.Copy(props, approvedOnly(productanalytics.PropModelKind, modelKind, productanalytics.ModelKinds))
	if slices.Contains(httputil.WriterSurfaces, submittedFrom) {
		props[productanalytics.PropSubmittedFrom] = submittedFrom
	}
	return props
}

func providerTheRunReports(provider, modelName, modelKind string) string {
	if modelKind == taxonomy.ModelKindDecision || modelName == "" {
		return provider
	}
	runProvider, _ := samapp.ParseModelProvider(ResolveLiteLLMModelName(provider, modelKind, modelName))
	return llm.ProviderLabel(runProvider)
}

func approvedOnly(key, value string, vocabulary []string) map[string]any {
	if !slices.Contains(vocabulary, value) {
		return map[string]any{}
	}
	return map[string]any{key: value}
}

func (r *ResourceReporter) AwaitReports(ctx context.Context) {
	if r == nil {
		return
	}
	held := 0
	defer func() {
		for range held {
			<-r.slots
		}
	}()
	for range cap(r.slots) {
		select {
		case r.slots <- struct{}{}:
			held++
		case <-ctx.Done():
			return
		}
	}
}

const resourceReportConcurrency = 4

func (r *ResourceReporter) report(ctx context.Context, name productanalytics.EventName, props map[string]any) {
	r.detached(ctx, func(ctx context.Context) { r.deliver(ctx, name, props) })
}

func (r *ResourceReporter) detached(ctx context.Context, work func(context.Context)) {
	if r == nil {
		return
	}
	select {
	case r.slots <- struct{}{}:
	default:
		r.logger.Debug("product analytics resource report dropped, all slots busy")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resourceReportTimeout)
	go func() {
		defer func() { <-r.slots }()
		defer cancel()
		defer func() {
			if rec := recover(); rec != nil {
				r.logger.Warn("product analytics resource report recovered from panic", "panic", productanalytics.SanitizedPanic(rec))
			}
		}()
		work(ctx)
	}()
}

func (r *ResourceReporter) reportFirstDeploy(ctx context.Context, name productanalytics.EventName, deploymentID string, isFirst func(context.Context, string) (bool, error)) {
	if r == nil || deploymentID == "" {
		return
	}
	if r.deploys == nil {
		r.logger.Debug("product analytics first-deploy report skipped, this process has no deployment history to read", "event", name)
		return
	}
	r.detached(ctx, func(ctx context.Context) {
		first, err := isFirst(ctx, deploymentID)
		if err != nil {
			r.logger.Debug("product analytics deploy history read failed", "event", name, "error", productanalytics.SanitizedError(err))
			return
		}
		if !first {
			return
		}
		r.deliver(ctx, name, map[string]any{})
	})
}

func (r *ResourceReporter) deliver(ctx context.Context, name productanalytics.EventName, props map[string]any) {
	r.analytics.Enqueue(ctx, productanalytics.SurfaceServer, []productanalytics.RawEvent{{
		OccurredAt: time.Now().UTC().Format(productanalytics.TimestampLayout),
		Name:       name,
		Properties: props,
	}})
}
