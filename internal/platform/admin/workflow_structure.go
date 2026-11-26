// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

func validateWorkflowStructure(name string, appConfig map[string]any) error {
	problems := workflow.StructureProblems(appConfig)
	if len(problems) == 0 {
		return nil
	}
	label := "workflow"
	if name != "" {
		label = fmt.Sprintf("workflow %q", name)
	}
	named := make([]string, len(problems))
	for i, p := range problems {
		named[i] = label + ": " + p
	}
	return &ValidationError{
		Message: "workflow cannot be deployed as written: " + strings.Join(named, "; "),
		Field:   workflowNodesField(appConfig),
		Details: map[string][]string{workflowNodesField(appConfig): named},
	}
}

func validateToolNodeNames(name string, appConfig map[string]any) error {
	named := unknownToolNodeMessages(name, appConfig)
	if len(named) == 0 {
		return nil
	}
	return &ValidationError{
		Message: "workflow cannot be deployed as written: " + strings.Join(named, "; "),
		Field:   workflowNodesField(appConfig),
		Details: map[string][]string{workflowNodesField(appConfig): named},
	}
}

func unknownToolNodeMessages(name string, appConfig map[string]any) []string {
	label := "workflow"
	if name != "" {
		label = fmt.Sprintf("workflow %q", name)
	}
	messages := workflow.FatalMessages(workflow.UnknownToolNodeProblems(appConfig))
	named := make([]string, len(messages))
	for i, m := range messages {
		named[i] = label + ": " + m
	}
	return named
}

const msgToolNodeCannotStopRun = "workflow tool node fails whenever it runs but cannot stop a run"

func logToolNodesThatCannotStopRun(logger *slog.Logger, appConfig map[string]any) {
	if logger == nil {
		logger = slog.Default()
	}
	for _, p := range workflow.UnknownToolNodeProblems(appConfig) {
		if p.Downgraded() {
			logger.Warn(msgToolNodeCannotStopRun, "nodeID", p.NodeID, "toolName", p.ToolName, "why", p.NotFatalBecause)
		}
	}
}

func workflowNodesField(appConfig map[string]any) string {
	if _, nested := appConfig["workflow"].(map[string]any); nested {
		return "appConfig.workflow.nodes"
	}
	return "appConfig.nodes"
}
