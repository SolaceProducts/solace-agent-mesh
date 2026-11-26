// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

var fabricatedTurnStops = []string{"\n\nHuman:", currentTimePrefix}

func (a *Agent) guardAgainstFabricatedTurn(ctx context.Context, state *TaskExecutionState, req *llm.ChatRequest, history []llm.ChatMessage) bool {
	if !llm.IsClaudeModel(a.callReportModel(ctx, state)) || !endsWithHostNotifyResult(history) {
		return false
	}
	req.StopSequences = slices.Clone(fabricatedTurnStops)
	return true
}

func endsWithHostNotifyResult(history []llm.ChatMessage) bool {
	if len(history) == 0 {
		return false
	}
	last := history[len(history)-1]
	return last.Role == "tool" && last.ToolName == "_notify_artifact_save" &&
		strings.HasPrefix(last.ToolCallID, hostNotifyCallIDPrefix)
}
