// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"slices"
	"testing"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func blockTexts(m schemas.ChatMessage) []string {
	var out []string
	if m.Content == nil {
		return out
	}
	if m.Content.ContentStr != nil {
		return []string{*m.Content.ContentStr}
	}
	for _, b := range m.Content.ContentBlocks {
		text := ""
		if b.Text != nil {
			text = *b.Text
		}
		if b.CacheControl != nil {
			text += " <BP>"
		}
		out = append(out, text)
	}
	return out
}

func roles(msgs []schemas.ChatMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Role)
	}
	return out
}

func TestConvertMessagesMergesAdjacentSameRoleTurns(t *testing.T) {
	got := convertMessages([]ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "question"},
		{Role: "user", Content: "<system-reminder>context</system-reminder>"},
	})

	if want := []string{"system", "user"}; !slices.Equal(roles(got), want) {
		t.Fatalf("roles = %v, want %v", roles(got), want)
	}
	texts := blockTexts(got[1])
	if want := []string{"question", "<system-reminder>context</system-reminder>"}; !slices.Equal(texts, want) {
		t.Errorf("merged blocks = %v, want %v (order must be preserved)", texts, want)
	}
}

func TestConvertMessagesMergesRunsLongerThanTwo(t *testing.T) {
	got := convertMessages([]ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "question"},
		{Role: "user", Content: "context"},
		{Role: "user", Content: "last call"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2 (system + one merged user turn): %v", len(got), roles(got))
	}
	if want := []string{"question", "context", "last call"}; !slices.Equal(blockTexts(got[1]), want) {
		t.Errorf("merged blocks = %v, want %v", blockTexts(got[1]), want)
	}
}

func TestMergePreservesBreakpointPosition(t *testing.T) {
	got := convertMessages([]ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "cached question", CacheControl: &CacheControl{Type: "ephemeral"}},
		{Role: "user", Content: "volatile context", NoCacheBreakpoint: true},
	})
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2: %v", len(got), roles(got))
	}

	want := []string{"cached question <BP>", "volatile context"}
	if got := blockTexts(got[1]); !slices.Equal(got, want) {
		t.Errorf("blocks = %v, want %v — the breakpoint must stay on the cached content, "+
			"not move onto the volatile block, or the cached prefix carries per-request "+
			"content (the defect this ordering exists to avoid)", got, want)
	}
}

func TestConvertMessagesDoesNotMergeToolOrSystemTurns(t *testing.T) {
	cases := map[string][]ChatMessage{
		"tool results": {
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "t", Arguments: "{}"}}},
			{Role: "tool", ToolCallID: "c1", Content: "result 1", ToolName: "t"},
			{Role: "tool", ToolCallID: "c2", Content: "result 2", ToolName: "t"},
		},
		"assistant turns carrying tool calls": {
			{Role: "assistant", Content: "thinking", ToolCalls: []ToolCall{{ID: "c1", Name: "t", Arguments: "{}"}}},
			{Role: "assistant", Content: "more", ToolCalls: []ToolCall{{ID: "c2", Name: "t", Arguments: "{}"}}},
		},
		"system messages": {
			{Role: "system", Content: "first"},
			{Role: "system", Content: "second"},
		},
	}
	for name, msgs := range cases {
		t.Run(name, func(t *testing.T) {
			got := convertMessages(msgs)
			if len(got) != len(msgs) {
				t.Fatalf("%d messages collapsed to %d; these must not merge: %v",
					len(msgs), len(got), roles(got))
			}
			for i, m := range msgs {
				if m.ToolCallID == "" {
					continue
				}
				tm := got[i].ChatToolMessage
				if tm == nil || tm.ToolCallID == nil || *tm.ToolCallID != m.ToolCallID {
					t.Errorf("message %d lost its tool_call_id %q", i, m.ToolCallID)
				}
			}
		})
	}
}

func TestConvertMessagesMergesPlainAssistantTurns(t *testing.T) {
	got := convertMessages([]ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "part one"},
		{Role: "assistant", Content: "part two"},
	})
	if want := []string{"user", "assistant"}; !slices.Equal(roles(got), want) {
		t.Fatalf("roles = %v, want %v", roles(got), want)
	}
	if want := []string{"part one", "part two"}; !slices.Equal(blockTexts(got[1]), want) {
		t.Errorf("merged blocks = %v, want %v", blockTexts(got[1]), want)
	}
}

