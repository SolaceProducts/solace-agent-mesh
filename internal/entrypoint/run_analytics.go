// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"context"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

var builtinAgentDisplayNames = map[string]bool{
	platform.BuiltinOrchestratorName:    true,
	platform.BuiltinSentinelName:        true,
	platform.BuiltinActivityMonitorName: true,
}

const (
	runReportTimeout = 2 * time.Second

	runReportConcurrency = 4
)

func (g *GatewayCore) productAnalytics() *productanalytics.Service {
	if g.svc == nil {
		return nil
	}
	svc, _ := g.svc.UsageReporter().(*productanalytics.Service)
	return svc
}

type analyticsWiring struct {
	slots chan struct{}
}

func (w *analyticsWiring) register(g *GatewayCore) {
	if g.productAnalytics() == nil {
		return
	}
	w.slots = make(chan struct{}, runReportConcurrency)
	g.OnStatusUpdate(g.recordRunModel)
	g.OnTaskComplete(g.reportRunCompleted)
}

func (g *GatewayCore) recordRunModel(taskID string, event *a2a.TaskStatusUpdateEvent) {
	model := announcedModel(event.Metadata)
	if model == (usagefacts.Model{}) || g.tasks == nil {
		return
	}
	g.tasks.SetRunModel(taskID, model)
}

func announcedModel(source map[string]any) usagefacts.Model {
	family, _ := source[a2a.MetadataModelFamily].(string)
	provider, _ := source[a2a.MetadataModelProvider].(string)
	servedFrom, _ := source[a2a.MetadataModelType].(string)
	return usagefacts.Model{
		Family:     knownValue(family, modelfamily.Families),
		Provider:   knownValue(provider, taxonomy.ProviderLabels),
		ServedFrom: knownValue(servedFrom, taxonomy.ModelTypes),
	}
}

func (g *GatewayCore) reportRunCompleted(taskID string, task *a2a.Task, a2aErr *a2a.Error) {
	defer func() {
		if r := recover(); r != nil {
			g.logger.Warn("product analytics run report recovered from panic", "panic", productanalytics.SanitizedPanic(r))
		}
	}()

	svc := g.productAnalytics()
	if svc == nil || g.tasks == nil {
		return
	}

	info := g.tasks.ClaimRunReport(taskID)
	if info == nil {
		return
	}
	events := g.terminalTaskEvents(info, task, a2aErr)
	if len(events) == 0 {
		return
	}
	g.enqueueRunReport(svc, events)
}

func (g *GatewayCore) terminalTaskEvents(info *TaskInfo, task *a2a.Task, a2aErr *a2a.Error) []productanalytics.RawEvent {
	occurredAt := time.Now().UTC().Format(productanalytics.TimestampLayout)
	facts := readUsageFacts(task, a2aErr).orAnnounced(info)
	var events []productanalytics.RawEvent
	if reportableRun(info.TaskOrigin) {
		events = append(events, productanalytics.RawEvent{
			OccurredAt: occurredAt,
			Name:       productanalytics.EventAgentRunCompleted,
			Properties: runReportProperties(info, task, a2aErr, facts),
		})
	}
	if reportableUsageFacts(info.TaskOrigin) {
		events = append(events, usageFactEvents(info, occurredAt, facts)...)
	}
	if reportableEntrypointUse(info.TaskOrigin) {
		events = append(events, g.entrypointUsedEvent(task, a2aErr, occurredAt))
	}
	for i := range events {
		events[i].SessionID = info.AnalyticsSessionID
	}
	return events
}

type usageFacts struct {
	toolCategories      []string
	connectorCategories []string
	builtinCategories   []string
	peerDelegated       bool
	model               usagefacts.Model
	models              []usagefacts.Model
	toolModelFamilies   []string
}

func readUsageFacts(task *a2a.Task, a2aErr *a2a.Error) usageFacts {
	source := map[string]any(nil)
	if task != nil {
		source = task.Metadata
	}
	if source == nil && a2aErr != nil {
		source, _ = a2aErr.Data.(map[string]any)
	}
	delegated, _ := source[a2a.MetadataPeerDelegated].(bool)
	settled := announcedModel(source)
	return usageFacts{
		toolCategories:      knownOnly(enumList(source[a2a.MetadataToolCategories]), taxonomy.ToolCategories),
		connectorCategories: knownOnly(enumList(source[a2a.MetadataConnectorCategories]), taxonomy.ConnectorCategories),
		builtinCategories:   knownOnly(enumList(source[a2a.MetadataBuiltinCategories]), taxonomy.BuiltinToolCategories),
		peerDelegated:       delegated,
		model:               settled,
		models:              distinctModels(append(usagefacts.ReadModels(source[a2a.MetadataModelUsages]), settled)),
		toolModelFamilies:   knownOnly(enumList(source[a2a.MetadataToolModelFamilies]), modelfamily.Families),
	}
}

