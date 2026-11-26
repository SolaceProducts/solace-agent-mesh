// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		msg  session.Message
		want int
	}{
		{
			name: "short text",
			msg:  session.Message{Content: "hello"},
			want: 1,
		},
		{
			name: "longer text",
			msg:  session.Message{Content: "This is a longer message with more content that should estimate higher tokens."},
			want: 19,
		},
		{
			name: "empty message",
			msg:  session.Message{Content: ""},
			want: 1,
		},
		{
			name: "message with tool calls",
			msg: session.Message{
				Content: "I'll help with that.",
				ToolCalls: []session.ToolCall{
					{Name: "search", Arguments: `{"query": "test"}`},
				},
			},
			want: 5 + 4 + 1,
		},
		{
			name: "tool-call turn with replayed reasoning",
			msg: session.Message{
				Content:   "I'll help with that.",
				Reasoning: "The user asked for news, so I search it.",
				ToolCalls: []session.ToolCall{
					{Name: "search", Arguments: `{"query": "test"}`},
				},
			},
			want: 15 + 4 + 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTokens(tt.msg)
			if got != tt.want {
				t.Errorf("estimateTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFindCompactionCutoff_InsufficientTurns(t *testing.T) {
	messages := []session.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	_, err := findCompactionCutoff(messages, 0.5, 0)
	if err == nil {
		t.Fatal("expected error for insufficient turns, got nil")
	}
	if !errors.Is(err, ErrConversationTooShortToCompact) {
		t.Errorf("err = %v, want ErrConversationTooShortToCompact", err)
	}
}

func TestFindCompactionCutoff_NoUserTurns(t *testing.T) {
	messages := []session.Message{
		{Role: "assistant", Content: "System message"},
	}

	_, err := findCompactionCutoff(messages, 0.5, 0)
	if err == nil {
		t.Fatal("expected error for no user turns")
	}
	if !errors.Is(err, ErrConversationTooShortToCompact) {
		t.Errorf("err = %v, want ErrConversationTooShortToCompact", err)
	}
}

func TestPerformCompaction_SingleUserTurnReturnsSentinel(t *testing.T) {
	a := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	a.svc = svc
	if err := a.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sess := &session.Session{Messages: []session.Message{
		{Role: "user", Content: "a single, very large user turn that overflows the window"},
		{Role: "assistant", Content: "reply"},
	}}
	_, err := a.performCompaction(t.Context(), sess, CompactionConfig{Enabled: true, CompactionPercentage: 0.5}, nil)
	if !errors.Is(err, ErrConversationTooShortToCompact) {
		t.Fatalf("performCompaction err = %v, want ErrConversationTooShortToCompact", err)
	}
}

func TestFindCompactionCutoff_TwoTurns(t *testing.T) {
	messages := []session.Message{
		{Role: "user", Content: "First question about something important"},
		{Role: "assistant", Content: "First answer with detailed explanation"},
		{Role: "user", Content: "Second question"},
		{Role: "assistant", Content: "Second answer"},
	}

	cutoff, err := findCompactionCutoff(messages, 0.5, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cutoff != 2 {
		t.Errorf("cutoff = %d, want 2", cutoff)
	}
}

func TestFindCompactionCutoff_MultipleTurns(t *testing.T) {
	messages := []session.Message{
		{Role: "user", Content: "Turn 1 user message with some content here"},
		{Role: "assistant", Content: "Turn 1 assistant response with details"},
		{Role: "user", Content: "Turn 2 user message"},
		{Role: "assistant", Content: "Turn 2 response"},
		{Role: "user", Content: "Turn 3 user message"},
		{Role: "assistant", Content: "Turn 3 response"},
	}

	cutoff, err := findCompactionCutoff(messages, 0.25, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if messages[cutoff].Role != "user" {
		t.Errorf("cutoff at index %d is role %q, want 'user'", cutoff, messages[cutoff].Role)
	}
	if cutoff < 2 {
		t.Errorf("cutoff = %d, want at least 2", cutoff)
	}
}

func TestFindCompactionCutoff_WithPriorSummary(t *testing.T) {
	var messages []session.Message
	for i := range 8 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		messages = append(messages, session.Message{Role: role, Content: strings.Repeat("x", 1000)})
	}

	const prefixTokens = 2000
	cutoff, err := findCompactionCutoff(messages, 0.625, prefixTokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cutoff != 2 {
		t.Errorf("cutoff = %d, want 2 (prior summary's tokens must be in target calc)", cutoff)
	}
}

func TestFilterCompactedMessages(t *testing.T) {
	messages := []session.Message{
		{Role: "user", Content: "old message 1"},
		{Role: "assistant", Content: "old response 1"},
		{Role: "user", Content: "old message 2"},
		{Role: "assistant", Content: "old response 2"},
		{Role: "user", Content: "recent message"},
		{Role: "assistant", Content: "recent response"},
	}

	cm := &CompactionMetadata{
		Summary:        "Summary of old conversation",
		CoveredThrough: 4,
		Timestamp:      time.Now(),
	}

	filtered := filterCompactedMessages(messages, cm)

	if len(filtered) != 3 {
		t.Fatalf("filtered length = %d, want 3", len(filtered))
	}

	if filtered[0].Role != "assistant" {
		t.Errorf("filtered[0].Role = %q, want 'assistant'", filtered[0].Role)
	}
	if !contains(filtered[0].Content, "Summary of old conversation") {
		t.Errorf("filtered[0] should contain summary, got %q", filtered[0].Content)
	}

	if filtered[1].Content != "recent message" {
		t.Errorf("filtered[1].Content = %q, want 'recent message'", filtered[1].Content)
	}
	if filtered[2].Content != "recent response" {
		t.Errorf("filtered[2].Content = %q, want 'recent response'", filtered[2].Content)
	}
}

func TestFilterCompactedMessages_NilCompaction(t *testing.T) {
	messages := []session.Message{{Role: "user", Content: "hello"}}
	filtered := filterCompactedMessages(messages, nil)
	if len(filtered) != 1 {
		t.Fatalf("expected unmodified messages, got %d", len(filtered))
	}
}

func TestFilterCompactedMessages_InvalidCoveredThrough(t *testing.T) {
	messages := []session.Message{{Role: "user", Content: "hello"}}

	cm := &CompactionMetadata{Summary: "test", CoveredThrough: 10}
	filtered := filterCompactedMessages(messages, cm)
	if len(filtered) != 1 {
		t.Fatalf("expected unmodified messages for invalid cutoff, got %d", len(filtered))
	}
}

func TestGetSetCompactionMetadata(t *testing.T) {
	sess := &session.Session{
		Metadata: make(map[string]any),
	}

	if cm := getCompactionMetadata(sess); cm != nil {
		t.Fatal("expected nil compaction metadata")
	}

	cm := &CompactionMetadata{
		Summary:        "test summary",
		CoveredThrough: 5,
		Timestamp:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	setCompactionMetadata(sess, cm)

	got := getCompactionMetadata(sess)
	if got == nil {
		t.Fatal("expected non-nil compaction metadata")
	} else if got.Summary != "test summary" {
		t.Errorf("Summary = %q, want 'test summary'", got.Summary)
	}
	if got.CoveredThrough != 5 {
		t.Errorf("CoveredThrough = %d, want 5", got.CoveredThrough)
	}
}

func TestGetCompactionMetadata_FromMapAny(t *testing.T) {
	sess := &session.Session{
		Metadata: map[string]any{
			"_compaction": map[string]any{
				"summary":         "round-trip summary",
				"covered_through": float64(3),
				"timestamp":       "2024-01-01T00:00:00Z",
			},
		},
	}

	got := getCompactionMetadata(sess)
	if got == nil {
		t.Fatal("expected non-nil compaction metadata from map[string]any")
	}
	if got.Summary != "round-trip summary" {
		t.Errorf("Summary = %q, want 'round-trip summary'", got.Summary)
	}
	if got.CoveredThrough != 3 {
		t.Errorf("CoveredThrough = %d, want 3", got.CoveredThrough)
	}
}

func TestBuildSummarizationPrompt(t *testing.T) {
	messages := []session.Message{
		{Role: "user", Content: "What is Go?"},
		{Role: "assistant", Content: "Go is a programming language."},
		{Role: "user", Content: "Tell me more."},
		{Role: "assistant", Content: "It was created at Google."},
	}

	prompt := buildSummarizationPrompt(messages, 2, nil)

	if !contains(prompt, "user: What is Go?") {
		t.Error("prompt should contain first user message")
	}
	if !contains(prompt, "model: Go is a programming language.") {
		t.Error("prompt should contain first assistant message (as 'model')")
	}
	if contains(prompt, "Tell me more") {
		t.Error("prompt should not contain messages after cutoff")
	}
	if !contains(prompt, "rolling summary") {
		t.Error("prompt should contain summarization instructions")
	}
}

func TestBuildSummarizationPrompt_Progressive(t *testing.T) {
	messages := []session.Message{
		{Role: "user", Content: "New question."},
		{Role: "assistant", Content: "New answer."},
	}

	prev := &CompactionMetadata{
		Summary: "Previous summary of old conversation.",
	}

	prompt := buildSummarizationPrompt(messages, 1, prev)

	if contains(prompt, "model: Previous summary of old conversation.") {
		t.Error("prior summary should not be presented as a 'model:' anchor")
	}
	if !contains(prompt, "Previous summary (stale") {
		t.Error("prior summary should be labeled as a stale draft to be replaced")
	}
	if !contains(prompt, "Previous summary of old conversation.") {
		t.Error("progressive prompt should still include the previous summary text")
	}
	if !contains(prompt, "user: New question.") {
		t.Error("prompt should contain new messages")
	}
}

func TestBuildSummarizationPrompt_ReplacementFraming(t *testing.T) {
	prev := &CompactionMetadata{Summary: "OLD SUMMARY TEXT"}
	prompt := buildSummarizationPrompt([]session.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}, 1, prev)

	if contains(prompt, "model: OLD SUMMARY TEXT") {
		t.Error("prior summary should not be presented as a 'model:' prefix anchor")
	}
	if !contains(prompt, "Previous summary (stale") {
		t.Error("prior summary should be labeled as a stale draft to be replaced")
	}
	if !contains(prompt, "REPLACES any previous summary") {
		t.Error("prompt should frame output as a replacement, not an edit")
	}
}

func TestExtractLastUserText(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "First question"},
		{Role: "assistant", Content: "First answer"},
		{Role: "user", Content: "Second question"},
		{Role: "assistant", Content: "Second answer"},
	}

	text := extractLastUserText(messages)
	if text != "Second question" {
		t.Errorf("extractLastUserText = %q, want 'Second question'", text)
	}
}

func TestExtractLastUserText_NoUser(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "System"},
		{Role: "assistant", Content: "Response"},
	}

	text := extractLastUserText(messages)
	if text != "" {
		t.Errorf("extractLastUserText = %q, want empty", text)
	}
}

func TestFindCompactionCutoff_SingleUserTurnAgenticLoop(t *testing.T) {
	pad := func(n int) string { return strings.Repeat("x", n) }
	messages := []session.Message{
		{Role: "user", Content: pad(40)},
		{Role: "assistant", Content: pad(40)},
		{Role: "assistant", Content: pad(40)},
		{Role: "assistant", Content: pad(120)},
		{Role: "assistant", Content: pad(40)},
	}

	cutoff, err := findCompactionCutoff(messages, 0.5, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cutoff != 3 {
		t.Errorf("cutoff = %d, want 3 (the middle boundary closest to the token target)", cutoff)
	}
}

func TestPerformCompaction_SingleUserTurnToolLoopCompacts(t *testing.T) {
	a := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	svc := newMockServices(newMockLLM(&llm.ChatResponse{Content: "condensed history"}), newMemSessionStore())
	a.svc = svc
	if err := a.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sess := &session.Session{Messages: []session.Message{
		{Role: "user", Content: "Research question needing many tool calls to answer properly"},
		{Role: "assistant", Content: "Searching now", ToolCalls: []session.ToolCall{{ID: "t1", Name: "web_search", Arguments: `{"q":"first"}`}}},
		{Role: "tool", ToolCallResult: session.ToolCallResult{ToolCallID: "t1"}, Content: "first search results with a fair amount of content in them"},
		{Role: "assistant", Content: "Fetching page", ToolCalls: []session.ToolCall{{ID: "t2", Name: "web_fetch", Arguments: `{"url":"a"}`}}},
		{Role: "tool", ToolCallResult: session.ToolCallResult{ToolCallID: "t2"}, Content: "fetched page content, quite long, representing a big tool result"},
		{Role: "assistant", Content: "Final answer based on research"},
	}}

	res, err := a.performCompaction(t.Context(), sess, CompactionConfig{Enabled: true, CompactionPercentage: 0.5}, nil)
	if err != nil {
		t.Fatalf("performCompaction: %v", err)
	}

	ct := res.Metadata.CoveredThrough
	if ct != 3 {
		t.Fatalf("CoveredThrough = %d, want 3", ct)
	}
	if got := sess.Messages[ct].Role; got != "assistant" {
		t.Errorf("cutoff message role = %q, want 'assistant' (round boundary)", got)
	}

	filtered := filterCompactedMessages(sess.Messages, res.Metadata)
	if len(filtered) != 1+len(sess.Messages)-ct {
		t.Fatalf("filtered length = %d, want %d", len(filtered), 1+len(sess.Messages)-ct)
	}
	if filtered[0].Role != "assistant" || !strings.Contains(filtered[0].Content, "condensed history") {
		t.Errorf("filtered[0] = {%s, %q}, want assistant summary", filtered[0].Role, filtered[0].Content)
	}
	seen := map[string]bool{}
	for _, m := range filtered[1:] {
		for _, tc := range m.ToolCalls {
			seen[tc.ID] = true
		}
		if m.Role == "tool" && !seen[m.ToolCallID] {
			t.Errorf("tool result %q kept without its tool call", m.ToolCallID)
		}
	}
}