func TestConvertMessagesLeavesAlternatingConversationUnchanged(t *testing.T) {
	in := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
	}
	got := convertMessages(in)
	if len(got) != len(in) {
		t.Fatalf("got %d messages, want %d", len(got), len(in))
	}
	for i, m := range got {
		if m.Content == nil || m.Content.ContentStr == nil {
			t.Errorf("message %d was converted to blocks; an untouched turn should keep its string content", i)
			continue
		}
		if *m.Content.ContentStr != in[i].Content {
			t.Errorf("message %d content = %q, want %q", i, *m.Content.ContentStr, in[i].Content)
		}
	}
}

func TestMergeKeepsNonTextBlocks(t *testing.T) {
	got := convertMessages([]ChatMessage{
		{Role: "user", Content: "look", ContentBlocks: []ContentBlock{
			{Type: "text", Text: "look"},
			{Type: "image_url", ImageURL: "data:image/png;base64,AAAA"},
		}},
		{Role: "user", Content: "context"},
	})
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1: %v", len(got), roles(got))
	}
	blocks := got[0].Content.ContentBlocks
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3 (text, image, merged text)", len(blocks))
	}
	if blocks[1].Type != "image_url" {
		t.Errorf("block 1 type = %q, want image_url — the image was dropped or reordered", blocks[1].Type)
	}
	if want := []string{"look", "", "context"}; !slices.Equal(blockTexts(got[0]), want) {
		t.Errorf("blocks = %v, want %v", blockTexts(got[0]), want)
	}
}

func bedrockEffectiveRoles(msgs []schemas.ChatMessage) []string {
	var out []string
	prevWasToolRun := false
	for _, m := range msgs {
		switch m.Role {
		case schemas.ChatMessageRoleSystem:
			prevWasToolRun = false
		case schemas.ChatMessageRoleTool:
			if !prevWasToolRun {
				out = append(out, string(schemas.ChatMessageRoleUser))
			}
			prevWasToolRun = true
		default:
			out = append(out, string(m.Role))
			prevWasToolRun = false
		}
	}
	return out
}

func TestConvertedConversationAlternatesForBedrock(t *testing.T) {
	cases := []struct {
		name     string
		msgs     []ChatMessage
		knownGap string
	}{
		{
			name: "user turn then harness context",
			msgs: []ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "q"},
				{Role: "user", Content: "context"},
			},
		},
		{
			name: "compaction summary then answer",
			msgs: []ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "assistant", Content: "[Conversation Summary]"},
				{Role: "assistant", Content: "a"},
			},
		},
		{
			name: "structured-output retry then context",
			msgs: []ChatMessage{
				{Role: "user", Content: "q"},
				{Role: "assistant", Content: "bad json"},
				{Role: "user", Content: "invalid, retry"},
				{Role: "user", Content: "context"},
			},
		},
		{
			name: "tool result then harness turn",
			msgs: []ChatMessage{
				{Role: "user", Content: "q"},
				{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "t", Arguments: "{}"}}},
				{Role: "tool", ToolCallID: "c1", Content: "result", ToolName: "t"},
				{Role: "user", Content: "<system-reminder>last call</system-reminder>"},
			},
			knownGap: "sam-go#3736",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bedrockEffectiveRoles(convertMessages(c.msgs))
			var adjacent []string
			for i := 1; i < len(got); i++ {
				if got[i] == got[i-1] {
					adjacent = append(adjacent, got[i])
				}
			}
			switch {
			case c.knownGap != "" && len(adjacent) == 0:
				t.Errorf("this shape now alternates (%v) but is still recorded as a known "+
					"gap in %s — remove the knownGap marker", got, c.knownGap)
			case c.knownGap != "":
				t.Logf("known gap %s: roles %v repeat %v", c.knownGap, got, adjacent)
			case len(adjacent) > 0:
				t.Errorf("roles %v repeat %v — Converse rejects a conversation that does "+
					"not strictly alternate", got, adjacent)
			}
		})
	}
}
