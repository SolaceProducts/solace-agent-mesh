// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !product_analytics

package platform

import (
	"context"
)

func (p *PlatformCore) newResourceReporter() *ResourceReporter { return nil }

func (p *PlatformCore) registerCLIIngest() {
}

func (p *PlatformCore) appendInventoryReporter(services []BackgroundService) []BackgroundService {
	return services
}

type ResourceReporter struct{}

func (r *ResourceReporter) AgentCreated(context.Context, int, string) {
}

func (r *ResourceReporter) RemoteAgentCreated(context.Context, string) {
}

func (r *ResourceReporter) AgentDeployed(context.Context, string) {
}

func (r *ResourceReporter) WorkflowCreated(context.Context) {
}

func (r *ResourceReporter) WorkflowDeployed(context.Context, string) {
}

func (r *ResourceReporter) EvalCreated(context.Context, string) {
}

func (r *ResourceReporter) EvalRun(context.Context, string, string, string, *EvalRunShape, *EvalDecisionFacts) {
}

func (r *ResourceReporter) EvaluatorCreated(context.Context, string, string, string, string) {
}

func (r *ResourceReporter) AgentDeleted(context.Context) {
}

func (r *ResourceReporter) RemoteAgentDeleted(context.Context) {
}

func (r *ResourceReporter) ConnectorCreated(context.Context, string) {
}

func (r *ResourceReporter) ConnectorDeleted(context.Context) {
}

func (r *ResourceReporter) EntrypointCreated(context.Context, string) {
}

func (r *ResourceReporter) EntrypointDeleted(context.Context) {
}

func (r *ResourceReporter) ToolsetCreated(context.Context) {
}

func (r *ResourceReporter) ModelConfigured(context.Context, string, string, string, string, string) {
}

func (r *ResourceReporter) AwaitReports(context.Context) {
}
