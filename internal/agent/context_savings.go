// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func (a *Agent) SetContextSavingsCounting(on bool) {
	a.countsContextSavings = on
}

func (a *Agent) CountsContextSavings() bool {
	return a.countsContextSavings
}

func (a *Agent) agentTurnSpend(ctx context.Context, state *TaskExecutionState, prompt []llm.ChatMessage) *llm.SpendAttribution {
	attribution := a.taskSpendAttribution(ctx, state, spend.SurfaceAgent)
	if attribution != nil && a.countsContextSavings {
		window, known := a.byModelMaxInput(ctx, state, a.callReportModel(ctx, state))
		attribution.AvoidedInputBytes = new(withheldWithinContextWindow(withheldBytesOf(prompt), promptTextBytes(prompt), window, known))
	}
	return attribution
}

func withheldBytesOf(messages []llm.ChatMessage) int64 {
	var total int64
	for i := range messages {
		total += messages[i].WithheldBytes
	}
	return total
}

func promptTextBytes(messages []llm.ChatMessage) int64 {
	var total int64
	for i := range messages {
		total += int64(len(messages[i].Content))
		for _, tc := range messages[i].ToolCalls {
			total += int64(len(tc.Name) + len(tc.Arguments))
		}
	}
	return total
}

func withheldWithinContextWindow(withheld, promptBytes int64, maxInputTokens int, known bool) int64 {
	if !known || maxInputTokens <= 0 {
		return withheld
	}
	room := int64(maxInputTokens)*spend.BytesPerEstimatedToken - promptBytes
	return min(withheld, max(room, 0))
}

func (a *Agent) stampUserTurnWithheld(messages []llm.ChatMessage, historyOffset int, withheld int64) {
	if !a.countsContextSavings || withheld <= 0 || historyOffset >= len(messages) {
		return
	}
	messages[historyOffset].WithheldBytes = withheld
}

func withheldTextBytes(do tool.DataObject, plan dataObjectPlan, info *savedArtifactInfo) int64 {
	if info == nil || !plan.save || plan.inline || !isTextDataObject(do) {
		return 0
	}
	return max(info.SizeBytes, 0)
}

func routedWithheldBytes(result map[string]any, savedArtifacts []savedArtifactInfo) int64 {
	var text int64
	for _, saved := range savedArtifacts {
		text += saved.WithheldBytes
	}
	if text == 0 {
		return 0
	}
	references, err := json.Marshal(result[artifactsCreatedKey])
	if err != nil {
		return 0
	}
	return tool.WithheldNet(text, int64(len(references)))
}

func spilledResultWithheldBytes(encoded []byte, view string) int64 {
	return tool.WithheldNet(int64(len(encoded)), int64(len(view)))
}

func isTextDataObject(do tool.DataObject) bool {
	return isTextMIME(do.MIMEType) && (do.ArtifactRef != nil || utf8.Valid(do.Content))
}

func uploadWithheldBytes(mimeType string, sizeBytes int64, summary string) int64 {
	if !isInlineableTextMIME(mimeType) || sizeBytes <= 0 {
		return 0
	}
	return tool.WithheldNet(sizeBytes, int64(len(summary)))
}
