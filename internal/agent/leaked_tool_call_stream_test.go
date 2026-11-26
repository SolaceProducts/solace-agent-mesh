// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func streamWithTools(t *testing.T, response *llm.ChatResponse, info *streamCallInfo) (resp *llm.ChatResponse, published string) {
	t.Helper()
	h := newTemplateBlockHarness(t, response)
	info.MaxContinuations = -1

	resp, _, err := h.agent.streamLLMCall(t.Context(), h.sp, &llm.ChatRequest{Tools: leakTestTools}, info)
	if err != nil {
		t.Fatalf("streamLLMCall: %v", err)
	}
	return resp, publishedText(h.publishedParts(t))
}

func TestStreamLLMCallWithholdsAToolCallWrittenAsText(t *testing.T) {
	longArgument := strings.Repeat("x", 600)
	tests := []struct {
		name          string
		content       string
		wantPublished string
		wantTool      string
	}{
		{"name and json", "Looking.\n«grep_skill_resources {\"pattern\": \"Filters\"}", "Looking.\n", "grep_skill_resources"},
		{"embed syntax", "Looking.\n«grep_skill_resources:{\"pattern\":\"Filters\"}»", "Looking.\n", "grep_skill_resources"},
		{"closed with json", "Looking.\n«grep_skill_resources {\"pattern\": \"x\"}»", "Looking.\n", "grep_skill_resources"},
		{"closed with empty arguments", "«search {}»", "", "search"},
		{"closed with a guillemet in the arguments", "«search {\"q\": \"a»b\"}»", "", "search"},
		{"closed call then more words", "«search {\"q\": \"x\"}» and then done", "", "search"},
		{"long unclosed call then an embed", "«search {\"q\": \"" + longArgument + "\"}\n«status_update:x»", "", "search"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := &streamCallInfo{WithholdLeakedToolCall: true}

			resp, published := streamWithTools(t, &llm.ChatResponse{Content: tc.content}, info)

			if published != tc.wantPublished || resp.Content != tc.wantPublished {
				t.Errorf("published %q and Content %q, want only %q", published, resp.Content, tc.wantPublished)
			}
			if info.LeakedToolCall != tc.wantTool {
				t.Errorf("LeakedToolCall = %q, want %q", info.LeakedToolCall, tc.wantTool)
			}
		})
	}
}

func TestStreamLLMCallKeepsTheTextWhenTheCallerDoesNotOptIn(t *testing.T) {
	const content = "«grep_skill_resources {\"pattern\": \"Filters\"}"
	info := &streamCallInfo{}

	resp, published := streamWithTools(t, &llm.ChatResponse{Content: content}, info)

	if published != content || resp.Content != content {
		t.Errorf("published %q and Content %q, want the text untouched for a caller that discards nothing", published, resp.Content)
	}
	if info.LeakedToolCall != "" {
		t.Errorf("LeakedToolCall = %q, want none", info.LeakedToolCall)
	}
}

func TestStreamLLMCallPublishesProseThatQuotesAToolName(t *testing.T) {
	tests := []string{
		"Utilisez l'outil «grep_skill_resources» puis «search» pour chercher.",
		"Tapez «search engine» dans la barre.",
		"Tapez «grep_skill_resources maintenant» dans la console.",
		"Tapez «search engine",
		"Voir «sea",
	}
	for _, content := range tests {
		t.Run(content, func(t *testing.T) {
			info := &streamCallInfo{WithholdLeakedToolCall: true}

			resp, published := streamWithTools(t, &llm.ChatResponse{Content: content}, info)

			if published != content || resp.Content != content {
				t.Errorf("published %q and Content %q, want the prose untouched", published, resp.Content)
			}
			if info.LeakedToolCall != "" {
				t.Errorf("LeakedToolCall = %q, want none for prose", info.LeakedToolCall)
			}
		})
	}
}

func TestStreamLLMCallKeepsCallLookingTextBesideARealToolCall(t *testing.T) {
	const content = "Look «search {}"
	info := &streamCallInfo{WithholdLeakedToolCall: true}
	response := &llm.ChatResponse{Content: content, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "search", Arguments: "{}"}}}

	resp, published := streamWithTools(t, response, info)

	if published != content || resp.Content != content {
		t.Errorf("published %q and Content %q, want the text kept beside a real call", published, resp.Content)
	}
	if info.LeakedToolCall != "" || len(resp.ToolCalls) != 1 {
		t.Errorf("LeakedToolCall = %q with %d tool calls, want no leak and the real call kept", info.LeakedToolCall, len(resp.ToolCalls))
	}
}

func TestStreamLLMCallKeepsCallLookingTextAfterASavedArtifact(t *testing.T) {
	const saveBlock = BlockPrefixSaveArtifact + ` filename="report.md" description="A report"` + "\n# Report\n" + BlockDelimiterClose
	info := &streamCallInfo{WithholdLeakedToolCall: true, UserID: inlineTestUser, SessionID: inlineTestSession}

	resp, published := streamWithTools(t, &llm.ChatResponse{Content: saveBlock + "\nEt «search {}» après"}, info)

	if !strings.Contains(published, "Et «search {}» après") || !strings.Contains(resp.Content, "Et «search {}» après") {
		t.Errorf("published %q and Content %q, want the text after a saved artifact kept", published, resp.Content)
	}
	if info.LeakedToolCall != "" {
		t.Errorf("LeakedToolCall = %q, want none when an artifact was saved", info.LeakedToolCall)
	}
}

func TestStreamLLMCallKeepsCallLookingTextBesideASavedArtifact(t *testing.T) {
	const saveBlock = BlockPrefixSaveArtifact + ` filename="report.md" description="A report"` + "\n# Report\n" + BlockDelimiterClose
	info := &streamCallInfo{WithholdLeakedToolCall: true, UserID: inlineTestUser, SessionID: inlineTestSession}

	resp, published := streamWithTools(t, &llm.ChatResponse{Content: "Voir «search {}» ici\n" + saveBlock}, info)

	if !strings.Contains(published, "Voir «search {}»") || !strings.Contains(resp.Content, "Voir «search {}»") {
		t.Errorf("published %q and Content %q, want the text kept beside a saved artifact", published, resp.Content)
	}
	if info.LeakedToolCall != "" {
		t.Errorf("LeakedToolCall = %q, want none when an artifact was saved", info.LeakedToolCall)
	}
}
