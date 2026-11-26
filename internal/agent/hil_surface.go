// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"cmp"
	"encoding/json"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func agentDisplayName(cfg *AgentConfig) string {
	return cmp.Or(cfg.DisplayName, cfg.Name)
}

func generateApprovalSurface(requestID, toolName, agentName string, toolArgs map[string]any, schema *tool.JSONSchema, message string, showArgs bool, timeout time.Duration) (json.RawMessage, error) {
	return hilsurface.Generate(hilsurface.Request{
		RequestID:     requestID,
		ToolName:      toolName,
		RequesterKind: hilsurface.RequesterAgent,
		RequesterName: agentName,
		ToolArgs:      toolArgs,
		Schema:        schema,
		Message:       message,
		ShowArgs:      showArgs,
		Timeout:       timeout,
	})
}

func redactToolArgs(args map[string]any) map[string]any {
	return hilsurface.RedactToolArgs(args)
}

func a2uiLiteral(s string) map[string]any {
	return hilsurface.A2UILiteral(s)
}

func redactValue(value any) any {
	return hilsurface.RedactValue(value)
}
