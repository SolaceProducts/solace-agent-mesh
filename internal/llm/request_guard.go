// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"encoding/base64"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

const missingToolResultContent = `{"status":"error","message":"No result was recorded for this tool call. Do NOT retry it; continue with the current request."}`

const oversizedImageNote = "[An image was left out because it is larger than the model accepts.]"

func pairToolResults(msgs []ChatMessage) ([]ChatMessage, int, int) {
	out := make([]ChatMessage, 0, len(msgs))
	dropped, answered := 0, 0
	for i := 0; i < len(msgs); {
		m := msgs[i]
		i++
		if m.Role == "tool" {
			dropped++
			continue
		}
		out = append(out, m)
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		declared := make(map[string]bool, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			declared[tc.ID] = false
		}
		for ; i < len(msgs) && msgs[i].Role == "tool"; i++ {
			done, ours := declared[msgs[i].ToolCallID]
			if !ours || done {
				dropped++
				continue
			}
			declared[msgs[i].ToolCallID] = true
			out = append(out, msgs[i])
		}
		for _, tc := range m.ToolCalls {
			if declared[tc.ID] {
				continue
			}
			declared[tc.ID] = true
			out = append(out, ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    missingToolResultContent,
			})
			answered++
		}
	}
	if dropped == 0 && answered == 0 {
		return msgs, 0, 0
	}
	return out, dropped, answered
}

func limitsInlineImages(provider, model string) bool {
	p := schemas.ModelProvider(provider)
	return p == schemas.Anthropic || p == schemas.Bedrock || IsClaudeModel(model)
}

func dropOversizedImages(msgs []ChatMessage) ([]ChatMessage, int) {
	var out []ChatMessage
	dropped := 0
	for i, m := range msgs {
		var blocks []ContentBlock
		for j, b := range m.ContentBlocks {
			if b.Type != "image_url" || inlineImageBytes(b.ImageURL) <= visionimage.MaxImageBytes {
				continue
			}
			if blocks == nil {
				blocks = append([]ContentBlock(nil), m.ContentBlocks...)
			}
			blocks[j] = ContentBlock{Type: "text", Text: oversizedImageNote}
			dropped++
		}
		if blocks == nil {
			continue
		}
		if out == nil {
			out = append([]ChatMessage(nil), msgs...)
		}
		out[i].ContentBlocks = blocks
	}
	if out == nil {
		return msgs, 0
	}
	return out, dropped
}

func inlineImageBytes(url string) int {
	if !strings.HasPrefix(url, "data:") {
		return 0
	}
	_, payload, ok := strings.Cut(url, ";base64,")
	if !ok {
		return 0
	}
	return base64.StdEncoding.DecodedLen(len(payload))
}
