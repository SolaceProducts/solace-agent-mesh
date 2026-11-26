// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !product_analytics

package platform

import (
	"context"
	"testing"
)

func TestResourceReporter_NilReceiverToleratesEveryMethod(t *testing.T) {
	if got := (&PlatformCore{}).newResourceReporter(); got != nil {
		t.Fatalf("newResourceReporter() = %v, want nil — the premise of this test is that handlers hold nil here", got)
	}

	var r *ResourceReporter
	ctx := context.Background()

	for name, call := range map[string]func(){
		"AgentCreated":       func() { r.AgentCreated(ctx, 3, "ui") },
		"RemoteAgentCreated": func() { r.RemoteAgentCreated(ctx, "ui") },
		"AgentDeployed":      func() { r.AgentDeployed(ctx, "deployment-1") },
		"AgentDeleted":       func() { r.AgentDeleted(ctx) },
		"RemoteAgentDeleted": func() { r.RemoteAgentDeleted(ctx) },
		"WorkflowCreated":    func() { r.WorkflowCreated(ctx) },
		"WorkflowDeployed":   func() { r.WorkflowDeployed(ctx, "deployment-1") },
		"EvalCreated":        func() { r.EvalCreated(ctx, "workflow") },
		"EvalRun":            func() { r.EvalRun(ctx, "success", "", "workflow", &EvalRunShape{}, &EvalDecisionFacts{Evaluators: 1}) },
		"EvaluatorCreated":   func() { r.EvaluatorCreated(ctx, "decision", "choice", "on_error", "ui") },
		"ConnectorCreated":   func() { r.ConnectorCreated(ctx, "http") },
		"ConnectorDeleted":   func() { r.ConnectorDeleted(ctx) },
		"EntrypointCreated":  func() { r.EntrypointCreated(ctx, "httpsse") },
		"EntrypointDeleted":  func() { r.EntrypointDeleted(ctx) },
		"ToolsetCreated":     func() { r.ToolsetCreated(ctx) },
		"ModelConfigured":    func() { r.ModelConfigured(ctx, "openai", "gpt-4", "", "chat", "ui") },
		"AwaitReports":       func() { r.AwaitReports(ctx) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("panicked on a nil receiver: %v", p)
				}
			}()
			call()
		})
	}
}
