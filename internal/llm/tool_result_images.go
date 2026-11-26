// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"slices"

	"github.com/maximhq/bifrost/core/schemas"
)

const emptiedToolResultText = "Image attached below."

func keepsToolResultImages(provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.Anthropic, schemas.Bedrock:
		return true
	case schemas.Vertex, schemas.Azure:
		return IsClaudeModel(model)
	}
	return false
}

func chatWireRequest(bfReq *schemas.BifrostChatRequest) *schemas.BifrostChatRequest {
	if keepsToolResultImages(bfReq.Provider, bfReq.Model) || !slices.ContainsFunc(bfReq.Input, carriesToolImages) {
		return bfReq
	}
	out := *bfReq
	out.Input = withToolImagesAsUserMessages(bfReq.Input)
	return &out
}

func withToolImagesAsUserMessages(msgs []schemas.ChatMessage) []schemas.ChatMessage {
	toolNames := map[string]string{}
	for _, m := range msgs {
		if m.ChatAssistantMessage == nil {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != nil && tc.Function.Name != nil {
				toolNames[*tc.ID] = *tc.Function.Name
			}
		}
	}

	out := make([]schemas.ChatMessage, 0, len(msgs)+1)
	var moved []schemas.ChatContentBlock
	for i, m := range msgs {
		if carriesToolImages(m) {
			moved = append(moved, textBlock(toolImagesHeading(toolNames[toolCallID(m)])))
			var kept []schemas.ChatContentBlock
			for _, b := range m.Content.ContentBlocks {
				if b.Type == schemas.ChatContentBlockTypeImage {
					moved = append(moved, b)
				} else {
					kept = append(kept, b)
				}
			}
			if len(kept) == 0 {
				kept = []schemas.ChatContentBlock{textBlock(emptiedToolResultText)}
			}
			m.Content = &schemas.ChatMessageContent{ContentBlocks: kept}
		}
		out = append(out, m)
		if len(moved) > 0 && (i+1 == len(msgs) || msgs[i+1].Role != schemas.ChatMessageRoleTool) {
			out = append(out, schemas.ChatMessage{
				Role:    schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{ContentBlocks: moved},
			})
			moved = nil
		}
	}
	return out
}

func carriesToolImages(m schemas.ChatMessage) bool {
	return m.Role == schemas.ChatMessageRoleTool && m.Content != nil &&
		slices.ContainsFunc(m.Content.ContentBlocks, func(b schemas.ChatContentBlock) bool {
			return b.Type == schemas.ChatContentBlockTypeImage
		})
}

func toolCallID(m schemas.ChatMessage) string {
	if m.ChatToolMessage == nil || m.ToolCallID == nil {
		return ""
	}
	return *m.ToolCallID
}

func textBlock(text string) schemas.ChatContentBlock {
	return schemas.ChatContentBlock{Type: schemas.ChatContentBlockTypeText, Text: schemas.Ptr(text)}
}

func toolImagesHeading(toolName string) string {
	if toolName == "" {
		return "Images returned by the tool call above:"
	}
	return "Images returned by the " + toolName + " tool call above:"
}