func distinctModels(models []usagefacts.Model) []usagefacts.Model {
	out := make([]usagefacts.Model, 0, len(models))
	for _, model := range models {
		if model == (usagefacts.Model{}) || slices.Contains(out, model) {
			continue
		}
		out = append(out, model)
	}
	slices.SortFunc(out, usagefacts.CompareModels)
	if len(out) > usagefacts.MaxModelElements {
		out = out[:usagefacts.MaxModelElements]
	}
	return out
}

func (f usageFacts) orAnnounced(info *TaskInfo) usageFacts {
	if f.model == (usagefacts.Model{}) {
		f.model = info.Model
	}
	if len(f.models) == 0 {
		f.models = distinctModels([]usagefacts.Model{info.Model})
	}
	return f
}

func knownValue(value string, vocabulary []string) string {
	if slices.Contains(vocabulary, value) {
		return value
	}
	return ""
}

var clientSurfaces = httputil.SubmitterSurfaces

func knownOnly(values, vocabulary []string) []string {
	allowed := make(map[string]struct{}, len(vocabulary))
	for _, v := range vocabulary {
		allowed[v] = struct{}{}
	}
	out := make([]string, 0, min(len(values), len(vocabulary)))
	for _, v := range values {
		if _, ok := allowed[v]; !ok {
			continue
		}
		if slices.Contains(out, v) {
			continue
		}
		out = append(out, v)
	}
	return out
}

const maxArrivingSetElements = usagefacts.MaxArrivingSetElements

func enumList(v any) []string {
	values, ok := taxonomy.ParseArrivingStringSet(v, maxArrivingSetElements)
	if !ok {
		return nil
	}
	return values
}

func usageFactEvents(info *TaskInfo, occurredAt string, facts usageFacts) []productanalytics.RawEvent {
	var events []productanalytics.RawEvent
	origin := runOrigin(info.TaskOrigin)
	emit := func(name string, props map[string]any) {
		props[productanalytics.PropRunOrigin] = origin
		if info.TraceID != "" {
			props[productanalytics.PropTraceID] = info.TraceID
		}
		events = append(events, productanalytics.RawEvent{
			OccurredAt: occurredAt,
			Name:       name,
			Properties: props,
		})
	}

	for _, category := range facts.toolCategories {
		if category == taxonomy.ToolCategoryConnector && len(facts.connectorCategories) > 0 {
			continue
		}
		if category == taxonomy.ToolCategoryBuiltin && len(facts.builtinCategories) > 0 {
			continue
		}
		emit(productanalytics.EventToolUsed, map[string]any{
			productanalytics.PropToolCategory: category,
		})
	}
	for _, connectorCategory := range facts.connectorCategories {
		emit(productanalytics.EventToolUsed, map[string]any{
			productanalytics.PropToolCategory:      taxonomy.ToolCategoryConnector,
			productanalytics.PropConnectorCategory: connectorCategory,
		})
	}
	for _, builtinCategory := range facts.builtinCategories {
		emit(productanalytics.EventToolUsed, map[string]any{
			productanalytics.PropToolCategory:    taxonomy.ToolCategoryBuiltin,
			productanalytics.PropBuiltinCategory: builtinCategory,
		})
	}
	for _, model := range facts.models {
		props := map[string]any{}
		if model.Family != "" {
			props[productanalytics.PropModel] = model.Family
		}
		if model.Provider != "" {
			props[productanalytics.PropProvider] = model.Provider
		}
		if model.ServedFrom != "" {
			props[productanalytics.PropModelType] = model.ServedFrom
		}
		emit(productanalytics.EventModelUsed, props)
	}
	if facts.peerDelegated {
		emit(productanalytics.EventAgentPeerDelegated, map[string]any{})
	}
	return events
}

func reportableEntrypointUse(origin TaskOrigin) bool {
	return origin.MachineIngress && !origin.SystemInitiated
}

func reportableRun(origin TaskOrigin) bool {
	if origin.SystemInitiated || origin.MachineIngress {
		return false
	}
	return origin.Trigger == a2a.TriggerInteractive && origin.AgentType != ""
}

func reportableUsageFacts(origin TaskOrigin) bool {
	return !origin.SystemInitiated && origin.AgentType != ""
}

func runOrigin(origin TaskOrigin) string {
	switch {
	case origin.MachineIngress:
		return productanalytics.RunOriginMachineIngress
	case origin.Trigger == a2a.TriggerScheduled:
		return productanalytics.RunOriginScheduled
	default:
		return productanalytics.RunOriginInteractive
	}
}

func runReportProperties(info *TaskInfo, task *a2a.Task, a2aErr *a2a.Error, facts usageFacts) map[string]any {
	outcome := runOutcome(task, a2aErr)
	props := map[string]any{
		productanalytics.PropOutcome:   outcome,
		productanalytics.PropAgentType: info.AgentType,
	}
	if info.TraceID != "" {
		props[productanalytics.PropTraceID] = info.TraceID
	}
	if surface := knownValue(info.Surface, clientSurfaces); surface != "" {
		props[productanalytics.PropSubmittedFrom] = surface
	}
	if facts.model.Provider != "" {
		props[productanalytics.PropProvider] = facts.model.Provider
	}
	if facts.model.Family != "" {
		props[productanalytics.PropModel] = facts.model.Family
	}
	if facts.model.ServedFrom != "" {
		props[productanalytics.PropModelType] = facts.model.ServedFrom
	}
	if len(facts.toolModelFamilies) > 0 {
		props[productanalytics.PropToolModels] = facts.toolModelFamilies
	}
	if outcome == taxonomy.OutcomeError {
		category, provider := llmFailureClass(a2aErr)
		if category == "" {
			category = knownValue(taskFailureCategory(task), taxonomy.ErrorCategories)
		}
		if category != "" {
			props[productanalytics.PropErrorCategory] = category
		}
		if _, recorded := props[productanalytics.PropProvider]; !recorded && provider != "" {
			props[productanalytics.PropProvider] = provider
		}
	}
	return props
}

func llmFailureClass(a2aErr *a2a.Error) (category, provider string) {
	category, provider = llmFailureData(a2aErr)
	return knownValue(category, taxonomy.ErrorCategories), knownValue(provider, taxonomy.ProviderLabels)
}

func (g *GatewayCore) enqueueRunReport(svc *productanalytics.Service, events []productanalytics.RawEvent) {
	select {
	case g.analytics.slots <- struct{}{}:
	default:
		g.logger.Debug("product analytics run report dropped, every reporter slot is busy")
		return
	}
	go func() {
		defer func() { <-g.analytics.slots }()
		defer func() {
			if r := recover(); r != nil {
				g.logger.Warn("product analytics run report recovered from panic", "panic", productanalytics.SanitizedPanic(r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), runReportTimeout)
		defer cancel()
		svc.Enqueue(ctx, productanalytics.SurfaceServer, events)
	}()
}

func runOutcome(task *a2a.Task, a2aErr *a2a.Error) string {
	if IsTaskTimeoutError(a2aErr) {
		return taxonomy.OutcomeTimeout
	}
	if a2aErr != nil || task == nil {
		return taxonomy.OutcomeError
	}
	switch task.Status.State {
	case a2a.TaskStateCompleted:
		return taxonomy.OutcomeSuccess
	case a2a.TaskStateCancelled:
		return taxonomy.OutcomeCancelled
	default:
		return taxonomy.OutcomeError
	}
}

func (g *GatewayCore) runAgentType(agentName string) string {
	if g.agents == nil {
		return ""
	}
	card := g.agents.Get(agentName)
	if card == nil {
		return ""
	}
	if a2a.IsInternalCard(*card) || a2a.IsEphemeralCard(*card) {
		return ""
	}
	if g.agents.HandlerTypeFromName(agentName) == store.TaskHandlerTypeWorkflow {
		return productanalytics.AgentTypeWorkflow
	}
	if builtinAgentDisplayNames[platform.DisplayName(card)] {
		return productanalytics.AgentTypeBuiltin
	}
	return productanalytics.AgentTypeUserCreated
}

func submitRejectionProperties(reason, surface, traceID string) map[string]any {
	props := map[string]any{}
	if r := knownValue(reason, taxonomy.RejectReasons); r != "" {
		props[productanalytics.PropRejectReason] = r
	}
	if s := knownValue(surface, clientSurfaces); s != "" {
		props[productanalytics.PropSubmittedFrom] = s
	}
	if traceID != "" {
		props[productanalytics.PropTraceID] = traceID
	}
	return props
}

type SubmitRejection struct {
	TraceID   string
	Surface   string
	Reason    string
	SessionID string
}

func (g *GatewayCore) ReportSubmitRejected(rejection SubmitRejection) {
	defer func() {
		if r := recover(); r != nil {
			g.logger.Warn("product analytics submit rejection recovered from panic", "panic", productanalytics.SanitizedPanic(r))
		}
	}()
	svc := g.productAnalytics()
	if svc == nil {
		return
	}
	g.enqueueRunReport(svc, []productanalytics.RawEvent{{
		OccurredAt: time.Now().UTC().Format(productanalytics.TimestampLayout),
		SessionID:  rejection.SessionID,
		Name:       productanalytics.EventTaskSubmitRejected,
		Properties: g.rejectionProperties(rejection),
	}})
}

func (g *GatewayCore) rejectionProperties(rejection SubmitRejection) map[string]any {
	props := submitRejectionProperties(rejection.Reason, rejection.Surface, rejection.TraceID)
	if surface := g.relayedSurface(); surface != "" {
		props[productanalytics.PropSubmittedFrom] = surface
	}
	if entrypointType := g.entrypointType(); entrypointType != "" {
		props[productanalytics.PropEntrypointType] = entrypointType
	}
	return props
}
